package operations

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Route prefixes of the staff operations routes (Appendix B §B.2.11-§B.2.13, internal listener).
const (
	TasksPrefix     = "/v1/tasks"
	TripsPrefix     = "/v1/trips"
	StandbyPrefix   = "/v1/standby"
	IncidentsPrefix = "/v1/incidents"
)

// Groups are the operations routes on the internal listener behind requireAuth (auth.Service.RequireAuth, which
// resolves the principal through iam.RBAC.Authorize; an X-Api-Key is refused, R82). s may be nil while the route table
// is only listed. The write-back (T31) joins these groups. Static paths are registered before /{id}.
//
//	GET   /v1/tasks                       operations:view_first_mile | operations:view_line_haul | dispatch:view_operations
//	GET   /v1/tasks/by-number/{taskNo}    operations:view_first_mile | operations:view_line_haul
//	GET   /v1/tasks/{id}                  operations:view_first_mile | operations:view_line_haul | driver:self (R33)
//	GET   /v1/tasks/{id}/trip             operations:view_first_mile | operations:view_line_haul
//	PATCH /v1/tasks/{id}                  operations:manage_tasks | platform:cross_tenant_write; T28 slice: {tenantId,
//	                                      driverId?}, the R13 move by a dispatcher (a call-out of its organisation) or a
//	                                      platform admin
//	GET   /v1/trips                       operations:view_driver_monitor | accounting:view_income | dispatch:view_operations
//	GET   /v1/trips/monitor               operations:view_driver_monitor | dispatch:view_operations (W8, pages of 200)
//	GET   /v1/trips/by-no/{tripNo}        operations:view_driver_monitor | accounting:view_income
//	GET   /v1/trips/{id}                  operations:view_driver_monitor | accounting:view_income
//	GET   /v1/trips/{id}/photos           operations:view_driver_monitor
//	GET   /v1/standby                     operations:view_driver_monitor | dispatch:view_operations
//	GET   /v1/incidents                   operations:view_incidents | dispatch:view_operations
//
// The task type of a task is checked too: operations:view_first_mile reads first-mile tasks, operations:view_line_haul
// line-haul ones (dispatch:view_operations both). A dispatcher or a customer-scope principal is served from the scope_*
// projections (Appendix C §C.3.7).
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	need := authz.RequireCap
	return []ingress.Group{
		{Prefix: TasksPrefix, Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need(authz.OperationsViewFirstMile, authz.OperationsViewLineHaul, authz.DispatchViewOperations),
				h.listTasks)
			r.Get("/by-number/:taskNo", requireAuth, need(authz.OperationsViewFirstMile, authz.OperationsViewLineHaul),
				h.tasksByNumber)
			r.Get("/:id", requireAuth, staffOrDriver(authz.OperationsViewFirstMile, authz.OperationsViewLineHaul), h.getTask)
			r.Get("/:id/trip", requireAuth, need(authz.OperationsViewFirstMile, authz.OperationsViewLineHaul), h.taskTrip)
			r.Patch("/:id", requireAuth, need(authz.OperationsManageTasks, authz.PlatformCrossTenantWrite), h.patch)
		}},
		{Prefix: TripsPrefix, Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need(authz.OperationsViewDriverMon, authz.AccountingViewIncome, authz.DispatchViewOperations),
				h.listTrips)
			r.Get("/monitor", requireAuth, need(authz.OperationsViewDriverMon, authz.DispatchViewOperations), h.monitor)
			r.Get("/by-no/:tripNo", requireAuth, need(authz.OperationsViewDriverMon, authz.AccountingViewIncome), h.tripByNo)
			r.Get("/:id", requireAuth, need(authz.OperationsViewDriverMon, authz.AccountingViewIncome), h.getTrip)
			r.Get("/:id/photos", requireAuth, need(authz.OperationsViewDriverMon), h.tripPhotos)
		}},
		{Prefix: StandbyPrefix, Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need(authz.OperationsViewDriverMon, authz.DispatchViewOperations), h.listStandby)
		}},
		{Prefix: IncidentsPrefix, Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need(authz.OperationsViewIncidents, authz.DispatchViewOperations), h.listIncidents)
		}},
	}
}

