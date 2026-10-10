package firebase_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase/firebasetest"
)

const project = "logitrack-bridge-test"

func editJSON(t *testing.T, raw []byte, edit func(map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParseServiceAccount(t *testing.T) {
	b := firebasetest.New(t, project)
	good := b.ServiceAccountJSON()
	sa, err := firebase.ParseServiceAccount(good)
	if err != nil {
		t.Fatal(err)
	}
	if sa.ProjectID() != project || !strings.HasSuffix(sa.ClientEmail(), ".iam.gserviceaccount.com") {
		t.Fatalf("parsed %s %s", sa.ProjectID(), sa.ClientEmail())
	}
	if s := fmt.Sprintf("%v %+v %#v %s", sa, sa, sa, sa); strings.Contains(s, "PRIVATE") || strings.Contains(s, sa.ClientEmail()) {
		t.Fatalf("formatting a service account prints its fields: %s", s)
	}

	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(small)
	smallPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(firebasetest.NewKey(t))}))

	cases := map[string]struct {
		edit func(map[string]any)
		want string
	}{
		"not a service account": {func(m map[string]any) { m["type"] = "authorized_user" }, "not a service-account key"},
		"no client_email":       {func(m map[string]any) { delete(m, "client_email") }, "client_email"},
		"named client_email":    {func(m map[string]any) { m["client_email"] = "Bridge <b@x.test>" }, "client_email"},
		"no private key":        {func(m map[string]any) { delete(m, "private_key") }, "private_key"},
		"garbage private key": {func(m map[string]any) {
			m["private_key"] = "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"
		}, "private_key"},
		"short key":      {func(m map[string]any) { m["private_key"] = smallPEM }, "2048"},
		"http token uri": {func(m map[string]any) { m["token_uri"] = "http://oauth2.googleapis.com/token" }, "token_uri"},
		"bad project id": {func(m map[string]any) { m["project_id"] = "Not_A_Project" }, "project_id"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			raw := editJSON(t, good, tc.edit)
			_, err := firebase.ParseServiceAccount(raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error naming %q, got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), "MII") || strings.Contains(err.Error(), "@") {
				t.Fatalf("error carries file content: %v", err)
			}
		})
	}
	if _, err := firebase.ParseServiceAccount([]byte("{not json")); err == nil {
		t.Fatal("garbage accepted")
	}
	// PKCS #1 keys load too; a missing token_uri means Google's endpoint.
	raw := editJSON(t, good, func(m map[string]any) { m["private_key"] = pkcs1; delete(m, "token_uri") })
	if _, err := firebase.ParseServiceAccount(raw); err != nil {
		t.Fatalf("PKCS #1 key: %v", err)
	}
}

