//go:build integration

// The class C write-back of T31 end to end against the coexistence it was built for: the operations routes write the
// Firestore document first (the gcptest fake, through the same writeback.Firestore adapter the api uses), the
// mirror's listener applies it to PostgreSQL and raises cache:mirror_ack, and the handler, which waited for that ack,
// answers with the row as GET reads it. The minted uuidv7 doc id is the row's id (R25), and PostgreSQL is never
// written by the handler itself.
package coexist

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/push/pushtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

func TestWriteBackThroughTheMirror(t *testing.T) {
	f := newFixture(t, true)
	p := plan(t, "auth,masterdata")
	rdb, ks := cachetest.NewClient(t)
	acks := cache.New(rdb, ks)
	l, err := NewListener(ListenConfig{Interval: 50 * time.Millisecond, ETLPool: f.etl, Source: f.fs.Firestore(), Plan: p.Mirror,
		Bucket: "logitrack", PublicBucket: "logitrack-public", Lease: NewRedisLease(rdb, ks.MirrorListenLock(), ListenLeaseTTL),
		Acks: acks, Log: zerolog.Nop(), Metrics: NewMetrics(nil)})
	if err != nil || l == nil {
		t.Fatalf("listener %v %v", l, err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	svc, err := operations.New(operations.Deps{Pool: f.app, Log: zerolog.Nop(),
		WriteBack: &operations.WriteBack{Writer: writeback.Firestore{FS: f.fs.Firestore()}, Acks: acks, Wait: 10 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	own := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	staff := &authz.Principal{UserID: uuid.New(), TenantID: &own, TenantRole: authz.TenantAdmin, TenantKind: "own_fleet",
		Steward: true, Caps: authz.TenantDefaults(authz.TenantAdmin)}
	var driver, party uuid.UUID
	if err := f.etl.QueryRow(f.ctx, `SELECT id FROM drivers WHERE legacy_doc_id = 'drv_own'`).Scan(&driver); err != nil {
		t.Fatal(err)
	}
	if err := f.etl.QueryRow(f.ctx, `SELECT bp.id FROM billing_parties bp JOIN customers c ON c.id = bp.customer_id
		WHERE c.legacy_doc_id = 'cust_cjsf'`).Scan(&party); err != nil {
		t.Fatal(err)
	}
	var before int
	_ = f.etl.QueryRow(f.ctx, `SELECT count(*) FROM outbox_events WHERE headers->>'origin' IS NULL`).Scan(&before)
	body, _ := json.Marshal(map[string]any{"taskType": "first_mile", "date": time.Now().In(time.UTC).Format(time.DateOnly),
		"time": "08:15", "sourceHub": "HUBA", "destination": "SOCE", "billingPartyId": party, "driverId": driver,
		"jobCategory": "SUPPLEMENTARY"})
	res, err := svc.CreateTaskAPI(f.ctx, staff, "rid-1", body)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusCreated {
		t.Fatalf("not acknowledged within the wait: %d %+v", res.Status, res.Data)
	}
	view, _ := json.Marshal(res.Data)
	var got map[string]any
	_ = json.Unmarshal(view, &got)
	id := got["id"].(string)
	if _, ok := f.fs.Doc("tasks/" + id); !ok {
		t.Fatalf("no document under the row id %s", id)
	}
	if got["driverId"] != driver.String() || got["jobCategory"] != "SUPPLEMENTARY" || got["planTime"] != "08:15" ||
		got["status"] != "assigned" || got["billingPartyId"] != party.String() || got["taskNo"] == nil {
		t.Fatalf("the mirrored row %v", got)
	}
	var legacy string
	if err := f.etl.QueryRow(f.ctx, `SELECT legacy_doc_id FROM tasks WHERE id = $1`, id).Scan(&legacy); err != nil || legacy != id {
		t.Fatalf("legacy_doc_id %q %v", legacy, err)
	}
	// The write-back's own event is the push (origin=writeback, no topic); the mirror's event of the same change
	// (origin=etl.sync) carries the realtime topics; no event without an origin was appended by the handler.
	var wb, mirrored, plain int
	_ = f.etl.QueryRow(f.ctx, `SELECT count(*) FILTER (WHERE headers->>'origin' = 'writeback' AND routing_key = 'task.assigned'
			AND cardinality(realtime_topics) = 0),
		count(*) FILTER (WHERE headers->>'origin' = 'etl.sync' AND routing_key = 'task.created'),
		count(*) FILTER (WHERE headers->>'origin' IS NULL)
		FROM outbox_events WHERE aggregate_id = $1`, id).Scan(&wb, &mirrored, &plain)
	if wb != 1 || mirrored != 1 || plain != 0 {
		t.Fatalf("events: write-back %d, mirror %d, plain %d", wb, mirrored, plain)
	}
	// A PATCH writes only its fields and is answered from the mirrored row.
	res, err = svc.PatchTaskAPI(f.ctx, staff, "rid-2", uuid.MustParse(id), []byte(`{"jobCategory":"PRIMARY","time":"10:45"}`))
	if err != nil || res.Status != http.StatusOK {
		t.Fatalf("patch %d %v", res.Status, err)
	}
	view, _ = json.Marshal(res.Data)
	_ = json.Unmarshal(view, &got)
	if got["jobCategory"] != "PRIMARY" || got["planTime"] != "10:45" || got["driverId"] != driver.String() {
		t.Fatalf("patched row %v", got)
	}
	// The document is deleted in Firestore before the mirror removes anything: Firestore answers the update's
	// exists=true precondition with 404 NOT_FOUND, and the route answers 409 document_changed (reload), not 503.
	f.fs.Delete("tasks/" + id)
	_, err = svc.PatchTaskAPI(f.ctx, staff, "rid-3", uuid.MustParse(id), []byte(`{"time":"11:00"}`))
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != http.StatusConflict || he.Details["reason"] != operations.ReasonDocumentChanged {
		t.Fatalf("a PATCH of a deleted document: %v", err)
	}
}

// startListener runs the mirror's listeners until the returned stop is called.
func startListener(t *testing.T, f *fixture, rdb redis.UniversalClient, ks cache.Keyspace, acks Acker) (stop func()) {
	t.Helper()
	l, err := NewListener(ListenConfig{Interval: 50 * time.Millisecond, ETLPool: f.etl, Source: f.fs.Firestore(),
		Plan: plan(t, "auth,masterdata").Mirror, Bucket: "logitrack", PublicBucket: "logitrack-public",
		Lease: NewRedisLease(rdb, ks.MirrorListenLock(), ListenLeaseTTL), Acks: acks, Log: zerolog.Nop(), Metrics: NewMetrics(nil)})
	if err != nil || l == nil {
		t.Fatalf("listener %v %v", l, err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()
	stopped := false
	stop = func() {
		if !stopped {
			stopped = true
			cancel()
			<-done
		}
	}
	t.Cleanup(stop)
	return stop
}

// outboxDelivery is the notify.fcm delivery of the newest outbox event of an aggregate with a routing key and an
// origin, as the relay publishes it.
func outboxDelivery(t *testing.T, f *fixture, aggregate, key, origin string) *mq.Delivery {
	t.Helper()
	var id int64
	var tenant *uuid.UUID
	var payload []byte
	if err := f.etl.QueryRow(f.ctx, `SELECT id, tenant_id, payload FROM outbox_events
		WHERE aggregate_id = $1 AND routing_key = $2 AND headers->>'origin' = $3 ORDER BY id DESC LIMIT 1`,
		aggregate, key, origin).Scan(&id, &tenant, &payload); err != nil {
		t.Fatalf("outbox %s %s %s: %v", aggregate, key, origin, err)
	}
	return &mq.Delivery{Queue: notify.QueueFCM, MessageID: strconv.FormatInt(id, 10), Exchange: mq.ExchangeEvents,
		RoutingKey: key, EventType: key, TenantID: tenant, Body: payload, Headers: map[string]any{outbox.HeaderOrigin: origin}}
}

// T31 review: a write-back PATCH that assigns a mirrored task to a driver and is answered 202 (no acknowledgement
// within the wait) still pushes exactly once. The writer's task.reassigned (origin=writeback) is handled while the
// mirror has not applied the PATCH (the listener is stopped): it fails transiently and pushes nothing. Once the listener
// applied it and raised cache:mirror_ack, the mirror's own task.reassigned (origin=etl.sync) pushes nothing and the
// retry of the writer's event pushes the assignment to the new driver's device once. (A driverless task is not the
// fixture here: the mirror stamps a task without a driver into the quarantine tenant, D6.)
func TestWriteBackPushFollowsTheMirror(t *testing.T) {
	f := newFixture(t, true)
	rdb, ks := cachetest.NewClient(t)
	acks := cache.New(rdb, ks)
	stop := startListener(t, f, rdb, ks, acks)
	acked, err := operations.New(operations.Deps{Pool: f.app, Log: zerolog.Nop(),
		WriteBack: &operations.WriteBack{Writer: writeback.Firestore{FS: f.fs.Firestore()}, Acks: acks, Wait: 10 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	unacked, err := operations.New(operations.Deps{Pool: f.app, Log: zerolog.Nop(),
		WriteBack: &operations.WriteBack{Writer: writeback.Firestore{FS: f.fs.Firestore()}}}) // no Acks: every write 202
	if err != nil {
		t.Fatal(err)
	}
	own := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	staff := &authz.Principal{UserID: uuid.New(), TenantID: &own, TenantRole: authz.TenantAdmin, TenantKind: "own_fleet",
		Steward: true, Caps: authz.TenantDefaults(authz.TenantAdmin)}
	var driver, first, party uuid.UUID
	if err := f.etl.QueryRow(f.ctx, `SELECT id FROM drivers WHERE legacy_doc_id = 'drv_own'`).Scan(&driver); err != nil {
		t.Fatal(err)
	}
	if err := f.etl.QueryRow(f.ctx, `INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile, legacy_doc_id)
		VALUES ($1, 'self', 'สมปอง', 'ทดสอบ', '0810000009', 'drv_first') RETURNING id`, own).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := f.etl.QueryRow(f.ctx, `SELECT bp.id FROM billing_parties bp JOIN customers c ON c.id = bp.customer_id
		WHERE c.legacy_doc_id = 'cust_cjsf'`).Scan(&party); err != nil {
		t.Fatal(err)
	}
	// The new driver's phone.
	var user uuid.UUID
	if err := f.etl.QueryRow(f.ctx, `SELECT user_id FROM drivers WHERE id = $1`, driver).Scan(&user); err != nil || user == uuid.Nil {
		if err := f.etl.QueryRow(f.ctx, `INSERT INTO users (email, display_name) VALUES ('wbpush@example.test', 'wb')
			RETURNING id`).Scan(&user); err != nil {
			t.Fatal(err)
		}
		if _, err := f.etl.Exec(f.ctx, `INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'driver')`, user, own); err != nil {
			t.Fatal(err)
		}
		if _, err := f.etl.Exec(f.ctx, `UPDATE drivers SET user_id = $2 WHERE id = $1`, driver, user); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.etl.Exec(f.ctx, `INSERT INTO device_tokens (user_id, install_id, token, platform) VALUES ($1, 'inst-wb', 'tok-wb', 'android')`,
		user); err != nil {
		t.Fatal(err)
	}
	fcm := pushtest.New(t)
	// The consumer's clock follows the fake Firestore's logical one (2026-10-01T00:00:00Z plus a microsecond a write),
	// so the writes are minutes old, not days (a write older than cache.MirrorAckTTL is taken as applied).
	consumer := &notify.FCM{Pool: f.app, Sender: fcm.Client(0), Redis: rdb, Keys: ks, Enabled: true, Log: zerolog.Nop(),
		Now: func() time.Time { return time.Date(2026, 10, 1, 0, 1, 0, 0, time.UTC) }}

	// A task of the first driver, mirrored and acknowledged.
	body, _ := json.Marshal(map[string]any{"taskType": "first_mile", "date": time.Now().UTC().Format(time.DateOnly),
		"sourceHub": "HUBA", "destination": "SOCE", "billingPartyId": party, "driverId": first})
	res, err := acked.CreateTaskAPI(f.ctx, staff, "rid-1", body)
	if err != nil || res.Status != http.StatusCreated {
		t.Fatalf("create %d %v %+v", res.Status, err, res.Data)
	}
	view, _ := json.Marshal(res.Data)
	var got map[string]any
	_ = json.Unmarshal(view, &got)
	id := got["id"].(string)

	// The mirror stops; the PATCH assigns the driver and is answered 202.
	stop()
	res, err = unacked.PatchTaskAPI(f.ctx, staff, "rid-2", uuid.MustParse(id), []byte(`{"driverId":"`+driver.String()+`"}`))
	if err != nil || res.Status != http.StatusAccepted {
		t.Fatalf("patch %d %v", res.Status, err)
	}
	writer := outboxDelivery(t, f, id, notify.RouteTaskReassigned, outbox.OriginWriteBack)
	if err := consumer.Handle(f.ctx, writer); err == nil || mq.IsPermanent(err) {
		t.Fatalf("the writer's push before the mirror applied the PATCH: %v, want a transient error", err)
	}
	if fcm.Calls() != 0 {
		t.Fatal("pushed before the mirror applied the PATCH")
	}

	// The mirror applies the PATCH and acknowledges it.
	startListener(t, f, rdb, ks, acks)
	deadline := time.Now().Add(20 * time.Second)
	for {
		var n int
		_ = f.etl.QueryRow(f.ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND routing_key = 'task.reassigned'
			AND headers->>'origin' = 'etl.sync'`, id).Scan(&n)
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the mirror did not apply the PATCH")
		}
		time.Sleep(50 * time.Millisecond)
	}
	var updated time.Time
	if err := json.Unmarshal(mustJSON(t, res.Data, "updateTime"), &updated); err != nil {
		t.Fatal(err)
	}
	for {
		at, ok, err := acks.MirrorAck(f.ctx, "tasks", id)
		if err == nil && ok && !at.Before(updated.Truncate(time.Microsecond)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the mirror did not acknowledge the PATCH")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := consumer.Handle(f.ctx, outboxDelivery(t, f, id, notify.RouteTaskReassigned, outbox.OriginMirror)); err != nil {
		t.Fatal(err)
	}
	if fcm.Calls() != 0 {
		t.Fatal("the mirror's event pushed")
	}
	if err := consumer.Handle(f.ctx, writer); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Handle(f.ctx, writer); err != nil { // a redelivery
		t.Fatal(err)
	}
	sent := fcm.SentTo("tok-wb")
	if len(sent) != 1 || sent[0].Message.Data["type"] != "first_mile_task_assigned" || fcm.Calls() != 1 {
		t.Fatalf("pushes %+v (%d calls), want one first_mile_task_assigned", sent, fcm.Calls())
	}
}

// mustJSON is the JSON of one field of a value marshalled as the API answers it.
func mustJSON(t *testing.T, v any, field string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil || m[field] == nil {
		t.Fatalf("%s of %s: %v", field, b, err)
	}
	return m[field]
}
