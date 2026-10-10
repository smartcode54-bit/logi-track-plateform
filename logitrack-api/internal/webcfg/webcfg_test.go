package webcfg_test

import (
	"encoding/json"
	"io"
	"maps"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/webcfg"
)

func sources(f webcfg.Flags, src string) []string {
	var out []string
	for _, d := range webcfg.Domains {
		if f.Source(d) == src {
			out = append(out, d)
		}
	}
	return out
}

func TestDomainsAreTheNineCoexistenceDomains(t *testing.T) {
	want := []string{"auth", "masterdata", "operations", "billing", "hr", "comms", "security", "mobile_release", "dashboard"}
	if !slices.Equal(webcfg.Domains, want) {
		t.Fatalf("Domains = %v, want %v (developer-spec.md §12.1)", webcfg.Domains, want)
	}
}

func TestParseLayersOverridesOverOwnedDomains(t *testing.T) {
	cases := []struct {
		name, owned, overrides string
		wantGo                 []string
	}{
		{"nothing set: every domain on Firestore", "", "", nil},
		{"P0: auth flipped by override only", "", "auth=go", []string{"auth"}},
		{"P1: owned domains default to go", "auth,masterdata", "", []string{"auth", "masterdata"}},
		{"rollback of an owned domain", "auth, masterdata ,", "masterdata=firebase", []string{"auth"}},
		{"all", "all", "", webcfg.Domains},
		{"all with a rollback", " all ", "billing=firebase, hr = firebase", []string{"auth", "masterdata", "operations", "comms", "security", "mobile_release", "dashboard"}},
		{"override can add and remove", "billing", "billing=firebase,dashboard=go", []string{"dashboard"}},
		{"explicit firebase on a non-owned domain", "", "comms=firebase", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, errs := webcfg.Parse(tc.owned, tc.overrides)
			if len(errs) != 0 {
				t.Fatalf("errors: %v", errs)
			}
			if got := sources(f, webcfg.SourceGo); !slices.Equal(got, tc.wantGo) {
				t.Fatalf("go domains = %v, want %v", got, tc.wantGo)
			}
		})
	}
}

func TestParseRejectsBadValuesWithoutEchoingThem(t *testing.T) {
	cases := []struct {
		name, owned, overrides, wantVar string
	}{
		{"unknown owned domain", "auth,finance", "", "PG_OWNED_DOMAINS"},
		{"all combined", "all,auth", "", "PG_OWNED_DOMAINS"},
		{"owned twice", "hr,hr", "", "PG_OWNED_DOMAINS"},
		{"upper case", "AUTH", "", "PG_OWNED_DOMAINS"},
		{"override without value", "", "auth", "WEB_FLAG_OVERRIDES"},
		{"override unknown source", "", "auth=postgres", "WEB_FLAG_OVERRIDES"},
		{"override unknown domain", "", "finance=go", "WEB_FLAG_OVERRIDES"},
		{"override twice", "", "auth=go,auth=firebase", "WEB_FLAG_OVERRIDES"},
		{"override all", "", "all=go", "WEB_FLAG_OVERRIDES"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := webcfg.Parse(tc.owned, tc.overrides)
			if len(errs) != 1 || !strings.HasPrefix(errs[0], tc.wantVar+": ") {
				t.Fatalf("errors = %v, want one %s error", errs, tc.wantVar)
			}
			for _, v := range []string{"finance", "postgres", "AUTH"} {
				if strings.Contains(errs[0], v) {
					t.Fatalf("error echoes the value %q: %s", v, errs[0])
				}
			}
		})
	}
	if _, errs := webcfg.Parse("nope", "nope"); len(errs) != 2 {
		t.Fatalf("both variables are reported: %v", errs)
	}
}

func TestZeroFlagsServeEveryDomainFromFirestore(t *testing.T) {
	var f webcfg.Flags
	if got := sources(f, webcfg.SourceFirebase); !slices.Equal(got, webcfg.Domains) {
		t.Fatalf("zero value: firebase domains = %v", got)
	}
	if f.Source("unknown") != webcfg.SourceFirebase {
		t.Fatal("unknown domain must be firebase")
	}
}

func serve(t *testing.T, f webcfg.Flags, path string) (int, string, map[string]any) {
	t.Helper()
	fa := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	fa.Use(httpx.RequestID())
	ingress.Mount(fa, ingress.Internal, []ingress.Group{webcfg.Group(f)}, nil)
	resp, err := fa.Test(httptest.NewRequest("GET", path, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatalf("body %q: %v", b, err)
	}
	return resp.StatusCode, resp.Header.Get("Cache-Control"), body
}

func TestEndpointServesEveryDomainInTheDataEnvelope(t *testing.T) {
	f, errs := webcfg.Parse("auth,masterdata", "masterdata=firebase,billing=go")
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	status, cc, body := serve(t, f, "/v1/config/web-flags")
	if status != 200 || cc != "no-store" {
		t.Fatalf("status %d, Cache-Control %q", status, cc)
	}
	data, _ := body["data"].(map[string]any)
	domains, _ := data["domains"].(map[string]any)
	want := map[string]any{"auth": "go", "masterdata": "firebase", "operations": "firebase", "billing": "go",
		"hr": "firebase", "comms": "firebase", "security": "firebase", "mobile_release": "firebase", "dashboard": "firebase"}
	if !maps.Equal(domains, want) || len(body) != 1 || len(data) != 1 {
		t.Fatalf("body = %v, want {\"data\":{\"domains\":%v}}", body, want)
	}
}

func TestOtherConfigPathsAreNotFound(t *testing.T) {
	status, _, body := serve(t, webcfg.Flags{}, "/v1/config/other")
	e, _ := body["error"].(map[string]any)
	if status != 404 || e["code"] != "not_found" {
		t.Fatalf("status %d body %v", status, body)
	}
}

// The flags are served on the internal listener only, so the browser reaches them through the BFF
// and API_PUBLIC_DOMAIN answers 404 (main spec §2.6, Appendix B §B.2.1).
func TestServedOnTheInternalListenerOnly(t *testing.T) {
	cfg := &app.APIConfig{PublicRouteGroups: ingress.PublicPrefixes}
	a, err := app.NewAPI(cfg, zerolog.Nop(), webcfg.Group(webcfg.Flags{}))
	if err != nil {
		t.Fatal(err)
	}
	route := ingress.Route{Method: "GET", Path: "/v1/config/web-flags"}
	routes := a.Routes()
	if !slices.Contains(routes[ingress.Internal], route) {
		t.Fatalf("internal listener lacks %v: %v", route, routes[ingress.Internal])
	}
	for _, r := range routes[ingress.Public] {
		if strings.HasPrefix(r.Path, "/v1/config") {
			t.Fatalf("public listener serves %v", r)
		}
	}
}
