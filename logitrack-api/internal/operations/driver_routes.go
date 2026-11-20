package operations

import (
	"context"
	"net/http"
	"net/url"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// DriverRoutes are the operations routes of the APK 4.x core loop below /v1/mobile (T55, spec
// shared-docs/specs/mobile-go-core-loop.md), mounted by app.MobileGroups beside MobileRoutes. They take a Go access
// token only (requireAuth) and act for the driver itself (driver:self); none of them is a shim target, so the 3.x APK
// and its callables never reach them.
//
//	GET  /v1/mobile/tasks                  mobile:view_tasks   the driver's queue (DriverQueue, ADR 0028 order)
//	POST /v1/mobile/tasks/{id}/check-in ✱  mobile:checkin      CheckInForDriver
//	POST /v1/mobile/trips ✱                mobile:submit_trip  CreateTripForDriver (loading, single stop)
//	POST /v1/mobile/trips/{id}/deliver ✱   mobile:submit_trip  DeliverTripForDriver ({id}: uuid or trip number)
//	GET  /v1/mobile/trips/pending-in-transit   mobile:submit_trip  PendingTripForDriver
//	GET  /v1/mobile/trips/by-no/{tripNo}       mobile:submit_trip  TripForDriver
//
// idem is the Idempotency-Key middleware the ✱ write routes run after authentication. s may be nil while the route
// table is only listed.
func DriverRoutes(s *Service, requireAuth, idem fiber.Handler) func(r fiber.Router) {
	h := handlers{s}
	return func(r fiber.Router) {
		r.Get("/tasks", requireAuth, authz.RequireCap(authz.MobileViewTasks), h.driverTasks)
		r.Post("/tasks/:id/check-in", requireAuth, authz.RequireCap(authz.MobileCheckin), idem, h.driverWrite(h.s.CheckInForDriver))
		submit := authz.RequireCap(authz.MobileSubmitTrip)
		r.Post("/trips", requireAuth, submit, idem, h.driverWrite(h.s.CreateTripForDriver))
		r.Get("/trips/pending-in-transit", requireAuth, submit, h.driverPendingTrip)
		r.Get("/trips/by-no/:tripNo", requireAuth, submit, h.driverTripByNo)
		r.Post("/trips/:id/deliver", requireAuth, submit, idem, h.driverWrite(h.s.DeliverTripForDriver))
	}
}

// driverTasks is GET /v1/mobile/tasks[?status&limit&cursor]: the driver's own and helper tasks in queue order; without
// status the open queue on one page (up to MaxLimit), else a keyset page with nextCursor.
func (h handlers) driverTasks(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "status", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	page, err := h.s.DriverQueue(c.Context(), p, DriverQueueInput{Statuses: multi(c, "status"), Limit: limit,
		Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.Page(c, page.Items, page.NextCursor, nil)
}

// driverWrite adapts a driver write service (principal, request id, the {id} reference, body) to a handler. The
// handler works under c.Context() so the idempotency deadline cancels it (HandlerBudget).
func (h handlers) driverWrite(fn func(ctx context.Context, p *authz.Principal, rid, ref string, body []byte) (WriteResult, error)) fiber.Handler {
	return func(c fiber.Ctx) error {
		p, err := principal(c)
		if err != nil {
			return err
		}
		ref, err := url.PathUnescape(c.Params("id"))
		if err != nil {
			return httpx.ErrNotFound()
		}
		if len(c.Queries()) > 0 {
			return httpx.ErrBadRequest("this route takes no query parameter")
		}
		res, err := fn(c.Context(), p, httpx.RequestIDFrom(c), ref, c.Body())
		if err != nil {
			return err
		}
		return httpx.JSON(c, res.Status, res.Data)
	}
}

func (h handlers) driverPendingTrip(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	v, err := h.s.PendingTripForDriver(c.Context(), p)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusOK, v)
}

func (h handlers) driverTripByNo(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	no, err := url.PathUnescape(c.Params("tripNo"))
	if err != nil {
		return httpx.ErrNotFound()
	}
	v, err := h.s.TripForDriver(c.Context(), p, no)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return httpx.JSON(c, http.StatusOK, v)
}
