package etl

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// Reference resolution (Appendix A §A.3.0, main spec §13.4). Every lookup reads the rows earlier collections of the
// same transaction wrote, so the load order is what makes a link resolvable. A parent in the quarantine tenant is
// "not stamped" (tenantResolve.ts): its tenant is never handed to a child as a resolution.

// ref is a resolved row: its id and tenant.
type ref struct {
	id     uuid.UUID
	tenant uuid.UUID
}

func (r ref) ok() bool { return r.id != uuid.Nil }

// quarantined reports whether the row lives in the quarantine tenant.
func (c *docCtx) quarantined(r ref) bool { return r.tenant == c.e.quar }

// stamped is the tenant a child may inherit: "" for an unresolved or quarantined row.
func (c *docCtx) stamped(r ref) string {
	if !r.ok() || c.quarantined(r) {
		return ""
	}
	return r.tenant.String()
}

func (c *docCtx) one(sql string, args ...any) (ref, error) {
	var r ref
	err := c.tx.QueryRow(c.ctx, sql, args...).Scan(&r.id, &r.tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return ref{}, nil
	}
	if err != nil {
		return ref{}, fmt.Errorf("etl: %s: lookup: %w", c.doc.Path, err)
	}
	return r, nil
}

// driverMatch is a resolved legacy driver reference (R12): the raw value, how it matched, and the row.
type driverMatch struct {
	raw   string
	match string // doc_id | auth_uid | name | none
	ref
}

// driverByRef resolves a driverId-like value: drivers.legacy_doc_id, then drivers.legacy_auth_uid (the doc-id
// then authId order of tenantLookups.ts:62-78); for tasks only, then the name (matchDriverOptionId,
// driverName.ts:44-61). No phone or plate fallback (R28).
func (c *docCtx) driverByRef(raw, name string) (driverMatch, error) {
	m := driverMatch{raw: strings.TrimSpace(raw), match: "none"}
	if m.raw != "" {
		r, err := c.one(`SELECT id, tenant_id FROM drivers WHERE legacy_doc_id = $1`, m.raw)
		if err != nil || r.ok() {
			m.ref, m.match = r, "doc_id"
			return m, err
		}
		r, err = c.one(`SELECT id, tenant_id FROM drivers WHERE legacy_auth_uid = $1`, m.raw)
		if err != nil || r.ok() {
			m.ref, m.match = r, "auth_uid"
			return m, err
		}
	}
	if name = strings.TrimSpace(name); name != "" {
		// driverDisplayName (fullNameTh, then first + last) or the Latin "first last"; the first driver in doc-id
		// order wins, as Array.find over the Firestore listing does.
		r, err := c.one(`SELECT id, tenant_id FROM drivers
			WHERE legacy_doc_id IS NOT NULL AND (
			      coalesce(nullif(btrim(full_name_th), ''), btrim(concat_ws(' ', nullif(first_name, ''), nullif(last_name, '')))) = $1
			   OR btrim(concat_ws(' ', nullif(first_name, ''), nullif(last_name, ''))) = $1)
			ORDER BY legacy_doc_id COLLATE "C" LIMIT 1`, name)
		if err != nil || r.ok() {
			m.ref, m.match = r, "name"
			return m, err
		}
	}
	return m, nil
}

// lookups are the tenancy.Lookups of this transaction.
func (c *docCtx) lookups() tenancy.Lookups {
	get := func(f func(string) (ref, error)) func(string) string {
		return func(s string) string {
			r, err := f(s)
			if err != nil {
				c.e.cfg.Log.Warn().Err(err).Msg("tenant lookup failed")
				return ""
			}
			return c.stamped(r)
		}
	}
	return tenancy.Lookups{
		TenantOfTask:   get(func(s string) (ref, error) { r, _, err := c.taskByRef(s); return r, err }),
		TenantOfTrip:   get(c.tripByRef),
		TenantOfDriver: get(func(s string) (ref, error) { m, err := c.driverByRef(s, ""); return m.ref, err }),
		TenantOfTruck:  get(c.truckByRef),
		TenantOfCarrier: func(sub string) string {
			r, err := c.one(`SELECT id, id FROM tenants WHERE legacy_doc_id = $1 AND kind IN ('carrier', 'own_fleet')`, sub)
			if err != nil || !r.ok() {
				return ""
			}
			return r.id.String()
		},
		OwnFleetTenantID: c.e.ownFleet,
	}
}

