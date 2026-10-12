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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/tripcheck"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The write-back flows of the trip writes. A trip document's id is its legacy_doc_id (the number the driver typed,
// R25); trip statuses keep their literals (§13.8); deliveredTimestamp is the delivery time the loader reads.

// lockedTrip runs fn with the trip locked under the caller's RLS (authorization and plan only: PostgreSQL is not
// written while Firestore owns the collection).
func (s *Service) lockedTrip(ctx context.Context, p *authz.Principal, id uuid.UUID, fn func(tx pgx.Tx, q *operationsdb.Queries, cur Trip) error) error {
	return s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		cur, err := lockTrip(ctx, q, id)
		if err != nil {
			return err
		}
		if cur.LegacyDocID == nil || *cur.LegacyDocID == "" {
			return notProjected("id")
		}
		return fn(tx, q, cur)
	})
}

// answerTrip is the answer of an acknowledged trip write-back, else the 202 echo.
func (s *Service) answerTrip(ctx context.Context, p *authz.Principal, id uuid.UUID, w wbDoc, at time.Time, acked bool) (WriteResult, error) {
	if !acked {
		return echo(id, w, at), nil
	}
	res, err := s.tripView(ctx, p, id, http.StatusOK)
	if err != nil {
		return echo(id, w, at), nil //nolint:nilerr // written and acknowledged; a failed read answers the echo
	}
	return res, nil
}

// patchTripWriteBack writes the changed fields of a trip edit to its document. Stops, the derived job type and the task
// mirror (T34) are a read-modify-write of the trip and task documents in one commit (shapeDocs), retried twice when a
// document changed between the read and the commit.
func (s *Service) patchTripWriteBack(ctx context.Context, p *authz.Principal, rid string, d directory, id uuid.UUID, ch tripChange) (WriteResult, error) {
	var cur Trip
	var changed []string
	var pr operationsdb.UpdateTripDetailsParams
	var plan wbShape
	doc := map[string]any{}
	err := s.lockedTrip(ctx, p, id, func(tx pgx.Tx, q *operationsdb.Queries, c Trip) error {
		cur = c
		var err error
		pr, changed, err = s.mergeTrip(ctx, tx, d, cur, ch)
		if err != nil {
			return err
		}
		for _, k := range changed {
			switch k {
			case "sealCode":
				doc["sealCode"] = optStr(pr.SealCode)
			case "partnerCode":
				doc["partnerCode"] = optStr(pr.PartnerCode)
			case "ocrData":
				if len(pr.OcrData) == 0 {
					doc["ocrData"] = nil
				} else {
					var m map[string]any
					if err := json.Unmarshal(pr.OcrData, &m); err != nil {
						return invalid("ocrData", "invalid")
					}
					doc["ocrData"] = legacyJSON(m)
				}
			case "deliveredAt":
				doc["deliveredTimestamp"] = optTime(pr.DeliveredAt)
			case "needsAdminReview":
				doc["needsAdminReview"] = pr.NeedsAdminReview
			case "reviewStatus":
				doc["reviewStatus"] = optStr(pr.ReviewStatus)
			case "reviewReason":
				doc["reviewReason"] = optStr(pr.ReviewReason)
			case "origin":
				doc["origin"] = optStr(pr.OriginRaw)
			case "destination":
				doc["destination"] = optStr(pr.DestinationRaw)
			}
		}
		plan, err = s.planShapeWriteBack(ctx, q, d, cur, pr, changed, ch)
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	for attempt := 0; ; attempt++ {
		docs, taskChanged, write, err := s.shapeDocs(ctx, d, cur, pr, changed, ch, plan, doc, s.now().UTC())
		if err != nil {
			return WriteResult{}, err
		}
		if !write {
			return s.tripView(ctx, p, id, http.StatusOK)
		}
		if len(taskChanged) > 0 {
			// The task's mirror (route, billing link, type, planned stops) is a task edit: Appendix C row 23.
			if err := requireCap(p, authz.OperationsManageTasks); err != nil {
				return WriteResult{}, err
			}
		}
		at, acked, err := s.writeBack(ctx, docs...)
		if errors.Is(err, writeback.ErrConflict) && ch.stopsSet && attempt < 2 {
			continue // a document changed between the read and the commit: read it again
		}
		if err != nil {
			return WriteResult{}, err
		}
		if len(taskChanged) > 0 && plan.task != nil {
			next := *plan.task
			if jt, ok := d.jobTypeOf(pr.OriginRaw); ok && slices.Contains(taskChanged, "taskType") {
				next.TaskType = jt
			}
			s.notifyWriteBack(ctx, p, rid, at, onDoc(*next.LegacyDocID, taskEvent(RouteTaskUpdated, next, taskChanged))...)
		}
		return s.answerTrip(ctx, p, id, docs[0], at, acked)
	}
}

// legacyJSON turns decoded JSON numbers (float64) into the Firestore client's value types (an integral number stays a
// double, as the browser's JSON wrote it); maps and arrays recurse.
func legacyJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if e == nil {
				continue
			}
			out[k] = legacyJSON(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = legacyJSON(e)
		}
		return out
	}
	return v
}

