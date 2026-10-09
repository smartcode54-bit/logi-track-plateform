package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
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
	code := run(context.Background(), &stdout, &stderr)
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
	if code := run(context.Background(), &stdout, &stderr); code != app.ExitConfigError {
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
	if code := run(ctx, &stdout, &stderr); code != app.ExitOK {
		t.Fatalf("exit code = %d, stderr %q, stdout %q", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "api listening") || !strings.Contains(stdout.String(), "shutdown complete") {
		t.Fatalf("lifecycle not logged: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"API_INTERNAL_ADDR":"set"`) {
		t.Fatalf("config dump should report set/unset: %s", stdout.String())
	}
}
