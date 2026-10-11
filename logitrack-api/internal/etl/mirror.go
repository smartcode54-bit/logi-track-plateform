package etl

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// The FS->PG mirror (developer-spec.md §13.7, R25, R71): one etl.sync sweep reads the collections of a MirrorPlan
// from Firestore and applies them through the load transforms of `load`, in the load order, in one transaction:
// a document is applied only when its _updateTime is newer than the one etl.source_docs holds (or it waits pending),
// documents before the collection's watermark minus the 10-minute look-back are not even compared, and the
// watermarks advance as a load's do. The worker's etl.sync consumer runs it every ETL_SYNC_INTERVAL
// (internal/coexist); `etl mirror` runs one sweep by hand. Unlike a load, one document never stops a sweep: each
// applies in a savepoint (mirrorApply); an unknown literal keeps PostgreSQL's value of its field
// (status_out_of_vocab, §13.8), and a billable document that still cannot be applied (R19) is held: rolled back,
// recorded pending with its finding (it is retried by every sweep until it is fixed at the source), counted Held and
// reported by reconcile-sync, while the rest of the sweep commits.
//
// PostgreSQL-owned class B collections are swept in MirrorFields mode: drivers apply only activeTruck (the driver's
// active_* columns) and fcmToken (a legacy device_tokens row), and keep currentTruckId in etl.source_docs; hubs apply
// only the documents drivers created (createdByDriver) that PostgreSQL does not own. A document PostgreSQL owns is
// recorded and left alone, so the projection's own writes never re-enter PostgreSQL (no echo loop).

// DocSource reads the current documents of a collection: the Firestore client (gcp.Firestore), or a dump.
type DocSource interface {
	// Documents reads every document of a top-level collection, or of every collection with that id when group is
	// true (drivers/*/mobile_installations, chats/*/messages).
	Documents(ctx context.Context, collection string, group bool) ([]dump.Doc, error)
}

// DumpSource serves a dump as a DocSource; a collection the dump lacks has no documents.
type DumpSource struct{ Dump *dump.Dump }

// Documents implements DocSource.
func (s DumpSource) Documents(_ context.Context, collection string, _ bool) ([]dump.Doc, error) {
	if _, ok := s.Dump.Collection(collection); !ok {
		return nil, nil
	}
	return s.Dump.Read(collection)
}

// OwnFleet reads the own-fleet tenant (R56); ok false when the database has none yet (before the P1 load).
func OwnFleet(ctx context.Context, tx pgx.Tx) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM tenants WHERE kind = 'own_fleet'`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("etl: read the own-fleet tenant: %w", err)
	}
	return id, true, nil
}

// requireOwnFleet refuses a sweep on a database without the P1 load or with another own fleet.
func (e *Engine) requireOwnFleet(ctx context.Context, tx pgx.Tx) error {
	id, ok, err := OwnFleet(ctx, tx)
	switch {
	case err != nil:
		return err
	case !ok:
		return &AbortError{Detail: "the database has no own-fleet tenant: the mirror runs after the P1 initial load (etl load)"}
	case id != e.cfg.OwnFleetTenantID:
		return &AbortError{Detail: "the database's own-fleet tenant differs from the one the mirror was given"}
	}
	return nil
}

// mirrorSpec is the collection spec of one entry of a sweep: the load spec in MirrorFull mode, the field-level mapper
// of a PostgreSQL-owned class B collection in MirrorFields mode.
func mirrorSpec(m MirrorEntry) (collectionSpec, error) {
	switch m.Mode {
	case MirrorFull:
		if s, ok := specOf(m.Name); ok {
			return s, nil
		}
	case MirrorFields:
		switch m.Name {
		case "drivers":
			return collectionSpec{name: "drivers", mapper: mirrorDriverFields, rowNow: rowByLegacyID("drivers")}, nil
		case "hubs":
			return collectionSpec{name: "hubs", mapper: mirrorHubFields, prepare: prepareHubs, reapply: hubTakesOver,
				rowNow: rowByLegacyID("hubs")}, nil
		case "trucks":
			return collectionSpec{name: "trucks", mapper: mirrorTruckFields, rowNow: rowByLegacyID("trucks")}, nil
		case "users":
			return collectionSpec{name: "users", mapper: mirrorUserFields, rowNow: func(ctx context.Context, tx pgx.Tx, doc dump.Doc) (bool, error) {
				var found bool
				err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE legacy_auth_uid = $1)`, doc.ID).Scan(&found)
				return found, err
			}}, nil
		}
	}
	return collectionSpec{}, fmt.Errorf("etl: mirror: no %s mapping for %s", m.Mode, m.Name)
}

