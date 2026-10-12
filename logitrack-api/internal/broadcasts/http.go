package broadcasts

import (
	"net/http"
	"net/url"
	"slices"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Prefix is the route prefix of the staff broadcasts (Appendix B §B.2.16, internal listener).
const Prefix = "/v1/broadcasts"

// MobilePrefix is the path of the driver's broadcasts below the /v1/mobile group (Appendix B §B.2.21).
const MobilePrefix = "/broadcasts"

// Groups are the staff routes of /v1/broadcasts on the internal listener behind requireAuth (auth.Service.RequireAuth,
// people only). s may be nil while the route table is only listed.
//
//	GET    /v1/broadcasts        broadcasts:view  ?limit&cursor (keyset, newest first; read counts)
//	POST   /v1/broadcasts        broadcasts:send  {recipientGroup | recipientUserIds[], title, messageText} -> 201
//	DELETE /v1/broadcasts/{id}   broadcasts:send  soft delete (voided_at) -> 204
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	view, send := authz.RequireCap(authz.BroadcastsView), authz.RequireCap(authz.BroadcastsSend)
	return []ingress.Group{{Prefix: Prefix, Mount: func(r fiber.Router) {
		r.Get("", requireAuth, view, h.list)
		r.Post("", requireAuth, send, h.send)
		r.Delete("/:id", requireAuth, send, h.void)
	}}}
}

// MobileRoutes are the driver's broadcasts below the /v1/mobile group (public and internal listeners; app.MobileGroups).
// The list and the latest broadcast take a driver token (requireAuth, APK 4.x); the read receipt is the target of the
// markBroadcastRead callable shim (requireShim: a cf_shim API key with the driver's Firebase ID token, R45; APK 4.x's
// Go tokens join with T55). s may be nil while the route table is only listed.
//
//	GET  /v1/mobile/broadcasts             mobile:broadcasts  ?limit&cursor (addressed to the driver, not voided)
//	GET  /v1/mobile/broadcasts/latest      mobile:broadcasts  the newest one, or null
//	POST /v1/mobile/broadcasts/{id}/read   mobile:broadcasts  {id} = the uuid or the Firestore doc id -> {broadcastId, alreadyRead}
func MobileRoutes(s *Service, requireAuth, requireShim fiber.Handler) func(r fiber.Router) {
	h := handlers{s}
	own := authz.RequireCap(authz.MobileBroadcasts)
	return func(r fiber.Router) {
		r.Get(MobilePrefix, requireAuth, own, h.mine)
		r.Get(MobilePrefix+"/latest", requireAuth, own, h.latest)
		r.Post(MobilePrefix+"/:id/read", requireShim, own, h.markRead)
	}
}

type handlers struct{ s *Service }

func principal(c fiber.Ctx) (*authz.Principal, error) {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return nil, httpx.ErrUnauthenticated()
	}
	return p, nil
}

// onlyQuery refuses an undocumented query parameter (400 bad_request, Appendix B §B.1.5).
func onlyQuery(c fiber.Ctx, allowed ...string) error {
	for k := range c.Queries() {
		if !slices.Contains(allowed, k) {
			return httpx.ErrBadRequest("unknown query parameter " + k)
		}
	}
	return nil
}

func (h handlers) list(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "limit", "cursor"); err != nil {
		return err
	}
	limit, err := parseLimit(c.Query("limit"), MaxLimit)
	if err != nil {
		return err
	}
	out, next, err := h.s.List(c.Context(), p, limit, c.Query("cursor"))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.Page(c, out, next, nil)
}

func (h handlers) send(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	var in SendInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Send(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) void(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil || id == uuid.Nil {
		return httpx.ErrNotFound()
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	if err := h.s.Void(c.Context(), p, httpx.RequestIDFrom(c), id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h handlers) mine(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "limit", "cursor"); err != nil {
		return err
	}
	limit, err := parseLimit(c.Query("limit"), MobileMaxLimit)
	if err != nil {
		return err
	}
	out, next, err := h.s.Mine(c.Context(), p, limit, c.Query("cursor"))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.Page(c, out, next, nil)
}

func (h handlers) latest(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	out, err := h.s.Latest(c.Context(), p)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) markRead(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	ref, err := url.PathUnescape(c.Params("id"))
	if err != nil {
		return errBroadcastNotFound()
	}
	if len(c.Queries()) > 0 {
		return httpx.ErrBadRequest("this route takes no query parameter")
	}
	out, err := h.s.MarkRead(c.Context(), p, ref)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusOK, out)
}
