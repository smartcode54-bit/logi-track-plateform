package operations

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Driver core-loop constants (T55).
const (
	// RouteTaskCheckedIn is the routing key of a driver's check-in (Appendix B §B.5.2): notify.line sends the check-in
	// card, notify.fcm the silent tasks_changed push.
	RouteTaskCheckedIn = "task.checked_in"
	// ReasonTaskNotOpen is the 409 of a check-in on a task that is no longer pending or assigned.
	ReasonTaskNotOpen = "task_not_open"
	// ReasonQueueBlocked is the 409 of a check-in while an earlier task of the driver's queue is still open (Q2 of the
	// spec: the server enforces the queue gate the app shows, ADR 0028 order).
	ReasonQueueBlocked = "queue_blocked"
	// ReasonUnknownVehicleClass refuses a truck without a vehicle class: it could not price the trip (vehicle
	// identity).
	ReasonUnknownVehicleClass = "unknown_vehicle_class"

	checkinPhotoPurpose      = "checkin_photo"
	checkinAppPhotoPurpose   = "checkin_app_screenshot"
	maxDriverQueueForTheGate = MaxLimit
)

// driverTask resolves the task a driver route names ({id}: the uuid or the Firestore doc id) to one the driver drives
// or helps on (R28): another driver's task is 403 not_task_owner, a missing one 404 task_not_found.
func (s *Service) driverTask(ctx context.Context, p *authz.Principal, ref string) (uuid.UUID, error) {
	if s.owners == nil {
		return uuid.Nil, errors.New("operations: the driver task routes need a task owner resolver (iam.TaskOwners)")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > maxTaskRef {
		return uuid.Nil, errTaskNotFound()
	}
	task, ok, err := s.owners.TaskOwner(ctx, ref)
	if err != nil {
		return uuid.Nil, err
	}
	if !ok {
		return uuid.Nil, errTaskNotFound()
	}
	me := *p.DriverID
	if !sameID(task.DriverID, &me) && !sameID(task.HelperDriverID, &me) {
		return uuid.Nil, httpx.NewError(http.StatusForbidden, CodeNotTaskOwner, "the task belongs to another driver")
	}
	return task.ID, nil
}

// requireDriver is the guard of every driver core-loop service: a driver acting as itself holding cp, on a deployment
// whose operations PostgreSQL writes (T55 does not write back to Firestore: R27, 422 photos write_back_unsupported).
func (s *Service) requireDriver(p *authz.Principal, cp authz.Cap) error {
	if !driverPrincipal(p) {
		return authz.ErrPermissionDenied("the route acts for a driver (driver:self)")
	}
	if !p.Can(cp) {
		return authz.ErrPermissionDenied("missing capability", cp)
	}
	mode, err := s.mode()
	if err != nil {
		return err
	}
	if mode == modeWriteBack {
		return httpx.ErrInvalidArgument(violation("photos", ReasonWriteBackUnsupported))
	}
	return nil
}

// CheckInForDriver is POST /v1/mobile/tasks/{id}/check-in ✱ {truckId, checkInAt, lat?, lng?, photoKey,
// appScreenshotKey, installId?} (mobile:checkin, driver:self; spec mobile-go-core-loop R22). In one transaction under
// the driver's RLS: the task (pending or assigned, the driver's or its helper's) is locked; the queue gate of ADR 0028
// refuses it while an earlier task of the driver's queue is still open (409 queue_blocked {blockingTaskId}); the truck
// must be readable by the driver (its own company, RLS) and have a vehicle class; the check-in photo and the
// customer-app screenshot (ADR 0019, mandatory) are committed with the task as owner; the task becomes checked_in with
// the truck of this job; the driver's active_* columns name the truck and the task; the outbox gets task.checked_in.
//
// A task already checked in at the same checkInAt answers 200 with the task (a replay after IDEMPOTENCY_TTL); any
// other state is 409 task_not_open {status}. helperDriverId is refused in this slice (422 not_supported).
func (s *Service) CheckInForDriver(ctx context.Context, p *authz.Principal, rid, ref string, body []byte) (WriteResult, error) {
	if err := s.requireDriver(p, authz.MobileCheckin); err != nil {
		return WriteResult{}, err
	}
	var ck checker
	f, err := parseFields(body, &ck)
	if err != nil {
		return WriteResult{}, err
	}
	f.only("truckId", "checkInAt", "lat", "lng", "photoKey", "appScreenshotKey", "installId")
	truckID, at := f.id("truckId"), f.instant("checkInAt")
	lat, lng := f.number("lat"), f.number("lng")
	photoKey, appKey := f.required("photoKey"), f.required("appScreenshotKey")
	_ = f.optText("installId")
	if truckID == nil && !f.has("truckId") {
		ck.add("truckId", "required")
	}
	if at == nil && !f.has("checkInAt") {
		ck.add("checkInAt", "required")
	}
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	taskID, err := s.driverTask(ctx, p, ref)
	if err != nil {
		return WriteResult{}, err
	}
	me := *p.DriverID
	when := at.UTC()
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		q := operationsdb.New(tx)
		cur, err := q.LockTask(ctx, taskID)
		if err != nil {
			return MapError(notFound(err))
		}
		if cur.Status == TaskCheckedIn && cur.CheckInAt != nil && cur.CheckInAt.Equal(when) {
			return errReplayed
		}
		if cur.Status != TaskPending && cur.Status != TaskAssigned {
			return errPrecondition(ReasonTaskNotOpen, "only a pending or assigned task is checked in").
				WithDetails(map[string]any{"status": cur.Status})
		}
		blocking, err := queueBlocker(ctx, q, me, cur.ID)
		if err != nil {
			return err
		}
		if blocking != nil {
			return errPrecondition(ReasonQueueBlocked, "an earlier task of the queue is still open").
				WithDetails(map[string]any{"blockingTaskId": blocking.String()})
		}
		tr, err := q.WriteTruck(ctx, *truckID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return httpx.ErrInvalidArgument(violation("truckId", "not_found"))
		case err != nil:
			return MapError(err)
		case tr.TenantID != cur.TenantID:
			return httpx.ErrInvalidArgument(violation("truckId", "not_found"))
		case tr.VehicleClass == nil || *tr.VehicleClass == "":
			return httpx.ErrInvalidArgument(violation("truckId", ReasonUnknownVehicleClass))
		}
		if s.commits == nil {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "photoKey", Reason: "not_found",
				Params: map[string]any{"key": photoKey}})
		}
		photo, err := s.commits.Commit(ctx, tx, storage.CommitInput{Key: photoKey, Field: "photoKey",
			Purposes: []string{checkinPhotoPurpose}, OwnerID: cur.ID, UserID: p.UserID, TenantID: p.EffectiveTenant()})
		if err != nil {
			return err
		}
		app, err := s.commits.Commit(ctx, tx, storage.CommitInput{Key: appKey, Field: "appScreenshotKey",
			Purposes: []string{checkinAppPhotoPurpose}, OwnerID: cur.ID, UserID: p.UserID, TenantID: p.EffectiveTenant()})
		if err != nil {
			return err
		}
		plate := strings.TrimSpace(tr.LicensePlate)
		next, err := q.CheckInTask(ctx, operationsdb.CheckInTaskParams{CheckInAt: when, PhotoFileID: &photo.ID,
			AppScreenshotFileID: &app.ID, Lat: lat, Lng: lng, TruckID: &tr.ID, TruckType: tr.VehicleClass,
			LicensePlateSnapshot: &plate, ID: cur.ID})
		if err != nil {
			return MapError(err)
		}
		if _, err := q.SetDriverActiveTask(ctx, operationsdb.SetDriverActiveTaskParams{TruckID: &tr.ID, Plate: &plate,
			TaskID: &next.ID, StartedAt: when, DriverID: me}); err != nil {
			return MapError(err)
		}
		topics, err := taskTopics(ctx, q, next)
		if err != nil {
			return err
		}
		ev := withTopics(taskEvent(RouteTaskCheckedIn, next, []string{"status", "checkInAt", "truckId"}), topics)
		return appendEvents(ctx, tx, rid, "", ev)
	})
	if err != nil && !errors.Is(err, errReplayed) {
		return WriteResult{}, err
	}
	v, err := s.writtenTaskView(ctx, p, taskID)
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Status: http.StatusOK, Data: v}, nil
}

