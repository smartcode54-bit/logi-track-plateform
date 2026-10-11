//go:build integration

// Container checks of the staging VM kit (make test-integration): the nginx vhosts under nginx 1.24 (the
// VM's version) with dummy certificate files at the real paths, `nginx -t`, the routing they promise and
// what their error logs keep when an upstream is down; shellcheck over every script; and the README's
// restore run as written on PostgreSQL 18 (pgtest).
package vm_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

// TestMain stops the PostgreSQL container of the restore test with the binary.
func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

const (
	nginxImage      = "nginx:1.24@sha256:f6daac2445b0ce70e64d77442ccf62839f3f1b4c24bf6746a857eff014e798c8"
	shellcheckImage = "koalaman/shellcheck:v0.11.0@sha256:61862eba1fcf09a484ebcc6feea46f1782532571a34ed51fedf90dd25f925a8d"
)

// ubuntuNginxConf is the http block of Ubuntu 24.04's /etc/nginx/nginx.conf: the VM's vhosts load from
// sites-enabled and conf.d exactly as here.
const ubuntuNginxConf = `user www-data;
worker_processes auto;
pid /run/nginx.pid;
events { worker_connections 768; }
http {
	sendfile on;
	tcp_nopush on;
	types_hash_max_size 2048;
	include /etc/nginx/mime.types;
	default_type application/octet-stream;
	ssl_protocols TLSv1 TLSv1.1 TLSv1.2 TLSv1.3;
	ssl_prefer_server_ciphers on;
	access_log /var/log/nginx/access.log;
	error_log /var/log/nginx/error.log;
	gzip on;
	include /etc/nginx/conf.d/*.conf;
	include /etc/nginx/sites-enabled/*;
}
`

// upstreamStubs stand in for the web (3300) and the api's public listener (3302) and echo what reached
// them; the internal listener (3301) answers too, so a route to it would be visible.
const upstreamStubs = `server { listen 127.0.0.1:3300; client_max_body_size 0; location / { default_type text/plain;
  return 200 "web $request_uri xff=$http_x_forwarded_for proto=$http_x_forwarded_proto host=$host\n"; } }
server { listen 127.0.0.1:3301; location / { return 418; } }
server { listen 127.0.0.1:3302; client_max_body_size 0; location / { default_type text/plain;
  return 200 "public $request_uri xff=$http_x_forwarded_for proto=$http_x_forwarded_proto host=$host\n"; } }
`

// nginxFiles is the VM's nginx tree as provision.sh installs it, plus a self-signed certificate at the
// origin certificate's paths.
func nginxFiles(t *testing.T, extra ...testcontainers.ContainerFile) []testcontainers.ContainerFile {
	t.Helper()
	dir := t.TempDir()
	certPEM, keyPEM := selfSigned(t)
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		writeFile(t, p, content, 0o644)
		return p
	}
	kit := func(name string) string { return filepath.Join(kitDir(t), "nginx", name) }
	files := []testcontainers.ContainerFile{
		{HostFilePath: write("nginx.conf", ubuntuNginxConf), ContainerFilePath: "/etc/nginx/nginx.conf", FileMode: 0o644},
		{HostFilePath: write("cert.pem", certPEM), ContainerFilePath: "/etc/ssl/certs/showkhun.co.pem", FileMode: 0o644},
		{HostFilePath: write("key.pem", keyPEM), ContainerFilePath: "/etc/ssl/private/showkhun.co.key", FileMode: 0o600},
		{HostFilePath: kit("logi.showkhun.co.conf"), ContainerFilePath: "/etc/nginx/sites-enabled/logi.showkhun.co", FileMode: 0o644},
		{HostFilePath: kit("logi-api.showkhun.co.conf"), ContainerFilePath: "/etc/nginx/sites-enabled/logi-api.showkhun.co", FileMode: 0o644},
		{HostFilePath: kit("logitrack-proxy.conf"), ContainerFilePath: "/etc/nginx/snippets/logitrack-proxy.conf", FileMode: 0o644},
		{HostFilePath: kit("logitrack-cloudflare-realip.conf"), ContainerFilePath: "/etc/nginx/snippets/logitrack-cloudflare-realip.conf", FileMode: 0o644},
		{HostFilePath: kit("logitrack-log-format.conf"), ContainerFilePath: "/etc/nginx/conf.d/logitrack-log-format.conf", FileMode: 0o644},
	}
	return append(files, extra...)
}

