-- Trips (Appendix A §A.2.3, 0004_operations): trip_records with their stop progress (trip_delivery_stops), the evidence
-- set (trip_photos, merge by type, ADR 0018) and the renames (trip_no_history, append-only). Tenant-stamped ('task' |
-- 'driver' | 'quarantine', a trip on its task's tenant: deferred trip_records_tenant_check); RLS decides every row:
-- staff of the reach, the trip's driver (p_driver_read / insert / update), scope principals through scope_trips. The
-- staff queries name the reach (Appendix C §C.3.8) and reach_driver admits the driver's own trip.
--
-- Identity (main spec §3.1 #3): surrogate id + trip_no, UNIQUE and mutable, with the charset CHECK of
-- core/tripDocId.ts:10-18; a rename is an UPDATE plus one trip_no_history row, objects never move.
--
-- Indexes -> live query: trip_records_trip_no, trip_records_legacy, trip_no_history_old_no (lookups by number, the
-- pre-rename number too); trip_records_tenant_created / trip_records_created (monitor, the created axis);
-- trip_records_delivered / trip_records_party_status_del / trip_records_delivered_driver (the delivered axis);
-- trip_records_party_axis (the billing axis; without a customer through PostgreSQL 18's skip scan over the party);
-- trip_records_driver_created (driver history);
-- trip_records_pending_driver (resume); trip_records_seal (duplicate seal); trip_records_task; trip_records_truck_created.

-- --- writes -----------------------------------------------------------------------------------------------------

-- name: InsertTrip :one
-- The loading submit (POST /v1/mobile/trips, P7a) and the write-back echo. created_at is the device's loading time
-- when the caller has it (the Depart axis), else now(). trip_no is validated by the service (ValidateTripNo) and is
-- UNIQUE: a taken number is 409 duplicate_trip_id.
INSERT INTO trip_records (legacy_doc_id, tenant_id, tenant_source, trip_no, status, job_type, job_category, task_id,
                          legacy_task_ref, task_ref_match, driver_id, legacy_driver_ref, driver_ref_match, origin_raw,
                          origin_hub_id, destination_raw, destination_hub_id, destination_soc_key, seal_code,
                          partner_code, ocr_data, truck_id, truck_license_plate_snapshot, truck_type_raw, vehicle_class,
                          distance_km, parcel_count, seal_time, total_weight_kg, loading_lat, loading_lng, std, sta,
                          duration_minutes, is_multi_delivery, billing_party_id, billing_date, created_at)
VALUES (sqlc.narg(legacy_doc_id), sqlc.arg(tenant_id), sqlc.arg(tenant_source), sqlc.arg(trip_no), sqlc.arg(status),
        sqlc.arg(job_type), sqlc.narg(job_category), sqlc.narg(task_id), sqlc.narg(legacy_task_ref),
        sqlc.narg(task_ref_match), sqlc.narg(driver_id), sqlc.narg(legacy_driver_ref), sqlc.narg(driver_ref_match),
        sqlc.narg(origin_raw), sqlc.narg(origin_hub_id), sqlc.narg(destination_raw), sqlc.narg(destination_hub_id),
        sqlc.narg(destination_soc_key), sqlc.narg(seal_code), sqlc.narg(partner_code), sqlc.narg(ocr_data),
        sqlc.narg(truck_id), sqlc.narg(truck_license_plate_snapshot), sqlc.narg(truck_type_raw),
        sqlc.narg(vehicle_class), sqlc.narg(distance_km), sqlc.narg(parcel_count), sqlc.narg(seal_time),
        sqlc.narg(total_weight_kg), sqlc.narg(loading_lat), sqlc.narg(loading_lng), sqlc.narg(std), sqlc.narg(sta),
        sqlc.narg(duration_minutes), sqlc.arg(is_multi_delivery), sqlc.narg(billing_party_id), sqlc.narg(billing_date),
        coalesce(sqlc.narg(created_at)::timestamptz, now()))
RETURNING *;

-- name: LockTrip :one
SELECT * FROM trip_records WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: RenameTripNo :one
-- POST /v1/trips/{id}/rename: the number changes in place (the row, its photos and incidents keep their ids, so
-- nothing is re-pointed, unlike renameTripRecord.ts:51); the history row is written in the same transaction.
UPDATE trip_records SET trip_no = sqlc.arg(new_trip_no) WHERE id = sqlc.arg(id) RETURNING *;

-- name: InsertTripNoHistory :one
-- Append-only (REVOKE UPDATE, DELETE + trg_forbid_mutation); CHECK (old_trip_no <> new_trip_no).
INSERT INTO trip_no_history (trip_id, old_trip_no, new_trip_no, renamed_by, renamed_at)
VALUES (sqlc.arg(trip_id), sqlc.arg(old_trip_no), sqlc.arg(new_trip_no), sqlc.narg(renamed_by),
        coalesce(sqlc.narg(renamed_at)::timestamptz, now()))
RETURNING *;

-- name: TripNoHistory :many
-- The renames of a trip, oldest first (trip_no_history_trip; staff only, p_parent_read read_staff_only).
SELECT * FROM trip_no_history WHERE trip_id = sqlc.arg(trip_id) ORDER BY renamed_at, id;

-- name: UpdateTripDetails :one
-- PATCH /v1/trips/{id} (edit trip details) writes the merged row; status, delivery and billing columns have their
-- own statements.
UPDATE trip_records
SET seal_code = sqlc.narg(seal_code), partner_code = sqlc.narg(partner_code), ocr_data = sqlc.narg(ocr_data),
    origin_raw = sqlc.narg(origin_raw), origin_hub_id = sqlc.narg(origin_hub_id),
    destination_raw = sqlc.narg(destination_raw), destination_hub_id = sqlc.narg(destination_hub_id),
    destination_soc_key = sqlc.narg(destination_soc_key), truck_id = sqlc.narg(truck_id),
    truck_license_plate_snapshot = sqlc.narg(truck_license_plate_snapshot), truck_type_raw = sqlc.narg(truck_type_raw),
    vehicle_class = sqlc.narg(vehicle_class), distance_km = sqlc.narg(distance_km),
    parcel_count = sqlc.narg(parcel_count), seal_time = sqlc.narg(seal_time),
    total_weight_kg = sqlc.narg(total_weight_kg), std = sqlc.narg(std), sta = sqlc.narg(sta), ata = sqlc.narg(ata),
    duration_minutes = sqlc.narg(duration_minutes), delivered_at = sqlc.narg(delivered_at),
    needs_admin_review = sqlc.arg(needs_admin_review), review_status = sqlc.narg(review_status),
    review_reason = sqlc.narg(review_reason)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeliverTrip :one
-- The trip's delivery (driver or admin_web): only an in-transit or incident trip is delivered; a repeat finds no row.
UPDATE trip_records
SET status = 'delivered', delivered_at = sqlc.arg(delivered_at)::timestamptz, delivered_lat = sqlc.narg(lat),
    delivered_lng = sqlc.narg(lng), delivered_via = sqlc.arg(delivered_via)::text
WHERE id = sqlc.arg(id) AND status IN ('in_transit', 'incident')
RETURNING *;

-- name: SetTripStatus :one
-- incident / standby / cancelled written by another aggregate's transaction (an incident report, a standby submit, a
-- cancel).
UPDATE trip_records SET status = sqlc.arg(status) WHERE id = sqlc.arg(id) RETURNING *;

-- name: CancelInTransitTrips :many
-- POST /v1/trips/bulk-cancel: in-transit trips only; the ids that were not cancelled are the caller's failed[]. The
-- task of each cancelled trip is cancelled with it in the same transaction (EditTripDetailsDialog.tsx:782-797).
UPDATE trip_records SET status = 'cancelled'
WHERE id = ANY (sqlc.arg(ids)::uuid[]) AND status = 'in_transit'
RETURNING id, tenant_id, task_id, driver_id, trip_no;

-- name: MarkTripResubmitted :one
-- POST /v1/mobile/trips/{id}/resubmit-photos: back into the admin review queue.
UPDATE trip_records SET review_status = 'pending_review', resubmitted_at = sqlc.arg(resubmitted_at)::timestamptz
WHERE id = sqlc.arg(id)
RETURNING *;

-- --- photos (trip_photos: merge by type, last wins; R19 free-text type + STORED photo_type_known) ---------------

-- name: UpsertTripPhoto :one
-- One row per (trip, photo_type): a new file for a type replaces the old one (mergeTripPhotosReplacingTypes). Any
-- non-empty type is stored (R19); photo_type_known is generated (TRIP_PHOTO_TYPE_ENUM or stop_{n}_*).
INSERT INTO trip_photos (trip_id, stop_id, photo_type, file_id, geocode_lat, geocode_lng, geocode_address, geocoded_at,
                         position)
VALUES (sqlc.arg(trip_id), sqlc.narg(stop_id), sqlc.arg(photo_type), sqlc.arg(file_id), sqlc.narg(geocode_lat),
        sqlc.narg(geocode_lng), sqlc.narg(geocode_address), sqlc.narg(geocoded_at),
        coalesce((SELECT max(p.position) + 1 FROM trip_photos p WHERE p.trip_id = sqlc.arg(trip_id)), 0))
ON CONFLICT (trip_id, photo_type) DO UPDATE
SET file_id = EXCLUDED.file_id, stop_id = EXCLUDED.stop_id, geocode_lat = EXCLUDED.geocode_lat,
    geocode_lng = EXCLUDED.geocode_lng, geocode_address = EXCLUDED.geocode_address, geocoded_at = EXCLUDED.geocoded_at,
    position = EXCLUDED.position
RETURNING *;

-- name: DeleteTripPhoto :execrows
DELETE FROM trip_photos WHERE trip_id = sqlc.arg(trip_id) AND photo_type = sqlc.arg(photo_type);

-- name: TripPhotos :many
-- Insertion / replace order; the service sorts by the ADR 0018 workflow rank.
SELECT * FROM trip_photos WHERE trip_id = sqlc.arg(trip_id) ORDER BY position, id;

-- name: TripPhotosOf :many
SELECT * FROM trip_photos WHERE trip_id = ANY (sqlc.arg(trip_ids)::uuid[]) ORDER BY trip_id, position, id;

-- name: UnknownTripPhotos :many
-- Photos whose type is outside TRIP_PHOTO_TYPE_ENUM and stop_{n}_* (trip_photos_unknown_type): the ETL review list.
SELECT * FROM trip_photos WHERE trip_id = sqlc.arg(trip_id) AND NOT photo_type_known ORDER BY position, id;

-- --- stop progress (trip_delivery_stops: UNIQUE (trip_id, stop_index), so a stop delivery is idempotent per index) --

-- name: InsertTripStop :one
INSERT INTO trip_delivery_stops (trip_id, stop_index, destination_raw, destination_hub_id, source_id)
VALUES (sqlc.arg(trip_id), sqlc.arg(stop_index), sqlc.arg(destination_raw), sqlc.narg(destination_hub_id),
        sqlc.narg(source_id))
ON CONFLICT (trip_id, stop_index) DO NOTHING
RETURNING *;

-- name: DeliverTripStop :one
-- POST /v1/mobile/trips/{id}/stops/{index}/deliver: a pending stop only (a repeat finds no row and the service returns
-- the stored progress); completed_seq is the completion order, not the plan order.
UPDATE trip_delivery_stops s
SET status = 'delivered', delivered_at = sqlc.arg(delivered_at)::timestamptz, delivered_lat = sqlc.narg(lat),
    delivered_lng = sqlc.narg(lng),
    completed_seq = (SELECT coalesce(max(o.completed_seq), 0) + 1 FROM trip_delivery_stops o
                      WHERE o.trip_id = sqlc.arg(trip_id))
WHERE s.trip_id = sqlc.arg(trip_id) AND s.stop_index = sqlc.arg(stop_index) AND s.status = 'pending'
RETURNING *;

-- name: TripStops :many
SELECT * FROM trip_delivery_stops WHERE trip_id = sqlc.arg(trip_id) ORDER BY stop_index;

-- --- reads ------------------------------------------------------------------------------------------------------

-- name: GetTrip :one
SELECT * FROM trip_records t
WHERE t.id = sqlc.arg(id)
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR t.driver_id = sqlc.narg(reach_driver)::uuid);

-- name: TripByNo :one
-- GET /v1/trips/by-no/{tripNo} (trip_records_trip_no).
SELECT * FROM trip_records t
WHERE t.trip_no = sqlc.arg(trip_no)
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR t.driver_id = sqlc.narg(reach_driver)::uuid);

