-- Dispatcher and customer-scope reads of standby records: the scope_standby projection only (no billing_*,
-- Appendix C §C.3.7).

-- name: ListScopeStandby :many
SELECT * FROM scope_standby
 WHERE (sqlc.narg(before_created_at)::timestamptz IS NULL
        OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);
