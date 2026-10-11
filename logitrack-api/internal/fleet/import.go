package fleet

import (
	"context"
	"errors"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/fleetdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/xlsx"
)

// MaxImportRows bounds one import.
const MaxImportRows = 2000

// ImportRow is one row of an import: Line is the spreadsheet row number (the header is row 1) or the 1-based index of
// rows[] in a JSON body; Cells maps a column (see importColumns) to its cell text; Invalid lists the columns of a JSON
// row whose value was neither a string nor a number.
type ImportRow struct {
	Line    int
	Cells   map[string]string
	Invalid []string
}

// ImportInput is POST /v1/trucks/import: the rows, the tenant they are registered in (tenantId, as on POST
// /v1/trucks: the effective tenant or a carrier of its contractor reach) and dryRun (validate and count, write
// nothing).
type ImportInput struct {
	TenantID string
	DryRun   bool
	Rows     []ImportRow
}

// ImportRowError is one rejected row with its reasons (the field vocabulary of a 422 invalid_argument; a plate the
// tenant already has is licensePlate taken for a caller without fleet:edit_truck, a repeated plate
// licensePlate duplicate_in_file with params.row the first row that carried it).
type ImportRowError struct {
	Row          int                    `json:"row"`
	LicensePlate *string                `json:"licensePlate"`
	Fields       []httpx.FieldViolation `json:"fields"`
}

// ImportResult is the answer of POST /v1/trucks/import: rows created, rows that updated an existing truck of the same
// plate, rows that matched one without a change, and the rejected rows. With dryRun nothing was written.
type ImportResult struct {
	Created   int              `json:"created"`
	Updated   int              `json:"updated"`
	Unchanged int              `json:"unchanged"`
	Errors    []ImportRowError `json:"errors"`
	DryRun    bool             `json:"dryRun"`
}

// importColumns maps a normalised header (letters and digits, lower case: "License Plate", "licensePlate" and
// "license_plate" are all licenseplate) to its field: the legacy template of TruckImportDialog.tsx, the field names of
// POST /v1/trucks and the Thai headers of hand-made sheets.
var importColumns = func() map[string]string {
	out := map[string]string{}
	for field, aliases := range map[string][]string{
		"licensePlate":        {"licenseplate", "plate", "plateno", "platenumber", "ทะเบียน", "ทะเบียนรถ", "เลขทะเบียน"},
		"province":            {"province", "จังหวัด"},
		"type":                {"type", "trucktype", "vehicletype", "ประเภท", "ประเภทรถ"},
		"brand":               {"brand", "ยี่ห้อ"},
		"model":               {"model", "รุ่น"},
		"color":               {"color", "colour", "สี"},
		"year":                {"year", "ปี", "ปีรถ", "ปีผลิต"},
		"vin":                 {"vin", "chassisnumber", "เลขตัวถัง"},
		"engineNumber":        {"enginenumber", "engineno", "เลขเครื่อง", "เลขเครื่องยนต์"},
		"fuelType":            {"fueltype", "fuel", "เชื้อเพลิง"},
		"ownership":           {"ownership", "ownershiptype"},
		"status":              {"status", "truckstatus", "สถานะ"},
		"registrationDate":    {"registrationdate", "วันจดทะเบียน", "วันที่จดทะเบียน"},
		"buyingDate":          {"buyingdate", "วันที่ซื้อ"},
		"maxLoadWeightKg":     {"maxload", "maxloadweight", "maxloadweightkg", "น้ำหนักบรรทุก"},
		"engineCapacity":      {"enginecapacity"},
		"fuelCapacity":        {"fuelcapacity"},
		"seats":               {"seats", "ที่นั่ง"},
		"gpsVehicleId":        {"gpsvehicleid", "gps", "gpsid"},
		"taxExpiryDate":       {"taxexpirydate", "วันหมดอายุภาษี"},
		"insuranceExpiryDate": {"insuranceexpirydate", "วันหมดอายุประกัน"},
		"currentMileage":      {"currentmileage", "mileage", "เลขไมล์"},
		"notes":               {"notes", "note", "หมายเหตุ"},
	} {
		for _, a := range aliases {
			out[a] = field
		}
	}
	return out
}()

