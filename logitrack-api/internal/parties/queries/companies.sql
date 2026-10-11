-- Companies: the invoice issuers of a tenant (Appendix B §B.2.10, Appendix C §C.1 "Company"). Tenant-stamped
-- (tenant_source 'self'); RLS G-tenant decides: staff read and write the companies of their tenant reach (own tenant
-- and the carriers working for it), never the quarantine tenant's. withholding_tax_rate is NUMERIC(5,2), a percent
-- (R18: statements snapshot it as a fraction); it crosses the boundary as float8, Go keeps at most two decimals.
-- The reads name the caller's reach (main spec §4.7, Appendix C §C.3.8; authz.Principal.TenantReach): all_tenants
-- under X-Act-On-Tenant: *, else tenant_ids = the effective tenant of a staff principal and its contractor reach; RLS
-- then re-checks every row.

-- name: ListCompanies :many
-- GET /v1/companies, newest first.
SELECT id, tenant_id, name_th, name_en, short_name, tax_id, branch_type, branch_number, address, phone, email,
       logo_file_id, stamp_file_id, signature_file_id, signatory_name, bank_name, account_number, account_name,
       withholding_tax_rate::float8 AS withholding_tax_rate, company_type, is_active, max_trucks, max_drivers,
       legacy_doc_id, created_at, updated_at
FROM companies
WHERE (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(company_type)::text IS NULL OR company_type = sqlc.narg(company_type))
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(name_th), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(coalesce(name_en, '')), lower(sqlc.narg(q)::text)) > 0
       OR strpos(lower(coalesce(short_name, '')), lower(sqlc.narg(q)::text)) > 0
       OR tax_id = sqlc.narg(q)::text)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetOwnerCompany :one
-- GET /v1/companies/owner: the one company of type owner (companies_owner_one), when the caller's reach holds it.
SELECT id, tenant_id, name_th, name_en, short_name, tax_id, branch_type, branch_number, address, phone, email,
       logo_file_id, stamp_file_id, signature_file_id, signatory_name, bank_name, account_number, account_name,
       withholding_tax_rate::float8 AS withholding_tax_rate, company_type, is_active, max_trucks, max_drivers,
       legacy_doc_id, created_at, updated_at
FROM companies
WHERE (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND company_type = 'owner'
ORDER BY created_at, id
LIMIT 1;

-- name: GetCompany :one
SELECT id, tenant_id, name_th, name_en, short_name, tax_id, branch_type, branch_number, address, phone, email,
       logo_file_id, stamp_file_id, signature_file_id, signatory_name, bank_name, account_number, account_name,
       withholding_tax_rate::float8 AS withholding_tax_rate, company_type, is_active, max_trucks, max_drivers,
       legacy_doc_id, created_at, updated_at
FROM companies
WHERE id = sqlc.arg(id);

-- name: LockCompany :one
SELECT id, tenant_id, name_th, name_en, short_name, tax_id, branch_type, branch_number, address, phone, email,
       logo_file_id, stamp_file_id, signature_file_id, signatory_name, bank_name, account_number, account_name,
       withholding_tax_rate::float8 AS withholding_tax_rate, company_type, is_active, max_trucks, max_drivers,
       legacy_doc_id, created_at, updated_at
FROM companies
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: InsertCompany :one
-- tenant_id is the request's effective tenant (tenant_source 'self', Appendix C §C.1.3), never a body field.
INSERT INTO companies (tenant_id, tenant_source, name_th, name_en, short_name, tax_id, branch_type, branch_number,
                       address, phone, email, signatory_name, bank_name, account_number, account_name,
                       withholding_tax_rate, company_type, is_active, max_trucks, max_drivers)
VALUES (sqlc.arg(tenant_id), 'self', sqlc.arg(name_th), sqlc.narg(name_en), sqlc.narg(short_name), sqlc.arg(tax_id),
        sqlc.arg(branch_type), sqlc.narg(branch_number), sqlc.arg(address), sqlc.narg(phone),
        sqlc.narg(email)::text::citext, sqlc.narg(signatory_name), sqlc.narg(bank_name), sqlc.narg(account_number),
        sqlc.narg(account_name), sqlc.arg(withholding_tax_rate)::float8::numeric(5,2), sqlc.arg(company_type),
        sqlc.arg(is_active), sqlc.narg(max_trucks)::int, sqlc.narg(max_drivers)::int)
RETURNING id;

-- name: UpdateCompany :exec
-- PATCH writes the merged row (Go merges the request into the locked row); tenant_id never changes (frozen).
UPDATE companies
SET name_th = sqlc.arg(name_th), name_en = sqlc.narg(name_en), short_name = sqlc.narg(short_name),
    tax_id = sqlc.arg(tax_id), branch_type = sqlc.arg(branch_type), branch_number = sqlc.narg(branch_number),
    address = sqlc.arg(address), phone = sqlc.narg(phone), email = sqlc.narg(email)::text::citext,
    logo_file_id = sqlc.narg(logo_file_id)::uuid, stamp_file_id = sqlc.narg(stamp_file_id)::uuid,
    signature_file_id = sqlc.narg(signature_file_id)::uuid, signatory_name = sqlc.narg(signatory_name),
    bank_name = sqlc.narg(bank_name), account_number = sqlc.narg(account_number),
    account_name = sqlc.narg(account_name), withholding_tax_rate = sqlc.arg(withholding_tax_rate)::float8::numeric(5,2),
    company_type = sqlc.arg(company_type), is_active = sqlc.arg(is_active), max_trucks = sqlc.narg(max_trucks)::int,
    max_drivers = sqlc.narg(max_drivers)::int
WHERE id = sqlc.arg(id);

-- name: SetCompanyFiles :exec
-- POST: the assets are committed to the new row's id, then linked.
UPDATE companies
SET logo_file_id = sqlc.narg(logo_file_id)::uuid, stamp_file_id = sqlc.narg(stamp_file_id)::uuid,
    signature_file_id = sqlc.narg(signature_file_id)::uuid
WHERE id = sqlc.arg(id);

-- name: TenantKind :one
-- The kind of a company's tenant (an owner company belongs to the own fleet); tenants p_read: the caller's reach.
SELECT kind FROM tenants WHERE id = sqlc.arg(id);
