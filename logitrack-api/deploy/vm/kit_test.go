// Tests of the staging VM kit (deploy/vm, issue OPSVM). The scripts run under bash against a temporary
// tree (--prefix) with every external command replaced by testdata/stub.sh, so nothing here touches the
// machine, the network or a VM. The integration-tagged file adds nginx -t and shellcheck in containers.
package vm_test

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

// The commands the kit calls; each becomes a link to testdata/stub.sh.
var stubbed = []string{"id", "useradd", "chown", "runuser", "psql", "docker", "curl", "systemctl", "nginx", "ss", "ssh", "go"}

// Generated names whose values are secrets (vmenv), and the URLs that embed a generated password.
var (
	secretNames = []string{"RABBITMQ_DEFAULT_PASS", "LOCAL_MEDIA_SIGNING_KEY", "API_KEY_PEPPER", "BOOTSTRAP_ADMIN_PASSWORD", "SEED_DEFAULT_PASSWORD"}
	urlNames    = []string{"DATABASE_URL", "MIGRATE_DATABASE_URL", "ETL_DATABASE_URL", "REDIS_URL", "RABBITMQ_URL"}
)

func kitDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd() // go test runs in the package directory: logitrack-api/deploy/vm
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

func needBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
}

var (
	vmenvOnce sync.Once
	vmenvPath string
	vmenvErr  error
)

// vmenv builds tools/vmenv for this machine once per test binary.
func vmenv(t *testing.T) string {
	t.Helper()
	vmenvOnce.Do(func() {
		dir, err := os.MkdirTemp("", "vmenv-test-")
		if err != nil {
			vmenvErr = err
			return
		}
		vmenvPath = filepath.Join(dir, "vmenv")
		cmd := exec.Command("go", "build", "-o", vmenvPath, "./tools/vmenv")
		cmd.Dir = filepath.Join(kitDir(t), "..", "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			vmenvErr = errors.New(string(out))
		}
	})
	if vmenvErr != nil {
		t.Fatalf("build tools/vmenv: %v", vmenvErr)
	}
	return vmenvPath
}

// readKit parses kit.conf (kit_name=value).
func readKit(t *testing.T) map[string]string {
	t.Helper()
	kit := map[string]string{}
	for _, l := range lines(t, filepath.Join(kitDir(t), "kit.conf")) {
		if k, v, ok := strings.Cut(l, "="); ok && strings.HasPrefix(k, "kit_") {
			kit[k] = v
		}
	}
	return kit
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// dotenv parses KEY=VALUE lines.
func dotenv(t *testing.T, path string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, l := range lines(t, path) {
		k, v, _ := strings.Cut(l, "=")
		m[k] = v
	}
	return m
}

// names reads env-names/<program>.txt.
func names(t *testing.T, program string) []string {
	return lines(t, filepath.Join(kitDir(t), "env-names", program+".txt"))
}

// perProcess are the names env.vm.example declares as set by ecosystem.config.cjs.
func perProcess(t *testing.T) []string {
	t.Helper()
	raw, _ := os.ReadFile(filepath.Join(kitDir(t), "env.vm.example"))
	m := regexp.MustCompile(`(?m)^#\s*per-process \(ecosystem\.config\.cjs\):(.*)$`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("env.vm.example has no per-process line")
	}
	f := strings.Fields(m[1])
	sort.Strings(f)
	return f
}

// processEnv is the per-process environment ecosystem.config.cjs gives each pm2 app, derived from kit.conf
// the same way (TestEcosystem compares the two).
func processEnv(t *testing.T) map[string]map[string]string {
	k := readKit(t)
	lb := func(p string) string { return "127.0.0.1:" + k[p] }
	return map[string]map[string]string{
		"api": {"API_INTERNAL_ADDR": lb("kit_api_internal_port"), "API_PUBLIC_ADDR": lb("kit_api_public_port"),
			"METRICS_ADDR": lb("kit_api_metrics_port")},
		"worker":    {"METRICS_ADDR": lb("kit_worker_metrics_port")},
		"scheduler": {"METRICS_ADDR": lb("kit_scheduler_metrics_port")},
		"web": {"GO_API_INTERNAL_URL": "http://" + lb("kit_api_internal_port"), "PORT": k["kit_web_port"],
			"HOSTNAME": "127.0.0.1", "NODE_ENV": "production"},
		"migrate": {}, "seed": {},
	}
}

// renderEnv writes a real .env with vmenv (generated secrets) under dir and returns its path and values.
func renderEnv(t *testing.T, prefix string) (string, map[string]string) {
	t.Helper()
	out := filepath.Join(prefix, "opt", "logitrack", "shared", ".env")
	cmd := exec.Command(vmenv(t), "render", "-template", filepath.Join(kitDir(t), "env.vm.example"), "-out", out, "-prefix", prefix)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("vmenv render: %v\n%s", err, b)
	}
	return out, dotenv(t, out)
}

// secrets lists every generated secret of an .env: the plain secrets, the passwords inside the URLs and the
// URLs themselves, and the base64 body of the JWT key.
func secrets(t *testing.T, env map[string]string, keyFile string) []string {
	t.Helper()
	var out []string
	for _, n := range secretNames {
		if env[n] == "" {
			t.Fatalf("%s was not generated", n)
		}
		out = append(out, env[n])
	}
	for _, n := range urlNames {
		u, err := url.Parse(env[n])
		if err != nil || u.User == nil {
			t.Fatalf("%s is not a URL with credentials", n)
		}
		pw, _ := u.User.Password()
		out = append(out, env[n], pw)
	}
	if keyFile != "" {
		raw, err := os.ReadFile(keyFile)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(string(raw), "\n") {
			if l != "" && !strings.HasPrefix(l, "-----") {
				out = append(out, l)
			}
		}
	}
	return out
}

func assertNoSecret(t *testing.T, what, text string, secrets []string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(text, s) {
			t.Errorf("%s holds a generated secret (%d characters)", what, len(s))
		}
	}
}

// sandbox is a fake VM: a --prefix tree, stub commands first on PATH, and the stubs' state directory.
type sandbox struct {
	prefix, state, bin, tmp string
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	needBash(t)
	base := t.TempDir()
	s := &sandbox{prefix: filepath.Join(base, "root"), state: filepath.Join(base, "state"),
		bin: filepath.Join(base, "bin"), tmp: filepath.Join(base, "tmp")}
	stub, err := filepath.Abs(filepath.Join("testdata", "stub.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{s.prefix, s.state, s.bin, s.tmp} {
		mustMkdir(t, d)
	}
	for _, c := range stubbed {
		if err := os.Symlink(stub, filepath.Join(s.bin, c)); err != nil {
			t.Fatal(err)
		}
	}
	// The VM as provision.sh finds it: node and pm2 of the other apps, the origin certificate, nginx.
	nodeBin := filepath.Join(s.prefix, "opt", "node-v24.14.0", "bin")
	mustMkdir(t, nodeBin)
	for _, c := range []string{"node", "pm2"} {
		if err := os.Symlink(stub, filepath.Join(nodeBin, c)); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(s.prefix, "etc", "ssl", "certs", "showkhun.co.pem"), "certificate", 0o644)
	writeFile(t, filepath.Join(s.prefix, "etc", "ssl", "private", "showkhun.co.key"), "key", 0o600)
	for _, d := range []string{"sites-available", "sites-enabled", "snippets", "conf.d"} {
		mustMkdir(t, filepath.Join(s.prefix, "etc", "nginx", d))
	}
	writeFile(t, filepath.Join(s.state, "calls.log"), "", 0o644)
	return s
}

func (s *sandbox) env() []string {
	return append(os.Environ(), "PATH="+s.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB_STATE="+s.state, "TMPDIR="+s.tmp)
}

// run executes a kit script and returns its combined output and exit code.
func (s *sandbox) run(t *testing.T, script string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = s.env()
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func (s *sandbox) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.state, name))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(b)
}

