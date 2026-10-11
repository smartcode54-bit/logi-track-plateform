package etl

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/tripcheck"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// Operations (Appendix A §A.3.5, §A.3.6, §A.3.8). Billable rows are never rejected (R19): c.reject aborts the run
// for a document with billing evidence.

// DefaultCustomerID is the hard-coded customer of the blanket link backfill (backfillCustomerLinks.ts:6, Q9).
const DefaultCustomerID = "7gbnX0Tv9xNQgTKrgp0F"

// The trip_records.trip_no CHECK and the known trip photo types (photo_type_known, 0004) are tripcheck's, the one Go
// copy of both rules (the operations integration tests compare it with the schema).

// jobCategory is PRIMARY, SUPPLEMENTARY or NULL (absent legacy: never defaulted, ADR 0010).
func (c *docCtx) jobCategory(field string) *string {
	s := strings.ToUpper(c.str(field))
	switch s {
	case "":
		return nil
	case "PRIMARY", "SUPPLEMENTARY":
		return &s
	}
	c.find(field, ReasonStatusOutOfVocab, "neither PRIMARY nor SUPPLEMENTARY", c.get(field))
	return nil
}

// textOrJSON keeps a legacy location value as text (a map is written as its JSON).
func (c *docCtx) textOrJSON(field string) *string {
	if s := c.text(field); s != nil {
		return s
	}
	if v := c.get(field); present(v) {
		s := string(rawJSON(v))
		return &s
	}
	return nil
}

// driverField resolves a driverId field (no name fallback) and records driver_unresolved for a value that matches
// nothing (R12). The raw value and the match kind go into legacy_driver_ref / driver_ref_match.
func (c *docCtx) driverField(field string) (driverMatch, error) {
	m, err := c.driverByRef(c.str(field), "")
	if err == nil && m.raw != "" && !m.ok() {
		c.find(field, ReasonDriverUnresolved, "matches no drivers.legacy_doc_id or legacy_auth_uid", m.raw)
	}
	return m, err
}

func (m driverMatch) idPtr() *uuid.UUID {
	if !m.ok() {
		return nil
	}
	return &m.id
}

func (m driverMatch) rawPtr() *string {
	if m.raw == "" {
		return nil
	}
	return &m.raw
}

func refID(r ref) *uuid.UUID {
	if !r.ok() {
		return nil
	}
	return &r.id
}

// loadTask: tasks/{id} -> tasks (§A.3.5). Tenant from the driver (doc id, auth uid or name); a driverless or
// unmatched task goes to the quarantine tenant (D6). A task without a plan date is rejected unless a trip with
// billing evidence references it (then the run aborts, R19).
func loadTask(c *docCtx) error {
	taskType, ok := c.vocab("taskType", "first_mile", "line_haul")
	switch {
	case !ok:
		return c.reject("taskType", ReasonStatusOutOfVocab, "neither FIRST_MILE nor LINE_HAUL", c.get("taskType"))
	case taskType == "":
		return c.reject("taskType", ReasonMissingRequired, "tasks.task_type is NOT NULL", nil)
	}
	status, ok := c.vocab("status", "pending", "assigned", "checked_in", "in_transit", "completed", "cancelled")
	switch {
	case !ok:
		return c.reject("status", ReasonStatusOutOfVocab, "no lossless task status", c.get("status"))
	case status == "":
		return c.reject("status", ReasonMissingRequired, "tasks.status is NOT NULL", nil)
	}
	if !c.has("date") {
		return c.reject("date", ReasonMissingRequired, "tasks.plan_at is NOT NULL", nil)
	}
	planAt := c.ts("date")
	if planAt == nil {
		return c.reject("date", ReasonBadTimestamp, "tasks.plan_at is NOT NULL", c.get("date"))
	}
	drv, err := c.driverByRef(c.str("driverId"), c.str("driverName"))
	if err != nil {
		return err
	}
	if !drv.ok() && (drv.raw != "" || c.str("driverName") != "") {
		c.find("driverId", ReasonDriverUnresolved, "no driver by doc id, auth uid or name", c.get("driverId"))
	}
	l := c.lookups()
	l.TenantOfDriver = func(string) string { return c.stamped(drv.ref) }
	tenant, err := c.stampWith(tenancy.Tasks, map[string]any{"driverId": "matched"}, l)
	if err != nil {
		return err
	}

	helper, err := c.taskHelper(drv, tenant)
	if err != nil {
		return err
	}
	tr, err := c.truck("truckId")
	if err != nil {
		return err
	}
	var legacyTruckType *string
	tt := c.vehicleClass("truckType")
	if raw := c.str("truckType"); raw != "" && (tt == nil || *tt != raw) {
		legacyTruckType = &raw
	}
	src, err := c.hub("sourceHub", c.str("sourceHub"))
	if err != nil {
		return err
	}
	dst, err := c.hub("destination", c.str("destination"))
	if err != nil {
		return err
	}
	billing, err := c.party("billingCustomerId")
	if err != nil {
		return err
	}
	srcParty, err := c.party("sourceHubLinkedCustomerId")
	if err != nil {
		return err
	}
	dstParty, err := c.party("destinationLinkedCustomerId")
	if err != nil {
		return err
	}
	if err := c.blanketLink("sourceHubLinkedCustomerId", src); err != nil {
		return err
	}
	if err := c.blanketLink("destinationLinkedCustomerId", dst); err != nil {
		return err
	}
	runOrder := c.integer("runOrder")
	if runOrder != nil && *runOrder < 1 {
		c.find("runOrder", ReasonBadNumber, "run order below 1", c.get("runOrder"))
		runOrder = nil
	}
	r := newRow("tasks").set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).
		set("task_no", c.text("taskId")).set("task_type", taskType).set("job_category", c.jobCategory("jobCategory")).
		set("status", status).set("plan_at", planAt).set("legacy_date_str", c.text("dateStr")).
		set("source_hub_raw", c.str("sourceHub")).set("source_hub_id", src.hubID).
		set("destination_raw", c.str("destination")).set("destination_hub_id", dst.hubID).set("destination_soc_key", dst.socKey).
		set("source_linked_party_id", srcParty).set("destination_linked_party_id", dstParty).set("billing_party_id", billing).
		set("truck_id", refID(tr)).set("truck_type", tt).set("legacy_truck_type", legacyTruckType).
		set("license_plate_snapshot", c.text("licensePlate")).
		set("driver_id", drv.idPtr()).set("legacy_driver_ref", drv.rawPtr()).set("driver_ref_match", drv.match).
		set("helper_driver_id", helper).set("run_order", runOrder).
		set("is_multi_delivery", c.isTrue("isMultiDelivery")).
		set("line_checkin_notified_at", c.ts("lineCheckinNotifiedAt"))
	c.times(r)
	id, err := c.rowID("tasks")
	if err != nil {
		return err
	}
	if err := c.files(r, id, tenant, "task", [][3]string{{"checkInPhotoUrl", "checkin_photo", "check_in_photo_file_id"},
		{"checkInAppScreenshotUrl", "checkin_app_screenshot", "check_in_app_screenshot_file_id"}}); err != nil {
		return err
	}
	if _, err := upsertLegacy(c, r.set("id", id)); err != nil {
		return err
	}
	if err := c.taskStops(id); err != nil {
		return err
	}
	c.done("tasks", id)
	return nil
}

