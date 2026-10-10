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

// A load needs SEED_DEFAULT_PASSWORD and the database URLs; the bootstrap commands (T19) need their names.
func TestRequiredInputs(t *testing.T) {
	env := baseEnv("local")[:4]
	if code, _, stderr := runSeed(t, env); code != app.ExitConfigError || !strings.Contains(stderr, "SEED_DEFAULT_PASSWORD") {
		t.Errorf("load without SEED_DEFAULT_PASSWORD: exit %d, %q", code, stderr)
	}
	if code, _, stderr := runSeed(t, baseEnv("local")); code != 2 || !strings.Contains(stderr, "ETL_DATABASE_URL") {
		t.Errorf("load without ETL_DATABASE_URL: exit %d, %q", code, stderr)
	}
	if code, _, stderr := runSeed(t, baseEnv("local"), "bootstrap-platform-admins"); code != app.ExitConfigError ||
		!strings.Contains(stderr, "PLATFORM_ADMIN_EMAILS") {
		t.Errorf("bootstrap-platform-admins without PLATFORM_ADMIN_EMAILS: exit %d, %q", code, stderr)
	}
	if code, _, stderr := runSeed(t, baseEnv("local"), "--bootstrap-admin"); code != app.ExitConfigError ||
		!strings.Contains(stderr, "BOOTSTRAP_ADMIN_EMAIL, BOOTSTRAP_ADMIN_PASSWORD") {
		t.Errorf("--bootstrap-admin without its names: exit %d, %q", code, stderr)
	}
	if code, _, stderr := runSeed(t, baseEnv("local"), "--bootstrap-admin", "--profile", "smoke"); code != app.ExitConfigError ||
		!strings.Contains(stderr, "runs alone") {
		t.Errorf("--bootstrap-admin with --profile: exit %d, %q", code, stderr)
	}
	if code, _, _ := runSeed(t, baseEnv("local"), "--profile", "smoke", "extra"); code != app.ExitConfigError {
		t.Errorf("a stray argument: exit %d", code)
	}
}

// The bootstrap admin's password passes the policy before anything connects; the refusal names the rule, never
// the value (owner addition to T19: no password in any output).
func TestBootstrapAdminPasswordPolicy(t *testing.T) {
	const short = "Sh0rt-pw" // 8 characters, under PASSWORD_MIN_LENGTH=12
	env := append(baseEnv("prod"), "BOOTSTRAP_ADMIN_EMAIL=root@example.test", "BOOTSTRAP_ADMIN_PASSWORD="+short,
		"PASSWORD_MIN_LENGTH=12", "PLATFORM_ADMIN_EMAILS=root@example.test")
	code, out, stderr := runSeed(t, env, "--bootstrap-admin")
	if code != app.ExitConfigError || !strings.Contains(stderr, "too_short") || !strings.Contains(stderr, "at least 12") {
		t.Fatalf("a short BOOTSTRAP_ADMIN_PASSWORD: exit %d, %q", code, stderr)
	}
	if strings.Contains(out+stderr, short) {
		t.Fatal("the refused BOOTSTRAP_ADMIN_PASSWORD reached the output")
	}
	// A valid password and no database: exit 2 (dependency), still without the password anywhere. APP_ENV=prod
	// does not refuse the bootstrap.
	const long = "a-long-Bootstrap-passphrase-9"
	env = append(baseEnv("prod"), "BOOTSTRAP_ADMIN_EMAIL=root@example.test", "BOOTSTRAP_ADMIN_PASSWORD="+long,
		"ARGON2_MEMORY_KB=8192", "ARGON2_ITERATIONS=1", "ARGON2_PARALLELISM=1")
	code, out, stderr = runSeed(t, env, "--bootstrap-admin")
	if code != app.ExitConfigError || !strings.Contains(stderr, "ETL_DATABASE_URL") || strings.Contains(stderr, "APP_ENV=prod") {
		t.Fatalf("bootstrap without a database: exit %d, %q", code, stderr)
	}
	if strings.Contains(out+stderr, long) {
		t.Fatal("BOOTSTRAP_ADMIN_PASSWORD reached the output")
	}
}
