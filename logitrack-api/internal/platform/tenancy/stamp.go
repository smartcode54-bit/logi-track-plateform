package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// linkTables are the tables a tenant chain links to by id (main spec §13.5): a task or trip a row follows, the driver
// that ran it, the truck it used. Identifiers come only from this set.
var linkTables = map[string]bool{"tasks": true, "trip_records": true, "drivers": true, "trucks": true}

// TenantOf reads the tenant of one row a tenant chain links to (tasks, trip_records, drivers, trucks) in the caller's
// transaction, so the caller's RLS decides whether the row exists for it: a row the caller cannot see is not found, the
// way a reference the writer cannot read must never stamp a tenant (T28, Appendix C §C.3.10). A row in the quarantine
// tenant is "not stamped" (tenantResolve.ts) and reported as not found too: its tenant is never handed on.
func TenantOf(ctx context.Context, tx pgx.Tx, table string, id uuid.UUID) (uuid.UUID, bool, error) {
	if !linkTables[table] {
		return uuid.Nil, false, fmt.Errorf("tenancy: %s is not a table a tenant chain links to", table)
	}
	var tenant uuid.UUID
	err := tx.QueryRow(ctx, `SELECT tenant_id FROM `+pgx.Identifier{table}.Sanitize()+` WHERE id = $1`, id).Scan(&tenant)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil, false, nil
	case err != nil:
		return uuid.Nil, false, fmt.Errorf("tenancy: read the tenant of %s %s: %w", table, id, err)
	case tenant == uuid.MustParse(QuarantineTenantID):
		return uuid.Nil, false, nil
	}
	return tenant, true, nil
}
