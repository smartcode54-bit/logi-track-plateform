-- Vehicle expenses (T40, Appendix B §B.2.15; Appendix A §A.2.5): fuel and other expenses of drivers and trucks (mobile,
-- staff, toll import). Tenant rows (tenant_source 'driver', else 'truck'); RLS G-tenant decides (Appendix C §C.3.0):
-- staff read and write the rows of their tenant reach (own tenant and the carriers working for it), never a scope
-- principal's (no p_scope_read). Every statement runs in db.WithPrincipal; the list and the lookups also name the reach
-- (main spec §4.7, Appendix C §C.3.8; authz.Principal.TenantReach).
--
-- Lock order of every expense write: the expense rows (in id order), then the trucks the PM check locks (in id order).
-- A staff entry that runs the PM check locks its truck before it inserts its row (LockTruck): the insert's foreign-key
-- check takes FOR KEY SHARE on the truck, so locking it FOR UPDATE afterwards would deadlock two entries of one truck.
-- A toll import holds the truck's import lock (LockTollImport) for its whole transaction.

-- name: ListExpenses :many
-- GET /v1/expenses (W8 server filter, R35): the types the caller may read are a filter of the SQL, never a filter in
-- the client (features/accounting/api/expenses.ts:90-101 read every type); keyset (expense_at, id), newest first.
-- The plate is the row's frozen snapshot; driver_name follows lib/driverName.ts (Thai full name, else first + last).
SELECT sqlc.embed(e),
       coalesce(nullif(btrim(d.full_name_th), ''), nullif(btrim(d.first_name || ' ' || d.last_name), ''), '')::text
         AS driver_name,
       rf.object_key AS receipt_key, odo.object_key AS odometer_key
FROM vehicle_expenses e
LEFT JOIN drivers d ON d.id = e.driver_id
LEFT JOIN file_objects rf ON rf.id = e.receipt_file_id
LEFT JOIN file_objects odo ON odo.id = e.odometer_file_id
WHERE (sqlc.arg(all_tenants)::boolean OR e.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND e.expense_type = ANY (sqlc.arg(types)::text[])
  AND (sqlc.narg(status)::text IS NULL OR e.status = sqlc.narg(status)::text)
  AND (sqlc.narg(category)::text IS NULL OR e.category = sqlc.narg(category)::text)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR e.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(truck_id)::uuid IS NULL OR e.truck_id = sqlc.narg(truck_id)::uuid)
  AND (sqlc.narg(from_date)::date IS NULL OR e.expense_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR e.expense_date <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(after_at)::timestamptz IS NULL
       OR (e.expense_at, e.id) < (sqlc.narg(after_at)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY e.expense_at DESC, e.id DESC
LIMIT sqlc.arg(row_limit);

-- name: CountExpensesByStatus :many
-- meta.statusCounts of the first page: every row matching the other filters, per status (the audit page's tabs).
SELECT e.status, count(*)::bigint AS n
FROM vehicle_expenses e
WHERE (sqlc.arg(all_tenants)::boolean OR e.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND e.expense_type = ANY (sqlc.arg(types)::text[])
  AND (sqlc.narg(category)::text IS NULL OR e.category = sqlc.narg(category)::text)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR e.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(truck_id)::uuid IS NULL OR e.truck_id = sqlc.narg(truck_id)::uuid)
  AND (sqlc.narg(from_date)::date IS NULL OR e.expense_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR e.expense_date <= sqlc.narg(to_date)::date)
GROUP BY e.status;

-- name: GetExpenses :many
-- The payload of written rows (the list's shape), in id order.
SELECT sqlc.embed(e),
       coalesce(nullif(btrim(d.full_name_th), ''), nullif(btrim(d.first_name || ' ' || d.last_name), ''), '')::text
         AS driver_name,
       rf.object_key AS receipt_key, odo.object_key AS odometer_key
FROM vehicle_expenses e
LEFT JOIN drivers d ON d.id = e.driver_id
LEFT JOIN file_objects rf ON rf.id = e.receipt_file_id
LEFT JOIN file_objects odo ON odo.id = e.odometer_file_id
WHERE e.id = ANY (sqlc.arg(ids)::uuid[])
ORDER BY e.id;

-- name: LockExpenses :many
-- The rows a status or edit request changes, locked in id order (two bulk requests never wait for each other in
-- reverse), in the caller's reach.
SELECT * FROM vehicle_expenses
WHERE id = ANY (sqlc.arg(ids)::uuid[])
  AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
ORDER BY id
FOR UPDATE;

-- name: GetDriver :one
-- The driver of a staff-entered expense, in the caller's reach: its tenant stamps the row (tenant_source 'driver'),
-- its truck defaults the row's truck (the active truck, else the home truck of an active assignment).
SELECT d.id, d.tenant_id, d.active_truck_id, a.truck_id AS assignment_truck_id
FROM drivers d
LEFT JOIN truck_assignments a ON a.id = d.current_assignment_id AND a.status = 'active'
WHERE d.id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR d.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]));

-- name: GetTruck :one
-- A truck an expense names, in the caller's reach: its plate is snapshotted on the row (frozen, never re-resolved).
SELECT id, tenant_id, license_plate
FROM trucks
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]));

