package expenses

import (
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/expenses/expensesdb"
)

// Collection is the Firestore collection of vehicle expenses (class C until P7b).
const Collection = "vehicle_expenses"

// LegacyStatus is the literal the installed APKs read: the canonical status in upper case (main spec §13.8).
func LegacyStatus(status string) string { return strings.ToUpper(status) }

// legacyNames maps the JSON fields of a staff write onto the Firestore fields they set (the shapes of
// vehicle_expense.dart toFirestore and expenses.ts updateVehicleExpense). The photo keys have none: Go uploads carry no
// legacy URL (main spec §13.8).
var legacyNames = map[string][]string{
	"date": {"date"}, "amountThb": {"amount"}, "category": {"category"}, "volumeLiters": {"volumeLiters"},
	"pricePerLiter": {"pricePerLiter"}, "odometerKm": {"odometer"}, "stationTaxId": {"stationTaxId"},
	"taxInvId": {"taxInvId"}, "refillLat": {"refillLocation"}, "refillLng": {"refillLocation"}, "note": {"note"},
	"description": {"description"}, "adminNote": {"adminNote"}, "distanceKm": {"distanceKm"},
	"tollImportSequence": {"tollImportSequence"}, "tollLocation": {"tollLocation"}, "tollLane": {"tollLane"},
	"tollSourceType": {"tollSourceType"}, "truckId": {"truckId", "truckLicensePlate"}, "status": {"status"},
}

// LegacyRefs are the Firestore ids a legacy write names: the row's driver (the Firebase uid, main spec §13.8 Q1), its
// truck (the trucks document id) and, for a created row, its creator (createdByUid; empty = left out).
type LegacyRefs struct{ Driver, Truck, CreatedBy string }

// LegacyFields is the Firestore shape of a row's staff-written fields: driverId and truckId are the Firestore ids,
// status the upper-case literal, refillLocation "lat,lng", nil for an empty field (a write-back removes it). The
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

// LegacyWrite is the Firestore write of one staff write of e: the fields the class C write-back of T31 merges into the
// row's document (main spec §11.10, §13.8: Firestore first, the FS->PG mirror fills PostgreSQL). A created row (a
// staff entry, a toll import row) sets every staff field of LegacyFields with createdAt and createdByUid. A change
// (PATCH: merge's changed list plus truckId; an audit decision: status and adminNote) sets only the Firestore fields of
// the JSON fields it changed, so the driver's own fields of a pending row and every field the change did not touch
// keep their Firestore values. Both stamp updatedAt. A change of no Firestore field (a photo key alone) is nil:
// nothing is written.
func LegacyWrite(e expensesdb.VehicleExpense, refs LegacyRefs, changed []string, created bool, now time.Time) map[string]any {
	all := LegacyFields(e, refs.Driver, refs.Truck)
	fields := map[string]any{}
	if created {
		maps.Copy(fields, all)
		fields["createdAt"] = now
		if refs.CreatedBy != "" {
			fields["createdByUid"] = refs.CreatedBy
		}
	} else {
		for _, c := range changed {
			for _, name := range legacyNames[c] {
				fields[name] = all[name]
			}
		}
		if len(fields) == 0 {
			return nil
		}
	}
	fields["updatedAt"] = now
	return fields
}