func TestLoadServiceAccount(t *testing.T) {
	b := firebasetest.New(t, project)
	dir := t.TempDir()
	path := filepath.Join(dir, "sa.json")
	if err := os.WriteFile(path, b.ServiceAccountJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := firebase.LoadServiceAccount(path); err != nil {
		t.Fatal(err)
	}
	if _, err := firebase.LoadServiceAccount(filepath.Join(dir, "missing.json")); err == nil ||
		!strings.Contains(err.Error(), "GOOGLE_APPLICATION_CREDENTIALS") || strings.Contains(err.Error(), dir) {
		t.Fatalf("missing file: %v", err)
	}
	big := filepath.Join(dir, "big.json")
	if err := os.WriteFile(big, make([]byte, 70<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := firebase.LoadServiceAccount(big); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized file: %v", err)
	}
}

func TestCustomToken(t *testing.T) {
	b := firebasetest.New(t, project)
	sa := b.ServiceAccount()
	now := time.Unix(1_790_000_000, 0)
	claims := map[string]any{"admin": false, "role": "partner", "partnerScopeId": "sub-doc-1"}
	tok, err := sa.CustomToken("legacy-uid-1", claims, now)
	if err != nil {
		t.Fatal(err)
	}
	header, c := firebasetest.ParseCustomToken(t, sa, tok, now)
	if len(header) != 2 || header["alg"] != "RS256" || header["typ"] != "JWT" {
		t.Fatalf("header = %v (the Admin SDK sends exactly alg and typ)", header)
	}
	var keys []string
	for k := range c {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if strings.Join(keys, ",") != "aud,claims,exp,iat,iss,sub,uid" {
		t.Fatalf("claims = %v", keys)
	}
	if c["iss"] != sa.ClientEmail() || c["sub"] != sa.ClientEmail() || c["aud"] != firebase.CustomTokenAudience ||
		c["uid"] != "legacy-uid-1" || c["iat"].(float64) != float64(now.Unix()) || c["exp"].(float64) != float64(now.Unix()+3600) {
		t.Fatalf("claims = %v", c)
	}
	dev := c["claims"].(map[string]any)
	if len(dev) != 3 || dev["role"] != "partner" || dev["admin"] != false || dev["partnerScopeId"] != "sub-doc-1" {
		t.Fatalf("developer claims = %v", dev)
	}

	// No developer claims: the claims member is left out.
	tok, err = sa.CustomToken("u", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, c := firebasetest.ParseCustomToken(t, sa, tok, now); c["claims"] != nil {
		t.Fatalf("empty claims sent: %v", c)
	}

	for name, tc := range map[string]struct {
		uid    string
		claims map[string]any
	}{
		"empty uid":      {"", nil},
		"long uid":       {strings.Repeat("u", 129), nil},
		"reserved claim": {"u", map[string]any{"firebase": "x"}},
		"oversized":      {"u", map[string]any{"role": strings.Repeat("x", 1000)}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := sa.CustomToken(tc.uid, tc.claims, now); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestVerifier(t *testing.T) {
	b := firebasetest.New(t, project)
	now := time.Unix(1_790_000_000, 0)
	clock := func() time.Time { return now }
	v := b.Verifier(clock)
	ctx := context.Background()
	if b.KeyRequests() != 0 {
		t.Fatal("NewVerifier fetched keys")
	}

	auth := now.Add(-2 * time.Hour)
	good := b.IDTokenClaims("fb-uid-1", auth, now.Add(-time.Minute))
	got, err := v.Verify(ctx, b.SignIDToken(good))
	if err != nil {
		t.Fatal(err)
	}
	if got.UID != "fb-uid-1" || !got.AuthTime.Equal(auth.Truncate(time.Second)) || got.SignInProvider != "password" ||
		got.Email != "fb-uid-1@example.test" {
		t.Fatalf("token = %+v", got)
	}

	edit := func(f func(map[string]any)) string {
		c := b.IDTokenClaims("fb-uid-1", auth, now.Add(-time.Minute))
		f(c)
		return b.SignIDToken(c)
	}
	cases := map[string]struct {
		raw    string
		reason string
	}{
		"other project audience": {edit(func(c map[string]any) { c["aud"] = "other-project-01" }), firebase.ReasonInvalid},
		"other issuer":           {edit(func(c map[string]any) { c["iss"] = "https://securetoken.google.com/other-project-01" }), firebase.ReasonInvalid},
		"google issuer":          {edit(func(c map[string]any) { c["iss"] = "https://accounts.google.com" }), firebase.ReasonInvalid},
		"expired":                {edit(func(c map[string]any) { c["exp"] = now.Add(-time.Second).Unix() }), firebase.ReasonExpired},
		"iat in the future":      {edit(func(c map[string]any) { c["iat"] = now.Add(10 * time.Minute).Unix() }), firebase.ReasonClaims},
		"no iat":                 {edit(func(c map[string]any) { delete(c, "iat") }), firebase.ReasonClaims},
		"no auth_time":           {edit(func(c map[string]any) { delete(c, "auth_time") }), firebase.ReasonClaims},
		"auth_time ahead":        {edit(func(c map[string]any) { c["auth_time"] = now.Add(10 * time.Minute).Unix() }), firebase.ReasonClaims},
		"empty sub":              {edit(func(c map[string]any) { c["sub"] = "" }), firebase.ReasonClaims},
		"long sub":               {edit(func(c map[string]any) { c["sub"] = strings.Repeat("s", 129) }), firebase.ReasonClaims},
		"tenant token": {edit(func(c map[string]any) {
			c["firebase"] = map[string]any{"sign_in_provider": "password", "tenant": "tenant-1"}
		}), firebase.ReasonClaims},
		"unpublished key": {firebasetest.SignWith(t, firebasetest.NewKey(t), firebasetest.KeyID, "RS256", good), firebase.ReasonInvalid},
		"unsigned":        {unsigned(t, good), firebase.ReasonInvalid},
		"garbage":         {"not.a.jwt", firebase.ReasonInvalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := v.Verify(ctx, tc.raw)
			ie, ok := errors.AsType[*firebase.InvalidError](err)
			if !ok || ie.Reason != tc.reason {
				t.Fatalf("want InvalidError %s, got %v", tc.reason, err)
			}
			if errors.Is(err, firebase.ErrUnavailable) {
				t.Fatal("a judged token reported as unavailable")
			}
		})
	}

	// A custom token is not an ID token.
	ct, err := b.ServiceAccount().CustomToken("fb-uid-1", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(ctx, ct); err == nil {
		t.Fatal("custom token accepted as an ID token")
	}
}

func unsigned(t *testing.T, claims map[string]any) string {
	t.Helper()
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + enc(b) + "."
}

// TestVerifierKeysUnavailable pins the go-oidc behaviour the probe relies on: a key set that cannot be
// fetched is ErrUnavailable, never an InvalidError blaming the token.
func TestVerifierKeysUnavailable(t *testing.T) {
	b := firebasetest.New(t, project)
	now := time.Now()
	raw := b.SignIDToken(b.IDTokenClaims("fb-uid-1", now.Add(-time.Hour), now))
	for name, set := range map[string]func(bool){"network down": b.SetDown, "keys 500": b.SetKeysFailing} {
		t.Run(name, func(t *testing.T) {
			v := b.Verifier(nil)
			set(true)
			_, err := v.Verify(context.Background(), raw)
			set(false)
			if !errors.Is(err, firebase.ErrUnavailable) {
				t.Fatalf("want ErrUnavailable, got %v", err)
			}
			if _, err := v.Verify(context.Background(), raw); err != nil {
				t.Fatalf("after recovery: %v", err)
			}
		})
	}
}

func TestNewVerifierProjectID(t *testing.T) {
	for _, id := range []string{"", "ab", "Upper-case", "ends-with-", "1starts-digit"} {
		if _, err := firebase.NewVerifier(firebase.VerifierConfig{ProjectID: id}); err == nil {
			t.Fatalf("project id %q accepted", id)
		}
	}
	if !firebase.ValidProjectID("logitrack-dev") {
		t.Fatal("logitrack-dev refused")
	}
}

func TestAccounts(t *testing.T) {
	b := firebasetest.New(t, project)
	a := b.Accounts()
	ctx := context.Background()
	b.Put(firebasetest.Account{UID: "fb-1", Email: "one@example.test", Password: "old-password", CustomAttributes: `{"role":"driver","note":"keep"}`})

	acc, err := a.Lookup(ctx, "fb-1")
	if err != nil {
		t.Fatal(err)
	}
	if acc.UID != "fb-1" || acc.Email != "one@example.test" || acc.Disabled || acc.CustomAttributes != `{"role":"driver","note":"keep"}` || !acc.ValidSince.IsZero() {
		t.Fatalf("lookup = %+v", acc)
	}
	if _, err := a.Lookup(ctx, "missing"); !errors.Is(err, firebase.ErrUserNotFound) {
		t.Fatalf("unknown uid: %v", err)
	}

	pw, off, at, attrs := "new-password-1", true, time.Unix(1_790_000_123, 0), `{"admin":false}`
	if err := a.Update(ctx, "fb-1", firebase.Update{Password: &pw, Disabled: &off, RevokeBefore: &at, CustomAttributes: &attrs}); err != nil {
		t.Fatal(err)
	}
	got, _ := b.Get("fb-1")
	if got.Password != pw || !got.Disabled || got.ValidSince != at.Unix() || got.CustomAttributes != attrs {
		t.Fatalf("after update: %+v", got)
	}
	calls := b.Calls()
	last := calls[len(calls)-1]
	if last.Method != "update" || last.Body["validSince"] != "1790000123" || last.Body["disableUser"] != true || last.Body["localId"] != "fb-1" {
		t.Fatalf("update body = %v (validSince is a string of seconds, as the Admin SDK sends it)", last.Body)
	}
	// Enabling sends disableUser:false explicitly.
	on := false
	if err := a.Update(ctx, "fb-1", firebase.Update{Disabled: &on}); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Get("fb-1"); got.Disabled {
		t.Fatal("not re-enabled")
	}
	if v, ok := b.Calls()[len(b.Calls())-1].Body["disableUser"]; !ok || v != false {
		t.Fatal("disableUser:false not sent")
	}
	// An empty update sends nothing.
	n := len(b.Calls())
	if err := a.Update(ctx, "fb-1", firebase.Update{}); err != nil || len(b.Calls()) != n {
		t.Fatalf("empty update: %v, %d calls", err, len(b.Calls())-n)
	}
	if err := a.Update(ctx, "missing", firebase.Update{Disabled: &off}); !errors.Is(err, firebase.ErrUserNotFound) {
		t.Fatalf("update of an unknown uid: %v", err)
	}
	bad := `{"firebase":1}`
	if err := a.Update(ctx, "fb-1", firebase.Update{CustomAttributes: &bad}); err == nil {
		t.Fatal("reserved attribute sent")
	}

	if err := a.Create(ctx, firebase.NewAccount{UID: "fb-2", Email: "two@example.test", Password: "a-password-2", Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if got, ok := b.Get("fb-2"); !ok || got.Password != "a-password-2" || !got.Disabled {
		t.Fatalf("created: %+v", got)
	}
	if err := a.Create(ctx, firebase.NewAccount{UID: "fb-2"}); !errors.Is(err, firebase.ErrUIDExists) {
		t.Fatalf("duplicate uid: %v", err)
	}
	if err := a.Create(ctx, firebase.NewAccount{UID: "fb-3", Email: "TWO@example.test"}); !errors.Is(err, firebase.ErrEmailExists) {
		t.Fatalf("duplicate email: %v", err)
	}

	// The holder of an email (GetUserByEmail's wire format), case-insensitively.
	held, err := a.LookupEmail(ctx, "Two@Example.test")
	if err != nil || held.UID != "fb-2" || !held.Disabled {
		t.Fatalf("lookup by email = %+v, %v", held, err)
	}
	if last := b.Calls()[len(b.Calls())-1]; last.Method != "lookup" || len(last.Body) != 1 || last.Body["email"] == nil {
		t.Fatalf("lookup-by-email body = %v", last.Body)
	}
	if _, err := a.LookupEmail(ctx, "nobody@example.test"); !errors.Is(err, firebase.ErrUserNotFound) {
		t.Fatalf("unheld email: %v", err)
	}

	if err := a.Delete(ctx, "fb-2"); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Get("fb-2"); ok {
		t.Fatal("not deleted")
	}
	if last := b.Calls()[len(b.Calls())-1]; last.Method != "delete" || last.Body["localId"] != "fb-2" || len(last.Body) != 1 {
		t.Fatalf("delete body = %v", last.Body)
	}
	if err := a.Delete(ctx, "fb-2"); !errors.Is(err, firebase.ErrUserNotFound) {
		t.Fatalf("delete of an unknown uid: %v", err)
	}
	if b.TokenRequests() != 1 {
		t.Fatalf("token requests = %d: the access token is reused", b.TokenRequests())
	}
}

func TestAccountsFailures(t *testing.T) {
	b := firebasetest.New(t, project)
	a := b.Accounts()
	ctx := context.Background()
	b.Put(firebasetest.Account{UID: "fb-1"})
	pw := "secret-password-xyz"

	b.SetToolkitError(http.StatusInternalServerError, "INTERNAL_ERROR")
	err := a.Update(ctx, "fb-1", firebase.Update{Password: &pw})
	if !errors.Is(err, firebase.ErrUnavailable) {
		t.Fatalf("500: %v", err)
	}
	if strings.Contains(err.Error(), pw) || strings.Contains(err.Error(), "detail that must not leak") {
		t.Fatalf("error leaks request or response text: %v", err)
	}
	b.SetToolkitError(http.StatusBadRequest, "WEAK_PASSWORD")
	err = a.Update(ctx, "fb-1", firebase.Update{Password: &pw})
	if ae, ok := errors.AsType[*firebase.APIError](err); !ok || ae.Code != "WEAK_PASSWORD" || errors.Is(err, firebase.ErrUnavailable) {
		t.Fatalf("400: %v", err)
	}
	b.SetToolkitError(0, "")

	b.SetDown(true)
	err = a.Update(ctx, "fb-1", firebase.Update{Password: &pw})
	b.SetDown(false)
	if !errors.Is(err, firebase.ErrUnavailable) || strings.Contains(err.Error(), pw) {
		t.Fatalf("network down: %v", err)
	}
	if err := a.Update(ctx, "fb-1", firebase.Update{Password: &pw}); err != nil {
		t.Fatalf("after recovery: %v", err)
	}
	if _, err := firebase.NewAccounts(firebase.AccountsConfig{ProjectID: project}); err == nil {
		t.Fatal("no credentials accepted")
	}
}
