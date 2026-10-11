-- Maintenance (T40, Appendix B §B.2.15, §B.2.8 pm-check; Appendix A §A.2.5): maintenance_records with their files
-- (maintenance_files -> file_objects) and the truck columns the maintenance flows own (status while in the shop,
-- active_maintenance_id, last_alert_mileage, current_mileage, last_service_date, next_service_mileage). Tenant rows of
-- the truck's tenant (tenant_source 'truck'); RLS G-tenant decides (Appendix C §C.3.0): staff read and write the rows of
-- their tenant reach (own tenant and the carriers working for it). Every statement runs in db.WithPrincipal; the list
-- and lookups also name the reach (main spec §4.7, Appendix C §C.3.8; authz.Principal.TenantReach).
--
-- Lock order of every maintenance write: the truck first (LockTruck), then the record (LockRecord). The PM check locks
-- only the truck, so two writes on one truck serialise and no two of them wait for each other in reverse.

-- name: ListMaintenance :many
-- GET /v1/maintenance, newest first; keyset (created_at, id). The truck columns come from the truck row (the plate
-- and brand snapshots when the truck is not readable, '' when the record has none either: a quarantined ETL record has
-- no truck, and the ETL snapshots no brand); the file keys are aggregated per kind in position order.
SELECT m.*,
       coalesce(t.license_plate, m.truck_license_plate_snapshot, '')::text AS plate,
       coalesce(t.brand, m.truck_brand_snapshot, '')::text AS brand,
       t.model AS truck_model, t.ownership_type AS truck_ownership, t.status AS truck_status,
       coalesce((SELECT array_agg(f.object_key ORDER BY mf.position, f.object_key)
                 FROM maintenance_files mf JOIN file_objects f ON f.id = mf.file_id
                 WHERE mf.maintenance_id = m.id AND mf.kind = 'image'), '{}')::text[] AS image_keys,
       coalesce((SELECT array_agg(f.object_key ORDER BY mf.position, f.object_key)
                 FROM maintenance_files mf JOIN file_objects f ON f.id = mf.file_id
                 WHERE mf.maintenance_id = m.id AND mf.kind = 'receipt'), '{}')::text[] AS receipt_keys,
       coalesce((SELECT array_agg(f.object_key ORDER BY mf.position, f.object_key)
                 FROM maintenance_files mf JOIN file_objects f ON f.id = mf.file_id
                 WHERE mf.maintenance_id = m.id AND mf.kind = 'invoice'), '{}')::text[] AS invoice_keys
FROM maintenance_records m
LEFT JOIN trucks t ON t.id = m.truck_id
WHERE (sqlc.arg(all_tenants)::boolean OR m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(truck_id)::uuid IS NULL OR m.truck_id = sqlc.narg(truck_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR m.status = sqlc.narg(status)::text)
  AND (sqlc.narg(maintenance_type)::text IS NULL OR m.maintenance_type = sqlc.narg(maintenance_type)::text)
  AND (sqlc.narg(id)::uuid IS NULL OR m.id = sqlc.narg(id)::uuid)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (m.created_at, m.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY m.created_at DESC, m.id DESC
LIMIT sqlc.arg(row_limit);

-- name: CountMaintenanceByStatus :many
-- meta.statusCounts of the first page: every record matching the other filters, per status.
SELECT m.status, count(*)::bigint AS n
FROM maintenance_records m
WHERE (sqlc.arg(all_tenants)::boolean OR m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(truck_id)::uuid IS NULL OR m.truck_id = sqlc.narg(truck_id)::uuid)
  AND (sqlc.narg(maintenance_type)::text IS NULL OR m.maintenance_type = sqlc.narg(maintenance_type)::text)
GROUP BY m.status;

-- name: RecordTruck :one
-- The truck of a record, read before the locks (a record's truck never changes through the API).
SELECT truck_id FROM maintenance_records
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]));

