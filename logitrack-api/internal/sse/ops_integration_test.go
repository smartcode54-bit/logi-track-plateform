//go:build integration

// Acceptance tests of T33 (issue #53, main spec §8.3, Appendix B §B.4.3-§B.4.4): the operations events end to end, as
// the staging VPS and the coexistence run them. A write of the 3.x app in Firestore (the in-process stand-in; a real
// project is a hitl measurement) is applied by the mirror's listener at the default ETL_LISTEN_INTERVAL, its domain
// event leaves the outbox through the relay (RabbitMQ stubbed, the realtime step real) and reaches the open streams of
// a tenant's staff (tenant:{tid}:trips|tasks) and of a dispatcher whose grant names the billing party of the work
// (dispatch:{partyId}:trips) on an api replica within 5 s; the dispatcher's payloads carry no billing, party, PII or
// cost field.
package sse_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/coexist"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/sse"
)

// budget is the acceptance bound of #53: a mobile write reaches a subscribed client within 5 s.
const budget = 5 * time.Second

// listenInterval is the default ETL_LISTEN_INTERVAL (internal/app background_config.go): the test runs the listener
// the way the worker-sync replica does.
const listenInterval = 2 * time.Second

// opsWorld is the sse world plus the P1 load of the ETL fixtures, the Firestore stand-in holding the same documents,
// the mirror's listener and the relay, all running until the test ends.
type opsWorld struct {
	*world
	fs *gcptest.Backend
}

