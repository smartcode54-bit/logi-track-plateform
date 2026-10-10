-- Push devices of the caller (Appendix B §B.2.3, Appendix C §C.8, R4): device_tokens PK (user_id,
-- install_id), token UNIQUE, driver_id nullable. Written under WithSystem after Go authorization: the
-- token may sit on another user's row, which RLS (p_self) would hide.

-- name: LockDeviceToken :exec
-- Registrations of one token run one at a time (transaction advisory lock on the token's hash), so two
-- devices or users racing for it never deadlock or trip the UNIQUE index.
SELECT pg_advisory_xact_lock(hashtextextended('device_tokens:' || sqlc.arg(token)::text, 0));

-- name: ReleaseDeviceToken :many
-- The rows a registration may take the token from, deleted in the same transaction so the UNIQUE index
-- never fails: the caller's own other rows (a reinstall, the caller's ETL legacy row of the device) and
-- another user's row of the install the caller's session is signed in on (shared phone: the previous
-- user did not log out). session_install is NULL for a session without an install (web): own rows
-- only. Any other holder keeps the token (TokenHeldElsewhere).
DELETE FROM device_tokens
WHERE token = sqlc.arg(token)
  AND ((user_id = sqlc.arg(user_id)::uuid AND install_id <> sqlc.arg(install_id)::text)
       OR (user_id <> sqlc.arg(user_id)::uuid AND install_id = sqlc.narg(session_install)::text))
RETURNING user_id, install_id, legacy_source;

-- name: TokenHeldElsewhere :one
-- After ReleaseDeviceToken: another user's row still holds the token (409 already_exists).
SELECT EXISTS (SELECT 1 FROM device_tokens WHERE token = sqlc.arg(token) AND user_id <> sqlc.arg(user_id)::uuid)::boolean AS held;

-- name: UpsertDeviceToken :exec
-- driver_id is the caller's linked driver read now, never a claim (R4).
INSERT INTO device_tokens (user_id, install_id, token, driver_id, platform, app_flavor, app_version, created_at, last_seen_at)
VALUES (sqlc.arg(user_id), sqlc.arg(install_id), sqlc.arg(token),
        (SELECT d.id FROM drivers d WHERE d.user_id = sqlc.arg(user_id)::uuid),
        sqlc.arg(platform), sqlc.narg(app_flavor), sqlc.narg(app_version), sqlc.arg(at), sqlc.arg(at))
ON CONFLICT (user_id, install_id) DO UPDATE
SET token = EXCLUDED.token, driver_id = EXCLUDED.driver_id, platform = EXCLUDED.platform,
    app_flavor = EXCLUDED.app_flavor, app_version = coalesce(EXCLUDED.app_version, device_tokens.app_version),
    legacy_source = NULL, last_seen_at = EXCLUDED.last_seen_at;

-- name: PruneUserDevices :execrows
-- At most auth.MaxDevicesPerUser rows per user: after a registration the least recently seen rows
-- beyond @keep others go (never the install just registered), so a reinstall never fails and no user
-- can grow the fan-out of notify.fcm without bound.
DELETE FROM device_tokens dt
WHERE dt.user_id = sqlc.arg(user_id) AND dt.install_id <> sqlc.arg(install_id)::text
  AND dt.install_id NOT IN (SELECT k.install_id FROM device_tokens k
                         WHERE k.user_id = sqlc.arg(user_id) AND k.install_id <> sqlc.arg(install_id)::text
                         ORDER BY k.last_seen_at DESC, k.install_id
                         LIMIT sqlc.arg(keep)::bigint);
