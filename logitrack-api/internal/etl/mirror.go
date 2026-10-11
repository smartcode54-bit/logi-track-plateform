package etl

import (
	"context"
	"errors"
	"fmt"
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
// (internal/coexist); `etl mirror` runs one sweep by hand. As a load, a billable document that cannot be applied stops
// the sweep (R19, AbortError) until it is fixed at the source, and nothing of the sweep is committed.
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
			return collectionSpec{name: "drivers", mapper: mirrorDriverFields}, nil
		case "hubs":
			return collectionSpec{name: "hubs", mapper: mirrorHubFields, prepare: prepareHubs, reapply: hubTakesOver}, nil
		case "trucks":
			return collectionSpec{name: "trucks", mapper: mirrorTruckFields}, nil
		}
	}
	return collectionSpec{}, fmt.Errorf("etl: mirror: no %s mapping for %s", m.Mode, m.Name)
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
			stats, err := e.loadCollection(ctx, tx, specs[i], docs[m.Name], read, LoadOptions{SinceWatermark: true}, billable, rep, r)
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
