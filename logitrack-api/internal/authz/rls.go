package authz

import (
	"slices"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// RLS sets exact selected-tenant, read-only reach in support, with no stewardship or inherited axes.
// Members, scope principals and API keys retain their normal request GUCs.
func (p *Principal) RLS() db.RLS {
	if p == nil {
		return db.RLS{} // fails closed; db.WithPrincipal refuses a nil *Principal before calling RLS
	}
	r := db.RLS{UserID: p.UserID, Steward: p.Steward}
	switch {
	case IsSupport(p):
		return db.RLS{UserID: p.UserID, TenantID: p.TenantID, Role: string(TenantAdmin), ReadOnly: true}
	case p.IsMachine():
		if p.APIKeyID != nil {
			r.UserID = *p.APIKeyID
		}
		if p.TenantID != nil {
			r.TenantID, r.Role = p.TenantID, string(User)
		}
		return r
	}
	if p.TenantID != nil {
		r.TenantID, r.Role = p.TenantID, string(p.TenantRole)
		if p.TenantRole == Driver {
			r.DriverID = p.DriverID
		} else {
			r.SubtenantIDs = slices.Clone(p.SubtenantIDs)
		}
	} else if p.IsCustomerScope() {
		r.Role = string(ScopeCustomer)
	}
	if len(p.PartyIDs) > 0 && (p.Dispatcher || p.IsCustomerScope()) {
		r.CustomerIDs = slices.Clone(p.PartyIDs)
	}
	r.Dispatcher = p.Dispatcher
	return r
}

// TenantReach matches the tenant set admitted by RLS: exact in support, membership plus contractor
// reach for staff, and empty for driver/customer/platform-only principals. tenants is never nil.
func (p *Principal) TenantReach() (all bool, tenants []uuid.UUID) {
	r := p.RLS()
	tenants = []uuid.UUID{}
	if r.Bypass {
		return true, tenants
	}
	if r.TenantID != nil && TenantRole(r.Role).IsStaff() {
		tenants = append(append(tenants, *r.TenantID), r.SubtenantIDs...)
	}
	return false, tenants
}

var _ db.Principal = (*Principal)(nil)
