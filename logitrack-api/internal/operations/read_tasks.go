package operations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope/scopedb"
)

// --- payloads -------------------------------------------------------------------------------------------------

// TaskBase is a task as every reader may see it: the columns of the scope_tasks projection (Appendix C §C.3.7) with the
// driver and helper names, the resolved places, the planned stops and the newest trip. It is the whole payload of a
// dispatcher, a customer-scope principal and a driver reading its own task (driver:self, R33).
type TaskBase struct {
	ID                 uuid.UUID     `json:"id"`
	TenantID           uuid.UUID     `json:"tenantId"`
	TaskNo             *string       `json:"taskNo"`
	TaskType           string        `json:"taskType"`
	JobCategory        *string       `json:"jobCategory"`
	Status             string        `json:"status"`
	PlanAt             time.Time     `json:"planAt"`
	PlanDate           *string       `json:"planDate"` // yyyy-MM-dd, Bangkok
	PlanTime           *string       `json:"planTime"`
	ActualPickupAt     *time.Time    `json:"actualPickupAt"`
	SourceHub          string        `json:"sourceHub"`
	SourceHubID        *uuid.UUID    `json:"sourceHubId"`
	Source             Place         `json:"source"`
	Destination        string        `json:"destination"`
	DestinationHubID   *uuid.UUID    `json:"destinationHubId"`
	DestinationSocKey  *string       `json:"destinationSocKey"`
	DestinationPlace   Place         `json:"destinationPlace"`
	TruckID            *uuid.UUID    `json:"truckId"`
	TruckType          *string       `json:"truckType"`
	LicensePlate       *string       `json:"licensePlate"`
	DriverID           *uuid.UUID    `json:"driverId"`
	DriverName         *string       `json:"driverName"`
	HelperDriverID     *uuid.UUID    `json:"helperDriverId"`
	HelperName         *string       `json:"helperName"`
	RunOrder           *int32        `json:"runOrder"`
	CheckInAt          *time.Time    `json:"checkInAt"`
	CheckInPhotoFileID *uuid.UUID    `json:"checkInPhotoFileId"`
	CheckInAppFileID   *uuid.UUID    `json:"checkInAppScreenshotFileId"`
	CheckInLat         *float64      `json:"checkInLat"`
	CheckInLng         *float64      `json:"checkInLng"`
	IsMultiDelivery    bool          `json:"isMultiDelivery"`
	Stops              []TaskStopRow `json:"stops"`
	Trip               *TripRef      `json:"trip"`
	CreatedAt          time.Time     `json:"createdAt"`
	UpdatedAt          time.Time     `json:"updatedAt"`
	// CheckInPhoto and CheckInApp are signed in the detail only (GET /v1/tasks/{id}).
	CheckInPhoto *FileRef `json:"checkInPhoto,omitempty"`
	CheckInApp   *FileRef `json:"checkInAppScreenshot,omitempty"`
}

// TaskView is a task as tenant staff read it: TaskBase plus the party links, the stamping and the cancellation.
type TaskView struct {
	TaskBase
	TenantSource             string     `json:"tenantSource"`
	BillingPartyID           *uuid.UUID `json:"billingPartyId"`
	SourceLinkedPartyID      *uuid.UUID `json:"sourceLinkedPartyId"`
	DestinationLinkedPartyID *uuid.UUID `json:"destinationLinkedPartyId"`
	LegacyTruckType          *string    `json:"legacyTruckType"`
	CancelledAt              *time.Time `json:"cancelledAt"`
	CancelReason             *string    `json:"cancelReason"`
}

// TaskStopRow is one planned stop of a multi-drop task (stop 1 is the task's own destination).
type TaskStopRow struct {
	Index                    int32      `json:"index"`
	Destination              string     `json:"destination"`
	DestinationHubID         *uuid.UUID `json:"destinationHubId"`
	Place                    Place      `json:"place"`
	Status                   string     `json:"status"`
	DeliveredAt              *time.Time `json:"deliveredAt"`
	DeliveredLat             *float64   `json:"deliveredLat"`
	DeliveredLng             *float64   `json:"deliveredLng"`
	Sequence                 *int32     `json:"sequence"`
	AddedAt                  *time.Time `json:"addedAt"`
	SourceID                 *string    `json:"sourceId"`
	IsCustom                 bool       `json:"isCustom"`
	DestinationLinkedPartyID *uuid.UUID `json:"destinationLinkedPartyId,omitempty"` // staff only
}