func (s *sandbox) resetCalls(t *testing.T) {
	writeFile(t, filepath.Join(s.state, "calls.log"), "", 0o644)
}

func mustMkdir(t *testing.T, d string) {
	t.Helper()
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestScriptsParse(t *testing.T) {
	needBash(t)
	for _, f := range []string{"provision.sh", "deploy.sh", "rollout.sh", "run-app.sh", "kit.conf"} {
		if out, err := exec.Command("bash", "-n", f).CombinedOutput(); err != nil {
			t.Errorf("bash -n %s: %v\n%s", f, err, out)
		}
	}
	for _, f := range []string{"provision.sh", "deploy.sh", "rollout.sh", "run-app.sh"} {
		st, err := os.Stat(f)
		if err != nil || st.Mode()&0o111 == 0 {
			t.Errorf("%s must be executable", f)
		}
	}
}

// TestTemplateStartsEveryProgram: the .env provision.sh writes from env.vm.example, filtered by each
// program's allow-list and completed by its per-process values, loads in the real configuration of api,
// worker, scheduler, migrate and seed, with STORAGE_BACKEND=local and every S3_* empty; and every name
// those configurations read is in the program's allow-list.
func TestTemplateStartsEveryProgram(t *testing.T) {
	_, env := renderEnv(t, t.TempDir())
	if env["STORAGE_BACKEND"] != "local" || env["APP_ENV"] != "dev" {
		t.Fatalf("STORAGE_BACKEND=%s APP_ENV=%s", env["STORAGE_BACKEND"], env["APP_ENV"])
	}
	for k, v := range env {
		// The two TTLs also time the local backend's signed URLs; every connection setting stays empty.
		if strings.HasPrefix(k, "S3_") && !strings.HasSuffix(k, "_TTL") && v != "" {
			t.Errorf("%s is set; the first deployment runs without S3", k)
		}
	}
	if env["AUTH_FIREBASE_BRIDGE_MODE"] != "off" || env["FCM_ENABLED"] != "false" {
		t.Error("the Firebase bridge and FCM stay off until their files are placed")
	}
	procEnv := processEnv(t)
	environ := func(program string) []string {
		var out []string
		for _, n := range names(t, program) {
			if v, ok := procEnv[program][n]; ok {
				out = append(out, n+"="+v)
			} else if v, ok := env[n]; ok {
				out = append(out, n+"="+v)
			}
		}
		return out
	}
	load := map[string]func([]string) (map[string]string, error){
		"api":       loader[app.APIConfig],
		"worker":    loader[app.WorkerConfig],
		"scheduler": loader[app.SchedulerConfig],
		"migrate":   loader[app.MigrateConfig],
		"seed":      loader[app.SeedConfig],
	}
	for program, f := range load {
		declared, err := f(environ(program))
		if err != nil {
			t.Errorf("%s does not start with the VM environment: %v", program, err)
			continue
		}
		allowed := names(t, program)
		for n := range declared {
			// Common declares OTEL_* for every binary, but only api, worker and scheduler export traces
			// (§16.1 consumers); the one-shot programs run with them unset, as in compose.
			if (program == "migrate" || program == "seed") && strings.HasPrefix(n, "OTEL_") {
				continue
			}
			if !slices.Contains(allowed, n) {
				t.Errorf("%s reads %s, which env-names/%s.txt does not export", program, n, program)
			}
		}
	}
}

func loader[T any](environ []string) (map[string]string, error) {
	if _, err := config.LoadFrom[T](environ); err != nil {
		return nil, err
	}
	return config.Describe[T](), nil
}

// TestEcosystem loads ecosystem.config.cjs in node: four apps through run-app.sh, per-process values from
// kit.conf, the web with web-server names only and no Go app with a web-only name.
func TestEcosystem(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	out, err := exec.Command(node, "-e", `process.stdout.write(JSON.stringify(require(process.argv[1])))`,
		filepath.Join(kitDir(t), "ecosystem.config.cjs")).Output()
	if err != nil {
		t.Fatalf("node cannot load ecosystem.config.cjs: %v", err)
	}
	var raw struct {
		Apps []map[string]any `json:"apps"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"logitrack-api": "api", "logitrack-worker": "worker", "logitrack-scheduler": "scheduler", "logitrack-web": "web"}
	if len(raw.Apps) != len(want) {
		t.Fatalf("%d apps, want %d", len(raw.Apps), len(want))
	}
	webOnly := map[string]bool{}
	goNames := map[string]bool{}
	for _, p := range []string{"api", "worker", "scheduler", "migrate", "seed"} {
		for _, n := range names(t, p) {
			goNames[n] = true
		}
	}
	for _, n := range names(t, "web") {
		if !goNames[n] {
			webOnly[n] = true
		}
	}
	procEnv := processEnv(t)
	var declared []string
	for _, a := range raw.Apps {
		name, _ := a["name"].(string)
		program, ok := want[name]
		if !ok {
			t.Errorf("unexpected app %s", name)
			continue
		}
		args, _ := a["args"].([]any)
		if a["script"] != "/opt/logitrack/current/deploy/vm/run-app.sh" || len(args) != 1 || args[0] != program ||
			a["interpreter"] != "/bin/bash" {
			t.Errorf("%s: script %v args %v interpreter %v", name, a["script"], args, a["interpreter"])
		}
		if !strings.HasPrefix(a["out_file"].(string), "/opt/logitrack/shared/logs/") {
			t.Errorf("%s logs to %v", name, a["out_file"])
		}
		env, _ := a["env"].(map[string]any)
		got := map[string]string{}
		for k, v := range env {
			got[k], _ = v.(string)
			declared = append(declared, k)
			if program != "web" && webOnly[k] {
				t.Errorf("%s gets the web name %s", name, k)
			}
			if !slices.Contains(names(t, program), k) {
				t.Errorf("%s gets %s, which is not one of its §16.1 names", name, k)
			}
		}
		if len(got) != len(procEnv[program]) {
			t.Errorf("%s env = %v, want %v", name, got, procEnv[program])
		}
		for k, v := range procEnv[program] {
			if got[k] != v {
				t.Errorf("%s %s = %q, want %q", name, k, got[k], v)
			}
		}
	}
	sort.Strings(declared)
	declared = slices.Compact(declared)
	if !slices.Equal(declared, perProcess(t)) {
		t.Errorf("ecosystem env names %v differ from the per-process line of env.vm.example %v", declared, perProcess(t))
	}
}

// TestNginxMatchesKit: the vhosts proxy only to the web and the public listener of kit.conf, never to the
// internal one; the body limit equals UPLOAD_MAX_BYTES; the certificate paths are kit.conf's.
func TestNginxMatchesKit(t *testing.T) {
	k := readKit(t)
	tmpl := dotenv(t, filepath.Join(kitDir(t), "env.vm.example"))
	if tmpl["UPLOAD_MAX_BYTES"] != "10485760" {
		t.Fatalf("UPLOAD_MAX_BYTES = %s; client_max_body_size 10m must follow it", tmpl["UPLOAD_MAX_BYTES"])
	}
	proxyRx := regexp.MustCompile(`proxy_pass\s+http://127\.0\.0\.1:(\d+)`)
	for host, want := range map[string][]string{
		k["kit_web_host"]: {k["kit_web_port"], k["kit_api_public_port"]},
		k["kit_api_host"]: {k["kit_api_public_port"]},
	} {
		raw, err := os.ReadFile(filepath.Join("nginx", host+".conf"))
		if err != nil {
			t.Fatal(err)
		}
		conf := string(raw)
		var ports []string
		for _, m := range proxyRx.FindAllStringSubmatch(conf, -1) {
			ports = append(ports, m[1])
		}
		sort.Strings(ports)
		ports = slices.Compact(ports)
		sort.Strings(want)
		if !slices.Equal(ports, want) {
			t.Errorf("%s proxies to ports %v, want %v", host, ports, want)
		}
		if strings.Contains(conf, ":"+k["kit_api_internal_port"]) {
			t.Errorf("%s mentions the internal listener", host)
		}
		for _, s := range []string{"client_max_body_size 10m;", "ssl_certificate " + k["kit_tls_cert"] + ";",
			"ssl_certificate_key " + k["kit_tls_key"] + ";", "server_name " + host + ";", "return 301 https://",
			"include /etc/nginx/snippets/logitrack-cloudflare-realip.conf;", "listen 443 ssl http2;"} {
			if !strings.Contains(conf, s) {
				t.Errorf("%s lacks %q", host, s)
			}
		}
	}
	// Every Cloudflare range listed, CF-Connecting-IP as the source.
	realip := string(must(os.ReadFile(filepath.Join("nginx", "logitrack-cloudflare-realip.conf"))))
	if strings.Count(realip, "set_real_ip_from ") != 22 || !strings.Contains(realip, "real_ip_header CF-Connecting-IP;") {
		t.Error("logitrack-cloudflare-realip.conf: want the 22 published ranges and real_ip_header CF-Connecting-IP")
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// TestRunAppEnvironment: each program receives exactly its allow-listed names from the .env (other names
// are unset even when inherited), keeps its per-process values, and the wrapper prints no value.
func TestRunAppEnvironment(t *testing.T) {
	s := newSandbox(t)
	_, env := renderEnv(t, s.prefix)
	rel := fakeRelease(t, s, "0123456789abcdef0123456789abcdef01234567")
	sec := secrets(t, env, "")
	procEnv := processEnv(t)
	for _, program := range []string{"api", "worker", "scheduler", "migrate", "seed", "web"} {
		cmd := exec.Command("bash", filepath.Join(rel, "deploy", "vm", "run-app.sh"), program, "--flag")
		base := s.env()
		// An inherited secret of another program must not survive (the web never sees DATABASE_URL).
		base = append(base, "DATABASE_URL=inherited", "JWT_ISSUER=inherited")
		for k, v := range procEnv[program] {
			base = append(base, k+"="+v)
		}
		cmd.Env = base
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run-app.sh %s: %v\n%s", program, err, out)
		}
		assertNoSecret(t, "run-app.sh output", string(out), sec)
		got := strings.Fields(s.read(t, "env-"+program))
		allowed := names(t, program)
		for n := range env {
			if slices.Contains(allowed, n) != slices.Contains(got, n) {
				t.Errorf("%s: .env name %s exported=%v, allowed=%v", program, n, slices.Contains(got, n), slices.Contains(allowed, n))
			}
		}
		for n := range procEnv[program] {
			if !slices.Contains(got, n) {
				t.Errorf("%s lost its per-process %s", program, n)
			}
		}
		if !strings.Contains(s.read(t, "programs.log"), program+" --flag") {
			t.Errorf("%s was not executed with its arguments", program)
		}
	}
	webDir, err := filepath.EvalSymlinks(filepath.Join(rel, "web", "logitrack-web"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.read(t, "programs.log"), "web --flag cwd="+webDir) {
		t.Errorf("the web did not start in its standalone directory: %s", s.read(t, "programs.log"))
	}
}

// fakeRelease lays out <prefix>/opt/logitrack/releases/<id> with this kit and recording programs.
func fakeRelease(t *testing.T, s *sandbox, id string) string {
	t.Helper()
	rel := filepath.Join(s.prefix, "opt", "logitrack", "releases", id)
	kit := filepath.Join(rel, "deploy", "vm")
	mustMkdir(t, filepath.Join(kit, "env-names"))
	for _, f := range []string{"run-app.sh", "rollout.sh", "kit.conf", "ecosystem.config.cjs"} {
		writeFile(t, filepath.Join(kit, f), string(must(os.ReadFile(f))), 0o755)
	}
	entries, _ := os.ReadDir("env-names")
	for _, e := range entries {
		writeFile(t, filepath.Join(kit, "env-names", e.Name()), string(must(os.ReadFile(filepath.Join("env-names", e.Name())))), 0o644)
	}
	// The state path is written into the program: rollout.sh starts programs under env -i.
	record := func(program string) string {
		return "#!/bin/sh\necho \"" + program + " $* cwd=$(pwd)\" >>\"" + s.state + "/programs.log\"\n" +
			"env | cut -d= -f1 | sort >\"" + s.state + "/env-" + program + "\"\n"
	}
	for _, p := range []string{"api", "worker", "scheduler", "migrate", "seed"} {
		writeFile(t, filepath.Join(rel, "bin", p), strings.Replace(record(p), " cwd=$(pwd)", "", 1), 0o755)
	}
	writeFile(t, filepath.Join(rel, "web", "logitrack-web", "server.js"), "// standalone server", 0o644)
	// The web's node: a recording program first on PATH (its first argument is server.js).
	writeFile(t, filepath.Join(s.bin, "node"), "#!/bin/sh\n[ \"$1\" = server.js ] && shift\n"+strings.TrimPrefix(record("web"), "#!/bin/sh\n"), 0o755)
	return rel
}

// TestProvision: --dry-run changes nothing; a real run (stubs) writes the .env and key, sets up the database,
// containers, nginx and pm2 without a secret in any output or process argument; a second run changes nothing.
func TestProvision(t *testing.T) {
	s := newSandbox(t)
	script := filepath.Join(kitDir(t), "provision.sh")
	args := []string{"--prefix", s.prefix, "--vmenv", vmenv(t)}
	root := filepath.Join(s.prefix, "opt", "logitrack")

	out, code := s.run(t, script, append(args, "--dry-run")...)
	if code != 0 {
		t.Fatalf("dry run: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "[dry-run] would") || !strings.Contains(out, "vmenv: would write") {
		t.Errorf("dry run prints no plan:\n%s", out)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry run created %s", root)
	}
	assertNoMutation(t, s.read(t, "calls.log"))
	for _, d := range []string{"sites-available", "sites-enabled", "snippets", "conf.d"} {
		if e, _ := os.ReadDir(filepath.Join(s.prefix, "etc", "nginx", d)); len(e) != 0 {
			t.Errorf("dry run wrote into nginx %s", d)
		}
	}
	if _, err := os.Stat(filepath.Join(s.prefix, "etc", "systemd")); !errors.Is(err, os.ErrNotExist) {
		t.Error("dry run wrote the pm2 unit")
	}

	// The real run. The tree exists on the VM as root creates it (mkdir -p is not stubbed).
	s.resetCalls(t)
	out, code = s.run(t, script, args...)
	if code != 0 {
		t.Fatalf("provision: exit %d\n%s", code, out)
	}
	envPath := filepath.Join(root, "shared", ".env")
	env := dotenv(t, envPath)
	keyFile := filepath.Join(s.prefix, env["JWT_SIGNING_KEY_FILE"])
	sec := secrets(t, env, keyFile)
	calls := s.read(t, "calls.log")
	assertNoSecret(t, "provision.sh output", out, sec)
	assertNoSecret(t, "a process argument (calls.log)", calls, sec)
	// Root owns what root acts on; the app user only reads it (group logitrack).
	for f, mode := range map[string]os.FileMode{envPath: 0o640, keyFile: 0o640, filepath.Dir(keyFile): 0o750} {
		if st, err := os.Stat(f); err != nil || st.Mode().Perm() != mode {
			t.Errorf("%s: mode %v, want %v", f, st.Mode().Perm(), mode)
		}
	}
	for _, want := range []string{"chown root:logitrack " + envPath, "chown -h root:logitrack " + keyFile,
		"chown root:logitrack " + filepath.Dir(keyFile)} {
		if !strings.Contains(calls, want+"\n") {
			t.Errorf("provision did not run %q", want)
		}
	}
	if strings.Contains(calls, "chown logitrack:logitrack "+envPath) || strings.Contains(calls, "chown logitrack:logitrack "+keyFile) {
		t.Error("the .env or the key was handed to the app user")
	}
	// The empty pm2 dump is written by the app user (runuser), never by root through the user's home.
	if string(must(os.ReadFile(filepath.Join(s.prefix, "home", "logitrack", ".pm2", "dump.pm2")))) != "[]\n" ||
		!strings.Contains(calls, "runuser -u logitrack -- sh -c") {
		t.Error("the empty pm2 dump was not written as the app user")
	}
	if env["JWT_ACTIVE_KID"] != token.Thumbprint(publicKey(t, keyFile)) {
		t.Error("JWT_ACTIVE_KID is not the RFC 7638 thumbprint of the key")
	}
	// Role passwords reach psql as SCRAM verifiers on stdin, never in clear.
	psqlIn := s.read(t, "psql.stdin")
	if strings.Count(psqlIn, "WITH PASSWORD 'SCRAM-SHA-256$4096:") != 3 {
		t.Errorf("psql stdin holds no three SCRAM verifiers:\n%s", psqlIn)
	}
	assertNoSecret(t, "psql stdin", psqlIn, sec)
	if strings.Count(psqlIn, "CREATE ROLE logitrack_migrator") != 1 {
		t.Error("00-roles.sql did not reach psql's stdin once")
	}
	for _, want := range []string{"CREATE DATABASE logitrack", "ALTER DATABASE logitrack OWNER TO logitrack_migrator",
		"REVOKE CONNECT ON DATABASE logitrack FROM PUBLIC", "docker run -d --name logitrack-redis", "127.0.0.1:6381:6379",
		"docker run -d --name logitrack-rabbitmq", "--hostname logitrack-rabbitmq", "127.0.0.1:5674:5672", "127.0.0.1:15674:15672",
		"api/definitions", "nginx -t", "systemctl reload nginx", "systemctl enable --now pm2-logitrack", "pm2 install pm2-logrotate",
		"useradd --system"} {
		if !strings.Contains(calls, want) {
			t.Errorf("provision did not run %q", want)
		}
	}
	// The containers' credentials came from env-files that are gone afterwards.
	if !strings.HasPrefix(s.read(t, "envfile-logitrack-redis"), "REDISCLI_AUTH=") ||
		!strings.Contains(s.read(t, "envfile-logitrack-rabbitmq"), "RABBITMQ_DEFAULT_PASS="+env["RABBITMQ_DEFAULT_PASS"]) {
		t.Error("container env-files do not carry the credentials")
	}
	if !strings.Contains(s.read(t, "curl.stdin"), `user = "logitrack:`+env["RABBITMQ_DEFAULT_PASS"]+`"`) {
		t.Error("the RabbitMQ management API credentials did not go through curl's stdin")
	}
	if left, _ := filepath.Glob(filepath.Join(s.tmp, "*")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
	nginx := filepath.Join(s.prefix, "etc", "nginx")
	for _, f := range []string{"sites-available/logi.showkhun.co", "sites-available/logi-api.showkhun.co",
		"snippets/logitrack-proxy.conf", "snippets/logitrack-cloudflare-realip.conf", "conf.d/logitrack-log-format.conf"} {
		if _, err := os.Stat(filepath.Join(nginx, f)); err != nil {
			t.Errorf("not installed: %s", f)
		}
	}
	for _, h := range []string{"logi.showkhun.co", "logi-api.showkhun.co"} {
		if target, err := os.Readlink(filepath.Join(nginx, "sites-enabled", h)); err != nil || target != filepath.Join(nginx, "sites-available", h) {
			t.Errorf("sites-enabled/%s -> %q (%v)", h, target, err)
		}
	}
	unit := string(must(os.ReadFile(filepath.Join(s.prefix, "etc", "systemd", "system", "pm2-logitrack.service"))))
	if !strings.Contains(unit, "User=logitrack") || !strings.Contains(unit, "ExecStart=/opt/node-v24.14.0/bin/pm2 resurrect") ||
		strings.Contains(unit, "@kit_") {
		t.Errorf("pm2 unit not rendered:\n%s", unit)
	}

	// Second run: everything is kept.
	before := string(must(os.ReadFile(envPath)))
	keyBefore := string(must(os.ReadFile(keyFile)))
	s.resetCalls(t)
	out, code = s.run(t, script, args...)
	if code != 0 {
		t.Fatalf("second provision: exit %d\n%s", code, out)
	}
	if string(must(os.ReadFile(envPath))) != before || string(must(os.ReadFile(keyFile))) != keyBefore {
		t.Error("a second run changed the .env or the JWT key")
	}
	calls = s.read(t, "calls.log")
	for _, unexpected := range []string{"docker run", "CREATE DATABASE", "useradd", "systemctl reload nginx"} {
		if strings.Contains(calls, unexpected) {
			t.Errorf("second run ran %q", unexpected)
		}
	}
	if strings.Count(s.read(t, "psql.stdin"), "CREATE ROLE logitrack_migrator") != 1 {
		t.Error("the second run ran 00-roles.sql again")
	}
	if !strings.Contains(out, "unchanged") {
		t.Errorf("second run reports no unchanged files:\n%s", out)
	}
	assertNoSecret(t, "second provision output", out, sec)
	assertNoSecret(t, "second run process arguments", calls, sec)
}

// assertNoMutation fails on any stubbed call that would change the VM.
func assertNoMutation(t *testing.T, calls string) {
	t.Helper()
	for _, l := range strings.Split(calls, "\n") {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		mutating := false
		switch f[0] {
		case "useradd", "chown", "systemctl", "nginx", "pm2":
			mutating = true
		case "docker":
			mutating = len(f) > 1 && f[1] != "info" && f[1] != "inspect"
		case "psql":
			mutating = !strings.Contains(l, " -tAc ")
		case "curl":
			mutating = true
		}
		if mutating {
			t.Errorf("dry run called: %s", l)
		}
	}
}

func publicKey(t *testing.T, path string) ed25519.PublicKey {
	t.Helper()
	blk, _ := pem.Decode(must(os.ReadFile(path)))
	if blk == nil {
		t.Fatal("key is not PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return k.(ed25519.PrivateKey).Public().(ed25519.PublicKey)
}

// TestProvisionRefusesForeignFiles: another vhost with our server_name, or an unmanaged file at our path,
// stops provisioning before nginx is touched.
func TestProvisionRefusesForeignFiles(t *testing.T) {
	for name, setup := range map[string]func(nginx string){
		"server_name taken": func(nginx string) {
			writeFile(t, filepath.Join(nginx, "sites-enabled", "plodpai"), "server { server_name logi.showkhun.co; }", 0o644)
		},
		"unmanaged file": func(nginx string) {
			writeFile(t, filepath.Join(nginx, "sites-available", "logi-api.showkhun.co"), "server {}", 0o644)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t)
			nginx := filepath.Join(s.prefix, "etc", "nginx")
			setup(nginx)
			out, code := s.run(t, filepath.Join(kitDir(t), "provision.sh"), "--prefix", s.prefix, "--vmenv", vmenv(t))
			if code == 0 || !strings.Contains(out, "refusing to touch it") && !strings.Contains(out, "already declares server_name") {
				t.Fatalf("exit %d\n%s", code, out)
			}
			if strings.Contains(s.read(t, "calls.log"), "nginx -t") {
				t.Error("nginx was tested or reloaded")
			}
			if _, err := os.Stat(filepath.Join(nginx, "snippets", "logitrack-proxy.conf")); err == nil {
				t.Error("a snippet was installed")
			}
		})
	}
}

// TestRollout: first deploy (migrate, seed, bootstrap, switch, pm2, checks), a second deploy without seed,
// a failing deploy switched back automatically, rollback, pruning to three releases and a dry run that
// changes nothing; no secret in any output.
func TestRollout(t *testing.T) {
	s := newSandbox(t)
	_, env := renderEnv(t, s.prefix)
	sec := secrets(t, env, "")
	root := filepath.Join(s.prefix, "opt", "logitrack")
	ids := []string{"1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222",
		"3333333333333333333333333333333333333333", "4444444444444444444444444444444444444444"}
	// Each release is uploaded right before its activation, as deploy.sh does (prune removes releases
	// that were never live).
	uploaded := map[string]bool{}
	rollout := func(id string, args ...string) (string, int) {
		if !uploaded[id] {
			fakeRelease(t, s, id)
			uploaded[id] = true
		}
		return s.run(t, filepath.Join(root, "releases", id, "deploy", "vm", "rollout.sh"),
			append(args, "--prefix", s.prefix, "--health-wait", "0")...)
	}
	current := func() string { l, _ := os.Readlink(filepath.Join(root, "current")); return l }

	// Dry run first: nothing happens.
	out, code := rollout(ids[0], "activate", "--dry-run")
	if code != 0 || current() != "" || s.read(t, "programs.log") != "" {
		t.Fatalf("dry run changed something (exit %d, current %q)\n%s", code, current(), out)
	}

	out, code = rollout(ids[0], "activate")
	if code != 0 {
		t.Fatalf("first deploy: exit %d\n%s", code, out)
	}
	progs := s.read(t, "programs.log")
	for _, want := range []string{"migrate up", "seed --profile demo --allow-shared", "seed --bootstrap-admin"} {
		if !strings.Contains(progs, want) {
			t.Errorf("first deploy did not run %q:\n%s", want, progs)
		}
	}
	if current() != "releases/"+ids[0] {
		t.Fatalf("current -> %q", current())
	}
	calls := s.read(t, "calls.log")
	if !strings.Contains(calls, "pm2 startOrReload "+root+"/current/deploy/vm/ecosystem.config.cjs --update-env") ||
		!strings.Contains(calls, "pm2 save") || !strings.Contains(calls, "127.0.0.1:3301/readyz") ||
		!strings.Contains(calls, "127.0.0.1:3302/v1/tasks") {
		t.Errorf("pm2 or the checks did not run:\n%s", calls)
	}
	assertNoSecret(t, "rollout output", out, sec)

	// Second deploy: no seed.
	writeFile(t, filepath.Join(s.state, "programs.log"), "", 0o644)
	if out, code = rollout(ids[1], "activate"); code != 0 {
		t.Fatalf("second deploy: exit %d\n%s", code, out)
	}
	if strings.Contains(s.read(t, "programs.log"), "seed") || !strings.Contains(s.read(t, "programs.log"), "migrate up") {
		t.Errorf("second deploy: %s", s.read(t, "programs.log"))
	}

	// A failing release goes back to the previous one by itself.
	writeFile(t, filepath.Join(s.state, "fail"), ":3301/readyz\n", 0o644)
	if out, code = rollout(ids[2], "activate"); code == 0 || current() != "releases/"+ids[1] {
		t.Fatalf("failed deploy: exit %d, current %q\n%s", code, current(), out)
	}
	if err := os.Remove(filepath.Join(s.state, "fail")); err != nil {
		t.Fatal(err)
	}

	if out, code = rollout(ids[1], "rollback"); code != 0 || current() != "releases/"+ids[0] {
		t.Fatalf("rollback: exit %d, current %q\n%s", code, current(), out)
	}
	if out, code = rollout(ids[0], "rollback", "--to", ids[1]); code != 0 || current() != "releases/"+ids[1] {
		t.Fatalf("rollback --to: exit %d, current %q\n%s", code, current(), out)
	}

	// A fourth activation keeps three releases.
	if out, code = rollout(ids[3], "activate"); code != 0 {
		t.Fatalf("fourth deploy: exit %d\n%s", code, out)
	}
	left, _ := os.ReadDir(filepath.Join(root, "releases"))
	if len(left) != 3 {
		var n []string
		for _, e := range left {
			n = append(n, e.Name())
		}
		t.Errorf("releases kept: %v, want 3", n)
	}
	if _, err := os.Stat(filepath.Join(root, "releases", ids[3])); err != nil {
		t.Error("the current release was pruned")
	}
	assertNoSecret(t, "rollout output", out, sec)
	assertNoSecret(t, "process arguments", s.read(t, "calls.log"), sec)

	// The pm2 passthrough runs as the app user with its PM2_HOME and a clean environment.
	out, code = s.run(t, filepath.Join(root, "current", "deploy", "vm", "rollout.sh"), "--prefix", s.prefix, "pm2", "reload", "logitrack-api")
	if code != 0 || !strings.Contains(s.read(t, "calls.log"), "pm2 reload logitrack-api") {
		t.Errorf("rollout.sh pm2: exit %d\n%s", code, out)
	}
	if !strings.Contains(s.read(t, "calls.log"), "runuser -u logitrack -- env -i HOME=") {
		t.Error("pm2 does not run under env -i as logitrack")
	}
}

// TestDeploy: --dry-run builds and contacts nothing; a run (stubs) passes the web's build values only through
// docker's environment, uploads a release without tests or env files, and prints no value.
func TestDeploy(t *testing.T) {
	s := newSandbox(t)
	script := filepath.Join(kitDir(t), "deploy.sh")
	webEnv := filepath.Join(t.TempDir(), "web-build.env")
	values := map[string]string{
		"NEXT_PUBLIC_FIREBASE_API_KEY":             "sentinel-api-key-7f3a",
		"NEXT_PUBLIC_FIREBASE_AUTH_DOMAIN":         "sentinel-auth-domain-7f3a",
		"NEXT_PUBLIC_FIREBASE_PROJECT_ID":          "sentinel-project-7f3a",
		"NEXT_PUBLIC_FIREBASE_STORAGE_BUCKET":      "sentinel-bucket-7f3a",
		"NEXT_PUBLIC_FIREBASE_MESSAGING_SENDER_ID": "sentinel-sender-7f3a",
		"NEXT_PUBLIC_FIREBASE_APP_ID":              `"sentinel-app-id-7f3a"`,
		"NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID":        "sentinel-client-7f3a",
	}
	var b strings.Builder
	var sentinels []string
	for k, v := range values {
		b.WriteString(k + "=" + v + "\n")
		sentinels = append(sentinels, strings.Trim(v, `"`))
	}
	writeFile(t, webEnv, b.String(), 0o600)

	out, code := s.run(t, script, "--web-build-env", webEnv, "--dry-run", "--allow-dirty")
	if code != 0 {
		t.Fatalf("dry run: exit %d\n%s", code, out)
	}
	if calls := s.read(t, "calls.log"); calls != "" {
		t.Errorf("dry run called: %s", calls)
	}
	assertNoSecret(t, "deploy --dry-run output", out, sentinels)
	if !strings.Contains(out, "NEXT_PUBLIC_FIREBASE_API_KEY") || !strings.Contains(out, "[dry-run] would") {
		t.Errorf("dry run prints no plan:\n%s", out)
	}

	out, code = s.run(t, script, "--web-build-env", webEnv, "--allow-dirty")
	if code != 0 {
		t.Fatalf("deploy: exit %d\n%s", code, out)
	}
	calls := s.read(t, "calls.log")
	assertNoSecret(t, "deploy output", out, sentinels)
	assertNoSecret(t, "process arguments", calls, sentinels)
	if !strings.Contains(calls, "rollout.sh activate --pending") || !strings.Contains(calls, "rollout.sh confirm ") {
		t.Errorf("the release is not activated pending and confirmed after the public checks:\n%s", calls)
	}
	if !strings.Contains(calls, "docker buildx build --platform linux/amd64") || !strings.Contains(calls, "--target standalone") ||
		!strings.Contains(calls, "--build-arg NEXT_PUBLIC_FIREBASE_API_KEY ") {
		t.Errorf("docker buildx not called as expected:\n%s", calls)
	}
	built := s.read(t, "docker-build.env")
	for k, v := range values {
		if !strings.Contains(built, k+"="+strings.Trim(v, `"`)+"\n") {
			t.Errorf("docker did not receive %s through its environment", k)
		}
	}
	if goEnv := s.read(t, "go.env"); !strings.Contains(goEnv, "GOOS=linux") || !strings.Contains(goEnv, "GOARCH=amd64") || !strings.Contains(goEnv, "CGO_ENABLED=0") {
		t.Errorf("go build environment: %s", goEnv)
	}
	for _, want := range []string{"rollout.sh activate", "https://logi-api.showkhun.co/healthz", "https://logi.showkhun.co/login",
		"https://logi-api.showkhun.co/v1/tasks"} {
		if !strings.Contains(calls, want) {
			t.Errorf("deploy did not run %q", want)
		}
	}
	uploads, _ := filepath.Glob(filepath.Join(s.state, "upload.*.tgz"))
	if len(uploads) != 1 {
		t.Fatalf("uploads: %v", uploads)
	}
	files := tarNames(t, uploads[0])
	for _, want := range []string{"./bin/api", "./bin/vmenv", "./web/logitrack-web/server.js", "./deploy/vm/run-app.sh",
		"./deploy/vm/env-names/web.txt", "./deploy/postgres-init/00-roles.sql", "./deploy/rabbitmq-definitions.json", "./REVISION"} {
		if !slices.Contains(files, want) {
			t.Errorf("the release lacks %s", want)
		}
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.Contains(filepath.Base(f), ".env") && !strings.HasSuffix(f, "env.vm.example") {
			t.Errorf("the release carries %s", f)
		}
	}
}

func tarNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(bufio.NewReader(f))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h.Name)
	}
	return out
}

// TestDeployRefusesBadWebEnv: a tracked file, a server-only name or a missing Firebase value stops the
// deploy before anything is built, and the message names variables only.
func TestDeployRefusesBadWebEnv(t *testing.T) {
	s := newSandbox(t)
	script := filepath.Join(kitDir(t), "deploy.sh")
	dir := t.TempDir()
	serverName := filepath.Join(dir, "server.env")
	writeFile(t, serverName, "NEXT_PUBLIC_FIREBASE_API_KEY=x\nGO_API_INTERNAL_URL=sentinel-server-value-9c1\n", 0o600)
	missing := filepath.Join(dir, "missing.env")
	writeFile(t, missing, "NEXT_PUBLIC_FIREBASE_API_KEY=sentinel-only-one-9c1\n", 0o600)
	for name, c := range map[string]struct{ file, want string }{
		"tracked":     {filepath.Join(kitDir(t), "..", "..", ".env.example"), "tracked by git"},
		"server name": {serverName, "GO_API_INTERNAL_URL is not a NEXT_PUBLIC_* build argument"},
		"missing":     {missing, "does not set: NEXT_PUBLIC_FIREBASE_AUTH_DOMAIN"},
	} {
		out, code := s.run(t, script, "--web-build-env", c.file, "--allow-dirty")
		if code == 0 || !strings.Contains(out, c.want) {
			t.Errorf("%s: exit %d\n%s", name, code, out)
		}
		assertNoSecret(t, name+" output", out, []string{"sentinel-server-value-9c1", "sentinel-only-one-9c1"})
	}
	if calls := s.read(t, "calls.log"); calls != "" {
		t.Errorf("a refused deploy called: %s", calls)
	}
}

// TestProvisionRefusesAForeignKeyPath: provision.sh runs as root and reads JWT_SIGNING_KEY_FILE from the .env;
// a value outside shared/secrets/, or a key replaced by a symbolic link, stops it before root creates or
// chowns anything there (an app-user process must not get root to hand it /etc/ld.so.preload).
func TestProvisionRefusesAForeignKeyPath(t *testing.T) {
	for name, tamper := range map[string]func(t *testing.T, envPath, keyFile, prefix string){
		"path outside secrets": func(t *testing.T, envPath, _, _ string) {
			raw := string(must(os.ReadFile(envPath)))
			raw = regexp.MustCompile(`(?m)^JWT_SIGNING_KEY_FILE=.*$`).ReplaceAllString(raw, "JWT_SIGNING_KEY_FILE=/etc/ld.so.preload")
			writeFile(t, envPath, raw, 0o640)
		},
		"key is a symbolic link": func(t *testing.T, _, keyFile, prefix string) {
			if err := os.Remove(keyFile); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(prefix, "etc", "ld.so.preload"), keyFile); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t)
			script := filepath.Join(kitDir(t), "provision.sh")
			args := []string{"--prefix", s.prefix, "--vmenv", vmenv(t)}
			if out, code := s.run(t, script, args...); code != 0 {
				t.Fatalf("provision: exit %d\n%s", code, out)
			}
			envPath := filepath.Join(s.prefix, "opt", "logitrack", "shared", ".env")
			keyFile := filepath.Join(s.prefix, dotenv(t, envPath)["JWT_SIGNING_KEY_FILE"])
			tamper(t, envPath, keyFile, s.prefix)
			s.resetCalls(t)
			out, code := s.run(t, script, args...)
			if code == 0 || !strings.Contains(out, "JWT_SIGNING_KEY_FILE") {
				t.Fatalf("a foreign key path was accepted: exit %d\n%s", code, out)
			}
			target := filepath.Join(s.prefix, "etc", "ld.so.preload")
			if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("provision created %s", target)
			}
			if calls := s.read(t, "calls.log"); strings.Contains(calls, "ld.so.preload") || strings.Contains(calls, "chown -h") {
				t.Errorf("provision acted on the foreign path:\n%s", calls)
			}
		})
	}
}

