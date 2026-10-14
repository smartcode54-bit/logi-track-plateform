-- Trucks: the truck master of a tenant (Appendix B §B.2.8, Appendix A §A.2.2). Tenant-stamped (tenant_source 'self':
-- the own fleet, or the carrier a subcontractor truck belongs to); RLS G-tenant decides (Appendix C §C.3.0): staff
-- read and write the trucks of their tenant reach (own tenant and the carriers working for it), never the quarantine
-- tenant's. Every statement runs in db.WithPrincipal. The list names the reach as well (main spec §4.7, Appendix C
-- §C.3.8; authz.Principal.TenantReach): all_tenants under X-Act-On-Tenant: *, else tenant_ids.

-- name: ListTrucks :many
-- GET /v1/trucks, newest first; keyset (created_at, id). type matches the vehicle class or the master type word. q
-- matches the plate, province, brand and model folded (search_fold, 0026: q_words are its folded words as LIKE
-- patterns, each somewhere in the row, in any order; NULL is no search), or the whole of q equals the GPS id or the VIN.
SELECT *
FROM trucks
WHERE (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(tenant_id)::uuid IS NULL OR tenant_id = sqlc.narg(tenant_id)::uuid)
  AND (sqlc.narg(ownership)::text IS NULL OR ownership_type = sqlc.narg(ownership)::text)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(truck_type)::text IS NULL OR vehicle_class = upper(sqlc.narg(truck_type)::text)
       OR lower(type_raw) = lower(sqlc.narg(truck_type)::text))
  AND (sqlc.narg(q_words)::text[] IS NULL
       OR search_fold(coalesce(license_plate, '') || ' ' || coalesce(province, '') || ' ' || coalesce(brand, '') || ' '
                      || coalesce(model, '')) LIKE ALL (sqlc.narg(q_words)::text[])
       OR lower(coalesce(gps_vehicle_id, '')) = lower(sqlc.narg(q)::text)
       OR lower(coalesce(vin, '')) = lower(sqlc.narg(q)::text))
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: CountTrucksByStatus :many
-- meta.statusCounts of GET /v1/trucks (UI8): every row matching the filters other than status (and the cursor), not
-- just the page, so a counter reads one row instead of every truck (the truck-assignment page's "total trucks").
SELECT status, count(*)::int AS n
FROM trucks
WHERE (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(tenant_id)::uuid IS NULL OR tenant_id = sqlc.narg(tenant_id)::uuid)
  AND (sqlc.narg(ownership)::text IS NULL OR ownership_type = sqlc.narg(ownership)::text)
  AND (sqlc.narg(truck_type)::text IS NULL OR vehicle_class = upper(sqlc.narg(truck_type)::text)
       OR lower(type_raw) = lower(sqlc.narg(truck_type)::text))
  AND (sqlc.narg(q_words)::text[] IS NULL
       OR search_fold(coalesce(license_plate, '') || ' ' || coalesce(province, '') || ' ' || coalesce(brand, '') || ' '
                      || coalesce(model, '')) LIKE ALL (sqlc.narg(q_words)::text[])
       OR lower(coalesce(gps_vehicle_id, '')) = lower(sqlc.narg(q)::text)
       OR lower(coalesce(vin, '')) = lower(sqlc.narg(q)::text))
GROUP BY status
ORDER BY status;

-- name: GetTruck :one
-- GET /v1/trucks/{id} names the reach too: a dispatcher's scope branch (p_scope_read) opens in-scope trucks of other
-- tenants to the scope_trucks projection only, never to this full row (Appendix C §C.3.7).
SELECT * FROM trucks
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]));

-- name: LockTruck :one
SELECT * FROM trucks WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: LockTruckByPlate :one
-- POST /v1/trucks/import: the truck an import row with this plate updates (trucks_plate_per_tenant).
SELECT * FROM trucks WHERE tenant_id = sqlc.arg(tenant_id) AND license_plate = sqlc.arg(license_plate) FOR UPDATE;

