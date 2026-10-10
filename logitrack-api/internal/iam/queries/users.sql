-- Users administration (T19, Appendix B §B.2.5, Appendix C §C.8): the identity tables are platform-only under
-- RLS, so iam reads and writes them in db.WithSystem after Go authorization. Every read names the principal's
-- reach explicitly (all_users, tenant_ids, scope_only), the same rule RLS p_staff_read applies to tenant staff,
-- widened for stewards to the accounts that belong to no tenant (customer scopes are global master data, R60).

-- name: LockAdminUser :one
-- The users row first (lock order users -> sessions -> refresh_tokens, Appendix C §C.4.4).
SELECT id, email, display_name, status, auth_version, legacy_auth_uid
FROM users
WHERE id = sqlc.arg(id)
FOR NO KEY UPDATE;

-- name: UserInReach :one
SELECT EXISTS (
  SELECT 1 FROM users u
  WHERE u.id = sqlc.arg(id)
    AND (sqlc.arg(all_users)::boolean
      OR EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
      OR (sqlc.arg(scope_only)::boolean AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id)
        AND NOT EXISTS (SELECT 1 FROM user_platform_roles r WHERE r.user_id = u.id)))
)::boolean AS in_reach;

-- name: TargetPrivileges :one
-- What the target holds beyond the caller's reach (Appendix C §C.8 "Reach": an admin route acts only on a user it
-- outranks): a platform role, every membership tenant and the tenants it administers (any status, unfiltered by
-- reach), and its scope kinds.
SELECT EXISTS (SELECT 1 FROM user_platform_roles r WHERE r.user_id = sqlc.arg(id)::uuid)::boolean AS platform_role,
       array(SELECT m.tenant_id FROM memberships m WHERE m.user_id = sqlc.arg(id)::uuid ORDER BY m.tenant_id)::uuid[] AS tenant_ids,
       array(SELECT m.tenant_id FROM memberships m WHERE m.user_id = sqlc.arg(id)::uuid AND m.role = 'tenant_admin'
             ORDER BY m.tenant_id)::uuid[] AS admin_tenant_ids,
       EXISTS (SELECT 1 FROM user_scopes s WHERE s.user_id = sqlc.arg(id)::uuid AND s.kind = 'dispatcher')::boolean AS dispatcher,
       EXISTS (SELECT 1 FROM user_scopes s WHERE s.user_id = sqlc.arg(id)::uuid AND s.kind = 'customer')::boolean AS customer;

-- name: ActiveTenantMembers :many
-- The active members of a tenant whose claims a status change alters (PATCH /v1/tenants/{id} suspend or reactivate),
-- in user id order (the users rows are locked in that order).
SELECT user_id FROM memberships WHERE tenant_id = sqlc.arg(tenant_id) AND status = 'active' ORDER BY user_id;

-- name: GetAdminUser :one
SELECT u.id, u.email, u.display_name, u.photo_file_id, u.status, u.must_change_password, u.last_login_at,
       u.created_at, u.legacy_auth_uid
FROM users u
WHERE u.id = sqlc.arg(id)
  AND (sqlc.arg(all_users)::boolean
    OR EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
    OR (sqlc.arg(scope_only)::boolean AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id)
        AND NOT EXISTS (SELECT 1 FROM user_platform_roles r WHERE r.user_id = u.id)));

-- name: ListUsersByCreated :many
-- GET /v1/users, default order created_at DESC, id DESC (Appendix B §B.1.5). q matches email or display name
-- (the caller escapes LIKE wildcards); member_role, scope_kind and platform_role filter by the role axis.
SELECT u.id, u.email, u.display_name, u.photo_file_id, u.status, u.must_change_password, u.last_login_at,
       u.created_at, u.legacy_auth_uid