// taskStops replaces task_delivery_stops from deliveryStops[] (+ the CF-only sequence, addedAt, sourceId, isCustom;
// §A.3.5): destinationLinked* -> party; a destination repeated within the task is not loaded (the UNIQUE (task_id,
// destination_raw) would refuse it) and raises duplicate_natural_key; the duplicate stays in etl.source_docs.
func (c *docCtx) taskStops(id uuid.UUID) error {
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM task_delivery_stops WHERE task_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	stops, _ := c.get("deliveryStops").([]any)
	usedIndex, usedDest := map[int64]bool{}, map[string]bool{}
	for i, s := range stops {
		m, _ := s.(map[string]any)
		sc := c.sub(m)
		field := fmt.Sprintf("deliveryStops[%d]", i)
		dest := sc.str("destination")
		if dest == "" {
			c.find(field+".destination", ReasonMissingRequired, "a stop without destination is not loaded", s)
			continue
		}
		if usedDest[dest] {
			c.find(field+".destination", ReasonDuplicateNaturalKey, "the destination is already a stop of this task; not loaded", dest)
			continue
		}
		idx := int64(i + 1)
		if n := sc.integer("index"); n != nil && *n >= 1 && !usedIndex[*n] {
			idx = *n
		} else if usedIndex[idx] {
			c.find(field+".index", ReasonBadNumber, "no free stop index; not loaded", m["index"])
			continue
		}
		usedIndex[idx], usedDest[dest] = true, true
		place, err := sc.hub("destination", dest)
		if err != nil {
			return err
		}
		party, err := sc.party("destinationLinkedCustomerId")
		if err != nil {
			return err
		}
		status, ok := lowerVocab(m["status"], "pending", "delivered", "failed")
		if !ok {
			sc.find("status", ReasonStatusOutOfVocab, "not pending, delivered or failed; pending kept", m["status"])
			status = ""
		}
		if status == "" {
			status = "pending"
		}
		r := newRow("task_delivery_stops").set("task_id", id).set("stop_index", idx).set("destination_raw", dest).
			set("destination_hub_id", place.hubID).set("destination_linked_party_id", party).set("status", status).
			set("delivered_at", sc.ts("deliveredAt")).set("delivered_lat", sc.float("deliveredLat")).set("delivered_lng", sc.float("deliveredLng")).
			set("estimated_rate_thb", sc.money("estimatedRateThb", false)).set("drop_fee_thb", sc.money("dropFeeThb", false)).
			set("sequence", sc.integer("sequence")).set("added_at", sc.ts("addedAt")).set("source_id", sc.text("sourceId")).
			set("is_custom", sc.isTrue("isCustom"))
		c.adopt(sc, field+".")
		if err := insert(c.ctx, c.tx, r); err != nil {
			return fmt.Errorf("%s: %w", c.doc.Path, err)
		}
	}
	return nil
}

// files registers URL fields ({field, purpose, column}) owned by row id and sets their file ids on the row (NULL
// when absent).
func (c *docCtx) files(r *row, id, tenant uuid.UUID, ownerKind string, fields [][3]string) error {
	for _, f := range fields {
		fid, err := c.fileRef(f[0], c.get(f[0]), f[1], ownerKind, id, tenant)
		if err != nil {
			return err
		}
		r.set(f[2], fid)
	}
	return nil
}

// taskHelper resolves helperDriverIds[0] (auth uids, ADR 0011 cap 1).
func (c *docCtx) taskHelper(drv driverMatch, tenant uuid.UUID) (*uuid.UUID, error) {
	helpers, _ := c.get("helperDriverIds").([]any)
	if len(helpers) == 0 {
		return nil, nil
	}
	if len(helpers) > 1 {
		c.find("helperDriverIds", ReasonHelperOverflow, "more than the one helper ADR 0011 allows; [0] kept", helpers)
	}
	raw, _ := helpers[0].(string)
	h, err := c.driverByRef(raw, "")
	if err != nil {
		return nil, err
	}
	if !h.ok() {
		c.find("helperDriverIds", ReasonDriverUnresolved, "helper matches no driver", helpers[0])
		return nil, nil
	}
	if drv.ok() && h.id == drv.id {
		return nil, nil // the CHECK forbids a helper equal to the driver; the raw array stays in etl.source_docs
	}
	if h.tenant != tenant && tenant != c.e.quar {
		hc, err := c.contractorOf(h.tenant)
		if err != nil {
			return nil, err
		}
		tc, err := c.contractorOf(tenant)
		if err != nil {
			return nil, err
		}
		if hc != tenant && tc != h.tenant {
			c.find("helperDriverIds", ReasonHelperTenantMismatch, "helper outside the task's tenant and its contractor link", helpers[0])
		}
	}
	return &h.id, nil
}

