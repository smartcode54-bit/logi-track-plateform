package parties

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

// Route prefixes (Appendix B §B.2.10, internal listener).
const (
	CustomersPrefix = "/v1/customers"
	CompaniesPrefix = "/v1/companies"
)

// Groups are the routes of T20 on the internal listener behind requireAuth (auth.Service.RequireAuth, which
// resolves the principal through iam.RBAC.Authorize). s may be nil while the route table is only listed.
//
//	GET   /v1/customers          authenticated (fields=minimal) | fleet:manage_customers (full, checked inside)
//	POST  /v1/customers          fleet:manage_customers (a steward key, R60)
//	GET   /v1/customers/{id}     fleet:manage_customers
//	PATCH /v1/customers/{id}     fleet:manage_customers
//	GET   /v1/companies          company:view
//	GET   /v1/companies/owner    company:view
//	POST  /v1/companies          company:manage, an active tenant
//	PATCH /v1/companies/{id}     company:manage
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	need := authz.RequireCap
	return []ingress.Group{
		{Prefix: CustomersPrefix, Mount: func(r fiber.Router) {
			r.Get("", requireAuth, h.listCustomers)
			r.Post("", requireAuth, need(authz.FleetManageCustomers), h.createCustomer)
			r.Get("/:id", requireAuth, need(authz.FleetManageCustomers), h.getCustomer)
			r.Patch("/:id", requireAuth, need(authz.FleetManageCustomers), h.patchCustomer)
		}},
		{Prefix: CompaniesPrefix, Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need(authz.CompanyView), h.listCompanies)
			r.Get("/owner", requireAuth, need(authz.CompanyView), h.ownerCompany)
			r.Post("", requireAuth, need(authz.CompanyManage), authz.RequireTenant(), h.createCompany)
			r.Patch("/:id", requireAuth, need(authz.CompanyManage), h.patchCompany)
		}},
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

func (h handlers) listCustomers(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "fields", "q", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, err := h.s.ListCustomers(c.Context(), p, ListCustomersInput{Fields: c.Query("fields"), Q: c.Query("q"),
		Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, nil)
}

func (h handlers) getCustomer(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	out, err := h.s.GetCustomer(c.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) createCustomer(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in CustomerInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.CreateCustomer(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) patchCustomer(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in CustomerInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.PatchCustomer(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) listCompanies(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "type", "q", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, err := h.s.ListCompanies(c.Context(), p, ListCompaniesInput{Type: c.Query("type"), Q: c.Query("q"),
		Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, nil)
}

func (h handlers) ownerCompany(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	out, err := h.s.OwnerCompany(c.Context(), p)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) createCompany(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in CompanyInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.CreateCompany(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) patchCompany(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in CompanyInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.PatchCompany(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}
