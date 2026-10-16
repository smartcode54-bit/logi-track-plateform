package forms

import (
	"context"
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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

type waitlistRow []any

func (r waitlistRow) Scan(dst ...any) error {
	if len(dst) != len(r) {
		return fmt.Errorf("scan %d/%d", len(dst), len(r))
	}
	for i, v := range r {
		reflect.ValueOf(dst[i]).Elem().Set(reflect.ValueOf(v))
	}
	return nil
}

type waitlistRows struct {
	pgx.Rows
	rows  []waitlistRow
	index int
}

func (r *waitlistRows) Next() bool            { r.index++; return r.index <= len(r.rows) }
func (r *waitlistRows) Scan(dst ...any) error { return r.rows[r.index-1].Scan(dst...) }
func (*waitlistRows) Close()                  {}
func (*waitlistRows) Err() error              { return nil }

type waitlistTx struct {
	pgx.Tx
	p        *authz.Principal
	rawReads int
}

func (*waitlistTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (*waitlistTx) Commit(context.Context) error   { return nil }
func (*waitlistTx) Rollback(context.Context) error { return nil }
func (tx *waitlistTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "CountWaitlist"):
		return waitlistRow{int64(1)}
	case strings.Contains(sql, "SELECT auth_version,status"):
		return waitlistRow{int32(1), "active"}
	case strings.Contains(sql, "SELECT access_mode,context_version"):
		return waitlistRow{tx.p.AccessMode, int64(0), tx.p.TenantID, (*time.Time)(nil), time.Now().Add(time.Hour)}
	case strings.Contains(sql, "SELECT support_access_enabled,support_access_version"):
		return waitlistRow{false, int64(0)}
	}
	panic("unexpected query: " + sql)
}
func (tx *waitlistTx) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "user_platform_roles") {
		out := &waitlistRows{}
		for _, role := range tx.p.Platform {
			out.rows = append(out.rows, waitlistRow{string(role)})
		}
		return out, nil
	}
	if strings.Contains(sql, "-- name: ListWaitlist") {
		tx.rawReads++
		return &waitlistRows{rows: []waitlistRow{{uuid.New(), "SENTINEL-lead@example.test", "SENTINEL-name", "+66", (*string)(nil), time.Now()}}}, nil
	}
	return nil, fmt.Errorf("unexpected query: %s", sql)
}

type waitlistPool struct{ tx *waitlistTx }

func (p waitlistPool) Begin(context.Context) (pgx.Tx, error) { return p.tx, nil }

func TestWaitlistCountsOnlyInSupportSessions(t *testing.T) {
	for _, mode := range []string{"member", "platform", "support"} {
		t.Run(mode, func(t *testing.T) {
			tenant := uuid.New()
			p := &authz.Principal{UserID: uuid.New(), AuthVersion: 1, TenantID: &tenant, TenantRole: authz.TenantAdmin, AccessMode: "member", Steward: true, Caps: authz.NewCapSet(authz.WaitlistView)}
			if mode != "member" {
				p.Platform = []authz.PlatformRole{authz.PlatformAdmin}
			}
			if mode == "support" {
				p.AccessMode = "support"
				p.TenantRole = authz.SupportReadOnly
				p.SessionID = uuid.New()
			}
			tx := &waitlistTx{p: p}
			s := &Service{pool: waitlistPool{tx}}
			app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
			app.Use(httpx.AdmissionGuard(func(c fiber.Ctx) {
				audience := httpx.ProjectionAudienceFrom(c)
				audience.Platform, audience.Support = p.IsPlatform(), authz.IsSupport(p)
				audience.Tenant, audience.ContextVersion, audience.ConsentVersion = tenant.String(), "0", "0"
				httpx.SetProjectionAudience(c, audience)
			}))
			ingress.Mount(app, ingress.Internal, Groups(s, Options{Auth: func(c fiber.Ctx) error { authz.SetPrincipal(c, p); return c.Next() }}), nil)
			res, err := app.Test(httptest.NewRequest("GET", WaitlistPrefix, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			raw, _ := io.ReadAll(res.Body)
			if res.StatusCode != 200 {
				t.Fatalf("waitlist: %d %s", res.StatusCode, raw)
			}
			if mode == "support" {
				if tx.rawReads != 0 || strings.Contains(string(raw), "SENTINEL") || !strings.Contains(string(raw), `"total":1`) {
					t.Fatalf("support read lead rows: %s", raw)
				}
			} else if tx.rawReads != 1 || !strings.Contains(string(raw), "SENTINEL-lead@example.test") {
				t.Fatalf("ordinary lead contract changed: %s", raw)
			}
		})
	}
}
