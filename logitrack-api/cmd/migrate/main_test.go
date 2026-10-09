package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func runCLI(t *testing.T, environ []string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(context.Background(), args, environ, migrations.FS, &out, &errb)
	return code, out.String(), errb.String()
}

func TestUsageErrors(t *testing.T) {
	env := []string{"APP_ENV=local", "MIGRATE_DATABASE_URL=postgres://logitrack_migrator:pw@127.0.0.1:1/logitrack"}
	for _, args := range [][]string{
		{},
		{"sideways"},
		{"up", "extra"},
		{"up-to"},
		{"up-to", "0"},
		{"up-to", "nine"},
		{"down-to", "-1"},
		{"down-to", "1", "2"},
		{"status", "now"},
		{"check", "extra"},
		{"create"},
		{"create", "a", "b"},
	} {
		code, _, stderr := runCLI(t, env, args...)
		if code != 2 || !strings.Contains(stderr, "usage: migrate") {
			t.Errorf("%v: exit %d, stderr %q", args, code, stderr)
		}
	}
	if code, stdout, _ := runCLI(t, nil, "help"); code != 0 || !strings.Contains(stdout, "up-to VERSION") {
		t.Errorf("help: exit %d", code)
	}
}

func TestCheckEmbeddedAndDirectory(t *testing.T) {
	code, stdout, stderr := runCLI(t, nil, "check")
	if code != 0 || !strings.Contains(stdout, "embedded chain") {
		t.Fatalf("check: exit %d, %s%s", code, stdout, stderr)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0001_no_down.sql"), []byte("-- +goose Up\nSELECT 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = runCLI(t, nil, "check", "-dir", dir)
	if code != 1 || !strings.Contains(stderr, "has no '-- +goose Down' section") {
		t.Fatalf("a file without Down: exit %d, stderr %q", code, stderr)
	}
}

func TestCreate(t *testing.T) {
	dir := t.TempDir()
	code, stdout, stderr := runCLI(t, nil, "create", "-dir", dir, "add_trips")
	if code != 0 || !strings.Contains(stdout, "0001_add_trips.sql") {
		t.Fatalf("create: exit %d, %s%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "0001_add_trips.sql")); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runCLI(t, nil, "create", "-dir", dir, "Bad-Name"); code != 2 {
		t.Fatalf("bad name: exit %d", code)
	}
}

func TestDatabaseCommandsNeedConfig(t *testing.T) {
	code, _, stderr := runCLI(t, nil, "up")
	if code != 2 || !strings.Contains(stderr, "APP_ENV") || !strings.Contains(stderr, "MIGRATE_DATABASE_URL") {
		t.Fatalf("no env: exit %d, stderr %q", code, stderr)
	}
	code, _, stderr = runCLI(t, []string{"APP_ENV=local", "MIGRATE_DATABASE_URL=mysql://x"}, "status")
	if code != 2 || !strings.Contains(stderr, "must be a postgres:// URL") {
		t.Fatalf("mysql URL: exit %d, stderr %q", code, stderr)
	}
}

// Production never runs goose down: the refusal comes before any connection attempt (the URL
// points at a closed port, so a connection attempt would exit 1 instead).
func TestDownRefusedInProd(t *testing.T) {
	env := []string{"APP_ENV=prod", "MIGRATE_DATABASE_URL=postgres://logitrack_migrator:s3cret-value@127.0.0.1:1/logitrack"}
	for _, args := range [][]string{{"down"}, {"down-to", "0"}} {
		code, _, stderr := runCLI(t, env, args...)
		if code != 2 || !strings.Contains(stderr, "refused when APP_ENV=prod") {
			t.Errorf("%v: exit %d, stderr %q", args, code, stderr)
		}
	}
}

func TestUnreachableDatabaseKeepsThePasswordOut(t *testing.T) {
	env := []string{"APP_ENV=local", "MIGRATE_DATABASE_URL=postgres://logitrack_migrator:s3cret-value@127.0.0.1:1/logitrack?connect_timeout=2"}
	code, stdout, stderr := runCLI(t, env, "up")
	if code != 1 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if strings.Contains(stdout+stderr, "s3cret-value") {
		t.Fatalf("the password leaked: %s%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "database unavailable") {
		t.Fatalf("stderr %q", stderr)
	}
}
