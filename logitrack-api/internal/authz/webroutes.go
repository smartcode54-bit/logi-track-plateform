package authz

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"unicode"
)

// webRoutes is ROUTE_CAPABILITIES of the web edge gate (proxy.ts, R39): the 53 entries of
// logitrack-web/lib/capabilities.ts:340-394 re-keyed to the catalog with the changes of Appendix C
// §C.2.7 and main spec §10.5. An empty list means any signed-in principal the gate lets reach the map (not a
// driver-only one): /app/dashboard, /app/unauthorized and, since UI3C, /app/support stay open; any-of otherwise. A path matches exactly, else by its longest mapped
// prefix; an unmapped path is no page and opens for nobody: the gate answers it with "Page not found" (HTTP 404,
// UI3D), a mapped path the principal lacks with the 403 page (/app/unauthorized). That 404 is true only while every
// web page is mapped (TestEveryWebPageIsMapped). It is generated into shared-docs/schemas/capabilities.ts, which
// TW3 turns into lib/routeCapabilities.ts. The gate is a necessary-condition filter: Go authorises
// every request again.
var webRoutes = map[string][]Cap{
	"/app/dashboard":    {},
	"/app/unauthorized": {},
	"/app/support":      {}, // UI3C: help inside the shell for every console principal, customers and partners too (a driver-only one is sent to /app/unauthorized first)

	"/app/trucks":                   {FleetViewTrucks},
	"/app/trucks/new":               {FleetCreateTruck},
	"/app/trucks/view":              {FleetViewTrucks},
	"/app/trucks/edit":              {FleetEditTruck},
	"/app/trucks/renew":             {FleetManageRenewals}, // was fleet:view_renewals (§10.5: the page records renewals)
	"/app/trucks/maintenance":       {FleetManageMaintenance, FleetViewMaintenance},
	"/app/truck-assignment":         {FleetViewAssignments},
	"/app/renewals":                 {FleetViewRenewals},
	"/app/maintenance":              {FleetManageMaintenance, FleetViewMaintenance},
	"/app/subcontractors":           {FleetManageSubcontractors, FleetViewSubcontractors},
	"/app/subcontractors/new":       {FleetManageSubcontractors},
	"/app/subcontractors/[id]":      {FleetManageSubcontractors, FleetViewSubcontractors},
	"/app/subcontractors/[id]/edit": {FleetManageSubcontractors},
	"/app/customers/[id]":           {FleetManageCustomers},
	"/app/customers/[id]/edit":      {FleetManageCustomers},
	"/app/customers":                {FleetManageCustomers},
	"/app/customers/new":            {FleetManageCustomers},
	"/app/drivers":                  {DriversView},
	"/app/drivers/new":              {DriversCreate}, // new (§10.5)
	"/app/drivers/edit":             {DriversEdit},   // new (§10.5)
	"/app/drivers/view":             {DriversView},   // new (§10.5)
	"/app/chat":                     {ChatView},
	"/app/chat/with-driver":         {ChatSend}, // new (§10.5)

	// Task boards: the page key; writes need operations:manage_tasks (buttons, and Go on every write).
	"/app/first-mile":       {OperationsViewFirstMile},
	"/app/line-haul":        {OperationsViewLineHaul},
	"/app/job-assign":       {OperationsViewFirstMile},
	"/app/sources":          {OperationsManageSources},
	"/app/driver-monitor":   {OperationsViewDriverMon},
	"/app/incident-reports": {OperationsViewIncidents},
	"/app/standby-records":  {OperationsViewDriverMon},

	// Security Center: mapped keys instead of admin-only (permissions.ts:55-57).
	"/app/security-center":                {SecurityViewOverview},
	"/app/security-center/users":          {UsersView}, // was security:manage_users, which no longer exists
	"/app/security-center/roles":          {SecurityManageRoles},
	"/app/security-center/audit":          {SecurityViewAudit},
	"/app/security-center/api-keys":       {SecurityManageAPIKeys},
	"/app/security-center/status":         {SecurityViewStatus},
	"/app/security-center/mobile-clients": {SecurityViewMobileClients},
	"/app/security-center/mobile-release": {SecurityManageMobileRel},
	"/app/security-center/tenants":        {PlatformManageTenants}, // T18: platform admins onboard carrier tenants

	// Utilities run backfills with billing impact: accounting:recompute_force (was security:view_overview);
	// /app/utilities itself has no page and is no longer mapped.
	"/app/utilities/backfill":       {AccountingRecomputeForce},
	"/app/utilities/billing-impact": {AccountingRecomputeForce},

	"/app/accounting/fuel":                  {AccountingViewFuel},
	"/app/accounting/fuel-price-history":    {AccountingViewFuel}, // new (§10.5; unmapped today)
	"/app/accounting/other":                 {AccountingViewOther},
	"/app/accounting/audit":                 {AccountingAuditExpense},
	"/app/accounting/rate-card":             {AccountingViewRateCard},
	"/app/accounting/income":                {AccountingViewIncome},
	"/app/accounting/billing-document":      {AccountingBillingDocument},
	"/app/accounting/billing-result":        {AccountingBillingResult}, // status actions: accounting:manage_statements
	"/app/accounting/shopee-express-report": {AccountingShopeeReport},

	"/app/companies":                {CompanyView},
	"/app/settings/company-profile": {CompanyManage},
	"/app/payroll":                  {HRViewPayroll},
	"/app/payroll/config":           {HRManagePayroll},
	"/app/payroll/penalties":        {HRManagePayroll},
	"/app/leave-requests":           {HRViewLeave},
	"/app/holidays":                 {HRManageHolidays},
	"/app/waitlist":                 {WaitlistView},
	// Removed (no page, §10.5): /app/companies/new, /app/analytics, /app/packages, /app/utilities.
}

