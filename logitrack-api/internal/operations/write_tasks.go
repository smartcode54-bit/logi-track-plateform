package operations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/vehicleclass"
	hrcompute "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// Task writes (Appendix B §B.2.11): POST /v1/tasks, PATCH /v1/tasks/{id} (with the R13 reassignment of T28 for a body
// whose tenantId differs from the task's), POST /v1/tasks/{id}/cancel, POST /v1/tasks/{id}/stops, POST
// /v1/tasks/import (write_import.go). They replace createOrUpdateTask (tasks.ts:70), the boards' direct addDoc /
// updateDoc writes (useFirstMileTask.ts, useLineHaulTask.ts, job-assign) and notifyTaskUpdate (triggers.ts:312, which
// had no authentication): every route is authenticated and capability-gated (operations:manage_tasks).

// Body fields of the task writes.
var (
	taskCreateFields = []string{"taskType", "date", "time", "actualPickupAt", "sourceHub", "sourceHubId", "destination",
		"destinationHubId", "jobCategory", "truckId", "truckType", "driverId", "helperDriverId", "billingPartyId",
		"sourceLinkedPartyId", "destinationLinkedPartyId", "isMultiDelivery", "deliveryStops"}
	taskPatchFields = append(slices.DeleteFunc(slices.Clone(taskCreateFields), func(k string) bool { return k == "taskType" }),
		"status", "runOrder", "tenantId")
	stopFields = []string{"destination", "destinationHubId", "destinationLinkedPartyId", "sourceId", "isCustom"}
)

// Reasons of the 409 failed_precondition of task writes.
const (
	ReasonTaskInProgress   = "task_in_progress"
	ReasonTaskClosed       = "task_closed"
	ReasonDriverInactive   = "driver_inactive"
	ReasonStatusNeedDriver = "status_needs_driver"
	// ReasonNoPrimaryDestination: a stop is added to a task without a destination to keep as stop 1.
	ReasonNoPrimaryDestination = "no_primary_destination"
)

// taskFields are the parsed fields of a task write: set names those the body carries (a PATCH changes only them).
type taskFields struct {
	set                      map[string]bool
	taskType                 string
	planAt                   *time.Time
	planTime                 *string
	actualPickupAt           *time.Time
	sourceHub                *string
	sourceHubID              *uuid.UUID
	destination              *string
	destinationHubID         *uuid.UUID
	jobCategory              *string
	truckID                  *uuid.UUID
	truckType                *string
	driverID                 *uuid.UUID
	helperDriverID           *uuid.UUID
	billingPartyID           *uuid.UUID
	sourceLinkedPartyID      *uuid.UUID
	destinationLinkedPartyID *uuid.UUID
	isMultiDelivery          *bool
	stops                    []stopInput
	status                   *string
	runOrder                 *int32
	tenantID                 *uuid.UUID
	taskNo                   *string // the import's number of record (a sheet's own id)
}

// stopInput is one planned stop of a task write.
type stopInput struct {
	destination              string
	destinationHubID         *uuid.UUID
	destinationLinkedPartyID *uuid.UUID
	sourceID                 *string
	isCustom                 bool
}

func (t taskFields) has(k string) bool { return t.set[k] }

// parseTaskFields reads a task body: create (POST: taskType, date, sourceHub, destination and billingPartyId are
// required) or patch (PATCH: every field optional, plus status, runOrder and tenantId).
func parseTaskFields(f fields, create bool) taskFields {
	allowed := taskPatchFields
	if create {
		allowed = taskCreateFields
	}
	f.only(allowed...)
	t := taskFields{set: map[string]bool{}}
	for _, k := range allowed {
		if f.has(k) {
			t.set[k] = true
		}
	}
	if create {
		if v, ok := taskTypeOf(f.required("taskType")); ok {
			t.taskType = v
		} else if f.has("taskType") && !f.isNull("taskType") {
			f.ck.add("taskType", "invalid")
		}
		t.planAt = f.planInstant("date")
		if !f.has("date") {
			f.ck.add("date", "required")
		}
		if v := f.required("sourceHub"); v != "" {
			t.sourceHub = &v
		}
		if v := f.required("destination"); v != "" {
			t.destination = &v
		}
		if t.billingPartyID = f.id("billingPartyId"); t.billingPartyID == nil && !f.ck.hasField("billingPartyId") {
			f.ck.add("billingPartyId", "required") // ADR 0027: the billing customer is chosen at assign
		}
	} else {
		if f.has("date") {
			t.planAt = f.planInstant("date")
		}
		if f.has("sourceHub") {
			if v := f.required("sourceHub"); v != "" {
				t.sourceHub = &v
			}
		}
		if f.has("destination") {
			if v := f.required("destination"); v != "" {
				t.destination = &v
			}
		}
		t.billingPartyID = f.id("billingPartyId")
		if f.has("status") {
			switch v := f.optText("status"); {
			case v == nil:
				f.ck.add("status", "required")
			case *v == TaskPending || *v == TaskAssigned:
				t.status = v
			default:
				f.ck.add("status", "not_settable") // the lifecycle moves through check-in, delivery and cancel
			}
		}
		if f.has("runOrder") {
			if t.runOrder = f.integer("runOrder"); t.runOrder != nil && *t.runOrder < 1 {
				f.ck.add("runOrder", "out_of_range")
			}
		}
		t.tenantID = f.id("tenantId")
	}
	t.planTime = f.planTime("time")
	t.actualPickupAt = f.instant("actualPickupAt")
	t.sourceHubID = f.id("sourceHubId")
	t.destinationHubID = f.id("destinationHubId")
	if f.has("jobCategory") {
		if v := f.optText("jobCategory"); v == nil {
			f.ck.add("jobCategory", "required") // NULL is the legacy unknown, never written by a form (ADR 0010)
		} else if c, ok := jobCategoryOf(*v); ok {
			t.jobCategory = &c
		} else {
			f.ck.add("jobCategory", "invalid")
		}
	}
	t.truckID = f.id("truckId")
	if v := f.optText("truckType"); v != nil {
		if up := strings.ToUpper(*v); vehicleclass.Valid(up) {
			t.truckType = &up
		} else {
			f.ck.add("truckType", "invalid")
		}
	}
	t.driverID = f.id("driverId")
	t.helperDriverID = f.id("helperDriverId")
	t.sourceLinkedPartyID = f.id("sourceLinkedPartyId")
	t.destinationLinkedPartyID = f.id("destinationLinkedPartyId")
	t.isMultiDelivery = f.boolean("isMultiDelivery")
	if raw := f.raw("deliveryStops"); raw != nil && !isNull(raw) {
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			f.ck.add("deliveryStops", "invalid")
		}
		for _, it := range items {
			sf, err := parseFields(it, f.ck)
			if err != nil || len(it) == 0 || it[0] != '{' {
				f.ck.add("deliveryStops", "invalid")
				continue
			}
			sf.only(stopFields...)
			st := stopInput{destination: sf.required("destination"), destinationHubID: sf.id("destinationHubId"),
				destinationLinkedPartyID: sf.id("destinationLinkedPartyId"), sourceID: sf.optText("sourceId")}
			if b := sf.boolean("isCustom"); b != nil {
				st.isCustom = *b
			}
			t.stops = append(t.stops, st)
		}
	}
	return t
}

