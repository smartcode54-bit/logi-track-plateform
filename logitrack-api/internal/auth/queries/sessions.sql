-- Sessions and refresh-token families (Appendix C §C.4.4, R2, R37, R83). PostgreSQL is the source of truth;
-- Redis auth:* keys are a hot index rebuilt from these rows.

-- name: RevokeInstallSessions :many
-- R83: one live session per (user, install); a re-login on the same install revokes the older one first.
UPDATE sessions
SET revoked_at = sqlc.arg(at)::timestamptz, revoked_reason = 'device_relogin'
WHERE user_id = sqlc.arg(user_id) AND install_id = sqlc.arg(install_id)::text AND revoked_at IS NULL
RETURNING id;

-- name: InsertSession :one
INSERT INTO sessions (user_id, platform, amr, active_tenant_id, install_id, app_version, ip, user_agent,
                      absolute_expires_at, created_at, last_seen_at)
VALUES (sqlc.arg(user_id), sqlc.arg(platform), sqlc.arg(amr), sqlc.narg(active_tenant_id), sqlc.narg(install_id),
        sqlc.narg(app_version), sqlc.narg(ip)::inet, sqlc.narg(user_agent), sqlc.arg(absolute_expires_at),
        sqlc.arg(created_at), sqlc.arg(created_at))
RETURNING id;

-- name: GetSession :one
SELECT id, user_id, platform, amr, active_tenant_id, install_id, absolute_expires_at, revoked_at
FROM sessions
WHERE id = sqlc.arg(id);

-- name: ListLiveSessions :many
SELECT id, platform, amr, install_id, device_label, app_version, coalesce(host(ip), '')::text AS ip, user_agent, created_at, last_seen_at
FROM sessions
WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL AND absolute_expires_at > sqlc.arg(now)
ORDER BY last_seen_at DESC, id DESC;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = sqlc.arg(at)::timestamptz, ip = coalesce(sqlc.narg(ip)::inet, ip) WHERE id = sqlc.arg(id);

-- name: SetActiveTenant :exec
UPDATE sessions SET active_tenant_id = sqlc.narg(tenant_id) WHERE id = sqlc.arg(id);

-- name: RevokeSessions :many
-- Revokes live sessions of one user: all of them, only those in only_ids, and never except_id.
UPDATE sessions
SET revoked_at = sqlc.arg(at)::timestamptz, revoked_reason = sqlc.arg(reason)::text, revoked_by = sqlc.narg(revoked_by)
WHERE user_id = sqlc.arg(user_id)
  AND revoked_at IS NULL
  AND (sqlc.narg(only_ids)::uuid[] IS NULL OR id = ANY (sqlc.narg(only_ids)::uuid[]))
  AND (sqlc.narg(except_id)::uuid IS NULL OR id <> sqlc.narg(except_id)::uuid)
RETURNING id, install_id;

-- name: SessionAuthState :one
-- The PostgreSQL side of the per-request check (C.4.3: never fail open): on a version miss, for a token
-- newer than the cached version, and whenever Redis is unreachable.
SELECT u.auth_version, s.revoked_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.id = sqlc.arg(session_id) AND s.user_id = sqlc.arg(user_id);

-- name: InsertRefreshToken :one
INSERT INTO refresh_tokens (session_id, family_id, token_hash, issued_at, expires_at)
VALUES (sqlc.arg(session_id), sqlc.arg(family_id), sqlc.arg(token_hash), sqlc.arg(issued_at), sqlc.arg(expires_at))
RETURNING id;

-- name: RefreshTokenUser :one
-- The user of a presented refresh token, without a lock (sessions.user_id never changes): Refresh and
-- Logout lock that user before the token and its session (lock order users -> sessions -> refresh_tokens).
SELECT s.user_id
FROM refresh_tokens rt
JOIN sessions s ON s.id = rt.session_id
WHERE rt.token_hash = sqlc.arg(token_hash);

