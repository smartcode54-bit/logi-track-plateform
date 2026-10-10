package seed

// The SQL halves of the twelve invariants of Appendix D §D.3, run first in T16 and kept there in the same
// words. Every query returns violating rows (any row fails the invariant); ids are cast to text so a row
// prints readably. The Go halves (engine recompute #2, Redis hub maps #8, object stat and hash #9, the
// isolation role-play #10c/#10d and per carrier, non-v5 ids #12) are in verify.go, store.go and engine.go.

type sqlCheck struct {
	n       int
	name    string
	queries []string
}

var sqlChecks = []sqlCheck{
	{1, "trip integrity: same-tenant task and a truck snapshot", []string{inv1}},
	{2, "delivered work priced or carrying a stored reason that re-derives", []string{inv2a, inv2b}},
	{3, "หลัก/เสริม: trip job_category equals its task's", []string{inv3}},
	{4, "priced SUPPLEMENTARY rows frozen; the corrupted fixture not frozen", []string{inv4}},
	{5, "period locks, invoice counters and invoice numbers", []string{inv5}},
	{6, "payroll totals, net and ledger; penalty balances", []string{inv6}},
	{7, "at most one helper per task, linked and not the driver", []string{inv7}},
	{8, "hub maps never merge directions; SPK890174 priced; SPK-GW looks up as SPK", []string{inv8}},
	{9, "media: objects exist with recorded size and sha256; no row references a pending upload", []string{inv9b}},
	{10, "tenant isolation", []string{inv10a, inv10b}},
	{11, "Bangkok calendar columns and the switch-day round", []string{inv11}},
	{12, "determinism", nil},
}

const inv1 = `SELECT t.id::text, t.trip_no, 'task/tenant/truck snapshot' AS problem
FROM trip_records t
LEFT JOIN tasks k ON k.id = t.task_id
WHERE k.id IS NULL
   OR t.tenant_id <> k.tenant_id
   OR (t.truck_id IS NULL AND t.truck_license_plate_snapshot IS NULL)
   OR (t.truck_id IS NOT NULL AND (t.truck_license_plate_snapshot IS NULL OR t.vehicle_class IS NULL))`

const inv2a = `SELECT t.id::text, t.trip_no, coalesce(s.unpriced_reason, 'no snapshot / no reason') AS problem
FROM trip_records t
JOIN tasks k ON k.id = t.task_id
LEFT JOIN trip_billing_snapshots s ON s.trip_id = t.id
CROSS JOIN LATERAL (SELECT COALESCE(k.billing_party_id, k.source_linked_party_id,
                                    k.destination_linked_party_id) AS party) p
WHERE t.status = 'delivered'
  AND (s.trip_id IS NULL
       OR (s.estimate_thb IS NULL AND NOT CASE s.unpriced_reason
             WHEN 'no_customer'      THEN p.party IS NULL
             WHEN 'no_vehicle_class' THEN p.party IS NOT NULL AND k.truck_type IS NULL
             WHEN 'no_rate'          THEN p.party IS NOT NULL AND k.truck_type IS NOT NULL AND NOT EXISTS (
                   SELECT 1 FROM customer_rate_entries r
                   WHERE NOT r.voided AND r.billing_party_id = p.party
                     AND r.hub_code = s.lookup_hub_code AND r.destination_code = s.lookup_destination_code)
             ELSE false END))`

const inv2b = `SELECT s.id::text, coalesce(s.billing_unpriced_reason, 'no price / no reason') AS problem
FROM standby_records s
LEFT JOIN tasks k ON k.id = s.task_id
CROSS JOIN LATERAL (SELECT COALESCE(s.customer_party_id, k.source_linked_party_id,
                                    k.destination_linked_party_id) AS party) p
WHERE s.status = 'completed' AND s.billing_estimate_thb IS NULL
  AND NOT CASE s.billing_unpriced_reason
        WHEN 'no_ended_at' THEN s.ended_at IS NULL
        WHEN 'no_customer' THEN s.ended_at IS NOT NULL AND p.party IS NULL
        WHEN 'no_rate'     THEN s.ended_at IS NOT NULL AND p.party IS NOT NULL
                            AND NOT EXISTS (SELECT 1 FROM standby_rate_entries e
                                            WHERE e.billing_party_id = p.party AND e.voided_at IS NULL)
                            AND NOT EXISTS (SELECT 1 FROM customer_service_fees f
                                            WHERE f.billing_party_id = p.party AND f.fee_type = 'standby')
        ELSE false END`

