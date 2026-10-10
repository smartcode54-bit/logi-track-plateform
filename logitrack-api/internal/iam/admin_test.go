package iam

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
)

func TestValidThaiID(t *testing.T) {
	for in, want := range map[string]bool{
		"0105536000313": true, "1101700203450": true, "1101700203451": false, "0105536000315": false, "010553600031": false,
		"01055360003133": false, "010553600031a": false, "": false,
	} {
		if got := ValidThaiID(in); got != want {
			t.Errorf("ValidThaiID(%q) = %t, want %t", in, got, want)
		}
	}
}

func TestParseEmails(t *testing.T) {
	emails, bad := ParseEmails(" Admin@Example.test, ops@example.test,,admin@example.test , not-an-address ")
	if !slices.Equal(emails, []string{"admin@example.test", "ops@example.test"}) || !slices.Equal(bad, []string{"not-an-address"}) {
		t.Fatalf("emails %v, bad %v", emails, bad)
	}
	if e, b := ParseEmails(""); len(e) != 0 || len(b) != 0 {
		t.Fatalf("empty list: %v %v", e, b)
	}
}

func TestLikePattern(t *testing.T) {
	if got := likePattern(`50%_off\x`); got != `%50\%\_off\\x%` {
		t.Fatalf("likePattern = %q", got)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	id := uuid.New()
	at := time.Date(2026, 10, 1, 1, 2, 3, 456000, time.UTC)
	c, ok := decodeCursor(encodeCursor(&at, id), false)
	if !ok || c.ID != id || c.T == nil || !c.T.Equal(at) {
		t.Fatalf("round trip: %+v %t", c, ok)
	}
	// A NULL sort value (a user who never signed in) is a valid last_login_at cursor only.
	n := encodeCursor(nil, id)
	if _, ok := decodeCursor(n, false); ok {
		t.Fatal("a NULL created_at cursor was accepted")
	}
	if c, ok := decodeCursor(n, true); !ok || c.T != nil {
		t.Fatal("a NULL last_login_at cursor was refused")
	}
	for _, bad := range []string{"", "!!", "e30", encodeCursor(&at, uuid.Nil)} {
		if _, ok := decodeCursor(bad, true); ok {
			t.Errorf("cursor %q accepted", bad)
		}
	}
}

func TestField(t *testing.T) {
	var in struct {
		A Field[string] `json:"a"`
		B Field[string] `json:"b"`
		C Field[string] `json:"c"`
	}
	if err := json.Unmarshal([]byte(`{"a":"x","b":null}`), &in); err != nil {
		t.Fatal(err)
	}
	if !in.A.Set || in.A.Null || in.A.V != "x" || !in.B.Set || !in.B.Null || in.C.Set {
		t.Fatalf("fields %+v", in)
	}
}

func principal(role authz.TenantRole, tenant *uuid.UUID, platform ...authz.PlatformRole) *authz.Principal {
	p := &authz.Principal{UserID: uuid.New(), TenantID: tenant, TenantRole: role, Platform: platform}
	return p
}

func TestReach(t *testing.T) {
	own, sub, other := uuid.New(), uuid.New(), uuid.New()
	staff := principal(authz.Manager, &own)
	staff.SubtenantIDs, staff.Steward = []uuid.UUID{sub}, true
	r := readReach(staff)
	if r.all || !r.scopeOnly || !r.hasTenant(own) || !r.hasTenant(sub) || r.hasTenant(other) {
		t.Fatalf("own-fleet staff reach %+v", r)
	}
	driver := principal(authz.Driver, &own)
	if r := readReach(driver); r.hasTenant(own) || r.scopeOnly {
		t.Fatalf("a driver reaches %+v", r)
	}
	pa := principal("", nil, authz.PlatformAdmin)
	pa.Steward = true
	if r := readReach(pa); r.all || len(r.tenants) != 0 || !r.scopeOnly {
		t.Fatalf("platform admin read reach without the header %+v", r)
	}
	if r := writeReach(pa); !r.all {
		t.Fatal("a platform admin writes everywhere without the header")
	}
	all := principal("", nil, authz.Support)
	all.ActOnAll = true
	if r := readReach(all); !r.all {
		t.Fatal("X-Act-On-Tenant: * reads everyone")
	}
	if r := writeReach(all); r.all || len(r.tenants) != 0 {
		t.Fatal("X-Act-On-Tenant: * is read-only")
	}
}

func TestCanAssign(t *testing.T) {
	own, carrier := uuid.New(), uuid.New()
	ta := principal(authz.TenantAdmin, &own)
	ta.Caps = authz.NewCapSet(authz.UsersAssignRole)
	if err := canAssign(ta, own, authz.TenantAdmin, ""); err != nil {
		t.Errorf("a tenant_admin grants tenant_admin in its tenant: %v", err)
	}
	if err := canAssign(ta, carrier, authz.TenantAdmin, ""); err == nil {
		t.Error("a tenant_admin granted tenant_admin in another tenant")
	}
	if err := canAssign(ta, carrier, authz.Manager, authz.TenantAdmin); err == nil {
		t.Error("a tenant_admin demoted another tenant's admin")
	}
	if err := canAssign(ta, carrier, authz.Operator, authz.Manager); err != nil {
		t.Errorf("a role change below tenant_admin: %v", err)
	}
	mg := principal(authz.Manager, &own)
	if err := canAssign(mg, own, authz.User, ""); err == nil {
		t.Error("a principal without users:assign_role assigned a role")
	}
	pa := principal("", nil, authz.PlatformAdmin)
	pa.Caps = authz.NewCapSet(authz.UsersAssignRole)
	if err := canAssign(pa, carrier, authz.TenantAdmin, ""); err != nil {
		t.Errorf("platform admin grants tenant_admin: %v", err)
	}
	if err := notSelf(ta, ta.UserID); err == nil {
		t.Error("an admin route accepted the caller")
	}
}
