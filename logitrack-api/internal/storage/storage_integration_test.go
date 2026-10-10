//go:build integration

// Acceptance tests of issue T11 on postgres:18-alpine and the compose MinIO image: presign -> PUT -> commit on
// both backends (the browser's CORS preflight from http://localhost:3000 included), the 422 commit failures that
// leave the row pending, URLs signed for S3_PRESIGN_ENDPOINT, presigned GET expiry, storage.gc on both backends,
// reads that follow each row's backend after STORAGE_BACKEND changes, and the evidence token verifier.
package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate"
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
	runner     *migrate.Runner
}

func setup(t *testing.T) *fixture {
	t.Helper()
	return setupAt(t, func(ctx context.Context, r *migrate.Runner) error {
		_, err := r.Up(ctx)
		return err
	})
}

// setupAt migrates a fresh database with schema (the whole chain, or a production stop) and seeds the tenants.
func setupAt(t *testing.T, schema func(context.Context, *migrate.Runner) error) *fixture {
	t.Helper()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	if err := schema(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	f := &fixture{pool: d.Pool(t, db.RoleApp), etl: d.Pool(t, db.RoleETL), now: time.Now().UTC().Truncate(time.Millisecond),
		localDir: t.TempDir(), runner: r}
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
	pre.Header.Set("Access-Control-Request-Headers", "content-type,if-none-match")
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
	wrong.Headers = map[string]string{"Content-Type": "text/html", "If-None-Match": "*"}
	if r := put(t, &wrong, "", body, ""); r.StatusCode != http.StatusForbidden {
		t.Fatalf("PUT with an unsigned content type: %d", r.StatusCode)
	}
	// The PUT can only create the object: the same URL again (a retry after a lost response, or a swap) is 412.
	if r := put(t, p, "", bytes.Repeat([]byte{0xdd}, len(body)), ""); r.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("second PUT on the same URL: %d", r.StatusCode)
	}
	// The body is bound to the declared size: longer or shorter bodies fail the signature and store nothing.
	sized, err := svc.Presign(ctx, driver, storage.PresignInput{Purpose: "trip_photo", EntityID: trip.String(), Variant: "sized",
		ContentType: "image/jpeg", SizeBytes: 10}, storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"oversize": bytes.Repeat([]byte{0xff}, 8<<20), "short": []byte("abc")} {
		if r := put(t, sized, "", b, ""); r.StatusCode != http.StatusForbidden {
			t.Errorf("%s body: %d", name, r.StatusCode)
		}
	}
	if _, err := s3.Stat(ctx, storage.Object{Bucket: cfg.Bucket, Key: sized.Key}); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatalf("a refused body was stored: %v", err)
	}

	in := storage.CommitInput{Key: p.Key, Field: "photoKey", Purposes: []string{"trip_photo"}, OwnerID: trip, UserID: f.driver, TenantID: &f.own}
	// Wrong size: declared 3 bytes, an object with more written out of band (the presigned PUT cannot).
	short, err := svc.Presign(ctx, driver, storage.PresignInput{Purpose: "trip_photo", EntityID: trip.String(), Variant: "closing",
		ContentType: "image/jpeg", SizeBytes: 3}, storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s3.Put(ctx, storage.Object{Bucket: cfg.Bucket, Key: short.Key}, bytes.NewReader(body), int64(len(body)), "image/jpeg"); err != nil {
		t.Fatal(err)
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
	// The upload URL is still valid after the commit, but it cannot replace the committed object.
	if r := put(t, p, "", bytes.Repeat([]byte{0xee}, len(body)), ""); r.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("PUT over a committed object: %d", r.StatusCode)
	}
	if info, err := s3.Stat(ctx, storage.Object{Bucket: cfg.Bucket, Key: p.Key}); err != nil || info.Size != int64(len(body)) {
		t.Fatalf("committed object after a second PUT: %+v %v", info, err)
	}

	// Download: GET /v1/files resolves on S3_PRESIGN_ENDPOINT; a URL dies after its TTL.
	u, err := svc.DownloadURL(ctx, f.caller(f.staff, f.own, true), p.Key)
	if err != nil {
		t.Fatal(err)
	}
	if gu, _ := url.Parse(u); gu.Host != pu.Host {
		t.Fatalf("download URL %s not on the presign host", u)
	}
	// MinIO checks the expiry against its own clock: a 5 s URL and a poll for the 403 tolerate a container clock
	// a few seconds ahead of or behind the test process (Docker Desktop after sleep).
	short2, err := svc.SignedURL(ctx, c.ID, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if mustQueryOf(t, short2).Get("X-Amz-Expires") != "5" {
		t.Fatalf("short URL %s", short2)
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
	deadline := time.Now().Add(20 * time.Second)
	for {
		r, err := http.Get(short2)
		if err == nil {
			_ = r.Body.Close()
			if r.StatusCode == http.StatusForbidden {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("presigned GET still valid long after its TTL: %v %v", r, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func mustQueryOf(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
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
	// An owner-kind authorizer is authoritative ("a file is readable iff its referencing row is"): it widens reads
	// to staff of an unrelated tenant here, and narrows them for the owner's own staff.
	svc.RegisterAuthorizer(storage.OwnerChat, func(_ context.Context, c storage.Caller, file storage.FileRef) (bool, error) {
		return c.UserID == f.otherStaff && file.Purpose == "chat_image" && file.Key == p.Key, nil
	})
	if _, err := svc.DownloadURL(ctx, f.caller(f.otherStaff, f.other, true), p.Key); err != nil {
		t.Fatalf("authorizer: %v", err)
	}
	if _, err := svc.DownloadURL(ctx, f.caller(f.staff, f.own, true), p.Key); err == nil {
		t.Fatal("a registered authorizer did not narrow the staff rule")
	}
	if _, err := svc.DownloadURL(ctx, driver, p.Key); err != nil {
		t.Fatalf("the uploader is decided before the authorizer: %v", err)
	}
	if _, err := svc.DownloadURL(ctx, driver, "nope/missing.jpg"); err == nil {
		t.Fatal("unknown key")
	}
}

// Driver ID cards and licences (main spec §9.2, §9.5; Appendix C §C.2 row 15, §C.9 row 11): staff in reach need
// drivers:view_pii, the URL lives 5 minutes; an OwnerDriver authorizer decides instead of the staff rule.
func TestDriverPIIReads(t *testing.T) {
	f := setup(t)
	svc := f.service(t, storage.BackendLocal, f.local(t))
	ctx := context.Background()
	driverID := uuid.New()
	pii := func(c authz.Cap) bool { return c == authz.DriversViewPII }
	manager := storage.Caller{UserID: uuid.New(), TenantID: &f.own, Staff: true, Can: pii}
	operator := storage.Caller{UserID: uuid.New(), TenantID: &f.own, Staff: true}
	contractor := storage.Caller{UserID: uuid.New(), TenantID: &f.own, Staff: true}
	expires := func(t *testing.T, u string) int64 {
		t.Helper()
		e, err := strconv.ParseInt(mustQueryOf(t, u).Get(storage.QueryExpires), 10, 64)
		if err != nil {
			t.Fatalf("URL %s: %v", u, err)
		}
		return e - f.now.Unix()
	}
	var keys []string
	var ids []uuid.UUID
	for _, purpose := range []string{"driver_id_card", "driver_license"} {
		up := f.caller(f.staff, f.own, true)
		p, err := svc.Presign(ctx, up, storage.PresignInput{Purpose: purpose, EntityID: driverID.String(), ContentType: "image/jpeg", SizeBytes: 2},
			storage.PutOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.svcWrite(t, svc, p.Key, []byte("id"), "image/jpeg"); err != nil {
			t.Fatal(err)
		}
		c, err := f.commit(t, svc, storage.CommitInput{Key: p.Key, Purposes: []string{purpose}, OwnerID: driverID, UserID: f.staff, TenantID: &f.own})
		if err != nil {
			t.Fatal(err)
		}
		keys, ids = append(keys, p.Key), append(ids, c.ID)
	}
	// A carrier's driver document, read by the own fleet's staff (contractor reach).
	sp, err := svc.Presign(ctx, f.caller(f.subStaff, f.sub, true), storage.PresignInput{Purpose: "driver_id_card", EntityID: uuid.NewString(),
		ContentType: "image/jpeg", SizeBytes: 2}, storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svcWrite(t, svc, sp.Key, []byte("id"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.commit(t, svc, storage.CommitInput{Key: sp.Key, Purposes: []string{"driver_id_card"}, OwnerID: uuid.New(), UserID: f.subStaff, TenantID: &f.sub}); err != nil {
		t.Fatal(err)
	}

	for _, key := range keys {
		for name, tc := range map[string]struct {
			c  storage.Caller
			ok bool
		}{
			"uploader":                   {f.caller(f.staff, f.own, true), true},
			"manager (drivers:view_pii)": {manager, true},
			"operator":                   {operator, false},
			"read-only bypass":           {storage.Caller{UserID: uuid.New(), ReadAll: true}, true},
		} {
			u, err := svc.DownloadURL(ctx, tc.c, key)
			if (err == nil) != tc.ok {
				t.Errorf("%s reads %s: %v", name, key, err)
			}
			if err == nil && name != "uploader" && expires(t, u) != 300 {
				t.Errorf("%s: URL lives %d s, want 300", name, expires(t, u))
			}
		}
	}
	if _, err := svc.DownloadURL(ctx, contractor, sp.Key); err == nil {
		t.Fatal("contractor staff without drivers:view_pii read a carrier's ID card")
	}
	contractor.Can = pii
	if _, err := svc.DownloadURL(ctx, contractor, sp.Key); err != nil {
		t.Fatalf("contractor staff with drivers:view_pii: %v", err)
	}
	// Entity payloads (GET /v1/drivers/{id}/documents/{kind}) get 5 minutes too, whatever they ask for.
	if u, err := svc.SignedURL(ctx, ids[0], 0); err != nil || expires(t, u) != 300 {
		t.Fatalf("entity URL of an ID card: %s %v", u, err)
	}

	// The drivers domain's authorizer decides: it can refuse a capability holder and grant the driver.
	asked := 0
	svc.RegisterAuthorizer(storage.OwnerDriver, func(_ context.Context, c storage.Caller, file storage.FileRef) (bool, error) {
		asked++
		return file.OwnerID == driverID && c.UserID == operator.UserID, nil
	})
	if _, err := svc.DownloadURL(ctx, manager, keys[0]); err == nil {
		t.Fatal("a denying authorizer did not narrow the read")
	}
	u, err := svc.DownloadURL(ctx, operator, keys[1])
	if err != nil || expires(t, u) != 300 {
		t.Fatalf("a granting authorizer: %s %v", u, err)
	}
	if asked != 2 {
		t.Fatalf("the authorizer was asked %d times, want 2", asked)
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

// Production P0 (R59, R88): the database stops at up-to 9 plus apply 11 while 0010 waits for the P1 sign-off. The
// storage service works there (presign, upload, commit, download, storage.gc), and the P1 runbook's up later
// applies 0010 underneath it.
func TestStorageOnTheProductionP0Schema(t *testing.T) {
	f := setupAt(t, func(ctx context.Context, r *migrate.Runner) error {
		if _, err := r.UpTo(ctx, 9); err != nil {
			return err
		}
		_, err := r.Apply(ctx, 11)
		return err
	})
	ctx := context.Background()
	st, err := f.runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st[9].Applied || !st[10].Applied {
		t.Fatalf("schema: 0010 %+v, 0011 %+v", st[9], st[10])
	}
	svc := f.service(t, storage.BackendLocal, f.local(t))
	upload := func() string {
		t.Helper()
		p, err := svc.Presign(ctx, f.caller(f.driver, f.own, false), storage.PresignInput{Purpose: "chat_image", EntityID: uuid.NewString(),
			ContentType: "image/jpeg", SizeBytes: 3}, storage.PutOptions{})
		if err != nil {
			t.Fatalf("presign at up-to 9 + 11: %v", err)
		}
		if _, err := f.svcWrite(t, svc, p.Key, []byte("abc"), "image/jpeg"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.commit(t, svc, storage.CommitInput{Key: p.Key, Purposes: []string{"chat_image"}, OwnerID: uuid.New(), UserID: f.driver}); err != nil {
			t.Fatalf("commit at up-to 9 + 11: %v", err)
		}
		if _, err := svc.DownloadURL(ctx, f.caller(f.staff, f.own, true), p.Key); err != nil {
			t.Fatalf("GET /v1/files at up-to 9 + 11: %v", err)
		}
		return p.Key
	}
	key := upload()
	if _, err := svc.GC(ctx); err != nil {
		t.Fatalf("storage.gc at up-to 9 + 11: %v", err)
	}
	// P1 runbook: up applies the held 0010 (out of order); storage keeps working.
	res, err := f.runner.Up(ctx)
	if err != nil || len(res) == 0 || res[0].Name != "0010_d5_unique_constraints.sql" {
		t.Fatalf("up after apply 11: %+v %v", res, err)
	}
	if _, err := svc.DownloadURL(ctx, f.caller(f.staff, f.own, true), key); err != nil {
		t.Fatal(err)
	}
	upload()
}

// serveStorage runs the storage routes on a real listener (a blocked request must not hit app.Test's timeout).
func serveStorage(t *testing.T, svc *storage.Service) string {
	t.Helper()
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	ingress.Mount(app, ingress.Internal, svc.Groups(storage.HTTPOptions{Auth: func(c fiber.Ctx) error { return c.Next() },
		Caller: func(fiber.Ctx) (storage.Caller, bool) { return storage.Caller{}, false }}), nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return "http://" + ln.Addr().String()
}

// putResult is a PUT made from a goroutine (no t.Fatal there).
type putResult struct {
	status int
	body   string
	err    error
}

func putAsync(p *storage.Presigned, base string, body []byte) putResult {
	req, err := http.NewRequest(p.Method, base+p.URL, bytes.NewReader(body))
	if err != nil {
		return putResult{err: err}
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return putResult{err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return putResult{status: resp.StatusCode, body: string(b)}
}

// A still-valid local upload URL cannot replace bytes a commit verified: the upload is renamed into place only
// under the pending row's lock, which the entity transaction's commit holds until it ends (review finding of PR
// #109). The second PUT waits on the lock, then finds the row committed: 409 not_pending, the disk untouched.
func TestLocalUploadCannotReplaceBytesDuringACommit(t *testing.T) {
	f := setup(t)
	svc := f.service(t, storage.BackendLocal, f.local(t))
	base := serveStorage(t, svc)
	ctx := context.Background()
	sum := sha256.Sum256([]byte("abc"))
	p, err := svc.Presign(ctx, f.caller(f.driver, f.own, false), storage.PresignInput{Purpose: "chat_image", EntityID: uuid.NewString(),
		ContentType: "image/jpeg", SizeBytes: 3, SHA256: hex.EncodeToString(sum[:])}, storage.PutOptions{APIPath: true})
	if err != nil {
		t.Fatal(err)
	}
	if r := putAsync(p, base, []byte("abc")); r.err != nil || r.status != http.StatusOK {
		t.Fatalf("first PUT: %+v", r)
	}
	done := make(chan putResult, 1)
	err = db.WithSystem(ctx, f.pool, nil, func(tx pgx.Tx) error {
		if _, err := svc.Commit(ctx, tx, storage.CommitInput{Key: p.Key, Purposes: []string{"chat_image"}, OwnerID: uuid.New(), UserID: f.driver}); err != nil {
			return err
		}
		// Same size (the signature binds it), other bytes, while the entity transaction is still open.
		go func() { done <- putAsync(p, base, []byte("xyz")) }()
		deadline := time.Now().Add(15 * time.Second)
		for {
			var waiting int
			if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
				WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
				return err
			}
			if waiting > 0 {
				return nil // the PUT waits on the row lock: commit now
			}
			select {
			case r := <-done:
				return fmt.Errorf("the PUT finished while the commit held the row: %+v", r)
			default:
			}
			if time.Now().After(deadline) {
				return errors.New("the PUT never waited on the row lock")
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.status != http.StatusConflict || !strings.Contains(r.body, "not_pending") {
		t.Fatalf("PUT during the commit: %+v", r)
	}
	got, err := os.ReadFile(f.localDir + "/private/" + p.Key)
	if err != nil || string(got) != "abc" {
		t.Fatalf("committed bytes on disk: %q %v", got, err)
	}
	if info, err := svc.Local().Stat(ctx, storage.Object{Key: p.Key}); err != nil || info.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sidecar after the refused PUT: %+v %v", info, err)
	}

	// Without an artificial hold: PUTs racing commits never leave a committed row whose bytes are not the ones it
	// verified (the declared sha256 of "abc").
	for i := range 20 {
		p, err := svc.Presign(ctx, f.caller(f.driver, f.own, false), storage.PresignInput{Purpose: "chat_image", EntityID: uuid.NewString(),
			ContentType: "image/jpeg", SizeBytes: 3, SHA256: hex.EncodeToString(sum[:])}, storage.PutOptions{APIPath: true})
		if err != nil {
			t.Fatal(err)
		}
		if r := putAsync(p, base, []byte("abc")); r.status != http.StatusOK {
			t.Fatalf("round %d first PUT: %+v", i, r)
		}
		results := make(chan putResult, 3)
		for range 3 {
			go func() { results <- putAsync(p, base, []byte("xyz")) }()
		}
		_, commitErr := f.commit(t, svc, storage.CommitInput{Key: p.Key, Purposes: []string{"chat_image"}, OwnerID: uuid.New(), UserID: f.driver})
		for range 3 {
			<-results
		}
		got, _ := os.ReadFile(f.localDir + "/private/" + p.Key)
		if st, _ := f.status(t, p.Key); commitErr == nil && (st != "committed" || string(got) != "abc") {
			t.Fatalf("round %d: committed row (%s) over bytes %q", i, st, got)
		}
	}
}

// The bootstrap owns only the anonymous grants and the expire-cache rule (§9.1, §9.10): an operator's deny
// statements and extra lifecycle rules survive an api restart, an anonymous grant on the private bucket does not.
func TestBootstrapKeepsOperatorBucketSettings(t *testing.T) {
	s3, cfg := s3Backend(t)
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := minio.New(u.Host, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""), Region: cfg.Region,
		BucketLookup: minio.BucketLookupPath})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hold := func(bucket string) string {
		return `{"Sid":"LegalHold","Effect":"Deny","Principal":{"AWS":["*"]},"Action":["s3:DeleteObject"],` +
			`"Resource":["arn:aws:s3:::` + bucket + `/legal-hold/*"]}`
	}
	anon := `{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::` + cfg.Bucket + `/*"]}`
	if err := admin.SetBucketPolicy(ctx, cfg.Bucket, `{"Version":"2012-10-17","Statement":[`+hold(cfg.Bucket)+`,`+anon+`]}`); err != nil {
		t.Fatal(err)
	}
	pub, err := admin.GetBucketPolicy(ctx, cfg.PublicBucket)
	if err != nil {
		t.Fatal(err)
	}
	var pd map[string]any
	if err := json.Unmarshal([]byte(pub), &pd); err != nil {
		t.Fatal(err)
	}
	var holdStmt any
	_ = json.Unmarshal([]byte(hold(cfg.PublicBucket)), &holdStmt)
	pd["Statement"] = append(pd["Statement"].([]any), holdStmt)
	pubWithHold, _ := json.Marshal(pd)
	if err := admin.SetBucketPolicy(ctx, cfg.PublicBucket, string(pubWithHold)); err != nil {
		t.Fatal(err)
	}
	lc, err := admin.GetBucketLifecycle(ctx, cfg.Bucket)
	if err != nil {
		t.Fatal(err)
	}
	lc.Rules = append(lc.Rules, lifecycle.Rule{ID: "expire-etl", Status: "Enabled", RuleFilter: lifecycle.Filter{Prefix: "etl/dumps/"},
		Expiration: lifecycle.Expiration{Days: 90}})
	if err := admin.SetBucketLifecycle(ctx, cfg.Bucket, lc); err != nil {
		t.Fatal(err)
	}

	for range 2 { // an api restart, then another one that has nothing to change
		if err := s3.Bootstrap(ctx, zerolog.Nop()); err != nil {
			t.Fatal(err)
		}
		priv, err := admin.GetBucketPolicy(ctx, cfg.Bucket)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(priv, "LegalHold") || strings.Contains(priv, `"Allow"`) {
			t.Fatalf("private policy after bootstrap: %s", priv)
		}
		pub, err := admin.GetBucketPolicy(ctx, cfg.PublicBucket)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(pub, "LegalHold") || !strings.Contains(pub, cfg.PublicBucket+"/app_releases/*") {
			t.Fatalf("public policy after bootstrap: %s", pub)
		}
		lc, err := admin.GetBucketLifecycle(ctx, cfg.Bucket)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, r := range lc.Rules {
			ids[r.ID] = true
		}
		if !ids["expire-cache"] || !ids["expire-etl"] || len(lc.Rules) != 2 {
			t.Fatalf("lifecycle after bootstrap: %+v", lc.Rules)
		}
	}
	// Anonymous reads: app_releases/ of the public bucket only.
	if err := s3.Put(ctx, storage.Object{Bucket: cfg.PublicBucket, Key: "app_releases/dev/a.apk"}, strings.NewReader("apk"), 3,
		"application/vnd.android.package-archive"); err != nil {
		t.Fatal(err)
	}
	if r, err := http.Get(cfg.Endpoint + "/" + cfg.PublicBucket + "/app_releases/dev/a.apk"); err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("anonymous APK GET: %v %v", r, err)
	} else {
		_ = r.Body.Close()
	}
	if err := s3.Put(ctx, storage.Object{Bucket: cfg.Bucket, Key: "chats/x/a.jpg"}, strings.NewReader("x"), 1, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if r, err := http.Get(cfg.Endpoint + "/" + cfg.Bucket + "/chats/x/a.jpg"); err != nil || r.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous private GET: %v %v", r, err)
	} else {
		_ = r.Body.Close()
	}
}
