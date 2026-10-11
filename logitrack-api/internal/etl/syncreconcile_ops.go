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

// reconcileOps runs the two operations checks of one collection.
func reconcileOps(ctx context.Context, tx pgx.Tx, coll string, docs []dump.Doc, rec *Reconciliation) error {
	cmp, ok := opsCompare[coll]
	if !ok {
		return nil
	}
	table := opsTables[coll].table
	// The documents that were applied at their current version and whose row the comparison reads.
	rows, err := tx.Query(ctx, `SELECT doc_path, source_update_time FROM etl.source_docs
		WHERE collection = $1 AND status IN ('loaded', 'quarantined')`, coll)
	if err != nil {
		return err
	}
	applied := map[string]time.Time{}
	for rows.Next() {
		var p string
		var t time.Time
		if err := rows.Scan(&p, &t); err != nil {
			rows.Close()
			return err
		}
		applied[p] = t
	}
	if err := rows.Err(); err != nil {
		return err
	}
	findings := map[string]map[string]bool{}
	rows, err = tx.Query(ctx, `SELECT doc_path, split_part(field, '.', 1) FROM etl.quarantine
		WHERE collection = $1 AND resolved_at IS NULL AND field IS NOT NULL`, coll)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p, f string
		if err := rows.Scan(&p, &f); err != nil {
			rows.Close()
			return err
		}
		if findings[p] == nil {
			findings[p] = map[string]bool{}
		}
		findings[p][f] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var ids []string
	byID := map[string]dump.Doc{}
	for _, d := range docs {
		if t, ok := applied[d.Path]; ok && !t.Before(d.UpdateTime.Truncate(time.Microsecond)) {
			ids = append(ids, d.ID)
			byID[d.ID] = d
		}
	}
	got := map[string]opsRow{}
	if len(ids) > 0 {
		rows, err = tx.Query(ctx, cmp.query, ids)
		if err != nil {
			return fmt.Errorf("etl: reconcile-sync %s: %w", coll, err)
		}
		cols := opsColumns[coll]
		for rows.Next() {
			vals := make([]any, len(cols)+1)
			var id string
			vals[0] = &id
			raw := make([]any, len(cols))
			for i := range cols {
				raw[i] = new(any)
				vals[i+1] = raw[i]
			}
			if err := rows.Scan(vals...); err != nil {
				rows.Close()
				return fmt.Errorf("etl: reconcile-sync %s: %w", coll, err)
			}
			r := opsRow{}
			for i, c := range cols {
				switch v := (*raw[i].(*any)).(type) {
				case nil:
					r[c] = ""
				case time.Time:
					r[c] = v.UTC().Format(time.RFC3339Nano)
				default:
					r[c] = fmt.Sprint(v)
				}
			}
			got[id] = r
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("etl: reconcile-sync %s: %w", coll, err)
		}
	}
	var missing, differ []string
	for _, id := range ids {
		d := byID[id]
		row, ok := got[id]
		if !ok {
			missing = append(missing, d.Path)
			continue
		}
		c := &docCtx{ctx: ctx, f: d.Fields, doc: d}
		want := cmp.want(c)
		var bad []string
		for _, f := range opsColumns[coll] {
			if findings[d.Path][f] {
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
			differ = append(differ, d.Path+" ("+strings.Join(bad, ",")+")")
		}
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
	rec.add("ops_fields", coll, len(ids), len(ids)-len(missing)-len(differ), len(missing)+len(differ) == 0, note)

	// Rows whose document Firestore no longer has.
	inSource := make(map[string]bool, len(docs))
	for _, d := range docs {
		inSource[d.ID] = true
	}
	rows, err = tx.Query(ctx, `SELECT legacy_doc_id FROM `+pgx.Identifier{table}.Sanitize()+` WHERE legacy_doc_id IS NOT NULL`)
	if err != nil {
		return err
	}
	legacy, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	var orphans []string
	for _, id := range legacy {
		if !inSource[id] {
			orphans = append(orphans, coll+"/"+id)
		}
	}
	if len(orphans) > 0 {
		rec.add("ops_rows_without_document", coll, 0, len(orphans), true,
			"rows kept although Firestore no longer has their document (never deleted: a console delete, or a trip renamed twice between two runs): "+listed(orphans))
	}
	return nil
}
