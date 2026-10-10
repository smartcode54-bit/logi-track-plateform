//go:build integration

package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

func cli(t *testing.T, url string, chain fs.FS, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	env := []string{"APP_ENV=local", "LOG_FORMAT=json", "MIGRATE_DATABASE_URL=" + url}
	code := run(context.Background(), args, env, chain, &out, &errb)
	return code, out.String(), errb.String()
}

// The production order of R59/R88 through the binary: up-to 9, status shows 0010 pending, up.
func TestUpToNineStatusUpAndDown(t *testing.T) {
	d := pgtest.NewDatabase(t)
	url := d.URL(db.RoleMigrator)
	shape := os.DirFS("../../internal/platform/migrate/testdata/baseline-shape")

	if code, _, stderr := cli(t, url, shape, "up-to", "9"); code != 0 {
		t.Fatalf("up-to 9: exit %d\n%s", code, stderr)
	}
	code, stdout, stderr := cli(t, url, shape, "status")
	if code != 0 {
		t.Fatalf("status: exit %d\n%s", code, stderr)
	}
	for _, want := range []string{
		`(?m)^0009\s+applied\s+\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\s+0009_infra\.sql$`,
		`(?m)^0010\s+pending\s+-\s+0010_d5_unique_constraints\.sql$`,
	} {
		if !regexp.MustCompile(want).MatchString(stdout) {
			t.Fatalf("status output does not match %s:\n%s", want, stdout)
		}
	}
	if code, _, stderr := cli(t, url, shape, "status", "-fail-on-pending"); code != 1 || !strings.Contains(stderr, "1 migration(s) pending") {
		t.Fatalf("status -fail-on-pending: exit %d, %q", code, stderr)
	}
	expectVersion(t, url, shape, "9")

	if code, _, stderr := cli(t, url, shape, "up"); code != 0 {
		t.Fatalf("up: exit %d\n%s", code, stderr)
	}
	expectVersion(t, url, shape, "10")
	if code, _, stderr := cli(t, url, shape, "status", "-fail-on-pending"); code != 0 {
		t.Fatalf("status after up: exit %d\n%s", code, stderr)
	}
	if code, _, stderr := cli(t, url, shape, "down"); code != 0 {
		t.Fatalf("down: exit %d\n%s", code, stderr)
	}
	expectVersion(t, url, shape, "9")
	if code, _, stderr := cli(t, url, shape, "down-to", "0"); code != 0 {
		t.Fatalf("down-to 0: exit %d\n%s", code, stderr)
	}
	expectVersion(t, url, shape, "0")
	if code, _, stderr := cli(t, url, shape, "down"); code != 0 {
		t.Fatalf("down on an empty chain: exit %d\n%s", code, stderr)
	}
}

func TestEmbeddedChainUp(t *testing.T) {
	d := pgtest.NewDatabase(t)
	url := d.URL(db.RoleMigrator)
	code, _, stderr := cli(t, url, migrations.FS, "up")
	if code != 0 || !strings.Contains(stderr, `"file":"0001_preamble.sql"`) {
		t.Fatalf("up: exit %d\n%s", code, stderr)
	}
	expectVersion(t, url, migrations.FS, "1")
}

func TestRefusesOtherLogins(t *testing.T) {
	d := pgtest.NewDatabase(t)
	for _, role := range []string{db.RoleApp, db.RoleETL} {
		code, _, stderr := cli(t, d.URL(role), migrations.FS, "up")
		if code != 1 || !strings.Contains(stderr, "migrations run as logitrack_migrator through MIGRATE_DATABASE_URL") {
			t.Errorf("%s: exit %d\n%s", role, code, stderr)
		}
	}
}

func expectVersion(t *testing.T, url string, chain fs.FS, want string) {
	t.Helper()
	code, stdout, stderr := cli(t, url, chain, "version")
	if code != 0 || strings.TrimSpace(stdout) != want {
		t.Fatalf("version: exit %d, got %q, want %s\n%s", code, stdout, want, stderr)
	}
}
