// Package authz holds the request principal and, from T07, the capability catalog, role defaults,
// overrides and the RequireCap / RequireTenant middleware (Appendix C §C.2). T05 creates the identity
// half of the principal: what a verified access token says. T07 adds the resolved capabilities (Caps),
// the tenant kind and steward flag, cross-tenant acting (ActOnTenant, ActOnAll) and API-key principals.
package authz

import (
	"slices"

	"github.com/google/uuid"
)

// TenantRole is a membership role (memberships.role, Appendix C §C.2.2).
type TenantRole string

// Tenant roles; the CHECK memberships_tenant_role_check holds the same six values.
const (
	TenantAdmin    TenantRole = "tenant_admin"
	Manager        TenantRole = "manager"
	OperationStaff TenantRole = "operation_staff"
	Operator       TenantRole = "operator"
	User           TenantRole = "user"
	Driver         TenantRole = "driver"
)

// PlatformRole is a user_platform_roles.role: the platform axis, never merged with tenant roles.
type PlatformRole string

// Platform roles.
const (
	PlatformAdmin PlatformRole = "platform_admin"
	Support       PlatformRole = "support"
)

// Principal is the authenticated caller of one request, built from a verified Go access token
// (claims sub, sid, ver, tid, rol, plt, dsp, drv, cs, amr, jti; Appendix C §C.4.1). Capabilities are
// never in the token: T07 resolves them per request.
type Principal struct {
	UserID      uuid.UUID // sub
	SessionID   uuid.UUID // sid
	AuthVersion int32     // ver
	AMR         string    // "pwd" | "google"
	TokenID     string    // jti, log correlation only

	TenantID   *uuid.UUID     // tid; nil for customer-scope and platform-only principals
	TenantRole TenantRole     // rol; "" when TenantID is nil
	Platform   []PlatformRole // plt
	Dispatcher bool           // dsp
	DriverID   *uuid.UUID     // drv; set only when TenantRole is driver
	PartyIDs   []uuid.UUID    // cs: billing_parties.id of the customer scopes and the dispatcher grant
}

// HasPlatform reports whether the principal holds platform role r.
func (p *Principal) HasPlatform(r PlatformRole) bool { return slices.Contains(p.Platform, r) }

// EffectiveTenant is the tenant the request acts in. Until T07 adds X-Act-On-Tenant it is the token's tid.
func (p *Principal) EffectiveTenant() *uuid.UUID { return p.TenantID }