// hasField reports whether a violation of field was recorded already.
func (c *checker) hasField(field string) bool {
	return slices.ContainsFunc(c.bad, func(v httpx.FieldViolation) bool { return v.Field == field })
}

// --- resolution ---------------------------------------------------------------------------------------------------

// taskPlan is the row a task write leads to, with the master rows it read (the write-back names them by their
// Firestore ids) and the planned stops when the write sets them.
type taskPlan struct {
	next     Task
	stops    []NewStop
	stopsSet bool
	truck    *operationsdb.WriteTruckRow
	driver   *operationsdb.WriteDriverRow
	helper   *operationsdb.WriteDriverRow
	parties  map[uuid.UUID]operationsdb.WritePartyRow
	newDrv   bool // the driver changed
	prevDrv  *uuid.UUID
	changed  []string // the payload fields that changed (camelCase)
	planMove bool     // the Bangkok plan day moved
}

// planTask merges a task write into cur (the zero Task for a create) inside the caller's transaction: places resolve
// to their hub (and the hub's linked party when the body names none), the truck's plate and vehicle class are
// snapshotted from the truck master (unknown class = NULL, the raw type kept, R15), the driver, helper and parties
// must be rows the caller reads (422 <field> not_found), and the driver and status rules of a PATCH apply: status and
// run_order change only with the driver or when the body sets them (fixes tasks.ts:120-137).
func (s *Service) planTask(ctx context.Context, q *operationsdb.Queries, d directory, ck *checker, cur Task, in taskFields,
	create bool) (taskPlan, error) {
	pl := taskPlan{next: cur, parties: map[uuid.UUID]operationsdb.WritePartyRow{}}
	n := &pl.next
	mark := func(k string) {
		if !slices.Contains(pl.changed, k) {
			pl.changed = append(pl.changed, k)
		}
	}
	if create {
		n.TaskType = in.taskType
		jc := "PRIMARY" // resolveJobCategoryWrite(undefined, isCreate): PRIMARY on create (ADR 0010 R2)
		n.JobCategory = &jc
	}
	if in.planAt != nil && (create || !in.planAt.Equal(cur.PlanAt)) {
		if !create && PlanDate(*in.planAt) != PlanDate(cur.PlanAt) {
			pl.planMove = true
			mark("planDate")
		}
		n.PlanAt = *in.planAt
		pd := PlanDate(*in.planAt)
		n.PlanDate = &pd
		mark("planAt")
	}
	if in.has("time") && !sameStr(in.planTime, cur.PlanTime) {
		n.PlanTime = in.planTime
		mark("planTime")
	}
	if in.has("actualPickupAt") && !sameTime(in.actualPickupAt, cur.ActualPickupAt) {
		n.ActualPickupAt = in.actualPickupAt
		mark("actualPickupAt")
	}
	if in.has("jobCategory") && !sameStr(in.jobCategory, cur.JobCategory) {
		n.JobCategory = in.jobCategory
		mark("jobCategory")
	}
	// Places: the raw text is what the legacy documents carry; the hub id is resolved here, as the ETL resolves it at
	// load (main spec §13.4), so the monitor's place keys work for rows born in Go.
	if create || in.has("sourceHub") || in.has("sourceHubId") {
		raw, hub, party := placeOf(d, ck, "sourceHub", in.sourceHub, in.sourceHubID, cur.SourceHubRaw)
		if raw != cur.SourceHubRaw || !sameID(hub, cur.SourceHubID) {
			n.SourceHubRaw, n.SourceHubID = raw, hub
			mark("sourceHub")
			if !in.has("sourceLinkedPartyId") {
				n.SourceLinkedPartyID = party
			}
		}
	}
	if create || in.has("destination") || in.has("destinationHubId") {
		raw, hub, party := placeOf(d, ck, "destination", in.destination, in.destinationHubID, cur.DestinationRaw)
		if raw != cur.DestinationRaw || !sameID(hub, cur.DestinationHubID) {
			n.DestinationRaw, n.DestinationHubID = raw, hub
			n.DestinationSocKey = d.resolve(raw).socKey
			if hub != nil {
				n.DestinationSocKey = nil
			}
			mark("destination")
			if !in.has("destinationLinkedPartyId") {
				n.DestinationLinkedPartyID = party
			}
		}
	}
	for _, link := range []struct {
		field string
		id    *uuid.UUID
		dst   **uuid.UUID
	}{{"billingPartyId", in.billingPartyID, &n.BillingPartyID},
		{"sourceLinkedPartyId", in.sourceLinkedPartyID, &n.SourceLinkedPartyID},
		{"destinationLinkedPartyId", in.destinationLinkedPartyID, &n.DestinationLinkedPartyID}} {
		if !in.has(link.field) && (!create || link.id == nil) {
			continue
		}
		if link.id != nil {
			if _, err := s.party(ctx, q, ck, link.field, *link.id, pl.parties); err != nil {
				return pl, err
			}
		}
		*link.dst = link.id
	}
	if !sameID(n.BillingPartyID, cur.BillingPartyID) {
		mark("billingPartyId")
	}
	if !sameID(n.SourceLinkedPartyID, cur.SourceLinkedPartyID) {
		mark("sourceLinkedPartyId")
	}
	if !sameID(n.DestinationLinkedPartyID, cur.DestinationLinkedPartyID) {
		mark("destinationLinkedPartyId")
	}
	if err := loadParties(ctx, q, pl.parties, n.SourceLinkedPartyID, n.DestinationLinkedPartyID, n.BillingPartyID); err != nil {
		return pl, err
	}
	// The truck of this job (ADR "vehicle identity": the task carries its truck, never the driver's binding).
	if in.has("truckId") {
		if in.truckID == nil {
			if cur.TruckID != nil || cur.LicensePlateSnapshot != nil {
				n.TruckID, n.LicensePlateSnapshot = nil, nil
				mark("truckId")
			}
		} else {
			tr, err := q.WriteTruck(ctx, *in.truckID)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				ck.add("truckId", "not_found")
			case err != nil:
				return pl, MapError(err)
			default:
				pl.truck = &tr
				plate := strings.TrimSpace(tr.LicensePlate)
				n.TruckID, n.LicensePlateSnapshot = &tr.ID, &plate
				n.TruckType, n.LegacyTruckType = tr.VehicleClass, nil
				if tr.VehicleClass == nil && strings.TrimSpace(tr.TypeRaw) != "" {
					raw := tr.TypeRaw
					n.LegacyTruckType = &raw // unknown class: unpriced no_vehicle_class (R15), the raw type kept
				}
				if in.truckType != nil && tr.VehicleClass != nil && *in.truckType != *tr.VehicleClass {
					ck.add("truckType", "not_truck_class")
				}
				if !sameID(n.TruckID, cur.TruckID) || !sameStr(n.LicensePlateSnapshot, cur.LicensePlateSnapshot) ||
					!sameStr(n.TruckType, cur.TruckType) {
					mark("truckId")
				}
			}
		}
	}
	if in.truckType != nil && in.truckID == nil && !sameStr(in.truckType, cur.TruckType) {
		if !in.has("truckId") && cur.TruckID != nil {
			// The task keeps its truck: a class-only change must be the truck's class, as on create (R15, the vehicle
			// identity ADR: the truck's class wins; billing prices with the task's class). A truck of unknown class may
			// take one; a truck the caller no longer reads is not checked.
			tr, err := q.WriteTruck(ctx, *cur.TruckID)
			switch {
			case err == nil:
				pl.truck = &tr
				if tr.VehicleClass != nil && *tr.VehicleClass != *in.truckType {
					ck.add("truckType", "not_truck_class")
				}
			case !errors.Is(err, pgx.ErrNoRows):
				return pl, MapError(err)
			}
		}
		n.TruckType, n.LegacyTruckType = in.truckType, nil
		mark("truckType")
	}
	if n.TruckID != nil && pl.truck == nil {
		tr, err := q.WriteTruck(ctx, *n.TruckID)
		if err == nil {
			pl.truck = &tr
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return pl, MapError(err)
		}
	}
	// People: a new driver or helper must be a driver the caller reads and an active one; one the body sends back
	// unchanged is only read for the encoder (a driver deactivated since the assignment does not block the task's other
	// edits). The helper is never the driver (ADR 0011).
	if in.has("driverId") || (create && in.driverID != nil) {
		if in.driverID != nil {
			dr, err := s.personRow(ctx, q, ck, "driverId", *in.driverID, !create && sameID(in.driverID, cur.DriverID))
			if err != nil {
				return pl, err
			}
			pl.driver = dr
		}
		if !sameID(in.driverID, cur.DriverID) {
			pl.newDrv, pl.prevDrv = true, cur.DriverID
			n.DriverID = in.driverID
			n.LegacyDriverRef, n.DriverRefMatch = nil, nil // Go chose the driver: the legacy reference no longer applies
			mark("driverId")
		}
	} else if n.DriverID != nil {
		dr, err := q.WriteDriver(ctx, *n.DriverID)
		if err == nil {
			pl.driver = &dr
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return pl, MapError(err)
		}
	}
	if in.has("helperDriverId") || (create && in.helperDriverID != nil) {
		if in.helperDriverID != nil {
			hr, err := s.personRow(ctx, q, ck, "helperDriverId", *in.helperDriverID, !create && sameID(in.helperDriverID, cur.HelperDriverID))
			if err != nil {
				return pl, err
			}
			pl.helper = hr
		}
		if !sameID(in.helperDriverID, cur.HelperDriverID) {
			n.HelperDriverID = in.helperDriverID
			mark("helperDriverId")
		}
	} else if n.HelperDriverID != nil {
		hr, err := q.WriteDriver(ctx, *n.HelperDriverID)
		if err == nil {
			pl.helper = &hr
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return pl, MapError(err)
		}
	}
	if n.HelperDriverID != nil && n.DriverID != nil && *n.HelperDriverID == *n.DriverID {
		ck.add("helperDriverId", "same_as_driver")
	}
	// Planned stops (multi-drop): a write that names them replaces the plan; stop 1 is the task's destination.
	if in.has("deliveryStops") || (in.isMultiDelivery != nil && !*in.isMultiDelivery && cur.IsMultiDelivery) {
		pl.stopsSet = true
		for _, st := range in.stops {
			r := d.resolve(st.destination)
			hub := st.destinationHubID
			if hub != nil {
				if _, ok := d.hubByID(ck, "deliveryStops.destinationHubId", *hub); !ok {
					hub = nil
				}
			} else {
				hub = r.hubID
			}
			party := st.destinationLinkedPartyID
			if party == nil && hub != nil {
				party = d.byID[*hub].LinkedPartyID
			}
			if party != nil {
				if _, err := s.party(ctx, q, ck, "deliveryStops.destinationLinkedPartyId", *party, pl.parties); err != nil {
					return pl, err
				}
			}
			pl.stops = append(pl.stops, NewStop{Destination: st.destination, DestinationHubID: hub,
				DestinationLinkedPartyID: party, SourceID: st.sourceID, IsCustom: st.isCustom})
		}
		mark("deliveryStops")
	}
	if in.isMultiDelivery != nil {
		n.IsMultiDelivery = *in.isMultiDelivery
	}
	if pl.stopsSet {
		n.IsMultiDelivery = len(pl.stops) > 0
	}
	if n.IsMultiDelivery != cur.IsMultiDelivery {
		mark("isMultiDelivery")
	}
	if create {
		n.Status = TaskPending
		if n.DriverID != nil {
			n.Status = TaskAssigned
		}
		return pl, nil
	}
	return pl, s.planLifecycle(ctx, q, ck, cur, in, &pl)
}

