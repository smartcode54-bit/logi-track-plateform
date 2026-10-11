package etl

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// Helpers the T24 mappers share (Appendix A §A.3.0 casting and lookups).

// ownFleet is the own-fleet tenant (OWN_FLEET_TENANT_ID, R56): the billing carrier of every rate table, statement,
// counter and compensation config today (form, R61).
func (c *docCtx) ownFleet() uuid.UUID { return c.e.cfg.OwnFleetTenantID }

// partyID resolves a customer id to billing_parties.id without recording anything (customers first, then
// subcontractors, ADR 0028); nil when it names no party.
func (c *docCtx) partyID(raw string) (*uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var id uuid.UUID
	err := c.tx.QueryRow(c.ctx, `SELECT id FROM (
		  SELECT bp.id, 1 AS rank FROM billing_parties bp JOIN customers cu ON cu.id = bp.customer_id WHERE cu.legacy_doc_id = $1
		  UNION ALL
		  SELECT bp.id, 2 FROM billing_parties bp JOIN tenants t ON t.id = bp.tenant_id WHERE t.legacy_doc_id = $1
		) p ORDER BY rank LIMIT 1`, raw).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("etl: %s: party lookup: %w", c.doc.Path, err)
	}
	return &id, nil
}

// requiredParty is the party of a rate row (billing_party_id NOT NULL): an unresolved or missing customerId rejects
// the row with party_unresolved / missing_required (Appendix A §A.3.9: a rate row without a party can never be
// selected).
func (c *docCtx) requiredParty(field string) (*uuid.UUID, bool, error) {
	raw := c.str(field)
	if raw == "" {
		return nil, false, c.reject(field, ReasonMissingRequired, "billing_party_id is NOT NULL", nil)
	}
	id, err := c.partyID(raw)
	if err != nil {
		return nil, false, err
	}
	if id == nil {
		return nil, false, c.reject(field, ReasonPartyUnresolved, "matches no customers or subcontractors doc; a rate row without a party is never selected", raw)
	}
	return id, true, nil
}

// effectiveFrom is the Bangkok rule of announcement rows (Appendix A §A.3.0): effective_from_date :=
// effectiveFromDateStr ?? bkk_date(effectiveFrom); effective_from_at := effectiveFrom (the stored instant, untouched;
// Bangkok midnight of the date when only the date string exists). ok is false when neither is usable.
func (c *docCtx) effectiveFrom() (day, at *time.Time, ok bool) {
	at = c.ts("effectiveFrom")
	if s := c.str("effectiveFromDateStr"); s != "" {
		if t, isDate := clock.MidnightFromDateString(s); isDate {
			d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
			day = &d
			if at == nil {
				m := t.UTC()
				at = &m
			}
		} else {
			c.find("effectiveFromDateStr", ReasonBadTimestamp, "not yyyy-MM-dd; the Bangkok day of effectiveFrom is used", s)
		}
	}
	if day == nil && at != nil {
		b := at.In(clock.Bangkok)
		d := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
		day = &d
	}
	return day, at, day != nil && at != nil
}

// requiredMoney is a NOT NULL money column: absent -> missing_required, unparseable -> bad_number, negative where
// the column requires >= 0 -> negative_money, each rejecting the row.
func (c *docCtx) requiredMoney(field, column string, nonNegative bool) (int64, bool, error) {
	n, res := c.moneyCents(field, nonNegative)
	switch res {
	case moneyAbsent:
		return 0, false, c.reject(field, ReasonMissingRequired, column+" is NOT NULL", nil)
	case moneyBad:
		return 0, false, c.reject(field, ReasonBadNumber, column+" is NOT NULL", c.get(field))
	case moneyNegative:
		return 0, false, c.reject(field, ReasonNegativeMoney, column+" must be >= 0", c.get(field))
	}
	return n, true, nil
}

// plain turns a dump value into plain JSON (a Firestore timestamp is its RFC 3339 string, a geopoint {lat, lng}, a
// reference its path, bytes base64), for jsonb columns that readers parse as ordinary JSON (settings values, snapshot
// items, tiers, payloads).
func plain(v any) any {
	switch x := v.(type) {
	case dump.Timestamp:
		return x.UTC().Format(time.RFC3339Nano)
	case dump.GeoPoint:
		return map[string]any{"lat": x.Lat, "lng": x.Lng}
	case dump.Ref:
		return x.Path
	case dump.Bytes:
		return base64.StdEncoding.EncodeToString(x)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = plain(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = plain(e)
		}
		return out
	}
	return v
}

// lowerVocab maps a legacy literal onto a lower_snake vocabulary (canon); "" when absent.
func lowerVocab(v any, allowed ...string) (string, bool) {
	s, _ := v.(string)
	if strings.TrimSpace(s) == "" {
		return "", true
	}
	x := canon(s)
	for _, a := range allowed {
		if x == a {
			return x, true
		}
	}
	return x, false
}

// userByUID resolves a Firebase uid to users.id without recording anything.
func (c *docCtx) userByUID(uid string) (*uuid.UUID, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil, nil
	}
	var id uuid.UUID
	err := c.tx.QueryRow(c.ctx, `SELECT id FROM users WHERE legacy_auth_uid = $1`, uid).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("etl: %s: user lookup: %w", c.doc.Path, err)
	}
	return &id, nil
}

// stampedRowWhere is the tenant an existing row (found by a key other than legacy_doc_id) carries.
func (c *docCtx) stampedRowWhere(table, where string, args ...any) (stampedRow, bool, error) {
	var r stampedRow
	var src string
	err := c.tx.QueryRow(c.ctx, `SELECT tenant_id, tenant_source FROM `+pgx.Identifier{table}.Sanitize()+` WHERE `+where, args...).Scan(&r.tenant, &src)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, fmt.Errorf("etl: %s: read the row's tenant: %w", c.doc.Path, err)
	}
	r.source = tenancy.Source(src)
	return r, true, nil
}

// statusEntry is one legacy statusHistory[] element.
type statusEntry struct {
	status, previous, label, reason string
	at                              time.Time
	user                            *uuid.UUID
	raw                             any
}

// appendStatusHistory writes the entries that status_history (append-only) does not hold yet, matched on (entity,
// changed_at, status), so a re-applied document adds only its new entries.
func (c *docCtx) appendStatusHistory(entity string, id uuid.UUID, entries []statusEntry) error {
	for _, s := range entries {
		var prev, label, reason *string
		if s.previous != "" {
			prev = &s.previous
		}
		if s.label != "" {
			label = &s.label
		}
		if s.reason != "" {
			reason = &s.reason
		}
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO status_history (entity_type, entity_id, status, previous_status, changed_at,
			  changed_by_user_id, changed_by_label, reason, legacy_raw)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9
			WHERE NOT EXISTS (SELECT 1 FROM status_history WHERE entity_type = $1 AND entity_id = $2 AND changed_at = $5 AND status = $3)`,
			entity, id, s.status, prev, s.at, s.user, label, reason, jsonValue(s.raw)); err != nil {
			return fmt.Errorf("etl: %s: status history: %w", c.doc.Path, err)
		}
	}
	return nil
}