// TestRolloutPendingRelease: deploy.sh activates with --pending and confirms after its public checks. A
// release that fails them is switched back from and never recorded, so a later plain rollback cannot return
// to it, and pruning removes it.
func TestRolloutPendingRelease(t *testing.T) {
	s := newSandbox(t)
	_, env := renderEnv(t, s.prefix)
	sec := secrets(t, env, "")
	root := filepath.Join(s.prefix, "opt", "logitrack")
	a, b, c := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccccccccccc"
	run := func(script string, args ...string) (string, int) {
		return s.run(t, script, append(args, "--prefix", s.prefix, "--health-wait", "0")...)
	}
	in := func(id string) string { return filepath.Join(root, "releases", id, "deploy", "vm", "rollout.sh") }
	cur := filepath.Join(root, "current", "deploy", "vm", "rollout.sh")
	current := func() string { l, _ := os.Readlink(filepath.Join(root, "current")); return l }
	history := func() string { return string(must(os.ReadFile(filepath.Join(root, "shared", "releases.log")))) }

	fakeRelease(t, s, a)
	if out, code := run(in(a), "activate"); code != 0 {
		t.Fatalf("activate %s: exit %d\n%s", a, code, out)
	}
	// B passes the on-VM checks but is only pending: nothing recorded, nothing pruned.
	fakeRelease(t, s, b)
	out, code := run(in(b), "activate", "--pending")
	if code != 0 || current() != "releases/"+b || strings.Contains(history(), b) {
		t.Fatalf("activate --pending: exit %d, current %q, history %q\n%s", code, current(), history(), out)
	}
	if out, _ := run(cur, "status"); !strings.Contains(out, "not recorded as live") {
		t.Errorf("status does not flag the pending release:\n%s", out)
	}
	// deploy.sh's public checks failed: back to A.
	if out, code = run(cur, "rollback"); code != 0 || current() != "releases/"+a {
		t.Fatalf("rollback after the public checks: exit %d, current %q\n%s", code, current(), out)
	}
	// A later deploy.sh --rollback must not land on B.
	if out, code = run(cur, "rollback"); code == 0 || current() != "releases/"+a || !strings.Contains(out, "no earlier release") {
		t.Fatalf("a plain rollback returned to the failed release: exit %d, current %q\n%s", code, current(), out)
	}
	// C is confirmed; pruning drops B, which never went live.
	fakeRelease(t, s, c)
	if out, code = run(in(c), "activate", "--pending"); code != 0 {
		t.Fatalf("activate %s: exit %d\n%s", c, code, out)
	}
	if out, code = run(cur, "confirm", a); code == 0 || strings.Contains(history(), c) {
		t.Fatalf("confirm of a release that is not current: exit %d\n%s", code, out)
	}
	if out, code = run(cur, "confirm", c); code != 0 || !strings.HasSuffix(strings.TrimSpace(history()), " "+c) {
		t.Fatalf("confirm: exit %d, history %q\n%s", code, history(), out)
	}
	if _, err := os.Stat(filepath.Join(root, "releases", b)); !errors.Is(err, os.ErrNotExist) {
		t.Error("the release that failed its public checks was kept")
	}
	if out, code = run(cur, "rollback"); code != 0 || current() != "releases/"+a {
		t.Fatalf("rollback from %s: exit %d, current %q\n%s", c, code, current(), out)
	}
	assertNoSecret(t, "process arguments", s.read(t, "calls.log"), sec)
}

