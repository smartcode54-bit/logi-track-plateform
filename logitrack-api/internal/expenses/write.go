package expenses

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/expenses/expensesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Input is the body of POST /v1/expenses and PATCH /v1/expenses/{id}. date is an RFC 3339 instant, a Bangkok wall
// time yyyy-MM-ddTHH:mm[:ss] or a calendar day yyyy-MM-dd; money is THB with two decimals (volumeLiters three);
// receiptKey / odometerKey come from POST /v1/uploads/presign (purposes expense_receipt, expense_odometer; null
// unlinks). truckId sets the truck and its plate snapshot together (null clears both). Fuel fields (volumeLiters,
// pricePerLiter, stationTaxId, taxInvId, odometerKey) on an other row, and other fields (category, the toll fields)
// on a fuel row, are 422 not_available (a null or blank one counts as absent).
type Input struct {
	Type               Field[string]  `json:"type"`
	DriverID           Field[string]  `json:"driverId"`
	TruckID            Field[string]  `json:"truckId"`
	Status             Field[string]  `json:"status"`
	Date               Field[string]  `json:"date"`
	AmountThb          Field[float64] `json:"amountThb"`
	Category           Field[string]  `json:"category"`
	VolumeLiters       Field[float64] `json:"volumeLiters"`
	PricePerLiter      Field[float64] `json:"pricePerLiter"`
	OdometerKm         Field[int64]   `json:"odometerKm"`
	StationTaxID       Field[string]  `json:"stationTaxId"`
	TaxInvID           Field[string]  `json:"taxInvId"`
	RefillLat          Field[float64] `json:"refillLat"`
	RefillLng          Field[float64] `json:"refillLng"`
	Note               Field[string]  `json:"note"`
	Description        Field[string]  `json:"description"`
	AdminNote          Field[string]  `json:"adminNote"`
	DistanceKm         Field[float64] `json:"distanceKm"`
	TollImportSequence Field[int64]   `json:"tollImportSequence"`
	TollLocation       Field[string]  `json:"tollLocation"`
	TollLane           Field[string]  `json:"tollLane"`
	TollSourceType     Field[string]  `json:"tollSourceType"`
	ReceiptKey         Field[string]  `json:"receiptKey"`
	OdometerKey        Field[string]  `json:"odometerKey"`
}

// Bounds of the expense fields.
const (
	maxAmountThb   = 10_000_000
	maxLiters      = 100_000
	maxPricePerL   = 1_000
	maxOdometerKm  = 10_000_000
	maxDistanceKm  = 1_000_000
	maxSequence    = 1_000_000_000
	maxShortText   = 100
	maxPlaceText   = 200
	maxNoteText    = 2000
	maxDescription = 1000
)

// Default admin notes of staff-entered rows (createFuelExpense / createOtherExpense, expenses.ts:394-481).
const (
	defaultFuelAdminNote  = "บันทึกโดย admin (ข้อมูลจาก petro app)"
	defaultOtherAdminNote = "บันทึกโดย admin"
)

// fields is the merged state of the columns a staff write sets.
type fields struct {
	At                                    time.Time
	Amount                                float64
	Category                              *string
	Volume, Price                         *float64
	Odometer                              *int32
	Station, TaxInv                       *string
	Lat, Lng                              *float64
	Note, Description, AdminNote          *string
	Distance                              *float64
	Sequence                              *int32
	TollLocation, TollLane, TollSourceTyp *string
}

