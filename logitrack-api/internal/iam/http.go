package iam

import (
	"net/http"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Roles is the body of GET /v1/roles: the catalog and the default role matrix (Appendix B, /v1/roles;
// replaces CAPABILITY_META and DEFAULT_ROLE_CAPABILITIES of the web). The per-tenant matrix with
// overrides is GET /v1/roles/matrix (Matrix, T51).
type Roles struct {
	Capabilities []authz.CapInfo      `json:"capabilities"`
	Roles        []authz.RoleDefaults `json:"roles"`
	// Steward lists the global keys (R60): effective only for own-fleet staff and platform_admin.
	Steward []authz.Cap `json:"stewardCapabilities"`
}

// RoleGroups are the route groups of T07: GET /v1/roles for any authenticated principal (internal
// listener only; the handler reads nothing per request) behind catalogAuth, and of T51: GET and PUT
// /v1/roles/matrix (security:manage_roles) over the administration a behind requireAuth. Both resolve the
// principal through RBAC.Authorize: requireAuth is auth.Service.RequireAuth() and admits people only (a matrix
// save names a users row); catalogAuth is RequireAuth(auth.AllowAPIKeys(integration, script)) in cmd/api, so the
// catalog also answers those API keys (T32K). a may be nil while the route table is only listed.
func RoleGroups(a *Admin, requireAuth, catalogAuth fiber.Handler) []ingress.Group {
	body := Roles{Capabilities: authz.Catalog(), Roles: authz.DefaultMatrix(), Steward: authz.GlobalCaps()}
	h := adminHTTP{a}
	return []ingress.Group{{
		Prefix: "/v1/roles",
		Mount: func(r fiber.Router) {
			r.Get("", catalogAuth, func(c fiber.Ctx) error {
				c.Set(fiber.HeaderCacheControl, "private, max-age=300")
				return httpx.JSON(c, http.StatusOK, body)
			})
			r.Get("/matrix", requireAuth, authz.RequireCap(authz.SecurityManageRoles), h.getMatrix)
			r.Put("/matrix", requireAuth, authz.RequireCap(authz.SecurityManageRoles), h.putMatrix)
		},
	}}
}