const inv3 = `SELECT t.id::text, t.trip_no
FROM trip_records t JOIN tasks k ON k.id = t.task_id
WHERE k.job_category IS NOT NULL AND t.job_category IS DISTINCT FROM k.job_category`

const inv4 = `SELECT t.trip_no, 'SUPPLEMENTARY not frozen' AS problem
FROM trip_records t JOIN trip_billing_snapshots s ON s.trip_id = t.id
WHERE s.estimate_thb IS NOT NULL AND t.job_category = 'SUPPLEMENTARY' AND t.trip_no <> 'ZXJB26090700112'
  AND NOT (s.manual_override AND s.fuel_adjustment_id IS NULL AND s.rate_multiplier = 1 AND s.add_thb_per_trip = 0)
UNION ALL
SELECT t.trip_no, 'corrupted fixture must not be frozen'
FROM trip_records t JOIN trip_billing_snapshots s ON s.trip_id = t.id
WHERE t.trip_no = 'ZXJB26090700112'
  AND (s.manual_override OR NOT (s.fuel_adjustment_id IS NOT NULL OR s.rate_multiplier <> 1 OR s.add_thb_per_trip <> 0))`

const inv5 = `SELECT 'trip' AS kind, s.trip_id::text AS id, b.invoice_number
FROM trip_billing_snapshots s
JOIN trip_records t ON t.id = s.trip_id
JOIN billing_statements b ON b.billing_party_id = t.billing_party_id AND b.status IN ('sent','paid')
 AND b.period_year = EXTRACT(YEAR FROM t.billing_axis_date)::int
 AND b.period_month = EXTRACT(MONTH FROM t.billing_axis_date)::int
WHERE s.updated_at > b.sent_at
UNION ALL
SELECT 'standby', r.id::text, b.invoice_number
FROM standby_records r
JOIN billing_statements b ON b.billing_party_id = r.billing_party_id AND b.status IN ('sent','paid')
 AND b.period_year = EXTRACT(YEAR FROM r.billing_axis_date)::int
 AND b.period_month = EXTRACT(MONTH FROM r.billing_axis_date)::int
WHERE r.billing_computed_at > b.sent_at
UNION ALL
SELECT 'counter', concat_ws('/', billing_party_id, period_year, period_month), NULL
FROM billing_counters c
FULL JOIN (SELECT billing_party_id, period_year, period_month, count(*) AS n
           FROM billing_statements GROUP BY 1, 2, 3) n USING (billing_party_id, period_year, period_month)
WHERE c.last_seq IS DISTINCT FROM n.n
UNION ALL
SELECT 'invoice_number', id::text, invoice_number FROM billing_statements
WHERE invoice_number !~ ('^' || customer_code_snapshot || '-' || period_year || lpad(period_month::text, 2, '0') || '-[0-9]{3,}$')`

