// Package googletest is an in-process stand-in for accounts.google.com: the discovery document and a
// JWKS with a locally generated RSA key, served through an http.RoundTripper, so tests of Google sign-in
// never open a socket or reach Google (Appendix C §C.9.3). Test support only; production code never
// imports it.
package googletest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/google"
)

// KeyID is the kid of the provider's signing key.
const KeyID = "googletest-key-1"

var (
	keyOnce sync.Once
	key     *rsa.PrivateKey
	keyErr  error
)

// signingKey is one RSA key per test binary (generating 2048-bit keys is slow under -race).
func signingKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() { key, keyErr = rsa.GenerateKey(rand.Reader, 2048) })
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	return key
}

// NewKey returns a fresh RSA key the provider does not publish (tokens signed with it must fail).
func NewKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// Provider serves https://accounts.google.com/.well-known/openid-configuration and
// https://accounts.google.com/keys from memory.
type Provider struct {
	t   testing.TB
	key *rsa.PrivateKey
	srv *oidctest.Server

	mu       sync.Mutex
	down     bool     // every request fails like an unreachable network
	failKeys bool     // the JWKS answers 500
	requests []string // paths served, in order
}

// New returns a provider whose JWKS publishes KeyID.
func New(t testing.TB) *Provider {
	t.Helper()
	k := signingKey(t)
	srv := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{PublicKey: k.Public(), KeyID: KeyID, Algorithm: oidc.RS256}}}
	srv.SetIssuer(google.Issuer)
	return &Provider{t: t, key: k, srv: srv}
}

// SetDown makes every request fail (true) or succeed again (false).
func (p *Provider) SetDown(down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = down
}

// SetKeysFailing makes the JWKS answer 500 while discovery still works.
func (p *Provider) SetKeysFailing(fail bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failKeys = fail
}

// Requests returns the paths served so far.
func (p *Provider) Requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requests...)
}

// Client is an HTTP client whose transport serves the provider in-process. A request for any other host
// fails: no test reaches the network.
func (p *Provider) Client() *http.Client { return &http.Client{Transport: transport{p}} }

// Verifier is a google.Verifier on this provider accepting clientIDs, with clock now (nil: time.Now).
func (p *Provider) Verifier(now func() time.Time, clientIDs ...string) *google.Verifier {
	p.t.Helper()
	v, err := google.New(google.Config{ClientIDs: clientIDs, HTTPClient: p.Client(), Now: now})
	if err != nil {
		p.t.Fatal(err)
	}
	return v
}

type transport struct{ p *Provider }

func (tr transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		_ = r.Body.Close()
	}
	if r.URL.Scheme+"://"+r.URL.Host != google.Issuer {
		return nil, errors.New("googletest: no network: " + r.URL.Host)
	}
	tr.p.mu.Lock()
	down, failKeys := tr.p.down, tr.p.failKeys
	tr.p.requests = append(tr.p.requests, r.URL.Path)
	tr.p.mu.Unlock()
	if down {
		return nil, errors.New("googletest: network down")
	}
	rec := httptest.NewRecorder()
	if failKeys && r.URL.Path == "/keys" {
		http.Error(rec, "backend error", http.StatusInternalServerError)
	} else {
		tr.p.srv.ServeHTTP(rec, r)
	}
	res := rec.Result()
	res.Request = r
	return res, nil
}

// Claims are the claims of a valid token for sub / email issued for clientID at now: iss, aud, sub,
// email, email_verified=true, iat, exp = now + 1 h. Tests edit the map before signing.
func Claims(clientID, sub, email string, now time.Time) map[string]any {
	return map[string]any{
		"iss": google.Issuer, "aud": clientID, "azp": clientID, "sub": sub, "email": email,
		"email_verified": true, "name": "Test User", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
}

// Sign signs claims with the provider key (RS256, kid KeyID).
func (p *Provider) Sign(claims map[string]any) string {
	p.t.Helper()
	return SignWith(p.t, p.key, KeyID, "RS256", claims)
}

// SignWith signs claims with any key, kid and algorithm.
func SignWith(t testing.TB, k any, kid, alg string, claims map[string]any) string {
	t.Helper()
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return oidctest.SignIDToken(k, kid, alg, string(b))
}

// Unsigned returns claims as an alg=none token.
func Unsigned(t testing.TB, claims map[string]any) string {
	t.Helper()
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + enc(b) + "."
}
