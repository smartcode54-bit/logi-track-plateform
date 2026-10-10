package authz

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

type envelope struct {
	Error struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// serve runs one request through guard with p as the principal (nil: none).
func serve(t *testing.T, p *Principal, guard fiber.Handler) (int, envelope) {
	t.Helper()
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Use(func(c fiber.Ctx) error {
		if p != nil {
			SetPrincipal(c, p)
		}
		return c.Next()
	})
	app.Get("/x", guard, func(c fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
	res, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var env envelope
	if res.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(res.Body)
		if err := json.Unmarshal(b, &env); err != nil {
			t.Fatalf("status %d, body %s", res.StatusCode, b)
		}
	}
	return res.StatusCode, env
}

func TestRequireCap(t *testing.T) {
	manager := &Principal{TenantID: ptr(carrier), TenantRole: Manager, Caps: RoleCaps(Manager, false, nil)}
	if code, _ := serve(t, manager, RequireCap(OperationsManageTasks)); code != http.StatusNoContent {
		t.Fatalf("manager with operations:manage_tasks: %d", code)
	}
	// any-of
	if code, _ := serve(t, manager, RequireCap(HRViewPayroll, OperationsViewLineHaul)); code != http.StatusNoContent {
		t.Fatalf("any-of: %d", code)
	}
	code, env := serve(t, manager, RequireCap(AccountingRecomputeForce, AccountingOverridePrice))
	if code != http.StatusForbidden || env.Error.Code != CodePermissionDenied {
		t.Fatalf("missing key: %d %s", code, env.Error.Code)
	}
	missing, _ := env.Error.Details["missingCapability"].([]any)
	if len(missing) != 2 || missing[0] != "accounting:recompute_force" || missing[1] != "accounting:override_price" {
		t.Fatalf("details.missingCapability = %v", env.Error.Details)
	}
	// A carrier manager holds no global key (R60).
	if code, _ := serve(t, manager, RequireCap(FleetManageCustomers)); code != http.StatusForbidden {
		t.Fatalf("carrier manager on a global key: %d", code)
	}
	// An unresolved principal holds nothing: fail closed.
	if code, _ := serve(t, &Principal{TenantID: ptr(ownFleet), TenantRole: TenantAdmin}, RequireCap(FleetViewTrucks)); code != http.StatusForbidden {
		t.Fatalf("unresolved principal: %d", code)
	}
	if code, env := serve(t, nil, RequireCap(FleetViewTrucks)); code != http.StatusUnauthorized || env.Error.Code != httpx.CodeUnauthenticated {
		t.Fatalf("no principal: %d %s", code, env.Error.Code)
	}
	for _, bad := range [][]Cap{nil, {"fleet:typo"}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("RequireCap(%v) did not panic", bad)
				}
			}()
			RequireCap(bad...)
		}()
	}
}

func TestRequireTenant(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    *Principal
		want int
	}{
		{"member", &Principal{TenantID: ptr(carrier), TenantRole: User}, http.StatusNoContent},
		{"customer scope", &Principal{PartyIDs: []uuid.UUID{uuid.New()}}, http.StatusForbidden},
		{"platform without header", &Principal{Platform: []PlatformRole{PlatformAdmin}}, http.StatusForbidden},
		{"platform acting as a tenant", &Principal{Platform: []PlatformRole{PlatformAdmin}, ActOnTenant: ptr(carrier)}, http.StatusNoContent},
		{"platform with *", &Principal{Platform: []PlatformRole{Support}, ActOnAll: true}, http.StatusNoContent},
	} {
		code, env := serve(t, tc.p, RequireTenant())
		if code != tc.want || (code == http.StatusForbidden && env.Error.Code != CodeTenantRequired) {
			t.Errorf("%s: %d %s, want %d", tc.name, code, env.Error.Code, tc.want)
		}
	}
}

func TestRequirePlatformAndSteward(t *testing.T) {
	pa := &Principal{Platform: []PlatformRole{PlatformAdmin}, Steward: true}
	su := &Principal{Platform: []PlatformRole{Support}}
	if code, _ := serve(t, pa, RequirePlatform(PlatformAdmin)); code != http.StatusNoContent {
		t.Errorf("platform_admin: %d", code)
	}
	if code, env := serve(t, su, RequirePlatform(PlatformAdmin)); code != http.StatusForbidden || env.Error.Code != CodePermissionDenied {
		t.Errorf("support on a platform_admin route: %d %s", code, env.Error.Code)
	}
	if code, _ := serve(t, pa, RequireSteward()); code != http.StatusNoContent {
		t.Errorf("steward: %d", code)
	}
	carrierTA := &Principal{TenantID: ptr(carrier), TenantRole: TenantAdmin}
	if code, _ := serve(t, carrierTA, RequireSteward()); code != http.StatusForbidden {
		t.Errorf("carrier tenant_admin on a steward route: %d", code)
	}
}
