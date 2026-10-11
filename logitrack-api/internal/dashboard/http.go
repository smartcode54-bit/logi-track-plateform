package dashboard

import (
	"net/http"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Route prefixes (Appendix B §B.2.19, internal listener).
const (
	DashboardPrefix = "/v1/dashboard"
	BadgesPrefix    = "/v1/badges"
)

// Groups are the routes of T53 on the internal listener behind requireAuth (auth.Service.RequireAuth). Both admit any
// signed-in person (the dashboard and the shell are open, Appendix C §C.2.7); each block and counter checks its own
// capability. s may be nil while the route table is only listed.
//
//	GET /v1/dashboard/summary[?from&to]  per block
//	GET /v1/badges                       per counter
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	return []ingress.Group{
		{Prefix: DashboardPrefix, Mount: func(r fiber.Router) {
			r.Get("/summary", requireAuth, h.summary)
		}},
		{Prefix: BadgesPrefix, Mount: func(r fiber.Router) {
			r.Get("", requireAuth, h.badges)
		}},
	}
}

type handlers struct{ s *Service }

// summary is GET /v1/dashboard/summary?from&to (yyyy-MM-dd Bangkok days, both included; default the last seven days).
func (h handlers) summary(c fiber.Ctx) error {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return httpx.ErrUnauthenticated()
	}
	for k := range c.Queries() {
		if k != "from" && k != "to" {
			return httpx.ErrBadRequest("unknown query parameter " + k)
		}
	}
	per, err := ParsePeriod(c.Query("from"), c.Query("to"), h.s.now())
	if err != nil {
		return err
	}
	out, err := h.s.Summary(c.Context(), p, per)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusOK, out)
}

// badges is GET /v1/badges.
func (h handlers) badges(c fiber.Ctx) error {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return httpx.ErrUnauthenticated()
	}
	if len(c.Queries()) > 0 {
		return httpx.ErrBadRequest("the badges take no query parameter")
	}
	out, err := h.s.Badges(c.Context(), p)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusOK, out)
}