// blanketLink flags a link equal to the hard-coded default customer while the hub links elsewhere (Q9).
func (c *docCtx) blanketLink(field string, p place) error {
	if c.str(field) != DefaultCustomerID || p.hubID == nil {
		return nil
	}
	var differs bool
	err := c.tx.QueryRow(c.ctx, `SELECT h.linked_party_id IS DISTINCT FROM (
		  SELECT bp.id FROM billing_parties bp JOIN customers cu ON cu.id = bp.customer_id WHERE cu.legacy_doc_id = $2)
		FROM hubs h WHERE h.id = $1`, *p.hubID, DefaultCustomerID).Scan(&differs)
	if err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	if differs {
		c.find(field, ReasonLinkBlanketDefault, "blanket DEFAULT_CUSTOMER_ID link while the hub links elsewhere", DefaultCustomerID)
	}
	return nil
}

// loadTrip: trip_records/{id} -> trip_records + trip_photos + trip_billing_snapshots + trip_no_history (§A.3.6).
func loadTrip(c *docCtx) error {
	status, ok := c.vocab("status", "in_transit", "incident", "delivered", "standby", "cancelled")
	switch {
	case !ok:
		return c.reject("status", ReasonStatusOutOfVocab, "no lossless trip status ('loading' and 'departure' have no writer)", c.get("status"))
	case status == "":
		return c.reject("status", ReasonMissingRequired, "trip_records.status is NOT NULL", nil)
	}
	taskRaw := c.str("taskId")
	task, taskMatch, err := c.taskByRef(taskRaw)
	if err != nil {
		return err
	}
	switch {
	case taskMatch == "ambiguous":
		c.find("taskId", ReasonTaskAmbiguous, "several tasks carry this business task number (Q4)", taskRaw)
		taskMatch = "none"
	case taskRaw != "" && !task.ok():
		c.find("taskId", ReasonTaskUnresolved, "matches no task doc id or number", taskRaw)
	}
	jobType, ok := c.vocab("jobType", "first_mile", "line_haul")
	if !ok {
		return c.reject("jobType", ReasonStatusOutOfVocab, "neither first_mile nor line_haul", c.get("jobType"))
	}
	if jobType == "" && task.ok() {
		if err := c.tx.QueryRow(c.ctx, `SELECT task_type FROM tasks WHERE id = $1`, task.id).Scan(&jobType); err != nil {
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
	}
	if jobType == "" {
		return c.reject("jobType", ReasonMissingRequired, "trip_records.job_type is NOT NULL and the task gives none", nil)
	}
	if err := c.renamedFrom(); err != nil {
		return err
	}
	drv, err := c.driverField("driverId")
	if err != nil {
		return err
	}
	tenant, err := c.stamp(tenancy.TripRecords, map[string]any{"taskId": taskRaw, "driverId": drv.raw}, task)
	if err != nil {
		return err
	}
	if task = c.keepLink("taskId", task, tenant); !task.ok() {
		taskMatch = "none"
	}

	tripNo, err := c.tripNo()
	if err != nil {
		return err
	}
	createdAt := c.tripCreatedAt()
	deliveredAt := c.ts("deliveredTimestamp")
	if status == "delivered" && deliveredAt == nil {
		c.find("deliveredTimestamp", ReasonMissingDeliveredAt, "delivered without deliveredTimestamp (Q13); never derived", nil)
	}
	party, err := c.party("billingCustomerId")
	if err != nil {
		return err
	}
	billingDate := c.ts("billingDate")
	if basis, err := c.partyBasis(party); err != nil {
		return err
	} else if basis == "plan" && billingDate == nil && (status == "delivered" || hasBillingEvidence("trip_records", c.f)) {
		c.find("billingDate", ReasonMissingBillingDate, "plan-basis party without billingDate (D7)", nil)
		if locked, err := c.restampLocked(party, task); err != nil {
			return err
		} else if locked {
			c.find("billingDate", ReasonBillingDateLocked, "the task's plan month has a sent or paid statement of this party: no restamp (D7)", nil)
		}
	}
	if status == "standby" {
		c.find("status", ReasonLegacyStandbyTrip, "legacy standby trip not migrated to standby_records (Q5)", nil)
	}
	tr, err := c.truck("truckId")
	if err != nil {
		return err
	}
	origin, err := c.hub("origin", c.str("origin"))
	if err != nil {
		return err
	}
	dest, err := c.hub("destination", c.str("destination"))
	if err != nil {
		return err
	}
	partner := c.text("partnerCode")
	if ocr, ok := c.get("ocrData").(map[string]any); partner == nil && ok {
		if s, ok := ocr["partnerCode"].(string); ok && strings.TrimSpace(s) != "" {
			partner = &s
		}
	}
	dlat, dlng := c.float("deliveredLat"), c.float("deliveredLng")
	review := c.str("reviewStatus")
	var reviewStatus *string
	if review == "pending_review" {
		reviewStatus = &review
	} else if review != "" {
		c.find("reviewStatus", ReasonStatusOutOfVocab, "not pending_review", review)
	}
	r := newRow("trip_records").set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).set("trip_no", tripNo).
		set("status", status).set("job_type", jobType).set("job_category", c.jobCategory("jobCategory")).
		set("task_id", refID(task)).set("legacy_task_ref", strPtr(taskRaw)).set("task_ref_match", taskMatch).
		set("driver_id", drv.idPtr()).set("legacy_driver_ref", drv.rawPtr()).set("driver_ref_match", drv.match).
		set("origin_raw", c.text("origin")).set("origin_hub_id", origin.hubID).
		set("destination_raw", c.text("destination")).set("destination_hub_id", dest.hubID).set("destination_soc_key", dest.socKey).
		set("seal_code", c.text("sealCode")).set("partner_code", partner).set("ocr_data", jsonValue(c.get("ocrData"))).
		set("truck_id", refID(tr)).set("truck_license_plate_snapshot", c.text("truckLicensePlate")).
		set("truck_type_raw", c.text("truckType")).set("vehicle_class", c.vehicleClass("truckType")).
		set("distance_km", c.float("distance")).set("parcel_count", c.integer("parcelCount")).
		set("seal_time", c.bangkokSealTime("sealTime")).set("total_weight_kg", c.float("totalWeight")).
		set("loading_lat", c.float("lat")).set("loading_lng", c.float("lng")).
		set("std", c.ts("std")).set("sta", c.ts("sta")).set("ata", c.ts("ata")).set("duration_minutes", c.float("durationMinutes")).
		set("delivered_at", deliveredAt).set("delivered_lat", dlat).set("delivered_lng", dlng).
		set("delivered_via", c.optVocab("deliveredVia", "mobile", "admin_web")).set("is_multi_delivery", c.isTrue("isMultiDelivery")).
		set("billing_party_id", party).set("billing_date", billingDate).
		set("needs_admin_review", c.isTrue("needsAdminReview")).set("review_status", reviewStatus).
		set("review_reason", c.text("reviewReason")).set("resubmitted_at", c.ts("resubmittedAt")).
		set("line_delivered_notified_at", c.ts("lineDeliveredNotifiedAt")).set("evidence_token", c.text("evidenceToken")).
		set("created_at", createdAt).setPresent("updated_at", c.ts("updatedAt"))
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	if err := c.tripHistory(id, tripNo); err != nil {
		return err
	}
	if err := c.tripPhotos(id, tenant); err != nil {
		return err
	}
	if err := c.tripStops(id, tenant); err != nil {
		return err
	}
	if err := c.tripSnapshot(id, party, task); err != nil {
		return err
	}
	c.done("trip_records", id)
	return nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// tripNo is spxTripId ?? doc id (Q3). A spxTripId that differs from the doc id without rename provenance, or a
// number another doc already holds, is trip_no_mismatch; the doc id is used when it is free, otherwise the run
// stops for a manual decision (both rows may be billable, R19).
func (c *docCtx) tripNo() (string, error) {
	no := c.doc.ID
	if spx := c.str("spxTripId"); spx != "" && spx != c.doc.ID {
		if tripcheck.ValidateTripNo(spx) != "" {
			c.find("spxTripId", ReasonTripNoMismatch, "fails the trip number charset; the doc id is the trip number", spx)
		} else {
			no = spx
			if c.str("renamedFromTripId") == "" {
				c.find("spxTripId", ReasonTripNoMismatch, "differs from the doc id without rename provenance", spx)
			}
		}
	}
	held := func(n string) (bool, error) {
		var x int
		err := c.tx.QueryRow(c.ctx, `SELECT 1 FROM trip_records WHERE trip_no = $1 AND legacy_doc_id IS DISTINCT FROM $2`, n, c.doc.ID).Scan(&x)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	}
	taken, err := held(no)
	if err != nil {
		return "", fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	if !taken {
		return no, nil
	}
	if no != c.doc.ID {
		idTaken, err := held(c.doc.ID)
		if err != nil {
			return "", fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
		if !idTaken {
			c.find("spxTripId", ReasonTripNoMismatch, "trip number "+no+" is held by another doc; the doc id is the trip number", no)
			return c.doc.ID, nil
		}
	}
	return "", &AbortError{Collection: c.coll, Path: c.doc.Path, Reason: ReasonTripNoMismatch,
		Detail: "trip number " + no + " is held by another document and the doc id is not free; resolve by hand"}
}

// tripCreatedAt is COALESCE(createdAt, std, deliveredTimestamp, Firestore create time) (R19, R68); a derived value
// adds created_at_derived. A trip is never rejected for this field.
func (c *docCtx) tripCreatedAt() time.Time {
	if t := c.ts("createdAt"); t != nil {
		return *t
	}
	for _, f := range []string{"std", "deliveredTimestamp"} {
		if v := c.get(f); present(v) {
			if t, ok := toTime(v); ok {
				c.find("createdAt", ReasonCreatedAtDerived, "createdAt missing; taken from "+f, nil)
				return t.Truncate(time.Microsecond)
			}
		}
	}
	c.find("createdAt", ReasonCreatedAtDerived, "createdAt missing; taken from the Firestore create time", nil)
	if c.doc.CreateTime.IsZero() {
		return c.doc.UpdateTime.Truncate(time.Microsecond)
	}
	return c.doc.CreateTime.Truncate(time.Microsecond)
}

// renamedFrom re-keys the row of a renamed trip (main spec §13.7, T29). renameTripRecord copies trip_records/{old} to
// trip_records/{new} with renamedFromTripId = old and deletes the old document in one batch (renameTripRecord.ts:110-124),
// so the first run that sees the new document finds no row of its id while the old document's row is still there.
// When the source no longer holds the old document, that row takes the new document id (legacy_doc_id): the upsert of
// loadTrip then updates it in place, trip_no becomes the new number and tripHistory appends the one trip_no_history
// row, so the trip keeps its id, its children and every link to it (incident reports re-pointed in the same batch
// resolve the new id to the same row). The old document's etl.source_docs entry is settled: its open findings are
// resolved fixed_at_source and the entry removed, so nothing reports it deleted at source. An old document the
// source still holds (a rename-trip-doc.js copy, Q3) keeps its row and the new document loads as its own, as before;
// a run that cannot tell (a quarantine retry) does not re-key. A trip renamed twice between two runs names only the
// middle id, which no row holds: it loads as a new row and the reconciliation reports the first id deleted at source.
func (c *docCtx) renamedFrom() error {
	old := c.str("renamedFromTripId")
	if old == "" || old == c.doc.ID {
		return nil
	}
	var x int
	err := c.tx.QueryRow(c.ctx, `SELECT 1 FROM trip_records WHERE legacy_doc_id = $1`, c.doc.ID).Scan(&x)
	switch {
	case err == nil:
		return nil // the row already holds the new id
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	var id uuid.UUID
	err = c.tx.QueryRow(c.ctx, `SELECT id FROM trip_records WHERE legacy_doc_id = $1 FOR UPDATE`, old).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	oldPath := c.doc.ParentPath()
	if oldPath != "" {
		oldPath += "/"
	}
	oldPath += c.coll + "/" + old
	found, known, err := c.run.exists(c.coll, oldPath)
	if err != nil {
		return fmt.Errorf("etl: %s: is %s still there: %w", c.doc.Path, oldPath, err)
	}
	if found || !known {
		return nil
	}
	if _, err := c.tx.Exec(c.ctx, `UPDATE trip_records SET legacy_doc_id = $2 WHERE id = $1`, id, c.doc.ID); err != nil {
		return fmt.Errorf("etl: %s: re-key the renamed trip: %w", c.doc.Path, err)
	}
	if _, err := c.tx.Exec(c.ctx, `UPDATE etl.quarantine SET resolved_at = now(), resolved_by = 'etl-rename', resolution = 'fixed_at_source'
		WHERE collection = $1 AND doc_path = $2 AND resolved_at IS NULL`, c.coll, oldPath); err != nil {
		return fmt.Errorf("etl: %s: settle %s: %w", c.doc.Path, oldPath, err)
	}
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM etl.source_docs WHERE collection = $1 AND doc_path = $2`, c.coll, oldPath); err != nil {
		return fmt.Errorf("etl: %s: settle %s: %w", c.doc.Path, oldPath, err)
	}
	c.renamed = old
	return nil
}

// tripHistory appends trip_no_history for a renamed trip (append-only: written once per rename).
func (c *docCtx) tripHistory(id uuid.UUID, tripNo string) error {
	old := c.str("renamedFromTripId")
	if old == "" || old == tripNo {
		return nil
	}
	by, err := c.user("renamedBy", c.str("renamedBy"))
	if err != nil {
		return err
	}
	at := c.ts("renamedAt")
	_, err = c.tx.Exec(c.ctx, `INSERT INTO trip_no_history (trip_id, old_trip_no, new_trip_no, renamed_by, renamed_at)
		SELECT $1, $2, $3, $4, coalesce($5, now())
		WHERE NOT EXISTS (SELECT 1 FROM trip_no_history WHERE trip_id = $1 AND old_trip_no = $2 AND new_trip_no = $3)`,
		id, old, tripNo, by, at)
	if err != nil {
		return fmt.Errorf("etl: %s: trip history: %w", c.doc.Path, err)
	}
	return nil
}

// tripPhotos replaces trip_photos from photos[] (ADR 0018: the whole evidence set). The type is stored verbatim;
// an unknown type still loads (photo_type_known false + photo_type_unknown, R19); duplicate types: last wins.
func (c *docCtx) tripPhotos(id, tenant uuid.UUID) error {
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM trip_photos WHERE trip_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	photos, _ := c.get("photos").([]any)
	for i, p := range photos {
		m, _ := p.(map[string]any)
		field := fmt.Sprintf("photos[%d]", i)
		typ, _ := m["type"].(string)
		if strings.TrimSpace(typ) == "" {
			c.find(field+".type", ReasonPhotoTypeUnknown, "photo without a type is not loaded", p)
			continue
		}
		if !tripcheck.PhotoTypeKnown(typ) {
			c.find(field+".type", ReasonPhotoTypeUnknown, "outside TRIP_PHOTO_TYPE_ENUM and stop_{n}_*; loaded with photo_type_known=false", typ)
		}
		fid, err := c.fileRef(field+".url", m["url"], "trip_photo", "trip", id, tenant)
		if err != nil {
			return err
		}
		if fid == nil {
			continue
		}
		var lat, lng *float64
		var addr *string
		var at *time.Time
		if g, ok := m["geocoding"].(map[string]any); ok {
			sub := c.sub(g)
			lat, lng, addr, at = sub.float("lat"), sub.float("lng"), sub.text("address"), sub.ts("timestamp")
			c.adopt(sub, field+".geocoding.")
		}
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO trip_photos (trip_id, photo_type, file_id, geocode_lat, geocode_lng, geocode_address,
			geocoded_at, position) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (trip_id, photo_type) DO UPDATE SET file_id = EXCLUDED.file_id, geocode_lat = EXCLUDED.geocode_lat,
			  geocode_lng = EXCLUDED.geocode_lng, geocode_address = EXCLUDED.geocode_address, geocoded_at = EXCLUDED.geocoded_at,
			  position = EXCLUDED.position`, id, typ, *fid, lat, lng, addr, at, i); err != nil {
			return fmt.Errorf("etl: %s: photo: %w", c.doc.Path, err)
		}
	}
	return nil
}

// restampLocked reports whether the restamp of a plan-basis trip without billingDate (its task's plan date) would land
// in a period the party already has a sent or paid statement for (D7). The statements load after the trips, so the
// first full load raises the finding in afterStatements; a later load of the trip raises it here.
func (c *docCtx) restampLocked(party *uuid.UUID, task ref) (bool, error) {
	if party == nil || !task.ok() {
		return false, nil
	}
	var locked bool
	err := c.tx.QueryRow(c.ctx, `SELECT EXISTS (SELECT 1 FROM billing_statements s, tasks t WHERE t.id = $2 AND s.billing_party_id = $1
		AND s.status IN ('sent', 'paid') AND s.period_year = extract(year FROM bkk_date(t.plan_at))::int
		AND s.period_month = extract(month FROM bkk_date(t.plan_at))::int)`, *party, task.id).Scan(&locked)
	if err != nil {
		return false, fmt.Errorf("etl: %s: period lock: %w", c.doc.Path, err)
	}
	return locked, nil
}

// tripStops replaces trip_delivery_stops from deliveryStopsProgress[] (completion order, Q16): completed_seq is the
// array position, stop_index the plan index; photos[] inside a stop link their trip_photos row (merge-by-type, ADR
// 0018: the flat photos[] is the whole evidence set, so a stop photo is usually already there) through stop_id.
func (c *docCtx) tripStops(id, tenant uuid.UUID) error {
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM trip_delivery_stops WHERE trip_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	stops, _ := c.get("deliveryStopsProgress").([]any)
	used := map[int64]bool{}
	for i, s := range stops {
		m, _ := s.(map[string]any)
		sc := c.sub(m)
		field := fmt.Sprintf("deliveryStopsProgress[%d]", i)
		dest := sc.str("destination")
		idx := sc.integer("index")
		if dest == "" || idx == nil || *idx < 1 || used[*idx] {
			c.find(field, ReasonMissingRequired, "a stop needs a destination and a unique index >= 1; not loaded", s)
			continue
		}
		used[*idx] = true
		place, err := sc.hub("destination", dest)
		if err != nil {
			return err
		}
		status, ok := lowerVocab(m["status"], "pending", "delivered", "failed")
		if !ok {
			sc.find("status", ReasonStatusOutOfVocab, "not pending, delivered or failed; pending kept", m["status"])
			status = ""
		}
		if status == "" {
			status = "pending"
		}
		var stopID uuid.UUID
		if err := c.tx.QueryRow(c.ctx, `INSERT INTO trip_delivery_stops (trip_id, stop_index, destination_raw, destination_hub_id, source_id,
			status, delivered_at, delivered_lat, delivered_lng, completed_seq) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
			id, *idx, dest, place.hubID, sc.text("sourceId"), status, sc.ts("deliveredAt"), sc.float("deliveredLat"), sc.float("deliveredLng"),
			i+1).Scan(&stopID); err != nil {
			return fmt.Errorf("etl: %s: stop: %w", c.doc.Path, err)
		}
		photos, _ := m["photos"].([]any)
		for j, p := range photos {
			pm, _ := p.(map[string]any)
			typ, _ := pm["type"].(string)
			pf := fmt.Sprintf("%s.photos[%d]", field, j)
			if strings.TrimSpace(typ) == "" {
				sc.find(fmt.Sprintf("photos[%d].type", j), ReasonPhotoTypeUnknown, "photo without a type is not loaded", p)
				continue
			}
			if !tripcheck.PhotoTypeKnown(typ) {
				sc.find(fmt.Sprintf("photos[%d].type", j), ReasonPhotoTypeUnknown, "outside TRIP_PHOTO_TYPE_ENUM and stop_{n}_*; loaded with photo_type_known=false", typ)
			}
			fid, err := c.fileRef(pf+".url", pm["url"], "trip_photo", "trip", id, tenant)
			if err != nil {
				return err
			}
			if fid == nil {
				continue
			}
			if _, err := c.tx.Exec(c.ctx, `INSERT INTO trip_photos (trip_id, stop_id, photo_type, file_id, position)
				VALUES ($1, $2, $3, $4, (SELECT coalesce(max(position) + 1, 0) FROM trip_photos WHERE trip_id = $1))
				ON CONFLICT (trip_id, photo_type) DO UPDATE SET stop_id = EXCLUDED.stop_id`, id, stopID, typ, *fid); err != nil {
				return fmt.Errorf("etl: %s: stop photo: %w", c.doc.Path, err)
			}
		}
		c.adopt(sc, field+".")
	}
	return nil
}

// tripSnapshot loads trip_billing_snapshots iff billingEstimateThb is a number or any billing* field is present
// ("stamped but unpriced"). Legacy prices are never recomputed (R15): computed_by 'etl', compute_version 0. The
// tenant is the billing carrier, the own fleet today (form, R61). billingRateImportId stays in rate_import_id and
// resolves to rate_entry_id by (import_id, party, hub, destination, class, category) when exactly one rate row
// matches (the class from the trip's or its task's truck type, left out when neither has one); billingFuelAdjustmentId
// resolves by legacy doc id. billingMultiDeliveryBreakdown[] -> trip_billing_stop_breakdown.
func (c *docCtx) tripSnapshot(id uuid.UUID, party *uuid.UUID, task ref) error {
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM trip_billing_snapshots WHERE trip_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	stamped := false
	for k, v := range c.f {
		if strings.HasPrefix(k, "billing") && v != nil && k != "billingCustomerId" && k != "billingDate" {
			stamped = true
		}
	}
	if !stamped {
		return nil
	}
	estimate := c.money("billingEstimateThb", false)
	var unpriced *string
	if u := c.str("billingUnpricedReason"); u != "" && estimate == nil {
		switch u {
		case "no_customer", "no_rate", "no_vehicle_class", "no_billing_date":
			unpriced = &u
		default:
			c.find("billingUnpricedReason", ReasonStatusOutOfVocab, "not an unpriced reason of R62", u)
		}
	}
	if c.isTrue("isMultiDelivery") && !c.isTrue("billingIsMultiDelivery") && estimate != nil {
		c.find("billingIsMultiDelivery", ReasonMultidropAsSingle, "multi-drop trip priced as single; loaded as stored", nil)
	}
	computedAt := c.ts("billingComputedAt")
	if computedAt == nil {
		t := c.doc.UpdateTime.Truncate(time.Microsecond)
		computedAt = &t
	}
	r := newRow("trip_billing_snapshots").set("trip_id", id).set("tenant_id", c.e.cfg.OwnFleetTenantID).set("tenant_source", "form").
		set("estimate_thb", estimate).set("unpriced_reason", unpriced).
		set("base_rate_thb", c.money("billingBaseRateThb", false)).set("stop_charge_thb", c.money("billingStopChargeThb", false)).
		set("rate_import_id", c.text("billingRateImportId")).set("lookup_hub_code", c.text("billingLookupHubId")).
		set("lookup_destination_code", c.text("billingLookupDestination")).
		setPresent("rate_multiplier", c.float("billingRateMultiplier")).setPresent("add_thb_per_trip", c.money("billingAddThbPerTrip", false)).
		set("fuel_effective_from_date", c.date("billingEffectiveFromDateStr")).
		set("round_effective_from_date", c.date("billingRoundEffectiveFromDateStr")).
		set("fuel_band_lower_thb", c.money("billingFuelBandLowerThb", false)).set("fuel_band_upper_thb", c.money("billingFuelBandUpperThb", false)).
		set("reference_fuel_price_thb", c.money("billingReferenceFuelPriceThb", false)).
		set("is_multi_delivery", c.isTrue("billingIsMultiDelivery")).set("manual_override", c.isTrue("billingManualOverride")).
		set("job_category_at_pricing", c.jobCategory("billingJobCategory")).
		set("computed_by", "etl").set("compute_version", 0).set("computed_at", computedAt)
	rateEntry, err := c.snapshotRateEntry(party, task)
	if err != nil {
		return err
	}
	fuel, err := c.legacyRef("customer_fuel_rate_adjustments", c.str("billingFuelAdjustmentId"))
	if err != nil {
		return err
	}
	r.set("rate_entry_id", rateEntry).set("fuel_adjustment_id", fuel)
	if err := insert(c.ctx, c.tx, r); err != nil {
		return fmt.Errorf("%s: %w", c.doc.Path, err)
	}
	stops, _ := c.get("billingMultiDeliveryBreakdown").([]any)
	used := map[int64]bool{}
	for i, s := range stops {
		m, _ := s.(map[string]any)
		sc := c.sub(m)
		idx := sc.integer("stopIndex")
		dest := sc.str("destination")
		base, okBase := sc.moneyCents("baseRateThb", false)
		final, okFinal := sc.moneyCents("finalRateThb", false)
		if idx == nil || *idx < 1 || used[*idx] || dest == "" || okBase != moneyOK || okFinal != moneyOK {
			c.find(fmt.Sprintf("billingMultiDeliveryBreakdown[%d]", i), ReasonBadNumber,
				"a breakdown line needs a unique stopIndex >= 1, a destination and both rates; not loaded", s)
			continue
		}
		used[*idx] = true
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO trip_billing_stop_breakdown (trip_id, stop_index, destination_code, base_rate_thb, final_rate_thb)
			VALUES ($1, $2, $3, $4, $5)`, id, *idx, dest, numeric2(base), numeric2(final)); err != nil {
			return fmt.Errorf("etl: %s: breakdown: %w", c.doc.Path, err)
		}
	}
	return nil
}

// legacyRef is the id of the row of table whose legacy_doc_id is raw (nil when blank or unknown). table is a
// constant of the mappers.
func (c *docCtx) legacyRef(table, raw string) (*uuid.UUID, error) {
	if raw == "" {
		return nil, nil
	}
	var id uuid.UUID
	err := c.tx.QueryRow(c.ctx, `SELECT id FROM `+pgx.Identifier{table}.Sanitize()+` WHERE legacy_doc_id = $1`, raw).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	return &id, nil
}

// snapshotRateEntry resolves billingRateImportId to the one rate row of that import for the trip's party, lookup
// hub and destination, category (billingJobCategory, PRIMARY when absent, as the selector coerces it) and, when the
// trip or its task has one, the folded vehicle class. 'manual' and an ambiguous or unknown match stay NULL.
func (c *docCtx) snapshotRateEntry(party *uuid.UUID, task ref) (*uuid.UUID, error) {
	imp := c.str("billingRateImportId")
	if imp == "" || imp == "manual" || party == nil {
		return nil, nil
	}
	cat := strings.ToUpper(c.str("billingJobCategory"))
	if cat != "SUPPLEMENTARY" {
		cat = "PRIMARY"
	}
	class, hasClass := compute.FoldVehicleClass(c.str("truckType"))
	if !hasClass && task.ok() {
		var tt *string
		if err := c.tx.QueryRow(c.ctx, `SELECT truck_type FROM tasks WHERE id = $1`, task.id).Scan(&tt); err != nil {
			return nil, fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
		if tt != nil {
			class, hasClass = compute.FoldVehicleClass(*tt)
		}
	}
	rows, err := c.tx.Query(c.ctx, `SELECT id FROM customer_rate_entries WHERE import_id = $1 AND billing_party_id = $2
		AND hub_code = upper($3) AND destination_code = $4 AND job_category = $5 AND (NOT $6 OR vehicle_class = $7) LIMIT 2`,
		imp, *party, c.str("billingLookupHubId"), c.str("billingLookupDestination"), cat, hasClass, class)
	if err != nil {
		return nil, fmt.Errorf("etl: %s: rate entry: %w", c.doc.Path, err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil || len(ids) != 1 {
		return nil, err
	}
	return &ids[0], nil
}

// loadStandby: standby_records/{id} -> standby_records + standby_photos (§A.3.8); tenant task -> trip -> driver.
func loadStandby(c *docCtx) error {
	status, ok := c.vocab("status", "completed")
	if !ok {
		return c.reject("status", ReasonStatusOutOfVocab, "standby_records.status is completed only", c.get("status"))
	}
	if status == "" {
		status = "completed"
	}
	drv, err := c.driverField("driverId")
	if err != nil {
		return err
	}
	taskRaw, tripRaw := c.str("taskId"), c.str("tripId")
	task, match, err := c.taskByRef(taskRaw)
	if err != nil {
		return err
	}
	if taskRaw != "" && !task.ok() {
		reason := ReasonTaskUnresolved
		if match == "ambiguous" {
			reason = ReasonTaskAmbiguous
		}
		c.find("taskId", reason, "the task link does not resolve to one task", taskRaw)
	}
	trip, err := c.tripByRef(tripRaw)
	if err != nil {
		return err
	}
	if tripRaw != "" && !trip.ok() {
		c.find("tripId", ReasonTripUnresolved, "matches no trip, rename history included", tripRaw)
	}
	tenant, err := c.stamp(tenancy.StandbyRecords, map[string]any{"taskId": taskRaw, "tripId": tripRaw, "driverId": drv.raw}, task, trip)
	if err != nil {
		return err
	}
	// The trigger checks the task link, else the trip link (0004 trg_standby_tenant_check).
	if task = c.keepLink("taskId", task, tenant); !task.ok() {
		trip = c.keepLink("tripId", trip, tenant)
	}
	customer, err := c.party("customerId")
	if err != nil {
		return err
	}
	billingParty, err := c.party("billingCustomerId")
	if err != nil {
		return err
	}
	ended := c.ts("endedAt")
	if ended == nil {
		c.find("endedAt", ReasonMissingEndedAt, "standby without endedAt: unpriced no_ended_at, never derived (R62)", nil)
	}
	estimate := c.money("billingEstimateThb", true)
	var source *string
	switch e := c.str("billingRateEntryId"); {
	case e == "service_fee":
		s := "service_fee"
		source = &s
	case e != "":
		s := "standby_rate"
		source = &s
	}
	var rateEntry *uuid.UUID
	if source != nil && *source == "standby_rate" {
		var err error
		if rateEntry, err = c.legacyRef("standby_rate_entries", c.str("billingRateEntryId")); err != nil {
			return err
		}
	}
	var unpriced *string
	if u := c.str("billingUnpricedReason"); u != "" && estimate == nil {
		switch u {
		case "no_customer", "no_rate", "no_ended_at":
			unpriced = &u
		default:
			c.find("billingUnpricedReason", ReasonStatusOutOfVocab, "not a standby unpriced reason", u)
		}
	}
	migrated := c.text("migratedFromSpxTripId", "migratedFromTripId")
	r := newRow("standby_records").set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).
		set("driver_id", drv.idPtr()).set("legacy_driver_ref", drv.rawPtr()).set("driver_ref_match", drv.match).
		set("task_id", refID(task)).set("trip_id", refID(trip)).set("customer_party_id", customer).
		set("customer_resolved", c.isTrue("customerResolved")).
		set("customer_resolved_from", c.optVocab("customerResolvedFrom", "task", "origin_hub", "manual")).
		set("job_category", c.jobCategory("jobCategory")).set("start_location_raw", c.textOrJSON("startLocation")).
		set("end_location_raw", c.textOrJSON("endLocation")).set("started_at", c.ts("startedAt")).set("ended_at", ended).
		set("duration_minutes", c.integer("durationMinutes")).set("note", c.text("note")).set("status", status).
		set("lat", c.float("lat")).set("lng", c.float("lng")).set("truck_license_plate_snapshot", c.text("truckLicensePlate")).
		set("backfilled_by_admin", c.isTrue("backfilledByAdmin")).set("migrated_from_trip_no", migrated).
		set("billing_estimate_thb", estimate).set("billing_party_id", billingParty).set("billing_rate_source", source).set("billing_rate_entry_id", rateEntry).
		set("billing_effective_from_date", c.date("billingEffectiveFromDateStr")).set("billing_unpriced_reason", unpriced).
		set("billing_computed_at", c.ts("billingComputedAt")).set("line_notified_at", c.ts("lineNotifiedAt")).
		set("evidence_token", c.text("evidenceToken"))
	tr, err := c.truck("truckId")
	if err != nil {
		return err
	}
	r.set("truck_id", refID(tr))
	c.times(r)
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM standby_photos WHERE standby_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	photos, _ := c.get("photos").([]any)
	for i, p := range photos {
		m, _ := p.(map[string]any)
		typ, _ := m["type"].(string)
		field := fmt.Sprintf("photos[%d]", i)
		if typ != "customer_worksheet" && typ != "site_photo" {
			c.find(field+".type", ReasonPhotoTypeUnknown, "standby photo type outside customer_worksheet, site_photo; not loaded", p)
			continue
		}
		fid, err := c.fileRef(field+".url", m["url"], "standby_photo", "standby", id, tenant)
		if err != nil {
			return err
		}
		if fid != nil {
			if _, err := c.tx.Exec(c.ctx, `INSERT INTO standby_photos (standby_id, photo_type, file_id) VALUES ($1, $2, $3)
				ON CONFLICT (standby_id, photo_type) DO UPDATE SET file_id = EXCLUDED.file_id`, id, typ, *fid); err != nil {
				return fmt.Errorf("etl: %s: photo: %w", c.doc.Path, err)
			}
		}
	}
	c.done("standby_records", id)
	return nil
}

// loadIncident: incidentReport/{id} -> incident_reports (§A.3.8); tenant trip -> driver. tripId may be a pre-rename
// number: legacy doc id, then trip_no, then trip_no_history; else legacy_trip_ref + trip_unresolved.
func loadIncident(c *docCtx) error {
	drv, err := c.driverField("driverId")
	if err != nil {
		return err
	}
	tripRaw := c.str("tripId")
	trip, err := c.tripByRef(tripRaw)
	if err != nil {
		return err
	}
	var legacyTrip *string
	if tripRaw != "" && !trip.ok() {
		c.find("tripId", ReasonTripUnresolved, "matches no trip, rename history included", tripRaw)
		legacyTrip = &tripRaw
	}
	tenant, err := c.stamp(tenancy.IncidentReport, map[string]any{"tripId": tripRaw, "driverId": drv.raw}, trip)
	if err != nil {
		return err
	}
	if trip = c.keepLink("tripId", trip, tenant); !trip.ok() && tripRaw != "" {
		legacyTrip = &tripRaw
	}
	tr, err := c.truck("truckId")
	if err != nil {
		return err
	}
	kind := "driver"
	if c.str("reportedBy") == "admin" {
		kind = "admin"
	}
	reporter, err := c.user("reportedByUid", c.str("reportedByUid"))
	if err != nil {
		return err
	}
	r := newRow("incident_reports").set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).
		set("driver_id", drv.idPtr()).set("legacy_driver_ref", drv.rawPtr()).set("driver_ref_match", drv.match).
		set("trip_id", refID(trip)).set("legacy_trip_ref", legacyTrip).set("delay_cause", c.text("delayCause")).
		set("description", c.text("description")).set("lat", c.float("lat")).set("lng", c.float("lng")).
		set("truck_id", refID(tr)).set("truck_license_plate_snapshot", c.text("truckLicensePlate", "truckPlate")).
		set("reported_by_kind", kind).set("reported_by_user_id", reporter)
	c.times(r)
	id, err := c.rowID("incident_reports")
	if err != nil {
		return err
	}
	if err := c.files(r, id, tenant, "incident", [][3]string{{"mapPhotoUrl", "incident_photo", "map_photo_file_id"},
		{"situation1PhotoUrl", "incident_photo", "situation1_photo_file_id"},
		{"situation2PhotoUrl", "incident_photo", "situation2_photo_file_id"}}); err != nil {
		return err
	}
	if _, err := upsertLegacy(c, r.set("id", id)); err != nil {
		return err
	}
	c.done("incident_reports", id)
	return nil
}
