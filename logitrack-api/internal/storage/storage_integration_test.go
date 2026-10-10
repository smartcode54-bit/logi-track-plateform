//go:build integration

// Acceptance tests of issue T11 on postgres:18-alpine and the compose MinIO image: presign -> PUT -> commit on
// both backends (the browser's CORS preflight from http://localhost:3000 included), the 422 commit failures that
// leave the row pending, URLs signed for S3_PRESIGN_ENDPOINT, presigned GET expiry, storage.gc on both backends,
// reads that follow each row's backend after STORAGE_BACKEND changes, and the evidence token verifier.
package storage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage/storagetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) {
	code := pgtest.Main(m)
	storagetest.Terminate()
	os.Exit(code)
}

// signingKey is a test-only LOCAL_MEDIA_SIGNING_KEY (low entropy on purpose).
var signingKey = strings.Repeat("s", 48)

type fixture struct {
	pool, etl  *pgxpool.Pool
	own, sub   uuid.UUID // own fleet and a carrier working for it
	other      uuid.UUID // an unrelated carrier
	driver     uuid.UUID // uploader in the own fleet
	staff      uuid.UUID // operator of the own fleet
	subStaff   uuid.UUID
	otherStaff uuid.UUID
	now        time.Time
	localDir   string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := &fixture{pool: d.Pool(t, db.RoleApp), etl: d.Pool(t, db.RoleETL), now: time.Now().UTC().Truncate(time.Millisecond),
		localDir: t.TempDir()}
	ctx := context.Background()
	q := func(sql string, args ...any) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := f.etl.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return id
	}
	f.own = q(`INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'Own') RETURNING id`)
	f.sub = q(`INSERT INTO tenants (kind, name_th, legal_type, contractor_tenant_id) VALUES ('carrier', 'Sub', 'company', $1) RETURNING id`, f.own)
	f.other = q(`INSERT INTO tenants (kind, name_th, legal_type) VALUES ('carrier', 'Other', 'company') RETURNING id`)
	for _, u := range []*uuid.UUID{&f.driver, &f.staff, &f.subStaff, &f.otherStaff} {
		*u = q(`INSERT INTO users (email) VALUES ($1) RETURNING id`, uuid.NewString()+"@example.test")
	}
	return f
}

func (f *fixture) caller(user uuid.UUID, tenant uuid.UUID, staff bool) storage.Caller {
	return storage.Caller{UserID: user, TenantID: &tenant, Staff: staff}
}

