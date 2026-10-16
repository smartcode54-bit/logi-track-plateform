package iam

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// auditValue keeps a crafted X-Act-On-Tenant value from bloating the audit row or failing its insert
// (jsonb refuses NUL).
func TestAuditValue(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"*", "*"},
		{"0199c000-0000-7000-8000-0000000000ff", "0199c000-0000-7000-8000-0000000000ff"},
		{"a\x00b\tcé", "a?b?c??"},
		{strings.Repeat("y", 500), strings.Repeat("y", auditValueMax)},
		{"", ""},
	} {
		if got := auditValue(tc.in); got != tc.want {
			t.Errorf("auditValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Ordinary callers are refused without a DB write; platform attempts retain mandatory auditing.
func TestRetiredHeaderAuditsOnlyPlatformPrincipals(t *testing.T) {
	for _, role := range []authz.PlatformRole{"", authz.PlatformAdmin, authz.Support} {
		pool := &auditRefusalPool{}
		r := &RBAC{pool: pool, now: time.Now}
		p := &authz.Principal{UserID: uuid.New()}
		if role != "" {
			p.Platform = []authz.PlatformRole{role}
		}
		app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
		app.Get("/tenant", func(c fiber.Ctx) error { return r.Authorize(c, p) })
		req := httptest.NewRequest("GET", "/tenant", nil)
		req.Header.Set(authz.HeaderActOnTenant, "*")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		want, wantCalls := 400, 0
		if role != "" {
			want, wantCalls = 503, 1
		}
		if res.StatusCode != want || pool.calls != wantCalls {
			t.Fatalf("role %q: status=%d DB calls=%d", role, res.StatusCode, pool.calls)
		}
	}
}

type auditRefusalPool struct{ calls int }

func (p *auditRefusalPool) Begin(context.Context) (pgx.Tx, error) {
	p.calls++
	return nil, errors.New("audit DB unavailable")
}