// planLifecycle applies the status and queue rules of a PATCH: a driver change of an open task (pending, assigned, or
// cancelled, which the new driver reopens as the legacy edit did) moves it between pending and assigned and to the
// end of the new driver's queue; a task that is checked in, in transit or completed keeps its driver (409
// task_in_progress). Status and runOrder change otherwise only when the body sets them.
func (s *Service) planLifecycle(ctx context.Context, q *operationsdb.Queries, ck *checker, cur Task, in taskFields, pl *taskPlan) error {
	n := &pl.next
	open := cur.Status == TaskPending || cur.Status == TaskAssigned || cur.Status == TaskCancelled
	if pl.stopsSet && cur.Status != TaskPending && cur.Status != TaskAssigned {
		return errPrecondition(ReasonTaskInProgress, "the planned stops change only while the task is pending or assigned").
			WithDetails(map[string]any{"status": cur.Status})
	}
	if pl.newDrv {
		if !open {
			return errPrecondition(ReasonTaskInProgress, "the driver of a task that is checked in, in transit or completed does not change").
				WithDetails(map[string]any{"status": cur.Status})
		}
		if n.DriverID != nil {
			n.Status = TaskAssigned
			n.CancelledAt, n.CancelReason = nil, nil
		} else if cur.Status == TaskAssigned {
			n.Status = TaskPending
		}
		n.RunOrder = nil
		if n.DriverID != nil && in.runOrder == nil {
			ro, err := s.nextRunOrder(ctx, q, *n.DriverID)
			if err != nil {
				return err
			}
			n.RunOrder = &ro
		}
		if !slices.Contains(pl.changed, "status") && n.Status != cur.Status {
			pl.changed = append(pl.changed, "status")
		}
		pl.changed = append(pl.changed, "runOrder")
	}
	if in.status != nil && *in.status != n.Status {
		switch {
		case *in.status == TaskAssigned && n.DriverID == nil:
			ck.add("status", ReasonStatusNeedDriver)
		case *in.status == TaskPending && n.DriverID != nil:
			ck.add("status", "driver_assigned")
		case !open:
			return errPrecondition(ReasonTaskInProgress, "only a pending, assigned or cancelled task changes its status here").
				WithDetails(map[string]any{"status": cur.Status})
		default:
			n.Status = *in.status
			n.CancelledAt, n.CancelReason = nil, nil
			pl.changed = append(pl.changed, "status")
		}
	}
	if in.runOrder != nil && !sameInt(in.runOrder, n.RunOrder) {
		if n.DriverID == nil {
			ck.add("runOrder", "needs_driver")
		} else {
			if err := q.LockDriverQueue(ctx, *n.DriverID); err != nil {
				return MapError(err)
			}
			n.RunOrder = in.runOrder
			s.holdRunOrder(ctx, *n.DriverID, *in.runOrder)
			if !slices.Contains(pl.changed, "runOrder") {
				pl.changed = append(pl.changed, "runOrder")
			}
		}
	}
	return nil
}

