package iam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// EventCrossTenantAccess is the security_events type of every X-Act-On-Tenant request (C.3.9, R85).
const EventCrossTenantAccess = "platform_cross_tenant_access"

// Authorize implements auth.Authorizer: auth.RequireAuth calls it for every authenticated request, so
// no route can skip it. It applies X-Act-On-Tenant (actOnTenant) and then resolves the principal
// (Resolve). Route guards (authz.RequireCap, RequireTenant, ...) and db.WithPrincipal read the result.
func (r *RBAC) Authorize(c fiber.Ctx, p *authz.Principal) error {
	if err := r.actOnTenant(c, p); err != nil {
		return err
	}
	return r.Resolve(c.Context(), p)
}

// actOnTenant handles X-Act-On-Tenant (Appendix C §C.3.9, R4, R85):
//
//   - only on the internal listener (the public one answers 400 header_not_allowed before routing);
//   - only for platform principals: anyone else is 403 permission_denied, without an audit row (they
//     gain nothing);
//   - <tenant uuid>: platform_admin acts as that tenant's tenant_admin; reads (GET, HEAD) need
//     platform:cross_tenant_read, writes platform:cross_tenant_write;
//   - *: read-only bypass for platform_admin and support (platform:cross_tenant_read), GET and HEAD
//     only; the request transaction is READ ONLY with app.bypass_tenant=on (db.WithPrincipal);
//   - a repeated or malformed header is 400 bad_request, an unknown tenant id 404 not_found.
//
// Every X-Act-On-Tenant request of a platform principal on the internal listener writes one
// security_events row platform_cross_tenant_access, whatever its outcome: the decision is made first,
// then the row is committed in its own transaction (before the handler's transaction begins), then the
// refusal is returned or the target applied. So a refused attempt (a wrong method or capability, a
// repeated or malformed value, an unknown tenant id) is on record too; details.outcome says which.
// No audit, no access: when the row cannot be written the request fails (503).
func (r *RBAC) actOnTenant(c fiber.Ctx, p *authz.Principal) error {
	vals := c.Request().Header.PeekAll(authz.HeaderActOnTenant)
	if len(vals) == 0 {
		return nil
	}
	if httpx.ListenerFrom(c) == ingress.Public {
		return httpx.NewError(http.StatusBadRequest, httpx.CodeHeaderNotAllowed, "header not allowed on this listener").
			WithDetails(map[string]any{"header": authz.HeaderActOnTenant})
	}
	if !p.IsPlatform() || p.IsMachine() {
		return authz.ErrPermissionDenied("X-Act-On-Tenant is for platform principals")
	}
	d, err := r.decideActOnTenant(c, p, vals)
	if err != nil {
		return err // the target could not be looked up (PostgreSQL); nothing was decided
	}
	if err := r.auditCrossTenant(c, p, d); err != nil {
		return err
	}
	if d.refusal != nil {
		return d.refusal
	}
	if d.target != nil {
		p.ActOnTenant, p.TenantKind = d.target, d.kind
	} else {
		p.ActOnAll = true
	}
	return nil
}

// Outcomes of an X-Act-On-Tenant request (security_events details.outcome).
const (
	outcomeAccepted      = "accepted"       // the target is applied
	outcomeRefused       = "refused"        // 403: method, platform role or capability
	outcomeRepeated      = "repeated"       // 400: the header was sent more than once
	outcomeMalformed     = "malformed"      // 400: neither a tenant id nor *
	outcomeUnknownTenant = "unknown_tenant" // 404: no such tenant
)

// actOnDecision is what one X-Act-On-Tenant request of a platform principal resolves to.
type actOnDecision struct {
	value   string     // the header value(s) as audited
	outcome string     // outcome* above
	target  *uuid.UUID // an existing tenant; nil for * and for every 400 / 404
	kind    string     // the target's tenants.kind
	refusal error      // returned after the audit row commits; nil when accepted
}