func fieldsOf(e expensesdb.VehicleExpense) fields {
	return fields{At: e.ExpenseAt, Amount: e.AmountThb, Category: e.Category, Volume: e.VolumeLiters,
		Price: e.PricePerLiter, Odometer: e.OdometerKm, Station: e.StationTaxID, TaxInv: e.TaxInvID, Lat: e.RefillLat,
		Lng: e.RefillLng, Note: e.Note, Description: e.Description, AdminNote: e.AdminNote, Distance: e.DistanceKm,
		Sequence: e.TollImportSequence, TollLocation: e.TollLocation, TollLane: e.TollLane, TollSourceTyp: e.TollSourceType}
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// merge applies the request to f for a row of type typ and returns the changed JSON fields; violations go to ck.
// create requires date and amountThb.
func merge(ck *checker, in *Input, f *fields, typ string, create bool) []string {
	var changed []string
	note := func(field string, did bool) {
		if did {
			changed = append(changed, field)
		}
	}
	// only reports a value the row's type does not take (not_available); a null or blank one is absent; want ""
	// is a field of every type.
	only := func(field string, set bool, value bool, want string) bool {
		if set && value && want != "" && typ != want {
			ck.add(field, "not_available")
			return false
		}
		return true
	}
	text := func(field string, v Field[string], max int, dst **string, want string) {
		if !v.Set {
			return
		}
		if !only(field, true, !v.Null && strings.TrimSpace(v.V) != "", want) {
			return
		}
		var out *string
		if !v.Null {
			s, bad := cleanText(v.V, max)
			if bad != "" {
				ck.add(field, bad)
				return
			}
			if s != "" {
				out = &s
			}
		}
		note(field, !eqPtr(*dst, out))
		*dst = out
	}
	number := func(field string, v Field[float64], max float64, places int, dst **float64, want string) {
		if !v.Set || !only(field, true, !v.Null, want) {
			return
		}
		var out *float64
		if !v.Null {
			x := v.V
			switch {
			case math.IsNaN(x) || x < 0 || x > max:
				ck.addParams(field, "out_of_range", map[string]any{"min": 0, "max": max})
				return
			case !decimals(x, places):
				ck.add(field, "too_precise")
				return
			}
			out = &x
		}
		note(field, !eqPtr(*dst, out))
		*dst = out
	}
	integer := func(field string, v Field[int64], max int64, dst **int32, want string) {
		if !v.Set || !only(field, true, !v.Null, want) {
			return
		}
		var out *int32
		if !v.Null {
			if v.V < 0 || v.V > max {
				ck.addParams(field, "out_of_range", map[string]any{"min": 0, "max": max})
				return
			}
			x := int32(v.V)
			out = &x
		}
		note(field, !eqPtr(*dst, out))
		*dst = out
	}
	coord := func(field string, v Field[float64], lim float64, dst **float64) {
		if !v.Set {
			return
		}
		var out *float64
		if !v.Null {
			if math.IsNaN(v.V) || v.V < -lim || v.V > lim {
				ck.addParams(field, "out_of_range", map[string]any{"min": -lim, "max": lim})
				return
			}
			x := v.V
			out = &x
		}
		note(field, !eqPtr(*dst, out))
		*dst = out
	}
	if d := in.Date; d.Set || create {
		t, ok := parseInstant(d.V)
		switch {
		case d.Null || strings.TrimSpace(d.V) == "":
			ck.add("date", "required")
		case !ok:
			ck.add("date", "invalid")
		default:
			note("date", !t.Equal(f.At))
			f.At = t
		}
	}
	if a := in.AmountThb; a.Set || create {
		switch {
		case !a.Set || a.Null:
			ck.add("amountThb", "required")
		case math.IsNaN(a.V) || a.V < 0 || a.V > maxAmountThb:
			ck.addParams("amountThb", "out_of_range", map[string]any{"min": 0, "max": maxAmountThb})
		case !decimals(a.V, 2):
			ck.add("amountThb", "too_precise")
		default:
			note("amountThb", a.V != f.Amount)
			f.Amount = a.V
		}
	}
	if c := in.Category; c.Set && only("category", true, !c.Null && strings.TrimSpace(c.V) != "", TypeOther) {
		var out *string
		if !c.Null && strings.TrimSpace(c.V) != "" {
			v := strings.ToLower(strings.TrimSpace(c.V))
			if !slices.Contains(Categories, v) {
				ck.add("category", "invalid")
			}
			out = &v
		}
		note("category", !eqPtr(f.Category, out))
		f.Category = out
	}
	number("volumeLiters", in.VolumeLiters, maxLiters, 3, &f.Volume, TypeFuel)
	number("pricePerLiter", in.PricePerLiter, maxPricePerL, 2, &f.Price, TypeFuel)
	integer("odometerKm", in.OdometerKm, maxOdometerKm, &f.Odometer, "")
	text("stationTaxId", in.StationTaxID, maxShortText, &f.Station, TypeFuel)
	text("taxInvId", in.TaxInvID, maxShortText, &f.TaxInv, TypeFuel)
	coord("refillLat", in.RefillLat, 90, &f.Lat)
	coord("refillLng", in.RefillLng, 180, &f.Lng)
	if (f.Lat == nil) != (f.Lng == nil) {
		ck.add("refillLng", "incomplete")
	}
	text("note", in.Note, maxNoteText, &f.Note, "")
	text("description", in.Description, maxDescription, &f.Description, "")
	text("adminNote", in.AdminNote, maxNoteText, &f.AdminNote, "")
	number("distanceKm", in.DistanceKm, maxDistanceKm, 2, &f.Distance, "")
	integer("tollImportSequence", in.TollImportSequence, maxSequence, &f.Sequence, TypeOther)
	text("tollLocation", in.TollLocation, maxPlaceText, &f.TollLocation, TypeOther)
	text("tollLane", in.TollLane, maxPlaceText, &f.TollLane, TypeOther)
	text("tollSourceType", in.TollSourceType, maxPlaceText, &f.TollSourceTyp, TypeOther)
	return changed
}

// --- shared write steps ------------------------------------------------------------------------------------

// truckRef resolves a truck of the caller's reach: 422 truckId not_found otherwise.
func truckRef(ctx context.Context, q *expensesdb.Queries, p *authz.Principal, id uuid.UUID) (expensesdb.GetTruckRow, error) {
	all, tenants := p.TenantReach()
	t, err := q.GetTruck(ctx, expensesdb.GetTruckParams{ID: id, AllTenants: all, TenantIds: tenants})
	if errors.Is(err, pgx.ErrNoRows) {
		return t, httpx.ErrInvalidArgument(violation("truckId", "not_found"))
	}
	return t, err
}

// checkTaxInvoice is the D5 rule: (driver, tax invoice) of fuel rows is unique, so a second one is 409
// duplicate_tax_invoice. The advisory lock serialises two writers of one invoice, so the check holds where the unique
// index of 0010 is not applied yet (production until the P1 runbook); where it is, the index decides as well.
func checkTaxInvoice(ctx context.Context, q *expensesdb.Queries, typ string, driver *uuid.UUID, taxInv *string, except *uuid.UUID) error {
	if typ != TypeFuel || driver == nil || taxInv == nil {
		return nil
	}
	if err := q.LockFuelTaxInvoice(ctx, expensesdb.LockFuelTaxInvoiceParams{DriverID: *driver, TaxInvID: *taxInv}); err != nil {
		return err
	}
	taken, err := q.FuelTaxInvoiceTaken(ctx, expensesdb.FuelTaxInvoiceTakenParams{DriverID: driver, TaxInvID: taxInv, ExceptID: except})
	if err != nil {
		return err
	}
	if taken {
		return errDuplicateTaxInvoice()
	}
	return nil
}

// commitFile resolves a photo key of a request: the file the row already links when the key is its own (sent back
// unchanged), else the key committed to the row (the uploader or its tenant, storage.Commit).
func (s *Service) commitFile(ctx context.Context, tx pgx.Tx, p *authz.Principal, field, purpose, key string, row uuid.UUID,
	current *uuid.UUID, currentKey *string) (*uuid.UUID, error) {
	key = strings.TrimSpace(key)
	if current != nil && currentKey != nil && *currentKey == key {
		return current, nil
	}
	if s.files == nil {
		return nil, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: field, Reason: "not_found", Params: map[string]any{"key": key}})
	}
	f, err := s.files.Commit(ctx, tx, storage.CommitInput{Key: key, Field: field, Purposes: []string{purpose}, OwnerID: row,
		UserID: p.UserID, TenantID: p.EffectiveTenant()})
	if err != nil {
		return nil, err
	}
	return &f.ID, nil
}

