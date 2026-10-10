// Package etl loads a Firestore dump into PostgreSQL 18 (main spec §13, Appendix A §A.3) and runs the ETL's
// other passes: reconcile, export-back, media-copy, rewrite-urls, quarantine list|resolve and status.
//
// The package never opens a transaction policy of its own: cmd/etl hands it a TxFunc that runs every statement as
// logitrack_etl (ETL_DATABASE_URL, BYPASSRLS, R66) inside db.WithSystem with app.etl_load on, so the
// frozen-tenant and file-registry triggers accept ETL writes (Appendix C §C.3.2). One load is one transaction:
// either every collection of the run commits or nothing does, and `--dry-run` rolls the same work back after
// writing its report.
package etl

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// TxFunc runs fn in one transaction as the ETL login (cmd/etl: db.WithSystem + app.etl_load).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Config wires an Engine.
type Config struct {
	Tx TxFunc
	// OwnFleetTenantID is OWN_FLEET_TENANT_ID: the single own-fleet tenant (R56). Required.
	OwnFleetTenantID uuid.UUID
	// Bucket is S3_BUCKET: file_objects.bucket of every registered legacy object (APKs excepted, T24).
	Bucket string
	Log    zerolog.Logger
	// Now is the clock of reports and resolutions (tests pin it).
	Now func() time.Time
}

// Engine runs the ETL passes.
type Engine struct {
	cfg      Config
	ownFleet string
	quar     uuid.UUID
}

