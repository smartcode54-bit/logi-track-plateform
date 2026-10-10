//go:build integration

// The storage routes as cmd/api mounts them (T11) with STORAGE_BACKEND=local, end to end through both listeners:
// presign (web, an API path through the BFF) -> PUT with the upload route's own body limit -> commit through
// PATCH /v1/me photoKey -> GET /v1/me photoUrl and GET /v1/files 302 to a signed /media URL on the public listener;
// tampered signatures 403, directory paths 404, a committed key cannot be overwritten, and every other route keeps
// the 4 MiB limit.
package app_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/asynctest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

// raw sends body with the given content type and returns status, headers and body.
func raw(t *testing.T, method, url, contentType string, body []byte, bearer string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

// announce sends only the request head with Content-Length n and returns the status the server answers before
// any body byte arrives: an over-limit body is refused from its headers (fasthttp HeaderReceived), unread.
func announce(t *testing.T, method, rawURL string, n int) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(c, "%s %s HTTP/1.1\r\nHost: %s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n",
		method, u.RequestURI(), u.Host, n); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLocalStorageEndToEnd(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)
	rdb := asynctest.SharedRedis(t).Client(t)
	ks, err := cache.NewKeyspace("local")
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
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	// STORAGE_BACKEND=local; LOCAL_MEDIA_PUBLIC_BASE_URL reaches /media on the public listener.
	publicAddr := freeAddr(t)
	dir := t.TempDir()
	local, err := storage.NewLocal(storage.LocalConfig{Dir: dir, PublicBaseURL: "http://" + publicAddr + "/media",
		SigningKey: bytes.Repeat([]byte("e"), 40), APIUploadPath: storage.LocalUploadPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	const uploadMax = 6 << 20
	st, err := storage.New(pool, storage.Config{Active: storage.BackendLocal, Bucket: "logitrack", PublicBucket: "logitrack-public",
		PresignPutTTL: 15 * time.Minute, PresignGetTTL: time.Hour, UploadMaxBytes: uploadMax}, zerolog.Nop(), local)
	if err != nil {
		t.Fatal(err)
	}
	limiter := ratelimit.New(rdb, ks, zerolog.Nop())
	svc, err := auth.New(auth.Config{RefreshTTLWeb: 168 * time.Hour, RefreshTTLMobile: 2160 * time.Hour,
		PasswordResetTTL: 30 * time.Minute, LoginIP: ratelimit.Limit{Count: 1000, Window: time.Minute}},
		auth.Deps{Pool: pool, Store: auth.NewStore(rdb, ks.Prefix()), Limiter: limiter, Keys: token.New(priv, "http://localhost", "test", 15*time.Minute),
			Hasher: hasher, Policy: policy, Log: zerolog.Nop(), Files: st})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	deps := app.APIDeps{Auth: svc, Jobs: jobs.NewService(pool, jobs.NewRedisLocker(rdb, ks)), Storage: st, Limiter: limiter, RateLimitEnabled: true}
	cfg := &app.APIConfig{
		Common:       app.Common{AppEnv: "local", LogLevel: "error", LogFormat: "json", OTelSamplerArg: 1},
		Runtime:      app.Runtime{MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second},
		StorageAPI:   app.StorageAPI{UploadMaxBytes: uploadMax},
		InternalAddr: "127.0.0.1:0", PublicAddr: publicAddr, PublicRouteGroups: ingress.PublicPrefixes,
	}
	groups := append(svc.Groups(), app.JobGroups(deps)...)
	a, err := app.NewAPI(cfg, zerolog.Nop(), append(groups, app.StorageGroups(deps)...)...)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CheckRoutes(); err != nil {
		t.Fatal(err)
	}
	if err := a.Listen(); err != nil {
		t.Fatal(err)
	}
	sctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = a.Serve(sctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	in, pub, _ := a.Addrs()
	internal, public := "http://"+in, "http://"+pub

	etl := d.Pool(t, db.RoleETL)
	hash, err := hasher.Hash(ctx, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	var tenant, alice string
	if err := etl.QueryRow(ctx, `INSERT INTO tenants (kind, name_th, name_en) VALUES ('own_fleet', 'Own', 'Own') RETURNING id::text`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if err := etl.QueryRow(ctx, `INSERT INTO users (email, email_verified, display_name, password_hash) VALUES ('alice@logitrack.test', true, 'Alice', $1)
		RETURNING id::text`, hash).Scan(&alice); err != nil {
		t.Fatal(err)
	}
	if _, err := etl.Exec(ctx, `INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'operator')`, alice, tenant); err != nil {
		t.Fatal(err)
	}
	r := do(t, http.MethodPost, internal+"/v1/auth/login", "", map[string]any{"email": "alice@logitrack.test", "password": testPassword, "platform": "web"})
	at, _ := r.body["data"].(map[string]any)["accessToken"].(string)
	if at == "" {
		t.Fatalf("login: %d %v", r.status, r.body)
	}

	// A photo larger than the 4 MiB API limit: the upload route has its own (UPLOAD_MAX_BYTES).
	photo := bytes.Repeat([]byte{0xff, 0xd8, 0xff, 0xe0}, (5<<20)/4)
	if r := do(t, http.MethodPost, internal+"/v1/uploads/presign", "", map[string]any{"purpose": "user_photo"}); r.status != http.StatusUnauthorized {
		t.Fatalf("presign without a bearer: %d", r.status)
	}
	if r := do(t, http.MethodPost, public+"/v1/uploads/presign", at, map[string]any{"purpose": "user_photo"}); r.status != http.StatusNotFound {
		t.Fatalf("presign on the public listener: %d", r.status)
	}
	r = do(t, http.MethodPost, internal+"/v1/uploads/presign", at, map[string]any{"purpose": "user_photo", "contentType": "image/jpeg", "sizeBytes": len(photo)})
	data, _ := r.body["data"].(map[string]any)
	key, _ := data["key"].(string)
	upURL, _ := data["url"].(string)
	if r.status != http.StatusOK || !strings.HasPrefix(upURL, "/v1/uploads/local/users/"+alice+"/photo-") || data["method"] != "PUT" ||
		data["storageBackend"] != "local" || data["headers"].(map[string]any)["Content-Type"] != "image/jpeg" {
		t.Fatalf("presign: %d %v", r.status, r.body)
	}
	var backend string
	if err := etl.QueryRow(ctx, `SELECT storage_backend FROM file_objects WHERE object_key = $1 AND status = 'pending'`, key).Scan(&backend); err != nil || backend != "local" {
		t.Fatalf("pending row: %q %v", backend, err)
	}

	// The upload route: wrong content type and a tampered signature are 403; the public listener has no
	// /v1/uploads route; then the web's PUT (through the BFF) lands.
	if s, _, _ := raw(t, http.MethodPut, internal+upURL, "text/html", photo, ""); s != http.StatusForbidden {
		t.Fatalf("PUT with another content type: %d", s)
	}
	if s, _, _ := raw(t, http.MethodPut, internal+strings.Replace(upURL, "X-LT-Signature=", "X-LT-Signature=x", 1), "image/jpeg", photo, ""); s != http.StatusForbidden {
		t.Fatalf("PUT with a tampered signature: %d", s)
	}
	// The public listener has no /v1/uploads route: 404, and a large body there is refused before it is read.
	if s, _, _ := raw(t, http.MethodPut, public+upURL, "image/jpeg", []byte("x"), ""); s != http.StatusNotFound {
		t.Fatalf("PUT /v1/uploads/local on the public listener: %d", s)
	}
	if s := announce(t, http.MethodPut, public+upURL, len(photo)); s != http.StatusRequestEntityTooLarge {
		t.Fatalf("5 MiB PUT outside an upload path: %d", s)
	}
	if s, h, b := raw(t, http.MethodPut, internal+upURL, "image/jpeg", photo, ""); s != http.StatusOK || h.Get("ETag") == "" {
		t.Fatalf("PUT: %d %s", s, b)
	}
	fi, err := os.Stat(filepath.Join(dir, "private", filepath.FromSlash(key)))
	if err != nil || fi.Size() != int64(len(photo)) || fi.Mode().Perm() != 0o640 {
		t.Fatalf("stored file: %v %v", fi, err)
	}
	// Every other route keeps the 4 MiB limit, the upload routes UPLOAD_MAX_BYTES.
	if s := announce(t, http.MethodPost, internal+"/v1/uploads/presign", len(photo)); s != http.StatusRequestEntityTooLarge {
		t.Fatalf("5 MiB on presign: %d", s)
	}
	if s := announce(t, http.MethodPut, internal+upURL, uploadMax+1); s != http.StatusRequestEntityTooLarge {
		t.Fatalf("over UPLOAD_MAX_BYTES on the upload route: %d", s)
	}

	// Commit in the entity transaction (PATCH /v1/me), then signed GETs on the public listener.
	r = do(t, http.MethodPatch, internal+"/v1/me", at, map[string]any{"photoKey": key})
	photoURL, _ := r.body["data"].(map[string]any)["photoUrl"].(string)
	if r.status != http.StatusOK || !strings.HasPrefix(photoURL, public+"/media/users/") {
		t.Fatalf("PATCH /v1/me photoKey: %d %v", r.status, r.body)
	}
	if err := etl.QueryRow(ctx, `SELECT f.storage_backend FROM users u JOIN file_objects f ON f.id = u.photo_file_id
		WHERE u.id = $1 AND f.status = 'committed' AND f.owner_kind = 'user'`, alice).Scan(&backend); err != nil || backend != "local" {
		t.Fatalf("committed photo: %q %v", backend, err)
	}
	if s, h, b := raw(t, http.MethodGet, photoURL, "", nil, ""); s != http.StatusOK || !bytes.Equal(b, photo) || h.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("GET photoUrl: %d %d bytes %v", s, len(b), h)
	}
	if s, _, _ := raw(t, http.MethodGet, strings.Replace(photoURL, "X-LT-Signature=", "X-LT-Signature=x", 1), "", nil, ""); s != http.StatusForbidden {
		t.Fatalf("tampered GET: %d", s)
	}
	if s, _, _ := raw(t, http.MethodGet, strings.Split(photoURL, "?")[0], "", nil, ""); s != http.StatusForbidden {
		t.Fatalf("unsigned GET: %d", s)
	}
	for _, p := range []string{"/media/", "/media/users/", "/media/users/" + alice + "/"} {
		if s, _, b := raw(t, http.MethodGet, public+p, "", nil, ""); s != http.StatusNotFound || strings.Contains(string(b), "photo-") {
			t.Fatalf("directory %s: %d %s", p, s, b)
		}
	}
	// GET /v1/files?key= (the web, through the BFF) redirects to the signed URL.
	s, h, _ := raw(t, http.MethodGet, internal+"/v1/files?key="+key, "", nil, at)
	if loc := h.Get("Location"); s != http.StatusFound || !strings.HasPrefix(loc, public+"/media/users/") || h.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("GET /v1/files: %d %v", s, h)
	} else if s, _, b := raw(t, http.MethodGet, loc, "", nil, ""); s != http.StatusOK || len(b) != len(photo) {
		t.Fatalf("follow /v1/files: %d", s)
	}
	if s, _, _ := raw(t, http.MethodGet, internal+"/v1/files?key=users/nobody/x.jpg", "", nil, at); s != http.StatusNotFound {
		t.Fatalf("unknown key: %d", s)
	}
	// The committed object cannot be overwritten through its still-valid upload URL.
	if s, _, b := raw(t, http.MethodPut, internal+upURL, "image/jpeg", []byte("evil"), ""); s != http.StatusConflict || !strings.Contains(string(b), "not_pending") {
		t.Fatalf("PUT over a committed object: %d %s", s, b)
	}

	// The driver app's route: an absolute URL under LOCAL_MEDIA_PUBLIC_BASE_URL, PUT on the public listener.
	tid := mustUUID(t, tenant)
	mp, err := st.Presign(ctx, storage.Caller{UserID: mustUUID(t, alice), TenantID: &tid}, storage.PresignInput{Purpose: "chat_image",
		EntityID: alice, ContentType: "image/png", SizeBytes: 3}, storage.PutOptions{})
	if err != nil || !strings.HasPrefix(mp.URL, public+"/media/chats/") {
		t.Fatalf("mobile presign: %+v %v", mp, err)
	}
	if s, _, b := raw(t, http.MethodPut, mp.URL, "image/png", []byte("png"), ""); s != http.StatusOK {
		t.Fatalf("mobile PUT on the public listener: %d %s", s, b)
	}
}