// staffOrDriver is RequireCap(caps...) that also admits a driver acting as itself (driver:self, R33): the service then
// reads with the driver's reach (its own and helper tasks), so another driver's task is 404.
func staffOrDriver(caps ...authz.Cap) fiber.Handler {
	staff := authz.RequireCap(caps...)
	return func(c fiber.Ctx) error {
		if p := authz.PrincipalFrom(c); p != nil && driverPrincipal(p) {
			return c.Next()
		}
		return staff(c)
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

// pathText decodes a text path parameter (Fiber leaves path params percent-encoded); a malformed escape names nothing.
func pathText(c fiber.Ctx, name string) (string, error) {
	v, err := url.PathUnescape(c.Params(name))
	if err != nil || strings.TrimSpace(v) == "" {
		return "", httpx.ErrNotFound()
	}
	return v, nil
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

// multi reads a repeatable list parameter: every occurrence, each split on commas.
func multi(c fiber.Ctx, name string) []string {
	var out []string
	for _, raw := range c.RequestCtx().QueryArgs().PeekMulti(name) {
		for v := range strings.SplitSeq(string(raw), ",") {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// --- tasks ------------------------------------------------------------------------------------------------------

func (h handlers) listTasks(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "taskType", "date", "from", "to", "status", "driverId", "truckId", "q", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	items, next, err := h.s.ListTasksView(c.Context(), p, TaskListInput{TaskType: c.Query("taskType"), Date: c.Query("date"),
		From: c.Query("from"), To: c.Query("to"), Statuses: multi(c, "status"), DriverID: c.Query("driverId"),
		TruckID: c.Query("truckId"), Q: c.Query("q"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, items, next, nil)
}

func (h handlers) tasksByNumber(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	no, err := pathText(c, "taskNo")
	if err != nil {
		return err
	}
	out, err := h.s.TasksByNumberView(c.Context(), p, no)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) getTask(c fiber.Ctx) error {
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
	out, err := h.s.GetTaskView(c.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) taskTrip(c fiber.Ctx) error {
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
	out, err := h.s.TaskTripView(c.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

// patch is PATCH /v1/tasks/{id}: 200 with the reassignment (Reassigned).
func (h handlers) patch(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if len(c.Queries()) > 0 {
		return httpx.ErrBadRequest("this route takes no query parameter")
	}
	in, err := ParseReassign(c.Body())
	if err != nil {
		return err
	}
	out, err := h.s.ReassignTask(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

// --- trips ------------------------------------------------------------------------------------------------------

func (h handlers) listTrips(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "axis", "from", "to", "status", "driverId", "customerId", "truckId", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	items, next, err := h.s.ListTripsView(c.Context(), p, TripListInput{Axis: c.Query("axis"), From: c.Query("from"),
		To: c.Query("to"), Statuses: multi(c, "status"), DriverID: c.Query("driverId"), CustomerID: c.Query("customerId"),
		TruckID: c.Query("truckId"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, items, next, nil)
}

func (h handlers) monitor(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "from", "to", "month", "status", "jobType", "driverId", "customerId", "partner", "plate",
		"vehicleClass", "origin", "destination", "q", "limit", "cursor", "facets"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	noFacets := false
	switch c.Query("facets") {
	case "", "true", "1":
	case "false", "0":
		noFacets = true
	default:
		return httpx.ErrInvalidArgument(violation("facets", "invalid"))
	}
	rows, next, meta, err := h.s.Monitor(c.Context(), p, MonitorInput{From: c.Query("from"), To: c.Query("to"),
		Month: c.Query("month"), Statuses: multi(c, "status"), JobType: c.Query("jobType"), DriverID: c.Query("driverId"),
		CustomerID: c.Query("customerId"), Partner: c.Query("partner"), Plate: c.Query("plate"),
		VehicleClass: c.Query("vehicleClass"), Origin: c.Query("origin"), Destination: c.Query("destination"),
		Q: c.Query("q"), Limit: limit, Cursor: c.Query("cursor"), NoFacets: noFacets})
	if err != nil {
		return err
	}
	if meta == nil {
		return httpx.Page(c, rows, next, nil)
	}
	return httpx.Page(c, rows, next, meta)
}

func (h handlers) tripByNo(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	no, err := pathText(c, "tripNo")
	if err != nil {
		return err
	}
	out, err := h.s.TripByNoView(c.Context(), p, no)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) getTrip(c fiber.Ctx) error {
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
	out, err := h.s.GetTripView(c.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) tripPhotos(c fiber.Ctx) error {
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
	out, err := h.s.TripPhotosView(c.Context(), p, id)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "private, no-store") // the URLs are short-lived capabilities
	return httpx.JSON(c, http.StatusOK, out)
}

// --- standby and incidents -----------------------------------------------------------------------------------

func (h handlers) listStandby(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "sort", "from", "to", "driverId", "customerId", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	items, next, err := h.s.ListStandbyView(c.Context(), p, StandbyListInput{Sort: c.Query("sort"), From: c.Query("from"),
		To: c.Query("to"), DriverID: c.Query("driverId"), CustomerID: c.Query("customerId"), Limit: limit,
		Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, items, next, nil)
}

func (h handlers) listIncidents(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "from", "to", "tripId", "driverId", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	items, next, err := h.s.ListIncidentsView(c.Context(), p, IncidentListInput{From: c.Query("from"), To: c.Query("to"),
		TripID: c.Query("tripId"), DriverID: c.Query("driverId"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, items, next, nil)
}