func selfSigned(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "*.showkhun.co"},
		DNSNames: []string{"*.showkhun.co"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder}))
}

// runToExit runs a container to completion and returns its exit code and output.
func runToExit(t *testing.T, image string, opts ...testcontainers.ContainerCustomizer) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ctr, err := testcontainers.Run(ctx, image, append(opts, testcontainers.WithWaitStrategy(wait.ForExit().WithExitTimeout(2*time.Minute)))...)
	if ctr != nil {
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })
	}
	if err != nil {
		t.Fatalf("run %s: %v", image, err)
	}
	st, err := ctr.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	logs, err := ctr.Logs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(logs)
	return st.ExitCode, string(out)
}

// TestNginxConfigTest: the two vhosts and their snippets pass `nginx -t` under nginx 1.24 with the
// certificate files at /etc/ssl/certs/showkhun.co.pem and /etc/ssl/private/showkhun.co.key.
func TestNginxConfigTest(t *testing.T) {
	code, out := runToExit(t, nginxImage, testcontainers.WithFiles(nginxFiles(t)...), testcontainers.WithCmd("nginx", "-t"))
	if code != 0 || !strings.Contains(out, "test is successful") {
		t.Fatalf("nginx -t: exit %d\n%s", code, out)
	}
}

// requestFunc sends one request to a vhost of a running nginx container: scheme, host, method, path, body,
// headers; it returns the status, the body and the response headers.
type requestFunc func(scheme, vhost, method, path string, body io.Reader, hdr map[string]string) (int, string, http.Header)

