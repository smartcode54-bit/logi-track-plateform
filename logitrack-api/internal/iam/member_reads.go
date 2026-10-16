package iam

import (
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Only legacy member reads may use real membership authority. Explicit platform endpoints always project.
func (h adminHTTP) memberReadContext(c fiber.Ctx) error {
	p := authz.PrincipalFrom(c)
	if p == nil || !p.IsPlatform() || p.IsMachine() || authz.IsSupport(p) || p.TenantID == nil || c.Method() != http.MethodGet {
		return nil
	}
	switch httpx.EndpointPolicyFrom(c).Route {
	case "/v1/users", "/v1/users/:id":
	case "/v1/tenants/:id/members":
		id, err := uuid.Parse(c.Params("id"))
		if err != nil || id != *p.TenantID {
			return nil
		}
	default:
		return nil
	}
	eligible := false
	err := db.WithSystem(c.Context(), h.a.pool, nil, func(tx pgx.Tx) error {
		facts, err := supportMembershipFacts(c.Context(), tx, *p.TenantID, []uuid.UUID{p.UserID})
		if err == nil {
			eligible = eligibilityBasis(facts[p.UserID]) != "pending"
		}
		return err
	})
	if err != nil || !eligible {
		return err
	}
	member, err := h.a.memberPrincipal(call{ctx: c.Context(), p: p}, authz.UsersView)
	if err != nil {
		return err
	}
	// This read uses only the ordinary member's reach/capabilities. Write requests retain the real platform actor.
	authz.SetPrincipal(c, member)
	return nil
}
