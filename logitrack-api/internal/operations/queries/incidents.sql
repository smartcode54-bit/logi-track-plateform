-- Incidents (Appendix A §A.2.3, 0004_operations; collection incidentReport): a driver's or an admin's report on a trip.
-- Tenant-stamped ('trip' | 'driver' | 'quarantine'; an incident on its trip's tenant: deferred
-- incident_reports_tenant_check); RLS: staff of the reach, the driver's own reports (p_driver_read / insert / update),
-- scope principals through scope_incidents (incidents of visible trips only).
--
-- Indexes -> live query: incident_reports_trip (the Edit Trip dialog, the evidence page, LINE), the per-trip counts of the
-- monitor; incident_reports_tenant_created / incident_reports_created (the incident page, dashboard counts);
-- incident_reports_driver (driver history); incident_reports_client_op (R63); incident_reports_legacy.

-- name: InsertIncident :one
-- POST /v1/mobile/incidents (P7a, idempotent per (driver_id, client_op_id), R63) and POST /v1/incidents (admin,
-- reported_by_kind 'admin'). The photos are committed by the caller in the same transaction.
INSERT INTO incident_reports (legacy_doc_id, tenant_id, tenant_source, driver_id, legacy_driver_ref, driver_ref_match,
                              trip_id, legacy_trip_ref, delay_cause, description, lat, lng, truck_id,
                              truck_license_plate_snapshot, map_photo_file_id, situation1_photo_file_id,
                              situation2_photo_file_id, reported_by_kind, reported_by_user_id, client_op_id)
VALUES (sqlc.narg(legacy_doc_id), sqlc.arg(tenant_id), sqlc.arg(tenant_source), sqlc.narg(driver_id),
        sqlc.narg(legacy_driver_ref), sqlc.narg(driver_ref_match), sqlc.narg(trip_id), sqlc.narg(legacy_trip_ref),
        sqlc.narg(delay_cause), sqlc.narg(description), sqlc.narg(lat), sqlc.narg(lng), sqlc.narg(truck_id),
        sqlc.narg(truck_license_plate_snapshot), sqlc.narg(map_photo_file_id), sqlc.narg(situation1_photo_file_id),
        sqlc.narg(situation2_photo_file_id), sqlc.arg(reported_by_kind), sqlc.narg(reported_by_user_id),
        sqlc.narg(client_op_id))
ON CONFLICT (driver_id, client_op_id) WHERE client_op_id IS NOT NULL DO NOTHING
RETURNING *;

-- name: IncidentByClientOp :one
SELECT * FROM incident_reports WHERE driver_id = sqlc.arg(driver_id)::uuid AND client_op_id = sqlc.arg(client_op_id)::uuid;

-- name: GetIncident :one
SELECT * FROM incident_reports i
WHERE i.id = sqlc.arg(id)
  AND (sqlc.arg(all_tenants)::boolean OR i.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR i.driver_id = sqlc.narg(reach_driver)::uuid);

-- name: IncidentByLegacyDocID :one
SELECT * FROM incident_reports i
WHERE i.legacy_doc_id = sqlc.arg(legacy_doc_id)::text
  AND (sqlc.arg(all_tenants)::boolean OR i.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR i.driver_id = sqlc.narg(reach_driver)::uuid);

-- name: IncidentsOfTrip :many
-- A trip's incidents, oldest first (the Edit Trip dialog, tripEvidence.ts:139, lineNotify.ts:411; GET
-- /v1/mobile/incidents?tripId for the trip's driver).
SELECT * FROM incident_reports i
WHERE i.trip_id = sqlc.arg(trip_id)::uuid
  AND (sqlc.arg(all_tenants)::boolean OR i.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR i.driver_id = sqlc.narg(reach_driver)::uuid)
ORDER BY i.created_at, i.id;

-- name: CountIncidentsOfTrips :many
-- The monitor's incident count per trip of a page (useDriverMonitor.ts:482-503 listened to every incident).
SELECT i.trip_id, count(*)::int AS n
FROM incident_reports i
WHERE i.trip_id = ANY (sqlc.arg(trip_ids)::uuid[])
  AND (sqlc.arg(all_tenants)::boolean OR i.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
GROUP BY i.trip_id
ORDER BY i.trip_id;

-- name: ListIncidents :many
-- GET /v1/incidents (removes the 200-row cap; incident_reports_tenant_created, incident_reports_created under
-- X-Act-On-Tenant: *): newest first, keyset (created_at, id). from / to bound created_at, [from, to).
SELECT * FROM incident_reports i
WHERE (sqlc.arg(all_tenants)::boolean OR i.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR i.created_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR i.created_at < sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(trip_id)::uuid IS NULL OR i.trip_id = sqlc.narg(trip_id)::uuid)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR i.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (i.created_at, i.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY i.created_at DESC, i.id DESC
LIMIT sqlc.arg(row_limit);

-- name: DriverIncidents :many
-- The driver's own reports (GET /v1/mobile/incidents; incident_reports_driver), optionally of one trip.
SELECT * FROM incident_reports i
WHERE i.driver_id = sqlc.arg(driver_id)::uuid
  AND (sqlc.narg(trip_id)::uuid IS NULL OR i.trip_id = sqlc.narg(trip_id)::uuid)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (i.created_at, i.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY i.created_at DESC, i.id DESC
LIMIT sqlc.arg(row_limit);