-- name: LockTruck :exec
-- The truck of a staff entry that runs the PM check, locked before the row is inserted (the lock order above); the PM
-- check's own lock of the row is then a re-lock. A truck outside the caller's reach locks nothing (the PM check
-- answers truck_not_found).
SELECT id FROM trucks
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
FOR UPDATE;

-- name: LockTollImport :exec
-- Serialises the toll imports of one truck in this transaction, so the duplicate check of a row (TollRowExists) and
-- its insert are one step: two imports of one history, overlapping (a double submit, a retry after a proxy timeout),
-- add it once (the second one, waiting here, reads the first one's committed rows).
SELECT pg_advisory_xact_lock(hashtextextended('vehicle_expenses_toll:' || sqlc.arg(truck_id)::uuid::text, 0));

-- name: LockFuelTaxInvoice :exec
-- Serialises the writers of one driver's fuel tax invoice in this transaction, so the check below and the write are
-- one step even where the D5 unique index (vehicle_expenses_fuel_taxinv, 0010) is not applied yet.
SELECT pg_advisory_xact_lock(hashtextextended('vehicle_expenses_fuel_taxinv:' || sqlc.arg(driver_id)::uuid::text || ':'
                                              || sqlc.arg(tax_inv_id)::text, 0));

-- name: FuelTaxInvoiceTaken :one
-- (driver_id, tax_inv_id) of fuel rows is unique (D5): another fuel row of the driver with this tax invoice.
SELECT EXISTS (SELECT 1 FROM vehicle_expenses
               WHERE expense_type = 'fuel' AND driver_id = sqlc.arg(driver_id) AND tax_inv_id = sqlc.arg(tax_inv_id)
                 AND id IS DISTINCT FROM sqlc.narg(except_id)::uuid)::boolean AS taken;

-- name: InsertExpense :one
-- Staff-entered rows and toll imports. tenant_id is the driver's (else the truck's), resolved by the service, never
-- taken from the body. A row born here has no legacy_doc_id (the FS->PG mirror sets it on the rows it loads).
INSERT INTO vehicle_expenses (id, tenant_id, tenant_source, driver_id, truck_id,
                              truck_license_plate_snapshot, expense_type, category, expense_at, amount_thb, status,
                              volume_liters, price_per_liter, odometer_km, station_tax_id, tax_inv_id, refill_lat,
                              refill_lng, receipt_file_id, odometer_file_id, note, description, admin_note, distance_km,
                              toll_import_sequence, toll_location, toll_lane, toll_source_type, created_by_user_id)
