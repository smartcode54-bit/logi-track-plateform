-- Rendered documents of a statement (statement_documents, R14; main spec §9.9, T39) and the reads documents.render needs.
-- The worker runs these in db.WithSystem (no request principal: the event or job names the statement); the download
-- route reads StatementDocument under the caller's RLS (parent-scoped through billing_statements). A document's file is
-- a committed file_objects row of purpose statement_document; a regenerate renders under a new key and points the row
-- at the new file, the old object staying (§9.9).

-- name: RenderStatement :one
-- The statement as its documents print it: number, period, totals and the stored withholding rate (R18), the party's
-- basis, the bill-to (the customer, or the carrier tenant the party bills), the issuing company and whether the detail
-- sheet carries the วันรับงานจริง column (0016).
SELECT s.id, s.tenant_id, s.billing_party_id, s.invoice_number, s.customer_name_snapshot, s.customer_code_snapshot,
       s.period_year, s.period_month, s.total_amount, s.withholding_tax_rate, s.withholding_tax, s.net_amount, s.status,
       s.generated_at, s.paid_at, s.show_actual_pickup,
       bp.kind AS party_kind, bp.billing_date_basis,
       c.code AS customer_code, c.name AS customer_name, c.address AS customer_address, c.tax_id AS customer_tax_id,
       c.branch_type AS customer_branch_type, c.branch_number AS customer_branch_number,
       c.contact_name AS customer_contact_name, c.contact_phone AS customer_contact_phone, c.payment_terms_days,
       c.invoice_note,
       t.code AS tenant_code, t.name_th AS tenant_name, t.address AS tenant_address, t.tax_id AS tenant_tax_id,
       t.contact_person AS tenant_contact, t.phone AS tenant_phone,
       co.id AS company_id, co.name_th AS company_name, co.short_name AS company_short_name, co.address AS company_address,
       co.tax_id AS company_tax_id, co.bank_name, co.account_number, co.account_name, co.signatory_name,
       co.stamp_file_id, co.signature_file_id
FROM billing_statements s
JOIN billing_parties bp ON bp.id = s.billing_party_id
LEFT JOIN customers c ON c.id = bp.customer_id
LEFT JOIN tenants t ON t.id = bp.tenant_id
LEFT JOIN companies co ON co.id = s.issuer_company_id
WHERE s.id = sqlc.arg(id);

-- name: RenderLines :many
-- The lines of a statement with the stored amount (never repriced) and what the documents print beside it, read from the
-- trip, its snapshot and stop, or the standby record, and their task and driver.
SELECT l.id, l.row_type, l.trip_id, l.stop_index, l.standby_id, l.amount_thb,
       tr.trip_no, tr.delivered_at, tr.billing_date, tr.job_category AS trip_job_category,
       tr.truck_license_plate_snapshot AS trip_plate, tr.vehicle_class AS trip_vehicle_class,
       s.lookup_hub_code, s.lookup_destination_code, s.add_thb_per_trip, s.round_effective_from_date,
       s.fuel_band_lower_thb, s.fuel_band_upper_thb, s.reference_fuel_price_thb,
       b.destination_code AS stop_destination_code,
       sr.ended_at, sr.started_at, sr.start_location_raw, sr.end_location_raw, sr.job_category AS standby_job_category,
       sr.truck_license_plate_snapshot AS standby_plate, sr.migrated_from_trip_no, strp.trip_no AS standby_trip_no,
       tk.job_category AS task_job_category, tk.truck_type, tk.license_plate_snapshot AS task_plate, tk.source_hub_raw,
       tk.destination_raw, tk.actual_pickup_at,
       d.full_name_th, d.first_name, d.last_name, d.email AS driver_email, d.mobile AS driver_mobile,
       dt.kind AS driver_tenant_kind, dt.name_th AS driver_tenant_name
FROM billing_statement_lines l
LEFT JOIN trip_records tr ON tr.id = l.trip_id
LEFT JOIN trip_billing_snapshots s ON s.trip_id = l.trip_id
LEFT JOIN trip_billing_stop_breakdown b ON b.trip_id = l.trip_id AND b.stop_index = l.stop_index
LEFT JOIN standby_records sr ON sr.id = l.standby_id
LEFT JOIN trip_records strp ON strp.id = sr.trip_id
LEFT JOIN tasks tk ON tk.id = coalesce(tr.task_id, sr.task_id)
LEFT JOIN drivers d ON d.id = coalesce(tr.driver_id, sr.driver_id, tk.driver_id)
LEFT JOIN tenants dt ON dt.id = d.tenant_id
WHERE l.statement_id = sqlc.arg(statement_id)
ORDER BY tr.trip_no COLLATE "C" NULLS LAST, l.stop_index NULLS FIRST, sr.ended_at, l.id;

