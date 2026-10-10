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
// overrides is GET /v1/roles/matrix (T51).
type Roles struct {
	Capabilities []authz.CapInfo      `json:"capabilities"`
	Roles        []authz.RoleDefaults `json:"roles"`
	// Steward lists the global keys (R60): effective only for own-fleet staff and platform_admin.
	Steward []authz.Cap `json:"stewardCapabilities"`
}

// RoleGroups are the route groups of T07: GET /v1/roles for any authenticated principal (internal
// listener only). requireAuth is auth.Service.RequireAuth(), which resolves the principal through
// RBAC.Authorize; the handler reads nothing per request.
func RoleGroups(requireAuth fiber.Handler) []ingress.Group {
	body := Roles{Capabilities: authz.Catalog(), Roles: authz.DefaultMatrix(), Steward: authz.GlobalCaps()}
	return []ingress.Group{{
		Prefix: "/v1/roles",
		Mount: func(r fiber.Router) {
			r.Get("", requireAuth, func(c fiber.Ctx) error {
				c.Set(fiber.HeaderCacheControl, "private, max-age=300")
				return httpx.JSON(c, http.StatusOK, body)
			})
		},
	}}
}
