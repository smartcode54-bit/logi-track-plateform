package authz

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var (
	ownFleet = uuid.MustParse("0199c000-0000-7000-8000-00000000000a")
	carrier  = uuid.MustParse("0199c000-0000-7000-8000-00000000000b")
)

func ptr(id uuid.UUID) *uuid.UUID { return &id }

func TestOverridable(t *testing.T) {
	for _, tc := range []struct {
		role      TenantRole
		key       Cap
		tenantRow bool
		want      bool
	}{
		{TenantAdmin, FleetViewTrucks, true, false},           // tenant_admin is not overridable
		{TenantAdmin, SecurityManageRoles, false, false},      // not even platform-wide
		{Manager, AccountingRecomputeForce, true, true},       // tenant key, tenant row
		{Manager, FleetManageCustomers, true, true},           // global: allowed here, stripped unless steward
		{Manager, UsersAssignRole, true, false},               // privilege escalation: platform-wide only
		{Manager, UsersAssignRole, false, true},               // platform-wide row
		{Manager, PlatformCrossTenantRead, false, false},      // platform keys never
		{Manager, DispatchViewOperations, false, false},       // scope keys never
		{Manager, MobileCheckin, true, false},                 // a staff row cannot grant mobile keys
		{Driver, MobileCreateHub, true, true},                 // self keys for drivers
		{Driver, DriversView, true, false},                    // a driver row cannot grant staff keys
		{Operator, Cap("fleet:not_in_catalog"), true, false},  // unknown keys never
		{TenantRole("partner"), FleetViewTrucks, true, false}, // not a tenant role
	} {
		if got := Overridable(tc.role, tc.key, tc.tenantRow); got != tc.want {
			t.Errorf("Overridable(%s, %s, tenantRow=%v) = %v, want %v", tc.role, tc.key, tc.tenantRow, got, tc.want)
		}
	}
}

func TestRoleCapsPrecedenceAndStewardRule(t *testing.T) {
	ov := []Override{
		{TenantID: nil, Cap: AccountingViewRateCard, Allowed: true},           // platform-wide grant
		{TenantID: ptr(carrier), Cap: AccountingViewRateCard, Allowed: false}, // tenant row wins
		{TenantID: nil, Cap: HRViewPayroll, Allowed: true},
		{TenantID: ptr(carrier), Cap: FleetManageCustomers, Allowed: true}, // global key by override
		{TenantID: ptr(carrier), Cap: OperationsViewFirstMile, Allowed: false},
		{TenantID: ptr(carrier), Cap: PlatformManageTenants, Allowed: true}, // never grantable
		{TenantID: ptr(carrier), Cap: UsersAssignRole, Allowed: true},       // tenant row: ignored
	}
	op := RoleCaps(Operator, false, ov)
	switch {
	case op.Has(AccountingViewRateCard):
		t.Error("the tenant row must win over the platform-wide row")
	case !op.Has(HRViewPayroll):
		t.Error("platform-wide grant lost")
	case op.Has(FleetManageCustomers):
		t.Error("a global key granted by override is effective for a carrier operator (R60)")
	case op.Has(OperationsViewFirstMile):
		t.Error("tenant revoke lost")
	case op.Has(PlatformManageTenants), op.Has(UsersAssignRole):
		t.Error("a non-grantable key was granted")
	case op.Has(OperationsManageSources):
		t.Error("operations:manage_sources (global) effective outside the own fleet")
	}
	// The same rows for a steward (own-fleet operator): the global keys are effective.
	steward := RoleCaps(Operator, true, ov)
	if !steward.Has(FleetManageCustomers) || !steward.Has(OperationsManageSources) {
		t.Error("an own-fleet operator lost a global key")
	}
	// tenant_admin ignores every override and keeps its 64 keys only as a steward.
	ta := RoleCaps(TenantAdmin, false, []Override{{TenantID: ptr(carrier), Cap: SecurityManageRoles, Allowed: false}})
	if !ta.Has(SecurityManageRoles) || ta.Len() != 57 {
		t.Errorf("carrier tenant_admin holds %d keys (want the 64 defaults minus the 7 global keys)", ta.Len())
	}
	if n := RoleCaps(TenantAdmin, true, nil).Len(); n != 64 {
		t.Errorf("own-fleet tenant_admin holds %d keys, want 64", n)
	}
	if RoleCaps("", true, ov).Len() != 0 {
		t.Error("no role, no keys")
	}
}

