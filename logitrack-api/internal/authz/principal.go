// Package authz holds the request principal, the capability catalog (81 keys, R73), the role defaults,
// the resolution of a principal's effective capability set (Appendix C §C.2), the route map of the web
// edge gate (§C.2.7), and the Fiber guards RequireCap, RequireTenant, RequirePlatform and RequireSteward
// (§C.2.8). It does no I/O: internal/iam loads the inputs (overrides, the own-fleet tenant, contractor
// reach), resolves every authenticated request and handles X-Act-On-Tenant.
//
// Layers (§C.3.1): capabilities decide which verbs a principal may perform; PostgreSQL RLS decides which
// rows. Principal.RLS turns a resolved principal into the GUCs db.WithPrincipal sets.
package authz

import (
	"slices"
	"time"

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

// Tenant kinds (tenants.kind).
const (
	TenantKindOwnFleet   = "own_fleet"
	TenantKindCarrier    = "carrier"
	TenantKindQuarantine = "quarantine"
)

// AMR values of a principal.
const (
	AMRPassword = "pwd"
	AMRGoogle   = "google"
	AMRFirebase = "firebase" // a cf_shim request carrying the end user's Firebase ID token (C.6.2, T32)
	AMRAPIKey   = "apikey"   // a machine principal from an api_keys row (C.4.11, T32)
)

// APIKeyScope is api_keys.scope (R82): it decides the listener and routes a key may use (T32).
type APIKeyScope string

// API key scopes.
const (
	KeyIntegration      APIKeyScope = "integration"
	KeyScript           APIKeyScope = "script"
	KeyCFShim           APIKeyScope = "cf_shim"
	KeyReleasePublisher APIKeyScope = "release_publisher"
)

// Principal is the authenticated caller of one request. The identity half comes from a verified Go
// access token (claims sub, sid, ver, tid, rol, plt, dsp, drv, cs, amr, jti; Appendix C §C.4.1) or,
// from T32, from an api_keys row; capabilities are never in the token. The rest is resolved per
// request by internal/iam (Resolved reports it): the tenant kind and steward flag, contractor reach,
// the effective capability set, and cross-tenant acting through X-Act-On-Tenant.
type Principal struct {
	UserID      uuid.UUID // sub; the key id for a machine principal without a user (AMRAPIKey)
	SessionID   uuid.UUID // sid; zero for amr=firebase and amr=apikey
	AuthVersion int32     // ver
	AMR         string    // AMRPassword | AMRGoogle | AMRFirebase (a Firebase ID-token principal, T08: no session) | AMRAPIKey
	TokenID     string    // jti, log correlation only
	// TokenExpiresAt is the exp of the access token (zero for principals without one). An SSE stream ends
	// with event: reconnect 30 s before it (main spec §8.1); a mobile SSE ticket's principal carries the
	// end of the access lifetime it was redeemed under (auth.Service.SSETicketPrincipal).
	TokenExpiresAt time.Time

	TenantID   *uuid.UUID     // tid; nil for customer-scope and platform-only principals
	TenantRole TenantRole     // rol; "" when TenantID is nil (and for an API key)
	Platform   []PlatformRole // plt
	Dispatcher bool           // dsp
	DriverID   *uuid.UUID     // drv; set only when TenantRole is driver
	PartyIDs   []uuid.UUID    // cs: billing_parties.id of the customer scopes or the dispatcher grant

	// APIKeyID is set when the request carried X-Api-Key (T32): the key of a machine principal, or the
	// attribution of a cf_shim request made for an end user.
	APIKeyID *uuid.UUID
	// APIKeyScope is the scope of that key.
	APIKeyScope APIKeyScope
	// APIKeyCaps are the key's capabilities (api_keys.capabilities) of a machine principal; Resolve
	// keeps only the catalog keys its class rule allows.
	APIKeyCaps []Cap

	// Resolved per request (internal/iam):

	// TenantKind is the kind of EffectiveTenant(): own_fleet | carrier | quarantine, "" without one.
	TenantKind string
	// Steward is (own-fleet tenant and a staff role) or platform_admin, or acting as a tenant through
	// X-Act-On-Tenant (R60, C.3.9). GUC app.steward.
	Steward bool
	// SubtenantIDs are the carrier tenants whose contractor_tenant_id is the effective tenant (contractor
	// reach, R60, C.3.4); staff only. GUC app.subtenant_ids.
	SubtenantIDs []uuid.UUID
	// Caps is the effective capability set.
	Caps CapSet
	// ActOnTenant is the target of X-Act-On-Tenant: <uuid> (platform_admin acting as that tenant's
	// tenant_admin).
	ActOnTenant *uuid.UUID
	// ActOnAll is X-Act-On-Tenant: * (read-only bypass of a platform principal, GET and HEAD only).
	ActOnAll bool

	resolved bool
}

// Can reports whether the effective set holds c.
func (p *Principal) Can(c Cap) bool { return p.Caps.Has(c) }

// HasPlatform reports whether the principal holds platform role r.
func (p *Principal) HasPlatform(r PlatformRole) bool { return slices.Contains(p.Platform, r) }

// IsPlatform reports whether the principal holds any platform role.
func (p *Principal) IsPlatform() bool { return len(p.Platform) > 0 }

// IsMachine reports whether the principal is an API key without an end user (amr=apikey).
func (p *Principal) IsMachine() bool { return p.AMR == AMRAPIKey }

// EffectiveTenant is the tenant the request acts in: the X-Act-On-Tenant target, else the token's
// tid. nil for customer-scope and platform-only principals and under X-Act-On-Tenant: *.
func (p *Principal) EffectiveTenant() *uuid.UUID {
	if p.ActOnTenant != nil {
		return p.ActOnTenant
	}
	if p.ActOnAll {
		return nil
	}
	return p.TenantID
}

// EffectiveRole is the tenant role the request acts with: tenant_admin under X-Act-On-Tenant: <uuid>,
// else the membership role ("" without one).
func (p *Principal) EffectiveRole() TenantRole {
	if p.ActOnTenant != nil {
		return TenantAdmin
	}
	if p.ActOnAll {
		return ""
	}
	return p.TenantRole
}

// IsCustomerScope reports a customer-scope principal: billing parties in cs, no dispatcher grant and
// no tenant membership (C.1.2). Its RLS role is "customer".
func (p *Principal) IsCustomerScope() bool {
	return len(p.PartyIDs) > 0 && !p.Dispatcher && p.TenantID == nil && !p.IsMachine()
}

// Resolved reports whether internal/iam completed the principal for this request.
func (p *Principal) Resolved() bool { return p.resolved }

// MarkResolved records that the per-request fields are set (internal/iam only).
func (p *Principal) MarkResolved() { p.resolved = true }