// openTaskDoc is the task document a trip write completes or cancels with the trip: the task's Firestore id when
// the task is visible, open and has one.
func openTaskDoc(ctx context.Context, q *operationsdb.Queries, trip Trip) (*Task, error) {
	if trip.TaskID == nil {
		return nil, nil
	}
	task, err := q.LockTask(ctx, *trip.TaskID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, MapError(err)
	case task.Status == TaskCompleted || task.Status == TaskCancelled || task.LegacyDocID == nil:
		return nil, nil
	}
	return &task, nil
}

// deliverTripWriteBack writes the admin close: the trip document delivered (admin_web), its task Completed and the
// driver's activeTruck removed when it still names that task, in one Firestore batch.
func (s *Service) deliverTripWriteBack(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, at *time.Time, lat, lng *float64) (WriteResult, error) {
	var cur Trip
	var task *Task
	var driverDoc string
	var when time.Time
	err := s.lockedTrip(ctx, p, id, func(_ pgx.Tx, q *operationsdb.Queries, c Trip) error {
		cur = c
		if c.Status != TripInTransit && c.Status != TripIncident {
			return errPrecondition(ReasonTripNotOpen, "only an in-transit or incident trip is delivered").
				WithDetails(map[string]any{"status": c.Status})
		}
		when = deliveryTime(at, c.DeliveredAt, s.now())
		var err error
		if task, err = openTaskDoc(ctx, q, c); err != nil || task == nil || task.DriverID == nil {
			return err
		}
		dr, err := q.WriteDriver(ctx, *task.DriverID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return MapError(err)
		}
		if dr.ActiveTaskID != nil && *dr.ActiveTaskID == task.ID && dr.LegacyDocID != nil {
			driverDoc = *dr.LegacyDocID
		}
		return nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	now := s.now().UTC()
	trip := map[string]any{"status": TripDelivered, "deliveredTimestamp": when, "deliveredVia": deliveredViaAdminWeb, "updatedAt": now}
	if lat != nil {
		trip["deliveredLat"] = *lat
	}
	if lng != nil {
		trip["deliveredLng"] = *lng
	}
	docs := []wbDoc{{collection: CollTrips, docID: *cur.LegacyDocID, fields: trip, mustExist: true}}
	var evs []opsEvent
	next := cur
	next.Status, next.DeliveredAt = TripDelivered, &when
	evs = append(evs, onDoc(*cur.LegacyDocID, tripEvent(RouteTripDelivered, next, []string{"status", "deliveredAt"}))...)
	if task != nil {
		docs = append(docs, wbDoc{collection: CollTasks, docID: *task.LegacyDocID, mustExist: true,
			fields: map[string]any{"status": legacyTaskStatus[TaskCompleted], "updatedAt": now}})
		done := *task
		done.Status = TaskCompleted
		evs = append(evs, onDoc(*task.LegacyDocID, taskEvent(RouteTaskUpdated, done, []string{"status"}))...)
		if driverDoc != "" {
			docs = append(docs, wbDoc{collection: "drivers", docID: driverDoc, mustExist: true,
				fields: map[string]any{"activeTruck": nil}})
		}
	}
	wat, acked, err := s.writeBack(ctx, docs...)
	if err != nil {
		return WriteResult{}, err
	}
	s.notifyWriteBack(ctx, p, rid, wat, evs...)
	return s.answerTrip(ctx, p, id, docs[0], wat, acked)
}

// cancelTripWriteBack writes the trip document cancelled and its open task Cancelled in one batch.
func (s *Service) cancelTripWriteBack(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID) (WriteResult, error) {
	var cur Trip
	var task *Task
	skip := false
	err := s.lockedTrip(ctx, p, id, func(_ pgx.Tx, q *operationsdb.Queries, c Trip) error {
		cur = c
		switch c.Status {
		case TripCancelled:
			skip = true
			return nil
		case TripDelivered:
			return errPrecondition(ReasonTripDelivered, "a delivered trip is not cancelled").WithDetails(map[string]any{"status": c.Status})
		}
		var err error
		task, err = openTaskDoc(ctx, q, c)
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	if skip {
		return s.tripView(ctx, p, id, http.StatusOK)
	}
	docs, evs := s.cancelDocs(cur, task)
	at, acked, err := s.writeBack(ctx, docs...)
	if err != nil {
		return WriteResult{}, err
	}
	s.notifyWriteBack(ctx, p, rid, at, evs...)
	return s.answerTrip(ctx, p, id, docs[0], at, acked)
}

// cancelDocs are the documents and notify events of one trip cancellation.
func (s *Service) cancelDocs(trip Trip, task *Task) ([]wbDoc, []opsEvent) {
	now := s.now().UTC()
	docs := []wbDoc{{collection: CollTrips, docID: *trip.LegacyDocID, mustExist: true,
		fields: map[string]any{"status": TripCancelled, "updatedAt": now}}}
	var evs []opsEvent
	if task != nil {
		reason := "trip " + trip.TripNo + " cancelled"
		docs = append(docs, wbDoc{collection: CollTasks, docID: *task.LegacyDocID, mustExist: true, fields: map[string]any{
			"status": legacyTaskStatus[TaskCancelled], "cancelledAt": now, "cancelReason": reason, "updatedAt": now}})
		done := *task
		done.Status = TaskCancelled
		evs = append(evs, onDoc(*task.LegacyDocID, taskEvent(RouteTaskCancelled, done, []string{"status"}))...)
	}
	return docs, evs
}

// bulkCancelWriteBack cancels the in-transit trips among ids and their open tasks in one Firestore batch.
func (s *Service) bulkCancelWriteBack(ctx context.Context, p *authz.Principal, rid string, ids []uuid.UUID) (WriteResult, error) {
	out := BulkCancelResult{Cancelled: []uuid.UUID{}, Failed: []BulkCancelFailure{}}
	var docs []wbDoc
	var evs []opsEvent
	err := s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		for _, id := range ids {
			cur, err := q.LockTrip(ctx, id)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				out.Failed = append(out.Failed, BulkCancelFailure{ID: id, Reason: "not_found"})
				continue
			case err != nil:
				return MapError(err)
			case cur.Status != TripInTransit:
				out.Failed = append(out.Failed, BulkCancelFailure{ID: id, Reason: ReasonNotInTransit, Status: cur.Status})
				continue
			case cur.LegacyDocID == nil:
				out.Failed = append(out.Failed, BulkCancelFailure{ID: id, Reason: ReasonNotProjected})
				continue
			}
			task, err := openTaskDoc(ctx, q, cur)
			if err != nil {
				return err
			}
			d, e := s.cancelDocs(cur, task)
			docs, evs = append(docs, d...), append(evs, e...)
			out.Cancelled = append(out.Cancelled, id)
		}
		return nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	if len(docs) == 0 {
		return WriteResult{Status: http.StatusOK, Data: out}, nil
	}
	at, acked, err := s.writeBack(ctx, docs...)
	if err != nil {
		return WriteResult{}, err
	}
	s.notifyWriteBack(ctx, p, rid, at, evs...)
	if !acked {
		out.Pending = true
		return WriteResult{Status: http.StatusAccepted, Data: out}, nil
	}
	return WriteResult{Status: http.StatusOK, Data: out}, nil
}

// renameTripWriteBack keeps renameTripRecord's batch (renameTripRecord.ts:51-150) while Firestore owns the trips: the
// delivered trip document is copied to the new number with spxTripId, renamedFromTripId, renamedAt and renamedBy
// (created only if no document holds the number), its incident reports are re-pointed, and the old document is
// deleted under the update time it was read with, all in one commit; trip_record_renamed is appended after it. The
// mirror applies the batch as an in-place rename with one trip_no_history row (T29).
func (s *Service) renameTripWriteBack(ctx context.Context, p *authz.Principal, rid string, id uuid.UUID, newNo string) (WriteResult, error) {
	newNo = strings.TrimSpace(newNo)
	if r := tripcheck.ValidateTripNo(newNo); r != "" {
		return WriteResult{}, invalid("newTripNo", r)
	}
	var cur Trip
	var incidents []string
	var actor string
	err := s.lockedTrip(ctx, p, id, func(_ pgx.Tx, q *operationsdb.Queries, c Trip) error {
		cur = c
		switch {
		case c.TripNo == newNo || *c.LegacyDocID == newNo:
			return invalid("newTripNo", "unchanged")
		case c.Status != TripDelivered:
			return errPrecondition("not_delivered", "only a delivered trip can be renamed").WithDetails(map[string]any{"status": c.Status})
		}
		var err error
		if actor, err = actorRef(ctx, q, p); err != nil {
			return err
		}
		r := ReachOf(p)
		rows, err := q.IncidentsOfTrip(ctx, operationsdb.IncidentsOfTripParams{TripID: c.ID, AllTenants: r.All,
			TenantIds: r.tenants(), ReachDriver: r.Driver})
		if err != nil {
			return MapError(err)
		}
		for _, inc := range rows {
			if inc.LegacyDocID != nil {
				incidents = append(incidents, *inc.LegacyDocID)
			}
		}
		return nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	if s.renamer == nil {
		return WriteResult{}, errors.New("operations: no trip renamer is wired")
	}
	old := *cur.LegacyDocID
	for attempt := 0; ; attempt++ {
		fields, updated, ok, err := s.wb.Writer.Get(ctx, CollTrips, old)
		if err != nil {
			return WriteResult{}, writeback.ErrUnavailable(err)
		}
		if !ok {
			return WriteResult{}, notFound(pgx.ErrNoRows)
		}
		if stringOf(fields["status"]) != TripDelivered {
			return WriteResult{}, errPrecondition("not_delivered", "only a delivered trip can be renamed").
				WithDetails(map[string]any{"status": stringOf(fields["status"])})
		}
		now := s.now().UTC()
		copied := make(map[string]any, len(fields)+5)
		for k, v := range fields {
			copied[k] = v
		}
		copied["spxTripId"], copied["renamedFromTripId"] = newNo, old
		copied["renamedAt"], copied["renamedBy"], copied["updatedAt"] = now, actor, now
		docs := []wbDoc{{collection: CollTrips, docID: newNo, fields: copied, replace: true, create: true}}
		for _, inc := range incidents {
			docs = append(docs, wbDoc{collection: CollIncidents, docID: inc, mustExist: true,
				fields: map[string]any{"tripId": newNo}})
		}
		docs = append(docs, wbDoc{collection: CollTrips, docID: old, del: true, updateTime: updated})
		at, acked, err := s.writeBack(ctx, docs...)
		if errors.Is(err, writeback.ErrConflict) && attempt < 2 {
			if _, _, exists, gerr := s.wb.Writer.Get(ctx, CollTrips, newNo); gerr == nil && exists {
				return WriteResult{}, ErrDuplicateTripNo(newNo)
			}
			continue
		}
		if err != nil {
			return WriteResult{}, err
		}
		if err := s.renamer.AuditRename(ctx, p, rid, cur, cur.TripNo, newNo, len(incidents)); err != nil {
			s.log.Error().Err(err).Str("trip", cur.ID.String()).Msg("write-back rename: trip_record_renamed not appended")
		}
		return s.answerTrip(ctx, p, id, docs[0], at, acked)
	}
}
