package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

type supportTestRow struct {
	values []any
	err    error
}

func (r supportTestRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan: %d destinations for %d values", len(dest), len(r.values))
	}
	for i, value := range r.values {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(value))
	}
	return nil
}

type supportTestRows struct {
	pgx.Rows
	values [][]any
	index  int
}

func (r *supportTestRows) Next() bool { r.index++; return r.index <= len(r.values) }
func (r *supportTestRows) Scan(dest ...any) error {
	return (supportTestRow{values: r.values[r.index-1]}).Scan(dest...)
}
func (r *supportTestRows) Close()     {}
func (r *supportTestRows) Err() error { return nil }

type supportTestTx struct {
	pgx.Tx
	row        func(string, ...any) pgx.Row
	query      func(string, ...any) (pgx.Rows, error)
	statements []string
}

func (t *supportTestTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	t.statements = append(t.statements, sql)
	return t.row(sql, args...)
}
func (t *supportTestTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	t.statements = append(t.statements, sql)
	return t.query(sql, args...)
}
func (t *supportTestTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	t.statements = append(t.statements, sql)
	return pgconn.NewCommandTag("UPDATE 1"), nil
}
func (t *supportTestTx) Commit(context.Context) error   { return nil }
func (t *supportTestTx) Rollback(context.Context) error { return nil }

type supportTestPool struct{ tx *supportTestTx }

func (p supportTestPool) Begin(context.Context) (pgx.Tx, error) { return p.tx, nil }
func supportPtr[T any](v T) *T                                  { return &v }

func TestSupportLabelCodeSafetyIndependentOfLegalType(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		personal bool
	}{{"own_fleet", false}, {"carrier", false}, {"carrier", true}} {
		personal := tc.personal
		for _, enabled := range []bool{false, true} {
			tx := &supportTestTx{row: func(string, ...any) pgx.Row {
				return supportTestRow{values: []any{"Tenant Name", supportPtr("Tenant EN"), tc.kind, "active", supportPtr("PERSON-NAME"), personal, enabled, int64(1)}}
			}}
			label, err := supportTenantLabel(context.Background(), tx, &authz.Principal{}, uuid.New(), "manager")
			if err != nil {
				t.Fatal(err)
			}
			if enabled && (label.Code == nil || *label.Code != "PERSON-NAME") {
				t.Fatal("consenting code hidden")
			}
			if !enabled && label.Code != nil {
				t.Fatalf("unreviewed code disclosed OFF (personal=%t)", personal)
			}
			if (personal && !enabled) != (label.NameTh == "****") {
				t.Fatal("name classification changed")
			}
		}
	}
}

