package main

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/tools/internal/dotenv"
)

const template = `# names only
APP_ENV=dev
LOG_LEVEL=info
DATABASE_URL=
MIGRATE_DATABASE_URL=
ETL_DATABASE_URL=
REDIS_URL=
RABBITMQ_URL=
RABBITMQ_DEFAULT_USER=logitrack
RABBITMQ_DEFAULT_PASS=
LOCAL_MEDIA_SIGNING_KEY=
API_KEY_PEPPER=
BOOTSTRAP_ADMIN_PASSWORD=
SEED_DEFAULT_PASSWORD=
OWN_FLEET_TENANT_ID=
JWT_SIGNING_KEY_FILE=/opt/logitrack/shared/secrets/jwt-ed25519.pem
JWT_ACTIVE_KID=
`

type fixture struct {
	dir, tmpl, out string
}

func newFixture(t *testing.T, tmpl string) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{dir: dir, tmpl: filepath.Join(dir, "env.vm.example"), out: filepath.Join(dir, "opt", "logitrack", "shared", ".env")}
	if err := os.WriteFile(f.tmpl, []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixture) render(t *testing.T, extra ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	args := append([]string{"render", "-template", f.tmpl, "-out", f.out, "-prefix", f.dir}, extra...)
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("render: exit %d: %s", code, errb.String())
	}
	return out.String()
}

func readEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	l, err := dotenv.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return dotenv.Map(l)
}

func TestRenderGeneratesKeepsAndNeverPrints(t *testing.T) {
	f := newFixture(t, template)
	out := f.render(t)
	env := readEnv(t, f.out)
	st, err := os.Stat(f.out)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf(".env mode %v", st.Mode().Perm())
	}
	for _, n := range []string{"RABBITMQ_DEFAULT_PASS", "LOCAL_MEDIA_SIGNING_KEY", "API_KEY_PEPPER", "BOOTSTRAP_ADMIN_PASSWORD", "SEED_DEFAULT_PASSWORD"} {
		if len(env[n]) < 20 {
			t.Errorf("%s not generated", n)
		}
		if strings.Contains(out, env[n]) {
			t.Errorf("render printed %s", n)
		}
	}
	for n, want := range map[string]string{"DATABASE_URL": "logitrack_app", "MIGRATE_DATABASE_URL": "logitrack_migrator", "ETL_DATABASE_URL": "logitrack_etl"} {
		u, err := url.Parse(env[n])
		if err != nil || u.User.Username() != want || u.Host != "127.0.0.1:5432" || u.Path != "/logitrack" || u.Query().Get("sslmode") != "disable" {
			t.Errorf("%s = %s", n, u.Redacted())
		}
		pw, _ := u.User.Password()
		if len(pw) != 32 || strings.Contains(out, pw) {
			t.Errorf("%s password: %d characters, printed=%v", n, len(pw), strings.Contains(out, pw))
		}
	}
	if u, _ := url.Parse(env["REDIS_URL"]); u.Host != "127.0.0.1:6381" || u.Path != "/0" {
		t.Errorf("REDIS_URL = %s", u.Redacted())
	}
	if env["RABBITMQ_URL"] != "amqp://logitrack:"+env["RABBITMQ_DEFAULT_PASS"]+"@127.0.0.1:5674/" {
		t.Error("RABBITMQ_URL does not carry RABBITMQ_DEFAULT_USER/PASS")
	}
	keyFile := filepath.Join(f.dir, env["JWT_SIGNING_KEY_FILE"])
	pub, err := loadKey(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if env["JWT_ACTIVE_KID"] != token.Thumbprint(pub) {
		t.Error("JWT_ACTIVE_KID is not the RFC 7638 thumbprint of the key")
	}
	if st, _ := os.Stat(keyFile); st.Mode().Perm() != 0o600 {
		t.Errorf("key mode %v", st.Mode().Perm())
	}
	if env["OWN_FLEET_TENANT_ID"] == "" || env["LOG_LEVEL"] != "info" {
		t.Error("template values or the own-fleet id missing")
	}

	// A second run keeps every value and the key; an operator's edit and extra name survive; a name added
	// to the template later is appended with its template value.
	l, _ := dotenv.Read(f.out)
	_ = dotenv.Write(f.out, append(l, dotenv.Line{Raw: "EXTRA_SETTING=1", Key: "EXTRA_SETTING", Value: "1"}),
		map[string]string{"LOG_LEVEL": "debug"}, 0o600)
	before := readEnv(t, f.out)
	if err := os.WriteFile(f.tmpl, []byte(template+"NEW_NAME=from-template\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = f.render(t)
	after := readEnv(t, f.out)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed on a re-run", k)
		}
	}
	if after["NEW_NAME"] != "from-template" || !strings.Contains(out, "from the template: NEW_NAME") ||
		!strings.Contains(out, "kept although not in the template: EXTRA_SETTING") {
		t.Errorf("re-run: %s", out)
	}
	if strings.Contains(out, "generated on this server") {
		t.Errorf("a re-run generated values: %s", out)
	}
}

func TestRenderDryRunWritesNothing(t *testing.T) {
	f := newFixture(t, template)
	out := f.render(t, "-dry-run")
	if !strings.Contains(out, "would write") || !strings.Contains(out, "would be created") {
		t.Errorf("dry run: %s", out)
	}
	if _, err := os.Stat(filepath.Dir(f.out)); !os.IsNotExist(err) {
		t.Error("dry run created the .env directory")
	}
}

