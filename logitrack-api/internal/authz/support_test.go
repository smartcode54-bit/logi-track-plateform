package authz

import (
	"github.com/google/uuid"
	"testing"
)

func TestDerivedReadCompatibility(t *testing.T) {
	tenant := uuid.New()
	for _, pair := range [][2]Cap{{FleetManageMaintenance, FleetViewMaintenance}, {FleetManageSubcontractors, FleetViewSubcontractors}} {
		for _, steward := range []bool{false, true} {
			for _, rows := range [][]Override{
				nil,
				{{Cap: pair[0], Allowed: false}},
				{{TenantID: &tenant, Cap: pair[0], Allowed: false}},
				{{Cap: pair[0], Allowed: false}, {TenantID: &tenant, Cap: pair[0], Allowed: true}},
				{{Cap: pair[0], Allowed: true}, {TenantID: &tenant, Cap: pair[0], Allowed: false}},
				{{Cap: pair[1], Allowed: true}},
			} {
				for _, role := range []TenantRole{TenantAdmin, Manager, Operator} {
					resolved := RoleCaps(role, steward, rows)
					if resolved.Has(pair[0]) != resolved.Has(pair[1]) {
						t.Fatalf("derived read changed effective old authority: %v %v %v", pair, role, rows)
					}
					if Overridable(role, pair[1], true) || Overridable(role, pair[1], false) {
						t.Fatal("derived read is independently editable")
					}
				}
			}
		}
	}
	if RoleCaps(TenantAdmin, true, nil).Has(UsersViewSessions) {
		t.Fatal("tenant session reads are deferred")
	}
	if !PlatformCaps(&Principal{Platform: []PlatformRole{PlatformAdmin}}).Has(UsersViewSessions) {
		t.Fatal("platform revoke grant must derive session reads")
	}
	if PlatformCaps(&Principal{Platform: []PlatformRole{Support}}).Has(UsersViewSessions) {
		t.Fatal("platform read widened beyond old revoke authority")
	}
	for _, key := range TenantSupportReads().Keys() {
		if ClassOf(key) == ClassPlatform || ClassOf(key) == ClassSelf || key == UsersViewSessions {
			t.Fatal("non-tenant read", key)
		}
	}
}
func TestSupportDynamicActionsAndInvalidPaths(t *testing.T) {
	for _, path := range []string{"/app/subcontractors/id/edit", "/app/subcontractors/id/%65dit", "/app/subcontractors/new", "/app/customers/id", "/app/trucks/new", "/app/subcontractors/id%2fedit", "/app/subcontractors/%5c", "/app/subcontractors/..", "/app/subcontractors/%ZZ", "/app/subcontractors/%00"} {
		if SupportRouteAllowed(path) {
			t.Fatal("support action/deferred/invalid path allowed", path)
		}
	}
	for _, path := range []string{"/app/subcontractors/018f47a0-7b31-7c22-8b10-9733e2bba819", "/app/subcontractors/opaque/", "/app/maintenance"} {
		if !SupportRouteAllowed(path) {
			t.Fatal(path)
		}
	}
}

func TestAmbiguousTemplatesAndCapabilityPlanes(t *testing.T) {
	if ValidateWebRouteTemplates(map[string][]Cap{"/app/a/[id]": {}, "/app/[id]/b": {}}) == nil {
		t.Fatal("ambiguous templates accepted")
	}
	if err := ValidateWebRouteTemplates(WebRoutes()); err != nil {
		t.Fatal(err)
	}
	platform := NewCapSet(SecurityManageMobileRel, PlatformManageTenants, FleetManageSubcontractors)
	if !RouteAllowedInPlane(CapSet{}, platform, "/app/security-center/mobile-release", "platform") {
		t.Fatal("platform control lost")
	}
	if RouteAllowedInPlane(CapSet{}, platform, "/app/subcontractors/new", "tenant") || RouteAllowedInPlane(CapSet{}, platform, "/app/subcontractors/new", "platform") {
		t.Fatal("platform plane admitted tenant write")
	}
}
