//go:build integration

package dashboard_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func (w *world) support(target uuid.UUID, bearer string) string {
	w.t.Helper()
	r := w.call(w.internal, http.MethodPost, "/v1/auth/tenant", bearer, map[string]any{"mode": "support", "tenantId": target.String()})
	expect(w.t, r, 200, "")
	return strData(r, "accessToken")
}

func strData(r resp, key string) string { s, _ := r.data()[key].(string); return s }

func TestSupportDashboardUsesExactTenantCountsAndProjectsRecent(t *testing.T) {
	w := newWorld(t, worldOpts{supportEntry: true})
	w.exec(`SELECT set_config('app.role','system',false),set_config('app.bypass_tenant','on',false)`)
	// A and C have independent data; A works for O but never contributes to O in support.
	w.exec(`UPDATE drivers SET first_name='PERSON_SENTINEL',last_name='',full_name_th=NULL WHERE id=$1`, w.dO)
	w.exec(`UPDATE trip_records SET destination_raw='ADDRESS_SENTINEL' WHERE tenant_id=$1`, w.O)
	w.exec(`UPDATE maintenance_records SET service_type='TEXT_SENTINEL' WHERE tenant_id=$1`, w.O)
	w.exec(`INSERT INTO waitlist(email,name) VALUES('lead@example.test','LEAD_SENTINEL')`)
	shared := w.id(`SELECT id FROM users WHERE email='ta.o@example.test'`)
	w.exec(`INSERT INTO memberships(user_id,tenant_id,role) VALUES($1,$2,'manager')`, shared, w.C)
	// An inactive membership is not a current company user; a disabled holder of an active membership still counts.
	w.member("inactive.o@example.test", w.O, "manager")
	w.exec(`UPDATE memberships SET status='suspended' WHERE user_id=(SELECT id FROM users WHERE email='inactive.o@example.test')`)
	w.member("disabled.o@example.test", w.O, "manager")
	w.exec(`UPDATE users SET status='disabled',disabled_at=now() WHERE email='disabled.o@example.test'`)

	member := w.login("ta.o@example.test")
	platform := w.login("pa@example.test")
	support := w.support(w.O, platform)
	r := w.get("/v1/dashboard/summary", support)
	expect(t, r, 200, "")
	d := r.data()
	if num(obj(d["trips"])["delivered"]) != 3 || num(obj(d["trips"])["parcels"]) != 35 || num(obj(d["drivers"])["total"]) != 3 {
		t.Fatalf("support inherited contractor rows: %s", r.raw)
	}
	u := obj(d["users"])
	if num(u["total"]) != 4 || num(obj(u["byRole"])["tenant_admin"]) != 1 || num(obj(u["byRole"])["manager"]) != 1 {
		t.Fatalf("selected user count: %s", r.raw)
	}
	if d["incidents"] != nil {
		t.Fatalf("incident widget was admitted: %s", r.raw)
	}
	for _, sentinel := range []string{"PERSON_SENTINEL", "ADDRESS_SENTINEL", "TEXT_SENTINEL", "LEAD_SENTINEL"} {
		if strings.Contains(string(r.raw), sentinel) {
			t.Fatalf("OFF leak: %s", r.raw)
		}
	}
	for _, row := range d["recent"].([]any) {
		m := obj(row)
		if m["type"] == "incident" {
			t.Fatal("support loaded incident detail")
		}
		if m["type"] == "driver" && m["name"] != "****" {
			t.Fatalf("unmasked recent driver: %v", m)
		}
	}
	if r.header.Get("Cache-Control") != "private, no-store" {
		t.Fatal(r.header)
	}
	b := w.get("/v1/badges", support)
	expect(t, b, 200, "")
	if num(b.data()["pendingExpenses"]) != 2 || num(b.data()["unassignedChats"]) != 2 || num(b.data()["urgentChats"]) != 1 || num(b.data()["waitlist"]) != 1 {
		t.Fatalf("badges escaped tenant/capability boundary: %s", b.raw)
	}
	if strings.Contains(string(b.raw), "LEAD_SENTINEL") {
		t.Fatal(string(b.raw))
	}
	// A normal member retains contractor reach and its original response shape.
	m := w.get("/v1/dashboard/summary", member)
	expect(t, m, 200, "")
	if num(obj(m.data()["trips"])["delivered"]) != 4 || m.data()["users"] != nil || m.body["meta"] != nil || !strings.Contains(string(m.raw), "PERSON_SENTINEL") {
		t.Fatalf("member contract changed: %s", m.raw)
	}
	// Context switches cannot turn O's data into C's, and old support credentials cannot be replayed.
	next := w.support(w.C, support)
	expect(t, w.get("/v1/dashboard/summary", support), 401, "token_expired")
	r = w.get("/v1/dashboard/summary", next)
	expect(t, r, 200, "")
	if num(obj(r.data()["trips"])["parcels"]) != 200 || num(obj(r.data()["users"])["total"]) != 2 {
		t.Fatalf("switched tenant count: %s", r.raw)
	}
	if strings.Contains(string(r.raw), "SENTINEL") {
		t.Fatal(string(r.raw))
	}
	// The public listener never exposes these internal endpoints.
	expect(t, w.call(w.public, http.MethodGet, "/v1/dashboard/summary", next, nil), 404, "not_found")
}