// TripRef is the newest trip of a task (the boards' trip column, first-mile/page.tsx:136-141).
type TripRef struct {
	ID     uuid.UUID `json:"id"`
	TripNo string    `json:"tripNo"`
	Status string    `json:"status"`
}

func dayString(d *time.Time) *string {
	if d == nil {
		return nil
	}
	v := d.Format(time.DateOnly)
	return &v
}

func (d directory) taskBase(t Task) TaskBase {
	return TaskBase{ID: t.ID, TenantID: t.TenantID, TaskNo: t.TaskNo, TaskType: t.TaskType, JobCategory: t.JobCategory,
		Status: t.Status, PlanAt: t.PlanAt, PlanDate: dayString(t.PlanDate), PlanTime: t.PlanTime,
		ActualPickupAt: t.ActualPickupAt, SourceHub: t.SourceHubRaw, SourceHubID: t.SourceHubID,
		Source: d.place(&t.SourceHubRaw, t.SourceHubID, nil), Destination: t.DestinationRaw,
		DestinationHubID: t.DestinationHubID, DestinationSocKey: t.DestinationSocKey,
		DestinationPlace: d.place(&t.DestinationRaw, t.DestinationHubID, t.DestinationSocKey), TruckID: t.TruckID,
		TruckType: t.TruckType, LicensePlate: t.LicensePlateSnapshot, DriverID: t.DriverID,
		HelperDriverID: t.HelperDriverID, RunOrder: t.RunOrder, CheckInAt: t.CheckInAt,
		CheckInPhotoFileID: t.CheckInPhotoFileID, CheckInAppFileID: t.CheckInAppScreenshotFileID, CheckInLat: t.CheckInLat,
		CheckInLng: t.CheckInLng, IsMultiDelivery: t.IsMultiDelivery, Stops: []TaskStopRow{}, CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt}
}

func (d directory) taskBaseScope(t scopedb.ScopeTask) TaskBase {
	return TaskBase{ID: t.ID, TenantID: t.TenantID, TaskNo: t.TaskNo, TaskType: t.TaskType, JobCategory: t.JobCategory,
		Status: t.Status, PlanAt: t.PlanAt, PlanDate: dayString(t.PlanDate), PlanTime: t.PlanTime,
		ActualPickupAt: t.ActualPickupAt, SourceHub: t.SourceHubRaw, SourceHubID: t.SourceHubID,
		Source: d.place(&t.SourceHubRaw, t.SourceHubID, nil), Destination: t.DestinationRaw,
		DestinationHubID: t.DestinationHubID, DestinationSocKey: t.DestinationSocKey,
		DestinationPlace: d.place(&t.DestinationRaw, t.DestinationHubID, t.DestinationSocKey), TruckID: t.TruckID,
		TruckType: t.TruckType, LicensePlate: t.LicensePlateSnapshot, DriverID: t.DriverID,
		HelperDriverID: t.HelperDriverID, RunOrder: t.RunOrder, CheckInAt: t.CheckInAt,
		CheckInPhotoFileID: t.CheckInPhotoFileID, CheckInAppFileID: t.CheckInAppScreenshotFileID, CheckInLat: t.CheckInLat,
		CheckInLng: t.CheckInLng, IsMultiDelivery: t.IsMultiDelivery, Stops: []TaskStopRow{}, CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt}
}

func taskView(b TaskBase, t Task) TaskView {
	return TaskView{TaskBase: b, TenantSource: t.TenantSource, BillingPartyID: t.BillingPartyID,
		SourceLinkedPartyID: t.SourceLinkedPartyID, DestinationLinkedPartyID: t.DestinationLinkedPartyID,
		LegacyTruckType: t.LegacyTruckType, CancelledAt: t.CancelledAt, CancelReason: t.CancelReason}
}

