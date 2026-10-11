package iam

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// EventTaskTenantReassigned is the security event of the R13 task reassignment (Appendix C §C.4.13), written in the
// move's transaction.
const EventTaskTenantReassigned = "task_tenant_reassigned"

// Outbox routing keys of a reassignment (Appendix B §B.2.11): task.reassigned when the driver changed (notify.fcm tells
// the previous driver it lost the task and the new one it got it), else task.updated (the silent tasks_changed push).
const (
	RouteTaskReassigned = "task.reassigned"
	RouteTaskUpdated    = "task.updated"
)

// details.reason of the 409s of a reassignment.
const (
	ReasonTaskHasTrips       = "task_has_trips"
	ReasonTaskNotOpen        = "task_not_open"
	ReasonTaskChanged        = "task_changed"
	ReasonDriverNotInTenant  = "driver_not_in_tenant"
	ReasonHelperNotInTenant  = "helper_not_in_tenant"
	ReasonTruckNotInTenant   = "truck_not_in_tenant"
	ReasonDriverInactive     = "driver_inactive"
	ReasonTargetTenantClosed = "tenant_inactive"
)

// TaskMoverDeps are the collaborators of the task reassignment.
type TaskMoverDeps struct {
	// Pool is the logitrack_app pool: the dispatcher's visibility check runs in db.WithPrincipal, the move in
	// db.WithSystem.
	Pool db.Beginner
	Log  zerolog.Logger
	Now  func() time.Time // optional, defaults to time.Now
}

// TaskMover is the R13 task reassignment (T28; Appendix C §C.1.7, §C.3.6), the service behind PATCH /v1/tasks/{id}
// with tenantId (operations.Reassigner). It lives with the other tenant-move paths that need a system transaction
// after Go authorization (the driver move, the quarantine re-home): once moved, the task is outside the dispatcher's
// RLS write reach, and the deferred move checks must see every child.
type TaskMover struct {
	pool db.Beginner
	log  zerolog.Logger
	now  func() time.Time
}

// NewTaskMover builds the reassignment.
func NewTaskMover(d TaskMoverDeps) (*TaskMover, error) {
	if d.Pool == nil {
		return nil, errors.New("iam: the task reassignment needs a pool")
	}
	m := &TaskMover{pool: d.Pool, log: d.Log, now: d.Now}
	if m.now == nil {
		m.now = time.Now
	}
	return m, nil
}

var _ operations.Reassigner = (*TaskMover)(nil)