// errReplayed ends a driver write's transaction without a change: the request was already applied (a replay after the
// Idempotency-Key record expired), so the handler answers with the current state.
var errReplayed = errors.New("operations: the driver write was already applied")

// queueBlocker is the server side of the check-in queue gate (taskBlocksSuccessorInQueue, isQueueEligibleForCheckIn of
// task_repository.dart:79-115; spec Q2): walking the driver's open queue in ADR 0028 order up to task, the first task
// that is pending, assigned, in transit, or checked in without a delivered trip blocks it. nil = not blocked (also
// when task is not in the open queue: the status check answers for it).
func queueBlocker(ctx context.Context, q *operationsdb.Queries, driver, task uuid.UUID) (*uuid.UUID, error) {
	rows, err := q.DriverTasks(ctx, operationsdb.DriverTasksParams{DriverID: driver, Statuses: OpenTaskStatuses,
		RowLimit: int32(maxDriverQueueForTheGate + 1)})
	if err != nil {
		return nil, MapError(err)
	}
	var earlier []Task
	found := false
	for _, r := range rows {
		if r.Task.ID == task {
			found = true
			break
		}
		earlier = append(earlier, r.Task)
	}
	if !found {
		return nil, nil
	}
	var checkedIn []uuid.UUID
	for _, t := range earlier {
		if t.Status == TaskCheckedIn {
			checkedIn = append(checkedIn, t.ID)
		}
	}
	delivered := map[uuid.UUID]bool{}
	if len(checkedIn) > 0 {
		trips, err := q.TripsOfTasks(ctx, operationsdb.TripsOfTasksParams{TaskIds: checkedIn, ReachDriver: &driver})
		if err != nil {
			return nil, MapError(err)
		}
		for _, tr := range trips {
			if tr.TaskID != nil && tr.Status == TripDelivered {
				delivered[*tr.TaskID] = true
			}
		}
	}
	for _, t := range earlier {
		if t.Status == TaskCheckedIn && delivered[t.ID] {
			continue
		}
		id := t.ID
		return &id, nil
	}
	return nil, nil
}
