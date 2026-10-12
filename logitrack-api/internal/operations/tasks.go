package operations

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// TaskInsert are the columns of a new task (operationsdb.InsertTaskParams). TaskNo nil: CreateTask allocates the
// number (R10), the case of every runtime create (POST /v1/tasks, the import, whose numbers are sequenced server-side,
// and the driver's manual task); a TaskNo is a number of record kept verbatim (a legacy or mirrored number). RunOrder
// nil with a driver: CreateTask puts the task at the end of the driver's queue.
type TaskInsert = operationsdb.InsertTaskParams

// NewTask is a task to create, with the planned stops of a multi-drop task (stop 1 first).
type NewTask struct {
	TaskInsert
	Stops []NewStop
}

// NewStop is one planned delivery stop.
type NewStop struct {
	Destination              string
	DestinationHubID         *uuid.UUID
	DestinationLinkedPartyID *uuid.UUID
	SourceID                 *string
	IsCustom                 bool
}

// CreateTask inserts a task in the caller's transaction (tx: a request transaction of the writer, or a system one):
//
//   - the number of record: next_task_seq() for (task_type, Bangkok plan day), formatted FM|LH-ddMMyyyy-NNN (R10);
//     the counter row stays locked until the transaction ends, so concurrent creations of one type and day get
//     distinct numbers. A task that carries its number (a legacy or mirrored number of record, kept verbatim, never
//     re-formatted) allocates nothing; when that number is canonical (ParseTaskNo: padded, at least three digits) it
//     moves the counter of the (type, day) the number names up to its sequence (bump_task_seq(), under the same row
//     lock), so the allocator never mints it again. Unpadded legacy numbers do not move the counter (Appendix A
//     §A.3.5);
//   - the queue position of an assigned task without one: max(run_order) + 1 of the driver's tasks, under the
//     per-driver transaction advisory lock LockDriverQueue (two assignments to one driver never share a position;
//     every run_order writer takes the same lock, developer-spec.md §3.6);
//   - the planned stops of a multi-drop task (at least two, destinations unique after trimming and upper-casing,
//     createOrUpdateTask tasks.ts:103-131) as stop 1..n. Stops make the task multi-drop, flag or not.
//
// A driver's manual task with a client_op_id is idempotent per (driver_id, client_op_id) (R63): a replay returns the
// stored task with replayed = true and allocates nothing. The caller stamps the tenant first (Stamp, T28); RLS and the
// deferred tenant triggers still decide at write and at COMMIT.
func CreateTask(ctx context.Context, tx pgx.Tx, in NewTask) (task Task, replayed bool, err error) {
	if err := in.validate(); err != nil {
		return Task{}, false, err
	}
	q := operationsdb.New(tx)
	if in.ClientOpID != nil && in.DriverID != nil {
		t, err := q.TaskByClientOp(ctx, operationsdb.TaskByClientOpParams{DriverID: *in.DriverID, ClientOpID: *in.ClientOpID})
		switch {
		case err == nil:
			return t, true, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return Task{}, false, MapError(err)
		}
	}
	p := in.TaskInsert
	if p.TaskNo == nil {
		day := PlanDate(p.PlanAt)
		seq, err := q.NextTaskSeq(ctx, operationsdb.NextTaskSeqParams{TaskType: p.TaskType, PlanDate: day})
		if err != nil {
			return Task{}, false, seqError(err)
		}
		p.TaskNo = ptr(FormatTaskNo(p.TaskType, day, int(seq)))
	} else if typ, day, seq, padded, ok := ParseTaskNo(*p.TaskNo); ok && padded && seq <= MaxBumpSeq {
		if _, err := q.BumpTaskSeq(ctx, operationsdb.BumpTaskSeqParams{TaskType: typ, PlanDate: day, Seq: int32(seq)}); err != nil {
			return Task{}, false, seqError(err)
		}
	}
	if p.DriverID != nil && p.RunOrder == nil {
		if err := q.LockDriverQueue(ctx, *p.DriverID); err != nil {
			return Task{}, false, MapError(err)
		}
		n, err := q.NextRunOrder(ctx, *p.DriverID)
		if err != nil {
			return Task{}, false, MapError(err)
		}
		p.RunOrder = &n
	}
	if len(in.Stops) > 0 {
		p.IsMultiDelivery = true
	}
	task, err = q.InsertTask(ctx, p)
	if errors.Is(err, pgx.ErrNoRows) && p.ClientOpID != nil && p.DriverID != nil {
		// A concurrent replay won the (driver_id, client_op_id) key between the lookup and the insert.
		t, err := q.TaskByClientOp(ctx, operationsdb.TaskByClientOpParams{DriverID: *p.DriverID, ClientOpID: *p.ClientOpID})
		return t, err == nil, MapError(err)
	}
	if err != nil {
		return Task{}, false, MapError(err)
	}
	for i, st := range in.Stops {
		if _, err := q.InsertTaskStop(ctx, operationsdb.InsertTaskStopParams{
			TaskID: task.ID, StopIndex: int32(i + 1), DestinationRaw: strings.TrimSpace(st.Destination),
			DestinationHubID: st.DestinationHubID, DestinationLinkedPartyID: st.DestinationLinkedPartyID,
			Sequence: ptr(int32(i)), SourceID: st.SourceID, IsCustom: st.IsCustom,
		}); err != nil {
			return Task{}, false, MapError(err)
		}
	}
	return task, false, nil
}

