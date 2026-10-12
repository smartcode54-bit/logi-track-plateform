//go:build integration

// The broadcasts projection (T48, issue #68) on postgres:18-alpine against the in-process Firestore stand-in: the loaded
// fixture broadcast projects unchanged; broadcasts written in PostgreSQL reach Firestore in the legacy shape the 3.x app
// lists; a carrier's broadcast stays out; AC: a voided broadcast disappears from the
// projection (its document is deleted on broadcast.voided, the row stays) and the reconciliation reports a voided
// broadcast's document until then; the projection runs only once comms is PostgreSQL-owned.
package etl

import (
	"slices"
	"strings"
	"testing"
	"time"

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

	// The fixture's broadcast (bc_1, no title, one reader) says what PostgreSQL says: nothing is written, and its
	// readCount and readDriverAuthIds stay as they are (outside the mask).
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
	if d.Fields["recipientGroup"] != "all_tenants" || d.Fields["recipientCount"] != int64(40) || d.Fields["createdBy"] != nil {
		t.Fatalf("platform-wide document %v", d.Fields)
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