-- name: LockRecord :one
SELECT * FROM maintenance_records WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: RecordStatus :one
-- The status of the truck's active maintenance (the PM check clears a terminal one).
SELECT status FROM maintenance_records WHERE id = sqlc.arg(id);

-- name: OpenPMBookingExists :one
-- checkMaintenanceAlert's guard (fn:triggers.ts:480-487): an open PM booking or scheduled visit of the truck.
SELECT EXISTS (SELECT 1 FROM maintenance_records
               WHERE truck_id = sqlc.arg(truck_id) AND status IN ('pm_booking', 'scheduled'))::boolean AS open;

-- name: InsertRecord :one
-- tenant_id is the truck's (tenant_source 'truck', tenancy.Resolve "maintenance"). A record born here has no
-- legacy_doc_id (the FS->PG mirror sets it on the rows it loads).
INSERT INTO maintenance_records (id, tenant_id, tenant_source, truck_id, truck_license_plate_snapshot,
                                 truck_brand_snapshot, maintenance_type, service_type, start_date, end_date, status,
                                 appointment_time, pickup_appointment_at, cost_labor_thb, cost_parts_thb, total_cost_thb,
                                 invoice_amount_thb, provider, provider_lat, provider_lng, payment_method, current_mileage,
                                 next_service_mileage, notes, created_by, updated_by)
VALUES (sqlc.arg(id), sqlc.arg(tenant_id), 'truck', sqlc.arg(truck_id),
        sqlc.narg(plate_snapshot), sqlc.narg(brand_snapshot), sqlc.arg(maintenance_type), sqlc.arg(service_type),
        sqlc.arg(start_date)::date, sqlc.narg(end_date)::date, sqlc.arg(status), sqlc.narg(appointment_time),
        sqlc.narg(pickup_appointment_at)::timestamptz, sqlc.narg(cost_labor_thb)::numeric,
        sqlc.narg(cost_parts_thb)::numeric, sqlc.narg(total_cost_thb)::numeric, sqlc.narg(invoice_amount_thb)::numeric,
        sqlc.narg(provider), sqlc.narg(provider_lat)::double precision, sqlc.narg(provider_lng)::double precision,
        sqlc.narg(payment_method), sqlc.narg(current_mileage)::int, sqlc.narg(next_service_mileage)::int,
        sqlc.narg(notes), sqlc.narg(actor)::uuid, sqlc.narg(actor)::uuid)
RETURNING *;

-- name: UpdateRecord :one
-- PATCH writes the merged row (Go merges the request into the locked row); truck_id and tenant_id never change.
UPDATE maintenance_records
SET maintenance_type = sqlc.arg(maintenance_type), service_type = sqlc.arg(service_type),
    start_date = sqlc.arg(start_date)::date, end_date = sqlc.narg(end_date)::date, status = sqlc.arg(status),
    appointment_time = sqlc.narg(appointment_time), pickup_appointment_at = sqlc.narg(pickup_appointment_at)::timestamptz,
    cost_labor_thb = sqlc.narg(cost_labor_thb)::numeric, cost_parts_thb = sqlc.narg(cost_parts_thb)::numeric,
    total_cost_thb = sqlc.narg(total_cost_thb)::numeric, invoice_amount_thb = sqlc.narg(invoice_amount_thb)::numeric,
    provider = sqlc.narg(provider), provider_lat = sqlc.narg(provider_lat)::double precision,
    provider_lng = sqlc.narg(provider_lng)::double precision, payment_method = sqlc.narg(payment_method),
    current_mileage = sqlc.narg(current_mileage)::int, next_service_mileage = sqlc.narg(next_service_mileage)::int,
    notes = sqlc.narg(notes), updated_by = sqlc.narg(actor)::uuid
WHERE id = sqlc.arg(id)
RETURNING *;

-- --- files (maintenance_files -> file_objects, main spec §9.3) --------------------------------------------------

