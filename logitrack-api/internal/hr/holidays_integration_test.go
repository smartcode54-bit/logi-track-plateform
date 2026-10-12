//go:build integration

// Integration tests of the holidays of T45 (issue #65): the calendar read (public + the tenant's own, ?year, ETag),
// the upsert, the partial update, the delete and the generator's save, with the steward rule of the public calendar
// (R60) and the holidays.changed events the projection and SSE consume.
package hr_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// holidayEvents are the holidays.changed events appended so far, oldest first.
func (w *world) holidayEvents() []map[string]any {
	w.t.Helper()
	rows, err := w.etl.Query(context.Background(), `SELECT payload::text, realtime_topics::text, coalesce(tenant_id::text, '')
		FROM outbox_events WHERE routing_key = 'holidays.changed' ORDER BY id`)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var payload, topics, tenant string
		if err := rows.Scan(&payload, &topics, &tenant); err != nil {
			w.t.Fatal(err)
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			w.t.Fatal(err)
		}
		m["_topics"], m["_tenant"] = topics, tenant
		out = append(out, m)
	}
	return out
}

func strs(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, str(e))
	}
	return out
}

func (w *world) patch(path, bearer string, body any) resp {
	w.t.Helper()
	return w.do(http.MethodPatch, path, bearer, body)
}

func (w *world) put(path, bearer string, body any) resp {
	w.t.Helper()
	return w.do(http.MethodPut, path, bearer, body)
}

func (w *world) del(path, bearer string) resp {
	w.t.Helper()
	return w.do(http.MethodDelete, path, bearer, nil)
}

