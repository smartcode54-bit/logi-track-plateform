//go:build integration

package drivers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase/firebasetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// piiKeys are the JSON keys of the PII fields (Appendix C §C.2.3 row 15).
var piiKeys = []string{"idCard", "idCardExpiredDate", "birthDate", "idCardKey", "idCardUrl", "licenseKey", "licenseUrl"}

// POST /v1/drivers writes the profile, its files and customer driver ids, the first status entry ("Initial
// Registration") and driver.created in one transaction; GET returns it; PATCH changes what it names, appends a status
// entry with its actor and reason on a status change, emits driver.updated with the changed fields, and writes
// nothing when nothing changes. The form rules apply to new and changed values; a legacy value sent back as it is
// stays editable.
func TestDriverCreateGetPatch(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o")
	taID := w.id(`SELECT id::text FROM users WHERE email = 'ta.o@example.test'`)
	tmp := "019a0000-0000-7000-8000-000000000001" // the client's id for the presign path; the commit decides the owner
	profile := w.upload(ta, "driver_profile", tmp, "image/png", png)
	idCard := w.upload(ta, "driver_id_card", tmp, "application/pdf", pdf)
	r := w.post("/v1/drivers", ta, newDriver("Somchai", map[string]any{
		"email": "Somchai@Example.test", "idCard": validIDCard, "birthDate": "1990-05-01", "truckLicenseId": "12345678",
		"licenseType": "ท.2", "licenseExpiredDate": "2028-01-31", "contractYears": 2.5, "hireDate": "2024-01-01",
		"probationPassed": true, "profileKey": profile, "idCardKey": idCard,
		"customerDriverIds": []map[string]any{{"customerId": w.spx, "idTypeKey": "appId", "value": "SPX-001"}},
	}))
	expect(t, r, http.StatusCreated, "")
	d := r.data()
	id := str(d["id"])
	if d["tenantId"] != w.O || d["status"] != "active" || d["employmentType"] != "full_time" || d["email"] != "somchai@example.test" ||
		d["displayName"] != "Somchai ดี" || d["idCard"] != validIDCard || d["birthDate"] != "1990-05-01" || d["contractYears"] != 2.5 {
		t.Fatalf("created driver %s", r.raw)
	}
	if d["activeTruck"] != nil || d["currentAssignment"] != nil || d["userId"] != nil || d["login"] != nil {
		t.Errorf("a new driver holds no truck and no login: %s", r.raw)
	}
	if str(d["profileKey"]) != profile || str(d["profileUrl"]) == "" || str(d["idCardKey"]) != idCard || str(d["idCardUrl"]) == "" {
		t.Errorf("files %s", r.raw)
	}
	if r.header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("a payload with PII is not cacheable: %q", r.header.Get("Cache-Control"))
	}
	codes := d["customerDriverIds"].([]any)
	if len(codes) != 1 || codes[0].(map[string]any)["customerCode"] != "SPX" || codes[0].(map[string]any)["value"] != "SPX-001" {
		t.Errorf("customer driver ids %v", codes)
	}
	hist := d["statusHistory"].([]any)
	if len(hist) != 1 {
		t.Fatalf("status history %v", hist)
	}
	if h := hist[0].(map[string]any); h["status"] != "active" || h["reason"] != "Initial Registration" || h["changedBy"] != taID ||
		h["changedByLabel"] != "ta.o@example.test" {
		t.Errorf("first status entry %v", h)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'driver.created' AND aggregate_id = $1
		AND realtime_topics = ARRAY['tenant:' || $2 || ':fleet']`, id, w.O); n != 1 {
		t.Errorf("driver.created events %d", n)
	}
	if n := w.count(`SELECT count(*) FROM file_objects WHERE owner_kind = 'driver' AND owner_id = $1 AND status = 'committed'`, id); n != 2 {
		t.Errorf("committed files %d", n)
	}

	// A carrier of the contractor reach: the driver goes there, a subcontractor by default.
	r = w.post("/v1/drivers", ta, newDriver("Anan", map[string]any{"tenantId": w.A}))
	expect(t, r, http.StatusCreated, "")
	if r.data()["tenantId"] != w.A || r.data()["employmentType"] != "subcontractor" || r.data()["tenantNameTh"] != "A" {
		t.Errorf("carrier driver %s", r.raw)
	}
	expect(t, w.post("/v1/drivers", ta, newDriver("Chai", map[string]any{"tenantId": w.C})), http.StatusUnprocessableEntity, "invalid_argument")

	// Validation: the form's rules, one reason per field.
	r = w.post("/v1/drivers", ta, map[string]any{"firstName": "X", "lastName": "Y", "mobile": "0", "email": "nope",
		"idCard": "1101700230709", "truckLicenseId": "1234567", "birthDate": "2010-01-01", "status": "busy",
		"licenseType": "x", "employmentStart": "2024-05-01", "employmentEnd": "2024-04-01",
		"customerDriverIds": []map[string]any{{"customerId": w.spx, "idTypeKey": "nope", "value": "1"},
			{"customerId": w.partyX, "idTypeKey": "appId", "value": "1"}}})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	want := map[string]string{"fullNameTh": "required", "email": "invalid", "idCard": "invalid", "truckLicenseId": "invalid_length",
		"birthDate": "out_of_range", "status": "invalid", "licenseType": "invalid", "employmentEnd": "before_start"}
	for f, reason := range want {
		if got := r.fields()[f]; got != reason {
			t.Errorf("%s: %q, want %q (%s)", f, got, reason, r.raw)
		}
	}
	r = w.post("/v1/drivers", ta, newDriver("Dao", map[string]any{"customerDriverIds": []map[string]any{
		{"customerId": w.spx, "idTypeKey": "nope", "value": "1"}, {"customerId": w.partyX, "idTypeKey": "appId", "value": "1"}}}))
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["customerDriverIds[0].idTypeKey"] != "not_found" || f["customerDriverIds[1].customerId"] != "not_found" {
		t.Errorf("customer driver ids %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM drivers WHERE first_name = 'Dao'`); n != 0 {
		t.Errorf("a refused create left %d drivers", n)
	}

	// PATCH: a status change appends its entry; the same body again changes nothing and writes nothing.
	patch := map[string]any{"status": "inactive", "statusNote": "on leave", "assignToProject": "SPX North"}
	r = w.patch("/v1/drivers/"+id, ta, patch)
	expect(t, r, http.StatusOK, "")
	if r.data()["status"] != "inactive" || r.data()["assignToProject"] != "SPX North" {
		t.Fatalf("patched %s", r.raw)
	}
	h := r.data()["statusHistory"].([]any)[0].(map[string]any)
	if h["status"] != "inactive" || h["previousStatus"] != "active" || h["reason"] != "on leave" || h["changedBy"] != taID {
		t.Errorf("status entry %v", h)
	}
	if v := w.opt(`SELECT payload->>'changed' FROM outbox_events WHERE routing_key = 'driver.updated' AND aggregate_id = $1`, id); v != `["status", "assignToProject"]` {
		t.Errorf("driver.updated changed %s", v)
	}
	expect(t, w.patch("/v1/drivers/"+id, ta, patch), http.StatusOK, "")
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'driver.updated' AND aggregate_id = $1`, id); n != 1 {
		t.Errorf("an unchanged PATCH emitted: %d driver.updated", n)
	}
	if n := w.count(`SELECT count(*) FROM status_history WHERE entity_type = 'driver' AND entity_id = $1`, id); n != 2 {
		t.Errorf("status entries %d", n)
	}
	// The set of customer driver ids and the files are replaced or cleared.
	r = w.patch("/v1/drivers/"+id, ta, map[string]any{"customerDriverIds": nil, "profileKey": nil})
	expect(t, r, http.StatusOK, "")
	if len(r.data()["customerDriverIds"].([]any)) != 0 || r.data()["profileKey"] != nil {
		t.Errorf("cleared %s", r.raw)
	}
	// A legacy row that breaks the form rules stays editable while its values come back unchanged.
	legacy := w.driver(w.O, "Legacy", "id_card", "'1234567890123'", "truck_license_id", "'123'", "birth_date", "'1950-01-01'")
	expect(t, w.patch("/v1/drivers/"+legacy, ta, map[string]any{"idCard": "1234567890123", "truckLicenseId": "123",
		"birthDate": "1950-01-01", "mobile": "0899999999"}), http.StatusOK, "")
	r = w.patch("/v1/drivers/"+legacy, ta, map[string]any{"idCard": "1234567890124"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.patch("/v1/drivers/"+legacy, ta, map[string]any{"fullNameTh": ""}), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.patch("/v1/drivers/019a0000-0000-7000-8000-0000000000ff", ta, map[string]any{"mobile": "1"}), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/drivers/not-a-uuid", ta), http.StatusNotFound, "not_found")
}

// AC: PII fields are absent without drivers:view_pii — in the detail, the list and every write's answer — and the
// documents are read only with it: GET /v1/drivers/{id}/documents/{kind} answers a 5-minute presigned GET, and
// GET /v1/files refuses the ID card to staff without the capability. Writing a PII field needs it too.
func TestDriverPIIGated(t *testing.T) {
	w := newWorld(t)
	w.override(w.O, "operator", "drivers:edit", true)
	ta, mg, op := w.login("ta.o"), w.login("mg.o"), w.login("op.o")
	tmp := "019a0000-0000-7000-8000-000000000002"
	idCard := w.upload(ta, "driver_id_card", tmp, "application/pdf", pdf)
	license := w.upload(ta, "driver_license", tmp, "application/pdf", pdf)
	profile := w.upload(ta, "driver_profile", tmp, "image/png", png)
	r := w.post("/v1/drivers", ta, newDriver("Pii", map[string]any{"idCard": validIDCard, "idCardExpiredDate": "2030-01-01",
		"birthDate": "1985-02-02", "idCardKey": idCard, "licenseKey": license, "profileKey": profile}))
	expect(t, r, http.StatusCreated, "")
	id := str(r.data()["id"])

	r = w.get("/v1/drivers/"+id, op)
	expect(t, r, http.StatusOK, "")
	for _, k := range piiKeys {
		if _, ok := r.data()[k]; ok {
			t.Errorf("operator without drivers:view_pii reads %s: %s", k, r.raw)
		}
	}
	if str(r.data()["profileKey"]) != profile || r.data()["firstName"] != "Pii" {
		t.Errorf("the non-PII fields stay: %s", r.raw)
	}
	r = w.get("/v1/drivers", op)
	expect(t, r, http.StatusOK, "")
	for _, it := range r.list() {
		for _, k := range piiKeys {
			if _, ok := it[k]; ok {
				t.Errorf("the list carries %s", k)
			}
		}
	}
	r = w.get("/v1/drivers?q=Pii", mg)
	for _, it := range r.list() {
		for _, k := range piiKeys {
			if _, ok := it[k]; ok {
				t.Errorf("the list carries %s even for drivers:view_pii", k)
			}
		}
	}
	r = w.get("/v1/drivers/"+id+"/documents/id_card", op)
	expect(t, r, http.StatusForbidden, "permission_denied")
	if missing(r) != "drivers:view_pii" {
		t.Errorf("missing capability %v", missing(r))
	}
	expect(t, w.get("/v1/files?key="+url.QueryEscape(idCard), op), http.StatusNotFound, "not_found")

	// With the capability: the detail, the documents (5 minutes, never cached) and the files route.
	r = w.get("/v1/drivers/"+id, mg)
	expect(t, r, http.StatusOK, "")
	if r.data()["idCard"] != validIDCard || r.data()["birthDate"] != "1985-02-02" || r.data()["idCardExpiredDate"] != "2030-01-01" ||
		str(r.data()["licenseKey"]) != license || r.header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("manager with drivers:view_pii: %s", r.raw)
	}
	for _, kind := range []string{"id_card", "license", "profile"} {
		r = w.get("/v1/drivers/"+id+"/documents/"+kind, mg)
		expect(t, r, http.StatusFound, "")
		loc := r.header.Get("Location")
		if r.header.Get("Cache-Control") != "private, no-store" || loc == "" {
			t.Fatalf("%s: %v", kind, r.header)
		}
		u, err := url.Parse(loc)
		if err != nil {
			t.Fatal(err)
		}
		exp, _ := strconv.ParseInt(u.Query().Get(storage.QueryExpires), 10, 64)
		if left := time.Until(time.Unix(exp, 0)); left > 5*time.Minute+2*time.Second || left < 4*time.Minute {
			t.Errorf("%s URL lives %s, want 5 minutes", kind, left)
		}
		status, body := w.fetch(loc)
		if status != http.StatusOK || (kind != "profile" && string(body) != string(pdf)) {
			t.Errorf("%s: %d", kind, status)
		}
	}
	expect(t, w.get("/v1/drivers/"+id+"/documents/passport", mg), http.StatusNotFound, "not_found")
	bare := w.driver(w.O, "Bare")
	expect(t, w.get("/v1/drivers/"+bare+"/documents/license", mg), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/files?key="+url.QueryEscape(idCard), mg), http.StatusFound, "")

	// Writing PII needs the capability that reads it (the operator holds drivers:edit by override).
	r = w.patch("/v1/drivers/"+id, op, map[string]any{"birthDate": "1985-02-03"})
	expect(t, r, http.StatusForbidden, "permission_denied")
	if missing(r) != "drivers:view_pii" {
		t.Errorf("missing capability %v", missing(r))
	}
	r = w.patch("/v1/drivers/"+id, op, map[string]any{"mobile": "0811111111"})
	expect(t, r, http.StatusOK, "")
	for _, k := range piiKeys {
		if _, ok := r.data()[k]; ok {
			t.Errorf("a write's answer carries %s for the operator", k)
		}
	}
	if r.header.Get("Cache-Control") == "private, no-store" {
		t.Errorf("no PII, ordinary caching headers: %q", r.header.Get("Cache-Control"))
	}
}

// AC: an unauthenticated (or unauthorised) driver-account call is impossible. Every driver and truck-assignment route
// needs a bearer (401) and is absent from the public listener (404); the drivers routes never create, link or
// password an account (the body is refused, nothing is written); accounts come only from the users administration
// (users:manage), and a driver's temporary password needs drivers:set_password too, is generated (R29), shown once,
// and recorded as user_password_temporary_issued in its transaction (closes triggers.ts:48-131, 142-145).
func TestDriverAccountsOnlyThroughUsers(t *testing.T) {
	w := newWorld(t)
	w.override(w.O, "manager", "users:manage", true)
	drv := w.driver(w.O, "Acct")
	asg := "019a0000-0000-7000-8000-0000000000aa"
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/drivers"}, {http.MethodPost, "/v1/drivers"}, {http.MethodGet, "/v1/drivers/" + drv},
		{http.MethodPatch, "/v1/drivers/" + drv}, {http.MethodGet, "/v1/drivers/" + drv + "/documents/id_card"},
		{http.MethodGet, "/v1/truck-assignments"}, {http.MethodPost, "/v1/truck-assignments"},
		{http.MethodPost, "/v1/truck-assignments/" + asg + "/revoke"},
	} {
		var body any
		if c.method != http.MethodGet {
			body = map[string]any{"firstName": "Anon", "password": "chosen-by-caller"}
		}
		expect(t, w.do(c.method, c.path, "", body), http.StatusUnauthorized, "unauthenticated")
		expect(t, w.send(w.public, c.method, c.path, "", body), http.StatusNotFound, "not_found")
	}

	ta, mg, us := w.login("ta.o"), w.login("mg.o"), w.login("us.o")
	users, drivers := w.count(`SELECT count(*) FROM users`), w.count(`SELECT count(*) FROM drivers`)
	for field, reason := range map[string]string{"login": "not_allowed", "password": "not_allowed",
		"loginPassword": "not_allowed", "userId": "not_editable", "authId": "not_editable"} {
		var v any = "x"
		if field == "login" {
			v = map[string]any{"email": "new.driver@example.test"}
		}
		r := w.post("/v1/drivers", ta, newDriver("Refused", map[string]any{field: v}))
		expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
		if r.fields()[field] != reason {
			t.Errorf("%s: %s", field, r.raw)
		}
		expect(t, w.patch("/v1/drivers/"+drv, ta, map[string]any{field: v}), http.StatusUnprocessableEntity, "invalid_argument")
	}
	if w.count(`SELECT count(*) FROM users`) != users || w.count(`SELECT count(*) FROM drivers`) != drivers {
		t.Fatal("a refused driver request created a user or a driver")
	}
	r := w.post("/v1/drivers", us, newDriver("NoCap", nil))
	expect(t, r, http.StatusForbidden, "permission_denied")
	if missing(r) != "drivers:create" {
		t.Errorf("%s", r.raw)
	}
	// Another tenant's staff never reach the driver (RLS and the named reach).
	tc := w.login("ta.c")
	expect(t, w.get("/v1/drivers/"+drv, tc), http.StatusNotFound, "not_found")
	expect(t, w.patch("/v1/drivers/"+drv, tc, map[string]any{"mobile": "1"}), http.StatusNotFound, "not_found")
	if v := w.id(`SELECT mobile FROM drivers WHERE id = $1`, drv); v != "0812345678" {
		t.Errorf("mobile %s after another tenant's PATCH", v)
	}

	// The account comes from the users administration (POST /v1/users with driverId: user, membership, link, one tx).
	r = w.post("/v1/users", ta, map[string]any{"email": "acct@example.test", "displayName": "Acct Dee", "role": "driver",
		"driverId": drv})
	expect(t, r, http.StatusCreated, "")
	uid := str(r.data()["user"].(map[string]any)["id"])
	if str(r.data()["temporaryPassword"]) == "" {
		t.Fatalf("no temporary password: %s", r.raw)
	}
	r = w.get("/v1/drivers/"+drv, ta)
	expect(t, r, http.StatusOK, "")
	login, _ := r.data()["login"].(map[string]any)
	if r.data()["userId"] != uid || login == nil || login["userId"] != uid || login["mustChangePassword"] != true {
		t.Errorf("driver login %s", r.raw)
	}
	// drivers.user_id is unique: a second account cannot take the linked driver.
	r = w.post("/v1/users", ta, map[string]any{"email": "acct2@example.test", "displayName": "Acct Two", "role": "driver",
		"driverId": drv})
	expect(t, r, http.StatusConflict, "already_exists")

	// The temporary password of a driver: users:manage alone is not enough.
	events := func() int {
		return w.count(`SELECT count(*) FROM security_events WHERE event_type = 'user_password_temporary_issued' AND target_user_id = $1`, uid)
	}
	r = w.post("/v1/users/"+uid+"/password/temporary", mg, map[string]any{})
	expect(t, r, http.StatusForbidden, "permission_denied")
	if missing(r) != "drivers:set_password" || events() != 0 {
		t.Errorf("manager without drivers:set_password: %s", r.raw)
	}
	r = w.post("/v1/users/"+uid+"/password/temporary", ta, map[string]any{"password": "chosen-by-caller"})
	expect(t, r, http.StatusOK, "")
	temp := str(r.data()["temporaryPassword"])
	if temp == "" || temp == "chosen-by-caller" || r.header.Get("Cache-Control") != "no-store" || events() != 1 {
		t.Fatalf("temporary password %s (%v), %d events", r.raw, r.header, events())
	}
	// It signs in only to change it (R79).
	r = w.post("/v1/auth/login", "", map[string]any{"email": "acct@example.test", "password": temp, "platform": "android"})
	expect(t, r, http.StatusForbidden, auth.CodePasswordChangeRequired)
}

// The temporary password of a driver is mirrored to its Firebase account in the same transaction while the bridge
// mirrors (Appendix C §C.6.4): a mirror failure is 503 bridge_unavailable with nothing committed in PostgreSQL and no
// security event. Nothing reaches Google: the Identity Toolkit is the in-process fake.
func TestTemporaryPasswordMirrorsFirebase(t *testing.T) {
	b := firebasetest.New(t, "logitrack-drivers-test")
	w := newWorldWith(t, auth.Firebase{Mode: auth.BridgeWeb, Verifier: b.Verifier(time.Now), Signer: b.ServiceAccount(),
		Accounts: b.Accounts()})
	const fbUID = "fbDriver0000000000000000001"
	uid := w.user("drv.fb@example.test")
	w.exec(`UPDATE users SET legacy_auth_uid = $1 WHERE id = $2`, fbUID, uid)
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'driver')`, uid, w.O)
	drv := w.driver(w.O, "Firebase", "user_id", "'"+uid+"'")
	b.Put(firebasetest.Account{UID: fbUID, Email: "drv.fb@example.test", Password: "old-legacy-password"})
	ta := w.login("ta.o")
	hash := w.id(`SELECT password_hash FROM users WHERE id = $1`, uid)

	r := w.post("/v1/users/"+uid+"/password/temporary", ta, map[string]any{})
	expect(t, r, http.StatusOK, "")
	acc, ok := b.Get(fbUID)
	if !ok || acc.Password != str(r.data()["temporaryPassword"]) || acc.ValidSince == 0 {
		t.Fatalf("Firebase account after the temporary password: %+v", acc)
	}
	if w.id(`SELECT password_hash FROM users WHERE id = $1`, uid) == hash {
		t.Error("the PostgreSQL hash did not change")
	}

	b.SetToolkitError(http.StatusInternalServerError, "INTERNAL")
	hash = w.id(`SELECT password_hash FROM users WHERE id = $1`, uid)
	before := w.count(`SELECT count(*) FROM security_events WHERE event_type = 'user_password_temporary_issued'`)
	expect(t, w.post("/v1/users/"+uid+"/password/temporary", ta, map[string]any{}), http.StatusServiceUnavailable, "bridge_unavailable")
	if w.id(`SELECT password_hash FROM users WHERE id = $1`, uid) != hash ||
		w.count(`SELECT count(*) FROM security_events WHERE event_type = 'user_password_temporary_issued'`) != before {
		t.Error("a failed mirror committed the password or its event")
	}
	b.SetToolkitError(0, "")
	_ = drv
}

