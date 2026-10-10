package tenancy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

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

// Moved is one row a re-home moved: a tenant-stamped row or, with Table FileObjects, a file it owns.
type Moved struct {
	Table string
	ID    uuid.UUID
}

// FileObjects is the Moved.Table of a file that followed its owner.
const FileObjects = "file_objects"

// fileOwnerKinds are the file_objects.owner_kind values of the rows Rehome moves (storage.Owner*, the CHECK of
// 0002_identity); truck_assignments and payroll_runs own no file.
var fileOwnerKinds = map[string]string{
	"tasks": "task", "trip_records": "trip", "standby_records": "standby", "incident_reports": "incident",
	"drivers": "driver", "trucks": "truck", "companies": "company", "vehicle_expenses": "expense",
	"maintenance_records": "maintenance", "driver_penalties": "penalty", "leave_requests": "leave", "chats": "chat",
}

// Rehome moves one quarantined row to tenant `to` with its children and their files, inside the caller's
// transaction (Appendix C §C.3.10, the service of POST /v1/tenants/quarantine/rows/{table}/{id}/rehome and of
// `etl quarantine resolve --action=rehome`). It turns on app.tenant_move for this transaction only, so the
// frozen-tenant trigger lets exactly this move through; the deferred link triggers re-check parents and
// children at COMMIT (mobile_installations and vehicle_locations have no quarantine rows: unresolved ones are
// rejected). The row takes tenant_source 'form' (an explicit platform-admin decision, like an API
// row taking the caller's chosen tenant); a child takes the link it follows ('task' or 'trip'); Follow moves the
// rest. The caller writes the audit record (tenant_rehomed) in the same transaction. A table whose CHECK needs a
// driver or truck for a non-quarantine row (chats, payroll, maintenance without a truck, ...) refuses the move with
// that CHECK. The result starts with the row itself.
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
	rest, err := Follow(ctx, tx, table, id, to)
	if err != nil {
		return nil, err
	}
	return append([]Moved{{Table: table, ID: id}}, rest...), nil
}

// Follow moves what a row takes along when it leaves the quarantine tenant for `to`: its children still in the
// quarantine tenant, re-stamped with the link they follow (recursively), then the file_objects that the row and
// those children own while the files are in the quarantine tenant (a key another tenant's row registered first
// keeps its tenant). The caller has already moved the row itself, under app.tenant_move (Rehome) or app.etl_load
// (cmd/etl: a load or retry whose fix at source now resolves the row's chain, main spec §13.5); both GUCs open the
// frozen-tenant trigger, and the file trigger accepts the system context or app.etl_load. The result lists the
// moved children, then the moved files, not the row.
func Follow(ctx context.Context, tx pgx.Tx, table string, id, to uuid.UUID) ([]Moved, error) {
	if _, ok := rehomeTables[table]; !ok {
		return nil, fmt.Errorf("tenancy: %s is not a tenant-stamped table Rehome supports", table)
	}
	q := uuid.MustParse(QuarantineTenantID)
	children, err := moveChildren(ctx, tx, table, id, to, q, nil)
	if err != nil {
		return nil, err
	}
	files, err := moveFiles(ctx, tx, append([]Moved{{Table: table, ID: id}}, children...), to, q)
	if err != nil {
		return nil, err
	}
	return append(children, files...), nil
}

// moveFiles moves the files the owners own from the quarantine tenant to `to`.
func moveFiles(ctx context.Context, tx pgx.Tx, owners []Moved, to, q uuid.UUID) ([]Moved, error) {
	kinds, ids := make([]string, 0, len(owners)), make([]uuid.UUID, 0, len(owners))
	for _, o := range owners {
		if k, ok := fileOwnerKinds[o.Table]; ok {
			kinds, ids = append(kinds, k), append(ids, o.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `UPDATE file_objects f SET tenant_id = $1
		FROM unnest($3::text[], $4::uuid[]) AS m(kind, id)
		WHERE f.tenant_id = $2 AND f.owner_kind = m.kind AND f.owner_id = m.id
		RETURNING f.id`, to, q, kinds, ids)
	if err != nil {
		return nil, fmt.Errorf("tenancy: move the files of %s %s: %w", owners[0].Table, owners[0].ID, err)
	}
	fids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("tenancy: move the files of %s %s: %w", owners[0].Table, owners[0].ID, err)
	}
	slices.SortFunc(fids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	out := make([]Moved, 0, len(fids))
	for _, f := range fids {
		out = append(out, Moved{Table: FileObjects, ID: f})
	}
	return out, nil
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