const inv6 = `SELECT r.id::text, 'run totals' AS problem
FROM payroll_runs r
LEFT JOIN (SELECT payroll_run_id,
                  COALESCE(sum(amount_thb) FILTER (WHERE item_type = 'earning'), 0)   AS e,
                  COALESCE(sum(amount_thb) FILTER (WHERE item_type = 'deduction'), 0) AS d
           FROM payroll_line_items GROUP BY 1) l ON l.payroll_run_id = r.id
WHERE r.total_earnings_thb <> COALESCE(l.e, 0) OR r.total_deductions_thb <> COALESCE(l.d, 0)
   OR r.net_pay_thb <> GREATEST(0, r.total_earnings_thb - r.total_deductions_thb)
   OR (r.status IN ('approved','paid') AND r.ledger_transaction_id IS NULL)
UNION ALL
SELECT p.id::text, 'penalty balance'
FROM driver_penalties p
LEFT JOIN (SELECT a.penalty_id, sum(a.applied_thb) AS applied, count(*) AS n
           FROM payroll_penalty_applications a JOIN payroll_runs r ON r.id = a.payroll_run_id
           WHERE r.status IN ('approved','paid') GROUP BY 1) x ON x.penalty_id = p.id
WHERE (p.legacy_doc_id IS NULL AND p.status <> 'cancelled'
       AND (p.remaining_thb <> p.total_thb - COALESCE(x.applied, 0) OR p.installments_paid <> COALESCE(x.n, 0)))
   OR (p.status = 'cleared' AND p.remaining_thb <> 0)
   OR (p.status IN ('pending','partially_deducted') AND p.remaining_thb = 0)`

const inv7 = `SELECT k.id::text, k.task_no
FROM tasks k JOIN drivers h ON h.id = k.helper_driver_id
WHERE h.user_id IS NULL OR k.helper_driver_id = k.driver_id`

const inv8 = `SELECT 'alias equals a code' AS problem, a.alias::text
FROM hub_name_aliases a JOIN hubs h ON upper(h.source_id::text) = upper(a.alias::text)
UNION ALL
SELECT 'SPK890174 must resolve to a live rate', NULL
WHERE NOT EXISTS (SELECT 1 FROM customer_rate_entries WHERE destination_code = 'SPK890174' AND NOT voided)
UNION ALL
SELECT 'SPK-GW destination must look up as SPK', NULL
WHERE NOT EXISTS (SELECT 1 FROM trip_billing_snapshots s JOIN trip_records t ON t.id = s.trip_id
                  WHERE t.destination_raw = 'SPK-GW' AND s.lookup_destination_code = 'SPK')`

const inv9b = `SELECT 'trip_photos' AS t, p.id::text FROM trip_photos p JOIN file_objects f ON f.id = p.file_id WHERE f.status = 'pending'
UNION ALL SELECT 'tasks', k.id::text FROM tasks k JOIN file_objects f
  ON f.id IN (k.check_in_photo_file_id, k.check_in_app_screenshot_file_id) WHERE f.status = 'pending'
UNION ALL SELECT 'incident_reports', i.id::text FROM incident_reports i JOIN file_objects f
  ON f.id IN (i.map_photo_file_id, i.situation1_photo_file_id, i.situation2_photo_file_id) WHERE f.status = 'pending'
UNION ALL SELECT 'chat_messages', m.id::text FROM chat_messages m JOIN file_objects f ON f.id = m.image_file_id WHERE f.status = 'pending'
UNION ALL SELECT 'vehicle_expenses', e.id::text FROM vehicle_expenses e JOIN file_objects f
  ON f.id IN (e.receipt_file_id, e.odometer_file_id) WHERE f.status = 'pending'
UNION ALL SELECT 'statement_documents', d.statement_id::text FROM statement_documents d JOIN file_objects f ON f.id = d.file_id WHERE f.status = 'pending'
UNION ALL SELECT 'companies', c.id::text FROM companies c JOIN file_objects f
  ON f.id IN (c.logo_file_id, c.stamp_file_id, c.signature_file_id) WHERE f.status = 'pending'`

const inv10a = `SELECT 'tasks' AS t, k.id::text
FROM tasks k LEFT JOIN drivers d ON d.id = k.driver_id LEFT JOIN trucks tr ON tr.id = k.truck_id
WHERE k.tenant_source <> 'quarantine'
  AND ((tr.id IS NOT NULL AND tr.tenant_id <> k.tenant_id)
    OR (d.id IS NOT NULL AND d.tenant_id <> k.tenant_id AND NOT EXISTS (
          SELECT 1 FROM memberships m WHERE m.user_id = d.user_id AND m.tenant_id = k.tenant_id AND m.role = 'driver')))
UNION ALL
SELECT 'trip_records', t.id::text
FROM trip_records t LEFT JOIN drivers d ON d.id = t.driver_id LEFT JOIN trucks tr ON tr.id = t.truck_id
WHERE (tr.id IS NOT NULL AND tr.tenant_id <> t.tenant_id)
   OR (d.id IS NOT NULL AND d.tenant_id <> t.tenant_id AND NOT EXISTS (
         SELECT 1 FROM memberships m WHERE m.user_id = d.user_id AND m.tenant_id = t.tenant_id AND m.role = 'driver'))`

