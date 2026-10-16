-- name: SupportLockVisitSessions :many
SELECT s.id, s.revoked_at, s.revoked_reason, s.absolute_expires_at,
    (SELECT max(rt.expires_at) FROM refresh_tokens rt WHERE rt.session_id=s.id AND rt.revoked_at IS NULL),
    (SELECT min(e.created_at) FROM security_events e WHERE e.target_user_id=s.user_id
      AND e.event_type='platform_role_revoked' AND e.details->>'role'='platform_admin' AND e.created_at>=v.started_at)
    FROM sessions s JOIN support_visits v ON v.session_id=s.id AND v.ended_at IS NULL
    WHERE s.user_id=$1 ORDER BY s.id FOR UPDATE OF s;
