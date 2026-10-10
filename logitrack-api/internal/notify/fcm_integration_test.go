//go:build integration

// Acceptance tests of notify.fcm (issue T13) on PostgreSQL 18 (the goose chain), Redis 7 (compose image)
// and the FCM HTTP v1 stand-in (pushtest: the real push.Client, its OAuth2 grant and wire format, no
// Google): UNREGISTERED deletes the token, the legacy data.type strings and channels, one silent
// tasks_changed per driver per 30 s, one session_revoked to the revoked session's device only (end to
// end from auth's revocation through the relay and RabbitMQ), and never the same message twice to a
// token.
package notify_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/push"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/push/pushtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

type fcmEnv struct {
	t        *testing.T
	ctx      context.Context
	app, etl *pgxpool.Pool
	rdb      *redis.Client
	ks       cache.Keyspace
	fcm      *pushtest.Server
	consumer *notify.FCM
	tenant   uuid.UUID
	seq      atomic.Int64
}

func newFCMEnv(t *testing.T) *fcmEnv {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	ks, err := cache.NewKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	e := &fcmEnv{t: t, ctx: ctx, app: d.Pool(t, db.RoleApp), etl: d.Pool(t, db.RoleETL),
		rdb: asynctest.SharedRedis(t).Client(t), ks: ks, fcm: pushtest.New(t)}
	e.seq.Store(1000)
	e.consumer = &notify.FCM{Pool: e.app, Sender: e.fcm.Client(0), Redis: e.rdb, Keys: ks, Enabled: true, Log: zerolog.Nop()}
	e.tenant = scan[uuid.UUID](e, `INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'กองรถ', 'Own fleet') RETURNING id`)
	return e
}

