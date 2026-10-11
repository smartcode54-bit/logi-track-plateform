-- Truck assignments: the home-truck binding of a driver (truckAssignment collection, Appendix B §B.2.8, T23). A row
-- is stamped with the tenant of its truck (tenant_source 'truck', Appendix C §C.3.10) and RLS G-tenant decides
-- (staff of the tenant reach; a driver reads its own rows). trucks.currentAssignments[] is derived from
-- status='active' (R14); drivers.current_assignment_id points at the driver's active row. An assignment never
-- touches drivers.active_truck_* (the truck of the job running now) nor tasks.truck_id (the truck of one job):
-- three separate concepts (.vibe-rules.md "Vehicle identity"). Every statement runs in db.WithPrincipal; the driver's
-- side that the caller's RLS may hide (the driver's active row in another tenant, a driver outside the revoker's
-- reach) goes through the two SECURITY DEFINER helpers of 0013, which check the caller themselves.

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
-- FOR NO KEY UPDATE, the lock of the revoke's own UPDATE: a release that moves another driver's pointer onto this row
-- (its foreign key takes FOR KEY SHARE) does not wait for it while this transaction waits for that driver.
SELECT id, tenant_id, truck_id, driver_id, status FROM truck_assignments WHERE id = sqlc.arg(id) FOR NO KEY UPDATE;

-- name: LockAssignDriver :one
-- The driver of a new assignment, locked first: two assignments of one driver serialise here, and so do an assignment
-- and a revoke's release (assignment_release_driver takes the same lock). Staff in reach only (p_tenant_staff applies
-- to FOR UPDATE).
SELECT id, tenant_id, status FROM drivers WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: DriverActiveAssignment :one
-- The driver's newest active assignment in any tenant; no row when it holds none (legacy rows may hold one without
-- current_assignment_id). Not under the caller's RLS: a row carries its truck's tenant, so the own fleet's truck
-- holding a carrier's driver is invisible to that carrier's staff and still binds the driver. The SECURITY DEFINER
-- helper driver_active_assignment (0013) answers only staff that reach the driver (asked after LockAssignDriver).
SELECT h.id::uuid AS id
FROM driver_active_assignment(sqlc.arg(driver_id)::uuid) AS h (id)
WHERE h.id IS NOT NULL;

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

-- name: ReleaseAssignmentDriver :one
-- The driver's side of a revoke, after RevokeAssignment in the same transaction: the SECURITY DEFINER helper
-- assignment_release_driver (0013) locks the driver whatever the caller can read (a carrier revoking the row of its
-- truck that binds an own-fleet driver cannot read that driver), moves a current_assignment_id that names this row,
-- a non-active row or nothing to the driver's newest active row in any tenant or clears it, turns on_duty without one
-- into active with its status entry, and returns the driver's tenant. The label is resolved here, under the caller's
-- RLS (a person reads its own users row), as in InsertAssignmentDriverStatus.
SELECT assignment_release_driver(sqlc.arg(assignment_id)::uuid, sqlc.narg(user_id)::uuid,
         coalesce(sqlc.narg(label)::text,
                  (SELECT coalesce(u.display_name, u.email::text) FROM users u WHERE u.id = sqlc.narg(user_id)::uuid)),
         sqlc.arg(reason)::text, sqlc.arg(at)::timestamptz)::uuid AS driver_tenant_id;

-- name: SetDriverAssignment :exec
-- The driver's home truck and status on_duty of a new assignment (a revoke releases the driver through
-- ReleaseAssignmentDriver).
UPDATE drivers SET current_assignment_id = sqlc.narg(assignment_id)::uuid, status = sqlc.arg(status)
WHERE id = sqlc.arg(id);

-- name: InsertAssignmentDriverStatus :exec
-- The driver's status entry of a new assignment that changed its status (status_history, entity 'driver').
INSERT INTO status_history (entity_type, entity_id, status, previous_status, changed_at, changed_by_user_id,
                            changed_by_label, reason)
VALUES ('driver', sqlc.arg(driver_id), sqlc.arg(status), sqlc.narg(previous_status), sqlc.arg(changed_at)::timestamptz,
        sqlc.narg(user_id)::uuid,
        coalesce(sqlc.narg(label)::text,
                 (SELECT coalesce(u.display_name, u.email::text) FROM users u WHERE u.id = sqlc.narg(user_id)::uuid)),
        sqlc.narg(reason));
