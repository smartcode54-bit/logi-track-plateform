package etl

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// Removed documents (main spec §13.7 "Removed applied only where legacy deletes"; T29). A sweep reads whole
// collections, so a document etl.source_docs holds (loaded or quarantined) that the snapshot lacks was deleted in
// Firestore. Only where the legacy app deletes documents does the mirror remove the row; everywhere else (the
// operations collections, whose only legacy delete is the rename, handled by renamedFrom, and every collection the
// legacy app never deletes from) the row stays and reconcile-sync reports the document deleted_at_source.
//
// Legacy deletes and what the mirror does with them:
//   - billing_statements: the web deletes draft statements only (lib/billingStatement.ts:277); a draft row is deleted
//     (trg_billing_statement_draft_only_delete refuses any other), a sent or paid one is kept and reported;
//   - customer_service_fees (features/accounting/api/billing.ts:592), holidays (functions/src/holidays.ts:136, deleteHoliday),
//     chats/*/messages (named by main spec §13.7; no current writer deletes one, a console delete is honoured), broadcasts (BroadcastComposer.tsx:178, BroadcastHistoryModal.tsx:80) and waitlist
//     (waitlist/page.tsx:46): the row is deleted (their children cascade);
//   - standby_rate_entries (billing.ts:690): kept and reported: the table is soft-delete only and a void needs the
//     staff member who voided it (voided_by), which a Firestore delete does not carry (R20).
//
// A removal that the database refuses (a row another table still references) keeps the row, reported like the rest. A
// removed row's etl.source_docs entry becomes a 'removed' tombstone at the sweep's read time (0018, settleGone) and its
// open findings are resolved fixed_at_source: a sweep that read the collection before the delete and commits after this
// one (a hand-run `etl mirror` beside the worker's) holds a copy that is not newer, so it cannot bring the row back. A
// collection read with no document at all removes nothing (an empty read of a collection PostgreSQL holds rows for is
// far likelier a wrong project or a permission problem than a mass delete).
//
// A sweep judges only the documents its snapshot can see: an entry whose recorded version is newer than the sweep's
// read time was created (or written) after that read, by a run that read later and committed first, so its absence
// from this snapshot is no delete.
var removable = map[string]string{
	"billing_statements":    "billing_statements",
	"customer_service_fees": "customer_service_fees",
	"holidays":              "holidays",
	"messages":              "chat_messages",
	"broadcasts":            "broadcasts",
	"waitlist":              "waitlist",
}

// mirrorRemoved settles the documents of one swept collection that Firestore no longer has. at is the sweep's
// Firestore read time (zero: a source without one, a dump, whose snapshot judges every entry).
func (e *Engine) mirrorRemoved(ctx context.Context, r *runState, coll string, docs []dump.Doc, at time.Time, stats *CollectionStats) error {
	tx := r.tx
	var readAt *time.Time
	if !at.IsZero() {
		t := at.UTC()
		readAt = &t
	}
	rows, err := tx.Query(ctx, `SELECT doc_path, target_table, target_id FROM etl.source_docs
		WHERE collection = $1 AND status IN ('loaded', 'quarantined') AND ($2::timestamptz IS NULL OR source_update_time <= $2)
		ORDER BY doc_path`, coll, readAt)
	if err != nil {
		return fmt.Errorf("etl: %s: removed documents: %w", coll, err)
	}
	type gone struct{ path, table, target string }
	var stored []gone
	for rows.Next() {
		var g gone
		var table, target *string
		if err := rows.Scan(&g.path, &table, &target); err != nil {
			rows.Close()
			return fmt.Errorf("etl: %s: removed documents: %w", coll, err)
		}
		if table != nil && target != nil {
			g.table, g.target = *table, *target
		}
		stored = append(stored, g)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("etl: %s: removed documents: %w", coll, err)
	}
	present := make(map[string]bool, len(docs))
	for _, d := range docs {
		present[d.Path] = true
	}
	table, deletes := removable[coll]
	for _, g := range stored {
		if present[g.path] {
			continue
		}
		if !deletes || len(docs) == 0 || g.table != table || g.target == "" {
			stats.Kept++
			continue
		}
		removed, err := removeRow(ctx, tx, table, g.target)
		if err != nil {
			return fmt.Errorf("etl: %s: remove the row of %s: %w", coll, g.path, err)
		}
		if !removed {
			stats.Kept++
			continue
		}
		keep, err := r.tombstones()
		if err != nil {
			return err
		}
		if err := settleGone(ctx, tx, keep, coll, g.path, path.Base(g.path), StatusRemoved, at, "etl-sync"); err != nil {
			return fmt.Errorf("etl: %s: %w", coll, err)
		}
		stats.Removed++
	}
	return nil
}

// removeRow deletes one row in a savepoint: false when the row is gone already, a statement is no longer a draft, or
// the database refuses the delete (a reference, a trigger), which keeps the row.
func removeRow(ctx context.Context, tx pgx.Tx, table, id string) (bool, error) {
	if table == "billing_statements" {
		var status string
		err := tx.QueryRow(ctx, `SELECT status FROM billing_statements WHERE id = $1`, id).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && status != "draft") {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT etl_mirror_remove`); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM `+pgx.Identifier{table}.Sanitize()+` WHERE id = $1`, id)
	if err == nil {
		// Deferred references are checked here, inside the savepoint, not at the sweep's commit.
		if _, err = tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err == nil {
			_, err = tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`)
		}
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if _, rerr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT etl_mirror_remove`); rerr != nil {
			return false, rerr
		}
		if _, rerr := tx.Exec(ctx, `RELEASE SAVEPOINT etl_mirror_remove`); rerr != nil {
			return false, rerr
		}
		_, rerr := tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`)
		return false, rerr
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT etl_mirror_remove`); err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
