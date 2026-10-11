-- Role matrix (T51, Appendix B §B.2.6, Appendix C §C.2.5): the override rows of one layer of
-- role_capability_overrides, a tenant (tenant_id = the tenant) or the platform-wide layer (tenant_id NULL). The
-- table is platform-only under RLS, so iam reads and writes it in db.WithSystem after Go authorization.

-- name: LockOverrideLayer :exec
-- Serialises the saves of one layer (PUT /v1/roles/matrix, etl auth-rbac): the diff of role_matrix_saved is taken
-- under it, so two concurrent saves never record the same "before".
SELECT pg_advisory_xact_lock(hashtext('lt-rco:' || coalesce(sqlc.narg(tenant_id)::uuid::text, 'platform')));

-- name: LayerOverrides :many
SELECT role, capability, allowed
FROM role_capability_overrides
WHERE tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::uuid
ORDER BY role, capability;

-- name: UpsertOverride :exec
INSERT INTO role_capability_overrides (tenant_id, role, capability, allowed, updated_by)
VALUES (sqlc.narg(tenant_id)::uuid, sqlc.arg(role), sqlc.arg(capability), sqlc.arg(allowed), sqlc.arg(updated_by))
ON CONFLICT ON CONSTRAINT rco_key DO UPDATE
SET allowed = EXCLUDED.allowed, updated_by = EXCLUDED.updated_by
WHERE role_capability_overrides.allowed IS DISTINCT FROM EXCLUDED.allowed;

-- name: DeleteOverride :exec
DELETE FROM role_capability_overrides
WHERE tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::uuid
  AND role = sqlc.arg(role) AND capability = sqlc.arg(capability);

-- name: MatrixTenant :one
-- The tenant of a tenant layer: its kind decides the global keys (overridable only in the own fleet, R60).
SELECT kind, status FROM tenants WHERE id = sqlc.arg(id);

-- name: ConfigTenants :many
-- Every tenant whose members a platform-wide save concerns: SSE roles.changed goes to each tenant:{tid}:config.
SELECT id FROM tenants WHERE kind <> 'quarantine' ORDER BY id;
