-- Drivers: the driver master of a tenant (Appendix B §B.2.7, Appendix A §A.2.2). Tenant-stamped (tenant_source 'self':
-- the own fleet, or the carrier the driver runs for); RLS G-tenant decides (Appendix C §C.3.0): staff read and write
-- the drivers of their tenant reach (own tenant and the carriers working for it), never the quarantine tenant's. Every
-- statement runs in db.WithPrincipal. The list and detail queries name the reach as well (main spec §4.7, Appendix C
-- §C.3.8; authz.Principal.TenantReach): all_tenants under X-Act-On-Tenant: *, else tenant_ids. Dispatchers and
-- customer-scope principals read the scope_drivers projection through internal/scope instead (no PII, §C.3.7).
--
-- Three concepts stay apart (.vibe-rules.md "Vehicle identity"): current_assignment_id is the home truck (written by
-- the truck assignments of internal/fleet), active_truck_* the truck the driver is responsible for right now (set at
-- check-in, cleared at the end of the trip, by the driver app), tasks.truck_id the truck of one job. Nothing here
-- writes either of the first two.

-- name: ListDrivers :many
-- GET /v1/drivers, newest first; keyset (created_at, id). q matches the names, the licence number, the mobile and
-- the e-mail, folded (search_fold, 0026): q_words are its folded words as LIKE patterns (searchtext.LikePatterns),
-- each somewhere in the row, in any order; NULL is no search. The joins read under the caller's RLS: a tenant,
-- assignment or truck the caller cannot see stays NULL. assignment_status tells a home truck apart from a pointer at
-- a revoked or cancelled row (loaded data), which the payload does not show.
SELECT sqlc.embed(d),
       t.name_th AS tenant_name_th, t.name_en AS tenant_name_en, t.kind AS tenant_kind,
       a.truck_id AS assignment_truck_id, tr.license_plate AS assignment_truck_plate, tr.model AS assignment_truck_model,
       a.created_at AS assignment_created_at, a.status AS assignment_status
FROM drivers d
LEFT JOIN tenants t ON t.id = d.tenant_id
LEFT JOIN truck_assignments a ON a.id = d.current_assignment_id
LEFT JOIN trucks tr ON tr.id = a.truck_id
WHERE (sqlc.arg(all_tenants)::boolean OR d.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(tenant_id)::uuid IS NULL OR d.tenant_id = sqlc.narg(tenant_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR d.status = sqlc.narg(status)::text)
  AND (sqlc.narg(q_words)::text[] IS NULL
       OR search_fold(coalesce(d.full_name_th, '') || ' ' || coalesce(d.first_name, '') || ' ' || coalesce(d.last_name, '')
                      || ' ' || coalesce(d.truck_license_id, '') || ' ' || coalesce(d.mobile, '') || ' '
                      || coalesce(d.email::text, '')) LIKE ALL (sqlc.narg(q_words)::text[]))
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (d.created_at, d.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY d.created_at DESC, d.id DESC
LIMIT sqlc.arg(row_limit);

-- name: CountDriversByStatus :many
-- meta.statusCounts of GET /v1/drivers: every row matching the other filters, not just the page (the drivers page's
-- stats counted only the first 100 rows, DriversList.tsx:58-63).
SELECT d.status, count(*)::int AS n
FROM drivers d
WHERE (sqlc.arg(all_tenants)::boolean OR d.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(tenant_id)::uuid IS NULL OR d.tenant_id = sqlc.narg(tenant_id)::uuid)
  AND (sqlc.narg(q_words)::text[] IS NULL
       OR search_fold(coalesce(d.full_name_th, '') || ' ' || coalesce(d.first_name, '') || ' ' || coalesce(d.last_name, '')
                      || ' ' || coalesce(d.truck_license_id, '') || ' ' || coalesce(d.mobile, '') || ' '
                      || coalesce(d.email::text, '')) LIKE ALL (sqlc.narg(q_words)::text[]))
GROUP BY d.status
ORDER BY d.status;

-- name: GetDriver :one
-- GET /v1/drivers/{id} names the reach too: a dispatcher's scope branch (p_scope_read) opens in-scope drivers of
-- other tenants to the scope_drivers projection only, never to this full row (Appendix C §C.3.7).
SELECT sqlc.embed(d),
       t.name_th AS tenant_name_th, t.name_en AS tenant_name_en, t.kind AS tenant_kind,
       a.truck_id AS assignment_truck_id, tr.license_plate AS assignment_truck_plate, tr.model AS assignment_truck_model,
       a.created_at AS assignment_created_at, a.status AS assignment_status
FROM drivers d
LEFT JOIN tenants t ON t.id = d.tenant_id
LEFT JOIN truck_assignments a ON a.id = d.current_assignment_id
LEFT JOIN trucks tr ON tr.id = a.truck_id
WHERE d.id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR d.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]));

-- name: DriverTenant :one
-- PATCH /v1/drivers/{id}: the tenant a tenantId in the body is compared with (the reach is named, as in GetDriver).
SELECT tenant_id FROM drivers
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]));

