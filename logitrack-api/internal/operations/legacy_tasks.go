package operations

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The write-back encoders and flows of the task writes (main spec §13.8 "Encoding"): the legacy shape the 3.x app and
// the mirror's loader (internal/etl loadTask) read. Status literals per the table of §13.8; date = plan_at,
// dateStr = Bangkok ddMMyyyy; tasks.driverId = drivers.legacy_doc_id; helperDriverIds = [the helper's Firebase uid];
// truckId = trucks.legacy_doc_id; the party links = the customers (or subcontractors) document id with its code and
// name. A master row born in PostgreSQL carries its Firestore id once the projection minted it (legacy_doc_id =
// id::text, firestore.shadow, T24S); until then the write is refused (409 failed_precondition not_projected) rather
// than written with a reference the mirror could not resolve.

// ReasonNotProjected is details.reason of the 409 of a write-back that names a master row without a Firestore id.
const ReasonNotProjected = "not_projected"

// legacyTaskStatus is the Firestore literal of a task status (§13.8).
var legacyTaskStatus = map[string]string{TaskPending: "Pending", TaskAssigned: "Assigned", TaskCheckedIn: "Checked in",
	TaskInTransit: "In-Transit", TaskCompleted: "Completed", TaskCancelled: "Cancelled"}

// legacyTaskType is the Firestore literal of a task type.
var legacyTaskType = map[string]string{FirstMile: "FIRST_MILE", LineHaul: "LINE_HAUL"}

func notProjected(field string) error {
	return errPrecondition(ReasonNotProjected, "a row the write names has no Firestore document yet; retry once the projection ran").
		WithDetails(map[string]any{"field": field})
}

// legacyDateStr is the Bangkok ddMMyyyy of a plan instant (useFirstMileTask.ts: format(date, "ddMMyyyy")).
func legacyDateStr(t time.Time) string { return t.In(clock.Bangkok).Format("02012006") }

// partyDoc is the Firestore side of a billing party: the customers or subcontractors document id, code and name.
func partyDoc(p operationsdb.WritePartyRow) (id, code, name, kind string) {
	switch {
	case p.CustomerDocID != nil:
		id, kind = *p.CustomerDocID, "customer"
		if p.CustomerCode != nil {
			code = *p.CustomerCode
		}
		if p.CustomerName != nil {
			name = *p.CustomerName
		}
	case p.TenantDocID != nil:
		id, kind = *p.TenantDocID, "partner"
		if p.TenantCode != nil {
			code = *p.TenantCode
		}
		if p.TenantName != nil {
			name = *p.TenantName
		}
	}
	return id, code, name, kind
}

// taskEncoder writes the legacy fields of a task plan.
type taskEncoder struct {
	pl  taskPlan
	out map[string]any
	err error
}

func (e *taskEncoder) set(k string, v any) { e.out[k] = v }

// party writes the four fields of a party link (prefix + Id / Name / Code, and the link kind when kindField is set);
// nil removes them.
func (e *taskEncoder) party(field, idKey, nameKey, codeKey, kindKey string, id *uuid.UUID) {
	if id == nil {
		e.set(idKey, nil)
		e.set(nameKey, nil)
		e.set(codeKey, nil)
		if kindKey != "" {
			e.set(kindKey, nil)
		}
		return
	}
	p, ok := e.pl.parties[*id]
	docID, code, name, kind := partyDoc(p)
	if !ok || docID == "" {
		if e.err == nil {
			e.err = notProjected(field)
		}
		return
	}
	e.set(idKey, docID)
	e.set(nameKey, name)
	e.set(codeKey, code)
	if kindKey != "" {
		e.set(kindKey, kind)
	}
}