// normHeader keeps the letters, digits and combining marks (Thai vowels and tone marks) of a header, lower case.
func normHeader(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.In(r, unicode.Mn, unicode.Mc) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ErrNoPlateColumn: a sheet whose header has no plate column (every row would fail).
var ErrNoPlateColumn = errors.New("fleet: the sheet has no license plate column")

// RowsFromSheet maps a read worksheet onto import rows; unknown columns are ignored, a repeated column keeps its first
// occurrence.
func RowsFromSheet(s *xlsx.Sheet) ([]ImportRow, error) {
	cols := map[int]string{}
	seen := map[string]bool{}
	for i, h := range s.Header {
		if f, ok := importColumns[normHeader(h)]; ok && !seen[f] {
			cols[i], seen[f] = f, true
		}
	}
	if !seen["licensePlate"] {
		return nil, ErrNoPlateColumn
	}
	out := make([]ImportRow, 0, len(s.Rows))
	for n, r := range s.Rows {
		cells := map[string]string{}
		for i, f := range cols {
			if v := xlsx.Value(r, i); v != "" {
				cells[f] = v
			}
		}
		out = append(out, ImportRow{Line: s.Line[n], Cells: cells})
	}
	return out, nil
}

// RowsFromJSON maps the rows[] of a JSON import (objects keyed by the column names, values strings or numbers) onto
// import rows. A value of another type is reported as the row's field invalid; of two names of one column the first
// in key order wins.
func RowsFromJSON(rows []map[string]any) []ImportRow {
	out := make([]ImportRow, 0, len(rows))
	for n, r := range rows {
		row := ImportRow{Line: n + 1, Cells: map[string]string{}}
		seen := map[string]bool{}
		for _, k := range slices.Sorted(maps.Keys(r)) { // two names of one column: the first in key order wins
			f, ok := importColumns[normHeader(k)]
			if !ok || seen[f] {
				continue
			}
			seen[f] = true
			switch x := r[k].(type) {
			case nil:
			case string:
				if s := strings.TrimSpace(x); s != "" {
					row.Cells[f] = s
				}
			case float64:
				row.Cells[f] = strconv.FormatFloat(x, 'f', -1, 64)
			default:
				if !slices.Contains(row.Invalid, f) {
					row.Invalid = append(row.Invalid, f)
				}
			}
		}
		slices.Sort(row.Invalid)
		out = append(out, row)
	}
	return out
}

// canonStatus is the lower_snake form of a status cell ("In-Transit" -> in_transit); 'available' is active (the
// ETL's reading of the legacy literal).
func canonStatus(s string) string {
	s = strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(s)))
	if s == "available" {
		return "active"
	}
	return s
}

// ownershipCell reads the legacy Ownership column (Company -> own, Subcontractor -> subcontractor).
func ownershipCell(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "own", "company", "บริษัท", "รถบริษัท":
		return OwnershipOwn, true
	case "subcontractor", "partner", "ผู้รับเหมา", "รถร่วม":
		return OwnershipSubcontractor, true
	}
	return "", false
}

