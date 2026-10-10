package google_test

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/google"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/google/googletest"
)

// The web GIS client and the OAuth client the installed APKs pass as serverClientId (both must be in
// GOOGLE_OIDC_ALLOWED_CLIENT_IDS). Placeholders in the Google client-id shape, not real clients.
const (
	webClient = "100000000001-webtest.apps.googleusercontent.com"
	apkClient = "100000000002-apktest.apps.googleusercontent.com"
	other     = "100000000003-other.apps.googleusercontent.com"
)

type clock struct{ offset atomic.Int64 }

func (c *clock) Now() time.Time          { return time.Now().Add(time.Duration(c.offset.Load())) }
func (c *clock) Advance(d time.Duration) { c.offset.Add(int64(d)) }

func reason(t *testing.T, err error) string {
	t.Helper()
	ie, ok := errors.AsType[*google.InvalidError](err)
	if !ok {
		t.Fatalf("want *google.InvalidError, got %v", err)
	}
	if errors.Is(err, google.ErrUnavailable) {
		t.Fatalf("an invalid token must not read as unavailable: %v", err)
	}
	return ie.Reason
}

// Appendix C §C.4.10 / §C.9.3: signature, issuer, exp, aud in the allow list, email_verified; the
// tokens are signed with a local RSA key whose JWKS is served in-process (no network).
func TestVerify(t *testing.T) {
	ctx := context.Background()
	p := googletest.New(t)
	c := &clock{}
	v := p.Verifier(c.Now, webClient, apkClient)
	now := time.Now()
	valid := func() map[string]any {
		return googletest.Claims(webClient, "110000000000000000001", "Somchai@Example.COM", now)
	}

	id, err := v.Verify(ctx, p.Sign(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "110000000000000000001" || id.Email != "somchai@example.com" || id.ClientID != webClient || id.Nonce != "" ||
		id.AuthorizedParty != webClient || id.HostedDomain != "" || id.NativeApp() {
		t.Fatalf("identity = %+v", id)
	}

	withNonce := valid()
	withNonce["nonce"] = "n-123"
	if id, err := v.Verify(ctx, p.Sign(withNonce)); err != nil || id.Nonce != "n-123" {
		t.Fatalf("nonce claim: %+v %v", id, err)
	}

	// The driver app's token: aud = the serverClientId, azp = the Android client.
	apk := googletest.Claims(apkClient, "110000000000000000002", "driver@example.com", now)
	apk["azp"] = "100000000009-android.apps.googleusercontent.com"
	if id, err := v.Verify(ctx, p.Sign(apk)); err != nil {
		t.Fatalf("apk token: %v", err)
	} else if !id.NativeApp() || id.AuthorizedParty != "100000000009-android.apps.googleusercontent.com" {
		t.Fatalf("apk identity = %+v", id)
	}

	// A Workspace account: hd is read and lower-cased.
	workspace := valid()
	workspace["hd"] = "Carrier.CO.TH"
	if id, err := v.Verify(ctx, p.Sign(workspace)); err != nil || id.HostedDomain != "carrier.co.th" || !id.EmailAuthoritative() {
		t.Fatalf("hd claim: %+v %v", id, err)
	}

	// Only ASCII letters are lower-cased: U+212A KELVIN SIGN must not become "k".
	kelvin := valid()
	kelvin["email"] = "\u212Aelvin@Gmail.com"
	if id, err := v.Verify(ctx, p.Sign(kelvin)); err != nil || id.Email != "\u212Aelvin@gmail.com" || id.EmailAuthoritative() {
		t.Fatalf("non-ASCII email: %+v %v", id, err)
	}

	// Google sometimes spells iss without the scheme; go-oidc accepts it for Google only.
	noScheme := valid()
	noScheme["iss"] = "accounts.google.com"
	if _, err := v.Verify(ctx, p.Sign(noScheme)); err != nil {
		t.Fatalf("iss accounts.google.com: %v", err)
	}

	// Some Google tokens carry email_verified as a string.
	stringVerified := valid()
	stringVerified["email_verified"] = "true"
	if _, err := v.Verify(ctx, p.Sign(stringVerified)); err != nil {
		t.Fatalf("email_verified \"true\": %v", err)
	}

	edit := func(f func(m map[string]any)) map[string]any {
		m := valid()
		f(m)
		return m
	}
	otherKey := googletest.NewKey(t)
	cases := map[string]struct {
		token  string
		reason string
	}{
		"aud outside the list":        {p.Sign(edit(func(m map[string]any) { m["aud"] = other })), google.ReasonAudience},
		"one aud outside the list":    {p.Sign(edit(func(m map[string]any) { m["aud"] = []string{webClient, other} })), google.ReasonAudience},
		"email_verified false":        {p.Sign(edit(func(m map[string]any) { m["email_verified"] = false })), google.ReasonEmailUnverified},
		"email_verified \"false\"":    {p.Sign(edit(func(m map[string]any) { m["email_verified"] = "false" })), google.ReasonEmailUnverified},
		"email_verified missing":      {p.Sign(edit(func(m map[string]any) { delete(m, "email_verified") })), google.ReasonEmailUnverified},
		"email missing":               {p.Sign(edit(func(m map[string]any) { delete(m, "email") })), google.ReasonClaims},
		"sub missing":                 {p.Sign(edit(func(m map[string]any) { delete(m, "sub") })), google.ReasonClaims},
		"expired":                     {p.Sign(edit(func(m map[string]any) { m["exp"] = now.Add(-time.Minute).Unix() })), google.ReasonExpired},
		"not yet valid":               {p.Sign(edit(func(m map[string]any) { m["nbf"] = now.Add(time.Hour).Unix() })), google.ReasonExpired},
		"other issuer":                {p.Sign(edit(func(m map[string]any) { m["iss"] = "https://securetoken.google.com/x" })), google.ReasonInvalid},
		"signed by an unknown key":    {googletest.SignWith(t, otherKey, googletest.KeyID, "RS256", valid()), google.ReasonInvalid},
		"unknown kid":                 {googletest.SignWith(t, otherKey, "rotated-away", "RS256", valid()), google.ReasonInvalid},
		"HS256 with the key id":       {googletest.SignWith(t, []byte("0123456789abcdef0123456789abcdef"), googletest.KeyID, "HS256", valid()), google.ReasonInvalid},
		"alg none":                    {googletest.Unsigned(t, valid()), google.ReasonInvalid},
		"not a JWT":                   {"not-a-jwt", google.ReasonInvalid},
		"payload tampered after sign": {tamper(p.Sign(valid())), google.ReasonInvalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := v.Verify(ctx, tc.token)
			if got := reason(t, err); got != tc.reason {
				t.Fatalf("reason = %s, want %s (%v)", got, tc.reason, err)
			}
		})
	}

	// exp is judged on the verifier's clock.
	tok := p.Sign(valid())
	c.Advance(2 * time.Hour)
	if got := reason(t, func() error { _, err := v.Verify(ctx, tok); return err }()); got != google.ReasonExpired {
		t.Fatalf("after the clock moved: %s", got)
	}
}

// Google is authoritative for an email only for Gmail addresses and Workspace accounts (hd); for any
// other address email_verified says nothing about who holds the mailbox now.
func TestEmailAuthoritative(t *testing.T) {
	cases := map[string]struct {
		email, hd string
		want      bool
	}{
		"gmail":                         {"somchai@gmail.com", "", true},
		"googlemail":                    {"somchai@googlemail.com", "", true},
		"workspace":                     {"dispatch@carrier.co.th", "carrier.co.th", true},
		"workspace secondary domain":    {"dispatch@carrier-logistics.com", "carrier.co.th", true},
		"consumer account, other email": {"dispatch@carrier.co.th", "", false},
		"gmail look-alike subdomain":    {"x@mail.gmail.com", "", false},
		"gmail as a local part":         {"gmail.com@carrier.co.th", "", false},
		"non-ASCII local part":          {"\u212Aelvin@gmail.com", "", false},
		"non-ASCII with hd":             {"\u212Aelvin@carrier.co.th", "carrier.co.th", false},
		"no domain":                     {"somchai@", "", false},
		"no local part":                 {"@gmail.com", "", false},
		"empty":                         {"", "", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			id := &google.Identity{Email: tc.email, HostedDomain: tc.hd}
			if got := id.EmailAuthoritative(); got != tc.want {
				t.Fatalf("EmailAuthoritative(%q, hd %q) = %v", tc.email, tc.hd, got)
			}
		})
	}
}

