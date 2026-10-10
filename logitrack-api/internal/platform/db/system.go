package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Beginner starts transactions: a *pgxpool.Pool, or a *pgx.Conn in tests.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// setContextSQL sets the nine request GUCs of Appendix C §C.3.2 in one round trip right after BEGIN.
// The third argument of set_config is true: every value is transaction-local and reset at COMMIT or
// ROLLBACK, so a pooled connection never carries one transaction's context into the next.
const setContextSQL = `SELECT set_config('app.user_id', $1, true), set_config('app.tenant_id', $2, true),
       set_config('app.role', $3, true), set_config('app.driver_id', $4, true),
       set_config('app.customer_ids', $5, true), set_config('app.subtenant_ids', $6, true),
       set_config('app.dispatcher', $7, true), set_config('app.steward', $8, true),
       set_config('app.bypass_tenant', $9, true)`

// WithSystem runs fn in a transaction with the system context: app.bypass_tenant = on, app.role =
// 'system', no user, and app.tenant_id = tenantID when it is not nil (Appendix C §C.3.2, R12). It is
// the entry for work that has no request principal: login and refresh before a principal exists,
// identity writes after Go authorization, security-event appends, workers, the scheduler, ETL and seed.
// The tenant-move and ETL GUCs are never set here. fn's error rolls the transaction back; a panic too.
//
// Request transactions use WithPrincipal instead. tools/analyzers/withsystem (make lint) fails the build
// when a package outside its allow-list calls WithSystem (Appendix C §C.3.2).
func WithSystem(ctx context.Context, b Beginner, tenantID *uuid.UUID, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, b, func(tx pgx.Tx) error {
		tid := ""
		if tenantID != nil {
			tid = tenantID.String()
		}
		if _, err := tx.Exec(ctx, setContextSQL, "", tid, "system", "", "", "", "off", "off", "on"); err != nil {
			return fmt.Errorf("db: set system context: %w", err)
		}
		return fn(tx)
	})
}