-- name: MarkDocumentsRendering :exec
-- A render starts: every kind it renders is pending -> rendering (a row is created for a first render); a ready document
-- keeps serving its file until the new one replaces it.
INSERT INTO statement_documents (statement_id, kind, status, job_id)
SELECT sqlc.arg(statement_id)::uuid, k, 'rendering', sqlc.narg(job_id)::uuid
FROM unnest(sqlc.arg(kinds)::text[]) AS k
ON CONFLICT (statement_id, kind) DO UPDATE
  SET status = 'rendering', error = NULL, job_id = excluded.job_id
  WHERE statement_documents.status <> 'ready';

-- name: FinishDocument :exec
-- A rendered document: ready with its new file (the previous file row stays, unreferenced).
INSERT INTO statement_documents (statement_id, kind, status, file_id, job_id, rendered_at)
VALUES (sqlc.arg(statement_id), sqlc.arg(kind), 'ready', sqlc.arg(file_id), sqlc.narg(job_id)::uuid, sqlc.arg(rendered_at))
ON CONFLICT (statement_id, kind) DO UPDATE
  SET status = 'ready', file_id = excluded.file_id, job_id = excluded.job_id, error = NULL,
      rendered_at = excluded.rendered_at;

-- name: FailDocuments :exec
-- A render that failed: the kinds not ready yet are failed with the reason; a ready document keeps its file (the job, if
-- any, carries the failure of a regenerate).
INSERT INTO statement_documents (statement_id, kind, status, error, job_id)
SELECT sqlc.arg(statement_id)::uuid, k, 'failed', sqlc.arg(error)::text, sqlc.narg(job_id)::uuid
FROM unnest(sqlc.arg(kinds)::text[]) AS k
ON CONFLICT (statement_id, kind) DO UPDATE
  SET status = 'failed', error = excluded.error, job_id = excluded.job_id
  WHERE statement_documents.status <> 'ready';

-- name: SetStatementShowActualPickup :exec
-- A regenerate that names showActualPickup stores it in the transaction that succeeds its job (0016), so the column
-- always names what the stored detail sheet shows.
UPDATE billing_statements SET show_actual_pickup = sqlc.arg(show_actual_pickup) WHERE id = sqlc.arg(id);

-- name: StatementDocument :one
-- GET /v1/billing/statements/{id}/documents/{kind}: the document of a statement the caller can read.
SELECT d.kind, d.status, d.file_id, d.error, d.rendered_at
FROM statement_documents d
WHERE d.statement_id = sqlc.arg(statement_id) AND d.kind = sqlc.arg(kind);

-- name: InboxSeen :one
-- Whether a consumer already committed a message (consumer_inbox): documents.render checks before it renders, then
-- claims in the transaction that stores the result.
SELECT EXISTS (SELECT 1 FROM consumer_inbox WHERE consumer = sqlc.arg(consumer) AND message_id = sqlc.arg(message_id));

-- name: LatestShopeeRender :one
-- The newest finished Shopee report render for the same parameters (customer, month, half, reach): GET
-- /v1/billing/shopee-report.pdf redirects to it while its rows version is still the period's.
SELECT j.id, j.result, j.finished_at
FROM jobs j
WHERE j.type = 'documents.shopee-report' AND j.status = 'succeeded' AND j.params @> sqlc.arg(params)::jsonb
ORDER BY j.finished_at DESC NULLS LAST, j.id DESC
LIMIT 1;

-- name: OwnerCompanyName :one
-- The Shopee report's header: the owner company's name (getOwnerCompany of the page).
SELECT c.name_th FROM companies c WHERE c.company_type = 'owner' ORDER BY c.created_at, c.id LIMIT 1;

-- name: StatementLineCount :one
-- A statement's line count: a legacy statement the ETL loaded has none (billing_statement_lines starts with T37), so
-- nothing can be rendered from it.
SELECT count(*)::int FROM billing_statement_lines WHERE statement_id = sqlc.arg(statement_id);
