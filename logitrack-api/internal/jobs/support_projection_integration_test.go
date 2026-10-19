//go:build integration

package jobs_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

func TestSupportJobsExactTenantProjectionAndVisitCursors(t *testing.T) {
	f := setup(t)
	user := f.user(t, "support-jobs@example.test")
	own, foreign := uuid.New(), uuid.New()
	var visible jobs.Job
	var hidden []jobs.Job
	err := db.WithSystem(t.Context(), f.pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `INSERT INTO tenants(id,kind,name_th,legal_type)VALUES($1,'carrier','Own','company'),($2,'carrier','Foreign','company')`, own, foreign); err != nil {
			return err
		}
		for n := 0; n < 102; n++ {
			j, err := jobs.Enqueue(t.Context(), tx, jobs.NewInput{Type: jobs.TypeCartrackSync, TenantID: &own, OwnerUserID: &user, Params: map[string]any{"private": "PRIVATE-params"}})
			if err != nil {
				return err
			}
			visible = j
		}
		for _, item := range []struct {
			typ    string
			tenant *uuid.UUID
		}{{jobs.TypeCartrackSync, &foreign}, {jobs.TypeCartrackSync, nil}, {jobs.TypePayrollRun, &own}, {"future.unknown", &own}} {
			j, err := jobs.Enqueue(t.Context(), tx, jobs.NewInput{Type: item.typ, TenantID: item.tenant, OwnerUserID: &user, Params: map[string]any{"private": "PRIVATE-foreign-or-deferred"}})
			if err != nil {
				return err
			}
			hidden = append(hidden, j)
		}
		if _, err := jobs.Start(t.Context(), tx, visible.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(t.Context(), `UPDATE jobs SET progress=$2 WHERE id=$1`, visible.ID, json.RawMessage(`{"done":1,"total":2,"private":"PRIVATE-progress"}`)); err != nil {
			return err
		}
		_, err := jobs.Fail(t.Context(), tx, visible.ID, "PRIVATE-error", map[string]any{"private": "PRIVATE-result"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	tenant := own.String()
	audience := httpx.ProjectionAudience{Support: true, Platform: true, Actor: user.String(), Session: uuid.NewString(), Tenant: tenant, ContextVersion: "1", ConsentVersion: "0", Consents: map[string]httpx.Consent{tenant: {Version: "0"}}}
	audience.Finalize = func(_ context.Context, _ []string, fn func(map[string]httpx.Consent) error) error {
		return fn(audience.Consents)
	}
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Use(httpx.RequestID(), httpx.AdmissionGuard(nil))
	svc := jobs.NewService(f.pool, f.locks)
	ingress.Mount(app, ingress.Internal, svc.Groups(jobs.HTTPOptions{Auth: func(c fiber.Ctx) error {
		p := &authz.Principal{UserID: user, SessionID: uuid.MustParse(audience.Session), AccessMode: "support", TenantID: &own, TenantRole: authz.SupportReadOnly, Platform: []authz.PlatformRole{authz.PlatformAdmin}}
		authz.SetPrincipal(c, p)
		httpx.SetProjectionAudience(c, audience)
		return c.Next()
	}, Caller: func(p *authz.Principal) (jobs.Caller, bool) {
		return jobs.Caller{UserID: p.UserID, TenantID: p.TenantID, Support: true, PlatformAdmin: true}, true
	}}), nil)
	f.app = app
	for _, enabled := range []bool{false, true} {
		audience.Consents[tenant] = httpx.Consent{Enabled: enabled, Version: audience.ConsentVersion}
		page := f.do(t, "GET", "/v1/jobs?limit=100", &user, "")
		cursor, _ := page.body["nextCursor"].(string)
		raw, _ := json.Marshal(page.body)
		if page.status != 200 || len(ids(page)) != 100 || cursor == "" || strings.Contains(string(raw), "PRIVATE") {
			t.Fatalf("page ON=%t: %d %s", enabled, page.status, raw)
		}
		rest := f.do(t, "GET", "/v1/jobs?limit=100&cursor="+url.QueryEscape(cursor), &user, "")
		if rest.status != 200 || len(ids(rest)) != 2 {
			t.Fatalf("page2: %d %v", rest.status, rest.body)
		}
		one := f.do(t, "GET", "/v1/jobs/"+visible.ID.String(), &user, "")
		raw, _ = json.Marshal(one.body)
		data, _ := one.body["data"].(map[string]any)
		if one.status != 200 || data["params"] != nil || data["result"] != nil || data["error"] != nil || strings.Contains(string(raw), "PRIVATE") {
			t.Fatalf("detail ON=%t: %d %s", enabled, one.status, raw)
		}
		progress, _ := data["progress"].(map[string]any)
		if len(progress) != 2 || progress["done"] != float64(1) || progress["total"] != float64(2) {
			t.Fatalf("safe count progress: %v", progress)
		}
		for _, j := range hidden {
			if r := f.do(t, "GET", "/v1/jobs/"+j.ID.String(), &user, ""); r.status != 404 {
				t.Fatalf("owned foreign/global/deferred: %d %v", r.status, r.body)
			}
		}
		for _, path := range []string{"/v1/jobs?type=payroll.run", "/v1/jobs?q=PRIVATE", "/v1/jobs?limit=101", "/v1/jobs?limit=1&cursor=" + url.QueryEscape(cursor)} {
			if r := f.do(t, "GET", path, &user, ""); r.status < 400 {
				t.Fatalf("admitted %s: %d", path, r.status)
			}
		}
		audience.ContextVersion = "2"
		if r := f.do(t, "GET", "/v1/jobs?limit=100&cursor="+url.QueryEscape(cursor), &user, ""); r.status != 403 {
			t.Fatalf("old visit cursor: %d %v", r.status, r.body)
		}
		audience.ContextVersion = "1"
	}
	var before, after int
	if err := f.etl.QueryRow(t.Context(), `SELECT count(*) FROM jobs`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if r := f.do(t, "POST", "/v1/admin/queues/billing/replay", &user, "platform_admin"); r.status != 403 || code(r) != "support_read_only" {
		t.Fatalf("replay: %d %v", r.status, r.body)
	}
	if err := f.etl.QueryRow(t.Context(), `SELECT count(*) FROM jobs`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("support replay enqueued a job")
	}
}
