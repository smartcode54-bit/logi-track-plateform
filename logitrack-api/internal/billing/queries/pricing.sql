-- Compute-on-delivery of T38 (main spec §6.10, §6.13; Appendix B §B.2.11-§B.2.14, §B.5.3): the pricing transaction of a
-- trip or a standby record, run in db.WithSystem (every billing carrier, R61) by the billing.compute and billing.backfill
-- consumers and by the billing endpoints. Order of locks: the trip (or standby record) FOR UPDATE, its task FOR SHARE
-- (a plan-date change committing meanwhile is either read or ordered after this price, so its event is never dropped
-- as stale wrongly), then the rate tables FOR SHARE (PricingRateEntries, rates.sql) and the period locks
-- (PeriodAdvisoryShared + PeriodStatements, statements.sql). Money is written as exact decimal text (two places, six
-- for the multiplier).

-- name: LockTripForPricing :one
SELECT tr.id, tr.tenant_id, tr.legacy_doc_id, tr.trip_no, tr.status, tr.task_id, tr.driver_id, tr.is_multi_delivery,
       tr.delivered_at, tr.delivered_via, tr.created_at, tr.billing_party_id, tr.billing_date, tr.job_category
FROM trip_records tr
WHERE tr.id = sqlc.arg(id)
FOR UPDATE;

-- name: TripSnapshotForPricing :one
SELECT s.trip_id, s.tenant_id, s.estimate_thb, s.unpriced_reason, s.fuel_adjustment_id, s.rate_multiplier,
       s.add_thb_per_trip, s.manual_override, s.is_multi_delivery, s.last_event_id
FROM trip_billing_snapshots s
WHERE s.trip_id = sqlc.arg(trip_id)
FOR UPDATE;

-- name: LockTaskForPricing :one
SELECT t.id, t.legacy_doc_id, t.job_category, t.plan_at, t.source_hub_raw, t.destination_raw, t.truck_type,
       t.billing_party_id, t.source_linked_party_id, t.destination_linked_party_id, t.driver_id, t.helper_driver_id
FROM tasks t
WHERE t.id = sqlc.arg(id)
FOR SHARE;

-- name: TripStopsForPricing :many
-- The delivery progress of a trip in completion order (main spec §6.8: delivered stops with a destination, by
-- completed_seq; legacy deliveryStopsProgress[] array order).
SELECT ds.stop_index, ds.destination_raw, ds.status, ds.completed_seq
FROM trip_delivery_stops ds
WHERE ds.trip_id = sqlc.arg(trip_id)
ORDER BY ds.completed_seq NULLS LAST, ds.stop_index;

-- name: PartyBasis :one
SELECT bp.billing_date_basis FROM billing_parties bp WHERE bp.id = sqlc.arg(id);

-- name: UpsertTripSnapshot :exec
-- The one writer of trip_billing_snapshots outside the ETL (main spec §6.10 states 5 and 7): every column of the
-- snapshot, so a reprice never leaves a stale provenance field behind (fixes the reduced set of setTripJobCategory,
-- fn:tripBillingOnDelivered.ts:849-902). Unpriced rows carry their reason and no amount (R62).
INSERT INTO trip_billing_snapshots (trip_id, tenant_id, tenant_source, estimate_thb, unpriced_reason, base_rate_thb,
                                    stop_charge_thb, rate_entry_id, rate_import_id, lookup_hub_code, lookup_destination_code,
                                    fuel_adjustment_id, rate_multiplier, add_thb_per_trip, fuel_effective_from_date,
                                    round_effective_from_date, fuel_band_lower_thb, fuel_band_upper_thb,
                                    reference_fuel_price_thb, is_multi_delivery, manual_override, job_category_at_pricing,
                                    computed_by, compute_version, last_event_id, computed_at)
