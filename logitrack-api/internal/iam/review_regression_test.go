package iam

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestReviewEventCannotUseEventTenantForUnresolvedActor(t *testing.T) {
	tenant := uuid.New()
	email := "foreign-actor@example.test"
	app := fiber.New()
	app.Get("/events", func(c fiber.Ctx) error {
		httpx.SetProjectionAudience(c, httpx.ProjectionAudience{Platform: true, Finalize: func(_ context.Context, _ []string, fn func(map[string]httpx.Consent) error) error {
			return fn(map[string]httpx.Consent{tenant.String(): {Enabled: true, Version: "1"}})
		}})
		if err := httpx.EndpointGate(c, iamPolicy("GET", "/v1/platform/security-events", "iam.platform-event")); err != nil {
			return err
		}
		return httpx.JSON(c, 200, []PlatformEvent{{ID: uuid.New(), TenantID: &tenant, ActorEmail: &email}})
	})
	res, err := app.Test(httptest.NewRequest("GET", "/events", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || strings.Contains(string(raw), email) {
		t.Fatalf("unresolved actor leaked: %d %s", res.StatusCode, raw)
	}
}

func TestReviewCallResolvesAudienceBeforeReadingContextVersion(t *testing.T) {
	app := fiber.New()
	app.Use(httpx.AdmissionGuard(func(c fiber.Ctx) { httpx.SetProjectionAudience(c, httpx.ProjectionAudience{ContextVersion: "7"}) }))
	app.Get("/call", func(c fiber.Ctx) error {
		authz.SetPrincipal(c, &authz.Principal{UserID: uuid.New()})
		cl, err := (adminHTTP{}).call(c)
		if err != nil {
			return err
		}
		if cl.contextVersion != "7" {
			t.Errorf("context version=%q", cl.contextVersion)
		}
		return c.SendStatus(204)
	})
	res, err := app.Test(httptest.NewRequest("GET", "/call", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
}

func TestReviewTenantMatrixServiceGuard(t *testing.T) {
	p := &authz.Principal{TenantRole: authz.SupportReadOnly, Platform: []authz.PlatformRole{authz.PlatformAdmin}}
	_, _, err := (&Admin{}).SaveMatrix(call{p: p}, MatrixTarget{Layer: LayerTenant}, SaveMatrixInput{})
	if e, ok := err.(*httpx.Error); !ok || e.Code != httpx.CodeSupportReadOnly {
		t.Fatalf("guard=%v", err)
	}
}

func TestReviewQuarantineLabelIsStructural(t *testing.T) {
	id := uuid.New()
	p := &authz.Principal{Platform: []authz.PlatformRole{authz.PlatformAdmin}}
	c := call{p: p, labels: map[uuid.UUID]platformLabel{id: {kind: "quarantine", nameTh: "Quarantine"}}}
	out := Tenant{ID: id}
	if err := (&Admin{}).projectTenantLabels(c, &out); err != nil {
		t.Fatal(err)
	}
	if out.NameTh != "Quarantine" {
		t.Fatalf("quarantine label=%q", out.NameTh)
	}
}

func TestReviewPlatformPageCountsNestedOwners(t *testing.T) {
	users := make([]User, 60)
	for i := range users {
		users[i].Memberships = []Membership{{TenantID: uuid.New()}, {TenantID: uuid.New()}}
	}
	p := &authz.Principal{Platform: []authz.PlatformRole{authz.PlatformAdmin}}
	if n := platformUserPageSize(users, p); n != 50 {
		t.Fatalf("page=%d want 50", n)
	}
}

func TestReviewSupportCursorContinuesAcrossInstances(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("SUPPORT_CURSOR_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{23}, 32)))
	key1, err := supportCursorKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	key2, err := supportCursorKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	first, second := &Admin{cursorKey: key1}, &Admin{cursorKey: key2}
	actor := uuid.New()
	cur := supportCursor{Actor: actor, Query: supportQueryHash("query"), Time: time.Now().UTC(), ID: uuid.New()}
	if _, ok := second.decodeSupportCursor(first.encodeSupportCursor(cur), actor, "query"); !ok {
		t.Fatal("replica could not continue cursor")
	}
	t.Setenv("SUPPORT_CURSOR_KEY", "")
	t.Setenv("JWT_SIGNING_KEY_FILE", "")
	if _, err := supportCursorKey(nil); err == nil {
		t.Fatal("production used a random cursor key")
	}
}

func TestReviewSupportPlatformControlPredicates(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		policy                   httpx.EndpointPolicy
		want                     int
	}{
		{"provision", "POST", "/v1/tenants", `{"kind":"carrier","code":"NEW","nameTh":"Company","legalType":"company"}`, provisioningPolicy(), 204},
		{"provision plus account grant", "POST", "/v1/tenants", `{"kind":"carrier","userId":"x"}`, provisioningPolicy(), 403},
		{"lifecycle", "PATCH", "/v1/tenants/" + uuid.NewString(), `{"status":"suspended","reason":"review"}`, tenantPatchPolicy(), 204},
		{"profile", "PATCH", "/v1/tenants/" + uuid.NewString(), `{"nameTh":"edited"}`, tenantPatchPolicy(), 403},
		{"mixed", "PATCH", "/v1/tenants/" + uuid.NewString(), `{"status":"active","reason":"review","nameTh":"edited"}`, tenantPatchPolicy(), 403},
		{"duplicate status", "PATCH", "/v1/tenants/" + uuid.NewString(), `{"status":"active","status":"suspended","reason":"review"}`, tenantPatchPolicy(), 403},
		{"empty", "PATCH", "/v1/tenants/" + uuid.NewString(), `{}`, tenantPatchPolicy(), 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error { return httpx.WriteError(c, err.(*httpx.Error)) }})
			app.Add([]string{tc.method}, tc.policy.Route, func(c fiber.Ctx) error {
				authz.SetPrincipal(c, &authz.Principal{Platform: []authz.PlatformRole{authz.PlatformAdmin}, TenantRole: authz.SupportReadOnly})
				httpx.SetProjectionAudience(c, httpx.ProjectionAudience{Platform: true, Support: true, Actor: uuid.NewString(), Tenant: uuid.NewString()})
				if err := httpx.EndpointGate(c, tc.policy); err != nil {
					return err
				}
				return c.SendStatus(204)
			})
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			raw, _ := io.ReadAll(res.Body)
			if res.StatusCode != tc.want {
				t.Fatalf("status=%d want=%d %s", res.StatusCode, tc.want, raw)
			}
		})
	}
}

type reviewContextTx struct {
	pgx.Tx
	version int64
}
type reviewContextRow struct{ version int64 }

func (tx reviewContextTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return reviewContextRow{version: tx.version}
}
func (r reviewContextRow) Scan(dst ...any) error {
	*dst[0].(*string) = "member"
	*dst[1].(*int64) = r.version
	*dst[2].(**uuid.UUID) = nil
	*dst[3].(**time.Time) = nil
	*dst[4].(*time.Time) = time.Now().Add(time.Hour)
	*dst[5].(*int32) = 1
	*dst[6].(*string) = "active"
	return nil
}
func TestReviewLiveIAMContextUsesResolvedVersionAndServiceAdapter(t *testing.T) {
	p := &authz.Principal{UserID: uuid.New(), SessionID: uuid.New(), AuthVersion: 1}
	for _, version := range []string{"7", ""} {
		if err := liveIAMContext(call{ctx: context.Background(), p: p, contextVersion: version}, reviewContextTx{version: 7}); err != nil {
			t.Fatalf("version %q: %v", version, err)
		}
	}
	if err := liveIAMContext(call{ctx: context.Background(), p: p, contextVersion: "0"}, reviewContextTx{version: 7}); err == nil {
		t.Fatal("stale explicit legacy version accepted")
	}
}
