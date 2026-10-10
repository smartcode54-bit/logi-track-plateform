package ingress_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// The edge proxy (deploy/Caddyfile, TW2) is the first fence of the ingress policy: the
// API_PUBLIC_DOMAIN site may forward only the public route groups, and no site may reach the
// internal listener (main spec §2.6, §10.12).

const caddyfilePath = "../../../deploy/Caddyfile"

// siteBlock returns the lines of the top-level block whose header is `header {`.
func siteBlock(t *testing.T, file []string, header string) []string {
	t.Helper()
	for i, l := range file {
		if l != header+" {" {
			continue
		}
		for j := i + 1; j < len(file); j++ {
			if file[j] == "}" {
				return file[i+1 : j]
			}
		}
	}
	t.Fatalf("%s: no site block %q", caddyfilePath, header)
	return nil
}

func readCaddyfile(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(caddyfilePath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(b), "\n")
}

func TestCaddyfilePublicSiteForwardsOnlyPublicPrefixes(t *testing.T) {
	block := siteBlock(t, readCaddyfile(t), "{$API_PUBLIC_DOMAIN}")
	var paths []string
	for _, l := range block {
		if f := strings.Fields(l); len(f) > 2 && f[0] == "@public" && f[1] == "path" {
			paths = f[2:]
		}
	}
	if paths == nil {
		t.Fatal("API_PUBLIC_DOMAIN site has no `@public path ...` matcher")
	}
	var prefixes []string
	for _, p := range paths {
		p = strings.TrimSuffix(p, "/*")
		if !slices.Contains(prefixes, p) {
			prefixes = append(prefixes, p)
		}
	}
	want := slices.Clone(ingress.PublicPrefixes)
	slices.Sort(prefixes)
	slices.Sort(want)
	if !slices.Equal(prefixes, want) {
		t.Fatalf("Caddy public matcher covers %v, ingress.PublicPrefixes is %v", prefixes, want)
	}
	if !slices.ContainsFunc(block, func(l string) bool { return strings.TrimSpace(l) == "import not_found" }) {
		t.Fatal("API_PUBLIC_DOMAIN site must answer everything else with the not_found envelope")
	}
}

func TestCaddyfileNeverReachesTheInternalListener(t *testing.T) {
	allowed := []string{"web:3000", "api{$API_PUBLIC_ADDR}", "minio:9000"}
	n := 0
	for i, l := range readCaddyfile(t) {
		code, _, _ := strings.Cut(l, "#")
		if strings.Contains(code, "API_INTERNAL") || strings.Contains(code, ":8080") {
			t.Errorf("%s:%d mentions the internal listener: %q", caddyfilePath, i+1, l)
		}
		f := strings.Fields(code)
		if len(f) >= 2 && f[0] == "reverse_proxy" {
			n++
			if !slices.Contains(allowed, f[1]) {
				t.Errorf("%s:%d proxies to %q, want one of %v", caddyfilePath, i+1, f[1], allowed)
			}
		}
	}
	if n == 0 {
		t.Fatal("no reverse_proxy found")
	}
}

func TestCaddyfileTunnelSiteExposesOnlyPostbacks(t *testing.T) {
	block := siteBlock(t, readCaddyfile(t), "http://:8090")
	var handles []string
	for _, l := range block {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "handle" && f[1] != "{" {
			handles = append(handles, f[1])
		}
	}
	if !slices.Equal(handles, []string{"/public/v1/*"}) {
		t.Fatalf("tunnel site handles %v, want only /public/v1/*", handles)
	}
}
