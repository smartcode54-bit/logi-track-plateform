-- Firebase bridge (T08, Appendix C §C.6): the Firebase side of a user, the facts behind the legacy claim
-- object (C.6.3) and the account-status writes the mirror follows (C.6.4). Every statement runs inside
-- db.WithSystem, like the rest of auth (R12).

-- name: GetBridgeUser :one
-- The user's Firebase uid (users.legacy_auth_uid, kept forever, R55) and whether the account was imported
-- from Firebase Auth: cmd/etl auth-import writes an auth_identities row of provider firebase_legacy for
-- every exported account (main spec §4.11), and nothing else does, so a user created in Go is never
-- "imported" even after the bridge gives it a Firebase uid.
SELECT u.id, u.legacy_auth_uid, u.email, u.email_verified, u.display_name, u.status, u.must_change_password,
       u.password_changed_at, u.auth_version,
       EXISTS (SELECT 1 FROM auth_identities i WHERE i.user_id = u.id AND i.provider = 'firebase_legacy') AS imported
FROM users u
WHERE u.id = sqlc.arg(id);

-- name: GetUserIDByLegacyUID :one
-- Firebase ID token -> user (index users_legacy_uid). Deleted users keep their uid but do not exist here.
SELECT id FROM users WHERE legacy_auth_uid = sqlc.arg(legacy_auth_uid)::text AND status <> 'deleted';

-- name: AssignLegacyAuthUID :one
-- The first custom token of a user created in Go (C.6.3), or its first mirrored account (C.6.4): the
-- Firebase uid becomes users.id::text. A uid that is already set is kept.
UPDATE users SET legacy_auth_uid = id::text
WHERE id = sqlc.arg(id) AND legacy_auth_uid IS NULL
RETURNING legacy_auth_uid::text;

-- name: GetTenantLegacy :one
-- Kind and subcontractors doc id of a tenant: a carrier tenant's legacy_doc_id is the legacy
-- partnerScopeId value.
SELECT kind, legacy_doc_id FROM tenants WHERE id = sqlc.arg(id);

-- name: OldestCustomerScopeLegacyID :one
-- customerScopeId of a customer-scope user (C.6.3): the legacy doc id of its oldest customer scope that
-- has one. C.5.5 resolved the legacy value through customers.legacy_doc_id, else tenants.legacy_doc_id,
-- so the same two columns give it back.
SELECT coalesce(c.legacy_doc_id, t.legacy_doc_id)::text AS legacy_doc_id
FROM user_scopes s
JOIN billing_parties bp ON bp.id = s.billing_party_id
LEFT JOIN customers c ON c.id = bp.customer_id
LEFT JOIN tenants t ON t.id = bp.tenant_id
WHERE s.user_id = sqlc.arg(user_id) AND s.kind = 'customer'
  AND coalesce(c.legacy_doc_id, t.legacy_doc_id) IS NOT NULL
ORDER BY s.created_at, s.billing_party_id
LIMIT 1;

-- name: GetDriverLegacyRef :one
-- driverId claim of a driver (C.6.3): the Firestore drivers doc id, or the uuid of a driver created in Go
-- (its write-back doc id, R25).
SELECT coalesce(legacy_doc_id, id::text)::text AS ref FROM drivers WHERE user_id = sqlc.arg(user_id)::uuid;

-- name: SetUserDisabled :exec
-- Disable (C.4.7): status disabled from active or reset_required.
UPDATE users SET status = 'disabled', disabled_at = sqlc.arg(at)::timestamptz WHERE id = sqlc.arg(id);

-- name: SetUserEnabled :exec
-- Enable: a disabled user becomes active again.
UPDATE users SET status = 'active', disabled_at = NULL WHERE id = sqlc.arg(id) AND status = 'disabled';

-- name: SetTemporaryPassword :one
-- An admin-issued temporary password (R29, R79): like SetPassword, but the user must change it at the
-- next sign-in.
UPDATE users
SET password_hash        = sqlc.arg(password_hash)::text,
    legacy_scrypt_hash   = NULL,
    legacy_scrypt_salt   = NULL,
    must_change_password = true,
    password_changed_at  = sqlc.arg(changed_at)::timestamptz,
    status               = CASE WHEN status = 'reset_required' THEN 'active' ELSE status END,
    auth_version         = auth_version + 1
WHERE id = sqlc.arg(id)
RETURNING auth_version;