// input turns a row into a TruckInput (only non-empty cells are set) and its ownership cell; cells that cannot be
// read (a date, a number) are reported.
func (r ImportRow) input(ck *checker) (TruckInput, string) {
	var in TruckInput
	for _, f := range r.Invalid {
		ck.add(f, "invalid")
	}
	text := func(field string, dst *Field[string]) {
		if v, ok := r.Cells[field]; ok {
			*dst = Field[string]{Set: true, V: v}
		}
	}
	text("licensePlate", &in.LicensePlate)
	text("province", &in.Province)
	text("type", &in.Type)
	text("brand", &in.Brand)
	text("model", &in.Model)
	text("color", &in.Color)
	text("vin", &in.VIN)
	text("engineNumber", &in.EngineNumber)
	text("fuelType", &in.FuelType)
	text("gpsVehicleId", &in.GPSVehicleID)
	text("notes", &in.Notes)
	if v, ok := r.Cells["status"]; ok {
		in.Status = Field[string]{Set: true, V: canonStatus(v)}
	}
	integer := func(field string, dst *Field[int32], buddhist bool) {
		v, ok := r.Cells[field]
		if !ok {
			return
		}
		n, good := xlsx.Number(v)
		if !good || n != math.Trunc(n) || math.Abs(n) > math.MaxInt32 {
			ck.add(field, "invalid")
			return
		}
		if buddhist && n >= 2400 {
			n -= 543 // a Thai sheet's year (2566 -> 2023)
		}
		*dst = Field[int32]{Set: true, V: int32(n)}
	}
	integer("year", &in.Year, true)
	integer("seats", &in.Seats, false)
	integer("currentMileage", &in.CurrentMileage, false)
	number := func(field string, dst *Field[float64]) {
		v, ok := r.Cells[field]
		if !ok {
			return
		}
		n, good := xlsx.Number(v)
		if !good {
			ck.add(field, "invalid")
			return
		}
		*dst = Field[float64]{Set: true, V: math.Round(n*100) / 100}
	}
	number("maxLoadWeightKg", &in.MaxLoadWeightKg)
	number("engineCapacity", &in.EngineCapacity)
	number("fuelCapacity", &in.FuelCapacity)
	date := func(field string, dst *Field[string]) {
		v, ok := r.Cells[field]
		if !ok {
			return
		}
		d, good := xlsx.Date(v)
		if !good {
			ck.add(field, "invalid")
			return
		}
		*dst = Field[string]{Set: true, V: d.Format("2006-01-02")}
	}
	date("registrationDate", &in.RegistrationDate)
	date("buyingDate", &in.BuyingDate)
	date("taxExpiryDate", &in.TaxExpiryDate)
	date("insuranceExpiryDate", &in.InsuranceExpiryDate)
	ownership := ""
	if v, ok := r.Cells["ownership"]; ok {
		var good bool
		if ownership, good = ownershipCell(v); !good {
			ck.add("ownership", "invalid")
		}
	}
	return in, ownership
}

// errDryRun rolls a dry-run import back.
var errDryRun = errors.New("fleet: dry run")

// rowViolations turns the error of one row's writes into its reasons; ok false for an error that is not about the
// row (it aborts the import).
func rowViolations(err error) ([]httpx.FieldViolation, bool) {
	var he *httpx.Error
	if !errors.As(mapDBError(err), &he) {
		return nil, false
	}
	switch he.Code {
	case httpx.CodeInvalidArgument:
		if fs, ok := he.Details["fields"].([]httpx.FieldViolation); ok {
			return fs, true
		}
	case CodeAlreadyExists:
		field, _ := he.Details["field"].(string)
		reason, _ := he.Details["reason"].(string)
		return []httpx.FieldViolation{{Field: field, Reason: reason}}, true
	}
	return nil, false
}