func (f *fixture) local(t *testing.T) *storage.Local {
	t.Helper()
	l, err := storage.NewLocal(storage.LocalConfig{Dir: f.localDir, PublicBaseURL: "http://media.test/media",
		SigningKey: []byte(signingKey), APIUploadPath: storage.LocalUploadPath, Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func (f *fixture) service(t *testing.T, active string, backends ...storage.Backend) *storage.Service {
	t.Helper()
	return f.serviceOn(t, active, storage.S3Config{Bucket: "logitrack", PublicBucket: "logitrack-public"}, backends...)
}

// serviceOn uses the bucket names of an S3 test configuration (fresh buckets per test).
func (f *fixture) serviceOn(t *testing.T, active string, buckets storage.S3Config, backends ...storage.Backend) *storage.Service {
	t.Helper()
	s, err := storage.New(f.pool, storage.Config{Active: active, Bucket: buckets.Bucket, PublicBucket: buckets.PublicBucket,
		PresignPutTTL: 15 * time.Minute, PresignGetTTL: time.Hour, UploadMaxBytes: 1 << 20}, zerolog.Nop(), backends...)
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return f.now })
	return s
}

// s3 returns a bootstrapped S3 backend on fresh buckets. Its URLs are signed for another spelling of the
// container's host than the server-side endpoint, so a URL carrying the server host would be visible.
func s3Backend(t *testing.T) (*storage.S3, storage.S3Config) {
	t.Helper()
	m := storagetest.Shared(t)
	u, err := url.Parse(m.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(u.Host)
	other := map[string]string{"localhost": "127.0.0.1", "127.0.0.1": "localhost"}[host]
	if other == "" {
		t.Skipf("docker host %s has no second loopback spelling", host)
	}
	cfg := m.Config("http://" + net.JoinHostPort(other, port))
	s, err := storage.NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := s.Bootstrap(ctx, zerolog.Nop()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := s.Bootstrap(ctx, zerolog.Nop()); err != nil {
		t.Fatalf("bootstrap is not idempotent: %v", err)
	}
	cfg.Endpoint = m.Endpoint
	return s, cfg
}

func put(t *testing.T, p *storage.Presigned, base string, body []byte, origin string) *http.Response {
	t.Helper()
	target := p.URL
	if strings.HasPrefix(target, "/") {
		target = base + target
	}
	req, err := http.NewRequest(p.Method, target, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp
}

func (f *fixture) commit(t *testing.T, s *storage.Service, in storage.CommitInput) (storage.Committed, error) {
	t.Helper()
	var out storage.Committed
	err := db.WithSystem(context.Background(), f.pool, nil, func(tx pgx.Tx) error {
		var err error
		out, err = s.Commit(context.Background(), tx, in)
		return err
	})
	return out, err
}

func (f *fixture) status(t *testing.T, key string) (status, backend string) {
	t.Helper()
	if err := f.etl.QueryRow(context.Background(), `SELECT status, storage_backend FROM file_objects WHERE object_key = $1`, key).
		Scan(&status, &backend); err != nil {
		t.Fatalf("row of %s: %v", key, err)
	}
	return status, backend
}

func wantField(t *testing.T, err error, field, reason string) {
	t.Helper()
	e, ok := errors.AsType[*httpx.Error](err)
	if !ok || e.Status != http.StatusUnprocessableEntity || e.Code != "invalid_argument" {
		t.Fatalf("err = %v, want 422 invalid_argument %s/%s", err, field, reason)
	}
	b, _ := json.Marshal(e.Details)
	if !strings.Contains(string(b), `"field":"`+field+`"`) || !strings.Contains(string(b), `"reason":"`+reason+`"`) {
		t.Fatalf("details %s, want %s/%s", b, field, reason)
	}
}

// AC 1-3 on the s3 backend: the browser's presigned PUT from http://localhost:3000 passes MinIO's CORS, every URL
// carries S3_PRESIGN_ENDPOINT, a commit with a wrong size or content type is 422 and leaves the row pending, a good
// one commits; a presigned GET expires after its TTL.
func TestS3PresignUploadCommit(t *testing.T) {
	f := setup(t)
	s3, cfg := s3Backend(t)
	svc := f.serviceOn(t, storage.BackendS3, cfg, s3)
	ctx := context.Background()
	driver := f.caller(f.driver, f.own, false)
	trip := uuid.New()
	body := []byte("\xff\xd8\xff not really a jpeg")

	p, err := svc.Presign(ctx, driver, storage.PresignInput{Purpose: "trip_photo", EntityID: trip.String(), Variant: "seal",
		ContentType: "image/jpeg", SizeBytes: int64(len(body))}, storage.PutOptions{APIPath: true})
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(p.URL)
	server, _ := url.Parse(cfg.Endpoint)
	if pu.Host == server.Host || pu.Host != strings.TrimPrefix(cfg.PresignEndpoint, "http://") || p.StorageBackend != "s3" {
		t.Fatalf("URL %s is not signed for S3_PRESIGN_ENDPOINT %s (server %s)", p.URL, cfg.PresignEndpoint, cfg.Endpoint)
	}
	if st, backend := f.status(t, p.Key); st != "pending" || backend != "s3" {
		t.Fatalf("row %s/%s", st, backend)
	}

	// The browser: CORS preflight, then the PUT with Origin (CORS_ALLOWED_ORIGINS of .env.example).
	pre, _ := http.NewRequest(http.MethodOptions, p.URL, nil)
	pre.Header.Set("Origin", "http://localhost:3000")
	pre.Header.Set("Access-Control-Request-Method", "PUT")
	pre.Header.Set("Access-Control-Request-Headers", "content-type")
	resp, err := http.DefaultClient.Do(pre)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 || resp.Header.Get("Access-Control-Allow-Origin") == "" ||
		!strings.Contains(strings.ToUpper(resp.Header.Get("Access-Control-Allow-Methods")), "PUT") {
		t.Fatalf("preflight: %d %v", resp.StatusCode, resp.Header)
	}
	if r := put(t, p, "", body, "http://localhost:3000"); r.StatusCode != http.StatusOK ||
		(r.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" && r.Header.Get("Access-Control-Allow-Origin") != "*") {
		t.Fatalf("browser PUT: %d %v", r.StatusCode, r.Header)
	}
	// A PUT with another content type than the signed one is refused by the signature.
	wrong := *p
	wrong.Headers = map[string]string{"Content-Type": "text/html"}
	if r := put(t, &wrong, "", body, ""); r.StatusCode != http.StatusForbidden {
		t.Fatalf("PUT with an unsigned content type: %d", r.StatusCode)
	}

	in := storage.CommitInput{Key: p.Key, Field: "photoKey", Purposes: []string{"trip_photo"}, OwnerID: trip, UserID: f.driver, TenantID: &f.own}
	// Wrong size: declared 3 bytes, the object has more.
	short, err := svc.Presign(ctx, driver, storage.PresignInput{Purpose: "trip_photo", EntityID: trip.String(), Variant: "closing",
		ContentType: "image/jpeg", SizeBytes: 3}, storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r := put(t, short, "", body, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("PUT: %d", r.StatusCode)
	}
	_, err = f.commit(t, svc, storage.CommitInput{Key: short.Key, Field: "photoKey", Purposes: []string{"trip_photo"}, OwnerID: trip, UserID: f.driver})
	wantField(t, err, "photoKey", "size_mismatch")
	if st, _ := f.status(t, short.Key); st != "pending" {
		t.Fatalf("after a failed commit the row is %s", st)
	}
	// Wrong content type: an object written out of band with another type.
	odd, err := svc.Presign(ctx, driver, storage.PresignInput{Purpose: "trip_photo", EntityID: trip.String(), Variant: "runsheet",
		ContentType: "image/jpeg", SizeBytes: int64(len(body))}, storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s3.Put(ctx, storage.Object{Bucket: cfg.Bucket, Key: odd.Key}, bytes.NewReader(body), int64(len(body)), "image/png"); err != nil {
		t.Fatal(err)
	}
	_, err = f.commit(t, svc, storage.CommitInput{Key: odd.Key, Purposes: []string{"trip_photo"}, OwnerID: trip, UserID: f.driver})
	wantField(t, err, "key", "content_type_mismatch")
	if st, _ := f.status(t, odd.Key); st != "pending" {
		t.Fatalf("after a failed commit the row is %s", st)
	}
	// Not uploaded, other purpose, other uploader in another tenant.
	missing, _ := svc.Presign(ctx, driver, storage.PresignInput{Purpose: "trip_photo", EntityID: trip.String(), Variant: "pre_close",
		ContentType: "image/jpeg", SizeBytes: 5}, storage.PutOptions{})
	_, err = f.commit(t, svc, storage.CommitInput{Key: missing.Key, Purposes: []string{"trip_photo"}, OwnerID: trip, UserID: f.driver})
	wantField(t, err, "key", "upload_missing")
	_, err = f.commit(t, svc, storage.CommitInput{Key: p.Key, Purposes: []string{"standby_photo"}, OwnerID: trip, UserID: f.driver})
	wantField(t, err, "key", "purpose_mismatch")
	_, err = f.commit(t, svc, storage.CommitInput{Key: p.Key, Purposes: []string{"trip_photo"}, OwnerID: trip, UserID: f.otherStaff, TenantID: &f.other})
	wantField(t, err, "key", "not_found")

	// Success: committed, owner linked, storage.object_committed queued; a replay returns the same row.
	c, err := f.commit(t, svc, in)
	if err != nil {
		t.Fatal(err)
	}
	var owner uuid.UUID
	var events int
	if err := f.etl.QueryRow(ctx, `SELECT owner_id, (SELECT count(*) FROM outbox_events WHERE routing_key = 'storage.object_committed'
		AND aggregate_id = $2) FROM file_objects WHERE id = $1 AND status = 'committed' AND expires_at IS NULL`, c.ID, c.ID.String()).Scan(&owner, &events); err != nil ||
		owner != trip || events != 1 {
		t.Fatalf("committed row owner %s events %d (%v)", owner, events, err)
	}
	if again, err := f.commit(t, svc, in); err != nil || again.ID != c.ID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	other := in
	other.OwnerID = uuid.New()
	_, err = f.commit(t, svc, other)
	wantField(t, err, "photoKey", "already_committed")

	// Download: GET /v1/files resolves on S3_PRESIGN_ENDPOINT; a URL dies after its TTL.
	u, err := svc.DownloadURL(ctx, f.caller(f.staff, f.own, true), p.Key)
	if err != nil {
		t.Fatal(err)
	}
	if gu, _ := url.Parse(u); gu.Host != pu.Host {
		t.Fatalf("download URL %s not on the presign host", u)
	}
	short2, err := svc.SignedURL(ctx, c.ID, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := http.Get(short2); err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("fresh GET: %v %v", r, err)
	} else {
		got, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if !bytes.Equal(got, body) {
			t.Fatal("downloaded bytes differ")
		}
	}
	time.Sleep(3 * time.Second)
	if r, err := http.Get(short2); err != nil || r.StatusCode != http.StatusForbidden {
		t.Fatalf("expired presigned GET: %v %v", r, err)
	} else {
		_ = r.Body.Close()
	}
}

// Presign validation, tenant rules, re-signing and the read rules of GET /v1/files.
func TestPresignRulesAndReadRules(t *testing.T) {
	f := setup(t)
	svc := f.service(t, storage.BackendLocal, f.local(t))
	ctx := context.Background()
	driver := f.caller(f.driver, f.own, false)
	entity := uuid.New().String()

	_, err := svc.Presign(ctx, driver, storage.PresignInput{Purpose: "apk", ContentType: "image/jpeg", SizeBytes: 1}, storage.PutOptions{})
	wantField(t, err, "purpose", "invalid")
	_, err = svc.Presign(ctx, driver, storage.PresignInput{Purpose: "chat_image", EntityID: entity, ContentType: "text/html", SizeBytes: 1}, storage.PutOptions{})
	wantField(t, err, "contentType", "not_allowed")
	_, err = svc.Presign(ctx, driver, storage.PresignInput{Purpose: "chat_image", EntityID: entity, ContentType: "image/jpeg", SizeBytes: 2 << 20}, storage.PutOptions{})
	wantField(t, err, "sizeBytes", "out_of_range")
	_, err = svc.Presign(ctx, driver, storage.PresignInput{Purpose: "chat_image", ContentType: "image/jpeg", SizeBytes: 1}, storage.PutOptions{})
	wantField(t, err, "entityId", "required")
	_, err = svc.Presign(ctx, driver, storage.PresignInput{Purpose: "standby_photo", EntityID: entity, Variant: "../x", ContentType: "image/jpeg", SizeBytes: 1}, storage.PutOptions{})
	wantField(t, err, "variant", "invalid")
	if _, err := svc.Presign(ctx, driver, storage.PresignInput{Purpose: "user_photo", EntityID: f.staff.String(), ContentType: "image/jpeg", SizeBytes: 1}, storage.PutOptions{}); err == nil {
		t.Fatal("a profile photo was presigned for another user")
	}
	if _, err := svc.Presign(ctx, storage.Caller{UserID: f.driver}, storage.PresignInput{Purpose: "chat_image", EntityID: entity, ContentType: "image/jpeg", SizeBytes: 1}, storage.PutOptions{}); err == nil {
		t.Fatal("an upload without a tenant")
	}
	machine := f.caller(uuid.New(), f.own, false)
	machine.Machine = true
	if _, err := svc.Presign(ctx, machine, storage.PresignInput{Purpose: "chat_image", EntityID: entity, ContentType: "image/jpeg", SizeBytes: 1}, storage.PutOptions{}); err == nil {
		t.Fatal("an API-key principal uploaded")
	}
	// A steward acting in no tenant (platform_admin) uploads a platform object.
	if p, err := svc.Presign(ctx, storage.Caller{UserID: f.staff, Steward: true}, storage.PresignInput{Purpose: "chat_image", EntityID: entity,
		ContentType: "image/jpeg", SizeBytes: 1}, storage.PutOptions{}); err != nil {
		t.Fatalf("steward platform upload: %v", err)
	} else {
		var tenant *uuid.UUID
		if err := f.etl.QueryRow(ctx, `SELECT tenant_id FROM file_objects WHERE object_key = $1`, p.Key).Scan(&tenant); err != nil || tenant != nil {
			t.Fatalf("platform object tenant %v %v", tenant, err)
		}
	}

	p, err := svc.Presign(ctx, driver, storage.PresignInput{Purpose: "chat_image", EntityID: entity, ContentType: "image/jpeg", SizeBytes: 4}, storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Re-sign (offline retry): only the uploader, only while pending; the row lives another 24 h.
	f.now = f.now.Add(20 * time.Hour)
	again, err := svc.Presign(ctx, driver, storage.PresignInput{Key: p.Key}, storage.PutOptions{})
	if err != nil || again.Key != p.Key {
		t.Fatalf("re-sign: %+v %v", again, err)
	}
	var exp time.Time
	if err := f.etl.QueryRow(ctx, `SELECT expires_at FROM file_objects WHERE object_key = $1`, p.Key).Scan(&exp); err != nil ||
		!exp.Equal(f.now.Add(storage.PendingTTL)) {
		t.Fatalf("expires_at %s, want %s", exp, f.now.Add(storage.PendingTTL))
	}
	_, err = svc.Presign(ctx, f.caller(f.staff, f.own, true), storage.PresignInput{Key: p.Key}, storage.PutOptions{})
	wantField(t, err, "key", "not_found")
	_, err = svc.Presign(ctx, driver, storage.PresignInput{Key: "../../etc/passwd"}, storage.PutOptions{})
	wantField(t, err, "key", "not_found")

	// Read rules (p_read): uploader (also while pending), staff of the tenant and of its contractor; not others.
	if _, err := svc.DownloadURL(ctx, driver, p.Key); err != nil {
		t.Fatalf("own pending upload: %v", err)
	}
	if _, err := svc.DownloadURL(ctx, f.caller(f.staff, f.own, true), p.Key); err == nil {
		t.Fatal("staff read a pending upload of someone else")
	}
	if _, err := f.svcWrite(t, svc, p.Key, []byte("abcd"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.commit(t, svc, storage.CommitInput{Key: p.Key, Purposes: []string{"chat_image"}, OwnerID: uuid.MustParse(entity), UserID: f.driver}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		c  storage.Caller
		ok bool
	}{
		"uploader":           {driver, true},
		"staff of the owner": {f.caller(f.staff, f.own, true), true},
		"driver of the same": {f.caller(f.staff, f.own, false), false},
		"staff of a carrier": {f.caller(f.subStaff, f.sub, true), false},
		"unrelated staff":    {f.caller(f.otherStaff, f.other, true), false},
		"read-only bypass":   {storage.Caller{UserID: f.otherStaff, ReadAll: true}, true},
	} {
		_, err := svc.DownloadURL(ctx, tc.c, p.Key)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Contractor reach (R60): staff of the own fleet read a carrier's files.
	sp, err := svc.Presign(ctx, f.caller(f.subStaff, f.sub, true), storage.PresignInput{Purpose: "chat_image", EntityID: entity, ContentType: "image/jpeg", SizeBytes: 1}, storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svcWrite(t, svc, sp.Key, []byte("x"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.commit(t, svc, storage.CommitInput{Key: sp.Key, Purposes: []string{"chat_image"}, OwnerID: uuid.New(), UserID: f.subStaff, TenantID: &f.sub}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DownloadURL(ctx, f.caller(f.staff, f.own, true), sp.Key); err != nil {
		t.Fatalf("own-fleet staff read a carrier's file: %v", err)
	}
	// An owner-kind authorizer widens reads ("a file is readable iff its referencing row is").
	svc.RegisterAuthorizer(storage.OwnerChat, func(_ context.Context, c storage.Caller, _ uuid.UUID) (bool, error) {
		return c.UserID == f.otherStaff, nil
	})
	if _, err := svc.DownloadURL(ctx, f.caller(f.otherStaff, f.other, true), p.Key); err != nil {
		t.Fatalf("authorizer: %v", err)
	}
	if _, err := svc.DownloadURL(ctx, driver, "nope/missing.jpg"); err == nil {
		t.Fatal("unknown key")
	}
}

// svcWrite stands in for the client's PUT on the local backend.
func (f *fixture) svcWrite(t *testing.T, s *storage.Service, key string, body []byte, ct string) (storage.Info, error) {
	t.Helper()
	return s.Local().Write(key, bytes.NewReader(body), ct, 1<<20)
}

// AC 4: pending objects older than 24 h are deleted with their rows on both backends; committed ones never; the
// job.storage.gc consumer records the counts on its jobs row.
func TestGCOnBothBackends(t *testing.T) {
	f := setup(t)
	s3, cfg := s3Backend(t)
	l := f.local(t)
	ctx := context.Background()
	start := f.now
	uploads := map[string]*storage.Presigned{}
	upload := func(svc *storage.Service, name string, commit bool) {
		t.Helper()
		p, err := svc.Presign(ctx, f.caller(f.driver, f.own, false), storage.PresignInput{Purpose: "chat_image", EntityID: uuid.NewString(),
			ContentType: "image/jpeg", SizeBytes: 3}, storage.PutOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if p.StorageBackend == storage.BackendLocal {
			_, err = l.Write(p.Key, strings.NewReader("abc"), "image/jpeg", 10)
		} else {
			err = s3.Put(ctx, storage.Object{Bucket: cfg.Bucket, Key: p.Key}, strings.NewReader("abc"), 3, "image/jpeg")
		}
		if err != nil {
			t.Fatal(err)
		}
		if commit {
			if _, err := f.commit(t, svc, storage.CommitInput{Key: p.Key, Purposes: []string{"chat_image"}, OwnerID: uuid.New(), UserID: f.driver}); err != nil {
				t.Fatal(err)
			}
		}
		uploads[name] = p
	}
	// The s3 backend under the bucket names its test buckets carry.
	localSvc := f.serviceOn(t, storage.BackendLocal, cfg, l, s3)
	s3Svc := f.serviceOn(t, storage.BackendS3, cfg, s3, l)
	upload(localSvc, "local pending old", false)
	upload(localSvc, "local committed old", true)
	upload(s3Svc, "s3 pending old", false)
	upload(s3Svc, "s3 committed old", true)
	f.now = start.Add(23 * time.Hour)
	upload(localSvc, "local pending recent", false)
	upload(s3Svc, "s3 pending recent", false)
	// An abandoned partial upload in the local .tmp/ goes too.
	tmp := f.localDir + "/.tmp/abandoned"
	if err := os.WriteFile(tmp, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(tmp, start, start); err != nil {
		t.Fatal(err)
	}

	f.now = start.Add(24*time.Hour + time.Minute)
	var job jobs.Job
	if err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		var err error
		job, err = jobs.Enqueue(ctx, tx, jobs.NewInput{Type: storage.JobTypeGC, Params: map[string]any{"scheduledFor": f.now}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cmd, _ := json.Marshal(jobs.Command{JobID: job.ID, Type: storage.JobTypeGC})
	reg := s3Svc.GCRegistration()
	if reg.Queue != "storage.gc" {
		t.Fatalf("queue %s", reg.Queue)
	}
	if err := reg.Handler(ctx, &mq.Delivery{Queue: reg.Queue, RoutingKey: storage.RouteGC, Body: cmd}); err != nil {
		t.Fatal(err)
	}
	var status string
	var result map[string]int
	if err := f.etl.QueryRow(ctx, `SELECT status, result FROM jobs WHERE id = $1`, job.ID).Scan(&status, &result); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || result["deleted"] != 2 || result["failed"] != 0 || result["tempRemoved"] != 1 {
		t.Fatalf("job %s %v", status, result)
	}
	// A duplicate of the finished command is acknowledged.
	if err := reg.Handler(ctx, &mq.Delivery{Queue: reg.Queue, RoutingKey: storage.RouteGC, Body: cmd}); err != nil {
		t.Fatal(err)
	}

	for name, p := range uploads {
		gone := strings.HasSuffix(name, "pending old")
		var n int
		if err := f.etl.QueryRow(ctx, `SELECT count(*) FROM file_objects WHERE object_key = $1`, p.Key).Scan(&n); err != nil {
			t.Fatal(err)
		}
		b, _ := localSvc.Backend(p.StorageBackend)
		_, statErr := b.Stat(ctx, storage.Object{Bucket: cfg.Bucket, Key: p.Key})
		if gone && (n != 0 || !errors.Is(statErr, storage.ErrObjectNotFound)) {
			t.Errorf("%s: row %d, object %v; want both deleted", name, n, statErr)
		}
		if !gone && (n != 1 || statErr != nil) {
			t.Errorf("%s: row %d, object %v; want both kept", name, n, statErr)
		}
	}
}

// Owner AC: an object uploaded under local still downloads after STORAGE_BACKEND switches to s3; new uploads go
// to s3; every row records its backend; a row without one cannot be written.
func TestReadsFollowTheRowsBackendAfterTheSwitch(t *testing.T) {
	f := setup(t)
	s3, cfg := s3Backend(t)
	ctx := context.Background()
	before := f.service(t, storage.BackendLocal, f.local(t))
	p, err := before.Presign(ctx, f.caller(f.driver, f.own, false), storage.PresignInput{Purpose: "chat_image", EntityID: uuid.NewString(),
		ContentType: "image/png", SizeBytes: 5}, storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svcWrite(t, before, p.Key, []byte("png!!"), "image/png"); err != nil {
		t.Fatal(err)
	}
	c, err := f.commit(t, before, storage.CommitInput{Key: p.Key, Purposes: []string{"chat_image"}, OwnerID: uuid.New(), UserID: f.driver})
	if err != nil {
		t.Fatal(err)
	}

	// The switch: STORAGE_BACKEND=s3, LOCAL_MEDIA_DIR kept.
	after := f.serviceOn(t, storage.BackendS3, cfg, s3, f.local(t))
	u, err := after.DownloadURL(ctx, f.caller(f.staff, f.own, true), p.Key)
	if err != nil || !strings.HasPrefix(u, "http://media.test/media/") {
		t.Fatalf("download URL of a local object after the switch: %s %v", u, err)
	}
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	ingress.Mount(app, ingress.Internal, after.Groups(storage.HTTPOptions{Auth: func(c fiber.Ctx) error { return c.Next() },
		Caller: func(fiber.Ctx) (storage.Caller, bool) { return storage.Caller{}, false }}), nil)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, strings.TrimPrefix(u, "http://media.test"), nil))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(got) != "png!!" || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("local object after the switch: %d %q", resp.StatusCode, got)
	}
	if photo, err := after.SignedURL(ctx, c.ID, 0); err != nil || !strings.HasPrefix(photo, "http://media.test/media/") {
		t.Fatalf("entity URL %s %v", photo, err)
	}
	n, err := after.Presign(ctx, f.caller(f.driver, f.own, false), storage.PresignInput{Purpose: "chat_image", EntityID: uuid.NewString(),
		ContentType: "image/png", SizeBytes: 5}, storage.PutOptions{})
	if err != nil || n.StorageBackend != storage.BackendS3 {
		t.Fatalf("new upload after the switch: %+v %v", n, err)
	}
	if _, backend := f.status(t, n.Key); backend != "s3" {
		t.Fatalf("new row backend %s", backend)
	}
	// A pending local upload re-signed after the switch stays local.
	pend, err := before.Presign(ctx, f.caller(f.driver, f.own, false), storage.PresignInput{Purpose: "chat_image", EntityID: uuid.NewString(),
		ContentType: "image/png", SizeBytes: 5}, storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rs, err := after.Presign(ctx, f.caller(f.driver, f.own, false), storage.PresignInput{Key: pend.Key}, storage.PutOptions{}); err != nil ||
		rs.StorageBackend != storage.BackendLocal {
		t.Fatalf("re-sign of a local pending upload: %+v %v", rs, err)
	}

	var missing int
	if err := f.etl.QueryRow(ctx, `SELECT count(*) FROM file_objects WHERE storage_backend NOT IN ('local','s3')`).Scan(&missing); err != nil || missing != 0 {
		t.Fatalf("rows without a backend: %d %v", missing, err)
	}
	_, err = f.etl.Exec(ctx, `INSERT INTO file_objects (bucket, object_key, purpose, status, committed_at) VALUES ('logitrack', 'x/y.jpg', 'chat_image', 'committed', now())`)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "23502" {
		t.Fatalf("insert without storage_backend: %v", err)
	}
	_, err = f.etl.Exec(ctx, `INSERT INTO file_objects (bucket, object_key, purpose, status, committed_at, storage_backend) VALUES ('logitrack', 'x/z.jpg', 'chat_image', 'committed', now(), 'gcs')`)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "23514" {
		t.Fatalf("insert with an unknown backend: %v", err)
	}
}

// AC 5 (evidence): a token is accepted after 365 simulated days (EVIDENCE_TOKEN_TTL_DAYS=0) and refused once
// revoked; the next forced LINE send mints a new one; trip tokens win over standby ones; a TTL applies when set.
func TestEvidenceVerifier(t *testing.T) {
	f := setup(t)
	svc := f.service(t, storage.BackendLocal, f.local(t))
	ctx := context.Background()
	issued := f.now
	tok, err := storage.NewEvidenceToken(issued)
	if err != nil {
		t.Fatal(err)
	}
	var trip, standby uuid.UUID
	if err := f.etl.QueryRow(ctx, `INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, evidence_token)
		VALUES ($1, 'form', 'TR-EVID-1', 'delivered', 'first_mile', $2) RETURNING id`, f.own, tok).Scan(&trip); err != nil {
		t.Fatal(err)
	}
	const legacy = "Q2xhdWRlQ29kZSEh" // 12 bytes, the shape lineNotify.ts mints
	if err := f.etl.QueryRow(ctx, `INSERT INTO standby_records (tenant_id, tenant_source, evidence_token) VALUES ($1, 'form', $2)
		RETURNING id`, f.own, legacy).Scan(&standby); err != nil {
		t.Fatal(err)
	}

	f.now = issued.Add(365 * 24 * time.Hour)
	if got, err := svc.ResolveEvidence(ctx, tok); err != nil || got.Kind != storage.EvidenceTrip || got.ID != trip || got.TenantID != f.own {
		t.Fatalf("trip token after 365 days: %+v %v", got, err)
	}
	if got, err := svc.ResolveEvidence(ctx, legacy); err != nil || got.Kind != storage.EvidenceStandby || got.ID != standby {
		t.Fatalf("legacy standby token: %+v %v", got, err)
	}
	if _, err := svc.ResolveEvidence(ctx, ""); !errors.Is(err, storage.ErrEvidenceEmpty) {
		t.Fatalf("empty token: %v", err)
	}
	if _, err := svc.ResolveEvidence(ctx, "unknown-token"); !errors.Is(err, storage.ErrEvidenceNotFound) {
		t.Fatalf("unknown token: %v", err)
	}

	// Revoke (POST /v1/trips/{id}/evidence/revoke): refused from then on; revoking twice keeps the first instant.
	var revoked, again bool
	if err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		var err error
		if revoked, err = storage.RevokeEvidence(ctx, tx, storage.EvidenceTrip, trip, f.now); err != nil {
			return err
		}
		again, err = storage.RevokeEvidence(ctx, tx, storage.EvidenceTrip, trip, f.now.Add(time.Hour))
		return err
	}); err != nil || !revoked || again {
		t.Fatalf("revoke %v %v %v", revoked, again, err)
	}
	if _, err := svc.ResolveEvidence(ctx, tok); !errors.Is(err, storage.ErrEvidenceNotFound) {
		t.Fatalf("revoked token: %v", err)
	}
	// The next automatic send keeps the gallery closed; the forced one mints and stores a new token.
	var cur string
	var revokedAt *time.Time
	if err := f.etl.QueryRow(ctx, `SELECT evidence_token, evidence_token_revoked_at FROM trip_records WHERE id = $1`, trip).Scan(&cur, &revokedAt); err != nil {
		t.Fatal(err)
	}
	if next, mint, _ := storage.EvidenceTokenForSend(&cur, revokedAt, 4, false, f.now); next != "" || mint {
		t.Fatal("an automatic send re-opened a revoked gallery")
	}
	next, mint, err := storage.EvidenceTokenForSend(&cur, revokedAt, 4, true, f.now)
	if err != nil || !mint {
		t.Fatal("a forced send mints")
	}
	if err := db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		return storage.SetEvidenceToken(ctx, tx, storage.EvidenceTrip, trip, next)
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.ResolveEvidence(ctx, next); err != nil || got.ID != trip {
		t.Fatalf("new token: %+v %v", got, err)
	}
	if _, err := svc.ResolveEvidence(ctx, tok); !errors.Is(err, storage.ErrEvidenceNotFound) {
		t.Fatalf("old token after re-mint: %v", err)
	}

	// With EVIDENCE_TOKEN_TTL_DAYS=30 a year-old token expires.
	ttl, err := storage.New(f.pool, storage.Config{Active: storage.BackendLocal, Bucket: "logitrack", PublicBucket: "logitrack-public",
		EvidenceTokenTTLDays: 30}, zerolog.Nop(), f.local(t))
	if err != nil {
		t.Fatal(err)
	}
	ttl.SetClock(func() time.Time { return f.now })
	if _, err := ttl.ResolveEvidence(ctx, legacy); !errors.Is(err, storage.ErrEvidenceNotFound) {
		t.Fatalf("expired legacy token with a TTL: %v", err)
	}
	if _, err := ttl.ResolveEvidence(ctx, next); err != nil {
		t.Fatalf("fresh token with a TTL: %v", err)
	}
}
