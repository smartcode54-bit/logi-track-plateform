package billing

import (
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// readRoutes are the routes of T39 under /v1/billing (Appendix B §B.2.14), mounted by Groups:
//
//	GET  /v1/billing/rows                                   accounting:billing_document (?customerId=<id>|all&year&month&types&categories&plate&vehicleClass&reviewMonth)
//	GET  /v1/billing/rows/missing-billing-date              accounting:billing_document (?customerId&year&month)
//	GET  /v1/billing/standby-diagnostics                    accounting:billing_document (?customerId=<id>|all&year&month)
//	GET  /v1/billing/shopee-report                          accounting:shopee_report (?customerId&year&month&half)
//	GET  /v1/billing/shopee-report.pdf                      accounting:shopee_report (302 to a current render, else 202 {jobId})
//	GET  /v1/billing/statements/{id}/documents/{kind}       accounting:billing_result (302 to the stored document)
//	POST /v1/billing/statements/{id}/documents/regenerate   accounting:manage_statements * ({showActualPickup?} -> 202 {jobId})
//
// The billing aggregates and the regenerate are the own fleet's (403 own_fleet_only for another tenant, like the
// statements they preview); the Shopee report reads under the caller's RLS.
func readRoutes(r fiber.Router, h handlers, requireAuth fiber.Handler) {
	need := authz.RequireCap
	doc := need(authz.AccountingBillingDocument)
	r.Get("/rows", requireAuth, doc, h.billingRows)
	r.Get("/rows/missing-billing-date", requireAuth, doc, h.missingBillingDate)
	r.Get("/standby-diagnostics", requireAuth, doc, h.standbyDiagnostics)
	r.Get("/shopee-report", requireAuth, need(authz.AccountingShopeeReport), h.shopeeReport)
	r.Get("/shopee-report.pdf", requireAuth, need(authz.AccountingShopeeReport), h.shopeeReportPDF)
	r.Get("/statements/:id/documents/:kind", requireAuth, need(authz.AccountingBillingResult), h.documentDownload)
	r.Post("/statements/:id/documents/regenerate", requireAuth, need(authz.AccountingManageStatements), h.regenerateDocuments)
}

// queryList reads a list parameter given as repeated keys (types=a&types=b), the bracket form (types[]=a) or one
// comma-separated value; set reports whether the parameter was given at all.
func queryList(c fiber.Ctx, name string) (values []string, set bool) {
	args := c.RequestCtx().QueryArgs()
	for _, key := range []string{name, name + "[]"} {
		for _, raw := range args.PeekMulti(key) {
			set = true
			for v := range strings.SplitSeq(string(raw), ",") {
				if v = strings.TrimSpace(v); v != "" {
					values = append(values, v)
				}
			}
		}
	}
	return values, set
}

// period reads ?year&month (0 when absent: the service refuses it).
func period(c fiber.Ctx) (int, int, error) {
	year, err := queryInt(c, "year")
	if err != nil {
		return 0, 0, err
	}
	month, err := queryInt(c, "month")
	return year, month, err
}

func (h handlers) billingRows(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "customerId", "year", "month", "types", "types[]", "categories", "categories[]", "plate",
		"vehicleClass", "reviewMonth"); err != nil {
		return err
	}
	year, month, err := period(c)
	if err != nil {
		return err
	}
	types, typesSet := queryList(c, "types")
	cats, catsSet := queryList(c, "categories")
	out, err := h.s.BillingRows(c.Context(), p, RowsInput{CustomerID: c.Query("customerId"), Year: year, Month: month,
		Types: types, TypesSet: typesSet, Categories: cats, CategoriesSet: catsSet, Plate: c.Query("plate"),
		VehicleClass: c.Query("vehicleClass"), ReviewMonth: c.Query("reviewMonth")})
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) missingBillingDate(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "customerId", "year", "month"); err != nil {
		return err
	}
	year, month, err := period(c)
	if err != nil {
		return err
	}
	out, err := h.s.MissingBillingDate(c.Context(), p, c.Query("customerId"), year, month)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) standbyDiagnostics(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "customerId", "year", "month"); err != nil {
		return err
	}
	year, month, err := period(c)
	if err != nil {
		return err
	}
	out, err := h.s.StandbyDiagnostics(c.Context(), p, c.Query("customerId"), year, month)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) shopeeInput(c fiber.Ctx) (ShopeeInput, error) {
	if err := onlyQuery(c, "customerId", "year", "month", "half"); err != nil {
		return ShopeeInput{}, err
	}
	year, month, err := period(c)
	if err != nil {
		return ShopeeInput{}, err
	}
	return ShopeeInput{CustomerID: c.Query("customerId"), Year: year, Month: month, Half: c.Query("half")}, nil
}

func (h handlers) shopeeReport(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	in, err := h.shopeeInput(c)
	if err != nil {
		return err
	}
	out, err := h.s.ShopeeReport(c.Context(), p, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

// shopeeReportPDF answers 302 to the stored render, or 202 {"data":{"jobId"}} while one renders.
func (h handlers) shopeeReportPDF(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	in, err := h.shopeeInput(c)
	if err != nil {
		return err
	}
	out, err := h.s.ShopeeReportPDF(c.Context(), p, in)
	if err != nil {
		return err
	}
	if out.URL != "" {
		c.Set(fiber.HeaderCacheControl, "no-store")
		c.Set(fiber.HeaderLocation, out.URL)
		return c.SendStatus(http.StatusFound)
	}
	return httpx.JSON(c, http.StatusAccepted, map[string]any{"jobId": out.JobID})
}

// documentDownload answers 302 to a signed URL of the stored document.
func (h handlers) documentDownload(c fiber.Ctx) error {
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
	u, err := h.s.DocumentDownload(c.Context(), p, id, c.Params("kind"))
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderLocation, u)
	return c.SendStatus(http.StatusFound)
}

func (h handlers) regenerateDocuments(c fiber.Ctx) error {
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
	var in RegenerateInput // optional body {showActualPickup?}
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	j, err := h.s.RegenerateDocuments(c.Context(), p, id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusAccepted, map[string]any{"jobId": j.ID})
}
