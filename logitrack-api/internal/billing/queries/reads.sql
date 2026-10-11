-- Billing read aggregates of T39 (Appendix B §B.2.14, main spec §6.14, W8): GET /v1/billing/rows (one request for the
-- six-hop fetchBillingTripRows chain, logitrack-web/features/accounting/api/billing.ts:737-1094), rows/missing-billing-date,
-- standby-diagnostics and the Shopee Express report. Requests run in db.WithPrincipal: RLS decides which trips, standby
-- records, tasks, drivers and price snapshots the caller reads (the snapshots only for its billing carrier, R61). The
-- statement's invoice set is InsertStatementLines (statements.sql); the rows API flags the same set in Go from the columns
-- below (billing.rowInInvoiceSet) and TestRowsInvoiceSetEqualsStatementLines keeps the two equal.

-- name: BillingRowTrips :many
-- Delivered trips of a Bangkok month for one billing party, or every party (all_parties: the delivered axis, as the legacy
-- "all" view): the trips stamped to the party on its axis (billing_date for a plan-basis party, else delivered_at; the
-- invoice predicate), the unstamped trips whose task resolves to it (its axis read from the task's plan instant or the
-- delivery), and the trips no party resolves for at all (delivered axis; unpriced no_customer, shown in every view so
-- they cannot hide). Snapshot, task, driver and the driver's tenant are joined for display.
SELECT tr.id, tr.trip_no, tr.task_id, tr.tenant_id, tr.billing_party_id, tr.billing_date, tr.delivered_at, tr.created_at,
       tr.job_category AS trip_job_category, tr.is_multi_delivery, tr.truck_id AS trip_truck_id,
       tr.truck_license_plate_snapshot AS trip_plate, tr.vehicle_class AS trip_vehicle_class, tr.driver_id AS trip_driver_id,
       tr.origin_raw, tr.destination_raw AS trip_destination_raw,
       (s.trip_id IS NOT NULL)::boolean AS has_snapshot, s.tenant_id AS snapshot_tenant_id, s.estimate_thb,
       s.unpriced_reason, s.base_rate_thb, s.lookup_hub_code, s.lookup_destination_code,
       s.fuel_adjustment_id, s.rate_multiplier, s.add_thb_per_trip,
       s.round_effective_from_date, s.fuel_band_lower_thb,
       s.fuel_band_upper_thb, s.reference_fuel_price_thb,
       s.is_multi_delivery AS snapshot_multi, s.manual_override,
       (tk.id IS NOT NULL)::boolean AS has_task, tk.job_category AS task_job_category, tk.truck_type,
       tk.license_plate_snapshot AS task_plate, tk.truck_id AS task_truck_id, tk.source_hub_raw, tk.destination_raw,
       tk.plan_at, tk.actual_pickup_at, tk.billing_party_id AS task_party_id, tk.source_linked_party_id,
       tk.destination_linked_party_id, tk.driver_id AS task_driver_id,
       d.id AS driver_id, d.full_name_th, d.first_name, d.last_name, d.email AS driver_email, d.mobile AS driver_mobile,
       dt.kind AS driver_tenant_kind, dt.name_th AS driver_tenant_name
FROM trip_records tr
LEFT JOIN trip_billing_snapshots s ON s.trip_id = tr.id
LEFT JOIN tasks tk ON tk.id = tr.task_id
LEFT JOIN drivers d ON d.id = coalesce(tr.driver_id, tk.driver_id)
LEFT JOIN tenants dt ON dt.id = d.tenant_id
WHERE tr.status = 'delivered'
  AND (
    (sqlc.arg(all_parties)::boolean
       AND tr.delivered_at >= sqlc.arg(period_from)::timestamptz AND tr.delivered_at < sqlc.arg(period_to)::timestamptz)
    OR (NOT sqlc.arg(all_parties)::boolean AND tr.billing_party_id = sqlc.arg(party_id)::uuid
       AND CASE WHEN sqlc.arg(plan_basis)::boolean THEN tr.billing_date ELSE tr.delivered_at END >= sqlc.arg(period_from)::timestamptz
       AND CASE WHEN sqlc.arg(plan_basis)::boolean THEN tr.billing_date ELSE tr.delivered_at END < sqlc.arg(period_to)::timestamptz)
    OR (NOT sqlc.arg(all_parties)::boolean AND tr.billing_party_id IS NULL
       AND coalesce(tk.billing_party_id, tk.source_linked_party_id, tk.destination_linked_party_id) = sqlc.arg(party_id)::uuid
       AND CASE WHEN sqlc.arg(plan_basis)::boolean THEN coalesce(tk.plan_at, tr.delivered_at) ELSE tr.delivered_at END
           >= sqlc.arg(period_from)::timestamptz
       AND CASE WHEN sqlc.arg(plan_basis)::boolean THEN coalesce(tk.plan_at, tr.delivered_at) ELSE tr.delivered_at END
           < sqlc.arg(period_to)::timestamptz)
    OR (NOT sqlc.arg(all_parties)::boolean AND tr.billing_party_id IS NULL
       AND coalesce(tk.billing_party_id, tk.source_linked_party_id, tk.destination_linked_party_id) IS NULL
       AND tr.delivered_at >= sqlc.arg(period_from)::timestamptz AND tr.delivered_at < sqlc.arg(period_to)::timestamptz)
  )
