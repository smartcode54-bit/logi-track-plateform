package auth

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
)

// AUTH_FIREBASE_BRIDGE_MODE (R8, Appendix C §C.6.1): what each mode switches on.
func TestBridgeModes(t *testing.T) {
	cases := map[string]struct{ web, mobile, mirror bool }{
		"off": {}, "": {}, "mobile": {mobile: true, mirror: true}, "web": {web: true, mirror: true},
		"both": {web: true, mobile: true, mirror: true},
	}
	for in, want := range cases {
		m, ok := ParseBridgeMode(in)
		if !ok || m.Web() != want.web || m.Mobile() != want.mobile || m.Mirror() != want.mirror {
			t.Fatalf("%q -> %q ok=%v web=%v mobile=%v mirror=%v", in, m, ok, m.Web(), m.Mobile(), m.Mirror())
		}
	}
	for _, bad := range []string{"OFF", "Web", "true", "on", "web,mobile"} {
		if _, ok := ParseBridgeMode(bad); ok {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// The collaborators each mode needs are checked when the service is built, never at request time.
func TestFirebaseValidate(t *testing.T) {
	v, s, a := &firebase.Verifier{}, &firebase.ServiceAccount{}, &firebase.Accounts{}
	ok := []Firebase{
		{}, {Mode: BridgeOff, Verifier: v},
		{Mode: BridgeWeb, Signer: s, Accounts: a},
		{Mode: BridgeMobile, Verifier: v, Accounts: a},
		{Mode: BridgeBoth, Verifier: v, Signer: s, Accounts: a},
	}
	for _, f := range ok {
		if err := f.validate(); err != nil {
			t.Fatalf("%+v: %v", f, err)
		}
	}
	bad := []Firebase{
		{Mode: "sometimes"},
		{Mode: BridgeWeb, Accounts: a},
		{Mode: BridgeWeb, Signer: s},
		{Mode: BridgeMobile, Accounts: a},
		{Mode: BridgeMobile, Verifier: v},
		{Mode: BridgeBoth, Signer: s, Accounts: a},
	}
	for _, f := range bad {
		if err := f.validate(); err == nil {
			t.Fatalf("%+v accepted", f)
		}
	}
}

// The mirror owns exactly the legacy claim keys and keeps every other attribute (C.6.4).
func TestMergeAttributes(t *testing.T) {
	cases := []struct {
		existing string
		claims   map[string]any
		want     string
	}{
		{"", map[string]any{"admin": true, "role": "admin"}, `{"admin":true,"role":"admin"}`},
		{`{"role":"driver","driverId":"d1","companyId":"c","tier":2}`, map[string]any{"admin": false, "role": "manager"},
			`{"admin":false,"companyId":"c","role":"manager","tier":2}`},
		{`{"admin":true,"role":"admin","partnerScopeId":"p","customerScopeId":"c"}`, nil, `{}`},
		{`not json`, map[string]any{"role": "user", "admin": false}, `{"admin":false,"role":"user"}`},
	}
	for _, tc := range cases {
		got, err := mergeAttributes(tc.existing, tc.claims)
		if err != nil || got != tc.want {
			t.Fatalf("merge(%s, %v) = %s %v, want %s", tc.existing, tc.claims, got, err, tc.want)
		}
	}
	if _, err := mergeAttributes(`{"blob":"`+strings.Repeat("x", 990)+`"}`, map[string]any{"role": "admin"}); err == nil {
		t.Fatal("oversized attributes accepted")
	}
}

// The active context of a session decides the claims of its custom token (C.6.3).
func TestPrincipalContext(t *testing.T) {
	tid, party := uuid.New(), uuid.New()
	lc := principalContext(&authz.Principal{TenantID: &tid, TenantRole: authz.Manager, PartyIDs: []uuid.UUID{party}})
	if lc.tenantID == nil || *lc.tenantID != tid || lc.role != authz.Manager || lc.customerScope || lc.platformAdmin {
		t.Fatalf("tenant context = %+v", lc)
	}
	lc = principalContext(&authz.Principal{PartyIDs: []uuid.UUID{party}})
	if !lc.customerScope || lc.tenantID != nil {
		t.Fatalf("customer context = %+v", lc)
	}
	lc = principalContext(&authz.Principal{Platform: []authz.PlatformRole{authz.Support}, Dispatcher: true})
	if lc.platformAdmin || !lc.dispatcher {
		t.Fatalf("support dispatcher context = %+v", lc)
	}
}