FROM users u
WHERE (sqlc.arg(all_users)::boolean
    OR EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
    OR (sqlc.arg(scope_only)::boolean AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id)
        AND NOT EXISTS (SELECT 1 FROM user_platform_roles r WHERE r.user_id = u.id)))
  AND (sqlc.narg(q)::text IS NULL OR u.email::text ILIKE sqlc.narg(q) ESCAPE '\' OR u.display_name ILIKE sqlc.narg(q) ESCAPE '\')
  AND ((sqlc.narg(status)::text IS NULL AND u.status <> 'deleted') OR u.status = sqlc.narg(status))
  AND (sqlc.narg(member_role)::text IS NULL OR EXISTS (
        SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.role = sqlc.narg(member_role)
          AND (sqlc.arg(all_users) OR m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))))
  AND (sqlc.narg(scope_kind)::text IS NULL OR EXISTS (
        SELECT 1 FROM user_scopes s WHERE s.user_id = u.id AND s.kind = sqlc.narg(scope_kind)))
  AND (sqlc.narg(platform_role)::text IS NULL OR EXISTS (
        SELECT 1 FROM user_platform_roles r WHERE r.user_id = u.id AND r.role = sqlc.narg(platform_role)))
  AND (sqlc.narg(after_created)::timestamptz IS NULL
    OR (u.created_at, u.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY u.created_at DESC, u.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListUsersByLastLogin :many
-- GET /v1/users?sort=last_login_at: last_login_at DESC NULLS LAST, id DESC (index users_last_login); a cursor
-- on a never-signed-in user carries a NULL time.
SELECT u.id, u.email, u.display_name, u.photo_file_id, u.status, u.must_change_password, u.last_login_at,
       u.created_at, u.legacy_auth_uid
FROM users u
WHERE (sqlc.arg(all_users)::boolean
    OR EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
    OR (sqlc.arg(scope_only)::boolean AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id)
        AND NOT EXISTS (SELECT 1 FROM user_platform_roles r WHERE r.user_id = u.id)))
  AND (sqlc.narg(q)::text IS NULL OR u.email::text ILIKE sqlc.narg(q) ESCAPE '\' OR u.display_name ILIKE sqlc.narg(q) ESCAPE '\')
  AND ((sqlc.narg(status)::text IS NULL AND u.status <> 'deleted') OR u.status = sqlc.narg(status))
  AND (sqlc.narg(member_role)::text IS NULL OR EXISTS (
        SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.role = sqlc.narg(member_role)
          AND (sqlc.arg(all_users) OR m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))))
  AND (sqlc.narg(scope_kind)::text IS NULL OR EXISTS (
        SELECT 1 FROM user_scopes s WHERE s.user_id = u.id AND s.kind = sqlc.narg(scope_kind)))
  AND (sqlc.narg(platform_role)::text IS NULL OR EXISTS (
        SELECT 1 FROM user_platform_roles r WHERE r.user_id = u.id AND r.role = sqlc.narg(platform_role)))
  AND (NOT sqlc.arg(has_cursor)::boolean
    OR (sqlc.narg(after_login)::timestamptz IS NOT NULL
        AND (u.last_login_at < sqlc.narg(after_login)::timestamptz
          OR (u.last_login_at = sqlc.narg(after_login)::timestamptz AND u.id < sqlc.arg(after_id)::uuid)
          OR u.last_login_at IS NULL))
    OR (sqlc.narg(after_login)::timestamptz IS NULL AND u.last_login_at IS NULL AND u.id < sqlc.arg(after_id)::uuid))
ORDER BY u.last_login_at DESC NULLS LAST, u.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListUserMemberships :many
-- Memberships of the listed users within the reach (every membership for all_users), own fleet first.
SELECT m.user_id, m.tenant_id, t.name_th, t.name_en, t.kind, m.role, m.status
FROM memberships m
JOIN tenants t ON t.id = m.tenant_id
WHERE m.user_id = ANY (sqlc.arg(user_ids)::uuid[])
  AND (sqlc.arg(all_users)::boolean OR m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
ORDER BY m.user_id, (t.kind = 'own_fleet') DESC, t.name_th, m.tenant_id;

-- name: ListUserScopes :many
SELECT s.user_id, s.kind, s.billing_party_id, coalesce(c.name, t.name_th, '')::text AS party_name
FROM user_scopes s
JOIN billing_parties bp ON bp.id = s.billing_party_id
LEFT JOIN customers c ON c.id = bp.customer_id
LEFT JOIN tenants t ON t.id = bp.tenant_id
WHERE s.user_id = ANY (sqlc.arg(user_ids)::uuid[])
ORDER BY s.user_id, s.kind, s.created_at, s.billing_party_id;

-- name: ListUserPlatformRoles :many
SELECT user_id, role FROM user_platform_roles WHERE user_id = ANY (sqlc.arg(user_ids)::uuid[]) ORDER BY user_id, role;

-- name: ListUserDrivers :many
-- The driver row linked to each listed user, when its tenant is in reach.
SELECT d.user_id::uuid AS user_id, d.id, d.tenant_id,
       coalesce(nullif(btrim(d.full_name_th), ''), btrim(d.first_name || ' ' || d.last_name))::text AS display_name
FROM drivers d
WHERE d.user_id = ANY (sqlc.arg(user_ids)::uuid[])
  AND (sqlc.arg(all_users)::boolean OR d.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]));

-- name: InsertAdminUser :one
-- POST /v1/users: a temporary password (must change at the first sign-in, R29, R79) or none (an invite).
INSERT INTO users (email, email_verified, display_name, password_hash, must_change_password, password_changed_at)
VALUES (sqlc.arg(email)::text, false, sqlc.arg(display_name)::text, sqlc.narg(password_hash)::text,
        sqlc.arg(must_change_password)::boolean, sqlc.narg(password_changed_at)::timestamptz)
RETURNING id;

-- name: UpdateAdminUser :exec
UPDATE users
SET display_name = CASE WHEN sqlc.arg(set_display_name)::boolean THEN sqlc.narg(display_name)::text ELSE display_name END,
    email        = CASE WHEN sqlc.arg(set_email)::boolean THEN sqlc.narg(email)::text::citext ELSE email END,
    email_verified = CASE WHEN sqlc.arg(set_email)::boolean THEN false ELSE email_verified END
