package expenses

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/xlsx"
)

// Prefix is the route prefix of the vehicle expenses (Appendix B §B.2.15, internal listener).
const Prefix = "/v1/expenses"

// Groups are the routes of /v1/expenses on the internal listener behind requireAuth (auth.Service.RequireAuth,
// which resolves the principal through iam.RBAC.Authorize). s may be nil while the route table is only listed. The
// static paths (bulk-status, toll-import) are registered before /{id}.
//
//	GET   /v1/expenses                 accounting:view_fuel | accounting:view_other | accounting:audit_expense (per type)
//	POST  /v1/expenses                 accounting:edit_fuel | accounting:edit_other (per type)
//	POST  /v1/expenses/bulk-status     accounting:audit_expense
//	POST  /v1/expenses/toll-import     accounting:edit_other
//	PATCH /v1/expenses/{id}            accounting:edit_fuel | accounting:edit_other (the row's type)
//	POST  /v1/expenses/{id}/status     accounting:audit_expense
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	need := authz.RequireCap
	return []ingress.Group{{Prefix: Prefix, Mount: func(r fiber.Router) {
		r = ingress.DenyRoutes(r, "P1.0")
		r.Get("", requireAuth, need(authz.AccountingViewFuel, authz.AccountingViewOther, authz.AccountingAuditExpense), h.list)
		r.Post("", requireAuth, need(authz.AccountingEditFuel, authz.AccountingEditOther), h.create)
		r.Post("/bulk-status", requireAuth, need(authz.AccountingAuditExpense), h.bulkStatus)
		r.Post("/toll-import", requireAuth, need(authz.AccountingEditOther), h.tollImport)
		r.Patch("/:id", requireAuth, need(authz.AccountingEditFuel, authz.AccountingEditOther), h.patch)
		r.Post("/:id/status", requireAuth, need(authz.AccountingAuditExpense), h.status)
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

type listMeta struct {
	StatusCounts StatusCounts `json:"statusCounts"`
}

func (h handlers) list(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "type", "status", "category", "driverId", "truckId", "from", "to", "limit", "cursor"); err != nil {
		return err
	}
	limit := 0
	if v := c.Query("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit == 0 {
			return httpx.ErrInvalidArgument(violation("limit", "out_of_range"))
		}
	}
	out, next, counts, err := h.s.List(c.Context(), p, ListInput{Type: c.Query("type"), Status: c.Query("status"),
		Category: c.Query("category"), DriverID: c.Query("driverId"), TruckID: c.Query("truckId"), From: c.Query("from"),
		To: c.Query("to"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	var meta any
	if counts != nil {
		meta = listMeta{StatusCounts: counts}
	}
	return httpx.Page(c, out, next, meta)
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

func (h handlers) bulkStatus(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in BulkStatusInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.BulkStatus(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

// MaxImportColumns bounds the width of a toll sheet (the card histories have six columns).
const MaxImportColumns = 256

// importRetryAfter is the Retry-After (seconds) of an import refused while the process's spreadsheet reads are busy.
const importRetryAfter = 2

// tollBody is the JSON form of POST /v1/expenses/toll-import.
type tollBody struct {
	DriverID string         `json:"driverId"`
	TruckID  string         `json:"truckId"`
	DryRun   bool           `json:"dryRun"`
	Rows     []TollRowInput `json:"rows"`
}

// tollImport reads a multipart upload (field file: an XLSX workbook or CSV, first sheet; form fields driverId,
// truckId, dryRun) or a JSON body {driverId, truckId, dryRun?, rows[]}. The file is read within the bounds of
// internal/platform/xlsx; while the process already runs xlsx.MaxConcurrentReads reads the answer is 429
// resource_exhausted with Retry-After.
func (h handlers) tollImport(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in TollImportInput
	if strings.HasPrefix(strings.ToLower(c.Get(fiber.HeaderContentType)), fiber.MIMEMultipartForm) {
		fh, err := c.FormFile("file")
		if err != nil {
			return httpx.ErrInvalidArgument(violation("file", "required"))
		}
		f, err := fh.Open()
		if err != nil {
			return httpx.ErrInvalidArgument(violation("file", "invalid"))
		}
		defer func() { _ = f.Close() }()
		sheet, err := xlsx.Read(f, xlsx.Options{MaxRows: MaxTollRows, MaxCols: MaxImportColumns})
		switch {
		case errors.Is(err, xlsx.ErrBusy):
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(importRetryAfter))
			return httpx.NewError(http.StatusTooManyRequests, httpx.CodeResourceExhaust, "too many imports in progress").
				WithDetails(map[string]any{"retryAfterSeconds": importRetryAfter})
		case errors.Is(err, xlsx.ErrTooManyRows):
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "file", Reason: "too_many_rows",
				Params: map[string]any{"max": MaxTollRows}})
		case errors.Is(err, xlsx.ErrTooManyColumns):
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "file", Reason: "too_many_columns",
				Params: map[string]any{"max": MaxImportColumns}})
		case errors.Is(err, xlsx.ErrNotSpreadsheet):
			return httpx.ErrInvalidArgument(violation("file", "invalid"))
		case err != nil:
			return err
		}
		if sheet.Header == nil {
			return httpx.ErrInvalidArgument(violation("file", "empty"))
		}
		if in.Rows, err = TollRowsFromSheet(sheet); err != nil {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "file", Reason: "missing_column",
				Params: map[string]any{"columns": []string{"date", "type", "amount"}}})
		}
		in.DriverID, in.TruckID = c.FormValue("driverId"), c.FormValue("truckId")
		if v := c.FormValue("dryRun"); v != "" {
			if in.DryRun, err = strconv.ParseBool(v); err != nil {
				return httpx.ErrInvalidArgument(violation("dryRun", "invalid"))
			}
		}
	} else {
		var body tollBody
		if err := httpx.DecodeJSON(c, &body); err != nil {
			return err
		}
		if len(body.Rows) > MaxTollRows {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "rows", Reason: "too_many",
				Params: map[string]any{"max": MaxTollRows}})
		}
		in = TollImportInput{DriverID: body.DriverID, TruckID: body.TruckID, DryRun: body.DryRun, Rows: TollRowsFromJSON(body.Rows)}
	}
	out, err := h.s.ImportTolls(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}