// ReassignTask moves task taskID to in.TenantID and, when in.DriverSet, assigns in.DriverID there (nil: no driver), in
// one system transaction. operations.Service.ReassignTask has authorized the caller as a platform admin (in.Platform)
// or a dispatcher; a dispatcher must read the task (its own organisation's tasks, or the scope of its parties: RLS
// decides in a request transaction first; otherwise 404), and the task must not have changed tenant since (409
// task_changed). Rules, each refusing with nothing written:
//
//   - the target exists and is not the quarantine tenant (422 tenantId not_found) and is active (409 tenant_inactive);
//   - the task is open, pending or assigned (409 task_not_open), and no trip record references it (409 task_has_trips:
//     a trip's tenant is the work's, frozen when it was written);
//   - a new driver exists (422 driverId not_found), belongs to the target tenant (422 driverId not_in_tenant), is not
//     inactive (409 driver_inactive) and is not the task's helper (422 driverId same_as_helper); without driverId the
//     task's current driver must already belong to the target (409 driver_not_in_tenant: send a driver of the target,
//     or null);
//   - the helper and the truck the task keeps belong to the target or a tenant linked to it by contractor_tenant_id
//     either way (409 helper_not_in_tenant, truck_not_in_tenant; the helper rule of Appendix C §C.3.6).
//
// Then tenancy.MoveTask (app.tenant_move for that move only; tenant_source 'form'; standby records and files follow),
// the driver assignment (status pending <-> assigned, the queue position under the driver's run-order lock), one
// task_tenant_reassigned row (actor the caller, tenant the target; only when the tenant changed) and the outbox event
// (task.reassigned when the driver changed, else task.updated) on the tasks topics of both tenants, the task's parties
// and the drivers concerned. A request that changes nothing writes nothing.
func (m *TaskMover) ReassignTask(ctx context.Context, p *authz.Principal, rid string, taskID uuid.UUID, in operations.Reassign) (operations.Reassigned, error) {
	var seen *uuid.UUID
	if !in.Platform {
		var tenant uuid.UUID
		var found bool
		err := db.WithPrincipal(ctx, m.pool, p, func(tx pgx.Tx) error {
			var err error
			tenant, found, err = tenancy.TenantOf(ctx, tx, "tasks", taskID)
			return err
		})
		if err != nil {
			return operations.Reassigned{}, err
		}
		if !found {
			return operations.Reassigned{}, httpx.ErrNotFound()
		}
		seen = &tenant
	}
	var out operations.Reassigned
	err := db.WithSystem(ctx, m.pool, nil, func(tx pgx.Tx) error {
		ops, q := operationsdb.New(tx), iamdb.New(tx)
		task, err := ops.LockTask(ctx, taskID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if task.TenantID == uuid.MustParse(tenancy.QuarantineTenantID) {
			return httpx.ErrNotFound() // a quarantined row moves only through the re-home of a platform admin
		}
		if seen != nil && *seen != task.TenantID {
			return errFailedPrecondition(ReasonTaskChanged, "the task moved meanwhile; reload it and retry")
		}
		from, to := task.TenantID, in.TenantID
		target, err := q.TaskMoveTarget(ctx, to)
		switch {
		case errors.Is(err, pgx.ErrNoRows), err == nil && target.Kind == authz.TenantKindQuarantine:
			return errInvalid(violation("tenantId", "not_found"))
		case err != nil:
			return err
		case target.Status != "active" && from != to:
			return errFailedPrecondition(ReasonTargetTenantClosed, "the target tenant is not active").
				WithDetails(map[string]any{"reason": ReasonTargetTenantClosed, "status": target.Status})
		}
		if task.Status != operations.TaskPending && task.Status != operations.TaskAssigned {
			return errFailedPrecondition(ReasonTaskNotOpen, "only a pending or assigned task is reassigned").
				WithDetails(map[string]any{"reason": ReasonTaskNotOpen, "status": task.Status})
		}
		driver := task.DriverID
		if in.DriverSet {
			driver = in.DriverID
		}
		if err := m.checkPeople(ctx, q, task, in, driver, to); err != nil {
			return err
		}
		moved := from != to
		prev := cloneID(task.DriverID)
		driverChanged := !sameID(driver, prev)
		if !moved && !driverChanged {
			out = reassigned(task, from, nil, false)
			return nil
		}
		if moved {
			files, err := tenancy.MoveTask(ctx, tx, taskID, from, to)
			switch {
			case errors.Is(err, tenancy.ErrTaskHasTrips):
				return errFailedPrecondition(ReasonTaskHasTrips, "the task already has a trip record; its tenant is the trip's")
			case errors.Is(err, tenancy.ErrBadTarget):
				return errInvalid(violation("tenantId", "not_found"))
			case err != nil:
				return err
			}
			task.TenantID, task.TenantSource = to, string(tenancy.SourceForm)
			if err := m.audit(ctx, tx, p, rid, task, from, driver, prev, files); err != nil {
				return err
			}
		}
		if driverChanged {
			if task, err = assignDriver(ctx, ops, task, driver); err != nil {
				return err
			}
		}
		if !driverChanged {
			prev = nil
		}
		out = reassigned(task, from, prev, moved)
		return m.emit(ctx, tx, q, p, rid, task, from, out)
	})
	if err != nil {
		return operations.Reassigned{}, operations.MapError(err)
	}
	return out, nil
}

// checkPeople applies the driver, helper and truck rules of a reassignment to the target tenant.
func (m *TaskMover) checkPeople(ctx context.Context, q *iamdb.Queries, task operationsdb.Task, in operations.Reassign,
	driver *uuid.UUID, to uuid.UUID) error {
	if in.DriverSet && driver != nil && !sameID(driver, task.DriverID) {
		d, err := q.TaskMoveDriver(ctx, *driver)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return errInvalid(violation("driverId", "not_found"))
		case err != nil:
			return err
		case d.TenantID != to:
			return errInvalid(violation("driverId", "not_in_tenant"))
		case d.Status == "inactive":
			return errFailedPrecondition(ReasonDriverInactive, "the driver is inactive")
		case sameID(driver, task.HelperDriverID):
			return errInvalid(violation("driverId", "same_as_helper"))
		}
	} else if driver != nil {
		d, err := q.TaskMoveDriver(ctx, *driver)
		if err != nil {
			return err
		}
		if d.TenantID != to {
			return errFailedPrecondition(ReasonDriverNotInTenant,
				"the task's driver does not belong to the target tenant; send driverId (a driver of the target, or null)")
		}
	}
	if task.HelperDriverID != nil {
		h, err := q.TaskMoveDriver(ctx, *task.HelperDriverID)
		if err != nil {
			return err
		}
		linked, err := q.TenantsLinked(ctx, iamdb.TenantsLinkedParams{A: h.TenantID, B: to})
		if err != nil {
			return err
		}
		if !linked {
			return errFailedPrecondition(ReasonHelperNotInTenant, "the task's helper belongs to a tenant unrelated to the target")
		}
	}
	if task.TruckID != nil {
		tt, err := q.TaskMoveTruck(ctx, *task.TruckID)
		if err != nil {
			return err
		}
		linked, err := q.TenantsLinked(ctx, iamdb.TenantsLinkedParams{A: tt, B: to})
		if err != nil {
			return err
		}
		if !linked {
			return errFailedPrecondition(ReasonTruckNotInTenant, "the task's truck belongs to a tenant unrelated to the target")
		}
	}
	return nil
}

