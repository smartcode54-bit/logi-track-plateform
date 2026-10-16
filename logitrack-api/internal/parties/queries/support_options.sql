-- name: ListSupportCustomerOptions :many
-- This is a code-only dependency, never a customer directory. RLS and exact
-- selected-tenant references both apply; no inherited contractor reach.
SELECT c.id, c.code::text AS code
FROM customers c
WHERE (c.id = ANY (sqlc.arg(ids)::uuid[]) OR c.code = ANY (sqlc.arg(codes)::text[]))
  AND (
    EXISTS (SELECT 1 FROM driver_customer_codes dc
            JOIN drivers d ON d.id = dc.driver_id
            WHERE dc.customer_id = c.id AND d.tenant_id = sqlc.arg(tenant_id))
    OR EXISTS (SELECT 1 FROM billing_parties bp
               JOIN tasks t ON bp.id IN (t.billing_party_id, t.source_linked_party_id, t.destination_linked_party_id)
               WHERE bp.customer_id = c.id AND t.tenant_id = sqlc.arg(tenant_id))
    OR EXISTS (SELECT 1 FROM billing_parties bp
               JOIN trip_records t ON bp.id = t.billing_party_id
               WHERE bp.customer_id = c.id AND t.tenant_id = sqlc.arg(tenant_id))
  )
ORDER BY c.id
LIMIT 100;
