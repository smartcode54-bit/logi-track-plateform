//go:build integration

// Container checks of the staging VM kit (make test-integration): the nginx vhosts under nginx 1.24 (the
// VM's version) with dummy certificate files at the real paths, `nginx -t` and the routing they promise,
// and shellcheck over every script.
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
	"io"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

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

// TestNginxRouting runs the vhosts against echo upstreams and checks what each host forwards.
func TestNginxRouting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	stubs := filepath.Join(dir, "stubs.conf")
	writeFile(t, stubs, upstreamStubs, 0o644)
	ctr, err := testcontainers.Run(ctx, nginxImage,
		testcontainers.WithFiles(nginxFiles(t, testcontainers.ContainerFile{HostFilePath: stubs,
			ContainerFilePath: "/etc/nginx/conf.d/zz-upstream-stubs.conf", FileMode: 0o644})...),
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
	do := func(scheme, vhost, method, path string, body io.Reader, hdr map[string]string) (int, string, http.Header) {
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
	const web, api = "logi.showkhun.co", "logi-api.showkhun.co"
	spoof := map[string]string{"X-Forwarded-For": "203.0.113.66", "CF-Connecting-IP": "198.51.100.77"}

	for _, c := range []struct {
		vhost, path, want string
	}{
		{web, "/login", "web /login "},
		{web, "/api/go/v1/events", "web /api/go/v1/events "},
		{web, "/media/private/a/b.jpg?X-LT-Expires=1&X-LT-Signature=s", "public /media/private/a/b.jpg?X-LT-Expires=1&X-LT-Signature=s "},
		{api, "/healthz", "public /healthz "},
		{api, "/v1/mobile/events?ticket=t", "public /v1/mobile/events?ticket=t "},
		{api, "/v1/auth/login", "public /v1/auth/login "},
		{api, "/public/v1/x", "public /public/v1/x "},
		{api, "/evidence/tok", "public /evidence/tok "},
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
