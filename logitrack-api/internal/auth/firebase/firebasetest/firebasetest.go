// Package firebasetest is an in-process stand-in for the Google endpoints the Firebase bridge talks to
// (Appendix C §C.9.5): the securetoken JWKS that signs Firebase ID tokens, the OAuth2 token endpoint of
// the service-account JWT bearer grant, and the Identity Toolkit v1 account methods (lookup, update,
// create) over an in-memory account table. Everything is served through an http.RoundTripper, so tests
// never open a socket or reach Google. Test support only; production code never imports it.
package firebasetest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/golang-jwt/jwt/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase"
)

// KeyID is the kid of the securetoken key that signs ID tokens.
const KeyID = "firebasetest-securetoken-1"

var (
	keysOnce          sync.Once
	saKey, idTokenKey *rsa.PrivateKey
	keysErr           error
)

// keys are two RSA keys per test binary (2048-bit generation is slow under -race): the service
// account's and securetoken's.
func keys(t testing.TB) (*rsa.PrivateKey, *rsa.PrivateKey) {
	t.Helper()
	keysOnce.Do(func() {
		if saKey, keysErr = rsa.GenerateKey(rand.Reader, 2048); keysErr != nil {
			return
		}
		idTokenKey, keysErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if keysErr != nil {
		t.Fatal(keysErr)
	}
	return saKey, idTokenKey
}

// NewKey returns a fresh RSA key nobody publishes (tokens signed with it must fail).
func NewKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// Account is one account of the fake Identity Toolkit.
type Account struct {
	UID              string
	Email            string
	Password         string
	Disabled         bool
	CustomAttributes string
	ValidSince       int64 // seconds; 0 = never set
}

// Call is one Identity Toolkit request as the fake received it (the body includes any password: test data).
type Call struct {
	Method string // "lookup", "update", "create", "delete"
	Body   map[string]any
}

// Backend is the fake Google.
type Backend struct {
	t         testing.TB
	projectID string
	saKey     *rsa.PrivateKey
	idKey     *rsa.PrivateKey
	email     string

	mu            sync.Mutex
	accounts      map[string]*Account
	calls         []Call
	tokens        map[string]bool // issued access tokens
	tokenRequests int
	keyRequests   int
	down          bool // every request fails like an unreachable network
	keysFailing   bool // the JWKS answers 500
	toolkitStatus int  // non-zero: every Identity Toolkit call answers this status
	toolkitCode   string
}

// New returns a backend for projectID with no accounts.
func New(t testing.TB, projectID string) *Backend {
	t.Helper()
	sa, id := keys(t)
	return &Backend{
		t: t, projectID: projectID, saKey: sa, idKey: id,
		email:    "logitrack-bridge@" + projectID + ".iam.gserviceaccount.com",
		accounts: map[string]*Account{}, tokens: map[string]bool{},
	}
}

// ProjectID is the backend's Firebase project.
func (b *Backend) ProjectID() string { return b.projectID }

// ServiceAccountJSON is a service-account key file whose key the token endpoint trusts.
func (b *Backend) ServiceAccountJSON() []byte {
	b.t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(b.saKey)
	if err != nil {
		b.t.Fatal(err)
	}
	f := map[string]string{
		"type": "service_account", "project_id": b.projectID, "private_key_id": "firebasetest-sa-key-1",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": b.email, "client_id": "100000000000000000001",
		"auth_uri": "https://accounts.google.com/o/oauth2/auth", "token_uri": firebase.DefaultTokenURL,
	}
	out, err := json.Marshal(f)
	if err != nil {
		b.t.Fatal(err)
	}
	return out
}

// ServiceAccount parses ServiceAccountJSON.
func (b *Backend) ServiceAccount() *firebase.ServiceAccount {
	b.t.Helper()
	sa, err := firebase.ParseServiceAccount(b.ServiceAccountJSON())
	if err != nil {
		b.t.Fatal(err)
	}
	return sa
}

// Client is an HTTP client served by the backend in-process; any other host fails.
func (b *Backend) Client() *http.Client { return &http.Client{Transport: transport{b}} }

// Verifier is a firebase.Verifier for the backend's project with clock now (nil: time.Now).
func (b *Backend) Verifier(now func() time.Time) *firebase.Verifier {
	b.t.Helper()
	v, err := firebase.NewVerifier(firebase.VerifierConfig{ProjectID: b.projectID, HTTPClient: b.Client(), Now: now})
	if err != nil {
		b.t.Fatal(err)
	}
	return v
}

// Accounts is a firebase.Accounts client of the backend.
func (b *Backend) Accounts() *firebase.Accounts {
	b.t.Helper()
	a, err := firebase.NewAccounts(firebase.AccountsConfig{ProjectID: b.projectID, Credentials: b.ServiceAccount(), HTTPClient: b.Client()})
	if err != nil {
		b.t.Fatal(err)
	}
	return a
}

// SetDown makes every request fail (true) or succeed again (false).
func (b *Backend) SetDown(down bool) { b.mu.Lock(); b.down = down; b.mu.Unlock() }

// SetKeysFailing makes the securetoken JWKS answer 500.
func (b *Backend) SetKeysFailing(fail bool) { b.mu.Lock(); b.keysFailing = fail; b.mu.Unlock() }

// SetToolkitError makes every Identity Toolkit call answer status with error code code (status 0 clears).
func (b *Backend) SetToolkitError(status int, code string) {
	b.mu.Lock()
	b.toolkitStatus, b.toolkitCode = status, code
	b.mu.Unlock()
}

// Put stores an account.
func (b *Backend) Put(a Account) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := a
	b.accounts[a.UID] = &c
}

// Get returns a copy of the account of uid.
func (b *Backend) Get(uid string) (Account, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a, ok := b.accounts[uid]
	if !ok {
		return Account{}, false
	}
	return *a, true
}

// Calls returns the Identity Toolkit calls received so far.
func (b *Backend) Calls() []Call {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.calls)
}

