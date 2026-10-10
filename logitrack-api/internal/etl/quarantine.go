package etl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// `etl quarantine list|resolve` (main spec §13.2): findings are listed with filters and resolved per document:
// retry re-applies the document from etl.source_docs.raw (after a fix in a dependency, or a mapping change; a row
// already outside the quarantine tenant keeps its tenant, main spec §13.5), skip accepts the findings as they are,
// and rehome moves a quarantined row with its children and their files to a tenant through tenancy.Rehome, the
// service of POST /v1/tenants/quarantine/rows/{table}/{id}/rehome, appends tenant_rehomed in the same transaction
// and settles only the tenant_unresolved finding of each moved document: its field findings (driver, trip number,
// hub, ...) stay open, since the move fixed the tenant and nothing else.

// FindingRow is one etl.quarantine row.
type FindingRow struct {
	ID                         uuid.UUID
	Collection, DocPath, Field string
	Reason, Detail, Resolution string
	CreatedAt                  time.Time
	ResolvedAt                 *time.Time
}

// FindingFilter narrows a listing.
type FindingFilter struct {
	Collection, Reason string
	OpenOnly           bool
}

// ListFindings lists findings ordered by collection, document and field.
func (e *Engine) ListFindings(ctx context.Context, f FindingFilter) ([]FindingRow, error) {
	var out []FindingRow
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, collection, doc_path, coalesce(field, ''), reason_code, coalesce(detail, ''),
			  coalesce(resolution, ''), created_at, resolved_at
			FROM etl.quarantine
			WHERE ($1 = '' OR collection = $1) AND ($2 = '' OR reason_code = $2) AND (NOT $3 OR resolved_at IS NULL)
			ORDER BY collection, doc_path, field NULLS FIRST, reason_code, id`, f.Collection, f.Reason, f.OpenOnly)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (FindingRow, error) {
			var x FindingRow
			err := r.Scan(&x.ID, &x.Collection, &x.DocPath, &x.Field, &x.Reason, &x.Detail, &x.Resolution, &x.CreatedAt, &x.ResolvedAt)
			return x, err
		})
		return err
	})
	return out, err
}

// Resolve actions.
const (
	ActionRetry  = "retry"
	ActionSkip   = "skip"
	ActionRehome = "rehome"
)

// ResolveOptions name one document and what to do with its open findings.
type ResolveOptions struct {
	Collection, DocPath string
	Action              string
	Tenant              uuid.UUID // rehome target
	By                  string    // operator label stored in resolved_by
}

// ResolveResult reports what a resolution did.
type ResolveResult struct {
	Resolved int
	Outcome  Outcome         // retry: the new outcome
	Moved    []tenancy.Moved // rehome: the row, its children and their files (Table tenancy.FileObjects)
}

// rehomeEvent is the tenant_rehomed audit record of a CLI re-home (Appendix C §C.4.13). The CLI has no user
// principal: the actor stays NULL and the operator label of --by goes into details.by; the endpoint of T28 writes
// the same details with the platform admin as actor.
func rehomeEvent(table string, id uuid.UUID, o ResolveOptions, moved []tenancy.Moved, at time.Time) security.Event {
	rows := make([]map[string]string, 0, len(moved))
	for _, m := range moved {
		rows = append(rows, map[string]string{"table": m.Table, "id": m.ID.String()})
	}
	to := o.Tenant
	return security.Event{
		EventType: "tenant_rehomed", Severity: security.SeverityInfo,
		Summary:  fmt.Sprintf("%s %s re-homed from the quarantine tenant with %d moved row(s)", table, id, len(moved)),
		TenantID: &to, OccurredAt: at,
		Details: map[string]any{"table": table, "id": id.String(), "fromTenantId": tenancy.QuarantineTenantID,
			"toTenantId": to.String(), "moved": rows, "by": o.By, "via": "etl quarantine resolve",
			"collection": o.Collection, "docPath": o.DocPath},
	}
}

// ErrNoDocument: the document is not in etl.source_docs.
var ErrNoDocument = errors.New("etl: no such document in etl.source_docs")

// Resolve resolves the open findings of one document.
func (e *Engine) Resolve(ctx context.Context, o ResolveOptions) (*ResolveResult, error) {
	if o.By == "" {
		o.By = "etl-cli"
	}
	res := &ResolveResult{}
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		var raw []byte
		var created *time.Time
		var updated, exported time.Time
		var docID, status string
		var table, target *string
		err := tx.QueryRow(ctx, `SELECT doc_id, raw, source_create_time, source_update_time, exported_at, status, target_table, target_id
			FROM etl.source_docs WHERE collection = $1 AND doc_path = $2 FOR UPDATE`, o.Collection, o.DocPath).
			Scan(&docID, &raw, &created, &updated, &exported, &status, &table, &target)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoDocument
		}
		if err != nil {
			return err
		}
		resolve := func(resolution, coll, path string) (int, error) {
			tag, err := tx.Exec(ctx, `UPDATE etl.quarantine SET resolved_at = now(), resolved_by = $3, resolution = $4
				WHERE collection = $1 AND doc_path = $2 AND resolved_at IS NULL`, coll, path, o.By, resolution)
			return int(tag.RowsAffected()), err
		}
		switch o.Action {
		case ActionSkip:
			res.Resolved, err = resolve("skipped", o.Collection, o.DocPath)
			return err
		case ActionRetry:
			spec := collectionSpec{name: o.Collection}
			spec.mapper, _ = mapperOf(o.Collection)
			if spec.mapper == nil {
				return fmt.Errorf("etl: %s has no mapping in this release; nothing to retry", o.Collection)
			}
			fields, err := dump.UnmarshalFields(raw)
			if err != nil {
				return err
			}
			if res.Resolved, err = resolve("retried", o.Collection, o.DocPath); err != nil {
				return err
			}
			doc := dump.Doc{ID: docID, Path: o.DocPath, UpdateTime: updated, Fields: fields}
			if created != nil {
				doc.CreateTime = *created
			}
			c := &docCtx{ctx: ctx, tx: tx, e: e, coll: o.Collection, doc: doc, f: fields, billable: hasBillingEvidence(o.Collection, fields)}
			if err := e.apply(c, spec); err != nil {
				return err
			}
			if err := e.record(c, exported); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
				return err
			}
			res.Outcome = c.outcome
			return nil
		case ActionRehome:
			if status != string(Quarantined) || table == nil || target == nil {
				return fmt.Errorf("etl: %s is %s, not a quarantined row", o.DocPath, status)
			}
			id, err := uuid.Parse(*target)
			if err != nil {
				return err
			}
			moved, err := tenancy.Rehome(ctx, tx, *table, id, o.Tenant)
			if err != nil {
				return err
			}
			res.Moved = moved
			// No audit, no change (R22, Appendix C §C.4.13): the event commits or rolls back with the move.
			if err := security.Append(ctx, tx, rehomeEvent(*table, id, o, moved, e.cfg.Now())); err != nil {
				return err
			}
			if res.Resolved, err = settleMoved(ctx, tx, moved, o.By, "rehomed"); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`)
			return err
		default:
			return fmt.Errorf("etl: --action must be retry, skip or rehome")
		}
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// CollectionStatus is one line of `etl status`.
type CollectionStatus struct {
	Collection   string
	Watermark    *time.Time
	LastDocPath  string
	Lag          time.Duration // now - watermark
	Counts       map[string]int
	OpenFindings int
}

