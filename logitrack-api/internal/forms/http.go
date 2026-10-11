package forms

import (
	"net/http"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Route prefixes (Appendix B §B.2.19): internal listener only, so the public listener answers 404 for both forms.
const (
	WaitlistPrefix        = "/v1/waitlist"
	PartnerInterestPrefix = "/v1/partner-interest"
)

// Options wire the routes: Auth is auth.Service.RequireAuth() for the staff reads; FormLimit is the rate limit of the
// two anonymous posts (public_form_ip, then public_form_email; nil = none, for the route table).
type Options struct {
	Auth      fiber.Handler
	FormLimit fiber.Handler
}

// Groups are the routes of T53's forms. s may be nil while the route table is only listed.
//
//	POST   /v1/waitlist            anonymous (through the BFF's POST /api/forms/waitlist), rate-limited
//	POST   /v1/partner-interest    anonymous (through the BFF's POST /api/forms/partner-interest), rate-limited
//	GET    /v1/waitlist            waitlist:view (steward key)
//	DELETE /v1/waitlist/{id}       waitlist:view
func Groups(s *Service, o Options) []ingress.Group {
	h := handlers{s}
	post := func(handler fiber.Handler) []any {
		if o.FormLimit != nil {
			return []any{o.FormLimit, handler}
		}
		return []any{handler}
	}
	view := authz.RequireCap(authz.WaitlistView)
	return []ingress.Group{
		{Prefix: WaitlistPrefix, Mount: func(r fiber.Router) {
			hs := post(h.joinWaitlist)
			r.Post("", hs[0], hs[1:]...)
			r.Get("", o.Auth, view, h.listWaitlist)
			r.Delete("/:id", o.Auth, view, h.deleteWaitlist)
		}},
		{Prefix: PartnerInterestPrefix, Mount: func(r fiber.Router) {
			hs := post(h.partnerInterest)
			r.Post("", hs[0], hs[1:]...)
		}},
	}
}

type handlers struct{ s *Service }

func (h handlers) joinWaitlist(c fiber.Ctx) error {
	var in WaitlistInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.JoinWaitlist(c.Context(), in)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) partnerInterest(c fiber.Ctx) error {
	var in PartnerInterestInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.SubmitPartnerInterest(c.Context(), in)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusCreated, out)
}

// listWaitlist is GET /v1/waitlist?cursor&limit (meta.total: every entry).
func (h handlers) listWaitlist(c fiber.Ctx) error {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return httpx.ErrUnauthenticated()
	}
	for k := range c.Queries() {
		if k != "cursor" && k != "limit" {
			return httpx.ErrBadRequest("unknown query parameter " + k)
		}
	}
	limit := 0
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n == 0 {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "limit", Reason: "out_of_range",
				Params: map[string]any{"min": 1, "max": MaxLimit}})
		}
		limit = n
	}
	items, next, total, err := h.s.ListWaitlist(c.Context(), p, limit, c.Query("cursor"))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.Page(c, items, next, map[string]any{"total": total})
}

// deleteWaitlist is DELETE /v1/waitlist/{id}: 204, or 404 for an unknown id.
func (h handlers) deleteWaitlist(c fiber.Ctx) error {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return httpx.ErrUnauthenticated()
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return httpx.ErrNotFound()
	}
	if err := h.s.DeleteWaitlist(c.Context(), p, id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}
