//go:build integration

package iam_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
	"testing"
)

func TestSupportRequestLimitsAppRoleIsolation(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO support_request_limits VALUES('support_options','system',clock_timestamp())`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	verify := func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM support_request_limits`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("ordinary/unset context read %d system rows", n)
		}
		tag, err := tx.Exec(ctx, `UPDATE support_request_limits SET theoretical_at=clock_timestamp()`)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 0 {
			t.Fatal("ordinary context updated system limit")
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		_, err = sp.Exec(ctx, `INSERT INTO support_request_limits VALUES('support_options','ordinary',clock_timestamp())`)
		if sqlState(err) != "42501" {
			t.Fatalf("ordinary/unset insert: %v", err)
		}
		return sp.Rollback(ctx)
	}
	if err := pgx.BeginFunc(ctx, pool, verify); err != nil {
		t.Fatal(err)
	}
	tenant := uuid.New()
	if err := db.WithPrincipal(ctx, pool, &authz.Principal{UserID: uuid.New(), TenantID: &tenant, TenantRole: authz.TenantAdmin}, verify); err != nil {
		t.Fatal(err)
	}
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE support_request_limits SET theoretical_at=clock_timestamp() WHERE subject='system'`)
		if err == nil && tag.RowsAffected() != 1 {
			t.Fatal("system update denied")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
