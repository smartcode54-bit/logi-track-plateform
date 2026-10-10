-- Push devices of the caller (Appendix B §B.2.3, Appendix C §C.8, R4): device_tokens PK (user_id,
-- install_id), token UNIQUE, driver_id nullable. Written under WithSystem after Go authorization: the
-- token may sit on another user's row, which RLS (p_self) would hide.

-- name: LockDeviceToken :exec
-- Registrations of one token run one at a time (transaction advisory lock on the token's hash), so two
-- devices or users racing for it never deadlock or trip the UNIQUE index.
SELECT pg_advisory_xact_lock(hashtextextended('device_tokens:' || sqlc.arg(token)::text, 0));

-- name: ReleaseDeviceToken :execrows
-- A token held by another (user, install) moves to the caller in the same transaction (shared phone,
-- re-login as someone else, an ETL legacy row of the same device): the UNIQUE index never fails.
DELETE FROM device_tokens
WHERE token = sqlc.arg(token) AND (user_id <> sqlc.arg(user_id)::uuid OR install_id <> sqlc.arg(install_id)::text);

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