// pmDue reports whether approving e runs the PM check (audit/page.tsx:171-180: a fuel row with a truck and an
// odometer reading).
func pmDue(e expensesdb.VehicleExpense) bool {
	return e.ExpenseType == TypeFuel && e.TruckID != nil && e.OdometerKm != nil && *e.OdometerKm > 0
}

// --- create ------------------------------------------------------------------------------------------------

// Create is POST /v1/expenses (accounting:edit_fuel for a fuel row | accounting:edit_other for an other row): a
// staff-entered expense (createFuelExpense / createOtherExpense, expenses.ts:394-481) of a driver of the caller's reach
// (driverId; or of a truck alone), stamped with the driver's tenant (else the truck's), created_by the caller, status
// approved unless the body says otherwise, the legacy admin note when none is given. Without truckId the truck is the
// driver's active truck, else the home truck of an active assignment; its plate is snapshotted. An approved fuel row
// with an odometer reading runs the PM check in the same transaction, as an approval does. Outbox expense.created.
func (s *Service) Create(ctx context.Context, p *authz.Principal, rid string, in Input) (*StatusResult, error) {
	if err := writeTenant(p); err != nil {
		return nil, err
	}
	var ck checker
	typ := strings.ToLower(strings.TrimSpace(in.Type.V))
	switch {
	case !in.Type.Set || in.Type.Null || typ == "":
		ck.add("type", "required")
	case !slices.Contains(Types, typ):
		ck.add("type", "invalid")
	}
	if err := ck.err(); err != nil {
		return nil, err
	}
	if err := requireEdit(p, typ); err != nil {
		return nil, err
	}
	status := StatusApproved
	if in.Status.Set && !in.Status.Null {
		v, ok := NormalizeStatus(in.Status.V)
		if !ok {
			ck.add("status", "invalid")
		}
		status = v
	}
	parseID := func(field string, v Field[string]) *uuid.UUID {
		if !v.Set || v.Null || strings.TrimSpace(v.V) == "" {
			return nil
		}
		id, err := uuid.Parse(strings.TrimSpace(v.V))
		if err != nil {
			ck.add(field, "invalid")
			return nil
		}
		return &id
	}
	driverID, truckID := parseID("driverId", in.DriverID), parseID("truckId", in.TruckID)
	if driverID == nil && truckID == nil && !in.DriverID.Set {
		ck.add("driverId", "required")
	}
	f := fields{}
	merge(&ck, &in, &f, typ, true)
	if f.AdminNote == nil && !in.AdminNote.Set {
		n := defaultOtherAdminNote
		if typ == TypeFuel {
			n = defaultFuelAdminNote
		}
		f.AdminNote = &n
	}
	if in.OdometerKey.Set && !in.OdometerKey.Null && strings.TrimSpace(in.OdometerKey.V) != "" && typ != TypeFuel {
		ck.add("odometerKey", "not_available")
	}
	if err := ck.err(); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	var out StatusResult
	err = s.tx(ctx, p, func(tx pgx.Tx, q *expensesdb.Queries) error {
		all, tenants := p.TenantReach()
		var tenant uuid.UUID
		source := "driver"
		var driverRef *string
		if driverID != nil {
			d, err := q.GetDriver(ctx, expensesdb.GetDriverParams{ID: *driverID, AllTenants: all, TenantIds: tenants})
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.ErrInvalidArgument(violation("driverId", "not_found"))
			}
			if err != nil {
				return err
			}
			tenant, driverRef = d.TenantID, &d.LegacyRef
			if truckID == nil && !in.TruckID.Set { // the truck the driver holds now, else its home truck
				truckID = cmp.Or(d.ActiveTruckID, d.AssignmentTruckID)
			}
		}
		var plate, truckLegacy *string
		if truckID != nil {
			t, err := q.GetTruck(ctx, expensesdb.GetTruckParams{ID: *truckID, AllTenants: all, TenantIds: tenants})
			switch {
			case errors.Is(err, pgx.ErrNoRows) && in.TruckID.Set:
				return httpx.ErrInvalidArgument(violation("truckId", "not_found"))
			case errors.Is(err, pgx.ErrNoRows): // the driver's truck is outside the caller's reach: no truck
				truckID = nil
			case err != nil:
				return err
			default:
				plate, truckLegacy = &t.LicensePlate, &t.LegacyRef
				if driverID == nil {
					tenant, source = t.TenantID, "truck"
				}
			}
		}
		if driverID == nil && truckID == nil {
			return httpx.ErrInvalidArgument(violation("driverId", "required"))
		}
		if err := checkTaxInvoice(ctx, q, typ, driverID, f.TaxInv, nil); err != nil {
			return err
		}
		var receipt, odometer *uuid.UUID
		if k := in.ReceiptKey; k.Set && !k.Null && strings.TrimSpace(k.V) != "" {
			if receipt, err = s.commitFile(ctx, tx, p, "receiptKey", "expense_receipt", k.V, id, nil, nil); err != nil {
				return err
			}
		}
		if k := in.OdometerKey; k.Set && !k.Null && strings.TrimSpace(k.V) != "" {
			if odometer, err = s.commitFile(ctx, tx, p, "odometerKey", "expense_odometer", k.V, id, nil, nil); err != nil {
				return err
			}
		}
		var legacy *string
		if s.wb != nil {
			v := id.String() // the minted Firestore document id (R25)
			legacy = &v
		}
		row, err := q.InsertExpense(ctx, expensesdb.InsertExpenseParams{ID: id, LegacyDocID: legacy, TenantID: tenant,
			TenantSource: source, DriverID: driverID, TruckID: truckID, PlateSnapshot: plate, ExpenseType: typ,
			Category: f.Category, ExpenseAt: f.At, AmountThb: f.Amount, Status: status, VolumeLiters: f.Volume,
			PricePerLiter: f.Price, OdometerKm: f.Odometer, StationTaxID: f.Station, TaxInvID: f.TaxInv, RefillLat: f.Lat,
			RefillLng: f.Lng, ReceiptFileID: receipt, OdometerFileID: odometer, Note: f.Note, Description: f.Description,
			AdminNote: f.AdminNote, DistanceKm: f.Distance, TollImportSequence: f.Sequence, TollLocation: f.TollLocation,
			TollLane: f.TollLane, TollSourceType: f.TollSourceTyp, Actor: actor(p)})
		if err != nil {
			return err
		}
		if status == StatusApproved && pmDue(row) {
			res, err := s.pm.CheckPMInTx(ctx, tx, p, rid, *row.TruckID, *row.OdometerKm)
			if err != nil {
				return err
			}
			out.PMCheck = &res
		}
		if err := emit(ctx, tx, RouteCreated, row, nil, rid); err != nil {
			return err
		}
		if err := s.writeBack(ctx, q, row, legacyRefs{driver: driverRef, truck: truckLegacy}, nil, true); err != nil {
			return err
		}
		got, err := load(ctx, q, []uuid.UUID{id})
		if err != nil {
			return err
		}
		out.Expense = got[id]
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// --- patch -------------------------------------------------------------------------------------------------

// Patch is PATCH /v1/expenses/{id} (accounting:edit_fuel | accounting:edit_other, the row's type decides): partial, on
// a row of the caller's reach (expenses.ts:322-370). truckId sets truck_id and the plate snapshot together; a fuel
// row's new tax invoice must stay unique per driver (409 duplicate_tax_invoice). type, driverId and status are not
// editable (status: POST /v1/expenses/{id}/status). A request that changes nothing writes nothing; otherwise outbox
// expense.updated lists the changed fields.
func (s *Service) Patch(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in Input) (*Expense, error) {
	if err := writeTenant(p); err != nil {
		return nil, err
	}
	var out Expense
	err := s.tx(ctx, p, func(tx pgx.Tx, q *expensesdb.Queries) error {
		all, tenants := p.TenantReach()
		rows, err := q.LockExpenses(ctx, expensesdb.LockExpensesParams{Ids: []uuid.UUID{id}, AllTenants: all, TenantIds: tenants})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return httpx.ErrNotFound()
		}
		cur := rows[0]
		if err := requireEdit(p, cur.ExpenseType); err != nil {
			return err
		}
		var ck checker
		if t := in.Type; t.Set && (t.Null || strings.ToLower(strings.TrimSpace(t.V)) != cur.ExpenseType) {
			ck.add("type", "not_editable")
		}
		if d := in.DriverID; d.Set && (d.Null || cur.DriverID == nil || strings.TrimSpace(d.V) != cur.DriverID.String()) {
			ck.add("driverId", "not_editable")
		}
		if in.Status.Set {
			ck.add("status", "not_editable")
		}
		f := fieldsOf(cur)
		changed := merge(&ck, &in, &f, cur.ExpenseType, false)
		if k := in.OdometerKey; k.Set && !k.Null && strings.TrimSpace(k.V) != "" && cur.ExpenseType != TypeFuel {
			ck.add("odometerKey", "not_available")
		}
		truckID := cur.TruckID
		plate := cur.TruckLicensePlateSnapshot
		var truckLegacy *string
		if t := in.TruckID; t.Set {
			if t.Null || strings.TrimSpace(t.V) == "" {
				switch {
				case cur.TruckID != nil && cur.DriverID == nil: // a row needs a driver or a truck
					ck.add("truckId", "required")
				case cur.TruckID != nil:
					truckID, plate = nil, nil
					changed = append(changed, "truckId")
				}
			} else if tid, err := uuid.Parse(strings.TrimSpace(t.V)); err != nil {
				ck.add("truckId", "invalid")
			} else if cur.TruckID == nil || *cur.TruckID != tid {
				tr, err := truckRef(ctx, q, p, tid)
				if err != nil {
					return err
				}
				truckID, plate, truckLegacy = &tid, &tr.LicensePlate, &tr.LegacyRef
				changed = append(changed, "truckId")
			}
		}
		if err := ck.err(); err != nil {
			return err
		}
		if slices.Contains(changed, "taxInvId") {
			if err := checkTaxInvoice(ctx, q, cur.ExpenseType, cur.DriverID, f.TaxInv, &cur.ID); err != nil {
				return err
			}
		}
		receipt, odometer := cur.ReceiptFileID, cur.OdometerFileID
		if k := in.ReceiptKey; k.Set {
			next, err := s.photo(ctx, tx, q, p, "receiptKey", "expense_receipt", k, cur.ID, cur.ReceiptFileID)
			if err != nil {
				return err
			}
			if !eqPtr(next, receipt) {
				receipt = next
				changed = append(changed, "receiptKey")
			}
		}
		if k := in.OdometerKey; k.Set {
			next, err := s.photo(ctx, tx, q, p, "odometerKey", "expense_odometer", k, cur.ID, cur.OdometerFileID)
			if err != nil {
				return err
			}
			if !eqPtr(next, odometer) {
				odometer = next
				changed = append(changed, "odometerKey")
			}
		}
		if len(changed) > 0 {
			if err := q.UpdateExpense(ctx, expensesdb.UpdateExpenseParams{TruckID: truckID, PlateSnapshot: plate,
				Category: f.Category, ExpenseAt: f.At, AmountThb: f.Amount, VolumeLiters: f.Volume, PricePerLiter: f.Price,
				OdometerKm: f.Odometer, StationTaxID: f.Station, TaxInvID: f.TaxInv, RefillLat: f.Lat, RefillLng: f.Lng,
				ReceiptFileID: receipt, OdometerFileID: odometer, Note: f.Note, Description: f.Description,
				AdminNote: f.AdminNote, DistanceKm: f.Distance, TollImportSequence: f.Sequence, TollLocation: f.TollLocation,
				TollLane: f.TollLane, TollSourceType: f.TollSourceTyp, ID: cur.ID}); err != nil {
				return err
			}
			upd := cur
			upd.TruckID, upd.TruckLicensePlateSnapshot = truckID, plate
			if err := emit(ctx, tx, RouteUpdated, upd, map[string]any{"changed": changed}, rid); err != nil {
				return err
			}
			rows, err := q.LockExpenses(ctx, expensesdb.LockExpensesParams{Ids: []uuid.UUID{id}, AllTenants: all, TenantIds: tenants})
			if err != nil {
				return err
			}
			if err := s.writeBack(ctx, q, rows[0], legacyRefs{truck: truckLegacy}, changed, false); err != nil {
				return err
			}
		}
		got, err := load(ctx, q, []uuid.UUID{id})
		if err != nil {
			return err
		}
		out = got[id]
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// photo resolves the new file of a photo key in a PATCH: null or blank unlinks it (the object stays), the key the row
// already links keeps it, another key is committed to the row.
func (s *Service) photo(ctx context.Context, tx pgx.Tx, q *expensesdb.Queries, p *authz.Principal, field, purpose string,
	k Field[string], row uuid.UUID, current *uuid.UUID) (*uuid.UUID, error) {
	if k.Null || strings.TrimSpace(k.V) == "" {
		return nil, nil
	}
	var currentKey *string
	if current != nil {
		got, err := load(ctx, q, []uuid.UUID{row})
		if err != nil {
			return nil, err
		}
		e := got[row]
		currentKey = e.ReceiptKey
		if purpose == "expense_odometer" {
			currentKey = e.OdometerKey
		}
	}
	return s.commitFile(ctx, tx, p, field, purpose, k.V, row, current, currentKey)
}

// --- status ------------------------------------------------------------------------------------------------

// StatusInput is the body of POST /v1/expenses/{id}/status.
type StatusInput struct {
	Status    string  `json:"status"`
	AdminNote *string `json:"adminNote"`
}

// BulkStatusInput is the body of POST /v1/expenses/bulk-status.
type BulkStatusInput struct {
	IDs       []string `json:"ids"`
	Status    string   `json:"status"`
	AdminNote *string  `json:"adminNote"`
}

// MaxBulk bounds the rows of one bulk-status request.
const MaxBulk = 500

// StatusResult is an expense with the outcome of the PM check its approval ran (null when none ran).
type StatusResult struct {
	Expense
	PMCheck *maintenance.PMResult `json:"pmCheck"`
}

// SetStatus is POST /v1/expenses/{id}/status (accounting:audit_expense): the audit decision on one row of the caller's
// reach (updateVehicleExpenseStatus, expenses.ts:279-319). Approving a fuel row with a truck and an odometer reading
// runs the PM check in the same transaction (checkMaintenanceAlert after the approval, audit/page.tsx:171-180, which
// a failed call silently skipped): over the threshold it creates exactly one PM booking. A blank adminNote keeps the
// stored one. Outbox expense.approved (a row that became approved) or expense.status_changed; a request that changes
// nothing writes nothing.
func (s *Service) SetStatus(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in StatusInput) (*StatusResult, error) {
	out, err := s.setStatus(ctx, p, rid, []uuid.UUID{id}, in.Status, in.AdminNote, "")
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

// BulkStatus is POST /v1/expenses/bulk-status (accounting:audit_expense): SetStatus on every row of ids (at most
// MaxBulk, duplicates ignored) in one transaction (batchUpdateVehicleExpenseStatuses + the per-row PM checks of
// audit/page.tsx:218-245); every id must name a row of the caller's reach (422 ids not_found otherwise, nothing
// written). It answers the rows in id order.
func (s *Service) BulkStatus(ctx context.Context, p *authz.Principal, rid string, in BulkStatusInput) ([]StatusResult, error) {
	var ck checker
	if len(in.IDs) == 0 {
		ck.add("ids", "required")
	}
	if len(in.IDs) > MaxBulk {
		ck.addParams("ids", "too_many", map[string]any{"max": MaxBulk})
	}
	var ids []uuid.UUID
	for _, v := range in.IDs {
		id, err := uuid.Parse(strings.TrimSpace(v))
		if err != nil {
			ck.addParams("ids", "invalid", map[string]any{"id": v})
			continue
		}
		ids = append(ids, id)
	}
	if err := ck.err(); err != nil {
		return nil, err
	}
	return s.setStatus(ctx, p, rid, ids, in.Status, in.AdminNote, "ids")
}

func (s *Service) setStatus(ctx context.Context, p *authz.Principal, rid string, ids []uuid.UUID, status string,
	adminNote *string, idsField string) ([]StatusResult, error) {
	if err := writeTenant(p); err != nil {
		return nil, err
	}
	var ck checker
	to, ok := NormalizeStatus(status)
	if !ok {
		ck.add("status", "invalid")
	}
	var note *string
	if adminNote != nil {
		v, bad := cleanText(*adminNote, maxNoteText)
		if bad != "" {
			ck.add("adminNote", bad)
		}
		if v != "" {
			note = &v
		}
	}
	if err := ck.err(); err != nil {
		return nil, err
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	ids = slices.Compact(ids)
	out := make([]StatusResult, len(ids))
	err := s.tx(ctx, p, func(tx pgx.Tx, q *expensesdb.Queries) error {
		all, tenants := p.TenantReach()
		rows, err := q.LockExpenses(ctx, expensesdb.LockExpensesParams{Ids: ids, AllTenants: all, TenantIds: tenants})
		if err != nil {
			return err
		}
		if len(rows) != len(ids) {
			if idsField == "" {
				return httpx.ErrNotFound()
			}
			found := map[uuid.UUID]bool{}
			for _, r := range rows {
				found[r.ID] = true
			}
			var missing []string
			for _, id := range ids {
				if !found[id] {
					missing = append(missing, id.String())
				}
			}
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: idsField, Reason: "not_found",
				Params: map[string]any{"ids": missing}})
		}
		var approved []expensesdb.VehicleExpense
		pm := map[uuid.UUID]*maintenance.PMResult{}
		for _, r := range rows {
			if r.Status == to && (note == nil || eqPtr(r.AdminNote, note)) {
				continue
			}
			if err := q.SetExpenseStatus(ctx, expensesdb.SetExpenseStatusParams{Status: to, AdminNote: note, ID: r.ID}); err != nil {
				return err
			}
			key, extra := RouteStatusChanged, map[string]any{"previousStatus": r.Status}
			upd := r
			upd.Status = to
			if note != nil {
				upd.AdminNote = note
			}
			if to == StatusApproved && r.Status != StatusApproved {
				key = RouteApproved
				if pmDue(r) {
					approved = append(approved, r)
				}
			}
			if err := emit(ctx, tx, key, upd, extra, rid); err != nil {
				return err
			}
			if err := s.writeBack(ctx, q, upd, legacyRefs{}, []string{"status", "adminNote"}, false); err != nil {
				return err
			}
		}
		// The PM checks lock trucks after every expense row, in truck order (two bulk approvals never wait for each
		// other in reverse); rows of one truck run in id order, so the first one over the threshold books.
		slices.SortStableFunc(approved, func(a, b expensesdb.VehicleExpense) int {
			return strings.Compare(a.TruckID.String(), b.TruckID.String())
		})
		for _, r := range approved {
			res, err := s.pm.CheckPMInTx(ctx, tx, p, rid, *r.TruckID, *r.OdometerKm)
			if err != nil {
				return err
			}
			pm[r.ID] = &res
		}
		got, err := load(ctx, q, ids)
		if err != nil {
			return err
		}
		for i, id := range ids {
			out[i] = StatusResult{Expense: got[id], PMCheck: pm[id]}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
