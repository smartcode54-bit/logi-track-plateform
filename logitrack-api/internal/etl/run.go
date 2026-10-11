package etl

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// runState is what one load (or one `quarantine resolve --action=retry`) shares across its documents and collections.
type runState struct {
	ctx context.Context
	tx  pgx.Tx
	e   *Engine
	rep *Report
	// applied are the documents this run applied (loaded or quarantined), per collection: the after-hooks finish
	// only their links, so a delta load never rewrites a link of a row it did not touch (rows are PostgreSQL-owned
	// from their cut-over; only a document Firestore changed is re-applied, main spec §13.7).
	applied map[string][]dump.Doc
	// prep holds a collection's prepare result (the winning hub per source_id).
	prep map[string]any
	// d5 caches whether a D5 unique index of 0010_d5_unique_constraints exists.
	d5 map[string]bool
	// owned are the PostgreSQL-owned domains of a mirror sweep (nil for a load): the field mappers apply the
	// ForeignFields of it.
	owned map[string]bool
	// present are the document paths of each collection as the source holds it now, for runs that read whole
	// collections (a load, a sweep); a run that reads only changes (the listeners) asks lookup instead, and a run
	// with neither (a retry) cannot tell (T29: the rename check of loadTrip).
	present map[string]map[string]bool
	// lookup says whether a document exists at the run's read time (the listeners, gcp.Firestore.Lookup).
	lookup func(path string) (bool, error)
	// events is set on mirror runs: the operations collections emit their domain events (mirror_events.go, T29).
	events bool
}

// exists reports whether the source holds the document at path (of collection coll) now; known is false when the run
// cannot tell.
func (r *runState) exists(coll, path string) (found, known bool, err error) {
	if set, ok := r.present[coll]; ok {
		return set[path], true, nil
	}
	if r.lookup != nil {
		found, err := r.lookup(path)
		return found, err == nil, err
	}
	return false, false, nil
}

func (e *Engine) newRun(ctx context.Context, tx pgx.Tx, rep *Report) *runState {
	if rep == nil {
		rep = &Report{}
	}
	return &runState{ctx: ctx, tx: tx, e: e, rep: rep, applied: map[string][]dump.Doc{}, prep: map[string]any{}, d5: map[string]bool{},
		present: map[string]map[string]bool{}}
}

// runAfterHooks runs every collection's after-hook in load order for the documents of r (quarantine resolve
// --action=retry: the one document it re-applied). Each hook touches only the links of the documents the run applied,
// so the hooks of unrelated collections do nothing.
func (e *Engine) runAfterHooks(r *runState) error {
	for _, s := range mappedCollections() {
		if s.after == nil {
			continue
		}
		if err := s.after(r, s.name); err != nil {
			return fmt.Errorf("etl: %s: %w", s.name, err)
		}
	}
	return nil
}

// The three unique indexes of 0010_d5_unique_constraints (Appendix A §A.4 D5).
const (
	d5FuelTaxInv      = "vehicle_expenses_fuel_taxinv"
	d5OpenChat        = "chats_one_open_per_driver"
	d5ServiceFeeTypes = "customer_service_fees_one_per_type"
)

// d5Enforced reports whether 0010 created the D5 unique index (valid or not: an INVALID unique index left by a failed
// build still refuses duplicate inserts). Before 0010 a legacy duplicate loads unchanged and is reported for the owner's
// sign-off; afterwards the duplicate of a later delta load (or of the mirror) is quarantined on the field instead of
// failing the whole run on the index (the runbook of main spec §13.9, R59, R88).
func (r *runState) d5Enforced(index string) (bool, error) {
	if v, ok := r.d5[index]; ok {
		return v, nil
	}
	var v bool
	if err := r.tx.QueryRow(r.ctx, `SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname = $1 AND relkind = 'i')`, index).Scan(&v); err != nil {
		return false, fmt.Errorf("etl: %s: %w", index, err)
	}
	r.d5[index] = v
	return v, nil
}

