-- Truck assignments: the home-truck binding of a driver (truckAssignment collection, Appendix B §B.2.8, T23). A row
-- is stamped with the tenant of its truck (tenant_source 'truck', Appendix C §C.3.10) and RLS G-tenant decides
-- (staff of the tenant reach; a driver reads its own rows). trucks.currentAssignments[] is derived from
-- status='active' (R14); drivers.current_assignment_id points at the driver's active row. An assignment never
-- touches drivers.active_truck_* (the truck of the job running now) nor tasks.truck_id (the truck of one job):
-- three separate concepts (.vibe-rules.md "Vehicle identity"). Every statement runs in db.WithPrincipal.

-- name: ListAssignments :many
-- GET /v1/truck-assignments, newest first; keyset (created_at, id). The truck and driver columns read under the
-- caller's RLS (NULL / '' when not visible).
SELECT a.id, a.tenant_id, a.truck_id, a.driver_id, a.status, a.admin_user_id, a.admin_label, a.created_at,
       a.revoked_at, tr.license_plate AS truck_plate, tr.model AS truck_model,
       coalesce(nullif(btrim(d.full_name_th), ''), btrim(d.first_name || ' ' || d.last_name), '')::text AS driver_name
FROM truck_assignments a
LEFT JOIN trucks tr ON tr.id = a.truck_id
LEFT JOIN drivers d ON d.id = a.driver_id
WHERE (sqlc.arg(all_tenants)::boolean OR a.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(status)::text IS NULL OR a.status = sqlc.narg(status)::text)
  AND (sqlc.narg(truck_id)::uuid IS NULL OR a.truck_id = sqlc.narg(truck_id)::uuid)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR a.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (a.created_at, a.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetAssignment :one
SELECT a.id, a.tenant_id, a.truck_id, a.driver_id, a.status, a.admin_user_id, a.admin_label, a.created_at,
       a.revoked_at, tr.license_plate AS truck_plate, tr.model AS truck_model,
       coalesce(nullif(btrim(d.full_name_th), ''), btrim(d.first_name || ' ' || d.last_name), '')::text AS driver_name
FROM truck_assignments a
LEFT JOIN trucks tr ON tr.id = a.truck_id
LEFT JOIN drivers d ON d.id = a.driver_id
WHERE a.id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR a.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]));

-- name: LockAssignment :one
SELECT id, tenant_id, truck_id, driver_id, status FROM truck_assignments WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: LockAssignDriver :one
-- The driver an assignment binds, locked first: two assignments of one driver serialise here (the second sees the
-- first's current_assignment_id). Staff in reach only (p_tenant_staff applies to FOR UPDATE).
SELECT id, tenant_id, status, current_assignment_id FROM drivers WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: DriverActiveAssignment :one
-- An active assignment of the driver other than the one named (legacy rows may hold one without
-- current_assignment_id). All rows: RLS hides other tenants' rows only from a caller who cannot see them anyway.
SELECT id FROM truck_assignments
WHERE driver_id = sqlc.arg(driver_id) AND status = 'active' AND id <> sqlc.arg(except_id)::uuid
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: AssignTruck :one
-- The truck of a new assignment in the caller's reach; FOR KEY SHARE holds it against a delete while the row that
-- references it commits.
SELECT id, tenant_id, license_plate, model FROM trucks
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
FOR KEY SHARE;

-- name: TenantsLinked :one
-- Whether one of the two tenants is the contractor of the other (a carrier working for the own fleet, R60).
SELECT EXISTS (SELECT 1 FROM tenants
               WHERE (id = sqlc.arg(a)::uuid AND contractor_tenant_id = sqlc.arg(b)::uuid)
                  OR (id = sqlc.arg(b)::uuid AND contractor_tenant_id = sqlc.arg(a)::uuid))::boolean AS linked;

-- name: InsertAssignment :one
-- admin_label snapshots the actor's display name or e-mail (legacy adminName), else the service's label.
INSERT INTO truck_assignments (tenant_id, tenant_source, truck_id, driver_id, status, admin_user_id, admin_label,
                               created_at, updated_at)
VALUES (sqlc.arg(tenant_id), 'truck', sqlc.arg(truck_id), sqlc.arg(driver_id), 'active', sqlc.narg(user_id)::uuid,
        coalesce(sqlc.narg(label)::text,
                 (SELECT coalesce(u.display_name, u.email::text) FROM users u WHERE u.id = sqlc.narg(user_id)::uuid),
                 'System'),
        sqlc.arg(at)::timestamptz, sqlc.arg(at)::timestamptz)
RETURNING id;

-- name: RevokeAssignment :exec
UPDATE truck_assignments SET status = 'revoked', revoked_at = sqlc.arg(at)::timestamptz WHERE id = sqlc.arg(id);

-- name: SetDriverAssignment :exec
-- The driver's home truck and status (on_duty while it holds an assignment, back to active when it ends).
UPDATE drivers SET current_assignment_id = sqlc.narg(assignment_id)::uuid, status = sqlc.arg(status)
WHERE id = sqlc.arg(id);

-- name: InsertAssignmentDriverStatus :exec
-- The driver's status entry of an assignment that changed its status (status_history, entity 'driver').
INSERT INTO status_history (entity_type, entity_id, status, previous_status, changed_at, changed_by_user_id,
                            changed_by_label, reason)
VALUES ('driver', sqlc.arg(driver_id), sqlc.arg(status), sqlc.narg(previous_status), sqlc.arg(changed_at)::timestamptz,
        sqlc.narg(user_id)::uuid,
        coalesce(sqlc.narg(label)::text,
                 (SELECT coalesce(u.display_name, u.email::text) FROM users u WHERE u.id = sqlc.narg(user_id)::uuid)),
        sqlc.narg(reason));