func (d directory) taskStop(s TaskStop, staff bool) TaskStopRow {
	out := TaskStopRow{Index: s.StopIndex, Destination: s.DestinationRaw, DestinationHubID: s.DestinationHubID,
		Place: d.place(&s.DestinationRaw, s.DestinationHubID, nil), Status: s.Status, DeliveredAt: s.DeliveredAt,
		DeliveredLat: s.DeliveredLat, DeliveredLng: s.DeliveredLng, Sequence: s.Sequence, AddedAt: s.AddedAt,
		SourceID: s.SourceID, IsCustom: s.IsCustom}
	if staff {
		out.DestinationLinkedPartyID = s.DestinationLinkedPartyID
	}
	return out
}

func (d directory) taskStopScope(s scopedb.ScopeTaskStop) TaskStopRow {
	return TaskStopRow{Index: s.StopIndex, Destination: s.DestinationRaw, DestinationHubID: s.DestinationHubID,
		Place: d.place(&s.DestinationRaw, s.DestinationHubID, nil), Status: s.Status, DeliveredAt: s.DeliveredAt,
		DeliveredLat: s.DeliveredLat, DeliveredLng: s.DeliveredLng, Sequence: s.Sequence, AddedAt: s.AddedAt,
		SourceID: s.SourceID, IsCustom: s.IsCustom}
}

// --- GET /v1/tasks ----------------------------------------------------------------------------------------------

// TaskListInput is the query of GET /v1/tasks (Appendix B §B.2.11): taskType, a plan day (date) or plan-day range
// (from, to: Bangkok days, both inclusive; a range lists on the plan axis, latest day first, else on created_at,
// newest first), status (repeatable or comma-separated), driverId (the driver or the helper), truckId, q, limit,
// cursor.
type TaskListInput struct {
	TaskType, Date, From, To string
	Statuses                 []string
	DriverID, TruckID, Q     string
	Limit                    int
	Cursor                   string
}

// taskFilter checks the input against p: a type p does not read is 403 (missing its capability); without taskType a
// principal that reads one type lists that type only.
func taskFilter(p *authz.Principal, in TaskListInput) (TaskFilter, error) {
	var ck checker
	f := TaskFilter{TaskType: strings.TrimSpace(in.TaskType), Q: qText(&ck, in.Q), Limit: in.Limit, Cursor: in.Cursor}
	allowed := taskTypesFor(p)
	switch {
	case f.TaskType == "":
		if len(allowed) == 1 {
			f.TaskType = allowed[0]
		}
	case !slices.Contains(TaskTypes, f.TaskType):
		ck.add("taskType", "invalid")
	case !slices.Contains(allowed, f.TaskType):
		return f, errTaskType(f.TaskType)
	}
	if len(allowed) == 0 {
		return f, authz.ErrPermissionDenied("missing capability", authz.OperationsViewFirstMile, authz.OperationsViewLineHaul)
	}
	if strings.TrimSpace(in.Date) != "" {
		if in.From != "" || in.To != "" {
			ck.add("date", "exclusive")
		}
		f.From = planDay(&ck, "date", in.Date)
		f.To = f.From
	} else {
		f.From, f.To = planDay(&ck, "from", in.From), planDay(&ck, "to", in.To)
	}
	f.Statuses = statusList(&ck, "status", in.Statuses, TaskStatuses)
	f.DriverID, f.TruckID = optUUID(&ck, "driverId", in.DriverID), optUUID(&ck, "truckId", in.TruckID)
	return f, ck.err()
}

// ListTasksView is GET /v1/tasks: a keyset page of the boards with names, places, stops and the newest trip; staff
// read their reach ([]TaskView), a dispatcher or a customer-scope principal the scope_tasks projection ([]TaskBase).
func (s *Service) ListTasksView(ctx context.Context, p *authz.Principal, in TaskListInput) (any, string, error) {
	f, err := taskFilter(p, in)
	if err != nil {
		return nil, "", err
	}
	d, err := s.directory(ctx, p)
	if err != nil {
		return nil, "", err
	}
	if scoped(p) {
		pg, err := s.listScopeTasks(ctx, p, f)
		if err != nil {
			return nil, "", err
		}
		items, err := s.scopeTaskBases(ctx, p, d, pg.Items)
		return items, pg.NextCursor, err
	}
	pg, err := s.ListTasks(ctx, p, f)
	if err != nil {
		return nil, "", err
	}
	items, err := s.staffTaskViews(ctx, p, d, pg.Items)
	return items, pg.NextCursor, err
}

