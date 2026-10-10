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

// A document recorded pending (its collection loads with a later task) is applied by the first load that maps the
// collection, a --since or --since=watermark delta load included, even though its _updateTime is unchanged and
// before the delta's mark (review T15).
func TestPendingDocumentsApplyOnTheFirstMappedDeltaLoad(t *testing.T) {
	e, pool := testEngine(t)
	ctx := context.Background()
	ut := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	w, err := dump.NewWriter(dir, ut.Add(time.Hour), "p", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCollection("hubs", false, []dump.Doc{{ID: "h1", Path: "hubs/h1", UpdateTime: ut, Fields: map[string]any{"source_id": "HUBA"}}}); err != nil {
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
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM etl.watermarks WHERE collection = 'hubs'`).Scan(&marks); err != nil || marks != 0 {
		t.Fatalf("a pending collection moved the watermark: %d %v", marks, err)
	}
	docs, err := d.Read("hubs")
	if err != nil {
		t.Fatal(err)
	}
	errRollback := errors.New("rollback")
	for _, o := range []LoadOptions{{SinceWatermark: true}, {Since: ut.Add(time.Hour)}} {
		calls := 0
		spec := collectionSpec{name: "hubs", mapper: func(c *docCtx) error { calls++; return nil }} // T24 maps hubs
		var stats CollectionStats
		var status string
		err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
			// A watermark well past the document (beyond the look-back), as a database loaded before this fix may hold.
			if _, err := tx.Exec(ctx, `INSERT INTO etl.watermarks (collection, last_update_time, last_doc_path) VALUES ('hubs', $1, 'hubs/h9')
				ON CONFLICT (collection) DO NOTHING`, ut.Add(time.Hour)); err != nil {
				return err
			}
			var err error
			if stats, err = e.loadCollection(ctx, tx, spec, docs, d.Manifest.ExportedAt, o, billableSet{}, &Report{}); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT status FROM etl.source_docs WHERE doc_path = 'hubs/h1'`).Scan(&status); err != nil {
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
