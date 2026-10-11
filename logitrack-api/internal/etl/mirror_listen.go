package etl

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// The listeners of the FS->PG mirror (main spec §13.7, T29). The Firestore REST API has no streaming listen (the
// Listen RPC is gRPC only, and §13.1 keeps the ETL on the REST client), so the worker's listener polls each
// operations collection with a query on the timestamp field every writer of it keeps (ListenFields): the documents
// whose field is at or after the collection's cursor minus ListenOverlap, at one read time for all of them, applied
// in one transaction through the load transforms like a sweep's (savepoints, the update-time guard, the domain events,
// the rename). A listener never moves etl.watermarks and never removes rows: the sweep, which reads whole collections
// every ETL_SYNC_INTERVAL, stays the catch-up for anything a listener cannot see (a write that kept the field, a
// field written as something else than a timestamp, a document it deferred, a delete). A listener restarted after a
// gap starts from the collection's watermark minus ListenOverlap, so a gap loses nothing (and the sweep would apply it
// anyway).

// ListenFields is the timestamp field a listener polls per collection: the 3.x app, the web and the Cloud Functions
// set updatedAt on every write of these collections (delivery_trip_repository.dart, checkin_repository.dart,
// standby_repository.dart, incident_report_repository.dart). Which collections keep it reliably, and how far the
// writers' clocks drift from Firestore's, is measured on the first production dump (§13.7 UNVERIFIED, hitl): a write
// without it waits for the sweep.
var ListenFields = map[string]string{
	"tasks":           "updatedAt",
	"trip_records":    "updatedAt",
	"standby_records": "updatedAt",
	"incidentReport":  "updatedAt",
}

// ListenOverlap is how far before its cursor a listener reads again. updatedAt is the writer's clock, not the commit
// time: the 3.x app writes Timestamp.fromDate(DateTime.now()) on trip_records (delivery_trip_repository.dart),
// standby_records (standby_repository.dart), incidentReport (incident_report_repository.dart) and tasks
// (checkin_repository.dart), the web new Date(). A clock behind by up to this much, or two writes whose field order
// differs from their commit order, are still seen; a clock behind by more is applied by the sweep (up to
// ETL_SYNC_INTERVAL later). A clock ahead never moves a cursor past the poll's read time (MirrorChanges), so one phone
// dated ahead cannot blind the listener to the correctly-clocked writes behind it. The update-time guard skips what was
// applied already.
const ListenOverlap = time.Minute

// ChangeSource is what a listener reads (gcp.Firestore; gcptest in tests).
type ChangeSource interface {
	// Changed reads the documents of a top-level collection whose field is at or after since, at the read time at (zero:
	// now), and returns the read time used.
	Changed(ctx context.Context, collection, field string, since, at time.Time) ([]dump.Doc, time.Time, error)
	// Lookup reads one document at the read time at.
	Lookup(ctx context.Context, path string, at time.Time) (dump.Doc, bool, error)
}

// ListenCollections are the collections of plan a listener polls: the operations collections mirrored whole.
func ListenCollections(plan MirrorPlan) []string {
	var out []string
	for _, c := range OpsCollections {
		if plan.Mode(c) == MirrorFull {
			out = append(out, c)
		}
	}
	return out
}

// ListenStart is where a listener of coll starts: the collection's watermark (or, before the first sweep, now) minus
// ListenOverlap.
func (e *Engine) ListenStart(ctx context.Context, coll string) (time.Time, error) {
	var mark watermark
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		mark, err = readWatermark(ctx, tx, coll)
		return err
	})
	if err != nil {
		return time.Time{}, err
	}
	if mark.at.IsZero() {
		return e.cfg.Now().UTC().Add(-ListenOverlap), nil
	}
	return mark.at.Add(-ListenOverlap), nil
}

// ErrNoCursor is a MirrorChanges call without a cursor for a listened collection.
var ErrNoCursor = errors.New("etl: a listener needs a cursor per collection (ListenStart)")

// MirrorChanges reads the changes of the listened collections since their cursors (minus ListenOverlap) and applies
// them in one transaction. It returns the report (ExportedAt is the Firestore read time) and the next cursors: the
// newest field value read (the cursor given when none is newer), capped at the read time. The cap keeps a writer's
// clock that runs ahead (ListenOverlap) from pushing a cursor into the future: a document dated ahead is read again by
// every poll until the read time passes it, and the update-time guard skips it. A poll that reads no document opens no
// transaction.
func (e *Engine) MirrorChanges(ctx context.Context, src ChangeSource, plan MirrorPlan, cursors map[string]time.Time) (*Report, map[string]time.Time, error) {
	colls := ListenCollections(plan)
	next := make(map[string]time.Time, len(colls))
	docs := make(map[string][]dump.Doc, len(colls))
	var at time.Time
	total := 0
	for _, coll := range colls {
		since, ok := cursors[coll]
		if !ok || since.IsZero() {
			return nil, nil, fmt.Errorf("%w: %s", ErrNoCursor, coll)
		}
		field := ListenFields[coll]
		ds, read, err := src.Changed(ctx, coll, field, since.Add(-ListenOverlap), at)
		if err != nil {
			return nil, nil, fmt.Errorf("etl: listen: read %s: %w", coll, err)
		}
		at = read
		high := since
		for _, d := range ds {
			if ts, ok := d.Fields[field].(dump.Timestamp); ok && ts.After(high) {
				high = ts.UTC()
			}
		}
		if !read.IsZero() && high.After(read) {
			high = read.UTC() // a writer's clock ahead of Firestore's never moves the cursor past this read
		}
		next[coll], docs[coll] = high, ds
		total += len(ds)
	}
	rep := &Report{ExportedAt: at, ReadAt: at}
	if total == 0 {
		return rep, next, nil
	}
	billable := billableFrom(docs["trip_records"])
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('lt-etl-load'))`); err != nil {
			return fmt.Errorf("etl: lock: %w", err)
		}
		if err := e.requireOwnFleet(ctx, tx); err != nil {
			return err
		}
		r := e.newRun(ctx, tx, rep)
		r.owned = plan.Owned
		r.events, r.mirror = true, true
		r.lookup = func(path string) (bool, error) {
			_, found, err := src.Lookup(ctx, path, at)
			return found, err
		}
		for _, spec := range mappedCollections() {
			if !slices.Contains(colls, spec.name) {
				continue
			}
			stats, err := e.loadCollection(ctx, tx, spec, docs[spec.name], e.cfg.Now().UTC(), LoadOptions{Mirror: true, Changes: true},
				billable, rep, r)
			if err != nil {
				return err
			}
			stats.Mode = MirrorFull
			rep.Collections = append(rep.Collections, stats)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return rep, next, nil
}
