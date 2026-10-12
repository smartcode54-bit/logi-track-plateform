//go:build integration

package iam_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase/firebasetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/linenotify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/line/linetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// lineWorld is the world of the LINE request routes of T49 (Appendix B §B.2.11-§B.2.13, §B.2.21-§B.2.22) as cmd/api
// mounts them: the staff force and revoke routes below /v1/tasks, /v1/trips and /v1/standby (operations.Mounts), the
// three sendCustomerLineNotification shim targets in app.MobileGroups behind the real auth.RequireShim (a cf_shim key
// with a Firebase ID token of the test project), and the evidence gallery on the public listener. The jobs the routes
// submit are run by the worker's notify.line consumer against the LINE stand-in (linetest: no request leaves the
// process).
type lineWorld struct {
	*world
	fb       *firebasetest.Backend
	reg      *prometheus.Registry
	line     *linetest.Server
	consumer *notify.LINE

	party                      string // SPX's billing party (LINE group Cline0001)
	driver, driver2            string
	task, trip, standby        string
	driverTok, driver2Tok, key string
}

func newLineWorld(t *testing.T) *lineWorld {
	t.Helper()
	lw := &lineWorld{fb: firebasetest.New(t, "logitrack-line-test"), reg: prometheus.NewRegistry(), line: linetest.New(t)}
	lw.world = newWorldOpts(t, worldOpts{firebase: auth.Firebase{Verifier: lw.fb.Verifier(nil)},
		groups: func(w *world, svc *auth.Service) []ingress.Group {
			if err := svc.Register(lw.reg); err != nil {
				t.Fatal(err)
			}
			ln := linenotify.New(w.pool, jobs.NewService(w.pool, jobs.NewRedisLocker(w.rdb, w.ks)))
			local, err := storage.NewLocal(storage.LocalConfig{Dir: t.TempDir(), PublicBaseURL: "http://media.test/media",
				SigningKey: bytes.Repeat([]byte("k"), storage.MinSigningKeyBytes)})
			if err != nil {
				t.Fatal(err)
			}
			st, err := storage.New(w.pool, storage.Config{Active: storage.BackendLocal, Bucket: "logitrack",
				PublicBucket: "logitrack-public", PresignGetTTL: time.Hour, EvidencePresignTTL: 15 * time.Minute}, zerolog.Nop(), local)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(st.Close)
			groups := app.MobileGroups(app.APIDeps{Auth: svc, LineNotify: ln})
			groups = append(groups,
				ingress.Group{Prefix: "/v1/tasks", Mount: linenotify.TaskRoutes(ln, svc.RequireAuth())},
				ingress.Group{Prefix: "/v1/trips", Mount: linenotify.TripRoutes(ln, svc.RequireAuth())},
				ingress.Group{Prefix: "/v1/standby", Mount: linenotify.StandbyRoutes(ln, svc.RequireAuth())})
			return append(groups, st.Groups(storage.HTTPOptions{Auth: svc.RequireAuth(), Caller: app.StorageCaller})...)
		}})
	w := lw.world
	lw.consumer = &notify.LINE{Pool: w.pool, Client: lw.line.Client(), Redis: w.rdb, Keys: w.ks,
		Locks: jobs.NewRedisLocker(w.rdb, w.ks), Enabled: true, EvidenceBaseURL: w.public + "/evidence", Log: zerolog.Nop()}

	// SPX keeps a LINE group; two drivers of the own fleet O signed in to Firebase; a checked-in task, its delivered
	// trip with a photo, and a standby record with a photo, each with its Firestore document id.
	spx := w.id(`INSERT INTO customers (code, name, line_group_id) VALUES ('SPX', 'Shopee', 'Cline0001') RETURNING id::text`)
	lw.party = w.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, spx)
	driverOf := func(email, uid, mobile string) string {
		u := w.user(email)
		w.member(u, w.O, "driver")
		w.exec(`UPDATE users SET legacy_auth_uid = $2 WHERE id = $1`, u, uid)
		return w.id(`INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, full_name_th, mobile)
			VALUES ($1, 'self', $2, 'Somchai', 'Jaidee', 'สมชาย ใจดี', $3) RETURNING id::text`, w.O, u, mobile)
	}
	lw.driver = driverOf("line.driver1@example.test", "fbLineDriver000000000000001", "0811111111")
	lw.driver2 = driverOf("line.driver2@example.test", "fbLineDriver000000000000002", "0822222222")
	now := time.Now()
	token := func(uid string) string {
		return lw.fb.SignIDToken(lw.fb.IDTokenClaims(uid, now.Add(-5*time.Minute), now))
	}
	lw.driverTok, lw.driver2Tok = token("fbLineDriver000000000000001"), token("fbLineDriver000000000000002")
	lw.task = w.id(`INSERT INTO tasks (tenant_id, tenant_source, task_no, task_type, status, plan_at, source_hub_raw,
		destination_raw, source_linked_party_id, billing_party_id, driver_id, check_in_at, legacy_doc_id)
		VALUES ($1, 'driver', 'FM-LINE-1', 'first_mile', 'checked_in', now(), 'SRC', 'DST', $2, $2, $3, now(), 'fsLineTask1')
		RETURNING id::text`, w.O, lw.party, lw.driver)
	lw.trip = w.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, task_id, driver_id,
		origin_raw, destination_raw, delivered_at, billing_party_id, legacy_doc_id) VALUES ($1, 'task', 'SPXTH-LINE-1',
		'delivered', 'first_mile', $2, $3, 'SRC', 'DST', now(), $4, 'fsLineTrip1') RETURNING id::text`, w.O, lw.task, lw.driver, lw.party)
	lw.standby = w.id(`INSERT INTO standby_records (tenant_id, tenant_source, driver_id, customer_party_id, start_location_raw,
		started_at, ended_at, duration_minutes, legacy_doc_id) VALUES ($1, 'driver', $2, $3, 'SRC', now() - interval '1 hour',
		now(), 60, 'fsLineStandby1') RETURNING id::text`, w.O, lw.driver, lw.party)
	file := func(key string) string {
		return w.id(`INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, status, committed_at, storage_backend,
			content_type) VALUES ('logitrack', $1, $2, 'trip_photo', 'committed', now(), 'local', 'image/jpeg') RETURNING id::text`,
			key, w.O)
	}
	w.exec(`INSERT INTO trip_photos (trip_id, photo_type, file_id) VALUES ($1, 'seal', $2)`, lw.trip, file("trips/line/seal.jpg"))
	w.exec(`INSERT INTO standby_photos (standby_id, photo_type, file_id) VALUES ($1, 'site_photo', $2)`, lw.standby,
		file("standby/line/site.jpg"))

	_, lw.key = w.mustKey(w.login("pa@example.test"), map[string]any{"name": "callable shims", "scope": "cf_shim",
		"capabilities": []string{string(authz.MobileCheckin), string(authz.MobileSubmitTrip), string(authz.MobileSubmitStandby)}})
	return lw
}

// runJob hands the notify.line.force command of job id (its outbox row, as the relay publishes it) to the consumer.
func (lw *lineWorld) runJob(id string) (status string) {
	lw.t.Helper()
	ctx := context.Background()
	var (
		seq     int64
		key     string
		payload []byte
		tenant  *uuid.UUID
	)
	if err := lw.etl.QueryRow(ctx, `SELECT id, routing_key, payload, tenant_id FROM outbox_events WHERE aggregate_id = $1
		AND routing_key = $2`, id, notify.RouteLineForce).Scan(&seq, &key, &payload, &tenant); err != nil {
		lw.t.Fatalf("job %s: %v", id, err)
	}
	if err := lw.consumer.Handle(ctx, &mq.Delivery{Queue: notify.QueueLine, MessageID: strconv.FormatInt(seq, 10),
		Exchange: mq.ExchangeJobs, RoutingKey: key, EventType: key, TenantID: tenant, Body: payload}); err != nil {
		lw.t.Fatalf("job %s: %v", id, err)
	}
	if err := lw.etl.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1`, id).Scan(&status); err != nil {
		lw.t.Fatal(err)
	}
	return status
}

