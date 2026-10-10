package iam

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// Security event types written by the users and tenants administration (Appendix C §C.4.13, R85), each in
// the transaction of its change.
const (
	EventUserCreated           = "user_created"
	EventUserUpdated           = "user_updated" // T19 refinement: PATCH /v1/users/{id} (display name, email)
	EventUserInvited           = "user_invited"
	EventUserRoleChanged       = "user_role_changed"
	EventUserScopeChanged      = "user_scope_changed"
	EventUserDisabled          = "user_disabled"
	EventUserEnabled           = "user_enabled"
	EventUserSessionsRevoked   = "user_sessions_revoked"
	EventUserTemporaryPassword = "user_password_temporary_issued"
	EventDriverLinked          = "driver_linked"
	EventDriverUnlinked        = "driver_unlinked"
	EventPlatformRoleGranted   = "platform_role_granted"
	EventPlatformRoleRevoked   = "platform_role_revoked"
	EventTenantCreated         = "tenant_created"
	EventTenantUpdated         = "tenant_updated"
	EventTenantContractor      = "tenant_contractor_changed"
	EventPasswordChanged       = "password_changed"
)

// Outbox routing keys of the administration (Appendix B §B.5.2). user.sessions_revoked is emitted by auth
// (RevokeInTx) for every claims change.
const (
	RouteUserCreated   = "user.created"
	RouteUserInvited   = "user.invited"
	RouteTenantCreated = "tenant.created"
	RouteTenantUpdated = "tenant.updated"
)

// Stable error codes of the administration besides the shared ones (Appendix B §B.1.5).
const (
	CodeAlreadyExists      = "already_exists"
	CodeFailedPrecondition = "failed_precondition"
	CodeTooManyScopes      = "too_many_scopes"
)

// MaxScopes is the most billing parties a user may hold across its scopes: the JWT cs claim carries all of
// them (Appendix C §C.4.1, B.1.5 too_many_scopes).
const MaxScopes = 20

// Paging of the administration lists (Appendix B §B.1.5).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Files signs the photo URL of a user (internal/storage implements it); optional.
type Files interface {
	SignedURL(ctx context.Context, fileID uuid.UUID, ttl time.Duration) (string, error)
}

// Admin is the users and tenants administration of T19 (Appendix B §B.2.4, §B.2.5, Appendix C §C.8):
// PostgreSQL is the users writer from P0 (R49). Every write runs in one db.WithSystem transaction after Go
// authorization: the rows, auth_version and session revocation through auth (RevokeInTx, SetStatusInTx,
// SetTemporaryPasswordInTx, MirrorNewUserInTx, MirrorEmailInTx: the Firebase mirror is written before
// COMMIT), the security_events row of Appendix C §C.4.13 and the outbox events; the auth post-commit
// writes follow the COMMIT (auth.Service.Apply).
//
// Reach. A read names the users it may return explicitly: under X-Act-On-Tenant: * every user; otherwise
// the users with a membership in the request's tenant reach (the effective tenant and, for staff, the
// carriers that work for it: the RLS rule p_staff_read of users), and for a steward (own-fleet staff,
// platform_admin) also the accounts that belong to no tenant and hold no platform role (customer-scope
// accounts, whose parties are global master data, R60, and imported accounts still without a role). A
// platform_admin writes any user and any tenant's memberships without the header
// (Appendix B §B.1.4); everyone else writes only inside the same reach. A user outside the reach does not
// exist for the caller (404). Inside it, a route that targets a user also needs a user the caller outranks
// (outranks, 403): no platform role or dispatcher grant, every membership in reach, tenant_admin only where the
// caller administers. Admin routes never act on the caller itself (Appendix C §C.4.7).
type Admin struct {
	pool  db.Beginner
	auth  *auth.Service
	files Files
	log   zerolog.Logger
	now   func() time.Time
}

// AdminDeps are the collaborators of the administration.
type AdminDeps struct {
	// Pool is the logitrack_app pool; identity tables are read and written in db.WithSystem.
	Pool db.Beginner
	// Auth performs the revocations, status and password changes and the Firebase mirror.
	Auth  *auth.Service
	Files Files // optional
	Log   zerolog.Logger
	Now   func() time.Time // optional, defaults to time.Now
}

// NewAdmin builds the administration.
func NewAdmin(d AdminDeps) (*Admin, error) {
	if d.Pool == nil || d.Auth == nil {
		return nil, errors.New("iam: the administration needs a pool and the auth service")
	}
	a := &Admin{pool: d.Pool, auth: d.Auth, files: d.Files, log: d.Log, now: d.Now}
	if a.now == nil {
		a.now = time.Now
	}
	return a, nil
}

