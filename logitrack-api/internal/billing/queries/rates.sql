-- Rate tables (Appendix B §B.2.14, main spec §6.12, T37): route rate cards and fuel adjustments are announcement rows
-- (INSERT, then void only: trg_announcement_void_only, DELETE revoked), service fees are mutable and deletable (one per
-- party and fee type, D5), standby rates are mutable with a soft delete (voided_at, R20). Every row carries the billing
-- carrier's tenant (R61: the rate-card owner, own fleet today), stamped from the request's effective tenant with
-- tenant_source 'form'; RLS G-tenant decides (staff of the tenant reach). Requests run in db.WithPrincipal and name the
-- reach (main spec §4.7; authz.Principal.TenantReach). The Party* reads load one party's whole bundle (every tenant,
-- voided rows too) for cache:ratecard:{partyId} in a READ ONLY system transaction, so every reader caches the same
-- value; the service then keeps the caller's reach. The Pricing* reads are the pricing transaction's (R17): live rows
-- of one party and one billing carrier FOR SHARE (a row of another carrier never prices, whoever wrote it), in the R16
-- order (legacy_doc_id by bytes, NULLs last, then created_at, id; the selector of internal/billing/compute sorts again,
-- so the order is only determinism). Rows carry customer_id = the customer or carrier the party bills
-- (coalesce(billing_parties.customer_id, tenant_id)), the id the web keys its rate-card caches by (Appendix E).