ORDER BY tr.trip_no COLLATE "C", tr.id;

-- name: BillingRowStops :many
-- The multi-drop breakdown of the trips read (one row per billed stop, trip_billing_stop_breakdown; parent-scoped RLS).
SELECT b.trip_id, b.stop_index, b.destination_code, b.base_rate_thb,
       b.final_rate_thb
FROM trip_billing_stop_breakdown b
WHERE b.trip_id = ANY (sqlc.arg(trip_ids)::uuid[])
ORDER BY b.trip_id, b.stop_index;

-- name: BillingRowTripStops :many
-- The delivery progress of the unpriced multi-drop trips read, for their dry run (main spec §6.8: delivered stops with a
-- destination, in completion order).
SELECT ds.trip_id, ds.stop_index, ds.destination_raw, ds.status, ds.completed_seq
FROM trip_delivery_stops ds
WHERE ds.trip_id = ANY (sqlc.arg(trip_ids)::uuid[])
ORDER BY ds.trip_id, ds.completed_seq NULLS LAST, ds.stop_index;

-- name: BillingRowStandby :many
-- Completed standby records ended inside a Bangkok month (standby bills on ended_at for every party, ADR 0008): priced
-- for the party (billing_party_id), unpriced ones whose own or task party resolves to it, and those no party resolves
-- for; every record with all_parties. The billing carrier is the one recorded with the price (billing_tenant_id, 0015),
-- else the carrier of the rate entry it names, else the own fleet (the rule of the statement's invoice set, applied in
-- Go: billing.standbyCarrier).
SELECT sr.id, sr.tenant_id, sr.task_id, sr.trip_id, sr.driver_id AS standby_driver_id, sr.customer_party_id,
       sr.job_category AS standby_job_category, sr.start_location_raw, sr.end_location_raw, sr.started_at, sr.ended_at,
       sr.created_at, sr.duration_minutes, sr.migrated_from_trip_no, sr.truck_id AS standby_truck_id,
       sr.truck_license_plate_snapshot AS standby_plate, sr.billing_estimate_thb,
       sr.billing_party_id, sr.billing_rate_source, sr.billing_unpriced_reason,
       sr.billing_tenant_id, sre.tenant_id AS rate_entry_tenant_id,
       trp.trip_no AS trip_no,
       tk.job_category AS task_job_category, tk.truck_type, tk.license_plate_snapshot AS task_plate,
       tk.truck_id AS task_truck_id, tk.source_hub_raw, tk.destination_raw, tk.plan_at, tk.actual_pickup_at,
       tk.source_linked_party_id, tk.destination_linked_party_id,
       d.id AS driver_id, d.full_name_th, d.first_name, d.last_name, d.email AS driver_email, d.mobile AS driver_mobile,
       dt.kind AS driver_tenant_kind, dt.name_th AS driver_tenant_name
FROM standby_records sr
LEFT JOIN standby_rate_entries sre ON sre.id = sr.billing_rate_entry_id
LEFT JOIN tasks tk ON tk.id = sr.task_id
LEFT JOIN trip_records trp ON trp.id = sr.trip_id
LEFT JOIN drivers d ON d.id = coalesce(sr.driver_id, tk.driver_id)
LEFT JOIN tenants dt ON dt.id = d.tenant_id
WHERE sr.status = 'completed'
  AND sr.ended_at >= sqlc.arg(period_from)::timestamptz AND sr.ended_at < sqlc.arg(period_to)::timestamptz
  AND (sqlc.arg(all_parties)::boolean
       OR sr.billing_party_id = sqlc.arg(party_id)::uuid
       OR (sr.billing_party_id IS NULL
           AND (coalesce(sr.customer_party_id, tk.source_linked_party_id, tk.destination_linked_party_id) = sqlc.arg(party_id)::uuid
                OR coalesce(sr.customer_party_id, tk.source_linked_party_id, tk.destination_linked_party_id) IS NULL)))
ORDER BY sr.ended_at, sr.id;

