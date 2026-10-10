package authz

import (
	"maps"
	"slices"
	"strings"
)

// webRoutes is ROUTE_CAPABILITIES of the web edge gate (proxy.ts, R39): the 53 entries of
// logitrack-web/lib/capabilities.ts:340-394 re-keyed to the catalog with the changes of Appendix C
// §C.2.7 and main spec §10.5. An empty list means any signed-in principal (/app/dashboard and
// /app/unauthorized stay open); any-of otherwise. A path matches exactly, else by its longest mapped
// prefix; an unmapped path is denied. It is generated into shared-docs/schemas/capabilities.ts, which
// TW3 turns into lib/routeCapabilities.ts. The gate is a necessary-condition filter: Go authorises
// every request again.
var webRoutes = map[string][]Cap{
	"/app/dashboard":    {},
	"/app/unauthorized": {},

	"/app/trucks":             {FleetViewTrucks},
	"/app/trucks/new":         {FleetCreateTruck},
	"/app/trucks/view":        {FleetViewTrucks},
	"/app/trucks/edit":        {FleetEditTruck},
	"/app/trucks/renew":       {FleetManageRenewals}, // was fleet:view_renewals (§10.5: the page records renewals)
	"/app/trucks/maintenance": {FleetManageMaintenance},
	"/app/truck-assignment":   {FleetViewAssignments},
	"/app/renewals":           {FleetViewRenewals},
	"/app/maintenance":        {FleetManageMaintenance},
	"/app/subcontractors":     {FleetManageSubcontractors},
	"/app/subcontractors/new": {FleetManageSubcontractors},
	"/app/customers":          {FleetManageCustomers},
	"/app/customers/new":      {FleetManageCustomers},
	"/app/drivers":            {DriversView},
	"/app/drivers/new":        {DriversCreate}, // new (§10.5)
	"/app/drivers/edit":       {DriversEdit},   // new (§10.5)
	"/app/drivers/view":       {DriversView},   // new (§10.5)
	"/app/chat":               {ChatView},
	"/app/chat/with-driver":   {ChatSend}, // new (§10.5)

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
// segment boundary. ok is false for an unmapped path, which the gate denies.
func RouteFor(path string) (caps []Cap, matched string, ok bool) {
	p := strings.TrimSuffix(path, "/")
	for {
		if c, found := webRoutes[p]; found {
			return c, p, true
		}
		i := strings.LastIndexByte(p, '/')
		if i <= 0 {
			return nil, "", false
		}
		p = p[:i]
	}
}

// RouteAllowed is the edge gate's decision for a principal holding caps: an open route admits every
// signed-in principal, a mapped route needs one of its keys, an unmapped route is denied. (proxy.ts
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