-- name: InsertTruck :one
-- tenant_id is resolved by the service (the effective tenant, or a carrier of its contractor reach), never taken
-- unchecked from the body; vehicle_class comes from the one mapper (internal/fleet/vehicleclass), NULL when unknown.
INSERT INTO trucks (tenant_id, tenant_source, ownership_type, license_plate, province, vin, engine_number,
                    gps_vehicle_id, status, brand, model, year, color, type_raw, vehicle_class, seats, fuel_type,
                    engine_capacity, fuel_capacity, max_load_weight_kg, registration_date, buying_date, notes,
                    tax_expiry_date, tax_expense_thb, tax_responsible, tax_renewal_status, maintenance_responsible,
                    last_service_date, next_service_date, next_service_mileage, pm_interval_km, current_mileage,
                    insurance_policy_id, insurance_policy_number, insurance_company, insurance_type,
                    insurance_start_date, insurance_expiry_date, insurance_premium_thb, insurance_notes,
                    insurance_renewal_status, payment_method, created_by, updated_by)
VALUES (sqlc.arg(tenant_id), 'self', sqlc.arg(ownership_type), sqlc.arg(license_plate), sqlc.arg(province),
        sqlc.narg(vin), sqlc.narg(engine_number), sqlc.narg(gps_vehicle_id), sqlc.arg(status), sqlc.arg(brand),
        sqlc.arg(model), sqlc.arg(year), sqlc.arg(color), sqlc.arg(type_raw), sqlc.narg(vehicle_class),
        sqlc.narg(seats)::smallint, sqlc.narg(fuel_type), sqlc.narg(engine_capacity)::numeric,
        sqlc.narg(fuel_capacity)::numeric, sqlc.narg(max_load_weight_kg)::numeric, sqlc.narg(registration_date)::date,
        sqlc.narg(buying_date)::date, sqlc.narg(notes), sqlc.narg(tax_expiry_date)::date,
        sqlc.narg(tax_expense_thb)::numeric, sqlc.arg(tax_responsible), sqlc.narg(tax_renewal_status),
        sqlc.arg(maintenance_responsible), sqlc.narg(last_service_date)::date, sqlc.narg(next_service_date)::date,
        sqlc.narg(next_service_mileage)::int, sqlc.narg(pm_interval_km)::int, sqlc.narg(current_mileage)::int,
        sqlc.narg(insurance_policy_id), sqlc.narg(insurance_policy_number), sqlc.narg(insurance_company),
        sqlc.narg(insurance_type), sqlc.narg(insurance_start_date)::date, sqlc.narg(insurance_expiry_date)::date,
        sqlc.narg(insurance_premium_thb)::numeric, sqlc.narg(insurance_notes), sqlc.narg(insurance_renewal_status),
        sqlc.narg(payment_method), sqlc.narg(actor)::uuid, sqlc.narg(actor)::uuid)
RETURNING id;