-- name: StandbyMissingEndedAt :many
-- Completed standby records without ended_at: they belong to no month, so no period query finds them (ADR 0008 §6-9;
-- the legacy page scanned the newest 300). The newest first, row_limit + 1 read to report truncation (no silent cap).
SELECT sr.id, sr.driver_id AS standby_driver_id, sr.customer_party_id, sr.start_location_raw, sr.end_location_raw,
       sr.started_at, sr.created_at, sr.duration_minutes, sr.billing_unpriced_reason,
       tk.source_linked_party_id, tk.destination_linked_party_id,
       d.full_name_th, d.first_name, d.last_name, d.email AS driver_email
FROM standby_records sr
LEFT JOIN tasks tk ON tk.id = sr.task_id
LEFT JOIN drivers d ON d.id = coalesce(sr.driver_id, tk.driver_id)
WHERE sr.status = 'completed' AND sr.ended_at IS NULL
  AND (sqlc.arg(all_parties)::boolean
       OR coalesce(sr.billing_party_id, sr.customer_party_id, tk.source_linked_party_id, tk.destination_linked_party_id) = sqlc.arg(party_id)::uuid
       OR coalesce(sr.billing_party_id, sr.customer_party_id, tk.source_linked_party_id, tk.destination_linked_party_id) IS NULL)
ORDER BY sr.created_at DESC, sr.id DESC
LIMIT sqlc.arg(row_limit);

-- name: MissingBillingDateTrips :many
-- GET /v1/billing/rows/missing-billing-date (fetchTripsMissingBillingDate, billing.ts:1116-1145): delivered trips of
-- the party delivered inside the Bangkok month without billing_date, so no plan-basis period holds them (ADR 0027 §9);
-- priced when their snapshot carries an amount.
SELECT tr.id, tr.trip_no, tr.delivered_at, (s.estimate_thb IS NOT NULL)::boolean AS priced
FROM trip_records tr
LEFT JOIN trip_billing_snapshots s ON s.trip_id = tr.id
WHERE tr.status = 'delivered' AND tr.billing_party_id = sqlc.arg(party_id)::uuid AND tr.billing_date IS NULL
  AND tr.delivered_at >= sqlc.arg(period_from)::timestamptz AND tr.delivered_at < sqlc.arg(period_to)::timestamptz
ORDER BY tr.delivered_at, tr.id;

-- name: PartyRefs :many
-- The basis and billed id of billing parties (the dry run reads each unpriced row's own party).
SELECT bp.id, bp.billing_date_basis, coalesce(bp.customer_id, bp.tenant_id, bp.id)::uuid AS billed_id
FROM billing_parties bp
WHERE bp.id = ANY (sqlc.arg(ids)::uuid[]);

-- name: DisplayHubs :many
-- Every hub with its names, for the display labels of the rows (billingHubLabelFromFirestoreData: name_en, else
-- name_th, else the code) and the hub maps of a dry run; aliases come from DisplayHubAliases.
SELECT h.id, h.legacy_doc_id, h.source_id::text AS source_id, h.name_th, h.name_en
FROM hubs h
ORDER BY h.legacy_doc_id COLLATE "C" NULLS LAST, h.created_at, h.id;

-- name: DisplayHubAliases :many
SELECT a.hub_id, a.alias::text AS alias
FROM hub_name_aliases a
ORDER BY a.created_at, a.alias;

-- name: DryRunRateEntries :many
-- A dry run's rate cards: the live rows of the parties and one billing carrier, read without a lock (a diagnosis writes
-- nothing; pricing reads PricingRateEntries FOR SHARE), in the R16 order.
SELECT e.id, e.legacy_doc_id, e.billing_party_id, e.import_id, e.hub_code, e.destination_code, e.vehicle_class,
       e.rate_thb, e.job_category, e.effective_from_at, e.created_at
FROM customer_rate_entries e
WHERE e.billing_party_id = ANY (sqlc.arg(party_ids)::uuid[]) AND e.tenant_id = sqlc.arg(carrier_id) AND NOT e.voided
ORDER BY e.legacy_doc_id COLLATE "C" ASC NULLS LAST, e.created_at, e.id;

-- name: DryRunFuelAdjustments :many
SELECT a.id, a.legacy_doc_id, a.billing_party_id, a.effective_from_at, a.rate_multiplier,
       a.add_thb_per_trip, a.reference_fuel_price_thb,
       a.created_at
FROM customer_fuel_rate_adjustments a
WHERE a.billing_party_id = ANY (sqlc.arg(party_ids)::uuid[]) AND a.tenant_id = sqlc.arg(carrier_id) AND NOT a.voided
ORDER BY a.legacy_doc_id COLLATE "C" ASC NULLS LAST, a.created_at, a.id;