const inv10b = `SELECT table_name::text, column_name::text FROM information_schema.columns
WHERE table_schema = 'public' AND table_name IN
      ('scope_tasks','scope_trips','scope_standby','scope_incidents','scope_drivers','scope_trucks','scope_tenants')
  AND (column_name LIKE 'billing%' OR column_name LIKE '%_thb' OR column_name LIKE '%party_id'
       OR column_name IN ('evidence_token','id_card','id_card_file_id','license_file_id','birth_date','truck_license_id'))`

// sql10c runs as the dispatcher, the carrier tenant_admin and the customer-scope user ($1 = the principal's
// tenant, NULL for the customer): no cost, rate, payroll, expense or maintenance row of another tenant.
const sql10c = `SELECT 'trip_billing_snapshots' AS t, count(*)::text FROM trip_billing_snapshots HAVING count(*) > 0
UNION ALL SELECT 'customer_rate_entries', count(*)::text FROM customer_rate_entries HAVING count(*) > 0
UNION ALL SELECT 'payroll_runs', count(*)::text FROM payroll_runs WHERE tenant_id IS DISTINCT FROM $1::uuid HAVING count(*) > 0
UNION ALL SELECT 'vehicle_expenses', count(*)::text FROM vehicle_expenses WHERE tenant_id IS DISTINCT FROM $1::uuid HAVING count(*) > 0
UNION ALL SELECT 'maintenance_records', count(*)::text FROM maintenance_records WHERE tenant_id IS DISTINCT FROM $1::uuid HAVING count(*) > 0`

// sql10d runs as own-fleet operation staff ($1 = TN_TTP): the trip of the contractor NWR is in reach (R60),
// the tasks of TTP (no contractor link) are not.
const sql10d = `SELECT 'NWR trip ZXZB26072200102 must be in reach' AS problem
WHERE NOT EXISTS (SELECT 1 FROM trip_records WHERE trip_no = 'ZXZB26072200102')
UNION ALL
SELECT 'TTP tasks must be out of reach' FROM tasks WHERE tenant_id = $1::uuid HAVING count(*) > 0`

const inv11 = `SELECT 'tasks.plan_date' AS c, id::text FROM tasks WHERE plan_date <> bkk_date(plan_at)
UNION ALL SELECT 'rate.effective_from_date', id::text FROM customer_rate_entries WHERE effective_from_date <> bkk_date(effective_from_at)
UNION ALL SELECT 'fuel.effective_from_date', id::text FROM customer_fuel_rate_adjustments WHERE effective_from_date <> bkk_date(effective_from_at)
UNION ALL SELECT 'standby_rate.effective_from_date', id::text FROM standby_rate_entries WHERE effective_from_date <> bkk_date(effective_from_at)
UNION ALL SELECT 'payroll window', id::text FROM payroll_runs
  WHERE period_start <> bkk_midnight(bkk_date(period_start)) OR period_end <> bkk_midnight(bkk_date(period_end))
     OR (pay_round = 'R1' AND EXTRACT(DAY FROM bkk_date(period_end)) <> 16)
UNION ALL SELECT 'ledger date', id::text FROM transactions WHERE tx_date_source = 'bangkok' AND tx_date <> bkk_date(created_at)
UNION ALL SELECT 'switch-day round', t.trip_no FROM trip_records t JOIN trip_billing_snapshots s ON s.trip_id = t.id
  WHERE t.trip_no = 'ZXJB26081600104' AND s.round_effective_from_date IS DISTINCT FROM DATE '2026-08-16'`