-- name: UpdateTruck :exec
-- PATCH, a renewal and an import update write the merged row (Go merges the request into the locked row);
-- tenant_id and ownership_type never change here (tenant_id is frozen, trg_freeze_tenant_id). The maintenance columns
-- last_alert_mileage and active_maintenance_id belong to the PM check and maintenance flows (P3), not to this write.
-- legacy_status is the raw legacy value of the current status (Appendix A §A.2.2): a write that keeps the status
-- keeps it, a status change clears it (the right-hand status is the old row's value).
UPDATE trucks
SET license_plate = sqlc.arg(license_plate), province = sqlc.arg(province), vin = sqlc.narg(vin),
    engine_number = sqlc.narg(engine_number), gps_vehicle_id = sqlc.narg(gps_vehicle_id), status = sqlc.arg(status),
    legacy_status = CASE WHEN status IS DISTINCT FROM sqlc.arg(status) THEN NULL ELSE legacy_status END,
    brand = sqlc.arg(brand), model = sqlc.arg(model), year = sqlc.arg(year), color = sqlc.arg(color),
    type_raw = sqlc.arg(type_raw), vehicle_class = sqlc.narg(vehicle_class), seats = sqlc.narg(seats)::smallint,
    fuel_type = sqlc.narg(fuel_type), engine_capacity = sqlc.narg(engine_capacity)::numeric,
    fuel_capacity = sqlc.narg(fuel_capacity)::numeric, max_load_weight_kg = sqlc.narg(max_load_weight_kg)::numeric,
    registration_date = sqlc.narg(registration_date)::date, buying_date = sqlc.narg(buying_date)::date,
    notes = sqlc.narg(notes), tax_expiry_date = sqlc.narg(tax_expiry_date)::date,
    tax_expense_thb = sqlc.narg(tax_expense_thb)::numeric, tax_responsible = sqlc.arg(tax_responsible),
    tax_renewal_status = sqlc.narg(tax_renewal_status), maintenance_responsible = sqlc.arg(maintenance_responsible),
    last_service_date = sqlc.narg(last_service_date)::date, next_service_date = sqlc.narg(next_service_date)::date,
    next_service_mileage = sqlc.narg(next_service_mileage)::int, pm_interval_km = sqlc.narg(pm_interval_km)::int,
    current_mileage = sqlc.narg(current_mileage)::int, insurance_policy_id = sqlc.narg(insurance_policy_id),
    insurance_policy_number = sqlc.narg(insurance_policy_number), insurance_company = sqlc.narg(insurance_company),
    insurance_type = sqlc.narg(insurance_type), insurance_start_date = sqlc.narg(insurance_start_date)::date,
    insurance_expiry_date = sqlc.narg(insurance_expiry_date)::date,
    insurance_premium_thb = sqlc.narg(insurance_premium_thb)::numeric, insurance_notes = sqlc.narg(insurance_notes),
    insurance_renewal_status = sqlc.narg(insurance_renewal_status), payment_method = sqlc.narg(payment_method),
    updated_by = sqlc.narg(actor)::uuid
WHERE id = sqlc.arg(id);

-- name: TenantKind :one
-- The kind of the tenant a new truck is stamped with (own_fleet -> ownership own, carrier -> subcontractor); tenants
-- p_read: the caller's reach.
SELECT kind FROM tenants WHERE id = sqlc.arg(id);

-- --- status history (status_history, entity_type 'truck'; append-only: UPDATE and DELETE are revoked from the
-- --- writing roles and refused by trg_forbid_mutation) -------------------------------------------------------------

-- name: InsertTruckStatus :exec
-- changed_by_label snapshots the actor's display name or email (the legacy statusHistory changedBy string); a user
-- reads its own users row (p_self_read). A machine principal has no users row: user NULL, label from the service.
INSERT INTO status_history (entity_type, entity_id, status, previous_status, changed_at, changed_by_user_id,
                            changed_by_label, reason)
VALUES ('truck', sqlc.arg(truck_id), sqlc.arg(status), sqlc.narg(previous_status), sqlc.arg(changed_at)::timestamptz,
        sqlc.narg(user_id)::uuid,
        coalesce(sqlc.narg(label)::text,
                 (SELECT coalesce(u.display_name, u.email::text) FROM users u WHERE u.id = sqlc.narg(user_id)::uuid)),
        sqlc.narg(reason));

-- name: TruckStatusHistory :many
-- GET /v1/trucks/{id}: the whole history, newest first (append-only, a few rows per truck).
SELECT status, previous_status, changed_at, changed_by_user_id, changed_by_label, reason
FROM status_history
WHERE entity_type = 'truck' AND entity_id = sqlc.arg(truck_id)
ORDER BY changed_at DESC, id DESC;

-- --- current assignment (derived from truck_assignments, R14; written by T23) ----------------------------------

-- name: TruckCurrentAssignments :many
-- driver_name is '' when the driver row is not visible to the caller (the assignment still is).
SELECT a.id, a.driver_id, a.created_at,
       coalesce(nullif(btrim(d.full_name_th), ''), btrim(d.first_name || ' ' || d.last_name), '')::text AS driver_name
FROM truck_assignments a
LEFT JOIN drivers d ON d.id = a.driver_id
WHERE a.truck_id = sqlc.arg(truck_id) AND a.status = 'active'
ORDER BY a.created_at DESC, a.id DESC;

-- --- files (truck_files -> file_objects, main spec §9.3) -------------------------------------------------------

-- name: TruckFiles :many
SELECT kind, file_id, position FROM truck_files WHERE truck_id = sqlc.arg(truck_id) ORDER BY kind, position, file_id;

-- name: DeleteTruckFiles :exec
-- Unlinks the given files of a truck (the objects stay, main spec §9.3).
DELETE FROM truck_files WHERE truck_id = sqlc.arg(truck_id) AND file_id = ANY (sqlc.arg(file_ids)::uuid[]);

-- name: InsertTruckFiles :exec
-- One row per (file, kind, position) triple of the three equal-length arrays.
INSERT INTO truck_files (truck_id, file_id, kind, position)
SELECT sqlc.arg(truck_id)::uuid, (sqlc.arg(file_ids)::uuid[])[i], (sqlc.arg(kinds)::text[])[i], (sqlc.arg(positions)::int[])[i]
FROM generate_subscripts(sqlc.arg(file_ids)::uuid[], 1) AS i;
