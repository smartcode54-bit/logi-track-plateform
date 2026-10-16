package iam

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestConsentEligibilityUsesLiveCredentialAndGrantFacts(t *testing.T) {
	now := time.Now()
	base := iamdb.GetSupportMembershipAuthorityRow{Role: "tenant_admin", Status: "active", UserStatus: "active", CredentialEligible: true}
	for _, tc := range []struct {
		name, basis string
		mutate      func(*iamdb.GetSupportMembershipAuthorityRow)
	}{
		{"legacy member", "member", func(*iamdb.GetSupportMembershipAuthorityRow) {}},
		{"unratified platform grant", "pending", func(m *iamdb.GetSupportMembershipAuthorityRow) { m.GrantedByPlatform = true }},
		{"ratified grant", "ratified", func(m *iamdb.GetSupportMembershipAuthorityRow) { m.GrantedByPlatform = true; m.RatifiedAt = &now }},
		{"holder completed first onboarding grant", "first_admin_self_chosen", func(m *iamdb.GetSupportMembershipAuthorityRow) {
			m.GrantedByPlatform = true
			m.FirstAdminSelfChosen = true
		}},
		{"disabled admin", "pending", func(m *iamdb.GetSupportMembershipAuthorityRow) { m.UserStatus = "disabled" }},
		{"suspended membership", "pending", func(m *iamdb.GetSupportMembershipAuthorityRow) { m.Status = "suspended" }},
		{"non admin", "pending", func(m *iamdb.GetSupportMembershipAuthorityRow) { m.Role = "manager" }},
		{"platform actor with native membership", "member", func(m *iamdb.GetSupportMembershipAuthorityRow) { m.HasPlatformRole = true }},
		{"platform actor with unratified grant", "pending", func(m *iamdb.GetSupportMembershipAuthorityRow) { m.HasPlatformRole = true; m.GrantedByPlatform = true }},
		{"reset after ratification", "pending", func(m *iamdb.GetSupportMembershipAuthorityRow) {
			m.GrantedByPlatform = true
			m.RatifiedAt = &now
			m.CredentialEligible = false
		}},
		{"reset after bootstrap", "pending", func(m *iamdb.GetSupportMembershipAuthorityRow) {
			m.GrantedByPlatform = true
			m.FirstAdminSelfChosen = true
			m.CredentialEligible = false
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			tc.mutate(&m)
			if got := eligibilityBasis(m); got != tc.basis {
				t.Fatalf("basis=%s want %s", got, tc.basis)
			}
		})
	}
}
func TestSupportCursorBindsActorQueryAndIntegrity(t *testing.T) {
	a := &Admin{cursorKey: []byte(strings.Repeat("k", 32))}
	actor := uuid.New()
	cur := supportCursor{Actor: actor, Query: supportQueryHash("cafe"), Time: time.Now().UTC(), ID: uuid.New()}
	encoded := a.encodeSupportCursor(cur)
	if _, ok := a.decodeSupportCursor(encoded, actor, "ＣＡＦÉ"); !ok {
		t.Fatal("folded query did not round trip")
	}
	for name, in := range map[string]struct {
		token string
		actor uuid.UUID
		q     string
	}{
		"foreign actor": {encoded, uuid.New(), "cafe"}, "different query": {encoded, actor, "other"}, "tampered": {encoded + "x", actor, "cafe"}, "missing signature": {strings.Split(encoded, ".")[0], actor, "cafe"}, "invalid": {"not-a-cursor", actor, "cafe"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := a.decodeSupportCursor(in.token, in.actor, in.q); ok {
				t.Fatal("unbound cursor accepted")
			}
		})
	}
}
func TestConsentVersionsAreCanonicalDecimals(t *testing.T) {
	for _, s := range []string{"0", "1", "9223372036854775807"} {
		if _, ok := decimalVersion(s); !ok {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"", "-1", "+1", "01", " 1", "1.0", "9223372036854775808"} {
		if _, ok := decimalVersion(s); ok {
			t.Fatal(s)
		}
	}
}
func TestConsentRequiresMemberContextBeforeLiveAuthorityCheck(t *testing.T) {
	tenant := uuid.New()
	p := &authz.Principal{UserID: uuid.New(), TenantID: &tenant, TenantRole: authz.TenantAdmin}
	if !consentMember(p, tenant) {
		t.Fatal("ordinary member refused")
	}
	for _, role := range []authz.PlatformRole{authz.PlatformAdmin, authz.Support} {
		copy := *p
		copy.Platform = []authz.PlatformRole{role}
		if !consentMember(&copy, tenant) {
			t.Fatal("platform role erased real member context")
		}
	}
	for _, mutate := range []func(*authz.Principal){func(p *authz.Principal) { p.AMR = authz.AMRAPIKey }, func(p *authz.Principal) { p.TenantRole = authz.SupportReadOnly }, func(p *authz.Principal) { p.TenantID = nil }} {
		copy := *p
		mutate(&copy)
		if consentMember(&copy, tenant) {
			t.Fatal("non-member consent authority accepted")
		}
	}
}
func TestSupportDTOClassificationIsComplete(t *testing.T) {
	if _, err := httpx.SchemaContracts(); err != nil {
		t.Fatal(err)
	}
}

func TestSupportOptionsRejectsBoundsBeforeDatabaseAccess(t *testing.T) {
	a := &Admin{}
	c := call{p: &authz.Principal{UserID: uuid.New(), Platform: []authz.PlatformRole{authz.PlatformAdmin}}}
	for _, in := range []struct {
		q     string
		limit int
	}{{"", -1}, {"", 101}, {strings.Repeat("ก", 101), 50}, {"bad\x00query", 50}} {
		if _, _, err := a.SupportOptions(c, in.q, in.limit, ""); err == nil {
			t.Fatalf("accepted %+v", in)
		}
	}
}
func TestSupportServiceGuardsPrecedeDatabaseAndPasswordWork(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	p := &authz.Principal{UserID: uuid.New(), TenantID: &tenant, TenantRole: authz.SupportReadOnly, Platform: []authz.PlatformRole{authz.PlatformAdmin}, Caps: authz.NewCapSet(authz.UsersManage, authz.UsersAssignRole, authz.DriversEdit, authz.DriversSetPassword)}
	a := &Admin{}
	c := call{p: p}
	actions := map[string]func() error{
		"create":             func() error { _, err := a.CreateUser(c, CreateUserInput{}); return err },
		"grant":              func() error { _, err := a.SetMember(c, tenant, user, SetMemberInput{}); return err },
		"add":                func() error { _, err := a.AddMember(c, tenant, AddMemberInput{}); return err },
		"remove":             func() error { return a.RemoveMember(c, tenant, user) },
		"disable":            func() error { return a.SetDisabled(c, user, true, "") },
		"enable":             func() error { return a.SetDisabled(c, user, false, "") },
		"delete":             func() error { return a.DeleteUser(c, user) },
		"profile":            func() error { _, err := a.PatchUser(c, user, PatchUserInput{}); return err },
		"invite":             func() error { return a.Invite(c, user, "") },
		"temporary password": func() error { _, err := a.TemporaryPassword(c, user); return err },
		"driver link":        func() error { return a.LinkDriver(c, user, uuid.New()) },
		"driver unlink":      func() error { return a.UnlinkDriver(c, user) },
		"ratify":             func() error { _, err := a.Ratify(c, tenant, user, RatifyInput{}); return err },
	}
	for name, run := range actions {
		t.Run(name, func(t *testing.T) {
			err := run()
			e, ok := err.(*httpx.Error)
			if !ok || e.Code != httpx.CodeSupportReadOnly {
				t.Fatalf("guard: %v", err)
			}
		})
	}
}
