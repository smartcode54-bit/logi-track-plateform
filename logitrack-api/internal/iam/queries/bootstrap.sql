-- Platform bootstrap (T19, Appendix C §C.1.6, §C.5.5; owner addition 2026-10-10): cmd/seed
-- bootstrap-platform-admins grants platform_admin to the PLATFORM_ADMIN_EMAILS users, and seed --bootstrap-admin
-- creates or updates the super admin of a fresh deployment from BOOTSTRAP_ADMIN_EMAIL / _PASSWORD. Both run in
-- the seed's db.WithSystem transaction.

-- name: BootstrapUserByEmail :one
SELECT id, password_hash, status, must_change_password
FROM users
WHERE email = sqlc.arg(email)::text::citext AND status <> 'deleted'
FOR NO KEY UPDATE;

-- name: InsertBootstrapUser :one
INSERT INTO users (email, email_verified, display_name, password_hash, must_change_password, password_changed_at)
VALUES (sqlc.arg(email)::text::citext, true, sqlc.arg(display_name)::text, sqlc.arg(password_hash)::text, false,
        sqlc.arg(changed_at)::timestamptz)
RETURNING id;

-- name: SetBootstrapPassword :one
-- A new password for the bootstrap admin: the account becomes usable again (active, no legacy hash, no
-- must-change flag) and every token issued before is judged by the new auth_version.
UPDATE users
SET password_hash        = sqlc.arg(password_hash)::text,
    legacy_scrypt_hash   = NULL,
    legacy_scrypt_salt   = NULL,
    must_change_password = false,
    password_changed_at  = sqlc.arg(changed_at)::timestamptz,
    status               = 'active',
    disabled_at          = NULL,
    auth_version         = auth_version + 1
WHERE id = sqlc.arg(id)
RETURNING auth_version;

-- name: ReactivateBootstrapUser :one
-- The password is unchanged but the account was disabled, reset_required or flagged: make it usable.
UPDATE users
SET status = 'active', disabled_at = NULL, must_change_password = false, auth_version = auth_version + 1
WHERE id = sqlc.arg(id)
RETURNING auth_version;

-- name: RevokeAllSessions :many
UPDATE sessions
SET revoked_at = sqlc.arg(at)::timestamptz, revoked_reason = sqlc.arg(reason)::text
WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL
RETURNING id;

-- name: RevokeTokensOfSessions :exec
UPDATE refresh_tokens SET revoked_at = sqlc.arg(at)::timestamptz, revoked_reason = sqlc.arg(reason)::text
WHERE session_id = ANY (sqlc.arg(session_ids)::uuid[]) AND revoked_at IS NULL;

-- name: UsersByEmails :many
SELECT id, email::text AS email FROM users
WHERE email = ANY (sqlc.arg(emails)::text[]::citext[]) AND status <> 'deleted'
ORDER BY email;

-- name: PlatformAdminsOutside :many
-- platform_admin holders whose address is not in the list (Appendix C §C.5.9: no user holds platform_admin
-- unless listed in PLATFORM_ADMIN_EMAILS). Reported, never revoked: a later grant through the API is legitimate.
SELECT u.id, coalesce(u.email::text, '')::text AS email
FROM user_platform_roles r
JOIN users u ON u.id = r.user_id
WHERE r.role = 'platform_admin' AND (u.email IS NULL OR NOT (u.email = ANY (sqlc.arg(emails)::text[]::citext[])))
ORDER BY u.email, u.id;

-- name: BumpUserVersion :one
-- A claims change made by the bootstrap (platform_admin granted): tokens issued before are refreshed.
UPDATE users SET auth_version = auth_version + 1 WHERE id = sqlc.arg(id) RETURNING auth_version;
