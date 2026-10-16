package iam

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

func TestReviewProjectedDuplicateTenantKeepsConflict(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Use(httpx.AdmissionGuard(nil))
	app.Post("/tenant", func(c fiber.Ctx) error {
		httpx.SetProjectionAudience(c, httpx.ProjectionAudience{Platform: true})
		if err := httpx.EndpointGate(c, iamPolicy("POST", "/v1/tenants", "iam.tenant")); err != nil {
			return err
		}
		return errAlreadyExists("code", "SENSITIVE-input")
	})
	res, err := app.Test(httptest.NewRequest("POST", "/tenant", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 409 || !strings.Contains(string(raw), `"code":"already_exists"`) || strings.Contains(string(raw), "SENSITIVE-input") {
		t.Fatalf("projected conflict: %d %s", res.StatusCode, raw)
	}
}

func TestReviewKeyPredicatePreservesOrdinaryValidation(t *testing.T) {
	registry := ingress.Mount(fiber.New(), ingress.Internal, APIKeyGroups(nil, func(c fiber.Ctx) error { return c.Next() }), nil)
	var policy httpx.EndpointPolicy
	for _, p := range registry.Policies() {
		if p.ID == "POST /v1/api-keys" {
			policy = p
		}
	}
	for _, capability := range []string{"users:view", "platform:manage_tenants", "fleet:manage_customers"} {
		for _, support := range []bool{false, true} {
			app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
			app.Post("/keys", func(c fiber.Ctx) error {
				p := &authz.Principal{Platform: []authz.PlatformRole{authz.PlatformAdmin}}
				if support {
					p.TenantRole = authz.SupportReadOnly
				}
				authz.SetPrincipal(c, p)
				_, err := policy.Predicate(c)
				if err != nil {
					return err
				}
				return c.SendStatus(204)
			})
			body := `{"name":"x","scope":"cf_shim","capabilities":["` + capability + `"]}`
			req := httptest.NewRequest("POST", "/keys", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			want := 204
			if support {
				want = 400
			}
			if res.StatusCode != want {
				t.Errorf("capability=%s support=%v: %d want %d", capability, support, res.StatusCode, want)
			}
		}
	}
}

func TestReviewKeyFieldErrorsStayTyped(t *testing.T) {
	for _, tc := range []struct{ scope, cap, reason string }{
		{"script", "users:view", "not_allowed_for_platform_key"},
		{"script", "platform:manage_tenants", "not_allowed"},
		{"cf_shim", "fleet:manage_customers", "not_allowed_for_scope"},
	} {
		app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
		app.Use(httpx.AdmissionGuard(func(c fiber.Ctx) {
			if p := authz.PrincipalFrom(c); p != nil {
				httpx.SetProjectionAudience(c, httpx.ProjectionAudience{Platform: p.IsPlatform()})
			}
		}))
		groups := APIKeyGroups(&APIKeys{now: time.Now}, func(c fiber.Ctx) error {
			authz.SetPrincipal(c, &authz.Principal{Platform: []authz.PlatformRole{authz.PlatformAdmin}, Caps: authz.NewCapSet(authz.SecurityManageAPIKeys)})
			return c.Next()
		})
		ingress.Mount(app, ingress.Internal, groups, nil)
		body := `{"name":"x","scope":"` + tc.scope + `","capabilities":["` + tc.cap + `"]}`
		req := httptest.NewRequest("POST", "/v1/api-keys", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 422 || !strings.Contains(string(raw), `"reason":"`+tc.reason+`"`) || strings.Contains(string(raw), tc.cap) {
			t.Errorf("%s: %d %s", tc.cap, res.StatusCode, raw)
		}
	}
}