func TestIsSteward(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Principal
		kind string
		want bool
	}{
		{"own-fleet manager", Principal{TenantID: ptr(ownFleet), TenantRole: Manager}, TenantKindOwnFleet, true},
		{"own-fleet user", Principal{TenantID: ptr(ownFleet), TenantRole: User}, TenantKindOwnFleet, true},
		{"own-fleet driver", Principal{TenantID: ptr(ownFleet), TenantRole: Driver}, TenantKindOwnFleet, false},
		{"carrier tenant_admin", Principal{TenantID: ptr(carrier), TenantRole: TenantAdmin}, TenantKindCarrier, false},
		{"platform_admin", Principal{Platform: []PlatformRole{PlatformAdmin}}, "", true},
		{"support", Principal{Platform: []PlatformRole{Support}}, "", false},
		{"acting as a carrier", Principal{Platform: []PlatformRole{PlatformAdmin}, ActOnTenant: ptr(carrier)}, TenantKindCarrier, true},
		{"customer scope", Principal{PartyIDs: []uuid.UUID{carrier}}, "", false},
		{"platform API key", Principal{AMR: AMRAPIKey}, "", true},
		{"own-fleet API key", Principal{AMR: AMRAPIKey, TenantID: ptr(ownFleet)}, TenantKindOwnFleet, false},
	} {
		if got := IsSteward(&tc.p, tc.kind); got != tc.want {
			t.Errorf("%s: steward %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEffective(t *testing.T) {
	party := uuid.New()
	t.Run("customer scope", func(t *testing.T) {
		p := &Principal{PartyIDs: []uuid.UUID{party}}
		if got := Effective(p, CapSet{}); got != ScopeDefaults(ScopeCustomer) {
			t.Errorf("customer scope %v", got.Keys())
		}
	})
	t.Run("member with a customer scope", func(t *testing.T) {
		// RLS gives a member its staff reach and never the scope's rows (app.role stays the tenant role),
		// so the member keeps exactly its role set: no operations:view_* from the customer scope.
		p := &Principal{TenantID: ptr(carrier), TenantRole: User, PartyIDs: []uuid.UUID{party}}
		if got, want := Effective(p, RoleCaps(User, false, nil)), RoleCaps(User, false, nil); got != want {
			t.Errorf("member with a customer scope %v, want its role set %v", got.Keys(), want.Keys())
		}
		if p.IsCustomerScope() {
			t.Error("a member is not a customer-scope principal")
		}
	})
	t.Run("dispatcher", func(t *testing.T) {
		p := &Principal{TenantID: ptr(carrier), TenantRole: User, Dispatcher: true, PartyIDs: []uuid.UUID{party}}
		got := Effective(p, RoleCaps(User, false, nil))
		if !got.Has(DispatchViewOperations) || !got.Has(FleetViewLiveMap) || got.Has(OperationsManageTasks) {
			t.Errorf("dispatcher %v", got.Keys())
		}
		if got.Len() != 8 {
			t.Errorf("dispatcher with role user holds %d keys, want 2 + 6", got.Len())
		}
	})
	t.Run("platform roles", func(t *testing.T) {
		pa := Effective(&Principal{Platform: []PlatformRole{PlatformAdmin}}, CapSet{})
		if pa != PlatformDefaults(PlatformAdmin) || pa.Has(FleetViewTrucks) {
			t.Errorf("platform_admin without the header %v", pa.Keys())
		}
		both := Effective(&Principal{Platform: []PlatformRole{PlatformAdmin, Support}}, CapSet{})
		if both != PlatformDefaults(PlatformAdmin).Union(PlatformDefaults(Support)) {
			t.Errorf("two platform roles %v", both.Keys())
		}
	})
	t.Run("X-Act-On-Tenant: *", func(t *testing.T) {
		pa := Effective(&Principal{Platform: []PlatformRole{PlatformAdmin}, ActOnAll: true}, CapSet{})
		if !pa.Has(OperationsViewFirstMile) || !pa.Has(PlatformCrossTenantRead) || pa.Len() != 64+4 {
			t.Errorf("platform_admin with * holds %d keys, want the tenant_admin set and the platform set", pa.Len())
		}
		su := Effective(&Principal{Platform: []PlatformRole{Support}, ActOnAll: true}, CapSet{})
		if su != PlatformDefaults(Support) {
			t.Errorf("support with * %v, want its own read-only set", su.Keys())
		}
	})
	t.Run("API keys", func(t *testing.T) {
		keys := []Cap{FleetViewTrucks, FleetManageCustomers, MobileCheckin, PlatformManageTenants, DispatchViewOperations, "nope:x"}
		tenantKey := Effective(&Principal{AMR: AMRAPIKey, TenantID: ptr(carrier), APIKeyCaps: keys}, TenantDefaults(TenantAdmin))
		if tenantKey != NewCapSet(FleetViewTrucks) {
			t.Errorf("tenant key %v, want only its tenant-class key", tenantKey.Keys())
		}
		platformKey := Effective(&Principal{AMR: AMRAPIKey, APIKeyCaps: keys}, CapSet{})
		if platformKey != NewCapSet(FleetManageCustomers, MobileCheckin) {
			t.Errorf("platform key %v, want its global and mobile keys", platformKey.Keys())
		}
	})
}

// TestRoleSetFingerprint: the rbac:caps key carries a fingerprint of the compiled-in defaults, so a
// release that changes a default set or a key's class (which Overridable reads) reads its own cache
// entries, never those of another release (Appendix C §C.2.5).
func TestRoleSetFingerprint(t *testing.T) {
	if len(RoleSetFingerprint) != 8 || strings.Trim(RoleSetFingerprint, "0123456789abcdef") != "" {
		t.Fatalf("RoleSetFingerprint %q is not 8 hex digits", RoleSetFingerprint)
	}
	if got := roleSetFingerprint(TenantRoles, tenantDefaults, catalog); got != RoleSetFingerprint {
		t.Fatalf("not deterministic: %s vs %s", got, RoleSetFingerprint)
	}
	narrowed := maps.Clone(tenantDefaults)
	m := narrowed[Manager]
	m.Remove(AccountingViewRateCard)
	narrowed[Manager] = m
	if tenantDefaults[Manager] == narrowed[Manager] || !tenantDefaults[Manager].Has(AccountingViewRateCard) {
		t.Fatal("manager's default set is unchanged by the narrowing")
	}
	if roleSetFingerprint(TenantRoles, narrowed, catalog) == RoleSetFingerprint {
		t.Error("narrowing manager's default set leaves the fingerprint unchanged")
	}
	reclassed := slices.Clone(catalog)
	i := slices.IndexFunc(reclassed, func(e CapInfo) bool { return e.Key == FleetManageCustomers })
	reclassed[i].Class = ClassTenant
	if roleSetFingerprint(TenantRoles, tenantDefaults, reclassed) == RoleSetFingerprint {
		t.Error("changing a key's class leaves the fingerprint unchanged")
	}
}
