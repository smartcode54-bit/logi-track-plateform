//go:build integration

package jobs_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

func TestGate1PlatformJobPayloadsStayInOwnTenant(t *testing.T) {
	f := setup(t)
	user := f.user(t, "job-review@example.test")
	other := f.user(t, "other-job-review@example.test")
	own, foreign := uuid.New(), uuid.New()
	var local, remote, global, ownedGlobal, ownedRemote jobs.Job
	err := db.WithSystem(t.Context(), f.pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `INSERT INTO tenants(id,kind,name_th,legal_type)VALUES($1,'carrier','Own','company'),($2,'carrier','Foreign','company')`, own, foreign); err != nil {
			return err
		}
		for _, item := range []struct {
			tenant   *uuid.UUID
			owner    *uuid.UUID
			sentinel string
			dest     *jobs.Job
		}{{&own, &other, "OWN-params", &local}, {&foreign, &other, "FOREIGN-params", &remote}, {nil, &other, "GLOBAL-params", &global}, {nil, &user, "OWNER-global", &ownedGlobal}, {&foreign, &user, "OWNER-remote", &ownedRemote}} {
			j, err := jobs.Enqueue(t.Context(), tx, jobs.NewInput{Type: jobs.TypePayrollRun, OwnerUserID: item.owner, TenantID: item.tenant, Params: map[string]any{"private": item.sentinel}})
			if err != nil {
				return err
			}
			*item.dest = j
		}
		if _, err := jobs.Start(t.Context(), tx, remote.ID); err != nil {
			return err
		}
		if _, err := jobs.Fail(t.Context(), tx, remote.ID, "FOREIGN-error", map[string]any{"private": "FOREIGN-result"}); err != nil {
			return err
		}
		if _, err := jobs.Start(t.Context(), tx, global.ID); err != nil {
			return err
		}
		_, err := jobs.Succeed(t.Context(), tx, global.ID, map[string]any{"private": "GLOBAL-result"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"platform_admin", "support"} {
		for _, withTenant := range []bool{false, true} {
			headers := []string{}
			want := []string{ownedGlobal.ID.String(), ownedRemote.ID.String()}
			if withTenant {
				headers = []string{"X-Test-Tenant", own.String(), "X-Test-Capability", "hr:manage_payroll"}
				want = append(want, local.ID.String())
			}
			r := f.do(t, "GET", "/v1/jobs", &user, role, headers...)
			raw, _ := json.Marshal(r.body)
			got := ids(r)
			slices.Sort(got)
			slices.Sort(want)
			if r.status != 200 || !slices.Equal(got, want) || strings.Contains(string(raw), "FOREIGN") || strings.Contains(string(raw), "GLOBAL") {
				t.Fatalf("%s (tenant %t) list: %d %s, want %v", role, withTenant, r.status, raw, want)
			}
			for _, j := range []jobs.Job{local, remote, global, ownedGlobal, ownedRemote} {
				wantStatus := 404
				if slices.Contains(want, j.ID.String()) {
					wantStatus = 200
				}
				if r := f.do(t, "GET", "/v1/jobs/"+j.ID.String(), &user, role, headers...); r.status != wantStatus {
					t.Fatalf("%s (tenant %t) get %s: %d, want %d", role, withTenant, j.ID, r.status, wantStatus)
				}
			}
		}
	}
	if r := f.do(t, "GET", "/v1/jobs/"+remote.ID.String(), &other, ""); r.status != 200 {
		t.Fatal("ordinary owner contract changed")
	}
}