// taskByRef resolves a trip's taskId (Q4): the tasks doc id, then the business number only when exactly one task
// carries it. match is doc_id | task_no | ambiguous | none.
func (c *docCtx) taskByRef(raw string) (ref, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ref{}, "none", nil
	}
	r, err := c.one(`SELECT id, tenant_id FROM tasks WHERE legacy_doc_id = $1`, raw)
	if err != nil || r.ok() {
		return r, "doc_id", err
	}
	rows, err := c.tx.Query(c.ctx, `SELECT id, tenant_id FROM tasks WHERE task_no = $1 LIMIT 2`, raw)
	if err != nil {
		return ref{}, "", fmt.Errorf("etl: %s: task lookup: %w", c.doc.Path, err)
	}
	refs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ref, error) {
		var x ref
		return x, row.Scan(&x.id, &x.tenant)
	})
	switch {
	case err != nil:
		return ref{}, "", fmt.Errorf("etl: %s: task lookup: %w", c.doc.Path, err)
	case len(refs) == 1:
		return refs[0], "task_no", nil
	case len(refs) > 1:
		return ref{}, "ambiguous", nil
	}
	return ref{}, "none", nil
}

// tripByRef resolves an incident or standby tripId: trip_records.legacy_doc_id, then trip_no, then a renamed
// trip's old number (trip_no_history).
func (c *docCtx) tripByRef(raw string) (ref, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ref{}, nil
	}
	return c.one(`SELECT id, tenant_id FROM (
		  SELECT id, tenant_id, 1 AS rank FROM trip_records WHERE legacy_doc_id = $1
		  UNION ALL SELECT id, tenant_id, 2 FROM trip_records WHERE trip_no = $1
		  UNION ALL SELECT r.id, r.tenant_id, 3 FROM trip_no_history h JOIN trip_records r ON r.id = h.trip_id WHERE h.old_trip_no = $1
		) t ORDER BY rank LIMIT 1`, raw)
}

// truckByRef resolves a truckId (trucks doc id).
func (c *docCtx) truckByRef(raw string) (ref, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ref{}, nil
	}
	return c.one(`SELECT id, tenant_id FROM trucks WHERE legacy_doc_id = $1`, raw)
}

// truck resolves the truckId field; an id that matches no truck is NULL + truck_unresolved.
func (c *docCtx) truck(field string) (ref, error) {
	raw := c.str(field)
	r, err := c.truckByRef(raw)
	if err == nil && raw != "" && !r.ok() {
		c.find(field, ReasonTruckUnresolved, "no truck with this doc id", raw)
	}
	return r, err
}

// party resolves a customer id (billingCustomerId, *LinkedCustomerId, customerId) to billing_parties.id:
// customers first, then subcontractors (ADR 0028, tripBillingOnDelivered.ts:45-64); NULL + party_unresolved.
func (c *docCtx) party(field string) (*uuid.UUID, error) {
	raw := c.str(field)
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
		c.find(field, ReasonPartyUnresolved, "matches no customers or subcontractors doc", raw)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("etl: %s: party lookup: %w", c.doc.Path, err)
	}
	return &id, nil
}

// partyBasis is the billing_date_basis of a party.
func (c *docCtx) partyBasis(id *uuid.UUID) (string, error) {
	if id == nil {
		return "", nil
	}
	var b string
	if err := c.tx.QueryRow(c.ctx, `SELECT billing_date_basis FROM billing_parties WHERE id = $1`, *id).Scan(&b); err != nil {
		return "", fmt.Errorf("etl: %s: party basis: %w", c.doc.Path, err)
	}
	return b, nil
}

// user resolves a Firebase uid to users.id; NULL + user_unresolved.
func (c *docCtx) user(field, uid string) (*uuid.UUID, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil, nil
	}
	var id uuid.UUID
	err := c.tx.QueryRow(c.ctx, `SELECT id FROM users WHERE legacy_auth_uid = $1`, uid).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.find(field, ReasonUserUnresolved, "matches no users.legacy_auth_uid", uid)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("etl: %s: user lookup: %w", c.doc.Path, err)
	}
	return &id, nil
}

