package etl

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// The field-level mirror of the PostgreSQL-owned class B collections (main spec §12.1 class B, §13.7): only what the
// 3.x app writes on them reaches PostgreSQL, so the projection (which never writes those fields) and the mirror (which
// writes nothing else) are disjoint per field.

// legacyInstallID is the install id of a legacy FCM token until its device registers again: 'legacy:' + the first 16
// hex of sha256(token) (Appendix C §C.5.5).
func legacyInstallID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "legacy:" + hex.EncodeToString(sum[:])[:16]
}

// changedFields lists which of fields hold another value than when the document was last recorded (the P1 load, a
// sweep): a field-level mirror applies only those, so a write of the projection (which moves the document's update
// time without touching them) never re-applies a stale value over a PostgreSQL edit, and a field the other side did
// not change keeps PostgreSQL's value. A document never recorded has every field changed.
func (c *docCtx) changedFields(fields []string) (map[string]bool, error) {
	var raw []byte
	err := c.tx.QueryRow(c.ctx, `SELECT raw FROM etl.source_docs WHERE collection = $1 AND doc_path = $2`, c.coll, c.doc.Path).Scan(&raw)
	out := map[string]bool{}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		for _, f := range fields {
			out[f] = true
		}
		return out, nil
	case err != nil:
		return nil, fmt.Errorf("etl: %s: the recorded document: %w", c.doc.Path, err)
	}
	prev, err := dump.UnmarshalFields(raw)
	if err != nil {
		return nil, fmt.Errorf("etl: %s: the recorded document: %w", c.doc.Path, err)
	}
	// Both sides through the dump encoding, so a value and its recorded copy compare equal.
	cur := map[string]any{}
	for _, f := range fields {
		if v, ok := c.f[f]; ok {
			cur[f] = v
		}
	}
	b, err := dump.MarshalFields(cur)
	if err != nil {
		return nil, fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	now, err := dump.UnmarshalFields(b)
	if err != nil {
		return nil, fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	for _, f := range fields {
		if !reflect.DeepEqual(now[f], prev[f]) {
			out[f] = true
		}
	}
	return out, nil
}

// scope sets the finding scope of a field-level apply to the fields it applies (in order).
func (c *docCtx) scope(fields []string, changed map[string]bool) {
	c.findingScope = []string{}
	for _, f := range fields {
		if changed[f] {
			c.findingScope = append(c.findingScope, f)
		}
	}
}

// mirrorDriverFields applies the 3.x app's fields of a drivers document PostgreSQL owns, each only when the app changed
// it since the document was last recorded (changedFields): activeTruck {truckId, truckPlate, taskId, startedAt} ->
// drivers.active_truck_id, active_truck_plate, active_task_id, active_started_at (cleared when the app deleted the map
// at trip end), and fcmToken -> the driver's legacy device_tokens row (legacy_source drivers.fcmToken, replaced when
// the app writes another token, deleted when it clears it). Every other field is PostgreSQL's and is ignored;
// currentTruckId stays in etl.source_docs (Appendix A §A.3.4). A document with no driver row is rejected
// (driver_unresolved): PostgreSQL owns drivers, so a driver created in Firestore is a write the cut-over forbids,
// listed for the operator instead of loaded.
func mirrorDriverFields(c *docCtx) error {
	fields := []string{"activeTruck", "fcmToken"}
	c.findingScope = fields
	var id, tenant uuid.UUID
	var user *uuid.UUID
	err := c.tx.QueryRow(c.ctx, `SELECT id, tenant_id, user_id FROM drivers WHERE legacy_doc_id = $1`, c.doc.ID).Scan(&id, &tenant, &user)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.reject("", ReasonDriverUnresolved, "PostgreSQL owns drivers and has no row for this document; only the 3.x app's "+
			"fields (activeTruck, fcmToken) of a known driver are mirrored", nil)
	}
	if err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	changed, err := c.changedFields(fields)
	if err != nil {
		return err
	}
	c.scope(fields, changed)
	if changed["activeTruck"] {
		var truck, task *uuid.UUID
		var plate *string
		var started *time.Time
		if at, ok := c.get("activeTruck").(map[string]any); ok {
			sub := c.sub(at)
			tr, err := sub.truck("truckId")
			if err != nil {
				return err
			}
			if tr.ok() {
				truck = &tr.id
			}
			plate, started = sub.text("truckPlate"), sub.ts("startedAt")
			if ref := strings.TrimSpace(sub.str("taskId")); ref != "" {
				if err := c.tx.QueryRow(c.ctx, `SELECT coalesce((SELECT id FROM tasks WHERE legacy_doc_id = $1),
					  (SELECT min(id::text)::uuid FROM tasks WHERE task_no = $1 HAVING count(*) = 1))`, ref).Scan(&task); err != nil {
					return fmt.Errorf("etl: %s: active task: %w", c.doc.Path, err)
				}
			}
			c.adopt(sub, "activeTruck.")
		}
		// IS DISTINCT FROM keeps an unchanged row untouched (no updated_at bump).
		if _, err := c.tx.Exec(c.ctx, `UPDATE drivers SET active_truck_id = $2, active_truck_plate = $3, active_started_at = $4, active_task_id = $5
			WHERE id = $1 AND (active_truck_id, active_truck_plate, active_started_at, active_task_id) IS DISTINCT FROM ($2, $3, $4, $5)`,
			id, truck, plate, started, task); err != nil {
			return fmt.Errorf("etl: %s: active truck: %w", c.doc.Path, err)
		}
	}
	if changed["fcmToken"] {
		if err := c.mirrorDriverToken(id, user); err != nil {
			return err
		}
	}
	if tenant == c.e.quar {
		c.outcome, c.tenantSource = Quarantined, tenancy.SourceQuarantine
	}
	c.done("drivers", id)
	return nil
}

