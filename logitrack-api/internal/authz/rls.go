package authz

import (
	"slices"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// RLS is the request context db.WithPrincipal sets for p (Appendix C §C.3.2):
//
//   - X-Act-On-Tenant: * -> app.bypass_tenant=on in a READ ONLY transaction (never writable);
//   - X-Act-On-Tenant: <uuid> -> app.tenant_id = target, app.role = tenant_admin, app.steward = on,
//     contractor reach of the target;
//   - a machine principal (API key) -> app.user_id = the key id (no users row matches it, so no self
//     policy opens); a tenant key acts as role user in its tenant (staff reach), a platform key as a
//     steward without a tenant (C.4.11);
//   - a membership -> app.tenant_id + app.role (+ app.driver_id for a driver; app.subtenant_ids for
//     staff only, so a driver never reaches a sub-tenant);
//   - a customer-scope principal (cs, no tid) -> app.role = customer, app.customer_ids = cs;
//   - a dispatcher -> its own membership plus app.dispatcher = on and app.customer_ids = cs;
//   - a member whose cs holds customer-kind scopes only -> its membership alone: no RLS policy reads
//     app.customer_ids without app.role = customer or app.dispatcher, and Effective grants no scope keys;
//   - a platform principal without the header -> no tenant and no role: platform-shared reads only.
//
// app.steward comes from Steward, which internal/iam resolved (R60). An unresolved principal therefore
// never gets the steward flag or contractor reach: it can only see less.
func (p *Principal) RLS() db.RLS {
	if p == nil {
		return db.RLS{} // fails closed; db.WithPrincipal refuses a nil *Principal before calling RLS
	}
	r := db.RLS{UserID: p.UserID, Steward: p.Steward}
	switch {
	case p.ActOnAll:
		r.Bypass, r.ReadOnly = true, true
		return r
	case p.ActOnTenant != nil:
		r.TenantID, r.Role, r.Steward = p.ActOnTenant, string(TenantAdmin), true
		r.SubtenantIDs = slices.Clone(p.SubtenantIDs)
		return r
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

var _ db.Principal = (*Principal)(nil)
