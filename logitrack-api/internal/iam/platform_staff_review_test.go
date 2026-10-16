package iam

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/supportprojection"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestPlatformUserOwnerSurvivesAccountRedaction(t *testing.T) {
	for _, kind := range []string{"", "customer", "dispatcher"} {
		t.Run("scope="+kind, func(t *testing.T) {
			id := uuid.New()
			email, name := "foreign-platform@example.test", "Foreign platform account"
			tx := &gateTx{query: func(sql string, _ ...any) (pgx.Rows, error) {
				switch {
				case strings.Contains(sql, "-- name: ListUserMemberships"), strings.Contains(sql, "-- name: ListUserDrivers"):
					return &gateRows{}, nil
				case strings.Contains(sql, "-- name: ListUserScopes"):
					if kind == "" {
						return &gateRows{}, nil
					}
					return &gateRows{values: [][]any{{id, kind, uuid.New(), "Scoped customer"}}}, nil
				case strings.Contains(sql, "-- name: ListUserPlatformRoles"):
					return &gateRows{values: [][]any{{id, "support"}}}, nil
				case strings.Contains(sql, "-- name: TargetPrivilegesMany"):
					return &gateRows{values: [][]any{{id, true, []uuid.UUID{}, []uuid.UUID{}, kind == "dispatcher", kind == "customer"}}}, nil
				default:
					t.Fatalf("unexpected query: %s", sql)
					return nil, nil
				}
			}}
			a, q := &Admin{}, iamdb.New(tx)
			users, err := a.enrich(t.Context(), q, reach{all: true}, []userRow{{ID: id, Email: &email, DisplayName: &name, Status: "active"}})
			if err != nil {
				t.Fatal(err)
			}
			p := &authz.Principal{UserID: uuid.New(), Platform: []authz.PlatformRole{authz.Support}}
			if err := a.markAccountActions(t.Context(), q, p, users); err != nil {
				t.Fatal(err)
			}
			if users[0].AccountReason != ReasonPlatformTarget || len(users[0].Scopes) != 0 {
				t.Fatalf("account redaction not exercised: %+v", users[0])
			}
			for _, detail := range []bool{false, true} {
				app := fiber.New()
				app.Use(httpx.AdmissionGuard(nil))
				app.Get("/users", func(c fiber.Ctx) error {
					httpx.SetProjectionAudience(c, httpx.ProjectionAudience{Platform: true, Actor: p.UserID.String(), Finalize: func(_ context.Context, _ []string, fn func(map[string]httpx.Consent) error) error {
						return fn(map[string]httpx.Consent{supportprojection.PlatformOwner: {Enabled: true}})
					}})
					route := "/v1/platform/users"
					if detail {
						route += "/:id"
					}
					if err := httpx.EndpointGate(c, iamPolicy("GET", route, "iam.platform-user")); err != nil {
						return err
					}
					if detail {
						return httpx.JSON(c, 200, &users[0])
					}
					return httpx.JSON(c, 200, users)
				})
				res, err := app.Test(httptest.NewRequest("GET", "/users", nil))
				if err != nil {
					t.Fatal(err)
				}
				raw, err := io.ReadAll(res.Body)
				_ = res.Body.Close()
				if err != nil || res.StatusCode != 200 {
					t.Fatalf("detail=%t status=%d body=%s err=%v", detail, res.StatusCode, raw, err)
				}
				var envelope struct{ Data json.RawMessage }
				if err := json.Unmarshal(raw, &envelope); err != nil {
					t.Fatal(err)
				}
				var row map[string]any
				if detail {
					err = json.Unmarshal(envelope.Data, &row)
				} else {
					var rows []map[string]any
					err = json.Unmarshal(envelope.Data, &rows)
					if err == nil && len(rows) == 1 {
						row = rows[0]
					}
				}
				wantEmail, wantName := email, name
				if kind != "" {
					wantEmail, wantName = "****", "****"
				}
				if err != nil || row["email"] != wantEmail || row["displayName"] != wantName {
					t.Errorf("detail=%t projection=%s err=%v", detail, raw, err)
				}
			}
		})
	}
}

func TestPlatformOwnerUsesLiveProjectionFinalizer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		roles   []authz.PlatformRole
		support bool
		stale   bool
		want    int
	}{
		{name: "platform admin", roles: []authz.PlatformRole{authz.PlatformAdmin}, want: 204},
		{name: "platform support role", roles: []authz.PlatformRole{authz.Support}, want: 204},
		{name: "support session", roles: []authz.PlatformRole{authz.PlatformAdmin}, support: true, want: 204},
		{name: "nonplatform support session", support: true, want: 503},
		{name: "stale platform principal", roles: []authz.PlatformRole{authz.PlatformAdmin}, stale: true, want: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			p := &authz.Principal{UserID: uuid.New(), Platform: tc.roles, AuthVersion: 1}
			if tc.support {
				p.TenantRole, p.TenantID, p.SessionID = authz.SupportReadOnly, &tenant, uuid.New()
			}
			tenantReads := 0
			tx := &gateTx{row: func(sql string, args ...any) pgx.Row {
				switch {
				case strings.Contains(sql, "SELECT auth_version,status"):
					version := int32(1)
					if tc.stale {
						version++
					}
					return gateRow{[]any{version, "active"}}
				case strings.Contains(sql, "FROM sessions"):
					return gateRow{[]any{"support", int64(1), &tenant, (*time.Time)(nil), time.Now().Add(time.Hour)}}
				case strings.Contains(sql, "FROM tenants"):
					if args[0] != tenant {
						t.Fatalf("queried tenant for a platform owner: %v", args)
					}
					tenantReads++
					return gateRow{[]any{false, int64(7)}}
				default:
					t.Fatalf("unexpected query: %s", sql)
					return nil
				}
			}, query: func(sql string, _ ...any) (pgx.Rows, error) {
				if !strings.Contains(sql, "FROM user_platform_roles") {
					t.Fatalf("unexpected query: %s", sql)
				}
				rows := &gateRows{}
				for _, role := range tc.roles {
					rows.values = append(rows.values, []any{string(role)})
				}
				return rows, nil
			}}
			app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error {
				return httpx.WriteError(c, err.(*httpx.Error))
			}})
			app.Use(func(c fiber.Ctx) error {
				authz.SetPrincipal(c, p)
				httpx.SetProjectionAudience(c, httpx.ProjectionAudience{ContextVersion: "1"})
				return c.Next()
			}, PlatformProjection(gatePool{tx}))
			called := false
			app.Get("/finalize", func(c fiber.Ctx) error {
				err := httpx.ProjectionAudienceFrom(c).Finalize(c.Context(), []string{supportprojection.PlatformOwner, tenant.String()}, func(consents map[string]httpx.Consent) error {
					called = true
					if !consents[supportprojection.PlatformOwner].Enabled || consents[tenant.String()] != (httpx.Consent{Enabled: false, Version: "7"}) {
						t.Errorf("consents=%v", consents)
					}
					return nil
				})
				if err != nil {
					return err
				}
				return c.SendStatus(204)
			})
			res, err := app.Test(httptest.NewRequest("GET", "/finalize", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != tc.want || called != (tc.want == 204) || tx.committed != (tc.want == 204) {
				t.Fatalf("status=%d callback=%t committed=%t", res.StatusCode, called, tx.committed)
			}
			if tc.want == 204 && tenantReads != 1 {
				t.Fatalf("tenant reads=%d want 1", tenantReads)
			}
		})
	}
}
