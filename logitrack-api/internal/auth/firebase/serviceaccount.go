package firebase

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net/url"
	"os"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// maxKeyFileBytes bounds the service-account file (a real one is about 2.3 KB).
const maxKeyFileBytes = 64 << 10

// ServiceAccount is the service-account key of GOOGLE_APPLICATION_CREDENTIALS: it signs custom tokens
// and the OAuth2 assertions of Accounts. Every field is unexported and String hides them, so a log line
// or an error that formats the value never prints the key.
type ServiceAccount struct {
	projectID    string
	clientEmail  string
	privateKeyID string
	tokenURL     string
	key          *rsa.PrivateKey
	keyPEM       []byte
}

// LoadServiceAccount reads and parses the key file at path. Errors name the problem, never the content.
func LoadServiceAccount(path string) (*ServiceAccount, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("GOOGLE_APPLICATION_CREDENTIALS: cannot open the service-account file")
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxKeyFileBytes+1))
	if err != nil {
		return nil, errors.New("GOOGLE_APPLICATION_CREDENTIALS: cannot read the service-account file")
	}
	if len(b) > maxKeyFileBytes {
		return nil, errors.New("GOOGLE_APPLICATION_CREDENTIALS: the file is too large to be a service-account key")
	}
	return ParseServiceAccount(b)
}

// ParseServiceAccount parses a Google service-account key file (type "service_account"): client_email,
// private_key (PEM, PKCS #8 or PKCS #1 RSA), private_key_id, project_id and token_uri (https; Google's
// endpoint when absent).
func ParseServiceAccount(b []byte) (*ServiceAccount, error) {
	var f struct {
		Type         string `json:"type"`
		ProjectID    string `json:"project_id"`
		PrivateKeyID string `json:"private_key_id"`
		PrivateKey   string `json:"private_key"`
		ClientEmail  string `json:"client_email"`
		TokenURI     string `json:"token_uri"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, errors.New("GOOGLE_APPLICATION_CREDENTIALS: not a JSON service-account key file")
	}
	if f.Type != "service_account" {
		return nil, errors.New(`GOOGLE_APPLICATION_CREDENTIALS: the file is not a service-account key (type must be "service_account")`)
	}
	if a, err := mail.ParseAddress(f.ClientEmail); err != nil || a.Address != f.ClientEmail {
		return nil, errors.New("GOOGLE_APPLICATION_CREDENTIALS: client_email is missing or malformed")
	}
	key, err := parseRSAKey([]byte(f.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS: private_key: %w", err)
	}
	tokenURL := f.TokenURI
	if tokenURL == "" {
		tokenURL = DefaultTokenURL
	}
	if u, err := url.Parse(tokenURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("GOOGLE_APPLICATION_CREDENTIALS: token_uri must be an https URL")
	}
	if f.ProjectID != "" && !ValidProjectID(f.ProjectID) {
		return nil, errors.New("GOOGLE_APPLICATION_CREDENTIALS: project_id is malformed")
	}
	return &ServiceAccount{
		projectID: f.ProjectID, clientEmail: f.ClientEmail, privateKeyID: f.PrivateKeyID, tokenURL: tokenURL,
		key: key, keyPEM: []byte(f.PrivateKey),
	}, nil
}

// parseRSAKey reads one PEM block holding an RSA private key of at least 2048 bits.
func parseRSAKey(b []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("not a PEM private key")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("not an RSA key")
		}
		key = rk
	} else if rk, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = rk
	} else {
		return nil, errors.New("not a PKCS #8 or PKCS #1 RSA key")
	}
	if key.N.BitLen() < 2048 {
		return nil, errors.New("the RSA key is shorter than 2048 bits")
	}
	return key, nil
}

// ProjectID is the project_id of the key file ("" when absent).
func (sa *ServiceAccount) ProjectID() string { return sa.projectID }

// ClientEmail is the service account's address: the iss and sub of its custom tokens.
func (sa *ServiceAccount) ClientEmail() string { return sa.clientEmail }

// PublicKey is the public half of the signing key (tests verify custom tokens with it).
func (sa *ServiceAccount) PublicKey() *rsa.PublicKey { return &sa.key.PublicKey }

// String never prints the key.
func (sa *ServiceAccount) String() string { return "firebase.ServiceAccount{redacted}" }

// GoString never prints the key either.
func (sa *ServiceAccount) GoString() string { return sa.String() }

// customTokenClaims are the claims of a Firebase custom token (Admin SDK auth.go customToken).
type customTokenClaims struct {
	Iss    string         `json:"iss"`
	Sub    string         `json:"sub"`
	Aud    string         `json:"aud"`
	Iat    int64          `json:"iat"`
	Exp    int64          `json:"exp"`
	UID    string         `json:"uid"`
	Claims map[string]any `json:"claims,omitempty"`
}

// GetExpirationTime and the other jwt.Claims methods are never consulted: the token is only signed here.
func (c customTokenClaims) GetExpirationTime() (*jwt.NumericDate, error) {
	return jwt.NewNumericDate(time.Unix(c.Exp, 0)), nil
}
func (c customTokenClaims) GetIssuedAt() (*jwt.NumericDate, error) {
	return jwt.NewNumericDate(time.Unix(c.Iat, 0)), nil
}
func (c customTokenClaims) GetNotBefore() (*jwt.NumericDate, error) { return nil, nil }
func (c customTokenClaims) GetIssuer() (string, error)              { return c.Iss, nil }
func (c customTokenClaims) GetSubject() (string, error)             { return c.Sub, nil }
func (c customTokenClaims) GetAudience() (jwt.ClaimStrings, error) {
	return jwt.ClaimStrings{c.Aud}, nil
}

// CustomToken mints the Firebase custom token for uid with developer claims (Appendix C §C.6.3): an
// RS256 JWT with header {"alg":"RS256","typ":"JWT"} and iss = sub = client_email, aud =
// CustomTokenAudience, iat = now, exp = now + CustomTokenTTL, uid and claims. The browser exchanges it
// with signInWithCustomToken; the resulting ID token carries the claims.
func (sa *ServiceAccount) CustomToken(uid string, claims map[string]any, now time.Time) (string, error) {
	if !validUID(uid) {
		return "", fmt.Errorf("firebase: uid must be 1 to %d bytes", MaxUIDLength)
	}
	for k := range claims {
		if slices.Contains(ReservedClaims, k) {
			return "", fmt.Errorf("firebase: developer claim %q is reserved", k)
		}
	}
	if len(claims) > 0 {
		b, err := json.Marshal(claims)
		if err != nil {
			return "", fmt.Errorf("firebase: claims: %w", err)
		}
		if len(b) > MaxClaimsLength {
			return "", fmt.Errorf("firebase: claims exceed %d bytes", MaxClaimsLength)
		}
	}
	iat := now.Unix()
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, customTokenClaims{
		Iss: sa.clientEmail, Sub: sa.clientEmail, Aud: CustomTokenAudience,
		Iat: iat, Exp: iat + int64(CustomTokenTTL/time.Second), UID: uid, Claims: claims,
	})
	return t.SignedString(sa.key)
}
