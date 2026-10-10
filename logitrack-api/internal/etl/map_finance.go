package etl

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// Finance (Appendix A §A.3.10): vehicle expenses (tenant driver -> truck) and maintenance (tenant truck).

// loadExpense: vehicle_expenses/{id} -> vehicle_expenses. driverId is an auth uid (web toll import: authUid ??
// docId). A negative or unparseable amount rejects the row (amount_thb is NOT NULL); duplicate fuel receipts
// (driverId, taxInvId) load unchanged with duplicate_natural_key until the owner signs off and 0010 adds the
// UNIQUE index (D5).
func loadExpense(c *docCtx) error {
	typ, ok := c.vocab("type", "fuel", "other")
	switch {
	case !ok:
		return c.reject("type", ReasonStatusOutOfVocab, "neither fuel nor other", c.get("type"))
	case typ == "":
		return c.reject("type", ReasonMissingRequired, "vehicle_expenses.expense_type is NOT NULL", nil)
	}
	if !c.has("date") {
		return c.reject("date", ReasonMissingRequired, "vehicle_expenses.expense_at is NOT NULL", nil)
	}
	at := c.ts("date")
	if at == nil {
		return c.reject("date", ReasonBadTimestamp, "vehicle_expenses.expense_at is NOT NULL", c.get("date"))
	}
	amount, res := c.moneyCents("amount", true)
	switch res {
	case moneyAbsent:
		return c.reject("amount", ReasonMissingRequired, "vehicle_expenses.amount_thb is NOT NULL", nil)
	case moneyBad:
		return c.reject("amount", ReasonBadNumber, "vehicle_expenses.amount_thb is NOT NULL", c.get("amount"))
	case moneyNegative:
		return c.reject("amount", ReasonNegativeMoney, "vehicle_expenses.amount_thb must be >= 0", c.get("amount"))
	}
	status, ok := c.vocab("status", "pending", "approved", "rejected")
	if !ok {
		return c.reject("status", ReasonStatusOutOfVocab, "no lossless expense status", c.get("status"))
	}
	if status == "" {
		status = "pending"
	}
	drv, err := c.driverField("driverId")
	if err != nil {
		return err
	}
	tr, err := c.truck("truckId")
	if err != nil {
		return err
	}
	tenant, err := c.stamp(tenancy.VehicleExpenses, map[string]any{"driverId": drv.raw, "truckId": c.str("truckId")})
	if err != nil {
		return err
	}
	if c.tenantSource != tenancy.SourceQuarantine && !drv.ok() && !tr.ok() {
		// Only a re-applied row whose tenant is frozen (main spec §13.5) gets here; outside the quarantine tenant the
		// CHECK needs a driver or a truck, so the row stays as it was loaded before.
		return c.reject("driverId", ReasonDriverUnresolved, "the row keeps its tenant and needs a driver or truck there; fix at source and retry", c.get("driverId"))
	}
	odo := c.integer("odometer")
	if odo != nil && *odo < 0 {
		c.find("odometer", ReasonBadNumber, "negative odometer", c.get("odometer"))
		odo = nil
	}
	var lat, lng *float64
	if v := c.get("refillLocation"); present(v) {
		if la, lo, ok := geo(v); ok {
			lat, lng = &la, &lo
		} else {
			c.find("refillLocation", ReasonBadNumber, "not a 'lat,lng' pair", v)
		}
	}
	taxInv := c.text("taxInvId")
	if typ == "fuel" && taxInv != nil && drv.ok() {
		var other string
		err := c.tx.QueryRow(c.ctx, `SELECT legacy_doc_id FROM vehicle_expenses WHERE expense_type = 'fuel' AND driver_id = $1
			AND tax_inv_id = $2 AND legacy_doc_id IS DISTINCT FROM $3 LIMIT 1`, drv.id, *taxInv, c.doc.ID).Scan(&other)
		switch {
		case err == nil:
			c.find("taxInvId", ReasonDuplicateNaturalKey, "the driver's fuel receipt is also "+other+" (D5: both loaded)", *taxInv)
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
	}
	r := newRow("vehicle_expenses").set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).
		set("driver_id", drv.idPtr()).set("legacy_driver_ref", drv.rawPtr()).set("driver_ref_match", drv.match).
		set("truck_id", refID(tr)).set("truck_license_plate_snapshot", c.text("truckLicensePlate")).
		set("expense_type", typ).set("category", c.optVocab("category", "tire_repair", "maintenance", "toll", "parking", "other")).
		set("expense_at", at).set("amount_thb", numeric2(amount)).set("status", status).
		set("volume_liters", c.float("volumeLiters")).set("price_per_liter", c.money("pricePerLiter", false)).
		set("odometer_km", odo).set("station_tax_id", c.text("stationTaxId", "gasStation")).set("tax_inv_id", taxInv).
		set("refill_lat", lat).set("refill_lng", lng).set("note", c.text("note")).set("description", c.text("description")).
		set("admin_note", c.text("adminNote")).set("distance_km", c.float("distance")).
		set("toll_import_sequence", c.integer("tollImportSequence")).set("toll_location", c.text("tollLocation")).
		set("toll_lane", c.text("tollLane")).set("toll_source_type", c.text("tollSourceType"))
	c.times(r)
	id, err := c.rowID("vehicle_expenses")
	if err != nil {
		return err
	}
	if err := c.files(r, id, tenant, "expense", [][3]string{{"receiptPhotoUrl", "expense_receipt", "receipt_file_id"},
		{"odometerPhotoUrl", "expense_odometer", "odometer_file_id"}}); err != nil {
		return err
	}
	if _, err := upsertLegacy(c, r.set("id", id)); err != nil {
		return err
	}
	c.done("vehicle_expenses", id)
	return nil
}