// mirrorDriverToken keeps the driver's legacy token row equal to drivers.fcmToken. Rows the device registered itself
// (PUT /v1/mobile/me/devices, no legacy_source) and the users.fcmTokens rows are never touched. A driver without a
// linked user keeps no token (driver_unresolved, as in auth-import); a token another user's device row holds stays
// there (duplicate_natural_key). The token itself is never written into a finding.
func (c *docCtx) mirrorDriverToken(driver uuid.UUID, user *uuid.UUID) error {
	tok := strings.TrimSpace(c.str("fcmToken"))
	if user == nil {
		if tok != "" {
			c.find("fcmToken", ReasonDriverUnresolved, "the driver has no linked user; the token is not stored", nil)
		}
		return nil
	}
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM device_tokens WHERE user_id = $1 AND driver_id = $2 AND legacy_source = 'drivers.fcmToken'
		AND token IS DISTINCT FROM nullif($3, '')`, *user, driver, tok); err != nil {
		return fmt.Errorf("etl: %s: device token: %w", c.doc.Path, err)
	}
	if tok == "" {
		return nil
	}
	if _, err := c.tx.Exec(c.ctx, `INSERT INTO device_tokens (user_id, install_id, token, driver_id, platform, legacy_source)
		VALUES ($1, $2, $3, $4, 'other', 'drivers.fcmToken') ON CONFLICT DO NOTHING`, *user, legacyInstallID(tok), tok, driver); err != nil {
		return fmt.Errorf("etl: %s: device token: %w", c.doc.Path, err)
	}
	var holder uuid.UUID
	err := c.tx.QueryRow(c.ctx, `SELECT user_id FROM device_tokens WHERE token = $1`, tok).Scan(&holder)
	switch {
	case err != nil:
		return fmt.Errorf("etl: %s: device token: %w", c.doc.Path, err)
	case holder != *user:
		c.find("fcmToken", ReasonDuplicateNaturalKey, "another user's device row holds this token; it stays there", nil)
	}
	return nil
}

// mirrorHubFields applies the hubs documents drivers create (createdByDriver, hubs_repository.dart addHubByDriver) and
// nothing else of a hubs collection PostgreSQL owns:
//   - a document of a row the mirror inserted from a driver's document is applied as `load` does (Firestore stays the
//     writer of those rows; the projection skips them, §13.8);
//   - a document of a row PostgreSQL owns (the projection's own write, or a legacy document loaded at P1) is recorded
//     and left alone, so it never re-enters PostgreSQL;
//   - a new driver document is inserted, unless its code is already a PostgreSQL hub (the app checked duplicates in
//     Firestore only): then it is rejected (duplicate_hub_source_id) and maps to that hub's row;
//   - a new document not created by a driver is rejected (hub_unresolved): only Go writes hubs after the cut-over.
func mirrorHubFields(c *docCtx) error {
	var id uuid.UUID
	var driverRow bool
	var legacy, idText string
	err := c.tx.QueryRow(c.ctx, `SELECT id, created_by_driver, legacy_doc_id, id::text FROM hubs WHERE legacy_doc_id = $1`,
		c.doc.ID).Scan(&id, &driverRow, &legacy, &idText)
	switch {
	case err == nil && driverRow && legacy != idText:
		return loadHub(c)
	case err == nil:
		c.findingScope = []string{} // nothing applied: the P1 findings of the document stay
		c.done("hubs", id)
		return nil
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	sid := hubSourceID(c.f)
	if !c.isTrue("createdByDriver") {
		return c.reject("source_id", ReasonHubUnresolved, "PostgreSQL owns hubs: a hubs document not created by a driver is not "+
			"loaded (hubs are written through Go after the cut-over)", sid)
	}
	if sid != "" {
		var holder uuid.UUID
		err := c.tx.QueryRow(c.ctx, `SELECT id FROM hubs WHERE source_id = $1`, hubs.NormalizeSourceID(sid)).Scan(&holder)
		if err == nil {
			if err := c.reject("source_id", ReasonDuplicateHubSourceID, "the code is already a hub PostgreSQL owns; this document maps to its row", sid); err != nil {
				return err
			}
			c.table, c.target = "hubs", holder.String()
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
	}
	return loadHub(c)
}

// mirrorTruckFields applies the trucks fields another domain still writes in Firestore (ForeignFields: the maintenance
// flow's activeMaintenanceId, lastAlertMileage, currentMileage, lastServiceDate, nextServiceMileage and truckStatus
// while billing is Firestore-owned) to a truck PostgreSQL owns, each only when Firestore changed it since the document
// was last recorded (changedFields), so a truck-form edit of the same field in Go stays until the maintenance flow
// writes it again; a maintenance document mirrored later in the same sweep is linked by afterMaintenance from the raw
// document recorded here. A document with no truck row is rejected (truck_unresolved): PostgreSQL owns trucks.
func mirrorTruckFields(c *docCtx) error {
	var owned map[string]bool
	if c.run != nil {
		owned = c.run.owned
	}
	fields := ForeignFields("trucks", owned)
	c.findingScope = append([]string{}, fields...)
	var id, tenant uuid.UUID
	err := c.tx.QueryRow(c.ctx, `SELECT id, tenant_id FROM trucks WHERE legacy_doc_id = $1`, c.doc.ID).Scan(&id, &tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.reject("", ReasonTruckUnresolved, "PostgreSQL owns trucks and has no row for this document; only the fields "+
			"Firestore still writes (the maintenance flow's) of a known truck are mirrored", nil)
	}
	if err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	changed, err := c.changedFields(fields)
	if err != nil {
		return err
	}
	c.scope(fields, changed)
	for _, f := range c.findingScope {
		var err error
		switch f {
		case "activeMaintenanceId":
			var m *uuid.UUID
			if ref := strings.TrimSpace(c.str(f)); ref != "" {
				if err := c.tx.QueryRow(c.ctx, `SELECT (SELECT id FROM maintenance_records WHERE legacy_doc_id = $1)`, ref).Scan(&m); err != nil {
					return fmt.Errorf("etl: %s: active maintenance: %w", c.doc.Path, err)
				}
			}
			_, err = c.tx.Exec(c.ctx, `UPDATE trucks SET active_maintenance_id = $2 WHERE id = $1 AND active_maintenance_id IS DISTINCT FROM $2`, id, m)
		case "lastAlertMileage":
			_, err = c.tx.Exec(c.ctx, `UPDATE trucks SET last_alert_mileage = $2 WHERE id = $1 AND last_alert_mileage IS DISTINCT FROM $2`, id, c.integer(f))
		case "currentMileage":
			_, err = c.tx.Exec(c.ctx, `UPDATE trucks SET current_mileage = $2 WHERE id = $1 AND current_mileage IS DISTINCT FROM $2`, id, c.integer(f))
		case "nextServiceMileage":
			_, err = c.tx.Exec(c.ctx, `UPDATE trucks SET next_service_mileage = $2 WHERE id = $1 AND next_service_mileage IS DISTINCT FROM $2`, id, c.integer(f))
		case "lastServiceDate":
			_, err = c.tx.Exec(c.ctx, `UPDATE trucks SET last_service_date = $2 WHERE id = $1 AND last_service_date IS DISTINCT FROM $2`, id, c.date(f))
		case "truckStatus":
			// The loader's fold: a lossless status, 'Available' as active with the raw literal kept; anything else is
			// skipped (status_out_of_vocab), never overwriting PostgreSQL.
			status, ok := c.vocab(f, "active", "inactive", "maintenance", "insurance_claim", "sold", "available")
			if !ok || status == "" {
				c.find(f, ReasonStatusOutOfVocab, "no lossless truck status; the status is not changed", c.get(f))
				continue
			}
			var legacy *string
			if raw := c.str(f); status == "available" || raw != status {
				legacy = &raw
			}
			if status == "available" {
				status = "active"
			}
			_, err = c.tx.Exec(c.ctx, `UPDATE trucks SET status = $2, legacy_status = $3 WHERE id = $1
				AND (status, legacy_status) IS DISTINCT FROM ($2, $3)`, id, status, legacy)
		}
		if err != nil {
			return fmt.Errorf("etl: %s: %s: %w", c.doc.Path, f, err)
		}
	}
	if tenant == c.e.quar {
		c.outcome, c.tenantSource = Quarantined, tenancy.SourceQuarantine
	}
	c.done("trucks", id)
	return nil
}
