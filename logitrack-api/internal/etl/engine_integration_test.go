//go:build integration

package etl

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

// testEngine runs as logitrack_etl with app.etl_load on, the way cmd/etl does, on the production P0 schema.
func testEngine(t *testing.T) (*Engine, *pgxpool.Pool) {
	t.Helper()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	ctx := context.Background()
	if _, err := r.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply(ctx, 11); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleETL)
	tx := func(ctx context.Context, fn func(pgx.Tx) error) error {
		return db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT set_config('app.etl_load', 'on', true)`); err != nil {
				return err
			}
			return fn(tx)
		})
	}
	e, err := New(Config{Tx: tx, OwnFleetTenantID: uuid.MustParse("01900000-0000-7000-8000-000000000001"), Bucket: "logitrack",
		Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	return e, pool
}

// A document recorded pending (its collection has no mapping in `load`: permissions_config is read by auth-rbac) is
// applied by the first load that maps the collection, a --since or --since=watermark delta load included, even though
// its _updateTime is unchanged and before the delta's mark (review T15; T24 mapped every other collection, so the
// test maps permissions_config itself).
func TestPendingDocumentsApplyOnTheFirstMappedDeltaLoad(t *testing.T) {
	e, pool := testEngine(t)
	ctx := context.Background()
	ut := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	w, err := dump.NewWriter(dir, ut.Add(time.Hour), "p", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCollection("permissions_config", false, []dump.Doc{{ID: "h1", Path: "permissions_config/h1", UpdateTime: ut, Fields: map[string]any{"capabilities": map[string]any{}}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := dump.OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Load(ctx, d, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	var marks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM etl.watermarks WHERE collection = 'permissions_config'`).Scan(&marks); err != nil || marks != 0 {
		t.Fatalf("a pending collection moved the watermark: %d %v", marks, err)
	}
	docs, err := d.Read("permissions_config")
	if err != nil {
		t.Fatal(err)
	}
	errRollback := errors.New("rollback")
	for _, o := range []LoadOptions{{SinceWatermark: true}, {Since: ut.Add(time.Hour)}} {
		calls := 0
		spec := collectionSpec{name: "permissions_config", mapper: func(c *docCtx) error { calls++; return nil }} // a later mapping
		var stats CollectionStats
		var status string
		err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
			// A watermark well past the document (beyond the look-back), as a database loaded before this fix may hold.
			if _, err := tx.Exec(ctx, `INSERT INTO etl.watermarks (collection, last_update_time, last_doc_path) VALUES ('permissions_config', $1, 'permissions_config/h9')
				ON CONFLICT (collection) DO NOTHING`, ut.Add(time.Hour)); err != nil {
				return err
			}
			var err error
			if stats, err = e.loadCollection(ctx, tx, spec, docs, d.Manifest.ExportedAt, o, billableSet{}, &Report{}, nil); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT status FROM etl.source_docs WHERE doc_path = 'permissions_config/h1'`).Scan(&status); err != nil {
				return err
			}
			return errRollback
		})
		if !errors.Is(err, errRollback) {
			t.Fatal(err)
		}
		if calls != 1 || stats.BeforeMark != 0 || stats.Outcomes[Loaded] != 1 || status != string(Loaded) {
			t.Errorf("%+v: mapper calls %d, stats %+v, status %s", o, calls, stats, status)
		}
	}
}

// A driver's history rows an earlier load stored under the Auth UID an email / Google re-link replaced are pointed at
// that driver once their document names the old uid in authIdHistory (the 3.x backfillDriverAuthHistory callable
// records it). Without this the Go console keeps hiding those trips, standby records, incidents and expenses from the
// driver, because the console reads driver_id and no later load revisits a row whose own document did not change.
func TestPastAuthUIDHealsTheDriverHistoryRowsAlreadyStored(t *testing.T) {
	e, pool := testEngine(t)
	ctx := context.Background()
	ut := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	loadDrivers := func(at time.Time, history []any) {
		t.Helper()
		fields := map[string]any{"firstName": "สมชาย", "lastName": "ใจดี", "mobile": "0811111111", "authId": "uid_new"}
		if history != nil {
			fields["authIdHistory"] = history
		}
		dir := t.TempDir()
		w, err := dump.NewWriter(dir, at.Add(time.Hour), "p", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := w.WriteCollection("drivers", false, []dump.Doc{
			{ID: "drv_relinked", Path: "drivers/drv_relinked", UpdateTime: at, Fields: fields},
			{ID: "drv_other", Path: "drivers/drv_other", UpdateTime: at, Fields: map[string]any{"firstName": "สมศรี",
				"lastName": "มีสุข", "mobile": "0822222222", "authId": "uid_other"}},
		}); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		d, err := dump.OpenDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Load(ctx, d, LoadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	// The driver as it stands right after the re-link: the new uid only, the old one not recorded yet.
	loadDrivers(ut, nil)
	var drv, tenant uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id, tenant_id FROM drivers WHERE legacy_doc_id = 'drv_relinked'`).Scan(&drv, &tenant); err != nil {
		t.Fatal(err)
	}
	quarantine := uuid.MustParse(tenancy.QuarantineTenantID)
	// Rows an earlier load stored: two of the driver's own, one of a uid no driver claims, one quarantined.
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		for _, r := range []struct {
			tripNo, ref string
			tenant      uuid.UUID
			source      string
		}{
			{"TRIP-OLD-UID", "uid_old", tenant, "trip"},
			{"TRIP-STRANGER", "uid_stranger", tenant, "trip"},
			{"TRIP-QUARANTINED", "uid_old", quarantine, "quarantine"},
			{"TRIP-OTHER-DRIVER", "uid_other", tenant, "trip"},
		} {
			if _, err := tx.Exec(ctx, `INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type,
				legacy_doc_id, legacy_driver_ref, driver_ref_match) VALUES ($1, $2, $3, 'delivered', 'line_haul', $3, $4, 'none')`,
				r.tenant, r.source, r.tripNo, r.ref); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO incident_reports (tenant_id, tenant_source, legacy_doc_id, legacy_driver_ref,
			driver_ref_match) VALUES ($1, 'driver', 'inc_old_uid', 'uid_old', 'none')`, tenant)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// The callable records the uid the re-link replaced; the next load of the driver heals its rows.
	// "uid_other" is another driver's current uid: a history entry naming it must not take that driver's rows.
	loadDrivers(ut.Add(24*time.Hour), []any{"uid_old", "", "uid_old", "uid_other"})
	for _, want := range []struct {
		table, key, column string
		driver             *uuid.UUID
		match              string
	}{
		{"trip_records", "TRIP-OLD-UID", "legacy_doc_id", &drv, "auth_uid"},
		{"incident_reports", "inc_old_uid", "legacy_doc_id", &drv, "auth_uid"},
		{"trip_records", "TRIP-STRANGER", "legacy_doc_id", nil, "none"},
		{"trip_records", "TRIP-QUARANTINED", "legacy_doc_id", nil, "none"},
		{"trip_records", "TRIP-OTHER-DRIVER", "legacy_doc_id", nil, "none"},
	} {
		var got *uuid.UUID
		var match string
		if err := pool.QueryRow(ctx, `SELECT driver_id, driver_ref_match FROM `+want.table+
			` WHERE `+want.column+` = $1`, want.key).Scan(&got, &match); err != nil {
			t.Fatal(err)
		}
		if match != want.match || (want.driver == nil) != (got == nil) || (got != nil && *got != *want.driver) {
			t.Errorf("%s %s: driver %v match %s, want %v %s", want.table, want.key, got, match, want.driver, want.match)
		}
	}
	// Idempotent: a second load of the same history changes nothing.
	loadDrivers(ut.Add(48*time.Hour), []any{"uid_old"})
	var healed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM trip_records WHERE driver_id = $1`, drv).Scan(&healed); err != nil {
		t.Fatal(err)
	}
	if healed != 1 {
		t.Errorf("trips pointed at the driver: %d, want 1", healed)
	}
}