func TestRenderRefusesTheLocalTemplate(t *testing.T) {
	f := newFixture(t, strings.Replace(template, "APP_ENV=dev", "APP_ENV=local", 1))
	var out, errb bytes.Buffer
	if code := run([]string{"render", "-template", f.tmpl, "-out", f.out}, &out, &errb); code == 0 || !strings.Contains(errb.String(), "dev or prod") {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

func TestSecretOutputs(t *testing.T) {
	f := newFixture(t, template)
	f.render(t)
	env := readEnv(t, f.out)
	call := func(args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errb.String())
		}
		return out.String()
	}
	sql := call("roles-sql", "-env", f.out)
	if strings.Count(sql, "PASSWORD 'SCRAM-SHA-256$4096:") != 3 || !strings.Contains(sql, "SET log_statement = 'none';") {
		t.Errorf("roles-sql:\n%s", sql)
	}
	for _, n := range []string{"DATABASE_URL", "MIGRATE_DATABASE_URL", "ETL_DATABASE_URL"} {
		u, _ := url.Parse(env[n])
		pw, _ := u.User.Password()
		if strings.Contains(sql, pw) {
			t.Errorf("roles-sql holds the %s password in clear", n)
		}
	}
	u, _ := url.Parse(env["REDIS_URL"])
	pw, _ := u.User.Password()
	if got := call("container-env", "-env", f.out, "-service", "redis"); got != "REDISCLI_AUTH="+pw+"\n" {
		t.Error("container-env redis")
	}
	if got := call("container-env", "-env", f.out, "-service", "rabbitmq"); got != "RABBITMQ_DEFAULT_USER=logitrack\nRABBITMQ_DEFAULT_PASS="+env["RABBITMQ_DEFAULT_PASS"]+"\n" {
		t.Error("container-env rabbitmq")
	}
	if got := call("amqp-auth", "-env", f.out); got != `user = "logitrack:`+env["RABBITMQ_DEFAULT_PASS"]+"\"\n" {
		t.Error("amqp-auth")
	}
	var out, errb bytes.Buffer
	if code := run([]string{"container-env", "-env", f.out, "-service", "postgres"}, &out, &errb); code == 0 {
		t.Error("an unknown service was accepted")
	}
}

func TestRolesSQLRefusesAWrongRole(t *testing.T) {
	f := newFixture(t, template)
	f.render(t)
	l, _ := dotenv.Read(f.out)
	_ = dotenv.Write(f.out, l, map[string]string{"DATABASE_URL": "postgres://postgres:secretvalue@127.0.0.1:5432/logitrack"}, 0o600)
	var out, errb bytes.Buffer
	if code := run([]string{"roles-sql", "-env", f.out}, &out, &errb); code == 0 || !strings.Contains(errb.String(), "logitrack_app") ||
		strings.Contains(errb.String(), "secretvalue") {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

// A JWT_SIGNING_KEY_FILE the app user wrote into the .env must not make root create a file elsewhere
// (provision.sh runs render as root, then hands the key to the app user's group).
func TestRenderRefusesAKeyOutsideTheSecretsDir(t *testing.T) {
	const secretsDir = "/opt/logitrack/shared/secrets"
	f := newFixture(t, template)
	f.render(t, "-secrets-dir", secretsDir)
	for _, bad := range []string{"/etc/ld.so.preload", "/opt/logitrack/shared/secrets/../.env.x", "/opt/logitrack/shared/secrets/sub/k.pem",
		"/opt/logitrack/shared/secrets//jwt.pem", "relative.pem"} {
		l, _ := dotenv.Read(f.out)
		_ = dotenv.Write(f.out, l, map[string]string{"JWT_SIGNING_KEY_FILE": bad}, 0o600)
		var out, errb bytes.Buffer
		code := run([]string{"render", "-template", f.tmpl, "-out", f.out, "-prefix", f.dir, "-secrets-dir", secretsDir}, &out, &errb)
		if code == 0 || !strings.Contains(errb.String(), "JWT_SIGNING_KEY_FILE") {
			t.Errorf("%s: exit %d: %s", bad, code, errb.String())
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, "etc")); !os.IsNotExist(err) {
		t.Error("a key was created outside the secrets directory")
	}
	// Another file directly in the directory is fine (key rotation keeps the directory).
	l, _ := dotenv.Read(f.out)
	_ = dotenv.Write(f.out, l, map[string]string{"JWT_SIGNING_KEY_FILE": secretsDir + "/jwt-2.pem"}, 0o600)
	f.render(t, "-secrets-dir", secretsDir)
	if _, err := loadKey(filepath.Join(f.dir, secretsDir, "jwt-2.pem")); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"render", "-template", f.tmpl, "-out", f.out, "-secrets-dir", "opt/x"}, &out, &errb); code == 0 {
		t.Error("a relative -secrets-dir was accepted")
	}
}

// A key file replaced by a symbolic link, dangling or not, is refused rather than read or created through.
func TestRenderRefusesASymlinkedKey(t *testing.T) {
	f := newFixture(t, template)
	f.render(t)
	keyFile := filepath.Join(f.dir, readEnv(t, f.out)["JWT_SIGNING_KEY_FILE"])
	target := filepath.Join(f.dir, "elsewhere.pem")
	if err := os.Rename(keyFile, target); err != nil {
		t.Fatal(err)
	}
	for name, link := range map[string]string{"to a key": target, "dangling": filepath.Join(f.dir, "missing.pem")} {
		_ = os.Remove(keyFile)
		if err := os.Symlink(link, keyFile); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		code := run([]string{"render", "-template", f.tmpl, "-out", f.out, "-prefix", f.dir}, &out, &errb)
		if code == 0 || !strings.Contains(errb.String(), "not a regular file") {
			t.Errorf("%s: exit %d: %s", name, code, errb.String())
		}
	}
	if _, err := os.Lstat(filepath.Join(f.dir, "missing.pem")); !os.IsNotExist(err) {
		t.Error("a key was created through a dangling link")
	}
}