func newOpsWorld(t *testing.T) *opsWorld {
	t.Helper()
	w := newWorld(t)
	ctx := context.Background()
	etlPool := w.db.Pool(t, db.RoleETL)
	fx, err := dump.OpenDir(filepath.Join("..", "..", "cmd", "etl", "testdata", "firestore-fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := etl.New(etl.Config{Tx: func(ctx context.Context, fn func(pgx.Tx) error) error { return db.WithETL(ctx, etlPool, fn) },
		OwnFleetTenantID: uuid.MustParse(w.own), Bucket: "logitrack", Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Load(ctx, fx, etl.LoadOptions{}); err != nil {
		t.Fatalf("fixture load: %v", err)
	}
	fs := gcptest.New(t, "logitrack-test")
	for _, n := range fx.Names() {
		docs, err := fx.Read(n)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range docs {
			fs.Put(d)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	var done []chan struct{}
	t.Cleanup(func() {
		cancel()
		for _, d := range done {
			<-d
		}
	})
	run := func(f func(context.Context)) {
		d := make(chan struct{})
		done = append(done, d)
		go func() { defer close(d); f(runCtx) }()
	}
	// The scheduler leader's relay.
	relay := outbox.NewRelay(w.pool, func() (outbox.Publisher, error) { return nopPublisher{}, nil }, w.writer, outbox.RelayOptions{Log: zerolog.Nop()})
	run(func(ctx context.Context) { _ = relay.Run(ctx) })
	// The worker-sync replica's listener at the default interval.
	plan, errs := coexist.ParsePlan("auth,masterdata", "", "")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	l, err := coexist.NewListener(coexist.ListenConfig{Interval: listenInterval, ETLPool: etlPool, Source: fs.Firestore(), Plan: plan.Mirror,
		Bucket: "logitrack", PublicBucket: "logitrack-public", Lease: coexist.NewRedisLease(w.rdb, w.ks.MirrorListenLock(), coexist.LeaseTTLFor(listenInterval)),
		Acks: cache.New(w.rdb, w.ks), Log: zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.WarnLevel), Metrics: coexist.NewMetrics(prometheus.NewRegistry())})
	if err != nil || l == nil {
		t.Fatalf("listener %v %v", l, err)
	}
	run(l.Run)
	// The listener starts on its first tick: wait until it holds its lease, then one more interval for its first poll.
	deadline := time.Now().Add(wait)
	for {
		if n, _ := w.rdb.Exists(ctx, w.ks.MirrorListenLock()).Result(); n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the listener never took its lease")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(listenInterval + 500*time.Millisecond)
	return &opsWorld{world: w, fs: fs}
}

// await reads c until a frame with event name name about entity id arrives and returns it with the time since since;
// the test fails when none arrives within the budget. Every frame read on the way is handed to check.
func await(t *testing.T, c *client, name, id string, since time.Time, check func(frame)) (frame, time.Duration) {
	t.Helper()
	deadline := since.Add(budget)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			t.Fatalf("no %s of %s within %v", name, id, budget)
		}
		select {
		case f := <-c.frames:
			if f.Event == "" {
				continue
			}
			check(f)
			var env struct {
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal([]byte(f.Data), &env); err == nil && f.Event == name && env.Data["id"] == id {
				return f, time.Since(since)
			}
		case <-c.eof:
			t.Fatalf("the stream ended while waiting for %s", name)
		case <-time.After(left):
			t.Fatalf("no %s of %s within %v", name, id, budget)
		}
	}
}

// costKey reports a payload key that names a price, a cost, a billing value, a party or PII.
func costKey(k string) bool {
	k = strings.ToLower(k)
	for _, w := range []string{"billing", "price", "cost", "amount", "thb", "rate", "fee", "fuel", "party", "customer",
		"phone", "mobile", "email", "idcard", "evidence", "ocr", "review", "legacy", "name", "token"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// thinOnly checks a frame of an operations event: its payload holds the thin keys only, and on a dispatch topic no
// cost key and only the changed[] names a dispatcher sees.
func thinOnly(t *testing.T, f frame) {
	t.Helper()
	typ, topic, data := f.envelope(t)
	entity := realtime.OperationsEntity(typ)
	if entity == "" {
		return
	}
	for k := range data {
		if !slices.Contains(realtime.ThinKeys(), k) {
			t.Errorf("%s on %s carries %s", typ, topic, k)
		}
	}
	if !strings.HasPrefix(topic, "dispatch:") {
		return
	}
	for k := range data {
		if costKey(k) {
			t.Errorf("%s on %s carries the cost or party key %s", typ, topic, k)
		}
	}
	changed, _ := data["changed"].([]any)
	for _, n := range changed {
		if !slices.Contains(realtime.DispatchColumns(entity), n.(string)) {
			t.Errorf("%s on %s names the hidden column %s", typ, topic, n)
		}
	}
	if _, ok := realtime.DispatchFamily(typ); !ok {
		t.Errorf("%s reached a dispatcher", typ)
	}
}

// AC 1 of #53: a mobile write in Firestore reaches a subscribed client within 5 s, for the tenant's staff and for a
// dispatcher of the work's billing party; AC 2: the dispatch payloads contain no cost field.
func TestMobileWriteReachesSubscribersWithin5s(t *testing.T) {
	w := newOpsWorld(t)
	r := w.replica(sse.Config{})
	w.member("ta@example.test", w.own, "tenant_admin")
	ds := w.member("ds@example.test", w.other, "operator") // a dispatcher: operator of its organisation + a grant over SPX
	spx := w.id(`SELECT bp.id::text FROM billing_parties bp JOIN customers c ON c.id = bp.customer_id WHERE c.legacy_doc_id = 'cust_spx'`)
	w.exec(`INSERT INTO user_scopes (user_id, kind, billing_party_id) VALUES ($1, 'dispatcher', $2)`, ds, spx)

	staff := w.mustOpen(r.internal+"/v1/events", "Authorization", "Bearer "+w.login(r.internal, "ta@example.test", "web").access)
	staff.start()
	disp := w.mustOpen(r.internal+"/v1/events", "Authorization", "Bearer "+w.login(r.internal, "ds@example.test", "web").access)
	disp.start()
	check := func(f frame) { thinOnly(t, f) }

	// The 3.x app starts a trip on task_1 (billed to SPX): trip.created.
	now := time.Now().UTC()
	start := time.Now()
	w.fs.Set("trip_records/TRIP-LIVE", map[string]any{"status": "in_transit", "jobType": "first_mile", "taskId": "task_1",
		"driverId": "uid_own", "billingCustomerId": "cust_spx", "sealCode": "SEAL-LIVE", "createdAt": dump.Timestamp{Time: now},
		"updatedAt": dump.Timestamp{Time: now}})
	trip := ""
	deadline := time.Now().Add(budget)
	for trip == "" && time.Now().Before(deadline) {
		_ = w.etl.QueryRow(context.Background(), `SELECT id::text FROM trip_records WHERE legacy_doc_id = 'TRIP-LIVE'`).Scan(&trip)
		if trip == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if trip == "" {
		t.Fatal("the listener did not apply the trip within the budget")
	}
	f, took := await(t, staff, "trip.created", trip, start, check)
	if _, topic, _ := f.envelope(t); topic != realtime.TenantTopic(w.own, realtime.FamilyTrips) {
		t.Errorf("staff got trip.created on %s", topic)
	}
	t.Logf("trip.created reached the tenant's staff in %v", took)
	f, took = await(t, disp, "trip.created", trip, start, check)
	if _, topic, data := f.envelope(t); topic != realtime.DispatchTopic(spx, realtime.FamilyTrips) || data["tripNo"] != "TRIP-LIVE" {
		t.Errorf("dispatcher got trip.created on %s: %v", topic, data)
	}
	t.Logf("trip.created reached the dispatcher in %v", took)

	// The driver delivers it; a Cloud Function writes its price on the document at the same time.
	at := time.Now().UTC()
	start = time.Now()
	w.fs.Merge("trip_records/TRIP-LIVE", map[string]any{"status": "delivered", "deliveredTimestamp": dump.Timestamp{Time: at},
		"billingEstimateThb": 1234.5, "billingBaseRateThb": 1234.5, "updatedAt": dump.Timestamp{Time: at}})
	for _, c := range []*client{staff, disp} {
		f, took := await(t, c, "trip.delivered", trip, start, check)
		_, topic, data := f.envelope(t)
		if data["status"] != "delivered" {
			t.Errorf("trip.delivered on %s: %v", topic, data)
		}
		t.Logf("trip.delivered reached %s in %v", topic, took)
	}

	// The 3.x web creates a first-mile task for the own fleet's driver, billed to SPX: task.created on the tenant's and
	// the party's tasks topics.
	at = time.Now().UTC()
	start = time.Now()
	w.fs.Set("tasks/task_live", map[string]any{"taskId": "FM-12102026-901", "taskType": "FIRST_MILE", "status": "Assigned",
		"date": dump.Timestamp{Time: at.Add(time.Hour)}, "driverId": "drv_own", "sourceHub": "HUBA - Hub A", "destination": "SOCE (บัวโรย)",
		"billingCustomerId": "cust_spx", "createdAt": dump.Timestamp{Time: at}, "updatedAt": dump.Timestamp{Time: at}})
	task := ""
	deadline = time.Now().Add(budget)
	for task == "" && time.Now().Before(deadline) {
		_ = w.etl.QueryRow(context.Background(), `SELECT id::text FROM tasks WHERE legacy_doc_id = 'task_live'`).Scan(&task)
		if task == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if task == "" {
		t.Fatal("the listener did not apply the task within the budget")
	}
	want := map[*client]string{staff: realtime.TenantTopic(w.own, realtime.FamilyTasks), disp: realtime.DispatchTopic(spx, realtime.FamilyTasks)}
	for _, c := range []*client{staff, disp} {
		f, took := await(t, c, "task.created", task, start, check)
		if _, topic, data := f.envelope(t); topic != want[c] || data["taskType"] != "first_mile" {
			t.Errorf("task.created on %s: %v", topic, data)
		}
		t.Logf("task.created reached %s in %v", want[c], took)
	}

	// The task goes to another driver of the own fleet: task.reassigned.
	at = time.Now().UTC()
	start = time.Now()
	w.fs.Merge("tasks/task_live", map[string]any{"driverId": "drv_broker", "updatedAt": dump.Timestamp{Time: at}})
	for _, c := range []*client{staff, disp} {
		f, took := await(t, c, "task.reassigned", task, start, check)
		if _, topic, data := f.envelope(t); topic != want[c] || data["previousDriverId"] == nil {
			t.Errorf("task.reassigned on %s: %v", topic, data)
		}
		t.Logf("task.reassigned reached %s in %v", want[c], took)
	}

	// A trip of SPX's work moves to CJSF as it is delivered (one 3.x write sets both): SPX's dispatcher still hears of it,
	// so its board drops the row, but reads none of its new values (the envelope only); the tenant's staff read them.
	at = time.Now().UTC()
	start = time.Now()
	w.fs.Set("trip_records/TRIP-MOVE", map[string]any{"status": "in_transit", "jobType": "first_mile", "driverId": "uid_own",
		"billingCustomerId": "cust_spx", "createdAt": dump.Timestamp{Time: at}, "updatedAt": dump.Timestamp{Time: at}})
	moved := ""
	deadline = time.Now().Add(budget)
	for moved == "" && time.Now().Before(deadline) {
		_ = w.etl.QueryRow(context.Background(), `SELECT id::text FROM trip_records WHERE legacy_doc_id = 'TRIP-MOVE'`).Scan(&moved)
		if moved == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if moved == "" {
		t.Fatal("the listener did not apply the trip within the budget")
	}
	await(t, disp, "trip.created", moved, start, check)
	at = time.Now().UTC()
	start = time.Now()
	w.fs.Merge("trip_records/TRIP-MOVE", map[string]any{"billingCustomerId": "cust_cjsf", "status": "delivered",
		"deliveredTimestamp": dump.Timestamp{Time: at}, "updatedAt": dump.Timestamp{Time: at}})
	f, took = await(t, disp, "trip.delivered", moved, start, check)
	_, topic, data := f.envelope(t)
	if topic != realtime.DispatchTopic(spx, realtime.FamilyTrips) {
		t.Errorf("the trip that left SPX reached its dispatcher on %s", topic)
	}
	for k := range data {
		if !slices.Contains([]string{"entity", "id", "tenantId", "at", "version", "changed"}, k) {
			t.Errorf("SPX's dispatcher reads %s=%v of a trip that left its work", k, data[k])
		}
	}
	if ch, _ := data["changed"].([]any); ch == nil || len(ch) != 0 {
		t.Errorf("the envelope of a trip that left SPX: %v", data)
	}
	t.Logf("the trip that left SPX reached its dispatcher in %v", took)
	f, _ = await(t, staff, "trip.delivered", moved, start, check)
	if _, _, data := f.envelope(t); data["status"] != "delivered" || data["tripNo"] != "TRIP-MOVE" {
		t.Errorf("the tenant's staff got %v", data)
	}
}

// The changed[] names a dispatch topic keeps are columns of the scope_* projections (Appendix C §C.3.7) and none is a
// hidden column; the child markers stand for child projections that exist.
func TestDispatchColumnsAreScopeColumns(t *testing.T) {
	w := newWorld(t)
	views := map[string]string{"task": "scope_tasks", "trip": "scope_trips", "standby": "scope_standby", "incident": "scope_incidents"}
	markers := map[string]string{realtime.ChildDeliveryStops: "scope_task_stops", realtime.ChildPhotosOrStops: "scope_trip_stops"}
	for entity, view := range views {
		rows, err := w.etl.Query(context.Background(), `SELECT attname::text FROM pg_attribute
			WHERE attrelid = $1::regclass AND attnum > 0 AND NOT attisdropped`, view)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil || len(cols) == 0 {
			t.Fatalf("%s: %v %v", view, cols, err)
		}
		for _, n := range realtime.DispatchColumns(entity) {
			if v, ok := markers[n]; ok {
				if w.id(`SELECT count(*)::text FROM pg_class WHERE relname = $1 AND relkind = 'v'`, v) != "1" {
					t.Errorf("%s: the marker %s has no view %s", entity, n, v)
				}
				continue
			}
			col := snake(n)
			if !slices.Contains(cols, col) || scope.IsHidden(col) {
				t.Errorf("%s: %s (%s) is not a column of %s a dispatcher may read", entity, n, col, view)
			}
		}
	}
	if w.id(`SELECT count(*)::text FROM pg_class WHERE relname = 'scope_trip_photos' AND relkind = 'v'`) != "1" {
		t.Error("photosOrStops also stands for scope_trip_photos")
	}
}

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) { // the inverse of the mirror's camel (situation1_photo_file_id -> situation1PhotoFileId)
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
