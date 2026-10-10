-- Drivers, trucks and carrier names as a scope principal may see them: the scope_drivers, scope_trucks and
-- scope_tenants projections (no ID card, licence, birth date, insurance, tax or cost columns; Appendix C
-- §C.3.7). Drivers and trucks are visible only through recent in-scope work (app_recent_work_in_scope).

-- name: ScopeDriversByID :many
SELECT * FROM scope_drivers WHERE id = ANY (sqlc.arg(ids)::uuid[]) ORDER BY id;

-- name: ScopeTrucksByID :many
SELECT * FROM scope_trucks WHERE id = ANY (sqlc.arg(ids)::uuid[]) ORDER BY id;

-- name: ScopeTenantsByID :many
SELECT * FROM scope_tenants WHERE id = ANY (sqlc.arg(ids)::uuid[]) ORDER BY id;