-- name: RecordFiles :many
SELECT mf.kind, mf.file_id, mf.position, f.object_key
FROM maintenance_files mf JOIN file_objects f ON f.id = mf.file_id
WHERE mf.maintenance_id = sqlc.arg(maintenance_id)
ORDER BY mf.kind, mf.position, f.object_key;

-- name: DeleteRecordFiles :exec
-- Unlinks the files of the given kinds (the objects stay, main spec §9.3).
DELETE FROM maintenance_files WHERE maintenance_id = sqlc.arg(maintenance_id) AND kind = ANY (sqlc.arg(kinds)::text[]);

-- name: InsertRecordFiles :exec
-- One row per (file, kind, position) triple of the three equal-length arrays.
INSERT INTO maintenance_files (maintenance_id, file_id, kind, position)
SELECT sqlc.arg(maintenance_id)::uuid, (sqlc.arg(file_ids)::uuid[])[i], (sqlc.arg(kinds)::text[])[i],
       (sqlc.arg(positions)::int[])[i]
FROM generate_subscripts(sqlc.arg(file_ids)::uuid[], 1) AS i;

-- --- the truck ---------------------------------------------------------------------------------------------------

-- name: LockTruck :one
-- The truck of a maintenance write or a PM check, in the caller's reach (named here and checked by RLS).
SELECT id, tenant_id, license_plate, brand, model, status, legacy_status, next_service_mileage,
       current_mileage, last_alert_mileage, last_service_date, active_maintenance_id
FROM trucks
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
FOR UPDATE;

-- name: UpdateTruckMaintenance :exec
-- The maintenance columns of a truck (Go merges them into the locked row). legacy_status is the raw legacy value of the
-- current status (Appendix A §A.2.2): a status change clears it, as PATCH /v1/trucks does.
UPDATE trucks
SET status = sqlc.arg(status),
    legacy_status = CASE WHEN status IS DISTINCT FROM sqlc.arg(status) THEN NULL ELSE legacy_status END,
    active_maintenance_id = sqlc.narg(active_maintenance_id)::uuid,
    last_alert_mileage = sqlc.narg(last_alert_mileage)::int, current_mileage = sqlc.narg(current_mileage)::int,
    last_service_date = sqlc.narg(last_service_date)::date, next_service_mileage = sqlc.narg(next_service_mileage)::int,
    updated_by = sqlc.narg(actor)::uuid
WHERE id = sqlc.arg(id);

-- name: InsertTruckStatus :exec
-- A truck status change made by a maintenance flow appends its status_history row (append-only, as PATCH
-- /v1/trucks does): changed_by_label snapshots the actor's display name (a machine principal: the service's label).
INSERT INTO status_history (entity_type, entity_id, status, previous_status, changed_at, changed_by_user_id,
                            changed_by_label, reason)
VALUES ('truck', sqlc.arg(truck_id), sqlc.arg(status), sqlc.narg(previous_status), sqlc.arg(changed_at)::timestamptz,
        sqlc.narg(user_id)::uuid,
        coalesce(sqlc.narg(label)::text,
                 (SELECT coalesce(u.display_name, u.email::text) FROM users u WHERE u.id = sqlc.narg(user_id)::uuid)),
        sqlc.narg(reason));

-- name: TruckDriverIDs :many
-- The drivers a truck's maintenance reaches (realtime driver:{id}, the maintenance gate of app_driver_truck_ids()):
-- the drivers of its active assignments and the driver who holds it as the active truck. Rows the caller cannot read
-- are not listed.
SELECT DISTINCT driver_id::uuid AS driver_id
FROM (SELECT a.driver_id FROM truck_assignments a WHERE a.truck_id = sqlc.arg(truck_id) AND a.status = 'active'
      UNION ALL
      SELECT d.id FROM drivers d WHERE d.active_truck_id = sqlc.arg(truck_id)) reach
ORDER BY driver_id;