// mirrorApply applies one document of a sweep in a savepoint (the sweep is one transaction, and a document must not
// take the others down with it):
//   - a document rejected (or, carrying billing evidence, aborted, R19) on an out-of-vocabulary literal of a field is
//     applied again with that field as the version of the document last applied holds it (etl.source_docs raw): the
//     field keeps PostgreSQL's value and gets a status_out_of_vocab finding with the literal, the rest of the document
//     applies (§13.6, §13.8: an unknown literal from the mirror never overwrites PostgreSQL). The version recorded is
//     the one applied; the literal is the finding's raw value. A document never applied before has no such version
//     and stays rejected (a NOT NULL column has no value to keep);
//   - a billable document that still cannot be applied is rolled back and returned: the caller holds it (hold).
func (e *Engine) mirrorApply(c *docCtx, spec collectionSpec) (*AbortError, error) {
	if _, err := c.tx.Exec(c.ctx, `SAVEPOINT etl_mirror_doc`); err != nil {
		return nil, fmt.Errorf("etl: %s: savepoint: %w", c.doc.Path, err)
	}
	rollback := func() error {
		_, err := c.tx.Exec(c.ctx, `ROLLBACK TO SAVEPOINT etl_mirror_doc`)
		return err
	}
	release := func() error {
		_, err := c.tx.Exec(c.ctx, `RELEASE SAVEPOINT etl_mirror_doc`)
		return err
	}
	orig := c.doc
	var abort *AbortError
	err := e.apply(c, spec)
	switch {
	case errors.As(err, &abort) && abort.Path == c.doc.Path:
	case err != nil:
		return nil, err
	}
	field, literal := outOfVocab(c, abort)
	if field != "" {
		prev, found, err := appliedField(c, field)
		if err != nil {
			return nil, err
		}
		if found {
			if err := rollback(); err != nil {
				return nil, err
			}
			fields := maps.Clone(c.doc.Fields)
			if prev == nil {
				delete(fields, field)
			} else {
				fields[field] = prev
			}
			doc := c.doc
			doc.Fields = fields
			again := &docCtx{ctx: c.ctx, tx: c.tx, e: c.e, coll: c.coll, doc: doc, f: fields, billable: c.billable, run: c.run}
			abort = nil
			err := e.apply(again, spec)
			switch {
			case errors.As(err, &abort) && abort.Path == c.doc.Path:
			case err != nil:
				return nil, err
			}
			*c = *again
			switch {
			case abort == nil && c.outcome != Rejected:
				c.find(field, ReasonStatusOutOfVocab, "an unknown literal from the mirror: the field keeps PostgreSQL's value", literal)
			case abort == nil:
				c.doc, c.f = orig, orig.Fields // rejected for another reason: recorded as Firestore has it
			}
		}
	}
	if abort != nil {
		if err := rollback(); err != nil {
			return nil, err
		}
		c.doc, c.f = orig, orig.Fields // held as Firestore has it
		return abort, release()
	}
	return nil, release()
}

// outOfVocab is the field and literal of a document rejected or aborted on status_out_of_vocab ("" otherwise).
func outOfVocab(c *docCtx, abort *AbortError) (string, any) {
	if abort != nil {
		if abort.Reason == ReasonStatusOutOfVocab && abort.Field != "" && !strings.Contains(abort.Field, ".") {
			return abort.Field, c.get(abort.Field)
		}
		return "", nil
	}
	if c.outcome != Rejected {
		return "", nil
	}
	for _, f := range c.findings {
		if f.Reason == ReasonStatusOutOfVocab && f.Field != "" && !strings.Contains(f.Field, ".") {
			return f.Field, c.get(f.Field)
		}
	}
	return "", nil
}