-- name: LockRefreshToken :one
-- The presented token and its session, locked so concurrent refreshes of one family serialise. Callers
-- hold the user's row lock first (LockUser): every auth transaction locks users -> sessions ->
-- refresh_tokens, so a refresh never deadlocks with a revocation, a password change or a tenant switch.
SELECT rt.id, rt.session_id, rt.family_id, rt.expires_at, rt.rotated_at, rt.replaced_by, rt.revoked_at,
       s.user_id, s.platform, s.amr, s.active_tenant_id, s.install_id, s.absolute_expires_at,
       s.revoked_at AS session_revoked_at
FROM refresh_tokens rt
JOIN sessions s ON s.id = rt.session_id
WHERE rt.token_hash = sqlc.arg(token_hash)
FOR UPDATE OF rt, s;

-- name: LockRefreshTokenByID :one
SELECT id, rotated_at, revoked_at FROM refresh_tokens WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: RotateRefreshToken :exec
UPDATE refresh_tokens SET rotated_at = sqlc.arg(at)::timestamptz, replaced_by = sqlc.arg(replaced_by)::uuid WHERE id = sqlc.arg(id);

-- name: SetSuccessor :exec
-- Grace path (R37): the rotated token now points at the sibling; rotated_at keeps the first rotation time,
-- so the 30 s window never grows.
UPDATE refresh_tokens SET replaced_by = sqlc.arg(replaced_by)::uuid WHERE id = sqlc.arg(id);

-- name: RevokeRefreshToken :one
UPDATE refresh_tokens SET revoked_at = sqlc.arg(at)::timestamptz, revoked_reason = sqlc.arg(reason)::text
WHERE id = sqlc.arg(id)
RETURNING token_hash;

-- name: RevokeFamily :many
UPDATE refresh_tokens SET revoked_at = sqlc.arg(at)::timestamptz, revoked_reason = sqlc.arg(reason)::text
WHERE family_id = sqlc.arg(family_id) AND revoked_at IS NULL
RETURNING token_hash;

-- name: RevokeSessionTokens :many
UPDATE refresh_tokens SET revoked_at = sqlc.arg(at)::timestamptz, revoked_reason = sqlc.arg(reason)::text
WHERE session_id = ANY (sqlc.arg(session_ids)::uuid[]) AND revoked_at IS NULL
RETURNING token_hash;

-- name: DeleteDeviceToken :exec
-- POST /v1/auth/logout removes the push token of the install it signs out (C.8).
DELETE FROM device_tokens WHERE user_id = sqlc.arg(user_id) AND install_id = sqlc.arg(install_id);

-- name: InsertPasswordResetToken :one
-- Only sha256(token) is stored (C.4.9); the token itself never reaches the database or the outbox.
INSERT INTO password_reset_tokens (user_id, purpose, token_hash, expires_at, requested_ip, requested_by, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(purpose), sqlc.arg(token_hash), sqlc.arg(expires_at), sqlc.narg(requested_ip)::inet,
        sqlc.narg(requested_by), sqlc.arg(created_at))
RETURNING id;

-- name: LockPasswordResetToken :one
SELECT id, user_id, purpose, expires_at, used_at
FROM password_reset_tokens
WHERE token_hash = sqlc.arg(token_hash)
FOR UPDATE;

-- name: UsePasswordResetToken :exec
UPDATE password_reset_tokens SET used_at = sqlc.arg(at)::timestamptz WHERE id = sqlc.arg(id);

-- name: InsertOutboxEvent :exec
-- Transactional outbox (Appendix B §B.5.5): the api never talks to RabbitMQ; the relay publishes later.
INSERT INTO outbox_events (routing_key, aggregate_type, aggregate_id, event_type, tenant_id, payload, headers,
                           realtime_topics)
VALUES (sqlc.arg(routing_key), sqlc.arg(aggregate_type), sqlc.arg(aggregate_id), sqlc.arg(event_type),
        sqlc.narg(tenant_id), sqlc.arg(payload), sqlc.arg(headers), sqlc.arg(realtime_topics));