// GET /v1/drivers: keyset pages without the legacy cap of 100, newest first, meta.statusCounts over every row
// matching the other filters; q, status, tenantId, fields=minimal; the caller's tenant reach only.
func TestDriversListPagingAndCounts(t *testing.T) {
	w := newWorld(t)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := range 130 {
		status := "active"
		switch {
		case i%26 == 0:
			status = "inactive"
		case i%43 == 0:
			status = "on_duty"
		}
		w.driver(w.O, fmt.Sprintf("Driver%03d", i), "created_at", fmt.Sprintf("'%s'", base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339)),
			"status", "'"+status+"'", "truck_license_id", fmt.Sprintf("'L%07d'", i))
	}
	w.driver(w.A, "CarrierA")
	w.driver(w.C, "CarrierC")
	ta := w.login("ta.o")
	var ids []string
	cursor := ""
	pages := 0
	var counts map[string]any
	for {
		path := "/v1/drivers?limit=50"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := w.get(path, ta)
		expect(t, r, http.StatusOK, "")
		pages++
		c := r.meta()["statusCounts"].(map[string]any)
		if counts == nil {
			counts = c
		} else if fmt.Sprint(c) != fmt.Sprint(counts) {
			t.Errorf("statusCounts changed between pages: %v %v", counts, c)
		}
		var prev string
		for _, it := range r.list() {
			ids = append(ids, str(it["id"]))
			if prev != "" && str(it["createdAt"]) > prev {
				t.Fatalf("not newest first")
			}
			prev = str(it["createdAt"])
		}
		if cursor = r.next(); cursor == "" {
			break
		}
	}
	if len(ids) != 131 || pages != 3 { // 130 of O + 1 of A (contractor reach), not C's
		t.Fatalf("%d drivers in %d pages", len(ids), pages)
	}
	slices.Sort(ids)
	if len(slices.Compact(ids)) != 131 {
		t.Error("a driver appears twice")
	}
	if counts["inactive"] != 5.0 || counts["on_duty"] != 3.0 || counts["active"] != 123.0 {
		t.Errorf("statusCounts %v", counts)
	}
	r := w.get("/v1/drivers?status=inactive", ta)
	expect(t, r, http.StatusOK, "")
	if len(r.list()) != 5 || r.meta()["statusCounts"].(map[string]any)["active"] != 123.0 {
		t.Errorf("status filter: %d items, %v", len(r.list()), r.meta())
	}
	r = w.get("/v1/drivers?q=L0000042", ta)
	if len(r.list()) != 1 || r.list()[0]["firstName"] != "Driver042" {
		t.Errorf("q by licence: %s", r.raw)
	}
	r = w.get("/v1/drivers?tenantId="+w.A, ta)
	if len(r.list()) != 1 || r.list()[0]["tenantNameTh"] != "A" {
		t.Errorf("tenantId filter: %s", r.raw)
	}
	r = w.get("/v1/drivers?fields=minimal&q=Driver12&limit=20", ta)
	expect(t, r, http.StatusOK, "")
	if len(r.list()) != 10 {
		t.Errorf("minimal q: %d", len(r.list()))
	}
	for _, it := range r.list() {
		if len(it) != 2 || !strings.HasPrefix(str(it["displayName"]), "Driver12") {
			t.Errorf("minimal item %v", it)
		}
	}
	r = w.get("/v1/drivers", w.login("ta.c"))
	if len(r.list()) != 1 || r.list()[0]["firstName"] != "CarrierC" {
		t.Errorf("carrier C reads %s", r.raw)
	}
	for _, q := range []string{"status=busy", "fields=full", "cursor=bad", "limit=0", "limit=501"} {
		expect(t, w.get("/v1/drivers?"+q, ta), http.StatusUnprocessableEntity, "invalid_argument")
	}
	expect(t, w.get("/v1/drivers?sort=name", ta), http.StatusBadRequest, "bad_request")
}

