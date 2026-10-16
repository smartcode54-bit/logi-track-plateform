package iam

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scheduler"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

var (
	ownFleet = matrixLayer{tenant: ptr(uuid.MustParse("01900000-0000-7000-8000-000000000001")), kind: authz.TenantKindOwnFleet}
	carrier  = matrixLayer{tenant: ptr(uuid.MustParse("01900000-0000-7000-8000-000000000002")), kind: authz.TenantKindCarrier}
	platform = matrixLayer{}
)

func ptr[T any](v T) *T { return &v }

// The AC of T51: tenant callers cannot override platform:*, dispatch:* or users:assign_role; global keys only in
// the own fleet or platform-wide; tenant_admin never; a key of the other role class never. Every admitted cell is
// one authz.Overridable admits, so a stored row always takes effect.
func TestNotOverridable(t *testing.T) {
	cases := []struct {
		role  authz.TenantRole
		key   authz.Cap
		layer matrixLayer
		want  string
	}{
		{authz.Manager, authz.PlatformManageTenants, carrier, ReasonNotGrantable},
		{authz.Manager, authz.PlatformCrossTenantRead, platform, ReasonNotGrantable},
		{authz.Operator, authz.DispatchViewOperations, ownFleet, ReasonNotGrantable},
		{authz.Manager, authz.UsersAssignRole, carrier, ReasonPlatformWideOnly},
		{authz.Manager, authz.UsersAssignRole, ownFleet, ReasonPlatformWideOnly},
		{authz.Manager, authz.UsersAssignRole, platform, ""},
		{authz.TenantAdmin, authz.FleetViewTrucks, ownFleet, ReasonTenantAdminFixed},
		{authz.TenantAdmin, authz.FleetViewTrucks, platform, ReasonTenantAdminFixed},
		{authz.Manager, authz.FleetManageCustomers, carrier, ReasonGlobalOwnFleet},
		{authz.Manager, authz.FleetManageCustomers, ownFleet, ""},
		{authz.Manager, authz.FleetManageCustomers, platform, ""},
		{authz.Driver, authz.FleetViewTrucks, ownFleet, ReasonRoleClass},
		{authz.Manager, authz.MobileCheckin, ownFleet, ReasonRoleClass},
		{authz.Driver, authz.MobileCheckin, carrier, ""},
		{authz.Operator, authz.UsersManage, carrier, ""},
	}
	for _, c := range cases {
		if got := notOverridable(c.role, c.key, c.layer); got != c.want {
			t.Errorf("%s %s in %s/%s: %q, want %q", c.role, c.key, c.layer.name(), c.layer.kind, got, c.want)
		}
	}
	for _, l := range []matrixLayer{ownFleet, carrier, platform} {
		for _, role := range authz.TenantRoles {
			for _, e := range authz.Catalog() {
				if notOverridable(role, e.Key, l) == "" && !authz.Overridable(role, e.Key, l.tenant != nil) {
					t.Errorf("%s %s in %s: admitted by the API, ignored by authz.RoleCaps", role, e.Key, l.name())
				}
			}
		}
	}
}

func TestMatrixKeys(t *testing.T) {
	keys := matrixKeys()
	if len(keys) != 76 {
		t.Fatalf("%d keys, want the 76 of classes tenant, global and self", len(keys))
	}
	for _, k := range keys {
		if c := authz.ClassOf(k); c == authz.ClassPlatform || c == authz.ClassScope {
			t.Errorf("%s (%s) is in the matrix", k, c)
		}
	}
}