// New checks the configuration.
func New(cfg Config) (*Engine, error) {
	if cfg.Tx == nil {
		return nil, errors.New("etl: a transaction function is required")
	}
	if cfg.OwnFleetTenantID == uuid.Nil {
		return nil, errors.New("etl: OWN_FLEET_TENANT_ID is required (R56)")
	}
	if cfg.OwnFleetTenantID.String() == tenancy.QuarantineTenantID {
		return nil, errors.New("etl: OWN_FLEET_TENANT_ID must not be the quarantine tenant")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("etl: S3_BUCKET is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Engine{cfg: cfg, ownFleet: cfg.OwnFleetTenantID.String(), quar: uuid.MustParse(tenancy.QuarantineTenantID)}, nil
}

// AbortError stops a run without committing (exit 2): a billable document would have been rejected (R19), a
// trip number collision needs a manual decision, or the own-fleet configuration disagrees with the database.
type AbortError struct {
	Collection, Path string
	Reason           Reason
	Detail           string
}

func (e *AbortError) Error() string {
	if e.Path == "" {
		return "etl: run aborted: " + e.Detail
	}
	return fmt.Sprintf("etl: run aborted at %s (%s): %s", e.Path, e.Reason, e.Detail)
}

// LoadOptions select what one load applies.
type LoadOptions struct {
	// Collections limits the run (empty: every collection of the dump). Order is always the load order.
	Collections []string
	// DryRun rolls everything back after the report is built.
	DryRun bool
	// Since applies only documents whose _updateTime is after it (a delta load).
	Since time.Time
	// SinceWatermark applies only documents after each collection's etl.watermarks position.
	SinceWatermark bool
	// Force re-applies documents whose _updateTime is not newer than the stored one (after a mapping change).
	Force bool
}

// CollectionStats count one collection of a run.
type CollectionStats struct {
	Name       string
	Mapped     bool
	Total      int // documents in the dump
	Unchanged  int // _updateTime not newer than etl.source_docs (skipped)
	BeforeMark int // filtered out by --since / the watermark
	Outcomes   map[Outcome]int
}

// DocResult is one applied document.
type DocResult struct {
	Collection, Path string
	Outcome          Outcome
	Table, TargetID  string
	TenantSource     string
	Findings         []Finding
}

// Report is the result of a load.
type Report struct {
	DryRun      bool
	ExportedAt  time.Time
	Collections []CollectionStats
	Docs        []DocResult
}

// Applied counts the documents a run wrote.
func (r *Report) Applied() int {
	n := 0
	for _, c := range r.Collections {
		for _, v := range c.Outcomes {
			n += v
		}
	}
	return n
}

// errDryRun rolls a dry run back.
var errDryRun = errors.New("etl: dry run")

// Load applies a dump (main spec §13.2 load, Appendix A §A.3.0).
func (e *Engine) Load(ctx context.Context, d *dump.Dump, o LoadOptions) (*Report, error) {
	rep := &Report{DryRun: o.DryRun, ExportedAt: d.Manifest.ExportedAt}
	for _, name := range o.Collections {
		if _, ok := d.Collection(name); !ok {
			return nil, fmt.Errorf("etl: the dump has no collection %q", name)
		}
	}
	plan := loadPlan(d.Names(), o.Collections)
	billable, err := billableRefs(d)
	if err != nil {
		return nil, err
	}
	err = e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('lt-etl-load'))`); err != nil {
			return fmt.Errorf("etl: lock: %w", err)
		}
		if err := e.ensureOwnFleet(ctx, tx, d); err != nil {
			return err
		}
		for _, spec := range plan {
			docs, err := d.Read(spec.name)
			if err != nil {
				return err
			}
			stats, err := e.loadCollection(ctx, tx, spec, docs, d.Manifest.ExportedAt, o, billable, rep)
			if err != nil {
				return err
			}
			rep.Collections = append(rep.Collections, stats)
		}
		if o.DryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return rep, nil
}

// loadPlan orders the selected collections: mapped ones in the load order of Appendix A §A.3.0, then the rest
// (dropped, unknown, later tasks) in manifest order.
func loadPlan(names, selected []string) []collectionSpec {
	want := func(n string) bool { return len(selected) == 0 || slices.Contains(selected, n) }
	var plan []collectionSpec
	for _, s := range mappedCollections() {
		if slices.Contains(names, s.name) && want(s.name) {
			plan = append(plan, s)
		}
	}
	for _, n := range names {
		if _, mapped := mapperOf(n); !mapped && want(n) {
			plan = append(plan, collectionSpec{name: n})
		}
	}
	return plan
}

func (e *Engine) loadCollection(ctx context.Context, tx pgx.Tx, spec collectionSpec, docs []dump.Doc, exportedAt time.Time,
	o LoadOptions, billable billableSet, rep *Report) (CollectionStats, error) {
	stats := CollectionStats{Name: spec.name, Mapped: spec.mapper != nil, Total: len(docs), Outcomes: map[Outcome]int{}}
	since := o.Since.Truncate(time.Microsecond)
	var mark watermark
	if o.SinceWatermark {
		var err error
		if mark, err = readWatermark(ctx, tx, spec.name); err != nil {
			return stats, err
		}
	}
	var high watermark
	for _, doc := range docs {
		ut := doc.UpdateTime.Truncate(time.Microsecond)
		if (!since.IsZero() && !ut.After(since)) || (o.SinceWatermark && !mark.before(ut, doc.Path)) {
			stats.BeforeMark++
			continue
		}
		if high.before(ut, doc.Path) {
			high = watermark{at: ut, path: doc.Path}
		}
		var storedUT time.Time
		var storedStatus string
		err := tx.QueryRow(ctx, `SELECT source_update_time, status FROM etl.source_docs WHERE collection = $1 AND doc_path = $2`,
			spec.name, doc.Path).Scan(&storedUT, &storedStatus)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return stats, fmt.Errorf("etl: %s: read source doc: %w", doc.Path, err)
		case !o.Force && !ut.After(storedUT) && (storedStatus != string(Pending) || spec.mapper == nil):
			// A pending document waits for its mapping: it is applied by the first load that has one.
			stats.Unchanged++
			continue
		}
		c := &docCtx{ctx: ctx, tx: tx, e: e, coll: spec.name, doc: doc, f: doc.Fields, billable: billable.has(spec.name, doc)}
		if err := e.apply(c, spec); err != nil {
			return stats, err
		}
		if err := e.record(c, exportedAt); err != nil {
			return stats, err
		}
		stats.Outcomes[c.outcome]++
		rep.Docs = append(rep.Docs, DocResult{Collection: spec.name, Path: doc.Path, Outcome: c.outcome, Table: c.table,
			TargetID: c.target, TenantSource: string(c.tenantSource), Findings: c.findings})
	}
	// Deferred link checks (tenant consistency, driver membership) fire per collection, so a failure names it.
	if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		return stats, fmt.Errorf("etl: %s: deferred checks: %w", spec.name, err)
	}
	if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`); err != nil {
		return stats, fmt.Errorf("etl: %s: %w", spec.name, err)
	}
	if !high.at.IsZero() {
		if err := writeWatermark(ctx, tx, spec.name, high); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

// apply runs the mapper of one document, or classifies a collection without one.
func (e *Engine) apply(c *docCtx, spec collectionSpec) error {
	switch {
	case spec.mapper != nil:
		c.outcome = Loaded
		if err := spec.mapper(c); err != nil {
			return err
		}
		if c.outcome == Quarantined && c.tenantSource != tenancy.SourceQuarantine {
			return fmt.Errorf("etl: %s: quarantined without the quarantine tenant", c.doc.Path)
		}
	case droppedCollections[spec.name]:
		c.outcome = Dropped
	case laterCollections[spec.name]:
		c.outcome = Pending
	default:
		c.outcome = Dropped
		c.find("", ReasonUnknownCollection, "collection "+spec.name+" is not in the Appendix A mapping", nil)
	}
	return nil
}

// record writes etl.source_docs (the id map) and replaces the document's open findings.
func (e *Engine) record(c *docCtx, exportedAt time.Time) error {
	raw, err := dump.MarshalFields(c.doc.Fields)
	if err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	var created *time.Time
	if !c.doc.CreateTime.IsZero() {
		t := c.doc.CreateTime.Truncate(time.Microsecond)
		created = &t
	}
	var table, target *string
	if c.table != "" && c.target != "" {
		table, target = &c.table, &c.target
	}
	imported := "now()"
	if c.outcome == Pending {
		imported = "NULL"
	}
	_, err = c.tx.Exec(c.ctx, `INSERT INTO etl.source_docs (collection, doc_path, doc_id, raw, source_create_time, source_update_time,
		exported_at, imported_at, target_table, target_id, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, `+imported+`, $8, $9, $10)
		ON CONFLICT (collection, doc_path) DO UPDATE SET doc_id = EXCLUDED.doc_id, raw = EXCLUDED.raw,
		  source_create_time = EXCLUDED.source_create_time, source_update_time = EXCLUDED.source_update_time,
		  exported_at = EXCLUDED.exported_at, imported_at = EXCLUDED.imported_at,
		  target_table = COALESCE(EXCLUDED.target_table, etl.source_docs.target_table),
		  target_id = COALESCE(EXCLUDED.target_id, etl.source_docs.target_id), status = EXCLUDED.status`,
		c.coll, c.doc.Path, c.doc.ID, raw, created, c.doc.UpdateTime.Truncate(time.Microsecond), exportedAt, table, target, string(c.outcome))
	if err != nil {
		return fmt.Errorf("etl: %s: record source doc: %w", c.doc.Path, err)
	}
	return writeFindings(c.ctx, c.tx, c.coll, c.doc.Path, c.findings)
}

func writeFindings(ctx context.Context, tx pgx.Tx, coll, path string, findings []Finding) error {
	if _, err := tx.Exec(ctx, `DELETE FROM etl.quarantine WHERE collection = $1 AND doc_path = $2 AND resolved_at IS NULL`, coll, path); err != nil {
		return fmt.Errorf("etl: %s: clear findings: %w", path, err)
	}
	for _, f := range findings {
		var field, detail *string
		if f.Field != "" {
			field = &f.Field
		}
		if f.Detail != "" {
			detail = &f.Detail
		}
		var raw any
		if f.Raw != nil {
			raw = rawJSON(f.Raw)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO etl.quarantine (collection, doc_path, field, reason_code, detail, raw_value)
			VALUES ($1, $2, $3, $4, $5, $6)`, coll, path, field, string(f.Reason), detail, raw); err != nil {
			return fmt.Errorf("etl: %s: finding %s: %w", path, f.Reason, err)
		}
	}
	return nil
}

// watermark is a position in a collection: update time, then document path (collection-group safe tie-break).
type watermark struct {
	at   time.Time
	path string
}

// before reports whether the watermark is before (at, path).
func (w watermark) before(at time.Time, path string) bool {
	if w.at.IsZero() {
		return true
	}
	return at.After(w.at) || (at.Equal(w.at) && path > w.path)
}

func readWatermark(ctx context.Context, tx pgx.Tx, coll string) (watermark, error) {
	var w watermark
	var p *string
	err := tx.QueryRow(ctx, `SELECT last_update_time, last_doc_path FROM etl.watermarks WHERE collection = $1`, coll).Scan(&w.at, &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return watermark{}, nil
	}
	if p != nil {
		w.path = *p
	}
	if err != nil {
		return w, fmt.Errorf("etl: watermark %s: %w", coll, err)
	}
	return w, nil
}

// writeWatermark advances etl.watermarks to w when w is later than the stored position.
func writeWatermark(ctx context.Context, tx pgx.Tx, coll string, w watermark) error {
	_, err := tx.Exec(ctx, `INSERT INTO etl.watermarks AS m (collection, last_update_time, last_doc_path) VALUES ($1, $2, $3)
		ON CONFLICT (collection) DO UPDATE SET last_update_time = EXCLUDED.last_update_time, last_doc_path = EXCLUDED.last_doc_path,
		  updated_at = now()
		WHERE (EXCLUDED.last_update_time, EXCLUDED.last_doc_path COLLATE "C") > (m.last_update_time, coalesce(m.last_doc_path, '') COLLATE "C")`,
		coll, w.at, w.path)
	if err != nil {
		return fmt.Errorf("etl: watermark %s: %w", coll, err)
	}
	return nil
}

// ensureOwnFleet creates the own-fleet tenant with OWN_FLEET_TENANT_ID (R56) and refuses a database whose
// own-fleet row has another id.
func (e *Engine) ensureOwnFleet(ctx context.Context, tx pgx.Tx, d *dump.Dump) error {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM tenants WHERE kind = 'own_fleet'`).Scan(&id)
	switch {
	case err == nil && id == e.cfg.OwnFleetTenantID:
		return nil
	case err == nil:
		return &AbortError{Detail: "the database's own-fleet tenant differs from OWN_FLEET_TENANT_ID"}
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("etl: read the own-fleet tenant: %w", err)
	}
	legacy := ownFleetLegacyID(d)
	var legacyID *string
	if legacy != "" {
		legacyID = &legacy
	}
	_, err = tx.Exec(ctx, `INSERT INTO tenants (id, kind, legacy_doc_id, name_th, name_en) VALUES ($1, 'own_fleet', $2, 'Own fleet', 'Own fleet')`,
		e.cfg.OwnFleetTenantID, legacyID)
	if err != nil {
		return fmt.Errorf("etl: create the own-fleet tenant: %w", err)
	}
	e.cfg.Log.Info().Msg("created the own-fleet tenant from OWN_FLEET_TENANT_ID; its profile comes from the subcontractors doc settings/tenancy names, or is edited later")
	return nil
}

// ownFleetLegacyID is settings/tenancy.ownFleetTenantId (a subcontractors doc id; tenantLookups.ts:12-21) when the
// dump holds that document.
func ownFleetLegacyID(d *dump.Dump) string {
	if _, ok := d.Collection("settings"); !ok {
		return ""
	}
	docs, err := d.Read("settings")
	if err != nil {
		return ""
	}
	for _, doc := range docs {
		if doc.Path == "settings/tenancy" {
			s, _ := doc.Fields["ownFleetTenantId"].(string)
			return strings.TrimSpace(s)
		}
	}
	return ""
}
