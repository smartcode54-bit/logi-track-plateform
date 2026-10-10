-- Users, credentials and the principal's axes (Appendix C §C.1.3, §C.4.8, §C.5.4). Every statement runs
-- inside db.WithSystem: auth reads and writes identity rows before a principal exists (R12).

-- name: GetUserForLogin :one
-- Login lookup (C.5.4): deleted users do not exist for login; the partial unique index users_email
-- guarantees at most one row.
SELECT id, email, password_hash, legacy_scrypt_hash, legacy_scrypt_salt, status, must_change_password, auth_version
FROM users
WHERE email = sqlc.arg(email)::citext AND status <> 'deleted';

-- name: GetUser :one
SELECT id, email, email_verified, display_name, photo_file_id, password_hash, legacy_scrypt_hash, legacy_scrypt_salt,
       status, must_change_password, auth_version, password_changed_at
FROM users
WHERE id = sqlc.arg(id);

-- name: LockUser :one
-- Serialises logins, refreshes, password changes, tenant switches and revocations of one user. It is the
-- first lock of every auth transaction that writes sessions or refresh_tokens (lock order users ->
-- sessions -> refresh_tokens; RevokeInTx callers such as internal/iam follow it too). NO KEY UPDATE
-- conflicts with itself and with every UPDATE of users, but not with the KEY SHARE locks that foreign-key
-- checks of other tables take, so it serialises auth without blocking unrelated inserts.
SELECT id, email, password_hash, legacy_scrypt_hash, legacy_scrypt_salt, status, must_change_password, auth_version
FROM users
WHERE id = sqlc.arg(id)
FOR NO KEY UPDATE;

-- name: RehashPassword :exec
-- Transparent upgrade of a legacy Firebase-scrypt or weaker Argon2id hash (C.5.4): the plaintext is
-- unchanged, so password_changed_at and auth_version stay as they are.
UPDATE users
SET password_hash = sqlc.arg(password_hash)::text, legacy_scrypt_hash = NULL, legacy_scrypt_salt = NULL
WHERE id = sqlc.arg(id);

-- name: SetPassword :one
-- Every password set (change, reset, ticket change) ends the legacy hash, clears the must-change flag,
-- re-activates a reset_required user and bumps auth_version (C.4.7, C.4.8).
UPDATE users
SET password_hash        = sqlc.arg(password_hash)::text,
    legacy_scrypt_hash   = NULL,
    legacy_scrypt_salt   = NULL,
    must_change_password = false,
    password_changed_at  = sqlc.arg(changed_at)::timestamptz,
    status               = CASE WHEN status = 'reset_required' THEN 'active' ELSE status END,
    auth_version         = auth_version + 1
WHERE id = sqlc.arg(id)
RETURNING auth_version;

-- name: BumpAuthVersion :one
UPDATE users SET auth_version = auth_version + 1 WHERE id = sqlc.arg(id) RETURNING auth_version;

-- name: TouchLastLogin :exec
UPDATE users
SET last_login_at         = sqlc.arg(at)::timestamptz,
    last_login_lat        = sqlc.narg(lat),
    last_login_lng        = sqlc.narg(lng),
    last_login_geo_source = sqlc.narg(geo_source),
    last_login_accuracy_m = sqlc.narg(accuracy_m)
WHERE id = sqlc.arg(id);

-- name: UpdateProfile :exec
-- PATCH /v1/me: profile columns only (the column guard t_users_self_columns allows the same set).
UPDATE users
SET display_name  = coalesce(sqlc.narg(display_name), display_name),
    photo_file_id = coalesce(sqlc.narg(photo_file_id), photo_file_id)
WHERE id = sqlc.arg(id);

-- name: UpdateLastLoginGeo :exec
UPDATE users
SET last_login_lat        = sqlc.arg(lat),
    last_login_lng        = sqlc.arg(lng),
    last_login_geo_source = sqlc.arg(geo_source),
    last_login_accuracy_m = sqlc.narg(accuracy_m)
WHERE id = sqlc.arg(id);

-- name: ListMemberships :many
-- Active memberships in non-quarantine tenants, own fleet first (the default-tenant order).
SELECT m.tenant_id, m.role, t.kind, t.name_th, t.name_en, t.status AS tenant_status
FROM memberships m
JOIN tenants t ON t.id = m.tenant_id
WHERE m.user_id = sqlc.arg(user_id) AND m.status = 'active' AND t.kind <> 'quarantine'
ORDER BY (t.kind = 'own_fleet') DESC, t.name_th, t.id;

-- name: GetDriverForUser :one
-- The principal's DriverID comes from drivers.user_id, never from a uid search (C.1.4).
SELECT id, tenant_id, mobile FROM drivers WHERE user_id = sqlc.arg(user_id)::uuid;

-- name: ListPlatformRoles :many
SELECT role FROM user_platform_roles WHERE user_id = sqlc.arg(user_id) ORDER BY role;

-- name: ListScopes :many
-- Customer scopes and dispatcher grants (R86), with the party name for GET /v1/me.
SELECT s.kind, s.billing_party_id, coalesce(c.name, t.name_th, '')::text AS party_name
FROM user_scopes s
JOIN billing_parties bp ON bp.id = s.billing_party_id
LEFT JOIN customers c ON c.id = bp.customer_id
LEFT JOIN tenants t ON t.id = bp.tenant_id
WHERE s.user_id = sqlc.arg(user_id)
ORDER BY s.kind, s.created_at, s.billing_party_id;

-- name: LastActiveTenant :one
-- The tenant the user worked in most recently: the default tenant of a new login.
SELECT active_tenant_id FROM sessions
WHERE user_id = sqlc.arg(user_id) AND active_tenant_id IS NOT NULL
ORDER BY created_at DESC, id DESC
LIMIT 1;