// gallery is GET {public}/evidence/{token}: its status.
func (lw *lineWorld) gallery(token string) int {
	lw.t.Helper()
	res, err := http.Get(lw.public + "/evidence/" + token)
	if err != nil {
		lw.t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res.StatusCode
}

func (lw *lineWorld) token(table, id string) string {
	lw.t.Helper()
	var tok *string
	if err := lw.etl.QueryRow(context.Background(), `SELECT evidence_token FROM `+table+` WHERE id = $1`, id).Scan(&tok); err != nil {
		lw.t.Fatal(err)
	}
	if tok == nil {
		return ""
	}
	return *tok
}

// Staff force routes (operations:edit_trip_details, the record in the caller's write reach) and the revocations (R47):
// 202 with the notify.line.force job, 409 while one waits, 404 outside the reach; a revoked gallery link is 404 at
// once, and the next forced send carries a new token while the old link stays 404 (T49 AC 3 and 4).
func TestLineForceAndRevokeRoutes(t *testing.T) {
	lw := newLineWorld(t)
	op := lw.login("op.o@example.test")
	plain := lw.user("user.o@example.test")
	lw.member(plain, lw.O, "user")
	user := lw.login("user.o@example.test")

	// The delivered card: 202 with the job; a second request while it waits is 409 with that job.
	r := lw.send(http.MethodPost, "/v1/trips/"+lw.trip+"/notify-line", op, map[string]any{})
	expect(t, r, http.StatusAccepted, "")
	job := r.data().(map[string]any)["jobId"].(string)
	if r.data().(map[string]any)["status"] != jobs.StatusQueued {
		t.Fatalf("force: %s", r.raw)
	}
	r = lw.send(http.MethodPost, "/v1/trips/"+lw.trip+"/notify-line", op, map[string]any{"force": true})
	expect(t, r, http.StatusConflict, jobs.CodeAlreadyExists)
	if !strings.Contains(string(r.raw), job) {
		t.Fatalf("409 names another job: %s", r.raw)
	}
	if st := lw.runJob(job); st != jobs.StatusSucceeded || len(lw.line.Sent()) != 1 {
		t.Fatalf("the job: %s, %d cards", st, len(lw.line.Sent()))
	}
	first := lw.token("trip_records", lw.trip)
	if first == "" || !strings.Contains(string(lw.line.Sent()[0].Body), lw.public+"/evidence/"+first) || lw.gallery(first) != http.StatusOK {
		t.Fatalf("the card links the gallery: token %q, %s", first, lw.line.Sent()[0].Body)
	}
	// The job ended and released its lock: a request without force is accepted and sends nothing (already notified).
	r = lw.send(http.MethodPost, "/v1/trips/"+lw.trip+"/notify-line", op, nil)
	expect(t, r, http.StatusAccepted, "")
	if st := lw.runJob(r.data().(map[string]any)["jobId"].(string)); st != jobs.StatusSucceeded || len(lw.line.Sent()) != 1 {
		t.Fatalf("an unforced resend: %s, %d cards", st, len(lw.line.Sent()))
	}

	// Who may ask: the capability, the record in the caller's write reach (a carrier of O, an unrelated tenant: 404).
	expect(t, lw.send(http.MethodPost, "/v1/trips/"+lw.trip+"/notify-line", user, nil), http.StatusForbidden, authz.CodePermissionDenied)
	for _, email := range []string{"op.a@example.test", "ta.c@example.test"} {
		tok := lw.login(email)
		expect(t, lw.send(http.MethodPost, "/v1/trips/"+lw.trip+"/notify-line", tok, nil), http.StatusNotFound, "not_found")
		expect(t, lw.send(http.MethodPost, "/v1/trips/"+lw.trip+"/evidence/revoke", tok, nil), http.StatusNotFound, "not_found")
	}
	expect(t, lw.send(http.MethodPost, "/v1/trips/"+lw.trip+"/notify-line", "", nil), http.StatusUnauthorized, "unauthenticated")
	expect(t, lw.send(http.MethodPost, "/v1/trips/not-a-uuid/notify-line", op, nil), http.StatusNotFound, "not_found")
	expect(t, lw.send(http.MethodPost, "/v1/trips/"+lw.trip+"/notify-line?force=true", op, nil), http.StatusBadRequest, "")

	// Revoked: 204 (twice), and the link answers 404 from the next request on.
	expect(t, lw.send(http.MethodPost, "/v1/trips/"+lw.trip+"/evidence/revoke", user, nil), http.StatusForbidden, authz.CodePermissionDenied)
	expect(t, lw.send(http.MethodPost, "/v1/trips/"+lw.trip+"/evidence/revoke", op, nil), http.StatusNoContent, "")
	expect(t, lw.send(http.MethodPost, "/v1/trips/"+lw.trip+"/evidence/revoke", op, nil), http.StatusNoContent, "")
	if s := lw.gallery(first); s != http.StatusNotFound {
		t.Fatalf("revoked link: %d", s)
	}
	// The next forced send mints a new token; the old link stays 404.
	r = lw.send(http.MethodPost, "/v1/trips/"+lw.trip+"/notify-line", op, map[string]any{"force": true})
	expect(t, r, http.StatusAccepted, "")
	if st := lw.runJob(r.data().(map[string]any)["jobId"].(string)); st != jobs.StatusSucceeded || len(lw.line.Sent()) != 2 {
		t.Fatalf("forced resend: %s, %d cards", st, len(lw.line.Sent()))
	}
	fresh := lw.token("trip_records", lw.trip)
	if fresh == first || !strings.Contains(string(lw.line.Sent()[1].Body), lw.public+"/evidence/"+fresh) ||
		strings.Contains(string(lw.line.Sent()[1].Body), first) {
		t.Fatalf("forced resend after a revocation: %q -> %q, %s", first, fresh, lw.line.Sent()[1].Body)
	}
	if lw.gallery(fresh) != http.StatusOK || lw.gallery(first) != http.StatusNotFound {
		t.Fatal("after the forced resend the new link opens and the old one stays closed")
	}

	// The standby card and its revocation; the check-in card.
	r = lw.send(http.MethodPost, "/v1/standby/"+lw.standby+"/notify-line", op, nil)
	expect(t, r, http.StatusAccepted, "")
	if st := lw.runJob(r.data().(map[string]any)["jobId"].(string)); st != jobs.StatusSucceeded {
		t.Fatalf("standby job: %s", st)
	}
	stok := lw.token("standby_records", lw.standby)
	if stok == "" || lw.gallery(stok) != http.StatusOK {
		t.Fatalf("standby token %q", stok)
	}
	expect(t, lw.send(http.MethodPost, "/v1/standby/"+lw.standby+"/evidence/revoke", op, nil), http.StatusNoContent, "")
	if lw.gallery(stok) != http.StatusNotFound {
		t.Fatal("revoked standby link")
	}
	r = lw.send(http.MethodPost, "/v1/tasks/"+lw.task+"/notify-line", op, nil)
	expect(t, r, http.StatusAccepted, "")
	if st := lw.runJob(r.data().(map[string]any)["jobId"].(string)); st != jobs.StatusSucceeded {
		t.Fatalf("check-in job: %s", st)
	}
	if n := len(lw.line.Sent()); n != 4 || !strings.Contains(string(lw.line.Sent()[3].Body), "เช็คอินต้นทางแล้ว") {
		t.Fatalf("%d cards, the last %s", n, lw.line.Sent()[n-1].Body)
	}
}

// The sendCustomerLineNotification shim targets (POST /v1/mobile/{tasks|trips|standby}/{id}/line-notify): a driver acting
// as itself through the cf_shim key and its Firebase ID token, by the Firestore document id; {ok: true} queues the card
// once, {ok, skipped, reason} answers a card already queued or sent; force is refused; another driver's record is never
// sent; staff and a key alone cannot call them.
func TestLineShimTargets(t *testing.T) {
	lw := newLineWorld(t)
	shim := func(path, bearer string, body any) resp {
		t.Helper()
		return shimRequest(t, lw.public+path, http.MethodPost, bearer, body, auth.HeaderAPIKey, lw.key)
	}
	data := func(r resp) map[string]any {
		t.Helper()
		d, _ := r.data().(map[string]any)
		return d
	}
	jobOf := func(scope string) string {
		t.Helper()
		return lw.id(`SELECT id::text FROM jobs WHERE type = $1 AND params ->> 'card' = $2 ORDER BY created_at DESC, id DESC LIMIT 1`,
			jobs.TypeNotifyLineForce, scope)
	}

	// The check-in card by the task's Firestore id: queued, then "already queued" while it waits, then sent and
	// "already notified".
	r := shim("/v1/mobile/tasks/fsLineTask1/line-notify", lw.driverTok, map[string]any{})
	expect(t, r, http.StatusAccepted, "")
	if d := data(r); d["ok"] != true || d["skipped"] != nil {
		t.Fatalf("queued: %s", r.raw)
	}
	r = shim("/v1/mobile/tasks/fsLineTask1/line-notify", lw.driverTok, nil)
	if d := data(r); r.status != http.StatusOK || d["ok"] != true || d["skipped"] != true || d["reason"] != linenotify.ReasonAlreadyQueued {
		t.Fatalf("while queued: %d %s", r.status, r.raw)
	}
	if st := lw.runJob(jobOf(notify.LineCheckin)); st != jobs.StatusSucceeded || len(lw.line.Sent()) != 1 {
		t.Fatalf("the shim's job: %s, %d cards", st, len(lw.line.Sent()))
	}
	r = shim("/v1/mobile/tasks/"+lw.task+"/line-notify", lw.driverTok, nil)
	if d := data(r); r.status != http.StatusOK || d["skipped"] != true || d["reason"] != notify.ReasonAlreadyNotified {
		t.Fatalf("after the send: %d %s", r.status, r.raw)
	}

	// Drivers never force (staff use POST /v1/tasks/{id}/notify-line).
	r = shim("/v1/mobile/trips/fsLineTrip1/line-notify", lw.driverTok, map[string]any{"force": true})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")

	// Another driver's trip: the second driver cannot read it, so the request is queued by its id and the consumer
	// refuses it (not_task_owner): no card.
	r = shim("/v1/mobile/trips/fsLineTrip1/line-notify", lw.driver2Tok, nil)
	expect(t, r, http.StatusAccepted, "")
	if st := lw.runJob(jobOf(notify.LineDelivered)); st != jobs.StatusFailed || len(lw.line.Sent()) != 1 {
		t.Fatalf("another driver's trip: %s, %d cards", st, len(lw.line.Sent()))
	}
	// The trip's own driver, by trip number: the delivered card with its gallery link.
	r = shim("/v1/mobile/trips/SPXTH-LINE-1/line-notify", lw.driverTok, nil)
	expect(t, r, http.StatusAccepted, "")
	if st := lw.runJob(jobOf(notify.LineDelivered)); st != jobs.StatusSucceeded || len(lw.line.Sent()) != 2 {
		t.Fatalf("the driver's trip: %s, %d cards", st, len(lw.line.Sent()))
	}
	if tok := lw.token("trip_records", lw.trip); tok == "" || lw.gallery(tok) != http.StatusOK {
		t.Fatalf("the delivered card's link: %q", tok)
	}

	// The standby card; a record the mirror has not loaded yet is queued by its id (the consumer waits for it).
	expect(t, shim("/v1/mobile/standby/fsLineStandby1/line-notify", lw.driverTok, nil), http.StatusAccepted, "")
	r = shim("/v1/mobile/standby/fsNotMirroredYet/line-notify", lw.driverTok, nil)
	expect(t, r, http.StatusAccepted, "")
	if n := lw.count(`SELECT count(*)::int FROM jobs WHERE type = $1 AND params ->> 'ref' = 'fsNotMirroredYet'
		AND params ->> 'driverId' = $2`, jobs.TypeNotifyLineForce, lw.driver); n != 1 {
		t.Fatalf("%d jobs for the record not mirrored yet", n)
	}

	// Staff (no mobile:* capability), a key without the ID token, the ID token without the key, the internal listener.
	staff := lw.user("line.staff@example.test")
	lw.member(staff, lw.O, "operator")
	lw.exec(`UPDATE users SET legacy_auth_uid = 'fbLineStaff0000000000000001' WHERE id = $1`, staff)
	now := time.Now()
	staffTok := lw.fb.SignIDToken(lw.fb.IDTokenClaims("fbLineStaff0000000000000001", now.Add(-5*time.Minute), now))
	expect(t, shim("/v1/mobile/trips/fsLineTrip1/line-notify", staffTok, nil), http.StatusForbidden, authz.CodePermissionDenied)
	expect(t, shim("/v1/mobile/trips/fsLineTrip1/line-notify", "", nil), http.StatusUnauthorized, "unauthenticated")
	expect(t, shimRequest(t, lw.public+"/v1/mobile/trips/fsLineTrip1/line-notify", http.MethodPost, lw.driverTok, nil),
		http.StatusUnauthorized, "unauthenticated")
	r = shimRequest(t, lw.internal+"/v1/mobile/trips/fsLineTrip1/line-notify", http.MethodPost, lw.driverTok, nil,
		auth.HeaderAPIKey, lw.key)
	expect(t, r, http.StatusForbidden, authz.CodePermissionDenied)

	// shim_requests_total counts the authenticated calls of the callable (a refused credential and the internal
	// listener are no shim call).
	if n, ok := shimCount(t, lw.reg, app.ShimSendLineNotification); !ok || n != 9 {
		t.Fatalf("shim_requests_total{callable=%q} = %v (present %v), want 9", app.ShimSendLineNotification, n, ok)
	}
}