// WebRoutes returns a copy of the route map of the web edge gate.
func WebRoutes() map[string][]Cap {
	out := make(map[string][]Cap, len(webRoutes))
	for k, v := range webRoutes {
		out[k] = slices.Clone(v)
	}
	return out
}

// WebRoutePaths lists the mapped paths, sorted.
func WebRoutePaths() []string { return slices.Sorted(maps.Keys(webRoutes)) }

// RouteFor returns the mapping that governs path: the exact entry, else the longest mapped prefix at a
// segment boundary. ok is false for an unmapped path, which names no page (the gate answers 404, UI3D).
func RouteFor(path string) (caps []Cap, matched string, ok bool) {
	p, valid := CanonicalWebPath(path)
	if !valid {
		return nil, "", false
	}
	if c, found := webRoutes[p]; found {
		return c, p, true
	}
	segments := strings.Split(p, "/")
	best := ""
	score := -1
	for template := range webRoutes {
		if !strings.Contains(template, "[id]") {
			continue
		}
		parts := strings.Split(template, "/")
		if len(parts) != len(segments) {
			continue
		}
		matches := true
		literal := 0
		for i, part := range parts {
			if part == "[id]" {
				if segments[i] == "" {
					matches = false
				}
			} else if part != segments[i] {
				matches = false
			} else {
				literal++
			}
		}
		if matches && literal > score {
			best = template
			score = literal
		}
	}
	if best != "" {
		return webRoutes[best], best, true
	}
	for {
		i := strings.LastIndexByte(p, '/')
		if i <= 0 {
			return nil, "", false
		}
		p = p[:i]
		if c, found := webRoutes[p]; found && !strings.Contains(p, "[") {
			return c, p, true
		}
	}

}

// RouteAllowed is the edge gate's decision for a principal holding caps: an open route admits every
// signed-in principal, a mapped route needs one of its keys, an unmapped route opens for nobody (the gate
// shows "Page not found" there rather than the 403 page; UI3D). (proxy.ts
// sends a principal whose only role is driver to /app/unauthorized before it consults the map.)
func RouteAllowed(caps CapSet, path string) bool {
	need, _, ok := RouteFor(path)
	if !ok {
		return false
	}
	if len(need) == 0 {
		return true
	}
	for _, k := range need {
		if caps.Has(k) {
			return true
		}
	}
	return false
}

// CanonicalWebPath decodes exactly once and rejects ambiguous routing segments.
func CanonicalWebPath(path string) (string, bool) {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?"+"#"+"\\") {
		return "", false
	}
	path = strings.TrimSuffix(path, "/")
	parts := strings.Split(path, "/")
	for i := 1; i < len(parts); i++ {
		part, err := url.PathUnescape(parts[i])
		if err != nil || part == "" || part == "." || part == ".." || strings.ContainsAny(part, "/\\") || strings.ContainsFunc(part, unicode.IsControl) {
			return "", false
		}
		parts[i] = part
	}
	return strings.Join(parts, "/"), true
}

// SupportRouteAllowed uses the Phase 1 destination map, not ordinary list fallback.
func SupportRouteAllowed(path string) bool {
	p, ok := CanonicalWebPath(path)
	if !ok {
		return false
	}
	switch p {
	case "/app", "/app/dashboard", "/app/trucks", "/app/trucks/view", "/app/trucks/maintenance", "/app/truck-assignment", "/app/drivers", "/app/drivers/view", "/app/renewals", "/app/maintenance", "/app/subcontractors", "/app/support", "/app/unauthorized":
		return true
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	return len(parts) == 3 && parts[0] == "app" && parts[1] == "subcontractors" && parts[2] != "new"
}

// ValidateWebRouteTemplates rejects equally specific templates that overlap.
func ValidateWebRouteTemplates(routes map[string][]Cap) error {
	for a := range routes {
		for b := range routes {
			if a >= b || !strings.Contains(a, "[id]") || !strings.Contains(b, "[id]") {
				continue
			}
			as, bs := strings.Split(a, "/"), strings.Split(b, "/")
			if len(as) != len(bs) {
				continue
			}
			overlap := true
			ac, bc := 0, 0
			for i := range as {
				if as[i] != "[id]" {
					ac++
				}
				if bs[i] != "[id]" {
					bc++
				}
				if as[i] != "[id]" && bs[i] != "[id]" && as[i] != bs[i] {
					overlap = false
				}
			}
			if overlap && ac == bc {
				return fmt.Errorf("ambiguous web templates %s and %s", a, b)
			}
		}
	}
	return nil
}

var platformWebRoutes = map[string]bool{
	"/app/security-center": true, "/app/security-center/users": true, "/app/security-center/roles": true,
	"/app/security-center/audit": true, "/app/security-center/api-keys": true, "/app/security-center/status": true,
	"/app/security-center/mobile-clients": true, "/app/security-center/mobile-release": true,
	"/app/security-center/tenants": true, "/app/waitlist": true,
}

// RouteAllowedInPlane is for explicitly selected platform controls. Tenant pages
// and write affordances never inherit the separate platform capability plane.
func RouteAllowedInPlane(tenant, platform CapSet, path, plane string) bool {
	_, matched, ok := RouteFor(path)
	if !ok {
		return false
	}
	if plane == "platform" {
		if !platformWebRoutes[matched] {
			return false
		}
		return RouteAllowed(tenant.Union(platform), path)
	}
	if plane != "tenant" {
		return false
	}
	return RouteAllowed(tenant, path)
}