// startNginx runs nginx with the kit's files (plus extra) and returns the container and a request function.
func startNginx(t *testing.T, ctx context.Context, extra ...testcontainers.ContainerFile) (testcontainers.Container, requestFunc) {
	t.Helper()
	ctr, err := testcontainers.Run(ctx, nginxImage,
		testcontainers.WithFiles(nginxFiles(t, extra...)...),
		testcontainers.WithExposedPorts("80/tcp", "443/tcp"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("443/tcp").WithStartupTimeout(time.Minute)))
	if ctr != nil {
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })
	}
	if err != nil {
		t.Fatal(err)
	}
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	https, _ := ctr.MappedPort(ctx, "443/tcp")
	plain, _ := ctr.MappedPort(ctx, "80/tcp")
	client := func(port string) *http.Client {
		return &http.Client{
			Timeout:       20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
				},
				// The test's self-signed stand-in for the Cloudflare origin certificate.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test certificate
			},
		}
	}
	return ctr, func(scheme, vhost, method, path string, body io.Reader, hdr map[string]string) (int, string, http.Header) {
		t.Helper()
		port := https.Port()
		if scheme == "http" {
			port = plain.Port()
		}
		req, err := http.NewRequestWithContext(ctx, method, scheme+"://"+vhost+path, body)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := client(port).Do(req)
		if err != nil {
			t.Fatalf("%s %s%s: %v", method, vhost, path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
}

const web, api = "logi.showkhun.co", "logi-api.showkhun.co"

// TestNginxRouting runs the vhosts against echo upstreams and checks what each host forwards.
func TestNginxRouting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	stubs := filepath.Join(t.TempDir(), "stubs.conf")
	writeFile(t, stubs, upstreamStubs, 0o644)
	_, do := startNginx(t, ctx, testcontainers.ContainerFile{HostFilePath: stubs,
		ContainerFilePath: "/etc/nginx/conf.d/zz-upstream-stubs.conf", FileMode: 0o644})
	spoof := map[string]string{"X-Forwarded-For": "203.0.113.66", "CF-Connecting-IP": "198.51.100.77"}

	for _, c := range []struct {
		vhost, path, want string
	}{
		{web, "/login", "web /login "},
		{web, "/api/go/v1/events", "web /api/go/v1/events "},
		{web, "/media/private/a/b.jpg?X-LT-Expires=1&X-LT-Signature=s", "public /media/private/a/b.jpg?X-LT-Expires=1&X-LT-Signature=s "},
		{web, "/api/go/v1/uploads/local/k.jpg?X-LT-Signature=s", "web /api/go/v1/uploads/local/k.jpg?X-LT-Signature=s "},
		{api, "/healthz", "public /healthz "},
		{api, "/v1/mobile/events?ticket=t", "public /v1/mobile/events?ticket=t "},
		{api, "/v1/auth/login", "public /v1/auth/login "},
		{api, "/public/v1/x", "public /public/v1/x "},
		{api, "/evidence/tok", "public /evidence/tok "},
		{api, "/evidence?k=tok", "public /evidence?k=tok "},
		{api, "/media/app_releases/a.apk", "public /media/app_releases/a.apk "},
	} {
		code, body, _ := do("https", c.vhost, http.MethodGet, c.path, nil, spoof)
		if code != http.StatusOK || !strings.HasPrefix(body, c.want) {
			t.Errorf("%s%s: %d %q, want %q", c.vhost, c.path, code, body, c.want)
			continue
		}
		// A client that is no Cloudflare edge cannot choose its address: neither header survives.
		if strings.Contains(body, "203.0.113.66") || strings.Contains(body, "198.51.100.77") || !strings.Contains(body, "proto=https") {
			t.Errorf("%s%s forwarded a client-chosen address or no proto: %q", c.vhost, c.path, body)
		}
	}
	// The api host serves the public groups only, with the Go listener's 404 envelope; never the BFF.
	for _, p := range []string{"/v1/tasks", "/api/go/v1/events", "/", "/v1/mobilex", "/readyz", "/metrics"} {
		code, body, h := do("https", api, http.MethodGet, p, nil, nil)
		var env struct {
			Error struct{ Code, RequestID string } `json:"error"`
		}
		if code != http.StatusNotFound || json.Unmarshal([]byte(body), &env) != nil || env.Error.Code != "not_found" ||
			!strings.HasPrefix(h.Get("Content-Type"), "application/json") {
			t.Errorf("%s%s: %d %s %q", api, p, code, h.Get("Content-Type"), body)
		}
	}
	// Plain http redirects to https, path and query kept.
	code, _, h := do("http", web, http.MethodGet, "/login?next=%2Fapp", nil, nil)
	if code != http.StatusMovedPermanently || h.Get("Location") != "https://"+web+"/login?next=%2Fapp" {
		t.Errorf("http %s: %d %s", web, code, h.Get("Location"))
	}
	if code, _, h = do("http", api, http.MethodGet, "/healthz", nil, nil); code != http.StatusMovedPermanently || h.Get("Location") != "https://"+api+"/healthz" {
		t.Errorf("http %s: %d %s", api, code, h.Get("Location"))
	}
	// client_max_body_size = UPLOAD_MAX_BYTES (10 MiB): one byte more is refused by nginx.
	if code, _, _ = do("https", web, http.MethodPut, "/media/private/x", bytes.NewReader(make([]byte, 10<<20+1)), nil); code != http.StatusRequestEntityTooLarge {
		t.Errorf("an upload above 10 MiB: %d, want 413", code)
	}
	if code, body, _ := do("https", web, http.MethodPut, "/media/private/x", bytes.NewReader(make([]byte, 10<<20)), nil); code != http.StatusOK || !strings.HasPrefix(body, "public ") {
		t.Errorf("an upload of 10 MiB: %d %q", code, body)
	}
}

// TestShellcheck: every script of the kit is clean under shellcheck (sourcing kit.conf).
func TestShellcheck(t *testing.T) {
	scripts := []string{"provision.sh", "deploy.sh", "rollout.sh", "run-app.sh", "kit.conf", "testdata/stub.sh"}
	var files []testcontainers.ContainerFile
	for _, s := range scripts {
		files = append(files, testcontainers.ContainerFile{HostFilePath: filepath.Join(kitDir(t), s), ContainerFilePath: "/mnt/" + s, FileMode: 0o755})
	}
	args := []string{"-x", "-P", "SCRIPTDIR"}
	for _, s := range scripts {
		args = append(args, "/mnt/"+s)
	}
	code, out := runToExit(t, shellcheckImage, testcontainers.WithFiles(files...), testcontainers.WithCmd(args...))
	if code != 0 {
		t.Fatalf("shellcheck: exit %d\n%s", code, out)
	}
}

// TestNginxErrorLogsHoldNoCredential: with every upstream down (as while pm2 restarts the apps), the error and
// access logs keep no evidence token, SSE ticket or URL signature, while an ordinary failure is still logged
// with its request line and the evidence status stays visible.
func TestNginxErrorLogsHoldNoCredential(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ctr, do := startNginx(t, ctx)
	for _, r := range []struct{ vhost, method, path string }{
		{api, http.MethodGet, "/evidence/EVTOKEN-PATH-1"},
		{api, http.MethodGet, "/evidence?k=EVTOKEN-QUERY-2"},
		{api, http.MethodGet, "/v1/mobile/events?ticket=SSE-TICKET-3"},
		{api, http.MethodGet, "/media/t/o.jpg?X-LT-Expires=1&X-LT-Signature=API-MEDIA-SIG-4"},
		{web, http.MethodGet, "/media/t/o.jpg?X-LT-Expires=1&X-LT-Signature=WEB-MEDIA-SIG-5"},
		{web, http.MethodPut, "/api/go/v1/uploads/local/k.jpg?X-LT-Expires=1&X-LT-Signature=BFF-UPLOAD-SIG-6"},
		{api, http.MethodGet, "/v1/mobile/heartbeat?control=1"},
		{web, http.MethodGet, "/login?control=2"},
	} {
		if code, _, _ := do("https", r.vhost, r.method, r.path, nil, nil); code != http.StatusBadGateway {
			t.Errorf("%s %s%s: %d, want 502 with no upstream", r.method, r.vhost, r.path, code)
		}
	}
	// A signed upload over the body limit is refused by nginx itself (413), which the error log also notes.
	if code, _, _ := do("https", web, http.MethodPut, "/media/up.jpg?X-LT-Signature=OVERSIZE-SIG-7", bytes.NewReader(make([]byte, 10<<20+1)), nil); code != http.StatusRequestEntityTooLarge {
		t.Errorf("an upload above 10 MiB: %d, want 413", code)
	}
	logs := map[string]string{}
	for _, f := range []string{"logitrack-api.error.log", "logitrack-web.error.log", "logitrack-api.access.log", "logitrack-web.access.log"} {
		code, rd, err := ctr.Exec(ctx, []string{"cat", "/var/log/nginx/" + f}, tcexec.Multiplexed())
		if err != nil || code != 0 {
			t.Fatalf("cat %s: exit %d: %v", f, code, err)
		}
		logs[f] = string(must(io.ReadAll(rd)))
	}
	all := strings.Join([]string{logs["logitrack-api.error.log"], logs["logitrack-web.error.log"], logs["logitrack-api.access.log"], logs["logitrack-web.access.log"]}, "\n")
	for _, secret := range []string{"EVTOKEN-PATH-1", "EVTOKEN-QUERY-2", "SSE-TICKET-3", "API-MEDIA-SIG-4", "WEB-MEDIA-SIG-5", "BFF-UPLOAD-SIG-6", "OVERSIZE-SIG-7"} {
		if strings.Contains(all, secret) {
			t.Errorf("an nginx log holds %s:\n%s", secret, all)
		}
	}
	// Ordinary upstream failures keep their diagnostics.
	if !strings.Contains(logs["logitrack-api.error.log"], "GET /v1/mobile/heartbeat?control=1") ||
		!strings.Contains(logs["logitrack-web.error.log"], "GET /login?control=2") {
		t.Errorf("an ordinary upstream failure was not logged:\n%s", all)
	}
	// Evidence requests: the status only, the path replaced.
	if n := len(regexp.MustCompile(`"GET /evidence/- HTTP/[0-9.]+" 502 `).FindAllString(logs["logitrack-api.access.log"], -1)); n != 2 {
		t.Errorf("the evidence status lines are missing:\n%s", logs["logitrack-api.access.log"])
	}
}

// TestRestoreKeepsTheDefinerOwners runs README section 7's restore as written on a migrated PostgreSQL 18
// database: the SECURITY DEFINER helpers stay logitrack_rls_definer's, the owner check prints two equal
// numbers, and logitrack_app can number a task. The same restore with --no-owner --role=logitrack_migrator
// (the earlier runbook) breaks numbering, which is why the README forbids it.
func TestRestoreKeepsTheDefinerOwners(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var block string
	for _, b := range readmeBlocks(t, "7.") {
		if strings.Contains(b, "pg_restore") {
			block = b
		}
	}
	// The README's statements: psql -c ..., the pg_restore line, the owner check (-tAc).
	var create []string
	for _, m := range regexp.MustCompile(` -c "([^"]+)"`).FindAllStringSubmatch(block, -1) {
		create = append(create, m[1])
	}
	restoreLine := regexp.MustCompile(`pg_restore ([^<\n]*)<`).FindStringSubmatch(block)
	check := regexp.MustCompile(`-tAc "([^"]+)"`).FindStringSubmatch(block)
	if len(create) == 0 || restoreLine == nil || check == nil {
		t.Fatalf("README section 7 restore not understood:\n%s", block)
	}
	dbWord := regexp.MustCompile(`\blogitrack\b`)

	src := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, src, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	c := src.Cluster()
	dump := "/tmp/" + src.Name + ".dump"
	if code, out, err := c.Exec(ctx, "pg_dump", "-Fc", "--username=postgres", "--dbname="+src.Name, "--file="+dump); err != nil || code != 0 {
		t.Fatalf("pg_dump: exit %d: %v\n%s", code, err, out)
	}

	restore := func(t *testing.T, extra string) *pgtest.Database {
		t.Helper()
		dst := pgtest.NewDatabase(t)
		ident := pgx.Identifier{dst.Name}.Sanitize()
		for _, q := range create {
			if err := c.SuperExec(ctx, dbWord.ReplaceAllString(q, ident)); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		args := dbWord.ReplaceAllString(strings.TrimSpace(restoreLine[1]), dst.Name)
		cmd := "pg_restore --username=postgres " + args + " " + extra + " < " + dump
		if code, out, err := c.Exec(ctx, "sh", "-c", cmd); err != nil || code != 0 {
			t.Fatalf("%s: exit %d: %v\n%s", cmd, code, err, out)
		}
		return dst
	}
	conn := func(t *testing.T, url string) *pgx.Conn {
		t.Helper()
		cn, err := pgx.Connect(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cn.Close(context.Background()) })
		return cn
	}

	t.Run("as the README says", func(t *testing.T) {
		dst := restore(t, "")
		var definer, all int
		if err := conn(t, dst.SuperURL()).QueryRow(ctx, check[1]).Scan(&definer, &all); err != nil {
			t.Fatal(err)
		}
		if all == 0 || definer != all {
			t.Fatalf("owner check: %d of %d SECURITY DEFINER helpers are logitrack_rls_definer's", definer, all)
		}
		var n int
		if err := conn(t, dst.URL(db.RoleApp)).QueryRow(ctx, `SELECT next_task_seq('first_mile', current_date)`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("next_task_seq as logitrack_app after the restore: %d, %v", n, err)
		}
	})
	t.Run("without the owners", func(t *testing.T) {
		dst := restore(t, "--no-owner --role=logitrack_migrator")
		var n int
		err := conn(t, dst.URL(db.RoleApp)).QueryRow(ctx, `SELECT next_task_seq('first_mile', current_date)`).Scan(&n)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("next_task_seq after a restore without owners: %d, %v; want 42501 (the README must keep forbidding it)", n, err)
		}
	})
}