// placeOf resolves a place of a write: the hub id the body names (422 not_found when the master has none), else the
// hub the raw text resolves to; the raw text defaults to the hub's code. It returns the hub's linked party too.
func placeOf(d directory, ck *checker, field string, raw *string, hubID *uuid.UUID, cur string) (string, *uuid.UUID, *uuid.UUID) {
	text := cur
	if raw != nil {
		text = *raw
	}
	if hubID != nil {
		h, ok := d.hubByID(ck, field+"Id", *hubID)
		if !ok {
			return text, nil, nil
		}
		if raw == nil && (cur == "" || d.resolve(cur).hubID == nil || *d.resolve(cur).hubID != h.ID) {
			text = strings.TrimSpace(h.Code)
		}
		id := h.ID
		return text, &id, h.LinkedPartyID
	}
	r := d.resolve(text)
	return text, r.hubID, r.party
}

// personRow reads the driver or helper a task write names. A new or changed value must be a driver the caller can see
// (422 <field> not_found) and active (409 driver_inactive); the value the task already holds (unchanged) is only read
// for the encoder, inactive or not, and a row the caller no longer reads is left out (nil) without a violation, as when
// the body does not name the field.
func (s *Service) personRow(ctx context.Context, q *operationsdb.Queries, ck *checker, field string, id uuid.UUID,
	unchanged bool) (*operationsdb.WriteDriverRow, error) {
	dr, err := q.WriteDriver(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if !unchanged {
			ck.add(field, "not_found")
		}
		return nil, nil
	case err != nil:
		return nil, MapError(err)
	case dr.Status == "inactive" && !unchanged:
		return nil, errPrecondition(ReasonDriverInactive, "the driver is inactive").WithDetails(map[string]any{"field": field})
	}
	return &dr, nil
}

// ReasonNotInTenant is the reason of a 422 for a driver, helper or truck a write links to a row of another tenant.
const ReasonNotInTenant = "not_in_tenant"

// tenantLink is one link of an operations row to a driver, helper or truck, with the tenant of that master row.
type tenantLink struct {
	field  string
	tenant uuid.UUID
	exact  bool // the driver of a task: the row's own tenant only; a helper or a truck: that tenant or a linked one
}

// checkTenantLinks applies the tenant rules of the R13 mover (iam.TaskMover.checkPeople) to the links a write sets on
// a row of tenant (T31 review; Appendix C §C.1.7, §C.9.2 #12): a task's driver belongs to the task's tenant; a helper
// or a truck to that tenant or to a tenant linked to it by contractor_tenant_id (R60); and a master row of another
// tenant must be in the writer's own write reach (writable: its tenant and contractor reach, never the read-only
// dispatcher scope). Readable is wider than writable: a dispatcher reads other carriers' drivers and trucks through
// its parties' scope, and a driver of another carrier joins a call-out only through the audited move (PATCH with
// tenantId). A violation is 422 <field> not_in_tenant. Only new or changed links are passed: a link a row already
// holds (a legacy row the mirror loaded) stays editable.
func (s *Service) checkTenantLinks(ctx context.Context, q *operationsdb.Queries, p *authz.Principal, tenant uuid.UUID,
	links ...tenantLink) error {
	var ck checker
	for _, l := range links {
		if l.tenant == tenant {
			continue
		}
		ok := !l.exact && writable(p, l.tenant)
		if ok {
			linked, err := q.TenantsLinked(ctx, operationsdb.TenantsLinkedParams{A: l.tenant, B: tenant})
			if err != nil {
				return MapError(err)
			}
			ok = linked
		}
		if !ok {
			ck.add(l.field, ReasonNotInTenant)
		}
	}
	return ck.err()
}

// taskLinks are the links of a task plan to check against the task's tenant: on a create every driver, helper and
// truck it names; on an update the ones that change.
func taskLinks(pl taskPlan, cur Task, create bool) []tenantLink {
	n := pl.next
	var out []tenantLink
	if pl.driver != nil && n.DriverID != nil && (create || !sameID(n.DriverID, cur.DriverID)) {
		out = append(out, tenantLink{field: "driverId", tenant: pl.driver.TenantID, exact: true})
	}
	if pl.helper != nil && n.HelperDriverID != nil && (create || !sameID(n.HelperDriverID, cur.HelperDriverID)) {
		out = append(out, tenantLink{field: "helperDriverId", tenant: pl.helper.TenantID})
	}
	if pl.truck != nil && n.TruckID != nil && (create || !sameID(n.TruckID, cur.TruckID)) {
		out = append(out, tenantLink{field: "truckId", tenant: pl.truck.TenantID})
	}
	return out
}

// loadParties caches the parties a row names that the write did not check (a hub-derived link): the write-back names
// them by their Firestore ids. A party the caller cannot read is left out (the encoder then writes no link).
func loadParties(ctx context.Context, q *operationsdb.Queries, seen map[uuid.UUID]operationsdb.WritePartyRow, ids ...*uuid.UUID) error {
	for _, id := range ids {
		if id == nil {
			continue
		}
		if _, ok := seen[*id]; ok {
			continue
		}
		p, err := q.WriteParty(ctx, *id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			continue
		case err != nil:
			return MapError(err)
		}
		seen[*id] = p
	}
	return nil
}

// party reads a billing party the caller can see (422 <field> not_found), cached per write.
func (s *Service) party(ctx context.Context, q *operationsdb.Queries, ck *checker, field string, id uuid.UUID,
	seen map[uuid.UUID]operationsdb.WritePartyRow) (*operationsdb.WritePartyRow, error) {
	if p, ok := seen[id]; ok {
		return &p, nil
	}
	p, err := q.WriteParty(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		ck.add(field, "not_found")
		return nil, nil
	case err != nil:
		return nil, MapError(err)
	}
	seen[id] = p
	return &p, nil
}

