//go:build integration

// Integration tests of the leave requests of T45 (issue #65): the list (reach, filters, keyset, counts, evidence
// signed), the decisions on PostgreSQL (every domain owned: P7b, the staging VM) with their transitions and events, and
// the class C write-back (Firestore first, the mirror's acknowledgement or 202, never PostgreSQL next to Firestore).
package hr_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// leave inserts a leave request of drv in tenant created at the given instant; legacy is its Firestore document id
// ("" = none, a row born in PostgreSQL).
func (w *world) leave(tenant, drv, status, start, end, legacy string, created time.Time) string {
	w.t.Helper()
	var doc any
	if legacy != "" {
		doc = legacy
	}
	return w.id(`INSERT INTO leave_requests (tenant_id, tenant_source, driver_id, leave_type, status, start_date, end_date, reason,
		legacy_doc_id, created_at) VALUES ($1, 'driver', $2, 'sick', $3, $4, $5, 'ไข้', $6, $7) RETURNING id::text`,
		tenant, drv, status, start, end, doc, created)
}

// evidence links a committed leave_evidence file to a request.
func (w *world) evidence(tenant, leave, key string, pos int) {
	w.t.Helper()
	w.evidenceAs(tenant, leave, key, pos, "committed")
}

// evidenceAs links a leave_evidence file of the given status (committed, or missing_at_source as the ETL's media-copy
// leaves a file it did not find) to a request.
func (w *world) evidenceAs(tenant, leave, key string, pos int, status string) {
	w.t.Helper()
	f := w.id(`INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, owner_kind, owner_id, content_type, size_bytes,
		status, committed_at, storage_backend) VALUES ('logitrack', $1, $2, 'leave_evidence', 'leave', $3, 'image/jpeg', 10,
		$4, CASE WHEN $4 = 'committed' THEN now() END, 'local') RETURNING id::text`, key, tenant, leave, status)
	w.exec(`INSERT INTO leave_request_attachments (leave_request_id, file_id, position) VALUES ($1, $2, $3)`, leave, f, pos)
}

func pgOwned(_ *world, d *hr.Deps) { d.LeavePGOwned = true }

func ids(items []map[string]any) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, str(it["id"]))
	}
	return out
}

