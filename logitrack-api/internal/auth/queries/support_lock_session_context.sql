-- name: SupportLockSessionContext :one
SELECT s.access_mode, s.context_version, s.active_tenant_id, s.support_return_tenant_id,
 s.platform, s.amr, LEAST(s.absolute_expires_at, coalesce((SELECT max(rt.expires_at) FROM refresh_tokens rt
 WHERE rt.session_id=s.id AND rt.revoked_at IS NULL),s.absolute_expires_at)), s.revoked_at
FROM sessions s WHERE s.id=$1 AND s.user_id=$2 FOR UPDATE;
