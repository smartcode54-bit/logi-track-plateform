package httpx_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func newApp() *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Use(httpx.RequestID())
	app.Get("/ok", func(c fiber.Ctx) error { return httpx.JSON(c, 200, map[string]string{"a": "b"}) })
	app.Get("/typed", func(c fiber.Ctx) error {
		return httpx.NewError(409, "already_exists", "exists").WithDetails(map[string]any{"field": "plate"})
	})
	app.Get("/plain", func(c fiber.Ctx) error { return errors.New("db exploded: password=hunter2") })
	app.Post("/only-post", func(c fiber.Ctx) error { return nil })
	return app
}

func do(t *testing.T, app *fiber.App, method, path string, hdr map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	var body map[string]any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &body); err != nil {
			t.Fatalf("body is not JSON: %s", b)
		}
	}
	return resp, body
}

func assertEnvelope(t *testing.T, resp *http.Response, body map[string]any, status int, code string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d (%v)", resp.StatusCode, status, body)
	}
	if len(body) != 1 {
		t.Fatalf("top level must hold only \"error\": %v", body)
	}
	e, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error object: %v", body)
	}
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "code,details,message,requestId" {
		t.Fatalf("envelope fields = %v, want exactly code,details,message,requestId", keys)
	}
	if e["code"] != code {
		t.Fatalf("code = %v, want %s", e["code"], code)
	}
	if _, ok := e["details"].(map[string]any); !ok {
		t.Fatalf("details must be an object: %v", e["details"])
	}
	if e["requestId"] != resp.Header.Get(httpx.HeaderRequestID) || e["requestId"] == "" {
		t.Fatalf("requestId %v != X-Request-Id %q", e["requestId"], resp.Header.Get(httpx.HeaderRequestID))
	}
}

func TestErrorEnvelopeShapes(t *testing.T) {
	app := newApp()
	cases := []struct {
		method, path string
		status       int
		code         string
	}{
		{"GET", "/nope", 404, "not_found"},
		{"GET", "/typed", 409, "already_exists"},
		{"GET", "/plain", 500, "internal"},
		{"GET", "/only-post", 405, "method_not_allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp, body := do(t, app, tc.method, tc.path, nil)
			assertEnvelope(t, resp, body, tc.status, tc.code)
		})
	}
}

func TestInternalErrorsDoNotLeakCause(t *testing.T) {
	_, body := do(t, newApp(), "GET", "/plain", nil)
	b, _ := json.Marshal(body)
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "exploded") {
		t.Fatalf("internal cause leaked: %s", b)
	}
}

func TestDetailsArePreserved(t *testing.T) {
	_, body := do(t, newApp(), "GET", "/typed", nil)
	d := body["error"].(map[string]any)["details"].(map[string]any)
	if d["field"] != "plate" {
		t.Fatalf("details lost: %v", d)
	}
}

func TestRequestIDReusedWhenWellFormed(t *testing.T) {
	resp, body := do(t, newApp(), "GET", "/nope", map[string]string{httpx.HeaderRequestID: "0199c2f4-aaaa-7bbb-8ccc-1234567890ab"})
	if got := resp.Header.Get(httpx.HeaderRequestID); got != "0199c2f4-aaaa-7bbb-8ccc-1234567890ab" {
		t.Fatalf("incoming id not reused: %q", got)
	}
	assertEnvelope(t, resp, body, 404, "not_found")
}

func TestRequestIDReplacedWhenMalformed(t *testing.T) {
	resp, _ := do(t, newApp(), "GET", "/ok", map[string]string{httpx.HeaderRequestID: "bad id\nwith newline"})
	got := resp.Header.Get(httpx.HeaderRequestID)
	if got == "" || strings.Contains(got, " ") {
		t.Fatalf("malformed id was not replaced: %q", got)
	}
}

func TestSuccessEnvelope(t *testing.T) {
	resp, body := do(t, newApp(), "GET", "/ok", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if d, ok := body["data"].(map[string]any); !ok || d["a"] != "b" {
		t.Fatalf("want {\"data\":{\"a\":\"b\"}}, got %v", body)
	}
}

func TestRejectHeader(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Use(httpx.RequestID(), httpx.RejectHeader("X-Act-On-Tenant"))
	app.Get("/x", func(c fiber.Ctx) error { return c.SendStatus(204) })
	resp, body := do(t, app, "GET", "/x", map[string]string{"X-Act-On-Tenant": "*"})
	assertEnvelope(t, resp, body, 400, "header_not_allowed")
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header["X-Act-On-Tenant"] = []string{""}
	if r, _ := app.Test(req); r.StatusCode != 400 {
		t.Fatalf("empty header must be rejected, got %d", r.StatusCode)
	}
	resp, _ = do(t, app, "GET", "/x", nil)
	if resp.StatusCode != 204 {
		t.Fatalf("without header: %d", resp.StatusCode)
	}
}

func TestInternalErrorLoggedOnceWithoutDuplicateKeys(t *testing.T) {
	var buf strings.Builder
	base := zerolog.New(&buf)
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(base)})
	app.Use(httpx.RequestID(), httpx.Logger(base))
	app.Get("/boom", func(c fiber.Ctx) error { return errors.New("db down") })
	app.Get("/drain", func(c fiber.Ctx) error { return httpx.ErrUnavailable("draining") })
	if _, err := app.Test(httptest.NewRequest("GET", "/boom", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Test(httptest.NewRequest("GET", "/drain", nil)); err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.Count(line, `"request_id"`) != 1 {
			t.Fatalf("request_id must appear exactly once: %s", line)
		}
		if strings.Contains(line, `"request failed"`) {
			failed++
		}
		if strings.Contains(line, `"route":"/drain"`) && !strings.Contains(line, `"level":"warn"`) {
			t.Fatalf("503 unavailable should log at warn: %s", line)
		}
	}
	if failed != 1 {
		t.Fatalf("want exactly one 'request failed' line (for /boom), got %d", failed)
	}
}
