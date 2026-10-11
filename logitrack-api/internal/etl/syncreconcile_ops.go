package etl

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/tripcheck"
)

// The daily reconciliation of the operations mirror (main spec §13.10, T29): on top of "every document recorded at its
// current update time", each of the four operations collections mirrored whole compares, document by document, the
// columns that decide the boards, the monitor and billing with what the load transforms read from the document now
// (ops_fields), and reports the rows whose document Firestore no longer has (ops_rows_without_document, informational:
// the legacy app never deletes them but through a rename, which the mirror applies; anything else is a console delete
// or a trip renamed twice between two runs). A field that carries an open finding (an unknown literal keeps
// PostgreSQL's value, a trip number held by another document) is not compared; a document not mirrored yet or held is
// left to the mirror check.
//
// The listeners keep applying while the reconciliation runs (it reads Firestore first, then compares, without the load
// lock, which would stall them), so a row may have moved past the snapshot it is compared with. A document is
// compared only with the row of its own version: one statement reads, per document, the version etl.source_docs
// records, its open findings and the compared columns, in one snapshot of PostgreSQL (a listener's transaction writes
// the version and the row together), and a document whose recorded version is no longer the snapshot's is skipped
// (moved on since the read, counted in the note), never reported as a difference.

// opsRow is the compared columns of one row, as text (times in RFC 3339, UTC), keyed by document field.
type opsRow map[string]string

// opsCompare names, per collection, the query of the compared columns (by legacy_doc_id) and how a document gives them.
var opsCompare = map[string]struct {
	query string
	want  func(c *docCtx) opsRow
}{
	"tasks": {
		query: `SELECT legacy_doc_id, status, task_type, plan_at, task_no FROM tasks WHERE legacy_doc_id = ANY($1)`,
		want: func(c *docCtx) opsRow {
			status, _ := c.vocab("status", "pending", "assigned", "checked_in", "in_transit", "completed", "cancelled")
			taskType, _ := c.vocab("taskType", "first_mile", "line_haul")
			return opsRow{"status": status, "taskType": taskType, "date": timeText(c.ts("date")), "taskId": textOf(c.text("taskId"))}
		},
	},
	"trip_records": {
		query: `SELECT legacy_doc_id, status, trip_no, delivered_at FROM trip_records WHERE legacy_doc_id = ANY($1)`,
		want: func(c *docCtx) opsRow {
			status, _ := c.vocab("status", "in_transit", "incident", "delivered", "standby", "cancelled")
			no := c.doc.ID
			if spx := c.str("spxTripId"); spx != "" && spx != c.doc.ID && tripcheck.ValidateTripNo(spx) == "" {
				no = spx
			}
			return opsRow{"status": status, "spxTripId": no, "deliveredTimestamp": timeText(c.ts("deliveredTimestamp"))}
		},
	},
	"standby_records": {
		query: `SELECT legacy_doc_id, started_at, ended_at FROM standby_records WHERE legacy_doc_id = ANY($1)`,
		want: func(c *docCtx) opsRow {
			return opsRow{"startedAt": timeText(c.ts("startedAt")), "endedAt": timeText(c.ts("endedAt"))}
		},
	},
	"incidentReport": {
		query: `SELECT i.legacy_doc_id, i.delay_cause, i.description,
			  CASE WHEN i.trip_id IS NULL THEN NULL ELSE concat_ws('|', t.legacy_doc_id, t.trip_no,
			    (SELECT string_agg(h.old_trip_no, '|') FROM trip_no_history h WHERE h.trip_id = t.id)) END
			FROM incident_reports i LEFT JOIN trip_records t ON t.id = i.trip_id WHERE i.legacy_doc_id = ANY($1)`,
		want: func(c *docCtx) opsRow {
			return opsRow{"delayCause": textOf(c.text("delayCause")), "description": textOf(c.text("description"))}
		},
	},
}

// opsColumns are the document fields of the compared columns, in the query's order after legacy_doc_id.
var opsColumns = map[string][]string{
	"tasks":           {"status", "taskType", "date", "taskId"},
	"trip_records":    {"status", "spxTripId", "deliveredTimestamp"},
	"standby_records": {"startedAt", "endedAt"},
	"incidentReport":  {"delayCause", "description", "tripId"},
}

