-- Billing statements, their lines and invoice numbers (Appendix B §B.2.14, main spec §6.11, §6.14, T37). A statement
-- carries the billing carrier's tenant (R61): the request's effective tenant, the own fleet (the service refuses any
-- other until main spec §19 Q13 is decided), which next_invoice_seq() also receives
-- (SECURITY DEFINER, R66: billing_counters is reachable only through it; another carrier's counter returns no row). The
-- whole creation is one transaction, so a failure leaves no gap in the numbering. Lines are built from stored snapshots
-- (never repriced) and are immutable; totals are exact NUMERIC sums of the lines; withholding_tax_rate is the issuing
-- company's percent as a fraction, snapshotted at creation (R18).

-- name: IssuerCompanies :many
-- The invoice issuer of a billing tenant when the request names none: the owner company, else its active companies.
SELECT c.id, c.company_type, c.is_active, (c.withholding_tax_rate / 100)::float8 AS withholding_tax_rate
FROM companies c
WHERE c.tenant_id = sqlc.arg(tenant_id) AND (c.company_type = 'owner' OR c.is_active)
ORDER BY (c.company_type = 'owner') DESC, c.created_at, c.id
LIMIT 2;

-- name: IssuerCompany :one
SELECT c.id, c.company_type, c.is_active, (c.withholding_tax_rate / 100)::float8 AS withholding_tax_rate
FROM companies c
WHERE c.id = sqlc.arg(id) AND c.tenant_id = sqlc.arg(tenant_id);

-- name: NextInvoiceSeq :one
-- The allocator in the statement transaction (row lock on the period's counter, R66); NULL when another billing
-- carrier owns the counter (R61), read as 0.
SELECT coalesce(next_invoice_seq(sqlc.arg(tenant_id)::uuid, sqlc.arg(party_id)::uuid, sqlc.arg(period_year)::int,
                                 sqlc.arg(period_month)::int, sqlc.arg(customer_code)::text), 0)::int AS seq;

-- name: InsertStatement :one
-- A draft with zero totals; InsertStatementLines adds the lines and UpdateStatementTotals the sums. It returns the rate it
-- stored, read from the company here, and the withholding is computed from that value: the issuer read before
-- next_invoice_seq (which may wait on the counter lock) can see an older rate (R18).
INSERT INTO billing_statements (tenant_id, tenant_source, billing_party_id, issuer_company_id, invoice_number,
                                customer_name_snapshot, customer_code_snapshot, period_year, period_month,
                                total_amount, withholding_tax_rate, withholding_tax, net_amount, status, generated_at,
                                due_date, note, generated_by, show_actual_pickup)
VALUES (sqlc.arg(tenant_id), 'form', sqlc.arg(party_id), sqlc.arg(issuer_company_id), sqlc.arg(invoice_number),
        sqlc.narg(customer_name), sqlc.narg(customer_code), sqlc.arg(period_year), sqlc.arg(period_month), 0,
        (SELECT (c.withholding_tax_rate / 100)::numeric(5,4) FROM companies c WHERE c.id = sqlc.arg(issuer_company_id)),
        0, 0, 'draft', sqlc.arg(generated_at), sqlc.narg(due_date), sqlc.narg(note), sqlc.narg(generated_by)::uuid,
        sqlc.arg(show_actual_pickup))
RETURNING id, withholding_tax_rate::float8 AS withholding_tax_rate;

-- name: InsertStatementLines :execrows
-- The invoice set of a statement (port of fetchBillingTripRows, logitrack-web/features/accounting/api/billing.ts:737-1094,
-- minus the display joins): delivered trips of the party on its axis (billing_date for a plan-basis party, else
-- delivered_at) inside the Bangkok month, priced by this billing carrier (trip_billing_snapshots.tenant_id) with an
-- estimate other than 0; a multi-drop trip with a breakdown becomes one line per stop whose final rate is not 0; completed
-- standby records of the party by ended_at, priced (billing_estimate_thb <> 0) by this billing carrier
-- (standby_records.billing_tenant_id, stamped with the price, 0015: a fee deleted or re-typed later does not move it; a
-- priced row without one, as the ETL loads it, is the carrier's of the rate entry it names (immutable, never deleted),
-- else the own fleet's, so own_fleet says whether the billing tenant is it: the rule of operations.BillingVisible).
-- Type toggles (trip, multidrop, standby) and category toggles (PRIMARY, SUPPLEMENTARY, unverified = neither on the trip
-- nor on its task; standby exempt) choose the invoice set (main spec §6.14). The statement transaction runs under the
-- caller's RLS, so a row outside its tenant reach is not read; StatementInvoiceSet counts the same set without RLS and
-- the statement is refused when it holds more (rows_outside_reach). The WHERE clauses of the two queries are the same
-- text: change them together.
INSERT INTO billing_statement_lines (statement_id, row_type, trip_id, stop_index, standby_id, amount_thb)
SELECT sqlc.arg(statement_id)::uuid, x.row_type, x.trip_id, x.stop_index, x.standby_id, x.amount
FROM (
  SELECT 'trip'::text AS row_type, tr.id AS trip_id, NULL::int AS stop_index, NULL::uuid AS standby_id,
         s.estimate_thb AS amount
  FROM trip_records tr
  JOIN trip_billing_snapshots s ON s.trip_id = tr.id
  LEFT JOIN tasks tk ON tk.id = tr.task_id
  WHERE sqlc.arg(include_trips)::boolean
    AND tr.status = 'delivered' AND tr.billing_party_id = sqlc.arg(party_id)::uuid
    AND s.tenant_id = sqlc.arg(tenant_id)::uuid
    AND s.estimate_thb IS NOT NULL AND s.estimate_thb <> 0
    AND CASE WHEN sqlc.arg(plan_basis)::boolean THEN tr.billing_date ELSE tr.delivered_at END >= sqlc.arg(period_from)::timestamptz
    AND CASE WHEN sqlc.arg(plan_basis)::boolean THEN tr.billing_date ELSE tr.delivered_at END < sqlc.arg(period_to)::timestamptz
    AND NOT (s.is_multi_delivery AND EXISTS (SELECT 1 FROM trip_billing_stop_breakdown b WHERE b.trip_id = tr.id))
    AND coalesce(tr.job_category, tk.job_category, 'unverified') = ANY (sqlc.arg(categories)::text[])
  UNION ALL
  SELECT 'multidrop_stop'::text, tr.id, b.stop_index, NULL::uuid, b.final_rate_thb
  FROM trip_records tr
  JOIN trip_billing_snapshots s ON s.trip_id = tr.id
  JOIN trip_billing_stop_breakdown b ON b.trip_id = tr.id
  LEFT JOIN tasks tk ON tk.id = tr.task_id
  WHERE sqlc.arg(include_multidrop)::boolean
    AND tr.status = 'delivered' AND tr.billing_party_id = sqlc.arg(party_id)::uuid
    AND s.tenant_id = sqlc.arg(tenant_id)::uuid
    AND s.estimate_thb IS NOT NULL AND s.estimate_thb <> 0 AND s.is_multi_delivery AND b.final_rate_thb <> 0
    AND CASE WHEN sqlc.arg(plan_basis)::boolean THEN tr.billing_date ELSE tr.delivered_at END >= sqlc.arg(period_from)::timestamptz
    AND CASE WHEN sqlc.arg(plan_basis)::boolean THEN tr.billing_date ELSE tr.delivered_at END < sqlc.arg(period_to)::timestamptz
    AND coalesce(tr.job_category, tk.job_category, 'unverified') = ANY (sqlc.arg(categories)::text[])
  UNION ALL
  SELECT 'standby'::text, NULL::uuid, NULL::int, sr.id, sr.billing_estimate_thb
  FROM standby_records sr
  WHERE sqlc.arg(include_standby)::boolean
    AND sr.status = 'completed' AND sr.billing_party_id = sqlc.arg(party_id)::uuid
    AND sr.ended_at >= sqlc.arg(period_from)::timestamptz AND sr.ended_at < sqlc.arg(period_to)::timestamptz
    AND sr.billing_estimate_thb IS NOT NULL AND sr.billing_estimate_thb <> 0
    AND coalesce(sr.billing_tenant_id,
                 (SELECT r.tenant_id FROM standby_rate_entries r WHERE r.id = sr.billing_rate_entry_id),
                 CASE WHEN sqlc.arg(own_fleet)::boolean THEN sqlc.arg(tenant_id)::uuid END) = sqlc.arg(tenant_id)::uuid
) x;

-- name: StatementInvoiceSet :one
-- The invoice set of InsertStatementLines (the same WHERE clauses, change them together), counted and summed. Run in a
-- READ ONLY system transaction (db.WithSystem), it sees the rows the caller's RLS may hide: a trip or standby record
-- this billing carrier priced but run by a tenant outside the caller's reach (a carrier without a contractor link, the
-- quarantine tenant).
SELECT count(*)::int AS line_count, coalesce(sum(x.amount), 0)::float8 AS total
FROM (
  SELECT 'trip'::text AS row_type, tr.id AS trip_id, NULL::int AS stop_index, NULL::uuid AS standby_id,
         s.estimate_thb AS amount
  FROM trip_records tr
  JOIN trip_billing_snapshots s ON s.trip_id = tr.id
  LEFT JOIN tasks tk ON tk.id = tr.task_id
  WHERE sqlc.arg(include_trips)::boolean
    AND tr.status = 'delivered' AND tr.billing_party_id = sqlc.arg(party_id)::uuid
    AND s.tenant_id = sqlc.arg(tenant_id)::uuid
    AND s.estimate_thb IS NOT NULL AND s.estimate_thb <> 0
    AND CASE WHEN sqlc.arg(plan_basis)::boolean THEN tr.billing_date ELSE tr.delivered_at END >= sqlc.arg(period_from)::timestamptz
    AND CASE WHEN sqlc.arg(plan_basis)::boolean THEN tr.billing_date ELSE tr.delivered_at END < sqlc.arg(period_to)::timestamptz
    AND NOT (s.is_multi_delivery AND EXISTS (SELECT 1 FROM trip_billing_stop_breakdown b WHERE b.trip_id = tr.id))
    AND coalesce(tr.job_category, tk.job_category, 'unverified') = ANY (sqlc.arg(categories)::text[])
  UNION ALL
  SELECT 'multidrop_stop'::text, tr.id, b.stop_index, NULL::uuid, b.final_rate_thb
  FROM trip_records tr
  JOIN trip_billing_snapshots s ON s.trip_id = tr.id
  JOIN trip_billing_stop_breakdown b ON b.trip_id = tr.id
  LEFT JOIN tasks tk ON tk.id = tr.task_id
  WHERE sqlc.arg(include_multidrop)::boolean
    AND tr.status = 'delivered' AND tr.billing_party_id = sqlc.arg(party_id)::uuid
    AND s.tenant_id = sqlc.arg(tenant_id)::uuid
    AND s.estimate_thb IS NOT NULL AND s.estimate_thb <> 0 AND s.is_multi_delivery AND b.final_rate_thb <> 0
    AND CASE WHEN sqlc.arg(plan_basis)::boolean THEN tr.billing_date ELSE tr.delivered_at END >= sqlc.arg(period_from)::timestamptz
    AND CASE WHEN sqlc.arg(plan_basis)::boolean THEN tr.billing_date ELSE tr.delivered_at END < sqlc.arg(period_to)::timestamptz
    AND coalesce(tr.job_category, tk.job_category, 'unverified') = ANY (sqlc.arg(categories)::text[])
  UNION ALL
  SELECT 'standby'::text, NULL::uuid, NULL::int, sr.id, sr.billing_estimate_thb
  FROM standby_records sr
  WHERE sqlc.arg(include_standby)::boolean
    AND sr.status = 'completed' AND sr.billing_party_id = sqlc.arg(party_id)::uuid
    AND sr.ended_at >= sqlc.arg(period_from)::timestamptz AND sr.ended_at < sqlc.arg(period_to)::timestamptz
    AND sr.billing_estimate_thb IS NOT NULL AND sr.billing_estimate_thb <> 0
    AND coalesce(sr.billing_tenant_id,
                 (SELECT r.tenant_id FROM standby_rate_entries r WHERE r.id = sr.billing_rate_entry_id),
                 CASE WHEN sqlc.arg(own_fleet)::boolean THEN sqlc.arg(tenant_id)::uuid END) = sqlc.arg(tenant_id)::uuid
) x;

-- name: StatementTotals :one
-- Exact NUMERIC sums of a statement's lines (main spec §6.2: SQL statement totals are exact), returned as float8.
SELECT count(*)::int AS line_count,
       count(*) FILTER (WHERE row_type = 'trip')::int AS trip_only_count,
       count(*) FILTER (WHERE row_type = 'standby')::int AS standby_count,
       count(*) FILTER (WHERE row_type = 'multidrop_stop')::int AS multi_drop_count,
       coalesce(sum(amount_thb), 0)::float8 AS total
FROM billing_statement_lines
WHERE statement_id = sqlc.arg(statement_id);

-- name: UpdateStatementTotals :exec
-- Counts and subtotals from the lines, the withholding computed in Go (WithholdingTHB of the total and the stored
-- rate), net = total - withholding as NUMERIC.
UPDATE billing_statements s
SET total_amount = a.total, trip_count = a.n, trip_only_count = a.n_trip, trip_subtotal = a.trip_sum,
    standby_count = a.n_standby, standby_subtotal = a.standby_sum, multi_drop_count = a.n_multi,
    multi_drop_subtotal = a.multi_sum, withholding_tax = (sqlc.arg(withholding_tax)::text)::numeric(14,2),
    net_amount = a.total - (sqlc.arg(withholding_tax)::text)::numeric(14,2)
FROM (SELECT count(*)::int AS n,
             count(*) FILTER (WHERE row_type = 'trip')::int AS n_trip,
             count(*) FILTER (WHERE row_type = 'standby')::int AS n_standby,
             count(*) FILTER (WHERE row_type = 'multidrop_stop')::int AS n_multi,
             coalesce(sum(amount_thb), 0) AS total,
             coalesce(sum(amount_thb) FILTER (WHERE row_type = 'trip'), 0) AS trip_sum,
             coalesce(sum(amount_thb) FILTER (WHERE row_type = 'standby'), 0) AS standby_sum,
             coalesce(sum(amount_thb) FILTER (WHERE row_type = 'multidrop_stop'), 0) AS multi_sum
      FROM billing_statement_lines WHERE statement_id = sqlc.arg(id)) a
WHERE s.id = sqlc.arg(id);

-- name: GetStatement :one
-- customer_id is the customer or carrier the party bills; the creator's display name and email (both NULL when the
-- creator is not visible under the caller's RLS or generated_by is NULL) give generatedByName, so the billing-result page
-- needs no per-creator users read (Appendix E).
SELECT s.id, s.legacy_doc_id, s.tenant_id, s.billing_party_id, s.issuer_company_id, s.invoice_number,
       s.customer_name_snapshot, s.customer_code_snapshot, s.period_year, s.period_month, s.total_amount,
       s.withholding_tax_rate, s.withholding_tax, s.net_amount, s.trip_count, s.trip_only_count, s.trip_subtotal,
       s.standby_count, s.standby_subtotal, s.multi_drop_count, s.multi_drop_subtotal, s.status, s.generated_at, s.sent_at,
       s.paid_at, s.cancelled_at, s.due_date, s.note, s.generated_by, s.created_at, s.updated_at, s.show_actual_pickup,
       coalesce(bp.customer_id, bp.tenant_id, s.billing_party_id)::uuid AS customer_id,
       u.display_name AS generated_by_display_name, u.email AS generated_by_email
FROM billing_statements s
JOIN billing_parties bp ON bp.id = s.billing_party_id
LEFT JOIN users u ON u.id = s.generated_by
WHERE s.id = sqlc.arg(id);

-- name: ListStatements :many
-- GET /v1/billing/statements: newest first, keyset (generated_at, id) (billing_statements_tenant_gen).
SELECT s.id, s.legacy_doc_id, s.tenant_id, s.billing_party_id, s.issuer_company_id, s.invoice_number,
       s.customer_name_snapshot, s.customer_code_snapshot, s.period_year, s.period_month, s.total_amount,
       s.withholding_tax_rate, s.withholding_tax, s.net_amount, s.trip_count, s.trip_only_count, s.trip_subtotal,
       s.standby_count, s.standby_subtotal, s.multi_drop_count, s.multi_drop_subtotal, s.status, s.generated_at, s.sent_at,
       s.paid_at, s.cancelled_at, s.due_date, s.note, s.generated_by, s.created_at, s.updated_at, s.show_actual_pickup,
       coalesce(bp.customer_id, bp.tenant_id, s.billing_party_id)::uuid AS customer_id,
       u.display_name AS generated_by_display_name, u.email AS generated_by_email
FROM billing_statements s
JOIN billing_parties bp ON bp.id = s.billing_party_id
LEFT JOIN users u ON u.id = s.generated_by
WHERE (sqlc.arg(all_tenants)::boolean OR s.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(party_id)::uuid IS NULL OR s.billing_party_id = sqlc.narg(party_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR s.status = sqlc.narg(status)::text)
  AND (sqlc.narg(period_year)::int IS NULL OR s.period_year = sqlc.narg(period_year)::int)
  AND (sqlc.narg(period_month)::int IS NULL OR s.period_month = sqlc.narg(period_month)::int)
  AND (sqlc.narg(after_generated)::timestamptz IS NULL
       OR (s.generated_at, s.id) < (sqlc.narg(after_generated)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY s.generated_at DESC, s.id DESC
LIMIT sqlc.arg(row_limit);

-- name: StatementLines :many
SELECT l.id, l.row_type, l.trip_id, l.stop_index, l.standby_id, l.amount_thb, l.created_at,
       tr.trip_no
FROM billing_statement_lines l
LEFT JOIN trip_records tr ON tr.id = l.trip_id
WHERE l.statement_id = sqlc.arg(statement_id)
ORDER BY l.row_type, tr.trip_no COLLATE "C", l.stop_index NULLS FIRST, l.standby_id, l.id;

-- name: StatementDocuments :many
SELECT d.kind, d.status, d.file_id, d.error, d.rendered_at, d.updated_at
FROM statement_documents d
WHERE d.statement_id = sqlc.arg(statement_id)
ORDER BY d.kind;

-- name: StatementPeriod :one
-- The party and period of a statement, unlocked (the columns never change): a status change takes the period's
-- exclusive advisory lock with them before it locks the row.
SELECT s.billing_party_id, s.period_year, s.period_month FROM billing_statements s WHERE s.id = sqlc.arg(id);

-- name: PeriodAdvisoryShared :exec
-- The period lock of a pricing transaction (main spec §6.11): shared, per (party, Bangkok month), until the transaction
-- ends. A status change takes the exclusive twin, so a statement created after the pricing read cannot turn sent while
-- the price commits either; FOR SHARE on the rows read covers only the statements that existed.
SELECT pg_advisory_xact_lock_shared(hashtextextended('billing_period:' || sqlc.arg(party_id)::uuid::text || ':'
                                                    || sqlc.arg(period)::int::text, 0));

-- name: PeriodAdvisoryExclusive :exec
SELECT pg_advisory_xact_lock(hashtextextended('billing_period:' || sqlc.arg(party_id)::uuid::text || ':'
                                             || sqlc.arg(period)::int::text, 0));

-- name: LockStatement :one
-- A status change locks the statement FOR UPDATE after the period's exclusive advisory lock: it waits for every pricing
-- transaction that read the period, so a period cannot become sent while a reprice commits (main spec §6.11).
SELECT s.id, s.tenant_id, s.billing_party_id, s.invoice_number, s.status, s.period_year, s.period_month
FROM billing_statements s
WHERE s.id = sqlc.arg(id)
FOR UPDATE;

-- name: SetStatementStatus :exec
UPDATE billing_statements
SET status = sqlc.arg(status),
    sent_at = CASE WHEN sqlc.arg(status) = 'sent' THEN sqlc.arg(at)::timestamptz ELSE sent_at END,
    paid_at = CASE WHEN sqlc.arg(status) = 'paid' THEN sqlc.arg(at)::timestamptz ELSE paid_at END,
    cancelled_at = CASE WHEN sqlc.arg(status) = 'cancelled' THEN sqlc.arg(at)::timestamptz ELSE cancelled_at END
WHERE id = sqlc.arg(id);

-- name: DeleteStatement :execrows
-- Drafts only (trg_billing_statement_draft_only_delete); the lines and documents go with it (ON DELETE CASCADE), and the
-- number stays unused (main spec §6.14).
DELETE FROM billing_statements WHERE id = sqlc.arg(id);

-- name: PeriodStatements :many
-- The period lock read of a pricing transaction (main spec §6.11, R17), after PeriodAdvisoryShared of each month: every
-- statement of the party in the Bangkok months asked FOR SHARE, whatever its status, so a draft cannot turn sent (FOR
-- UPDATE) until the price commits (the advisory lock covers a statement created after this read); only sent and paid
-- statements lock (compute.NewPeriodLocks keeps those, the latest generated_at supplying the invoice number).
SELECT s.id, s.billing_party_id, s.invoice_number, s.status, s.period_year, s.period_month, s.generated_at
FROM billing_statements s
WHERE s.billing_party_id = sqlc.arg(party_id)
  AND s.period_year * 100 + s.period_month = ANY (sqlc.arg(periods)::int[])   -- yyyymm keys
ORDER BY s.id
FOR SHARE OF s;