func (a *Admin) clock() time.Time { return a.now().UTC().Truncate(time.Microsecond) }

// call is one request: its principal and request id.
type call struct {
	ctx context.Context
	p   *authz.Principal
	rid string
}

// reach is the set of users a request may see or change (see Admin).
type reach struct {
	all       bool
	tenants   []uuid.UUID
	scopeOnly bool
}

// readReach: everyone under X-Act-On-Tenant: *; else the members of the tenant reach of a staff principal
// (the effective tenant plus contractor reach) and, for a steward, the accounts without a membership.
func readReach(p *authz.Principal) reach {
	if p.ActOnAll {
		return reach{all: true}
	}
	r := reach{scopeOnly: p.Steward && !p.IsMachine(), tenants: []uuid.UUID{}}
	if t := p.EffectiveTenant(); t != nil && p.EffectiveRole().IsStaff() {
		r.tenants = append(append(r.tenants, *t), p.SubtenantIDs...)
	}
	return r
}

// writeReach: a platform_admin writes everywhere without X-Act-On-Tenant (Appendix B §B.1.4); X-Act-On-Tenant:
// * is read-only (Authorize refused the write already); everyone else writes inside its read reach.
func writeReach(p *authz.Principal) reach {
	if p.ActOnAll {
		return reach{tenants: []uuid.UUID{}}
	}
	if p.HasPlatform(authz.PlatformAdmin) && !p.IsMachine() {
		return reach{all: true}
	}
	return readReach(p)
}

func (r reach) hasTenant(t uuid.UUID) bool { return r.all || slices.Contains(r.tenants, t) }

// isTenantAdminOf: the principal may grant or change tenant_admin in tenant t: a platform_admin, or the
// tenant_admin of t itself (Appendix B §B.2.4: no self-escalation; only tenant_admin or platform grants it).
func isTenantAdminOf(p *authz.Principal, t uuid.UUID) bool {
	if p.HasPlatform(authz.PlatformAdmin) && !p.IsMachine() {
		return true
	}
	eff := p.EffectiveTenant()
	return eff != nil && *eff == t && p.EffectiveRole() == authz.TenantAdmin
}

// canAssign is CanAssignRole (Appendix C §C.8): users:assign_role, and tenant_admin granted or changed only by
// a tenant_admin of that tenant or a platform_admin.
func canAssign(p *authz.Principal, t uuid.UUID, newRole, oldRole authz.TenantRole) error {
	if !p.Can(authz.UsersAssignRole) {
		return authz.ErrPermissionDenied("missing capability", authz.UsersAssignRole)
	}
	if (newRole == authz.TenantAdmin || oldRole == authz.TenantAdmin) && !isTenantAdminOf(p, t) {
		return authz.ErrPermissionDenied("only a tenant_admin of the tenant or a platform admin grants or changes tenant_admin").
			WithDetails(map[string]any{"reason": ReasonTenantAdminOnly})
	}
	return nil
}

// notSelf refuses an admin route that targets the caller (Appendix C §C.4.7, kept from authSessions.ts:23-28
// and users.ts:318-320): users manage themselves through /v1/me*.
func notSelf(p *authz.Principal, target uuid.UUID) error {
	if p.UserID == target {
		return authz.ErrPermissionDenied("an admin route cannot act on the caller; use /v1/me").
			WithDetails(map[string]any{"reason": "self"})
	}
	return nil
}

// stewardOrPlatform: customer-scope accounts are global (their parties are global master data, R60), so only
// a steward (own-fleet staff) or a platform_admin creates them or changes customer scopes.
func stewardOrPlatform(p *authz.Principal) error {
	if (p.Steward || p.HasPlatform(authz.PlatformAdmin)) && !p.IsMachine() {
		return nil
	}
	return authz.ErrPermissionDenied("customer scopes are managed by the own fleet or a platform admin").
		WithDetails(map[string]any{"reason": ReasonStewardOnly})
}

// tx runs fn in one WithSystem transaction and applies the collected auth post-commit writes after COMMIT.
func (a *Admin) tx(ctx context.Context, fn func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error) error {
	var pcs []*auth.PostCommit
	err := db.WithSystem(ctx, a.pool, nil, func(tx pgx.Tx) error {
		pcs = pcs[:0]
		return fn(tx, iamdb.New(tx), func(pc *auth.PostCommit) {
			if pc != nil {
				pcs = append(pcs, pc)
			}
		})
	})
	if err != nil {
		return mapDBError(err)
	}
	for _, pc := range pcs {
		a.auth.Apply(ctx, pc)
	}
	return nil
}

