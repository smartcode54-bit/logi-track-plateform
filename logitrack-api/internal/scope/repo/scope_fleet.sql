-- Drivers, trucks and carrier names as a scope principal may see them: the scope_drivers, scope_trucks and
-- scope_tenants projections (no ID card, licence, birth date, insurance, tax or cost columns; Appendix C
-- §C.3.7). Drivers and trucks are visible only through recent in-scope work (app_recent_work_in_scope).

-- name: ScopeDriversByID :many
SELECT * FROM scope_drivers WHERE id = ANY (sqlc.arg(ids)::uuid[]) ORDER BY id;

-- name: ScopeTrucksByID :many
SELECT * FROM scope_trucks WHERE id = ANY (sqlc.arg(ids)::uuid[]) ORDER BY id;

-- name: ScopeTenantsByID :many
SELECT * FROM scope_tenants WHERE id = ANY (sqlc.arg(ids)::uuid[]) ORDER BY id;

-- name: ListScopeCarriers :many
-- GET /v1/subcontractors?fields=minimal for a scope principal (T20): carrier names only, by Thai name.
SELECT * FROM scope_tenants
WHERE kind = 'carrier'
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(name_th), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(coalesce(name_en, '')), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(coalesce(code::text, '')), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(after_name)::text IS NULL OR (name_th, id) > (sqlc.narg(after_name)::text, sqlc.arg(after_id)::uuid))
ORDER BY name_th, id
LIMIT sqlc.arg(row_limit);

-- name: ListScopeTrucks :many
-- The truck list of a dispatcher (T22): the projection only, by plate; type matches the class or the master word. q
-- matches the plate and the province, folded as the staff list does (search_fold, 0026; q_words NULL is no search).
SELECT * FROM scope_trucks
WHERE (sqlc.narg(tenant_id)::uuid IS NULL OR tenant_id = sqlc.narg(tenant_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(truck_type)::text IS NULL OR vehicle_class = upper(sqlc.narg(truck_type)::text)
       OR lower(type_raw) = lower(sqlc.narg(truck_type)::text))
  AND (sqlc.narg(q_words)::text[] IS NULL
       OR search_fold(coalesce(license_plate, '') || ' ' || coalesce(province, '')) LIKE ALL (sqlc.narg(q_words)::text[]))
  AND (sqlc.narg(after_plate)::text IS NULL
       OR (license_plate, id) > (sqlc.narg(after_plate)::text, sqlc.arg(after_id)::uuid))
ORDER BY license_plate, id
LIMIT sqlc.arg(row_limit);

-- name: CountScopeTrucksByStatus :many
-- meta.statusCounts of a dispatcher's truck list (UI8): every projected row matching the other filters.
SELECT status, count(*)::int AS n
FROM scope_trucks
WHERE (sqlc.narg(tenant_id)::uuid IS NULL OR tenant_id = sqlc.narg(tenant_id)::uuid)
  AND (sqlc.narg(truck_type)::text IS NULL OR vehicle_class = upper(sqlc.narg(truck_type)::text)
       OR lower(type_raw) = lower(sqlc.narg(truck_type)::text))
  AND (sqlc.narg(q_words)::text[] IS NULL
       OR search_fold(coalesce(license_plate, '') || ' ' || coalesce(province, '')) LIKE ALL (sqlc.narg(q_words)::text[]))
GROUP BY status
ORDER BY status;

-- name: ListScopeDrivers :many
-- The driver list of a dispatcher (T23): the projection only, by display name (the Thai full name, else first and
-- last name); q matches the names and the mobile, folded as the staff list does (search_fold, 0026; q_words NULL is
-- no search).
SELECT *
FROM scope_drivers
WHERE (sqlc.narg(tenant_id)::uuid IS NULL OR tenant_id = sqlc.narg(tenant_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(q_words)::text[] IS NULL
       OR search_fold(coalesce(full_name_th, '') || ' ' || coalesce(first_name, '') || ' ' || coalesce(last_name, '')
                      || ' ' || coalesce(mobile, '')) LIKE ALL (sqlc.narg(q_words)::text[]))
  AND (sqlc.narg(after_name)::text IS NULL
       OR (coalesce(nullif(btrim(full_name_th), ''), btrim(first_name || ' ' || last_name)), id)
          > (sqlc.narg(after_name)::text, sqlc.arg(after_id)::uuid))
ORDER BY coalesce(nullif(btrim(full_name_th), ''), btrim(first_name || ' ' || last_name)), id
LIMIT sqlc.arg(row_limit);

-- name: CountScopeDriversByStatus :many
-- meta.statusCounts of a dispatcher's driver list: every projected row matching the other filters.
SELECT status, count(*)::int AS n
FROM scope_drivers
WHERE (sqlc.narg(tenant_id)::uuid IS NULL OR tenant_id = sqlc.narg(tenant_id)::uuid)
  AND (sqlc.narg(q_words)::text[] IS NULL
       OR search_fold(coalesce(full_name_th, '') || ' ' || coalesce(first_name, '') || ' ' || coalesce(last_name, '')
                      || ' ' || coalesce(mobile, '')) LIKE ALL (sqlc.narg(q_words)::text[]))
GROUP BY status
ORDER BY status;
