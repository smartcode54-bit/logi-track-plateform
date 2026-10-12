//go:build integration

// Acceptance tests of notify.line (issue T49) on PostgreSQL 18 (the goose chain), Redis 7 (compose image) and the LINE
// Messaging API stand-in (linetest: the real line.HTTP client, its bearer token and X-Line-Retry-Key, no LINE): the
// three cards byte-equal to the Flex builders over the values PostgreSQL holds, the driver code of the target customer
// and never the national ID, the evidence link minted before the push and replaced only by a forced send after a
// revocation, and 0 duplicate sends (a redelivery, a lost answer, two messages for one card at once, the flag).
package notify_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/line/linetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

const evidenceBase = "https://logi-api.example.test/evidence"

type lineEnv struct {
	t        *testing.T
	ctx      context.Context
	app, etl *pgxpool.Pool
	rdb      *redis.Client
	ks       cache.Keyspace
	line     *linetest.Server
	consumer *notify.LINE
	seq      atomic.Int64
	now      time.Time

	tenant, user              uuid.UUID
	spx, spxCustomer, noGroup uuid.UUID // billing parties: SPX (group Cspx), JNT (no group)
	driver, other             uuid.UUID
	task, trip, standby       uuid.UUID
	checkInAt, std, ata, done time.Time
}

func newLineEnv(t *testing.T) *lineEnv {
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
	e := &lineEnv{t: t, ctx: ctx, app: d.Pool(t, db.RoleApp), etl: d.Pool(t, db.RoleETL),
		rdb: asynctest.SharedRedis(t).Client(t), ks: ks, line: linetest.New(t), now: time.Now().UTC()}
	e.seq.Store(5000)
	e.consumer = &notify.LINE{Pool: e.app, Client: e.line.Client(), Redis: e.rdb, Keys: ks, Locks: jobs.NewRedisLocker(e.rdb, ks),
		Enabled: true, EvidenceBaseURL: evidenceBase, Log: zerolog.Nop(), Now: func() time.Time { return e.now }}
	e.fixture()
	return e
}

func (e *lineEnv) id(sql string, args ...any) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.etl.QueryRow(e.ctx, sql, args...).Scan(&id); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return id
}

