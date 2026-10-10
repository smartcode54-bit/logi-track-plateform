-- Dispatcher and customer-scope reads of incident reports: incidents of visible trips only, through the
-- scope_incidents projection (Appendix C §C.3.7).

-- name: ListScopeIncidents :many
SELECT * FROM scope_incidents
 WHERE (sqlc.narg(before_created_at)::timestamptz IS NULL
        OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);
