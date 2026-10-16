-- name: MemberSessionAdmission :one
-- Ordinary member admission compares live context and credential versions without reloading axes.
SELECT s.access_mode, s.context_version, s.active_tenant_id, s.revoked_at,
  LEAST(s.absolute_expires_at, coalesce((SELECT max(rt.expires_at) FROM refresh_tokens rt
    WHERE rt.session_id=s.id AND rt.revoked_at IS NULL), s.absolute_expires_at))::timestamptz AS expires_at,
  u.auth_version, u.status AS user_status, coalesce(m.role, '')::text AS member_role,
  coalesce(m.status, '')::text AS member_status,
  EXISTS (SELECT 1 FROM user_platform_roles p WHERE p.user_id=u.id)::boolean AS has_platform_role
FROM sessions s JOIN users u ON u.id=s.user_id
LEFT JOIN memberships m ON m.user_id=u.id AND m.tenant_id=s.active_tenant_id
WHERE s.id=sqlc.arg(session_id) AND s.user_id=sqlc.arg(user_id);
