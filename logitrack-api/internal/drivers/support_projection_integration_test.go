//go:build integration

package drivers_test

import (
	"context"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

type supportSigningFiles struct {
	*storage.Service
	calls atomic.Int64
}

func (s *supportSigningFiles) SignedFile(ctx context.Context, id uuid.UUID, ttl time.Duration) (string, string, error) {
	s.calls.Add(1)
	return s.Service.SignedFile(ctx, id, ttl)
}
func (w *world) supportVisit(tenant, bearer string) string {
	w.t.Helper()
	r := w.post("/v1/auth/tenant", bearer, map[string]any{"mode": "support", "tenantId": tenant})
	expect(w.t, r, 200, "")
	return str(r.data()["accessToken"])
}
func supportRedacted(t *testing.T, r resp, path string) {
	t.Helper()
	entries, _ := r.meta()["redacted"].([]any)
	for _, e := range entries {
		if e.(map[string]any)["path"] == path {
			return
		}
	}
	t.Fatalf("missing redaction %s: %s", path, r.raw)
}
func supportNoLeak(t *testing.T, r resp, needles ...string) {
	t.Helper()
	for _, s := range needles {
		if strings.Contains(string(r.raw), s) {
			t.Fatalf("leak %s: %s", s, r.raw)
		}
	}
}

func TestSupportFleetExactReachProjectionAndNoSigning(t *testing.T) {
	w := newWorldWith(t, auth.Firebase{}, true)
	w.exec(`SELECT set_config('app.role','system',false),set_config('app.bypass_tenant','on',false)`)
	truck := w.truck(w.O, "กก-2222")
	foreign := w.truck(w.A, "กก-3333")
	driver := w.driver(w.O, "PERSON_SENTINEL", "id_card", "'ID_CARD_SENTINEL'", "birth_date", "'1990-01-02'", "legacy_auth_uid", "'PRIVATE_SENTINEL'")
	foreignDriver := w.driver(w.A, "FOREIGN_PERSON_SENTINEL")
	w.exec(`UPDATE trucks SET notes='TEXT_SENTINEL',gps_vehicle_id='TRACKER_SENTINEL',tax_expense_thb=123456.78,insurance_policy_number='POLICY_SENTINEL' WHERE id=$1`, truck)
	w.exec(`INSERT INTO truck_assignments(tenant_id,tenant_source,truck_id,driver_id,status,admin_label) VALUES($1,'truck',$2,$3,'active','ADMIN_SENTINEL'),($1,'truck',$2,$4,'active','FOREIGN_ADMIN_SENTINEL')`, w.O, truck, driver, foreignDriver)
	w.exec(`INSERT INTO driver_customer_codes(driver_id,customer_id,id_type_key,value) VALUES($1,$2,'appId','EXTERNAL_SENTINEL')`, driver, w.spx)
	mid := w.id(`INSERT INTO maintenance_records(tenant_id,truck_id,maintenance_type,service_type,start_date,status,total_cost_thb,provider,provider_lat,provider_lng,notes) VALUES($1,$2,'PM','SERVICE_SENTINEL','2026-10-18','completed',87654.32,'PROVIDER_SENTINEL',13.765432,100.123456,'MAINT_TEXT_SENTINEL') RETURNING id::text`, w.O, truck)
	w.exec(`INSERT INTO vehicle_locations(truck_id,tenant_id,tenant_source,gps_vehicle_id,lat,lng,driver_name_raw,position_description) VALUES($1,$2,'truck','TRACKER_SENTINEL',13.765432,100.123456,'RAW_GPS_PERSON_SENTINEL','ADDRESS_SENTINEL')`, truck, w.O)
	file := w.id(`INSERT INTO file_objects(bucket,object_key,tenant_id,purpose,owner_kind,owner_id,status,committed_at,storage_backend) VALUES('logitrack-private','FILE_KEY_SENTINEL',$1,'truck_document','truck',$2,'committed',now(),'local') RETURNING id::text`, w.O, truck)
	w.exec(`INSERT INTO truck_files(truck_id,file_id,kind) VALUES($1,$2,'document_register')`, truck, file)
	w.exec(`UPDATE drivers SET profile_file_id=$2,id_card_file_id=$2,license_file_id=$2 WHERE id=$1`, driver, file)
	w.exec(`INSERT INTO maintenance_files(maintenance_id,file_id,kind) VALUES($1,$2,'receipt')`, mid, file)

	login := w.post("/v1/auth/login", "", map[string]any{"email": "pa@example.test", "password": pw, "platform": "web"})
	expect(t, login, 200, "")
	refresh := str(login.data()["refreshToken"])
	support := w.supportVisit(w.O, str(login.data()["accessToken"]))
	for _, path := range []string{"/v1/trucks", "/v1/trucks/" + truck, "/v1/trucks/renewals", "/v1/trucks/renewals?q=2222", "/v1/truck-assignments", "/v1/drivers", "/v1/drivers?fields=minimal", "/v1/drivers/" + driver, "/v1/maintenance", "/v1/vehicle-locations?truckId=" + truck} {
		r := w.get(path, support)
		expect(t, r, 200, "")
		supportNoLeak(t, r, "PERSON_SENTINEL", "TEXT_SENTINEL", "TRACKER_SENTINEL", "POLICY_SENTINEL", "ADMIN_SENTINEL", "EXTERNAL_SENTINEL", "SERVICE_SENTINEL", "PROVIDER_SENTINEL", "ADDRESS_SENTINEL", "FILE_KEY_SENTINEL", "123456.78", "87654.32", "13.765432", "100.123456", foreign)
		if r.header.Get("Cache-Control") != "private, no-store" || r.meta()["supportAccess"] == nil {
			t.Fatalf("unsealed projection: %s", r.raw)
		}
	}
	r := w.get("/v1/trucks/"+truck, support)
	if r.data()["taxExpenseThb"] != nil || r.data()["gpsVehicleId"] != nil {
		t.Fatal(string(r.raw))
	}
	supportRedacted(t, r, "/data/gpsVehicleId")
	d := w.get("/v1/drivers/"+driver, support)
	if d.data()["birthDate"] != "1990-01-02" || d.data()["tenantNameTh"] != "O" || d.data()["displayName"] != "****" {
		t.Fatal(string(d.raw))
	}
	supportRedacted(t, d, "/data/idCard")
	expect(t, w.get("/v1/trucks/"+foreign, support), 404, "not_found")
	expect(t, w.get("/v1/drivers/"+foreignDriver, support), 404, "not_found")
	for _, kind := range []string{"profile", "id_card", "license"} {
		r = w.get("/v1/drivers/"+driver+"/documents/"+kind, support)
		expect(t, r, 403, "support_access_masked")
		if r.header.Get("Location") != "" {
			t.Fatal(r.header)
		}
	}
	if w.signing.calls.Load() != 0 {
		t.Fatal("support called signer")
	}
	before := w.count(`SELECT count(*) FROM outbox_events`)
	for _, v := range []struct{ method, path string }{{"POST", "/v1/trucks"}, {"PATCH", "/v1/trucks/" + truck}, {"POST", "/v1/trucks/import"}, {"POST", "/v1/trucks/" + truck + "/renewals"}, {"POST", "/v1/truck-assignments"}, {"POST", "/v1/drivers"}, {"PATCH", "/v1/drivers/" + driver}, {"POST", "/v1/maintenance"}, {"PATCH", "/v1/maintenance/" + mid}, {"POST", "/v1/maintenance/" + mid + "/remind"}, {"POST", "/v1/trucks/" + truck + "/pm-check"}, {"GET", "/v1/admin/cartrack/probe"}} {
		expect(t, w.do(v.method, v.path, support, map[string]any{}), 403, "support_read_only")
	}
	if w.count(`SELECT count(*) FROM outbox_events`) != before {
		t.Fatal("denied writes touched outbox")
	}
	// ON reveals attributable selected rows only; global/customer/admin/GPS labels and unbrokered files remain masked.
	w.exec(`UPDATE tenants SET support_access_enabled=true,support_access_version=support_access_version+1,support_access_changed_at=now(),support_access_changed_by=$2 WHERE id=$1`, w.O, w.pa)
	expect(t, w.get("/v1/drivers", support), 401, "token_expired")
	rotated := w.post("/v1/auth/refresh", "", map[string]any{"refreshToken": refresh})
	expect(t, rotated, 200, "")
	support = str(rotated.data()["accessToken"])
	r = w.get("/v1/drivers/"+driver, support)
	expect(t, r, 200, "")
	if !strings.Contains(string(r.raw), "PERSON_SENTINEL") {
		t.Fatal("ON selected person missing", string(r.raw))
	}
	supportNoLeak(t, r, "FILE_KEY_SENTINEL")
	if !strings.Contains(string(r.raw), "EXTERNAL_SENTINEL") {
		t.Fatal("ON lost selected-driver external ID", string(r.raw))
	}
	codes := r.data()["customerDriverIds"].([]any)
	code := codes[0].(map[string]any)
	if code["customerCode"] != nil || code["customerName"] != "****" {
		t.Fatal("global customer label inherited driver consent", code)
	}
	r = w.get("/v1/trucks/"+truck, support)
	expect(t, r, 200, "")
	if r.data()["taxExpenseThb"] != 123456.78 || r.data()["gpsVehicleId"] != "TRACKER_SENTINEL" {
		t.Fatal(string(r.raw))
	}
	supportNoLeak(t, r, "FOREIGN_PERSON_SENTINEL", "ADMIN_SENTINEL", "FILE_KEY_SENTINEL")
	r = w.get("/v1/vehicle-locations?truckId="+truck, support)
	expect(t, r, 200, "")
	supportNoLeak(t, r, "RAW_GPS_PERSON_SENTINEL")
	expect(t, w.get("/v1/truck-assignments?q=ADMIN", support), 403, "support_view_unavailable")
	r = w.get("/v1/maintenance", support)
	expect(t, r, 200, "")
	if r.list()[0]["displayCostThb"] != 87654.32 || r.list()[0]["providerLat"] != 13.765432 {
		t.Fatal(string(r.raw))
	}
	supportNoLeak(t, r, "FILE_KEY_SENTINEL")
	expect(t, w.get("/v1/drivers/"+driver+"/documents/license", support), 404, "not_found")
	if w.signing.calls.Load() != 0 {
		t.Fatal("ON called signer")
	}
	// A mode switch invalidates the old visit immediately.
	expect(t, w.post("/v1/auth/tenant", support, map[string]any{"mode": "support", "tenantId": w.A}), 200, "")
	expect(t, w.get("/v1/trucks", support), 401, "token_expired")
}

func TestSupportFleetPaginationPredicatesAndPersonalTenantLabels(t *testing.T) {
	w := newWorldWith(t, auth.Firebase{}, true)
	w.exec(`SELECT set_config('app.role','system',false),set_config('app.bypass_tenant','on',false)`)
	for i := range 3 {
		w.driver(w.A, "PERSON_PAGE_SENTINEL")
		w.truck(w.A, []string{"กก-1111", "กก-2222", "กก-3333"}[i])
	}
	w.exec(`UPDATE tenants SET legal_type='individual',name_th='TENANT_PERSON_SENTINEL' WHERE id=$1`, w.A)
	support := w.supportVisit(w.A, w.login("pa"))
	r := w.get("/v1/drivers?limit=1", support)
	expect(t, r, 200, "")
	next := r.next()
	if next == "" {
		t.Fatal("missing cursor")
	}
	supportNoLeak(t, r, "PERSON_PAGE_SENTINEL", "TENANT_PERSON_SENTINEL")
	page2 := w.get("/v1/drivers?limit=1&cursor="+url.QueryEscape(next), support)
	expect(t, page2, 200, "")
	if page2.list()[0]["id"] == r.list()[0]["id"] {
		t.Fatal("cursor did not advance")
	}
	for _, path := range []string{"/v1/drivers?limit=2&cursor=" + next, "/v1/trucks?limit=1&cursor=" + next, "/v1/drivers?limit=1&cursor=" + next + "x", "/v1/drivers?cursor=eyJpZCI6IjEifQ", "/v1/drivers?q=PERSON", "/v1/trucks?q=TRACKER", "/v1/truck-assignments?q=ADMIN", "/v1/drivers?limit=101", "/v1/drivers?status=active&status=inactive", "/v1/drivers?%71=PERSON", "/v1/vehicle-locations", "/v1/vehicle-locations?truckId=one&truckId=two"} {
		r = w.get(path, support)
		if r.status < 400 {
			t.Fatalf("predicate admitted %s: %s", path, r.raw)
		}
		supportNoLeak(t, r, "PERSON_PAGE_SENTINEL", "TENANT_PERSON_SENTINEL")
	}
	// Another actor/session cannot reuse the position even within the same tenant.
	user := w.user("second-pa@example.test")
	w.exec(`INSERT INTO user_platform_roles(user_id,role)VALUES($1,'platform_admin')`, user)
	second := w.supportVisit(w.A, w.login("second-pa"))
	expect(t, w.get("/v1/drivers?limit=1&cursor="+next, second), 403, "support_view_unavailable")
	// Ordinary users still get their original unmasked numeric/string contracts and larger pages.
	member := w.login("ta.a")
	r = w.get("/v1/drivers?limit=500", member)
	expect(t, r, 200, "")
	if !strings.Contains(string(r.raw), "PERSON_PAGE_SENTINEL") || r.meta()["supportAccess"] != nil {
		t.Fatal(string(r.raw))
	}
}

func TestSupportFleetOversizedNestedHistoryFailsClosed(t *testing.T) {
	w := newWorldWith(t, auth.Firebase{}, true)
	w.exec(`SELECT set_config('app.role','system',false),set_config('app.bypass_tenant','on',false)`)
	driver := w.driver(w.O, "NESTED_PERSON_SENTINEL")
	w.exec(`INSERT INTO status_history(entity_type,entity_id,status,changed_at,changed_by_label,reason) SELECT 'driver',$1,'active',now(),'NESTED_LABEL_SENTINEL','NESTED_TEXT_SENTINEL' FROM generate_series(1,101)`, driver)
	support := w.supportVisit(w.O, w.login("pa"))
	r := w.get("/v1/drivers/"+driver, support)
	expect(t, r, 503, "support_projection_unavailable")
	supportNoLeak(t, r, "NESTED_PERSON_SENTINEL", "NESTED_TEXT_SENTINEL", "NESTED_LABEL_SENTINEL")
}
