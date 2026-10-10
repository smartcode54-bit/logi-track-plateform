package authz

// ScopeKind is a user_scopes.kind: the scope axis (R86: customer and dispatcher only).
type ScopeKind string

// Scope kinds.
const (
	ScopeCustomer   ScopeKind = "customer"
	ScopeDispatcher ScopeKind = "dispatcher"
)

// TenantRoles are the six membership roles in the order of memberships_tenant_role_check.
var TenantRoles = []TenantRole{TenantAdmin, Manager, OperationStaff, Operator, User, Driver}

// PlatformRoles are the two platform roles.
var PlatformRoles = []PlatformRole{PlatformAdmin, Support}

// IsStaff reports whether r is a staff role (every tenant role but driver): the roles app_is_staff()
// accepts.
func (r TenantRole) IsStaff() bool {
	switch r {
	case TenantAdmin, Manager, OperationStaff, Operator, User:
		return true
	}
	return false
}

// Valid reports whether r is one of the six tenant roles.
func (r TenantRole) Valid() bool { return r == Driver || r.IsStaff() }

// rows returns the catalog keys at the given 1-based rows of Appendix C §C.2.3; lo-hi pairs are written
// as span(lo, hi).
func rows(spec ...[]int) CapSet {
	var s CapSet
	for _, part := range spec {
		for _, n := range part {
			s.Add(catalog[n-1].Key)
		}
	}
	return s
}

func span(lo, hi int) []int {
	out := make([]int, 0, hi-lo+1)
	for n := lo; n <= hi; n++ {
		out = append(out, n)
	}
	return out
}

func at(n ...int) []int { return n }

// Default role sets of Appendix C §C.2.4, by catalog row. The class rule (global keys only for
// stewards) is applied by Resolve, not here: tenant_admin holds 64 keys in every tenant and they are
// all effective only in the own fleet.
var (
	tenantDefaults = map[TenantRole]CapSet{
		TenantAdmin: rows(span(1, 64)),
		Manager: rows(span(1, 11), span(12, 15), span(17, 20), span(21, 24), span(26, 29), span(30, 40),
			at(43, 44, 50, 51, 52, 63, 64)),
		OperationStaff: rows(at(1, 4, 6, 9, 10, 11, 12, 17, 18, 20, 21, 22, 23, 24, 26, 27, 28, 29, 30, 32, 34, 35, 37, 64)),
		Operator:       rows(at(1, 4, 6, 9, 10, 11, 12, 17, 18, 20, 21, 22, 23, 24, 26, 27, 28)),
		User:           rows(at(1, 12)),
		Driver:         rows(span(65, 76)),
	}
	scopeDefaults = map[ScopeKind]CapSet{
		ScopeCustomer:   rows(at(21, 22, 26, 28)),
		ScopeDispatcher: rows(at(11, 21, 22, 26, 28, 77)),
	}
	platformDefaults = map[PlatformRole]CapSet{
		PlatformAdmin: rows(span(52, 63), span(78, 81)),
		Support:       rows(at(52, 58, 60, 61, 80)),
	}
)

// TenantDefaults is the catalog default set of a tenant role, before overrides and the steward rule.
func TenantDefaults(r TenantRole) CapSet { return tenantDefaults[r] }

// ScopeDefaults is the fixed set of a scope axis (never overridable).
func ScopeDefaults(k ScopeKind) CapSet { return scopeDefaults[k] }

// PlatformDefaults is the fixed set of a platform role (never overridable).
func PlatformDefaults(r PlatformRole) CapSet { return platformDefaults[r] }

// RoleDefaults is one row of the default matrix of GET /v1/roles.
type RoleDefaults struct {
	Role         string `json:"role"`
	Axis         string `json:"axis"` // tenant | scope | platform
	Capabilities []Cap  `json:"capabilities"`
}

// DefaultMatrix lists the default sets of every role and axis: the six tenant roles, the two scope
// kinds and the two platform roles (Appendix C §C.2.4).
func DefaultMatrix() []RoleDefaults {
	out := make([]RoleDefaults, 0, len(TenantRoles)+len(scopeDefaults)+len(PlatformRoles))
	for _, r := range TenantRoles {
		out = append(out, RoleDefaults{Role: string(r), Axis: "tenant", Capabilities: tenantDefaults[r].Keys()})
	}
	for _, k := range []ScopeKind{ScopeCustomer, ScopeDispatcher} {
		out = append(out, RoleDefaults{Role: string(k), Axis: "scope", Capabilities: scopeDefaults[k].Keys()})
	}
	for _, r := range PlatformRoles {
		out = append(out, RoleDefaults{Role: string(r), Axis: "platform", Capabilities: platformDefaults[r].Keys()})
	}
	return out
}
