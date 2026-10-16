package app

import (
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"net/http/httptest"
	"testing"
)

func TestSupportStorageCallerHasNoContractorReach(t *testing.T) {
	tenant := uuid.New()
	for _, role := range []authz.TenantRole{authz.SupportReadOnly, authz.Manager} {
		app := fiber.New()
		app.Get("/", func(c fiber.Ctx) error {
			authz.SetPrincipal(c, &authz.Principal{TenantID: &tenant, TenantRole: role, Steward: true, Platform: []authz.PlatformRole{authz.PlatformAdmin}})
			caller, ok := StorageCaller(c)
			if !ok || caller.TenantID == nil || *caller.TenantID != tenant {
				t.Fatal(caller)
			}
			if role == authz.SupportReadOnly {
				// mayRead requires Staff before it can call IsDirectSubtenant.
				if caller.Staff || caller.Steward || caller.ReadAll {
					t.Fatalf("support inherited file reach: %+v", caller)
				}
			} else if !caller.Staff {
				t.Fatal("member staff reach changed")
			}
			return c.SendStatus(204)
		})
		res, err := app.Test(httptest.NewRequest("GET", "/", nil))
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
	}
}