func sameID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameInt(a, b *int32) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// --- POST /v1/tasks -------------------------------------------------------------------------------------------------

// CreateTaskAPI is POST /v1/tasks (operations:manage_tasks): 201 with the task as GET /v1/tasks/{id} reads it.
//
// PostgreSQL path: in one request transaction, the tenant is stamped (Stamp: the driver's tenant, else the caller's
// active tenant with tenant_source 'form', R13: a dispatcher's driverless call-out lands in its organisation's
// tenant), the number is allocated by next_task_seq() inside the insert transaction (R10: no count()+1), an assigned
// task takes the end of its driver's queue under the driver's lock, and the outbox gets task.created and, with a
// driver, task.assigned (the one visible push of notify.fcm).
//
// Write-back path: the number is allocated in a transaction of its own (a failed Firestore write leaves a gap, never a
// duplicate), the document is created under a minted uuidv7 id (R25) and the mirror loads the row under that id.
func (s *Service) CreateTaskAPI(ctx context.Context, p *authz.Principal, rid string, body []byte) (WriteResult, error) {
	if err := requireCap(p, authz.OperationsManageTasks); err != nil {
		return WriteResult{}, err
	}
	mode, err := s.mode()
	if err != nil {
		return WriteResult{}, err
	}
	var ck checker
	f, err := parseFields(body, &ck)
	if err != nil {
		return WriteResult{}, err
	}
	in := parseTaskFields(f, true)
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	d, err := s.directory(ctx, p)
	if err != nil {
		return WriteResult{}, err
	}
	if mode == modeWriteBack {
		return s.createTaskWriteBack(ctx, p, rid, d, in)
	}
	var id uuid.UUID
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		task, err := s.insertTask(ctx, tx, p, rid, d, in)
		id = task.ID
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	view, err := s.writtenTaskView(ctx, p, id)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Status: http.StatusCreated, Data: view}, nil
}

// insertTask is the PostgreSQL path of one task creation in the caller's transaction (POST /v1/tasks and each row of
// the import): plan, stamp, CreateTask, events.
func (s *Service) insertTask(ctx context.Context, tx pgx.Tx, p *authz.Principal, rid string, d directory, in taskFields) (Task, error) {
	q := operationsdb.New(tx)
	var ck checker
	pl, err := s.planTask(ctx, q, d, &ck, Task{}, in, true)
	if err != nil {
		return Task{}, err
	}
	if err := ck.err(); err != nil {
		return Task{}, err
	}
	res, err := Stamp(ctx, tx, p, TableTasks, Links{DriverID: pl.next.DriverID})
	if err != nil {
		return Task{}, err
	}
	tenant, _ := uuid.Parse(res.TenantID)
	if err := s.checkTenantLinks(ctx, q, p, tenant, taskLinks(pl, Task{}, true)...); err != nil {
		return Task{}, err
	}
	n := pl.next
	var by *uuid.UUID
	if !p.IsMachine() {
		u := p.UserID
		by = &u
	}
	task, _, err := CreateTask(ctx, tx, NewTask{TaskInsert: TaskInsert{TenantID: tenant, TenantSource: string(res.Source),
		TaskNo: in.taskNo, TaskType: n.TaskType, JobCategory: n.JobCategory, Status: n.Status, PlanAt: n.PlanAt,
		PlanTime: n.PlanTime, ActualPickupAt: n.ActualPickupAt, SourceHubRaw: n.SourceHubRaw, SourceHubID: n.SourceHubID,
		SourceLinkedPartyID: n.SourceLinkedPartyID, DestinationRaw: n.DestinationRaw, DestinationHubID: n.DestinationHubID,
		DestinationSocKey: n.DestinationSocKey, DestinationLinkedPartyID: n.DestinationLinkedPartyID,
		BillingPartyID: n.BillingPartyID, TruckID: n.TruckID, TruckType: n.TruckType, LegacyTruckType: n.LegacyTruckType,
		LicensePlateSnapshot: n.LicensePlateSnapshot, DriverID: n.DriverID, HelperDriverID: n.HelperDriverID,
		IsMultiDelivery: n.IsMultiDelivery, CreatedBy: by}, Stops: pl.stops})
	if err != nil {
		return Task{}, err
	}
	topics, err := taskTopics(ctx, q, task)
	if err != nil {
		return Task{}, err
	}
	evs := []opsEvent{withTopics(taskEvent(RouteTaskCreated, task, nil), topics)}
	if task.DriverID != nil {
		ev := withTopics(taskEvent(RouteTaskAssigned, task, []string{"driverId", "status"}), topics)
		ev.payload["driverId"], ev.payload["previousDriverId"] = task.DriverID, nil
		evs = append(evs, ev)
	}
	return task, appendEvents(ctx, tx, rid, "", evs...)
}

// Task routing keys (Appendix B §B.5.2).
const (
	RouteTaskCreated         = "task.created"
	RouteTaskAssigned        = "task.assigned"
	RouteTaskReassigned      = "task.reassigned"
	RouteTaskUpdated         = "task.updated"
	RouteTaskCancelled       = "task.cancelled"
	RouteTaskPlanDateChanged = "task.plan_date_changed"
)

func withTopics(e opsEvent, topics []string) opsEvent {
	e.topics = topics
	return e
}

// --- PATCH /v1/tasks/{id} -------------------------------------------------------------------------------------------

