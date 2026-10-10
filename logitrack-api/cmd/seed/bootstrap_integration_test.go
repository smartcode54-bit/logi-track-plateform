//go:build integration

// Acceptance tests of the owner additions to issue T19 (#39): no password in any output of cmd/seed (the
// smoke profile's temporary password and BOOTSTRAP_ADMIN_PASSWORD reach neither stdout nor stderr; a local tester
// gets the former through --temporary-password-file, mode 0600), and the bootstrap super admin of a fresh
// deployment: idempotent (two runs leave one user and one platform role), Argon2id, no must_change_password,
// platform_admin only for an address in PLATFORM_ADMIN_EMAILS, allowed in APP_ENV=prod.
package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// bootstrapPassword is a test-only BOOTSTRAP_ADMIN_PASSWORD; the tests assert it never reaches the output.
const bootstrapPassword = "bootstrap-Test-passphrase-41"

// temporaryToken is the shape of a temporary password (password.Temporary).
var temporaryToken = regexp.MustCompile(`[A-Z2-9]{12}`)

func (s *stack) etlConn(t *testing.T) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(context.Background(), s.d.URL(db.RoleETL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

// temporaryPasswordNotPrinted checks that no 12-symbol token of the run's output is d7's temporary password: each
// candidate is verified against the stored Argon2id hash (the value itself is never known to the test).
func (s *stack) temporaryPasswordNotPrinted(t *testing.T, r result) {
	t.Helper()
	var phc string
	if err := s.etlConn(t).QueryRow(context.Background(), `SELECT password_hash FROM users
		WHERE email = 'd7.kitti@logitrack.test'`).Scan(&phc); err != nil {
		t.Fatal(err)
	}
	h, err := password.NewHasher(password.Params{MemoryKB: 8192, Iterations: 1, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range temporaryToken.FindAllString(r.stdout+"\n"+r.stderr, -1) {
		if ok, _, err := h.Verify(context.Background(), tok, phc); err == nil && ok {
			t.Fatal("the temporary password of d7.kitti reached the output")
		}
	}
}

// --temporary-password-file hands the value to a local tester (mode 0600) and the output never shows it.
func TestTemporaryPasswordFile(t *testing.T) {
	s := newStack(t, "local")
	path := filepath.Join(t.TempDir(), "d7.txt")
	if err := os.WriteFile(path, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := s.mustSeed(t, "--profile", "smoke", "--temporary-password-file", path)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("the temporary password file has mode %o, want 600", st.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	value := strings.TrimSpace(string(b))
	if len(value) != password.TemporaryLength || strings.Contains(r.stdout+r.stderr, value) {
		t.Fatalf("file value of %d characters; printed: %t", len(value), strings.Contains(r.stdout+r.stderr, value))
	}
	if !strings.Contains(r.stdout, "written to "+path) {
		t.Errorf("the load does not name the file:\n%s", r.stdout)
	}
	var phc string
	if err := s.etlConn(t).QueryRow(context.Background(), `SELECT password_hash FROM users WHERE email = 'd7.kitti@logitrack.test'`).Scan(&phc); err != nil {
		t.Fatal(err)
	}
	h, _ := password.NewHasher(password.Params{MemoryKB: 8192, Iterations: 1, Parallelism: 1})
	if ok, _, err := h.Verify(context.Background(), value, phc); err != nil || !ok {
		t.Fatal("the file does not hold d7.kitti's temporary password")
	}
}

func (s *stack) bootstrapEnv(extra ...string) *stack {
	c := *s
	c.env = append(append(append([]string{}, s.env...), "BOOTSTRAP_ADMIN_PASSWORD="+bootstrapPassword), extra...)
	return &c
}

func noBootstrapPassword(t *testing.T, r result) {
	t.Helper()
	if strings.Contains(r.stdout, bootstrapPassword) || strings.Contains(r.stderr, bootstrapPassword) {
		t.Fatal("BOOTSTRAP_ADMIN_PASSWORD reached the output")
	}
}

// AC: seed --profile smoke and --bootstrap-admin print neither the temporary password nor BOOTSTRAP_ADMIN_PASSWORD;
// running the bootstrap twice leaves one user and one platform role.
func TestBootstrapAdmin(t *testing.T) {
	s := newStack(t, "local")
	r := s.mustSeed(t, "--profile", "smoke")
	s.temporaryPasswordNotPrinted(t, r)
	conn := s.etlConn(t)
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := conn.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	const email = "root@logitrack.test"
	admins := func() int {
		return count(`SELECT count(*) FROM user_platform_roles r JOIN users u ON u.id = r.user_id
			WHERE u.email = $1 AND r.role = 'platform_admin'`, email)
	}
	// A production deployment: APP_ENV=prod does not refuse the bootstrap.
	b := s.bootstrapEnv("APP_ENV=prod", "BOOTSTRAP_ADMIN_EMAIL=Root@LogiTrack.test", "PLATFORM_ADMIN_EMAILS=root@logitrack.test")
	r = b.mustSeed(t, "--bootstrap-admin")
	noBootstrapPassword(t, r)
	if !strings.Contains(r.stdout, "bootstrap admin root@logitrack.test: created; platform_admin granted") {
		t.Errorf("first bootstrap:\n%s", r.stdout)
	}
	r = b.mustSeed(t, "--bootstrap-admin")
	noBootstrapPassword(t, r)
	if !strings.Contains(r.stdout, "unchanged; platform_admin already held") {
		t.Errorf("second bootstrap:\n%s", r.stdout)
	}
	if n := count(`SELECT count(*) FROM users WHERE email = $1`, email); n != 1 || admins() != 1 {
		t.Fatalf("after two runs: %d users, %d platform roles", n, admins())
	}
	var mustChange bool
	var phc string
	var version int
	if err := conn.QueryRow(context.Background(), `SELECT must_change_password, password_hash, auth_version FROM users
		WHERE email = $1`, email).Scan(&mustChange, &phc, &version); err != nil {
		t.Fatal(err)
	}
	h, _ := password.NewHasher(password.Params{MemoryKB: 8192, Iterations: 1, Parallelism: 1})
	if ok, _, err := h.Verify(context.Background(), bootstrapPassword, phc); err != nil || !ok || mustChange ||
		!strings.HasPrefix(phc, "$argon2id$") {
		t.Fatalf("bootstrap admin: Argon2id %t, must change %t", strings.HasPrefix(phc, "$argon2id$"), mustChange)
	}
	if n := count(`SELECT count(*) FROM security_events WHERE event_type IN ('user_created', 'platform_role_granted')
		AND details->>'source' = 'bootstrap'`); n != 2 {
		t.Errorf("%d bootstrap security events, want user_created + platform_role_granted", n)
	}
	// A new password: set, auth_version raised, sessions revoked; never printed.
	b2 := s.bootstrapEnv("BOOTSTRAP_ADMIN_EMAIL="+email, "PLATFORM_ADMIN_EMAILS=root@logitrack.test")
	b2.env = append(b2.env, "BOOTSTRAP_ADMIN_PASSWORD=another-Bootstrap-passphrase-2")
	r = b2.mustSeed(t, "--bootstrap-admin")
	if strings.Contains(r.stdout+r.stderr, "another-Bootstrap-passphrase-2") || !strings.Contains(r.stdout, "password set, sessions revoked") {
		t.Errorf("password change:\n%s", r.stdout)
	}
	if v := count(`SELECT auth_version FROM users WHERE email = $1`, email); v <= version {
		t.Errorf("auth_version %d after a new password, was %d", v, version)
	}
	// Without the address in PLATFORM_ADMIN_EMAILS the user is created but gets no platform role.
	b3 := s.bootstrapEnv("BOOTSTRAP_ADMIN_EMAIL=ops@logitrack.test", "PLATFORM_ADMIN_EMAILS=root@logitrack.test")
	r = b3.mustSeed(t, "--bootstrap-admin")
	noBootstrapPassword(t, r)
	if !strings.Contains(r.stdout, "not granted") || count(`SELECT count(*) FROM user_platform_roles r JOIN users u ON u.id = r.user_id
		WHERE u.email = 'ops@logitrack.test'`) != 0 {
		t.Errorf("an address outside PLATFORM_ADMIN_EMAILS got platform_admin:\n%s", r.stdout)
	}
	// Review panel: an address left out of PLATFORM_ADMIN_EMAILS while the user still holds platform_admin is
	// reported as held outside the list (kept), never as "not granted".
	b4 := s.bootstrapEnv("BOOTSTRAP_ADMIN_EMAIL="+email, "PLATFORM_ADMIN_EMAILS=ops@logitrack.test")
	b4.env = append(b4.env, "BOOTSTRAP_ADMIN_PASSWORD=another-Bootstrap-passphrase-2")
	r = b4.mustSeed(t, "--bootstrap-admin")
	if strings.Contains(r.stdout, "not granted") || !strings.Contains(r.stdout,
		"unchanged; platform_admin held but the address is not in PLATFORM_ADMIN_EMAILS (warning: kept") || admins() != 1 {
		t.Errorf("a holder outside the list:\n%s", r.stdout)
	}
	// Review panel: an existing account whose address nobody proved (created outside the import, the bootstrap and a
	// platform admin, e.g. by a carrier tenant_admin) is never granted, by either command.
	if err := db.WithSystem(context.Background(), s.d.Pool(t, db.RoleETL), nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO users (email, display_name) VALUES ('claimed@logitrack.test', 'x')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	claimedRoles := func() int {
		return count(`SELECT count(*) FROM user_platform_roles r JOIN users u ON u.id = r.user_id WHERE u.email = 'claimed@logitrack.test'`)
	}
	r = s.bootstrapEnv("BOOTSTRAP_ADMIN_EMAIL=claimed@logitrack.test", "PLATFORM_ADMIN_EMAILS=claimed@logitrack.test").
		mustSeed(t, "--bootstrap-admin")
	if !strings.Contains(r.stdout, "platform_admin refused (warning)") || !strings.Contains(r.stdout, "address_unproven") ||
		claimedRoles() != 0 {
		t.Errorf("--bootstrap-admin over an unproven account:\n%s", r.stdout)
	}
	r = s.bootstrapEnv("PLATFORM_ADMIN_EMAILS=claimed@logitrack.test").mustSeed(t, "bootstrap-platform-admins")
	if !strings.Contains(r.stdout, "refused 1") || !strings.Contains(r.stdout, "claimed@logitrack.test (user ") ||
		claimedRoles() != 0 {
		t.Errorf("bootstrap-platform-admins over an unproven account:\n%s", r.stdout)
	}
	// bootstrap-platform-admins: idempotent; the smoke fixture's platform admin is reported as outside the list.
	r = b2.mustSeed(t, "bootstrap-platform-admins")
	if !strings.Contains(r.stdout, "granted 0, already held 1, no user 0") ||
		!strings.Contains(r.stdout, "platform.admin@logitrack.test holds platform_admin but is not in PLATFORM_ADMIN_EMAILS") {
		t.Errorf("bootstrap-platform-admins:\n%s", r.stdout)
	}
	r = s.bootstrapEnv("PLATFORM_ADMIN_EMAILS=root@logitrack.test,ops@logitrack.test,ghost@logitrack.test").
		mustSeed(t, "bootstrap-platform-admins")
	if !strings.Contains(r.stdout, "granted 1, already held 1, no user 1") || admins() != 1 {
		t.Errorf("bootstrap-platform-admins with a new address:\n%s", r.stdout)
	}
}
