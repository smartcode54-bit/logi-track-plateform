// Command vmenv prepares the environment of the staging VM (deploy/vm, developer-spec.md §17.4) on the
// VM itself: provision.sh cross-compiles nothing, it runs this linux/amd64 binary from its kit as root.
// Every infra secret is generated here, on the server, and written only to the files named on the
// command line or to a pipe; no value is ever printed to a terminal or a log (§16.5).
//
//	vmenv render -template env.vm.example -out /opt/logitrack/shared/.env [-prefix DIR] [-dry-run]
//	      [-secrets-dir DIR] [-pg-addr HOST:PORT] [-pg-db NAME] [-redis-addr HOST:PORT] [-amqp-addr HOST:PORT]
//	    Writes the VM's .env (mode 0600) from the names-only template: an existing value is kept, a missing
//	    one comes from the template, and the generated names (role passwords inside the three database
//	    URLs, the Redis and RabbitMQ passwords inside their URLs, LOCAL_MEDIA_SIGNING_KEY, API_KEY_PEPPER,
//	    BOOTSTRAP_ADMIN_PASSWORD, SEED_DEFAULT_PASSWORD, OWN_FLEET_TENANT_ID) are created when missing or
//	    empty. The Ed25519 key of JWT_SIGNING_KEY_FILE is created when missing and JWT_ACTIVE_KID is its
//	    RFC 7638 thumbprint, computed by internal/auth/token as the api does. Prints names only.
//	    With -secrets-dir (provision.sh passes /opt/logitrack/shared/secrets), JWT_SIGNING_KEY_FILE must
//	    name a file directly in DIR: root creates that file, so a value pointing anywhere else (the .env is
//	    read before anything else) is refused. A key file that is a symbolic link is always refused.
//	vmenv roles-sql -env FILE
//	    ALTER ROLE statements with SCRAM-SHA-256 verifiers for the LOGIN roles of the three database URLs,
//	    for `psql` as the superuser (stdin). Refuses a terminal.
//	vmenv container-env -env FILE -service redis|rabbitmq
//	    The --env-file of a container (REDISCLI_AUTH, or RABBITMQ_DEFAULT_USER/PASS). Refuses a terminal.
//	vmenv amqp-auth -env FILE
//	    The curl config line `user = "name:pass"` of the RabbitMQ management API, for `curl -K -`.
//	    Refuses a terminal.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/tools/internal/dotenv"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/tools/internal/scram"
)

const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// dbRoles maps each database URL to the LOGIN role it must use (R66, R87).
var dbRoles = []struct{ env, role string }{
	{"DATABASE_URL", "logitrack_app"},
	{"MIGRATE_DATABASE_URL", "logitrack_migrator"},
	{"ETL_DATABASE_URL", "logitrack_etl"},
}

// randomSecrets are the plain random secrets and their lengths (all ASCII from alphabet).
var randomSecrets = map[string]int{
	"LOCAL_MEDIA_SIGNING_KEY":  48, // at least 32 bytes (storage.MinSigningKeyBytes)
	"API_KEY_PEPPER":           48,
	"BOOTSTRAP_ADMIN_PASSWORD": 24, // at least PASSWORD_MIN_LENGTH
	"SEED_DEFAULT_PASSWORD":    20,
	"RABBITMQ_DEFAULT_PASS":    32,
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: vmenv render -template FILE -out FILE [-prefix DIR] [-dry-run] [-pg-addr A] [-pg-db N] [-redis-addr A] [-amqp-addr A]
       vmenv roles-sql -env FILE
       vmenv container-env -env FILE -service redis|rabbitmq
       vmenv amqp-auth -env FILE
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "render":
		err = cmdRender(args[1:], stdout)
	case "roles-sql":
		err = cmdSecretOut(args[1:], stdout, "roles-sql", func(env map[string]string, _ string) (string, error) { return rolesSQL(env) })
	case "container-env":
		err = cmdSecretOut(args[1:], stdout, "container-env", containerEnv)
	case "amqp-auth":
		err = cmdSecretOut(args[1:], stdout, "amqp-auth", func(env map[string]string, _ string) (string, error) { return amqpAuth(env) })
	case "-h", "-help", "--help", "help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "vmenv:", err)
		return 1
	}
	return 0
}

// renderOptions are the flags of render.
type renderOptions struct {
	template, out, prefix, secretsDir string
	pgAddr, pgDB, redisAddr, amqpAddr string
	dryRun                            bool
}