-- name: ResolveParty :one
-- The billing party a request names: its billing_parties id, or the customer or carrier it bills. Code and name feed
-- invoice numbers and the statement snapshot (customers.code, else the carrier's code; main spec §6.14).
SELECT bp.id, bp.kind, bp.billing_date_basis, c.code AS customer_code, c.name AS customer_name,
       t.code AS tenant_code, t.name_th AS tenant_name, c.payment_terms_days,
       coalesce(bp.customer_id, bp.tenant_id, bp.id)::uuid AS billed_id
FROM billing_parties bp
LEFT JOIN customers c ON c.id = bp.customer_id
LEFT JOIN tenants t ON t.id = bp.tenant_id
WHERE bp.id = sqlc.arg(id) OR bp.customer_id = sqlc.arg(id) OR bp.tenant_id = sqlc.arg(id)
ORDER BY (bp.id = sqlc.arg(id)) DESC, bp.id
LIMIT 1;

-- name: ListRateEntries :many
-- GET /v1/billing/rate-entries without customerId: keyset (effective_from_at, import_id, id) DESC; import ids compare by
-- bytes (COLLATE "C"), as the bundle path pages in Go.
SELECT e.id, e.legacy_doc_id, e.tenant_id, e.billing_party_id, e.import_id, e.hub_code, e.raw_hub_name,
       e.destination_code, e.vehicle_class, e.rate_thb, e.distance_km, e.job_category, e.effective_from_date,
       e.effective_from_at, e.imported_at, e.created_by, e.created_at, e.updated_at, e.voided, e.voided_at, e.voided_by,
       e.voided_reason, coalesce(bp.customer_id, bp.tenant_id, e.billing_party_id)::uuid AS customer_id
FROM customer_rate_entries e
JOIN billing_parties bp ON bp.id = e.billing_party_id
WHERE (sqlc.arg(all_tenants)::boolean OR e.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.arg(include_voided)::boolean OR NOT e.voided)
  AND (sqlc.narg(after_at)::timestamptz IS NULL
       OR (e.effective_from_at, e.import_id COLLATE "C", e.id)
          < (sqlc.narg(after_at)::timestamptz, sqlc.arg(after_import)::text COLLATE "C", sqlc.arg(after_id)::uuid))
ORDER BY e.effective_from_at DESC, e.import_id COLLATE "C" DESC, e.id DESC
LIMIT sqlc.arg(row_limit);

-- name: PartyRateEntries :many
-- The rate entries of one party for the bundle cache: every tenant, voided rows too.
SELECT e.id, e.legacy_doc_id, e.tenant_id, e.billing_party_id, e.import_id, e.hub_code, e.raw_hub_name,
       e.destination_code, e.vehicle_class, e.rate_thb, e.distance_km, e.job_category, e.effective_from_date,
       e.effective_from_at, e.imported_at, e.created_by, e.created_at, e.updated_at, e.voided, e.voided_at, e.voided_by,
       e.voided_reason, coalesce(bp.customer_id, bp.tenant_id, e.billing_party_id)::uuid AS customer_id
FROM customer_rate_entries e
JOIN billing_parties bp ON bp.id = e.billing_party_id
WHERE e.billing_party_id = sqlc.arg(party_id)
ORDER BY e.effective_from_at DESC, e.import_id COLLATE "C" DESC, e.id DESC;

-- name: GetRateEntry :one
SELECT e.id, e.legacy_doc_id, e.tenant_id, e.billing_party_id, e.import_id, e.hub_code, e.raw_hub_name,
       e.destination_code, e.vehicle_class, e.rate_thb, e.distance_km, e.job_category, e.effective_from_date,
       e.effective_from_at, e.imported_at, e.created_by, e.created_at, e.updated_at, e.voided, e.voided_at, e.voided_by,
       e.voided_reason, coalesce(bp.customer_id, bp.tenant_id, e.billing_party_id)::uuid AS customer_id
FROM customer_rate_entries e
JOIN billing_parties bp ON bp.id = e.billing_party_id
WHERE e.id = sqlc.arg(id);

-- name: InsertRateEntries :many
-- One import (or one manual row): the arrays are the rows in input order; text arrays carry the optional values (an
-- empty string is NULL) so a row may leave raw_hub_name or distance_km empty. created_at is the transaction start, so the rows of one
-- import tie on it and their uuidv7 ids (monotonic within the session) keep the input order (R16).
INSERT INTO customer_rate_entries (tenant_id, tenant_source, billing_party_id, import_id, hub_code, raw_hub_name,
                                   destination_code, vehicle_class, rate_thb, distance_km, job_category,
                                   effective_from_date, effective_from_at, imported_at, created_by)
SELECT sqlc.arg(tenant_id)::uuid, 'form', sqlc.arg(party_id)::uuid, sqlc.arg(import_id)::text,
       (sqlc.arg(hub_codes)::text[])[i], nullif((sqlc.arg(raw_hub_names)::text[])[i], ''),
       (sqlc.arg(destination_codes)::text[])[i], (sqlc.arg(vehicle_classes)::text[])[i],
       ((sqlc.arg(rates)::text[])[i])::numeric(14,2), nullif((sqlc.arg(distances)::text[])[i], '')::numeric(9,2),
       (sqlc.arg(job_categories)::text[])[i], sqlc.arg(effective_from_date)::date,
       bkk_midnight(sqlc.arg(effective_from_date)::date), sqlc.arg(imported_at)::timestamptz, sqlc.narg(created_by)::uuid
FROM generate_subscripts(sqlc.arg(hub_codes)::text[], 1) AS i
ORDER BY i
RETURNING id;

-- name: LockRateEntry :one
SELECT id, tenant_id, billing_party_id, voided FROM customer_rate_entries WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: VoidRateEntry :exec
-- The only UPDATE an announcement row accepts (trg_announcement_void_only): voided false -> true with its columns.
UPDATE customer_rate_entries
SET voided = true, voided_at = sqlc.arg(voided_at), voided_by = sqlc.arg(voided_by), voided_reason = sqlc.arg(reason)
WHERE id = sqlc.arg(id);

-- name: PricingRateEntries :many
-- Pricing transaction (R17): live rate entries of a party and billing carrier FOR SHARE (a void waits for the price that
-- read the row).
SELECT e.id, e.legacy_doc_id, e.tenant_id, e.billing_party_id, e.import_id, e.hub_code, e.destination_code,
       e.vehicle_class, e.rate_thb, e.job_category, e.effective_from_at, e.created_at
FROM customer_rate_entries e
WHERE e.billing_party_id = sqlc.arg(party_id) AND e.tenant_id = sqlc.arg(carrier_id) AND NOT e.voided
ORDER BY e.legacy_doc_id COLLATE "C" ASC NULLS LAST, e.created_at, e.id
FOR SHARE;

-- name: ListFuelAdjustments :many
-- GET /v1/billing/fuel-adjustments without customerId: newest round first.
SELECT a.id, a.legacy_doc_id, a.tenant_id, a.billing_party_id, a.effective_from_date, a.effective_from_at,
       a.rate_multiplier, a.add_thb_per_trip, a.reference_fuel_price_thb, a.announcement_note, a.fuel_band_enabled,
       a.fuel_band_baseline_floor, a.fuel_band_thb_per_baht, a.created_by, a.created_at, a.updated_at, a.voided,
       a.voided_at, a.voided_by, a.voided_reason, coalesce(bp.customer_id, bp.tenant_id, a.billing_party_id)::uuid AS customer_id
FROM customer_fuel_rate_adjustments a
JOIN billing_parties bp ON bp.id = a.billing_party_id
WHERE (sqlc.arg(all_tenants)::boolean OR a.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.arg(include_voided)::boolean OR NOT a.voided)
ORDER BY a.effective_from_at DESC, a.id DESC;

-- name: PartyFuelAdjustments :many
SELECT a.id, a.legacy_doc_id, a.tenant_id, a.billing_party_id, a.effective_from_date, a.effective_from_at,
       a.rate_multiplier, a.add_thb_per_trip, a.reference_fuel_price_thb, a.announcement_note, a.fuel_band_enabled,
       a.fuel_band_baseline_floor, a.fuel_band_thb_per_baht, a.created_by, a.created_at, a.updated_at, a.voided,
       a.voided_at, a.voided_by, a.voided_reason, coalesce(bp.customer_id, bp.tenant_id, a.billing_party_id)::uuid AS customer_id
FROM customer_fuel_rate_adjustments a
JOIN billing_parties bp ON bp.id = a.billing_party_id
WHERE a.billing_party_id = sqlc.arg(party_id)
ORDER BY a.effective_from_at DESC, a.id DESC;

-- name: GetFuelAdjustment :one
SELECT a.id, a.legacy_doc_id, a.tenant_id, a.billing_party_id, a.effective_from_date, a.effective_from_at,
       a.rate_multiplier, a.add_thb_per_trip, a.reference_fuel_price_thb, a.announcement_note, a.fuel_band_enabled,
       a.fuel_band_baseline_floor, a.fuel_band_thb_per_baht, a.created_by, a.created_at, a.updated_at, a.voided,
       a.voided_at, a.voided_by, a.voided_reason, coalesce(bp.customer_id, bp.tenant_id, a.billing_party_id)::uuid AS customer_id
FROM customer_fuel_rate_adjustments a
JOIN billing_parties bp ON bp.id = a.billing_party_id
WHERE a.id = sqlc.arg(id);

-- name: InsertFuelAdjustment :one
-- Multiplier and add are computed once, here, from the request's mode (main spec §6.6) and never recomputed.
INSERT INTO customer_fuel_rate_adjustments (tenant_id, tenant_source, billing_party_id, effective_from_date,
                                            effective_from_at, rate_multiplier, add_thb_per_trip,
                                            reference_fuel_price_thb, announcement_note, fuel_band_enabled,
                                            fuel_band_baseline_floor, fuel_band_thb_per_baht, created_by)
VALUES (sqlc.arg(tenant_id), 'form', sqlc.arg(party_id), sqlc.arg(effective_from_date)::date,
        bkk_midnight(sqlc.arg(effective_from_date)::date), (sqlc.arg(rate_multiplier)::text)::numeric(10,6),
        (sqlc.arg(add_thb_per_trip)::text)::numeric(14,2), nullif(sqlc.arg(reference_fuel_price)::text, '')::numeric(8,2),
        sqlc.arg(announcement_note), sqlc.arg(fuel_band_enabled), sqlc.narg(fuel_band_baseline_floor)::int,
        nullif(sqlc.arg(fuel_band_thb_per_baht)::text, '')::numeric(10,2), sqlc.narg(created_by)::uuid)
RETURNING id;

-- name: LockFuelAdjustment :one
SELECT id, tenant_id, billing_party_id, voided FROM customer_fuel_rate_adjustments WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: VoidFuelAdjustment :exec
UPDATE customer_fuel_rate_adjustments
SET voided = true, voided_at = sqlc.arg(voided_at), voided_by = sqlc.arg(voided_by), voided_reason = sqlc.arg(reason)
WHERE id = sqlc.arg(id);

-- name: PricingFuelAdjustments :many
-- Pricing transaction (R17): live fuel rounds of a party and billing carrier FOR SHARE, R16 order.
SELECT a.id, a.legacy_doc_id, a.tenant_id, a.billing_party_id, a.effective_from_at, a.rate_multiplier,
       a.add_thb_per_trip, a.reference_fuel_price_thb, a.created_at
FROM customer_fuel_rate_adjustments a
WHERE a.billing_party_id = sqlc.arg(party_id) AND a.tenant_id = sqlc.arg(carrier_id) AND NOT a.voided
ORDER BY a.legacy_doc_id COLLATE "C" ASC NULLS LAST, a.created_at, a.id
FOR SHARE;

-- name: ListServiceFees :many
-- GET /v1/billing/service-fees without customerId: newest first (customer_service_fees_party, legacy createdAt desc).
SELECT f.id, f.legacy_doc_id, f.tenant_id, f.billing_party_id, f.fee_type, f.custom_type_name, f.amount_thb, f.unit,
       f.note, f.created_at, f.updated_at, coalesce(bp.customer_id, bp.tenant_id, f.billing_party_id)::uuid AS customer_id
FROM customer_service_fees f
JOIN billing_parties bp ON bp.id = f.billing_party_id
WHERE (sqlc.arg(all_tenants)::boolean OR f.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
ORDER BY f.created_at DESC, f.id DESC;

-- name: PartyServiceFees :many
SELECT f.id, f.legacy_doc_id, f.tenant_id, f.billing_party_id, f.fee_type, f.custom_type_name, f.amount_thb, f.unit,
       f.note, f.created_at, f.updated_at, coalesce(bp.customer_id, bp.tenant_id, f.billing_party_id)::uuid AS customer_id
FROM customer_service_fees f
JOIN billing_parties bp ON bp.id = f.billing_party_id
WHERE f.billing_party_id = sqlc.arg(party_id)
ORDER BY f.created_at DESC, f.id DESC;

-- name: GetServiceFee :one
SELECT f.id, f.legacy_doc_id, f.tenant_id, f.billing_party_id, f.fee_type, f.custom_type_name, f.amount_thb, f.unit,
       f.note, f.created_at, f.updated_at, coalesce(bp.customer_id, bp.tenant_id, f.billing_party_id)::uuid AS customer_id
FROM customer_service_fees f
JOIN billing_parties bp ON bp.id = f.billing_party_id
WHERE f.id = sqlc.arg(id);

-- name: InsertServiceFee :one
INSERT INTO customer_service_fees (tenant_id, tenant_source, billing_party_id, fee_type, custom_type_name, amount_thb,
                                   unit, note)
VALUES (sqlc.arg(tenant_id), 'form', sqlc.arg(party_id), sqlc.arg(fee_type), sqlc.narg(custom_type_name),
        (sqlc.arg(amount_thb)::text)::numeric(14,2), sqlc.arg(unit), sqlc.narg(note))
RETURNING id;

-- name: LockServiceFee :one
SELECT f.id, f.legacy_doc_id, f.tenant_id, f.billing_party_id, f.fee_type, f.custom_type_name, f.amount_thb, f.unit,
       f.note, f.created_at, f.updated_at, coalesce(bp.customer_id, bp.tenant_id, f.billing_party_id)::uuid AS customer_id
FROM customer_service_fees f
JOIN billing_parties bp ON bp.id = f.billing_party_id
WHERE f.id = sqlc.arg(id)
FOR UPDATE OF f;

-- name: UpdateServiceFee :exec
UPDATE customer_service_fees
SET fee_type = sqlc.arg(fee_type), custom_type_name = sqlc.narg(custom_type_name),
    amount_thb = (sqlc.arg(amount_thb)::text)::numeric(14,2), unit = sqlc.arg(unit), note = sqlc.narg(note)
WHERE id = sqlc.arg(id);

-- name: DeleteServiceFee :execrows
DELETE FROM customer_service_fees WHERE id = sqlc.arg(id);

-- name: PricingServiceFee :one
-- Pricing transaction (R17): the fee of one type of a party and billing carrier FOR SHARE (one per party and type
-- after D5: customer_service_fees_one_per_type).
SELECT f.id, f.tenant_id, f.amount_thb
FROM customer_service_fees f
WHERE f.billing_party_id = sqlc.arg(party_id) AND f.tenant_id = sqlc.arg(carrier_id) AND f.fee_type = sqlc.arg(fee_type)
ORDER BY f.created_at, f.id
LIMIT 1
FOR SHARE;

-- name: ListStandbyRates :many
-- GET /v1/billing/standby-rates without customerId: newest effective date first.
SELECT r.id, r.legacy_doc_id, r.tenant_id, r.billing_party_id, r.rate_thb, r.effective_from_date, r.effective_from_at,
       r.note, r.created_at, r.updated_at, r.voided_at, r.voided_by, r.voided_reason,
       coalesce(bp.customer_id, bp.tenant_id, r.billing_party_id)::uuid AS customer_id
FROM standby_rate_entries r
JOIN billing_parties bp ON bp.id = r.billing_party_id
WHERE (sqlc.arg(all_tenants)::boolean OR r.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.arg(include_voided)::boolean OR r.voided_at IS NULL)
ORDER BY r.effective_from_at DESC, r.id DESC;

-- name: PartyStandbyRates :many
SELECT r.id, r.legacy_doc_id, r.tenant_id, r.billing_party_id, r.rate_thb, r.effective_from_date, r.effective_from_at,
       r.note, r.created_at, r.updated_at, r.voided_at, r.voided_by, r.voided_reason,
       coalesce(bp.customer_id, bp.tenant_id, r.billing_party_id)::uuid AS customer_id
FROM standby_rate_entries r
JOIN billing_parties bp ON bp.id = r.billing_party_id
WHERE r.billing_party_id = sqlc.arg(party_id)
ORDER BY r.effective_from_at DESC, r.id DESC;

-- name: GetStandbyRate :one
SELECT r.id, r.legacy_doc_id, r.tenant_id, r.billing_party_id, r.rate_thb, r.effective_from_date, r.effective_from_at,
       r.note, r.created_at, r.updated_at, r.voided_at, r.voided_by, r.voided_reason,
       coalesce(bp.customer_id, bp.tenant_id, r.billing_party_id)::uuid AS customer_id
FROM standby_rate_entries r
JOIN billing_parties bp ON bp.id = r.billing_party_id
WHERE r.id = sqlc.arg(id);

-- name: InsertStandbyRate :one
-- effective_from_at is Bangkok midnight of the day, server-side (fixes the browser-local midnight, rate-card/page.tsx:1146).
INSERT INTO standby_rate_entries (tenant_id, tenant_source, billing_party_id, rate_thb, effective_from_date,
                                  effective_from_at, note)
VALUES (sqlc.arg(tenant_id), 'form', sqlc.arg(party_id), (sqlc.arg(rate_thb)::text)::numeric(14,2),
        sqlc.arg(effective_from_date)::date, bkk_midnight(sqlc.arg(effective_from_date)::date), sqlc.narg(note))
RETURNING id;

-- name: LockStandbyRate :one
SELECT r.id, r.legacy_doc_id, r.tenant_id, r.billing_party_id, r.rate_thb, r.effective_from_date, r.effective_from_at,
       r.note, r.created_at, r.updated_at, r.voided_at, r.voided_by, r.voided_reason,
       coalesce(bp.customer_id, bp.tenant_id, r.billing_party_id)::uuid AS customer_id
FROM standby_rate_entries r
JOIN billing_parties bp ON bp.id = r.billing_party_id
WHERE r.id = sqlc.arg(id)
FOR UPDATE OF r;

-- name: UpdateStandbyRate :exec
-- Edits never reprice a stored standby price (main spec §6.12): standby_records keep their amount.
UPDATE standby_rate_entries
SET rate_thb = (sqlc.arg(rate_thb)::text)::numeric(14,2), effective_from_date = sqlc.arg(effective_from_date)::date,
    effective_from_at = bkk_midnight(sqlc.arg(effective_from_date)::date), note = sqlc.narg(note)
WHERE id = sqlc.arg(id);

-- name: VoidStandbyRate :exec
UPDATE standby_rate_entries
SET voided_at = sqlc.arg(voided_at), voided_by = sqlc.arg(voided_by), voided_reason = sqlc.arg(reason)
WHERE id = sqlc.arg(id);

-- name: PricingStandbyRates :many
-- Pricing transaction (R17): live standby rates of a party and billing carrier FOR SHARE (voided_at IS NULL, R20), R16
-- order.
SELECT r.id, r.legacy_doc_id, r.tenant_id, r.billing_party_id, r.rate_thb, r.effective_from_at, r.created_at
FROM standby_rate_entries r
WHERE r.billing_party_id = sqlc.arg(party_id) AND r.tenant_id = sqlc.arg(carrier_id) AND r.voided_at IS NULL
ORDER BY r.legacy_doc_id COLLATE "C" ASC NULLS LAST, r.created_at, r.id
FOR SHARE;

-- name: OwnFleetTenant :one
-- The billing carrier of every price today (R61: the rate-card owner, the own fleet; main spec §19 Q13): the single
-- tenants row of kind own_fleet (tenants_one_own_fleet). A pricing transaction (db.WithSystem) reads it.
SELECT t.id FROM tenants t WHERE t.kind = 'own_fleet';