func timeText(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func textOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// reconcileOps runs the two operations checks of one collection; at is the read time of docs (zero: unknown).
func reconcileOps(ctx context.Context, tx pgx.Tx, coll string, docs []dump.Doc, at time.Time, rec *Reconciliation) error {
	cmp, ok := opsCompare[coll]
	if !ok {
		return nil
	}
	table := opsTables[coll].table
	byPath := make(map[string]dump.Doc, len(docs))
	ids := make([]string, 0, len(docs))
	paths := make([]string, 0, len(docs))
	for _, d := range docs {
		byPath[d.Path] = d
		ids = append(ids, d.ID)
		paths = append(paths, d.Path)
	}
	cols := opsColumns[coll]
	// cmp.query reads the rows by legacy_doc_id ($1); the four collections are top-level, so a row's document path is
	// the collection and its legacy_doc_id. Tombstones, rejected, pending and unrecorded documents are not returned:
	// the mirror check reports them.
	rows, err := tx.Query(ctx, `SELECT s.doc_path, s.source_update_time,
		  coalesce((SELECT array_agg(DISTINCT split_part(f.field, '.', 1)) FROM etl.quarantine f
		    WHERE f.collection = s.collection AND f.doc_path = s.doc_path AND f.resolved_at IS NULL AND f.field IS NOT NULL), '{}'),
		  x.*
		FROM etl.source_docs s LEFT JOIN (`+cmp.query+`) x ON s.doc_path = $2::text || '/' || x.legacy_doc_id
		WHERE s.collection = $2::text AND s.doc_path = ANY($3::text[]) AND s.status IN ('loaded', 'quarantined')
		ORDER BY s.doc_path`, ids, coll, paths)
	if err != nil {
		return fmt.Errorf("etl: reconcile-sync %s: %w", coll, err)
	}
	var missing, differ []string
	compared, moved := 0, 0
	for rows.Next() {
		var path string
		var version time.Time
		var skip []string
		var id *string
		raw := make([]any, len(cols))
		vals := []any{&path, &version, &skip, &id}
		for i := range cols {
			raw[i] = new(any)
			vals = append(vals, raw[i])
		}
		if err := rows.Scan(vals...); err != nil {
			rows.Close()
			return fmt.Errorf("etl: reconcile-sync %s: %w", coll, err)
		}
		d := byPath[path]
		switch ut := d.UpdateTime.Truncate(time.Microsecond); {
		case version.After(ut):
			moved++ // a newer version was applied since the read: its row is not this document's
			continue
		case version.Before(ut):
			continue // not mirrored yet: the mirror check reports it
		}
		compared++
		if id == nil {
			missing = append(missing, path)
			continue
		}
		row := opsRow{}
		for i, c := range cols {
			switch v := (*raw[i].(*any)).(type) {
			case nil:
				row[c] = ""
			case time.Time:
				row[c] = v.UTC().Format(time.RFC3339Nano)
			default:
				row[c] = fmt.Sprint(v)
			}
		}
		c := &docCtx{ctx: ctx, f: d.Fields, doc: d}
		want := cmp.want(c)
		var bad []string
		for _, f := range cols {
			if slices.Contains(skip, f) {
				continue
			}
			switch {
			case f == "tripId":
				// The incident's trip, when linked, is the one its tripId names (document id, number or a former
				// number, as loadIncident resolves it): an incident re-pointed by a rename follows its trip.
				ref := strings.TrimSpace(c.str("tripId"))
				if row[f] != "" && ref != "" && !slices.Contains(strings.Split(row[f], "|"), ref) {
					bad = append(bad, f)
				}
			case want[f] != row[f]:
				bad = append(bad, f)
			}
		}
		if len(bad) > 0 {
			differ = append(differ, path+" ("+strings.Join(bad, ",")+")")
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("etl: reconcile-sync %s: %w", coll, err)
	}
	note := "the boards', monitor's and billing columns equal the document"
	var notes []string
	if len(missing) > 0 {
		notes = append(notes, "no row: "+listed(missing))
	}
	if len(differ) > 0 {
		notes = append(notes, "differ: "+listed(differ))
	}
	if len(notes) > 0 {
		note = strings.Join(notes, "; ")
	}
	if moved > 0 {
		note += fmt.Sprintf(" (%d moved on since the read, not compared)", moved)
	}
	rec.add("ops_fields", coll, compared, compared-len(missing)-len(differ), len(missing)+len(differ) == 0, note)

	// Rows whose document Firestore no longer has; a row whose document was recorded after the read (created since,
	// or renamed since by a listener) is not one.
	var readAt *time.Time
	if !at.IsZero() {
		t := at.UTC()
		readAt = &t
	}
	rows, err = tx.Query(ctx, `SELECT x.legacy_doc_id FROM `+pgx.Identifier{table}.Sanitize()+` x
		WHERE x.legacy_doc_id IS NOT NULL AND x.legacy_doc_id <> ALL($1::text[])
		  AND ($3::timestamptz IS NULL OR NOT EXISTS (SELECT 1 FROM etl.source_docs s WHERE s.collection = $2::text
		      AND s.doc_path = $2::text || '/' || x.legacy_doc_id AND s.source_update_time > $3))`, ids, coll, readAt)
	if err != nil {
		return err
	}
	legacy, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	orphans := make([]string, 0, len(legacy))
	for _, id := range legacy {
		orphans = append(orphans, coll+"/"+id)
	}
	if len(orphans) > 0 {
		rec.add("ops_rows_without_document", coll, 0, len(orphans), true,
			"rows kept although Firestore no longer has their document (never deleted: a console delete, or a trip renamed twice between two runs): "+listed(orphans))
	}
	return nil
}