-- name: LockDriver :one
SELECT * FROM drivers WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: TenantKind :one
-- The kind of the tenant a new driver is stamped with (tenants p_read: the caller's reach).
SELECT kind FROM tenants WHERE id = sqlc.arg(id);

-- name: InsertDriver :one
-- tenant_id is resolved by the service (the effective tenant, or a carrier of its contractor reach), never taken
-- unchecked from the body; user_id is never set here (the driver link of PUT /v1/users/{id}/driver-link, T19), nor
-- current_assignment_id (truck assignments) or active_truck_* (the driver app's check-in).
INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, full_name_th, mobile, email, profile_file_id,
                     birth_date, id_card, id_card_expired_date, id_card_file_id, truck_license_id, license_type,
                     license_expired_date, license_file_id, employment_type, contract_years, employment_start,
                     employment_end, hire_date, probation_passed, status, assign_to_project)
VALUES (sqlc.arg(tenant_id), 'self', sqlc.arg(first_name), sqlc.arg(last_name), sqlc.narg(full_name_th),
        sqlc.arg(mobile), sqlc.narg(email), sqlc.narg(profile_file_id)::uuid, sqlc.narg(birth_date)::date,
        sqlc.narg(id_card), sqlc.narg(id_card_expired_date)::date, sqlc.narg(id_card_file_id)::uuid,
        sqlc.narg(truck_license_id), sqlc.narg(license_type), sqlc.narg(license_expired_date)::date,
        sqlc.narg(license_file_id)::uuid, sqlc.arg(employment_type), sqlc.narg(contract_years)::numeric,
        sqlc.narg(employment_start)::date, sqlc.narg(employment_end)::date, sqlc.narg(hire_date)::date,
        sqlc.arg(probation_passed), sqlc.arg(status), sqlc.narg(assign_to_project))
RETURNING id;

-- name: UpdateDriver :exec
-- PATCH writes the merged row (Go merges the request into the locked row). tenant_id never changes here (frozen,
-- trg_freeze_tenant_id; a move is iam.Admin.MoveDriver under app.tenant_move), nor user_id, current_assignment_id or
-- active_truck_* (see the header).
UPDATE drivers
SET first_name = sqlc.arg(first_name), last_name = sqlc.arg(last_name), full_name_th = sqlc.narg(full_name_th),
    mobile = sqlc.arg(mobile), email = sqlc.narg(email), profile_file_id = sqlc.narg(profile_file_id)::uuid,
    birth_date = sqlc.narg(birth_date)::date, id_card = sqlc.narg(id_card),
    id_card_expired_date = sqlc.narg(id_card_expired_date)::date, id_card_file_id = sqlc.narg(id_card_file_id)::uuid,
    truck_license_id = sqlc.narg(truck_license_id), license_type = sqlc.narg(license_type),
    license_expired_date = sqlc.narg(license_expired_date)::date, license_file_id = sqlc.narg(license_file_id)::uuid,
    employment_type = sqlc.arg(employment_type), contract_years = sqlc.narg(contract_years)::numeric,
    employment_start = sqlc.narg(employment_start)::date, employment_end = sqlc.narg(employment_end)::date,
    hire_date = sqlc.narg(hire_date)::date, probation_passed = sqlc.arg(probation_passed), status = sqlc.arg(status),
    assign_to_project = sqlc.narg(assign_to_project)
WHERE id = sqlc.arg(id);

-- --- status history (status_history, entity_type 'driver'; append-only) ------------------------------------------

-- name: InsertDriverStatus :exec
-- changed_by_label snapshots the actor's display name or e-mail (the legacy statusHistory changedByName); a user
-- reads its own users row (p_self_read). A machine principal has no users row: user NULL, label from the service.
INSERT INTO status_history (entity_type, entity_id, status, previous_status, changed_at, changed_by_user_id,
                            changed_by_label, reason)
VALUES ('driver', sqlc.arg(driver_id), sqlc.arg(status), sqlc.narg(previous_status), sqlc.arg(changed_at)::timestamptz,
        sqlc.narg(user_id)::uuid,
        coalesce(sqlc.narg(label)::text,
                 (SELECT coalesce(u.display_name, u.email::text) FROM users u WHERE u.id = sqlc.narg(user_id)::uuid)),
        sqlc.narg(reason));

-- name: DriverStatusHistory :many
-- GET /v1/drivers/{id}: the whole history, newest first.
SELECT status, previous_status, changed_at, changed_by_user_id, changed_by_label, reason
FROM status_history
WHERE entity_type = 'driver' AND entity_id = sqlc.arg(driver_id)
ORDER BY changed_at DESC, id DESC;

-- --- customer driver ids (driver_customer_codes; legacy customerDriverIds{customerCode:{idTypeKey:value}}) -------

-- name: DriverCustomerCodes :many
-- Parent-scoped (staff who see the driver). The customer is global master data (p_read: any principal).
SELECT c.customer_id, cu.code AS customer_code, cu.name AS customer_name, c.id_type_key, c.value
FROM driver_customer_codes c
JOIN customers cu ON cu.id = c.customer_id
WHERE c.driver_id = sqlc.arg(driver_id)
ORDER BY cu.code NULLS LAST, c.customer_id, c.id_type_key;

-- name: CustomerIDTypes :many
-- The id types each named customer defines (customers.driverIdTypes[]); a customer without a row here is unknown.
SELECT cu.id AS customer_id, t.key
FROM customers cu
LEFT JOIN customer_driver_id_types t ON t.customer_id = cu.id
WHERE cu.id = ANY (sqlc.arg(customer_ids)::uuid[]);

-- name: DeleteDriverCustomerCodes :exec
DELETE FROM driver_customer_codes WHERE driver_id = sqlc.arg(driver_id);

-- name: InsertDriverCustomerCodes :exec
-- One row per (customer, key, value) triple of the three equal-length arrays.
INSERT INTO driver_customer_codes (driver_id, customer_id, id_type_key, value)
SELECT sqlc.arg(driver_id)::uuid, (sqlc.arg(customer_ids)::uuid[])[i], (sqlc.arg(keys)::text[])[i],
       (sqlc.arg(vals)::text[])[i]
FROM generate_subscripts(sqlc.arg(customer_ids)::uuid[], 1) AS i;

-- --- the linked login (users: staff read the users of their tenant reach, p_staff_read) ---------------------------

-- name: DriverLogin :one
SELECT id, email, status, must_change_password FROM users WHERE id = sqlc.arg(id);