// decideActOnTenant parses the header and applies the method, role and capability rules without
// touching p. The error is an infrastructure failure only.
func (r *RBAC) decideActOnTenant(c fiber.Ctx, p *authz.Principal, vals [][]byte) (actOnDecision, error) {
	raw := make([]string, len(vals))
	for i, v := range vals {
		raw[i] = string(v)
	}
	d := actOnDecision{value: auditValue(strings.Join(raw, ", "))}
	if len(vals) != 1 {
		d.outcome, d.refusal = outcomeRepeated, badHeader("send X-Act-On-Tenant once")
		return d, nil
	}
	v := strings.TrimSpace(raw[0])
	if v != "*" {
		id, err := uuid.Parse(v)
		if err != nil || id == uuid.Nil {
			d.outcome, d.refusal = outcomeMalformed, badHeader("X-Act-On-Tenant is a tenant id or *")
			return d, nil
		}
		kind, found, err := r.lookupTenantKind(c.Context(), id)
		if err != nil {
			return d, err
		}
		if !found {
			d.outcome = outcomeUnknownTenant
			d.refusal = httpx.ErrNotFound().WithDetails(map[string]any{"header": authz.HeaderActOnTenant})
			return d, nil
		}
		d.target, d.kind = &id, kind
	}
	read := c.Method() == http.MethodGet || c.Method() == http.MethodHead
	pcaps := authz.PlatformCaps(p)
	switch {
	case d.target == nil && !read:
		d.refusal = authz.ErrPermissionDenied("X-Act-On-Tenant: * is read-only (GET and HEAD)")
	case d.target == nil && !pcaps.Has(authz.PlatformCrossTenantRead):
		d.refusal = authz.ErrPermissionDenied("missing capability", authz.PlatformCrossTenantRead)
	case d.target != nil && !p.HasPlatform(authz.PlatformAdmin):
		d.refusal = authz.ErrPermissionDenied("acting as a tenant needs platform_admin; support reads with X-Act-On-Tenant: *")
	case d.target != nil && read && !pcaps.Has(authz.PlatformCrossTenantRead):
		d.refusal = authz.ErrPermissionDenied("missing capability", authz.PlatformCrossTenantRead)
	case d.target != nil && !read && !pcaps.Has(authz.PlatformCrossTenantWrite):
		d.refusal = authz.ErrPermissionDenied("missing capability", authz.PlatformCrossTenantWrite)
	}
	d.outcome = outcomeAccepted
	if d.refusal != nil {
		d.outcome = outcomeRefused
	}
	return d, nil
}

// auditValueMax bounds the caller-supplied header value kept in the audit row: a uuid is 36 bytes, so
// a repeated header keeps its first values.
const auditValueMax = 128

// auditValue is the header value as stored in details.act_on_tenant: printable ASCII only (jsonb
// refuses NUL, and nothing longer or stranger than a uuid is a valid value) and at most auditValueMax
// bytes, so a crafted header can neither bloat the row nor make the insert fail.
func auditValue(s string) string {
	b := make([]byte, 0, min(len(s), auditValueMax))
	for i := 0; i < len(s) && len(b) < auditValueMax; i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e {
			c = '?'
		}
		b = append(b, c)
	}
	return string(b)
}

func badHeader(msg string) *httpx.Error {
	return httpx.ErrBadRequest(msg).WithDetails(map[string]any{"header": authz.HeaderActOnTenant})
}

// lookupTenantKind reads the kind of an X-Act-On-Tenant target; found is false for an unknown tenant.
func (r *RBAC) lookupTenantKind(ctx context.Context, id uuid.UUID) (kind string, found bool, err error) {
	err = r.system(ctx, func(q *iamdb.Queries) (err error) {
		kind, err = q.TenantKind(ctx, id)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("iam: X-Act-On-Tenant target: %w", err)
	}
	return kind, true, nil
}

// auditCrossTenant commits the platform_cross_tenant_access row of this request in a transaction of its
// own (a same-transaction insert is impossible for the read-only * transaction, C.3.9). tenant_id is the
// target only when it exists (an FK to tenants); NULL for *, and for a repeated, malformed or unknown
// value.
func (r *RBAC) auditCrossTenant(c fiber.Ctx, p *authz.Principal, d actOnDecision) error {
	actor := p.UserID
	ev := security.Event{
		EventType: EventCrossTenantAccess, Severity: security.SeverityWarning,
		Summary:     "platform principal used X-Act-On-Tenant",
		ActorUserID: &actor, TenantID: d.target, RequestID: httpx.RequestIDFrom(c), OccurredAt: r.now(),
		Details: map[string]any{
			"method": c.Method(), "path": c.Path(), "act_on_tenant": d.value, "outcome": d.outcome,
			"request_id": httpx.RequestIDFrom(c), "ip": httpx.ClientIPFrom(c), "platform_roles": p.Platform,
		},
	}
	err := db.WithSystem(c.Context(), r.pool, d.target, func(tx pgx.Tx) error { return security.Append(c.Context(), tx, ev) })
	if err != nil {
		return httpx.ErrUnavailable("the cross-tenant audit row could not be written").Wrap(err)
	}
	return nil
}
