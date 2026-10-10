package sse

import (
	"slices"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	rt "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// tenantFamily is one tenant:{tid}:{family} topic and the capabilities that subscribe to it (any-of,
// Appendix B §B.4.2).
type tenantFamily struct {
	family string
	caps   []authz.Cap
}

// tenantFamilies are the capability-gated tenant topics. tasks and trips go to dispatchers as
// dispatch:tasks / dispatch:trips instead; config (every member) is handled apart.
var tenantFamilies = []tenantFamily{
	{rt.FamilyTasks, []authz.Cap{authz.OperationsViewFirstMile, authz.OperationsViewLineHaul}},
	{rt.FamilyTrips, []authz.Cap{authz.OperationsViewDriverMon}},
	{rt.FamilyChats, []authz.Cap{authz.ChatView}},
	{rt.FamilyFleet, []authz.Cap{authz.FleetViewTrucks}},
	{rt.FamilyHR, []authz.Cap{authz.HRViewPayroll, authz.HRViewLeave}},
	{rt.FamilyExpenses, []authz.Cap{authz.AccountingAuditExpense}},
	{rt.FamilyBilling, []authz.Cap{authz.AccountingViewRateCard, authz.AccountingBillingDocument, authz.AccountingBillingResult}},
	{rt.FamilyVehicleLocations, []authz.Cap{authz.FleetViewLiveMap}},
}

func canAny(p *authz.Principal, caps ...authz.Cap) bool {
	return slices.ContainsFunc(caps, p.Can)
}

// isStaff reports a staff principal (Appendix B §B.4.2 "every staff principal"): a platform role, a staff
// tenant role, or a dispatcher grant. Drivers, customer-scope principals and machine keys are not.
func isStaff(p *authz.Principal) bool {
	if p.IsMachine() {
		return false
	}
	return p.IsPlatform() || p.EffectiveRole().IsStaff() || p.Dispatcher
}

// WebTopics is the implicit topic set of GET /v1/events for a principal resolved by iam.RBAC (main spec
// §8.2, Appendix B §B.4.2), sorted:
//
//   - user:{uid} always; driver:{did} for a driver principal;
//   - global for every staff principal; platform:security with security:view_audit for platform
//     principals and own-fleet staff;
//   - dispatch:tasks (operations:view_first_mile | operations:view_line_haul | dispatch:view_operations)
//     and dispatch:trips (operations:view_driver_monitor | dispatch:view_operations) for a dispatcher,
//     instead of the tenant tasks and trips topics;
//   - per effective tenant, the families of tenantFamilies the principal's capabilities open, and
//     tenant:{tid}:config for its members; staff with contractor reach (R60) also get the same families
//     (config excepted) of every sub-tenant, the rows RLS lets them read;
//   - nothing tenant-scoped for a customer-scope principal or under X-Act-On-Tenant: *.
func WebTopics(p *authz.Principal) []string {
	out := []string{rt.UserTopic(p.UserID.String())}
	if p.DriverID != nil {
		out = append(out, rt.DriverTopic(p.DriverID.String()))
	}
	staff := isStaff(p)
	if staff {
		out = append(out, rt.TopicGlobal)
	}
	if p.Can(authz.SecurityViewAudit) && (p.IsPlatform() || p.TenantKind == authz.TenantKindOwnFleet) {
		out = append(out, rt.TopicPlatformSecurity)
	}
	if p.Dispatcher {
		if canAny(p, authz.OperationsViewFirstMile, authz.OperationsViewLineHaul, authz.DispatchViewOperations) {
			out = append(out, rt.TopicDispatchTasks)
		}
		if canAny(p, authz.OperationsViewDriverMon, authz.DispatchViewOperations) {
			out = append(out, rt.TopicDispatchTrips)
		}
	}
	if tid := p.EffectiveTenant(); tid != nil && !p.IsCustomerScope() && !p.IsMachine() {
		tenants := []uuid.UUID{*tid}
		if p.EffectiveRole().IsStaff() {
			tenants = append(tenants, p.SubtenantIDs...)
		}
		for _, f := range tenantFamilies {
			if p.Dispatcher && (f.family == rt.FamilyTasks || f.family == rt.FamilyTrips) {
				continue
			}
			if !canAny(p, f.caps...) {
				continue
			}
			for _, t := range tenants {
				out = append(out, rt.TenantTopic(t.String(), f.family))
			}
		}
		if p.EffectiveRole() != "" {
			out = append(out, rt.TenantTopic(tid.String(), rt.FamilyConfig))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// MobileTopics is the implicit topic set of GET /v1/mobile/events (Appendix B §B.4.1): user:{uid} and,
// for a driver, driver:{did}. Chats are explicit.
func MobileTopics(p *authz.Principal) []string {
	out := []string{rt.UserTopic(p.UserID.String())}
	if p.DriverID != nil {
		out = append(out, rt.DriverTopic(p.DriverID.String()))
	}
	slices.Sort(out)
	return out
}
