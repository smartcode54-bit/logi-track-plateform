package etl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// The daily reconciliation of the coexistence (main spec §12.2 P1 cut-over "reconciliation 100% for 7 days",
// §13.10 "checksums of mirrored and projected fields"; T24): it reads the live collections (or a dump of them) and
// compares, without writing anything but its run:
//   - mirror: every document of a mirrored collection is recorded in etl.source_docs at its current update time (the
//     mirror caught up) and none is held (a billable document the sweep cannot apply), and the fields the field-level
//     mirror applies (activeTruck, fcmToken; the maintenance fields of a truck; a login's last sign-in and tokens) equal
//     the PostgreSQL columns;
//   - projection: every row a projected collection writes has its document, and the document's PostgreSQL-owned
//     fields are equivalent to the row (ExportDoc.Diff over the encoder's mask; ids are computed, never minted), and
//     no document is held (ExportDoc.Hold); documents no row projects to are reported (informational: legacy
//     duplicates such as a losing hub document), except in the one-document collections (metadata, settings);
//   - findings: the open findings per reason code against the last matching run, as `reconcile` does.
// The run is stored in etl.reconciliation_runs (kind sync), so `--accept-findings` and the baseline work as for a dump
// reconciliation.

// SyncReconcileOptions select one coexistence reconciliation.
type SyncReconcileOptions struct {
	Mirror     MirrorPlan
	Projection []string
	// AcceptFindingsBy records the owner's sign-off of the open findings (as ReconcileOptions).
	AcceptFindingsBy string
}

// maxListed bounds the paths a check's note names.
const maxListed = 10

func listed(paths []string) string {
	slices.Sort(paths)
	if len(paths) > maxListed {
		return strings.Join(paths[:maxListed], ", ") + fmt.Sprintf(" (+%d more)", len(paths)-maxListed)
	}
	return strings.Join(paths, ", ")
}

// ReconcileSync compares the live Firestore (src) with PostgreSQL for the mirror and the projection.
func (e *Engine) ReconcileSync(ctx context.Context, src DocSource, o SyncReconcileOptions) (*Reconciliation, error) {
	read := e.cfg.Now().UTC()
	docs := map[string][]dump.Doc{}
	names := o.Mirror.Names()
	for _, c := range o.Projection {
		if !slices.Contains(names, c) {
			names = append(names, c)
		}
	}
	for _, n := range names {
		ds, err := src.Documents(ctx, n, groupCollections[n])
		if err != nil {
			return nil, fmt.Errorf("etl: reconcile-sync: read %s: %w", n, err)
		}
		docs[n] = ds
	}
	rec := &Reconciliation{Kind: "sync", ExportedAt: read, Match: true, AcceptedBy: strings.TrimSpace(o.AcceptFindingsBy)}
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := e.settleRehomed(ctx, tx); err != nil {
			return err
		}
		for _, m := range o.Mirror.Entries {
			if err := reconcileMirror(ctx, tx, m, docs[m.Name], o.Mirror.Owned, rec); err != nil {
				return err
			}
		}
		for _, c := range o.Projection {
			if err := reconcileProjection(ctx, tx, c, docs[c], o.Mirror.Owned, rec); err != nil {
				return err
			}
		}
		if err := reconcileFindings(ctx, tx, rec, rec.AcceptedBy); err != nil {
			return err
		}
		report, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		outcome := "match"
		if !rec.Match {
			outcome = "mismatch"
		}
		_, err = tx.Exec(ctx, `INSERT INTO etl.reconciliation_runs (finished_at, outcome, report) VALUES (now(), $1, $2)`, outcome, report)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("etl: reconcile-sync: %w", err)
	}
	return rec, nil
}

