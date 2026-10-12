//go:build integration

// The broadcasts projection (T48, issue #68) on postgres:18-alpine against the in-process Firestore stand-in: the loaded
// fixture broadcast projects unchanged; broadcasts written in PostgreSQL reach Firestore in the legacy shape the 3.x app
// lists; a carrier's broadcast and a broadcast to selected users stay out; AC: a voided broadcast disappears from the
// projection (its document is deleted on broadcast.voided, the row stays) and the reconciliation reports a voided
// broadcast's document until then; the projection runs only once comms is PostgreSQL-owned. Review panel: the read
// receipts reach readCount and readDriverAuthIds (added to, never replaced); a send racing its void leaves no document
// behind; a P5 rollback (the mirror reads broadcasts again) keeps PostgreSQL's reads and the voided rows.
package etl

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

func TestProjectionOfBroadcasts(t *testing.T) {
	h := newSyncHarness(t)
	p5 := owned("auth", "masterdata", "billing", "hr", "comms")
	proj := h.projector(p5)

	// While comms is Firestore-owned (P1-P4) broadcasts are mirrored, never projected; once it is owned, never mirrored.
	if st := h.project(h.projector(owned("auth", "masterdata", "billing", "hr")), "broadcast.created",
		`{"id":"`+h.id(`SELECT id FROM broadcasts WHERE legacy_doc_id = 'bc_1'`).String()+`"}`); st.NotProjected != 1 {
		t.Fatalf("projected while Firestore-owned: %+v", st)
	}
	if h.plan(p5).Mode("broadcasts") != "" || h.plan(p5).Mode("chats") != MirrorFull {
		t.Fatal("broadcasts mirrored while projected, or chats not mirrored")
	}

	// The fixture's broadcast (bc_1, no title, one reader) says what PostgreSQL says: nothing is written; its reader
	// (an uid the load could not resolve) stays in readDriverAuthIds and in readCount.
	writes := len(h.fs.Writes())
	if st, err := proj.Project(h.ctx, ShadowTarget{Collection: "broadcasts", All: true}); err != nil || st.Unchanged != 1 || st.Written != 0 {
		t.Fatalf("fixture pass %+v %v", st, err)
	}
	if len(h.fs.Writes()) != writes {
		t.Fatal("the fixture broadcast was rewritten")
	}

	// A broadcast of the own fleet written in PostgreSQL (POST /v1/broadcasts): minted under its id, the legacy shape
	// the 3.x app reads (title, messageText, sentAt, createdByName) and the web's (createdBy, recipientCount,
	// recipientGroup).
	ownFleet := "01900000-0000-7000-8000-000000000001"
	sender := h.id(`INSERT INTO users (email, display_name, legacy_auth_uid) VALUES ('ops@example.test', 'Ops', 'uid_ops') RETURNING id`)
	sent := time.Date(2026, 10, 12, 2, 30, 0, 123456000, time.UTC)
	own := h.id(`INSERT INTO broadcasts (tenant_id, created_by_user_id, created_by_name, title, message_text, recipient_group,
		recipient_count, sent_at) VALUES ($1, $2, 'ฝ่ายปฏิบัติการ', 'วันหยุด', 'หยุดสงกรานต์', 'own_fleet', 12, $3) RETURNING id`,
		ownFleet, sender, sent)
	if st := h.project(proj, "broadcast.created", `{"entity":"broadcast","id":"`+own.String()+`"}`); st.Written != 1 {
		t.Fatalf("new broadcast %+v", st)
	}
	f := h.doc("broadcasts/" + own.String())
	if f["title"] != "วันหยุด" || f["messageText"] != "หยุดสงกรานต์" || f["createdByName"] != "ฝ่ายปฏิบัติการ" ||
		f["createdBy"] != "uid_ops" || f["recipientCount"] != int64(12) || f["recipientGroup"] != "own_fleet" ||
		f["sentAt"] != (dump.Timestamp{Time: sent}) || f["readCount"] != nil || f["readDriverAuthIds"] != nil {
		t.Fatalf("projected broadcast %v", f)
	}
	if v := h.scalar(`SELECT legacy_doc_id FROM broadcasts WHERE id = $1`, own); v != own.String() {
		t.Errorf("minted %v", v)
	}
	// A platform-wide one is projected too; a carrier's is not (the 3.x app lists every document).
	platform := h.id(`INSERT INTO broadcasts (title, message_text, recipient_group, recipient_count) VALUES ('ทุกบริษัท', 'm',
		'all_tenants', 40) RETURNING id`)
	carrier := h.id(`INSERT INTO tenants (kind, name_th, name_en, legal_type) VALUES ('carrier', 'C', 'C', 'company') RETURNING id`)
	carrierBc := h.id(`INSERT INTO broadcasts (tenant_id, title, message_text, recipient_count) VALUES ($1, 'C only', 'm', 3)
		RETURNING id`, carrier)
	if st := h.project(proj, "broadcast.created", `{"id":"`+platform.String()+`"}`); st.Written != 1 {
		t.Fatalf("platform-wide %+v", st)
	}
	if st := h.project(proj, "broadcast.created", `{"id":"`+carrierBc.String()+`"}`); st.Skipped != 1 || st.Written != 0 {
		t.Fatalf("carrier broadcast %+v", st)
	}
	if _, ok := h.fs.Doc("broadcasts/" + carrierBc.String()); ok {
		t.Error("a carrier's broadcast reached Firestore")
	}
	// A broadcast to selected users (recipientUserIds, from P7b) is not projected either: every 3.x user would list it.
	selected := h.id(`INSERT INTO broadcasts (tenant_id, title, message_text, recipient_group, recipient_count)
		VALUES ($1, 'ส่วนตัว', 'ถึงคุณคนเดียว', 'selected', 1) RETURNING id`, ownFleet)
	h.exec(`INSERT INTO broadcast_recipients (broadcast_id, user_id) VALUES ($1, $2)`, selected, sender)
	if st := h.project(proj, "broadcast.created", `{"id":"`+selected.String()+`"}`); st.Skipped != 1 || st.Written != 0 {
		t.Fatalf("selected broadcast %+v", st)
	}
	if _, ok := h.fs.Doc("broadcasts/" + selected.String()); ok {
		t.Error("a broadcast to selected users reached Firestore")
	}
	// The reconciliation matches (the carrier's row is not a projected row).
	docs, err := h.client.Documents(h.ctx, "broadcasts", false)
	if err != nil {
		t.Fatal(err)
	}
	if c := h.reconcileOne("broadcasts", docs, p5); !c.OK {
		t.Fatalf("reconciliation before the void %+v", c)
	}

	// AC: voided (DELETE /v1/broadcasts/{id}): the document disappears, the row stays; a second event and a voided
	// row that never had a document delete nothing.
	h.exec(`UPDATE broadcasts SET voided_at = now() WHERE id = $1 OR legacy_doc_id = 'bc_1'`, own)
	docs, _ = h.client.Documents(h.ctx, "broadcasts", false)
	c := h.reconcileOne("broadcasts", docs, p5)
	if c.OK || !strings.Contains(c.Note, "broadcasts/bc_1 exists") || !strings.Contains(c.Note, "broadcasts/"+own.String()+" exists") {
		t.Fatalf("a voided broadcast's document is not reported: %+v", c)
	}
	if st := h.project(proj, "broadcast.voided", `{"id":"`+own.String()+`"}`); st.Deleted != 1 {
		t.Fatalf("void %+v", st)
	}
	if st := h.project(proj, "broadcast.voided", `{"id":"`+own.String()+`"}`); st.Deleted != 0 || st.Unchanged != 1 {
		t.Fatalf("second void %+v", st)
	}
	if _, ok := h.fs.Doc("broadcasts/" + own.String()); ok {
		t.Fatal("the voided broadcast is still in Firestore")
	}
	if v := h.scalar(`SELECT count(*) FROM broadcasts WHERE id = $1 AND voided_at IS NOT NULL`, own); v != int64(1) {
		t.Fatalf("the row of the voided broadcast %v", v)
	}
	// The catch-up (`etl project`) deletes the document of a row voided while nothing projected (bc_1).
	st, err := proj.Project(h.ctx, ShadowTarget{Collection: "broadcasts", All: true})
	if err != nil || st.Deleted != 1 {
		t.Fatalf("catch-up %+v %v", st, err)
	}
	docs, _ = h.client.Documents(h.ctx, "broadcasts", false)
	var paths []string
	for _, d := range docs {
		paths = append(paths, d.Path)
	}
	if !slices.Equal(paths, []string{"broadcasts/" + platform.String()}) {
		t.Fatalf("documents after the voids %v", paths)
	}
	if c := h.reconcileOne("broadcasts", docs, p5); !c.OK {
		t.Fatalf("reconciliation after the voids %+v", c)
	}

	// A platform-wide broadcast created without a sender's Firebase uid has no createdBy.
	d, _ := h.fs.Doc("broadcasts/" + platform.String())
	if d.Fields["recipientGroup"] != "all_tenants" || d.Fields["recipientCount"] != int64(40) || d.Fields["createdBy"] != nil ||
		d.Fields["readCount"] != nil || d.Fields["readDriverAuthIds"] != nil {
		t.Fatalf("platform-wide document %v", d.Fields)
	}

	// Read receipts (the markBroadcastRead shim, broadcast.read): two readers with a Firebase uid and one born in Go
	// without one reach readCount 3 and the two uids.
	r1 := h.id(`INSERT INTO users (email, display_name, legacy_auth_uid) VALUES ('r1@example.test', 'R1', 'uid_r1') RETURNING id`)
	r2 := h.id(`INSERT INTO users (email, display_name, legacy_auth_uid) VALUES ('r2@example.test', 'R2', 'uid_r2') RETURNING id`)
	r3 := h.id(`INSERT INTO users (email, display_name) VALUES ('r3@example.test', 'R3') RETURNING id`)
	h.exec(`INSERT INTO broadcast_reads (broadcast_id, user_id) VALUES ($1, $2), ($1, $3), ($1, $4)`, platform, r2, r1, r3)
	if st := h.project(proj, "broadcast.read", `{"entity":"broadcast","id":"`+platform.String()+`"}`); st.Written != 1 {
		t.Fatalf("read %+v", st)
	}
	f = h.doc("broadcasts/" + platform.String())
	if f["readCount"] != int64(3) || !slices.Equal(storedStrings(f["readDriverAuthIds"]), []string{"uid_r1", "uid_r2"}) {
		t.Fatalf("readers %v %v", f["readCount"], f["readDriverAuthIds"])
	}
	// A reader the legacy callable added meanwhile (a caller the shim does not forward) is kept and counted when
	// PostgreSQL's next reader arrives; the list is only ever added to.
	h.fs.Merge("broadcasts/"+platform.String(), map[string]any{"readDriverAuthIds": []any{"uid_r1", "uid_r2", "uid_legacy"},
		"readCount": int64(4)})
	r4 := h.id(`INSERT INTO users (email, display_name, legacy_auth_uid) VALUES ('r4@example.test', 'R4', 'uid_r4') RETURNING id`)
	h.exec(`INSERT INTO broadcast_reads (broadcast_id, user_id) VALUES ($1, $2)`, platform, r4)
	if st := h.project(proj, "broadcast.read", `{"id":"`+platform.String()+`"}`); st.Written != 1 {
		t.Fatalf("second read %+v", st)
	}
	f = h.doc("broadcasts/" + platform.String())
	if f["readCount"] != int64(5) || !slices.Equal(storedStrings(f["readDriverAuthIds"]), []string{"uid_r1", "uid_r2", "uid_legacy", "uid_r4"}) {
		t.Fatalf("readers after a legacy read %v %v", f["readCount"], f["readDriverAuthIds"])
	}
	if st := h.project(proj, "broadcast.read", `{"id":"`+platform.String()+`"}`); st.Written != 0 || st.Unchanged != 1 {
		t.Fatalf("a repeated read event rewrote the document %+v", st)
	}
	docs, _ = h.client.Documents(h.ctx, "broadcasts", false)
	if c := h.reconcileOne("broadcasts", docs, p5); !c.OK {
		t.Fatalf("reconciliation after the reads %+v", c)
	}
}

