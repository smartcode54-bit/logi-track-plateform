// Package firebasescrypt verifies passwords hashed by Firebase Auth's modified scrypt, so a user
// imported from `firebase auth:export` signs in with the old password once and is re-hashed to Argon2id
// (Appendix C §C.5.3, §C.5.4, D4/R30). The algorithm follows the firebase/scrypt reference
// (main.c:147-160, lib/scryptenc/scryptenc.c:99-121): salt = user salt || salt separator; derive
// scrypt(password, salt, N = 2^mem_cost, r = rounds, p = 1, 64 bytes); AES-256-CTR with key = the first
// 32 derived bytes and an all-zero IV over the 64-byte signer key; the result must equal the stored hash.
//
// The parameters exist only in the api process (login) and in cmd/etl runs; never in worker or scheduler.
package firebasescrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// Params are the project-level "Password hash parameters" of the Firebase Console (Authentication ->
// Users), not the per-user values of the export (C.5.2).
type Params struct {
	SignerKey     []byte // decoded FIREBASE_SCRYPT_SIGNER_KEY (64 bytes)
	SaltSeparator []byte // decoded FIREBASE_SCRYPT_SALT_SEPARATOR
	Rounds        int    // FIREBASE_SCRYPT_ROUNDS -> scrypt r (block size), not a loop count
	MemCost       int    // FIREBASE_SCRYPT_MEM_COST -> scrypt N = 1 << MemCost
}

// ParseParams decodes the four environment values. All four empty means "not configured" (nil, nil):
// legacy users then cannot sign in with a password until the parameters are set (§19 Q1).
func ParseParams(signerKeyB64, saltSeparatorB64 string, rounds, memCost int) (*Params, error) {
	if signerKeyB64 == "" && saltSeparatorB64 == "" && rounds == 0 && memCost == 0 {
		return nil, nil
	}
	var problems []string
	signer, err := DecodeB64(signerKeyB64)
	if err != nil || len(signer) == 0 {
		problems = append(problems, "FIREBASE_SCRYPT_SIGNER_KEY: must be base64")
	}
	sep, err := DecodeB64(saltSeparatorB64)
	if err != nil {
		problems = append(problems, "FIREBASE_SCRYPT_SALT_SEPARATOR: must be base64")
	}
	if rounds < 1 || rounds > 64 {
		problems = append(problems, "FIREBASE_SCRYPT_ROUNDS: must be between 1 and 64")
	}
	if memCost < 1 || memCost > 20 {
		problems = append(problems, "FIREBASE_SCRYPT_MEM_COST: must be between 1 and 20")
	}
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	return &Params{SignerKey: signer, SaltSeparator: sep, Rounds: rounds, MemCost: memCost}, nil
}

// Verify reports whether password matches the stored legacy hash. salt and want are
// users.legacy_scrypt_salt and users.legacy_scrypt_hash (bytea, decoded once by cmd/etl with DecodeB64).
// The password is used byte for byte as Firebase hashed it (no Unicode normalisation).
func Verify(password string, salt, want []byte, p Params) (bool, error) {
	if len(want) == 0 || len(salt) == 0 {
		return false, nil
	}
	got, err := Hash(password, salt, p)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummySalt is the salt of DummyVerify; any 16 bytes do.
var dummySalt = []byte("logitrack-dummy!")

// DummyVerify spends the time of one Verify and discards the result, so a failed login costs the same
// whichever kind of credential the account holds (Appendix C §C.4.8: no account enumeration by
// timing). The caller bounds the password length.
func DummyVerify(password string, p Params) {
	_, _ = Hash(password, dummySalt, p)
}

// Hash computes the legacy hash of password for salt. Verify uses it; otherwise only cmd/seed (the scrypt
// fixture user of Appendix D) and tests need it: production never creates scrypt hashes.
func Hash(password string, salt []byte, p Params) ([]byte, error) {
	s := make([]byte, 0, len(salt)+len(p.SaltSeparator))
	s = append(append(s, salt...), p.SaltSeparator...) // salt || separator (main.c:148-151)
	dk, err := scrypt.Key([]byte(password), s, 1<<p.MemCost, p.Rounds, 1, 64)
	if err != nil {
		return nil, fmt.Errorf("firebasescrypt: %w", err)
	}
	block, err := aes.NewCipher(dk[:32]) // AES-256 key = first 32 derived bytes
	if err != nil {
		return nil, fmt.Errorf("firebasescrypt: %w", err)
	}
	out := make([]byte, len(p.SignerKey))
	cipher.NewCTR(block, make([]byte, aes.BlockSize)).XORKeyStream(out, p.SignerKey) // CTR, zero IV, over the signer key
	return out, nil
}

// DecodeB64 (used by cmd/etl auth-import) accepts standard and URL-safe alphabets, padded or not.
// UNVERIFIED: which alphabet a given export uses; accepting both is harmless.
func DecodeB64(v string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(v); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("firebasescrypt: not base64")
}