// appliedField is field in the version of the document a load or a sweep last applied (etl.source_docs raw of a
// loaded or quarantined document): nil when that version had no such field; found false when the document was never
// applied.
func appliedField(c *docCtx, field string) (any, bool, error) {
	var raw []byte
	err := c.tx.QueryRow(c.ctx, `SELECT raw FROM etl.source_docs WHERE collection = $1 AND doc_path = $2 AND status IN ('loaded', 'quarantined')`,
		c.coll, c.doc.Path).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("etl: %s: the applied document: %w", c.doc.Path, err)
	}
	prev, err := dump.UnmarshalFields(raw)
	if err != nil {
		return nil, false, fmt.Errorf("etl: %s: the applied document: %w", c.doc.Path, err)
	}
	return prev[field], true, nil
}

// hold records a billable document the sweep could not apply: pending (every sweep retries it, the watermark never
// passes it), its open findings replaced by the one that holds it, its row (if any) as the last applied version left it.
func (e *Engine) hold(c *docCtx, abort *AbortError, exportedAt time.Time) error {
	c.outcome, c.table, c.target, c.findingScope = Pending, "", "", nil
	c.findings = []Finding{{Field: abort.Field, Reason: abort.Reason, Detail: "held by the mirror, retried by every sweep until it is fixed " +
		"at the source (R19): " + abort.Detail, Raw: rawOf(c, abort.Field)}}
	return e.record(c, exportedAt)
}

func rawOf(c *docCtx, field string) any {
	if field == "" {
		return nil
	}
	return c.get(field)
}

// rowByLegacyID is the rowNow of a field-level mirror: a row of table holds the document's id. table is a constant.
func rowByLegacyID(table string) func(ctx context.Context, tx pgx.Tx, doc dump.Doc) (bool, error) {
	return func(ctx context.Context, tx pgx.Tx, doc dump.Doc) (bool, error) {
		var found bool
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+pgx.Identifier{table}.Sanitize()+` WHERE legacy_doc_id = $1)`, doc.ID).Scan(&found)
		return found, err
	}
}

// Mirror runs one sweep of plan from src (main spec §13.7). The documents are read first, each collection as one
// consistent snapshot (gcp.Firestore.Documents), then applied in one transaction under the load lock, so a sweep and
// an `etl load` never interleave.
func (e *Engine) Mirror(ctx context.Context, src DocSource, plan MirrorPlan) (*Report, error) {
	read := e.cfg.Now().UTC().Truncate(time.Microsecond)
	docs := make(map[string][]dump.Doc, len(plan.Entries))
	specs := make([]collectionSpec, len(plan.Entries))
	for i, m := range plan.Entries {
		spec, err := mirrorSpec(m)
		if err != nil {
			return nil, err
		}
		specs[i] = spec
		ds, err := src.Documents(ctx, m.Name, groupCollections[m.Name])
		if err != nil {
			return nil, fmt.Errorf("etl: mirror: read %s: %w", m.Name, err)
		}
		docs[m.Name] = ds
	}
	billable := billableFrom(docs["trip_records"])
	rep := &Report{ExportedAt: read}
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('lt-etl-load'))`); err != nil {
			return fmt.Errorf("etl: lock: %w", err)
		}
		if err := e.requireOwnFleet(ctx, tx); err != nil {
			return err
		}
		if _, err := e.settleRehomed(ctx, tx); err != nil {
			return err
		}
		r := e.newRun(ctx, tx, rep)
		r.owned = plan.Owned
		for i, m := range plan.Entries {
			stats, err := e.loadCollection(ctx, tx, specs[i], docs[m.Name], read, LoadOptions{SinceWatermark: true, Mirror: true}, billable, rep, r)
			if err != nil {
				return err
			}
			stats.Mode = m.Mode
			rep.Collections = append(rep.Collections, stats)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rep, nil
}
