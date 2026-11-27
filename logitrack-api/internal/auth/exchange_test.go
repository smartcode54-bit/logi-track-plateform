package auth

import (
	"strings"
	"testing"
)

// The request rules of POST /v1/auth/exchange (spec mobile-auth-exchange R3): the driver app only, with its
// install id.
func TestValidateExchange(t *testing.T) {
	cases := map[string]struct {
		in    ExchangeInput
		field string // "" = valid
		why   string
	}{
		"android":               {ExchangeInput{IDToken: "t", Platform: PlatformAndroid, InstallID: "i"}, "", ""},
		"ios with version":      {ExchangeInput{IDToken: "t", Platform: PlatformIOS, InstallID: "i", AppVersion: "4.0.0"}, "", ""},
		"web":                   {ExchangeInput{IDToken: "t", Platform: PlatformWeb, InstallID: "i"}, "platform", "one_of"},
		"script":                {ExchangeInput{IDToken: "t", Platform: PlatformScript, InstallID: "i"}, "platform", "one_of"},
		"no platform":           {ExchangeInput{IDToken: "t", InstallID: "i"}, "platform", "one_of"},
		"no idToken":            {ExchangeInput{Platform: PlatformAndroid, InstallID: "i"}, "idToken", "required"},
		"oversized idToken":     {ExchangeInput{IDToken: strings.Repeat("x", maxIDTokenBytes+1), Platform: PlatformAndroid, InstallID: "i"}, "idToken", "too_long"},
		"no installId":          {ExchangeInput{IDToken: "t", Platform: PlatformAndroid}, "installId", "required"},
		"oversized installId":   {ExchangeInput{IDToken: "t", Platform: PlatformAndroid, InstallID: strings.Repeat("i", 201)}, "installId", "too_long"},
		"invalid UTF-8 install": {ExchangeInput{IDToken: "t", Platform: PlatformAndroid, InstallID: "\xff"}, "installId", "too_long"},
		"oversized appVersion":  {ExchangeInput{IDToken: "t", Platform: PlatformAndroid, InstallID: "i", AppVersion: strings.Repeat("9", 65)}, "appVersion", "too_long"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			v := validateExchange(&tc.in)
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

// firebase.sign_in_provider -> amr (R6): only the two providers APK 3.x uses; anything else is refused.
func TestAMRForProvider(t *testing.T) {
	for p, want := range map[string]string{
		"password": AMRPassword, "google.com": AMRGoogle,
		"custom": "", "anonymous": "", "phone": "", "": "", "Password": "",
	} {
		if got := amrForProvider(p); got != want {
			t.Errorf("amrForProvider(%q) = %q, want %q", p, got, want)
		}
	}
}