// TokenRequests and KeyRequests count the token-endpoint and JWKS requests.
func (b *Backend) TokenRequests() int { b.mu.Lock(); defer b.mu.Unlock(); return b.tokenRequests }
func (b *Backend) KeyRequests() int   { b.mu.Lock(); defer b.mu.Unlock(); return b.keyRequests }

// IDTokenClaims are the claims of a valid Firebase ID token of uid: signed in at authTime with a
// password, issued at now, expiring an hour later.
func (b *Backend) IDTokenClaims(uid string, authTime, now time.Time) map[string]any {
	return map[string]any{
		"iss": firebase.SecureTokenIssuerPrefix + b.projectID, "aud": b.projectID, "sub": uid, "user_id": uid,
		"auth_time": authTime.Unix(), "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"email": uid + "@example.test", "email_verified": true,
		"firebase": map[string]any{"identities": map[string]any{}, "sign_in_provider": "password"},
	}
}

// SignIDToken signs claims with the securetoken key (RS256, kid KeyID).
func (b *Backend) SignIDToken(claims map[string]any) string {
	b.t.Helper()
	return SignWith(b.t, b.idKey, KeyID, "RS256", claims)
}

// SignWith signs claims with any key, kid and algorithm.
func SignWith(t testing.TB, k any, kid, alg string, claims map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return oidctest.SignIDToken(k, kid, alg, string(raw))
}

// ParseCustomToken verifies a custom token as Firebase would at instant now: the RS256 signature with the
// service account's public key, aud, iss, iat and exp. It returns the header and the claims.
func ParseCustomToken(t testing.TB, sa *firebase.ServiceAccount, token string, now time.Time) (header, claims map[string]any) {
	t.Helper()
	parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return sa.PublicKey(), nil },
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(firebase.CustomTokenAudience),
		jwt.WithIssuer(sa.ClientEmail()), jwt.WithExpirationRequired(), jwt.WithIssuedAt(),
		jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("custom token does not verify: %v", err)
	}
	return parsed.Header, map[string]any(parsed.Claims.(jwt.MapClaims))
}

type transport struct{ b *Backend }

func (tr transport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		_ = r.Body.Close()
	}
	b := tr.b
	b.mu.Lock()
	down := b.down
	b.mu.Unlock()
	if down {
		return nil, errors.New("firebasetest: network down")
	}
	rec := httptest.NewRecorder()
	switch {
	case r.URL.Host == "www.googleapis.com" && r.URL.Path == "/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com":
		b.serveKeys(rec)
	case r.URL.Scheme+"://"+r.URL.Host+r.URL.Path == firebase.DefaultTokenURL:
		b.serveToken(rec, body)
	case r.URL.Host == "identitytoolkit.googleapis.com" && strings.HasPrefix(r.URL.Path, "/v1/projects/"):
		b.serveToolkit(rec, r, body)
	default:
		return nil, errors.New("firebasetest: no network: " + r.URL.Host)
	}
	res := rec.Result()
	res.Request = r
	return res, nil
}

func (b *Backend) serveKeys(w http.ResponseWriter) {
	b.mu.Lock()
	b.keyRequests++
	failing := b.keysFailing
	b.mu.Unlock()
	if failing {
		http.Error(w, "backend error", http.StatusInternalServerError)
		return
	}
	pub := b.idKey.PublicKey
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": KeyID,
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

// serveToken implements the RFC 7523 JWT bearer grant of a service account.
func (b *Backend) serveToken(w http.ResponseWriter, body []byte) {
	b.mu.Lock()
	b.tokenRequests++
	b.mu.Unlock()
	form, err := url.ParseQuery(string(body))
	if err != nil || form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	tok, err := jwt.Parse(form.Get("assertion"), func(*jwt.Token) (any, error) { return &b.saKey.PublicKey, nil },
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(firebase.DefaultTokenURL), jwt.WithIssuer(b.email),
		jwt.WithExpirationRequired())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	scope, _ := tok.Claims.(jwt.MapClaims)["scope"].(string)
	if !slices.Contains(strings.Fields(scope), "https://www.googleapis.com/auth/identitytoolkit") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_scope"})
		return
	}
	access := "firebasetest-access-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b.mu.Lock()
	b.tokens[access] = true
	b.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600})
}

