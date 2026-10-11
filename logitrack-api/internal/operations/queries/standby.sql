-- Standby (Appendix A §A.2.3, 0004_operations): standby_records ("รถจอดรอ", ADR 0008) and their two photos
-- (standby_photos). Tenant-stamped ('task' | 'trip' | 'driver' | 'quarantine'; a standby on its task's tenant, else its
-- trip's: deferred standby_records_tenant_check); RLS: staff of the reach, the driver's own rows (p_driver_read,
-- p_driver_insert; no driver update), scope principals through scope_standby. The billing columns are inline (R61) and
-- the service shows them only to the billing carrier's principals (Appendix C §C.3.4): every read returns
-- billing_tenant_id, the tenant owning the rate entry or the standby service fee that priced the row (the zero uuid
-- when unpriced or when that row is outside the caller's reach), and the service decides.
--
-- Indexes -> live query: standby_records_tenant_created / standby_records_created (the standby page, I33),
-- standby_records_ended (the ended axis, I34), standby_records_driver (driver history), standby_records_task /
-- standby_records_trip, standby_records_client_op (R63), standby_records_legacy.

-- name: InsertStandby :one
-- The driver's submit (POST /v1/mobile/standby, P7a) and the admin backfill (POST /v1/standby/backfill,
-- backfilled_by_admin). Idempotent per (driver_id, client_op_id) (R63): a replay inserts nothing. The billing columns are
-- written by the billing service only (billing.compute), never here.
INSERT INTO standby_records (legacy_doc_id, tenant_id, tenant_source, driver_id, legacy_driver_ref, driver_ref_match,
                             task_id, trip_id, customer_party_id, customer_resolved, customer_resolved_from,
                             job_category, start_location_raw, end_location_raw, started_at, ended_at,
                             duration_minutes, note, lat, lng, truck_id, truck_license_plate_snapshot,
                             backfilled_by_admin, migrated_from_trip_no, client_op_id)
VALUES (sqlc.narg(legacy_doc_id), sqlc.arg(tenant_id), sqlc.arg(tenant_source), sqlc.narg(driver_id),
        sqlc.narg(legacy_driver_ref), sqlc.narg(driver_ref_match), sqlc.narg(task_id), sqlc.narg(trip_id),
        sqlc.narg(customer_party_id), sqlc.arg(customer_resolved), sqlc.narg(customer_resolved_from),
        sqlc.narg(job_category), sqlc.narg(start_location_raw), sqlc.narg(end_location_raw), sqlc.narg(started_at),
        sqlc.narg(ended_at), sqlc.narg(duration_minutes), sqlc.narg(note), sqlc.narg(lat), sqlc.narg(lng),
        sqlc.narg(truck_id), sqlc.narg(truck_license_plate_snapshot), sqlc.arg(backfilled_by_admin),
        sqlc.narg(migrated_from_trip_no), sqlc.narg(client_op_id))
ON CONFLICT (driver_id, client_op_id) WHERE client_op_id IS NOT NULL DO NOTHING
RETURNING *;

-- name: StandbyByClientOp :one
-- The stored record of a replayed submit (R63).
SELECT * FROM standby_records WHERE driver_id = sqlc.arg(driver_id)::uuid AND client_op_id = sqlc.arg(client_op_id)::uuid;

-- name: UpsertStandbyPhoto :one
-- One photo per type (customer_worksheet, site_photo): a new file replaces the old one.
INSERT INTO standby_photos (standby_id, photo_type, file_id, overlay_text)
VALUES (sqlc.arg(standby_id), sqlc.arg(photo_type), sqlc.arg(file_id), sqlc.narg(overlay_text))
ON CONFLICT (standby_id, photo_type) DO UPDATE SET file_id = EXCLUDED.file_id, overlay_text = EXCLUDED.overlay_text
RETURNING *;

-- name: StandbyPhotos :many
SELECT * FROM standby_photos WHERE standby_id = sqlc.arg(standby_id) ORDER BY photo_type;

-- name: StandbyPhotosOf :many
SELECT * FROM standby_photos WHERE standby_id = ANY (sqlc.arg(standby_ids)::uuid[]) ORDER BY standby_id, photo_type;

-- name: GetStandby :one
SELECT sqlc.embed(s),
       coalesce(CASE s.billing_rate_source
                  WHEN 'standby_rate' THEN (SELECT e.tenant_id FROM standby_rate_entries e WHERE e.id = s.billing_rate_entry_id)
                  WHEN 'service_fee' THEN (SELECT f.tenant_id FROM customer_service_fees f
                                            WHERE f.billing_party_id = s.billing_party_id AND f.fee_type = 'standby'
                                            ORDER BY f.created_at DESC, f.id DESC LIMIT 1)
                END, '00000000-0000-0000-0000-000000000000')::uuid AS billing_tenant_id
FROM standby_records s
WHERE s.id = sqlc.arg(id)
  AND (sqlc.arg(all_tenants)::boolean OR s.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR s.driver_id = sqlc.narg(reach_driver)::uuid);

-- name: StandbyByLegacyDocID :one
-- The Firestore standby doc id (the line-notify shim target; the mirror's key).
SELECT sqlc.embed(s),
       coalesce(CASE s.billing_rate_source
                  WHEN 'standby_rate' THEN (SELECT e.tenant_id FROM standby_rate_entries e WHERE e.id = s.billing_rate_entry_id)
                  WHEN 'service_fee' THEN (SELECT f.tenant_id FROM customer_service_fees f
                                            WHERE f.billing_party_id = s.billing_party_id AND f.fee_type = 'standby'
                                            ORDER BY f.created_at DESC, f.id DESC LIMIT 1)
                END, '00000000-0000-0000-0000-000000000000')::uuid AS billing_tenant_id
FROM standby_records s
WHERE s.legacy_doc_id = sqlc.arg(legacy_doc_id)::text
  AND (sqlc.arg(all_tenants)::boolean OR s.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR s.driver_id = sqlc.narg(reach_driver)::uuid);

-- name: StandbyOfTasks :many
-- The standby records of the given tasks (the boards and the monitor; standby_records_task).
SELECT sqlc.embed(s),
       coalesce(CASE s.billing_rate_source
                  WHEN 'standby_rate' THEN (SELECT e.tenant_id FROM standby_rate_entries e WHERE e.id = s.billing_rate_entry_id)
                  WHEN 'service_fee' THEN (SELECT f.tenant_id FROM customer_service_fees f
                                            WHERE f.billing_party_id = s.billing_party_id AND f.fee_type = 'standby'
                                            ORDER BY f.created_at DESC, f.id DESC LIMIT 1)
                END, '00000000-0000-0000-0000-000000000000')::uuid AS billing_tenant_id
FROM standby_records s
WHERE s.task_id = ANY (sqlc.arg(task_ids)::uuid[])
  AND (sqlc.arg(all_tenants)::boolean OR s.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR s.driver_id = sqlc.narg(reach_driver)::uuid)
ORDER BY s.task_id, s.created_at DESC, s.id DESC;

-- name: StandbyOfTrip :many
-- The standby record a legacy standby trip became (standby_records_trip).
SELECT sqlc.embed(s),
       coalesce(CASE s.billing_rate_source
                  WHEN 'standby_rate' THEN (SELECT e.tenant_id FROM standby_rate_entries e WHERE e.id = s.billing_rate_entry_id)
                  WHEN 'service_fee' THEN (SELECT f.tenant_id FROM customer_service_fees f
                                            WHERE f.billing_party_id = s.billing_party_id AND f.fee_type = 'standby'
                                            ORDER BY f.created_at DESC, f.id DESC LIMIT 1)
                END, '00000000-0000-0000-0000-000000000000')::uuid AS billing_tenant_id
FROM standby_records s
WHERE s.trip_id = sqlc.arg(trip_id)::uuid
  AND (sqlc.arg(all_tenants)::boolean OR s.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR s.driver_id = sqlc.narg(reach_driver)::uuid)
ORDER BY s.created_at DESC, s.id DESC;

-- name: ListStandbyByCreated :many
-- GET /v1/standby (the standby page, I33; removes the 300-row cap): newest first, keyset (created_at, id)
-- (standby_records_tenant_created; standby_records_created under X-Act-On-Tenant: *). customer matches the record's
-- customer or its billing party. from / to bound created_at, [from, to).
SELECT sqlc.embed(s),
       coalesce(CASE s.billing_rate_source
                  WHEN 'standby_rate' THEN (SELECT e.tenant_id FROM standby_rate_entries e WHERE e.id = s.billing_rate_entry_id)
                  WHEN 'service_fee' THEN (SELECT f.tenant_id FROM customer_service_fees f
                                            WHERE f.billing_party_id = s.billing_party_id AND f.fee_type = 'standby'
                                            ORDER BY f.created_at DESC, f.id DESC LIMIT 1)
                END, '00000000-0000-0000-0000-000000000000')::uuid AS billing_tenant_id
FROM standby_records s
WHERE (sqlc.arg(all_tenants)::boolean OR s.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR s.created_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR s.created_at < sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR s.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(customer_id)::uuid IS NULL OR s.customer_party_id = sqlc.narg(customer_id)::uuid
       OR s.billing_party_id = sqlc.narg(customer_id)::uuid)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (s.created_at, s.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY s.created_at DESC, s.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListStandbyByEnded :many
-- GET /v1/standby sorted by ended_at (Appendix B §B.2.13) and the billing safety net (I34; standby_records_ended):
-- records that ended, latest first, keyset (ended_at, id). Records without ended_at (unpriced no_ended_at) are on the
-- created axis only. from / to bound ended_at, [from, to).
SELECT sqlc.embed(s),
       coalesce(CASE s.billing_rate_source
                  WHEN 'standby_rate' THEN (SELECT e.tenant_id FROM standby_rate_entries e WHERE e.id = s.billing_rate_entry_id)
                  WHEN 'service_fee' THEN (SELECT f.tenant_id FROM customer_service_fees f
                                            WHERE f.billing_party_id = s.billing_party_id AND f.fee_type = 'standby'
                                            ORDER BY f.created_at DESC, f.id DESC LIMIT 1)
                END, '00000000-0000-0000-0000-000000000000')::uuid AS billing_tenant_id
FROM standby_records s
WHERE s.status = 'completed' AND s.ended_at IS NOT NULL
  AND (sqlc.arg(all_tenants)::boolean OR s.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR s.ended_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR s.ended_at < sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR s.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(customer_id)::uuid IS NULL OR s.customer_party_id = sqlc.narg(customer_id)::uuid
       OR s.billing_party_id = sqlc.narg(customer_id)::uuid)
  AND (sqlc.narg(after_ended)::timestamptz IS NULL
       OR (s.ended_at, s.id) < (sqlc.narg(after_ended)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY s.ended_at DESC, s.id DESC
LIMIT sqlc.arg(row_limit);

-- name: DriverStandby :many
-- The driver's history (GET /v1/mobile/standby, trip_history_page.dart:84-87; standby_records_driver). A driver never
-- sees the billing columns, so no billing_tenant_id here.
SELECT * FROM standby_records s
WHERE s.driver_id = sqlc.arg(driver_id)::uuid
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (s.created_at, s.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY s.created_at DESC, s.id DESC
LIMIT sqlc.arg(row_limit);