func TestSupportDashboardConsentVersionsAndMutationRefusal(t *testing.T) {
	w := newWorld(t, worldOpts{supportEntry: true})
	w.exec(`SELECT set_config('app.role','system',false),set_config('app.bypass_tenant','on',false)`)
	w.exec(`UPDATE drivers SET first_name='PERSON_SENTINEL',last_name='',full_name_th=NULL WHERE id=$1`, w.dO)
	login := w.call(w.public, http.MethodPost, "/v1/auth/login", "", map[string]any{"email": "pa@example.test", "password": pw, "platform": "web"})
	expect(t, login, 200, "")
	platform, refresh := strData(login, "accessToken"), strData(login, "refreshToken")
	rotate := func() string {
		r := w.call(w.public, http.MethodPost, "/v1/auth/refresh", "", map[string]any{"refreshToken": refresh})
		expect(t, r, 200, "")
		refresh = strData(r, "refreshToken")
		return strData(r, "accessToken")
	}
	support := w.support(w.O, platform)
	w.exec(`SELECT set_config('app.role','system',false)`)
	// This fixture update represents the already-tested consent service transaction.
	w.exec(`UPDATE tenants SET support_access_enabled=true,support_access_version=support_access_version+1,
		support_access_changed_at=now(),support_access_changed_by=(SELECT id FROM users WHERE email='ta.o@example.test') WHERE id=$1`, w.O)
	expect(t, w.get("/v1/dashboard/summary", support), 401, "token_expired")
	// Refresh reissues the current consent version without creating a new visit.
	support = rotate()
	r := w.get("/v1/dashboard/summary", support)
	expect(t, r, 200, "")
	if !strings.Contains(string(r.raw), "PERSON_SENTINEL") {
		t.Fatalf("ON did not reveal selected-tenant data: %s", r.raw)
	}
	w.exec(`UPDATE tenants SET support_access_enabled=false,support_access_version=support_access_version+1,
		support_access_changed_at=now(),support_access_changed_by=(SELECT id FROM users WHERE email='ta.o@example.test') WHERE id=$1`, w.O)
	expect(t, w.get("/v1/dashboard/summary", support), 401, "token_expired")
	support = rotate()
	r = w.get("/v1/dashboard/summary", support)
	expect(t, r, 200, "")
	if strings.Contains(string(r.raw), "PERSON_SENTINEL") {
		t.Fatal("OFF after lost notification leaked", string(r.raw))
	}
	before := w.count(`SELECT count(*) FROM jobs`)
	expect(t, w.get("/v1/admin/cartrack/probe", support), 403, "support_read_only")
	if w.count(`SELECT count(*) FROM jobs`) != before {
		t.Fatal("denied support GET created a job")
	}
	// The support histogram is bounded; the ordinary 366-day contract is preserved.
	expect(t, w.get("/v1/dashboard/summary?from=2026-01-01&to=2026-10-01", support), 422, "invalid_argument")
	expect(t, w.get("/v1/dashboard/summary?from=2026-01-01&to=2026-10-01", w.login("ta.o@example.test")), 200, "")
}
