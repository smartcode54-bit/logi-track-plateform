package drivers

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Prefix is the route prefix of T23's drivers (Appendix B §B.2.7, internal listener).
const Prefix = "/v1/drivers"

// Groups are the routes of T23's drivers on the internal listener behind requireAuth (auth.Service.RequireAuth, which
// resolves the principal through iam.RBAC.Authorize; an X-Api-Key is refused, R82). s may be nil while the route
// table is only listed.
//
//	GET   /v1/drivers                          drivers:view | dispatch:view_operations (a dispatcher: scope_drivers)
//	POST  /v1/drivers                          drivers:create
//	GET   /v1/drivers/{id}                     drivers:view (PII only with drivers:view_pii)
//	PATCH /v1/drivers/{id}                     drivers:edit (PII fields: + drivers:view_pii; tenantId: a steward)
//	GET   /v1/drivers/{id}/documents/{kind}    drivers:view_pii (302 to a 5-minute presigned GET)
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	need := authz.RequireCap
	return []ingress.Group{{Prefix: Prefix, Mount: func(r fiber.Router) {
		r.Get("", requireAuth, need(authz.DriversView, authz.DispatchViewOperations), h.list)
		r.Post("", requireAuth, need(authz.DriversCreate), h.create)
		r.Get("/:id", requireAuth, need(authz.DriversView), h.get)
		r.Patch("/:id", requireAuth, need(authz.DriversEdit), h.patch)
		r.Get("/:id/documents/:kind", requireAuth, need(authz.DriversViewPII), h.document)
	}}}
}

type handlers struct{ s *Service }

func principal(c fiber.Ctx) (*authz.Principal, error) {
	p := authz.PrincipalFrom(c)
	if p == nil {
		return nil, httpx.ErrUnauthenticated()
	}
	return p, nil
}

// pathID parses a uuid path parameter; anything else is 404 (no such resource).
func pathID(c fiber.Ctx) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, httpx.ErrNotFound()
	}
	return id, nil
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

// queryLimit reads ?limit (0 when absent); a non-number or an explicit 0 is out of range.
func queryLimit(c fiber.Ctx) (int, error) {
	v := c.Query("limit")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n == 0 {
		return 0, httpx.ErrInvalidArgument(violation("limit", "out_of_range"))
	}
	return n, nil
}

func (h handlers) list(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "status", "q", "fields", "tenantId", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, meta, err := h.s.List(c.Context(), p, ListInput{Status: c.Query("status"), Q: c.Query("q"),
		Fields: c.Query("fields"), TenantID: c.Query("tenantId"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, meta)
}

func (h handlers) get(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	out, err := h.s.Get(c.Context(), p, id)
	if err != nil {
		return err
	}
	if out.PII != nil {
		c.Set(fiber.HeaderCacheControl, "private, no-store")
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) create(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in DriverInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Create(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	if out.PII != nil {
		c.Set(fiber.HeaderCacheControl, "private, no-store")
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

// patch answers 200 with the driver, or 204 when a tenant move took the driver out of the caller's reach.
func (h handlers) patch(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in DriverInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Patch(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	if out == nil {
		return c.SendStatus(http.StatusNoContent)
	}
	if out.PII != nil {
		c.Set(fiber.HeaderCacheControl, "private, no-store")
	}
	return httpx.JSON(c, http.StatusOK, out)
}

// document is GET /v1/drivers/{id}/documents/{kind}: 302 to a 5-minute presigned GET, never cached.
func (h handlers) document(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	u, err := h.s.DocumentURL(c.Context(), p, id, c.Params("kind"))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	c.Set(fiber.HeaderLocation, u)
	return c.SendStatus(http.StatusFound)
}
