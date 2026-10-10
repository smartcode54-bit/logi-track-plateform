package authz

import "github.com/google/uuid"

// Override is one role_capability_overrides row of the role being resolved (Appendix C §C.2.5).
type Override struct {
	TenantID *uuid.UUID // nil = platform-wide default override
	Cap      Cap
	Allowed  bool
}

// Overridable reports whether an override row may change key for role. tenantRow is true for a row of
// one tenant and false for a platform-wide row. tenant_admin is not overridable (a tenant cannot lock
// itself out of security:manage_roles); platform and scope keys never are; users:assign_role only in a
// platform-wide row; a driver row changes only self keys (mobile:*), a staff row only tenant and global
// keys; a key outside the catalog is ignored (Appendix C §C.2.5). Global keys are allowed here and
// stripped later for non-stewards, so an override can never make one effective outside the own fleet
// (R60). The matrix API (T51) rejects the same rows with 422 capability_not_overridable.
func Overridable(role TenantRole, key Cap, tenantRow bool) bool {
	if role == TenantAdmin || !role.Valid() {
		return false
	}
	switch ClassOf(key) {
	case ClassTenant, ClassGlobal:
		if role == Driver {
			return false
		}
		return key != UsersAssignRole || !tenantRow
	case ClassSelf:
		return role == Driver
	}
	return false // platform, scope, unknown
}

// RoleCaps is the effective set of a tenant role in one tenant: the catalog default, then every
// platform-wide override, then every override of the tenant (tenant rows win), then the steward rule:
// global keys only when steward is true (Appendix C §C.2.4, R60). Rows that are not Overridable are
// ignored. RoleCaps(role, true, rows) is the set before the steward rule, which ApplySteward finishes.
func RoleCaps(role TenantRole, steward bool, overrides []Override) CapSet {
	s := tenantDefaults[role]
	for _, tenantRows := range []bool{false, true} {
		for _, o := range overrides {
			if (o.TenantID != nil) != tenantRows || !Overridable(role, o.Cap, tenantRows) {
				continue
			}
			if o.Allowed {
				s.Add(o.Cap)
			} else {
				s.Remove(o.Cap)
			}
		}
	}
	return ApplySteward(s, steward)
}

// ApplySteward removes the global keys from s unless the principal is a steward (R60).
func ApplySteward(s CapSet, steward bool) CapSet {
	if steward {
		return s
	}
	return s.Minus(globalSet)
}

// PlatformCaps is the union of the fixed sets of p's platform roles.
func PlatformCaps(p *Principal) CapSet {
	var s CapSet
	for _, r := range p.Platform {
		s = s.Union(platformDefaults[r])
	}
	return s
}

var globalSet = NewCapSet(GlobalCaps()...)

// IsSteward decides the steward flag of a principal whose effective tenant has kind tenantKind (R60):
// platform_admin; a principal acting as a tenant through X-Act-On-Tenant: <uuid> (C.3.9); a staff role
// in the own-fleet tenant; a platform-level API key (C.4.11). Drivers, carrier staff, scope principals
// and tenant API keys are not.
func IsSteward(p *Principal, tenantKind string) bool {
	switch {
	case p.IsMachine():
		return p.TenantID == nil
	case p.HasPlatform(PlatformAdmin), p.ActOnTenant != nil:
		return true
	}
	return tenantKind == TenantKindOwnFleet && p.EffectiveRole().IsStaff()
}

// Effective is the effective capability set of p (Appendix C §C.2.4): roleCaps (RoleCaps of
// p.EffectiveRole() in p.EffectiveTenant(), the caller loads the overrides) ∪ the fixed scope sets ∪
// the fixed platform sets. Under X-Act-On-Tenant: * a platform_admin also holds the tenant_admin set
// (reads only: the guard admits GET and HEAD and the transaction is READ ONLY), support only its own.
// A machine principal (amr=apikey) holds exactly its key's capabilities that the class rule allows:
// tenant keys only tenant keys, platform keys only global and mobile keys, never platform or scope keys
// (C.4.11).
func Effective(p *Principal, roleCaps CapSet) CapSet {
	if p.IsMachine() {
		var s CapSet
		for _, k := range p.APIKeyCaps {
			switch ClassOf(k) {
			case ClassTenant:
				if p.TenantID != nil {
					s.Add(k)
				}
			case ClassGlobal, ClassSelf:
				if p.TenantID == nil {
					s.Add(k)
				}
			}
		}
		return s
	}
	s := roleCaps
	if p.ActOnAll && p.HasPlatform(PlatformAdmin) {
		s = s.Union(tenantDefaults[TenantAdmin])
	}
	if len(p.PartyIDs) > 0 && !p.Dispatcher {
		s = s.Union(scopeDefaults[ScopeCustomer])
	}
	if p.Dispatcher {
		s = s.Union(scopeDefaults[ScopeDispatcher])
	}
	return s.Union(PlatformCaps(p))
}
