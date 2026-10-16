-- name: SupportLockVisitUser :exec
SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE;
