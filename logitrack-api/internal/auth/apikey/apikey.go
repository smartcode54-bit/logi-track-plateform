// Package apikey is the secret format and hash of api_keys (Appendix C §C.4.11, R45, R82): a secret
// ltk_<prefix>_<43 base64url characters>, shown once by POST /v1/api-keys and stored as
// sha256(secret ‖ API_KEY_PEPPER) next to its 8-character prefix, which the lookup uses and the UI shows.
// It does no I/O: internal/iam issues keys with it and internal/auth verifies the X-Api-Key header.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"strings"
)

// Format of a secret: Scheme + an 8-character prefix + "_" + 43 base64url characters (32 random bytes).
const (
	Scheme    = "ltk_"
	PrefixLen = 8
	BodyLen   = 43
	// Len is the length of every secret.
	Len = len(Scheme) + PrefixLen + 1 + BodyLen
	// MinPepperLen is the shortest API_KEY_PEPPER the api accepts (vmenv and devenv generate 48).
	MinPepperLen = 32
)

// prefixEncoding writes 5 random bytes as 8 lower-case base32 characters (a-z, 2-7): no "_" or "-", so the
// prefix reads unambiguously in the UI and in logs.
var prefixEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// ErrPepper is returned for an API_KEY_PEPPER shorter than MinPepperLen.
var ErrPepper = errors.New("apikey: API_KEY_PEPPER must be at least 32 characters")

// Pepper is API_KEY_PEPPER. Its zero value is "unset": API keys are off (no key is issued or accepted). It
// never prints its value.
type Pepper struct{ b []byte }

// NewPepper takes the value of API_KEY_PEPPER; "" is the unset pepper.
func NewPepper(v string) (Pepper, error) {
	if v == "" {
		return Pepper{}, nil
	}
	if len(v) < MinPepperLen {
		return Pepper{}, ErrPepper
	}
	return Pepper{b: []byte(v)}, nil
}

// Set reports whether a pepper is configured.
func (p Pepper) Set() bool { return len(p.b) > 0 }

// String never reveals the value.
func (p Pepper) String() string {
	if p.Set() {
		return "[set]"
	}
	return "[unset]"
}

// GoString never reveals the value either (%#v).
func (p Pepper) GoString() string { return p.String() }

// Hash is sha256(secret ‖ pepper), the 32 bytes of api_keys.key_hash. The whole secret is hashed, scheme and
// prefix included.
func (p Pepper) Hash(secret string) []byte {
	h := sha256.New()
	h.Write([]byte(secret))
	h.Write(p.b)
	return h.Sum(nil)
}

// Matches reports, in constant time, whether secret hashes to stored under p.
func (p Pepper) Matches(secret string, stored []byte) bool {
	return p.Set() && len(stored) == sha256.Size && subtle.ConstantTimeCompare(p.Hash(secret), stored) == 1
}

// Key is a newly issued key: the secret is returned to the caller once and never stored.
type Key struct {
	Secret string
	Prefix string
	Hash   []byte
}

// Generate issues a key under p. The prefix carries 40 random bits and the body 256.
func Generate(p Pepper) (Key, error) {
	if !p.Set() {
		return Key{}, errors.New("apikey: no API_KEY_PEPPER")
	}
	pre := make([]byte, 5)
	body := make([]byte, 32)
	if _, err := rand.Read(pre); err != nil {
		return Key{}, err
	}
	if _, err := rand.Read(body); err != nil {
		return Key{}, err
	}
	prefix := prefixEncoding.EncodeToString(pre)
	secret := Scheme + prefix + "_" + base64.RawURLEncoding.EncodeToString(body)
	return Key{Secret: secret, Prefix: prefix, Hash: p.Hash(secret)}, nil
}

// Prefix returns the prefix of a presented secret; ok is false when the value cannot be a secret of this
// format (wrong length, scheme, separator or alphabet), so it is refused before any lookup.
func Prefix(secret string) (prefix string, ok bool) {
	if len(secret) != Len || !strings.HasPrefix(secret, Scheme) || secret[len(Scheme)+PrefixLen] != '_' {
		return "", false
	}
	prefix = secret[len(Scheme) : len(Scheme)+PrefixLen]
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return "", false
		}
	}
	body := secret[len(Scheme)+PrefixLen+1:]
	if b, err := base64.RawURLEncoding.Strict().DecodeString(body); err != nil || len(b) != 32 {
		return "", false
	}
	return prefix, true
}