func (in NewTask) validate() error {
	var ck checker
	if !slices.Contains(TaskTypes, in.TaskType) {
		ck.add("taskType", "invalid")
	}
	if !slices.Contains(TaskStatuses, in.Status) {
		ck.add("status", "invalid")
	}
	if !slices.Contains(TenantSources, in.TenantSource) {
		ck.add("tenantSource", "invalid")
	}
	if in.TenantID == uuid.Nil {
		ck.add("tenantId", "required")
	}
	if in.PlanAt.IsZero() {
		ck.add("date", "required")
	}
	if strings.TrimSpace(in.SourceHubRaw) == "" {
		ck.add("sourceHub", "required")
	}
	if strings.TrimSpace(in.DestinationRaw) == "" {
		ck.add("destination", "required")
	}
	if in.JobCategory != nil && !slices.Contains(JobCategories, *in.JobCategory) {
		ck.add("jobCategory", "invalid")
	}
	if in.TruckType != nil && !slices.Contains(VehicleClasses, *in.TruckType) {
		ck.add("truckType", "invalid")
	}
	if in.TaskNo != nil && strings.TrimSpace(*in.TaskNo) == "" {
		ck.add("taskNo", "invalid")
	}
	if in.HelperDriverID != nil && in.DriverID != nil && *in.HelperDriverID == *in.DriverID {
		ck.add("helperDriverId", "same_as_driver") // ADR 0011: a helper is never the driver
	}
	if in.RunOrder != nil && *in.RunOrder < 1 {
		ck.add("runOrder", "out_of_range")
	}
	if (in.IsMultiDelivery || len(in.Stops) > 0) && len(in.Stops) < 2 {
		ck.add("deliveryStops", "too_few") // tasks.ts:103-110: two stops or more; planned stops make the task multi-drop
	}
	seen := map[string]bool{}
	for _, st := range in.Stops {
		d := normalizeDestination(st.Destination)
		switch {
		case d == "":
			ck.add("deliveryStops.destination", "required")
		case seen[d]:
			return ErrDuplicateDestination(strings.TrimSpace(st.Destination))
		}
		seen[d] = true
	}
	return ck.err()
}

// AddTaskStop adds a delivery stop to a task (addDeliveryStop, multiDeliveryTrips.ts:341-460) in the caller's
// transaction: the task is locked, a single-stop task is first given its own destination as stop 1 (and becomes
// multi-drop), a destination the task already has (trimmed, upper case) is 409 duplicate_destination, and the new stop
// takes the next index. The destination is stored trimmed and upper-cased, as the callable stored it. It returns
// every stop of the task.
func AddTaskStop(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, st NewStop, at time.Time) ([]TaskStop, error) {
	dest := normalizeDestination(st.Destination)
	if dest == "" {
		return nil, httpx.ErrInvalidArgument(violation("destination", "required"))
	}
	q := operationsdb.New(tx)
	task, err := q.LockTask(ctx, taskID)
	if err != nil {
		return nil, MapError(notFound(err))
	}
	stops, err := q.TaskStops(ctx, taskID)
	if err != nil {
		return nil, MapError(err)
	}
	if len(stops) == 0 {
		primary := normalizeDestination(task.DestinationRaw)
		if primary == "" {
			return nil, errPrecondition(ReasonNoPrimaryDestination, "the task has no destination to keep as stop 1")
		}
		first, err := q.InsertTaskStop(ctx, operationsdb.InsertTaskStopParams{
			TaskID: taskID, StopIndex: 1, DestinationRaw: primary, DestinationHubID: task.DestinationHubID,
			DestinationLinkedPartyID: task.DestinationLinkedPartyID, Sequence: ptr(int32(0)),
		})
		if err != nil {
			return nil, MapError(err)
		}
		stops = append(stops, first)
	}
	next := int32(0)
	for _, s := range stops {
		if normalizeDestination(s.DestinationRaw) == dest {
			return nil, ErrDuplicateDestination(dest)
		}
		next = max(next, s.StopIndex)
	}
	added, err := q.InsertTaskStop(ctx, operationsdb.InsertTaskStopParams{
		TaskID: taskID, StopIndex: next + 1, DestinationRaw: dest, DestinationHubID: st.DestinationHubID,
		DestinationLinkedPartyID: st.DestinationLinkedPartyID, Sequence: ptr(int32(len(stops))),
		AddedAt: &at, SourceID: trimmed(st.SourceID), IsCustom: st.IsCustom,
	})
	if err != nil {
		return nil, MapError(err)
	}
	if err := q.SetTaskMultiDelivery(ctx, taskID); err != nil {
		return nil, MapError(err)
	}
	return append(stops, added), nil
}

func trimmed(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}