// TestRolloutFirstDeploySeedsUntilLive: a first deploy that fails a check after its seed leaves no marker, so
// the next deploy seeds again (README section 3); the marker appears once a release is recorded as live.
func TestRolloutFirstDeploySeedsUntilLive(t *testing.T) {
	s := newSandbox(t)
	renderEnv(t, s.prefix)
	root := filepath.Join(s.prefix, "opt", "logitrack")
	marker := filepath.Join(root, "shared", ".first-deploy-done")
	activate := func(id string, args ...string) (string, int) {
		fakeRelease(t, s, id)
		writeFile(t, filepath.Join(s.state, "programs.log"), "", 0o644)
		return s.run(t, filepath.Join(root, "releases", id, "deploy", "vm", "rollout.sh"),
			append(append([]string{"activate"}, args...), "--prefix", s.prefix, "--health-wait", "0")...)
	}
	seeded := func() bool {
		p := s.read(t, "programs.log")
		return strings.Contains(p, "seed --profile demo --allow-shared") && strings.Contains(p, "seed --bootstrap-admin")
	}
	writeFile(t, filepath.Join(s.state, "fail"), ":3300/login\n", 0o644)
	if out, code := activate("1111111111111111111111111111111111111111"); code == 0 || !seeded() {
		t.Fatalf("failing first deploy: exit %d, seeded %v\n%s", code, seeded(), out)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a first deploy that failed its checks marked the first deploy as done")
	}
	if err := os.Remove(filepath.Join(s.state, "fail")); err != nil {
		t.Fatal(err)
	}
	if out, code := activate("2222222222222222222222222222222222222222", "--pending"); code != 0 || !seeded() {
		t.Fatalf("second attempt: exit %d, seeded %v\n%s", code, seeded(), out)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a pending release marked the first deploy as done")
	}
	if out, code := s.run(t, filepath.Join(root, "current", "deploy", "vm", "rollout.sh"), "confirm",
		"2222222222222222222222222222222222222222", "--prefix", s.prefix); code != 0 {
		t.Fatalf("confirm: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the first confirmed release did not mark the first deploy as done")
	}
	if out, code := activate("3333333333333333333333333333333333333333"); code != 0 || strings.Contains(s.read(t, "programs.log"), "seed") {
		t.Fatalf("a later deploy seeded again: exit %d\n%s", code, out)
	}
}