// PatchTaskAPI is PATCH /v1/tasks/{id}. A body whose tenantId differs from the task's tenant is the R13 reassignment of
// T28 (ReassignTask: a dispatcher's call-out or a platform admin; the body then carries tenantId and driverId only).
// Every other body is the ordinary update (operations:manage_tasks) under the caller's RLS: a task the caller cannot
// update is 404 (a dispatcher on another carrier's task changes 0 rows, Appendix C §C.9.2 #12), a platform admin needs
// X-Act-On-Tenant (403 act_on_tenant_required). Only the named fields change; jobCategory absent keeps it; status and
// run_order change only with the driver or when the body sets them. The answer is the task as GET /v1/tasks/{id}
// reads it (200), or the reassignment's answer.
func (s *Service) PatchTaskAPI(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, body []byte) (WriteResult, error) {
	var ck checker
	f, err := parseFields(body, &ck)
	if err != nil {
		return WriteResult{}, err
	}
	if f.has("tenantId") && !f.isNull("tenantId") {
		target := f.id("tenantId")
		if target == nil {
			return WriteResult{}, ck.err()
		}
		var cur uuid.UUID
		var found bool
		if err := s.Tx(ctx, p, func(tx pgx.Tx) error {
			var err error
			cur, found, err = tenancy.TenantOf(ctx, tx, TableTasks, id)
			return err
		}); err != nil {
			return WriteResult{}, err
		}
		if !found || *target != cur {
			// A move (or a task the caller does not read: ReassignTask authorizes the caller first, then its mover
			// answers 404 for a task outside the caller's sight).
			in, err := ParseReassign(body)
			if err != nil {
				return WriteResult{}, err
			}
			out, err := s.ReassignTask(ctx, p, rid, id, in)
			if err != nil {
				if p.IsPlatform() && p.ActOnTenant == nil && sameTenantRefusal(err) {
					// Not a move: the ordinary update of a task in its own tenant runs under that tenant's RLS, which a
					// platform admin reaches through X-Act-On-Tenant (audited, Appendix C §C.3.9).
					return WriteResult{}, authz.ErrPermissionDenied("a platform admin changes a task in its tenant through X-Act-On-Tenant",
						authz.OperationsManageTasks).WithDetails(map[string]any{"reason": ReasonActOnTenantRequired})
				}
				return WriteResult{}, err
			}
			return WriteResult{Status: http.StatusOK, Data: out}, nil
		}
		delete(f.m, "tenantId") // the task's own tenant: an ordinary update
	}
	if err := requireCap(p, authz.OperationsManageTasks); err != nil {
		return WriteResult{}, err
	}
	mode, err := s.mode()
	if err != nil {
		return WriteResult{}, err
	}
	in := parseTaskFields(f, false)
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	d, err := s.directory(ctx, p)
	if err != nil {
		return WriteResult{}, err
	}
	if mode == modeWriteBack {
		return s.patchTaskWriteBack(ctx, p, rid, d, id, in)
	}
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		cur, err := q.LockTask(ctx, id)
		if err != nil {
			return MapError(notFound(err))
		}
		var ck checker
		pl, err := s.planTask(ctx, q, d, &ck, cur, in, false)
		if err != nil {
			return err
		}
		if err := ck.err(); err != nil {
			return err
		}
		if len(pl.changed) == 0 {
			return nil // nothing changes: no write, no event
		}
		if err := s.checkTenantLinks(ctx, q, p, cur.TenantID, taskLinks(pl, cur, false)...); err != nil {
			return err
		}
		if err := planMoveLock(ctx, tx, q, cur, pl); err != nil {
			return err
		}
		if err := helperPayrollLock(ctx, q, cur, pl); err != nil {
			return err
		}
		next, err := s.updateTask(ctx, q, pl)
		if err != nil {
			return err
		}
		return appendTaskChange(ctx, q, tx, rid, "", cur, next, pl)
	})
	if err != nil {
		return WriteResult{}, err
	}
	view, err := s.writtenTaskView(ctx, p, id)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Status: http.StatusOK, Data: view}, nil
}

// planMoveLock reads, in the update's transaction, the period lock a move of the task's plan day reaches (main spec
// §6.10: "lock in the same tx", as PATCH /v1/trips/{id} deliveredAt): for each delivered (or billing-stamped) trip of
// the task billed on the plan axis, the period of its billing instant and of the new plan instant; a sent or paid
// statement there is 409 billing_period_locked with blockedInvoiceNumber, and nothing is written. A delivered-axis
// party's billing does not follow the plan day, so its trips are not read.
func planMoveLock(ctx context.Context, tx pgx.Tx, q *operationsdb.Queries, cur Task, pl taskPlan) error {
	if !pl.planMove {
		return nil
	}
	trips, err := q.TaskPlanBilledTrips(ctx, cur.ID)
	if err != nil {
		return MapError(err)
	}
	for _, r := range trips {
		old := cur.PlanAt
		if r.BillingDate != nil {
			old = *r.BillingDate
		}
		l, locked, err := billing.PeriodLock(ctx, tx, r.PartyID, old, pl.next.PlanAt)
		if err != nil {
			return MapError(err)
		}
		if locked {
			return billing.ErrPeriodLocked(l)
		}
	}
	return nil
}

// CodePayrollLocked is the 409 of a task change that moves a helper day of an approved or paid payroll round (Appendix
// B §B.1.5).
const CodePayrollLocked = "payroll_locked"

// payRound is a pay period (Bangkok YYYY-MM) and round (R1 = days 1-15, R2 = 16 to the month's end) of a plan instant:
// the round whose helper pay counts the task (hr ListHelperTasks reads tasks by plan_at; ADR 0011, 0013).
type payRound struct{ period, round string }

func payRoundOf(at time.Time) payRound {
	return payRound{period: at.In(clock.Bangkok).Format("2006-01"), round: string(hrcompute.AssignRound(at))}
}

// helperPayrollLock is the payroll lock of a task update (Appendix B §B.2.11 "helper change checks payroll lock", the
// legacy EditTripDetailsDialog check, now server-side): a change of the helper moves the day from the old helper's
// round to the new helper's, and a move of the plan day of a task with a helper moves it between that helper's rounds;
// when the payroll run of an affected helper for that round is approved or paid (approved line items are frozen, ADR
// 0013) the update is 409 payroll_locked with details {driverId, status, period, round} and nothing is written. The
// runs are read through task_helper_payroll_locks() (0020), so an operator without HR capabilities and a carrier
// helper's run in another tenant are checked alike.
func helperPayrollLock(ctx context.Context, q *operationsdb.Queries, cur Task, pl taskPlan) error {
	n := pl.next
	affected := map[payRound][]uuid.UUID{}
	add := func(driver *uuid.UUID, at time.Time) {
		if driver == nil {
			return
		}
		k := payRoundOf(at)
		if !slices.Contains(affected[k], *driver) {
			affected[k] = append(affected[k], *driver)
		}
	}
	switch {
	case !sameID(n.HelperDriverID, cur.HelperDriverID):
		add(cur.HelperDriverID, cur.PlanAt)
		add(n.HelperDriverID, n.PlanAt)
	case n.HelperDriverID != nil && payRoundOf(n.PlanAt) != payRoundOf(cur.PlanAt):
		add(n.HelperDriverID, cur.PlanAt)
		add(n.HelperDriverID, n.PlanAt)
	}
	keys := make([]payRound, 0, len(affected))
	for k := range affected {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b payRound) int { return strings.Compare(a.period+a.round, b.period+b.round) })
	for _, k := range keys {
		rows, err := q.HelperPayrollLocks(ctx, operationsdb.HelperPayrollLocksParams{DriverIds: affected[k],
			PayPeriod: k.period, PayRound: k.round})
		if err != nil {
			return MapError(err)
		}
		if len(rows) > 0 {
			return httpx.NewError(http.StatusConflict, CodePayrollLocked,
				"the helper's payroll for this period is already "+rows[0].Status+"; correct it with an adjustment").
				WithDetails(map[string]any{"field": "helperDriverId", "driverId": rows[0].DriverID, "status": rows[0].Status,
					"period": k.period, "round": k.round})
		}
	}
	return nil
}

