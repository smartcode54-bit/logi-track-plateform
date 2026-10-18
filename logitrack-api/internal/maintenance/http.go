package maintenance

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

// Prefix is the route prefix of the maintenance records (Appendix B §B.2.15, internal listener).
const Prefix = "/v1/maintenance"

// Groups are the routes of /v1/maintenance on the internal listener behind requireAuth (auth.Service.RequireAuth,
// which resolves the principal through iam.RBAC.Authorize). s may be nil while the route table is only listed.
//
//	GET   /v1/maintenance                fleet:manage_maintenance
//	POST  /v1/maintenance                fleet:manage_maintenance
//	PATCH /v1/maintenance/{id}           fleet:manage_maintenance
//	POST  /v1/maintenance/{id}/remind    fleet:manage_maintenance
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	need := authz.RequireCap(authz.FleetManageMaintenance)
	return []ingress.Group{{Prefix: Prefix, Mount: func(r fiber.Router) {
		ingress.Bind(r, "", supportPolicy(Prefix, "maintenance.record", "maintenance.meta", "truckId", "status", "type", "limit", "cursor"), requireAuth, authz.RequireCap(authz.FleetViewMaintenance, authz.FleetManageMaintenance), h.list)
		r.Post("", requireAuth, need, h.create)
		r.Patch("/:id", requireAuth, need, h.patch)
		r.Post("/:id/remind", requireAuth, need, h.remind)
	}}}
}

// TruckRoutes are the maintenance routes of the trucks group (Appendix B §B.2.8), mounted by fleet.Groups on the
// router of /v1/trucks:
//
//	POST /v1/trucks/{id}/pm-check   accounting:audit_expense
func TruckRoutes(s *Service, requireAuth fiber.Handler) func(r fiber.Router) {
	h := handlers{s}
	return func(r fiber.Router) {
		r.Post("/:id/pm-check", requireAuth, authz.RequireCap(authz.AccountingAuditExpense), h.pmCheck)
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

type listMeta struct {
	StatusCounts StatusCounts `json:"statusCounts"`
}

func (h handlers) list(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "truckId", "status", "type", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	cursor, err := httpx.SupportCursor(c)
	if err != nil {
		return err
	}
	out, next, counts, err := h.s.List(c.Context(), p, ListInput{TruckID: c.Query("truckId"), Status: c.Query("status"),
		Type: c.Query("type"), Limit: limit, Cursor: cursor})
	if err != nil {
		return err
	}
	var meta any
	if counts != nil {
		meta = listMeta{StatusCounts: counts}
	}
	if authz.IsSupport(p) {
		if counts != nil {
			return httpx.SupportPage(c, "maintenance.record", out, next, listMeta{StatusCounts: counts})
		}
		return httpx.SupportPage(c, "maintenance.record", out, next, nil)
	}
	return httpx.OrdinaryPage(c, out, next, meta)
}

func (h handlers) create(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in Input
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Create(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) patch(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in Input
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Patch(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) remind(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in RemindInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	event, err := h.s.Remind(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusAccepted, map[string]any{"eventId": event})
}

func (h handlers) pmCheck(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in PMCheckInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.PMCheck(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}