// scratchRepo is a git repository holding deploy.sh and what it reads, committed, so the dirty check and the
// clean export run against a known tree instead of this checkout.
func scratchRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	api := filepath.Join(repo, "logitrack-api")
	for _, f := range []string{"deploy.sh", "kit.conf"} {
		writeFile(t, filepath.Join(api, "deploy", "vm", f), string(must(os.ReadFile(f))), 0o755)
	}
	writeFile(t, filepath.Join(api, "deploy", "postgres-init", "00-roles.sql"), "-- roles\n", 0o644)
	writeFile(t, filepath.Join(api, "deploy", "rabbitmq-definitions.json"), "{}\n", 0o644)
	modLine := strings.SplitN(string(must(os.ReadFile(filepath.Join(kitDir(t), "..", "..", "go.mod")))), "\n", 2)[0]
	writeFile(t, filepath.Join(api, "go.mod"), modLine+"\n", 0o644)
	writeFile(t, filepath.Join(api, "migrations", "0001_init.sql"), "-- init\n", 0o644)
	writeFile(t, filepath.Join(repo, "logitrack-web", "Dockerfile"),
		string(must(os.ReadFile(filepath.Join(kitDir(t), "..", "..", "..", "logitrack-web", "Dockerfile")))), 0o644)
	for _, f := range []string{"package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "shared-docs/package.json"} {
		writeFile(t, filepath.Join(repo, f), "\n", 0o644)
	}
	writeFile(t, filepath.Join(repo, ".gitignore"), "*.wip\n", 0o644)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=kit", "-c", "user.email=kit@example.invalid",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "kit")
	return repo, git("rev-parse", "HEAD")
}

