// Package token signs and verifies the Go access JWT (Appendix C §C.4.1, §C.4.2, R3): EdDSA over
// Ed25519, kid = RFC 7638 thumbprint, iss/aud from the environment, 30 s leeway. It also renders the
// JWKS document that the internal GET /.well-known/jwks.json serves to the web edge gate (TW3).
package token

import (
	"crypto"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Leeway is the clock skew tolerated on exp, nbf and iat (C.4.1).
const Leeway = 30 * time.Second

// Errors returned by Parse. Callers map ErrExpired to 401 token_expired (details.reason "expired") and
// everything else to 401 invalid_token.
var (
	ErrExpired = errors.New("token: expired")
	ErrInvalid = errors.New("token: invalid")
)

// Claims is the exact claim set of an access token: registered iss, sub, aud, exp, nbf, iat, jti plus
// sid, ver, tid, rol, plt, dsp, drv, cs, amr. Empty optional claims are omitted, so a staff token has no
// plt / dsp / drv / cs and a customer-scope token has no tid / rol. No capabilities, email, name or
// tenant list: GET /v1/me serves them.
type Claims struct {
	SessionID      string   `json:"sid"`
	Version        int32    `json:"ver"`
	TenantID       string   `json:"tid,omitempty"`
	Role           string   `json:"rol,omitempty"`
	Platform       []string `json:"plt,omitempty"`
	Dispatcher     bool     `json:"dsp,omitempty"`
	DriverID       string   `json:"drv,omitempty"`
	CustomerScopes []string `json:"cs,omitempty"`
	AMR            string   `json:"amr"`
	jwt.RegisteredClaims
}

// Config names the key material and token settings (JWT_* of main spec §16.1).
type Config struct {
	SigningKeyFile  string        // JWT_SIGNING_KEY_FILE: PKCS#8 PEM Ed25519 private key
	PreviousKeyFile string        // JWT_PREVIOUS_KEY_FILE: optional, private or public PEM; verify-only
	ActiveKID       string        // JWT_ACTIVE_KID: must equal the thumbprint of the signing key
	Issuer          string        // JWT_ISSUER
	Audience        string        // JWT_AUDIENCE
	TTL             time.Duration // JWT_ACCESS_TTL
}

// KeySet signs with the active key and verifies with the active and the previous key.
type KeySet struct {
	signer    ed25519.PrivateKey
	activeKID string
	verify    map[string]ed25519.PublicKey
	order     []string // JWKS order: active first
	issuer    string
	audience  string
	ttl       time.Duration
}

// Load reads the key files and refuses a signing key whose thumbprint differs from ActiveKID, the
// start-up guard against mounting the wrong key (C.4.2). Errors name variables, never key material.
func Load(c Config) (*KeySet, error) {
	if c.Issuer == "" || c.Audience == "" {
		return nil, errors.New("JWT_ISSUER and JWT_AUDIENCE must be set")
	}
	if c.TTL <= 0 {
		return nil, errors.New("JWT_ACCESS_TTL must be positive")
	}
	raw, err := os.ReadFile(c.SigningKeyFile)
	if err != nil {
		return nil, errors.New("JWT_SIGNING_KEY_FILE: cannot read the file")
	}
	priv, ok := parseKey(raw)
	if !ok || priv == nil {
		return nil, errors.New("JWT_SIGNING_KEY_FILE: not a PKCS#8 PEM Ed25519 private key")
	}
	pub := priv.Public().(ed25519.PublicKey)
	if Thumbprint(pub) != c.ActiveKID {
		return nil, errors.New("JWT_ACTIVE_KID: does not equal the RFC 7638 thumbprint of JWT_SIGNING_KEY_FILE")
	}
	ks := &KeySet{
		signer: priv, activeKID: c.ActiveKID,
		verify: map[string]ed25519.PublicKey{c.ActiveKID: pub}, order: []string{c.ActiveKID},
		issuer: c.Issuer, audience: c.Audience, ttl: c.TTL,
	}
	if c.PreviousKeyFile != "" {
		raw, err := os.ReadFile(c.PreviousKeyFile)
		if err != nil {
			return nil, errors.New("JWT_PREVIOUS_KEY_FILE: cannot read the file")
		}
		prev, ok := parsePublic(raw)
		if !ok {
			return nil, errors.New("JWT_PREVIOUS_KEY_FILE: not a PEM Ed25519 key (PKCS#8 private or PKIX public)")
		}
		kid := Thumbprint(prev)
		if kid != c.ActiveKID {
			ks.verify[kid] = prev
			ks.order = append(ks.order, kid)
		}
	}
	return ks, nil
}

// New builds a key set from an in-memory key (tests and tools).
func New(priv ed25519.PrivateKey, issuer, audience string, ttl time.Duration, previous ...ed25519.PublicKey) *KeySet {
	pub := priv.Public().(ed25519.PublicKey)
	kid := Thumbprint(pub)
	ks := &KeySet{signer: priv, activeKID: kid, verify: map[string]ed25519.PublicKey{kid: pub}, order: []string{kid},
		issuer: issuer, audience: audience, ttl: ttl}
	for _, p := range previous {
		k := Thumbprint(p)
		if _, dup := ks.verify[k]; !dup {
			ks.verify[k] = p
			ks.order = append(ks.order, k)
		}
	}
	return ks
}

func parseKey(raw []byte) (ed25519.PrivateKey, bool) {
	blk, _ := pem.Decode(raw)
	if blk == nil || blk.Type != "PRIVATE KEY" {
		return nil, false
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, false
	}
	priv, ok := k.(ed25519.PrivateKey)
	return priv, ok
}

func parsePublic(raw []byte) (ed25519.PublicKey, bool) {
	if priv, ok := parseKey(raw); ok {
		return priv.Public().(ed25519.PublicKey), true
	}
	blk, _ := pem.Decode(raw)
	if blk == nil || blk.Type != "PUBLIC KEY" {
		return nil, false
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return nil, false
	}
	pub, ok := k.(ed25519.PublicKey)
	return pub, ok
}

// Thumbprint is the RFC 7638 JWK thumbprint of an Ed25519 public key: base64url SHA-256 of
// {"crv":"Ed25519","kty":"OKP","x":"<b64url>"} (members in lexicographic order, no spaces).
func Thumbprint(pub ed25519.PublicKey) string {
	x := base64.RawURLEncoding.EncodeToString(pub)
	sum := sha256.Sum256([]byte(`{"crv":"Ed25519","kty":"OKP","x":"` + x + `"}`))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ActiveKID is the kid of new tokens.
func (k *KeySet) ActiveKID() string { return k.activeKID }

// TTL is JWT_ACCESS_TTL.
func (k *KeySet) TTL() time.Duration { return k.ttl }

// Sign stamps iss, aud, iat, nbf and exp (= now + TTL) on c and signs it with the active key. The caller
// sets sub, jti and the private claims.
func (k *KeySet) Sign(c Claims, now time.Time) (string, error) {
	now = now.Truncate(time.Second)
	c.Issuer = k.issuer
	c.Audience = jwt.ClaimStrings{k.audience}
	c.IssuedAt = jwt.NewNumericDate(now)
	c.NotBefore = jwt.NewNumericDate(now)
	c.ExpiresAt = jwt.NewNumericDate(now.Add(k.ttl))
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	t.Header["kid"] = k.activeKID
	s, err := t.SignedString(crypto.Signer(k.signer))
	if err != nil {
		return "", fmt.Errorf("token: sign: %w", err)
	}
	return s, nil
}

// Parse verifies the signature with the key named by kid (active or previous), then iss, aud, exp
// (required), nbf and iat with Leeway, at the instant now. Only EdDSA is accepted: alg none, HS256 and
// RS256 tokens fail before any key is used.
func (k *KeySet) Parse(raw string, now time.Time) (*Claims, error) {
	p := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(k.issuer),
		jwt.WithAudience(k.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(Leeway),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	var c Claims
	_, err := p.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		pub, ok := k.verify[kid]
		if !ok {
			return nil, errors.New("unknown kid")
		}
		return pub, nil
	})
	switch {
	case err == nil:
		return &c, nil
	case errors.Is(err, jwt.ErrTokenExpired):
		return nil, ErrExpired
	default:
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
}

// JWK is one public key of the JWKS document.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
}

// JWKS is the document of GET /.well-known/jwks.json (C.4.2): the active key and, during a rotation,
// the previous one. The route itself is mounted by TW3 with Cache-Control: public, max-age=300.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWKS returns the public keys, active first.
func (k *KeySet) JWKS() JWKS {
	out := JWKS{Keys: make([]JWK, 0, len(k.order))}
	for _, kid := range k.order {
		out.Keys = append(out.Keys, JWK{
			Kty: "OKP", Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString(k.verify[kid]),
			Kid: kid, Use: "sig", Alg: "EdDSA",
		})
	}
	return out
}
