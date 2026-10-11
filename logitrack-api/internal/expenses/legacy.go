package expenses

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/expenses/expensesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// Collection is the Firestore collection of vehicle expenses (class C until P7b).
const Collection = "vehicle_expenses"

// LegacyStatus is the literal the installed APKs read: the canonical status in upper case (main spec §13.8).
func LegacyStatus(status string) string { return strings.ToUpper(status) }

// legacyNames maps the JSON fields of a staff write onto the Firestore fields they set (the shapes of
// vehicle_expense.dart toFirestore and expenses.ts updateVehicleExpense).
var legacyNames = map[string][]string{
	"date": {"date"}, "amountThb": {"amount"}, "category": {"category"}, "volumeLiters": {"volumeLiters"},
	"pricePerLiter": {"pricePerLiter"}, "odometerKm": {"odometer"}, "stationTaxId": {"stationTaxId"},
	"taxInvId": {"taxInvId"}, "refillLat": {"refillLocation"}, "refillLng": {"refillLocation"}, "note": {"note"},
	"description": {"description"}, "adminNote": {"adminNote"}, "distanceKm": {"distanceKm"},
	"tollImportSequence": {"tollImportSequence"}, "tollLocation": {"tollLocation"}, "tollLane": {"tollLane"},
	"tollSourceType": {"tollSourceType"}, "truckId": {"truckId", "truckLicensePlate"}, "status": {"status"},
}

// legacyRefs are the Firestore ids of the row's driver (the Firebase uid, main spec §13.8 Q1) and truck (the trucks
// document id), when the caller already holds them.
type legacyRefs struct{ driver, truck *string }

// LegacyFields is the Firestore shape of a row's staff-written fields: driverId and truckId are the Firestore ids,
// status the upper-case literal, refillLocation "lat,lng", nil for an empty field (the write-back removes it). The
// photos (receiptPhotoUrl, odometerPhotoUrl) are the APK's: Go uploads carry no legacy URL (main spec §13.8).
func LegacyFields(e expensesdb.VehicleExpense, driverRef, truckRef string) map[string]any {
	str := func(p *string) any {
		if p == nil || *p == "" {
			return nil
		}
		return *p
	}
	num := func(p *float64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	integer := func(p *int32) any {
		if p == nil {
			return nil
		}
		return int64(*p)
	}
	ref := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	var refill any
	if e.RefillLat != nil && e.RefillLng != nil {
		refill = strconv.FormatFloat(*e.RefillLat, 'f', -1, 64) + "," + strconv.FormatFloat(*e.RefillLng, 'f', -1, 64)
	}
	return map[string]any{
		"driverId": ref(driverRef), "truckId": ref(truckRef), "truckLicensePlate": str(e.TruckLicensePlateSnapshot),
		"type": e.ExpenseType, "category": str(e.Category), "date": e.ExpenseAt, "amount": e.AmountThb,
		"status": LegacyStatus(e.Status), "volumeLiters": num(e.VolumeLiters), "pricePerLiter": num(e.PricePerLiter),
		"odometer": integer(e.OdometerKm), "stationTaxId": str(e.StationTaxID), "taxInvId": str(e.TaxInvID),
		"refillLocation": refill, "note": str(e.Note), "description": str(e.Description), "adminNote": str(e.AdminNote),
		"distanceKm": num(e.DistanceKm), "tollImportSequence": integer(e.TollImportSequence),
		"tollLocation": str(e.TollLocation), "tollLane": str(e.TollLane), "tollSourceType": str(e.TollSourceType),
	}
}

// writeBack writes a staff change of a row to its Firestore document before COMMIT when the write-back is on (class
// C, main spec §13.8): a new row (created) writes every staff field with createdAt / createdByUid, a change only the
// Firestore fields of the changed JSON fields (the driver's own fields of a pending row stay the APK's), always with
// updatedAt. A row born in PostgreSQL gets the minted document id (its uuid, R25) as legacy_doc_id. A failed write
// rolls the request back (503 unavailable).
func (s *Service) writeBack(ctx context.Context, q *expensesdb.Queries, e expensesdb.VehicleExpense, refs legacyRefs,
	changed []string, created bool) error {
	if s.wb == nil {
		return nil
	}
	doc := e.ID.String()
	if e.LegacyDocID != nil && *e.LegacyDocID != "" {
		doc = *e.LegacyDocID
	} else if err := q.SetLegacyDocID(ctx, expensesdb.SetLegacyDocIDParams{LegacyDocID: &doc, ID: e.ID}); err != nil {
		return err
	}
	driverRef := ""
	switch {
	case refs.driver != nil:
		driverRef = *refs.driver
	case e.LegacyDriverRef != nil && *e.LegacyDriverRef != "":
		driverRef = *e.LegacyDriverRef
	case e.DriverID != nil && (created || slices.Contains(changed, "driverId")):
		v, err := q.DriverLegacyRef(ctx, *e.DriverID)
		if err != nil {
			return err
		}
		driverRef = v
	}
	truckRef := ""
	switch {
	case refs.truck != nil:
		truckRef = *refs.truck
	case e.TruckID != nil:
		truckRef = e.TruckID.String()
	}
	all := LegacyFields(e, driverRef, truckRef)
	now := s.now()
	fields := map[string]any{"updatedAt": now}
	if created {
		for k, v := range all {
			fields[k] = v
		}
		fields["createdAt"] = now
		if e.CreatedByUserID != nil {
			if uid, err := q.ActorLegacyUID(ctx, *e.CreatedByUserID); err == nil {
				fields["createdByUid"] = uid
			}
		}
	} else {
		for _, c := range changed {
			for _, name := range legacyNames[c] {
				fields[name] = all[name]
			}
		}
	}
	if err := s.wb.Merge(ctx, Collection, doc, fields); err != nil {
		return writeback.ErrUnavailable(err)
	}
	return nil
}