WHERE id = sqlc.arg(id);

-- name: GetMembership :one
SELECT role, status FROM memberships WHERE user_id = sqlc.arg(user_id) AND tenant_id = sqlc.arg(tenant_id);

-- name: UpsertMembership :exec
INSERT INTO memberships (user_id, tenant_id, role, status, created_by)
VALUES (sqlc.arg(user_id), sqlc.arg(tenant_id), sqlc.arg(role), 'active', sqlc.narg(created_by))
ON CONFLICT (user_id, tenant_id) DO UPDATE SET role = EXCLUDED.role, status = 'active';

-- name: DeleteMembership :one
DELETE FROM memberships WHERE user_id = sqlc.arg(user_id) AND tenant_id = sqlc.arg(tenant_id) RETURNING role;

-- name: ListTenantMembers :many
-- GET /v1/tenants/{id}/members: members newest first (membership created_at DESC, user id DESC).
SELECT m.user_id, u.email, u.display_name, m.role, m.status, u.last_login_at, m.created_at
FROM memberships m
JOIN users u ON u.id = m.user_id
WHERE m.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(role)::text IS NULL OR m.role = sqlc.narg(role))
  AND (sqlc.narg(after_created)::timestamptz IS NULL
    OR (m.created_at, m.user_id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY m.created_at DESC, m.user_id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetLinkDriver :one
SELECT id, tenant_id, user_id FROM drivers WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: DriverOfUser :one
SELECT id, tenant_id FROM drivers WHERE user_id = sqlc.arg(user_id)::uuid FOR UPDATE;

-- name: SetDriverUser :exec
UPDATE drivers SET user_id = sqlc.narg(user_id) WHERE id = sqlc.arg(id);

-- name: UserIsDriver :one
-- A driver account: a driver membership or a linked drivers row (POST /v1/users/{id}/password/temporary then
-- also needs drivers:set_password).
SELECT (EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = sqlc.arg(uid)::uuid AND m.role = 'driver')
     OR EXISTS (SELECT 1 FROM drivers d WHERE d.user_id = sqlc.arg(uid)::uuid))::boolean AS is_driver;

-- name: UserHasMembership :one
SELECT EXISTS (SELECT 1 FROM memberships WHERE user_id = sqlc.arg(user_id))::boolean AS has_membership;

-- name: ListScopeParties :many
SELECT billing_party_id FROM user_scopes WHERE user_id = sqlc.arg(user_id) AND kind = sqlc.arg(kind)
ORDER BY created_at, billing_party_id;

-- name: CountOtherScopes :one
SELECT count(*)::int FROM user_scopes WHERE user_id = sqlc.arg(user_id) AND kind <> sqlc.arg(kind);

-- name: DeleteScopesNotIn :exec
DELETE FROM user_scopes
WHERE user_id = sqlc.arg(user_id) AND kind = sqlc.arg(kind)
  AND NOT (billing_party_id = ANY (sqlc.arg(keep)::uuid[]));

-- name: InsertScope :exec
INSERT INTO user_scopes (user_id, kind, billing_party_id, created_by)
VALUES (sqlc.arg(user_id), sqlc.arg(kind), sqlc.arg(billing_party_id), sqlc.narg(created_by))
ON CONFLICT ON CONSTRAINT user_scopes_key DO NOTHING;

-- name: ExistingParties :many
SELECT id FROM billing_parties WHERE id = ANY (sqlc.arg(ids)::uuid[]);

-- name: InsertPlatformRole :execrows
INSERT INTO user_platform_roles (user_id, role, granted_by)
VALUES (sqlc.arg(user_id), sqlc.arg(role), sqlc.narg(granted_by))
ON CONFLICT (user_id, role) DO NOTHING;

-- name: DeletePlatformRole :execrows
DELETE FROM user_platform_roles WHERE user_id = sqlc.arg(user_id) AND role = sqlc.arg(role);

-- name: ListAdminSessions :many
-- GET /v1/users/{id}/sessions: the live sessions of the user, most recently used first.
SELECT id, platform, amr, install_id, device_label, app_version, coalesce(host(ip), '')::text AS ip, user_agent,
       created_at, last_seen_at
FROM sessions
WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL AND absolute_expires_at > sqlc.arg(now)::timestamptz
ORDER BY last_seen_at DESC, id DESC;

-- name: TenantForMembership :one
-- FOR KEY SHARE: a membership write takes the tenant before the user (the order of PATCH /v1/tenants/{id}, which
-- locks the tenant and then bumps its members), and the membership's foreign key needs this lock anyway.
SELECT kind, status FROM tenants WHERE id = sqlc.arg(id) FOR KEY SHARE;

-- name: TenantExists :one
-- The read of GET /v1/tenants/{id}/members (no lock).
SELECT EXISTS (SELECT 1 FROM tenants WHERE id = sqlc.arg(id))::boolean AS found;
