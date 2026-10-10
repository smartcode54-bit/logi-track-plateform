package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase/firebasetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
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

// required are the variables the api cannot start without.
var required = []string{"API_INTERNAL_ADDR", "API_PUBLIC_ADDR", "APP_ENV", "DATABASE_URL", "JWT_ACTIVE_KID",
	"JWT_AUDIENCE", "JWT_ISSUER", "JWT_SIGNING_KEY_FILE", "METRICS_ADDR", "REDIS_URL"}

// devKey writes a throw-away Ed25519 key and returns its path and RFC 7638 thumbprint.
func devKey(t *testing.T) (string, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "jwt.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, token.Thumbprint(priv.Public().(ed25519.PublicKey))
}

// startEnv sets a startable environment. The database and Redis URLs point at closed ports: the api
// connects lazily, so it starts and only /readyz would report them.
func startEnv(t *testing.T) {
	t.Helper()
	key, kid := devKey(t)
	for k, v := range map[string]string{
		"APP_ENV": "local", "API_INTERNAL_ADDR": "127.0.0.1:0", "API_PUBLIC_ADDR": "localhost:0",
		"METRICS_ADDR": "[::1]:0", "SHUTDOWN_TIMEOUT": "2s",
		"DATABASE_URL": "postgres://logitrack_app@127.0.0.1:1/logitrack", "REDIS_URL": "redis://127.0.0.1:1/0",
		"JWT_SIGNING_KEY_FILE": key, "JWT_ACTIVE_KID": kid, "JWT_ISSUER": "http://localhost", "JWT_AUDIENCE": "logitrack-test",
		"ARGON2_MEMORY_KB": "8192", "ARGON2_ITERATIONS": "1",
	} {
		t.Setenv(k, v)
	}
	clearEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT", "REDIS_KEY_PREFIX", "JWT_PREVIOUS_KEY_FILE", "FIREBASE_SCRYPT_SIGNER_KEY",
		"FIREBASE_SCRYPT_SALT_SEPARATOR", "FIREBASE_SCRYPT_ROUNDS", "FIREBASE_SCRYPT_MEM_COST",
		"AUTH_FIREBASE_BRIDGE_MODE", "FIREBASE_PROJECT_ID", "GOOGLE_APPLICATION_CREDENTIALS",
		"PG_OWNED_DOMAINS", "WEB_FLAG_OVERRIDES")
}

// An unreadable service-account file stops a bridged api with the configuration exit code, naming the
// variable but not the path (Appendix C §C.6).
func TestBridgeWithoutKeyFileRefusesToStart(t *testing.T) {
	startEnv(t)
	missing := filepath.Join(t.TempDir(), "secret-sa-name.json")
	t.Setenv("AUTH_FIREBASE_BRIDGE_MODE", "web")
	t.Setenv("FIREBASE_PROJECT_ID", "logitrack-bridge-test")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", missing)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), nil, &stdout, &stderr); code != app.ExitConfigError {
		t.Fatalf("exit code = %d, want %d; stdout %s", code, app.ExitConfigError, stdout.String())
	}
	out := stdout.String() + stderr.String()
	if !strings.Contains(out, "GOOGLE_APPLICATION_CREDENTIALS") || strings.Contains(out, "secret-sa-name") {
		t.Fatalf("want the variable named without its value: %s", out)
	}
}

// A bridged api starts without reaching Google (keys and tokens are fetched on first use) and never
// logs the key file's content.
func TestBridgeStartsWithoutGoogle(t *testing.T) {
	startEnv(t)
	b := firebasetest.New(t, "logitrack-bridge-test")
	path := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(path, b.ServiceAccountJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTH_FIREBASE_BRIDGE_MODE", "both")
	t.Setenv("FIREBASE_PROJECT_ID", "logitrack-bridge-test")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	if code := run(ctx, nil, &stdout, &stderr); code != app.ExitOK {
		t.Fatalf("exit code = %d, stderr %q, stdout %q", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `"account_mirror":true`) || !strings.Contains(out, `"web_custom_tokens":true`) ||
		!strings.Contains(out, `"GOOGLE_APPLICATION_CREDENTIALS":"set"`) {
		t.Fatalf("bridge state not logged: %s", out)
	}
	if strings.Contains(out, "PRIVATE KEY") || strings.Contains(out, "iam.gserviceaccount.com") || strings.Contains(out, path) {
		t.Fatalf("the key file leaked into the log: %s", out)
	}
}

func TestMissingRequiredEnvExitsNonZeroWithNames(t *testing.T) {
	clearEnv(t, required...)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, &stdout, &stderr)
	if code != app.ExitConfigError {
		t.Fatalf("exit code = %d, want %d", code, app.ExitConfigError)
	}
	msg := stderr.String()
	if !strings.Contains(msg, "missing required environment variables: "+strings.Join(required, ", ")) {
		t.Fatalf("unclear message: %q", msg)
	}
	if stdout.Len() != 0 {
		t.Fatalf("nothing should be logged before config is valid: %q", stdout.String())
	}
}

func TestInvalidEnvNamesVariableWithoutValue(t *testing.T) {
	startEnv(t)
	t.Setenv("APP_ENV", "staging-secret-name")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), nil, &stdout, &stderr); code != app.ExitConfigError {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "APP_ENV") || strings.Contains(stderr.String(), "staging-secret-name") {
		t.Fatalf("message must name APP_ENV but not its value: %q", stderr.String())
	}
}

// A signing key that is not the one JWT_ACTIVE_KID names stops the api with the configuration exit
// code before anything listens (Appendix C §C.4.2).
func TestWrongSigningKeyRefusesToStart(t *testing.T) {
	startEnv(t)
	_, otherKID := devKey(t)
	t.Setenv("JWT_ACTIVE_KID", otherKID)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), nil, &stdout, &stderr); code != app.ExitConfigError {
		t.Fatalf("exit code = %d, want %d; stdout %s", code, app.ExitConfigError, stdout.String())
	}
	if !strings.Contains(stdout.String(), "JWT_ACTIVE_KID") || strings.Contains(stdout.String(), "api listening") {
		t.Fatalf("want a JWT_ACTIVE_KID error before listening: %s", stdout.String())
	}
}

func TestStartsAndStopsCleanly(t *testing.T) {
	// ":0" three times passes validation only because the strings differ; startEnv uses distinct
	// host spellings.
	startEnv(t)
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
	// The auth groups (T05) reach the table through newAPI: /v1/auth on both listeners, /v1/me internal only;
	// the jobs groups (T10) and the web flags (T17) are internal only.
	for _, want := range []string{"GET /healthz internal,public\n", "GET /readyz internal\n", "GET /startupz internal\n",
		"POST /v1/auth/login internal,public\n", "POST /v1/auth/refresh internal,public\n", "GET /v1/me internal\n",
		"DELETE /v1/me/sessions/:sid internal\n", "GET /v1/jobs internal\n", "GET /v1/jobs/:id internal\n",
		"POST /v1/admin/queues/:queue/replay internal\n", "GET /v1/config/web-flags internal\n"} {
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
	// /public/v1 has no group of its own yet (the served /v1/mobile group belongs to the SSE stream, T12).
	code := routes(nil, &stdout, &stderr, ingress.Group{Prefix: "/public/v1", Public: true, Mount: func(r fiber.Router) {
		r.Use(func(c fiber.Ctx) error { return c.Next() })
		r.Post("/webhooks/x", nop)
	}})
	if code != app.ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	for _, want := range []string{"USE /public/v1 internal,public\n", "POST /public/v1/webhooks/x internal,public\n"} {
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