// assignDriver writes the new driver of a task (nil: none): an assigned task without a driver goes back to pending and
// leaves the queue; a pending task with a driver becomes assigned and takes the end of the driver's queue under the
// driver's run-order lock (operationsdb.LockDriverQueue, which every run_order writer takes, developer-spec.md §3.6).
// The legacy driver reference no longer describes the row once Go chooses the driver, so it is cleared.
func assignDriver(ctx context.Context, ops *operationsdb.Queries, task operationsdb.Task, driver *uuid.UUID) (operationsdb.Task, error) {
	status, runOrder := operations.TaskPending, (*int32)(nil)
	if driver != nil {
		status = operations.TaskAssigned
		if err := ops.LockDriverQueue(ctx, *driver); err != nil {
			return task, err
		}
		n, err := ops.NextRunOrder(ctx, *driver)
		if err != nil {
			return task, err
		}
		runOrder = &n
	}
	return ops.UpdateTask(ctx, operationsdb.UpdateTaskParams{
		JobCategory: task.JobCategory, Status: status, PlanAt: task.PlanAt, PlanTime: task.PlanTime,
		ActualPickupAt: task.ActualPickupAt, SourceHubRaw: task.SourceHubRaw, SourceHubID: task.SourceHubID,
		SourceLinkedPartyID: task.SourceLinkedPartyID, DestinationRaw: task.DestinationRaw,
		DestinationHubID: task.DestinationHubID, DestinationSocKey: task.DestinationSocKey,
		DestinationLinkedPartyID: task.DestinationLinkedPartyID, BillingPartyID: task.BillingPartyID,
		TruckID: task.TruckID, TruckType: task.TruckType, LegacyTruckType: task.LegacyTruckType,
		LicensePlateSnapshot: task.LicensePlateSnapshot, DriverID: driver, HelperDriverID: task.HelperDriverID,
		RunOrder: runOrder, IsMultiDelivery: task.IsMultiDelivery, ID: task.ID,
	})
}

