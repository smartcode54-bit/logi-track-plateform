package push

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"
)

// DefaultTokenURL is Google's OAuth2 token endpoint, used when the key file names none.
const DefaultTokenURL = "https://oauth2.googleapis.com/token"

// maxKeyFileBytes bounds the service-account file (a real one is about 2.3 KB).
const maxKeyFileBytes = 64 << 10

// Credentials is the messaging-only service account of FCM_SERVICE_ACCOUNT_JSON (main spec §16.1): the
// one Firebase credential that survives P8. Every field is unexported and String hides them, so a log
// line or an error that formats the value never prints the key.
type Credentials struct {
	projectID string
	cfg       *jwt.Config
}

// LoadCredentials reads and parses the key file at path. Errors name the variable and the problem,
// never the content.
func LoadCredentials(path string) (*Credentials, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("FCM_SERVICE_ACCOUNT_JSON: cannot open the service-account file")
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxKeyFileBytes+1))
	if err != nil {
		return nil, errors.New("FCM_SERVICE_ACCOUNT_JSON: cannot read the service-account file")
	}
	if len(b) > maxKeyFileBytes {
		return nil, errors.New("FCM_SERVICE_ACCOUNT_JSON: the file is too large to be a service-account key")
	}
	return ParseCredentials(b)
}

// ParseCredentials parses a Google service-account key file (type "service_account"): client_email,
// private_key (PEM, PKCS #8 or PKCS #1 RSA of at least 2048 bits), private_key_id, project_id and
// token_uri (https; Google's endpoint when absent). The same checks as the Firebase bridge's key file
// (internal/auth/firebase), under this variable's name.
func ParseCredentials(b []byte) (*Credentials, error) {
	var f struct {
		Type         string `json:"type"`
		ProjectID    string `json:"project_id"`
		PrivateKeyID string `json:"private_key_id"`
		PrivateKey   string `json:"private_key"`
		ClientEmail  string `json:"client_email"`
		TokenURI     string `json:"token_uri"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, errors.New("FCM_SERVICE_ACCOUNT_JSON: not a JSON service-account key file")
	}
	if f.Type != "service_account" {
		return nil, errors.New(`FCM_SERVICE_ACCOUNT_JSON: the file is not a service-account key (type must be "service_account")`)
	}
	if a, err := mail.ParseAddress(f.ClientEmail); err != nil || a.Address != f.ClientEmail {
		return nil, errors.New("FCM_SERVICE_ACCOUNT_JSON: client_email is missing or malformed")
	}
	if err := checkRSAKey([]byte(f.PrivateKey)); err != nil {
		return nil, errors.New("FCM_SERVICE_ACCOUNT_JSON: private_key: " + err.Error())
	}
	tokenURL := f.TokenURI
	if tokenURL == "" {
		tokenURL = DefaultTokenURL
	}
	if u, err := url.Parse(tokenURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("FCM_SERVICE_ACCOUNT_JSON: token_uri must be an https URL")
	}
	if f.ProjectID != "" && !ValidProjectID(f.ProjectID) {
		return nil, errors.New("FCM_SERVICE_ACCOUNT_JSON: project_id is malformed")
	}
	return &Credentials{projectID: f.ProjectID, cfg: &jwt.Config{
		Email: f.ClientEmail, PrivateKey: []byte(f.PrivateKey), PrivateKeyID: f.PrivateKeyID,
		Scopes: []string{Scope}, TokenURL: tokenURL,
	}}, nil
}

// checkRSAKey accepts one PEM block holding an RSA private key of at least 2048 bits.
func checkRSAKey(b []byte) error {
	block, _ := pem.Decode(b)
	if block == nil {
		return errors.New("not a PEM private key")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return errors.New("not an RSA key")
		}
		key = rk
	} else if rk, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = rk
	} else {
		return errors.New("not a PKCS #8 or PKCS #1 RSA key")
	}
	if key.N.BitLen() < 2048 {
		return errors.New("the RSA key is shorter than 2048 bits")
	}
	return nil
}

// ProjectID is the project_id of the key file ("" when absent).
func (c *Credentials) ProjectID() string { return c.projectID }

// TokenSource fetches and caches access tokens through client (nil: a client with a 10 s timeout).
func (c *Credentials) TokenSource(client *http.Client) oauth2.TokenSource {
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	return c.cfg.TokenSource(context.WithValue(context.Background(), oauth2.HTTPClient, client))
}

// String never prints the key.
func (c *Credentials) String() string { return "push.Credentials{redacted}" }

// GoString never prints the key either.
func (c *Credentials) GoString() string { return c.String() }