// sameTenantRefusal reports the mover's 422 tenantId same_tenant (a driver change without a move).
func sameTenantRefusal(err error) bool {
	var he *httpx.Error
	if !errors.As(err, &he) || he.Code != httpx.CodeInvalidArgument {
		return false
	}
	fs, _ := he.Details["fields"].([]httpx.FieldViolation)
	return len(fs) == 1 && fs[0].Field == "tenantId" && fs[0].Reason == "same_tenant"
}

// updateTask writes the merged row and, when the plan names them, replaces the planned stops.
func (s *Service) updateTask(ctx context.Context, q *operationsdb.Queries, pl taskPlan) (Task, error) {
	n := pl.next
	if pl.stopsSet {
		if _, err := q.DeleteTaskStops(ctx, n.ID); err != nil {
			return Task{}, MapError(err)
		}
		if len(pl.stops) > 0 && len(pl.stops) < 2 {
			return Task{}, httpx.ErrInvalidArgument(violation("deliveryStops", "too_few"))
		}
		probe := NewTask{TaskInsert: TaskInsert{TaskType: n.TaskType, Status: n.Status, TenantSource: n.TenantSource,
			TenantID: n.TenantID, PlanAt: n.PlanAt, SourceHubRaw: n.SourceHubRaw, DestinationRaw: n.DestinationRaw},
			Stops: pl.stops}
		if err := probe.validate(); err != nil {
			return Task{}, err
		}
		for i, st := range pl.stops {
			if _, err := q.InsertTaskStop(ctx, operationsdb.InsertTaskStopParams{TaskID: n.ID, StopIndex: int32(i + 1),
				DestinationRaw: strings.TrimSpace(st.Destination), DestinationHubID: st.DestinationHubID,
				DestinationLinkedPartyID: st.DestinationLinkedPartyID, Sequence: ptr(int32(i)), SourceID: st.SourceID,
				IsCustom: st.IsCustom}); err != nil {
				return Task{}, MapError(err)
			}
		}
	}
	t, err := q.UpdateTask(ctx, operationsdb.UpdateTaskParams{JobCategory: n.JobCategory, Status: n.Status, PlanAt: n.PlanAt,
		PlanTime: n.PlanTime, ActualPickupAt: n.ActualPickupAt, SourceHubRaw: n.SourceHubRaw, SourceHubID: n.SourceHubID,
		SourceLinkedPartyID: n.SourceLinkedPartyID, DestinationRaw: n.DestinationRaw, DestinationHubID: n.DestinationHubID,
		DestinationSocKey: n.DestinationSocKey, DestinationLinkedPartyID: n.DestinationLinkedPartyID,
		BillingPartyID: n.BillingPartyID, TruckID: n.TruckID, TruckType: n.TruckType, LegacyTruckType: n.LegacyTruckType,
		LicensePlateSnapshot: n.LicensePlateSnapshot, DriverID: n.DriverID, LegacyDriverRef: n.LegacyDriverRef,
		DriverRefMatch: n.DriverRefMatch, HelperDriverID: n.HelperDriverID, RunOrder: n.RunOrder,
		IsMultiDelivery: n.IsMultiDelivery, CancelledAt: n.CancelledAt, CancelReason: n.CancelReason, ID: n.ID})
	return t, MapError(err)
}

// taskChangeEvents are the events of a task update (the rules of the mirror, internal/etl/mirror_events.go): a driver
// set on a task without one is task.assigned, a driver changed or removed task.reassigned (notify.fcm tells the
// previous driver it lost the task and the new one it got it), anything else task.updated (the silent tasks_changed
// push); a moved Bangkok plan day adds task.plan_date_changed (billing restamps under the period lock).
func taskChangeEvents(cur, next Task, pl taskPlan) []opsEvent {
	var evs []opsEvent
	switch {
	case pl.newDrv && cur.DriverID == nil:
		ev := taskEvent(RouteTaskAssigned, next, pl.changed)
		ev.payload["driverId"], ev.payload["previousDriverId"] = next.DriverID, nil
		evs = append(evs, ev)
	case pl.newDrv:
		ev := taskEvent(RouteTaskReassigned, next, pl.changed)
		ev.payload["driverId"], ev.payload["previousDriverId"] = next.DriverID, cur.DriverID
		evs = append(evs, ev)
	case slices.ContainsFunc(pl.changed, func(k string) bool { return k != "planDate" }):
		evs = append(evs, taskEvent(RouteTaskUpdated, next, pl.changed))
	}
	if pl.planMove {
		evs = append(evs, taskEvent(RouteTaskPlanDateChanged, next, []string{"planDate"}))
	}
	return evs
}

// appendTaskChange appends the events of a task update with the task's topics (origin "") or as the write-back's
// notify events (origin writeback: no topics).
func appendTaskChange(ctx context.Context, q *operationsdb.Queries, tx pgx.Tx, rid, origin string, cur, next Task, pl taskPlan) error {
	evs := taskChangeEvents(cur, next, pl)
	if origin == "" {
		topics, err := taskTopics(ctx, q, next, cur.DriverID)
		if err != nil {
			return err
		}
		for i := range evs {
			evs[i].topics = topics
		}
	}
	return appendEvents(ctx, tx, rid, origin, evs...)
}

// --- POST /v1/tasks/{id}/cancel -------------------------------------------------------------------------------------

// CancelTaskAPI is POST /v1/tasks/{id}/cancel {reason?} (operations:manage_tasks): the task becomes cancelled with
// cancelled_at and the reason, and the outbox gets task.cancelled (the visible {type}_task_cancelled push to its
// driver). A cancelled task is answered as it is (no event); a completed task is 409 task_closed.
func (s *Service) CancelTaskAPI(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, body []byte) (WriteResult, error) {
	if err := requireCap(p, authz.OperationsManageTasks); err != nil {
		return WriteResult{}, err
	}
	mode, err := s.mode()
	if err != nil {
		return WriteResult{}, err
	}
	var ck checker
	f, err := parseFields(body, &ck)
	if err != nil {
		return WriteResult{}, err
	}
	f.only("reason")
	reason := f.optText("reason")
	if reason != nil && len([]rune(*reason)) > 500 {
		ck.add("reason", "too_long")
	}
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	if mode == modeWriteBack {
		return s.cancelTaskWriteBack(ctx, p, rid, id, reason)
	}
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		cur, err := q.LockTask(ctx, id)
		if err != nil {
			return MapError(notFound(err))
		}
		switch cur.Status {
		case TaskCancelled:
			return nil
		case TaskCompleted:
			return errPrecondition(ReasonTaskClosed, "a completed task is not cancelled").WithDetails(map[string]any{"status": cur.Status})
		}
		next, err := q.CancelTask(ctx, operationsdb.CancelTaskParams{ID: id, CancelledAt: s.now().UTC(), Reason: reason})
		if err != nil {
			return MapError(err)
		}
		topics, err := taskTopics(ctx, q, next)
		if err != nil {
			return err
		}
		return appendEvents(ctx, tx, rid, "", withTopics(taskEvent(RouteTaskCancelled, next, []string{"status"}), topics))
	})
	if err != nil {
		return WriteResult{}, err
	}
	view, err := s.writtenTaskView(ctx, p, id)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Status: http.StatusOK, Data: view}, nil
}