-- name: TripByOldNo :one
-- A number from before a rename (incident and evidence lookups, trip_no_history_old_no): the trip it was renamed
-- from most recently.
SELECT t.* FROM trip_no_history h
JOIN trip_records t ON t.id = h.trip_id
WHERE h.old_trip_no = sqlc.arg(trip_no)
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR t.driver_id = sqlc.narg(reach_driver)::uuid)
ORDER BY h.renamed_at DESC, h.id DESC
LIMIT 1;

-- name: TripByLegacyDocID :one
-- The Firestore trip doc id (shim targets; the mirror's key).
SELECT * FROM trip_records t
WHERE t.legacy_doc_id = sqlc.arg(legacy_doc_id)::text
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR t.driver_id = sqlc.narg(reach_driver)::uuid);

-- name: TripsOfTasks :many
-- GET /v1/tasks/{id}/trip and the boards' trip column (first-mile/page.tsx:136-141): the trips of the given tasks,
-- newest first per task (trip_records_task).
SELECT * FROM trip_records t
WHERE t.task_id = ANY (sqlc.arg(task_ids)::uuid[])
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR t.driver_id = sqlc.narg(reach_driver)::uuid)
ORDER BY t.task_id, t.created_at DESC, t.id DESC;

-- name: ListTripsByCreated :many
-- GET /v1/trips?axis=created and the monitor's page (W8): newest first, keyset (created_at, id)
-- (trip_records_tenant_created; trip_records_created under X-Act-On-Tenant: *; the driver and truck filters have
-- their own (x, created_at) indexes). from / to bound created_at, [from, to).
SELECT * FROM trip_records t
WHERE (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR t.created_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR t.created_at < sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(statuses)::text[] IS NULL OR t.status = ANY (sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(driver_id)::uuid IS NULL OR t.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(billing_party_id)::uuid IS NULL OR t.billing_party_id = sqlc.narg(billing_party_id)::uuid)
  AND (sqlc.narg(truck_id)::uuid IS NULL OR t.truck_id = sqlc.narg(truck_id)::uuid)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (t.created_at, t.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY t.created_at DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListTripsByDelivered :many
-- GET /v1/trips?axis=delivered (the Income page, I20; all-customer rows, I21): delivered trips by delivered_at, latest
-- first, keyset (delivered_at, id) (trip_records_delivered; with a customer trip_records_party_status_del, with a
-- driver trip_records_delivered_driver). from / to bound delivered_at, [from, to).
SELECT * FROM trip_records t
WHERE t.status = 'delivered' AND t.delivered_at IS NOT NULL
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR t.delivered_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR t.delivered_at < sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR t.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(billing_party_id)::uuid IS NULL OR t.billing_party_id = sqlc.narg(billing_party_id)::uuid)
  AND (sqlc.narg(truck_id)::uuid IS NULL OR t.truck_id = sqlc.narg(truck_id)::uuid)
  AND (sqlc.narg(after_delivered)::timestamptz IS NULL
       OR (t.delivered_at, t.id) < (sqlc.narg(after_delivered)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY t.delivered_at DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListTripsByBillingAxis :many
-- GET /v1/trips?axis=billing: delivered trips by their billing axis day (Bangkok day of billing_date, else of
-- delivered_at; ADR 0027), latest first, keyset (billing_axis_date, id) (trip_records_party_axis; without a customer
-- its skip scan over the parties, ANDed with the tenant index). from / to are Bangkok days, both inclusive.
SELECT * FROM trip_records t
WHERE t.status = 'delivered' AND t.billing_axis_date IS NOT NULL
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(from_date)::date IS NULL OR t.billing_axis_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR t.billing_axis_date <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR t.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(billing_party_id)::uuid IS NULL OR t.billing_party_id = sqlc.narg(billing_party_id)::uuid)
  AND (sqlc.narg(truck_id)::uuid IS NULL OR t.truck_id = sqlc.narg(truck_id)::uuid)
  AND (sqlc.narg(after_axis)::date IS NULL
       OR (t.billing_axis_date, t.id) < (sqlc.narg(after_axis)::date, sqlc.arg(after_id)::uuid))
ORDER BY t.billing_axis_date DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: DriverTrips :many
-- The driver's history (GET /v1/mobile/trips, replaces the take-300 and the dual-id query; trip_records_driver_created).
SELECT * FROM trip_records t
WHERE t.driver_id = sqlc.arg(driver_id)::uuid
  AND (sqlc.narg(statuses)::text[] IS NULL OR t.status = ANY (sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (t.created_at, t.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY t.created_at DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: PendingInTransitTrip :one
-- Resume the pending trip (trip_records_repository.dart:195-201; trip_records_pending_driver): the newest in-transit
-- trip of the driver.
SELECT * FROM trip_records t
WHERE t.driver_id = sqlc.arg(driver_id)::uuid AND t.status = 'in_transit'
ORDER BY t.updated_at DESC
LIMIT 1;

-- name: TripSealTaken :one
-- The duplicate-seal check of a trip submit (POST /v1/mobile/trips, P7a; 409 duplicate_seal; the legacy query read every
-- trip, trip_records_repository.dart:59-62): whether any trip other than exclude_id carries the seal, in every tenant
-- and every status (cancelled trips keep their seal, as in legacy). trip_seal_taken() is SECURITY DEFINER (0014): the
-- driver's own RLS shows only its own trips. It holds a per-seal transaction lock, so the trip insert that follows in
-- the same transaction cannot race another submit of the seal (trip_records_seal inside the function).
SELECT trip_seal_taken(sqlc.arg(seal_code)::text, sqlc.narg(exclude_id)::uuid)::boolean AS taken;

-- name: TripIDsBySeal :many
-- The trips of the caller's reach that carry the seal (trip_records_seal): the 409 duplicate_seal of a taken seal names
-- the first one (details.tripNo) only when the caller reaches it, so no other tenant's or driver's trip number leaks.
SELECT t.id, t.trip_no FROM trip_records t
WHERE t.seal_code = sqlc.arg(seal_code)::text
  AND t.id <> coalesce(sqlc.narg(exclude_id)::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR t.driver_id = sqlc.narg(reach_driver)::uuid)
ORDER BY t.created_at, t.id;
