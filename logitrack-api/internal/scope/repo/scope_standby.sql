-- Dispatcher and customer-scope reads of standby records: the scope_standby projection only (no billing_*,
-- Appendix C §C.3.7).

-- name: ListScopeStandby :many
SELECT * FROM scope_standby
 WHERE (sqlc.narg(before_created_at)::timestamptz IS NULL
        OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: ListScopeStandbyByCreated :many
-- GET /v1/standby for a scope principal (T30): newest first, keyset (created_at, id); from / to bound created_at,
-- [from, to).
SELECT * FROM scope_standby
 WHERE (sqlc.narg(from_at)::timestamptz IS NULL OR created_at >= sqlc.narg(from_at)::timestamptz)
   AND (sqlc.narg(to_at)::timestamptz IS NULL OR created_at < sqlc.narg(to_at)::timestamptz)
   AND (sqlc.narg(driver_id)::uuid IS NULL OR driver_id = sqlc.narg(driver_id)::uuid)
   AND (sqlc.narg(after_created)::timestamptz IS NULL
        OR (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: ListScopeStandbyByEnded :many
-- GET /v1/standby?sort=ended for a scope principal (T30): completed records by ended_at, latest first, keyset
-- (ended_at, id).
SELECT * FROM scope_standby
 WHERE status = 'completed' AND ended_at IS NOT NULL
   AND (sqlc.narg(from_at)::timestamptz IS NULL OR ended_at >= sqlc.narg(from_at)::timestamptz)
   AND (sqlc.narg(to_at)::timestamptz IS NULL OR ended_at < sqlc.narg(to_at)::timestamptz)
   AND (sqlc.narg(driver_id)::uuid IS NULL OR driver_id = sqlc.narg(driver_id)::uuid)
   AND (sqlc.narg(after_ended)::timestamptz IS NULL
        OR (ended_at, id) < (sqlc.narg(after_ended)::timestamptz, sqlc.arg(after_id)::uuid))
 ORDER BY ended_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: ScopeStandbyPhotosOf :many
-- The two photos of a page of the projection (scope_standby_photos, 0016).
SELECT * FROM scope_standby_photos WHERE standby_id = ANY (sqlc.arg(standby_ids)::uuid[]) ORDER BY standby_id, photo_type;
