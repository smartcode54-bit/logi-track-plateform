package operations

import (
	"context"
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
// is only listed. Static paths are registered before /{id}. The writes (T31, write*.go) go to PostgreSQL while the
// operations domain is PostgreSQL-owned, else to Firestore first (the write-back).
//
//	GET   /v1/tasks                       operations:view_first_mile | operations:view_line_haul | dispatch:view_operations
//	GET   /v1/tasks/by-number/{taskNo}    operations:view_first_mile | operations:view_line_haul
//	GET   /v1/tasks/{id}                  operations:view_first_mile | operations:view_line_haul | driver:self (R33)
//	GET   /v1/tasks/{id}/trip             operations:view_first_mile | operations:view_line_haul
//	POST  /v1/tasks                       operations:manage_tasks (T31)
//	POST  /v1/tasks/import                operations:manage_tasks (T31)
//	PATCH /v1/tasks/{id}                  operations:manage_tasks | platform:cross_tenant_write: a tenantId other than the
//	                                      task's is the R13 move of T28 (a dispatcher's call-out or a platform admin),
//	                                      every other body the ordinary update under the caller's RLS (T31)
//	POST  /v1/tasks/{id}/cancel           operations:manage_tasks (T31)
//	POST  /v1/tasks/{id}/stops            operations:manage_tasks (T31)
//	GET   /v1/trips                       operations:view_driver_monitor | accounting:view_income | dispatch:view_operations
//	GET   /v1/trips/monitor               operations:view_driver_monitor | dispatch:view_operations (W8, pages of 200)
//	GET   /v1/trips/by-no/{tripNo}        operations:view_driver_monitor | accounting:view_income
//	GET   /v1/trips/{id}                  operations:view_driver_monitor | accounting:view_income
//	GET   /v1/trips/{id}/photos           operations:view_driver_monitor
//	PATCH /v1/trips/{id}, POST /v1/trips/{id}/deliver|cancel|rename, POST /v1/trips/bulk-cancel
//	                                      operations:edit_trip_details (rename: as tenant_admin) (T31)
//	GET   /v1/standby                     operations:view_driver_monitor | dispatch:view_operations
//	POST  /v1/standby/backfill            operations:create_standby (T31)
//	GET   /v1/incidents                   operations:view_incidents | dispatch:view_operations
//	POST  /v1/incidents                   operations:edit_trip_details (T31)
//
// The task type of a task is checked too: operations:view_first_mile reads first-mile tasks, operations:view_line_haul
// line-haul ones (dispatch:view_operations both). A dispatcher or a customer-scope principal is served from the scope_*
// projections (Appendix C §C.3.7).
//
// more are routes other packages serve below these prefixes (one group per prefix): the billing routes of T38 below
// /v1/trips and /v1/standby (internal/billing TripRoutes, StandbyRoutes).
func Groups(s *Service, requireAuth fiber.Handler, more ...Mounts) []ingress.Group {
	h := handlers{s}
	need := authz.RequireCap
	return []ingress.Group{
		{Prefix: TasksPrefix, Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need(authz.OperationsViewFirstMile, authz.OperationsViewLineHaul, authz.DispatchViewOperations),
				h.listTasks)
			r.Post("", requireAuth, need(authz.OperationsManageTasks), h.write(h.s.CreateTaskAPI))
			r.Post("/import", requireAuth, need(authz.OperationsManageTasks), h.write(h.s.ImportTasksAPI))
			r.Get("/by-number/:taskNo", requireAuth, need(authz.OperationsViewFirstMile, authz.OperationsViewLineHaul),
				h.tasksByNumber)
			r.Get("/:id", requireAuth, staffOrDriver(authz.OperationsViewFirstMile, authz.OperationsViewLineHaul), h.getTask)
			r.Get("/:id/trip", requireAuth, need(authz.OperationsViewFirstMile, authz.OperationsViewLineHaul), h.taskTrip)
			r.Patch("/:id", requireAuth, need(authz.OperationsManageTasks, authz.PlatformCrossTenantWrite), h.writeID(h.s.PatchTaskAPI))
			r.Post("/:id/cancel", requireAuth, need(authz.OperationsManageTasks), h.writeID(h.s.CancelTaskAPI))
			r.Post("/:id/stops", requireAuth, need(authz.OperationsManageTasks), h.writeID(h.s.AddTaskStopAPI))
		}},
		{Prefix: TripsPrefix, Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need(authz.OperationsViewDriverMon, authz.AccountingViewIncome, authz.DispatchViewOperations),
				h.listTrips)
			r.Get("/monitor", requireAuth, need(authz.OperationsViewDriverMon, authz.DispatchViewOperations), h.monitor)
			r.Post("/bulk-cancel", requireAuth, need(authz.OperationsEditTripDetails), h.write(h.s.BulkCancelTripsAPI))
			r.Get("/by-no/:tripNo", requireAuth, need(authz.OperationsViewDriverMon, authz.AccountingViewIncome), h.tripByNo)
			r.Get("/:id", requireAuth, need(authz.OperationsViewDriverMon, authz.AccountingViewIncome), h.getTrip)
			r.Patch("/:id", requireAuth, need(authz.OperationsEditTripDetails), h.writeID(h.s.PatchTripAPI))
			r.Get("/:id/photos", requireAuth, need(authz.OperationsViewDriverMon), h.tripPhotos)
			r.Post("/:id/deliver", requireAuth, need(authz.OperationsEditTripDetails), h.writeID(h.s.DeliverTripAPI))
			r.Post("/:id/cancel", requireAuth, need(authz.OperationsEditTripDetails), h.writeID(h.s.CancelTripAPI))
			r.Post("/:id/rename", requireAuth, need(authz.OperationsEditTripDetails), h.writeID(h.s.RenameTripAPI))
			for _, m := range more {
				for _, mount := range m.Trips {
					mount(r)
				}
			}
		}},
		{Prefix: StandbyPrefix, Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need(authz.OperationsViewDriverMon, authz.DispatchViewOperations), h.listStandby)
			r.Post("/backfill", requireAuth, need(authz.OperationsCreateStandby), h.write(h.s.StandbyBackfillAPI))
			for _, m := range more {
				for _, mount := range m.Standby {
					mount(r)
				}
			}
		}},
		{Prefix: IncidentsPrefix, Mount: func(r fiber.Router) {
			r.Get("", requireAuth, need(authz.OperationsViewIncidents, authz.DispatchViewOperations), h.listIncidents)
			r.Post("", requireAuth, need(authz.OperationsEditTripDetails), h.write(h.s.CreateIncidentAPI))
		}},
	}
}

