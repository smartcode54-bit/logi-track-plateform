package app

import (
	"github.com/gofiber/fiber/v3"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// P1.1 installs the fully verified audience during authentication. The virtual
// role also fails closed if that installation is accidentally omitted.
func resolveSupportAudience(c fiber.Ctx) {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return
	}
	a := httpx.ProjectionAudienceFrom(c)
	if authz.IsSupport(p) {
		a.Support = true
	}
	a.Platform = p.IsPlatform()
	a.Actor = p.UserID.String()
	a.Session = p.SessionID.String()
	httpx.SetProjectionAudience(c, a)
}

func (a *API) SupportPolicies() map[string][]httpx.EndpointPolicy {
	out := map[string][]httpx.EndpointPolicy{}
	for listener, registry := range a.policies {
		out[listener] = registry.Policies()
	}
	return out
}