// A send racing its void (the two events reach firestore.shadow's handlers side by side, prefetch 4): the handler of
// broadcast.created encodes the row before the void commits and stalls on Firestore, reading the document (its Get) or
// writing it (its Patch), while the void commits and its handler runs to the end and finds no document to delete.
// Whatever the interleaving, no document of the voided broadcast is left behind (no later event would remove it).
func TestProjectionOfBroadcastsSendRacingItsVoid(t *testing.T) {
	for _, op := range []string{"get", "patch"} {
		t.Run(op, func(t *testing.T) {
			h := newSyncHarness(t)
			p5 := owned("auth", "masterdata", "billing", "hr", "comms")
			proj := h.projector(p5)
			id := h.id(`INSERT INTO broadcasts (tenant_id, title, message_text, recipient_group, recipient_count)
				VALUES ('01900000-0000-7000-8000-000000000001', 't', 'm', 'all_driver', 1) RETURNING id`)

			fs := &stallFS{FirestoreWriter: h.client, op: op, entered: make(chan struct{}), release: make(chan struct{})}
			created := *proj
			created.FS = fs
			var createdErr error
			var createdSt ProjectStats
			done := make(chan struct{})
			go func() {
				defer close(done)
				createdSt, createdErr = created.Project(context.Background(), ShadowTarget{Collection: "broadcasts", IDs: []uuid.UUID{id}})
			}()
			select {
			case <-fs.entered:
			case <-done:
				t.Fatalf("the send's projection did not reach Firestore: %v", createdErr)
			}
			// The void commits (DELETE /v1/broadcasts/{id}) and its handler runs to the end.
			h.exec(`UPDATE broadcasts SET voided_at = now() WHERE id = $1`, id)
			if st := h.project(proj, "broadcast.voided", `{"id":"`+id.String()+`"}`); st.Deleted != 0 {
				t.Fatalf("the void's handler deleted a document the send had not written yet: %+v", st)
			}
			close(fs.release)
			<-done
			if createdErr != nil {
				t.Fatal(createdErr)
			}
			if createdSt.Written != 1 || createdSt.Deleted != 1 {
				t.Fatalf("send %+v: want its write and then its delete", createdSt)
			}
			if _, ok := h.fs.Doc("broadcasts/" + id.String()); ok {
				t.Fatal("the voided broadcast's document is left in Firestore")
			}
			docs, _ := h.client.Documents(h.ctx, "broadcasts", false)
			if c := h.reconcileOne("broadcasts", docs, p5); !c.OK {
				t.Fatalf("reconciliation %+v", c)
			}
		})
	}
}

