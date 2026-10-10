package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotQuarantined: Rehome moves only rows of the quarantine tenant.
var ErrNotQuarantined = errors.New("tenancy: the row is not in the quarantine tenant")

// ErrBadTarget: the target tenant does not exist or is the quarantine tenant itself.
var ErrBadTarget = errors.New("tenancy: the target tenant does not exist or is the quarantine tenant")

// child is a table whose rows follow a parent through a link column (the tenant-consistency triggers of
// 0004_operations: trip <- task; standby <- task, else trip; incident <- trip).
type child struct {
	table, link, extra string
	source             Source
}

// rehomeTables are the tenant-stamped tables a quarantined row can live in, with the children a move takes
// along. Identifiers come only from this map.
var rehomeTables = map[string][]child{
	"tasks": {
		{table: "trip_records", link: "task_id", source: SourceTask},
		{table: "standby_records", link: "task_id", source: SourceTask},
	},
	"trip_records": {
		{table: "incident_reports", link: "trip_id", source: SourceTrip},
		{table: "standby_records", link: "trip_id", extra: " AND task_id IS NULL", source: SourceTrip},
	},
	"standby_records": nil, "incident_reports": nil, "drivers": nil, "trucks": nil, "companies": nil,
	"vehicle_expenses": nil, "maintenance_records": nil, "truck_assignments": nil, "driver_penalties": nil,
	"payroll_runs": nil, "leave_requests": nil, "chats": nil,
}

// RehomeTables lists the tables Rehome accepts.
func RehomeTables() []string {
	out := make([]string, 0, len(rehomeTables))
	for t := range rehomeTables {
		out = append(out, t)
	}
	return out
}

// Moved is one row a re-home moved.
type Moved struct {
	Table string
	ID    uuid.UUID
}

// Rehome moves one quarantined row to tenant `to` with its children, inside the caller's transaction
// (Appendix C §C.3.10, the service of POST /v1/tenants/quarantine/rows/{table}/{id}/rehome and of
// `etl quarantine resolve --action=rehome`). It turns on app.tenant_move for this transaction only, so the
// frozen-tenant trigger lets exactly this move through; the deferred link triggers re-check parents and
// children at COMMIT (mobile_installations and vehicle_locations have no quarantine rows: unresolved ones are
// rejected). The row takes tenant_source 'form' (an explicit platform-admin decision, like an API
// row taking the caller's chosen tenant); a child takes the link it follows ('task' or 'trip'). The caller
// writes the audit record (tenant_rehomed). A table whose CHECK needs a driver or truck for a non-quarantine
// row (chats, payroll, maintenance without a truck, ...) refuses the move with that CHECK.
func Rehome(ctx context.Context, tx pgx.Tx, table string, id, to uuid.UUID) ([]Moved, error) {
	if _, ok := rehomeTables[table]; !ok {
		return nil, fmt.Errorf("tenancy: %s is not a tenant-stamped table Rehome supports", table)
	}
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
	q := uuid.MustParse(QuarantineTenantID)
	ident := pgx.Identifier{table}.Sanitize()
	tag, err := tx.Exec(ctx, `UPDATE `+ident+` SET tenant_id = $1, tenant_source = 'form' WHERE id = $2 AND tenant_id = $3`, to, id, q)
	if err != nil {
		return nil, fmt.Errorf("tenancy: move %s %s: %w", table, id, err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotQuarantined
	}
	moved := []Moved{{Table: table, ID: id}}
	return moveChildren(ctx, tx, table, id, to, q, moved)
}

func moveChildren(ctx context.Context, tx pgx.Tx, table string, id, to, q uuid.UUID, moved []Moved) ([]Moved, error) {
	for _, c := range rehomeTables[table] {
		rows, err := tx.Query(ctx, `UPDATE `+pgx.Identifier{c.table}.Sanitize()+` SET tenant_id = $1, tenant_source = $2
			WHERE `+pgx.Identifier{c.link}.Sanitize()+` = $3 AND tenant_id = $4`+c.extra+` RETURNING id`, to, string(c.source), id, q)
		if err != nil {
			return nil, fmt.Errorf("tenancy: move children %s of %s %s: %w", c.table, table, id, err)
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return nil, fmt.Errorf("tenancy: move children %s of %s %s: %w", c.table, table, id, err)
		}
		for _, cid := range ids {
			moved = append(moved, Moved{Table: c.table, ID: cid})
			if moved, err = moveChildren(ctx, tx, c.table, cid, to, q, moved); err != nil {
				return nil, err
			}
		}
	}
	return moved, nil
}
