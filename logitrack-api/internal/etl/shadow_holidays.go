package etl

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
)

// The holidays projection (T45, issue #65; main spec §12.1 class B, §13.8): from P4 PostgreSQL writes the holidays
// (internal/hr) and firestore.shadow copies them to the Firestore holidays collection, which the installed 3.x APKs
// stream by year (holiday_repository.dart) and the legacy payroll callable reads (driverCompensation.ts:108-115) until
// P7b. Firestore has one calendar and no tenant: the public calendar (tenant_id NULL) and the own fleet's company and
// other holidays are projected (loadHoliday reads COMPANY / OTHER as the own fleet's), a carrier's own holidays are not
// (Skip). A holiday deleted in PostgreSQL deletes its document (deleteHoliday deleted it, holidays.ts:124): the event
// carries the document ids of the deleted rows (ShadowTarget.DeletedDocIDs), and a row that vanished while it was
// being projected has its minted document deleted again, so a create racing a delete leaves no document behind. A
// delete whose event was never projected (COMPAT_PROJECTION_ENABLED off, a worker whose plan did not project holidays
// yet, a dead-lettered message) is caught up by the All pass (`etl project`, deleteLeftBehind), and the reconciliation
// reports its document until then (projection_deleted, deletedDocs).

// deletable are the projected collections whose rows PostgreSQL deletes.
var deletable = map[string]bool{"holidays": true}

