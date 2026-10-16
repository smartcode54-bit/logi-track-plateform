package iam

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"slices"
)

// The API already configures JWT_SIGNING_KEY_FILE on every replica. Derive a purpose-specific
// pagination key from that shared secret unless a dedicated base64 SUPPORT_CURSOR_KEY is supplied.
func supportCursorKey(explicit []byte) ([]byte, error) {
	if len(explicit) > 0 {
		if len(explicit) < 32 {
			return nil, errors.New("iam: CursorKey must contain at least 32 bytes")
		}
		return slices.Clone(explicit), nil
	}
	if raw := os.Getenv("SUPPORT_CURSOR_KEY"); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(key) < 32 {
			return nil, errors.New("iam: SUPPORT_CURSOR_KEY must be base64 with at least 32 decoded bytes")
		}
		return key, nil
	}
	if path := os.Getenv("JWT_SIGNING_KEY_FILE"); path != "" {
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.New("iam: cannot read cursor signing key source")
		}
		if len(key) == 0 {
			return nil, errors.New("iam: empty cursor signing key source")
		}
		sum := sha256.Sum256(append([]byte("logitrack:iam:support-options:v1\x00"), key...))
		return sum[:], nil
	}
	if env := os.Getenv("APP_ENV"); env != "" && env != "local" {
		return nil, errors.New("iam: shared SUPPORT_CURSOR_KEY or JWT_SIGNING_KEY_FILE is required")
	}
	// Isolated local/test constructors can use ephemeral cursors.
	key := make([]byte, 32)
	_, err := rand.Read(key)
	return key, err
}
