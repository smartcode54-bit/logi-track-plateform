//go:build integration

// The holidays projection (T45, issue #65) and the leave write-back's round trip through the FS->PG mirror, on
// postgres:18-alpine against the in-process Firestore stand-in: the loaded fixture holidays project unchanged, holidays
// written in PostgreSQL reach Firestore in the legacy shape (and load back into the same rows), a carrier's own holiday
// stays out, deleted holidays lose their documents (by event, or by the catch-up when the event was not projected, which
// the reconciliation reports until then), a create racing a delete leaves no document, the projection runs
// only once hr is PostgreSQL-owned, and a staff decision written back by internal/hr is what the mirror loads.
package etl

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hr"
)

func (h *syncHarness) projectHolidays(p *Projector, t ShadowTarget) ProjectStats {
	h.t.Helper()
	t.Collection = "holidays"
	st, err := p.Project(h.ctx, t)
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

func (h *syncHarness) reconcileHolidays(o map[string]bool) []Check {
	h.t.Helper()
	docs, err := h.client.Documents(h.ctx, "holidays", false)
	if err != nil {
		h.t.Fatal(err)
	}
	rec := &Reconciliation{Match: true}
	if err := h.e.cfg.Tx(h.ctx, func(tx pgx.Tx) error { return reconcileProjection(h.ctx, tx, "holidays", docs, o, rec) }); err != nil {
		h.t.Fatal(err)
	}
	return rec.Checks
}

func TestProjectionOfHolidays(t *testing.T) {
	h := newSyncHarness(t)
	p4 := owned("auth", "masterdata", "billing", "hr")
	proj := h.projector(p4)

	// While hr is Firestore-owned (P1-P3) holidays are mirrored, never projected; once it is owned, never mirrored.
	if st := h.projectHolidays(h.projector(owned("auth", "masterdata")), ShadowTarget{All: true}); st.NotProjected != 1 {
		t.Fatalf("projected while Firestore-owned: %+v", st)
	}
	if h.plan(p4).Mode("holidays") != "" || h.plan(p4).Mode("leave_requests") != MirrorFull {
		t.Fatal("holidays mirrored while projected, or leave requests not mirrored")
	}

	// The fixture's holidays (ho_1 public, ho_2 the own fleet's company day) say what PostgreSQL says: the first pass
	// adds only updatedAt (the documents had none), the second writes nothing.
	if st := h.projectHolidays(proj, ShadowTarget{All: true}); st.Written != 2 {
		t.Fatalf("first pass %+v", st)
	}
	w := h.fs.Writes()
	for _, wr := range w[len(w)-2:] {
		if strings.Join(wr.Mask, ",") != "updatedAt" {
			t.Errorf("%s mask %v", wr.Path, wr.Mask)
		}
	}
	if f := h.doc("holidays/ho_1"); f["date"] != (dump.Timestamp{Time: time.Date(2026, 10, 22, 17, 0, 0, 0, time.UTC)}) || f["type"] != "PUBLIC" {
		t.Errorf("the legacy Bangkok-midnight date is kept: %v", f)
	}
	if st := h.projectHolidays(proj, ShadowTarget{All: true}); st.Unchanged != 2 || st.Written != 0 {
		t.Fatalf("second pass %+v", st)
	}

	// A public holiday written in PostgreSQL (POST /v1/holidays): minted under its id, the legacy shape.
	staff := h.id(`INSERT INTO users (email, display_name, legacy_auth_uid) VALUES ('hr@example.test', 'HR', 'uid_hr') RETURNING id`)
	pub := h.id(`INSERT INTO holidays (holiday_date, holiday_type, status, name, name_en, name_th, is_recurring, created_by)
		VALUES ('2026-12-05', 'public', 'published', 'วันพ่อแห่งชาติ', 'Father''s Day', 'วันพ่อแห่งชาติ', true, $1) RETURNING id`, staff)
	if st := h.projectHolidays(proj, ShadowTarget{IDs: []uuid.UUID{pub}}); st.Written != 1 {
		t.Fatalf("new holiday %+v", st)
	}
	f := h.doc("holidays/" + pub.String())
	if f["date"] != (dump.Timestamp{Time: time.Date(2026, 12, 5, 0, 0, 0, 0, time.UTC)}) || f["type"] != "PUBLIC" || f["status"] != "PUBLISHED" ||
		f["name"] != "วันพ่อแห่งชาติ" || f["holidayNameEN"] != "Father's Day" || f["holidayNameTH"] != "วันพ่อแห่งชาติ" ||
		f["isRecurring"] != true || f["createdBy"] != "uid_hr" || f["createdAt"] == nil || f["updatedAt"] == nil {
		t.Fatalf("projected holiday %v", f)
	}
	if v := h.scalar(`SELECT legacy_doc_id FROM holidays WHERE id = $1`, pub); v != pub.String() {
		t.Errorf("minted %v", v)
	}
	// A change writes only what moved.
	h.exec(`UPDATE holidays SET name_en = 'Father''s Day (Thailand)', status = 'archived' WHERE id = $1`, pub)
	if st := h.projectHolidays(proj, ShadowTarget{IDs: []uuid.UUID{pub}}); st.Written != 1 {
		t.Fatalf("change %+v", st)
	}
	w = h.fs.Writes()
	if mask := slices.Sorted(slices.Values(w[len(w)-1].Mask)); strings.Join(mask, ",") != "holidayNameEN,status,updatedAt" {
		t.Errorf("change mask %v", mask)
	}

	// A carrier's own holiday is not projected (Firestore holds one calendar: the public one and the own fleet's).
	carrier := h.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type) VALUES ('carrier', 'C', 'C', 'company') RETURNING id`)
	carrierDay := h.id(`INSERT INTO holidays (tenant_id, holiday_date, holiday_type, status, name)
		VALUES ($1, '2026-12-06', 'company', 'published', 'C outing') RETURNING id`, carrier)
	if st := h.projectHolidays(proj, ShadowTarget{IDs: []uuid.UUID{carrierDay}}); st.Skipped != 1 || st.Written != 0 {
		t.Fatalf("carrier holiday %+v", st)
	}
	if _, ok := h.fs.Doc("holidays/" + carrierDay.String()); ok {
		t.Error("a carrier's holiday reached Firestore")
	}

	// The reconciliation matches (the carrier's row is not a projected row).
	for _, c := range h.reconcileHolidays(p4) {
		if !c.OK {
			t.Errorf("reconciliation %+v", c)
		}
	}

	// Deleted in PostgreSQL (DELETE /v1/holidays/{id}, the generator's dropped days): the documents go, a document id
	// a live row still holds stays, a document already gone is no error.
	h.exec(`DELETE FROM holidays WHERE id = $1 OR legacy_doc_id = 'ho_2'`, pub)
	st := h.projectHolidays(proj, ShadowTarget{DeletedDocIDs: []string{pub.String(), "ho_2", "ho_1", "never_there", "a/b"}})
	if st.Deleted != 3 { // pub, ho_2, never_there (a no-op delete); ho_1 is live, a/b is not a document id
		t.Fatalf("deletes %+v", st)
	}
	for path, want := range map[string]bool{"holidays/" + pub.String(): false, "holidays/ho_2": false, "holidays/ho_1": true} {
		if _, ok := h.fs.Doc(path); ok != want {
			t.Errorf("%s exists %v", path, ok)
		}
	}

	// A create racing a delete: the row is gone by the time its projection runs, and its document (written by a
	// projection that read the row before the delete) is deleted again.
	raced := uuid.Must(uuid.NewV7())
	h.fs.Set("holidays/"+raced.String(), map[string]any{"name": "ghost", "type": "PUBLIC"})
	if st := h.projectHolidays(proj, ShadowTarget{IDs: []uuid.UUID{raced}}); st.Deleted != 1 {
		t.Fatalf("vanished %+v", st)
	}
	if _, ok := h.fs.Doc("holidays/" + raced.String()); ok {
		t.Error("the raced document stays")
	}

	// Rollback (hr back to Firestore-owned): the mirror loads the projected documents into the same rows.
	again := h.id(`INSERT INTO holidays (holiday_date, holiday_type, status, name, description_th) VALUES
		('2026-12-10', 'public', 'draft', 'วันรัฐธรรมนูญ', 'หยุดราชการ') RETURNING id`)
	h.projectHolidays(proj, ShadowTarget{IDs: []uuid.UUID{again}})
	before := h.scalar(`SELECT count(*) FROM holidays`)
	h.sweep(h.plan(owned("auth", "masterdata", "billing")))
	if after := h.scalar(`SELECT count(*) FROM holidays`); after != before {
		t.Errorf("rows %v -> %v", before, after)
	}
	if v := h.scalar(`SELECT holiday_date::text || ' ' || holiday_type || ' ' || status || ' ' || name || ' ' || description_th
		FROM holidays WHERE id = $1`, again); v != "2026-12-10 public draft วันรัฐธรรมนูญ หยุดราชการ" {
		t.Errorf("loaded back %v", v)
	}
}

// A delete whose event was never projected (COMPAT_PROJECTION_ENABLED off, a worker whose plan did not project
// holidays yet, a dead-lettered message): the reconciliation reports the documents left behind as a difference, the
// catch-up (`etl project`, ProjectAll) deletes them, and the legacy orphan (ho_dup, rejected at load) and the live
// rows' documents stay.
func TestProjectionOfHolidaysCatchUpDeletes(t *testing.T) {
	h := newSyncHarness(t)
	p4 := owned("auth", "masterdata", "billing", "hr")
	proj := h.projector(p4)
	h.projectHolidays(proj, ShadowTarget{All: true})
	pub := h.id(`INSERT INTO holidays (holiday_date, holiday_type, status, name) VALUES ('2026-12-05', 'public', 'published',
		'วันพ่อแห่งชาติ') RETURNING id`)
	if st := h.projectHolidays(proj, ShadowTarget{IDs: []uuid.UUID{pub}}); st.Written != 1 {
		t.Fatalf("new holiday %+v", st)
	}
	keep := h.id(`INSERT INTO holidays (holiday_date, holiday_type, status, name) VALUES ('2026-12-10', 'public', 'published',
		'วันรัฐธรรมนูญ') RETURNING id`)
	h.projectHolidays(proj, ShadowTarget{IDs: []uuid.UUID{keep}})
	// Deleted with no projection: a holiday born in PostgreSQL (its minted uuid) and a loaded legacy one (ho_2).
	h.exec(`DELETE FROM holidays WHERE id = $1 OR legacy_doc_id = 'ho_2'`, pub)
	stale := []string{"holidays/" + pub.String(), "holidays/ho_2"}

	checks := h.reconcileHolidays(p4)
	var deleted, only *Check
	for i, c := range checks {
		switch c.Name {
		case "projection_deleted":
			deleted = &checks[i]
		case "firestore_only":
			only = &checks[i]
		default:
			if !c.OK {
				t.Errorf("reconciliation %+v", c)
			}
		}
	}
	if deleted == nil || deleted.OK || deleted.Source != "2" || deleted.Note == "" ||
		!strings.Contains(deleted.Note, stale[0]) || !strings.Contains(deleted.Note, stale[1]) {
		t.Fatalf("the documents of deleted rows are not reported: %+v", deleted)
	}
	if only == nil || !only.OK || only.Source != "1" || !strings.Contains(only.Note, "holidays/ho_dup") {
		t.Errorf("legacy orphans %+v", only)
	}

	// The catch-up deletes them (and only them).
	holidaysOnly := *proj
	holidaysOnly.Collections = []string{"holidays"}
	stats, err := holidaysOnly.ProjectAll(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st := stats["holidays"]; st.Deleted != 2 || st.Written != 0 {
		t.Fatalf("catch-up %+v", st)
	}
	for path, want := range map[string]bool{stale[0]: false, stale[1]: false, "holidays/ho_1": true, "holidays/ho_dup": true,
		"holidays/" + keep.String(): true} {
		if _, ok := h.fs.Doc(path); ok != want {
			t.Errorf("%s exists %v", path, ok)
		}
	}
	for _, c := range h.reconcileHolidays(p4) {
		if !c.OK || c.Name == "projection_deleted" {
			t.Errorf("after the catch-up %+v", c)
		}
	}
	if st := h.projectHolidays(proj, ShadowTarget{All: true}); st.Deleted != 0 || st.Written != 0 {
		t.Errorf("a second catch-up %+v", st)
	}
}

// The leave write-back of internal/hr (hr.FirestoreLeaveDocs, hr.LegacyLeaveDecision) against the Firestore stand-in,
// then the mirror: the row the sweep loads carries the decision, its maker (approverId through users.legacy_auth_uid)
// and time, and a rejection's reason.
func TestLeaveWriteBackRoundTrip(t *testing.T) {
	h := newSyncHarness(t)
	staff := h.id(`INSERT INTO users (email, display_name, legacy_auth_uid) VALUES ('hr@example.test', 'HR', 'uid_hr') RETURNING id`)
	docs := hr.FirestoreLeaveDocs{FS: h.client}
	uid := "uid_hr"
	decide := func(docID, status string, reason *string) time.Time {
		t.Helper()
		f, at, ok, err := docs.Get(h.ctx, docID)
		if err != nil || !ok || hr.LegacyLeaveStatus(f["status"]) != "pending" {
			t.Fatalf("%s: %v %v %v", docID, f, ok, err)
		}
		now := time.Date(2026, 10, 12, 2, 30, 0, 0, time.UTC)
		written, err := docs.Update(h.ctx, docID, hr.LegacyLeaveDecision(status, &uid, reason, now), at)
		if err != nil || !written.After(at) {
			t.Fatalf("update %s: %v %v", docID, written, err)
		}
		// The precondition: a second write against the old update time is refused.
		if _, err := docs.Update(h.ctx, docID, map[string]any{"status": "X"}, at); err != hr.ErrDocumentChanged {
			t.Fatalf("stale update: %v", err)
		}
		return now
	}
	at := decide("lv_2", "approved", nil)
	h.fs.Set("leave_requests/lv_3", map[string]any{"driverId": "drv_own", "type": "BUSINESS", "status": "PENDING",
		"startDate": dump.Timestamp{Time: time.Date(2026, 10, 20, 17, 0, 0, 0, time.UTC)},
		"endDate":   dump.Timestamp{Time: time.Date(2026, 10, 20, 17, 0, 0, 0, time.UTC)}, "reason": "ธุระ"})
	h.sweep(h.plan(owned("auth", "masterdata", "billing", "hr")))
	reason := "งานเร่ง"
	decide("lv_3", "rejected", &reason)
	h.sweep(h.plan(owned("auth", "masterdata", "billing", "hr")))
	if v := h.scalar(`SELECT status || ' ' || approver_user_id::text || ' ' || (decided_at = $1)::text FROM leave_requests
		WHERE legacy_doc_id = 'lv_2'`, at); v != "approved "+staff.String()+" true" {
		t.Errorf("lv_2 %v", v)
	}
	if v := h.scalar(`SELECT status || ' ' || rejection_reason || ' ' || approver_user_id::text FROM leave_requests
		WHERE legacy_doc_id = 'lv_3'`); v != "rejected งานเร่ง "+staff.String() {
		t.Errorf("lv_3 %v", v)
	}
	// A cancel writes the status alone; the decision's maker and time stay.
	f, ut, _, _ := docs.Get(h.ctx, "lv_2")
	if _, err := docs.Update(h.ctx, "lv_2", hr.LegacyLeaveDecision("cancelled", &uid, nil, at.Add(time.Hour)), ut); err != nil || f["approverId"] != "uid_hr" {
		t.Fatal(err, f)
	}
	h.sweep(h.plan(owned("auth", "masterdata", "billing", "hr")))
	if v := h.scalar(`SELECT status || ' ' || (decided_at = $1)::text FROM leave_requests WHERE legacy_doc_id = 'lv_2'`, at); v != "cancelled true" {
		t.Errorf("lv_2 cancelled %v", v)
	}
}