// NativeApp: a token requested by a client other than its audience (the driver app's google_sign_in);
// a GIS token has azp == aud, and a token without azp is not native.
func TestNativeApp(t *testing.T) {
	const ios = "100000000010-ios.apps.googleusercontent.com"
	cases := map[string]struct {
		id   google.Identity
		want bool
	}{
		"ios":              {google.Identity{ClientID: apkClient, Audience: []string{apkClient}, AuthorizedParty: ios}, true},
		"gis":              {google.Identity{ClientID: webClient, Audience: []string{webClient}, AuthorizedParty: webClient}, false},
		"no azp":           {google.Identity{ClientID: webClient, Audience: []string{webClient}}, false},
		"azp is a 2nd aud": {google.Identity{ClientID: webClient, Audience: []string{webClient, apkClient}, AuthorizedParty: apkClient}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.id.NativeApp(); got != tc.want {
				t.Fatalf("NativeApp(%+v) = %v", tc.id, got)
			}
		})
	}
}

// tamper swaps the payload of a signed token for another one, keeping header and signature.
func tamper(jwt string) string {
	parts := strings.Split(jwt, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"attacker","email":"x@example.com","email_verified":true}`))
	return strings.Join(parts, ".")
}

// Discovery runs on the first token, not at start-up, and the keys are cached: later tokens with a known
// kid cause no request. Every request goes to accounts.google.com through the in-process transport.
func TestDiscoveryIsLazyAndKeysCached(t *testing.T) {
	ctx := context.Background()
	p := googletest.New(t)
	v := p.Verifier(nil, webClient)
	if n := len(p.Requests()); n != 0 {
		t.Fatalf("New made %d requests", n)
	}
	for i := range 3 {
		if _, err := v.Verify(ctx, p.Sign(googletest.Claims(webClient, "sub-1", "a@example.com", time.Now()))); err != nil {
			t.Fatalf("token %d: %v", i, err)
		}
	}
	if got := p.Requests(); !slices.Equal(got, []string{"/.well-known/openid-configuration", "/keys"}) {
		t.Fatalf("requests = %v", got)
	}
}

// Google unreachable is 503, never a 401 that blames the token: discovery failures back off, key fetch
// failures are reported per token, and both heal once Google answers again.
func TestUnreachableGoogleIsUnavailable(t *testing.T) {
	ctx := context.Background()
	p := googletest.New(t)
	c := &clock{}
	v := p.Verifier(c.Now, webClient)
	tok := func() string { return p.Sign(googletest.Claims(webClient, "sub-1", "a@example.com", c.Now())) }

	p.SetDown(true)
	if _, err := v.Verify(ctx, tok()); !errors.Is(err, google.ErrUnavailable) {
		t.Fatalf("discovery down: %v", err)
	}
	p.SetDown(false)
	before := len(p.Requests())
	if _, err := v.Verify(ctx, tok()); !errors.Is(err, google.ErrUnavailable) {
		t.Fatalf("inside the backoff: %v", err)
	}
	if len(p.Requests()) != before {
		t.Fatal("a discovery inside the backoff window was attempted")
	}
	c.Advance(11 * time.Second)

	p.SetKeysFailing(true)
	if _, err := v.Verify(ctx, tok()); !errors.Is(err, google.ErrUnavailable) {
		t.Fatalf("keys failing: %v", err)
	} else if _, ok := errors.AsType[*google.InvalidError](err); ok {
		t.Fatalf("a key fetch failure must not be an InvalidError: %v", err)
	}
	p.SetKeysFailing(false)
	if _, err := v.Verify(ctx, tok()); err != nil {
		t.Fatalf("after recovery: %v", err)
	}

	// With the keys cached, a token naming an unknown kid refetches; Google being down then is 503.
	p.SetDown(true)
	if _, err := v.Verify(ctx, tok()); err != nil {
		t.Fatalf("cached key must not need the network: %v", err)
	}
	unknown := googletest.SignWith(t, googletest.NewKey(t), "rotated-in", "RS256", googletest.Claims(webClient, "s", "a@example.com", c.Now()))
	if _, err := v.Verify(ctx, unknown); !errors.Is(err, google.ErrUnavailable) {
		t.Fatalf("unknown kid while down: %v", err)
	}
}

func TestNewNeedsAClientID(t *testing.T) {
	for _, ids := range [][]string{nil, {}, {" ", ""}} {
		if _, err := google.New(google.Config{ClientIDs: ids}); err == nil {
			t.Fatalf("New(%q) accepted no client id", ids)
		}
	}
}