// reconcileMirror: the collection's documents are recorded at their update time; in MirrorFields mode the applied
// fields equal the row.
func reconcileMirror(ctx context.Context, tx pgx.Tx, m MirrorEntry, docs []dump.Doc, owned map[string]bool, rec *Reconciliation) error {
	rows, err := tx.Query(ctx, `SELECT doc_path, source_update_time, status FROM etl.source_docs WHERE collection = $1`, m.Name)
	if err != nil {
		return err
	}
	stored := map[string]time.Time{}
	pending := map[string]bool{}
	for rows.Next() {
		var p, status string
		var t time.Time
		if err := rows.Scan(&p, &t, &status); err != nil {
			rows.Close()
			return err
		}
		stored[p] = t
		pending[p] = status == string(Pending)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var behind, held, differ []string
	inSource := map[string]bool{}
	for _, d := range docs {
		inSource[d.Path] = true
		t, ok := stored[d.Path]
		if !ok || t.Before(d.UpdateTime.Truncate(time.Microsecond)) {
			behind = append(behind, d.Path)
			continue
		}
		if pending[d.Path] { // a billable document the sweep holds (R19) until it is fixed at the source
			held = append(held, d.Path)
			continue
		}
		if m.Mode != MirrorFields {
			continue
		}
		same, err := mirroredFieldsMatch(ctx, tx, m.Name, d, owned)
		if err != nil {
			return err
		}
		if !same {
			differ = append(differ, d.Path)
		}
	}
	key := m.Name + " (" + m.Mode + ")"
	var notes []string
	if len(behind) > 0 {
		notes = append(notes, "not mirrored yet: "+listed(behind))
	}
	if len(held) > 0 {
		notes = append(notes, "held (billable, cannot be applied; fix at the source): "+listed(held))
	}
	note := "documents recorded at their current update time"
	if len(notes) > 0 {
		note = strings.Join(notes, "; ")
	}
	rec.add("mirror", key, len(docs), len(docs)-len(behind)-len(held), len(behind)+len(held) == 0, note)
	if m.Mode == MirrorFields {
		note := "the fields the 3.x app (or another domain) writes equal the row"
		if len(differ) > 0 {
			note = "differ: " + listed(differ)
		}
		n := len(docs) - len(behind) - len(held)
		rec.add("mirror_fields", m.Name, n, n-len(differ), len(differ) == 0, note)
	}
	gone := 0
	for p := range stored {
		if !inSource[p] {
			gone++
		}
	}
	if gone > 0 {
		rec.add("deleted_at_source", m.Name, 0, gone, true, "documents in etl.source_docs that Firestore no longer has (reported, never deleted)")
	}
	return nil
}

// mirroredFieldsMatch compares the fields the field-level mirror applies with the row behind the document. A document
// with no row (rejected by the mirror) has nothing to compare. A truck field the maintenance flow owns until P3 that a
// truck-form edit in Go changed differs too: reported, since Firestore readers still see the maintenance flow's value.
func mirroredFieldsMatch(ctx context.Context, tx pgx.Tx, coll string, d dump.Doc, owned map[string]bool) (bool, error) {
	c := &docCtx{ctx: ctx, tx: tx, f: d.Fields}
	switch coll {
	case "drivers":
		var truck, task, user *uuid.UUID
		var plate *string
		var started *time.Time
		var driver uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id, active_truck_id, active_truck_plate, active_started_at, active_task_id, user_id FROM drivers
			WHERE legacy_doc_id = $1`, d.ID).Scan(&driver, &truck, &plate, &started, &task, &user)
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		var wantTruck, wantTask *uuid.UUID
		var wantPlate *string
		var wantStarted *time.Time
		if at, ok := c.get("activeTruck").(map[string]any); ok {
			sub := c.sub(at)
			tr, err := sub.truckByRef(sub.str("truckId"))
			if err != nil {
				return false, err
			}
			if tr.ok() {
				wantTruck = &tr.id
			}
			wantPlate, wantStarted = sub.text("truckPlate"), sub.ts("startedAt")
			if ref := strings.TrimSpace(sub.str("taskId")); ref != "" {
				if err := tx.QueryRow(ctx, `SELECT coalesce((SELECT id FROM tasks WHERE legacy_doc_id = $1),
					  (SELECT min(id::text)::uuid FROM tasks WHERE task_no = $1 HAVING count(*) = 1))`, ref).Scan(&wantTask); err != nil {
					return false, err
				}
			}
		}
		if !sameUUID(truck, wantTruck) || !sameUUID(task, wantTask) || deref(plate, "") != deref(wantPlate, "") || !sameTime(started, wantStarted) {
			return false, nil
		}
		if tok := strings.TrimSpace(c.str("fcmToken")); tok != "" && user != nil {
			owner, err := driverTokenOwner(ctx, tx, *user, d.Fields)
			if err != nil || !owner { // another account's token is never stored (mirrorDriverToken)
				return err == nil, err
			}
			var held bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM device_tokens WHERE token = $1)`, tok).Scan(&held); err != nil {
				return false, err
			}
			return held, nil
		}
		return true, nil
	case "users":
		var last *time.Time
		var status string
		err := tx.QueryRow(ctx, `SELECT last_login_at, status FROM users WHERE legacy_auth_uid = $1`, d.ID).Scan(&last, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		if err != nil || status == "deleted" {
			return err == nil, err
		}
		if at, ok := userDocTime(d.Fields, "lastLogin"); ok && (last == nil || last.Before(at.Truncate(time.Microsecond))) {
			return false, nil
		}
		for _, tok := range userTokens(d.Fields["fcmTokens"]) {
			var held bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM device_tokens WHERE token = $1)`, tok).Scan(&held); err != nil || !held {
				return false, err
			}
		}
		return true, nil
	case "trucks":
		var maint *uuid.UUID
		var alert, current, next *int32
		var service *time.Time
		var status string
		var legacyStatus *string
		err := tx.QueryRow(ctx, `SELECT active_maintenance_id, last_alert_mileage, current_mileage, next_service_mileage, last_service_date,
			  status, legacy_status FROM trucks WHERE legacy_doc_id = $1`, d.ID).Scan(&maint, &alert, &current, &next, &service, &status, &legacyStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		for _, f := range ForeignFields("trucks", owned) {
			switch f {
			case "activeMaintenanceId":
				var want *uuid.UUID
				if ref := strings.TrimSpace(c.str(f)); ref != "" {
					if err := tx.QueryRow(ctx, `SELECT (SELECT id FROM maintenance_records WHERE legacy_doc_id = $1)`, ref).Scan(&want); err != nil {
						return false, err
					}
				}
				if !sameUUID(maint, want) {
					return false, nil
				}
			case "lastAlertMileage":
				if !sameInt(alert, c.integer(f)) {
					return false, nil
				}
			case "currentMileage":
				if !sameInt(current, c.integer(f)) {
					return false, nil
				}
			case "nextServiceMileage":
				if !sameInt(next, c.integer(f)) {
					return false, nil
				}
			case "lastServiceDate":
				if !sameTime(service, c.date(f)) {
					return false, nil
				}
			case "truckStatus":
				if s := c.str(f); s != "" && truckStatusCanon(s) != status {
					return false, nil
				}
			}
		}
	}
	return true, nil
}

func sameUUID(a, b *uuid.UUID) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func sameTime(a, b *time.Time) bool {
	return (a == nil) == (b == nil) && (a == nil || a.Truncate(time.Microsecond).Equal(b.Truncate(time.Microsecond)))
}

func sameInt(a *int32, b *int64) bool {
	return (a == nil) == (b == nil) && (a == nil || int64(*a) == *b)
}

// reconcileProjection: every projected row has its document and the document's owned fields are equivalent.
func reconcileProjection(ctx context.Context, tx pgx.Tx, coll string, docs []dump.Doc, owned map[string]bool, rec *Reconciliation) error {
	enc, ok := projectors[coll]
	if !ok {
		return fmt.Errorf("etl: %s is not a projected collection", coll)
	}
	want, err := enc(ctx, tx, selectAll(), false)
	if err != nil {
		return err
	}
	byPath := make(map[string]dump.Doc, len(docs))
	for _, d := range docs {
		byPath[d.Path] = d
	}
	foreign := ForeignFields(coll, owned)
	projected := map[string]bool{}
	live := map[string]bool{}
	for _, w := range want {
		if !w.Retired && w.Skip == "" {
			live[w.Path] = true
		}
	}
	var missing, differ []string
	rows := 0
	for _, w := range want {
		projected[w.Path] = true
		if w.Skip != "" {
			continue
		}
		cur, ok := byPath[w.Path]
		if w.Retired && (!ok || live[w.Path]) {
			continue // a former id: checked only while its document exists and no row took it over
		}
		rows++
		if !ok {
			missing = append(missing, w.Path)
			continue
		}
		if w.Hold != nil {
			if reason := w.Hold(cur.Fields); reason != "" {
				differ = append(differ, w.Path+" held: "+reason)
				continue
			}
		}
		if _, mask := w.without(foreign).Diff(cur.Fields); len(mask) > 0 {
			differ = append(differ, w.Path+" "+strings.Join(mask, "/"))
		}
	}
	note := "every projected row has an equivalent document"
	switch {
	case len(missing) > 0 && len(differ) > 0:
		note = "missing: " + listed(missing) + "; differ: " + listed(differ)
	case len(missing) > 0:
		note = "missing: " + listed(missing)
	case len(differ) > 0:
		note = "differ: " + listed(differ)
	}
	rec.add("projection", coll, rows, rows-len(missing)-len(differ), len(missing)+len(differ) == 0, note)
	if singleDocument[coll] {
		return nil // the collection's other documents are settings keys the projection does not own
	}
	var orphans []string
	for _, d := range docs {
		if !projected[d.Path] {
			orphans = append(orphans, d.Path)
		}
	}
	if len(orphans) > 0 {
		rec.add("firestore_only", coll, len(orphans), 0, true, "documents no PostgreSQL row projects to (legacy duplicates, rejected "+
			"documents; reported): "+listed(orphans))
	}
	return nil
}
