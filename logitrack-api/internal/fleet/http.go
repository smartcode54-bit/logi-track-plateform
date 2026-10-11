package fleet

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

// TrucksPrefix is the route prefix of T22 (Appendix B §B.2.8, internal listener).
const TrucksPrefix = "/v1/trucks"

// AssignmentsPrefix is the route prefix of T23's truck assignments (Appendix B §B.2.8, internal listener).
const AssignmentsPrefix = "/v1/truck-assignments"

// Groups are the routes of T22 on the internal listener behind requireAuth (auth.Service.RequireAuth, which resolves
// the principal through iam.RBAC.Authorize). s may be nil while the route table is only listed. The static paths
// (import, renewals) are registered before /{id}.
//
//	GET   /v1/trucks                 fleet:view_trucks | dispatch:view_operations (a dispatcher: scope_trucks)
//	POST  /v1/trucks                 fleet:create_truck
//	POST  /v1/trucks/import          fleet:create_truck (updating an existing plate also needs fleet:edit_truck)
//	GET   /v1/trucks/renewals        fleet:view_renewals
//	GET   /v1/trucks/{id}            fleet:view_trucks
//	PATCH /v1/trucks/{id}            fleet:edit_truck
//	POST  /v1/trucks/{id}/renewals   fleet:manage_renewals
//
// and the truck assignments of T23:
//
//	GET   /v1/truck-assignments              fleet:view_assignments
//	POST  /v1/truck-assignments              fleet:manage_assignments
//	POST  /v1/truck-assignments/{id}/revoke  fleet:manage_assignments
func Groups(s *Service, requireAuth fiber.Handler) []ingress.Group {
	h := handlers{s}
	need := authz.RequireCap
	return []ingress.Group{{Prefix: TrucksPrefix, Mount: func(r fiber.Router) {
		r.Get("", requireAuth, need(authz.FleetViewTrucks, authz.DispatchViewOperations), h.list)
		r.Post("", requireAuth, need(authz.FleetCreateTruck), h.create)
		r.Post("/import", requireAuth, need(authz.FleetCreateTruck), h.importTrucks)
		r.Get("/renewals", requireAuth, need(authz.FleetViewRenewals), h.renewals)
		r.Get("/:id", requireAuth, need(authz.FleetViewTrucks), h.get)
		r.Patch("/:id", requireAuth, need(authz.FleetEditTruck), h.patch)
		r.Post("/:id/renewals", requireAuth, need(authz.FleetManageRenewals), h.renew)
	}}, {Prefix: AssignmentsPrefix, Mount: func(r fiber.Router) {
		r.Get("", requireAuth, need(authz.FleetViewAssignments), h.listAssignments)
		r.Post("", requireAuth, need(authz.FleetManageAssignments), h.assign)
		r.Post("/:id/revoke", requireAuth, need(authz.FleetManageAssignments), h.revoke)
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

func (h handlers) list(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "ownership", "tenantId", "status", "type", "q", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, err := h.s.ListTrucks(c.Context(), p, ListTrucksInput{Ownership: c.Query("ownership"),
		TenantID: c.Query("tenantId"), Status: c.Query("status"), Type: c.Query("type"), Q: c.Query("q"),
		Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, nil)
}

func (h handlers) get(c fiber.Ctx) error {
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
	out, err := h.s.GetTruck(c.Context(), p, id)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) create(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in TruckInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.CreateTruck(c.Context(), p, httpx.RequestIDFrom(c), in)
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
	var in TruckInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.PatchTruck(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) renewals(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "due", "ownership", "status", "tenantId", "q", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, err := h.s.ListRenewals(c.Context(), p, ListRenewalsInput{Due: c.Query("due"), Ownership: c.Query("ownership"),
		Status: c.Query("status"), TenantID: c.Query("tenantId"), Q: c.Query("q"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, nil)
}

func (h handlers) renew(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	var in RenewalInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, created, err := h.s.RecordRenewal(c.Context(), p, httpx.RequestIDFrom(c), id, in)
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return httpx.JSON(c, status, out)
}

// MaxImportColumns bounds the width of an import sheet (the legacy template has 14 columns).
const MaxImportColumns = 256

// importRetryAfter is the Retry-After (seconds) of an import refused while the process's spreadsheet reads are busy.
const importRetryAfter = 2

// importBody is the JSON form of POST /v1/trucks/import.
type importBody struct {
	TenantID string           `json:"tenantId"`
	DryRun   bool             `json:"dryRun"`
	Rows     []map[string]any `json:"rows"`
}

// importTrucks reads a multipart upload (field file: an XLSX workbook or CSV, first sheet, header row first; form
// fields tenantId and dryRun) or a JSON body {tenantId?, dryRun?, rows[]}. The file is read within the bounds of
// internal/platform/xlsx (size, shared strings, rows, lines, columns); while the process already runs
// xlsx.MaxConcurrentReads reads the answer is 429 resource_exhausted with Retry-After.
func (h handlers) importTrucks(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in ImportInput
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
		sheet, err := xlsx.Read(f, xlsx.Options{MaxRows: MaxImportRows, MaxCols: MaxImportColumns})
		switch {
		case errors.Is(err, xlsx.ErrBusy):
			// Every import of the process shares MaxConcurrentReads reads (each may hold tens of megabytes).
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(importRetryAfter))
			return httpx.NewError(http.StatusTooManyRequests, httpx.CodeResourceExhaust, "too many imports in progress").
				WithDetails(map[string]any{"retryAfterSeconds": importRetryAfter})
		case errors.Is(err, xlsx.ErrTooManyRows):
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "file", Reason: "too_many_rows",
				Params: map[string]any{"max": MaxImportRows}})
		case errors.Is(err, xlsx.ErrTooManyColumns):
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "file", Reason: "too_many_columns",
				Params: map[string]any{"max": MaxImportColumns}})
		case errors.Is(err, xlsx.ErrNotSpreadsheet):
			return httpx.ErrInvalidArgument(violation("file", "invalid"))
		case err != nil:
			return err
		}
		if in.Rows, err = RowsFromSheet(sheet); err != nil {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "file", Reason: "missing_column",
				Params: map[string]any{"column": "licensePlate"}})
		}
		in.TenantID = strings.TrimSpace(c.FormValue("tenantId"))
		if v := c.FormValue("dryRun"); v != "" {
			if in.DryRun, err = strconv.ParseBool(v); err != nil {
				return httpx.ErrInvalidArgument(violation("dryRun", "invalid"))
			}
		}
	} else {
		var body importBody
		if err := httpx.DecodeJSON(c, &body); err != nil {
			return err
		}
		if len(body.Rows) > MaxImportRows {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "rows", Reason: "too_many",
				Params: map[string]any{"max": MaxImportRows}})
		}
		in = ImportInput{TenantID: strings.TrimSpace(body.TenantID), DryRun: body.DryRun, Rows: RowsFromJSON(body.Rows)}
	}
	out, err := h.s.ImportTrucks(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}

func (h handlers) listAssignments(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	if err := onlyQuery(c, "status", "truckId", "driverId", "q", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	out, next, err := h.s.ListAssignments(c.Context(), p, ListAssignmentsInput{Status: c.Query("status"),
		TruckID: c.Query("truckId"), DriverID: c.Query("driverId"), Q: c.Query("q"), Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		return err
	}
	return httpx.Page(c, out, next, nil)
}

func (h handlers) assign(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	var in AssignInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.CreateAssignment(c.Context(), p, httpx.RequestIDFrom(c), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusCreated, out)
}

func (h handlers) revoke(c fiber.Ctx) error {
	p, err := principal(c)
	if err != nil {
		return err
	}
	id, err := pathID(c)
	if err != nil {
		return err
	}
	out, err := h.s.RevokeAssignment(c.Context(), p, httpx.RequestIDFrom(c), id)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, out)
}