// extraFinding writes an open finding on another collection's document, once (a rerun or a second hook leaves an
// open finding of the same field and reason as it is), and adds it to the run's report.
func (r *runState) extraFinding(coll, path string, f Finding) error {
	var field *string
	if f.Field != "" {
		field = &f.Field
	}
	var raw any
	if f.Raw != nil {
		raw = rawJSON(f.Raw)
	}
	tag, err := r.tx.Exec(r.ctx, `INSERT INTO etl.quarantine (collection, doc_path, field, reason_code, detail, raw_value)
		SELECT $1, $2, $3, $4, $5, $6
		WHERE NOT EXISTS (SELECT 1 FROM etl.quarantine WHERE collection = $1 AND doc_path = $2 AND field IS NOT DISTINCT FROM $3
		  AND reason_code = $4 AND resolved_at IS NULL)`, coll, path, field, string(f.Reason), f.Detail, raw)
	if err != nil {
		return fmt.Errorf("etl: %s: finding %s: %w", path, f.Reason, err)
	}
	if tag.RowsAffected() > 0 {
		r.rep.Extra = append(r.rep.Extra, ExtraFinding{Collection: coll, Path: path, Finding: f})
	}
	return nil
}

// flagHolder raises a D5 finding on the other document of a duplicate pair (the one loaded first), so the owner's
// report lists every member of the pair (Appendix D §D.5.2); a holder created in PostgreSQL (no legacy doc) has no
// document to flag.
func (c *docCtx) flagHolder(holderDocID string, f Finding) error {
	if holderDocID == "" || c.run == nil {
		return nil
	}
	return c.run.extraFinding(c.coll, c.coll+"/"+holderDocID, f)
}

// settleRehomed brings the ETL bookkeeping up to date with re-homes done through the API (POST
// /v1/tenants/quarantine/rows/{table}/{id}/rehome): the api login cannot reach schema etl (Appendix C §C.3.2), so the
// endpoint moves the rows and writes tenant_rehomed, and the next ETL command that reads the bookkeeping (load,
// reconcile, status, quarantine list) marks the documents behind rows that left the quarantine tenant loaded and
// resolves their row-level tenant_unresolved finding as rehomed, by the actor of the event that moved them (a
// re-home through `etl quarantine resolve` settles in its own transaction and leaves nothing here). It returns the
// number of documents settled.
func (e *Engine) settleRehomed(ctx context.Context, tx pgx.Tx) (int, error) {
	rows, err := tx.Query(ctx, `SELECT collection, doc_path, target_table, target_id FROM etl.source_docs
		WHERE status = 'quarantined' AND target_table = ANY($1::text[]) ORDER BY collection, doc_path`, tenancy.RehomeTables())
	if err != nil {
		return 0, fmt.Errorf("etl: quarantined documents: %w", err)
	}
	type doc struct{ coll, path, table, target string }
	var docs []doc
	for rows.Next() {
		var d doc
		if err := rows.Scan(&d.coll, &d.path, &d.table, &d.target); err != nil {
			rows.Close()
			return 0, err
		}
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range docs {
		var moved bool
		// The table name comes from tenancy.RehomeTables (the ANY filter above), never from document data.
		err := tx.QueryRow(ctx, `SELECT tenant_id <> app_quarantine_tenant_id() FROM `+pgx.Identifier{d.table}.Sanitize()+
			` WHERE id = $1::uuid`, d.target).Scan(&moved)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !moved) {
			continue
		}
		if err != nil {
			return n, fmt.Errorf("etl: %s: %w", d.path, err)
		}
		by := "api"
		var actor *string
		err = tx.QueryRow(ctx, `SELECT coalesce(actor_email, actor_user_id::text) FROM security_events
			WHERE event_type = 'tenant_rehomed' AND details -> 'moved' @> jsonb_build_array(jsonb_build_object('table', $1::text, 'id', $2::text))
			ORDER BY created_at DESC, id DESC LIMIT 1`, d.table, d.target).Scan(&actor)
		switch {
		case err == nil && actor != nil:
			by = "api:" + *actor
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return n, fmt.Errorf("etl: %s: the re-home event: %w", d.path, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE etl.source_docs SET status = 'loaded' WHERE collection = $1 AND doc_path = $2`, d.coll, d.path); err != nil {
			return n, fmt.Errorf("etl: %s: %w", d.path, err)
		}
		if _, err := resolveTenantFinding(ctx, tx, d.coll, d.path, by, "rehomed"); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