// TestDeployBuildsTheCommit: a release named after a commit is built from that commit: untracked files under
// the release paths (a new migration go:embed would take) are refused without --allow-dirty, an ignored file
// never reaches the build, and changes outside the release paths do not count.
func TestDeployBuildsTheCommit(t *testing.T) {
	s := newSandbox(t)
	repo, sha := scratchRepo(t)
	script := filepath.Join(repo, "logitrack-api", "deploy", "vm", "deploy.sh")
	webEnv := filepath.Join(t.TempDir(), "web-build.env")
	var b strings.Builder
	for _, n := range []string{"API_KEY", "AUTH_DOMAIN", "PROJECT_ID", "STORAGE_BUCKET", "MESSAGING_SENDER_ID", "APP_ID"} {
		b.WriteString("NEXT_PUBLIC_FIREBASE_" + n + "=x\n")
	}
	writeFile(t, webEnv, b.String(), 0o600)
	deploy := func(args ...string) (string, int) {
		return s.run(t, script, append([]string{"--web-build-env", webEnv}, args...)...)
	}

	// Outside the release paths, and ignored: still the commit.
	writeFile(t, filepath.Join(repo, ".claude", "settings.local.json"), "{}\n", 0o644)
	writeFile(t, filepath.Join(repo, "logitrack-api", "migrations", "0099_ignored.wip"), "-- ignored\n", 0o644)
	out, code := deploy("--dry-run")
	if code != 0 || !strings.Contains(out, "Version="+sha+")") || !strings.Contains(out, "clean export of "+sha) {
		t.Fatalf("clean checkout: exit %d\n%s", code, out)
	}

	wip := filepath.Join(repo, "logitrack-api", "migrations", "0012_wip.sql")
	writeFile(t, wip, "-- work in progress\n", 0o644)
	if out, code = deploy("--dry-run"); code == 0 || !strings.Contains(out, "untracked files") || strings.Contains(out, "Version=") {
		t.Fatalf("an untracked migration was not refused: exit %d\n%s", code, out)
	}
	out, code = deploy("--dry-run", "--allow-dirty")
	if code != 0 || !regexp.MustCompile(`Version=`+sha+`-dirty-[0-9]{14}\)`).MatchString(out) || !strings.Contains(out, "working tree") {
		t.Fatalf("--allow-dirty: exit %d\n%s", code, out)
	}
	if err := os.Remove(wip); err != nil {
		t.Fatal(err)
	}

	// A real run (stubs) builds from an export of the commit, not from the checkout.
	if out, code = deploy(); code != 0 {
		t.Fatalf("deploy: exit %d\n%s", code, out)
	}
	repoReal, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	tmpReal, err := filepath.EvalSymlinks(s.tmp)
	if err != nil {
		t.Fatal(err)
	}
	if cwd := strings.TrimSpace(s.read(t, "go.cwd")); !strings.HasPrefix(cwd, tmpReal+string(os.PathSeparator)) || strings.HasPrefix(cwd, repoReal) {
		t.Errorf("go build ran in %s, want an export under %s", cwd, tmpReal)
	}
	if m := strings.Fields(s.read(t, "go.migrations")); !slices.Equal(m, []string{"0001_init.sql"}) {
		t.Errorf("the build saw the migrations %v, want the committed one only", m)
	}
	if ctx := s.read(t, "docker.context"); strings.HasPrefix(ctx, repo) || strings.HasPrefix(ctx, repoReal) {
		t.Errorf("the web was built from the checkout %s", ctx)
	}
	calls := s.read(t, "calls.log")
	if !strings.Contains(calls, "rollout.sh activate --pending") || !strings.Contains(calls, "rollout.sh confirm "+sha) {
		t.Errorf("activate --pending and confirm were not called:\n%s", calls)
	}
	// The upload is the release only (the export stays on the operator's machine), and nothing is left behind.
	uploads, _ := filepath.Glob(filepath.Join(s.state, "upload.*.tgz"))
	if len(uploads) != 1 {
		t.Fatalf("uploads: %v", uploads)
	}
	files := tarNames(t, uploads[0])
	for _, want := range []string{"./bin/api", "./web/logitrack-web/server.js", "./deploy/vm/deploy.sh", "./REVISION"} {
		if !slices.Contains(files, want) {
			t.Errorf("the release lacks %s", want)
		}
	}
	for _, f := range files {
		if top, _, _ := strings.Cut(strings.TrimPrefix(f, "./"), "/"); !slices.Contains([]string{"", "bin", "web", "deploy", "REVISION"}, top) {
			t.Errorf("the release carries %s", f)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(s.tmp, "*")); len(left) != 0 {
		t.Errorf("temporary directories left behind: %v", left)
	}
}

// readmeBlocks returns the fenced code blocks of README.md under the heading that starts with section.
func readmeBlocks(t *testing.T, section string) []string {
	t.Helper()
	raw := string(must(os.ReadFile(filepath.Join(kitDir(t), "README.md"))))
	start := strings.Index(raw, "\n## "+section)
	if start < 0 {
		t.Fatalf("README.md has no section %q", section)
	}
	body := raw[start+1:]
	if end := strings.Index(body[3:], "\n## "); end >= 0 {
		body = body[:end+3]
	}
	var blocks []string
	for {
		i := strings.Index(body, "```sh\n")
		if i < 0 {
			return blocks
		}
		body = body[i+len("```sh\n"):]
		j := strings.Index(body, "```")
		blocks = append(blocks, body[:j])
		body = body[j+3:]
	}
}

// TestReadmeRunbook holds the runbook's commands to what the kit needs: the section 2 reload reaches both apps,
// the section 7 backups are private files in a private directory, and no restore drops the dump's owners.
func TestReadmeRunbook(t *testing.T) {
	// Section 2's reload, run through rollout.sh against the pm2 stub, reloads each app on its own.
	var reload []string
	for _, blk := range readmeBlocks(t, "2.") {
		for _, l := range strings.Split(blk, "\n") {
			if _, args, ok := strings.Cut(l, "rollout.sh pm2 "); ok {
				reload = strings.Fields(args)
			}
		}
	}
	if len(reload) < 2 || reload[0] != "reload" {
		t.Fatalf("section 2 has no rollout.sh pm2 reload: %v", reload)
	}
	s := newSandbox(t)
	renderEnv(t, s.prefix)
	rel := fakeRelease(t, s, "0123456789abcdef0123456789abcdef01234567")
	out, code := s.run(t, filepath.Join(rel, "deploy", "vm", "rollout.sh"), append([]string{"--prefix", s.prefix, "pm2"}, reload...)...)
	if code != 0 {
		t.Fatalf("section 2 reload: exit %d\n%s", code, out)
	}
	calls := strings.Split(s.read(t, "calls.log"), "\n")
	for _, app := range reload[1:] {
		if !slices.Contains(calls, "pm2 reload "+app) {
			t.Errorf("section 2 did not reload %s:\n%s", app, strings.Join(calls, "\n"))
		}
	}
	// An option among several names could be read as a name: refused.
	if out, code := s.run(t, filepath.Join(rel, "deploy", "vm", "rollout.sh"), "--prefix", s.prefix, "pm2", "reload",
		"logitrack-api", "--parallel", "2"); code == 0 || !strings.Contains(out, "one name") {
		t.Errorf("reload with an option and several names: exit %d\n%s", code, out)
	}

	// Section 7: every backup file is created under umask 077 in a 0700 root directory.
	var backup, restore string
	for _, blk := range readmeBlocks(t, "7.") {
		if strings.Contains(blk, "pg_dump") {
			backup = blk
		}
		if strings.Contains(blk, "pg_restore") {
			restore = blk
		}
	}
	if !strings.Contains(backup, "install -d -m 0700 -o root -g root /var/backups/logitrack\n") {
		t.Errorf("the backup directory is not created 0700 root:\n%s", backup)
	}
	open := strings.Index(backup, "( umask 077")
	closing := strings.LastIndex(backup, ")")
	for _, cmd := range []string{"pg_dump -Fc logitrack >", "-czf \"$d/logitrack-media-", "-czf \"$d/logitrack-secrets-"} {
		if i := strings.Index(backup, cmd); open < 0 || i < open || i > closing {
			t.Errorf("%q does not run under umask 077:\n%s", cmd, backup)
		}
	}
	// No pg_restore keeps a dump without its owners (the SECURITY DEFINER helpers would fall to the migrator).
	if restore == "" {
		t.Fatal("section 7 has no restore command")
	}
	for _, blk := range readmeBlocks(t, "") {
		for _, l := range strings.Split(blk, "\n") {
			if strings.Contains(l, "pg_restore") && (strings.Contains(l, "--no-owner") || strings.Contains(l, "--role") || strings.Contains(l, " -O")) {
				t.Errorf("README restores without the dump's owners: %s", l)
			}
		}
	}
}
