//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

type supportVisitFixture struct {
	pool                      *pgxpool.Pool
	actor, other, tenant, sid uuid.UUID
	now                       time.Time
}

func newSupportVisitFixture(t *testing.T) supportVisitFixture {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := db.NewPool(ctx, d.URL(db.RoleApp), "support-visits-test", db.Options{MaxConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f := supportVisitFixture{pool: pool, actor: uuid.New(), other: uuid.New(), tenant: uuid.New(), sid: uuid.New(),
		now: time.Now().UTC().Truncate(time.Microsecond)}
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		for _, id := range []uuid.UUID{f.actor, f.other} {
			if _, err := tx.Exec(ctx, `INSERT INTO users (id) VALUES ($1)`, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, kind, legal_type, name_th)
 VALUES ($1, 'carrier', 'company', 'Support tenant')`, f.tenant); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO sessions (id, user_id, platform, amr, absolute_expires_at)
 VALUES ($1, $2, 'web', 'pwd', $3)`, f.sid, f.actor, f.now.Add(24*time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func supportVisitSQLState(t *testing.T, err error, state string) {
	t.Helper()
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != state {
		t.Fatalf("want SQLSTATE %s, got %v", state, err)
	}
}

func TestSupportVisitLifecycle(t *testing.T) {
	ctx := context.Background()
	f := newSupportVisitFixture(t)
	var visit db.SupportVisit
	err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		var err error
		visit, err = db.CreateSupportVisit(ctx, tx, f.sid, f.actor, f.tenant, f.now, 0)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if visit.ID.Version() != 7 || visit.SessionID == nil || *visit.SessionID != f.sid || visit.EndedAt != nil || !visit.StartedAt.Equal(f.now) {
		t.Fatalf("new visit = %+v", visit)
	}
	if err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		_, err := db.CreateSupportVisit(ctx, tx, f.sid, f.actor, f.tenant, f.now, 0)
		return err
	}); err == nil {
		t.Fatal("a session acquired two open visits")
	} else {
		supportVisitSQLState(t, err, "23505")
	}
	for _, target := range []struct{ actor, tenant uuid.UUID }{
		{f.actor, uuid.MustParse("00000000-0000-7000-8000-00000000000f")}, {f.other, f.tenant}, {f.actor, uuid.New()},
	} {
		err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
			_, err := db.CreateSupportVisit(ctx, tx, f.sid, target.actor, target.tenant, f.now, 0)
			return err
		})
		if !errors.Is(err, db.ErrSupportVisitTarget) {
			t.Fatalf("invalid entry: %v", err)
		}
	}
	// A session must be closed first, even when a caller bypasses the repository.
	err = db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, f.sid)
		return err
	})
	supportVisitSQLState(t, err, "23000")
	failedAudit := errors.New("entry/exit audit failed")
	err = db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		if _, err := db.CloseSupportVisit(ctx, tx, f.sid, db.SupportVisitExpired, f.now.Add(time.Hour), f.now.Add(2*time.Hour)); err != nil {
			return err
		}
		return failedAudit
	})
	if !errors.Is(err, failedAudit) {
		t.Fatal(err)
	}
	if err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		open, err := db.OpenSupportVisit(ctx, tx, f.sid)
		if err != nil || open.ID != visit.ID {
			t.Fatalf("rollback lost open visit: %+v (%v)", open, err)
		}
		closed, err := db.CloseSupportVisit(ctx, tx, f.sid, db.SupportVisitExpired, f.now.Add(time.Hour), f.now.Add(2*time.Hour))
		if err != nil {
			return err
		}
		if closed == nil || !closed.EndedAt.Equal(f.now.Add(time.Hour)) || !closed.EndRecordedAt.Equal(f.now.Add(2*time.Hour)) {
			t.Fatalf("effective/recorded close = %+v", closed)
		}
		if again, err := db.CloseSupportVisit(ctx, tx, f.sid, db.SupportVisitLogout, f.now.Add(3*time.Hour), f.now.Add(3*time.Hour)); err != nil || again != nil {
			t.Fatalf("repeated close = %+v (%v)", again, err)
		}
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, f.sid)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		var session *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT session_id FROM support_visits WHERE id = $1`, visit.ID).Scan(&session); err != nil {
			return err
		}
		if session != nil {
			t.Fatal("deleted session reference survived")
		}
		_, err := tx.Exec(ctx, `UPDATE support_visits SET ended_at = NULL, end_reason = NULL, end_recorded_at = NULL WHERE id = $1`, visit.ID)
		return err
	}); err == nil {
		t.Fatal("closed visit reopened")
	} else {
		supportVisitSQLState(t, err, "23000")
	}
}

func TestSupportVisitSystemOnlyAndPoolReset(t *testing.T) {
	ctx := context.Background()
	f := newSupportVisitFixture(t)
	if err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		_, err := db.CreateSupportVisit(ctx, tx, f.sid, f.actor, f.tenant, f.now, 0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, context := range []struct{ name, role, bypass string }{
		{"unset", "", ""}, {"member", "tenant_admin", "off"}, {"role_only", "system", "off"}, {"bypass_only", "tenant_admin", "on"},
	} {
		t.Run(context.name, func(t *testing.T) {
			inContext := func(fn func(pgx.Tx) error) error {
				return pgx.BeginFunc(ctx, f.pool, func(tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, `SELECT set_config('app.role', $1, true), set_config('app.bypass_tenant', $2, true),
 set_config('app.user_id', $3, true), set_config('app.tenant_id', $4, true)`, context.role, context.bypass, f.actor.String(), f.tenant.String()); err != nil {
						return err
					}
					return fn(tx)
				})
			}
			if err := inContext(func(tx pgx.Tx) error {
				var n int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM support_visits`).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					t.Fatalf("private visits visible: %d", n)
				}
				tag, err := tx.Exec(ctx, `UPDATE support_visits SET ended_at = $1, end_reason = 'leave', end_recorded_at = $1`, f.now.Add(time.Hour))
				if err == nil && tag.RowsAffected() != 0 {
					t.Fatal("ordinary context closed a visit")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := inContext(func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO support_visits (session_id, actor_user_id, tenant_id, consent_version_at_entry)
 VALUES ($1, $2, $3, 0)`, f.sid, f.actor, f.tenant)
				return err
			}); err == nil {
				t.Fatal("ordinary context inserted a visit")
			}
			if err := inContext(func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `DELETE FROM support_visits`)
				return err
			}); err == nil {
				t.Fatal("app role deleted a visit")
			} else {
				supportVisitSQLState(t, err, "42501")
			}
		})
	}
	for _, rollback := range []bool{false, true} {
		boom := errors.New("rollback")
		err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
			if rollback {
				return boom
			}
			return nil
		})
		if err != nil && !errors.Is(err, boom) {
			t.Fatal(err)
		}
		var n int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM support_visits`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("context leaked after rollback=%t: count=%d (%v)", rollback, n, err)
		}
	}
	if err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM support_visits`)
		return err
	}); err == nil {
		t.Fatal("system GUCs conferred DELETE on app role")
	} else {
		supportVisitSQLState(t, err, "42501")
	}
}

func TestSupportVisitDatabaseConstraints(t *testing.T) {
	ctx := context.Background()
	f := newSupportVisitFixture(t)
	for _, tc := range []struct {
		name, columns, values string
		args                  []any
	}{
		{"quarantine", "", "", []any{f.sid, f.actor, uuid.MustParse("00000000-0000-7000-8000-00000000000f"), int64(0)}},
		{"wrong_actor", "", "", []any{f.sid, f.other, f.tenant, int64(0)}},
		{"sessionless_open", "", "", []any{nil, f.actor, f.tenant, int64(0)}},
		{"negative_consent", "", "", []any{f.sid, f.actor, f.tenant, int64(-1)}},
		{"half_closed", ", ended_at", ", now()", []any{f.sid, f.actor, f.tenant, int64(0)}},
		{"unknown_reason", ", ended_at, end_recorded_at, end_reason", ", now(), now(), 'idle'", []any{f.sid, f.actor, f.tenant, int64(0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO support_visits (session_id, actor_user_id, tenant_id, consent_version_at_entry`+
					tc.columns+`) VALUES ($1, $2, $3, $4`+tc.values+`)`, tc.args...)
				return err
			})
			supportVisitSQLState(t, err, "23514")
		})
	}
}