// A dispatcher (dispatch:view_operations, no drivers:view) reads only the scope_drivers projection of drivers with
// recent in-scope work: no PII, no e-mail, no account (Appendix C §C.3.7); it never opens the full driver.
func TestDispatcherReadsScopeDrivers(t *testing.T) {
	w := newWorld(t)
	in := w.driver(w.A, "InScope", "id_card", "'"+validIDCard+"'", "email", "'in@example.test'")
	w.driver(w.A, "OutOfScope")
	w.exec(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw,
		billing_party_id, driver_id) VALUES ($1, 'form', 'first_mile', 'pending', now(), 'SRC', 'DST', $2, $3)`, w.A, w.partyX, in)
	ds := w.login("ds")
	r := w.get("/v1/drivers", ds)
	expect(t, r, http.StatusOK, "")
	if len(r.list()) != 1 || r.list()[0]["id"] != in {
		t.Fatalf("dispatcher reads %s", r.raw)
	}
	for _, k := range append(piiKeys, "email", "userId", "truckLicenseId", "employmentType") {
		if _, ok := r.list()[0][k]; ok {
			t.Errorf("the projection carries %s", k)
		}
	}
	if r.list()[0]["displayName"] != "InScope ดี" || r.meta()["statusCounts"].(map[string]any)["active"] != 1.0 {
		t.Errorf("projection %s", r.raw)
	}
	r = w.get("/v1/drivers?fields=minimal", ds)
	if len(r.list()) != 1 || len(r.list()[0]) != 2 {
		t.Errorf("minimal %s", r.raw)
	}
	// Its operator role in its own organisation D holds drivers:view, but the detail names the reach (D): a driver of
	// another tenant is never opened to the full row through the scope branch.
	expect(t, w.get("/v1/drivers/"+in, ds), http.StatusNotFound, "not_found")
}

// The broker-carrier move (Appendix C §C.1.4): only a steward or a platform admin moves a driver, alone in its
// request, into a tenant of its reach, while the driver holds no truck; the same transaction moves the driver row,
// its files and the linked user's driver membership, and writes driver_tenant_moved and driver.updated.
func TestDriverTenantMove(t *testing.T) {
	w := newWorld(t)
	taA := w.login("ta.a")
	profile := w.upload(taA, "driver_profile", "019a0000-0000-7000-8000-000000000003", "image/png", png)
	r := w.post("/v1/drivers", taA, newDriver("Mover", map[string]any{"profileKey": profile}))
	expect(t, r, http.StatusCreated, "")
	drv := str(r.data()["id"])
	uid := w.user("mover@example.test")
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'driver')`, uid, w.A)
	w.exec(`UPDATE drivers SET user_id = $1 WHERE id = $2`, uid, drv)
	ver := w.count(`SELECT auth_version FROM users WHERE id = $1`, uid)
	taO := w.login("ta.o")

	r = w.patch("/v1/drivers/"+drv, taA, map[string]any{"tenantId": w.B})
	expect(t, r, http.StatusForbidden, "permission_denied")
	if r.detail("reason") != "steward_only" {
		t.Errorf("carrier admin: %s", r.raw)
	}
	r = w.patch("/v1/drivers/"+drv, taO, map[string]any{"tenantId": w.B, "mobile": "0800000000"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["tenantId"] != "exclusive" {
		t.Errorf("%s", r.raw)
	}
	r = w.patch("/v1/drivers/"+drv, taO, map[string]any{"tenantId": w.C})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["tenantId"] != "not_in_reach" {
		t.Errorf("%s", r.raw)
	}
	// Sending the driver's own tenant back is no move: the rest of the PATCH applies.
	expect(t, w.patch("/v1/drivers/"+drv, taO, map[string]any{"tenantId": w.A, "mobile": "0800000001"}), http.StatusOK, "")
	// Not while it holds a truck.
	tr := w.truck(w.A, "กข-1001")
	r = w.post("/v1/truck-assignments", taO, map[string]any{"truckId": tr, "driverId": drv})
	expect(t, r, http.StatusCreated, "")
	asg := str(r.data()["id"])
	r = w.patch("/v1/drivers/"+drv, taO, map[string]any{"tenantId": w.B})
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "driver_holds_truck" {
		t.Errorf("%s", r.raw)
	}
	expect(t, w.post("/v1/truck-assignments/"+asg+"/revoke", taO, nil), http.StatusOK, "")

	r = w.patch("/v1/drivers/"+drv, taO, map[string]any{"tenantId": w.B})
	expect(t, r, http.StatusOK, "")
	if r.data()["tenantId"] != w.B || r.data()["tenantNameTh"] != "B" {
		t.Fatalf("moved driver %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM memberships WHERE user_id = $1 AND tenant_id = $2 AND role = 'driver'`, uid, w.B); n != 1 {
		t.Error("no driver membership in the new tenant")
	}
	if n := w.count(`SELECT count(*) FROM memberships WHERE user_id = $1 AND tenant_id = $2`, uid, w.A); n != 0 {
		t.Error("the former driver membership stayed")
	}
	if n := w.count(`SELECT count(*) FROM file_objects WHERE owner_kind = 'driver' AND owner_id = $1 AND tenant_id = $2`, drv, w.B); n != 1 {
		t.Errorf("files in the new tenant: %d", n)
	}
	if w.count(`SELECT auth_version FROM users WHERE id = $1`, uid) <= ver {
		t.Error("the user's claims did not change")
	}
	if n := w.count(`SELECT count(*) FROM security_events WHERE event_type = 'driver_tenant_moved' AND tenant_id = $1::uuid
		AND details->>'from' = $2::text AND details->>'to' = $1::text AND target_user_id = $3::uuid`, w.B, w.A, uid); n != 1 {
		t.Errorf("driver_tenant_moved %d", n)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'driver.updated' AND aggregate_id = $1
		AND realtime_topics @> ARRAY['tenant:' || $2 || ':fleet', 'tenant:' || $3 || ':fleet']`, drv, w.A, w.B); n != 1 {
		t.Errorf("driver.updated on both fleets: %d", n)
	}
	expect(t, w.get("/v1/drivers/"+drv, taA), http.StatusNotFound, "not_found")
	// The same tenant again is no move and writes nothing.
	expect(t, w.patch("/v1/drivers/"+drv, taO, map[string]any{"tenantId": w.B}), http.StatusOK, "")
	if n := w.count(`SELECT count(*) FROM security_events WHERE event_type = 'driver_tenant_moved'`); n != 1 {
		t.Errorf("events after a no-op move: %d", n)
	}
	// A platform admin acting as B moves it out of B's reach: 204.
	pa := w.login("pa")
	r = w.patch("/v1/drivers/"+drv, pa, map[string]any{"tenantId": w.C}, "X-Act-On-Tenant", w.B)
	expect(t, r, http.StatusNoContent, "")
	if v := w.id(`SELECT tenant_id::text FROM drivers WHERE id = $1`, drv); v != w.C {
		t.Errorf("tenant %s after the platform move", v)
	}
}
