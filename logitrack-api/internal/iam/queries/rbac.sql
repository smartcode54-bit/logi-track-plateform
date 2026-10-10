-- Inputs of the per-request authorization (Appendix C §C.2.5, §C.3.4, §C.3.9). The identity tables are
-- platform-only under RLS, so the RBAC service reads them in db.WithSystem; nothing here is returned to a
-- caller as data.

-- name: RoleOverrides :many
-- The override rows of one role that apply in one tenant: the platform-wide rows (tenant_id NULL) and the
-- rows of that tenant.
SELECT tenant_id, capability, allowed
  FROM role_capability_overrides
 WHERE role = sqlc.arg(role) AND (tenant_id IS NULL OR tenant_id = sqlc.arg(tenant_id)::uuid)
 ORDER BY tenant_id NULLS FIRST, capability;

-- name: OwnFleetTenant :one
SELECT id FROM tenants WHERE kind = 'own_fleet';

-- name: Subtenants :many
-- Contractor reach (R60): the carrier tenants that work for tenant_id; one level, never transitive.
SELECT id FROM tenants WHERE contractor_tenant_id = sqlc.arg(tenant_id)::uuid ORDER BY id;

-- name: TenantKind :one
SELECT kind FROM tenants WHERE id = sqlc.arg(id)::uuid;
