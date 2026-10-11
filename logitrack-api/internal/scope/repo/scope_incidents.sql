-- Dispatcher and customer-scope reads of incident reports: incidents of visible trips only, through the
-- scope_incidents projection (Appendix C §C.3.7).

-- name: ListScopeIncidents :many
SELECT * FROM scope_incidents
 WHERE (sqlc.narg(before_created_at)::timestamptz IS NULL
        OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: ListScopeIncidentsFiltered :many
-- GET /v1/incidents for a scope principal (T30): newest first, keyset (created_at, id); from / to bound created_at,
-- [from, to). A principal whose parties have no visible trip reads none (p_scope_read: incidents of visible trips;
-- the legacy page showed every incident then, incident-reports/page.tsx:185).
SELECT * FROM scope_incidents
 WHERE (sqlc.narg(from_at)::timestamptz IS NULL OR created_at >= sqlc.narg(from_at)::timestamptz)
   AND (sqlc.narg(to_at)::timestamptz IS NULL OR created_at < sqlc.narg(to_at)::timestamptz)
   AND (sqlc.narg(trip_id)::uuid IS NULL OR trip_id = sqlc.narg(trip_id)::uuid)
   AND (sqlc.narg(driver_id)::uuid IS NULL OR driver_id = sqlc.narg(driver_id)::uuid)
   AND (sqlc.narg(after_created)::timestamptz IS NULL
        OR (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);
