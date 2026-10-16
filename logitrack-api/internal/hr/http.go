package hr

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Route prefixes (Appendix B §B.2.17, internal listener). The job route is its own group next to /v1/jobs (T10).
const (
	PayrollPrefix   = "/v1/payroll"
	PenaltiesPrefix = "/v1/penalties"
	JobPrefix       = "/v1/jobs/" + jobs.TypePayrollRun
	LeavePrefix     = "/v1/leave-requests"
	HolidaysPrefix  = "/v1/holidays"
)

// Groups are the HR routes on the internal listener behind requireAuth (auth.Service.RequireAuth, which resolves the
// principal through iam.RBAC.Authorize). s may be nil while the route table is only listed. /v1/payroll/config is
// registered before /v1/payroll/{id}.
//
//	GET   /v1/payroll                    hr:view_payroll
//	GET   /v1/payroll/config             hr:manage_payroll
//	POST  /v1/payroll/config             hr:manage_payroll
//	GET   /v1/payroll/{id}               hr:view_payroll
//	POST  /v1/payroll/{id}/approve       hr:manage_payroll
//	POST  /v1/payroll/{id}/status        hr:manage_payroll
//	GET   /v1/penalties                  hr:manage_payroll
//	POST  /v1/penalties                  hr:manage_payroll
//	POST  /v1/penalties/{id}/cancel      hr:manage_payroll
//	POST  /v1/jobs/payroll.run           hr:manage_payroll
//	GET   /v1/leave-requests             hr:view_leave
//	POST  /v1/leave-requests/{id}/approve  hr:manage_leave (also reject, cancel)
//	GET   /v1/holidays                   authenticated (?year; ETag)
//	POST  /v1/holidays                   hr:manage_holidays (public rows: steward)
//	PUT   /v1/holidays/generate          hr:manage_holidays (public rows: steward)
//	PATCH /v1/holidays/{id}              hr:manage_holidays (public rows: steward)
//	DELETE /v1/holidays/{id}             hr:manage_holidays (public rows: steward)
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	need := authz.RequireCap
	return []ingress.Group{
		{Prefix: PayrollPrefix, Mount: func(r fiber.Router) {
			r = ingress.DenyRoutes(r, "P1.0")
			r.Get("", requireAuth, need(authz.HRViewPayroll), h.runs)
			r.Get("/config", requireAuth, need(authz.HRManagePayroll), h.configs)
			r.Post("/config", requireAuth, need(authz.HRManagePayroll), h.createConfig)
			r.Get("/:id", requireAuth, need(authz.HRViewPayroll), h.run)
			r.Post("/:id/approve", requireAuth, need(authz.HRManagePayroll), h.approve)
			r.Post("/:id/status", requireAuth, need(authz.HRManagePayroll), h.status)
		}},
		{Prefix: PenaltiesPrefix, Mount: func(r fiber.Router) {
			r = ingress.DenyRoutes(r, "P1.0")
			r.Get("", requireAuth, need(authz.HRManagePayroll), h.penalties)
			r.Post("", requireAuth, need(authz.HRManagePayroll), h.createPenalty)
			r.Post("/:id/cancel", requireAuth, need(authz.HRManagePayroll), h.cancelPenalty)
		}},
		{Prefix: JobPrefix, Mount: func(r fiber.Router) {
			r = ingress.DenyRoutes(r, "P1.0")
			r.Post("", requireAuth, need(jobs.TypeCapabilities[jobs.TypePayrollRun]), h.submitRun)
		}},
		{Prefix: LeavePrefix, Mount: func(r fiber.Router) {
			r = ingress.DenyRoutes(r, "P1.0")
			r.Get("", requireAuth, need(authz.HRViewLeave), h.leaveRequests)
			for _, action := range []string{ActionApprove, ActionReject, ActionCancel} {
				r.Post("/:id/"+action, requireAuth, need(authz.HRManageLeave), h.decide(action))
			}
		}},
		{Prefix: HolidaysPrefix, Mount: func(r fiber.Router) {
			r = ingress.DenyRoutes(r, "P1.0")
			r.Get("", requireAuth, h.holidays)
			r.Post("", requireAuth, need(authz.HRManageHolidays), h.saveHoliday)
			r.Put("/generate", requireAuth, need(authz.HRManageHolidays), h.generateHolidays)
			r.Patch("/:id", requireAuth, need(authz.HRManageHolidays), h.patchHoliday)
			r.Delete("/:id", requireAuth, need(authz.HRManageHolidays), h.deleteHoliday)
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

// queryLimit reads ?limit= (0 when absent; 422 when not a number).
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

func (h handlers) runs(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "period", "round", "status", "driverId", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, counts, err := h.s.Runs(c.Context(), p, RunQuery{Period: c.Query("period"), Round: c.Query("round"),
		Status: c.Query("status"), DriverID: c.Query("driverId"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	var meta any
	if counts != nil {
		meta = listMeta{StatusCounts: counts}
	}
	return httpx.Page(c, out, next, meta)
}

func (h handlers) run(c fiber.Ctx) error {
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
	out, err := h.s.Run(c.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) approve(c fiber.Ctx) error {
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
	var body struct{}
	if err := httpx.DecodeJSON(c, &body); err != nil {
		return err
	}
	out, err := h.s.Approve(c.Context(), p, httpx.RequestIDFrom(c), id)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) status(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in StatusInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.SetStatus(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

// configs is GET /v1/payroll/config: {"data": [versions]} without ?at, {"data": version | null} with it.
func (h handlers) configs(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "at"); err != nil {
		return err
	}
	out, err := h.s.Configs(c.Context(), p, ConfigQuery{At: c.Query("at")})
	if err != nil {
		return err
	}
	if out.ByInstant {
		return httpx.JSON(c, http.StatusOK, out.Effective)
	}
	return httpx.JSON(c, http.StatusOK, out.Versions)
}

func (h handlers) createConfig(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in ConfigInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.CreateConfig(c.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) penalties(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "driverId", "status", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	var statuses []string
	for _, v := range c.Request().URI().QueryArgs().PeekMulti("status") {
		statuses = append(statuses, string(v))
	}
	out, next, err := h.s.Penalties(c.Context(), p, PenaltyQuery{DriverID: c.Query("driverId"), Statuses: statuses, Limit: limit,
		Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, nil)
}

func (h handlers) createPenalty(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in PenaltyInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.CreatePenalty(c.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) cancelPenalty(c fiber.Ctx) error {
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
	out, err := h.s.CancelPenalty(c.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

// submitRun is POST /v1/jobs/payroll.run: 202 {"data":{"jobId"}} (Appendix B §B.1.5).
func (h handlers) submitRun(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	var in RunParams
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	j, err := h.s.SubmitRun(c.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusAccepted, map[string]any{"jobId": j.ID})
}