// encodeTask is the legacy document of a task plan: every field on a create (all), else the fields of the changed
// keys (a PATCH writes only what it changed, so fields the 3.x app writes on the same document keep their values).
func encodeTask(pl taskPlan, all bool, now time.Time) (map[string]any, error) {
	e := &taskEncoder{pl: pl, out: map[string]any{}}
	n := pl.next
	changed := func(k string) bool {
		if all {
			return true
		}
		for _, c := range pl.changed {
			if c == k {
				return true
			}
		}
		return false
	}
	if all {
		e.set("taskType", legacyTaskType[n.TaskType])
		if n.TaskNo != nil {
			e.set("taskId", *n.TaskNo)
		}
		e.set("createdAt", now)
	}
	if changed("status") || all {
		e.set("status", legacyTaskStatus[n.Status])
	}
	if changed("planAt") {
		e.set("date", n.PlanAt)
		e.set("dateStr", legacyDateStr(n.PlanAt))
	}
	if changed("planTime") {
		e.set("time", optStr(n.PlanTime))
	}
	if changed("actualPickupAt") {
		e.set("actualPickupAt", optTime(n.ActualPickupAt))
	}
	if changed("jobCategory") {
		e.set("jobCategory", optStr(n.JobCategory))
	}
	if changed("sourceHub") {
		e.set("sourceHub", n.SourceHubRaw)
	}
	if changed("destination") {
		e.set("destination", n.DestinationRaw)
	}
	if changed("billingPartyId") {
		e.party("billingPartyId", "billingCustomerId", "billingCustomerName", "billingCustomerCode", "", n.BillingPartyID)
	}
	if changed("sourceLinkedPartyId") || changed("sourceHub") {
		e.party("sourceLinkedPartyId", "sourceHubLinkedCustomerId", "sourceHubLinkedCustomerName",
			"sourceHubLinkedCustomerCode", "sourceHubCustomerLinkKind", n.SourceLinkedPartyID)
	}
	if changed("destinationLinkedPartyId") || changed("destination") {
		e.party("destinationLinkedPartyId", "destinationLinkedCustomerId", "destinationLinkedCustomerName",
			"destinationLinkedCustomerCode", "destinationCustomerLinkKind", n.DestinationLinkedPartyID)
	}
	if changed("truckId") || changed("truckType") {
		switch {
		case n.TruckID == nil:
			e.set("truckId", nil)
		case pl.truck == nil || pl.truck.LegacyDocID == nil:
			e.err = errors.Join(e.err, notProjected("truckId"))
		default:
			e.set("truckId", *pl.truck.LegacyDocID)
		}
		tt := optStr(n.TruckType)
		if tt == nil {
			tt = optStr(n.LegacyTruckType)
		}
		e.set("truckType", tt)
		e.set("licensePlate", optStr(n.LicensePlateSnapshot))
	}
	if changed("driverId") {
		switch {
		case n.DriverID == nil:
			e.set("driverId", nil)
			e.set("driverName", nil)
			e.set("driverPhone", nil)
		case pl.driver == nil || pl.driver.LegacyDocID == nil:
			e.err = errors.Join(e.err, notProjected("driverId"))
		default:
			e.set("driverId", *pl.driver.LegacyDocID)
			e.set("driverName", optStr(driverName(pl.driver.FullNameTh, &pl.driver.FirstName, &pl.driver.LastName)))
			e.set("driverPhone", strings.TrimSpace(pl.driver.Mobile))
		}
	}
	if changed("helperDriverId") {
		switch {
		case n.HelperDriverID == nil:
			e.set("helperDriverIds", []any{})
		case pl.helper == nil || authUID(pl.helper) == "":
			e.err = errors.Join(e.err, notProjected("helperDriverId"))
		default:
			e.set("helperDriverIds", []any{authUID(pl.helper)}) // the 3.x app matches its helper tasks by its uid
		}
	}
	if changed("runOrder") || (all && n.RunOrder != nil) {
		if n.RunOrder == nil {
			e.set("runOrder", nil)
		} else {
			e.set("runOrder", int64(*n.RunOrder))
		}
	}
	if changed("isMultiDelivery") || changed("deliveryStops") {
		e.set("isMultiDelivery", n.IsMultiDelivery)
	}
	if changed("deliveryStops") {
		stops := make([]any, 0, len(pl.stops))
		for i, st := range pl.stops {
			m := map[string]any{"index": int64(i + 1), "destination": normalizeDestination(st.Destination),
				"sequence": int64(i), "status": "pending"}
			if st.SourceID != nil {
				m["sourceId"] = *st.SourceID
			}
			if st.IsCustom {
				m["isCustom"] = true
			}
			if st.DestinationLinkedPartyID != nil {
				p := pl.parties[*st.DestinationLinkedPartyID]
				if id, code, name, kind := partyDoc(p); id != "" {
					m["destinationLinkedCustomerId"], m["destinationLinkedCustomerName"] = id, name
					m["destinationLinkedCustomerCode"], m["destinationCustomerLinkKind"] = code, kind
				} else if e.err == nil {
					e.err = notProjected("deliveryStops.destinationLinkedPartyId")
				}
			}
			stops = append(stops, m)
		}
		if len(stops) == 0 {
			e.set("deliveryStops", nil)
		} else {
			e.set("deliveryStops", stops)
		}
	}
	if changed("cancelled") {
		e.set("cancelledAt", optTime(n.CancelledAt))
		e.set("cancelReason", optStr(n.CancelReason))
	}
	e.set("updatedAt", now)
	return e.out, e.err
}

