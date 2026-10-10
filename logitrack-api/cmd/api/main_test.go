package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

func clearEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		t.Setenv(n, "") // registers restore
		if err := os.Unsetenv(n); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMissingRequiredEnvExitsNonZeroWithNames(t *testing.T) {
	clearEnv(t, "APP_ENV", "API_INTERNAL_ADDR", "API_PUBLIC_ADDR", "METRICS_ADDR")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, &stdout, &stderr)
	if code != app.ExitConfigError {
		t.Fatalf("exit code = %d, want %d", code, app.ExitConfigError)
	}
	msg := stderr.String()
	if !strings.Contains(msg, "missing required environment variables: API_INTERNAL_ADDR, API_PUBLIC_ADDR, APP_ENV, METRICS_ADDR") {
		t.Fatalf("unclear message: %q", msg)
	}
	if stdout.Len() != 0 {
		t.Fatalf("nothing should be logged before config is valid: %q", stdout.String())
	}
}

func TestInvalidEnvNamesVariableWithoutValue(t *testing.T) {
	clearEnv(t, "API_INTERNAL_ADDR", "API_PUBLIC_ADDR", "METRICS_ADDR")
	t.Setenv("APP_ENV", "staging-secret-name")
	t.Setenv("API_INTERNAL_ADDR", "127.0.0.1:0")
	t.Setenv("API_PUBLIC_ADDR", "127.0.0.1:1")
	t.Setenv("METRICS_ADDR", "127.0.0.1:2")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), nil, &stdout, &stderr); code != app.ExitConfigError {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "APP_ENV") || strings.Contains(stderr.String(), "staging-secret-name") {
		t.Fatalf("message must name APP_ENV but not its value: %q", stderr.String())
	}
}

func TestStartsAndStopsCleanly(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("API_INTERNAL_ADDR", "127.0.0.1:0")
	t.Setenv("API_PUBLIC_ADDR", "127.0.0.1:0")
	t.Setenv("METRICS_ADDR", "127.0.0.1:0")
	t.Setenv("SHUTDOWN_TIMEOUT", "2s")
	clearEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT")
	// ":0" three times passes validation only because the strings differ;
	// use distinct host spellings.
	t.Setenv("API_PUBLIC_ADDR", "localhost:0")
	t.Setenv("METRICS_ADDR", "[::1]:0")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stop immediately after startup
	var stdout, stderr bytes.Buffer
	if code := run(ctx, nil, &stdout, &stderr); code != app.ExitOK {
		t.Fatalf("exit code = %d, stderr %q, stdout %q", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "api listening") || !strings.Contains(stdout.String(), "shutdown complete") {
		t.Fatalf("lifecycle not logged: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"API_INTERNAL_ADDR":"set"`) {
		t.Fatalf("config dump should report set/unset: %s", stdout.String())
	}
}

func TestRoutesTableMatchesTheCommittedFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"routes"}, &stdout, &stderr); code != app.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	committed, err := os.ReadFile("../../api/routes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != string(committed) {
		t.Fatalf("api/routes.txt is stale; run make gen\n--- generated\n%s--- committed\n%s", stdout.String(), committed)
	}
	for _, want := range []string{"GET /healthz internal,public\n", "GET /readyz internal\n", "GET /startupz internal\n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("table lacks %q:\n%s", want, stdout.String())
		}
	}
}

// go-ci gen-check runs `api routes` through go generate: a group marked public outside the
// allow-list stops it with exit 1, so the job fails before any table is written.
func TestRoutesRefusesAPublicGroupOutsideTheAllowList(t *testing.T) {
	out := filepath.Join(t.TempDir(), "routes.txt")
	for _, g := range []ingress.Group{
		{Prefix: "/v1/admin", Public: true, Mount: func(r fiber.Router) { r.Get("", nop) }},
		{Prefix: "/v1/mobilex", Public: true, Mount: func(r fiber.Router) { r.Get("", nop) }},
	} {
		var stdout, stderr bytes.Buffer
		code := routes([]string{"-o", out}, &stdout, &stderr, g)
		if code != app.ExitRuntimeError || !strings.Contains(stderr.String(), `group "`+g.Prefix+`" is marked public`) {
			t.Fatalf("%s: exit %d, stderr %q", g.Prefix, code, stderr.String())
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("%s: a table was written despite the violation (%v)", g.Prefix, err)
		}
	}
	// An internal group of any name is fine and shows up as internal only.
	var stdout, stderr bytes.Buffer
	code := routes(nil, &stdout, &stderr, ingress.Group{Prefix: "/v1/admin", Mount: func(r fiber.Router) { r.Post("/queues/:queue/replay", nop) }})
	if code != app.ExitOK || !strings.Contains(stdout.String(), "POST /v1/admin/queues/:queue/replay internal\n") {
		t.Fatalf("exit %d, stderr %q, table:\n%s", code, stderr.String(), stdout.String())
	}
}

// Middleware registered with Use is listed as USE with its listeners: a public group's auth
// middleware passes, and Use matches by prefix, so the table shows what answers below PATH.
func TestRoutesListsUseRegistrations(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := routes(nil, &stdout, &stderr, ingress.Group{Prefix: "/v1/mobile", Public: true, Mount: func(r fiber.Router) {
		r.Use(func(c fiber.Ctx) error { return c.Next() })
		r.Get("/tasks", nop)
	}})
	if code != app.ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	for _, want := range []string{"USE /v1/mobile internal,public\n", "GET /v1/mobile/tasks internal,public\n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("table lacks %q:\n%s", want, stdout.String())
		}
	}
}

func TestUnknownArgumentIsAUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"serve-now"}, &stdout, &stderr); code != app.ExitConfigError ||
		!strings.Contains(stderr.String(), "api routes") {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if code := run(context.Background(), []string{"routes", "extra"}, &stdout, &stderr); code != app.ExitConfigError {
		t.Fatalf("routes with an argument: exit %d", code)
	}
}

func nop(fiber.Ctx) error { return nil }
