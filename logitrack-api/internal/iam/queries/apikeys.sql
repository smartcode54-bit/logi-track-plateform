-- API keys administration (T32, Appendix B §B.2.6, Appendix C §C.4.11): GET / POST / DELETE /v1/api-keys. api_keys
-- is platform-only under RLS, so every statement runs in db.WithSystem after Go authorization (security:
-- manage_api_keys and the reach of iam.APIKeys); key_hash is written once and never read here.

-- name: ListAPIKeys :many
-- Newest first. all_keys (X-Act-On-Tenant: *), or the platform keys (tenant_id NULL) for a platform admin, or
-- the keys of the listed tenants.
SELECT k.id, k.name, k.scope, k.key_prefix, k.tenant_id, k.capabilities, k.rate_per_min, k.created_by,
       u.email::text AS created_by_email, k.expires_at, k.last_used_at, k.revoked_at, k.revoked_by, k.created_at,
       t.name_th AS tenant_name_th, t.name_en AS tenant_name_en
FROM api_keys k
JOIN users u ON u.id = k.created_by
LEFT JOIN tenants t ON t.id = k.tenant_id
WHERE (sqlc.arg(all_keys)::boolean
       OR (sqlc.arg(platform_keys)::boolean AND k.tenant_id IS NULL)
       OR k.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (k.created_at, k.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY k.created_at DESC, k.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetAPIKey :one
SELECT k.id, k.name, k.scope, k.key_prefix, k.tenant_id, k.capabilities, k.rate_per_min, k.created_by,
       u.email::text AS created_by_email, k.expires_at, k.last_used_at, k.revoked_at, k.revoked_by, k.created_at,
       t.name_th AS tenant_name_th, t.name_en AS tenant_name_en
FROM api_keys k
JOIN users u ON u.id = k.created_by
LEFT JOIN tenants t ON t.id = k.tenant_id
WHERE k.id = sqlc.arg(id);

-- name: LockAPIKey :one
SELECT id, name, scope, key_prefix, tenant_id, revoked_at
FROM api_keys
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: KeyTenant :one
-- The tenant a new tenant key belongs to: it must exist, not be the quarantine row and be active.
SELECT kind, status FROM tenants WHERE id = sqlc.arg(id);

-- name: InsertAPIKey :one
INSERT INTO api_keys (name, scope, key_prefix, key_hash, tenant_id, capabilities, created_by, expires_at)
VALUES (sqlc.arg(name), sqlc.arg(scope), sqlc.arg(key_prefix), sqlc.arg(key_hash), sqlc.narg(tenant_id),
        sqlc.arg(capabilities)::text[], sqlc.arg(created_by), sqlc.narg(expires_at))
RETURNING id;

-- name: RevokeAPIKey :exec
UPDATE api_keys
SET revoked_at = sqlc.arg(revoked_at)::timestamptz, revoked_by = sqlc.arg(revoked_by)
WHERE id = sqlc.arg(id) AND revoked_at IS NULL;
