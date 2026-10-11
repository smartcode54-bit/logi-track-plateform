package maintenance

import (
	"context"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/maintenance/maintenancedb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// Collection is the Firestore collection of maintenance records (class C until P7b).
const Collection = "maintenance"

// LegacyStatus maps a canonical status onto the literal the installed APKs read (main spec §13.8 status table):
// pm_booking 'PM Booking', scheduled 'Scheduled', in_progress 'In-Progress'; completed and cancelled as they are.
func LegacyStatus(status string) string {
	switch status {
	case StatusPMBooking:
		return "PM Booking"
	case StatusScheduled:
		return "Scheduled"
	case StatusInProgress:
		return "In-Progress"
	}
	return status
}

// LegacyFields is the Firestore shape of the staff-owned fields of a record (validate/maintenanceSchema.ts and the
// PM booking of fn:triggers.ts:497-509): the fields a staff write sets, nil for an empty one (the write-back removes
// it). The driver's fields (driverSubmitted, checkInAt, checkOutAt, images, receipts, invoiceUrl) are the APK's and
// never written back. truckRef is the truck's Firestore id; created adds createdAt / createdBy.
func LegacyFields(r maintenancedb.MaintenanceRecord, truckRef, actorRef string, now time.Time, created bool) map[string]any {
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
	f := map[string]any{
		"truckId": truck, "truckLicensePlate": str(r.TruckLicensePlateSnapshot), "truckBrand": str(r.TruckBrandSnapshot),
		"type": r.MaintenanceType, "serviceType": r.ServiceType, "startDate": r.StartDate.Format(time.DateOnly),
		"endDate": date(r.EndDate), "status": LegacyStatus(r.Status), "appointmentTime": str(r.AppointmentTime),
		"pickupAppointment": pickup, "costLabor": num(r.CostLaborThb), "costParts": num(r.CostPartsThb),
		"totalCost": num(r.TotalCostThb), "invoiceAmount": num(r.InvoiceAmountThb), "provider": str(r.Provider),
		"providerLat": num(r.ProviderLat), "providerLng": num(r.ProviderLng), "paymentMethod": str(r.PaymentMethod),
		"currentMileage": integer(r.CurrentMileage), "nextServiceMileage": integer(r.NextServiceMileage),
		"notes": str(r.Notes), "updatedAt": now, "updatedBy": actorRef,
	}
	if created {
		f["createdAt"], f["createdBy"] = now, actorRef
	}
	return f
}

// writeBack writes a record's staff fields to its Firestore document before COMMIT when the write-back is on (class
// C, main spec §13.8): a record born in PostgreSQL gets the minted document id (its uuid, R25) as legacy_doc_id. A
// failed write rolls the request back (503 unavailable).
func (s *Service) writeBack(ctx context.Context, q *maintenancedb.Queries, r maintenancedb.MaintenanceRecord, t *truckState, created bool) error {
	if s.wb == nil {
		return nil
	}
	doc := r.ID.String()
	if r.LegacyDocID != nil && *r.LegacyDocID != "" {
		doc = *r.LegacyDocID
	} else if err := q.SetLegacyDocID(ctx, maintenancedb.SetLegacyDocIDParams{LegacyDocID: &doc, ID: r.ID}); err != nil {
		return err
	}
	truckRef := ""
	switch {
	case t != nil && t.LegacyDocID != nil && *t.LegacyDocID != "":
		truckRef = *t.LegacyDocID
	case r.TruckID != nil:
		truckRef = r.TruckID.String()
	}
	actorRef := "system"
	if r.UpdatedBy != nil {
		if v, err := q.ActorLegacyUID(ctx, *r.UpdatedBy); err == nil {
			actorRef = v
		}
	}
	if err := s.wb.Merge(ctx, Collection, doc, LegacyFields(r, truckRef, actorRef, s.now(), created)); err != nil {
		return writeback.ErrUnavailable(err)
	}
	return nil
}
