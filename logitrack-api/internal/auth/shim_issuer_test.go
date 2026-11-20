package auth

import (
	"encoding/base64"
	"testing"
)

// RequireShimOrAuth answers a keyless Firebase ID token 401 unauthenticated, as the shim path always did (T55): the
// unverified issuer is what tells it apart from a Go access token.
func TestFirebaseIssued(t *testing.T) {
	jwt := func(payload string) string {
		enc := base64.RawURLEncoding.EncodeToString
		return enc([]byte(`{"alg":"RS256"}`)) + "." + enc([]byte(payload)) + ".sig"
	}
	for _, tc := range []struct {
		name, raw string
		want      bool
	}{
		{"firebase", jwt(`{"iss":"https://securetoken.google.com/logi-track-wrt-dev"}`), true},
		{"go token", jwt(`{"iss":"https://logi-api.showkhun.co"}`), false},
		{"no iss", jwt(`{"sub":"x"}`), false},
		{"not a jwt", "abc", false},
		{"bad base64", "a.!!!.c", false},
		{"bad json", jwt(`{`), false},
	} {
		if got := firebaseIssued(tc.raw); got != tc.want {
			t.Errorf("%s: firebaseIssued = %v, want %v", tc.name, got, tc.want)
		}
	}
}