func TestBuildMatrix(t *testing.T) {
	platformRows := map[cellKey]bool{
		{authz.Operator, authz.UsersView}:           true,  // platform-wide grant
		{authz.Manager, authz.UsersAssignRole}:      true,  // platform-wide only key
		{authz.Manager, authz.FleetManageCustomers}: false, // a global key withdrawn platform-wide
	}
	tenantRows := map[cellKey]bool{
		{authz.Operator, authz.UsersView}:              false, // the tenant withdraws the platform grant
		{authz.Operator, authz.FleetCreateTruck}:       true,
		{authz.Manager, authz.OperationsManageSources}: true, // stray: global key in a carrier tenant (ignored there)
	}
	m := buildMatrix(carrier, platformRows, tenantRows)
	if m.Layer != LayerTenant || m.TenantID == nil || *m.TenantKind != authz.TenantKindCarrier || len(m.Roles) != 6 {
		t.Fatalf("matrix head %+v", m)
	}
	cell := func(m Matrix, role authz.TenantRole, k authz.Cap) MatrixCell {
		for _, r := range m.Roles {
			if r.Role == string(role) {
				c, ok := r.Capabilities[string(k)]
				if !ok {
					t.Fatalf("%s has no %s", role, k)
				}
				return c
			}
		}
		t.Fatalf("no role %s", role)
		return MatrixCell{}
	}
	if c := cell(m, authz.Operator, authz.UsersView); c.Default || c.Effective || !c.Overridden || !c.Overridable ||
		c.PlatformOverride == nil || !*c.PlatformOverride {
		t.Errorf("operator users:view in the carrier: %+v", c)
	}
	if c := cell(m, authz.Operator, authz.FleetCreateTruck); c.Default || !c.Effective || !c.Overridden {
		t.Errorf("operator fleet:create_truck: %+v", c)
	}
	if c := cell(m, authz.Manager, authz.UsersAssignRole); !c.Effective || c.Overridden || c.Overridable {
		t.Errorf("manager users:assign_role inherits the platform-wide row and is not overridable in a tenant: %+v", c)
	}
	if c := cell(m, authz.Manager, authz.OperationsManageSources); c.Effective || c.Overridden || c.Overridable || !c.Default {
		t.Errorf("a global key is never effective in a carrier: %+v", c)
	}
	for _, r := range m.Roles {
		if r.Role == string(authz.TenantAdmin) {
			if r.Overridable {
				t.Error("tenant_admin is overridable")
			}
			for k, c := range r.Capabilities {
				if c.Overridable || c.Overridden {
					t.Errorf("tenant_admin %s: %+v", k, c)
				}
			}
		}
	}
	// The own fleet: a global key is effective for staff.
	own := buildMatrix(ownFleet, nil, nil)
	if c := cell(own, authz.Manager, authz.FleetManageCustomers); !c.Effective || !c.Overridable {
		t.Errorf("manager fleet:manage_customers in the own fleet: %+v", c)
	}
	// The platform-wide layer shows its own rows as overridden, before the steward rule.
	pm := buildMatrix(platform, platformRows, nil)
	if pm.Layer != LayerPlatform || pm.TenantID != nil || pm.TenantKind != nil {
		t.Fatalf("platform matrix head %+v", pm)
	}
	if c := cell(pm, authz.Manager, authz.FleetManageCustomers); c.Effective || !c.Overridden || c.PlatformOverride != nil {
		t.Errorf("platform-wide manager fleet:manage_customers: %+v", c)
	}
	if c := cell(pm, authz.Manager, authz.UsersAssignRole); !c.Effective || !c.Overridden || !c.Overridable {
		t.Errorf("platform-wide manager users:assign_role: %+v", c)
	}
}

