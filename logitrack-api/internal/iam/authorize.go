package iam

import (
	"context"
	"errors"
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
//     only; the request transaction is READ ONLY with app.bypass_tenant=on (db.WithPrincipal).
//
// Every request of a platform principal that names a well-formed, existing target writes one
// security_events row platform_cross_tenant_access in its own transaction, committed before the
// handler's transaction begins, and before the method and capability checks, so a refused attempt is
// on record too. No audit, no access: when the row cannot be written the request fails (503).
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
	if len(vals) != 1 {
		return badHeader("send X-Act-On-Tenant once")
	}
	v := strings.TrimSpace(string(vals[0]))
	var target *uuid.UUID
	kind := ""
	if v != "*" {
		id, err := uuid.Parse(v)
		if err != nil || id == uuid.Nil {
			return badHeader("X-Act-On-Tenant is a tenant id or *")
		}
		kind, err = r.lookupTenantKind(c.Context(), id)
		if err != nil {
			return err
		}
		target = &id
	}
	if err := r.auditCrossTenant(c, p, v, target); err != nil {
		return err
	}
	read := c.Method() == http.MethodGet || c.Method() == http.MethodHead
	pcaps := authz.PlatformCaps(p)
	switch {
	case target == nil && !read:
		return authz.ErrPermissionDenied("X-Act-On-Tenant: * is read-only (GET and HEAD)")
	case target == nil && !pcaps.Has(authz.PlatformCrossTenantRead):
		return authz.ErrPermissionDenied("missing capability", authz.PlatformCrossTenantRead)
	case target != nil && !p.HasPlatform(authz.PlatformAdmin):
		return authz.ErrPermissionDenied("acting as a tenant needs platform_admin; support reads with X-Act-On-Tenant: *")
	case target != nil && read && !pcaps.Has(authz.PlatformCrossTenantRead):
		return authz.ErrPermissionDenied("missing capability", authz.PlatformCrossTenantRead)
	case target != nil && !read && !pcaps.Has(authz.PlatformCrossTenantWrite):
		return authz.ErrPermissionDenied("missing capability", authz.PlatformCrossTenantWrite)
	}
	if target != nil {
		p.ActOnTenant, p.TenantKind = target, kind
	} else {
		p.ActOnAll = true
	}
	return nil
}

func badHeader(msg string) *httpx.Error {
	return httpx.ErrBadRequest(msg).WithDetails(map[string]any{"header": authz.HeaderActOnTenant})
}

// lookupTenantKind reads the kind of an X-Act-On-Tenant target; an unknown tenant is 404 not_found.
func (r *RBAC) lookupTenantKind(ctx context.Context, id uuid.UUID) (string, error) {
	var kind string
	err := r.system(ctx, func(q *iamdb.Queries) (err error) {
		kind, err = q.TenantKind(ctx, id)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", httpx.ErrNotFound().WithDetails(map[string]any{"header": authz.HeaderActOnTenant})
	}
	return kind, err
}

// auditCrossTenant commits the platform_cross_tenant_access row of this request in a transaction of its
// own (a same-transaction insert is impossible for the read-only * transaction, C.3.9).
func (r *RBAC) auditCrossTenant(c fiber.Ctx, p *authz.Principal, value string, target *uuid.UUID) error {
	actor := p.UserID
	ev := security.Event{
		EventType: EventCrossTenantAccess, Severity: security.SeverityWarning,
		Summary:     "platform principal used X-Act-On-Tenant",
		ActorUserID: &actor, TenantID: target, RequestID: httpx.RequestIDFrom(c), OccurredAt: r.now(),
		Details: map[string]any{
			"method": c.Method(), "path": c.Path(), "act_on_tenant": value, "request_id": httpx.RequestIDFrom(c),
			"ip": httpx.ClientIPFrom(c), "platform_roles": p.Platform,
		},
	}
	err := db.WithSystem(c.Context(), r.pool, target, func(tx pgx.Tx) error { return security.Append(c.Context(), tx, ev) })
	if err != nil {
		return httpx.ErrUnavailable("the cross-tenant audit row could not be written").Wrap(err)
	}
	return nil
}