// audit appends task_tenant_reassigned in the move's transaction: actor the caller, tenant the target (the staff of the
// tenant that now runs the task read it; platform principals read every row).
func (m *TaskMover) audit(ctx context.Context, tx pgx.Tx, p *authz.Principal, rid string, task operationsdb.Task,
	from uuid.UUID, driver, prev *uuid.UUID, files []tenancy.Moved) error {
	to, actor := task.TenantID, p.UserID
	nStandby, nFiles := 0, 0
	for _, mv := range files {
		if mv.Table == tenancy.FileObjects {
			nFiles++
		} else {
			nStandby++
		}
	}
	details := map[string]any{"taskId": task.ID, "taskNo": task.TaskNo, "from": from, "to": to, "driverId": driver,
		"previousDriverId": prev, "by": by(p), "standbyRecords": nStandby, "files": nFiles}
	return security.Append(ctx, tx, security.Event{
		EventType: EventTaskTenantReassigned, Severity: security.SeverityWarning,
		Summary: "task moved to another tenant", Details: details, ActorUserID: &actor, TenantID: &to, RequestID: rid,
		OccurredAt: m.now().UTC().Truncate(time.Microsecond),
	})
}

// by names the authority the caller used: dispatcher or platform_admin.
func by(p *authz.Principal) string {
	if p.HasPlatform(authz.PlatformAdmin) && p.Can(authz.PlatformCrossTenantWrite) {
		return string(authz.PlatformAdmin)
	}
	return "dispatcher"
}

// emit appends the task event of the reassignment: on the tasks topics of the former and the new tenant, of every
// party of the task (the dispatcher boards) and of the drivers concerned (their queues).
func (m *TaskMover) emit(ctx context.Context, tx pgx.Tx, q *iamdb.Queries, p *authz.Principal, rid string,
	task operationsdb.Task, from uuid.UUID, out operations.Reassigned) error {
	parties, err := q.TaskPartyIDs(ctx, task.ID)
	if err != nil {
		return err
	}
	topics := []string{realtime.TenantTopic(task.TenantID.String(), realtime.FamilyTasks)}
	if from != task.TenantID {
		topics = append(topics, realtime.TenantTopic(from.String(), realtime.FamilyTasks))
	}
	for _, party := range parties {
		topics = append(topics, realtime.DispatchTopic(party.String(), realtime.FamilyTasks))
	}
	for _, d := range []*uuid.UUID{out.DriverID, out.PreviousDriverID, task.HelperDriverID} {
		if d != nil && !slices.Contains(topics, realtime.DriverTopic(d.String())) {
			topics = append(topics, realtime.DriverTopic(d.String()))
		}
	}
	changed := []string{}
	if out.Moved {
		changed = append(changed, "tenantId")
	}
	route := RouteTaskUpdated
	payload := map[string]any{"entity": "task", "id": task.ID, "tenantId": task.TenantID, "at": m.now().UTC(),
		"taskType": task.TaskType, "planDate": planDate(task)}
	if !sameID(out.DriverID, out.PreviousDriverID) {
		route = RouteTaskReassigned
		changed = append(changed, "driverId", "status")
		payload["driverId"], payload["previousDriverId"] = out.DriverID, out.PreviousDriverID
	}
	payload["changed"] = changed
	return emitTopics(call{ctx: ctx, p: p, rid: rid}, tx, route, "task", task.ID.String(), &task.TenantID, payload, topics)
}

func planDate(t operationsdb.Task) string {
	if t.PlanDate == nil {
		return ""
	}
	return t.PlanDate.Format(time.DateOnly)
}

func reassigned(t operationsdb.Task, from uuid.UUID, prev *uuid.UUID, moved bool) operations.Reassigned {
	return operations.Reassigned{ID: t.ID, TaskNo: t.TaskNo, TenantID: t.TenantID, TenantSource: t.TenantSource,
		PreviousTenantID: from, Status: t.Status, DriverID: t.DriverID, RunOrder: t.RunOrder, Moved: moved,
		PreviousDriverID: prev}
}

func sameID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func cloneID(id *uuid.UUID) *uuid.UUID {
	if id == nil {
		return nil
	}
	v := *id
	return &v
}