func TestDesiredRows(t *testing.T) {
	platformRows := map[cellKey]bool{{authz.Operator, authz.UsersView}: true}
	in := func(list ...MatrixOverride) SaveMatrixInput { return SaveMatrixInput{Overrides: &list} }
	yes, no := ptr(true), ptr(false)
	want, err := desiredRows(in(
		MatrixOverride{Role: "operator", Capability: "fleet:create_truck", Allowed: yes}, // differs from default: a row
		MatrixOverride{Role: "operator", Capability: "fleet:view_trucks", Allowed: yes},  // equals default: no row
		// Equals the inherited platform-wide grant, but a platform-wide row covers the cell: the tenant row is kept,
		// so it pins the tenant's choice when the platform-wide row is lifted later.
		MatrixOverride{Role: "operator", Capability: "users:view", Allowed: yes},
		MatrixOverride{Role: "manager", Capability: "drivers:view", Allowed: no},
	), carrier, platformRows, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != 3 || !want[cellKey{authz.Operator, authz.FleetCreateTruck}] || want[cellKey{authz.Manager, authz.DriversView}] ||
		!want[cellKey{authz.Operator, authz.UsersView}] {
		t.Fatalf("rows %v", want)
	}
	// A tenant deny that mirrors a platform-wide deny is stored; the same request in the platform-wide layer is the
	// layer's own row (no pinning there), and a value equal to the inherited one with no platform-wide row is not.
	deny := map[cellKey]bool{{authz.Manager, authz.UsersView}: false}
	if want, err = desiredRows(in(MatrixOverride{Role: "manager", Capability: "users:view", Allowed: no}), carrier, deny, nil); err != nil ||
		len(want) != 1 || want[cellKey{authz.Manager, authz.UsersView}] {
		t.Fatalf("pinned tenant deny: %v %v", want, err)
	}
	if want, err = desiredRows(in(MatrixOverride{Role: "manager", Capability: "users:view", Allowed: yes}), carrier, nil, nil); err != nil ||
		len(want) != 0 {
		t.Fatalf("a row equal to the default with no platform-wide row: %v %v", want, err)
	}
	// A row the layer already holds is kept when the body repeats it, even once it equals the inherited value (the
	// platform-wide row it pinned was lifted): a GET -> PUT round trip never removes a row.
	held := map[cellKey]bool{{authz.Manager, authz.UsersView}: true}
	if want, err = desiredRows(in(MatrixOverride{Role: "manager", Capability: "users:view", Allowed: yes}), carrier, nil, held); err != nil ||
		len(want) != 1 || !want[cellKey{authz.Manager, authz.UsersView}] {
		t.Fatalf("a held row repeated by the body: %v %v", want, err)
	}
	if want, err = desiredRows(in(), carrier, nil, held); err != nil || len(want) != 0 {
		t.Fatalf("a held row left out of the body is removed: %v %v", want, err)
	}

	expect422 := func(name string, err error, code string) *httpx.Error {
		t.Helper()
		var he *httpx.Error
		if !errors.As(err, &he) || he.Status != http.StatusUnprocessableEntity || he.Code != code {
			t.Fatalf("%s: want 422 %s, got %v", name, code, err)
		}
		return he
	}
	_, err = desiredRows(SaveMatrixInput{}, carrier, nil, nil)
	_ = expect422("missing overrides", err, httpx.CodeInvalidArgument)
	_, err = desiredRows(in(MatrixOverride{Role: "boss", Capability: "fleet:nope", Allowed: nil}), carrier, nil, nil)
	he := expect422("bad row", err, httpx.CodeInvalidArgument)
	if f := he.Details["fields"].([]httpx.FieldViolation); len(f) != 3 {
		t.Errorf("fields %v", f)
	}
	_, err = desiredRows(in(MatrixOverride{Role: "operator", Capability: "fleet:create_truck", Allowed: yes},
		MatrixOverride{Role: "operator", Capability: "fleet:create_truck", Allowed: no}), carrier, nil, nil)
	_ = expect422("duplicate", err, httpx.CodeInvalidArgument)
	// The AC: a tenant caller cannot override platform:*, dispatch:* or users:assign_role.
	for _, k := range []authz.Cap{authz.PlatformManageTenants, authz.DispatchViewOperations, authz.UsersAssignRole} {
		_, err = desiredRows(in(MatrixOverride{Role: "manager", Capability: string(k), Allowed: yes}), ownFleet, nil, nil)
		he := expect422(string(k), err, CodeCapabilityNotOverridable)
		if r := he.Details["overrides"].([]refusal); len(r) != 1 || r[0].Capability != string(k) {
			t.Errorf("%s: %v", k, r)
		}
	}
	if _, err = desiredRows(in(MatrixOverride{Role: "manager", Capability: "users:assign_role", Allowed: yes}), platform, nil, nil); err != nil {
		t.Errorf("users:assign_role platform-wide: %v", err)
	}
	_, err = desiredRows(in(MatrixOverride{Role: "tenant_admin", Capability: "fleet:view_trucks", Allowed: no}), platform, nil, nil)
	_ = expect422("tenant_admin", err, CodeCapabilityNotOverridable)
	many := make([]MatrixOverride, MaxMatrixOverrides+1)
	_, err = desiredRows(SaveMatrixInput{Overrides: &many}, carrier, nil, nil)
	_ = expect422("too many", err, httpx.CodeInvalidArgument)
}

// ?layer=tenant names a tenant layer: without ?tenantId or an effective tenant it is 403 tenant_required, never
// the platform-wide layer (whose rows apply to every tenant), for reads and writes alike. Without ?layer a platform
// admin with no tenant still reads and writes the platform-wide layer; ?layer=platform names it; anything else is
// 422.
func TestResolveLayerNoTenant(t *testing.T) {
	a := &Admin{}
	pa := &authz.Principal{UserID: uuid.New(), AMR: authz.AMRPassword, Platform: []authz.PlatformRole{authz.PlatformAdmin}}
	for _, write := range []bool{false, true} {
		c := call{ctx: t.Context(), p: pa}
		_, err := a.resolveLayer(c, nil, MatrixTarget{Layer: LayerTenant}, write)
		var he *httpx.Error
		if !errors.As(err, &he) || he.Status != http.StatusForbidden || he.Code != authz.CodeTenantRequired {
			t.Errorf("?layer=tenant (write %t): want 403 tenant_required, got %v", write, err)
		}
		for _, layer := range []string{"", LayerPlatform} {
			if l, err := a.resolveLayer(c, nil, MatrixTarget{Layer: layer}, write); err != nil || l.tenant != nil {
				t.Errorf("?layer=%q (write %t): want the platform-wide layer, got %+v %v", layer, write, l, err)
			}
		}
		if _, err := a.resolveLayer(c, nil, MatrixTarget{Layer: "world"}, write); !errors.As(err, &he) ||
			he.Status != http.StatusUnprocessableEntity {
			t.Errorf("?layer=world: %v", err)
		}
	}
}

func TestDiffRows(t *testing.T) {
	before := map[cellKey]bool{
		{authz.Operator, authz.FleetCreateTruck}: true,
		{authz.Manager, authz.DriversView}:       false,
		{authz.Driver, authz.MobileChat}:         false,
	}
	after := map[cellKey]bool{
		{authz.Operator, authz.FleetCreateTruck}: true,  // unchanged
		{authz.Manager, authz.DriversView}:       true,  // changed
		{authz.User, authz.FleetViewTrucks}:      false, // added
	}
	got := diffRows(before, after)
	if len(got) != 3 {
		t.Fatalf("changes %+v", got)
	}
	// Role order of the catalog: manager, user, driver.
	if got[0].Role != "manager" || *got[0].From || !*got[0].To ||
		got[1].Role != "user" || got[1].From != nil || *got[1].To ||
		got[2].Role != "driver" || got[2].To != nil || *got[2].From {
		t.Fatalf("changes %+v", got)
	}
	if len(diffRows(after, after)) != 0 {
		t.Fatal("no change expected")
	}
}

func TestSchedulerLockName(t *testing.T) {
	if SchedulerLockName != scheduler.LockName {
		t.Fatalf("the status page looks for %q, the scheduler takes %q", SchedulerLockName, scheduler.LockName)
	}
}

// Every security event type this package writes is in the catalogue the audit page filters by.
func TestEventTypesCatalogued(t *testing.T) {
	for _, e := range []string{EventUserCreated, EventUserUpdated, EventUserInvited, EventUserRoleChanged, EventUserScopeChanged,
		EventUserDisabled, EventUserEnabled, EventUserDeleted, EventUserSessionsRevoked, EventUserTemporaryPassword,
		EventDriverLinked, EventDriverUnlinked, EventPlatformRoleGranted, EventPlatformRoleRevoked, EventTenantCreated,
		EventTenantUpdated, EventTenantContractor, EventPasswordChanged, EventRoleMatrixSaved, EventCrossTenantAccess,
		EventMembershipAdded, EventMembershipRoleChanged, EventMembershipRemoved, auth.EventTenantSwitched} {
		if !security.KnownType(e) {
			t.Errorf("%s is not in security.EventTypes", e)
		}
	}
}