// read runs fn in a WithSystem transaction (the identity tables are platform-only under RLS).
func (a *Admin) read(ctx context.Context, fn func(q *iamdb.Queries) error) error {
	err := db.WithSystem(ctx, a.pool, nil, func(tx pgx.Tx) error { return fn(iamdb.New(tx)) })
	return mapDBError(err)
}

// claimsChanged bumps auth_version and announces claims_changed (the user's clients refresh and stay
// signed in, R50); the Firebase claims follow while the bridge mirrors. Call after the rows changed.
func (a *Admin) claimsChanged(c call, tx pgx.Tx, uid uuid.UUID, keep func(*auth.PostCommit)) error {
	pc, _, err := a.auth.RevokeInTx(c.ctx, tx, auth.Revocation{
		UserID: uid, Reason: auth.RevokeClaimsChanged, BumpVersion: true, RequestID: c.rid,
	})
	if err != nil {
		return err
	}
	keep(pc)
	return nil
}

// audit appends the change's security_events row in its transaction (no audit, no change).
func (a *Admin) audit(c call, tx pgx.Tx, eventType, summary string, target *uuid.UUID, tenant *uuid.UUID, details map[string]any) error {
	actor := c.p.UserID
	sev := security.SeverityInfo
	switch eventType {
	case EventPlatformRoleGranted, EventPlatformRoleRevoked, EventUserDisabled, EventUserSessionsRevoked,
		EventUserTemporaryPassword:
		sev = security.SeverityWarning
	}
	return security.Append(c.ctx, tx, security.Event{
		EventType: eventType, Severity: sev, Summary: summary, Details: details,
		ActorUserID: &actor, TargetUserID: target, TenantID: tenant, RequestID: c.rid, OccurredAt: a.clock(),
	})
}

// emit appends an outbox event in the change's transaction.
func emit(c call, tx pgx.Tx, routingKey, aggType, aggID string, tenant *uuid.UUID, payload any) error {
	var headers map[string]string
	if c.rid != "" {
		headers = map[string]string{"requestId": c.rid}
	}
	_, err := outbox.Append(c.ctx, tx, outbox.Event{
		RoutingKey: routingKey, AggregateType: aggType, AggregateID: aggID, TenantID: tenant, Payload: payload, Headers: headers,
	})
	return err
}

// --- errors ---------------------------------------------------------------------------------------------

func errInvalid(fields ...httpx.FieldViolation) *httpx.Error {
	return httpx.ErrInvalidArgument(fields...)
}

func violation(field, reason string) httpx.FieldViolation {
	return httpx.FieldViolation{Field: field, Reason: reason}
}

func errAlreadyExists(field, reason string) *httpx.Error {
	return httpx.NewError(http.StatusConflict, CodeAlreadyExists, "already exists").
		WithDetails(map[string]any{"field": field, "reason": reason})
}

func errFailedPrecondition(reason, msg string) *httpx.Error {
	return httpx.NewError(http.StatusConflict, CodeFailedPrecondition, msg).WithDetails(map[string]any{"reason": reason})
}

func errTooManyScopes() *httpx.Error {
	return httpx.NewError(http.StatusUnprocessableEntity, CodeTooManyScopes, "a user holds at most 20 billing parties").
		WithDetails(map[string]any{"field": "billingPartyIds", "max": MaxScopes})
}

// uniqueFields maps the unique constraints a write can meet to the request field that collided.
var uniqueFields = map[string]string{
	"users_email": "email", "tenants_code": "code", "drivers_user": "driverId", "device_tokens_token": "token",
}

// mapDBError turns the constraint violations a race can still reach into the API's codes; anything else is
// returned as is (500 for an unknown database error).
func mapDBError(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Code {
	case "23505":
		if f, ok := uniqueFields[pe.ConstraintName]; ok {
			return errAlreadyExists(f, "taken").Wrap(err)
		}
		return httpx.NewError(http.StatusConflict, CodeAlreadyExists, "already exists").Wrap(err)
	case "23503":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "not_found")).Wrap(err)
	case "23514":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "invalid")).Wrap(err)
	}
	return err
}

// --- keyset cursors ---------------------------------------------------------------------------------------

// cursor is the opaque keyset position (sort value, id) of Appendix B §B.1.5, base64url JSON. T is nil for
// a NULL sort value (a user who never signed in, under sort=last_login_at).
type cursor struct {
	T  *time.Time `json:"t"`
	ID uuid.UUID  `json:"id"`
}

func encodeCursor(t *time.Time, id uuid.UUID) string {
	b, _ := json.Marshal(cursor{T: t, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string, nullable bool) (cursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, false
	}
	var c cursor
	if json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil || (!nullable && c.T == nil) {
		return cursor{}, false
	}
	return c, true
}
