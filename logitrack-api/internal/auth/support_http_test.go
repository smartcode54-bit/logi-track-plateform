package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

func TestTenantContextInputAndDisabledEntry(t *testing.T) {
	svc := &Service{}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"mode":"leave","tenantId":null}`, 422}, {`{"mode":"support"}`, 422}, {`{"mode":"unknown"}`, 422},
		{`{"mode":null}`, 422}, {`{"mode":""}`, 422}, {`{"mode":"leave","role":"tenant_admin"}`, 422},
		{`{"mode":"leave","mode":"member"}`, 400}, {`{"mode":"support","tenantId":"00000000-0000-0000-0000-000000000000"}`, 422},
		{`{"mode":"support","tenantId":"0199c000-0000-7000-8000-00000000000b"}`, 403},
	} {
		app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
		app.Post("/tenant", func(c fiber.Ctx) error {
			authz.SetPrincipal(c, &authz.Principal{UserID: uuid.New(), SessionID: uuid.New(), AMR: AMRPassword})
			return svc.handleTenant(c)
		})
		req := httptest.NewRequest("POST", "/tenant", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != tc.status {
			t.Fatalf("%s: %d %s", tc.body, res.StatusCode, raw)
		}
	}
}

func TestOwnAccountResponseSchemas(t *testing.T) {
	tid := uuid.New()
	for _, tc := range []struct {
		schema string
		value  any
	}{
		{"auth.me", &Me{Tenant: &Tenant{ID: tid, NameTh: "****"}, Tenants: []Tenant{{ID: tid}}, SessionContext: SessionContextView{Mode: "support", Version: "1"}, SupportAccess: &SupportAccessView{TenantID: tid, Masked: true, Version: "0", ReadOnly: true}}},
		{"auth.tenant_label", []Tenant{{ID: tid, NameTh: "****"}}}, {"auth.sessions", []SessionView{{ID: uuid.New()}}},
		{"auth.access", &AccessToken{AccessToken: "own-response-token", ExpiresIn: 900}},
	} {
		app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
		app.Use(httpx.AdmissionGuard(nil), func(c fiber.Ctx) error {
			httpx.SetProjectionAudience(c, httpx.ProjectionAudience{Support: true, Actor: "own", Tenant: tid.String(), ConsentVersion: "0", Finalize: func(_ context.Context, owners []string, fn func(map[string]httpx.Consent) error) error {
				m := map[string]httpx.Consent{}
				for _, id := range owners {
					m[id] = httpx.Consent{Version: "0"}
				}
				return fn(m)
			}})
			return c.Next()
		})
		registry := ingress.Mount(app, ingress.Internal, []ingress.Group{{Prefix: "/v1", Mount: func(r fiber.Router) {
			bindOwn(r, "GET", "/own", "/v1/own", tc.schema, httpx.PolicyOwnAccount, func(c fiber.Ctx) error { return httpx.JSON(c, 200, tc.value) })
		}}}, nil)
		if err := registry.Check(); err != nil {
			t.Fatal(err)
		}
		res, err := app.Test(httptest.NewRequest("GET", "/v1/own", nil))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d %s", tc.schema, res.StatusCode, raw)
		}
	}
}

func TestMaskedTenantLabelMetadata(t *testing.T) {
	tid := uuid.New()
	tx := &supportTestTx{row: func(query string, args ...any) pgx.Row {
		return supportTestRow{values: []any{"Person", (*string)(nil), "carrier", "active", supportPtr("PERSON-CODE"), true, false, int64(0)}}
	}}
	label, err := supportTenantLabel(context.Background(), tx, &authz.Principal{}, tid, "manager")
	if err != nil {
		t.Fatal(err)
	}
	for _, list := range []bool{false, true} {
		app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
		app.Use(httpx.AdmissionGuard(nil), func(c fiber.Ctx) error {
			httpx.SetProjectionAudience(c, httpx.ProjectionAudience{Support: true, Actor: "own", Tenant: tid.String(), ConsentVersion: "0", Finalize: func(_ context.Context, owners []string, fn func(map[string]httpx.Consent) error) error {
				return fn(map[string]httpx.Consent{tid.String(): {Version: "0"}})
			}})
			return c.Next()
		})
		schema := "auth.me"
		if list {
			schema = "auth.tenant_label"
		}
		ingress.Mount(app, ingress.Internal, []ingress.Group{{Prefix: "/v1", Mount: func(r fiber.Router) {
			bindOwn(r, "GET", "/labels", "/v1/labels", schema, httpx.PolicyOwnAccount, func(c fiber.Ctx) error {
				if list {
					return writeTenantLabels(c, []Tenant{label})
				}
				return writeMe(c, &Me{Tenant: &label, Tenants: []Tenant{label}})
			})
		}}}, nil)
		res, err := app.Test(httptest.NewRequest("GET", "/v1/labels", nil))
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Data json.RawMessage
			Meta struct{ Redacted []httpx.RedactedField }
		}
		err = json.NewDecoder(res.Body).Decode(&body)
		_ = res.Body.Close()
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("status %d: %s %v", res.StatusCode, body.Data, err)
		}
		prefixes := []string{"/data/tenant", "/data/tenants/0"}
		if list {
			prefixes = []string{"/data/0"}
		}
		var want []httpx.RedactedField
		for _, prefix := range prefixes {
			for _, field := range []string{"nameTh", "nameEn", "code"} {
				want = append(want, httpx.RedactedField{Path: prefix + "/" + field, Kind: httpx.Person})
			}
		}
		if !reflect.DeepEqual(body.Meta.Redacted, want) {
			t.Fatalf("redacted=%v, want %v", body.Meta.Redacted, want)
		}
		if !strings.Contains(string(body.Data), `"code":null`) {
			t.Fatalf("masked code must be explicitly null: %s", body.Data)
		}
	}
}
