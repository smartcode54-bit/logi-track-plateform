package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotInTenant: MoveDriver found the driver outside the tenant the caller locked it in.
var ErrNotInTenant = errors.New("tenancy: the driver is not in the tenant it is moved from")

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
