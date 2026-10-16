package iam

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/apikey"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

type gateRow struct{ values []any }

func (r gateRow) Scan(dst ...any) error {
	if len(dst) != len(r.values) {
		return fmt.Errorf("scan %d fields into %d destinations", len(r.values), len(dst))
	}
	for i, value := range r.values {
		reflect.ValueOf(dst[i]).Elem().Set(reflect.ValueOf(value))
	}
	return nil
}

type gateRows struct {
	pgx.Rows
	values [][]any
	index  int
}

func (r *gateRows) Next() bool            { r.index++; return r.index <= len(r.values) }
func (r *gateRows) Scan(dst ...any) error { return (gateRow{r.values[r.index-1]}).Scan(dst...) }
func (*gateRows) Close()                  {}
func (*gateRows) Err() error              { return nil }

type gateTx struct {
	pgx.Tx
	row                   func(string, ...any) pgx.Row
	query                 func(string, ...any) (pgx.Rows, error)
	writes                []string
	committed, rolledBack bool
}

func (tx *gateTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	return tx.row(sql, args...)
}
func (tx *gateTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	return tx.query(sql, args...)
}
func (tx *gateTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if !strings.Contains(sql, "set_config") {
		tx.writes = append(tx.writes, sql)
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (tx *gateTx) Commit(context.Context) error   { tx.committed = true; return nil }
func (tx *gateTx) Rollback(context.Context) error { tx.rolledBack = true; return nil }

type gatePool struct{ tx *gateTx }

func (p gatePool) Begin(context.Context) (pgx.Tx, error) { return p.tx, nil }

func TestGate1TenantAPIKeyConsentProbe(t *testing.T) {
	for _, role := range []authz.PlatformRole{authz.PlatformAdmin, authz.Support} {
		t.Run(string(role), func(t *testing.T) {
			tenant, actor, admin := uuid.New(), uuid.New(), uuid.New()
			tx := &gateTx{}
			tx.row = func(sql string, _ ...any) pgx.Row {
				if strings.Contains(sql, "-- name: KeyTenant") {
					return gateRow{[]any{"carrier", "active"}}
				}
				if !strings.Contains(sql, "LockSupportAccess") || !strings.Contains(sql, "FOR UPDATE") {
					t.Fatalf("mutation preceded D2 lock: %s", sql)
				}
				return gateRow{[]any{tenant, false, int64(0), (*time.Time)(nil), (*uuid.UUID)(nil)}}
			}
			tx.query = func(sql string, _ ...any) (pgx.Rows, error) {
				if !strings.Contains(sql, "ActiveSupportTenantAdmins") || strings.Contains(sql, "users.status") {
					t.Fatalf("admin count changed: %s", sql)
				}
				return &gateRows{values: [][]any{{admin}}}, nil
			}
			pepper, err := apikey.NewPepper(strings.Repeat("probe-pepper-", 4))
			if err != nil {
				t.Fatal(err)
			}
			keys := &APIKeys{pool: gatePool{tx}, pepper: pepper, now: time.Now}
			p := &authz.Principal{UserID: actor, TenantID: &tenant, Platform: []authz.PlatformRole{role}, Caps: authz.NewCapSet(authz.DriversViewPII, authz.OperationsManageTasks)}
			id := tenant.String()
			out, err := keys.Create(call{ctx: t.Context(), p: p}, CreateAPIKeyInput{Name: "probe", Scope: "script", TenantID: &id, Capabilities: []string{"drivers:view_pii", "operations:manage_tasks"}})
			var he *httpx.Error
			if !errors.As(err, &he) || he.Code != "support_access_required" || out != nil || len(tx.writes) != 0 || tx.committed || !tx.rolledBack {
				t.Fatalf("probe: out=%v err=%v writes=%v committed=%t rollback=%t", out, err, tx.writes, tx.committed, tx.rolledBack)
			}
		})
	}
}

func TestGate1MembershiplessCredentialTargetsAreNotD2Tenants(t *testing.T) {
	for _, action := range []string{"invite", "temporary_password"} {
		tx := &gateTx{query: func(sql string, _ ...any) (pgx.Rows, error) {
			if !strings.Contains(sql, "FROM memberships") {
				t.Fatalf("unexpected query: %s", sql)
			}
			return &gateRows{}, nil
		}}
		a := &Admin{now: time.Now}
		p := &authz.Principal{UserID: uuid.New(), Platform: []authz.PlatformRole{authz.PlatformAdmin}}
		if err := a.supportIAM(call{ctx: t.Context(), p: p}, tx, uuid.New(), nil, action, true); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if len(tx.writes) != 0 {
			t.Fatal("non-D2 target produced tenant side effects")
		}
	}
}

func TestGate1QuarantineMaintenanceIsNarrow(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    *authz.Principal
		want bool
	}{
		{"platform tenant administrator", &authz.Principal{Platform: []authz.PlatformRole{authz.PlatformAdmin}}, true},
		{"retired write capability", &authz.Principal{Caps: authz.NewCapSet(authz.PlatformCrossTenantWrite)}, false},
		{"support role", &authz.Principal{Platform: []authz.PlatformRole{authz.Support}}, false},
		{"member", &authz.Principal{TenantRole: authz.TenantAdmin}, false},
		{"machine", &authz.Principal{AMR: authz.AMRAPIKey, Platform: []authz.PlatformRole{authz.PlatformAdmin}}, false},
		{"support session", &authz.Principal{TenantRole: authz.SupportReadOnly, Platform: []authz.PlatformRole{authz.PlatformAdmin}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := quarantineMaintenance(tc.p)
			if (err == nil) != tc.want {
				t.Fatalf("maintenance: %v", err)
			}
			if !tc.want {
				if _, err := (&Admin{}).RehomeQuarantineRow(call{p: tc.p}, "drivers", uuid.New(), RehomeInput{}); err == nil {
					t.Fatal("service omitted maintenance guard")
				}
			}
		})
	}
}

func TestGate1NativeMemberReadBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, route     string
		granted, native bool
	}{
		{"native member legacy read", "/v1/users", false, true},
		{"unratified grant legacy read", "/v1/users", true, false},
		{"native member explicit platform read", "/v1/platform/users", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenant, uid := uuid.New(), uuid.New()
			p := &authz.Principal{UserID: uid, AuthVersion: 1, TenantID: &tenant, TenantRole: authz.TenantAdmin, Platform: []authz.PlatformRole{authz.PlatformAdmin}}
			tx := &gateTx{}
			tx.row = func(sql string, _ ...any) pgx.Row {
				switch {
				case strings.Contains(sql, "-- name: GetMembership"):
					return gateRow{[]any{"tenant_admin", "active"}}
				case strings.Contains(sql, "-- name: TenantKind"):
					return gateRow{[]any{"carrier"}}
				case strings.Contains(sql, "SELECT auth_version,status"):
					return gateRow{[]any{int32(1), "active"}}
				case strings.Contains(sql, "SELECT support_access_enabled,support_access_version"):
					return gateRow{[]any{false, int64(0)}}
				}
				t.Fatalf("unexpected read: %s", sql)
				return gateRow{}
			}
			tx.query = func(sql string, _ ...any) (pgx.Rows, error) {
				switch {
				case strings.Contains(sql, "FROM memberships m JOIN users u"):
					return &gateRows{values: [][]any{{uid, uuid.New(), "tenant_admin", "active", time.Now(), tc.granted, (*uuid.UUID)(nil), (*time.Time)(nil), (*uuid.UUID)(nil), (*time.Time)(nil), int64(0), "active", false, (*time.Time)(nil), (*time.Time)(nil), true, true, false}}}, nil
				case strings.Contains(sql, "-- name: RoleOverrides"):
					return &gateRows{}, nil
				case strings.Contains(sql, "SELECT role FROM user_platform_roles"):
					return &gateRows{values: [][]any{{"platform_admin"}}}, nil
				}
				return nil, fmt.Errorf("unexpected read: %s", sql)
			}
			h := adminHTTP{&Admin{pool: gatePool{tx}}}
			app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
			app.Use(httpx.AdmissionGuard(func(c fiber.Ctx) {
				if p := authz.PrincipalFrom(c); p != nil {
					a := httpx.ProjectionAudienceFrom(c)
					a.Platform = p.IsPlatform()
					a.Actor = p.UserID.String()
					httpx.SetProjectionAudience(c, a)
				}
			}))
			name := "SENTINEL-own-staff@example.test"
			ingress.Mount(app, ingress.Internal, []ingress.Group{{Prefix: tc.route, Mount: func(r fiber.Router) {
				ingress.Bind(r, "", iamPolicy("GET", tc.route, "iam.platform-user"), func(c fiber.Ctx) error { authz.SetPrincipal(c, p); return c.Next() }, h.projection, func(c fiber.Ctx) error {
					current := authz.PrincipalFrom(c)
					if current.IsPlatform() == tc.native {
						t.Error("read used the wrong authority plane")
					}
					return httpx.JSON(c, 200, User{ID: uuid.New(), Email: &name, Memberships: []Membership{{TenantID: tenant}}, PlatformRoles: []string{}})
				})
			}}}, nil)
			res, err := app.Test(httptest.NewRequest("GET", tc.route, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			raw, _ := io.ReadAll(res.Body)
			if res.StatusCode != 200 || strings.Contains(string(raw), name) != tc.native {
				t.Fatalf("member read: %d %s", res.StatusCode, raw)
			}
		})
	}
}

func TestGate1PlatformRatificationCannotUseNativeMemberContext(t *testing.T) {
	tenant := uuid.New()
	for _, role := range []authz.PlatformRole{authz.PlatformAdmin, authz.Support} {
		p := &authz.Principal{UserID: uuid.New(), TenantID: &tenant, TenantRole: authz.TenantAdmin, Platform: []authz.PlatformRole{role}}
		_, err := (&Admin{}).Ratify(call{p: p}, tenant, uuid.New(), RatifyInput{ExpectedGrantVersion: "0"})
		var he *httpx.Error
		if !errors.As(err, &he) || he.Code != CodeRatificationNotAllowed {
			t.Fatalf("platform ratification: %v", err)
		}
	}
}