// ImportTrucks is POST /v1/trucks/import (fleet:create_truck): every row is a truck of the target tenant, created, or,
// when the tenant already has its plate and the caller holds fleet:edit_truck, updated with the row's non-empty
// cells. Rows are validated like POST /v1/trucks (plate rule, vocabularies, the one vehicle-class mapper) and written
// one savepoint each, so a bad row is reported and the others still land; each written truck appends its
// status_history entry and outbox truck.created / truck.updated. dryRun runs the same checks and writes, then rolls
// everything back.
func (s *Service) ImportTrucks(ctx context.Context, p *authz.Principal, rid string, in ImportInput) (*ImportResult, error) {
	own, err := writeTenant(p)
	if err != nil {
		return nil, err
	}
	var ck checker
	tenant := targetTenant(&ck, p, own, Field[string]{Set: in.TenantID != "", V: in.TenantID})
	switch {
	case len(in.Rows) == 0:
		ck.add("rows", "required")
	case len(in.Rows) > MaxImportRows:
		ck.addParams("rows", "too_many", map[string]any{"max": MaxImportRows})
	}
	if err := ck.err(); err != nil {
		return nil, err
	}
	canEdit := p.Can(authz.FleetEditTruck)
	var res *ImportResult
	err = s.tx(ctx, p, func(tx pgx.Tx, q *fleetdb.Queries) error {
		res = &ImportResult{DryRun: in.DryRun, Errors: []ImportRowError{}}
		kind, err := q.TenantKind(ctx, tenant)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrInvalidArgument(violation("tenantId", "not_found"))
		}
		if err != nil {
			return err
		}
		ownership, err := ownershipOf(kind)
		if err != nil {
			return err
		}
		first := map[string]int{}
		for _, row := range in.Rows {
			var rck checker
			input, rowOwnership := row.input(&rck)
			plate := strings.TrimSpace(input.LicensePlate.V)
			reject := func(fields []httpx.FieldViolation) {
				e := ImportRowError{Row: row.Line, Fields: fields}
				if plate != "" {
					e.LicensePlate = &plate
				}
				res.Errors = append(res.Errors, e)
			}
			if rowOwnership != "" && rowOwnership != ownership {
				rck.addParams("ownership", "mismatch", map[string]any{"tenantOwnership": ownership})
			}
			if line, dup := first[plate]; dup && plate != "" {
				rck.addParams("licensePlate", "duplicate_in_file", map[string]any{"row": line})
			}
			if len(rck.bad) > 0 {
				reject(rck.bad)
				continue
			}
			if plate != "" {
				first[plate] = row.Line
			}
			sp, err := tx.Begin(ctx) // a savepoint: a failing row leaves the import's transaction usable
			if err != nil {
				return err
			}
			outcome, err := s.importRow(ctx, sp, fleetdb.New(sp), p, rid, tenant, ownership, input, canEdit)
			if err != nil {
				_ = sp.Rollback(ctx)
				fields, ok := rowViolations(err)
				if !ok {
					return err
				}
				reject(fields)
				continue
			}
			if err := sp.Commit(ctx); err != nil {
				return err
			}
			switch outcome {
			case RouteTruckCreated:
				res.Created++
			case RouteTruckUpdated:
				res.Updated++
			default:
				res.Unchanged++
			}
		}
		if in.DryRun {
			return errDryRun
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDryRun) {
		return nil, err
	}
	return res, nil
}

// importRow writes one validated row in its savepoint: the routing key of what it did ("" for an unchanged truck).
func (s *Service) importRow(ctx context.Context, tx pgx.Tx, q *fleetdb.Queries, p *authz.Principal, rid string,
	tenant uuid.UUID, ownership string, in TruckInput, canEdit bool) (string, error) {
	var ck checker
	cur, err := q.LockTruckByPlate(ctx, fleetdb.LockTruckByPlateParams{TenantID: tenant, LicensePlate: strings.TrimSpace(in.LicensePlate.V)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if !in.Status.Set {
			in.Status = Field[string]{Set: true, V: "active"} // the legacy import's default
		}
		m := newTruck()
		merge(&ck, &in, &m, true)
		if err := ck.err(); err != nil {
			return "", err
		}
		id, err := insertTruck(ctx, q, p, tenant, ownership, m, ptr("imported"), s.now())
		if err != nil {
			return "", err
		}
		return RouteTruckCreated, emit(ctx, tx, RouteTruckCreated, id, tenant, nil, rid)
	case err != nil:
		return "", err
	case !canEdit:
		return "", errAlreadyExists("licensePlate", "taken")
	}
	m := rowOf(cur)
	changed := merge(&ck, &in, &m, false)
	if err := ck.err(); err != nil {
		return "", err
	}
	if len(changed) == 0 {
		return "", nil
	}
	if err := updateTruck(ctx, q, p, m, cur.Status, ptr("imported"), s.now()); err != nil {
		return "", err
	}
	return RouteTruckUpdated, emit(ctx, tx, RouteTruckUpdated, cur.ID, cur.TenantID, changed, rid)
}
