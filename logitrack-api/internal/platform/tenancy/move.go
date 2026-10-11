package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotInTenant: MoveDriver or MoveTask found the row outside the tenant the caller locked it in.
var ErrNotInTenant = errors.New("tenancy: the row is not in the tenant it is moved from")

// ErrTaskHasTrips: MoveTask refuses a task that trip records reference (Appendix C §C.3.6: the R13 reassignment moves a
// call-out before it runs; a trip's tenant is the work's, frozen when it was written).
var ErrTaskHasTrips = errors.New("tenancy: the task has trip records")

// MoveTask is the R13 reassignment of a task to tenant `to` (Appendix C §C.1.7, §C.3.6) inside the caller's
// transaction: the task service runs it in a system transaction after Go authorization, with the task row locked, and
// writes task_tenant_reassigned in the same transaction. A task that trip records reference is refused
// (ErrTaskHasTrips), and so is a target that does not exist or is the quarantine tenant (ErrBadTarget). It turns on
// app.tenant_move for these UPDATEs only (and off again), so the frozen-tenant trigger lets exactly this move through.
// The task takes tenant_source 'form' (the tenant an authorised caller chose, R13); the standby records that follow it
// (task_id, the deferred standby_records_tenant_check) move with it and take 'task'. The files the task and those
// records own follow while they are in the former tenant; a key another tenant registered keeps its tenant. The
// result lists the moved standby records, then the moved files, not the task.
func MoveTask(ctx context.Context, tx pgx.Tx, id, from, to uuid.UUID) ([]Moved, error) {
	var kind string
	err := tx.QueryRow(ctx, `SELECT kind FROM tenants WHERE id = $1`, to).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == "quarantine") {
		return nil, ErrBadTarget
	}
	if err != nil {
		return nil, fmt.Errorf("tenancy: read target tenant: %w", err)
	}
	var trips bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM trip_records WHERE task_id = $1)`, id).Scan(&trips); err != nil {
		return nil, fmt.Errorf("tenancy: trips of task %s: %w", id, err)
	}
	if trips {
		return nil, ErrTaskHasTrips
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_move', 'on', true)`); err != nil {
		return nil, fmt.Errorf("tenancy: enable the tenant move: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE tasks SET tenant_id = $1, tenant_source = 'form' WHERE id = $2 AND tenant_id = $3`, to, id, from)
	if err != nil {
		return nil, fmt.Errorf("tenancy: move task %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotInTenant
	}
	rows, err := tx.Query(ctx, `UPDATE standby_records SET tenant_id = $1, tenant_source = 'task'
		WHERE task_id = $2 AND tenant_id = $3 RETURNING id`, to, id, from)
	if err != nil {
		return nil, fmt.Errorf("tenancy: move the standby records of task %s: %w", id, err)
	}
	standby, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("tenancy: move the standby records of task %s: %w", id, err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_move', 'off', true)`); err != nil {
		return nil, fmt.Errorf("tenancy: disable the tenant move: %w", err)
	}
	owners := []Moved{{Table: "tasks", ID: id}}
	out := make([]Moved, 0, len(standby))
	for _, s := range standby {
		out = append(out, Moved{Table: "standby_records", ID: s})
		owners = append(owners, Moved{Table: "standby_records", ID: s})
	}
	files, err := moveFilesBetween(ctx, tx, owners, to, from)
	if err != nil {
		return nil, err
	}
	return append(out, files...), nil
}

// MoveDriver is the tenant move of a driver (the broker-carrier move, Appendix C §C.1.4, glossary "Broker carrier")
// inside the caller's transaction: iam.Admin.MoveDriver runs it in a system transaction after Go authorization,
// with the driver row locked, and moves the driver membership and writes driver_tenant_moved in the same
// transaction. It turns on app.tenant_move for this move only (and off again), so the frozen-tenant trigger lets
// exactly this UPDATE through; the driver keeps tenant_source 'self' (a driver's tenant is its own carrier). The files
// the driver owns (profile, ID card, licence) follow it while they are in its former tenant, so the new tenant's
// staff read them under the staff rule of GET /v1/files and the former tenant's no longer do; a key another tenant
// registered keeps its tenant. Rows that reference the driver keep their frozen tenant (R24: the driver still reads
// its own history by driver_id). It returns the moved files.
func MoveDriver(ctx context.Context, tx pgx.Tx, id, from, to uuid.UUID) ([]Moved, error) {
	var kind string
	err := tx.QueryRow(ctx, `SELECT kind FROM tenants WHERE id = $1`, to).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == "quarantine") {
		return nil, ErrBadTarget
	}
	if err != nil {
		return nil, fmt.Errorf("tenancy: read target tenant: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_move', 'on', true)`); err != nil {
		return nil, fmt.Errorf("tenancy: enable the tenant move: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE drivers SET tenant_id = $1 WHERE id = $2 AND tenant_id = $3`, to, id, from)
	if err != nil {
		return nil, fmt.Errorf("tenancy: move driver %s: %w", id, err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_move', 'off', true)`); err != nil {
		return nil, fmt.Errorf("tenancy: disable the tenant move: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotInTenant
	}
	rows, err := tx.Query(ctx, `UPDATE file_objects SET tenant_id = $1
		WHERE owner_kind = 'driver' AND owner_id = $2 AND tenant_id = $3 RETURNING id`, to, id, from)
	if err != nil {
		return nil, fmt.Errorf("tenancy: move the files of driver %s: %w", id, err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("tenancy: move the files of driver %s: %w", id, err)
	}
	out := make([]Moved, 0, len(ids))
	for _, f := range ids {
		out = append(out, Moved{Table: FileObjects, ID: f})
	}
	return out, nil
}
