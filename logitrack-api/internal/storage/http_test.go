package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// mediaApp mounts the storage groups on one Fiber app; the GET route needs no database.
func mediaApp(t *testing.T, now func() time.Time) (*fiber.App, *Local) {
	t.Helper()
	l, _ := newTestLocal(t, now)
	s, err := New(nil, Config{Active: BackendLocal, Bucket: "logitrack", PublicBucket: "logitrack-public"}, zerolog.Nop(), l)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	deny := func(c fiber.Ctx) error { return httpx.ErrUnauthenticated() }
	groups := s.Groups(HTTPOptions{Auth: deny, Caller: func(fiber.Ctx) (Caller, bool) { return Caller{}, false }})
	if err := ingress.Validate(groups); err != nil {
		t.Fatal(err)
	}
	ingress.Mount(app, ingress.Internal, groups, nil)
	return app, l
}

func call(t *testing.T, app *fiber.App, method, target string) (*http.Response, string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(method, target, nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(b)
}

func errorCode(t *testing.T, body string) (string, string) {
	t.Helper()
	var e struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("not an error envelope: %s", body)
	}
	reason, _ := e.Error.Details["reason"].(string)
	return e.Error.Code, reason
}

func TestMediaServesSignedObjectsOnly(t *testing.T) {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	app, l := mediaApp(t, func() time.Time { return now })
	ctx := context.Background()
	key := "trips/t1/seal 1.jpg"
	if _, err := l.Write(key, strings.NewReader("jpeg-bytes"), "image/jpeg", 100); err != nil {
		t.Fatal(err)
	}
	signed, _, err := l.PresignGet(ctx, Object{Key: key}, time.Hour, GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	target := strings.TrimPrefix(signed, "https://logi.example.test")

	resp, body := call(t, app, http.MethodGet, target)
	if resp.StatusCode != http.StatusOK || body != "jpeg-bytes" || resp.Header.Get("Content-Type") != "image/jpeg" ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Cache-Control") != "private, max-age=3600" {
		t.Fatalf("signed GET: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if resp, body := call(t, app, http.MethodHead, target); resp.StatusCode != http.StatusOK || body != "" ||
		resp.Header.Get("Content-Length") != "10" {
		t.Fatalf("HEAD: %d %q %v", resp.StatusCode, body, resp.Header)
	}

	// No, tampered or expired signature: 403.
	for name, tc := range map[string]struct{ target, reason string }{
		"unsigned":  {strings.Split(target, "?")[0], "signature_invalid"},
		"tampered":  {strings.Replace(target, "X-LT-Signature=", "X-LT-Signature=A", 1), "signature_invalid"},
		"other key": {strings.Replace(target, "seal%201.jpg", "seal%202.jpg", 1), "signature_invalid"},
	} {
		resp, body := call(t, app, http.MethodGet, tc.target)
		if code, reason := errorCode(t, body); resp.StatusCode != http.StatusForbidden || code != "permission_denied" || reason != tc.reason {
			t.Errorf("%s: %d %s", name, resp.StatusCode, body)
		}
	}
	now = now.Add(time.Hour + time.Second)
	if resp, body := call(t, app, http.MethodGet, target); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expired: %d %s", resp.StatusCode, body)
	} else if _, reason := errorCode(t, body); reason != "signature_expired" {
		t.Fatalf("expired reason: %s", body)
	}

	// Directory paths, traversal and unknown keys: 404, never a listing. A directory named without the slash is a
	// key like any other: unsigned 403 (nothing is disclosed), signed 404.
	dir, _, err := l.PresignGet(ctx, Object{Key: "trips/t1"}, time.Hour, GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp, body := call(t, app, http.MethodGet, strings.TrimPrefix(dir, "https://logi.example.test")); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("signed directory: %d %s", resp.StatusCode, body)
	}
	for _, p := range []string{"/media/", "/media/trips/", "/media/trips/t1/", "/media/../etc/passwd",
		"/media/%2e%2e/%2e%2e/etc/passwd", "/media/trips/%2e%2e/%2e%2e/x", "/media/a%00b", "/media/app_releases/missing.apk"} {
		resp, body := call(t, app, http.MethodGet, p)
		if resp.StatusCode != http.StatusNotFound || strings.Contains(body, "seal") {
			t.Errorf("GET %s: %d %s", p, resp.StatusCode, body)
		}
	}

	// app_releases/ is public: no signature.
	if _, err := l.Write("app_releases/prod/logitrack-prod-v3.5.0.apk", strings.NewReader("apk"), "application/vnd.android.package-archive", 10); err != nil {
		t.Fatal(err)
	}
	resp, body = call(t, app, http.MethodGet, "/media/app_releases/prod/logitrack-prod-v3.5.0.apk")
	if resp.StatusCode != http.StatusOK || body != "apk" || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("public GET: %d %q %v", resp.StatusCode, body, resp.Header)
	}

	// PUT with a bad or missing signature never reaches the disk or the database.
	if resp, _ := call(t, app, http.MethodPut, "/media/trips/t1/new.jpg"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unsigned PUT: %d", resp.StatusCode)
	}
	if resp, _ := call(t, app, http.MethodPut, "/v1/uploads/local/..%2F..%2Fx.jpg"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("traversal PUT: %d", resp.StatusCode)
	}
}