VALUES (sqlc.arg(trip_id)::uuid, sqlc.arg(tenant_id)::uuid, 'form', sqlc.narg(estimate_thb)::text::numeric(14,2),
        sqlc.narg(unpriced_reason)::text, sqlc.narg(base_rate_thb)::text::numeric(14,2),
        sqlc.narg(stop_charge_thb)::text::numeric(14,2), sqlc.narg(rate_entry_id)::uuid, sqlc.narg(rate_import_id)::text,
        sqlc.narg(lookup_hub_code)::text, sqlc.narg(lookup_destination_code)::text, sqlc.narg(fuel_adjustment_id)::uuid,
        sqlc.arg(rate_multiplier)::text::numeric(10,6), sqlc.arg(add_thb_per_trip)::text::numeric(14,2),
        sqlc.narg(fuel_effective_from_date)::text::date, sqlc.narg(round_effective_from_date)::text::date,
        sqlc.narg(fuel_band_lower_thb)::text::numeric(8,2), sqlc.narg(fuel_band_upper_thb)::text::numeric(8,2),
        sqlc.narg(reference_fuel_price_thb)::text::numeric(8,2), sqlc.arg(is_multi_delivery)::boolean,
        sqlc.arg(manual_override)::boolean, sqlc.narg(job_category_at_pricing)::text, sqlc.arg(computed_by)::text, 1,
        sqlc.narg(last_event_id)::bigint, sqlc.arg(computed_at)::timestamptz)
ON CONFLICT (trip_id) DO UPDATE
SET tenant_id = EXCLUDED.tenant_id, tenant_source = EXCLUDED.tenant_source, estimate_thb = EXCLUDED.estimate_thb,
    unpriced_reason = EXCLUDED.unpriced_reason, base_rate_thb = EXCLUDED.base_rate_thb,
    stop_charge_thb = EXCLUDED.stop_charge_thb, rate_entry_id = EXCLUDED.rate_entry_id,
    rate_import_id = EXCLUDED.rate_import_id, lookup_hub_code = EXCLUDED.lookup_hub_code,
    lookup_destination_code = EXCLUDED.lookup_destination_code, fuel_adjustment_id = EXCLUDED.fuel_adjustment_id,
    rate_multiplier = EXCLUDED.rate_multiplier, add_thb_per_trip = EXCLUDED.add_thb_per_trip,
    fuel_effective_from_date = EXCLUDED.fuel_effective_from_date,
    round_effective_from_date = EXCLUDED.round_effective_from_date, fuel_band_lower_thb = EXCLUDED.fuel_band_lower_thb,
    fuel_band_upper_thb = EXCLUDED.fuel_band_upper_thb, reference_fuel_price_thb = EXCLUDED.reference_fuel_price_thb,
    is_multi_delivery = EXCLUDED.is_multi_delivery, manual_override = EXCLUDED.manual_override,
    job_category_at_pricing = EXCLUDED.job_category_at_pricing, computed_by = EXCLUDED.computed_by,
    compute_version = EXCLUDED.compute_version,
    last_event_id = coalesce(EXCLUDED.last_event_id, trip_billing_snapshots.last_event_id),
    computed_at = EXCLUDED.computed_at;

-- name: DeleteTripStopBreakdown :exec
DELETE FROM trip_billing_stop_breakdown WHERE trip_id = sqlc.arg(trip_id);

-- name: InsertTripStopBreakdown :exec
-- The multi-drop breakdown of a priced trip (billingMultiDeliveryBreakdown[]), arrays in stop order.
INSERT INTO trip_billing_stop_breakdown (trip_id, stop_index, destination_code, base_rate_thb, final_rate_thb)
SELECT sqlc.arg(trip_id)::uuid, (sqlc.arg(stop_indexes)::int[])[i], (sqlc.arg(destination_codes)::text[])[i],
       ((sqlc.arg(base_rates)::text[])[i])::numeric(14,2), ((sqlc.arg(final_rates)::text[])[i])::numeric(14,2)
FROM generate_subscripts(sqlc.arg(stop_indexes)::int[], 1) AS i;

-- name: SetSnapshotLastEvent :exec
UPDATE trip_billing_snapshots SET last_event_id = sqlc.arg(last_event_id)::bigint WHERE trip_id = sqlc.arg(trip_id);

-- name: StampTripAxis :exec
-- The axis columns trip_records carries for billing (main spec §6.3, §6.10): the resolved party and the stored billing
-- date; a priced trip also its resolved หลัก/เสริม (job_category NULL keeps the stored value).
UPDATE trip_records
SET billing_party_id = sqlc.arg(party_id)::uuid, billing_date = sqlc.narg(billing_date)::timestamptz,
    job_category = coalesce(sqlc.narg(job_category)::text, job_category)
WHERE id = sqlc.arg(id);

-- name: RestampTripBillingDate :exec
-- A frozen trip's forced recompute moves its axis only (main spec §6.10 state 3, ADR 0027 §9).
UPDATE trip_records SET billing_date = sqlc.arg(billing_date)::timestamptz WHERE id = sqlc.arg(id);

