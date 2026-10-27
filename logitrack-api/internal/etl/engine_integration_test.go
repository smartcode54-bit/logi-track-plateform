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

// A row written before an email / Google re-link names its driver by the uid the re-link replaced, which the 3.x
// callables keep in the driver document's authIdHistory: it resolves to that driver like the current uid does.
func TestDriverByRefResolvesAPastAuthUID(t *testing.T) {
	e, _ := testEngine(t)
	ctx := context.Background()
	ut := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	w, err := dump.NewWriter(dir, ut.Add(time.Hour), "p", "")
	if err != nil {
		t.Fatal(err)
	}
	drivers := []dump.Doc{
		{ID: "drv_relinked", Path: "drivers/drv_relinked", UpdateTime: ut, Fields: map[string]any{"firstName": "สมชาย",
			"lastName": "ใจดี", "mobile": "0811111111", "authId": "uid_new", "authIdHistory": []any{"uid_old"}}},
		{ID: "drv_other", Path: "drivers/drv_other", UpdateTime: ut, Fields: map[string]any{"firstName": "สมศรี",
			"lastName": "มีสุข", "mobile": "0822222222", "authId": "uid_other"}},
	}
	if err := w.WriteCollection("drivers", false, drivers); err != nil {
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
	err = e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		ids := map[string]uuid.UUID{}
		for _, doc := range []string{"drv_relinked", "drv_other"} {
			var id uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM drivers WHERE legacy_doc_id = $1`, doc).Scan(&id); err != nil {
				return err
			}
			ids[doc] = id
		}
		c := &docCtx{ctx: ctx, tx: tx, e: e, doc: dump.Doc{Path: "trip_records/t1"}}
		for raw, want := range map[string][2]string{"drv_relinked": {"doc_id", "drv_relinked"}, "uid_new": {"auth_uid", "drv_relinked"},
			"uid_old": {"auth_uid", "drv_relinked"}, "uid_other": {"auth_uid", "drv_other"}, "uid_unknown": {"none", ""}} {
			m, err := c.driverByRef(raw, "")
			if err != nil {
				return err
			}
			if m.match != want[0] || m.id != ids[want[1]] {
				t.Errorf("%s: match %s id %v, want %s %s", raw, m.match, m.id, want[0], want[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
