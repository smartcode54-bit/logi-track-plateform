// Package scram builds PostgreSQL SCRAM-SHA-256 password verifiers (RFC 5802/7677) for the tools
// that set the LOGIN passwords of the R66 roles: tools/devdb locally, tools/vmenv on the staging VM
// (developer-spec.md §15.3). A verifier given to ALTER ROLE ... PASSWORD is stored as is, so the
// cleartext never reaches the server, its log or pg_stat_statements.
package scram

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// Iterations is the PBKDF2 iteration count PostgreSQL uses by default (scram_iterations).
const Iterations = 4096

// Verifier returns SCRAM-SHA-256$<iterations>:<salt>$<StoredKey>:<ServerKey> for password with a
// 16-byte random salt. The tools generate ASCII passwords, which SASLprep leaves unchanged.
func Verifier(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return verifier(password, salt, Iterations)
}

func verifier(password string, salt []byte, iterations int) (string, error) {
	storedKey, serverKey, err := keys(password, salt, iterations)
	if err != nil {
		return "", err
	}
	enc := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iterations, enc(salt), enc(storedKey), enc(serverKey)), nil
}

// keys derives StoredKey and ServerKey (RFC 5802 §3).
func keys(password string, salt []byte, iterations int) (storedKey, serverKey []byte, err error) {
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		return nil, nil, err
	}
	clientKey := mac(salted, "Client Key")
	stored := sha256.Sum256(clientKey)
	return stored[:], mac(salted, "Server Key"), nil
}

func mac(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}