func toolkitError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": status, "message": code + " : detail that must not leak", "status": "INVALID_ARGUMENT"}})
}

func (b *Backend) serveToolkit(w http.ResponseWriter, r *http.Request, body []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.Method != http.MethodPost {
		toolkitError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !b.tokens[auth] {
		toolkitError(w, http.StatusUnauthorized, "CREDENTIAL_MISSING")
		return
	}
	prefix := "/v1/projects/" + b.projectID
	if !strings.HasPrefix(r.URL.Path, prefix+"/") {
		toolkitError(w, http.StatusBadRequest, "PROJECT_NOT_FOUND")
		return
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		toolkitError(w, http.StatusBadRequest, "INVALID_JSON")
		return
	}
	method := map[string]string{"/accounts:lookup": "lookup", "/accounts:update": "update", "/accounts": "create",
		"/accounts:delete": "delete"}[strings.TrimPrefix(r.URL.Path, prefix)]
	if method == "" {
		toolkitError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	b.calls = append(b.calls, Call{Method: method, Body: req})
	if b.toolkitStatus != 0 {
		toolkitError(w, b.toolkitStatus, b.toolkitCode)
		return
	}
	switch method {
	case "lookup":
		// By localId or by email, as Google answers both (emails compare case-insensitively).
		var found []*Account
		ids, _ := req["localId"].([]any)
		for _, id := range ids {
			s, _ := id.(string)
			if a, ok := b.accounts[s]; ok {
				found = append(found, a)
			}
		}
		emails, _ := req["email"].([]any)
		for _, e := range emails {
			s, _ := e.(string)
			for _, a := range b.accounts {
				if s != "" && strings.EqualFold(a.Email, s) {
					found = append(found, a)
				}
			}
		}
		var users []map[string]any
		for _, a := range found {
			u := map[string]any{"localId": a.UID, "email": a.Email, "disabled": a.Disabled,
				"passwordHash": "UkVEQUNURUQ=", "salt": "c2FsdA=="}
			if a.CustomAttributes != "" {
				u["customAttributes"] = a.CustomAttributes
			}
			if a.ValidSince != 0 {
				u["validSince"] = strconv.FormatInt(a.ValidSince, 10)
			}
			users = append(users, u)
		}
		out := map[string]any{"kind": "identitytoolkit#GetAccountInfoResponse"}
		if len(users) > 0 {
			out["users"] = users
		}
		writeJSON(w, http.StatusOK, out)
	case "update":
		uid, _ := req["localId"].(string)
		a, ok := b.accounts[uid]
		if !ok {
			toolkitError(w, http.StatusBadRequest, "USER_NOT_FOUND")
			return
		}
		if v, ok := req["password"].(string); ok {
			if len(v) < 6 {
				toolkitError(w, http.StatusBadRequest, "WEAK_PASSWORD")
				return
			}
			a.Password = v
		}
		if v, ok := req["disableUser"].(bool); ok {
			a.Disabled = v
		}
		if v, ok := req["email"].(string); ok {
			for _, o := range b.accounts {
				if o.UID != uid && v != "" && strings.EqualFold(o.Email, v) {
					toolkitError(w, http.StatusBadRequest, "EMAIL_EXISTS")
					return
				}
			}
			a.Email = v
		}
		if v, ok := req["validSince"].(string); ok {
			s, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				toolkitError(w, http.StatusBadRequest, "INVALID_VALID_SINCE")
				return
			}
			a.ValidSince = s
		}
		if v, ok := req["customAttributes"].(string); ok {
			var m map[string]any
			if json.Unmarshal([]byte(v), &m) != nil || len(v) > firebase.MaxClaimsLength {
				toolkitError(w, http.StatusBadRequest, "INVALID_CLAIMS")
				return
			}
			a.CustomAttributes = v
		}
		writeJSON(w, http.StatusOK, map[string]any{"kind": "identitytoolkit#SetAccountInfoResponse", "localId": uid})
	case "create":
		uid, _ := req["localId"].(string)
		if _, ok := b.accounts[uid]; ok {
			toolkitError(w, http.StatusBadRequest, "DUPLICATE_LOCAL_ID")
			return
		}
		email, _ := req["email"].(string)
		for _, a := range b.accounts {
			if email != "" && strings.EqualFold(a.Email, email) {
				toolkitError(w, http.StatusBadRequest, "EMAIL_EXISTS")
				return
			}
		}
		a := &Account{UID: uid, Email: email}
		a.Password, _ = req["password"].(string)
		a.Disabled, _ = req["disabled"].(bool)
		b.accounts[uid] = a
		writeJSON(w, http.StatusOK, map[string]any{"kind": "identitytoolkit#SignupNewUserResponse", "localId": uid})
	case "delete":
		uid, _ := req["localId"].(string)
		if _, ok := b.accounts[uid]; !ok {
			toolkitError(w, http.StatusBadRequest, "USER_NOT_FOUND")
			return
		}
		delete(b.accounts, uid)
		writeJSON(w, http.StatusOK, map[string]any{"kind": "identitytoolkit#DeleteAccountResponse"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