func TestLeaveList(t *testing.T) {
	w := newWorld(t, pgOwned)
	base := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	o1 := w.leave(w.O, w.dO, "pending", "2026-10-12", "2026-10-13", "lv_o1", base)
	o2 := w.leave(w.O, w.dO2, "approved", "2026-10-02", "2026-10-02", "", base.Add(time.Hour))
	a1 := w.leave(w.A, w.dA, "pending", "2026-10-05", "2026-10-07", "", base.Add(2*time.Hour))
	c1 := w.leave(w.C, w.dC, "pending", "2026-10-05", "2026-10-05", "", base.Add(3*time.Hour))
	w.evidence(w.O, o1, "leave/"+w.dO+"/1_0.jpg", 0)
	w.evidence(w.O, o1, "leave/"+w.dO+"/1_1.jpg", 1)
	w.evidenceAs(w.O, o1, "leave/"+w.dO+"/1_2.jpg", 2, "missing_at_source")
	// A quarantined request (unresolved at load) is never listed.
	w.exec(`INSERT INTO leave_requests (tenant_id, tenant_source, leave_type, status, start_date, end_date)
		VALUES (app_quarantine_tenant_id(), 'quarantine', 'sick', 'pending', '2026-10-01', '2026-10-01')`)

	ta := w.login("ta.o")
	r := w.get("/v1/leave-requests", ta)
	expect(t, r, http.StatusOK, "")
	// The own fleet's staff read their tenant and the carrier working for it (A), newest first; never C.
	if got := strings.Join(ids(r.list()), ","); got != strings.Join([]string{a1, o2, o1}, ",") {
		t.Fatalf("list %s", got)
	}
	counts := r.meta()["statusCounts"].(map[string]any)
	if num(counts["pending"]) != 2 || num(counts["approved"]) != 1 || num(counts["rejected"]) != 0 || num(counts["cancelled"]) != 0 {
		t.Errorf("counts %v", counts)
	}
	first := r.list()[2]
	if first["startDate"] != "2026-10-12" || first["endDate"] != "2026-10-13" || num(first["days"]) != 2 || first["type"] != "sick" ||
		first["driverName"] != "Somchai ดี" || first["status"] != "pending" {
		t.Errorf("request %v", first)
	}
	atts := first["attachments"].([]any)
	if len(atts) != 3 {
		t.Fatalf("attachments %v", atts)
	}
	for i, a := range atts[:2] {
		m := a.(map[string]any)
		if !strings.HasSuffix(str(m["key"]), "/1_"+string(rune('0'+i))+".jpg") || !strings.Contains(str(m["url"]), "/media/") {
			t.Errorf("attachment %d %v", i, m)
		}
	}
	// A file missing at source keeps its key, with a null url (main spec §10 as built T25, item 9).
	if m := atts[2].(map[string]any); m["key"] != "leave/"+w.dO+"/1_2.jpg" || m["url"] != nil {
		t.Errorf("missing at source %v", m)
	}
	// Filters: status (legacy upper case, a comma list), the driver; the counts keep the driver filter only.
	if r := w.get("/v1/leave-requests?status=PENDING", ta); strings.Join(ids(r.list()), ",") != a1+","+o1 {
		t.Errorf("pending %s", r.raw)
	}
	if r := w.get("/v1/leave-requests?status=approved,rejected", ta); strings.Join(ids(r.list()), ",") != o2 {
		t.Errorf("approved %s", r.raw)
	}
	r = w.get("/v1/leave-requests?driverId="+w.dO, ta)
	if strings.Join(ids(r.list()), ",") != o1 || num(r.meta()["statusCounts"].(map[string]any)["pending"]) != 1 {
		t.Errorf("driver %s", r.raw)
	}
	// Keyset: pages of one, no cap, no counts after the first page.
	var seen []string
	cur := ""
	for page := 0; ; page++ {
		q := "/v1/leave-requests?limit=1"
		if cur != "" {
			q += "&cursor=" + cur
		}
		r := w.get(q, ta)
		expect(t, r, http.StatusOK, "")
		if page > 0 && r.meta() != nil {
			t.Errorf("counts on page %d", page)
		}
		seen = append(seen, ids(r.list())...)
		if cur = r.next(); cur == "" {
			break
		}
	}
	if strings.Join(seen, ",") != strings.Join([]string{a1, o2, o1}, ",") {
		t.Errorf("pages %v", seen)
	}
	// A carrier's admin reads its own requests only; another carrier's are invisible.
	if r := w.get("/v1/leave-requests", w.login("ta.a")); strings.Join(ids(r.list()), ",") != a1 {
		t.Errorf("carrier A %s", r.raw)
	}
	if r := w.get("/v1/leave-requests", w.login("ta.c")); strings.Join(ids(r.list()), ",") != c1 {
		t.Errorf("carrier C %s", r.raw)
	}
	// Capability and parameters.
	expect(t, w.get("/v1/leave-requests", w.login("mg.o")), http.StatusForbidden, "permission_denied")
	expect(t, w.get("/v1/leave-requests?status=done", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/leave-requests?driverId=x", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/leave-requests?cursor=zz", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/leave-requests?sort=x", ta), http.StatusBadRequest, "bad_request")
	expect(t, w.get("/v1/leave-requests", ""), http.StatusUnauthorized, "unauthenticated")
}

// leaveEvents are the leave.decided events of a request, oldest first.
func (w *world) leaveEvents(id string) []map[string]any {
	w.t.Helper()
	rows, err := w.etl.Query(context.Background(), `SELECT payload::text, realtime_topics::text, coalesce(headers->>'origin', '')
		FROM outbox_events WHERE routing_key = 'leave.decided' AND aggregate_id = $1 ORDER BY id`, id)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var payload, topics, origin string
		if err := rows.Scan(&payload, &topics, &origin); err != nil {
			w.t.Fatal(err)
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			w.t.Fatal(err)
		}
		m["_topics"], m["_origin"] = topics, origin
		out = append(out, m)
	}
	return out
}

// AC: approve and reject only from pending (cancel only from approved), on PostgreSQL when every domain is owned.
func TestLeaveDecisionsOnPostgreSQL(t *testing.T) {
	w := newWorld(t, pgOwned)
	now := time.Now()
	p1 := w.leave(w.O, w.dO, "pending", "2026-10-12", "2026-10-13", "", now)
	p2 := w.leave(w.O, w.dO, "pending", "2026-10-20", "2026-10-20", "", now)
	a1 := w.leave(w.A, w.dA, "pending", "2026-10-05", "2026-10-05", "", now)
	c1 := w.leave(w.C, w.dC, "pending", "2026-10-05", "2026-10-05", "", now)
	ta := w.login("ta.o")
	admin := w.id(`SELECT id::text FROM users WHERE email = 'ta.o@example.test'`)

	r := w.post("/v1/leave-requests/"+p1+"/approve", ta, nil)
	expect(t, r, http.StatusOK, "")
	d := r.data()
	if d["status"] != "approved" || d["approverUserId"] != admin || d["decidedAt"] == nil || d["rejectionReason"] != nil {
		t.Fatalf("approved %v", d)
	}
	ev := w.leaveEvents(p1)
	if len(ev) != 1 || ev[0]["status"] != "approved" || ev[0]["from"] != "pending" || ev[0]["driverId"] != w.dO ||
		!strings.Contains(str(ev[0]["_topics"]), "tenant:"+w.O+":hr") || !strings.Contains(str(ev[0]["_topics"]), "driver:"+w.dO) ||
		ev[0]["_origin"] != "" {
		t.Fatalf("event %v", ev)
	}
	// From approved: approve and reject refused, cancel allowed (the decision's maker and time stay).
	r = w.post("/v1/leave-requests/"+p1+"/approve", ta, nil)
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "transition_not_allowed" || r.detail("status") != "approved" || r.detail("to") != "approved" {
		t.Errorf("approve again %s", r.raw)
	}
	expect(t, w.post("/v1/leave-requests/"+p1+"/reject", ta, map[string]any{"reason": "x"}), http.StatusConflict, "failed_precondition")
	r = w.post("/v1/leave-requests/"+p1+"/cancel", ta, nil)
	expect(t, r, http.StatusOK, "")
	if r.data()["status"] != "cancelled" || r.data()["approverUserId"] != admin || r.data()["decidedAt"] != d["decidedAt"] {
		t.Errorf("cancelled %v", r.data())
	}
	// From cancelled nothing moves.
	for _, a := range []string{"approve", "cancel"} {
		expect(t, w.post("/v1/leave-requests/"+p1+"/"+a, ta, nil), http.StatusConflict, "failed_precondition")
	}
	expect(t, w.post("/v1/leave-requests/"+p1+"/reject", ta, map[string]any{"reason": "x"}), http.StatusConflict, "failed_precondition")

	// Reject needs a reason (and only reject takes one); cancel of a pending request is refused.
	r = w.post("/v1/leave-requests/"+p2+"/reject", ta, nil)
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if r.fields()["reason"] != "required" {
		t.Errorf("reject without a reason %s", r.raw)
	}
	r = w.post("/v1/leave-requests/"+p2+"/approve", ta, map[string]any{"reason": "x"})
	if expect(t, r, http.StatusUnprocessableEntity, "invalid_argument"); r.fields()["reason"] != "not_supported" {
		t.Errorf("approve with a reason %s", r.raw)
	}
	expect(t, w.post("/v1/leave-requests/"+p2+"/cancel", ta, nil), http.StatusConflict, "failed_precondition")
	r = w.post("/v1/leave-requests/"+p2+"/reject", ta, map[string]any{"reason": "  งานเร่ง  "})
	expect(t, r, http.StatusOK, "")
	if r.data()["status"] != "rejected" || r.data()["rejectionReason"] != "งานเร่ง" || r.data()["approverUserId"] != admin {
		t.Errorf("rejected %v", r.data())
	}
	if ev := w.leaveEvents(p2); len(ev) != 1 || ev[0]["reason"] != "งานเร่ง" || ev[0]["status"] != "rejected" {
		t.Errorf("reject event %v", ev)
	}
	expect(t, w.post("/v1/leave-requests/"+p2+"/cancel", ta, nil), http.StatusConflict, "failed_precondition")

	// Reach: the own fleet decides its carrier's request; another carrier's is 404; a carrier cannot reach the own fleet.
	expect(t, w.post("/v1/leave-requests/"+a1+"/approve", ta, nil), http.StatusOK, "")
	expect(t, w.post("/v1/leave-requests/"+c1+"/approve", ta, nil), http.StatusNotFound, "not_found")
	expect(t, w.post("/v1/leave-requests/"+p2+"/cancel", w.login("ta.c"), nil), http.StatusNotFound, "not_found")
	// Capability, unknown ids, a decision the API does not have.
	expect(t, w.post("/v1/leave-requests/"+c1+"/approve", w.login("mg.o"), nil), http.StatusForbidden, "permission_denied")
	expect(t, w.post("/v1/leave-requests/not-a-uuid/approve", ta, nil), http.StatusNotFound, "not_found")
	expect(t, w.post("/v1/leave-requests/"+c1+"/archive", ta, nil), http.StatusNotFound, "not_found")
	if n := w.count(`SELECT count(*) FROM leave_requests WHERE id = $1 AND status = 'pending'`, c1); n != 1 {
		t.Errorf("c1 changed")
	}
}

// Two decisions of one request at once: exactly one wins (the request is locked first).
func TestLeaveDecisionsRace(t *testing.T) {
	w := newWorld(t, pgOwned)
	id := w.leave(w.O, w.dO, "pending", "2026-10-12", "2026-10-13", "", time.Now())
	ta, ta2 := w.login("ta.o"), w.login("ta2.o")
	type out struct{ status int }
	ch := make(chan out, 2)
	go func() { ch <- out{w.post("/v1/leave-requests/"+id+"/approve", ta, nil).status} }()
	go func() {
		ch <- out{w.post("/v1/leave-requests/"+id+"/reject", ta2, map[string]any{"reason": "no"}).status}
	}()
	a, b := <-ch, <-ch
	if got := []int{a.status, b.status}; !slices.Equal(got, []int{200, 409}) && !slices.Equal(got, []int{409, 200}) {
		t.Fatalf("statuses %d %d", a.status, b.status)
	}
	if n := len(w.leaveEvents(id)); n != 1 {
		t.Errorf("%d events", n)
	}
}

// putLeave stores leave_requests/{docID} in the Firestore stand-in as the 3.x app wrote it (its update time now).
func putLeave(docs *writeback.Memory, docID string, fields map[string]any) {
	docs.Put(hr.LeaveCollection, docID, fields, time.Now().UTC())
}

// fakeAcks plays the mirror's acknowledgement.
type fakeAcks struct {
	ack func(docID string, want time.Time) bool
}

func (f fakeAcks) WaitMirrorAck(_ context.Context, _, docID string, want time.Time, _ time.Duration) (bool, error) {
	return f.ack(docID, want), nil
}

// While Firestore writes leave_requests (any domain Firestore-owned): the decision goes to the Firestore document first
// in the legacy shape, never to PostgreSQL; with the mirror's acknowledgement the answer is the mirrored row, without it
// 202 with the document written.
func TestLeaveWriteBack(t *testing.T) {
	docs := writeback.NewMemory()
	mirror := map[string]bool{} // doc ids the fake mirror applies and acknowledges
	var w *world
	acks := fakeAcks{ack: func(docID string, _ time.Time) bool {
		if !mirror[docID] {
			return false
		}
		f, _ := docs.Doc(hr.LeaveCollection, docID)
		w.exec(`UPDATE leave_requests SET status = lower($2), rejection_reason = $3, decided_at = coalesce($4, decided_at)
			WHERE legacy_doc_id = $1`, docID, f["status"], f["rejectionReason"], f["approvedAt"])
		return true
	}}
	w = newWorld(t, func(_ *world, d *hr.Deps) {
		d.LeaveWriteBack = &hr.LeaveWriteBack{Writer: docs, Acks: acks, Wait: 50 * time.Millisecond}
	})
	w.exec(`UPDATE users SET legacy_auth_uid = 'uid_ta_o' WHERE email = 'ta.o@example.test'`)
	ta := w.login("ta.o")
	now := time.Now()
	acked := w.leave(w.O, w.dO, "pending", "2026-10-12", "2026-10-13", "lv_acked", now)
	pending := w.leave(w.O, w.dO, "pending", "2026-10-14", "2026-10-14", "lv_pending", now)
	born := w.leave(w.O, w.dO, "pending", "2026-10-15", "2026-10-15", "", now)
	gone := w.leave(w.O, w.dO, "pending", "2026-10-16", "2026-10-16", "lv_gone", now)
	app := map[string]any{"driverId": "drv_o", "type": "SICK", "status": "PENDING", "reason": "ไข้", "driverName": "Somchai"}
	putLeave(docs, "lv_acked", app)
	putLeave(docs, "lv_pending", app)
	mirror["lv_acked"] = true

	// Acknowledged: 200 with the mirrored row; the document says what the legacy web wrote.
	r := w.post("/v1/leave-requests/"+acked+"/approve", ta, nil)
	expect(t, r, http.StatusOK, "")
	if r.data()["status"] != "approved" || r.data()["id"] != acked {
		t.Fatalf("acknowledged approval %v", r.data())
	}
	f, _ := docs.Doc(hr.LeaveCollection, "lv_acked")
	if f["status"] != "APPROVED" || f["approverId"] != "uid_ta_o" || f["driverName"] != "Somchai" || f["reason"] != "ไข้" {
		t.Errorf("document %v", f)
	}
	if _, ok := f["approvedAt"].(time.Time); !ok {
		t.Errorf("approvedAt %T", f["approvedAt"])
	}
	if _, ok := f["updatedAt"].(time.Time); !ok {
		t.Errorf("updatedAt %T", f["updatedAt"])
	}
	ev := w.leaveEvents(acked)
	if len(ev) != 1 || ev[0]["_origin"] != "writeback" || ev[0]["status"] != "approved" || ev[0]["from"] != "pending" ||
		!strings.Contains(str(ev[0]["_topics"]), "driver:"+w.dO) {
		t.Errorf("write-back event %v", ev)
	}

	// Not acknowledged within the wait: 202 with the echo; PostgreSQL is not written (the mirror will).
	r = w.post("/v1/leave-requests/"+pending+"/reject", ta, map[string]any{"reason": "งานเร่ง"})
	expect(t, r, http.StatusAccepted, "")
	d := r.data()
	doc := d["document"].(map[string]any)
	if d["pending"] != true || d["docId"] != "lv_pending" || d["collection"] != "leave_requests" || d["status"] != "rejected" ||
		doc["status"] != "REJECTED" || doc["rejectionReason"] != "งานเร่ง" || doc["approverId"] != "uid_ta_o" || d["updateTime"] == nil {
		t.Fatalf("echo %v", d)
	}
	if n := w.count(`SELECT count(*) FROM leave_requests WHERE id = $1 AND status = 'pending' AND decided_at IS NULL`, pending); n != 1 {
		t.Error("PostgreSQL was written next to Firestore")
	}
	if len(w.leaveEvents(pending)) != 1 {
		t.Error("no event for the 202")
	}

	// The transition is checked against the document (Firestore is the writer): the mirror lags, the row still says
	// pending, but the document says REJECTED, so approve is refused and nothing is written.
	before := len(w.leaveEvents(pending))
	r = w.post("/v1/leave-requests/"+pending+"/approve", ta, nil)
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "transition_not_allowed" || r.detail("status") != "rejected" {
		t.Errorf("lagging mirror %s", r.raw)
	}
	if len(w.leaveEvents(pending)) != before {
		t.Error("a refused decision appended an event")
	}

	// Two decisions before the mirror caught up: approved in Firestore (202, the row still says pending), then
	// cancelled (the document's APPROVED allows it). The cancel's event names the status it was checked against
	// (approved), not the lagging row's.
	lag := w.leave(w.O, w.dO, "pending", "2026-10-17", "2026-10-17", "lv_lag", now)
	putLeave(docs, "lv_lag", app)
	expect(t, w.post("/v1/leave-requests/"+lag+"/approve", ta, nil), http.StatusAccepted, "")
	expect(t, w.post("/v1/leave-requests/"+lag+"/cancel", ta, nil), http.StatusAccepted, "")
	if f, _ := docs.Doc(hr.LeaveCollection, "lv_lag"); f["status"] != "CANCELLED" {
		t.Errorf("lagging document %v", f)
	}
	if ev := w.leaveEvents(lag); len(ev) != 2 || ev[0]["status"] != "approved" || ev[0]["from"] != "pending" ||
		ev[1]["status"] != "cancelled" || ev[1]["from"] != "approved" {
		t.Errorf("lagging mirror events %v", ev)
	}

	// A row born in PostgreSQL has no document; a document deleted in Firestore; Firestore down.
	r = w.post("/v1/leave-requests/"+born+"/approve", ta, nil)
	if expect(t, r, http.StatusConflict, "failed_precondition"); r.detail("reason") != "not_in_firestore" {
		t.Errorf("born in PostgreSQL %s", r.raw)
	}
	r = w.post("/v1/leave-requests/"+gone+"/approve", ta, nil)
	if expect(t, r, http.StatusConflict, "failed_precondition"); r.detail("reason") != "document_changed" {
		t.Errorf("deleted document %s", r.raw)
	}
	putLeave(docs, "lv_gone", app)
	docs.Fail(errors.New("firestore down"))
	r = w.post("/v1/leave-requests/"+gone+"/approve", ta, nil)
	if expect(t, r, http.StatusServiceUnavailable, "unavailable"); r.detail("reason") != "write_back" {
		t.Errorf("Firestore down %s", r.raw)
	}
	docs.Fail(nil)
	if n := len(w.leaveEvents(gone)); n != 0 {
		t.Errorf("%d events for failed write-backs", n)
	}
}

// A concurrent writer (the driver's app, another staff member) changed the document between the read and the write:
// the precondition refuses the write (409 document_changed), and the decision is not half-applied.
func TestLeaveWriteBackPrecondition(t *testing.T) {
	docs := writeback.NewMemory()
	w := newWorld(t, func(_ *world, d *hr.Deps) {
		d.LeaveWriteBack = &hr.LeaveWriteBack{Writer: racingDocs{Memory: docs}}
	})
	id := w.leave(w.O, w.dO, "pending", "2026-10-12", "2026-10-13", "lv_race", time.Now())
	putLeave(docs, "lv_race", map[string]any{"status": "PENDING"})
	r := w.post("/v1/leave-requests/"+id+"/approve", w.login("ta.o"), nil)
	if expect(t, r, http.StatusConflict, "failed_precondition"); r.detail("reason") != "document_changed" {
		t.Fatalf("raced write %s", r.raw)
	}
	if f, _ := docs.Doc(hr.LeaveCollection, "lv_race"); f["status"] != "PENDING" || f["note"] != "app" {
		t.Errorf("document %v", f)
	}
}

// racingDocs writes the document (as the app would) between every Get and Commit: a new version, a new update time.
type racingDocs struct{ *writeback.Memory }

func (r racingDocs) Get(ctx context.Context, collection, docID string) (map[string]any, time.Time, bool, error) {
	f, at, ok, err := r.Memory.Get(ctx, collection, docID)
	if ok {
		next := map[string]any{"note": "app"}
		for k, v := range f {
			next[k] = v
		}
		r.Put(collection, docID, next, at.Add(time.Microsecond))
	}
	return f, at, ok, err
}

// Without a Firestore writer while Firestore writes leave requests, decisions are refused: PostgreSQL is never written
// in Firestore's place.
func TestLeaveWriteBackUnconfigured(t *testing.T) {
	w := newWorld(t)
	id := w.leave(w.O, w.dO, "pending", "2026-10-12", "2026-10-13", "lv_1", time.Now())
	r := w.post("/v1/leave-requests/"+id+"/approve", w.login("ta.o"), nil)
	if expect(t, r, http.StatusServiceUnavailable, "unavailable"); r.detail("reason") != "write_back_unconfigured" {
		t.Fatalf("unconfigured %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM leave_requests WHERE id = $1 AND status = 'pending'`, id); n != 1 {
		t.Error("written")
	}
	// The list is served either way.
	expect(t, w.get("/v1/leave-requests", w.login("ta.o")), http.StatusOK, "")
}

// The real read-your-writes path: the mirror raises cache:mirror_ack in Redis after applying the document, and the
// handler that waits on it answers with the mirrored row (cache.Cache.WaitMirrorAck).
func TestLeaveWriteBackRedisAck(t *testing.T) {
	docs := writeback.NewMemory()
	var w *world
	docs.OnCommit = func(writes []writeback.DocWrite, at time.Time) {
		for _, wr := range writes {
			go func() {
				time.Sleep(100 * time.Millisecond) // the mirror's sweep, later than the write
				w.exec(`UPDATE leave_requests SET status = lower($2), decided_at = now() WHERE legacy_doc_id = $1`, wr.DocID, wr.Fields["status"])
				if err := w.caches.SetMirrorAck(context.Background(), wr.Collection, wr.DocID, at); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	w = newWorld(t, func(w *world, d *hr.Deps) {
		d.LeaveWriteBack = &hr.LeaveWriteBack{Writer: docs, Acks: w.caches, Wait: 3 * time.Second}
	})
	var _ hr.MirrorAcks = (*cache.Cache)(nil)
	id := w.leave(w.O, w.dO, "approved", "2026-10-12", "2026-10-13", "lv_ack", time.Now())
	putLeave(docs, "lv_ack", map[string]any{"status": "APPROVED"})
	r := w.post("/v1/leave-requests/"+id+"/cancel", w.login("ta.o"), nil)
	expect(t, r, http.StatusOK, "")
	if r.data()["status"] != "cancelled" {
		t.Fatalf("acknowledged cancel %v", r.data())
	}
	// A cancel writes the status alone (and updatedAt), as the legacy page did.
	if f, _ := docs.Doc(hr.LeaveCollection, "lv_ack"); len(f) != 2 || f["status"] != "CANCELLED" {
		t.Errorf("cancel document %v", f)
	}
}

// capturedAMQP plays RabbitMQ for the relay: the bodies it published, by routing key.
type capturedAMQP struct{ bodies map[string][][]byte }

func (c *capturedAMQP) Publish(_ context.Context, _, key string, msg amqp.Publishing) error {
	c.bodies[key] = append(c.bodies[key], msg.Body)
	return nil
}

// A rejection's free-text reason travels in the outbox row, which the relay publishes whole to RabbitMQ (notify.fcm
// reads it there: on the write-back path the row holds no reason until the mirror applied the document), and never on
// a stream (realtime.Project): tenant:{tid}:hr also opens on hr:view_payroll, which does not read leave requests, and
// the driver's copy names no driver.
func TestLeaveDecidedStreamsCarryNoReason(t *testing.T) {
	w := newWorld(t, pgOwned)
	ctx := context.Background()
	id := w.leave(w.O, w.dO, "pending", "2026-10-12", "2026-10-13", "", time.Now())
	const reason = "ใบรับรองแพทย์ไม่ตรงกับอาการที่แจ้ง"
	expect(t, w.post("/v1/leave-requests/"+id+"/reject", w.login("ta.o"), map[string]any{"reason": reason}), http.StatusOK, "")
	if ev := w.leaveEvents(id); len(ev) != 1 || ev[0]["reason"] != reason {
		t.Fatalf("outbox row %v", ev)
	}
	broker := &capturedAMQP{bodies: map[string][][]byte{}}
	relay := outbox.NewRelay(w.pool, func() (outbox.Publisher, error) { return broker, nil }, realtime.NewWriter(w.rdb, w.ks, 100, time.Hour),
		outbox.RelayOptions{Log: zerolog.Nop()})
	if _, err := relay.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	bodies := broker.bodies["leave.decided"]
	if len(bodies) != 1 {
		t.Fatalf("published %d leave.decided", len(bodies))
	}
	var body map[string]any
	if err := json.Unmarshal(bodies[0], &body); err != nil || body["reason"] != reason {
		t.Errorf("the AMQP body lost the reason: %s %v", bodies[0], err)
	}
	want := map[string]any{"entity": "leave_request", "id": id, "tenantId": w.O, "status": "rejected", "from": "pending", "driverId": w.dO}
	for _, topic := range []string{realtime.TenantTopic(w.O, realtime.FamilyHR), realtime.DriverTopic(w.dO)} {
		entries, err := w.rdb.XRange(ctx, w.ks.RealtimeLog(topic), "-", "+").Result()
		if err != nil {
			t.Fatal(err)
		}
		var got []map[string]any
		for _, e := range entries {
			if e.Values["type"] != "leave.decided" {
				continue
			}
			m := map[string]any{}
			if err := json.Unmarshal([]byte(str(e.Values["data"])), &m); err != nil {
				t.Fatal(err)
			}
			got = append(got, m)
		}
		exp := maps.Clone(want)
		if strings.HasPrefix(topic, "driver:") {
			delete(exp, "driverId")
		}
		if len(got) != 1 || !maps.Equal(got[0], exp) {
			t.Errorf("%s replay log %v, want %v", topic, got, exp)
		}
	}
}