func TestPasswordCompletionRejectsUnchangedCredentialUnderLock(t *testing.T) {
	ctx := context.Background()
	hasher, err := password.NewHasher(password.Params{MemoryKB: 8 * 1024, Iterations: 1, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := hasher.Hash(ctx, "platform temporary passphrase")
	if err != nil {
		t.Fatal(err)
	}
	uid := uuid.New()
	tx := &supportTestTx{row: func(sql string, _ ...any) pgx.Row {
		if !strings.Contains(sql, "FOR NO KEY UPDATE") {
			t.Fatalf("credential not read under user lock: %s", sql)
		}
		return supportTestRow{values: []any{uid, supportPtr("holder@example.test"), &hash, []byte(nil), []byte(nil), "active", true, int32(3)}}
	}}
	svc := &Service{pool: supportTestPool{tx}, hasher: hasher, now: time.Now}
	err = svc.changePassword(ctx, uid, ChangeInput{NewPassword: "platform temporary passphrase"}, "unused new hash", nil, nil, &pwchgTicket{AuthVersion: 3})
	if err == nil || !strings.Contains(err.Error(), "invalid_argument") {
		t.Fatalf("same credential accepted: %v", err)
	}
	for _, sql := range tx.statements {
		if strings.Contains(sql, "SetPassword") || strings.Contains(sql, "credential_self_chosen_at") {
			t.Fatal("rejected completion wrote credential provenance")
		}
	}
}

func supportReadFixture(t *testing.T) (*Service, *supportTestTx, *authz.Principal) {
	t.Helper()
	p := &authz.Principal{UserID: uuid.New(), SessionID: uuid.New(), TenantID: supportPtr(uuid.New()), TenantRole: authz.SupportReadOnly, AccessMode: AccessModeSupport, ContextVersion: 1, SupportAccessVersion: 1, AuthVersion: 1, Platform: []authz.PlatformRole{authz.PlatformAdmin}, AMR: AMRPassword}
	now := time.Now()
	tx := &supportTestTx{}
	tx.row = func(sql string, args ...any) pgx.Row {
		switch {
		case sql == supportReadSessionContextSQL || sql == supportLockSessionContextSQL:
			return supportTestRow{values: []any{p.AccessMode, p.ContextVersion, p.TenantID, (*uuid.UUID)(nil), PlatformWeb, AMRPassword, now.Add(time.Hour), (*time.Time)(nil)}}
		case strings.Contains(sql, "-- name: GetUser :one"):
			return supportTestRow{values: []any{p.UserID, (*string)(nil), false, (*string)(nil), (*uuid.UUID)(nil), (*string)(nil), []byte(nil), []byte(nil), "active", false, p.AuthVersion, (*time.Time)(nil)}}
		case sql == supportReadUserStateSQL:
			return supportTestRow{values: []any{"active", p.AuthVersion}}
		case sql == supportReadTenantConsentSQL:
			return supportTestRow{values: []any{"carrier", p.SupportAccessEnabled, p.SupportAccessVersion}}
		case sql == supportReadOpenVisitIDSQL:
			return supportTestRow{values: []any{uuid.New()}}
		case sql == supportLockTenantConsentSQL:
			return supportTestRow{values: []any{p.SupportAccessEnabled, p.SupportAccessVersion}}
		case strings.Contains(sql, "-- name: SessionAuthState"):
			return supportTestRow{values: []any{p.AuthVersion, (*time.Time)(nil)}}
		case strings.Contains(sql, "-- name: GetDriverForUser"):
			return supportTestRow{err: pgx.ErrNoRows}
		default:
			t.Fatalf("unexpected read: %s", sql)
			return supportTestRow{err: pgx.ErrNoRows}
		}
	}
	tx.query = func(sql string, _ ...any) (pgx.Rows, error) {
		rows := &supportTestRows{}
		if strings.Contains(sql, "-- name: ListMemberships") && p.AccessMode == AccessModeMember && p.TenantID != nil {
			rows.values = append(rows.values, []any{*p.TenantID, string(p.TenantRole), "carrier", "Member tenant", (*string)(nil), "active", now})
		}
		if strings.Contains(sql, "-- name: ListPlatformRoles") {
			for _, role := range p.Platform {
				rows.values = append(rows.values, []any{string(role)})
			}
		}
		return rows, nil
	}
	return &Service{pool: supportTestPool{tx}, now: func() time.Time { return now }}, tx, p
}

func TestSupportAdmissionDoesNotLockVisit(t *testing.T) {
	svc, tx, p := supportReadFixture(t)
	if err := svc.checkContext(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.SupportVisitID == uuid.Nil {
		t.Fatal("admission did not resolve the open visit")
	}
	for _, sql := range tx.statements {
		if strings.Contains(sql, "FOR UPDATE") || strings.Contains(sql, "FOR NO KEY UPDATE") {
			t.Fatalf("admission acquired an exclusive lock: %s", sql)
		}
	}
}

func TestSupportFinalizationLocksOnlyOrderedTenantConsents(t *testing.T) {
	svc, tx, p := supportReadFixture(t)
	owners := []string{p.TenantID.String(), uuid.NewString()}
	sort.Sort(sort.Reverse(sort.StringSlice(owners)))
	var locked []string
	readRow := tx.row
	tx.row = func(sql string, args ...any) pgx.Row {
		if sql == supportLockTenantConsentSQL {
			locked = append(locked, args[0].(uuid.UUID).String())
		}
		return readRow(sql, args...)
	}
	called := false
	err := svc.finalizeSupportAudience(p)(context.Background(), owners, func(consents map[string]httpx.Consent) error {
		called = true
		if len(consents) != 2 {
			t.Fatalf("consents: %v", consents)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("finalization: %v (called=%t)", err, called)
	}
	for _, sql := range tx.statements {
		if strings.Contains(sql, "FOR UPDATE") || strings.Contains(sql, "FOR NO KEY UPDATE") {
			t.Fatalf("response acquired an exclusive lock: %s", sql)
		}
	}
	if len(locked) != 2 || !sort.StringsAreSorted(locked) {
		t.Fatal("tenant locks were not ordered")
	}
	// A changed selected consent still fails before serialization.
	p.SupportAccessVersion++
	originalRow := tx.row
	tx.row = func(sql string, args ...any) pgx.Row {
		if sql == supportLockTenantConsentSQL {
			return supportTestRow{values: []any{false, int64(1)}}
		}
		return originalRow(sql, args...)
	}
	err = svc.finalizeSupportAudience(p)(context.Background(), owners, func(map[string]httpx.Consent) error { t.Fatal("stale consent serialized"); return nil })
	if err == nil {
		t.Fatal("stale consent accepted")
	}
}

func TestHumanHeaderRefusalThroughRequireAuth(t *testing.T) {
	for _, mode := range []string{"support-off", "support-on", "platform-admin", "platform-support", "member"} {
		t.Run(mode, func(t *testing.T) {
			svc, _, p := supportReadFixture(t)
			p.SupportAccessEnabled = mode == "support-on"
			if !strings.HasPrefix(mode, "support-") {
				p.AccessMode = AccessModeMember
				p.TenantID = nil
				p.TenantRole = ""
				p.SupportAccessVersion = 0
			}
			if mode == "platform-support" {
				p.Platform = []authz.PlatformRole{authz.Support}
			}
			if mode == "member" {
				p.Platform = nil
				p.TenantID = supportPtr(uuid.New())
				p.TenantRole = authz.Manager
			}
			_, priv, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			svc.keys = token.New(priv, "test", "test", 15*time.Minute)
			c := token.Claims{SessionID: p.SessionID.String(), Version: 1, ContextVersion: "1", AMR: AMRPassword, Platform: []string{}}
			for _, role := range p.Platform {
				c.Platform = append(c.Platform, string(role))
			}
			c.Subject = p.UserID.String()
			c.ID = uuid.NewString()
			if p.TenantID != nil {
				c.TenantID = p.TenantID.String()
				c.Role = string(p.TenantRole)
			}
			if authz.IsSupport(p) {
				c.AccessMode = AccessModeSupport
				c.Role = string(authz.SupportReadOnly)
				c.TenantID = p.TenantID.String()
				c.SupportAccessVersion = "1"
			}
			raw, err := svc.keys.Sign(c, svc.clock())
			if err != nil {
				t.Fatal(err)
			}
			dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialerRetries: 1, DialTimeout: time.Millisecond * 50})
			t.Cleanup(func() { _ = dead.Close() })
			svc.store = NewStore(dead, "test:")
			svc.fallback = prometheus.NewCounter(prometheus.CounterOpts{Name: "test_fallback_total"})
			app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
			app.Use(svc.RequireAuth())
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				app.Add([]string{method}, "/tenant", func(c fiber.Ctx) error { t.Error("header reached business handler"); return c.SendStatus(204) })
				for _, header := range []string{uuid.NewString(), "*"} {
					req := httptest.NewRequest(method, "/tenant", nil)
					req.Header.Set("Authorization", "Bearer "+raw)
					req.Header.Set(authz.HeaderActOnTenant, header)
					res, err := app.Test(req)
					if err != nil {
						t.Fatal(err)
					}
					body, _ := io.ReadAll(res.Body)
					_ = res.Body.Close()
					if res.StatusCode != 400 || !strings.Contains(string(body), "header_not_allowed") {
						t.Fatalf("%s %s: %d %s", method, header, res.StatusCode, body)
					}
				}
			}
		})
	}
}