// authUID is the Firebase uid of a driver's login (Q1): the driver's own legacy uid, else its linked user's.
func authUID(d *operationsdb.WriteDriverRow) string {
	if d.DriverAuthUid != nil && *d.DriverAuthUid != "" {
		return *d.DriverAuthUid
	}
	if d.UserAuthUid != nil {
		return *d.UserAuthUid
	}
	return ""
}

// driverRef is the Firestore id of a driver on trip, standby and incident documents: the Firebase uid (Q1), else the
// driver document id (the loader resolves either).
func driverRef(d *operationsdb.WriteDriverRow) string {
	if uid := authUID(d); uid != "" {
		return uid
	}
	if d.LegacyDocID != nil {
		return *d.LegacyDocID
	}
	return ""
}

func optStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func optTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

// --- flows ----------------------------------------------------------------------------------------------------------

// createTaskWriteBack is POST /v1/tasks while operations is Firestore-owned: the plan is checked and stamped under the
// caller's RLS (Stamp: the caller may write in the tenant the row will land in; the mirror stamps the row itself), the
// number is allocated (next_task_seq() in a transaction of its own) and the queue position computed, then the task
// document is created under a minted uuidv7 (R25) and the writer's task.assigned is queued for notify.fcm.
func (s *Service) createTaskWriteBack(ctx context.Context, p *authz.Principal, rid string, d directory, in taskFields) (WriteResult, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return WriteResult{}, err
	}
	var doc map[string]any
	var pl taskPlan
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		var ck checker
		var err error
		if pl, err = s.planTask(ctx, q, d, &ck, Task{}, in, true); err != nil {
			return err
		}
		if err := ck.err(); err != nil {
			return err
		}
		probe := NewTask{TaskInsert: TaskInsert{TaskType: pl.next.TaskType, Status: pl.next.Status, TenantSource: "form",
			TenantID: uuid.Max, PlanAt: pl.next.PlanAt, SourceHubRaw: pl.next.SourceHubRaw, DestinationRaw: pl.next.DestinationRaw,
			JobCategory: pl.next.JobCategory, TruckType: pl.next.TruckType, HelperDriverID: pl.next.HelperDriverID,
			DriverID: pl.next.DriverID, IsMultiDelivery: pl.next.IsMultiDelivery}, Stops: pl.stops}
		if err := probe.validate(); err != nil {
			return err
		}
		res, err := Stamp(ctx, tx, p, TableTasks, Links{DriverID: pl.next.DriverID})
		if err != nil {
			return err
		}
		pl.next.ID, pl.next.TenantID = id, uuid.MustParse(res.TenantID)
		if err := s.checkTenantLinks(ctx, q, p, pl.next.TenantID, taskLinks(pl, Task{}, true)...); err != nil {
			return err
		}
		no := in.taskNo
		if no == nil {
			day := PlanDate(pl.next.PlanAt)
			seq, err := q.NextTaskSeq(ctx, operationsdb.NextTaskSeqParams{TaskType: pl.next.TaskType, PlanDate: day})
			if err != nil {
				return seqError(err)
			}
			no = ptr(FormatTaskNo(pl.next.TaskType, day, int(seq)))
		}
		pl.next.TaskNo = no
		if pl.next.DriverID != nil {
			ro, err := s.nextRunOrder(ctx, q, *pl.next.DriverID)
			if err != nil {
				return err
			}
			pl.next.RunOrder = &ro
		}
		pl.stopsSet = len(pl.stops) > 0
		doc, err = encodeTask(pl, true, s.now().UTC())
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	w := wbDoc{collection: CollTasks, docID: id.String(), fields: doc, create: true}
	at, acked, err := s.writeBack(ctx, w)
	if err != nil {
		return WriteResult{}, err
	}
	if pl.next.DriverID != nil {
		ev := taskEvent(RouteTaskAssigned, pl.next, []string{"driverId", "status"})
		ev.payload["driverId"], ev.payload["previousDriverId"] = pl.next.DriverID, nil
		s.notifyWriteBack(ctx, p, rid, at, onDoc(id.String(), ev)...)
	}
	if !acked {
		return echo(id, w, at), nil
	}
	view, err := s.writtenTaskView(ctx, p, id)
	if err != nil {
		return echo(id, w, at), nil //nolint:nilerr // written and acknowledged; a failed read answers the echo
	}
	return WriteResult{Status: http.StatusCreated, Data: view}, nil
}