var hhmm = regexp.MustCompile(`^[0-9]{2}:[0-9]{2}$`)

// loadMaintenance: maintenance/{id} -> maintenance_records; tenant from the truck. Images, receipts and the
// invoice (maintenance_files) load with T24.
func loadMaintenance(c *docCtx) error {
	typ := strings.ToUpper(c.str("type"))
	switch typ {
	case "PM", "CM":
	case "":
		return c.reject("type", ReasonMissingRequired, "maintenance_records.maintenance_type is NOT NULL", nil)
	default:
		return c.reject("type", ReasonStatusOutOfVocab, "neither PM nor CM", c.get("type"))
	}
	if !c.has("serviceType") {
		return c.reject("serviceType", ReasonMissingRequired, "maintenance_records.service_type is NOT NULL", nil)
	}
	if !c.has("startDate") {
		return c.reject("startDate", ReasonMissingRequired, "maintenance_records.start_date is NOT NULL", nil)
	}
	start := c.date("startDate")
	if start == nil {
		return c.reject("startDate", ReasonBadTimestamp, "maintenance_records.start_date is NOT NULL", c.get("startDate"))
	}
	status, ok := c.vocab("status", "pm_booking", "scheduled", "in_progress", "completed", "cancelled")
	switch {
	case !ok:
		return c.reject("status", ReasonStatusOutOfVocab, "no lossless maintenance status", c.get("status"))
	case status == "":
		return c.reject("status", ReasonMissingRequired, "maintenance_records.status is NOT NULL", nil)
	}
	tr, err := c.truck("truckId")
	if err != nil {
		return err
	}
	tenant, err := c.stamp(tenancy.Maintenance, map[string]any{"truckId": c.str("truckId")})
	if err != nil {
		return err
	}
	if c.tenantSource != tenancy.SourceQuarantine && !tr.ok() {
		// A re-applied row whose tenant is frozen (main spec §13.5): outside the quarantine tenant the CHECK needs a
		// truck, so the row stays as it was loaded before.
		return c.reject("truckId", ReasonTruckUnresolved, "the row keeps its tenant and needs a truck there; fix at source and retry", c.get("truckId"))
	}
	var appt *string
	if s := c.str("appointmentTime"); s != "" {
		if hhmm.MatchString(s) {
			appt = &s
		} else {
			c.find("appointmentTime", ReasonBadTimestamp, "not HH:mm", s)
		}
	}
	r := newRow("maintenance_records").set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).
		set("truck_id", refID(tr)).set("truck_license_plate_snapshot", c.text("truckLicensePlate", "licensePlate")).
		set("maintenance_type", typ).set("service_type", c.text("serviceType")).set("start_date", start).
		set("end_date", c.date("endDate")).set("status", status).set("appointment_time", appt).
		set("pickup_appointment_at", c.ts("pickupAppointment")).set("cost_labor_thb", c.money("costLabor", false)).
		set("cost_parts_thb", c.money("costParts", false)).set("total_cost_thb", c.money("totalCost", false)).
		set("invoice_amount_thb", c.money("invoiceAmount", false)).set("provider", c.text("provider")).
		set("provider_lat", c.float("providerLat")).set("provider_lng", c.float("providerLng")).
		set("payment_method", c.optVocab("paymentMethod", "cash", "credit_card", "billing", "transfer", "insurance_claim")).
		set("current_mileage", c.integer("currentMileage")).set("next_service_mileage", c.integer("nextServiceMileage")).
		set("driver_submitted", c.isTrue("driverSubmitted")).set("notes", c.text("notes"))
	c.times(r)
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	c.done("maintenance_records", id)
	return nil
}
