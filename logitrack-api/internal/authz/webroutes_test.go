package authz

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// legacyRoutes reads ROUTE_CAPABILITIES of logitrack-web/lib/capabilities.ts (the 53 entries of
// :340-394) with each entry's legacy colon value.
func legacyRoutes(t *testing.T) map[string]Cap {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(moduleRoot(t), "..", "logitrack-web", "lib", "capabilities.ts"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	values := map[string]Cap{}
	for _, m := range regexp.MustCompile(`(?m)^\s*([a-z_]+): "([a-z]+:[a-z_]+)",`).FindAllStringSubmatch(src, -1) {
		values[m[1]] = Cap(m[2])
	}
	i := strings.Index(src, "export const ROUTE_CAPABILITIES")
	if i < 0 {
		t.Fatal("lib/capabilities.ts has no ROUTE_CAPABILITIES")
	}
	out := map[string]Cap{}
	for _, m := range regexp.MustCompile(`"(/app[^"]*)": CAPABILITIES\.([a-z_]+)`).FindAllStringSubmatch(src[i:], -1) {
		v, ok := values[m[2]]
		if !ok {
			t.Fatalf("%s: CAPABILITIES.%s has no value", m[1], m[2])
		}
		out[m[1]] = v
	}
	return out
}

// The deliberate differences from the legacy table (Appendix C §C.2.7, main spec §10.5).
var (
	changedRoutes = map[string][]Cap{
		"/app/dashboard":                {}, // open to every signed-in principal (permissions.ts:50-53), unchanged
		"/app/trucks/renew":             {FleetManageRenewals},
		"/app/security-center/users":    {UsersView}, // security:manage_users no longer exists
		"/app/utilities/backfill":       {AccountingRecomputeForce},
		"/app/utilities/billing-impact": {AccountingRecomputeForce},
	}
	removedRoutes = []string{"/app/utilities", "/app/companies/new", "/app/analytics", "/app/packages"}
)

func TestWebRoutesTranslateTheLegacyTable(t *testing.T) {
	legacy := legacyRoutes(t)
	if len(legacy) != 53 {
		t.Fatalf("lib/capabilities.ts ROUTE_CAPABILITIES has %d entries, want 53", len(legacy))
	}
	for path, old := range legacy {
		got, ok := webRoutes[path]
		switch {
		case slices.Contains(removedRoutes, path):
			if ok {
				t.Errorf("%s is removed (no page, §10.5) but still mapped", path)
			}
		case changedRoutes[path] != nil:
			if !slices.Equal(got, changedRoutes[path]) {
				t.Errorf("%s: %v, want %v", path, got, changedRoutes[path])
			}
		case !ok:
			t.Errorf("%s is not mapped", path)
		case !slices.Equal(got, []Cap{old}):
			t.Errorf("%s: %v, want the legacy key %s", path, got, old)
		}
	}
	for path, caps := range webRoutes {
		for _, k := range caps {
			if !Known(k) {
				t.Errorf("%s maps %s, which is not a catalog key", path, k)
			}
		}
	}
}

// TestEveryWebPageIsMapped: the edge gate denies unmapped paths, so every page under app/app/** needs a
// mapping (Appendix C §C.2.7) except the pages proxy.ts or next.config redirects (main spec §10.5,
// §10.12).
func TestEveryWebPageIsMapped(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "..", "logitrack-web", "app")
	redirected := []string{"/app", "/app/users", "/app/operations/roles"}
	n := 0
	err := filepath.WalkDir(filepath.Join(root, "app"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "page.tsx" {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		route := "/" + filepath.ToSlash(rel)
		n++
		if slices.Contains(redirected, route) {
			return nil
		}
		if _, _, ok := RouteFor(route); !ok {
			t.Errorf("page %s has no mapping: the edge gate would deny it", route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 50 {
		t.Fatalf("found %d pages under logitrack-web/app/app, expected the whole app", n)
	}
}

func TestRouteFor(t *testing.T) {
	for _, tc := range []struct {
		path, matched string
		ok            bool
	}{
		{"/app/trucks", "/app/trucks", true},
		{"/app/trucks/", "/app/trucks", true},
		{"/app/customers/0199c000-0000-7000-8000-000000000001/edit", "/app/customers", true},
		{"/app/chat/room", "/app/chat", true},
		{"/app/chat/with-driver", "/app/chat/with-driver", true},
		{"/app/utilities", "", false},
		{"/app/utilities/backfill/x", "/app/utilities/backfill", true},
		{"/app/trucksx", "", false},
		{"/app", "", false},
		{"/app/nowhere", "", false},
	} {
		_, matched, ok := RouteFor(tc.path)
		if ok != tc.ok || matched != tc.matched {
			t.Errorf("RouteFor(%q) = %q, %v; want %q, %v", tc.path, matched, ok, tc.matched, tc.ok)
		}
	}
}

// edgePrincipals are the principal classes of the role × route test, resolved as internal/iam does
// (no overrides): own-fleet (o) and carrier (c) staff, a driver, a customer-scope user, a dispatcher
// (membership role user in its own organisation), platform_admin without the header, acting as a
// carrier (t) and with * (all), support without and with *.
func edgePrincipals() map[string]*Principal {
	party := uuid.New()
	mk := func(p Principal, kind string) *Principal {
		p.TenantKind = kind
		p.Steward = IsSteward(&p, kind)
		p.Caps = Effective(&p, RoleCaps(p.EffectiveRole(), p.Steward, nil))
		p.MarkResolved()
		return &p
	}
	member := func(tid uuid.UUID, r TenantRole) Principal { return Principal{TenantID: ptr(tid), TenantRole: r} }
	pa := []PlatformRole{PlatformAdmin}
	su := []PlatformRole{Support}
	return map[string]*Principal{
		"TAo":   mk(member(ownFleet, TenantAdmin), TenantKindOwnFleet),
		"TAc":   mk(member(carrier, TenantAdmin), TenantKindCarrier),
		"MGo":   mk(member(ownFleet, Manager), TenantKindOwnFleet),
		"MGc":   mk(member(carrier, Manager), TenantKindCarrier),
		"OSo":   mk(member(ownFleet, OperationStaff), TenantKindOwnFleet),
		"OSc":   mk(member(carrier, OperationStaff), TenantKindCarrier),
		"OPo":   mk(member(ownFleet, Operator), TenantKindOwnFleet),
		"USo":   mk(member(ownFleet, User), TenantKindOwnFleet),
		"DR":    mk(Principal{TenantID: ptr(ownFleet), TenantRole: Driver, DriverID: ptr(uuid.New())}, TenantKindOwnFleet),
		"CU":    mk(Principal{PartyIDs: []uuid.UUID{party}}, ""),
		"DS":    mk(Principal{TenantID: ptr(carrier), TenantRole: User, Dispatcher: true, PartyIDs: []uuid.UUID{party}}, TenantKindCarrier),
		"PA":    mk(Principal{Platform: pa}, ""),
		"PAt":   mk(Principal{Platform: pa, ActOnTenant: ptr(carrier)}, TenantKindCarrier),
		"PAall": mk(Principal{Platform: pa, ActOnAll: true}, ""),
		"SU":    mk(Principal{Platform: su}, ""),
		"SUall": mk(Principal{Platform: su, ActOnAll: true}, ""),
	}
}

// TestEveryRoleEveryRoute is the table-driven role × route test of Appendix C §C.9.1 for the web edge
// gate: every principal class against every route of lib/capabilities.ts:340-394 (53) and the routes
// §10.5 adds. The expectations are written out from the holder columns of §C.2.3 (global keys only
// for stewards; platform_admin acting through X-Act-On-Tenant holds the tenant_admin set), not
// computed, so a change to the catalog, the defaults or the route map has to change this table too.
func TestEveryRoleEveryRoute(t *testing.T) {
	const (
		all       = "TAo TAc MGo MGc OSo OSc OPo USo DR CU DS PA PAt PAall SU SUall"
		taOnly    = "TAo TAc PAt PAall"
		taMg      = "TAo TAc MGo MGc PAt PAall"
		taMgOs    = "TAo TAc MGo MGc OSo OSc PAt PAall"
		taMgOsOp  = "TAo TAc MGo MGc OSo OSc OPo PAt PAall"
		staffAll  = "TAo TAc MGo MGc OSo OSc OPo USo DS PAt PAall" // fleet:view_trucks, drivers:view (DS: role user)
		boards    = "TAo TAc MGo MGc OSo OSc OPo CU DS PAt PAall"  // operations:view_* of customer and dispatcher scope
		globalMg  = "TAo MGo PAt PAall"                            // global key held by TA MG: own fleet only
		globalOp  = "TAo MGo OSo OPo PAt PAall"                    // operations:manage_sources
		secTA     = "TAo TAc PA PAt PAall"                         // security key held by TA PA
		secTAsu   = "TAo TAc PA PAt PAall SU SUall"                // ... and SU
		usersView = "TAo TAc MGo MGc PA PAt PAall SU SUall"        // users:view
		status    = "TAo PA PAt PAall SU SUall"                    // security:view_status (global; SU by its platform set)
		release   = "TAo PA PAt PAall"                             // security:manage_mobile_release (global)
		waitlist  = "TAo MGo PA PAt PAall"                         // waitlist:view (global)
		none      = ""
	)
	want := map[string]string{
		"/app/dashboard":                        all,
		"/app/trucks":                           staffAll,
		"/app/trucks/new":                       taMg,
		"/app/trucks/view":                      staffAll,
		"/app/trucks/edit":                      taMg,
		"/app/trucks/renew":                     taMg,
		"/app/trucks/maintenance":               taMgOsOp,
		"/app/truck-assignment":                 taMgOsOp,
		"/app/renewals":                         taMgOsOp,
		"/app/maintenance":                      taMgOsOp,
		"/app/subcontractors":                   globalMg,
		"/app/subcontractors/new":               globalMg,
		"/app/customers":                        globalMg,
		"/app/customers/new":                    globalMg,
		"/app/drivers":                          staffAll,
		"/app/chat":                             taMgOsOp,
		"/app/first-mile":                       boards,
		"/app/line-haul":                        boards,
		"/app/job-assign":                       boards,
		"/app/sources":                          globalOp,
		"/app/driver-monitor":                   boards,
		"/app/incident-reports":                 boards,
		"/app/standby-records":                  boards,
		"/app/security-center":                  secTA,
		"/app/security-center/users":            usersView,
		"/app/security-center/roles":            secTA,
		"/app/security-center/audit":            secTAsu,
		"/app/security-center/api-keys":         secTA,
		"/app/security-center/status":           status,
		"/app/security-center/mobile-clients":   secTAsu,
		"/app/security-center/mobile-release":   release,
		"/app/utilities":                        none,
		"/app/utilities/backfill":               taOnly,
		"/app/utilities/billing-impact":         taOnly,
		"/app/accounting/fuel":                  taMgOs,
		"/app/accounting/other":                 taMgOs,
		"/app/accounting/audit":                 taMgOs,
		"/app/accounting/rate-card":             taMgOs,
		"/app/accounting/income":                taMgOs,
		"/app/accounting/billing-document":      taMg,
		"/app/accounting/billing-result":        taMg,
		"/app/accounting/shopee-express-report": taMg,
		"/app/companies":                        taMg,
		"/app/companies/new":                    taMg, // entry removed (no page): /app/companies governs it
		"/app/settings/company-profile":         taMg,
		"/app/analytics":                        none,
		"/app/payroll":                          taOnly,
		"/app/payroll/config":                   taOnly,
		"/app/payroll/penalties":                taOnly,
		"/app/leave-requests":                   taOnly,
		"/app/holidays":                         taOnly,
		"/app/waitlist":                         waitlist,
		"/app/packages":                         none,
		// added by §10.5
		"/app/unauthorized":                  all,
		"/app/accounting/fuel-price-history": taMgOs,
		"/app/drivers/new":                   taMg,
		"/app/drivers/edit":                  taMg,
		"/app/drivers/view":                  staffAll,
		"/app/chat/with-driver":              taMgOsOp,
	}
	legacy := legacyRoutes(t)
	for path := range legacy {
		if _, ok := want[path]; !ok {
			t.Errorf("legacy route %s has no expectation", path)
		}
	}
	for _, path := range WebRoutePaths() {
		if _, ok := want[path]; !ok {
			t.Errorf("mapped route %s has no expectation", path)
		}
	}
	principals := edgePrincipals()
	for path, holders := range want {
		allowed := strings.Fields(holders)
		for name, p := range principals {
			if got := RouteAllowed(p.Caps, path); got != slices.Contains(allowed, name) {
				t.Errorf("%s × %s: allowed %v, want %v", name, path, got, !got)
			}
		}
	}
}