-- name: DryRunStandbyRates :many
SELECT r.id, r.legacy_doc_id, r.billing_party_id, r.rate_thb, r.effective_from_at, r.created_at
FROM standby_rate_entries r
WHERE r.billing_party_id = ANY (sqlc.arg(party_ids)::uuid[]) AND r.tenant_id = sqlc.arg(carrier_id) AND r.voided_at IS NULL
ORDER BY r.legacy_doc_id COLLATE "C" ASC NULLS LAST, r.created_at, r.id;

-- name: DryRunServiceFees :many
-- extra_stop and standby fees of the parties and one billing carrier (one per party and type, D5).
SELECT f.billing_party_id, f.fee_type, f.amount_thb
FROM customer_service_fees f
WHERE f.billing_party_id = ANY (sqlc.arg(party_ids)::uuid[]) AND f.tenant_id = sqlc.arg(carrier_id)
  AND f.fee_type IN ('extra_stop', 'standby')
ORDER BY f.created_at, f.id;

-- name: ShopeeReportTrips :many
-- GET /v1/billing/shopee-report (fetchShopeeExpressReportTrips, billing.ts:1249-1363): delivered trips of a Bangkok
-- month or half-month whose party resolves to the customer (billing_party_id, else the task's source then destination
-- link: un-billed trips count), filtered here instead of in the browser over every customer's trips. Price-free. The
-- reach is named for the render's READ ONLY system transaction (the worker's, and the PDF route's rows version, which
-- digests what the render prints, so it is read the same way); GET /v1/billing/shopee-report also runs under RLS.
SELECT tr.id, tr.trip_no, tr.tenant_id, tr.delivered_at, tr.parcel_count, tr.origin_raw, tr.destination_raw AS trip_destination_raw,
       tr.truck_license_plate_snapshot AS trip_plate, tr.vehicle_class AS trip_vehicle_class,
       s.lookup_hub_code, s.lookup_destination_code,
       tk.truck_type, tk.license_plate_snapshot AS task_plate, tk.source_hub_raw, tk.destination_raw,
       d.id AS driver_id, d.full_name_th, d.first_name, d.last_name, d.email AS driver_email
FROM trip_records tr
LEFT JOIN trip_billing_snapshots s ON s.trip_id = tr.id
LEFT JOIN tasks tk ON tk.id = tr.task_id
LEFT JOIN drivers d ON d.id = coalesce(tr.driver_id, tk.driver_id)
WHERE tr.status = 'delivered'
  AND tr.delivered_at >= sqlc.arg(period_from)::timestamptz AND tr.delivered_at < sqlc.arg(period_to)::timestamptz
  AND coalesce(tr.billing_party_id, tk.source_linked_party_id, tk.destination_linked_party_id) = sqlc.arg(party_id)::uuid
  AND (sqlc.arg(all_tenants)::boolean OR tr.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
ORDER BY tr.delivered_at, tr.id;

-- name: ShopeeReportPhotos :many
-- The signed run-sheets of the trips read (runsheet_received and stop_{n}_runsheet_received), in the order they were
-- added (the legacy photos[] order).
SELECT p.trip_id, p.photo_type, p.file_id
FROM trip_photos p
WHERE p.trip_id = ANY (sqlc.arg(trip_ids)::uuid[])
  AND (p.photo_type = 'runsheet_received' OR p.photo_type ~ '^stop_[0-9]+_runsheet_received$')
ORDER BY p.trip_id, p.position, p.created_at, p.id;

-- name: TripBillingByIDs :many
-- The billing projection of trips for GET /v1/trips (accounting:view_income, issue #50 / T30): the stamped party and
-- axis and the stored snapshot, under the caller's RLS, so a tenant that ran a trip another carrier priced reads no
-- price (R61: the snapshot carries the billing carrier's tenant).
SELECT tr.id, tr.billing_party_id, tr.billing_date, coalesce(bp.customer_id, bp.tenant_id) AS billed_id,
       (s.trip_id IS NOT NULL)::boolean AS has_snapshot, s.tenant_id AS snapshot_tenant_id, s.estimate_thb,
       s.unpriced_reason, s.base_rate_thb, s.stop_charge_thb, s.rate_entry_id, s.rate_import_id, s.lookup_hub_code,
       s.lookup_destination_code, s.fuel_adjustment_id, s.rate_multiplier, s.add_thb_per_trip, s.fuel_effective_from_date,
       s.round_effective_from_date, s.fuel_band_lower_thb, s.fuel_band_upper_thb, s.reference_fuel_price_thb,
       s.is_multi_delivery, s.manual_override, s.job_category_at_pricing, s.computed_by, s.computed_at
FROM trip_records tr
LEFT JOIN billing_parties bp ON bp.id = tr.billing_party_id
LEFT JOIN trip_billing_snapshots s ON s.trip_id = tr.id
WHERE tr.id = ANY (sqlc.arg(trip_ids)::uuid[]);