// AC: public holidays (tenant_id NULL) are writable only by stewards (R60); a tenant's own holidays by its staff.
func TestHolidayWrites(t *testing.T) {
	w := newWorld(t)
	ta, taA, taC := w.login("ta.o"), w.login("ta.a"), w.login("ta.c")

	// A steward (own-fleet staff) creates a public holiday: 201, tenant null, draft by default, the name from nameTh.
	r := w.post("/v1/holidays", ta, map[string]any{"date": "2026-10-23", "type": "PUBLIC", "nameTh": "วันปิยมหาราช",
		"holidayNameEN": "Chulalongkorn Day", "isRecurring": true})
	expect(t, r, http.StatusCreated, "")
	pub := r.data()
	if pub["tenantId"] != nil || pub["type"] != "public" || pub["status"] != "draft" || pub["name"] != "วันปิยมหาราช" ||
		pub["nameEn"] != "Chulalongkorn Day" || pub["date"] != "2026-10-23" || pub["isRecurring"] != true || pub["createdBy"] == nil {
		t.Fatalf("public holiday %v", pub)
	}
	ev := w.holidayEvents()
	if len(ev) != 1 || ev[0]["_topics"] != "{global}" || ev[0]["_tenant"] != "" || num(ev[0]["year"]) != 2026 ||
		strings.Join(strs(ev[0]["ids"]), ",") != str(pub["id"]) {
		t.Fatalf("event %v", ev)
	}
	// The same day and type again: the upsert key (holidays.ts:98-113) updates it with the fields named (200).
	r = w.post("/v1/holidays", ta, map[string]any{"date": "2026-10-23T00:00:00+07:00", "status": "published", "description": "ราชการ"})
	expect(t, r, http.StatusOK, "")
	if r.data()["id"] != pub["id"] || r.data()["status"] != "published" || r.data()["name"] != "วันปิยมหาราช" || r.data()["description"] != "ราชการ" {
		t.Fatalf("upsert %v", r.data())
	}
	// A carrier's admin is no steward: public rows are refused (403), its own company holiday lands in its tenant.
	for _, rr := range []resp{
		w.post("/v1/holidays", taA, map[string]any{"date": "2026-12-05", "name": "x"}),
		w.patch("/v1/holidays/"+str(pub["id"]), taA, map[string]any{"name": "x"}),
		w.del("/v1/holidays/"+str(pub["id"]), taA),
		w.put("/v1/holidays/generate", taA, map[string]any{"year": 2026, "finalHolidays": []any{map[string]any{"date": "2026-01-01", "name": "ปีใหม่"}},
			"initialHolidays": []any{}}),
	} {
		expect(t, rr, http.StatusForbidden, "permission_denied")
	}
	r = w.post("/v1/holidays", taA, map[string]any{"date": "2026-11-02", "type": "company", "name": "Outing A"})
	expect(t, r, http.StatusCreated, "")
	compA := r.data()
	if compA["tenantId"] != w.A || compA["type"] != "company" {
		t.Fatalf("carrier holiday %v", compA)
	}
	if ev := w.holidayEvents(); ev[len(ev)-1]["_topics"] != "{tenant:"+w.A+":config}" || ev[len(ev)-1]["tenantId"] != w.A {
		t.Errorf("tenant event %v", ev[len(ev)-1])
	}
	r = w.post("/v1/holidays", ta, map[string]any{"date": "2026-11-02", "type": "company", "name": "Outing O"})
	expect(t, r, http.StatusCreated, "")
	compO := r.data()
	if compO["tenantId"] != w.O {
		t.Fatalf("own fleet holiday %v", compO)
	}

	// Reads: the public calendar and the tenant's own rows (not its carriers', not other tenants').
	names := func(r resp) []string {
		var out []string
		for _, h := range r.list() {
			out = append(out, str(h["name"]))
		}
		return out
	}
	if r := w.get("/v1/holidays", ta); strings.Join(names(r), ",") != "วันปิยมหาราช,Outing O" {
		t.Errorf("own fleet calendar %v", names(r))
	}
	if r := w.get("/v1/holidays?year=2026", taA); strings.Join(names(r), ",") != "วันปิยมหาราช,Outing A" {
		t.Errorf("carrier calendar %v", names(r))
	}
	if r := w.get("/v1/holidays", taC); strings.Join(names(r), ",") != "วันปิยมหาราช" {
		t.Errorf("other carrier calendar %v", names(r))
	}
	// Every authenticated principal reads the calendar (the manager has no HR key).
	if r := w.get("/v1/holidays?year=2027", w.login("mg.o")); r.status != http.StatusOK || len(r.list()) != 0 {
		t.Errorf("2027 %s", r.raw)
	}
	expect(t, w.get("/v1/holidays?year=26", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/holidays", ""), http.StatusUnauthorized, "unauthenticated")
	// ETag: a matching If-None-Match (weak or strong) is 304; a change gives a new tag.
	etag := etagOf(t, w, "/v1/holidays?year=2026", ta)
	if code := statusWithETag(t, w, "/v1/holidays?year=2026", ta, etag); etag == "" || code != http.StatusNotModified {
		t.Errorf("If-None-Match %q: %d", etag, code)
	}

	// PATCH: partial; null clears an optional text; company <-> other moves, public <-> tenant does not.
	r = w.patch("/v1/holidays/"+str(compO["id"]), ta, map[string]any{"type": "OTHER", "description": "ทั้งบริษัท"})
	expect(t, r, http.StatusOK, "")
	if r.data()["type"] != "other" || r.data()["description"] != "ทั้งบริษัท" || r.data()["name"] != "Outing O" {
		t.Fatalf("patch %v", r.data())
	}
	if r = w.patch("/v1/holidays/"+str(compO["id"]), ta, map[string]any{"description": nil}); r.data()["description"] != nil {
		t.Errorf("cleared %v", r.data())
	}
	r = w.patch("/v1/holidays/"+str(compO["id"]), ta, map[string]any{"type": "public"})
	if expect(t, r, http.StatusUnprocessableEntity, "invalid_argument"); r.fields()["type"] != "scope_change" {
		t.Errorf("scope change %s", r.raw)
	}
	if etag2 := etagOf(t, w, "/v1/holidays?year=2026", ta); etag2 == etag {
		t.Error("the tag did not change")
	}
	// The own fleet's staff reach the carrier's own holiday (contractor reach); the carrier never the own fleet's.
	expect(t, w.patch("/v1/holidays/"+str(compA["id"]), ta, map[string]any{"name": "Outing A2"}), http.StatusOK, "")
	expect(t, w.patch("/v1/holidays/"+str(compO["id"]), taA, map[string]any{"name": "x"}), http.StatusNotFound, "not_found")
	expect(t, w.patch("/v1/holidays/"+str(compA["id"]), taC, map[string]any{"name": "x"}), http.StatusNotFound, "not_found")
	// Moving onto another holiday's day and type: the UNIQUE key answers 409.
	r = w.post("/v1/holidays", ta, map[string]any{"date": "2026-11-03", "type": "other", "name": "Other day"})
	expect(t, r, http.StatusCreated, "")
	expect(t, w.patch("/v1/holidays/"+str(r.data()["id"]), ta, map[string]any{"date": "2026-11-02"}), http.StatusConflict, "already_exists")

	// Validation.
	for _, tc := range []struct {
		body  map[string]any
		field string
	}{
		{map[string]any{"name": "x"}, "date"},
		{map[string]any{"date": "2026-02-30", "name": "x"}, "date"},
		{map[string]any{"date": "2026-01-01"}, "name"},
		{map[string]any{"date": "2026-01-01", "name": "x", "type": "national"}, "type"},
		{map[string]any{"date": "2026-01-01", "name": "x", "status": "live"}, "status"},
		{map[string]any{"date": "2026-01-01", "name": "x", "color": "red"}, "color"},
		{map[string]any{"date": "2026-01-01", "name": "x", "nameTh": "a", "holidayNameTH": "b"}, "nameTh"},
	} {
		r := w.post("/v1/holidays", ta, tc.body)
		if expect(t, r, http.StatusUnprocessableEntity, "invalid_argument"); r.fields()[tc.field] == "" {
			t.Errorf("%v: %s", tc.body, r.raw)
		}
	}
	// Read-only fields a client sends back are ignored.
	expect(t, w.post("/v1/holidays", ta, map[string]any{"date": "2026-12-31", "name": "สิ้นปี", "id": "x", "createdAt": "y"}), http.StatusCreated, "")
	expect(t, w.post("/v1/holidays", w.login("mg.o"), map[string]any{"date": "2026-12-30", "name": "x"}), http.StatusForbidden, "permission_denied")

	// DELETE: 204; the event names the deleted document (legacy_doc_id, else the id).
	w.exec(`UPDATE holidays SET legacy_doc_id = 'ho_legacy' WHERE id = $1`, pub["id"])
	expect(t, w.del("/v1/holidays/"+str(pub["id"]), ta), http.StatusNoContent, "")
	expect(t, w.del("/v1/holidays/"+str(compA["id"]), taA), http.StatusNoContent, "")
	ev = w.holidayEvents()
	last2 := ev[len(ev)-2:]
	if strings.Join(strs(last2[0]["deletedDocIds"]), ",") != "ho_legacy" || last2[0]["_topics"] != "{global}" ||
		strings.Join(strs(last2[1]["deletedDocIds"]), ",") != str(compA["id"]) || last2[1]["_topics"] != "{tenant:"+w.A+":config}" {
		t.Errorf("delete events %v", last2)
	}
	expect(t, w.del("/v1/holidays/"+str(pub["id"]), ta), http.StatusNotFound, "not_found")
	if n := w.count(`SELECT count(*) FROM holidays WHERE id = $1`, pub["id"]); n != 0 {
		t.Error("not deleted")
	}
}

func etagOf(t *testing.T, w *world, path, bearer string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, w.internal+path, nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.Header.Get("ETag")
}

func statusWithETag(t *testing.T, w *world, path, bearer, etag string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, w.internal+path, nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("If-None-Match", `W/`+etag)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

// PUT /v1/holidays/generate (saveGeneratedHolidays): the reviewed list of a year saved as published, the dropped ones
// deleted, one holiday per day and type.
func TestGenerateHolidays(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o")
	// The calendar before the review: New Year (kept, will be updated), a day the review drops, and 2025's New Year
	// (another year: untouched).
	w.exec(`INSERT INTO holidays (holiday_date, holiday_type, status, name, description) VALUES
		('2026-01-01', 'public', 'draft', 'วันขึ้นปีใหม่', 'เดิม'), ('2026-04-06', 'public', 'draft', 'วันจักรี', NULL),
		('2025-01-01', 'public', 'published', 'วันขึ้นปีใหม่ 2025', NULL)`)
	gen := func(date, en, th string) map[string]any {
		return map[string]any{"name": en + " (" + th + ")", "holidayNameEN": en, "holidayNameTH": th, "date": date,
			"type": "PUBLIC", "status": "DRAFT", "isRecurring": true}
	}
	final := []any{
		gen("2026-01-01", "New Year's Day", "วันขึ้นปีใหม่"),
		gen("2026-04-13", "Songkran Festival", "วันสงกรานต์"),
		gen("2026-04-14", "Songkran Festival", "วันสงกรานต์"),
		gen("2026-04-15", "Songkran Festival", "วันสงกรานต์"),
		// A substitution day on a Songkran day: one holiday per day and type, the first one wins.
		gen("2026-04-15", "Substitution for Chakri Day", "วันหยุดชดเชยวันจักรี"),
	}
	initial := []any{gen("2026-01-01", "New Year's Day", "วันขึ้นปีใหม่"), gen("2026-04-06", "Chakri Day", "วันจักรี"),
		gen("2026-04-13", "Songkran Festival", "วันสงกรานต์")}
	r := w.put("/v1/holidays/generate", ta, map[string]any{"year": 2026, "finalHolidays": final, "initialHolidays": initial})
	expect(t, r, http.StatusOK, "")
	if num(r.data()["saved"]) != 4 || num(r.data()["deleted"]) != 1 || num(r.data()["duplicates"]) != 1 {
		t.Fatalf("result %v", r.data())
	}
	got := w.get("/v1/holidays?year=2026", ta).list()
	var days []string
	for _, h := range got {
		if h["status"] != "published" {
			t.Errorf("not published %v", h)
		}
		days = append(days, str(h["date"])+" "+str(h["nameEn"]))
	}
	want := []string{"2026-01-01 New Year's Day", "2026-04-13 Songkran Festival", "2026-04-14 Songkran Festival", "2026-04-15 Songkran Festival"}
	if !slices.Equal(days, want) {
		t.Errorf("calendar %v", days)
	}
	// The kept holiday was updated with the fields the generator names; what it does not name stays.
	if got[0]["description"] != "เดิม" || got[0]["name"] != "New Year's Day (วันขึ้นปีใหม่)" {
		t.Errorf("updated %v", got[0])
	}
	if n := w.count(`SELECT count(*) FROM holidays WHERE holiday_date = '2025-01-01'`); n != 1 {
		t.Error("another year was touched")
	}
	ev := w.holidayEvents()
	if len(ev) != 1 || len(strs(ev[0]["ids"])) != 4 || len(strs(ev[0]["deletedDocIds"])) != 1 || num(ev[0]["year"]) != 2026 {
		t.Errorf("events %v", ev)
	}
	// Saving the same review again changes nothing but the rows' update times: no duplicate, nothing deleted.
	r = w.put("/v1/holidays/generate", ta, map[string]any{"year": 2026, "finalHolidays": final, "initialHolidays": final})
	if expect(t, r, http.StatusOK, ""); num(r.data()["saved"]) != 4 || num(r.data()["deleted"]) != 0 {
		t.Errorf("again %v", r.data())
	}
	if n := w.count(`SELECT count(*) FROM holidays WHERE holiday_date >= '2026-01-01' AND holiday_date < '2027-01-01'`); n != 4 {
		t.Errorf("%d rows", n)
	}
	// A carrier saves its own company calendar (no steward needed).
	r = w.put("/v1/holidays/generate", w.login("ta.a"), map[string]any{"year": 2026, "initialHolidays": []any{},
		"finalHolidays": []any{map[string]any{"date": "2026-05-01", "type": "company", "name": "วันแรงงาน A"}}})
	if expect(t, r, http.StatusOK, ""); num(r.data()["saved"]) != 1 {
		t.Errorf("carrier %v", r.data())
	}
	if n := w.count(`SELECT count(*) FROM holidays WHERE tenant_id = $1 AND holiday_type = 'company' AND status = 'published'`, w.A); n != 1 {
		t.Error("carrier calendar")
	}
	// Validation: the year, the lists, every date inside the year.
	for _, tc := range []struct {
		body  map[string]any
		field string
	}{
		{map[string]any{"finalHolidays": []any{}, "initialHolidays": []any{}}, "year"},
		{map[string]any{"year": 1999, "finalHolidays": []any{}, "initialHolidays": []any{}}, "year"},
		{map[string]any{"year": 2026, "initialHolidays": []any{}}, "finalHolidays"},
		{map[string]any{"year": 2026, "finalHolidays": []any{gen("2027-01-01", "x", "y")}, "initialHolidays": []any{}}, "finalHolidays[0].date"},
		{map[string]any{"year": 2026, "finalHolidays": []any{"x"}, "initialHolidays": []any{}}, "finalHolidays[0]"},
		{map[string]any{"year": 2026, "finalHolidays": []any{}, "initialHolidays": []any{map[string]any{"type": "PUBLIC"}}}, "initialHolidays[0].date"},
	} {
		r := w.put("/v1/holidays/generate", ta, tc.body)
		if expect(t, r, http.StatusUnprocessableEntity, "invalid_argument"); r.fields()[tc.field] == "" {
			t.Errorf("%v: %s", tc.body, r.raw)
		}
	}
	if n := len(w.holidayEvents()); n != 3 {
		t.Errorf("%d events (refused requests append none)", n)
	}
}
