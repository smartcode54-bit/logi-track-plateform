//go:build integration

// Acceptance tests of issue T24 (#44, slice T24S) on postgres:18-alpine, against the in-process Firestore stand-in
// (gcptest, served through the real REST client): the etl.sync mirror (whole documents of the Firestore-owned
// collections, the 3.x app's fields of the PostgreSQL-owned ones), the firestore.shadow projection (masked writes,
// R25 id minting, no echo loop, mobile-owned fields never overwritten) and the daily coexistence reconciliation.
package etl

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

// syncHarness is a database migrated like production at P1 (up-to 9 and the later P0 migrations, 0010 held), the
// fixture dump loaded, and a Firestore stand-in holding the same documents (the source the dump was taken from).
type syncHarness struct {
	t      *testing.T
	ctx    context.Context
	e      *Engine
	pool   *pgxpool.Pool
	runner *migrate.Runner
	fs     *gcptest.Backend
	client *gcp.Firestore
}

func newSyncHarness(t *testing.T) *syncHarness {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	if _, err := r.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Files() {
		if f.Version > 10 {
			if _, err := r.Apply(ctx, f.Version); err != nil {
				t.Fatalf("apply %d: %v", f.Version, err)
			}
		}
	}
	pool := d.Pool(t, db.RoleETL)
	e, err := New(Config{Tx: func(ctx context.Context, fn func(pgx.Tx) error) error { return db.WithETL(ctx, pool, fn) },
		OwnFleetTenantID: uuid.MustParse("01900000-0000-7000-8000-000000000001"), Bucket: "logitrack", Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	fx, err := dump.OpenDir(filepath.Join("..", "..", "cmd", "etl", "testdata", "firestore-fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Load(ctx, fx, LoadOptions{}); err != nil {
		t.Fatalf("fixture load: %v", err)
	}
	b := gcptest.New(t, "logitrack-test")
	for _, n := range fx.Names() {
		docs, err := fx.Read(n)
		if err != nil {
			t.Fatal(err)
		}
		for _, doc := range docs {
			b.Put(doc)
		}
	}
	return &syncHarness{t: t, ctx: ctx, e: e, pool: pool, runner: r, fs: b, client: b.Firestore()}
}

func (h *syncHarness) scalar(sql string, args ...any) any {
	h.t.Helper()
	var v any
	if err := h.pool.QueryRow(h.ctx, sql, args...).Scan(&v); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func (h *syncHarness) id(sql string, args ...any) uuid.UUID {
	h.t.Helper()
	var id uuid.UUID
	if err := h.pool.QueryRow(h.ctx, sql, args...).Scan(&id); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
	return id
}

func (h *syncHarness) exec(sql string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(h.ctx, sql, args...); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
}

// owned is a PG_OWNED_DOMAINS value as the set the plans take.
func owned(domains ...string) map[string]bool {
	m := map[string]bool{}
	for _, d := range domains {
		m[d] = true
	}
	return m
}

func (h *syncHarness) plan(o map[string]bool) MirrorPlan {
	h.t.Helper()
	p, err := PlanMirror(o, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return p
}

func (h *syncHarness) projector(o map[string]bool) *Projector {
	h.t.Helper()
	colls, err := PlanProjection(o, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return &Projector{Tx: h.e.cfg.Tx, FS: h.client, Collections: colls, Owned: o, Log: zerolog.Nop()}
}

func (h *syncHarness) sweep(p MirrorPlan) *Report {
	h.t.Helper()
	rep, err := h.e.Mirror(h.ctx, h.client, p)
	if err != nil {
		h.t.Fatalf("sweep: %v", err)
	}
	return rep
}

func (h *syncHarness) doc(path string) map[string]any {
	h.t.Helper()
	d, ok := h.fs.Doc(path)
	if !ok {
		h.t.Fatalf("no document %s", path)
	}
	return d.Fields
}

func applied(rep *Report, coll string) int {
	for _, c := range rep.Collections {
		if c.Name == coll {
			n := 0
			for _, v := range c.Outcomes {
				n += v
			}
			return n
		}
	}
	return 0
}

// While a domain is Firestore-owned the sweep applies whole documents through the load transforms, only the documents
// Firestore changed, and moves the watermark; a second sweep applies nothing.
func TestMirrorSweepOfFirestoreOwnedCollections(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth"))
	if p.Mode("drivers") != MirrorFull || p.Mode("tasks") != MirrorFull || p.Mode("mobile_installations") != MirrorFull ||
		p.Mode("users") != MirrorFields || p.Mode("permissions_config") != "" {
		t.Fatalf("plan %+v", p.Entries)
	}
	if rep := h.sweep(p); rep.Applied() != 0 {
		t.Fatalf("the first sweep after the load applied %d documents", rep.Applied())
	}
	// A legacy web edit of a driver and a 3.x heartbeat of a new install.
	edited := h.fs.Merge("drivers/drv_own", map[string]any{"firstName": "สมชาย ใหม่"})
	h.fs.Set("drivers/drv_own/mobile_installations/inst_own2", map[string]any{"driverId": "drv_own", "platform": "android",
		"appVersion": "3.3.0", "buildNumber": "4", "flavor": "prod", "partnerId": "", "lastSeenAt": dump.Timestamp{Time: edited.UpdateTime}})
	rep := h.sweep(p)
	if rep.Applied() != 2 || applied(rep, "drivers") != 1 || applied(rep, "mobile_installations") != 1 {
		t.Fatalf("applied %d: %+v", rep.Applied(), rep.Collections)
	}
	if v := h.scalar(`SELECT first_name FROM drivers WHERE legacy_doc_id = 'drv_own'`); v != "สมชาย ใหม่" {
		t.Errorf("first name %v", v)
	}
	if v := h.scalar(`SELECT app_version FROM mobile_installations WHERE install_id = 'inst_own2'`); v != "3.3.0" {
		t.Errorf("installation %v", v)
	}
	var mark time.Time
	if err := h.pool.QueryRow(h.ctx, `SELECT last_update_time FROM etl.watermarks WHERE collection = 'drivers'`).Scan(&mark); err != nil ||
		!mark.Equal(edited.UpdateTime) {
		t.Errorf("drivers watermark %v %v, want %v", mark, err, edited.UpdateTime)
	}
	if rep := h.sweep(p); rep.Applied() != 0 {
		t.Errorf("a sweep without changes applied %d documents", rep.Applied())
	}
}

// AC: the projection never overwrites a mobile-owned field, and a projected write does not re-enter PostgreSQL (no echo
// loop). PostgreSQL owns masterdata: a Go edit of a driver reaches its document without touching what the 3.x app
// wrote, the sweep applies only the app's fields, a stray Firestore edit of a PostgreSQL-owned field stays out, and
// projecting and sweeping again changes nothing on either side.
func TestProjectionKeepsMobileFieldsAndNoEchoLoop(t *testing.T) {
	h := newSyncHarness(t)
	o := owned("auth", "masterdata")
	p, proj := h.plan(o), h.projector(o)
	for coll, mode := range map[string]string{"drivers": MirrorFields, "hubs": MirrorFields, "trucks": MirrorFields,
		"tasks": MirrorFull, "customers": "", "truckAssignment": ""} {
		if p.Mode(coll) != mode {
			t.Fatalf("%s mode %q, want %q", coll, p.Mode(coll), mode)
		}
	}
	// drv_own signs in to the 3.x app as a linked user.
	user := h.id(`INSERT INTO users (legacy_auth_uid, email) VALUES ('uid_own', 'own@example.test') RETURNING id`)
	h.exec(`INSERT INTO memberships (user_id, tenant_id, role) SELECT $1, tenant_id, 'driver' FROM drivers WHERE legacy_doc_id = 'drv_own'`, user)
	h.exec(`UPDATE drivers SET user_id = $1 WHERE legacy_doc_id = 'drv_own'`, user)
	driver := h.id(`SELECT id FROM drivers WHERE legacy_doc_id = 'drv_own'`)

	// The app checks in on another truck and refreshes its push token.
	started := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	h.fs.Merge("drivers/drv_own", map[string]any{"fcmToken": "tok-own-1", "updatedAt": dump.Timestamp{Time: started},
		"activeTruck": map[string]any{"truckId": "trk_alpha1", "truckPlate": "71-5678", "taskId": "task_1", "startedAt": dump.Timestamp{Time: started}}})
	// A Go edit of the driver (PostgreSQL owns drivers) is projected.
	h.exec(`UPDATE drivers SET first_name = 'Somchai', mobile = '0811111111' WHERE id = $1`, driver)
	st, err := proj.Project(h.ctx, ShadowTarget{Collection: "drivers", IDs: []uuid.UUID{driver}})
	if err != nil || st.Written != 1 {
		t.Fatalf("project: %+v %v", st, err)
	}
	f := h.doc("drivers/drv_own")
	at, _ := f["activeTruck"].(map[string]any)
	if f["firstName"] != "Somchai" || f["mobile"] != "0811111111" || f["fcmToken"] != "tok-own-1" || at["truckId"] != "trk_alpha1" ||
		f["updatedAt"] != (dump.Timestamp{Time: started}) {
		t.Fatalf("projected driver %v", f)
	}
	for _, w := range h.fs.Writes() {
		for _, m := range w.Mask {
			if strings.HasPrefix(w.Path, "drivers/") && slices.Contains(MobileOwnedFields["drivers"], m) {
				t.Errorf("the projection wrote the mobile-owned field %s of %s", m, w.Path)
			}
		}
	}

	// The sweep applies the app's fields, and only those.
	h.fs.Merge("drivers/drv_own", map[string]any{"lastName": "Stray web edit"})
	h.sweep(p)
	if v := h.scalar(`SELECT t.legacy_doc_id || ' ' || d.active_truck_plate || ' ' || k.legacy_doc_id || ' ' || d.last_name
		FROM drivers d JOIN trucks t ON t.id = d.active_truck_id JOIN tasks k ON k.id = d.active_task_id WHERE d.id = $1`, driver); v != "trk_alpha1 71-5678 task_1 ใจดี" {
		t.Errorf("active truck and the PostgreSQL-owned last name: %v", v)
	}
	if v := h.scalar(`SELECT install_id || ' ' || legacy_source FROM device_tokens WHERE user_id = $1 AND token = 'tok-own-1'`, user); v !=
		legacyInstallID("tok-own-1")+" drivers.fcmToken" {
		t.Errorf("device token %v", v)
	}
	// The app replaces its token: the legacy row follows, another device's row of the user stays.
	h.exec(`INSERT INTO device_tokens (user_id, install_id, token, platform) VALUES ($1, 'inst-4x', 'tok-4x', 'android')`, user)
	h.fs.Merge("drivers/drv_own", map[string]any{"fcmToken": "tok-own-2"})
	h.sweep(p)
	if v := h.scalar(`SELECT string_agg(token, ',' ORDER BY token) FROM device_tokens WHERE user_id = $1`, user); v != "tok-4x,tok-own-2" {
		t.Errorf("tokens after the app replaced its own: %v", v)
	}
	// The next Go write repairs the stray edit in Firestore and still keeps the app's fields.
	h.exec(`UPDATE drivers SET first_name = 'Somchai' WHERE id = $1`, driver)
	if _, err := proj.Project(h.ctx, ShadowTarget{Collection: "drivers", IDs: []uuid.UUID{driver}}); err != nil {
		t.Fatal(err)
	}
	if f := h.doc("drivers/drv_own"); f["lastName"] != "ใจดี" || f["fcmToken"] != "tok-own-2" {
		t.Errorf("after the repair: %v", f)
	}

	// No echo loop: sweep the projected document, then project and sweep again; nothing changes on either side.
	h.sweep(p)
	var before time.Time
	if err := h.pool.QueryRow(h.ctx, `SELECT updated_at FROM drivers WHERE id = $1`, driver).Scan(&before); err != nil {
		t.Fatal(err)
	}
	outbox := h.scalar(`SELECT count(*) FROM outbox_events`)
	writes := len(h.fs.Writes())
	st, err = proj.Project(h.ctx, ShadowTarget{Collection: "drivers", IDs: []uuid.UUID{driver}})
	if err != nil || st.Written != 0 || st.Unchanged != 1 || len(h.fs.Writes()) != writes {
		t.Fatalf("a projection of an unchanged row wrote: %+v %v", st, err)
	}
	if rep := h.sweep(p); applied(rep, "drivers") != 0 {
		t.Errorf("the sweep re-applied an unchanged document: %+v", rep.Collections)
	}
	var after time.Time
	if err := h.pool.QueryRow(h.ctx, `SELECT updated_at FROM drivers WHERE id = $1`, driver).Scan(&after); err != nil || !after.Equal(before) {
		t.Errorf("the driver row moved: %v -> %v %v", before, after, err)
	}
	if v := h.scalar(`SELECT count(*) FROM outbox_events`); v != outbox {
		t.Errorf("the sweep emitted events: %v -> %v", outbox, v)
	}

	// A hub: the projection's own write is swept as a PostgreSQL-owned document and never re-enters PostgreSQL.
	hub := h.id(`UPDATE hubs SET name_en = 'Hub A (new)' WHERE source_id = 'HUBA' RETURNING id`)
	if st, err := proj.Project(h.ctx, ShadowTarget{Collection: "hubs", IDs: []uuid.UUID{hub}}); err != nil || st.Written != 1 {
		t.Fatalf("project the hub: %+v %v", st, err)
	}
	if err := h.pool.QueryRow(h.ctx, `SELECT updated_at FROM hubs WHERE id = $1`, hub).Scan(&before); err != nil {
		t.Fatal(err)
	}
	h.fs.Merge("hubs/hub_a1", map[string]any{"source_name_en": "edited in Firestore"})
	if rep := h.sweep(p); applied(rep, "hubs") != 1 {
		t.Errorf("hubs swept: %+v", rep.Collections)
	}
	if err := h.pool.QueryRow(h.ctx, `SELECT updated_at FROM hubs WHERE id = $1`, hub).Scan(&after); err != nil || !after.Equal(before) {
		t.Errorf("the PostgreSQL-owned hub moved: %v -> %v %v", before, after, err)
	}
	if v := h.scalar(`SELECT name_en FROM hubs WHERE id = $1`, hub); v != "Hub A (new)" {
		t.Errorf("hub name %v", v)
	}
}

// AC: a hub created in PostgreSQL appears in Firestore with its minted document id, and a customer edited in
// PostgreSQL reaches the Firestore copy the Cloud Functions read (only the changed fields are written: the logo URL
// keeps its download token). Distances are minted with the natural id the 3.x app reads; carriers and companies follow
// their events; quarantined rows are never projected.
func TestProjectionMintsIDsAndReachesCloudFunctionCopies(t *testing.T) {
	h := newSyncHarness(t)
	o := owned("auth", "masterdata")
	proj := h.projector(o)
	hub := h.id(`INSERT INTO hubs (source_id, name_th, network) VALUES ('HUBNEW', 'ฮับใหม่', 'SPX') RETURNING id`)
	targets, err := ShadowTargets("hubs.changed", []byte(`{"ids":["`+hub.String()+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		if _, err := proj.Project(h.ctx, tg); err != nil {
			t.Fatal(err)
		}
	}
	if v := h.scalar(`SELECT legacy_doc_id FROM hubs WHERE id = $1`, hub); v != hub.String() {
		t.Fatalf("minted id %v", v)
	}
	f := h.doc("hubs/" + hub.String())
	if f["source_id"] != "HUBNEW" || f["source_name_th"] != "ฮับใหม่" || f["station_type"] != "HUB" || f["linkedCustomerId"] != nil {
		t.Errorf("hub document %v", f)
	}

	customer := h.id(`UPDATE customers SET name = 'Shopee Express Thailand' WHERE legacy_doc_id = 'cust_spx' RETURNING id`)
	targets, _ = ShadowTargets("customers.changed", []byte(`{"ids":["`+customer.String()+`"]}`))
	if st, err := proj.Project(h.ctx, targets[0]); err != nil || st.Written != 1 {
		t.Fatalf("project the customer: %+v %v", st, err)
	}
	writes := h.fs.Writes()
	last := writes[len(writes)-1]
	if last.Path != "customers/cust_spx" || strings.Join(last.Mask, ",") != "name,updatedAt" {
		t.Errorf("customer write %s %v", last.Path, last.Mask)
	}
	if f := h.doc("customers/cust_spx"); f["name"] != "Shopee Express Thailand" || !strings.Contains(fmt.Sprint(f["logoUrl"]), "token=tok") ||
		f["code"] != "spx" {
		t.Errorf("customer document %v", f)
	}

	h.exec(`INSERT INTO hub_soc_distances (hub_id, soc_key, direction, network, distance_m, distance_km, duration_s, duration_min,
		hub_lat, hub_lng, soc_lat, soc_lng) VALUES ($1, 'SOCE', 'hub_to_soc', 'SPX', 5000, 5, 630, 10.5, 13.7, 100.5, 13.8, 100.7)`, hub)
	h.exec(`INSERT INTO settings (key, value) VALUES ('distances_last_calculated', '{"at":"2026-10-01T02:00:00Z","pairs":3}')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	targets, _ = ShadowTargets("settings.changed", []byte(`{"key":"distances_last_calculated"}`))
	for _, tg := range targets {
		if _, err := proj.Project(h.ctx, tg); err != nil {
			t.Fatal(err)
		}
	}
	f = h.doc("hub_soc_distances/HUBNEW_SOCE")
	if f["hubId"] != "HUBNEW" || f["socId"] != "SOCE" || f["distanceKm"] != int64(5) || f["durationMinutes"] != 10.5 {
		t.Errorf("distance document %v", f)
	}
	if v := h.scalar(`SELECT legacy_doc_id FROM hub_soc_distances WHERE hub_id = $1`, hub); v != "HUBNEW_SOCE" {
		t.Errorf("distance id %v", v)
	}
	if f := h.doc("metadata/distances_last_calculated"); f["timestamp"] != (dump.Timestamp{Time: time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)}) {
		t.Errorf("metadata %v", f)
	}

	carrier := h.id(`INSERT INTO tenants (kind, name_th, legal_type, contractor_tenant_id, service_regions)
		VALUES ('carrier', 'ขนส่งใหม่', 'company', '01900000-0000-7000-8000-000000000001', '{ภาคเหนือ}') RETURNING id`)
	h.exec(`INSERT INTO billing_parties (kind, tenant_id) VALUES ('tenant', $1)`, carrier)
	targets, _ = ShadowTargets("tenant.created", []byte(`{"tenantId":"`+carrier.String()+`"}`))
	if _, err := proj.Project(h.ctx, targets[0]); err != nil {
		t.Fatal(err)
	}
	f = h.doc("subcontractors/" + carrier.String())
	if f["name"] != "ขนส่งใหม่" || f["type"] != "company" || f["status"] != "active" || fmt.Sprint(f["serviceRegions"]) != "[ภาคเหนือ]" {
		t.Errorf("carrier document %v", f)
	}

	// Quarantined rows (unresolved at P1) stay as Firestore holds them.
	st, err := proj.Project(h.ctx, ShadowTarget{Collection: "companies", All: true})
	if err != nil || st.Skipped != 1 {
		t.Fatalf("companies: %+v %v", st, err)
	}
	for _, w := range h.fs.Writes() {
		if w.Path == "companies/co_unknown" || w.Path == "drivers/drv_ghost" {
			t.Errorf("projected a quarantined row: %s", w.Path)
		}
	}
	// A Firestore-owned domain is never projected: operations stays out, and masterdata only while it is owned.
	if colls, _ := PlanProjection(owned("auth"), nil); len(colls) != 0 {
		t.Errorf("projection without masterdata: %v", colls)
	}
	if st, err := (&Projector{Tx: h.e.cfg.Tx, FS: h.client, Log: zerolog.Nop()}).Project(h.ctx, targets[0]); err != nil || st.NotProjected != 1 {
		t.Errorf("an unplanned target: %+v %v", st, err)
	}
}

// The hubs drivers create in the 3.x app (createdByDriver) are mirrored into the PostgreSQL-owned hubs table and stay
// Firestore's (the projection skips them); any other hubs document is not loaded, and a driver's hub that repeats a
// code PostgreSQL holds maps to that hub.
func TestDriverCreatedHubs(t *testing.T) {
	h := newSyncHarness(t)
	o := owned("auth", "masterdata")
	p, proj := h.plan(o), h.projector(o)
	h.fs.Set("hubs/drvhub1", map[string]any{"source_id": "DRV1", "source_name_en": "จุดส่งใหม่", "source_name_th": "จุดส่งใหม่",
		"station_type": "HUB", "createdByDriver": true})
	h.fs.Set("hubs/webhub1", map[string]any{"source_id": "WEB1", "source_name_th": "จากเว็บ"})
	h.fs.Set("hubs/drvdup", map[string]any{"source_id": "huba", "source_name_th": "ซ้ำ", "createdByDriver": true})
	h.sweep(p)
	if v := h.scalar(`SELECT name_th || ' ' || created_by_driver::text FROM hubs WHERE legacy_doc_id = 'drvhub1'`); v != "จุดส่งใหม่ true" {
		t.Errorf("driver hub %v", v)
	}
	for path, want := range map[string]string{"hubs/drvhub1": "loaded -", "hubs/webhub1": "rejected hub_unresolved",
		"hubs/drvdup": "rejected duplicate_hub_source_id"} {
		if v := h.scalar(`SELECT sd.status || ' ' || coalesce((SELECT string_agg(reason_code, ',') FROM etl.quarantine q
			WHERE q.doc_path = sd.doc_path AND q.resolved_at IS NULL), '-') FROM etl.source_docs sd WHERE sd.doc_path = $1`, path); v != want {
			t.Errorf("%s: %v, want %s", path, v, want)
		}
	}
	if v := h.scalar(`SELECT sd.target_id = h.id::text FROM etl.source_docs sd JOIN hubs h ON h.source_id = 'HUBA' WHERE sd.doc_path = 'hubs/drvdup'`); v != true {
		t.Error("the duplicate driver hub does not map to HUBA")
	}
	if v := h.scalar(`SELECT count(*) FROM hubs WHERE source_id = 'WEB1'`); v != int64(0) {
		t.Error("a non-driver hubs document was loaded")
	}
	writes := len(h.fs.Writes())
	st, err := proj.Project(h.ctx, ShadowTarget{Collection: "hubs", All: true})
	if err != nil || st.Skipped != 2 { // drvhub1 and the fixture's hub_drv, loaded at P1
		t.Fatalf("hubs projection: %+v %v", st, err)
	}
	for _, w := range h.fs.Writes()[writes:] {
		if w.Path == "hubs/drvhub1" || w.Path == "hubs/hub_drv" {
			t.Errorf("the projection wrote %s, a hub a driver created", w.Path)
		}
	}
	h.fs.Merge("hubs/drvhub1", map[string]any{"source_name_th": "จุดส่งใหม่ 2"})
	h.sweep(p)
	if v := h.scalar(`SELECT name_th FROM hubs WHERE legacy_doc_id = 'drvhub1'`); v != "จุดส่งใหม่ 2" {
		t.Errorf("driver hub after its edit: %v", v)
	}
}

// While billing is Firestore-owned, the maintenance flow's fields of a truck keep Firestore as their writer: the sweep
// applies Firestore's changes of them (only when Firestore changed them, so a projected write of the document does
// not bring a stale value back over a Go edit) and the projection leaves them out.
func TestTruckFieldsOfTheMaintenanceFlow(t *testing.T) {
	h := newSyncHarness(t)
	o := owned("auth", "masterdata")
	p, proj := h.plan(o), h.projector(o)
	truck := h.id(`SELECT id FROM trucks WHERE legacy_doc_id = 'trk_own1'`)
	// The web maintenance page opens a PM job on the truck (Firestore).
	h.fs.Merge("trucks/trk_own1", map[string]any{"truckStatus": "maintenance", "currentMileage": int64(120500), "lastAlertMileage": int64(120000)})
	h.sweep(p)
	if v := h.scalar(`SELECT status || ' ' || current_mileage || ' ' || last_alert_mileage FROM trucks WHERE id = $1`, truck); v != "maintenance 120500 120000" {
		t.Errorf("after the maintenance flow: %v", v)
	}
	// A Go edit of the truck: the color is projected, the maintenance flow's fields are not.
	h.exec(`UPDATE trucks SET color = 'blue', status = 'active', current_mileage = 1 WHERE id = $1`, truck)
	if _, err := proj.Project(h.ctx, ShadowTarget{Collection: "trucks", IDs: []uuid.UUID{truck}}); err != nil {
		t.Fatal(err)
	}
	f := h.doc("trucks/trk_own1")
	if f["color"] != "blue" || f["currentMileage"] != int64(120500) || f["truckStatus"] != "maintenance" || f["activeMaintenanceId"] != "mnt1" {
		t.Errorf("truck document %v", f)
	}
	// The sweep of the projected document does not bring the maintenance flow's unchanged values back.
	h.sweep(p)
	if v := h.scalar(`SELECT status || ' ' || current_mileage FROM trucks WHERE id = $1`, truck); v != "active 1" {
		t.Errorf("a projected write re-applied stale fields: %v", v)
	}
	// The maintenance flow completes the job: its new values win.
	h.fs.Merge("trucks/trk_own1", map[string]any{"truckStatus": "active", "currentMileage": int64(121000), "lastServiceDate": "2026-10-03",
		"lastAlertMileage": nil, "activeMaintenanceId": nil})
	h.sweep(p)
	if v := h.scalar(`SELECT status || ' ' || current_mileage || ' ' || last_service_date || ' ' || (last_alert_mileage IS NULL)::text || ' ' ||
		(active_maintenance_id IS NULL)::text FROM trucks WHERE id = $1`, truck); v != "active 121000 2026-10-03 true true" {
		t.Errorf("after the job: %v", v)
	}
	// Once billing is PostgreSQL-owned too the fields are PostgreSQL's: projected, no longer mirrored.
	o2 := owned("auth", "masterdata", "billing")
	if h.plan(o2).Mode("trucks") != "" {
		t.Error("trucks still mirrored with billing owned")
	}
	h.exec(`UPDATE trucks SET current_mileage = 121500 WHERE id = $1`, truck)
	if _, err := h.projector(o2).Project(h.ctx, ShadowTarget{Collection: "trucks", IDs: []uuid.UUID{truck}}); err != nil {
		t.Fatal(err)
	}
	if f := h.doc("trucks/trk_own1"); f["currentMileage"] != int64(121500) {
		t.Errorf("current mileage once billing is owned: %v", f["currentMileage"])
	}
}

// AC: daily reconciliation 100% on the fixtures. After the P1 cut-over steps (one full projection pass, one sweep) the
// coexistence reconciliation matches; a Firestore edit of a projected field and a 3.x write not swept yet are reported
// until the next projection and sweep.
func TestDailyReconciliationOnFixtures(t *testing.T) {
	h := newSyncHarness(t)
	o := owned("auth", "masterdata")
	p, proj := h.plan(o), h.projector(o)
	stats, err := proj.ProjectAll(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats["hubs"].Written == 0 || stats["drivers"].Skipped != 1 {
		t.Errorf("first projection pass: %+v", stats)
	}
	h.sweep(p)
	opts := SyncReconcileOptions{Mirror: p, Projection: proj.Collections}
	rec, err := h.e.ReconcileSync(h.ctx, h.client, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Match {
		t.Fatalf("reconciliation after the cut-over steps: %+v", rec.Mismatches())
	}
	for _, name := range []string{"mirror", "mirror_fields", "projection", "findings"} {
		if !slices.ContainsFunc(rec.Checks, func(c Check) bool { return c.Name == name }) {
			t.Errorf("no %s check", name)
		}
	}
	// A second pass changes nothing.
	if stats, err := proj.ProjectAll(h.ctx); err != nil || stats["customers"].Written+stats["trucks"].Written+stats["drivers"].Written != 0 {
		t.Errorf("second projection pass: %+v %v", stats, err)
	}

	h.fs.Merge("customers/cust_cjsf", map[string]any{"name": "tampered"})
	h.fs.Merge("drivers/drv_own", map[string]any{"activeTruck": map[string]any{"truckId": "trk_own1", "truckPlate": "70-1234",
		"taskId": "task_1", "startedAt": dump.Timestamp{Time: time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC)}}})
	rec, err = h.e.ReconcileSync(h.ctx, h.client, opts)
	if err != nil {
		t.Fatal(err)
	}
	var failed []string
	for _, c := range rec.Mismatches() {
		failed = append(failed, c.Name+" "+c.Key)
	}
	slices.Sort(failed)
	if strings.Join(failed, ",") != "mirror drivers (fields),projection customers" {
		t.Fatalf("drift: %v", failed)
	}
	if _, err := proj.Project(h.ctx, ShadowTarget{Collection: "customers", All: true}); err != nil {
		t.Fatal(err)
	}
	h.sweep(p)
	if rec, err = h.e.ReconcileSync(h.ctx, h.client, opts); err != nil || !rec.Match {
		t.Fatalf("after the projection and the sweep: %+v %v", rec.Mismatches(), err)
	}
	if v := h.scalar(`SELECT count(*) FROM etl.reconciliation_runs WHERE report->>'kind' = 'sync'`); v != int64(3) {
		t.Errorf("sync runs recorded: %v", v)
	}
}

// The mirror half of the runbook AC: after 0010, a duplicate of a D5 key the mirror brings is quarantined on its field
// and the sweep goes on (it does not stop on the unique index).
func TestMirroredD5DuplicateAfter0010(t *testing.T) {
	h := newSyncHarness(t)
	for _, sql := range []string{
		`UPDATE vehicle_expenses SET tax_inv_id = NULL WHERE legacy_doc_id = 'exp2'`,
		`UPDATE customer_service_fees SET fee_type = 'custom', custom_type_name = 'D5 loser: ' || fee_type WHERE legacy_doc_id IN ('fee_es_b', 'fee_sb_b')`,
		`UPDATE chats SET status = 'closed', closed_at = now() WHERE legacy_doc_id = 'chat_2'`,
	} {
		h.exec(sql)
	}
	if _, err := h.runner.Up(h.ctx); err != nil {
		t.Fatalf("goose up applies 0010: %v", err)
	}
	h.fs.Set("chats/chat_4", map[string]any{"driverId": "uid_own", "status": "open"})
	rep := h.sweep(h.plan(owned("auth")))
	if applied(rep, "chats") != 1 {
		t.Fatalf("chats swept: %+v", rep.Collections)
	}
	if v := h.scalar(`SELECT c.status || ' ' || q.field || ' ' || q.reason_code FROM chats c JOIN etl.quarantine q
		ON q.doc_path = 'chats/chat_4' AND q.resolved_at IS NULL WHERE c.legacy_doc_id = 'chat_4'`); v != "closed status duplicate_natural_key" {
		t.Errorf("chat_4: %v", v)
	}
}

// The encoders are the inverse of the loaders: a fixture document loaded at P1 and encoded back says the same thing,
// field by field, except where the load repaired or normalised the legacy value (listed here).
func TestEncodersRoundTripTheFixtures(t *testing.T) {
	h := newSyncHarness(t)
	var diffs []string
	err := h.e.cfg.Tx(h.ctx, func(tx pgx.Tx) error {
		for _, coll := range ProjectedCollections {
			docs, err := projectors[coll](h.ctx, tx, selectAll(), false)
			if err != nil {
				return err
			}
			for _, d := range docs {
				orig, ok := h.fs.Doc(d.Path)
				if d.Skip != "" || !ok {
					continue
				}
				_, mask := d.Diff(orig.Fields)
				for _, m := range mask {
					diffs = append(diffs, d.Path+" "+m)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(diffs)
	want := []string{
		"companies/co_alpha maxDrivers", // maxDrivers 0 is a bad number: NULL, the field goes
		"companies/co_alpha updatedAt",  // a document without updatedAt gets the row's
		"companies/co_owner updatedAt",
		"hubs/hub_a1 linkedCustomerName", // the denormalised label follows the customer's name
		"hubs/hub_a1 updatedAt",
		"hubs/hub_b latitude", "hubs/hub_b longitude", // legacy lat/lng: the current field names are added
		"hubs/hub_b source_name_en", "hubs/hub_b source_name_th", // Thai text in the English name moved to the Thai name
		"hubs/hub_b updatedAt", "hubs/hub_soce updatedAt", "hubs/hub_spk updatedAt", "hubs/unknown_3 updatedAt",
		"metadata/distances_last_calculated timestamp", // the callable's field, from the loaded calculatedAt
		"subcontractors/sub_wanpen updatedAt",
		// the assignment snapshots the web form takes are filled when missing
		"truckAssignment/ta_1 driverName", "truckAssignment/ta_1 id", "truckAssignment/ta_1 truckModel", "truckAssignment/ta_1 truckPlate",
		"truckAssignment/ta_2 driverName", "truckAssignment/ta_2 id", "truckAssignment/ta_2 truckModel", "truckAssignment/ta_2 truckPlate",
		"truckAssignment/ta_2 updatedAt",
		"trucks/trk_alpha1 updatedAt",
		"trucks/trk_own1 currentAssignments", // derived from the active assignment (R14)
		"trucks/trk_own1 updatedAt", "trucks/trk_trailer updatedAt",
	}
	if strings.Join(diffs, "\n") != strings.Join(want, "\n") {
		t.Errorf("round trip differences:\n%s\nwant:\n%s", strings.Join(diffs, "\n"), strings.Join(want, "\n"))
	}
}
