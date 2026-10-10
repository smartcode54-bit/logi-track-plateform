package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/cmd/seed/internal/seed"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
)

// secret is a test-only SEED_DEFAULT_PASSWORD.
const secret = "unit-test-Seed-Password-1"

func runSeed(t *testing.T, env []string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, env, &out, &errb)
	if strings.Contains(out.String(), secret) || strings.Contains(errb.String(), secret) {
		t.Fatalf("seed %v printed SEED_DEFAULT_PASSWORD", args)
	}
	return code, out.String(), errb.String()
}

func baseEnv(appEnv string) []string {
	return []string{"APP_ENV=" + appEnv, "LOG_FORMAT=json", "STORAGE_BACKEND=local", "LOCAL_MEDIA_DIR=/nonexistent-seed-test",
		"SEED_DEFAULT_PASSWORD=" + secret}
}

// Profile loads, --reset and --verify refuse APP_ENV=prod; nothing connects.
func TestProdIsRefused(t *testing.T) {
	for _, args := range [][]string{{"--profile", "smoke"}, {"--verify"}, {"--reset"}, {"--dry-run"}} {
		code, _, stderr := runSeed(t, baseEnv("prod"), args...)
		if code != app.ExitConfigError || !strings.Contains(stderr, "APP_ENV=prod") {
			t.Errorf("seed %v with APP_ENV=prod: exit %d, %q", args, code, stderr)
		}
	}
}

func TestDecideMode(t *testing.T) {
	for _, tc := range []struct {
		env   string
		o     options
		mode  seed.Mode
		reset bool
		err   string
	}{
		{env: "local", mode: seed.ModeInsert, reset: true},
		{env: "local", o: options{reset: false, resetSet: true}, mode: seed.ModeInsert, reset: false},
		{env: "local", o: options{mode: "upsert"}, mode: seed.ModeUpsert, reset: false},
		{env: "local", o: options{mode: "merge"}, err: "--mode"},
		{env: "dev", err: "--allow-shared"},
		{env: "dev", o: options{allowShared: true}, mode: seed.ModeUpsert, reset: false},
		{env: "dev", o: options{allowShared: true, mode: "insert"}, mode: seed.ModeUpsert, reset: false},
		{env: "dev", o: options{allowShared: true, reset: true, resetSet: true}, err: "--reset"},
		{env: "prod", o: options{allowShared: true}, err: "APP_ENV=prod"},
	} {
		mode, reset, msg := decideMode(tc.env, tc.o)
		if tc.err != "" {
			if !strings.Contains(msg, tc.err) {
				t.Errorf("%s %+v: %q, want an error naming %s", tc.env, tc.o, msg, tc.err)
			}
			continue
		}
		if msg != "" || mode != tc.mode || reset != tc.reset {
			t.Errorf("%s %+v: mode %s reset %t (%q), want %s %t", tc.env, tc.o, mode, reset, msg, tc.mode, tc.reset)
		}
	}
}

// --dry-run builds the plan without any database and prints the per-table counts of the profile.
func TestDryRun(t *testing.T) {
	code, out, stderr := runSeed(t, baseEnv("local"), "--dry-run", "--profile", "demo")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"profile demo", "users", "task_number_counters", "nothing written"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out)
		}
	}
	if code, _, _ := runSeed(t, baseEnv("local"), "--dry-run", "--profile", "bulk"); code != app.ExitConfigError {
		t.Errorf("an unknown profile: exit %d", code)
	}
}

// A load needs SEED_DEFAULT_PASSWORD and the database URLs; the bootstrap subcommand belongs to T19.
func TestRequiredInputs(t *testing.T) {
	env := baseEnv("local")[:4]
	if code, _, stderr := runSeed(t, env); code != app.ExitConfigError || !strings.Contains(stderr, "SEED_DEFAULT_PASSWORD") {
		t.Errorf("load without SEED_DEFAULT_PASSWORD: exit %d, %q", code, stderr)
	}
	if code, _, stderr := runSeed(t, baseEnv("local")); code != 2 || !strings.Contains(stderr, "ETL_DATABASE_URL") {
		t.Errorf("load without ETL_DATABASE_URL: exit %d, %q", code, stderr)
	}
	if code, _, _ := runSeed(t, baseEnv("local"), "bootstrap-platform-admins"); code != app.ExitNotImplemented {
		t.Errorf("bootstrap-platform-admins: exit %d, want %d (T19)", code, app.ExitNotImplemented)
	}
	if code, _, _ := runSeed(t, baseEnv("local"), "--profile", "smoke", "extra"); code != app.ExitConfigError {
		t.Errorf("a stray argument: exit %d", code)
	}
}