VALUES (sqlc.arg(id), sqlc.arg(tenant_id), sqlc.arg(tenant_source), sqlc.narg(driver_id)::uuid,
        sqlc.narg(truck_id)::uuid, sqlc.narg(plate_snapshot), sqlc.arg(expense_type), sqlc.narg(category),
        sqlc.arg(expense_at)::timestamptz, sqlc.arg(amount_thb)::numeric, sqlc.arg(status),
        sqlc.narg(volume_liters)::numeric, sqlc.narg(price_per_liter)::numeric, sqlc.narg(odometer_km)::int,
        sqlc.narg(station_tax_id), sqlc.narg(tax_inv_id), sqlc.narg(refill_lat)::double precision,
        sqlc.narg(refill_lng)::double precision, sqlc.narg(receipt_file_id)::uuid, sqlc.narg(odometer_file_id)::uuid,
        sqlc.narg(note), sqlc.narg(description), sqlc.narg(admin_note), sqlc.narg(distance_km)::numeric,
        sqlc.narg(toll_import_sequence)::int, sqlc.narg(toll_location), sqlc.narg(toll_lane),
        sqlc.narg(toll_source_type), sqlc.narg(actor)::uuid)
RETURNING *;

-- name: UpdateExpense :exec
-- PATCH writes the merged row (Go merges the request into the locked row). expense_type, driver_id, status and the
-- tenant never change here (status has its own route, the tenant is frozen by trg_freeze_tenant_id).
UPDATE vehicle_expenses
SET truck_id = sqlc.narg(truck_id)::uuid, truck_license_plate_snapshot = sqlc.narg(plate_snapshot),
    category = sqlc.narg(category), expense_at = sqlc.arg(expense_at)::timestamptz,
    amount_thb = sqlc.arg(amount_thb)::numeric, volume_liters = sqlc.narg(volume_liters)::numeric,
    price_per_liter = sqlc.narg(price_per_liter)::numeric, odometer_km = sqlc.narg(odometer_km)::int,
    station_tax_id = sqlc.narg(station_tax_id), tax_inv_id = sqlc.narg(tax_inv_id),
    refill_lat = sqlc.narg(refill_lat)::double precision, refill_lng = sqlc.narg(refill_lng)::double precision,
    receipt_file_id = sqlc.narg(receipt_file_id)::uuid, odometer_file_id = sqlc.narg(odometer_file_id)::uuid,
    note = sqlc.narg(note), description = sqlc.narg(description), admin_note = sqlc.narg(admin_note),
    distance_km = sqlc.narg(distance_km)::numeric, toll_import_sequence = sqlc.narg(toll_import_sequence)::int,
    toll_location = sqlc.narg(toll_location), toll_lane = sqlc.narg(toll_lane),
    toll_source_type = sqlc.narg(toll_source_type)
WHERE id = sqlc.arg(id);

-- name: SetExpenseStatus :exec
-- POST /v1/expenses/{id}/status and the bulk route: the audit decision and its note (a blank note keeps the stored one,
-- expenses.ts:279-319).
UPDATE vehicle_expenses
SET status = sqlc.arg(status), admin_note = coalesce(sqlc.narg(admin_note), admin_note)
WHERE id = sqlc.arg(id);

-- name: TollRowExists :one
-- A toll row already imported for the truck: same instant, amount, place and lane (a re-imported card history skips
-- the transactions it already holds).
SELECT EXISTS (SELECT 1 FROM vehicle_expenses
               WHERE truck_id = sqlc.arg(truck_id) AND expense_type = 'other' AND category = 'toll'
                 AND expense_at = sqlc.arg(expense_at)::timestamptz AND amount_thb = sqlc.arg(amount_thb)::numeric
                 AND coalesce(toll_location, '') = sqlc.arg(toll_location)::text
                 AND coalesce(toll_lane, '') = sqlc.arg(toll_lane)::text)::boolean AS taken;
