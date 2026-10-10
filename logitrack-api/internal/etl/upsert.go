package etl

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// row is one INSERT built from legacy columns. Identifiers come only from the mappers (constants), never from
// document data; values are always bind parameters.
type row struct {
	table string
	cols  []string
	vals  []any
}

func newRow(table string) *row { return &row{table: table} }

// set adds a column (nil or a typed nil pointer is SQL NULL).
func (r *row) set(col string, v any) *row {
	if i := slices.Index(r.cols, col); i >= 0 {
		r.vals[i] = v
		return r
	}
	r.cols = append(r.cols, col)
	r.vals = append(r.vals, v)
	return r
}

// setPresent adds a column only when v is not nil, so a NOT NULL column with a default keeps it (insert) or its
// current value (update) when the legacy document has no value.
func (r *row) setPresent(col string, v any) *row {
	if isNil(v) {
		return r
	}
	return r.set(col, v)
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

func (r *row) insertSQL() string {
	ph := make([]string, len(r.cols))
	idents := make([]string, len(r.cols))
	for i, c := range r.cols {
		ph[i] = "$" + strconv.Itoa(i+1)
		idents[i] = pgx.Identifier{c}.Sanitize()
	}
	return "INSERT INTO " + pgx.Identifier{r.table}.Sanitize() + " (" + strings.Join(idents, ", ") + ") VALUES (" + strings.Join(ph, ", ") + ")"
}

// legacyConflict is the conflict target of every table keyed by legacy_doc_id (partial UNIQUE).
const legacyConflict = "(legacy_doc_id) WHERE legacy_doc_id IS NOT NULL"

// upsert inserts the row or updates the existing one on conflict, and returns its id. keyCols are the conflict
// columns (left unchanged on update).
func upsert(ctx context.Context, tx pgx.Tx, r *row, conflict string, keyCols ...string) (uuid.UUID, error) {
	var sets []string
	for _, c := range r.cols {
		if slices.Contains(keyCols, c) {
			continue
		}
		q := pgx.Identifier{c}.Sanitize()
		sets = append(sets, q+" = EXCLUDED."+q)
	}
	sql := r.insertSQL() + " ON CONFLICT " + conflict
	if len(sets) == 0 {
		sql += " DO NOTHING"
	} else {
		sql += " DO UPDATE SET " + strings.Join(sets, ", ")
	}
	sql += " RETURNING id"
	var id uuid.UUID
	if err := tx.QueryRow(ctx, sql, r.vals...).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("etl: upsert %s: %w", r.table, err)
	}
	return id, nil
}

// rowID is the id of the row behind this document: the existing row's, or a new uuidv7 (time-ordered like the
// column default) so files can be registered with their owner before the single INSERT that references them; an
// UPDATE would let trg_set_updated_at replace the legacy updated_at.
func (c *docCtx) rowID(table string) (uuid.UUID, error) {
	var id uuid.UUID
	err := c.tx.QueryRow(c.ctx, `SELECT id FROM `+pgx.Identifier{table}.Sanitize()+` WHERE legacy_doc_id = $1`, c.doc.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.NewV7()
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	return id, nil
}

// upsertLegacy upserts by legacy_doc_id; id (from rowID) is kept on update.
func upsertLegacy(c *docCtx, r *row) (uuid.UUID, error) {
	r.set("legacy_doc_id", c.doc.ID)
	id, err := upsert(c.ctx, c.tx, r, legacyConflict, "legacy_doc_id", "id")
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s: %w", c.doc.Path, err)
	}
	return id, nil
}

// insert writes a row without conflict handling.
func insert(ctx context.Context, tx pgx.Tx, r *row) error {
	if _, err := tx.Exec(ctx, r.insertSQL(), r.vals...); err != nil {
		return fmt.Errorf("etl: insert %s: %w", r.table, err)
	}
	return nil
}
