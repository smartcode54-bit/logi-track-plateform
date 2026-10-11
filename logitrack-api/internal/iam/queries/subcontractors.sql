-- Carrier profiles: /v1/subcontractors* (T20, Appendix B §B.2.10), a view of tenants(kind='carrier') and their
-- billing_parties row (no subcontractors table, R6). ListCarrierProfiles runs in db.WithPrincipal (tenants p_read:
-- own reach, every carrier for a steward); the writes run in db.WithSystem after Go authorization (a steward edits
-- another tenant's profile, which no RLS policy grants) and touch the profile columns only: kind, status, code,
-- legacy_doc_id and contractor_tenant_id stay on PATCH /v1/tenants/{id} (trigger t_tenant_admin_columns).

-- name: ListCarrierProfiles :many
-- GET /v1/subcontractors, ordered by Thai name (pickers); the keyset is (name_th, id).
SELECT t.id, t.code, t.name_th, t.name_en, t.legal_type, t.status, t.contact_person, t.phone, t.email, t.fleet_size,
       t.line_group_id, t.contractor_tenant_id, t.created_at, bp.id AS billing_party_id, bp.billing_date_basis
FROM tenants t
LEFT JOIN billing_parties bp ON bp.tenant_id = t.id
WHERE t.kind = 'carrier'
  AND (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status))
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(t.name_th), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(coalesce(t.name_en, '')), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(coalesce(t.code::text, '')), lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(after_name)::text IS NULL OR (t.name_th, t.id) > (sqlc.narg(after_name)::text, sqlc.arg(after_id)::uuid))
ORDER BY t.name_th, t.id
LIMIT sqlc.arg(row_limit);

-- name: LockCarrierProfile :one
SELECT t.id, t.kind, t.code, t.name_th, t.name_en, t.legal_type, t.id_card_number, t.tax_id, t.contact_person,
       t.phone, t.email, t.website, t.address, t.designation, t.fleet_size, t.dispatch_center, t.service_regions,
       t.vehicle_types, t.line_group_id, t.contractor_tenant_id, t.status, t.created_at,
       bp.id AS billing_party_id, bp.billing_date_basis
FROM tenants t
LEFT JOIN billing_parties bp ON bp.tenant_id = t.id
WHERE t.id = sqlc.arg(id)
FOR UPDATE OF t;

-- name: UpdateCarrierProfile :exec
-- PATCH /v1/subcontractors/{id}: the merged profile (Go merges the request into the locked row).
UPDATE tenants
SET name_th = sqlc.arg(name_th), name_en = sqlc.narg(name_en), legal_type = sqlc.arg(legal_type),
    id_card_number = sqlc.narg(id_card_number), tax_id = sqlc.narg(tax_id), contact_person = sqlc.narg(contact_person),
    phone = sqlc.narg(phone), email = sqlc.narg(email)::text::citext, website = sqlc.narg(website),
    address = sqlc.narg(address), designation = sqlc.narg(designation), fleet_size = sqlc.arg(fleet_size),
    dispatch_center = sqlc.narg(dispatch_center), service_regions = sqlc.arg(service_regions)::text[],
    vehicle_types = sqlc.arg(vehicle_types)::text[], line_group_id = sqlc.narg(line_group_id)
WHERE id = sqlc.arg(id) AND kind = 'carrier';

-- name: SetTenantPartyBasis :one
-- The carrier's billing party carries billing_date_basis (R6, ADR 0027); created when a legacy carrier lacks it.
INSERT INTO billing_parties (kind, tenant_id, billing_date_basis)
VALUES ('tenant', sqlc.arg(tenant_id), sqlc.arg(billing_date_basis))
ON CONFLICT (tenant_id) WHERE tenant_id IS NOT NULL DO UPDATE SET billing_date_basis = EXCLUDED.billing_date_basis
RETURNING id;

-- name: OwnFleetTenantID :one
SELECT id FROM tenants WHERE kind = 'own_fleet';

-- name: TenantDocuments :many
-- subcontractors.documents[] (tenant_files): the files of the carrier profile, in their order, with their keys.
SELECT tf.file_id, tf.kind, tf.position, fo.object_key
FROM tenant_files tf
JOIN file_objects fo ON fo.id = tf.file_id
WHERE tf.tenant_id = sqlc.arg(tenant_id)
ORDER BY tf.position, tf.created_at, tf.file_id;

-- name: DeleteTenantDocumentsExcept :execrows
-- A documents list replaces the set: a dropped file keeps its committed object (never deleted, main spec §9.3).
DELETE FROM tenant_files
WHERE tenant_id = sqlc.arg(tenant_id) AND NOT (file_id = ANY (sqlc.arg(keep)::uuid[]));

-- name: UpsertTenantDocument :exec
INSERT INTO tenant_files (tenant_id, file_id, kind, position)
VALUES (sqlc.arg(tenant_id), sqlc.arg(file_id), sqlc.arg(kind), sqlc.arg(position))
ON CONFLICT (tenant_id, file_id) DO UPDATE SET kind = EXCLUDED.kind, position = EXCLUDED.position;