// Mounts are routes another package serves below the trips and standby prefixes (fleet.Groups' truckRoutes pattern).
type Mounts struct {
	Trips   []func(r fiber.Router)
	Standby []func(r fiber.Router)
}

// write is the handler of a write route without a path id: no query parameter, the JSON body to the service, the
// answer's status and data in the success envelope.
func (h handlers) write(fn func(context.Context, *authz.Principal, string, []byte) (WriteResult, error)) fiber.Handler {
	return func(c fiber.Ctx) error {
		p, err := principal(c)
		if err != nil {
			return err
		}
		if len(c.Queries()) > 0 {
			return httpx.ErrBadRequest("this route takes no query parameter")
		}
		res, err := fn(c.Context(), p, httpx.RequestIDFrom(c), c.Body())
		if err != nil {
			return err
		}
		return httpx.JSON(c, res.Status, res.Data)
	}
}

// writeID is write for a route of one row (/:id: a uuid, anything else 404).
func (h handlers) writeID(fn func(context.Context, *authz.Principal, string, uuid.UUID, []byte) (WriteResult, error)) fiber.Handler {
	return func(c fiber.Ctx) error {
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
		res, err := fn(c.Context(), p, httpx.RequestIDFrom(c), id, c.Body())
		if err != nil {
			return err
		}
		return httpx.JSON(c, res.Status, res.Data)
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
	if err := onlyQuery(c, "taskType", "date", "from", "to", "status", "driverId", "truckId", "q", "sourceHub", "destination",
		"limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	items, next, err := h.s.ListTasksView(c.Context(), p, TaskListInput{TaskType: c.Query("taskType"), Date: c.Query("date"),
		From: c.Query("from"), To: c.Query("to"), Statuses: multi(c, "status"), DriverID: c.Query("driverId"),
		TruckID: c.Query("truckId"), Q: c.Query("q"), SourceHub: c.Query("sourceHub"), Destination: c.Query("destination"),
		Limit: limit, Cursor: c.Query("cursor")})
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