-- name: SetTaskJobCategory :exec
-- POST /v1/trips/{id}/job-category: the task's category is the source of truth (ADR-0006), written with the trip's.
UPDATE tasks SET job_category = sqlc.arg(job_category)::text WHERE id = sqlc.arg(id);

-- name: TaskDeliveredTrips :many
-- The delivered trips of a task (task.plan_date_changed restamps or reprices each).
SELECT tr.id FROM trip_records tr WHERE tr.task_id = sqlc.arg(task_id)::uuid AND tr.status = 'delivered' ORDER BY tr.id;

-- name: TripVisible :one
-- A request's reach check before a compute: RLS of the caller's transaction decides (no row = 404).
SELECT tr.id FROM trip_records tr WHERE tr.id = sqlc.arg(id);

-- name: TripTaskLegacyDocID :one
-- The Firestore document of a trip's task (the job-category write-back, legacy.go): NULL for a task born in PostgreSQL.
SELECT t.legacy_doc_id FROM trip_records tr JOIN tasks t ON t.id = tr.task_id WHERE tr.id = sqlc.arg(id);

-- name: PartyLegacyDocID :one
-- The id Firestore names a billing party by (customerId, billingCustomerId; the etl projections' partyDocSQL): its
-- customer's document id, else its carrier's (subcontractors), else the PostgreSQL id of either.
SELECT coalesce(cu.legacy_doc_id, cu.id::text, tn.legacy_doc_id, tn.id::text)::text AS doc_id
FROM billing_parties bp
LEFT JOIN customers cu ON cu.id = bp.customer_id
LEFT JOIN tenants tn ON tn.id = bp.tenant_id
WHERE bp.id = sqlc.arg(id);

-- name: ResolveTripRef :one
-- The trip a mobile shim names (Appendix B §B.2.22): the PostgreSQL id (id: the ref parsed as a canonical uuid, else
-- NULL), the Firestore doc id (legacy_doc_id) or the trip number, in that order, with its driver and its task's drivers
-- for the driver:self check. Each branch is an index probe (BitmapOr over trip_records_pkey, trip_records_legacy and
-- trip_records_trip_no; explain_integration_test.go). Read in the system context: a trip that is not there is
-- not_mirrored, one of another driver is refused.
SELECT tr.id, tr.driver_id, t.driver_id AS task_driver_id, t.helper_driver_id AS task_helper_driver_id,
       (CASE WHEN tr.id = sqlc.narg(id)::uuid THEN 0 WHEN tr.legacy_doc_id = sqlc.arg(ref)::text THEN 1 ELSE 2 END)::int AS rank
FROM trip_records tr
LEFT JOIN tasks t ON t.id = tr.task_id
WHERE tr.id = sqlc.narg(id)::uuid OR tr.legacy_doc_id = sqlc.arg(ref)::text OR tr.trip_no = sqlc.arg(ref)::text
ORDER BY rank, tr.id
LIMIT 1;

-- --- standby -------------------------------------------------------------------------------------------------------

-- name: LockStandbyForPricing :one
SELECT sr.id, sr.tenant_id, sr.legacy_doc_id, sr.status, sr.task_id, sr.customer_party_id, sr.job_category,
       sr.started_at, sr.ended_at, sr.created_at, sr.billing_estimate_thb, sr.billing_party_id, sr.billing_rate_source,
       sr.billing_rate_entry_id, sr.billing_unpriced_reason, sr.billing_tenant_id, sr.billing_last_event_id
FROM standby_records sr
WHERE sr.id = sqlc.arg(id)
FOR UPDATE;

-- name: StandbyTaskForPricing :one
SELECT t.source_linked_party_id, t.destination_linked_party_id, t.job_category
FROM tasks t
WHERE t.id = sqlc.arg(id)
FOR SHARE;

-- name: SetStandbyPrice :exec
-- A priced standby record (main spec §6.9; fn:standbyBilling.ts:183-207): the inline snapshot, its billing carrier
-- (0015, R61) and the denormalised หลัก/เสริม label of its task when the record has none (ADR 0010).
UPDATE standby_records
SET billing_estimate_thb = sqlc.arg(estimate_thb)::text::numeric(14,2), billing_party_id = sqlc.arg(party_id)::uuid,
    billing_rate_source = sqlc.arg(rate_source)::text, billing_rate_entry_id = sqlc.narg(rate_entry_id)::uuid,
    billing_effective_from_date = sqlc.narg(effective_from_date)::text::date, billing_unpriced_reason = NULL,
    billing_computed_at = sqlc.arg(computed_at)::timestamptz, billing_tenant_id = sqlc.arg(carrier_id)::uuid,
    job_category = coalesce(job_category, sqlc.narg(job_category)::text),
    billing_last_event_id = coalesce(sqlc.narg(last_event_id)::bigint, billing_last_event_id)
WHERE id = sqlc.arg(id);

-- name: SetStandbyUnpriced :exec
-- An unpriced standby record keeps its reason (R62); a priced one keeps its price (a forced recompute that finds no
-- rate any more writes nothing, as fn:standbyBilling.ts:209).
UPDATE standby_records
SET billing_unpriced_reason = CASE WHEN billing_estimate_thb IS NULL THEN sqlc.arg(reason)::text ELSE billing_unpriced_reason END,
    billing_computed_at = CASE WHEN billing_estimate_thb IS NULL THEN sqlc.arg(computed_at)::timestamptz ELSE billing_computed_at END,
    billing_last_event_id = coalesce(sqlc.narg(last_event_id)::bigint, billing_last_event_id)
WHERE id = sqlc.arg(id);

-- name: SetStandbyLastEvent :exec
UPDATE standby_records SET billing_last_event_id = sqlc.arg(last_event_id)::bigint WHERE id = sqlc.arg(id);

-- name: SetStandbyCustomer :exec
-- PATCH /v1/standby/{id}/customer (assignStandbyCustomerAndPrice, billing.ts:1613-1632): the admin's case-by-case repair.
UPDATE standby_records
SET customer_party_id = sqlc.arg(party_id)::uuid, customer_resolved = true, customer_resolved_from = 'manual'
WHERE id = sqlc.arg(id);

-- name: StandbyVisible :one
SELECT sr.id FROM standby_records sr WHERE sr.id = sqlc.arg(id);

-- --- sweeps --------------------------------------------------------------------------------------------------------

-- name: SafetyNetTrips :many
-- job.billing.safety-net (main spec §6.13): delivered trips of the last window without a price, oldest first; with
-- born_in_pg_only (the mirror runs while billing is Firestore-owned) only the trips the mirror does not write.
SELECT tr.id
FROM trip_records tr
LEFT JOIN trip_billing_snapshots s ON s.trip_id = tr.id
WHERE tr.status = 'delivered' AND tr.delivered_at >= sqlc.arg(since)::timestamptz AND s.estimate_thb IS NULL
  AND (NOT sqlc.arg(born_in_pg_only)::boolean OR tr.legacy_doc_id IS NULL)
ORDER BY tr.delivered_at, tr.id
LIMIT sqlc.arg(row_limit);

-- name: SafetyNetStandby :many
SELECT sr.id
FROM standby_records sr
WHERE sr.status = 'completed' AND sr.ended_at >= sqlc.arg(since)::timestamptz AND sr.billing_estimate_thb IS NULL
  AND (NOT sqlc.arg(born_in_pg_only)::boolean OR sr.legacy_doc_id IS NULL)
ORDER BY sr.ended_at, sr.id
LIMIT sqlc.arg(row_limit);

-- name: BackfillTrips :many
-- POST /v1/jobs/billing.backfill-trips (backfillTripBillingSnapshots, fn:tripBillingOnDelivered.ts:990-1180): delivered
-- trips in [period_from, period_to) on the delivered axis, or, for a single plan-basis party, on billing_date (its
-- invoice axis, ADR 0027), at most max_scan, in axis order. With a party, a trip is the party's when it is stamped to it,
-- or unstamped and its task resolves to it (legacy compared the stamp only, so an unpriced trip never stamped was never
-- reached; an unstamped plan-basis trip is read on its task's plan instant).
SELECT tr.id, tr.billing_party_id, tr.billing_date, tr.delivered_at, (s.estimate_thb IS NOT NULL)::boolean AS priced
FROM trip_records tr
LEFT JOIN trip_billing_snapshots s ON s.trip_id = tr.id
LEFT JOIN tasks tk ON tk.id = tr.task_id
WHERE tr.status = 'delivered'
  AND (
    (NOT sqlc.arg(has_party)::boolean
       AND tr.delivered_at >= sqlc.arg(period_from)::timestamptz AND tr.delivered_at < sqlc.arg(period_to)::timestamptz)
    OR (sqlc.arg(has_party)::boolean AND tr.billing_party_id = sqlc.arg(party_id)::uuid
       AND CASE WHEN sqlc.arg(plan_axis)::boolean THEN tr.billing_date ELSE tr.delivered_at END >= sqlc.arg(period_from)::timestamptz
       AND CASE WHEN sqlc.arg(plan_axis)::boolean THEN tr.billing_date ELSE tr.delivered_at END < sqlc.arg(period_to)::timestamptz)
    OR (sqlc.arg(has_party)::boolean AND tr.billing_party_id IS NULL
       AND coalesce(tk.billing_party_id, tk.source_linked_party_id, tk.destination_linked_party_id) = sqlc.arg(party_id)::uuid
       AND CASE WHEN sqlc.arg(plan_axis)::boolean THEN coalesce(tk.plan_at, tr.delivered_at) ELSE tr.delivered_at END
           >= sqlc.arg(period_from)::timestamptz
       AND CASE WHEN sqlc.arg(plan_axis)::boolean THEN coalesce(tk.plan_at, tr.delivered_at) ELSE tr.delivered_at END
           < sqlc.arg(period_to)::timestamptz)
  )
ORDER BY CASE WHEN sqlc.arg(plan_axis)::boolean THEN coalesce(tr.billing_date, tk.plan_at, tr.delivered_at) ELSE tr.delivered_at END,
         tr.id
LIMIT sqlc.arg(max_scan);

-- name: BackfillStandby :many
-- POST /v1/jobs/billing.backfill-standby (backfillStandbyBillingSnapshots, fn:standbyBilling.ts:362-466): completed
-- standby records ended in [period_from, period_to) (ADR 0008 §3), at most max_scan, in ended_at order; with a party,
-- those billed to it or, unpriced, whose own or task party resolves to it.
SELECT sr.id, sr.billing_party_id, sr.ended_at, (sr.billing_estimate_thb IS NOT NULL)::boolean AS priced
FROM standby_records sr
LEFT JOIN tasks tk ON tk.id = sr.task_id
WHERE sr.status = 'completed'
  AND sr.ended_at >= sqlc.arg(period_from)::timestamptz AND sr.ended_at < sqlc.arg(period_to)::timestamptz
  AND (NOT sqlc.arg(has_party)::boolean
       OR sr.billing_party_id = sqlc.arg(party_id)::uuid
       OR (sr.billing_party_id IS NULL
           AND coalesce(sr.customer_party_id, tk.source_linked_party_id, tk.destination_linked_party_id) = sqlc.arg(party_id)::uuid))
ORDER BY sr.ended_at, sr.id
LIMIT sqlc.arg(max_scan);

-- --- impact report (ADR 0009, read-only) ---------------------------------------------------------------------------

-- name: ImpactFuelAdjustments :many
-- Every fuel round of the billing carrier (billingImpactReport, fn:billingRoundMigration.ts:94-128), voided included in
-- the count; the billed id is the customer (or carrier) the party bills, the id the web names customers by.
SELECT a.id, a.voided, coalesce(bp.customer_id, bp.tenant_id, bp.id)::uuid AS billed_id, a.effective_from_at,
       a.effective_from_date, a.reference_fuel_price_thb, a.fuel_band_enabled, a.fuel_band_baseline_floor,
       a.fuel_band_thb_per_baht, a.add_thb_per_trip
FROM customer_fuel_rate_adjustments a
JOIN billing_parties bp ON bp.id = a.billing_party_id
WHERE a.tenant_id = sqlc.arg(carrier_id)
ORDER BY a.legacy_doc_id COLLATE "C" ASC NULLS LAST, a.created_at, a.id;

-- name: ImpactRateEntryDays :many
SELECT e.voided, e.effective_from_at
FROM customer_rate_entries e
WHERE e.tenant_id = sqlc.arg(carrier_id);

-- name: ImpactTrips :many
-- Delivered trips in [period_from, period_to) on the delivered axis, at most max_scan (fn:billingRoundMigration.ts:136-142).
SELECT tr.id, tr.delivered_at, bp.id AS party_id, bp.customer_id AS party_customer_id, bp.tenant_id AS party_tenant_id,
       s.round_effective_from_date
FROM trip_records tr
LEFT JOIN billing_parties bp ON bp.id = tr.billing_party_id
LEFT JOIN trip_billing_snapshots s ON s.trip_id = tr.id
WHERE tr.status = 'delivered'
  AND tr.delivered_at >= sqlc.arg(period_from)::timestamptz AND tr.delivered_at < sqlc.arg(period_to)::timestamptz
ORDER BY tr.delivered_at, tr.id
LIMIT sqlc.arg(max_scan);