// Status reports watermarks, lag, outcome counts and open findings per collection (main spec §13.2).
func (e *Engine) Status(ctx context.Context) ([]CollectionStatus, error) {
	byName := map[string]*CollectionStatus{}
	get := func(n string) *CollectionStatus {
		if s, ok := byName[n]; ok {
			return s
		}
		s := &CollectionStatus{Collection: n, Counts: map[string]int{}}
		byName[n] = s
		return s
	}
	now := e.cfg.Now()
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT collection, status, count(*) FROM etl.source_docs GROUP BY 1, 2`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c, s string
			var n int
			if err := rows.Scan(&c, &s, &n); err != nil {
				return err
			}
			get(c).Counts[s] = n
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT collection, last_update_time, coalesce(last_doc_path, '') FROM etl.watermarks`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c, p string
			var t time.Time
			if err := rows.Scan(&c, &t, &p); err != nil {
				return err
			}
			s := get(c)
			s.Watermark, s.LastDocPath, s.Lag = &t, p, now.Sub(t)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT collection, count(*) FROM etl.quarantine WHERE resolved_at IS NULL GROUP BY 1`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c string
			var n int
			if err := rows.Scan(&c, &n); err != nil {
				return err
			}
			get(c).OpenFindings = n
		}
		return rows.Err()
	})
	out := make([]CollectionStatus, 0, len(byName))
	for _, n := range sortedKeys(func() map[string]bool {
		m := map[string]bool{}
		for k := range byName {
			m[k] = true
		}
		return m
	}()) {
		out = append(out, *byName[n])
	}
	return out, err
}