// --- POST /v1/tasks/{id}/stops --------------------------------------------------------------------------------------

// AddTaskStopAPI is POST /v1/tasks/{id}/stops {destination, destinationHubId?, destinationLinkedPartyId?, sourceId?,
// isCustom?} (operations:manage_tasks; the staff use of addDeliveryStop, multiDeliveryTrips.ts:341): stop 1 is
// bootstrapped from the task's destination, a destination the task has is 409 duplicate_destination, the task becomes
// multi-drop. The answer is {stops: [...]} (every stop, stop 1 first); the outbox gets task.updated.
func (s *Service) AddTaskStopAPI(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, body []byte) (WriteResult, error) {
	if err := requireCap(p, authz.OperationsManageTasks); err != nil {
		return WriteResult{}, err
	}
	if _, err := s.mode(); err != nil {
		return WriteResult{}, err
	}
	in, err := parseStop(body, stopFields)
	if err != nil {
		return WriteResult{}, err
	}
	out, err := s.addTaskStop(ctx, p, rid, id, in, waitForMirror)
	if err != nil {
		return WriteResult{}, err
	}
	return out.staffResult(), nil
}

// parseStop reads the body of a stops route, refusing every field outside allowed (422 not_supported).
func parseStop(body []byte, allowed []string) (stopInput, error) {
	var ck checker
	f, err := parseFields(body, &ck)
	if err != nil {
		return stopInput{}, err
	}
	f.only(allowed...)
	in := stopInput{destination: f.required("destination"), destinationHubID: f.id("destinationHubId"),
		destinationLinkedPartyID: f.id("destinationLinkedPartyId"), sourceID: f.optText("sourceId")}
	if b := f.boolean("isCustom"); b != nil {
		in.isCustom = *b
	}
	return in, ck.err()
}

// stopAdded is the outcome of one added stop: the task's stops after it (rows of the caller's view), or, for a
// write-back the mirror has not acknowledged yet, its 202 echo; wasMulti is whether the task was multi-drop before the
// stop (the isMultiDelivery flag addDeliveryStop answered by, multiDeliveryTrips.ts:432).
type stopAdded struct {
	taskID   uuid.UUID
	stops    []TaskStopRow
	echo     *WriteResult
	wasMulti bool
}

// staffResult is the answer of POST /v1/tasks/{id}/stops: 200 {taskId, stops}, or the 202 echo of the write-back.
func (o stopAdded) staffResult() WriteResult {
	if o.echo != nil {
		return *o.echo
	}
	return WriteResult{Status: http.StatusOK, Data: map[string]any{"taskId": o.taskID, "stops": o.stops}}
}

// stopRowsVisible reports whether a stop row carries its linked billing party: staff only, never a scope principal
// nor a driver (drivers never see billing, Appendix C §C.3.7).
func stopRowsVisible(p *authz.Principal) bool { return !scoped(p) && !driverPrincipal(p) }

// mirrorWait is whether a write-back waits for the mirror's acknowledgement before it answers (main spec §11.10,
// §13.7: up to WriteBack.Wait, then 202 with the document echoed).
type mirrorWait bool

const (
	// waitForMirror reads the written rows back once the mirror applied them (read-your-writes): the staff routes and
	// the APK 4.x routes.
	waitForMirror mirrorWait = true
	// answerOnCommit answers 202 right after the Firestore commit: the callable shim targets (T32R), whose callers
	// read the Firestore document, never the PostgreSQL rows, so the wait would only add the mirror's lag (seconds)
	// to every legacy call.
	answerOnCommit mirrorWait = false
)

// addTaskStop adds one stop to task id as p, in PostgreSQL under p's RLS (the task locked FOR UPDATE: staff reach, or
// the task's driver or helper through tasks.p_driver_update) or, while operations is Firestore-owned, to the task
// document first (addStopWriteBack, waiting for the mirror or not as wait says).
func (s *Service) addTaskStop(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, in stopInput, wait mirrorWait) (stopAdded, error) {
	mode, err := s.mode()
	if err != nil {
		return stopAdded{}, err
	}
	d, err := s.directory(ctx, p)
	if err != nil {
		return stopAdded{}, err
	}
	if mode == modeWriteBack {
		return s.addStopWriteBack(ctx, p, rid, d, id, in, wait)
	}
	var stops []TaskStop
	var wasMulti bool
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		before, err := q.LockTask(ctx, id)
		if err != nil {
			return MapError(notFound(err))
		}
		wasMulti = before.IsMultiDelivery
		st, err := s.stopOf(ctx, q, d, in)
		if err != nil {
			return err
		}
		if stops, err = AddTaskStop(ctx, tx, id, st, s.now().UTC()); err != nil {
			return err
		}
		task, err := q.LockTask(ctx, id)
		if err != nil {
			return MapError(err)
		}
		topics, err := taskTopics(ctx, q, task)
		if err != nil {
			return err
		}
		return appendEvents(ctx, tx, rid, "", withTopics(taskEvent(RouteTaskUpdated, task, []string{"deliveryStops"}), topics))
	})
	if err != nil {
		return stopAdded{}, err
	}
	out := stopAdded{taskID: id, stops: make([]TaskStopRow, 0, len(stops)), wasMulti: wasMulti}
	for _, st := range stops {
		out.stops = append(out.stops, d.taskStop(st, stopRowsVisible(p)))
	}
	return out, nil
}

// stopOf resolves one stop of the stops route: the hub id given (422 not_found) or the hub of the text, the hub's
// linked party when the body names none (422 not_found for an unreadable party).
func (s *Service) stopOf(ctx context.Context, q *operationsdb.Queries, d directory, in stopInput) (NewStop, error) {
	var ck checker
	hub := in.destinationHubID
	if hub != nil {
		if _, ok := d.hubByID(&ck, "destinationHubId", *hub); !ok {
			hub = nil
		}
	} else {
		hub = d.resolve(in.destination).hubID
	}
	party := in.destinationLinkedPartyID
	if party == nil && hub != nil {
		party = d.byID[*hub].LinkedPartyID
	}
	if party != nil {
		if _, err := s.party(ctx, q, &ck, "destinationLinkedPartyId", *party, map[uuid.UUID]operationsdb.WritePartyRow{}); err != nil {
			return NewStop{}, err
		}
	}
	if err := ck.err(); err != nil {
		return NewStop{}, err
	}
	return NewStop{Destination: in.destination, DestinationHubID: hub, DestinationLinkedPartyID: party,
		SourceID: in.sourceID, IsCustom: in.isCustom}, nil
}
