package password

import "crypto/rand"

// temporaryAlphabet has 32 symbols without the look-alikes 0/O, 1/I: an admin can read a temporary
// password aloud or over the phone (C.4.8).
const temporaryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// TemporaryLength is the length of an admin-issued temporary password (12 symbols, 60 bits).
const TemporaryLength = 12

// Temporary generates a temporary password for POST /v1/users/{id}/password/temporary (T19): returned
// once in the API response, never logged or emailed (R29), and the user must change it at the next
// sign-in (must_change_password, R79). It is never chosen by the caller.
func Temporary() (string, error) {
	b := make([]byte, TemporaryLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = temporaryAlphabet[b[i]&31] // 256 is a multiple of 32: no modulo bias
	}
	return string(b), nil
}
