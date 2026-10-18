package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

func supportApp(a httpx.ProjectionAudience, mount func(fiber.Router)) *fiber.App {
	aApp := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	aApp.Use(httpx.RequestID(), httpx.AdmissionGuard(nil), func(c fiber.Ctx) error {
		httpx.SetProjectionAudience(c, a)
		return c.Next()
	})
	ingress.Mount(aApp, ingress.Internal, []ingress.Group{{Prefix: "/v1", Mount: mount}}, nil)
	return aApp
}

func supportRequest(t *testing.T, a *fiber.App, method, path string) (int, string, httpx.ProjectionMeta) {
	t.Helper()
	r, err := a.Test(httptest.NewRequest(method, path, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Meta httpx.ProjectionMeta `json:"meta"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		t.Fatal(err)
	}
	if r.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("projected response is cacheable", r.Header)
	}
	return r.StatusCode, string(b), envelope.Meta
}

func supportAudience(enabled bool) httpx.ProjectionAudience {
	return httpx.ProjectionAudience{Support: true, Platform: true, Tenant: "selected", Actor: "actor", Session: "session", ContextVersion: "4", ConsentVersion: "7",
		Finalize: func(_ context.Context, owners []string, fn func(map[string]httpx.Consent) error) error {
			return fn(map[string]httpx.Consent{"selected": {Enabled: enabled, Version: "7"}})
		},
	}
}

func TestDashboardSummaryProjectsNestedFieldsWithoutMutatingSource(t *testing.T) {
	name, place, text, plate := "PERSON_SENTINEL", "ADDRESS_SENTINEL", "TEXT_SENTINEL", "70-0123"
	source := Summary{Period: Period{From: "2026-10-01", To: "2026-10-07", Days: 7},
		Users: &Users{Total: 2, ByRole: map[string]int64{"tenant_admin": 1, "driver": 1}},
		Trips: trips(3, 12, 1), Drivers: &Drivers{Active: 1, OnDuty: 1, Total: 2},
		Activity: &Activity{Days: []DayCount{{Date: "2026-10-01", Delivered: 3}}, Total: 3},
		Expenses: &Expenses{PendingFuel: int64Ptr(2)}, Chats: &Chats{Unassigned: 1, Urgent: 1},
		Compliance: &Compliance{TaxDue: 1, ItemsTotal: 1, Items: []ComplianceItem{{TruckID: uuid.New(), LicensePlate: plate, Kind: "tax", DueDate: &sourceDate}}},
		Recent: []RecentItem{{Type: "driver", ID: uuid.New(), At: time.Now(), Name: &name},
			{Type: "delivered", ID: uuid.New(), At: time.Now(), Plate: &plate, Place: &place},
			{Type: "maintenance", ID: uuid.New(), At: time.Now(), Text: &text}},
	}
	for _, on := range []bool{false, true} {
		t.Run(map[bool]string{false: "OFF", true: "ON"}[on], func(t *testing.T) {
			a := supportAudience(on)
			a.Finalize = func(_ context.Context, owners []string, fn func(map[string]httpx.Consent) error) error {
				if len(owners) != 1 || owners[0] != "selected" {
					t.Fatal("wrong projection owners", owners)
				}
				return fn(map[string]httpx.Consent{"selected": {Enabled: on, Version: "7"}})
			}
			app := supportApp(a, func(r fiber.Router) {
				ingress.Bind(r, "/dashboard/summary", dashboardPolicy("/v1/dashboard/summary", "dashboard.summary"), func(c fiber.Ctx) error { return httpx.JSON(c, 200, source) })
			})
			status, body, meta := supportRequest(t, app, "GET", "/v1/dashboard/summary")
			if status != 200 || meta.SupportAccess == nil || meta.SupportAccess.Masked == on || meta.SupportAccess.Version != "7" {
				t.Fatal(status, body)
			}
			for _, sentinel := range []string{name, place, text} {
				if strings.Contains(body, sentinel) != on {
					t.Fatal("incorrect disclosure", body)
				}
			}
			for _, safe := range []string{plate, sourceDate, `"delivered":3`, `"total":2`, `"tenant_admin":1`, `"onTimePct":66.7`} {
				if !strings.Contains(body, safe) {
					t.Fatal("safe value changed", safe, body)
				}
			}
			if !on {
				for _, want := range []httpx.RedactedField{{Path: "/data/recent/0/name", Kind: httpx.Person}, {Path: "/data/recent/1/place", Kind: httpx.Address}, {Path: "/data/recent/2/text", Kind: httpx.FreeText}} {
					found := false
					for _, got := range meta.Redacted {
						if got == want {
							found = true
						}
					}
					if !found {
						t.Fatal("missing redaction metadata", want, body)
					}
				}
			} else if len(meta.Redacted) != 0 {
				t.Fatal("ON response unexpectedly masked", body)
			}
		})
	}
	if *source.Recent[0].Name != name || *source.Recent[1].Place != place || *source.Recent[2].Text != text {
		t.Fatal("projection mutated cached member data")
	}
}

var sourceDate = "2026-10-18"

func int64Ptr(v int64) *int64 { return &v }

func TestDashboardFinalConsentFailureAndUnreviewedKeysFailClosed(t *testing.T) {
	for _, scenario := range []string{"version changed", "database failed", "unreviewed map key", "histogram too long", "unclassified DTO", "direct writer"} {
		t.Run(scenario, func(t *testing.T) {
			a := supportAudience(true)
			source := Summary{Recent: []RecentItem{}, Users: &Users{ByRole: map[string]int64{"driver": 1}}}
			switch scenario {
			case "version changed":
				a.Finalize = func(_ context.Context, _ []string, fn func(map[string]httpx.Consent) error) error {
					return fn(map[string]httpx.Consent{"selected": {Enabled: false, Version: "8"}})
				}
			case "database failed":
				a.Finalize = func(context.Context, []string, func(map[string]httpx.Consent) error) error {
					return errors.New("DATABASE_SENTINEL")
				}
			case "unreviewed map key":
				source.Users.ByRole = map[string]int64{"PERSON_SENTINEL": 1}
			case "histogram too long":
				source.Activity = &Activity{Days: make([]DayCount, 101)}
			}
			app := supportApp(a, func(r fiber.Router) {
				ingress.Bind(r, "/dashboard/summary", dashboardPolicy("/v1/dashboard/summary", "dashboard.summary"), func(c fiber.Ctx) error {
					if scenario == "unclassified DTO" {
						return httpx.JSON(c, 200, struct {
							Name string `json:"name"`
						}{"PERSON_SENTINEL"})
					}
					if scenario == "direct writer" {
						c.Set("Location", "PERSON_SENTINEL")
						return c.SendString("PERSON_SENTINEL")
					}
					return httpx.JSON(c, 200, source)
				})
			})
			status, body, _ := supportRequest(t, app, "GET", "/v1/dashboard/summary")
			if status == 200 || strings.Contains(body, "SENTINEL") {
				t.Fatal(status, body)
			}
			if scenario == "version changed" && (status != 401 || !strings.Contains(body, `"reason":"claims_changed"`)) {
				t.Fatal(status, body)
			}
		})
	}
}

func TestDashboardBindingsRefuseQueriesAndHEADBeforeServices(t *testing.T) {
	a := supportAudience(false)
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Use(httpx.RequestID(), httpx.AdmissionGuard(nil))
	identity := func(c fiber.Ctx) error {
		p := &authz.Principal{TenantRole: authz.SupportReadOnly, TenantID: uuidPtr(uuid.New())}
		authz.SetPrincipal(c, p)
		httpx.SetProjectionAudience(c, a)
		return c.Next()
	}
	ingress.Mount(app, ingress.Internal, Groups(&Service{now: time.Now}, identity), nil)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/v1/dashboard/summary?from=2026-10-01&%66rom=2026-10-02", 403},
		{"GET", "/v1/dashboard/summary?tenantId=FOREIGN_SENTINEL", 403},
		{"GET", "/v1/dashboard/summary?from=2026-01-01&to=2026-10-01", 422},
		{"GET", "/v1/badges?q=PERSON_SENTINEL", 403},
		{"HEAD", "/v1/badges", 403},
		{"HEAD", "/v1/dashboard/summary", 403},
	} {
		r, err := app.Test(httptest.NewRequest(tc.method, tc.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if r.StatusCode != tc.status || strings.Contains(string(b), "SENTINEL") {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, r.StatusCode, b)
		}
	}
}
func uuidPtr(v uuid.UUID) *uuid.UUID { return &v }

func TestDashboardBadgesAreCountOnlyInBothConsentStates(t *testing.T) {
	for _, on := range []bool{false, true} {
		app := supportApp(supportAudience(on), func(r fiber.Router) {
			ingress.Bind(r, "/badges", dashboardPolicy("/v1/badges", "dashboard.badges"), func(c fiber.Ctx) error {
				return httpx.JSON(c, 200, Badges{Waitlist: int64Ptr(4), PendingExpenses: int64Ptr(2), UnassignedChats: int64Ptr(3), UrgentChats: int64Ptr(1)})
			})
		})
		status, body, meta := supportRequest(t, app, "GET", "/v1/badges")
		if status != 200 || len(meta.Redacted) != 0 || !strings.Contains(body, `"pendingExpenses":2`) || !strings.Contains(body, `"waitlist":4`) {
			t.Fatal(status, body)
		}
	}
}