func cmdRender(args []string, stdout io.Writer) error {
	var o renderOptions
	fl := flag.NewFlagSet("render", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	fl.StringVar(&o.template, "template", "", "")
	fl.StringVar(&o.out, "out", "", "")
	fl.StringVar(&o.prefix, "prefix", "", "")
	fl.StringVar(&o.secretsDir, "secrets-dir", "", "")
	fl.StringVar(&o.pgAddr, "pg-addr", "127.0.0.1:5432", "")
	fl.StringVar(&o.pgDB, "pg-db", "logitrack", "")
	fl.StringVar(&o.redisAddr, "redis-addr", "127.0.0.1:6381", "")
	fl.StringVar(&o.amqpAddr, "amqp-addr", "127.0.0.1:5674", "")
	fl.BoolVar(&o.dryRun, "dry-run", false, "")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if o.template == "" || o.out == "" || fl.NArg() != 0 {
		return errors.New("render needs -template and -out")
	}
	if o.secretsDir != "" && (!filepath.IsAbs(o.secretsDir) || filepath.Clean(o.secretsDir) != o.secretsDir) {
		return errors.New("-secrets-dir must be a clean absolute path")
	}
	for _, a := range []string{o.pgAddr, o.redisAddr, o.amqpAddr} {
		if _, _, err := net.SplitHostPort(a); err != nil {
			return errors.New("-pg-addr, -redis-addr and -amqp-addr must be host:port")
		}
	}
	tmpl, err := dotenv.Read(o.template)
	if err != nil {
		return err
	}
	var existing []dotenv.Line
	if old, err := dotenv.Read(o.out); err == nil {
		existing = old
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	plan, err := buildPlan(tmpl, existing, o)
	if err != nil {
		return err
	}
	verb := "wrote"
	if o.dryRun {
		verb = "would write"
	} else {
		if plan.createKey {
			if err := writeKey(plan.keyPath, plan.key); err != nil {
				return err
			}
		}
		if err := writeAtomic(o.out, dotenv.Render(plan.lines, plan.values)); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(stdout, "vmenv: %s %s (mode 0600): %d names; kept %d existing values\n", verb, o.out, len(plan.values), len(plan.kept))
	if len(plan.generated) > 0 {
		_, _ = fmt.Fprintf(stdout, "vmenv: generated on this server (never printed): %s\n", strings.Join(plan.generated, ", "))
	}
	if len(plan.added) > 0 {
		_, _ = fmt.Fprintf(stdout, "vmenv: from the template: %s\n", strings.Join(plan.added, ", "))
	}
	if len(plan.extra) > 0 {
		_, _ = fmt.Fprintf(stdout, "vmenv: kept although not in the template: %s\n", strings.Join(plan.extra, ", "))
	}
	switch {
	case plan.keyPath == "":
	case plan.createKey && o.dryRun:
		_, _ = fmt.Fprintf(stdout, "vmenv: JWT signing key would be created at %s (Ed25519, mode 0600); JWT_ACTIVE_KID = its RFC 7638 thumbprint\n", plan.keyPath)
	case plan.createKey:
		_, _ = fmt.Fprintf(stdout, "vmenv: JWT signing key created at %s (Ed25519, mode 0600); JWT_ACTIVE_KID = its RFC 7638 thumbprint\n", plan.keyPath)
	default:
		_, _ = fmt.Fprintf(stdout, "vmenv: JWT signing key %s kept; JWT_ACTIVE_KID follows its thumbprint\n", plan.keyPath)
	}
	return nil
}

// plan is the outcome of render before anything is written.
type plan struct {
	lines                         []dotenv.Line
	values                        map[string]string
	kept, generated, added, extra []string
	keyPath                       string
	createKey                     bool
	key                           ed25519.PrivateKey
}

func buildPlan(tmpl, existing []dotenv.Line, o renderOptions) (*plan, error) {
	tv := dotenv.Map(tmpl)
	switch tv["APP_ENV"] {
	case "dev", "prod":
	default:
		return nil, errors.New("the template must set APP_ENV to dev or prod (this tool is for the VM, not the local stack)")
	}
	old := dotenv.Map(existing)
	p := &plan{values: map[string]string{}, lines: slices.Clone(tmpl)}
	inTemplate := map[string]bool{}
	for _, l := range tmpl {
		if l.Key != "" {
			inTemplate[l.Key] = true
		}
	}
	// Existing names the template does not list are kept at the end of the file.
	var extraLines []dotenv.Line
	for _, l := range existing {
		if l.Key != "" && !inTemplate[l.Key] {
			p.extra = append(p.extra, l.Key)
			extraLines = append(extraLines, dotenv.Line{Raw: l.Key + "=" + l.Value, Key: l.Key, Value: l.Value})
			p.values[l.Key] = l.Value
		}
	}
	if len(extraLines) > 0 {
		p.lines = append(p.lines, dotenv.Line{Raw: ""}, dotenv.Line{Raw: "# Kept from the previous .env (not in env.vm.example):"})
		p.lines = append(p.lines, extraLines...)
	}

	generated := func(k string) bool {
		if _, ok := randomSecrets[k]; ok {
			return true
		}
		switch k {
		case "DATABASE_URL", "MIGRATE_DATABASE_URL", "ETL_DATABASE_URL", "REDIS_URL", "RABBITMQ_URL", "OWN_FLEET_TENANT_ID", "JWT_ACTIVE_KID":
			return true
		}
		return false
	}
	// First pass: keep existing values; template values for names that are not generated.
	for _, l := range tmpl {
		k := l.Key
		if k == "" {
			continue
		}
		if v, ok := old[k]; ok && (v != "" || !generated(k)) {
			p.values[k] = v
			p.kept = append(p.kept, k)
			continue
		}
		if !generated(k) {
			p.values[k] = tv[k]
			if _, had := old[k]; !had && len(existing) > 0 {
				p.added = append(p.added, k)
			}
		}
	}
	gen := func(k, v string) {
		p.values[k] = v
		p.generated = append(p.generated, k)
	}
	need := func(k string) bool { _, ok := p.values[k]; return inTemplate[k] && !ok }
	for k, n := range randomSecrets {
		if need(k) {
			gen(k, random(n))
		}
	}
	for _, r := range dbRoles {
		if need(r.env) {
			gen(r.env, (&url.URL{Scheme: "postgres", User: url.UserPassword(r.role, random(32)), Host: o.pgAddr,
				Path: "/" + o.pgDB, RawQuery: "sslmode=disable"}).String())
		}
	}
	if need("REDIS_URL") {
		gen("REDIS_URL", (&url.URL{Scheme: "redis", User: url.UserPassword("", random(32)), Host: o.redisAddr, Path: "/0"}).String())
	}
	if need("RABBITMQ_URL") {
		user, pass := p.values["RABBITMQ_DEFAULT_USER"], p.values["RABBITMQ_DEFAULT_PASS"]
		if user == "" || pass == "" {
			return nil, errors.New("RABBITMQ_URL needs RABBITMQ_DEFAULT_USER (template) and RABBITMQ_DEFAULT_PASS")
		}
		gen("RABBITMQ_URL", (&url.URL{Scheme: "amqp", User: url.UserPassword(user, pass), Host: o.amqpAddr, Path: "/"}).String())
	}
	if need("OWN_FLEET_TENANT_ID") {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, err
		}
		gen("OWN_FLEET_TENANT_ID", id.String())
	}
	// JWT_ACTIVE_KID always follows the key of JWT_SIGNING_KEY_FILE (the api refuses a mismatch).
	if keyFile := p.values["JWT_SIGNING_KEY_FILE"]; keyFile != "" {
		if !filepath.IsAbs(keyFile) {
			return nil, errors.New("JWT_SIGNING_KEY_FILE must be an absolute path")
		}
		// Root creates this file and provision.sh hands it to the app user's group: only a file directly in
		// the secrets directory qualifies, whatever the .env says.
		if o.secretsDir != "" && (filepath.Clean(keyFile) != keyFile || filepath.Dir(keyFile) != o.secretsDir) {
			return nil, fmt.Errorf("JWT_SIGNING_KEY_FILE must name a file directly in %s", o.secretsDir)
		}
		p.keyPath = filepath.Join(o.prefix, keyFile)
		pub, err := loadKey(p.keyPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
			var priv ed25519.PrivateKey
			if pub, priv, err = ed25519.GenerateKey(rand.Reader); err != nil {
				return nil, err
			}
			p.createKey, p.key = true, priv
		case err != nil:
			return nil, err
		}
		kid := token.Thumbprint(pub)
		if inTemplate["JWT_ACTIVE_KID"] {
			if p.values["JWT_ACTIVE_KID"] != kid {
				p.kept = slices.DeleteFunc(p.kept, func(s string) bool { return s == "JWT_ACTIVE_KID" })
				gen("JWT_ACTIVE_KID", kid)
			}
		}
	}
	for _, k := range []string{"DATABASE_URL", "MIGRATE_DATABASE_URL", "ETL_DATABASE_URL", "REDIS_URL", "RABBITMQ_URL"} {
		if !inTemplate[k] {
			continue
		}
		if err := checkURL(k, p.values[k]); err != nil {
			return nil, err
		}
	}
	sort.Strings(p.generated)
	sort.Strings(p.added)
	sort.Strings(p.extra)
	return p, nil
}

// checkURL refuses a URL that would break the generated container configuration; messages name the variable.
func checkURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return fmt.Errorf("%s: not a URL with credentials", name)
	}
	pw, _ := u.User.Password()
	if pw == "" || strings.ContainsAny(pw+u.User.Username(), "\"\\\n\r") {
		return fmt.Errorf("%s: the password is empty or holds a quote, backslash or newline", name)
	}
	for _, r := range dbRoles {
		if r.env == name && u.User.Username() != r.role {
			return fmt.Errorf("%s must log in as %s (R66)", name, r.role)
		}
	}
	return nil
}

