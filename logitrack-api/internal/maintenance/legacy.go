package maintenance

import (
	"maps"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance/maintenancedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// Collection is the Firestore collection of maintenance records (class C until P7b).
const Collection = "maintenance"

// LegacyStatus maps a canonical status onto the literal the installed APKs read (main spec §13.8 status table):
// pm_booking 'PM Booking', scheduled 'Scheduled', completed and cancelled as they are. in_progress has two legacy
// literals that PostgreSQL folds into one status and tells apart by check_in_at: 'In-Progress' once the driver has
// checked in (the 3.x app writes it with checkInAt at its check-in, maintenance_repository.dart:78, and shows the
// completion form for it), the web's 'in_progress' before (the truck is sent to the shop and the driver's check-in step
// is still ahead: the 3.x app shows that step for 'Scheduled', 'PM Booking' and 'in_progress' only,
// maintenance_action_page.dart:206-231).
func LegacyStatus(status string, checkedIn bool) string {
	switch status {
	case StatusPMBooking:
		return "PM Booking"
	case StatusScheduled:
		return "Scheduled"
	case StatusInProgress:
		if checkedIn {
			return "In-Progress"
		}
		return "in_progress"
	}
	return status
}

// legacyNames maps the JSON fields of a staff write (merge's changed list) onto the Firestore field each one sets
// (validate/maintenanceSchema.ts). files has none: Go uploads carry no legacy URL (main spec §13.8).
var legacyNames = map[string]string{
	"type": "type", "serviceType": "serviceType", "startDate": "startDate", "endDate": "endDate", "status": "status",
	"appointmentTime": "appointmentTime", "pickupAppointmentAt": "pickupAppointment", "costLaborThb": "costLabor",
	"costPartsThb": "costParts", "totalCostThb": "totalCost", "invoiceAmountThb": "invoiceAmount",
	"provider": "provider", "providerLat": "providerLat", "providerLng": "providerLng", "paymentMethod": "paymentMethod",
	"currentMileage": "currentMileage", "nextServiceMileage": "nextServiceMileage", "notes": "notes",
}

// LegacyFields is the Firestore shape of the staff fields of a record (validate/maintenanceSchema.ts and the PM booking
// of fn:triggers.ts:497-509), nil for an empty one (a write-back removes it). The driver's fields (driverSubmitted,
// checkInAt, checkOutAt, images, receipts, invoiceUrl) are the APK's and never part of it. truckRef is the truck's
// Firestore id. status is LegacyStatus of the record's check-in.
func LegacyFields(r maintenancedb.MaintenanceRecord, truckRef string) map[string]any {
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
	date := func(p *time.Time) any {
		if p == nil {
			return nil
		}
		return p.Format(time.DateOnly)
	}
	var pickup any
	if r.PickupAppointmentAt != nil { // the admin form's local value yyyy-MM-dd'T'HH:mm:ss (Bangkok)
		pickup = r.PickupAppointmentAt.In(clock.Bangkok).Format("2006-01-02T15:04:05")
	}
	var truck any
	if truckRef != "" {
		truck = truckRef
	}
	return map[string]any{
		"truckId": truck, "truckLicensePlate": str(r.TruckLicensePlateSnapshot), "truckBrand": str(r.TruckBrandSnapshot),
		"type": r.MaintenanceType, "serviceType": r.ServiceType, "startDate": r.StartDate.Format(time.DateOnly),
		"endDate": date(r.EndDate), "status": LegacyStatus(r.Status, r.CheckInAt != nil),
		"appointmentTime": str(r.AppointmentTime), "pickupAppointment": pickup, "costLabor": num(r.CostLaborThb),
		"costParts": num(r.CostPartsThb), "totalCost": num(r.TotalCostThb), "invoiceAmount": num(r.InvoiceAmountThb),
		"provider": str(r.Provider), "providerLat": num(r.ProviderLat), "providerLng": num(r.ProviderLng),
		"paymentMethod": str(r.PaymentMethod), "currentMileage": integer(r.CurrentMileage),
		"nextServiceMileage": integer(r.NextServiceMileage), "notes": str(r.Notes),
	}
}

// LegacyWrite is the Firestore write of one staff write of r: the fields the class C write-back of T31 merges into the
// record's document (main spec §11.10, §13.8: Firestore first, the FS->PG mirror fills PostgreSQL). A created record
// (POST, the PM booking) sets every staff field of LegacyFields with createdAt and createdBy. A change sets only the
// Firestore fields of the JSON fields it changed (changed: merge's list), so the fields the 3.x app writes on the
// same document (status and checkInAt at its check-in, invoiceAmount and driverSubmitted at its completion) keep
// their values unless the staff changed them; status is written only by a status change. Both stamp updatedAt and
// updatedBy (actorRef: the actor's Firebase uid). A change of no Firestore field (files alone) is nil: nothing is
// written, so the document's updatedAt does not move for it.
func LegacyWrite(r maintenancedb.MaintenanceRecord, truckRef, actorRef string, changed []string, created bool, now time.Time) map[string]any {
	all := LegacyFields(r, truckRef)
	fields := map[string]any{}
	if created {
		maps.Copy(fields, all)
		fields["createdAt"], fields["createdBy"] = now, actorRef
	} else {
		for _, c := range changed {
			if name, ok := legacyNames[c]; ok {
				fields[name] = all[name]
			}
		}
		if len(fields) == 0 {
			return nil
		}
	}
	fields["updatedAt"], fields["updatedBy"] = now, actorRef
	return fields
}
