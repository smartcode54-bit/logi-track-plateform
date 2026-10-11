package billing

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

// Route prefixes (Appendix B §B.2.14, internal listener). The job route is its own group next to /v1/jobs (T10).
const (
	BillingPrefix = "/v1/billing"
	FuelPrefix    = "/v1/fuel"
	JobPrefix     = "/v1/jobs/" + jobs.TypeBangchakSnapshot
)

// Groups are the routes of T37 on the internal listener behind requireAuth (auth.Service.RequireAuth, which resolves
// the principal through iam.RBAC.Authorize). s may be nil while the route table is only listed. Writes stamp the
// request's effective tenant, the billing carrier (R61), so they also need one (authz.RequireTenant); every rate-table
// and statement write (marked *) further needs that tenant to be the own fleet, its staff or a platform admin acting
// on it (the service refuses another tenant with 403 permission_denied, reason own_fleet_only; main spec §19 Q13). The
// Bangchak job writes platform-wide snapshots, so it is steward-only (authz.RequireSteward, R60).
//
//	GET    /v1/billing/rate-entries                   accounting:view_rate_card (?customerId&includeVoided&limit&cursor)
//	POST   /v1/billing/rate-entries/import            accounting:edit_rate_card *
//	POST   /v1/billing/rate-entries                   accounting:edit_rate_card *
//	POST   /v1/billing/rate-entries/{id}/void         accounting:edit_rate_card *
//	GET    /v1/billing/fuel-adjustments               accounting:view_rate_card (?customerId&includeVoided)
//	POST   /v1/billing/fuel-adjustments               accounting:edit_rate_card *
//	POST   /v1/billing/fuel-adjustments/{id}/void     accounting:edit_rate_card *
//	GET    /v1/billing/service-fees                   accounting:view_rate_card (?customerId)
//	POST   /v1/billing/service-fees                   accounting:edit_rate_card *
//	PATCH  /v1/billing/service-fees/{id}              accounting:edit_rate_card *
//	DELETE /v1/billing/service-fees/{id}              accounting:edit_rate_card *
//	GET    /v1/billing/standby-rates                  accounting:view_rate_card (?customerId&includeVoided)
//	POST   /v1/billing/standby-rates                  accounting:edit_rate_card *
//	PATCH  /v1/billing/standby-rates/{id}             accounting:edit_rate_card *
//	POST   /v1/billing/standby-rates/{id}/void        accounting:edit_rate_card *
//	GET    /v1/billing/statements                     accounting:billing_result (?customerId&status&year&month&limit&cursor)
//	POST   /v1/billing/statements                     accounting:billing_document *
//	GET    /v1/billing/statements/{id}                accounting:billing_result
//	GET    /v1/billing/statements/{id}/lines          accounting:billing_result
//	POST   /v1/billing/statements/{id}/status         accounting:manage_statements *
//	DELETE /v1/billing/statements/{id}                accounting:manage_statements *
//	GET    /v1/fuel/daily                             accounting:view_fuel (?from&limit&cursor)
//	GET    /v1/fuel/daily/{dayKey}                    accounting:view_fuel
//	GET    /v1/fuel/monthly                           accounting:view_fuel (?limit&cursor)
//	GET    /v1/fuel/retail                            accounting:view_fuel (?locale=th|en)
//	POST   /v1/jobs/bangchak.snapshot                 accounting:edit_rate_card + steward (R60)
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	need := authz.RequireCap
	view, edit := need(authz.AccountingViewRateCard), need(authz.AccountingEditRateCard)
	tenant := authz.RequireTenant()
	return []ingress.Group{
		{Prefix: BillingPrefix, Mount: func(r fiber.Router) {
			r.Get("/rate-entries", requireAuth, view, h.listRateEntries)
			r.Post("/rate-entries/import", requireAuth, edit, tenant, h.importRateEntries)
			r.Post("/rate-entries", requireAuth, edit, tenant, h.createRateEntry)
			r.Post("/rate-entries/:id/void", requireAuth, edit, h.voidRateEntry)
			r.Get("/fuel-adjustments", requireAuth, view, h.listFuelAdjustments)
			r.Post("/fuel-adjustments", requireAuth, edit, tenant, h.createFuelAdjustment)
			r.Post("/fuel-adjustments/:id/void", requireAuth, edit, h.voidFuelAdjustment)
			r.Get("/service-fees", requireAuth, view, h.listServiceFees)
			r.Post("/service-fees", requireAuth, edit, tenant, h.createServiceFee)
			r.Patch("/service-fees/:id", requireAuth, edit, h.patchServiceFee)
			r.Delete("/service-fees/:id", requireAuth, edit, h.deleteServiceFee)
			r.Get("/standby-rates", requireAuth, view, h.listStandbyRates)
			r.Post("/standby-rates", requireAuth, edit, tenant, h.createStandbyRate)
			r.Patch("/standby-rates/:id", requireAuth, edit, h.patchStandbyRate)
			r.Post("/standby-rates/:id/void", requireAuth, edit, h.voidStandbyRate)
			r.Get("/statements", requireAuth, need(authz.AccountingBillingResult), h.listStatements)
			r.Post("/statements", requireAuth, need(authz.AccountingBillingDocument), tenant, h.createStatement)
			r.Get("/statements/:id", requireAuth, need(authz.AccountingBillingResult), h.getStatement)
			r.Get("/statements/:id/lines", requireAuth, need(authz.AccountingBillingResult), h.statementLines)
			r.Post("/statements/:id/status", requireAuth, need(authz.AccountingManageStatements), h.setStatementStatus)
			r.Delete("/statements/:id", requireAuth, need(authz.AccountingManageStatements), h.deleteStatement)
		}},
		{Prefix: FuelPrefix, Mount: func(r fiber.Router) {
			fuel := need(authz.AccountingViewFuel)
			r.Get("/daily", requireAuth, fuel, h.listDaily)
			r.Get("/daily/:dayKey", requireAuth, fuel, h.getDaily)
			r.Get("/monthly", requireAuth, fuel, h.listMonthly)
			r.Get("/retail", requireAuth, fuel, h.retail)
		}},
		{Prefix: JobPrefix, Mount: func(r fiber.Router) {
			r.Post("", requireAuth, need(jobs.TypeCapabilities[jobs.TypeBangchakSnapshot]), authz.RequireSteward(), h.submitBangchak)
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

// queryInt reads an integer parameter (0 when absent); a non-number or an explicit 0 is out of range.
func queryInt(c fiber.Ctx, name string) (int, error) {
	v := c.Query(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n == 0 {
		return 0, httpx.ErrInvalidArgument(violation(name, "out_of_range"))
	}
	return n, nil
}

// queryBool reads a true|false parameter (false when absent).
func queryBool(c fiber.Ctx, name string) (bool, error) {
	switch c.Query(name) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	}
	return false, httpx.ErrInvalidArgument(violation(name, "invalid"))
}

// listInput reads ?customerId&includeVoided&limit&cursor of the rate-table lists.
func listInput(c fiber.Ctx, allowed ...string) (ListInput, error) {
	if err := onlyQuery(c, allowed...); err != nil {
		return ListInput{}, err
	}
	in := ListInput{CustomerID: c.Query("customerId"), Cursor: c.Query("cursor")}
	var err error
	if in.IncludeVoided, err = queryBool(c, "includeVoided"); err != nil {
		return in, err
	}
	in.Limit, err = queryInt(c, "limit")
	return in, err
}

func (h handlers) listRateEntries(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	in, err := listInput(c, "customerId", "includeVoided", "limit", "cursor")
	if err != nil {
		return err
	}
	out, next, err := h.s.ListRateEntries(c.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, nil)
}

func (h handlers) importRateEntries(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in ImportInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.ImportRateEntries(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) createRateEntry(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in CreateRateEntryInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.CreateRateEntry(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

// void runs one of the three void routes.
func void[T any](c fiber.Ctx, fn func(p *authz.Principal, rid string, id uuid.UUID, in VoidInput) (*T, error)) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in VoidInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := fn(p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) voidRateEntry(c fiber.Ctx) error {
	return void(c, func(p *authz.Principal, rid string, id uuid.UUID, in VoidInput) (*RateEntry, error) {
		return h.s.VoidRateEntry(c.Context(), p, rid, id, in)
	})
}

func (h handlers) listFuelAdjustments(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	in, err := listInput(c, "customerId", "includeVoided")
	if err != nil {
		return err
	}
	out, err := h.s.ListFuelAdjustments(c.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) createFuelAdjustment(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in FuelAdjustmentInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.CreateFuelAdjustment(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) voidFuelAdjustment(c fiber.Ctx) error {
	return void(c, func(p *authz.Principal, rid string, id uuid.UUID, in VoidInput) (*FuelAdjustment, error) {
		return h.s.VoidFuelAdjustment(c.Context(), p, rid, id, in)
	})
}

func (h handlers) listServiceFees(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	in, err := listInput(c, "customerId")
	if err != nil {
		return err
	}
	out, err := h.s.ListServiceFees(c.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) createServiceFee(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in ServiceFeeInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.CreateServiceFee(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) patchServiceFee(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in ServiceFeeInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.PatchServiceFee(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) deleteServiceFee(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if err := h.s.DeleteServiceFee(c.Context(), p, httpx.RequestIDFrom(c), id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h handlers) listStandbyRates(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	in, err := listInput(c, "customerId", "includeVoided")
	if err != nil {
		return err
	}
	out, err := h.s.ListStandbyRates(c.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) createStandbyRate(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in StandbyRateInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.CreateStandbyRate(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) patchStandbyRate(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in StandbyRateInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.PatchStandbyRate(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) voidStandbyRate(c fiber.Ctx) error {
	return void(c, func(p *authz.Principal, rid string, id uuid.UUID, in VoidInput) (*StandbyRate, error) {
		return h.s.VoidStandbyRate(c.Context(), p, rid, id, in)
	})
}

func (h handlers) listStatements(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "customerId", "status", "year", "month", "limit", "cursor"); err != nil {
		return err
	}
	in := ListStatementsInput{CustomerID: c.Query("customerId"), Status: c.Query("status"), Cursor: c.Query("cursor")}
	for _, f := range []struct {
		name string
		dst  *int
	}{{"year", &in.Year}, {"month", &in.Month}, {"limit", &in.Limit}} {
		if *f.dst, err = queryInt(c, f.name); err != nil {
			return err
		}
	}
	out, next, err := h.s.ListStatements(c.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, nil)
}

func (h handlers) createStatement(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in StatementInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.CreateStatement(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) getStatement(c fiber.Ctx) error {
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
	out, err := h.s.GetStatement(c.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) statementLines(c fiber.Ctx) error {
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
	out, err := h.s.StatementLines(c.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) setStatementStatus(c fiber.Ctx) error {
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
	out, err := h.s.SetStatementStatus(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) deleteStatement(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	if err := h.s.DeleteStatement(c.Context(), p, id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (h handlers) listDaily(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "from", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryInt(c, "limit")
	if err != nil {
		return err
	}
	out, next, err := h.s.ListDailySnapshots(c.Context(), p, DailyInput{From: c.Query("from"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, nil)
}

func (h handlers) getDaily(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	out, err := h.s.GetDailySnapshot(c.Context(), p, c.Params("dayKey"))
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) listMonthly(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryInt(c, "limit")
	if err != nil {
		return err
	}
	out, next, err := h.s.ListMonthlySnapshots(c.Context(), p, MonthlyInput{Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, nil)
}

func (h handlers) retail(c fiber.Ctx) error {
	if err := onlyQuery(c, "locale"); err != nil {
		return err
	}
	out, err := h.s.RetailPrices(c.Context(), c.Query("locale"))
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

// submitBangchak is POST /v1/jobs/bangchak.snapshot: 202 {"data":{"jobId"}} (Appendix B §B.1.5).
func (h handlers) submitBangchak(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c); err != nil {
		return err
	}
	j, err := h.s.SubmitBangchakSnapshot(c.Context(), p)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusAccepted, map[string]any{"jobId": j.ID})
}