func TestMediaIs404WithoutTheLocalBackend(t *testing.T) {
	s3, err := NewS3(testS3Config())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(nil, Config{Active: BackendS3, Bucket: "logitrack", PublicBucket: "logitrack-public"}, zerolog.Nop(), s3)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	ingress.Mount(app, ingress.Internal, s.Groups(HTTPOptions{Auth: func(c fiber.Ctx) error { return c.Next() },
		Caller: func(fiber.Ctx) (Caller, bool) { return Caller{}, false }}), nil)
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		if resp, _ := call(t, app, m, "/media/trips/t1/a.jpg"); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", m, resp.StatusCode)
		}
	}
	if resp, _ := call(t, app, http.MethodPut, "/v1/uploads/local/trips/t1/a.jpg"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("local upload route: %d", resp.StatusCode)
	}
}

// The head check of the upload routes (fasthttp HeaderReceived): only a valid, unexpired signature for exactly
// the head's path, Content-Type and Content-Length earns the large body limit; anything else keeps the API
// defaults (0).
func TestUploadLimitNeedsAValidSignedHead(t *testing.T) {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	l, _ := newTestLocal(t, func() time.Time { return now })
	s, err := New(nil, Config{Active: BackendLocal, Bucket: "logitrack", PublicBucket: "logitrack-public", UploadMaxBytes: 1 << 20},
		zerolog.Nop(), l)
	if err != nil {
		t.Fatal(err)
	}
	key := "chats/c1/สติกเกอร์ 1.jpg"
	const size = 300_000
	for _, tc := range []struct {
		prefix string
		opts   PutOptions
	}{{LocalUploadPath + "/", PutOptions{APIPath: true}}, {MediaPrefix + "/", PutOptions{}}} {
		put, err := l.PresignPut(context.Background(), Object{Key: key}, "image/jpeg", size, time.Minute, tc.opts)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(put.URL)
		if err != nil {
			t.Fatal(err)
		}
		limit := s.UploadLimit(tc.prefix)
		head := ingress.UploadHead{Path: u.EscapedPath(), Query: u.RawQuery, ContentType: "image/jpeg", ContentLength: size}
		if got := limit(head); got != size {
			t.Fatalf("%s: signed head limit %d, want %d", tc.prefix, got, size)
		}
		for name, mut := range map[string]func(h *ingress.UploadHead){
			"unsigned": func(h *ingress.UploadHead) { h.Query = "" },
			"tampered": func(h *ingress.UploadHead) {
				h.Query = strings.Replace(h.Query, "X-LT-Signature=", "X-LT-Signature=A", 1)
			},
			"other length":       func(h *ingress.UploadHead) { h.ContentLength = size + 1 },
			"chunked":            func(h *ingress.UploadHead) { h.ContentLength = -1 },
			"other content type": func(h *ingress.UploadHead) { h.ContentType = "text/html" },
			"other key":          func(h *ingress.UploadHead) { h.Path = strings.Replace(h.Path, "c1", "c2", 1) },
			"directory":          func(h *ingress.UploadHead) { h.Path += "/" },
			"other route":        func(h *ingress.UploadHead) { h.Path = "/v1/files/" + key },
		} {
			h := head
			mut(&h)
			if got := limit(h); got != 0 {
				t.Errorf("%s %s: limit %d, want 0", tc.prefix, name, got)
			}
		}
		now = now.Add(2 * time.Minute)
		if got := limit(head); got != 0 {
			t.Errorf("%s expired: limit %d", tc.prefix, got)
		}
		now = now.Add(-2 * time.Minute)
	}
	// Above UPLOAD_MAX_BYTES the head check never grants, even when signed.
	big, err := l.PresignPut(context.Background(), Object{Key: key}, "image/jpeg", 2<<20, time.Minute, PutOptions{APIPath: true})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(big.URL)
	if got := s.UploadLimit(LocalUploadPath + "/")(ingress.UploadHead{Path: u.EscapedPath(), Query: u.RawQuery, ContentType: "image/jpeg",
		ContentLength: 2 << 20}); got != 0 {
		t.Fatalf("over UPLOAD_MAX_BYTES: %d", got)
	}
}