// --- reads ----------------------------------------------------------------------------------------------------

// TaskDetail is a task with its planned stops.
type TaskDetail struct {
	Task  Task
	Stops []TaskStop
}

// GetTask reads one task of the principal's reach (staff, or the task's driver or helper) with its stops; a task
// outside the reach is 404.
func (s *Service) GetTask(ctx context.Context, p *authz.Principal, id uuid.UUID) (TaskDetail, error) {
	r := ReachOf(p)
	var out TaskDetail
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		t, err := q.GetTask(ctx, operationsdb.GetTaskParams{ID: id, AllTenants: r.All, TenantIds: r.tenants(), ReachDriver: r.Driver})
		if err != nil {
			return notFound(err)
		}
		out.Task = t
		out.Stops, err = q.TaskStops(ctx, id)
		return err
	})
	return out, err
}

// TasksByNumber is every task of the reach that carries the number verbatim (legacy numbers are not unique).
func (s *Service) TasksByNumber(ctx context.Context, p *authz.Principal, taskNo string) ([]Task, error) {
	r := ReachOf(p)
	var out []Task
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		var err error
		out, err = q.TasksByNumber(ctx, operationsdb.TasksByNumberParams{TaskNo: taskNo, AllTenants: r.All,
			TenantIds: r.tenants(), ReachDriver: r.Driver})
		return err
	})
	return out, err
}

// TaskFilter is the query of GET /v1/tasks: with a plan-day range (From, To: Bangkok days, both inclusive; a single
// day sets both) the list runs on the plan axis, latest day first; without one on created_at, newest first.
type TaskFilter struct {
	TaskType string
	From, To *time.Time
	Statuses []string
	DriverID *uuid.UUID // the task's driver or helper
	TruckID  *uuid.UUID
	Q        string
	Limit    int
	Cursor   string
	// source and destination are the place filters of GET /v1/tasks (T34: the boards' source-hub and SOC filters),
	// parsed against the request's hub master (directory.parsePlace); zero: no filter.
	source, destination placeFilter
}

// ListTasks is a keyset page of the principal's staff reach (a driver reads its queue through DriverTasks).
func (s *Service) ListTasks(ctx context.Context, p *authz.Principal, f TaskFilter) (Page[Task], error) {
	var ck checker
	limit := pageLimit(&ck, f.Limit)
	if f.TaskType != "" && !slices.Contains(TaskTypes, f.TaskType) {
		ck.add("taskType", "invalid")
	}
	for _, st := range f.Statuses {
		if !slices.Contains(TaskStatuses, st) {
			ck.add("status", "invalid")
			break
		}
	}
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
		return Page[Task]{}, err
	}
	r := ReachOf(p)
	var rows []Task
	err := s.read(ctx, p, func(q *operationsdb.Queries) error {
		var err error
		if byPlan {
			rows, err = q.ListTasksByPlan(ctx, operationsdb.ListTasksByPlanParams{
				AllTenants: r.All, TenantIds: r.tenants(), FromDate: *f.From, ToDate: *f.To,
				TaskType: nonEmpty(f.TaskType), Statuses: f.Statuses, DriverID: f.DriverID, TruckID: f.TruckID,
				Q: nonEmpty(f.Q), TrimChars: trimChars, SourceOn: f.source.on, SourceHubs: emptyIDs(f.source.hubs),
				SourceSoc: f.source.soc, SourceRaw: f.source.raw, SourceNone: f.source.none, DestinationOn: f.destination.on,
				DestinationHubs: emptyIDs(f.destination.hubs), DestinationSoc: f.destination.soc,
				DestinationRaw: f.destination.raw, DestinationNone: f.destination.none,
				AfterPlan: c.day(), AfterID: c.id(), RowLimit: int32(limit + 1),
			})
			return err
		}
		rows, err = q.ListTasksByCreated(ctx, operationsdb.ListTasksByCreatedParams{
			AllTenants: r.All, TenantIds: r.tenants(), TaskType: nonEmpty(f.TaskType), Statuses: f.Statuses,
			DriverID: f.DriverID, TruckID: f.TruckID, Q: nonEmpty(f.Q), TrimChars: trimChars, SourceOn: f.source.on,
			SourceHubs: emptyIDs(f.source.hubs), SourceSoc: f.source.soc, SourceRaw: f.source.raw,
			SourceNone: f.source.none, DestinationOn: f.destination.on, DestinationHubs: emptyIDs(f.destination.hubs),
			DestinationSoc: f.destination.soc, DestinationRaw: f.destination.raw, DestinationNone: f.destination.none,
			AfterCreated: c.at(), AfterID: c.id(), RowLimit: int32(limit + 1),
		})
		return err
	})
	if err != nil {
		return Page[Task]{}, err
	}
	return page(rows, limit, func(t Task) cursor {
		if byPlan {
			return cursor{Axis: axisPlan, Day: t.PlanDate.Format(time.DateOnly), ID: t.ID}
		}
		return cursor{Axis: axisCreated, At: ptr(t.CreatedAt), ID: t.ID}
	}), nil
}

func nonEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}
