//go:build integration

package auth_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
)

// jwksDoc is GET /.well-known/jwks.json as a standard JWKS client reads it.
type jwksDoc struct {
	Keys []map[string]string `json:"keys"`
}

// fetchJWKS reads the document from base and checks the transport contract of Appendix C §C.4.2: 200,
// JSON, Cache-Control: public, max-age=300, the bare JWK Set (no {"data"} envelope) and no private part.
func fetchJWKS(t *testing.T, base string) jwksDoc {
	t.Helper()
	res, err := http.Get(base + "/.well-known/jwks.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("internal jwks: %d", res.StatusCode)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "public, max-age=300" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 || raw["keys"] == nil {
		t.Fatalf("jwks must be the bare {\"keys\"} document, got keys %v", raw)
	}
	var doc jwksDoc
	if err := json.Unmarshal(raw["keys"], &doc.Keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range doc.Keys {
		if k["kty"] != "OKP" || k["crv"] != "Ed25519" || k["alg"] != "EdDSA" || k["use"] != "sig" || k["kid"] == "" || k["d"] != "" {
			t.Fatalf("jwk = %v", k)
		}
	}
	return doc
}

// kids lists the key ids of the document in order.
func (d jwksDoc) kids() []string {
	out := make([]string, 0, len(d.Keys))
	for _, k := range d.Keys {
		out = append(out, k["kid"])
	}
	return out
}

// publicKey returns the Ed25519 key published under kid; its RFC 7638 thumbprint must be kid.
func (d jwksDoc) publicKey(t *testing.T, kid string) ed25519.PublicKey {
	t.Helper()
	for _, k := range d.Keys {
		if k["kid"] != kid {
			continue
		}
		x, err := base64.RawURLEncoding.DecodeString(k["x"])
		if err != nil || len(x) != ed25519.PublicKeySize {
			t.Fatalf("jwk %s: bad x", kid)
		}
		pub := ed25519.PublicKey(x)
		if token.Thumbprint(pub) != kid {
			t.Fatalf("jwk %s: kid is not the thumbprint of x", kid)
		}
		return pub
	}
	t.Fatalf("kid %s is not published", kid)
	return nil
}

// verifyWithJWKS checks raw the way an independent client does: the key named by the token's kid,
// taken from the published document, EdDSA only, iss and aud.
func verifyWithJWKS(t *testing.T, doc jwksDoc, raw string, at time.Time) error {
	t.Helper()
	_, err := jwt.Parse(raw, func(tk *jwt.Token) (any, error) {
		kid, _ := tk.Header["kid"].(string)
		return doc.publicKey(t, kid), nil
	}, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(issuer), jwt.WithAudience(audience),
		jwt.WithExpirationRequired(), jwt.WithTimeFunc(func() time.Time { return at }))
	return err
}

// TW3: the JWKS is served on the internal listener only; the public listener answers 404 not_found
// like every internal route, so Caddy's API site never exposes it.
func TestJWKSOnInternalListenerOnly(t *testing.T) {
	h := newHarness(t)
	doc := fetchJWKS(t, h.internal)
	if got := doc.kids(); len(got) != 1 || got[0] != h.keys.ActiveKID() {
		t.Fatalf("kids = %v, want the active kid only", got)
	}

	own := h.tenant("own_fleet", "Own")
	u := h.user("jwks@logitrack.test")
	h.member(u, own, "manager")
	s := h.mustLogin("jwks@logitrack.test", "web", "")
	if err := verifyWithJWKS(t, doc, s.access, h.clock.Now()); err != nil {
		t.Fatalf("a fresh access token must verify against the published key: %v", err)
	}

	r := h.call(h.public, http.MethodGet, "/.well-known/jwks.json", "", nil)
	expectError(t, r, http.StatusNotFound, "not_found")
}

// TW3 acceptance "previous key verifies until expiry" (Appendix C §C.4.2 rotation runbook): after a
// rotation the JWKS lists the new key first and the previous one second; a token signed by the previous
// key still verifies, both against the document and at Go, until its own exp; once the previous key is
// removed it no longer verifies.
func TestJWKSRotationPreviousKeyVerifiesUntilExpiry(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("rotate@logitrack.test")
	h.member(u, own, "manager")
	old := h.mustLogin("rotate@logitrack.test", "web", "")
	oldKID := h.keys.ActiveKID()
	oldPub := fetchJWKS(t, h.internal).publicKey(t, oldKID)

	cfg := auth.Config{
		RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour, PasswordResetTTL: 30 * time.Minute,
		Scrypt: &h.scrypt, RateLimitEnabled: true, LoginIP: ratelimit.Limit{Count: 1000, Window: time.Minute},
	}
	_, newPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	// Deploy with the new signing key and the old one as JWT_PREVIOUS_KEY_FILE.
	h.keys = token.New(newPriv, issuer, audience, 15*time.Minute, oldPub)
	rotated, _ := h.service(cfg, h.rdb)
	h.serve(rotated)
	rotatedBase := h.internal
	doc := fetchJWKS(t, rotatedBase)
	if got := doc.kids(); len(got) != 2 || got[0] != h.keys.ActiveKID() || got[1] != oldKID {
		t.Fatalf("kids after rotation = %v, want [new, previous]", got)
	}
	if err := verifyWithJWKS(t, doc, old.access, h.clock.Now()); err != nil {
		t.Fatalf("the previous key must verify through the JWKS during the rotation: %v", err)
	}
	if r := h.call(rotatedBase, http.MethodGet, "/v1/me", old.access, nil); r.status != http.StatusOK {
		t.Fatalf("GET /v1/me with a previous-key token: %d %s", r.status, r.raw)
	}
	fresh := h.mustLogin("rotate@logitrack.test", "web", "")
	if hd := header(t, fresh.access); hd["kid"] != h.keys.ActiveKID() {
		t.Fatalf("new tokens must carry the new kid, got %v", hd["kid"])
	}

	// Removing JWT_PREVIOUS_KEY_FILE before the old token expired makes it unverifiable.
	h.keys = token.New(newPriv, issuer, audience, 15*time.Minute)
	trimmed, _ := h.service(cfg, h.rdb)
	h.serve(trimmed)
	if got := fetchJWKS(t, h.internal).kids(); len(got) != 1 || got[0] != h.keys.ActiveKID() {
		t.Fatalf("kids after removing the previous key = %v", got)
	}
	expectError(t, h.call(h.internal, http.MethodGet, "/v1/me", old.access, nil), http.StatusUnauthorized, "invalid_token")

	// With the previous key still configured, the old token stops at its own exp (15 min + 30 s leeway).
	h.clock.Advance(15*time.Minute + 31*time.Second)
	if err := verifyWithJWKS(t, doc, old.access, h.clock.Now()); err == nil {
		t.Fatal("an expired previous-key token must not verify")
	}
	r := h.call(rotatedBase, http.MethodGet, "/v1/me", old.access, nil)
	expectError(t, r, http.StatusUnauthorized, "token_expired")
	if r.details()["reason"] != "expired" {
		t.Fatalf("details = %v", r.details())
	}
}
