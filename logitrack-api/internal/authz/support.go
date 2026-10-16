package authz

import "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"

// DerivedRead reports keys whose grants come only from their effective old key.
func DerivedRead(key Cap) bool {
	return key == FleetViewMaintenance || key == FleetViewSubcontractors || key == UsersViewSessions
}
func isDerivedRead(key Cap) bool { return DerivedRead(key) }

func deriveTenantReads(s CapSet) CapSet {
	for _, pair := range [][2]Cap{{FleetManageMaintenance, FleetViewMaintenance}, {FleetManageSubcontractors, FleetViewSubcontractors}} {
		s.Remove(pair[1])
		if s.Has(pair[0]) {
			s.Add(pair[1])
		}
	}
	return s
}
func derivePlatformReads(s CapSet) CapSet {
	s.Remove(UsersViewSessions)
	if s.Has(UsersRevokeSessions) {
		s.Add(UsersViewSessions)
	}
	return s
}

// TenantSupportReads is an explicit Phase 1 subset of tenant-admin defaults.
// It is independent of overrides and never includes a platform/scope/write key.
func TenantSupportReads() CapSet {
	defaults := TenantDefaults(TenantAdmin)
	var out CapSet
	for _, key := range []Cap{FleetViewTrucks, FleetViewRenewals, FleetViewAssignments, FleetViewLiveMap, DriversView, DriversViewPII} {
		if defaults.Has(key) {
			out.Add(key)
		}
	}
	if defaults.Has(FleetManageMaintenance) {
		out.Add(FleetViewMaintenance)
	}
	if defaults.Has(FleetManageSubcontractors) {
		out.Add(FleetViewSubcontractors)
	}
	return out
}

// SupportReadOnly is a virtual JWT role, never a memberships.role value.
const SupportReadOnly TenantRole = "support_readonly"

func IsSupport(p *Principal) bool { return p != nil && p.TenantRole == SupportReadOnly }

// RequireTenantWrite is also called by IAM/system/upload/job service adapters.
// It must precede platform shortcuts and transaction/queue/idempotency work.
func RequireTenantWrite(p *Principal) error {
	if IsSupport(p) {
		return httpx.ErrSupportReadOnly()
	}
	return nil
}