// encodeHolidays: holidays -> holidays/{id} (§A.3.10 inverse, loadHoliday): date as the Timestamp the legacy callables
// stored (new Date("yyyy-MM-dd"): UTC midnight of the day, on the same Bangkok day; any stored instant or yyyy-MM-dd of
// the day is kept), type and status upper case, the names and descriptions, isRecurring, createdBy (the creator's
// Firebase uid when it has one), createdAt, updatedAt.
func encodeHolidays(ctx context.Context, tx pgx.Tx, sel selection, mint bool) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT h.id, h.legacy_doc_id, (h.tenant_id IS NULL OR t.kind = 'own_fleet') AS projectable,
		  h.holiday_date, h.holiday_type, h.status, h.name, h.name_en, h.name_th, h.description, h.description_en,
		  h.description_th, h.is_recurring, u.legacy_auth_uid, h.created_at, h.updated_at
		FROM holidays h LEFT JOIN tenants t ON t.id = h.tenant_id LEFT JOIN users u ON u.id = h.created_by
		WHERE $1 OR h.id = ANY($2) ORDER BY h.id`, sel.all, sel.ids)
	if err != nil {
		return nil, fmt.Errorf("etl: encode holidays: %w", err)
	}
	type holiday struct {
		id                                               uuid.UUID
		legacy, nameEn, nameTh, desc, descEn, descTh, by *string
		projectable, recurring                           bool
		day                                              time.Time
		typ, status, name                                string
		created, updated                                 time.Time
	}
	var hs []holiday
	for rows.Next() {
		var h holiday
		if err := rows.Scan(&h.id, &h.legacy, &h.projectable, &h.day, &h.typ, &h.status, &h.name, &h.nameEn, &h.nameTh, &h.desc,
			&h.descEn, &h.descTh, &h.recurring, &h.by, &h.created, &h.updated); err != nil {
			rows.Close()
			return nil, err
		}
		hs = append(hs, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []ExportDoc
	for _, h := range hs {
		if !h.projectable {
			out = append(out, newDoc("holidays/"+deref(h.legacy, h.id.String())).
				skip("a carrier's own holiday: Firestore holds the public calendar and the own fleet's").doc())
			continue
		}
		docID, err := mintID(ctx, tx, "holidays", h.id, h.legacy, mint, &h.updated)
		if err != nil {
			return nil, err
		}
		day := time.Date(h.day.Year(), h.day.Month(), h.day.Day(), 0, 0, 0, 0, time.UTC)
		typ, status := h.typ, h.status
		b := newDoc("holidays/"+docID).
			set("date", dump.Timestamp{Time: day}).eq("date", sameDay(day)).
			set("type", strings.ToUpper(typ)).eq("type", sameCanon(typ, "public", nil)).
			set("status", strings.ToUpper(status)).eq("status", sameCanon(status, "draft", nil)).
			set("name", h.name).
			set("holidayNameEN", h.nameEn).set("holidayNameTH", h.nameTh).
			set("description", h.desc).set("descriptionEn", h.descEn).set("descriptionTh", h.descTh).
			set("isRecurring", h.recurring).eq("isRecurring", sameBool(h.recurring, false)).
			uid("createdBy", h.by).
			created(h.created).set("updatedAt", h.updated)
		out = append(out, b.doc())
	}
	return out, nil
}

// deleteDocs deletes the documents docIDs of coll that no row holds (by legacy_doc_id or id::text): a row of the id is
// still in PostgreSQL (a document id an earlier row freed and a later one took cannot happen, ids are never reused,
// but the check keeps the projection from ever deleting a live row's document). Invalid ids are skipped.
func (p *Projector) deleteDocs(ctx context.Context, coll string, docIDs []string) (int, error) {
	ids := slices.DeleteFunc(slices.Compact(slices.Sorted(slices.Values(docIDs))), func(id string) bool { return !validDocID(id) })
	if len(ids) == 0 {
		return 0, nil
	}
	table, idCol := projectedTable(coll)
	var live []string
	if err := p.Tx(ctx, func(tx pgx.Tx) error {
		t := pgx.Identifier{table}.Sanitize()
		rows, err := tx.Query(ctx, `SELECT legacy_doc_id FROM `+t+` WHERE legacy_doc_id = ANY($1)
			UNION SELECT `+idCol+`::text FROM `+t+` WHERE `+idCol+`::text = ANY($1)`, ids)
		if err != nil {
			return err
		}
		live, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	}); err != nil {
		return 0, fmt.Errorf("etl: project %s: deleted rows: %w", coll, err)
	}
	n := 0
	for _, id := range ids {
		if slices.Contains(live, id) {
			continue
		}
		if err := p.FS.Delete(ctx, coll+"/"+id, gcp.Precondition{}); err != nil {
			return n, fmt.Errorf("etl: project %s/%s: delete: %w", coll, id, err)
		}
		n++
	}
	return n, nil
}

// vanished are the ids of sel whose rows no longer exist (deleted while they were being projected): their minted
// document ids (id::text) are deleted again.
func (p *Projector) vanished(ctx context.Context, coll string, ids []uuid.UUID) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	table, idCol := projectedTable(coll)
	var found []uuid.UUID
	if err := p.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+idCol+` FROM `+pgx.Identifier{table}.Sanitize()+` WHERE `+idCol+` = ANY($1)`, ids)
		if err != nil {
			return err
		}
		found, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	}); err != nil {
		return nil, fmt.Errorf("etl: project %s: re-read: %w", coll, err)
	}
	var out []string
	for _, id := range ids {
		if !slices.Contains(found, id) {
			out = append(out, id.String())
		}
	}
	return out, nil
}

// deleteLeftBehind is the delete pass of the All projection of a deletable collection (the catch-up, ProjectAll): the
// collection is listed after the rows were projected and the documents rows deleted in PostgreSQL left behind
// (deletedDocs) are deleted, as the events of those deletes would have. deleteDocs checks again, right before, that no
// row holds an id, and a row born in PostgreSQL commits before its document is written, so a row created meanwhile is
// always seen.
func (p *Projector) deleteLeftBehind(ctx context.Context, coll string) (int, error) {
	docs, err := p.FS.Documents(ctx, coll, false)
	if err != nil {
		return 0, fmt.Errorf("etl: project %s: list: %w", coll, err)
	}
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	var gone []string
	if err := p.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		gone, err = deletedDocs(ctx, tx, coll, ids)
		return err
	}); err != nil {
		return 0, fmt.Errorf("etl: project %s: deleted rows: %w", coll, err)
	}
	return p.deleteDocs(ctx, coll, gone)
}

// deletedDocs are the ids of docIDs (documents of the deletable collection coll) that rows deleted in PostgreSQL left
// behind: no row holds the id (legacy_doc_id or id::text), and the id is either one PostgreSQL minted (a row's
// id::text, a canonical uuid; legacy documents carry Firestore auto-ids) or a legacy document the load put into a row
// since deleted (etl.source_docs loaded, its target row gone). The other documents no row projects to (a duplicate, a
// rejected or quarantined document the load did not put into a row, a document Firestore got after the cut-over) are
// not PostgreSQL's to delete: they stay, reported. It reads etl.source_docs, so it runs on the ETL login (`etl
// project`, the reconciliation), where the All pass runs.
func deletedDocs(ctx context.Context, tx pgx.Tx, coll string, docIDs []string) ([]string, error) {
	if len(docIDs) == 0 {
		return nil, nil
	}
	minted := []string{}
	for _, id := range docIDs {
		if u, err := uuid.Parse(id); err == nil && u.String() == id {
			minted = append(minted, id)
		}
	}
	table, idCol := projectedTable(coll)
	t := pgx.Identifier{table}.Sanitize()
	rows, err := tx.Query(ctx, `SELECT d FROM unnest($1::text[]) AS d
		WHERE NOT EXISTS (SELECT 1 FROM `+t+` r WHERE r.legacy_doc_id = d OR r.`+idCol+`::text = d)
		  AND (d = ANY($4::text[]) OR EXISTS (SELECT 1 FROM etl.source_docs sd WHERE sd.collection = $2 AND sd.doc_path = $2 || '/' || d
		    AND sd.status = 'loaded' AND sd.target_table = $3 AND NOT EXISTS (SELECT 1 FROM `+t+` r WHERE r.`+idCol+`::text = sd.target_id)))
		ORDER BY 1`, docIDs, coll, table, minted)
	if err != nil {
		return nil, fmt.Errorf("etl: %s: documents of deleted rows: %w", coll, err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