func (e *lineEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.etl.Exec(e.ctx, sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *lineEnv) str(sql string, args ...any) string {
	e.t.Helper()
	var s *string
	if err := e.etl.QueryRow(e.ctx, sql, args...).Scan(&s); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	if s == nil {
		return ""
	}
	return *s
}

func (e *lineEnv) file(key string) uuid.UUID {
	return e.id(`INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, status, committed_at, storage_backend, content_type)
		VALUES ('logitrack', $1, $2, 'trip_photo', 'committed', now(), 'local', 'image/jpeg') RETURNING id`, key, e.tenant)
}

func (e *lineEnv) fixture() {
	e.tenant = e.id(`INSERT INTO tenants (kind, name_th, code) VALUES ('own_fleet', 'กองรถ', 'OWN') RETURNING id`)
	e.user = e.id(`INSERT INTO users (email, display_name) VALUES ('staff@example.test', 'Staff') RETURNING id`)
	e.spxCustomer = e.id(`INSERT INTO customers (code, name, line_group_id) VALUES ('SPX', 'Shopee', 'Cspx0001') RETURNING id`)
	e.spx = e.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id`, e.spxCustomer)
	jnt := e.id(`INSERT INTO customers (code, name) VALUES ('JNT', 'J&T') RETURNING id`)
	e.noGroup = e.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id`, jnt)
	e.exec(`INSERT INTO hubs (source_id, name_th, name_en, network) VALUES ('HUBA', 'ฮับเอ', 'Hub A', 'SPX')`)
	e.exec(`INSERT INTO hubs (source_id, name_th, network, station_type) VALUES ('SOCE', 'บัวโรย', 'SPX', 'SOC')`)
	e.driver = e.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, full_name_th, mobile, email, id_card,
		truck_license_id) VALUES ($1, 'self', 'Somchai', 'Jaidee', 'สมชาย ใจดี', '0812345678', 's@example.test', '1234567890123',
		'AB123456') RETURNING id`, e.tenant)
	e.other = e.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile) VALUES ($1, 'self', 'Other',
		'Driver', '0899999999') RETURNING id`, e.tenant)
	e.exec(`INSERT INTO driver_customer_codes (driver_id, customer_id, id_type_key, value) VALUES ($1, $2, 'appId', '889900'),
		($1, $2, 'zone', 'Z9')`, e.driver, e.spxCustomer)
	truck := e.id(`INSERT INTO trucks (tenant_id, tenant_source, license_plate, province, status, brand, model, year, color, type_raw,
		vehicle_class) VALUES ($1, 'self', '70-1234', 'กรุงเทพมหานคร', 'active', 'ISUZU', 'FRR', 2020, 'white', '6 Wheels', '6WH')
		RETURNING id`, e.tenant)
	e.checkInAt = time.Date(2026, 10, 12, 1, 30, 0, 0, time.UTC) // 08:30 Bangkok
	e.std = time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC)
	e.ata = time.Date(2026, 10, 12, 5, 15, 0, 0, time.UTC)
	e.done = time.Date(2026, 10, 12, 5, 40, 0, 0, time.UTC)
	e.task = e.id(`INSERT INTO tasks (tenant_id, tenant_source, task_no, task_type, status, plan_at, source_hub_raw, destination_raw,
		source_linked_party_id, billing_party_id, truck_id, truck_type, license_plate_snapshot, driver_id, check_in_at, legacy_doc_id)
		VALUES ($1, 'driver', 'FM-12102026-001', 'first_mile', 'checked_in', '2026-10-12 08:00:00+07', 'HUBA', 'SOCE', $2, $2, $3,
		'6WH', '70-1234', $4, $5, 'fsTask1') RETURNING id`, e.tenant, e.spx, truck, e.driver, e.checkInAt)
	e.trip = e.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, task_id, driver_id, origin_raw,
		destination_raw, truck_id, truck_license_plate_snapshot, parcel_count, std, ata, delivered_at, delivered_via, billing_party_id,
		legacy_doc_id) VALUES ($1, 'task', 'SPXTH-T49', 'delivered', 'first_mile', $2, $3, 'HUBA', 'SOCE', $4, '70-1234', 42, $5, $6, $7,
		'admin_web', $8, 'SPXTH-T49') RETURNING id`, e.tenant, e.task, e.driver, truck, e.std, e.ata, e.done, e.spx)
	for i, typ := range []string{"pre_close", "seal", "arrived"} {
		e.exec(`INSERT INTO trip_photos (trip_id, photo_type, file_id, position) VALUES ($1, $2, $3, $4)`, e.trip, typ,
			e.file(fmt.Sprintf("trips/t49/%s.jpg", typ)), i)
	}
	e.exec(`INSERT INTO incident_reports (tenant_id, tenant_source, driver_id, trip_id, delay_cause, map_photo_file_id)
		VALUES ($1, 'trip', $2, $3, 'incident_cause_traffic', $4)`, e.tenant, e.driver, e.trip, e.file("incidents/t49/map.jpg"))
	e.standby = e.id(`INSERT INTO standby_records (tenant_id, tenant_source, driver_id, customer_party_id, start_location_raw,
		end_location_raw, started_at, ended_at, duration_minutes, note, truck_license_plate_snapshot, legacy_doc_id)
		VALUES ($1, 'driver', $2, $3, 'HUBA', 'บัวโรย', '2026-10-12 10:00:00+07', '2026-10-12 12:30:00+07', 150, 'งานหมด', '70-1234',
		'fsStandby1') RETURNING id`, e.tenant, e.driver, e.spx)
	for _, typ := range []string{"customer_worksheet", "site_photo"} {
		e.exec(`INSERT INTO standby_photos (standby_id, photo_type, file_id) VALUES ($1, $2, $3)`, e.standby, typ,
			e.file("standby/t49/"+typ+".jpg"))
	}
}

// event is a notify.line delivery of a domain event with a fresh message id, as the relay publishes it.
func (e *lineEnv) event(key string, id uuid.UUID, headers amqp.Table) *mq.Delivery {
	b, _ := json.Marshal(map[string]any{"id": id})
	return &mq.Delivery{Queue: notify.QueueLine, MessageID: strconv.FormatInt(e.seq.Add(1), 10), Exchange: mq.ExchangeEvents,
		RoutingKey: key, EventType: key, TenantID: &e.tenant, Body: b, Headers: headers}
}

// job creates a notify.line.force job row and returns its command delivery (the relay's copy of the outbox row).
func (e *lineEnv) job(p notify.LineForceParams, scope string) *mq.Delivery {
	e.t.Helper()
	var cmd jobs.Command
	if err := db.WithSystem(e.ctx, e.app, &e.tenant, func(tx pgx.Tx) error {
		j, err := jobs.Insert(e.ctx, tx, jobs.NewInput{Type: jobs.TypeNotifyLineForce, OwnerUserID: &e.user, TenantID: &e.tenant,
			Params: p, Scope: scope})
		cmd = jobs.Command{JobID: j.ID, Type: j.Type, Params: j.Params, Scope: scope}
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	b, _ := json.Marshal(cmd)
	return &mq.Delivery{Queue: notify.QueueLine, MessageID: strconv.FormatInt(e.seq.Add(1), 10), Exchange: mq.ExchangeJobs,
		RoutingKey: notify.RouteLineForce, EventType: notify.RouteLineForce, TenantID: &e.tenant, Body: b}
}

func (e *lineEnv) handle(d *mq.Delivery) {
	e.t.Helper()
	if err := e.consumer.Handle(e.ctx, d); err != nil {
		e.t.Fatalf("%s %s: %v", d.RoutingKey, d.MessageID, err)
	}
}

type lineRow struct{ Kind, Status, Error string }

func (e *lineEnv) rows() []lineRow {
	e.t.Helper()
	rs, err := e.etl.Query(e.ctx, `SELECT kind, status, coalesce(error, '') FROM notification_deliveries
		WHERE kind LIKE 'line_%' ORDER BY outbox_event_id, created_at`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rs.Close()
	var out []lineRow
	for rs.Next() {
		var r lineRow
		if err := rs.Scan(&r.Kind, &r.Status, &r.Error); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func (e *lineEnv) jobState(d *mq.Delivery) (status, result string) {
	e.t.Helper()
	var c jobs.Command
	_ = json.Unmarshal(d.Body, &c)
	if err := e.etl.QueryRow(e.ctx, `SELECT status, coalesce(result::text, '') || coalesce(' ' || error, '') FROM jobs WHERE id = $1`,
		c.JobID).Scan(&status, &result); err != nil {
		e.t.Fatal(err)
	}
	return status, result
}

// The check-in card: the values PostgreSQL holds in the card the TypeScript builder makes of them, byte for byte, to
// the source party's LINE group, with the customer's own driver id and never the national ID card or the licence. The
// flag is set; a redelivery sends nothing, a second event is deduplicated (one card per check-in).
func TestLineCheckinCard(t *testing.T) {
	e := newLineEnv(t)
	d := e.event(notify.RouteTaskCheckedIn, e.task, nil)
	e.handle(d)
	sent := e.line.Sent()
	if len(sent) != 1 {
		t.Fatalf("%d pushes, want 1", len(sent))
	}
	at := e.checkInAt
	want := notify.LinePushBody("Cspx0001", notify.BuildCheckinMessage(notify.LineTripCard{
		DateLine: notify.FormatBuddhistShortDate(at), OriginLabel: "ฮับเอ", DestinationLabel: "บัวโรย", TripNo: "FM-12102026-001",
		DriverNameTh: "สมชาย ใจดี", DriverCode: "889900", Plate: "70-1234", Phone: "0812345678", Partner: "SPX",
		TruckType: "6 Wheels", CheckInHm: notify.FormatBangkokHm(&at),
	}))
	if string(sent[0].Body) != string(want) {
		t.Fatalf("push body\n got %s\nwant %s", sent[0].Body, want)
	}
	for _, pii := range []string{"1234567890123", "AB123456"} {
		if strings.Contains(string(sent[0].Body), pii) {
			t.Fatalf("the card carries the driver's %s", pii)
		}
	}
	if e.str(`SELECT line_checkin_notified_at::text FROM tasks WHERE id = $1`, e.task) == "" {
		t.Fatal("line_checkin_notified_at not set")
	}
	e.handle(d)                                               // a redelivery of the same message
	e.handle(e.event(notify.RouteTaskCheckedIn, e.task, nil)) // another message for the same check-in
	if n := len(e.line.Sent()); n != 1 {
		t.Fatalf("%d pushes after a redelivery and a second event, want 1", n)
	}
	rows := e.rows()
	if len(rows) != 2 || rows[0] != (lineRow{"line_checkin", "sent", ""}) ||
		rows[1] != (lineRow{"line_checkin", "deduplicated", notify.ReasonAlreadyNotified}) {
		t.Fatalf("delivery rows %+v", rows)
	}

	// The customer keeps no id for this driver: "-", still never the national ID (forced resend, a staff action).
	e.exec(`DELETE FROM driver_customer_codes WHERE driver_id = $1`, e.driver)
	e.handle(e.job(notify.LineForceParams{Card: notify.LineCheckin, ID: &e.task, Force: true}, "checkin:"+e.task.String()))
	sent = e.line.Sent()
	if len(sent) != 2 || !strings.Contains(string(sent[1].Body), `"text":"รหัส","color":"#8C8C8C","size":"sm","flex":4},{"type":"text","text":"-"`) ||
		strings.Contains(string(sent[1].Body), "1234567890123") {
		t.Fatalf("the forced resend without customer ids: %s", sent[1].Body)
	}
	if sent[1].RetryKey == sent[0].RetryKey {
		t.Fatal("a forced resend reused the automatic retry key")
	}
}

// The delivered card carries the gallery link: a token is minted and committed before the push (the link resolves),
// the photo count is what the gallery shows (trip photos and the incident's map), and the notes name the delay cause
// and the admin close. After a revocation an automatic send never re-opens the gallery; the next forced send mints a
// new token, and the old link stays revoked.
func TestLineDeliveredEvidenceAndRevocation(t *testing.T) {
	e := newLineEnv(t)
	e.handle(e.event(notify.RouteTripDelivered, e.trip, nil))
	sent := e.line.Sent()
	if len(sent) != 1 {
		t.Fatalf("%d pushes, want 1", len(sent))
	}
	token := e.str(`SELECT evidence_token FROM trip_records WHERE id = $1`, e.trip)
	if _, ok := storage.EvidenceIssuedAt(token); !ok {
		t.Fatalf("token %q is not a v1 token", token)
	}
	checkIn, std, ata, done := e.checkInAt, e.std, e.ata, e.done
	want := notify.LinePushBody("Cspx0001", notify.BuildDeliveredMessage(notify.LineTripCard{
		DateLine: notify.FormatBuddhistShortDate(std), OriginLabel: "ฮับเอ", DestinationLabel: "บัวโรย", TripNo: "SPXTH-T49",
		DriverNameTh: "สมชาย ใจดี", DriverCode: "889900", Plate: "70-1234", Phone: "0812345678", Partner: "SPX",
		TruckType: "6 Wheels", Parcels: "42", CheckInHm: notify.FormatBangkokHm(&checkIn), DepartHm: notify.FormatBangkokHm(&std),
		ArriveHm: notify.FormatBangkokHm(&ata), DoneHm: notify.FormatBangkokHm(&done),
		Notes:       notify.BuildDelayNote([]string{"incident_cause_traffic"}) + " · " + notify.AdminWebCloseNote,
		EvidenceURL: evidenceBase + "/" + token, PhotoCount: 4,
	}))
	if string(sent[0].Body) != string(want) {
		t.Fatalf("push body\n got %s\nwant %s", sent[0].Body, want)
	}

	// Revoked (POST /v1/trips/{id}/evidence/revoke); the flag and the 24 h idem:line claim are cleared, as if a day had
	// passed and a Firestore version without the flag had been mirrored before P5, so that an automatic request sends.
	if err := db.WithSystem(e.ctx, e.app, &e.tenant, func(tx pgx.Tx) error {
		_, err := storage.RevokeEvidence(e.ctx, tx, storage.EvidenceTrip, e.trip, e.now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.exec(`UPDATE trip_records SET line_delivered_notified_at = NULL WHERE id = $1`, e.trip)
	if err := e.rdb.Del(e.ctx, e.ks.IdemLine(e.trip.String(), notify.LineDelivered)).Err(); err != nil {
		t.Fatal(err)
	}
	e.line.ForgetRetryKeys()
	e.handle(e.job(notify.LineForceParams{Card: notify.LineDelivered, ID: &e.trip}, "a"))
	sent = e.line.Sent()
	// (The delay note says "ดูรูปหลักฐาน" too: the button is the footer.)
	if len(sent) != 2 || strings.Contains(string(sent[1].Body), `"footer"`) || strings.Contains(string(sent[1].Body), token) {
		t.Fatalf("an automatic send after a revocation re-opened the gallery: %d pushes, last %s", len(sent), sent[len(sent)-1].Body)
	}
	if e.str(`SELECT evidence_token FROM trip_records WHERE id = $1`, e.trip) != token {
		t.Fatal("an automatic send replaced the revoked token")
	}

	// The next forced send mints a new token; the old one stays revoked.
	e.handle(e.job(notify.LineForceParams{Card: notify.LineDelivered, ID: &e.trip, Force: true}, "b"))
	sent = e.line.Sent()
	fresh := e.str(`SELECT evidence_token FROM trip_records WHERE id = $1`, e.trip)
	if len(sent) != 3 || fresh == token || !strings.Contains(string(sent[2].Body), evidenceBase+"/"+fresh) ||
		strings.Contains(string(sent[2].Body), token) {
		t.Fatalf("forced send after the revocation: token %s -> %s, body %s", token, fresh, sent[len(sent)-1].Body)
	}
	if e.str(`SELECT evidence_token_revoked_at::text FROM trip_records WHERE id = $1`, e.trip) != "" {
		t.Fatal("the new token is revoked")
	}
}

// The standby card goes to the record's customer party with its photos' gallery link.
func TestLineStandbyCard(t *testing.T) {
	e := newLineEnv(t)
	e.handle(e.event(notify.RouteStandbyCompleted, e.standby, nil))
	sent := e.line.Sent()
	if len(sent) != 1 {
		t.Fatalf("%d pushes, want 1", len(sent))
	}
	token := e.str(`SELECT evidence_token FROM standby_records WHERE id = $1`, e.standby)
	started, ended := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC), time.Date(2026, 10, 12, 5, 30, 0, 0, time.UTC)
	want := notify.LinePushBody("Cspx0001", notify.BuildStandbyMessage(notify.LineStandbyCard{
		DateLine: notify.FormatBuddhistShortDate(ended), StartLabel: "ฮับเอ", EndLabel: "บัวโรย", DriverNameTh: "สมชาย ใจดี",
		DriverCode: "889900", Plate: "70-1234", Phone: "0812345678", Partner: "SPX", StartedHm: notify.FormatBangkokHm(&started),
		EndedHm: notify.FormatBangkokHm(&ended), DurationText: "150 นาที", Notes: "งานหมด",
		EvidenceURL: evidenceBase + "/" + token, PhotoCount: 2,
	}))
	if string(sent[0].Body) != string(want) {
		t.Fatalf("push body\n got %s\nwant %s", sent[0].Body, want)
	}
	if e.str(`SELECT line_notified_at::text FROM standby_records WHERE id = $1`, e.standby) == "" {
		t.Fatal("line_notified_at not set")
	}
}

// 0 duplicate sends: the answer of an accepted push is lost (the message retries and LINE answers the retry key with
// 409: recorded sent, one card); two messages for the same card at once send one card.
func TestLineSendsOnce(t *testing.T) {
	e := newLineEnv(t)
	e.line.LoseNextAnswer()
	d := e.event(notify.RouteTaskCheckedIn, e.task, nil)
	if err := e.consumer.Handle(e.ctx, d); err == nil || mq.IsPermanent(err) {
		t.Fatalf("lost answer: %v, want a transient error", err)
	}
	if e.str(`SELECT line_checkin_notified_at::text FROM tasks WHERE id = $1`, e.task) != "" {
		t.Fatal("the flag was set without an answer")
	}
	e.handle(d) // the retry
	if n := len(e.line.Sent()); n != 1 {
		t.Fatalf("%d cards after a lost answer and its retry, want 1", n)
	}
	if rows := e.rows(); len(rows) != 1 || rows[0].Status != notify.StatusSent {
		t.Fatalf("rows %+v", rows)
	}

	// The delivered card: an API event and a driver's shim request at the same time.
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		var d *mq.Delivery
		if i%2 == 0 {
			d = e.event(notify.RouteTripDelivered, e.trip, nil)
		} else {
			d = e.job(notify.LineForceParams{Card: notify.LineDelivered, ID: &e.trip, DriverID: &e.driver}, "s"+strconv.Itoa(i))
		}
		wg.Go(func() { errs[i] = e.consumer.Handle(e.ctx, d) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	delivered := 0
	for _, s := range e.line.Sent() {
		if strings.Contains(string(s.Body), "ส่งงานสำเร็จ") {
			delivered++
		}
	}
	if delivered != 1 {
		t.Fatalf("%d delivered cards from 6 concurrent messages, want 1", delivered)
	}
}

// The FS->PG mirror's events (origin=etl.sync) send nothing: the Firestore-side writer (the legacy callable or its shim)
// owns the card. LINE_NOTIFY_ENABLED false acks events and ends jobs skipped.
func TestLineMirrorEventsAndDisabled(t *testing.T) {
	e := newLineEnv(t)
	e.handle(e.event(notify.RouteTripDelivered, e.trip, amqp.Table{outbox.HeaderOrigin: outbox.OriginMirror}))
	if len(e.line.Sent()) != 0 || len(e.rows()) != 0 {
		t.Fatal("a mirror event sent a card")
	}
	off := *e.consumer
	off.Enabled, off.Client = false, nil
	d := e.job(notify.LineForceParams{Card: notify.LineDelivered, ID: &e.trip, Force: true}, "off")
	if err := off.Handle(e.ctx, d); err != nil {
		t.Fatal(err)
	}
	if st, res := e.jobState(d); st != jobs.StatusSucceeded || !strings.Contains(res, notify.ReasonLineDisabled) {
		t.Fatalf("job with LINE off: %s %s", st, res)
	}
	if len(e.line.Sent()) != 0 {
		t.Fatal("LINE off sent a card")
	}
}

// Skipped cards: no LINE group configured (the job succeeds, as the legacy callable answered {ok, skipped}); LINE
// refusing with a 4xx (recorded with LINE's message, the job fails); 429 retries.
func TestLineSkips(t *testing.T) {
	e := newLineEnv(t)
	e.exec(`UPDATE standby_records SET customer_party_id = $2 WHERE id = $1`, e.standby, e.noGroup)
	d := e.job(notify.LineForceParams{Card: notify.LineStandby, ID: &e.standby}, "ng")
	e.handle(d)
	if st, res := e.jobState(d); st != jobs.StatusSucceeded || !strings.Contains(res, notify.ReasonNoLineGroup) {
		t.Fatalf("no group: %s %s", st, res)
	}
	if e.str(`SELECT line_notified_at::text FROM standby_records WHERE id = $1`, e.standby) != "" {
		t.Fatal("a skipped card set the flag")
	}

	e.line.FailNext(429, "Too Many Requests")
	d = e.event(notify.RouteTaskCheckedIn, e.task, nil)
	if err := e.consumer.Handle(e.ctx, d); err == nil || mq.IsPermanent(err) {
		t.Fatalf("429: %v, want a transient error", err)
	}
	e.line.FailNext(400, "The property, 'to', in the request body is invalid")
	e.handle(d)
	rows := e.rows()
	last := rows[len(rows)-1]
	if last.Kind != "line_checkin" || last.Status != notify.StatusSkipped || !strings.Contains(last.Error, "HTTP 400") {
		t.Fatalf("4xx row %+v", last)
	}
	if len(e.line.Sent()) != 0 {
		t.Fatal("a refused push was recorded as sent")
	}
	// A refused card does not block the next request (the idem:line claim is released).
	e.handle(e.event(notify.RouteTaskCheckedIn, e.task, nil))
	if len(e.line.Sent()) != 1 {
		t.Fatal("the card was not sent after LINE refused an earlier attempt")
	}
}

// A driver's shim request on a record the mirror has not loaded waits (transient) and is sent once the record exists;
// a request for another driver's record is never sent (not_task_owner, the job fails).
func TestLineDriverRequests(t *testing.T) {
	e := newLineEnv(t)
	d := e.job(notify.LineForceParams{Card: notify.LineDelivered, Ref: "fsTripLater", DriverID: &e.driver}, "delivered:fsTripLater")
	if err := e.consumer.Handle(e.ctx, d); err == nil || mq.IsPermanent(err) || !strings.Contains(err.Error(), "not mirrored") {
		t.Fatalf("a record not mirrored yet: %v, want a transient wait", err)
	}
	e.exec(`UPDATE trip_records SET legacy_doc_id = 'fsTripLater' WHERE id = $1`, e.trip)
	e.handle(d)
	if st, _ := e.jobState(d); st != jobs.StatusSucceeded || len(e.line.Sent()) != 1 {
		t.Fatalf("the driver's request once mirrored: job %s, %d pushes", st, len(e.line.Sent()))
	}

	d = e.job(notify.LineForceParams{Card: notify.LineCheckin, Ref: "fsTask1", DriverID: &e.other}, "checkin:fsTask1")
	e.handle(d)
	if st, res := e.jobState(d); st != jobs.StatusFailed || !strings.Contains(res, notify.ReasonNotTaskOwner) {
		t.Fatalf("another driver's task: %s %s", st, res)
	}
	if len(e.line.Sent()) != 1 {
		t.Fatal("another driver's request sent a card")
	}

	// Never found within LineMirrorWait: settled skipped (record not found), no dead letter.
	d = e.job(notify.LineForceParams{Card: notify.LineStandby, Ref: "fsNever", DriverID: &e.driver}, "standby:fsNever")
	e.now = time.Now().UTC().Add(notify.LineMirrorWait + time.Minute) // jobs.created_at is the database clock
	e.handle(d)
	if st, res := e.jobState(d); st != jobs.StatusFailed || !strings.Contains(res, notify.ReasonRecordNotFound) {
		t.Fatalf("a record never mirrored: %s %s", st, res)
	}
}
