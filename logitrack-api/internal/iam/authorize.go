package iam

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// EventCrossTenantAccess is the security_events type of platform X-Act-On-Tenant refusals (C.3.9, R85).
const EventCrossTenantAccess = "platform_cross_tenant_access"

// Authorize implements auth.Authorizer: auth.RequireAuth calls it for every authenticated request, so
// no route can skip it. It refuses the retired header and then resolves the principal
// (Resolve). Route guards (authz.RequireCap, RequireTenant, ...) and db.WithPrincipal read the result.
func (r *RBAC) Authorize(c fiber.Ctx, p *authz.Principal) error {
	if err := r.actOnTenant(c, p); err != nil {
		return err
	}
	return r.Resolve(c.Context(), p)
}

// actOnTenant refuses the retired override. Auditing never grants tenant authority.
func (r *RBAC) actOnTenant(c fiber.Ctx, p *authz.Principal) error {
	refusal := authz.RefuseTenantHeader(c)
	if refusal == nil {
		return nil
	}
	if !p.IsMachine() && p.IsPlatform() && httpx.ListenerFrom(c) != ingress.Public {
		vals := c.Request().Header.PeekAll(authz.HeaderActOnTenant)
		raw := make([]string, len(vals))
		for i, v := range vals {
			raw[i] = string(v)
		}
		if err := r.auditCrossTenant(c, p, actOnDecision{value: auditValue(strings.Join(raw, ", ")), outcome: "refused"}); err != nil {
			return err
		}
	}
	return refusal
}

type actOnDecision struct {
	value   string
	outcome string
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

// auditCrossTenant commits a platform principal's refused-header warning independently of
// business work. The retired header never resolves a target tenant, so tenant_id stays NULL.
func (r *RBAC) auditCrossTenant(c fiber.Ctx, p *authz.Principal, d actOnDecision) error {
	actor := p.UserID
	ev := security.Event{
		EventType: EventCrossTenantAccess, Severity: security.SeverityWarning,
		Summary:     "retired tenant override refused",
		ActorUserID: &actor, TenantID: nil, RequestID: httpx.RequestIDFrom(c), OccurredAt: r.now(),
		Details: map[string]any{
			"method": c.Method(), "path": c.Route().Path, "act_on_tenant": d.value, "outcome": d.outcome,
			"request_id": httpx.RequestIDFrom(c), "ip": httpx.ClientIPFrom(c), "platform_roles": p.Platform,
		},
	}
	err := db.WithSystem(c.Context(), r.pool, nil, func(tx pgx.Tx) error { return security.Append(c.Context(), tx, ev) })
	if err != nil {
		return httpx.ErrUnavailable("the cross-tenant audit row could not be written").Wrap(err)
	}
	return nil
}
