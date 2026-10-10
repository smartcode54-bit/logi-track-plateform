package auth

import (
	"strings"
	"testing"
)

// The request rules of POST /v1/auth/google (Appendix C §C.4.10): the web must send a nonce, the driver
// app may omit it, a nonce that cannot be one of ours is refused before any lookup, and Google sign-in
// has no script platform.
func TestValidateGoogle(t *testing.T) {
	n := strings.Repeat("A", 43) // the shape of a nonce: 32 bytes, base64url
	cases := map[string]struct {
		in    GoogleInput
		field string // "" = valid
		why   string
	}{
		"web with nonce":         {GoogleInput{IDToken: "t", Nonce: n, Platform: PlatformWeb}, "", ""},
		"android without nonce":  {GoogleInput{IDToken: "t", Platform: PlatformAndroid, InstallID: "i"}, "", ""},
		"ios with nonce":         {GoogleInput{IDToken: "t", Nonce: n, Platform: PlatformIOS}, "", ""},
		"web without nonce":      {GoogleInput{IDToken: "t", Platform: PlatformWeb}, "nonce", "required"},
		"malformed nonce":        {GoogleInput{IDToken: "t", Nonce: "abc", Platform: PlatformAndroid}, "nonce", "invalid"},
		"no idToken":             {GoogleInput{Platform: PlatformAndroid}, "idToken", "required"},
		"oversized idToken":      {GoogleInput{IDToken: strings.Repeat("x", maxIDTokenBytes+1), Platform: PlatformAndroid}, "idToken", "too_long"},
		"script platform":        {GoogleInput{IDToken: "t", Platform: PlatformScript}, "platform", "one_of"},
		"no platform":            {GoogleInput{IDToken: "t"}, "platform", "one_of"},
		"oversized installId":    {GoogleInput{IDToken: "t", Platform: PlatformAndroid, InstallID: strings.Repeat("i", 201)}, "installId", "too_long"},
		"oversized appVersion":   {GoogleInput{IDToken: "t", Platform: PlatformAndroid, AppVersion: strings.Repeat("9", 65)}, "appVersion", "too_long"},
		"invalid UTF-8 install":  {GoogleInput{IDToken: "t", Platform: PlatformAndroid, InstallID: "\xff"}, "installId", "too_long"},
		"nonce not base64url":    {GoogleInput{IDToken: "t", Nonce: strings.Repeat("+", 43), Platform: PlatformWeb}, "nonce", "invalid"},
		"nonce of another shape": {GoogleInput{IDToken: "t", Nonce: n + "A", Platform: PlatformWeb}, "nonce", "invalid"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			v := validateGoogle(&tc.in)
			if tc.field == "" {
				if len(v) != 0 {
					t.Fatalf("want valid, got %+v", v)
				}
				return
			}
			if len(v) != 1 || v[0].Field != tc.field || v[0].Reason != tc.why {
				t.Fatalf("want %s/%s, got %+v", tc.field, tc.why, v)
			}
		})
	}
}
