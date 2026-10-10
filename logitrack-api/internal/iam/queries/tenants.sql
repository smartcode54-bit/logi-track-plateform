-- Tenants administration (T19 owner addition, Appendix B §B.2.4, Appendix C §C.8): carrier tenants created
-- and edited by platform admins, with their billing_parties row and status history. Run in db.WithSystem
-- after Go authorization (the structural columns are platform-only, trigger t_tenant_admin_columns).

-- name: ListTenants :many
-- GET /v1/tenants, newest first; carriers_only for the steward view of fleet:manage_subcontractors.
SELECT id, kind, code, name_th, name_en, status, legal_type, contractor_tenant_id, created_at
FROM tenants
WHERE (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind))
  AND (NOT sqlc.arg(carriers_only)::boolean OR kind = 'carrier')
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(after_created)::timestamptz IS NULL
    OR (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetTenant :one
SELECT t.id, t.kind, t.code, t.name_th, t.name_en, t.legal_type, t.id_card_number, t.tax_id,
       t.contact_person, t.phone, t.email, t.website, t.address, t.designation, t.fleet_size,
       t.dispatch_center, t.service_regions, t.vehicle_types, t.line_group_id, t.contractor_tenant_id, t.status,
       t.created_at, t.updated_at, bp.id AS billing_party_id, bp.billing_date_basis
FROM tenants t
LEFT JOIN billing_parties bp ON bp.tenant_id = t.id
WHERE t.id = sqlc.arg(id);

-- name: LockTenant :one
SELECT id, kind, code, name_th, name_en, status, legal_type, contractor_tenant_id, created_at
FROM tenants WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: TenantStatusHistory :many
SELECT status, previous_status, changed_at, changed_by_user_id, changed_by_label, reason
FROM status_history
WHERE entity_type = 'tenant' AND entity_id = sqlc.arg(id)
ORDER BY changed_at DESC, id DESC
LIMIT 50;

-- name: InsertTenant :one
INSERT INTO tenants (kind, code, name_th, name_en, legal_type, id_card_number, tax_id, contact_person, phone, email,
                     website, address, designation, fleet_size, dispatch_center, service_regions, vehicle_types,
                     line_group_id, contractor_tenant_id, status)
VALUES ('carrier', sqlc.narg(code)::text::citext, sqlc.arg(name_th), sqlc.narg(name_en), sqlc.arg(legal_type),
        sqlc.narg(id_card_number), sqlc.narg(tax_id), sqlc.narg(contact_person), sqlc.narg(phone),
        sqlc.narg(email)::text::citext, sqlc.narg(website), sqlc.narg(address), sqlc.narg(designation),
        sqlc.arg(fleet_size), sqlc.narg(dispatch_center), sqlc.arg(service_regions)::text[],
        sqlc.arg(vehicle_types)::text[], sqlc.narg(line_group_id), sqlc.narg(contractor_tenant_id), sqlc.arg(status))
RETURNING id, kind, code, name_th, name_en, status, legal_type, contractor_tenant_id, created_at;

-- name: InsertTenantParty :one
-- The tenant's billing party (R6): every carrier is billable as a party of kind 'tenant'.
INSERT INTO billing_parties (kind, tenant_id) VALUES ('tenant', sqlc.arg(tenant_id)) RETURNING id;

-- name: UpdateTenant :one
UPDATE tenants
SET code    = CASE WHEN sqlc.arg(set_code)::boolean THEN sqlc.narg(code)::text::citext ELSE code END,
    name_th = coalesce(sqlc.narg(name_th)::text, name_th),
    name_en = CASE WHEN sqlc.arg(set_name_en)::boolean THEN sqlc.narg(name_en)::text ELSE name_en END,
    status  = coalesce(sqlc.narg(status)::text, status),
    contractor_tenant_id = CASE WHEN sqlc.arg(set_contractor)::boolean THEN sqlc.narg(contractor_tenant_id)::uuid
                                ELSE contractor_tenant_id END
WHERE id = sqlc.arg(id)
RETURNING id, kind, code, name_th, name_en, status, legal_type, contractor_tenant_id, created_at;

-- name: InsertTenantStatusHistory :exec
INSERT INTO status_history (entity_type, entity_id, status, previous_status, changed_at, changed_by_user_id, reason)
VALUES ('tenant', sqlc.arg(entity_id), sqlc.arg(status), sqlc.narg(previous_status), sqlc.arg(changed_at)::timestamptz,
        sqlc.narg(changed_by_user_id), sqlc.narg(reason));

-- name: IsActiveMember :one
SELECT EXISTS (SELECT 1 FROM memberships WHERE user_id = sqlc.arg(user_id) AND tenant_id = sqlc.arg(tenant_id)
               AND status = 'active')::boolean AS member;
