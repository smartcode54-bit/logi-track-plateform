//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

// WithSystem (Appendix C §C.3.2): logitrack_app sees the forced-RLS identity tables only inside it, the
// context is transaction-local (a pooled connection forgets it at COMMIT or ROLLBACK), and a tenant id
// lands in app.tenant_id.
func TestWithSystemContext(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	// One connection: every statement below reuses it, so a leaked GUC would show.
	pool, err := db.NewPool(ctx, d.URL(db.RoleApp), "system-test", db.Options{MaxConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	count := func(q interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	}) int64 {
		t.Helper()
		var n int64
		if err := q.QueryRow(ctx, `SELECT count(*) FROM tenants`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(pool); n != 0 {
		t.Fatalf("without a context logitrack_app must see no tenants, saw %d", n)
	}
	tid := uuid.MustParse("00000000-0000-7000-8000-00000000000f")
	err = db.WithSystem(ctx, pool, &tid, func(tx pgx.Tx) error {
		if n := count(tx); n != 1 {
			t.Errorf("WithSystem must see the quarantine tenant, saw %d rows", n)
		}
		var role, tenant, bypass string
		if err := tx.QueryRow(ctx, `SELECT current_setting('app.role'), current_setting('app.tenant_id'),
			current_setting('app.bypass_tenant')`).Scan(&role, &tenant, &bypass); err != nil {
			return err
		}
		if role != "system" || tenant != tid.String() || bypass != "on" {
			t.Errorf("context = %s %s %s", role, tenant, bypass)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := count(pool); n != 0 {
		t.Fatalf("the context leaked past COMMIT: %d tenants visible", n)
	}
	boom := errors.New("boom")
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'Own')`); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("want the callback error, got %v", err)
	}
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		if n := count(tx); n != 1 {
			t.Errorf("the failed transaction must roll back, saw %d tenants", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