// place is a resolved origin, destination or source hub.
type place struct {
	hubID  *uuid.UUID
	socKey *string
}

// hub resolves hub or destination text (Q6, Q7; placeFilter.ts:70-89): a SOC spelling becomes its SOC key; a
// code passes through (the full raw code first, so SPK-GW matches its own hub, then the code before " - ", then
// the code before the first "-"); then hub_name_aliases (name -> code only, never code -> name). Unresolved text
// stays raw and raises hub_unresolved (informational).
func (c *docCtx) hub(field, raw string) (place, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return place{}, nil
	}
	key := compute.NormalizeDestinationCode(raw)
	if key == "SOCE" || key == "SOCN" || key == "SOCW" {
		return place{socKey: &key}, nil
	}
	up := strings.ToUpper(raw)
	candidates := []string{up, compute.ExtractHubID(raw)}
	if i := strings.IndexByte(up, '-'); i > 0 {
		candidates = append(candidates, strings.TrimSpace(up[:i]))
	}
	for _, code := range candidates {
		if code == "" {
			continue
		}
		var id uuid.UUID
		err := c.tx.QueryRow(c.ctx, `SELECT id FROM hubs WHERE source_id = $1`, code).Scan(&id)
		if err == nil {
			return place{hubID: &id}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return place{}, fmt.Errorf("etl: %s: hub lookup: %w", c.doc.Path, err)
		}
	}
	var id uuid.UUID
	err := c.tx.QueryRow(c.ctx, `SELECT hub_id FROM hub_name_aliases WHERE alias = $1`, raw).Scan(&id)
	if err == nil {
		return place{hubID: &id}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return place{}, fmt.Errorf("etl: %s: alias lookup: %w", c.doc.Path, err)
	}
	c.find(field, ReasonHubUnresolved, "no hub, SOC or alias", raw)
	return place{}, nil
}

// contractorOf is tenants.contractor_tenant_id of a tenant.
func (c *docCtx) contractorOf(t uuid.UUID) (uuid.UUID, error) {
	var ct *uuid.UUID
	if err := c.tx.QueryRow(c.ctx, `SELECT contractor_tenant_id FROM tenants WHERE id = $1`, t).Scan(&ct); err != nil {
		return uuid.Nil, fmt.Errorf("etl: %s: tenant lookup: %w", c.doc.Path, err)
	}
	if ct == nil {
		return uuid.Nil, nil
	}
	return *ct, nil
}

// stamp resolves the tenant of a tenant-stamped row through the pure resolver (tenancy.Resolve). When the chain
// runs out the row goes to the quarantine tenant with a row-level tenant_unresolved finding (R11). A row linked
// to a quarantined parent follows it there: the tenant-consistency triggers of 0004 require a child to carry its
// parent's tenant, and a re-home moves both together (Appendix C §C.3.10).
func (c *docCtx) stamp(collection string, fields map[string]any, parents ...ref) uuid.UUID {
	return c.stampWith(collection, fields, c.lookups(), parents...)
}

// stampWith is stamp with explicit lookups (a task's driver matched by name).
func (c *docCtx) stampWith(collection string, fields map[string]any, l tenancy.Lookups, parents ...ref) uuid.UUID {
	res, ok := tenancy.Resolve(collection, fields, l)
	tenant, _ := uuid.Parse(res.TenantID)
	if ok && tenant != uuid.Nil {
		for _, p := range parents {
			if p.ok() {
				if c.quarantined(p) && tenant != c.e.quar {
					c.find("", ReasonTenantUnresolved, "its "+linkName(collection)+" is in the quarantine tenant; the row follows it", nil)
					return c.toQuarantine()
				}
				break // the trigger checks only the first linked parent
			}
		}
		c.tenantSource = res.Source
		return tenant
	}
	c.find("", ReasonTenantUnresolved, "the tenant chain of "+collection+" ran out", nil)
	return c.toQuarantine()
}

func linkName(collection string) string {
	if collection == tenancy.IncidentReport {
		return "trip"
	}
	return "linked task or trip"
}

func (c *docCtx) toQuarantine() uuid.UUID {
	c.outcome = Quarantined
	c.tenantSource = tenancy.SourceQuarantine
	return c.e.quar
}
