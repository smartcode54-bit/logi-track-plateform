-- Holidays (T45, Appendix B §B.2.17; Appendix A §A.2.7). holidays is a nullable-tenant table (Appendix C §C.3.0):
-- tenant_id NULL is the public calendar every tenant sees (holiday_type 'public', R12), written by stewards only (R60);
-- 'company' and 'other' rows belong to a tenant and are written by its staff (p_write). Request statements run in
-- db.WithPrincipal.

-- name: ListHolidays :many
-- GET /v1/holidays: the public calendar and the tenant's own rows (the calendar payroll prices against,
-- ListHolidayDates), every type and status, in [from_date, to_date) when given; by day.
SELECT * FROM holidays
WHERE (tenant_id IS NULL OR tenant_id = sqlc.narg(tenant_id)::uuid)
  AND (sqlc.narg(from_date)::date IS NULL OR holiday_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR holiday_date < sqlc.narg(to_date)::date)
ORDER BY holiday_date, holiday_type, id;

-- name: GetHoliday :one
-- A row the caller may change: public, or a tenant row of its reach (p_write).
SELECT * FROM holidays
WHERE id = sqlc.arg(id)
  AND (tenant_id IS NULL OR sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]));

-- name: LockHoliday :one
SELECT * FROM holidays WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: LockHolidayByKey :one
-- The upsert key of saveHoliday (holidays.ts:98-113): one holiday per (tenant, day, type), UNIQUE NULLS NOT DISTINCT.
SELECT * FROM holidays
WHERE tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::uuid AND holiday_date = sqlc.arg(holiday_date)
  AND holiday_type = sqlc.arg(holiday_type)
FOR UPDATE;

-- name: LockHolidaysOfYear :many
-- PUT /v1/holidays/generate: the rows of one scope (the public calendar, or one tenant's) in [from_date, to_date).
SELECT * FROM holidays
WHERE tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::uuid
  AND holiday_date >= sqlc.arg(from_date) AND holiday_date < sqlc.arg(to_date)
ORDER BY holiday_date, holiday_type, id
FOR UPDATE;

-- name: InsertHoliday :one
INSERT INTO holidays (tenant_id, holiday_date, holiday_type, status, name, name_en, name_th, description, description_en,
  description_th, is_recurring, created_by)
VALUES (sqlc.narg(tenant_id), sqlc.arg(holiday_date), sqlc.arg(holiday_type), sqlc.arg(status), sqlc.arg(name),
  sqlc.narg(name_en), sqlc.narg(name_th), sqlc.narg(description), sqlc.narg(description_en), sqlc.narg(description_th),
  sqlc.arg(is_recurring), sqlc.narg(created_by))
RETURNING *;

-- name: UpdateHoliday :one
UPDATE holidays
SET tenant_id = sqlc.narg(tenant_id), holiday_date = sqlc.arg(holiday_date), holiday_type = sqlc.arg(holiday_type),
    status = sqlc.arg(status), name = sqlc.arg(name), name_en = sqlc.narg(name_en), name_th = sqlc.narg(name_th),
    description = sqlc.narg(description), description_en = sqlc.narg(description_en),
    description_th = sqlc.narg(description_th), is_recurring = sqlc.arg(is_recurring)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteHoliday :one
-- The Firestore document id of the deleted row (legacy_doc_id, else id::text, R25): the projection deletes it.
DELETE FROM holidays WHERE id = sqlc.arg(id)
RETURNING coalesce(legacy_doc_id, id::text)::text AS doc_id;
