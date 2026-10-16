package iam

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestPlatformUserProjectionUsesEachTargetConsent(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	one, two, global := "alice@example.test", "bob@example.test", "global@example.test"
	users := []User{{ID: uuid.New(), Email: &one, Memberships: []Membership{{TenantID: a, TenantNameTh: "A"}}}, {ID: uuid.New(), Email: &two, Memberships: []Membership{{TenantID: b, TenantNameTh: "B"}}}, {ID: uuid.New(), Email: &global, Memberships: []Membership{{TenantID: a}, {TenantID: b}}}}
	for _, selected := range []string{"", a.String(), b.String()} {
		t.Run("selected="+selected, func(t *testing.T) {
			app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error {
				if e, ok := err.(*httpx.Error); ok {
					return httpx.WriteError(c, e)
				}
				return err
			}})
			app.Use(httpx.AdmissionGuard(nil))
			app.Get("/users", func(c fiber.Ctx) error {
				audience := httpx.ProjectionAudience{Platform: true, Actor: uuid.NewString(), Tenant: selected, Finalize: func(_ context.Context, owners []string, fn func(map[string]httpx.Consent) error) error {
					if len(owners) != 2 {
						t.Errorf("owners=%v", owners)
					}
					return fn(map[string]httpx.Consent{a.String(): {Enabled: true, Version: "1"}, b.String(): {Enabled: false, Version: "0"}})
				}}
				httpx.SetProjectionAudience(c, audience)
				if err := httpx.EndpointGate(c, iamPolicy("GET", "/v1/platform/users", "iam.platform-user")); err != nil {
					return err
				}
				return httpx.JSON(c, 200, users)
			})
			res, err := app.Test(httptest.NewRequest("GET", "/users", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			raw, _ := io.ReadAll(res.Body)
			if res.StatusCode != 200 {
				t.Fatalf("status=%d %s", res.StatusCode, raw)
			}
			var out struct {
				Data []struct {
					Email string `json:"email"`
				} `json:"data"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Data) != 3 || out.Data[0].Email != one || out.Data[1].Email != "****" || out.Data[2].Email != "****" {
				t.Fatalf("projection %s", raw)
			}
		})
	}
}
func TestPlatformMembershipProjectionNeverSignsFiles(t *testing.T) {
	id := uuid.New()
	url := "https://private.example.invalid/bearer"
	key := "private/object"
	at := time.Now()
	tenant := Subcontractor{TenantDetail: TenantDetail{Tenant: Tenant{ID: id, CreatedAt: at}}, Documents: []TenantDocument{{FileID: uuid.New(), URL: &url, Key: &key}}}
	app := fiber.New()
	app.Get("/partner", func(c fiber.Ctx) error {
		httpx.SetProjectionAudience(c, httpx.ProjectionAudience{Platform: true, Finalize: func(_ context.Context, _ []string, fn func(map[string]httpx.Consent) error) error {
			return fn(map[string]httpx.Consent{id.String(): {Enabled: true, Version: "1"}})
		}})
		httpx.RequireResponseProjection(c)
		if err := httpx.EndpointGate(c, iamPolicy("GET", "/v1/subcontractors/:id", "iam.subcontractor")); err != nil {
			return err
		}
		return httpx.JSON(c, 200, tenant)
	})
	res, err := app.Test(httptest.NewRequest("GET", "/partner", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, raw)
	}
	var out struct {
		Data struct {
			Documents []struct {
				URL *string `json:"url"`
				Key *string `json:"key"`
			} `json:"documents"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data.Documents) != 1 || out.Data.Documents[0].URL != nil || out.Data.Documents[0].Key != nil {
		t.Fatalf("file leaked %s", raw)
	}
}