// staffTaskViews completes a page of tasks read under p's staff reach: stops, newest trip and names.
func (s *Service) staffTaskViews(ctx context.Context, p *authz.Principal, d directory, tasks []Task) ([]TaskView, error) {
	ids := make([]uuid.UUID, len(tasks))
	var drivers []uuid.UUID
	for i, t := range tasks {
		ids[i] = t.ID
		drivers = appendID(appendID(drivers, t.DriverID), t.HelperDriverID)
	}
	r := ReachOf(p)
	stops := map[uuid.UUID][]TaskStopRow{}
	trips := map[uuid.UUID]TripRef{}
	if len(ids) > 0 {
		err := s.read(ctx, p, func(q *operationsdb.Queries) error {
			st, err := q.TaskStopsOf(ctx, ids)
			if err != nil {
				return err
			}
			for _, x := range st {
				stops[x.TaskID] = append(stops[x.TaskID], d.taskStop(x, !driverPrincipal(p)))
			}
			tr, err := q.TripsOfTasks(ctx, operationsdb.TripsOfTasksParams{TaskIds: ids, AllTenants: r.All,
				TenantIds: r.tenants(), ReachDriver: r.Driver})
			for _, x := range tr {
				if _, seen := trips[*x.TaskID]; !seen {
					trips[*x.TaskID] = TripRef{ID: x.ID, TripNo: x.TripNo, Status: x.Status}
				}
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	names, err := s.names(ctx, p, drivers)
	if err != nil {
		return nil, err
	}
	out := make([]TaskView, 0, len(tasks))
	for _, t := range tasks {
		b := d.taskBase(t)
		completeTask(&b, stops, trips, names)
		out = append(out, taskView(b, t))
	}
	return out, nil
}

func completeTask(b *TaskBase, stops map[uuid.UUID][]TaskStopRow, trips map[uuid.UUID]TripRef, names map[uuid.UUID]string) {
	if st, ok := stops[b.ID]; ok {
		b.Stops = st
	}
	if tr, ok := trips[b.ID]; ok {
		b.Trip = &tr
	}
	b.DriverName, b.HelperName = nameOf(names, b.DriverID), nameOf(names, b.HelperDriverID)
}

// scopeTaskBases completes a page of the projection: stops, newest trip and names, from the scope_* views.
func (s *Service) scopeTaskBases(ctx context.Context, p *authz.Principal, d directory, tasks []scopedb.ScopeTask) ([]TaskBase, error) {
	ids := make([]uuid.UUID, len(tasks))
	var drivers []uuid.UUID
	for i, t := range tasks {
		ids[i] = t.ID
		drivers = appendID(appendID(drivers, t.DriverID), t.HelperDriverID)
	}
	stops := map[uuid.UUID][]TaskStopRow{}
	trips := map[uuid.UUID]TripRef{}
	if len(ids) > 0 {
		err := s.scopeRead(ctx, p, func(q *scopedb.Queries) error {
			st, err := q.ScopeTaskStopsOf(ctx, ids)
			if err != nil {
				return err
			}
			for _, x := range st {
				stops[x.TaskID] = append(stops[x.TaskID], d.taskStopScope(x))
			}
			tr, err := q.ScopeTripsOfTasks(ctx, ids)
			for _, x := range tr {
				if _, seen := trips[*x.TaskID]; !seen {
					trips[*x.TaskID] = TripRef{ID: x.ID, TripNo: x.TripNo, Status: x.Status}
				}
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	names, err := s.names(ctx, p, drivers)
	if err != nil {
		return nil, err
	}
	out := make([]TaskBase, 0, len(tasks))
	for _, t := range tasks {
		b := d.taskBaseScope(t)
		completeTask(&b, stops, trips, names)
		out = append(out, b)
	}
	return out, nil
}

// listScopeTasks is ListTasks over scope_tasks (the same filters, axes and cursors).
func (s *Service) listScopeTasks(ctx context.Context, p *authz.Principal, f TaskFilter) (Page[scopedb.ScopeTask], error) {
	var ck checker
	limit := pageLimit(&ck, f.Limit)
	byPlan := f.From != nil || f.To != nil
	if byPlan && (f.From == nil || f.To == nil || f.To.Before(*f.From)) {
		ck.add("from", "invalid_range")
	}
	axis := axisCreated
	if byPlan {
		axis = axisPlan
	}
	c, ok := decodeCursor(f.Cursor, axis)
	if !ok {
		ck.add("cursor", "invalid")
	}
	if err := ck.err(); err != nil {
		return Page[scopedb.ScopeTask]{}, err
	}
	var rows []scopedb.ScopeTask
	err := s.scopeRead(ctx, p, func(q *scopedb.Queries) error {
		var err error
		if byPlan {
			rows, err = q.ListScopeTasksByPlan(ctx, scopedb.ListScopeTasksByPlanParams{FromDate: *f.From, ToDate: *f.To,
				TaskType: nonEmpty(f.TaskType), Statuses: f.Statuses, DriverID: f.DriverID, TruckID: f.TruckID,
				Q: nonEmpty(f.Q), AfterPlan: c.day(), AfterID: c.id(), RowLimit: int32(limit + 1)})
			return err
		}
		rows, err = q.ListScopeTasksByCreated(ctx, scopedb.ListScopeTasksByCreatedParams{TaskType: nonEmpty(f.TaskType),
			Statuses: f.Statuses, DriverID: f.DriverID, TruckID: f.TruckID, Q: nonEmpty(f.Q), AfterCreated: c.at(),
			AfterID: c.id(), RowLimit: int32(limit + 1)})
		return err
	})
	if err != nil {
		return Page[scopedb.ScopeTask]{}, err
	}
	return page(rows, limit, func(t scopedb.ScopeTask) cursor {
		if byPlan {
			return cursor{Axis: axisPlan, Day: t.PlanDate.Format(time.DateOnly), ID: t.ID}
		}
		return cursor{Axis: axisCreated, At: ptr(t.CreatedAt), ID: t.ID}
	}), nil
}

// --- GET /v1/tasks/{id}, /by-number/{taskNo}, /{id}/trip -------------------------------------------------------

// GetTaskView is GET /v1/tasks/{id}: the task with its stops, newest trip, names and signed check-in photos. Staff read
// their reach (TaskView), a driver its own or helper task (driver:self, R33: TaskBase), a scope principal the
// projection (TaskBase). Outside the reach 404; a task of a type p does not read 403.
func (s *Service) GetTaskView(ctx context.Context, p *authz.Principal, id uuid.UUID) (any, error) {
	return s.taskView(ctx, p, id, true)
}

// writtenTaskView is the answer of a task write (T31): GET /v1/tasks/{id}'s payload for p, without the task-type read
// check (the writer holds operations:manage_tasks, which reads the task it just wrote whatever its type).
func (s *Service) writtenTaskView(ctx context.Context, p *authz.Principal, id uuid.UUID) (any, error) {
	return s.taskView(ctx, p, id, false)
}

func (s *Service) taskView(ctx context.Context, p *authz.Principal, id uuid.UUID, checkType bool) (any, error) {
	d, err := s.directory(ctx, p)
	if err != nil {
		return nil, err
	}
	var base TaskBase
	var staff *Task
	if scoped(p) {
		var t scopedb.ScopeTask
		if err := s.scopeRead(ctx, p, func(q *scopedb.Queries) error {
			var err error
			t, err = q.GetScopeTask(ctx, id)
			return notFound(err)
		}); err != nil {
			return nil, err
		}
		if checkType && !mayReadTaskType(p, t.TaskType) {
			return nil, errTaskType(t.TaskType)
		}
		list, err := s.scopeTaskBases(ctx, p, d, []scopedb.ScopeTask{t})
		if err != nil {
			return nil, err
		}
		base = list[0]
	} else {
		detail, err := s.GetTask(ctx, p, id)
		if err != nil {
			return nil, err
		}
		if checkType && !mayReadTaskType(p, detail.Task.TaskType) {
			return nil, errTaskType(detail.Task.TaskType)
		}
		list, err := s.staffTaskViews(ctx, p, d, []Task{detail.Task})
		if err != nil {
			return nil, err
		}
		base, staff = list[0].TaskBase, &detail.Task
	}
	signed, err := s.sign(ctx, []uuid.UUID{deref(base.CheckInPhotoFileID), deref(base.CheckInAppFileID)})
	if err != nil {
		return nil, err
	}
	base.CheckInPhoto, base.CheckInApp = fileRef(signed, base.CheckInPhotoFileID), fileRef(signed, base.CheckInAppFileID)
	if staff == nil || driverPrincipal(p) {
		return base, nil
	}
	return taskView(base, *staff), nil
}

func deref(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

// TasksByNumberView is GET /v1/tasks/by-number/{taskNo}: every task of the reach that carries the number verbatim
// (legacy numbers are not unique), of the types p reads.
func (s *Service) TasksByNumberView(ctx context.Context, p *authz.Principal, taskNo string) (any, error) {
	taskNo = strings.TrimSpace(taskNo)
	if taskNo == "" {
		return nil, httpx.ErrNotFound()
	}
	d, err := s.directory(ctx, p)
	if err != nil {
		return nil, err
	}
	if scoped(p) {
		var rows []scopedb.ScopeTask
		if err := s.scopeRead(ctx, p, func(q *scopedb.Queries) error {
			var err error
			rows, err = q.ScopeTasksByNumber(ctx, taskNo)
			return err
		}); err != nil {
			return nil, err
		}
		rows = slices.DeleteFunc(rows, func(t scopedb.ScopeTask) bool { return !mayReadTaskType(p, t.TaskType) })
		return s.scopeTaskBases(ctx, p, d, rows)
	}
	rows, err := s.TasksByNumber(ctx, p, taskNo)
	if err != nil {
		return nil, err
	}
	rows = slices.DeleteFunc(rows, func(t Task) bool { return !mayReadTaskType(p, t.TaskType) })
	return s.staffTaskViews(ctx, p, d, rows)
}

// TaskTripView is GET /v1/tasks/{id}/trip: the newest trip of a task the caller reads, as GET /v1/trips/{id} answers
// it; 404 when the task has none (or is outside the reach).
func (s *Service) TaskTripView(ctx context.Context, p *authz.Principal, id uuid.UUID) (any, error) {
	var tripID uuid.UUID
	if scoped(p) {
		err := s.scopeRead(ctx, p, func(q *scopedb.Queries) error {
			t, err := q.GetScopeTask(ctx, id)
			if err != nil {
				return notFound(err)
			}
			if !mayReadTaskType(p, t.TaskType) {
				return errTaskType(t.TaskType)
			}
			trips, err := q.ScopeTripsOfTasks(ctx, []uuid.UUID{id})
			if err != nil {
				return err
			}
			if len(trips) == 0 {
				return httpx.ErrNotFound()
			}
			tripID = trips[0].ID
			return nil
		})
		if err != nil {
			return nil, err
		}
		return s.GetTripView(ctx, p, tripID)
	}
	r := ReachOf(p)
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		t, err := q.GetTask(ctx, operationsdb.GetTaskParams{ID: id, AllTenants: r.All, TenantIds: r.tenants(), ReachDriver: r.Driver})
		if err != nil {
			return notFound(err)
		}
		if !mayReadTaskType(p, t.TaskType) {
			return errTaskType(t.TaskType)
		}
		trips, err := q.TripsOfTasks(ctx, operationsdb.TripsOfTasksParams{TaskIds: []uuid.UUID{id}, AllTenants: r.All,
			TenantIds: r.tenants(), ReachDriver: r.Driver})
		if err != nil {
			return err
		}
		if len(trips) == 0 {
			return httpx.ErrNotFound()
		}
		tripID = trips[0].ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetTripView(ctx, p, tripID)
}

// OpenTaskStatuses are the statuses of a task still in a driver's queue: the ones that can block a successor at
// check-in (taskBlocksSuccessorInQueue, task_repository.dart:76-97). Completed and cancelled tasks never do.
var OpenTaskStatuses = []string{TaskPending, TaskAssigned, TaskCheckedIn, TaskInTransit}

// QueueTask is one task of a driver's queue with the instant it is queued at (ADR 0028: the actual pickup, else the
// plan day at its plan time).
type QueueTask struct {
	TaskBase
	QueueAt time.Time `json:"queueAt"`
}

// DriverQueueInput is the query of a driver's queue: statuses (none = OpenTaskStatuses), a page size (0 = MaxLimit)
// and the cursor of the previous page.
type DriverQueueInput struct {
	Statuses []string
	Limit    int
	Cursor   string
}

// queueCursor is the keyset position of a driver's queue: the ADR 0028 key of the last row of a page (queue instant,
// run order with none as 0, created_at, id).
type queueCursor struct {
	Axis    string    `json:"a"`
	At      time.Time `json:"t"`
	Run     int32     `json:"r"`
	Created time.Time `json:"c"`
	ID      uuid.UUID `json:"id"`
}

const axisQueue = "q"

func (c queueCursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeQueueCursor reads a queue cursor; "" is the first page. A cursor of another list is refused.
func decodeQueueCursor(s string) (*queueCursor, bool) {
	if s == "" {
		return nil, true
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	var c queueCursor
	if json.Unmarshal(b, &c) != nil || c.Axis != axisQueue || c.ID == uuid.Nil || c.At.IsZero() || c.Created.IsZero() ||
		c.Run < 0 {
		return nil, false
	}
	return &c, true
}

// DriverQueue is the driver's own queue (own and helper tasks, R28) in the queue order of ADR 0028, the service the
// driver route GET /v1/mobile/tasks (P7a, T57) answers with. Without statuses it is the open queue (OpenTaskStatuses:
// the tasks the check-in gate walks, all of them on one page up to MaxLimit); explicit statuses (history included)
// page in the same order. A keyset page: a longer queue gets NextCursor, never a silent cut (TW8).
func (s *Service) DriverQueue(ctx context.Context, p *authz.Principal, in DriverQueueInput) (Page[QueueTask], error) {
	if !driverPrincipal(p) {
		return Page[QueueTask]{}, authz.ErrPermissionDenied("driver only")
	}
	var ck checker
	st := statusList(&ck, "status", in.Statuses, TaskStatuses)
	if len(st) == 0 {
		st = OpenTaskStatuses
	}
	limit := in.Limit
	if limit == 0 {
		limit = MaxLimit
	} else if limit < 1 || limit > MaxLimit {
		ck.add("limit", "out_of_range")
	}
	after, ok := decodeQueueCursor(in.Cursor)
	if !ok {
		ck.add("cursor", "invalid")
	}
	if err := ck.err(); err != nil {
		return Page[QueueTask]{}, err
	}
	arg := operationsdb.DriverTasksParams{DriverID: *p.DriverID, Statuses: st, RowLimit: int32(limit + 1)}
	if after != nil {
		arg.AfterQueueAt, arg.AfterRunOrder, arg.AfterCreatedAt, arg.AfterID = &after.At, after.Run, after.Created, after.ID
	}
	var rows []operationsdb.DriverTasksRow
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		var err error
		rows, err = q.DriverTasks(ctx, arg)
		return err
	})
	if err != nil {
		return Page[QueueTask]{}, err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		c := queueCursor{Axis: axisQueue, At: last.QueueAt, Created: last.Task.CreatedAt, ID: last.Task.ID}
		if last.Task.RunOrder != nil {
			c.Run = *last.Task.RunOrder
		}
		next = c.encode()
	}
	tasks := make([]Task, len(rows))
	for i, r := range rows {
		tasks[i] = r.Task
	}
	d, err := s.directory(ctx, p)
	if err != nil {
		return Page[QueueTask]{}, err
	}
	views, err := s.staffTaskViews(ctx, p, d, tasks)
	if err != nil {
		return Page[QueueTask]{}, err
	}
	out := make([]QueueTask, len(views))
	for i, v := range views {
		out[i] = QueueTask{TaskBase: v.TaskBase, QueueAt: rows[i].QueueAt}
	}
	return Page[QueueTask]{Items: out, NextCursor: next}, nil
}
