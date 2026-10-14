-- Customers and their billing parties (Appendix B §B.2.10, R6): global master data, read by every authenticated
-- principal and written by stewards only (RLS G-global, R60; a customer-scope principal reads only its own parties,
-- 0012). Every statement runs in db.WithPrincipal: RLS decides, and the repository still names the scope reach.

-- name: ListCustomers :many
-- GET /v1/customers, ordered by code (unique case-insensitively, so the keyset (code, id) is total). party_ids
-- narrows a customer-scope principal to its own parties (RLS narrows it too, 0012). q matches the code and the name
-- folded (search_fold, 0026: q_words are its folded words as LIKE patterns, each somewhere in the row, in any order;
-- NULL is no search).
SELECT sqlc.embed(c), bp.id AS billing_party_id, bp.billing_date_basis
FROM customers c
LEFT JOIN billing_parties bp ON bp.customer_id = c.id
WHERE (sqlc.narg(q_words)::text[] IS NULL
       OR search_fold(c.code::text || ' ' || coalesce(c.name, '')) LIKE ALL (sqlc.narg(q_words)::text[]))
  AND (NOT sqlc.arg(scoped)::boolean OR bp.id = ANY (sqlc.arg(party_ids)::uuid[]))
  AND (sqlc.narg(after_code)::text IS NULL
       OR (c.code, c.id) > (sqlc.narg(after_code)::text::citext, sqlc.arg(after_id)::uuid))
ORDER BY c.code, c.id
LIMIT sqlc.arg(row_limit);

-- name: GetCustomer :one
SELECT sqlc.embed(c), bp.id AS billing_party_id, bp.billing_date_basis
FROM customers c
LEFT JOIN billing_parties bp ON bp.customer_id = c.id
WHERE c.id = sqlc.arg(id);

-- name: LockCustomer :one
-- PATCH: the row is locked (RLS: FOR UPDATE needs the steward write policy, so a non-steward finds nothing).
SELECT sqlc.embed(c), bp.id AS billing_party_id, bp.billing_date_basis
FROM customers c
LEFT JOIN billing_parties bp ON bp.customer_id = c.id
WHERE c.id = sqlc.arg(id)
FOR UPDATE OF c;

-- name: InsertCustomer :one
INSERT INTO customers (code, name, description, address, tax_id, branch_type, branch_number, contact_name,
                       contact_phone, billing_email, payment_terms_days, invoice_note, line_group_id)
VALUES (sqlc.arg(code)::text::citext, sqlc.arg(name), sqlc.narg(description), sqlc.narg(address), sqlc.narg(tax_id),
        sqlc.narg(branch_type), sqlc.narg(branch_number), sqlc.narg(contact_name), sqlc.narg(contact_phone),
        sqlc.narg(billing_email)::text::citext, sqlc.narg(payment_terms_days)::int, sqlc.narg(invoice_note),
        sqlc.narg(line_group_id))
RETURNING id;

-- name: UpdateCustomer :exec
-- PATCH writes the merged row (Go merges the request into the locked row; updated_at by trigger).
UPDATE customers
SET code = sqlc.arg(code)::text::citext, name = sqlc.arg(name), description = sqlc.narg(description),
    logo_file_id = sqlc.narg(logo_file_id)::uuid, address = sqlc.narg(address), tax_id = sqlc.narg(tax_id),
    branch_type = sqlc.narg(branch_type), branch_number = sqlc.narg(branch_number),
    contact_name = sqlc.narg(contact_name), contact_phone = sqlc.narg(contact_phone),
    billing_email = sqlc.narg(billing_email)::text::citext, payment_terms_days = sqlc.narg(payment_terms_days)::int,
    invoice_note = sqlc.narg(invoice_note), line_group_id = sqlc.narg(line_group_id)
WHERE id = sqlc.arg(id);

-- name: SetCustomerLogo :exec
UPDATE customers SET logo_file_id = sqlc.narg(logo_file_id)::uuid WHERE id = sqlc.arg(id);

-- name: InsertCustomerParty :one
-- The customer's billing party (R6): exactly one per customer (billing_parties_customer is unique).
INSERT INTO billing_parties (kind, customer_id, billing_date_basis)
VALUES ('customer', sqlc.arg(customer_id), sqlc.arg(billing_date_basis))
RETURNING id;

-- name: SetCustomerPartyBasis :execrows
UPDATE billing_parties SET billing_date_basis = sqlc.arg(billing_date_basis)
WHERE customer_id = sqlc.arg(customer_id) AND billing_date_basis IS DISTINCT FROM sqlc.arg(billing_date_basis);

-- name: ListDriverIDTypes :many
SELECT customer_id, key, label, position
FROM customer_driver_id_types
WHERE customer_id = ANY (sqlc.arg(customer_ids)::uuid[])
ORDER BY customer_id, position, key;

-- name: DeleteDriverIDTypes :exec
DELETE FROM customer_driver_id_types WHERE customer_id = sqlc.arg(customer_id);

-- name: InsertDriverIDTypes :exec
-- One row per entry of driverIdTypes[], position = its index.
INSERT INTO customer_driver_id_types (customer_id, key, label, position)
SELECT sqlc.arg(customer_id)::uuid, (sqlc.arg(keys)::text[])[i], (sqlc.arg(labels)::text[])[i], i - 1
FROM generate_subscripts(sqlc.arg(keys)::text[], 1) AS i;
