//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

type rls db.RLS

func (r rls) RLS() db.RLS { return db.RLS(r) }

// WithPrincipal (Appendix C §C.3.2): one round trip sets the nine GUCs from the principal, the helpers of
// 0001_preamble read them back, nothing survives COMMIT on a pooled connection, X-Act-On-Tenant: * runs
// READ ONLY, and a bypass outside a read-only transaction is refused before BEGIN.
func TestWithPrincipalContext(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := db.NewPool(ctx, d.URL(db.RoleApp), "principal-test", db.Options{MaxConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	user, tenant, driver := uuid.New(), uuid.New(), uuid.New()
	party1, party2, sub := uuid.New(), uuid.New(), uuid.New()

	p := rls{UserID: user, TenantID: &tenant, Role: "manager", DriverID: &driver, CustomerIDs: []uuid.UUID{party1, party2},
		SubtenantIDs: []uuid.UUID{sub}, Dispatcher: true, Steward: true}
	err = db.WithPrincipal(ctx, pool, p, func(tx pgx.Tx) error {
		var u, tn, role, drv string
		var parties, subs []uuid.UUID
		var dispatcher, steward, bypass, staff, scope, readOnly bool
		if err := tx.QueryRow(ctx, `SELECT app_user_id()::text, app_tenant_id()::text, app_role(), app_driver_id()::text,
			app_customer_ids(), app_subtenant_ids(), app_is_dispatcher(), app_is_steward(), app_bypass(), app_is_staff(),
			app_is_scope(), current_setting('transaction_read_only') = 'on'`).Scan(&u, &tn, &role, &drv, &parties, &subs,
			&dispatcher, &steward, &bypass, &staff, &scope, &readOnly); err != nil {
			return err
		}
		if u != user.String() || tn != tenant.String() || role != "manager" || drv != driver.String() ||
			len(parties) != 2 || parties[1] != party2 || len(subs) != 1 || subs[0] != sub ||
			!dispatcher || !steward || bypass || !staff || !scope || readOnly {
			t.Errorf("context %s %s %s %s %v %v dispatcher=%v steward=%v bypass=%v staff=%v scope=%v ro=%v",
				u, tn, role, drv, parties, subs, dispatcher, steward, bypass, staff, scope, readOnly)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The single pooled connection forgot everything at COMMIT.
	var leaked string
	if err := pool.QueryRow(ctx, `SELECT coalesce(current_setting('app.user_id', true), '') ||
		coalesce(current_setting('app.steward', true), '')`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != "" {
		t.Fatalf("the request context leaked past COMMIT: %q", leaked)
	}

	// X-Act-On-Tenant: *: bypass in a READ ONLY transaction; a write fails with 25006.
	err = db.WithPrincipal(ctx, pool, rls{UserID: user, Bypass: true, ReadOnly: true}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM tenants`).Scan(&n); err != nil {
			return err
		}
		if n != 1 { // the quarantine row, visible only under bypass
			t.Errorf("bypass sees %d tenants, want the quarantine row", n)
		}
		_, err := tx.Exec(ctx, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'X')`)
		return err
	})
	if pe, ok := errors.AsType[*pgconn.PgError](err); !ok || pe.Code != "25006" {
		t.Fatalf("a write under X-Act-On-Tenant: * must fail read-only, got %v", err)
	}
	if err := db.WithPrincipal(ctx, pool, rls{UserID: user, Bypass: true}, func(pgx.Tx) error { return nil }); !errors.Is(err, db.ErrBypassNeedsReadOnly) {
		t.Fatalf("a writable bypass must be refused, got %v", err)
	}
	if err := db.WithPrincipal(ctx, pool, nil, func(pgx.Tx) error { return nil }); !errors.Is(err, db.ErrNoPrincipal) {
		t.Fatalf("a nil principal must be refused, got %v", err)
	}
	// An empty principal (nothing resolved) sees nothing.
	err = db.WithPrincipal(ctx, pool, rls{}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM tenants`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("an empty principal sees %d tenants", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