func random(n int) string {
	b := make([]byte, n)
	for i := range b {
		k, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		b[i] = alphabet[k.Int64()]
	}
	return string(b)
}

// loadKey returns the public half of an existing PKCS#8 PEM Ed25519 key (os.ErrNotExist when there is none).
// A symbolic link is refused, dangling or not: root must never create or read a key through a link.
func loadKey(path string) (ed25519.PublicKey, error) {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return nil, errors.New("JWT_SIGNING_KEY_FILE: not a regular file (a symbolic link or another kind of file)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(raw)
	if blk == nil || blk.Type != "PRIVATE KEY" {
		return nil, errors.New("JWT_SIGNING_KEY_FILE: not a PKCS#8 PEM private key")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, errors.New("JWT_SIGNING_KEY_FILE: not a PKCS#8 PEM private key")
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("JWT_SIGNING_KEY_FILE: not an Ed25519 key")
	}
	return priv.Public().(ed25519.PublicKey), nil
}

// writeAtomic replaces path with content through a 0600 temporary file in the same directory (fsync, rename),
// so an interrupted run never leaves a truncated .env behind.
func writeAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".env.vmenv-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// writeKey creates the key file (never replaces one) with mode 0600 in a 0700 directory.
func writeKey(path string, priv ed25519.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// cmdSecretOut runs a command whose output holds secrets: it reads -env and refuses to write to a terminal.
func cmdSecretOut(args []string, stdout io.Writer, name string, render func(map[string]string, string) (string, error)) error {
	fl := flag.NewFlagSet(name, flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	envPath := fl.String("env", "", "")
	service := fl.String("service", "", "")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *envPath == "" || fl.NArg() != 0 {
		return fmt.Errorf("%s needs -env", name)
	}
	if isTerminal(stdout) {
		return fmt.Errorf("%s writes secrets: redirect stdout to a file or a pipe", name)
	}
	lines, err := dotenv.Read(*envPath)
	if err != nil {
		return err
	}
	out, err := render(dotenv.Map(lines), *service)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, out)
	return err
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// rolesSQL sets the LOGIN passwords of the three roles from their URLs, as SCRAM verifiers, in a session that
// logs no statement (log_statement, log_min_error_statement, pg_stat_statements utility tracking).
func rolesSQL(env map[string]string) (string, error) {
	var b strings.Builder
	b.WriteString("SET log_statement = 'none';\nSET log_min_duration_statement = -1;\nSET log_min_error_statement = panic;\n" +
		"SET pg_stat_statements.track_utility = off;\n")
	for _, r := range dbRoles {
		if err := checkURL(r.env, env[r.env]); err != nil {
			return "", err
		}
		u, _ := url.Parse(env[r.env])
		pw, _ := u.User.Password()
		v, err := scram.Verifier(pw)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "ALTER ROLE %s WITH PASSWORD '%s';\n", r.role, v)
	}
	return b.String(), nil
}

func containerEnv(env map[string]string, service string) (string, error) {
	switch service {
	case "redis":
		if err := checkURL("REDIS_URL", env["REDIS_URL"]); err != nil {
			return "", err
		}
		u, _ := url.Parse(env["REDIS_URL"])
		pw, _ := u.User.Password()
		// redis-cli reads REDISCLI_AUTH (the healthcheck); the container command writes requirepass from it.
		return "REDISCLI_AUTH=" + pw + "\n", nil
	case "rabbitmq":
		user, pass, err := rabbitCreds(env)
		if err != nil {
			return "", err
		}
		return "RABBITMQ_DEFAULT_USER=" + user + "\nRABBITMQ_DEFAULT_PASS=" + pass + "\n", nil
	}
	return "", errors.New("-service must be redis or rabbitmq")
}

func amqpAuth(env map[string]string) (string, error) {
	user, pass, err := rabbitCreds(env)
	if err != nil {
		return "", err
	}
	return `user = "` + user + ":" + pass + "\"\n", nil
}

func rabbitCreds(env map[string]string) (string, string, error) {
	user, pass := env["RABBITMQ_DEFAULT_USER"], env["RABBITMQ_DEFAULT_PASS"]
	if user == "" || pass == "" {
		return "", "", errors.New("RABBITMQ_DEFAULT_USER and RABBITMQ_DEFAULT_PASS must be set (run render first)")
	}
	if strings.ContainsAny(user+pass, "\"\\\n\r:") {
		return "", "", errors.New("RABBITMQ_DEFAULT_USER, RABBITMQ_DEFAULT_PASS: no quote, backslash, colon or newline")
	}
	return user, pass, nil
}
