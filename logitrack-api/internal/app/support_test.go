package app

import (
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Walk the actual production registrations, not a second route inventory. The
// synthetic virtual principal must be denied before a nil dependency is touched.
func TestSupportDefaultDenyOnBothProductionListeners(t *testing.T) {
	if httpx.SupportEntryEnabled {
		t.Fatal("support entry must remain OFF")
	}
	for _, on := range []bool{false, true} {
		a, err := FullAPI(&APIConfig{PublicRouteGroups: ingress.PublicPrefixes}, zerolog.Nop(), APIDeps{})
		if err != nil {
			t.Fatal(err)
		}
		for listener, server := range map[string]*fiber.App{ingress.Internal: a.internal, ingress.Public: a.public} {
			endpoints := map[uintptr]bool{}
			for _, route := range server.GetRoutes(true) {
				endpoints[reflect.ValueOf(route.Handlers).Pointer()] = true
			}
			for _, stack := range server.Stack() {
				for _, route := range stack {
					if route.Path != "/" && !endpoints[reflect.ValueOf(route.Handlers).Pointer()] {
						route.Handlers = []fiber.Handler{func(c fiber.Ctx) error { return c.Next() }} // Authentication has been supplied by the test identity.
					}
					if route.Path == "/" {
						route.Handlers = append([]fiber.Handler{func(c fiber.Ctx) error {
							authz.SetPrincipal(c, &authz.Principal{TenantRole: authz.TenantRole("support_readonly")})
							httpx.SetProjectionAudience(c, httpx.ProjectionAudience{Support: true, Tenant: "selected", ConsentVersion: "1", Consents: map[string]httpx.Consent{"selected": {Enabled: on, Version: "1"}}})
							return c.Next()
						}}, route.Handlers...)
					}
				}
			}
			policies := a.SupportPolicies()[listener]
			for _, p := range policies {
				if p.Phase == 1 {
					continue
				} // The narrow dependency is exercised by parties/support_options_test.go.
				path := p.Route
				for _, part := range strings.Split(path, "/") {
					if strings.HasPrefix(part, ":") {
						path = strings.ReplaceAll(path, part, "018f47a0-7b31-7c22-8b10-9733e2bba819")
					}
				}
				path = strings.ReplaceAll(path, "*", "opaque")
				req := httptest.NewRequest(p.Method, path, nil)
				res, err := server.Test(req)
				if err != nil {
					t.Fatalf("%s %s: %v", listener, p.ID, err)
				}
				body, _ := io.ReadAll(res.Body)
				_ = res.Body.Close()
				expected := httpx.CodeSupportViewUnavailable
				if p.Class == httpx.PolicyTenantWrite {
					expected = httpx.CodeSupportReadOnly
				}
				if res.StatusCode != 403 || !strings.Contains(string(body), expected) {
					t.Fatalf("%s %s ON=%v: %d %s", listener, p.ID, on, res.StatusCode, body)
				}
			}
		}
	}
}
