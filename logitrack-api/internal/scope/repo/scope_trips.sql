-- Dispatcher and customer-scope reads of trip records: the scope_trips projection only (Appendix C §C.3.7).

-- name: ListScopeTrips :many
SELECT * FROM scope_trips
 WHERE (sqlc.narg(before_created_at)::timestamptz IS NULL
        OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: GetScopeTrip :one
SELECT * FROM scope_trips WHERE id = sqlc.arg(id);