// A P5 rollback (main spec §12.2: flag off, comms out of PG_OWNED_DOMAINS, so the mirror reads broadcasts again) after
// P5 read receipts and voids written in PostgreSQL: the mirror only adds the readers a document holds (the shim's
// receipts survive, whether or not their broadcast.read was projected), and a broadcast voided during P5, whose
// document the projection deleted, keeps its row (R14), reported instead of removed.
func TestBroadcastsSurviveAP5Rollback(t *testing.T) {
	h := newSyncHarness(t)
	pre := owned("auth", "masterdata", "billing", "hr")
	p5 := owned("auth", "masterdata", "billing", "hr", "comms")
	proj := h.projector(p5)
	h.sweep(h.plan(pre))

	// P5: the catch-up, a broadcast born in PostgreSQL, the shim's reads (one projected, one not yet), a void.
	if _, err := proj.Project(h.ctx, ShadowTarget{Collection: "broadcasts", All: true}); err != nil {
		t.Fatal(err)
	}
	sender := h.id(`INSERT INTO users (email, display_name, legacy_auth_uid) VALUES ('ops@example.test', 'Ops', 'uid_ops') RETURNING id`)
	d1 := h.id(`INSERT INTO users (email, display_name, legacy_auth_uid) VALUES ('d1@example.test', 'D1', 'uid_d1') RETURNING id`)
	d2 := h.id(`INSERT INTO users (email, display_name, legacy_auth_uid) VALUES ('d2@example.test', 'D2', 'uid_d2') RETURNING id`)
	own := h.id(`INSERT INTO broadcasts (tenant_id, created_by_user_id, created_by_name, title, message_text, recipient_group,
		recipient_count, sent_at) VALUES ('01900000-0000-7000-8000-000000000001', $1, 'Ops', 'T', 'M', 'own_fleet', 12, now())
		RETURNING id`, sender)
	if st := h.project(proj, "broadcast.created", `{"id":"`+own.String()+`"}`); st.Written != 1 {
		t.Fatalf("new broadcast %+v", st)
	}
	h.exec(`INSERT INTO broadcast_reads (broadcast_id, user_id) VALUES ($1, $2)`, own, d1)
	if st := h.project(proj, "broadcast.read", `{"id":"`+own.String()+`"}`); st.Written != 1 {
		t.Fatalf("read %+v", st)
	}
	h.exec(`INSERT INTO broadcast_reads (broadcast_id, user_id) VALUES ($1, $2)`, own, d2) // its event not handled yet
	bc1 := h.id(`SELECT id FROM broadcasts WHERE legacy_doc_id = 'bc_1'`)
	h.exec(`INSERT INTO broadcast_reads (broadcast_id, user_id) VALUES ($1, $2)`, bc1, d1)
	h.exec(`UPDATE broadcasts SET voided_at = now() WHERE id = $1`, bc1)
	if st := h.project(proj, "broadcast.voided", `{"id":"`+bc1.String()+`"}`); st.Deleted != 1 {
		t.Fatalf("void %+v", st)
	}

	// The rollback: the mirror reads broadcasts again.
	rep := h.sweep(h.plan(pre))
	var bcStats *CollectionStats
	for i := range rep.Collections {
		if rep.Collections[i].Name == "broadcasts" {
			bcStats = &rep.Collections[i]
		}
	}
	if bcStats == nil || bcStats.Removed != 0 || bcStats.Kept != 1 {
		t.Fatalf("rollback sweep %+v", bcStats)
	}
	if v := h.scalar(`SELECT count(*) FROM broadcast_reads WHERE broadcast_id = $1`, own); v != int64(2) {
		t.Fatalf("the shim's reads after the rollback: %v, want 2", v)
	}
	if v := h.scalar(`SELECT count(*) FROM broadcasts WHERE id = $1 AND voided_at IS NOT NULL`, bc1); v != int64(1) {
		t.Fatalf("the voided row after the rollback: %v", v)
	}
	if v := h.scalar(`SELECT count(*) FROM broadcast_reads WHERE broadcast_id = $1 AND user_id = $2`, bc1, d1); v != int64(1) {
		t.Fatalf("the voided broadcast's reads after the rollback: %v", v)
	}
	if v := h.scalar(`SELECT tenant_id::text FROM broadcasts WHERE id = $1`, own); v != "01900000-0000-7000-8000-000000000001" {
		t.Fatalf("the own fleet's broadcast after the rollback belongs to %v", v)
	}
	// A second sweep changes nothing: the voided row stays, reported again.
	if rep := h.sweep(h.plan(pre)); applied(rep, "broadcasts") != 0 {
		t.Fatalf("second sweep %+v", rep.Collections)
	}
	if v := h.scalar(`SELECT count(*) FROM broadcasts WHERE id = $1`, bc1); v != int64(1) {
		t.Fatalf("the voided row after a second sweep: %v", v)
	}
}

// reconcileOne runs the projection check of one collection and returns its "projection" row.
func (h *syncHarness) reconcileOne(coll string, docs []dump.Doc, o map[string]bool) Check {
	h.t.Helper()
	rec := &Reconciliation{Match: true}
	if err := h.e.cfg.Tx(h.ctx, func(tx pgx.Tx) error { return reconcileProjection(h.ctx, tx, coll, docs, o, rec) }); err != nil {
		h.t.Fatal(err)
	}
	for _, c := range rec.Checks {
		if c.Name == "projection" {
			return c
		}
	}
	h.t.Fatalf("no projection check: %+v", rec.Checks)
	return Check{}
}
