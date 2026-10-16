package billing

import (
	"net/http"
	"net/url"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// TripRoutes are the billing routes of T38 below /v1/trips (internal listener), mounted by operations.Groups on the
// router of its trips group (one group per prefix):
//
//	POST /v1/trips/{id}/billing/compute   authenticated (the trip in the caller's reach); forceRecompute:
//	                                      accounting:recompute_force + own fleet; amounts answered to the own fleet only
//	PUT  /v1/trips/{id}/billing/manual    accounting:override_price + own fleet
//	POST /v1/trips/{id}/job-category      accounting:edit_rate_card + own fleet
func TripRoutes(s *Service, requireAuth fiber.Handler) func(r fiber.Router) {
	h := handlers{s}
	return func(r fiber.Router) {
		r = ingress.DenyRoutes(r, "P1.0")
		r.Post("/:id/billing/compute", requireAuth, h.computeTrip)
		r.Put("/:id/billing/manual", requireAuth, authz.RequireCap(authz.AccountingOverridePrice), authz.RequireTenant(), h.manualPrice)
		r.Post("/:id/job-category", requireAuth, authz.RequireCap(authz.AccountingEditRateCard), authz.RequireTenant(), h.jobCategory)
	}
}

// StandbyRoutes are the billing routes of T38 below /v1/standby (internal listener), mounted by operations.Groups:
//
//	POST  /v1/standby/{id}/billing/compute   authenticated; forceRecompute: accounting:recompute_force + own fleet
//	PATCH /v1/standby/{id}/customer          accounting:edit_rate_card + own fleet (a priced record: + recompute_force)
func StandbyRoutes(s *Service, requireAuth fiber.Handler) func(r fiber.Router) {
	h := handlers{s}
	return func(r fiber.Router) {
		r = ingress.DenyRoutes(r, "P1.0")
		r.Post("/:id/billing/compute", requireAuth, h.computeStandby)
		r.Patch("/:id/customer", requireAuth, authz.RequireCap(authz.AccountingEditRateCard), authz.RequireTenant(), h.standbyCustomer)
	}
}

// MobileRoutes is the shim target of T38 below /v1/mobile (public listener, and internal), mounted by app.MobileGroups:
//
//	POST /v1/mobile/trips/{id}/billing/compute   mobile:submit_trip + driver:self; requireShim admits a cf_shim API key
//	                                             together with the driver's Firebase ID token (R45)
func MobileRoutes(s *Service, requireShim fiber.Handler) func(r fiber.Router) {
	h := handlers{s}
	return func(r fiber.Router) {
		r = ingress.DenyRoutes(r, "P1.0")
		r.Post("/trips/:id/billing/compute", requireShim, authz.RequireCap(authz.MobileSubmitTrip), h.computeTripShim)
	}
}

// JobGroups are the billing jobs of T38, each its own group next to /v1/jobs (T10), behind requireAuth; the capability
// of each type is jobs.TypeCapabilities (staff of the job's tenant holding it read the job):
//
//	POST /v1/jobs/billing.backfill-trips     accounting:edit_rate_card + own fleet; forceRecompute: + recompute_force
//	POST /v1/jobs/billing.backfill-standby   accounting:edit_rate_card + own fleet; forceRecompute: + recompute_force
//	POST /v1/jobs/billing.impact-report      security:view_overview + own fleet (read-only, ADR 0009)
func JobGroups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	group := func(typ string) ingress.Group {
		return ingress.Group{Prefix: "/v1/jobs/" + typ, Mount: func(r fiber.Router) {
			r = ingress.DenyRoutes(r, "P1.0")
			r.Post("", requireAuth, authz.RequireCap(jobs.TypeCapabilities[typ]), authz.RequireTenant(), h.submitBillingJob(typ))
		}}
	}
	return []ingress.Group{group(jobs.TypeBillingBackfillTrips), group(jobs.TypeBillingBackfillStandby),
		group(jobs.TypeBillingImpactReport)}
}

func (h handlers) computeTrip(c fiber.Ctx) error {
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
	var in ComputeInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.ComputeTrip(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) jobCategory(c fiber.Ctx) error {
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
	var in JobCategoryInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.SetTripJobCategory(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) manualPrice(c fiber.Ctx) error {
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
	var in ManualPriceInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.SetTripManualPrice(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) computeStandby(c fiber.Ctx) error {
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
	var in ComputeInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.ComputeStandby(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) standbyCustomer(c fiber.Ctx) error {
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
	var in StandbyCustomerInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.AssignStandbyCustomer(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) computeTripShim(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	ref, err := url.PathUnescape(c.Params("id"))
	if err != nil {
		return httpx.ErrNotFound()
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	var in ComputeInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.ComputeTripForShim(c.Context(), p, httpx.RequestIDFrom(c), ref, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) submitBillingJob(typ string) fiber.Handler {
	return func(c fiber.Ctx) error {
		p, err := principal(c)
		if err != nil {
			return err
		}
		if err := onlyQuery(c); err != nil {
			return err
		}
		var in BillingJobParams
		if err := httpx.DecodeJSON(c, &in); err != nil {
			return err
		}
		j, err := h.s.SubmitBillingJob(c.Context(), p, typ, in)
		if err != nil {
			return err
		}
		return httpx.JSON(c, http.StatusAccepted, map[string]any{"jobId": j.ID})
	}
}
