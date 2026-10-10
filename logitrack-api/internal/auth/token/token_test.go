package token_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
)

const (
	iss = "http://localhost:8080"
	aud = "logitrack-test"
)

var t0 = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func claims() token.Claims {
	c := token.Claims{SessionID: "0199c000-0000-7000-8000-000000000001", Version: 3, AMR: "pwd"}
	c.Subject, c.ID = "0199c000-0000-7000-8000-0000000000a1", "0199c000-0000-7000-8000-00000000f001"
	return c
}

// RFC 8037 Appendix A.3: the JWK thumbprint of the Ed25519 example key.
func TestThumbprintRFC8037(t *testing.T) {
	x, err := base64.RawURLEncoding.DecodeString("11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo")
	if err != nil {
		t.Fatal(err)
	}
	if got := token.Thumbprint(ed25519.PublicKey(x)); got != "kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k" {
		t.Fatalf("thumbprint = %s", got)
	}
}

func TestSignAndParse(t *testing.T) {
	ks := token.New(newKey(t), iss, aud, 15*time.Minute)
	raw, err := ks.Sign(claims(), t0)
	if err != nil {
		t.Fatal(err)
	}
	hdr := segment(t, raw, 0)
	if hdr["alg"] != "EdDSA" || hdr["typ"] != "JWT" || hdr["kid"] != ks.ActiveKID() {
		t.Fatalf("header = %v", hdr)
	}
	body := segment(t, raw, 1)
	if body["iss"] != iss || body["exp"].(float64)-body["iat"].(float64) != 900 || body["nbf"] != body["iat"] {
		t.Fatalf("registered claims = %v", body)
	}
	c, err := ks.Parse(raw, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != claims().Subject || c.Version != 3 || c.SessionID != claims().SessionID {
		t.Fatalf("claims = %+v", c)
	}
}

func TestExpiryAndLeeway(t *testing.T) {
	ks := token.New(newKey(t), iss, aud, 15*time.Minute)
	raw, _ := ks.Sign(claims(), t0)
	if _, err := ks.Parse(raw, t0.Add(15*time.Minute+29*time.Second)); err != nil {
		t.Fatalf("inside the 30 s leeway: %v", err)
	}
	if _, err := ks.Parse(raw, t0.Add(15*time.Minute+31*time.Second)); !errors.Is(err, token.ErrExpired) {
		t.Fatalf("beyond the leeway want ErrExpired, got %v", err)
	}
	if _, err := ks.Parse(raw, t0.Add(-time.Minute)); !errors.Is(err, token.ErrInvalid) {
		t.Fatalf("issued in the future (beyond leeway) want ErrInvalid, got %v", err)
	}
}

func TestRejectsWrongIssuerAudienceAndKey(t *testing.T) {
	priv := newKey(t)
	ks := token.New(priv, iss, aud, 15*time.Minute)
	for name, other := range map[string]*token.KeySet{
		"issuer":   token.New(priv, "http://evil", aud, 15*time.Minute),
		"audience": token.New(priv, iss, "other", 15*time.Minute),
		"key":      token.New(newKey(t), iss, aud, 15*time.Minute),
	} {
		raw, _ := other.Sign(claims(), t0)
		if _, err := ks.Parse(raw, t0); !errors.Is(err, token.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

// alg none, HS256 (HMAC keyed with the public key: the classic algorithm confusion) and RS256 never
// verify, whatever the header says.
func TestRejectsOtherAlgorithms(t *testing.T) {
	priv := newKey(t)
	ks := token.New(priv, iss, aud, 15*time.Minute)
	c := claims()
	c.Issuer, c.Audience = iss, jwt.ClaimStrings{aud}
	c.IssuedAt, c.ExpiresAt = jwt.NewNumericDate(t0), jwt.NewNumericDate(t0.Add(time.Hour))

	none := jwt.NewWithClaims(jwt.SigningMethodNone, c)
	none.Header["kid"] = ks.ActiveKID()
	rawNone, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	hs.Header["kid"] = ks.ActiveKID()
	rawHS, err := hs.SignedString([]byte(priv.Public().(ed25519.PublicKey)))
	if err != nil {
		t.Fatal(err)
	}
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rs := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	rs.Header["kid"] = ks.ActiveKID()
	rawRS, err := rs.SignedString(rk)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{"none": rawNone, "HS256": rawHS, "RS256": rawRS, "garbage": "a.b.c"} {
		if _, err := ks.Parse(raw, t0); !errors.Is(err, token.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestUnknownKidRejected(t *testing.T) {
	priv := newKey(t)
	ks := token.New(priv, iss, aud, 15*time.Minute)
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims())
	tok.Header["kid"] = "not-a-known-kid"
	c := claims()
	c.Issuer, c.Audience = iss, jwt.ClaimStrings{aud}
	c.IssuedAt, c.ExpiresAt = jwt.NewNumericDate(t0), jwt.NewNumericDate(t0.Add(time.Hour))
	tok.Claims = c
	raw, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Parse(raw, t0); !errors.Is(err, token.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func writePEM(t *testing.T, typ string, der []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func privateFile(t *testing.T, k ed25519.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return writePEM(t, "PRIVATE KEY", der)
}

// Rotation (C.4.2): tokens of the previous key verify while JWT_PREVIOUS_KEY_FILE is mounted (private
// or public PEM) and stop verifying once it is removed; the JWKS lists both, active first.
func TestLoadRotationAndJWKS(t *testing.T) {
	oldKey, newK := newKey(t), newKey(t)
	oldSet := token.New(oldKey, iss, aud, 15*time.Minute)
	rawOld, _ := oldSet.Sign(claims(), t0)
	pubDER, err := x509.MarshalPKIXPublicKey(oldKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	cfg := token.Config{
		SigningKeyFile: privateFile(t, newK), PreviousKeyFile: writePEM(t, "PUBLIC KEY", pubDER),
		ActiveKID: token.Thumbprint(newK.Public().(ed25519.PublicKey)), Issuer: iss, Audience: aud, TTL: 15 * time.Minute,
	}
	ks, err := token.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Parse(rawOld, t0); err != nil {
		t.Fatalf("previous key must verify during rotation: %v", err)
	}
	doc, _ := json.Marshal(ks.JWKS())
	var jwks struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(doc, &jwks); err != nil {
		t.Fatal(err)
	}
	if len(jwks.Keys) != 2 || jwks.Keys[0]["kid"] != cfg.ActiveKID || jwks.Keys[1]["kid"] != oldSet.ActiveKID() {
		t.Fatalf("jwks = %s", doc)
	}
	for _, k := range jwks.Keys {
		if k["kty"] != "OKP" || k["crv"] != "Ed25519" || k["use"] != "sig" || k["alg"] != "EdDSA" || k["x"] == "" || k["d"] != "" {
			t.Fatalf("jwk = %v", k)
		}
	}

	cfg.PreviousKeyFile = ""
	ks, err = token.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Parse(rawOld, t0); !errors.Is(err, token.ErrInvalid) {
		t.Fatalf("after removing the previous key want ErrInvalid, got %v", err)
	}
}

func TestLoadRefusesWrongKid(t *testing.T) {
	k := newKey(t)
	_, err := token.Load(token.Config{SigningKeyFile: privateFile(t, k), ActiveKID: token.Thumbprint(newKey(t).Public().(ed25519.PublicKey)),
		Issuer: iss, Audience: aud, TTL: time.Minute})
	if err == nil || !strings.Contains(err.Error(), "JWT_ACTIVE_KID") {
		t.Fatalf("want a JWT_ACTIVE_KID error, got %v", err)
	}
	_, err = token.Load(token.Config{SigningKeyFile: filepath.Join(t.TempDir(), "missing.pem"), ActiveKID: "x",
		Issuer: iss, Audience: aud, TTL: time.Minute})
	if err == nil || !strings.Contains(err.Error(), "JWT_SIGNING_KEY_FILE") {
		t.Fatalf("want a JWT_SIGNING_KEY_FILE error, got %v", err)
	}
}

func segment(t *testing.T, raw string, i int) map[string]any {
	t.Helper()
	parts := strings.Split(raw, ".")
	b, err := base64.RawURLEncoding.DecodeString(parts[i])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
