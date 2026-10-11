-- Tax and insurance renewals (Appendix B §B.2.8): the renewal overview of the trucks of the caller's reach and the
-- append-only transactions ledger a completed renewal writes (tx_type tax | insurance, tenant from the truck, R75).

-- name: ListRenewals :many
-- GET /v1/trucks/renewals: most urgent first. due_key is the earlier of the two expiry dates; a truck missing either
-- date sorts first and counts as due (the legacy overview treats a missing date as overdue,
-- renewals/actions.client.ts:17). Keyset (due_key, id); due_by bounds due_key (?due=).
WITH r AS (
  SELECT t.*,
         CASE WHEN t.tax_expiry_date IS NULL OR t.insurance_expiry_date IS NULL THEN DATE '0001-01-01'
              ELSE least(t.tax_expiry_date, t.insurance_expiry_date) END AS due_key
  FROM trucks t
  WHERE (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
    AND (sqlc.narg(tenant_id)::uuid IS NULL OR t.tenant_id = sqlc.narg(tenant_id)::uuid)
    AND (sqlc.narg(ownership)::text IS NULL OR t.ownership_type = sqlc.narg(ownership)::text)
    AND (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status)::text)
    AND (sqlc.narg(q)::text IS NULL OR strpos(lower(t.license_plate), lower(sqlc.narg(q)::text)) > 0)
)
SELECT id, tenant_id, ownership_type, license_plate, province, status, type_raw, vehicle_class,
       tax_expiry_date, tax_renewal_status, tax_responsible, tax_expense_thb,
       insurance_expiry_date, insurance_renewal_status, insurance_company, insurance_policy_number, insurance_type,
       insurance_premium_thb, payment_method, due_key::date AS due_key
FROM r
WHERE (sqlc.narg(due_by)::date IS NULL OR r.due_key <= sqlc.narg(due_by)::date)
  AND (sqlc.narg(after_due)::date IS NULL OR (r.due_key, r.id) > (sqlc.narg(after_due)::date, sqlc.arg(after_id)::uuid))
ORDER BY r.due_key, r.id
LIMIT sqlc.arg(row_limit);

-- name: InsertRenewalTransaction :one
-- POST /v1/trucks/{id}/renewals (completed): the ledger row. tenant = the truck's (tenant_source 'truck', main spec
-- §13.5); performed_by_label snapshots the actor's display name or email like the legacy performedBy string.
INSERT INTO transactions (tenant_id, tenant_source, tx_type, sub_type, amount_thb, payment_method, tx_date,
                          tx_date_source, truck_id, receipt_file_id, performed_by_user_id, performed_by_label, notes)
VALUES (sqlc.arg(tenant_id), 'truck', sqlc.arg(tx_type), sqlc.narg(sub_type), sqlc.arg(amount_thb)::numeric,
        sqlc.narg(payment_method), sqlc.arg(tx_date)::date, 'bangkok', sqlc.arg(truck_id), sqlc.narg(receipt_file_id)::uuid,
        sqlc.narg(user_id)::uuid,
        coalesce(sqlc.narg(label)::text,
                 (SELECT coalesce(u.display_name, u.email::text) FROM users u WHERE u.id = sqlc.narg(user_id)::uuid)),
        sqlc.narg(notes))
RETURNING id, created_at;