// lockedTask runs fn with the task row locked under the caller's RLS (FOR UPDATE: the caller's UPDATE policy decides,
// so a task it may not change is 404), without writing PostgreSQL: the write-back's authorization and plan.
func (s *Service) lockedTask(ctx context.Context, p *authz.Principal, id uuid.UUID,
	fn func(tx pgx.Tx, q *operationsdb.Queries, cur Task) error) error {
	return s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		cur, err := q.LockTask(ctx, id)
		if err != nil {
			return MapError(notFound(err))
		}
		if cur.LegacyDocID == nil || *cur.LegacyDocID == "" {
			return notProjected("id")
		}
		return fn(tx, q, cur)
	})
}

// patchTaskWriteBack is PATCH /v1/tasks/{id} while operations is Firestore-owned: the changed fields only, merged
// into the task document (it must exist), then the writer's task.* notify events.
func (s *Service) patchTaskWriteBack(ctx context.Context, p *authz.Principal, rid string, d directory, id uuid.UUID, in taskFields) (WriteResult, error) {
	var cur Task
	var pl taskPlan
	var doc map[string]any
	err := s.lockedTask(ctx, p, id, func(tx pgx.Tx, q *operationsdb.Queries, c Task) error {
		cur = c
		var ck checker
		var err error
		if pl, err = s.planTask(ctx, q, d, &ck, cur, in, false); err != nil {
			return err
		}
		if err := ck.err(); err != nil {
			return err
		}
		if pl.stopsSet && len(pl.stops) > 0 {
			probe := NewTask{TaskInsert: TaskInsert{TaskType: cur.TaskType, Status: cur.Status, TenantSource: cur.TenantSource,
				TenantID: cur.TenantID, PlanAt: cur.PlanAt, SourceHubRaw: cur.SourceHubRaw, DestinationRaw: cur.DestinationRaw},
				Stops: pl.stops}
			if err := probe.validate(); err != nil {
				return err
			}
		}
		if len(pl.changed) == 0 {
			return nil
		}
		if err := s.checkTenantLinks(ctx, q, p, cur.TenantID, taskLinks(pl, cur, false)...); err != nil {
			return err
		}
		if err := planMoveLock(ctx, tx, q, cur, pl); err != nil { // before the Firestore commit
			return err
		}
		if err := helperPayrollLock(ctx, q, cur, pl); err != nil {
			return err
		}
		if slices.Contains(pl.changed, "status") && pl.next.Status != TaskCancelled && cur.Status == TaskCancelled {
			pl.changed = append(pl.changed, "cancelled")
		}
		doc, err = encodeTask(pl, false, s.now().UTC())
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	if len(pl.changed) == 0 {
		view, err := s.writtenTaskView(ctx, p, id)
		return WriteResult{Status: http.StatusOK, Data: view}, err
	}
	w := wbDoc{collection: CollTasks, docID: *cur.LegacyDocID, fields: doc, mustExist: true}
	at, acked, err := s.writeBack(ctx, w)
	if err != nil {
		return WriteResult{}, err
	}
	s.notifyWriteBack(ctx, p, rid, at, onDoc(*cur.LegacyDocID, taskChangeEvents(cur, pl.next, pl)...)...)
	return s.answerTask(ctx, p, id, w, at, acked)
}

// answerTask is the answer of an acknowledged task write-back (the task as GET reads it), else the 202 echo.
func (s *Service) answerTask(ctx context.Context, p *authz.Principal, id uuid.UUID, w wbDoc, at time.Time, acked bool) (WriteResult, error) {
	if !acked {
		return echo(id, w, at), nil
	}
	view, err := s.writtenTaskView(ctx, p, id)
	if err != nil {
		return echo(id, w, at), nil //nolint:nilerr // written and acknowledged; a failed read answers the echo
	}
	return WriteResult{Status: http.StatusOK, Data: view}, nil
}

// cancelTaskWriteBack is POST /v1/tasks/{id}/cancel while operations is Firestore-owned.
func (s *Service) cancelTaskWriteBack(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, reason *string) (WriteResult, error) {
	var cur Task
	skip := false
	err := s.lockedTask(ctx, p, id, func(_ pgx.Tx, _ *operationsdb.Queries, c Task) error {
		cur = c
		switch c.Status {
		case TaskCancelled:
			skip = true
		case TaskCompleted:
			return errPrecondition(ReasonTaskClosed, "a completed task is not cancelled").WithDetails(map[string]any{"status": c.Status})
		}
		return nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	if skip {
		view, err := s.writtenTaskView(ctx, p, id)
		return WriteResult{Status: http.StatusOK, Data: view}, err
	}
	now := s.now().UTC()
	next := cur
	next.Status, next.CancelledAt, next.CancelReason = TaskCancelled, &now, reason
	w := wbDoc{collection: CollTasks, docID: *cur.LegacyDocID, mustExist: true, fields: map[string]any{
		"status": legacyTaskStatus[TaskCancelled], "cancelledAt": now, "cancelReason": optStr(reason), "updatedAt": now}}
	at, acked, err := s.writeBack(ctx, w)
	if err != nil {
		return WriteResult{}, err
	}
	s.notifyWriteBack(ctx, p, rid, at, onDoc(*cur.LegacyDocID, taskEvent(RouteTaskCancelled, next, []string{"status"}))...)
	return s.answerTask(ctx, p, id, w, at, acked)
}

// addStopWriteBack is POST /v1/tasks/{id}/stops while operations is Firestore-owned: the stop is appended to the
// document's own deliveryStops (read from Firestore, so the progress the 3.x app wrote on the other stops is kept),
// under the update time it read; a concurrent change of the document retries the read (three times, then 409).
func (s *Service) addStopWriteBack(ctx context.Context, p *authz.Principal, rid string, d directory, id uuid.UUID, in stopInput) (WriteResult, error) {
	var cur Task
	var st NewStop
	var party *operationsdb.WritePartyRow
	err := s.lockedTask(ctx, p, id, func(_ pgx.Tx, q *operationsdb.Queries, c Task) error {
		cur = c
		var err error
		if st, err = s.stopOf(ctx, q, d, in); err != nil {
			return err
		}
		if st.DestinationLinkedPartyID != nil {
			var ck checker
			party, err = s.party(ctx, q, &ck, "destinationLinkedPartyId", *st.DestinationLinkedPartyID, map[uuid.UUID]operationsdb.WritePartyRow{})
			return errors.Join(err, ck.err())
		}
		return nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	dest := normalizeDestination(st.Destination)
	if dest == "" {
		return WriteResult{}, invalid("destination", "required")
	}
	for attempt := 0; ; attempt++ {
		fields, updated, ok, err := s.wb.Writer.Get(ctx, CollTasks, *cur.LegacyDocID)
		if err != nil {
			return WriteResult{}, writeback.ErrUnavailable(err)
		}
		if !ok {
			return WriteResult{}, notFound(pgx.ErrNoRows)
		}
		stops, _ := fields["deliveryStops"].([]any)
		if len(stops) == 0 {
			primary := normalizeDestination(stringOf(fields["destination"]))
			if primary == "" {
				return WriteResult{}, errPrecondition("no_primary_destination", "the task has no destination to keep as stop 1")
			}
			first := map[string]any{"index": int64(1), "destination": primary, "sequence": int64(0), "status": "pending"}
			for _, k := range []string{"destinationLinkedCustomerId", "destinationLinkedCustomerName",
				"destinationLinkedCustomerCode", "destinationCustomerLinkKind"} {
				if v, ok := fields[k]; ok {
					first[k] = v
				}
			}
			stops = []any{first}
		}
		next := int64(0)
		for _, raw := range stops {
			m, _ := raw.(map[string]any)
			if normalizeDestination(stringOf(m["destination"])) == dest {
				return WriteResult{}, ErrDuplicateDestination(dest)
			}
			if i, ok := m["index"].(int64); ok && i > next {
				next = i
			}
		}
		now := s.now().UTC()
		stop := map[string]any{"destination": dest, "index": next + 1, "sequence": int64(len(stops)), "status": "pending",
			"addedAt": now}
		if st.SourceID != nil {
			stop["sourceId"] = strings.TrimSpace(*st.SourceID)
		}
		if st.IsCustom {
			stop["isCustom"] = true
		}
		if party != nil {
			if pid, code, name, kind := partyDoc(*party); pid != "" {
				stop["destinationLinkedCustomerId"], stop["destinationLinkedCustomerName"] = pid, name
				stop["destinationLinkedCustomerCode"], stop["destinationCustomerLinkKind"] = code, kind
			} else {
				return WriteResult{}, notProjected("destinationLinkedPartyId")
			}
		}
		w := wbDoc{collection: CollTasks, docID: *cur.LegacyDocID, updateTime: updated, fields: map[string]any{
			"deliveryStops": append(stops, stop), "isMultiDelivery": true, "updatedAt": now}}
		at, acked, err := s.writeBack(ctx, w)
		if errors.Is(err, writeback.ErrConflict) && attempt < 2 {
			continue
		}
		if err != nil {
			return WriteResult{}, err
		}
		s.notifyWriteBack(ctx, p, rid, at, onDoc(*cur.LegacyDocID, taskEvent(RouteTaskUpdated, cur, []string{"deliveryStops"}))...)
		if !acked {
			return echo(id, w, at), nil
		}
		var rows []TaskStop
		if err := s.read(ctx, p, func(q *operationsdb.Queries) error {
			var err error
			rows, err = q.TaskStops(ctx, id)
			return err
		}); err != nil {
			return echo(id, w, at), nil //nolint:nilerr // written and acknowledged; a failed read answers the echo
		}
		out := make([]TaskStopRow, 0, len(rows))
		for _, r := range rows {
			out = append(out, d.taskStop(r, !scoped(p)))
		}
		return WriteResult{Status: http.StatusOK, Data: map[string]any{"taskId": id, "stops": out}}, nil
	}
}

// onDoc names the Firestore document of write-back events (notifyWriteBack).
func onDoc(doc string, evs ...opsEvent) []opsEvent {
	for i := range evs {
		evs[i].doc = doc
	}
	return evs
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func invalid(field, reason string) error { return httpx.ErrInvalidArgument(violation(field, reason)) }