func scan[T any](e *fcmEnv, sql string, args ...any) T {
	e.t.Helper()
	var v T
	if err := e.etl.QueryRow(e.ctx, sql, args...).Scan(&v); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func (e *fcmEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.etl.Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *fcmEnv) user(email string) uuid.UUID {
	return scan[uuid.UUID](e, `INSERT INTO users (email, display_name) VALUES ($1::text, $1::text) RETURNING id`, email)
}

// driver links a new driver to a new user; legacy is the Firestore doc id ("" for a Go-created one).
func (e *fcmEnv) driver(email, legacy string) (driver, user uuid.UUID) {
	user = e.user(email)
	e.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'driver')`, user, e.tenant)
	var leg any
	if legacy != "" {
		leg = legacy
	}
	driver = scan[uuid.UUID](e, `INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, mobile, legacy_doc_id)
		VALUES ($1, 'self', $2, 'สมชาย', 'ใจดี', '0810000000', $3) RETURNING id`, e.tenant, user, leg)
	return driver, user
}

func (e *fcmEnv) device(user uuid.UUID, install, tok, platform string) {
	e.exec(`INSERT INTO device_tokens (user_id, install_id, token, platform) VALUES ($1, $2, $3, $4)`, user, install, tok, platform)
}

func (e *fcmEnv) task(driver, helper *uuid.UUID, legacy string) uuid.UUID {
	var leg any
	if legacy != "" {
		leg = legacy
	}
	return scan[uuid.UUID](e, `INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, plan_time, source_hub_raw,
		destination_raw, driver_id, helper_driver_id, legacy_doc_id)
		VALUES ($1, 'driver', 'first_mile', 'assigned', '2026-10-10 08:30:00+07', '08:30', 'SPK-GW', 'SOCE', $2, $3, $4) RETURNING id`,
		e.tenant, driver, helper, leg)
}

// delivery builds a notify.fcm delivery with a fresh message id, as the relay would publish it.
func (e *fcmEnv) delivery(key string, payload any) *mq.Delivery {
	e.t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		e.t.Fatal(err)
	}
	return &mq.Delivery{Queue: notify.QueueFCM, MessageID: strconv.FormatInt(e.seq.Add(1), 10), Exchange: mq.ExchangeEvents,
		RoutingKey: key, EventType: key, TenantID: &e.tenant, Body: b}
}

func (e *fcmEnv) handle(d *mq.Delivery) {
	e.t.Helper()
	if err := e.consumer.Handle(e.ctx, d); err != nil {
		e.t.Fatalf("%s %s: %v", d.RoutingKey, d.MessageID, err)
	}
}

type deliveryRow struct {
	User, Install, Kind, Status string
	Data                        map[string]string
	Error                       *string
	Sent                        bool
	EventID                     *int64
}

func (e *fcmEnv) rows() []deliveryRow {
	e.t.Helper()
	rs, err := e.etl.Query(e.ctx, `SELECT user_id::text, install_id, kind, status, payload, error, sent_at IS NOT NULL, outbox_event_id
		FROM notification_deliveries ORDER BY outbox_event_id, install_id, kind`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rs.Close()
	var out []deliveryRow
	for rs.Next() {
		var r deliveryRow
		var raw []byte
		if err := rs.Scan(&r.User, &r.Install, &r.Kind, &r.Status, &raw, &r.Error, &r.Sent, &r.EventID); err != nil {
			e.t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &r.Data); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func count(rows []deliveryRow, kind, status string) int {
	n := 0
	for _, r := range rows {
		if r.Kind == kind && r.Status == status {
			n++
		}
	}
	return n
}

// The visible task pushes keep the legacy contract: data.type {first_mile|line_haul}_task_*, keys
// taskId and driverId (the Firestore document ids while APK 3.x is installed), the legacy texts,
// Android channel task_assignments, priority high, APNs sound default. Recipients are read from tasks:
// a stale task.assigned whose driver no longer holds the task sends nothing.
func TestFCMTaskPushesKeepTheLegacyContract(t *testing.T) {
	e := newFCMEnv(t)
	a, ua := e.driver("a@logitrack.test", "fsDriverA")
	b, ub := e.driver("b@logitrack.test", "")
	e.device(ua, "inst-a1", "tok-a1", "android")
	e.device(ua, "inst-a2", "tok-a2", "ios")
	e.device(ub, "inst-b", "tok-b", "android")
	task := e.task(&a, nil, "fsTask1")

	e.handle(e.delivery(notify.RouteTaskAssigned, map[string]any{"id": task, "driverId": a}))
	sent := e.fcm.Sent()
	if len(sent) != 2 {
		t.Fatalf("%d pushes for task.assigned, want one per device of driver A", len(sent))
	}
	for _, s := range sent {
		m := s.Message
		want := map[string]string{"type": "first_mile_task_assigned", "taskId": "fsTask1", "driverId": "fsDriverA"}
		if !maps.Equal(m.Data, want) || m.Notification == nil || m.Notification.Title != "New task assigned" ||
			m.Notification.Body != "SPK-GW → SOCE (2026-10-10 08:30)" {
			t.Fatalf("assigned push %s", s.Raw)
		}
		if m.Android == nil || m.Android.Priority != "high" || m.Android.Notification.ChannelID != notify.ChannelTaskAssignments ||
			m.APNS == nil || m.APNS.Payload.Aps.Sound != "default" {
			t.Fatalf("assigned push options %s", s.Raw)
		}
	}

	// Reassigned A -> B: A hears unassigned, B assigned (B has no legacy doc id: its uuid).
	e.exec(`UPDATE tasks SET driver_id = $2 WHERE id = $1`, task, b)
	e.handle(e.delivery(notify.RouteTaskReassigned, map[string]any{"id": task, "driverId": b, "previousDriverId": a}))
	for _, tok := range []string{"tok-a1", "tok-a2"} {
		got := e.fcm.SentTo(tok)
		if len(got) != 2 || got[1].Message.Data["type"] != "first_mile_task_unassigned" ||
			got[1].Message.Notification.Body != "You have been unassigned from this task." {
			t.Fatalf("%s: %+v", tok, got)
		}
	}
	gotB := e.fcm.SentTo("tok-b")
	if len(gotB) != 1 || !maps.Equal(gotB[0].Message.Data, map[string]string{"type": "first_mile_task_assigned", "taskId": "fsTask1", "driverId": b.String()}) {
		t.Fatalf("B: %+v", gotB)
	}

	// A late task.assigned for A (A no longer holds the task) sends nothing; a task.reassigned that
	// changed nothing sends nothing either.
	e.handle(e.delivery(notify.RouteTaskAssigned, map[string]any{"id": task, "driverId": a}))
	e.handle(e.delivery(notify.RouteTaskReassigned, map[string]any{"id": task, "driverId": b, "previousDriverId": b}))
	if n := len(e.fcm.Sent()); n != 5 {
		t.Fatalf("%d pushes after stale events, want 5", n)
	}

	// Cancelled: the current driver hears it.
	e.exec(`UPDATE tasks SET status = 'cancelled', cancelled_at = now() WHERE id = $1`, task)
	e.handle(e.delivery(notify.RouteTaskCancelled, map[string]any{"id": task}))
	gotB = e.fcm.SentTo("tok-b")
	if len(gotB) != 2 || gotB[1].Message.Data["type"] != "first_mile_task_cancelled" ||
		gotB[1].Message.Notification.Body != "This first mile task has been cancelled." {
		t.Fatalf("cancelled: %+v", gotB)
	}

	rows := e.rows()
	if len(rows) != 6 || count(rows, notify.KindTaskAssigned, notify.StatusSent) != 3 ||
		count(rows, notify.KindTaskUnassigned, notify.StatusSent) != 2 || count(rows, notify.KindTaskCancelled, notify.StatusSent) != 1 {
		t.Fatalf("delivery rows %+v", rows)
	}
	for _, r := range rows {
		if !r.Sent || r.Error != nil || r.EventID == nil || r.Data["type"] == "" {
			t.Fatalf("row %+v", r)
		}
	}
}

// AC: two task.updated events within 30 s for one driver produce one silent push (R21). The window is
// per driver: the helper's queue has its own; a suppressed push is logged deduplicated.
func TestFCMTasksChangedOncePerDriverPer30s(t *testing.T) {
	e := newFCMEnv(t)
	a, ua := e.driver("a@logitrack.test", "")
	h, uh := e.driver("helper@logitrack.test", "")
	e.device(ua, "inst-a1", "tok-a1", "android")
	e.device(ua, "inst-a2", "tok-a2", "android")
	e.device(uh, "inst-h", "tok-h", "android")
	task := e.task(&a, &h, "")

	e.handle(e.delivery(notify.RouteTaskUpdated, map[string]any{"id": task}))
	e.handle(e.delivery(notify.RouteTaskUpdated, map[string]any{"id": task}))
	e.handle(e.delivery(notify.RouteTaskCheckedIn, map[string]any{"id": task}))
	sent := e.fcm.Sent()
	if len(sent) != 3 {
		t.Fatalf("%d silent pushes, want one per device for the first event only", len(sent))
	}
	for _, s := range sent {
		m := s.Message
		if m.Notification != nil || !maps.Equal(m.Data, map[string]string{"type": "tasks_changed"}) || m.Android.Priority != "normal" ||
			m.APNS.Headers["apns-priority"] != "5" || m.APNS.Payload.Aps.ContentAvailable != 1 {
			t.Fatalf("tasks_changed is not silent: %s", s.Raw)
		}
	}
	rows := e.rows()
	if count(rows, notify.KindTasksChanged, notify.StatusSent) != 3 || count(rows, notify.KindTasksChanged, notify.StatusDeduplicated) != 6 {
		t.Fatalf("rows %+v", rows)
	}
	ttl := e.rdb.TTL(e.ctx, e.ks.IdemTasksChangedPush(a.String())).Val()
	if ttl <= 0 || ttl > notify.TasksChangedWindow {
		t.Fatalf("idem:push:tasks_changed TTL %v", ttl)
	}

	// Once A's 30 s are over, the next event reaches A again; the helper is still inside its window
	// only if its key is: both expire together here, so drop A's alone to show they are per driver.
	e.rdb.Del(e.ctx, e.ks.IdemTasksChangedPush(a.String()))
	e.handle(e.delivery(notify.RouteTaskPlanDateChanged, map[string]any{"id": task}))
	if len(e.fcm.SentTo("tok-a1")) != 2 || len(e.fcm.SentTo("tok-a2")) != 2 || len(e.fcm.SentTo("tok-h")) != 1 {
		t.Fatalf("after A's window: a1 %d a2 %d h %d", len(e.fcm.SentTo("tok-a1")), len(e.fcm.SentTo("tok-a2")), len(e.fcm.SentTo("tok-h")))
	}
}

// AC: an UNREGISTERED answer deletes the token row; INVALID_ARGUMENT naming the token too; a payload
// error is logged failed and keeps the token.
func TestFCMUnregisteredDeletesToken(t *testing.T) {
	e := newFCMEnv(t)
	a, ua := e.driver("a@logitrack.test", "")
	e.device(ua, "inst-dead", "tok-dead", "android")
	e.device(ua, "inst-bad", "tok-bad", "ios")
	e.device(ua, "inst-payload", "tok-payload", "android")
	e.device(ua, "inst-ok", "tok-ok", "android")
	e.fcm.Respond("tok-dead", pushtest.Unregistered())
	e.fcm.Respond("tok-bad", pushtest.InvalidToken())
	e.fcm.Respond("tok-payload", pushtest.InvalidPayload())
	task := e.task(&a, nil, "")

	e.handle(e.delivery(notify.RouteTaskAssigned, map[string]any{"id": task, "driverId": a}))
	left := []string{}
	rs, err := e.etl.Query(e.ctx, `SELECT token FROM device_tokens WHERE user_id = $1 ORDER BY token`, ua)
	if err != nil {
		t.Fatal(err)
	}
	for rs.Next() {
		var s string
		if err := rs.Scan(&s); err != nil {
			t.Fatal(err)
		}
		left = append(left, s)
	}
	rs.Close()
	if !slices.Equal(left, []string{"tok-ok", "tok-payload"}) {
		t.Fatalf("tokens left %v", left)
	}
	byInstall := map[string]deliveryRow{}
	for _, r := range e.rows() {
		byInstall[r.Install] = r
	}
	for install, want := range map[string]string{"inst-dead": notify.StatusTokenInvalid, "inst-bad": notify.StatusTokenInvalid,
		"inst-payload": notify.StatusFailed, "inst-ok": notify.StatusSent} {
		r := byInstall[install]
		if r.Status != want || (want != notify.StatusSent) != (r.Error != nil) || r.Sent != (want == notify.StatusSent) {
			t.Fatalf("%s: %+v, want %s", install, r, want)
		}
	}
	if *byInstall["inst-dead"].Error != "fcm: HTTP 404 (UNREGISTERED)" {
		t.Fatalf("error %q", *byInstall["inst-dead"].Error)
	}
}

// AC: the same message id is never sent twice to a token within 24 h (idem:fcm). A transient failure
// retries only the tokens that need it; a redelivery after the record is a no-op; even with the
// PostgreSQL record gone, the Redis claims keep the pushes from going out again; a claim another
// attempt holds is waited for, and given up as interrupted once stale (not resent).
func TestFCMSameMessageNeverSentTwice(t *testing.T) {
	e := newFCMEnv(t)
	a, ua := e.driver("a@logitrack.test", "")
	e.device(ua, "inst-1", "tok-1", "android")
	e.device(ua, "inst-2", "tok-2", "android")
	e.fcm.Respond("tok-2", pushtest.Unavailable())
	task := e.task(&a, nil, "")
	d := e.delivery(notify.RouteTaskAssigned, map[string]any{"id": task, "driverId": a})

	err := e.consumer.Handle(e.ctx, d)
	if err == nil || mq.IsPermanent(err) {
		t.Fatalf("a 503 must retry the message: %v", err)
	}
	if len(e.fcm.SentTo("tok-1")) != 1 || len(e.fcm.SentTo("tok-2")) != 0 || len(e.rows()) != 0 {
		t.Fatal("first attempt: tok-1 sent, tok-2 pending, nothing recorded")
	}
	e.handle(d) // the retry
	if len(e.fcm.SentTo("tok-1")) != 1 || len(e.fcm.SentTo("tok-2")) != 1 || count(e.rows(), notify.KindTaskAssigned, notify.StatusSent) != 2 {
		t.Fatalf("retry: tok-1 %d tok-2 %d rows %+v", len(e.fcm.SentTo("tok-1")), len(e.fcm.SentTo("tok-2")), e.rows())
	}
	key := e.ks.IdemFCM(d.MessageID, notify.TokenID("tok-1"))
	if ttl := e.rdb.TTL(e.ctx, key).Val(); ttl < 23*time.Hour || ttl > notify.PushIdemTTL {
		t.Fatalf("idem:fcm TTL %v", ttl)
	}
	calls := e.fcm.Calls()
	e.handle(d) // redelivered after the record: the inbox answers
	if e.fcm.Calls() != calls || len(e.rows()) != 2 {
		t.Fatal("a recorded message was processed again")
	}
	// The PostgreSQL record lost (or never committed): Redis still knows both pushes went out.
	e.exec(`DELETE FROM consumer_inbox WHERE consumer = 'notify.fcm'`)
	e.exec(`DELETE FROM notification_deliveries`)
	e.handle(d)
	if e.fcm.Calls() != calls || count(e.rows(), notify.KindTaskAssigned, notify.StatusSent) != 2 {
		t.Fatalf("resent from a lost record: calls %d -> %d, rows %+v", calls, e.fcm.Calls(), e.rows())
	}

	// Another attempt holds the claim of a third device: wait (retry) while it is fresh ...
	e.device(ua, "inst-3", "tok-3", "android")
	d2 := e.delivery(notify.RouteTaskAssigned, map[string]any{"id": task, "driverId": a})
	k3 := e.ks.IdemFCM(d2.MessageID, notify.TokenID("tok-3"))
	e.rdb.Set(e.ctx, k3, "pending|"+strconv.FormatInt(time.Now().UnixMilli(), 10)+"|other", time.Hour)
	if err := e.consumer.Handle(e.ctx, d2); err == nil || mq.IsPermanent(err) {
		t.Fatalf("a fresh foreign claim must retry: %v", err)
	}
	// ... and give it up as interrupted once stale: recorded failed, never sent.
	e.rdb.Set(e.ctx, k3, "pending|"+strconv.FormatInt(time.Now().Add(-notify.InFlightStale-time.Second).UnixMilli(), 10)+"|other", time.Hour)
	e.handle(d2)
	if len(e.fcm.SentTo("tok-3")) != 0 {
		t.Fatal("an interrupted push was sent again")
	}
	var interrupted int
	for _, r := range e.rows() {
		if r.Install == "inst-3" && r.Status == notify.StatusFailed && r.Error != nil {
			interrupted++
		}
	}
	if interrupted != 1 || len(e.fcm.SentTo("tok-1")) != 2 || len(e.fcm.SentTo("tok-2")) != 2 {
		t.Fatalf("second message: interrupted %d, tok-1 %d, tok-2 %d", interrupted, len(e.fcm.SentTo("tok-1")), len(e.fcm.SentTo("tok-2")))
	}
}

// R83: an install signed in again after the revocation holds a live session; the push would end it, so
// nothing is sent. A session without a device token (web) has nothing to send to.
func TestFCMSessionRevokedSkipsAReSignedInstall(t *testing.T) {
	e := newFCMEnv(t)
	u := e.user("u@logitrack.test")
	e.device(u, "inst-a", "tok-a", "android")
	session := func(install any, revoked bool) uuid.UUID {
		reason := any(nil)
		at := any(nil)
		if revoked {
			reason, at = "password_reset", time.Now()
		}
		return scan[uuid.UUID](e, `INSERT INTO sessions (user_id, platform, amr, install_id, absolute_expires_at, revoked_at, revoked_reason)
			VALUES ($1, 'android', 'pwd', $2, now() + interval '90 days', $3, $4) RETURNING id`, u, install, at, reason)
	}
	old := session("inst-a", true)
	web := session(nil, true)
	session("inst-a", false) // signed in again
	e.handle(e.delivery(notify.RouteSessionsRevoked, map[string]any{"userId": u, "sessionIds": []uuid.UUID{old, web}, "reason": "password_reset"}))
	if len(e.fcm.Sent()) != 0 || len(e.rows()) != 0 {
		t.Fatalf("pushed to a re-signed install: %+v", e.fcm.Sent())
	}
}

// AC: revoking one session sends one session_revoked data message to that session's device token,
// none to the user's other devices, and logs one notification_deliveries row of kind session_revoked;
// a role change (claims_changed) sends none. End to end: auth's revocation commits
// user.sessions_revoked, the relay publishes it, RabbitMQ routes it to notify.fcm, the consumer pushes.
func TestFCMSessionRevokedEndToEnd(t *testing.T) {
	e := newFCMEnv(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := password.NewHasher(password.Params{MemoryKB: 8 * 1024, Iterations: 1, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := password.NewPolicy(10)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.New(auth.Config{RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour,
		PasswordResetTTL: 30 * time.Minute, LoginIP: ratelimit.Limit{Count: 1000, Window: time.Minute}},
		auth.Deps{Pool: e.app, Store: auth.NewStore(e.rdb, e.ks.Prefix()), Limiter: ratelimit.New(e.rdb, e.ks, zerolog.Nop()),
			Keys: token.New(priv, "http://localhost", "test", 15*time.Minute), Hasher: hasher, Policy: policy, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)

	u := e.user("driver@logitrack.test")
	other := e.user("other@logitrack.test")
	session := func(user uuid.UUID, install string) uuid.UUID {
		return scan[uuid.UUID](e, `INSERT INTO sessions (user_id, platform, amr, install_id, absolute_expires_at)
			VALUES ($1, 'android', 'pwd', $2, now() + interval '90 days') RETURNING id`, user, install)
	}
	phone := session(u, "inst-phone")
	session(u, "inst-tablet")
	session(other, "inst-other")
	e.device(u, "inst-phone", "tok-phone", "android")
	e.device(u, "inst-tablet", "tok-tablet", "android")
	e.device(other, "inst-other", "tok-other", "ios")

	admin := e.user("admin@logitrack.test")
	if sids, err := svc.Revoke(e.ctx, auth.Revocation{UserID: u, Reason: auth.RevokeAdmin, SessionIDs: []uuid.UUID{phone}, RevokedBy: &admin}); err != nil || len(sids) != 1 {
		t.Fatalf("revoke: %v %v", sids, err)
	}
	// A role change: claims_changed, no session ends.
	if _, err := svc.Revoke(e.ctx, auth.Revocation{UserID: u, Reason: auth.RevokeClaimsChanged, BumpVersion: true}); err != nil {
		t.Fatal(err)
	}
	if n := scan[int64](e, `SELECT count(*) FROM outbox_events WHERE routing_key = 'user.sessions_revoked'`); n != 2 {
		t.Fatalf("%d user.sessions_revoked events, want 2", n)
	}

	// Relay -> RabbitMQ -> notify.fcm.
	url, _ := asynctest.SharedRabbit(t).VHost(t)
	conn, err := mq.Dial(url, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := mq.DeclareTopology(conn, mq.Default); err != nil {
		t.Fatal(err)
	}
	rt := realtime.NewWriter(e.rdb, e.ks, 100, time.Hour)
	relay := outbox.NewRelay(e.app, func() (outbox.Publisher, error) { return mq.NewPublisher(conn) }, rt, outbox.RelayOptions{Log: zerolog.Nop()})
	if _, err := relay.Drain(e.ctx); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(e.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = mq.Consume(cctx, conn, e.consumer.Registration(), mq.ConsumerOptions{Topology: mq.Default, Log: zerolog.New(zerolog.NewTestWriter(t))})
	}()
	defer func() { cancel(); <-done }()
	queued := func() int {
		ch, err := conn.Channel()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ch.Close() }()
		q, err := ch.QueueDeclarePassive(notify.QueueFCM, true, false, false, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		return q.Messages
	}
	asynctest.Eventually(t, 15*time.Second, "one session_revoked delivery row", func() bool { return len(e.rows()) == 1 })
	asynctest.Eventually(t, 15*time.Second, "notify.fcm drained", func() bool { return queued() == 0 })
	time.Sleep(500 * time.Millisecond) // nothing else may arrive

	sent := e.fcm.Sent()
	if len(sent) != 1 || sent[0].Message.Token != "tok-phone" {
		t.Fatalf("pushes %+v, want exactly one to the revoked session's device", sent)
	}
	m := sent[0].Message
	if !maps.Equal(m.Data, map[string]string{"type": "session_revoked", "reason": "admin_revoke", "sessionId": phone.String()}) ||
		m.Notification != nil || m.Android.Priority != push.PriorityNormal {
		t.Fatalf("session_revoked push %s", sent[0].Raw)
	}
	rows := e.rows()
	if len(rows) != 1 || rows[0].Kind != notify.KindSessionRevoked || rows[0].Status != notify.StatusSent ||
		rows[0].User != u.String() || rows[0].Install != "inst-phone" || rows[0].EventID == nil {
		t.Fatalf("delivery rows %+v", rows)
	}
	// The claims_changed message was consumed and recorded nothing; the revoked message is in the inbox.
	if n := scan[int64](e, `SELECT count(*) FROM consumer_inbox WHERE consumer = 'notify.fcm'`); n != 1 {
		t.Fatalf("%d inbox rows, want 1", n)
	}
}

// AC: the same message id is never sent twice to a token, also when FCM's answer is lost. A send whose
// request reached FCM and then lost its answer (connection reset, timeout, cancellation) may already be
// on the phone: it is recorded failed "outcome unknown" and never sent again. A send that never left (a
// refused connection) is retried.
func TestFCMLostAnswerIsNeverResent(t *testing.T) {
	e := newFCMEnv(t)
	a, ua := e.driver("a@logitrack.test", "")
	e.device(ua, "inst-reset", "tok-reset", "android")
	e.device(ua, "inst-timeout", "tok-timeout", "android")
	e.device(ua, "inst-refused", "tok-refused", "android")
	e.fcm.Respond("tok-reset", pushtest.LostAnswer())
	e.fcm.Respond("tok-timeout", pushtest.Hang())
	e.fcm.Respond("tok-refused", pushtest.Refused())
	task := e.task(&a, nil, "")
	d := e.delivery(notify.RouteTaskAssigned, map[string]any{"id": task, "driverId": a})

	// The attempt's deadline ends the hung send, as push.Client's 10 s request timeout would.
	ctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
	err := e.consumer.Handle(ctx, d)
	cancel()
	if err == nil || mq.IsPermanent(err) {
		t.Fatalf("the refused send must retry the message: %v", err)
	}
	for tok, want := range map[string]int{"tok-reset": 1, "tok-timeout": 1, "tok-refused": 0} {
		if n := len(e.fcm.SentTo(tok)); n != want {
			t.Fatalf("first attempt: FCM took %d messages for %s, want %d", n, tok, want)
		}
	}
	e.handle(d) // the retry
	e.handle(d) // a redelivery
	for _, tok := range []string{"tok-reset", "tok-timeout", "tok-refused"} {
		if n := len(e.fcm.SentTo(tok)); n != 1 {
			t.Fatalf("FCM took %d messages for %s, want 1", n, tok)
		}
	}
	byInstall := map[string]deliveryRow{}
	for _, r := range e.rows() {
		byInstall[r.Install] = r
	}
	for install, msg := range map[string]string{
		"inst-reset":   "outcome unknown: fcm: request failed: network error",
		"inst-timeout": "outcome unknown: fcm: request failed: timeout",
	} {
		if r := byInstall[install]; r.Status != notify.StatusFailed || r.Error == nil || *r.Error != msg || r.Sent {
			t.Fatalf("%s: %+v, want failed %q", install, r, msg)
		}
	}
	if r := byInstall["inst-refused"]; len(byInstall) != 3 || r.Status != notify.StatusSent {
		t.Fatalf("rows %+v", byInstall)
	}

	// A send cancelled after FCM took it (the worker stopping) is not resent either.
	b, ub := e.driver("b@logitrack.test", "")
	e.device(ub, "inst-cancel", "tok-cancel", "android")
	e.fcm.Respond("tok-cancel", pushtest.Hang())
	task2 := e.task(&b, nil, "")
	d2 := e.delivery(notify.RouteTaskAssigned, map[string]any{"id": task2, "driverId": b})
	cctx, stop := context.WithCancel(e.ctx)
	done := make(chan error, 1)
	go func() { done <- e.consumer.Handle(cctx, d2) }()
	asynctest.Eventually(t, 10*time.Second, "FCM took the message", func() bool { return len(e.fcm.SentTo("tok-cancel")) == 1 })
	stop()
	if err := <-done; err == nil || mq.IsPermanent(err) {
		t.Fatalf("a cancelled attempt must retry: %v", err)
	}
	e.handle(d2)
	var cancelled []deliveryRow
	for _, r := range e.rows() {
		if r.Install == "inst-cancel" {
			cancelled = append(cancelled, r)
		}
	}
	if len(e.fcm.SentTo("tok-cancel")) != 1 || len(cancelled) != 1 || cancelled[0].Status != notify.StatusFailed ||
		*cancelled[0].Error != "outcome unknown: fcm: request failed: canceled" {
		t.Fatalf("cancelled send: FCM took %d, rows %+v", len(e.fcm.SentTo("tok-cancel")), cancelled)
	}
}

// task.assigned and task.reassigned name the driver they assigned (driverId; null on task.reassigned
// = left without a driver). Read late, after a later change, or re-planned by a retry after one, they
// never tell the task's new driver twice that it was assigned, nor tell a driver it lost a task it
// holds again. An assignment event without driverId is refused (permanent) and sends nothing.
func TestFCMAssignmentEventsReadLate(t *testing.T) {
	e := newFCMEnv(t)
	a, ua := e.driver("a@logitrack.test", "")
	b, ub := e.driver("b@logitrack.test", "")
	c, uc := e.driver("c@logitrack.test", "")
	e.device(ua, "inst-a", "tok-a", "android")
	e.device(ub, "inst-b", "tok-b", "android")
	e.device(uc, "inst-c", "tok-c", "android")
	types := func(tok string) []string {
		var out []string
		for _, s := range e.fcm.SentTo(tok) {
			out = append(out, s.Message.Data["type"])
		}
		return out
	}
	const assigned, unassigned = "first_mile_task_assigned", "first_mile_task_unassigned"

	// A unassigned, then C assigned; both committed before notify.fcm reads the unassignment.
	task := e.task(&a, nil, "")
	unassign := e.delivery(notify.RouteTaskReassigned, map[string]any{"id": task, "driverId": nil, "previousDriverId": a})
	toC := e.delivery(notify.RouteTaskReassigned, map[string]any{"id": task, "driverId": c, "previousDriverId": nil})
	e.exec(`UPDATE tasks SET driver_id = $2 WHERE id = $1`, task, c)
	e.handle(unassign)
	e.handle(toC)
	if !slices.Equal(types("tok-a"), []string{unassigned}) || !slices.Equal(types("tok-c"), []string{assigned}) {
		t.Fatalf("stale unassignment: a %v c %v", types("tok-a"), types("tok-c"))
	}

	// The retry of an unassignment re-planned after a new assignment: A's push answered 503 while the
	// task had no driver; C was assigned before the retry.
	task2 := e.task(nil, nil, "")
	e.fcm.Respond("tok-a", pushtest.Unavailable())
	unassign2 := e.delivery(notify.RouteTaskReassigned, map[string]any{"id": task2, "driverId": nil, "previousDriverId": a})
	if err := e.consumer.Handle(e.ctx, unassign2); err == nil || mq.IsPermanent(err) {
		t.Fatalf("a 503 must retry: %v", err)
	}
	e.exec(`UPDATE tasks SET driver_id = $2 WHERE id = $1`, task2, c)
	e.handle(unassign2)
	e.handle(e.delivery(notify.RouteTaskAssigned, map[string]any{"id": task2, "driverId": c}))
	if !slices.Equal(types("tok-a"), []string{unassigned, unassigned}) || !slices.Equal(types("tok-c"), []string{assigned, assigned}) {
		t.Fatalf("retried unassignment: a %v c %v", types("tok-a"), types("tok-c"))
	}

	// A -> B and back to A, the A -> B event read after both: A holds the task and is not told it lost
	// it; the B -> A event tells A it is assigned and B that it is not.
	task3 := e.task(&a, nil, "")
	e.handle(e.delivery(notify.RouteTaskReassigned, map[string]any{"id": task3, "driverId": b, "previousDriverId": a}))
	e.handle(e.delivery(notify.RouteTaskReassigned, map[string]any{"id": task3, "driverId": a, "previousDriverId": b}))
	if !slices.Equal(types("tok-a"), []string{unassigned, unassigned, assigned}) || !slices.Equal(types("tok-b"), []string{unassigned}) {
		t.Fatalf("back to A: a %v b %v", types("tok-a"), types("tok-b"))
	}

	calls := e.fcm.Calls()
	for _, body := range []struct {
		key     string
		payload map[string]any
	}{
		{notify.RouteTaskAssigned, map[string]any{"id": task3}},
		{notify.RouteTaskAssigned, map[string]any{"id": task3, "driverId": nil}},
		{notify.RouteTaskReassigned, map[string]any{"id": task3, "previousDriverId": b}},
	} {
		if err := e.consumer.Handle(e.ctx, e.delivery(body.key, body.payload)); !mq.IsPermanent(err) {
			t.Fatalf("%s %v: %v, want a permanent error", body.key, body.payload, err)
		}
	}
	if e.fcm.Calls() != calls {
		t.Fatal("an assignment event without driverId was sent")
	}
}

// A retry re-plans from the current rows; the pushes an earlier attempt sent to devices that have left
// the plan since are still logged: one notification_deliveries row per push sent.
func TestFCMRetryLogsPushesOfEarlierAttempts(t *testing.T) {
	e := newFCMEnv(t)
	a, ua := e.driver("a@logitrack.test", "")
	b, ub := e.driver("b@logitrack.test", "")
	e.device(ua, "inst-1", "tok-1", "android")
	e.device(ua, "inst-2", "tok-2", "android")
	e.device(ub, "inst-b", "tok-b", "android")
	e.fcm.Respond("tok-2", pushtest.Unavailable())
	task := e.task(&a, nil, "")
	d := e.delivery(notify.RouteTaskAssigned, map[string]any{"id": task, "driverId": a})
	if err := e.consumer.Handle(e.ctx, d); err == nil || mq.IsPermanent(err) {
		t.Fatalf("a 503 must retry: %v", err)
	}
	e.exec(`UPDATE tasks SET driver_id = $2 WHERE id = $1`, task, b) // reassigned before the retry
	e.handle(d)                                                      // the retry plans nothing: A no longer holds the task
	if len(e.fcm.SentTo("tok-1")) != 1 || len(e.fcm.SentTo("tok-2")) != 0 || len(e.fcm.SentTo("tok-b")) != 0 {
		t.Fatalf("sent %+v", e.fcm.Sent())
	}
	rows := e.rows()
	if len(rows) != 1 || rows[0].Install != "inst-1" || rows[0].User != ua.String() || rows[0].Kind != notify.KindTaskAssigned ||
		rows[0].Status != notify.StatusSent || !rows[0].Sent || rows[0].Data["type"] != "first_mile_task_assigned" {
		t.Fatalf("rows %+v, want the push sent to tok-1", rows)
	}
	if n := scan[int64](e, `SELECT count(*) FROM consumer_inbox WHERE consumer = 'notify.fcm'`); n != 1 {
		t.Fatalf("%d inbox rows", n)
	}

	// session_revoked to a phone and a tablet: the tablet answers 503 once and the phone signs in again
	// before the retry, which then plans the tablet only.
	u := e.user("u@logitrack.test")
	session := func(install string, revoked bool) uuid.UUID {
		reason, at := any(nil), any(nil)
		if revoked {
			reason, at = "password_reset", time.Now()
		}
		return scan[uuid.UUID](e, `INSERT INTO sessions (user_id, platform, amr, install_id, absolute_expires_at, revoked_at, revoked_reason)
			VALUES ($1, 'android', 'pwd', $2, now() + interval '90 days', $3, $4) RETURNING id`, u, install, at, reason)
	}
	phone, tablet := session("inst-phone", true), session("inst-tablet", true)
	e.device(u, "inst-phone", "tok-phone", "android")
	e.device(u, "inst-tablet", "tok-tablet", "android")
	e.fcm.Respond("tok-tablet", pushtest.Unavailable())
	r := e.delivery(notify.RouteSessionsRevoked, map[string]any{"userId": u, "sessionIds": []uuid.UUID{phone, tablet}, "reason": "password_reset"})
	if err := e.consumer.Handle(e.ctx, r); err == nil || mq.IsPermanent(err) {
		t.Fatalf("a 503 must retry: %v", err)
	}
	session("inst-phone", false)
	e.handle(r)
	if len(e.fcm.SentTo("tok-phone")) != 1 || len(e.fcm.SentTo("tok-tablet")) != 1 {
		t.Fatalf("phone %d tablet %d", len(e.fcm.SentTo("tok-phone")), len(e.fcm.SentTo("tok-tablet")))
	}
	var revoked []string
	for _, row := range e.rows() {
		if row.Kind == notify.KindSessionRevoked && row.Status == notify.StatusSent && row.User == u.String() {
			revoked = append(revoked, row.Install)
		}
	}
	if !slices.Equal(revoked, []string{"inst-phone", "inst-tablet"}) {
		t.Fatalf("session_revoked rows %v, want one per push sent", revoked)
	}
}

// A driver push reaches at most notify.MaxDevicesPerDriver devices, the most recently seen ones.
func TestFCMDriverFanOutIsCapped(t *testing.T) {
	e := newFCMEnv(t)
	a, ua := e.driver("a@logitrack.test", "")
	for i := range notify.MaxDevicesPerDriver + 2 {
		e.exec(`INSERT INTO device_tokens (user_id, install_id, token, platform, last_seen_at)
			VALUES ($1, $2, $3, 'android', now() - make_interval(mins => $4::int))`, ua, "inst-"+strconv.Itoa(i), "tok-"+strconv.Itoa(i), i)
	}
	task := e.task(&a, nil, "")
	e.handle(e.delivery(notify.RouteTaskAssigned, map[string]any{"id": task, "driverId": a}))
	sent := e.fcm.Sent()
	if len(sent) != notify.MaxDevicesPerDriver || len(e.fcm.SentTo("tok-10")) != 0 || len(e.fcm.SentTo("tok-11")) != 0 {
		t.Fatalf("%d pushes, want the %d most recently seen devices", len(sent), notify.MaxDevicesPerDriver)
	}
}
