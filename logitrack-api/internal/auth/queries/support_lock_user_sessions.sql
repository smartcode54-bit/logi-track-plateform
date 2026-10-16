-- name: SupportLockUserSessions :many
SELECT id FROM sessions WHERE user_id=$1 AND access_mode='support' AND revoked_at IS NULL ORDER BY id FOR UPDATE;
