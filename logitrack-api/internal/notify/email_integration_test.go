//go:build integration

// Acceptance test of notify.email (issue T10) end to end with the auth service of #25:
// POST /v1/auth/password/forgot commits auth.password_reset_requested, the relay publishes it, the
// worker's consumer creates the token and mails the link, the mail arrives in Mailpit in Thai and in
// English, and the mailed token sets a new password at POST /v1/auth/password/reset exactly once.
package notify_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/email"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) {
	app.DrainGrace = 0
	code := pgtest.Main(m)
	asynctest.Terminate()
	os.Exit(code)
}

const (
	webBase = "https://web.logitrack.test"
	prefix  = "lt:local:"
)

var linkRx = regexp.MustCompile(regexp.QuoteMeta(webBase) + `/reset-password#token=([A-Za-z0-9_-]{43})`)

// serveAuth runs the api's listeners with the auth groups of #25 and returns the internal base URL.
func serveAuth(t *testing.T, svc *auth.Service) string {
	t.Helper()
	cfg := &app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second},
		InternalAddr: "127.0.0.1:0", PublicAddr: "127.0.0.1:0", PublicRouteGroups: ingress.PublicPrefixes,
	}
	a, err := app.NewAPI(cfg, zerolog.Nop(), svc.Groups()...)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = a.Serve(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	in, _, _ := a.Addrs()
	return "http://" + in
}

func post(t *testing.T, url string, body any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestPasswordResetMailArrivesInThaiAndEnglish(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	appPool, etl := d.Pool(t, db.RoleApp), d.Pool(t, db.RoleETL)
	rdb := asynctest.SharedRedis(t).Client(t)

	// The auth service of #25 with test-cheap Argon2id parameters.
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
	ks, err := cache.NewKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.New(auth.Config{RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour,
		PasswordResetTTL: 30 * time.Minute, LoginIP: ratelimit.Limit{Count: 1000, Window: time.Minute}},
		auth.Deps{Pool: appPool, Store: auth.NewStore(rdb, prefix), Limiter: ratelimit.New(rdb, ks, zerolog.Nop()), Keys: token.New(priv, "http://localhost", "test", 15*time.Minute),
			Hasher: hasher, Policy: policy, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	api := serveAuth(t, svc)

	user := func(email, name, status string) uuid.UUID {
		var id uuid.UUID
		disabled := any(nil)
		if status == "disabled" {
			disabled = time.Now()
		}
		if err := etl.QueryRow(ctx, `INSERT INTO users (email, display_name, status, disabled_at) VALUES ($1, $2, $3, $4) RETURNING id`,
			email, name, status, disabled).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	somchai := user("somchai@logitrack.test", "สมชาย ใจดี", "active")
	user("jane@logitrack.test", "Jane Doe", "active")
	invitee := user("new.staff@logitrack.test", "", "active")
	silent := user("silent@logitrack.test", "Silent", "active")
	user("gone@logitrack.test", "Gone", "disabled")

	// #25: forgot-password always answers 202 and commits only the outbox event (no token).
	for _, req := range []map[string]string{
		{"email": "somchai@logitrack.test", "locale": "th"},
		{"email": "jane@logitrack.test", "locale": "en"},
		{"email": "gone@logitrack.test"},   // disabled: 202, nothing queued
		{"email": "nobody@logitrack.test"}, // unknown: 202, nothing queued
	} {
		if code := post(t, api+"/v1/auth/password/forgot", req); code != http.StatusAccepted {
			t.Fatalf("forgot %s: %d", req["email"], code)
		}
	}
	var tokens int
	if err := etl.QueryRow(ctx, `SELECT count(*) FROM password_reset_tokens`).Scan(&tokens); err != nil || tokens != 0 {
		t.Fatalf("the api created %d tokens (%v); the consumer creates them", tokens, err)
	}
	// What the users admin (T19) will commit: an invite, and a creation without one.
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []outbox.Event{
		{RoutingKey: notify.RouteUserInvited, AggregateType: "user", AggregateID: invitee.String(),
			Payload: map[string]any{"userId": invitee, "locale": "en", "requestedBy": somchai}},
		{RoutingKey: notify.RouteUserCreated, AggregateType: "user", AggregateID: silent.String(),
			Payload: map[string]any{"userId": silent}}, // no sendInvite: nothing to mail
		{RoutingKey: notify.RouteUserCreated, AggregateType: "user", AggregateID: silent.String(),
			Payload: map[string]any{"userId": silent, "purpose": "invite", "sendInvite": false}}, // a purpose changes nothing
	} {
		if _, err := outbox.Append(ctx, tx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// Relay -> RabbitMQ -> worker -> Mailpit.
	url, _ := asynctest.SharedRabbit(t).VHost(t)
	conn, err := mq.Dial(url, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := mq.DeclareTopology(conn, mq.Default); err != nil {
		t.Fatal(err)
	}
	rt := realtime.NewWriter(rdb, ks, 100, time.Hour)
	relay := outbox.NewRelay(appPool, func() (outbox.Publisher, error) { return mq.NewPublisher(conn) }, rt, outbox.RelayOptions{Log: zerolog.Nop()})
	if _, err := relay.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	mp := asynctest.StartMailpit(t)
	sender, err := email.New(email.Config{Host: mp.SMTPHost, Port: mp.SMTPPort, From: "no-reply@logitrack.test", FromName: "LogiTrack"})
	if err != nil {
		t.Fatal(err)
	}
	consumer := &notify.Email{Pool: appPool, Sender: sender, Tokens: notify.ResetTokens{ResetTTL: 30 * time.Minute},
		WebBaseURL: webBase + "/", ResetTTL: 30 * time.Minute, Enabled: true, Log: zerolog.Nop()}
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = mq.Consume(cctx, conn, consumer.Registration(), mq.ConsumerOptions{Topology: mq.Default, Log: zerolog.New(zerolog.NewTestWriter(t))})
	}()
	defer func() { cancel(); <-done }()

	mp.WaitMessages(t, 3, 15*time.Second)
	time.Sleep(500 * time.Millisecond) // nothing else may arrive
	mails := mp.Messages(t)
	if len(mails) != 3 {
		t.Fatalf("Mailpit holds %d mails, want 3 (th reset, en reset, en invite)", len(mails))
	}
	byTo := map[string]asynctest.Mail{}
	for _, m := range mails {
		if len(m.To) != 1 || m.From.Address != "no-reply@logitrack.test" || m.From.Name != "LogiTrack" {
			t.Fatalf("mail headers: from %+v to %+v", m.From, m.To)
		}
		byTo[m.To[0].Address] = m
	}
	check := func(to, purpose, subject string, texts ...string) string {
		t.Helper()
		m, ok := byTo[to]
		if !ok {
			t.Fatalf("no mail to %s", to)
		}
		if m.Subject != subject {
			t.Fatalf("subject to %s = %q, want %q", to, m.Subject, subject)
		}
		if !asynctest.Contains(m.Text, texts...) || !strings.Contains(m.HTML, "reset-password#token=") {
			t.Fatalf("mail to %s lacks %q:\n%s", to, texts, m.Text)
		}
		tok := linkRx.FindStringSubmatch(m.Text)
		if tok == nil {
			t.Fatalf("mail to %s has no %s/reset-password#token= link:\n%s", to, webBase, m.Text)
		}
		sum := sha256.Sum256([]byte(tok[1]))
		var gotPurpose string
		var ttl float64
		if err := etl.QueryRow(ctx, `SELECT purpose, extract(epoch FROM expires_at - created_at) FROM password_reset_tokens
			WHERE token_hash = $1 AND used_at IS NULL`, sum[:]).Scan(&gotPurpose, &ttl); err != nil {
			t.Fatalf("the link of %s matches no password_reset_tokens row: %v", to, err)
		}
		want := 30 * time.Minute
		if purpose == notify.PurposeInvite {
			want = notify.InviteTTL
		}
		if gotPurpose != purpose || time.Duration(ttl*float64(time.Second)).Round(time.Minute) != want {
			t.Fatalf("token of %s: purpose %s ttl %vs", to, gotPurpose, ttl)
		}
		return tok[1]
	}
	th := check("somchai@logitrack.test", "reset", "ตั้งรหัสผ่านใหม่สำหรับ LogiTrack", "สวัสดีคุณสมชาย ใจดี", "30 นาที")
	check("jane@logitrack.test", "reset", "Reset your LogiTrack password", "Hello Jane Doe", "30 minutes")
	check("new.staff@logitrack.test", "invite", "You are invited to LogiTrack", "Hello new.staff@logitrack.test", "72 hours")

	var inbox int
	if err := etl.QueryRow(ctx, `SELECT (SELECT count(*) FROM password_reset_tokens), (SELECT count(*) FROM consumer_inbox WHERE consumer = 'notify.email')`).
		Scan(&tokens, &inbox); err != nil {
		t.Fatal(err)
	}
	if tokens != 3 || inbox != 3 {
		t.Fatalf("%d tokens and %d inbox rows, want 3 and 3", tokens, inbox)
	}
	var silentTokens int
	if err := etl.QueryRow(ctx, `SELECT count(*) FROM password_reset_tokens WHERE user_id = $1`, silent).Scan(&silentTokens); err != nil || silentTokens != 0 {
		t.Fatalf("user.created without sendInvite created %d tokens (%v)", silentTokens, err)
	}

	// The mailed token is the one #25's reset accepts, once.
	reset := map[string]string{"token": th, "newPassword": "a brand new passphrase 2"}
	if code := post(t, api+"/v1/auth/password/reset", reset); code != http.StatusNoContent {
		t.Fatalf("reset with the mailed token: %d", code)
	}
	if code := post(t, api+"/v1/auth/password/reset", reset); code != http.StatusUnprocessableEntity {
		t.Fatalf("second use of the token: %d, want 422", code)
	}
	if code := post(t, api+"/v1/auth/login", map[string]string{"email": "somchai@logitrack.test",
		"password": "a brand new passphrase 2", "platform": "web"}); code != http.StatusOK {
		t.Fatalf("login with the new password: %d", code)
	}
}

// job.notify.email: the params are the link request, and the job row follows the send (succeeded),
// or records why nothing was sent (failed, acked without retry).
func TestNotifyEmailJob(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	appPool, etl := d.Pool(t, db.RoleApp), d.Pool(t, db.RoleETL)
	var active, disabled uuid.UUID
	if err := etl.QueryRow(ctx, `INSERT INTO users (email, display_name) VALUES ('ops@logitrack.test', 'Ops') RETURNING id`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := etl.QueryRow(ctx, `INSERT INTO users (email, status, disabled_at) VALUES ('off@logitrack.test', 'disabled', now()) RETURNING id`).Scan(&disabled); err != nil {
		t.Fatal(err)
	}
	var ok, skipped jobs.Job
	if err := db.WithSystem(ctx, appPool, nil, func(tx pgx.Tx) error {
		var err error
		if ok, err = jobs.Enqueue(ctx, tx, jobs.NewInput{Type: "notify.email", Params: map[string]any{"userId": active, "locale": "en"}}); err != nil {
			return err
		}
		skipped, err = jobs.Enqueue(ctx, tx, jobs.NewInput{Type: "notify.email", Params: map[string]any{"userId": disabled}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	url, _ := asynctest.SharedRabbit(t).VHost(t)
	conn, err := mq.Dial(url, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := mq.DeclareTopology(conn, mq.Default); err != nil {
		t.Fatal(err)
	}
	ks, err := cache.NewKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	rt := realtime.NewWriter(asynctest.SharedRedis(t).Client(t), ks, 100, time.Hour)
	relay := outbox.NewRelay(appPool, func() (outbox.Publisher, error) { return mq.NewPublisher(conn) }, rt, outbox.RelayOptions{Log: zerolog.Nop()})
	if _, err := relay.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	mp := asynctest.StartMailpit(t)
	sender, err := email.New(email.Config{Host: mp.SMTPHost, Port: mp.SMTPPort, From: "no-reply@logitrack.test"})
	if err != nil {
		t.Fatal(err)
	}
	consumer := &notify.Email{Pool: appPool, Sender: sender, Tokens: notify.ResetTokens{ResetTTL: 30 * time.Minute},
		WebBaseURL: webBase, ResetTTL: 30 * time.Minute, Enabled: true, Log: zerolog.Nop()}
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = mq.Consume(cctx, conn, consumer.Registration(), mq.ConsumerOptions{Topology: mq.Default, Log: zerolog.Nop()})
	}()
	defer func() { cancel(); <-done }()

	status := func(id uuid.UUID) (string, *string) {
		var s string
		var e *string
		if err := appPool.QueryRow(ctx, `SELECT status, error FROM jobs WHERE id = $1`, id).Scan(&s, &e); err != nil {
			t.Fatal(err)
		}
		return s, e
	}
	asynctest.Eventually(t, 15*time.Second, "both jobs to finish", func() bool {
		a, _ := status(ok.ID)
		b, _ := status(skipped.ID)
		return a != "queued" && a != "running" && b != "queued" && b != "running"
	})
	if s, _ := status(ok.ID); s != "succeeded" {
		t.Fatalf("the job for an active user is %s", s)
	}
	if s, e := status(skipped.ID); s != "failed" || e == nil || !strings.Contains(*e, "disabled") {
		t.Fatalf("the job for a disabled user is %s (%v)", s, e)
	}
	if mails := mp.WaitMessages(t, 1, 5*time.Second); len(mails) != 1 || mails[0].Subject != "Reset your LogiTrack password" {
		t.Fatalf("mails %+v", mails)
	}
}

// With EMAIL_ENABLED=false a job.notify.email command creates no token and sends nothing, but its job
// still ends (succeeded, sent 0) instead of staying queued; a duplicate of the command is acked.
func TestNotifyEmailJobWithEmailDisabled(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	appPool, etl := d.Pool(t, db.RoleApp), d.Pool(t, db.RoleETL)
	var uid uuid.UUID
	if err := etl.QueryRow(ctx, `INSERT INTO users (email) VALUES ('ops@logitrack.test') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	var job jobs.Job
	if err := db.WithSystem(ctx, appPool, nil, func(tx pgx.Tx) error {
		var err error
		job, err = jobs.Enqueue(ctx, tx, jobs.NewInput{Type: "notify.email", Params: map[string]any{"userId": uid}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(jobs.Command{JobID: job.ID, Type: job.Type, Params: job.Params})
	if err != nil {
		t.Fatal(err)
	}
	consumer := &notify.Email{Pool: appPool, Tokens: notify.ResetTokens{ResetTTL: 30 * time.Minute},
		WebBaseURL: webBase, ResetTTL: 30 * time.Minute, Enabled: false, Log: zerolog.Nop()}
	for range 2 {
		if err := consumer.Handle(ctx, &mq.Delivery{Queue: notify.QueueEmail, MessageID: "7", Exchange: mq.ExchangeJobs,
			RoutingKey: notify.RouteJob, Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	var status, sent string
	var tokens int
	if err := etl.QueryRow(ctx, `SELECT status, result->>'sent', (SELECT count(*) FROM password_reset_tokens) FROM jobs WHERE id = $1`, job.ID).
		Scan(&status, &sent, &tokens); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || sent != "0" || tokens != 0 {
		t.Fatalf("job %s sent %s with %d tokens; want succeeded, 0, 0", status, sent, tokens)
	}
}
