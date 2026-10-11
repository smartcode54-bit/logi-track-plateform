-- HR: compensation config versions, penalties and payroll runs (T44, Appendix B §B.2.17; Appendix A §A.2.5; main spec
-- §6.15). Every table is a tenant table (G-tenant, Appendix C §C.3.0; penalty_types, payroll_line_items and
-- payroll_penalty_applications follow their parent): staff read and write the rows of their tenant reach (own tenant
-- and the carriers working for it), never a scope principal's. Request statements run in db.WithPrincipal and also name
-- the reach (main spec §4.7, Appendix C §C.3.8; authz.Principal.TenantReach); the payroll.run consumer runs in
-- db.WithSystem with the job's tenant and names that tenant.
--
-- Lock order: a run (FOR UPDATE, or the row lock of the generator's upsert), then the penalties it applies (in id
-- order). The generator takes the advisory lock of (tenant, period, round) first and never locks a penalty, so an
-- approval and a regeneration never wait for each other in reverse.

-- name: ListConfigs :many
-- GET /v1/payroll/config: every version of the tenant (append-only, a few a year), newest effective first.
SELECT * FROM driver_compensation_configs
WHERE (sqlc.narg(tenant_id)::uuid IS NULL OR tenant_id = sqlc.narg(tenant_id)::uuid)
  AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
ORDER BY effective_from_at DESC, id DESC;

-- name: ConfigAt :one
-- The version in effect at an instant: the latest effective_from_at <= at (a later-created version wins a tie).
SELECT * FROM driver_compensation_configs
WHERE tenant_id = sqlc.arg(tenant_id) AND effective_from_at <= sqlc.arg(at)
ORDER BY effective_from_at DESC, id DESC
LIMIT 1;

-- name: EarliestConfig :one
-- The fallback of a run before the first effective instant (fn:driverCompensation.ts:99-102).
SELECT * FROM driver_compensation_configs
WHERE tenant_id = sqlc.arg(tenant_id)
ORDER BY effective_from_at ASC, id ASC
LIMIT 1;

-- name: HasConfig :one
SELECT EXISTS (SELECT 1 FROM driver_compensation_configs WHERE tenant_id = sqlc.arg(tenant_id))::boolean;

-- name: ListPenaltyTypes :many
SELECT * FROM penalty_types
WHERE config_id = ANY (sqlc.arg(config_ids)::uuid[])
ORDER BY config_id, code;

-- name: InsertConfig :one
-- POST /v1/payroll/config: a new effective-dated version (history is never edited).
INSERT INTO driver_compensation_configs (tenant_id, tenant_source, effective_from_at, weekday_rate_thb, holiday_rate_thb,
  pay_standby, helper_day_rate_thb, fuel_incentive_tiers, fuel_min_refuels_per_month, trip_volume_tiers, sso_rate_percent,
  sso_base_existing_thb, sso_base_new_thb, sso_existing_hired_before_year, sso_max_age_inclusive, sso_probation_months,
  created_by)
VALUES (sqlc.arg(tenant_id), 'form', sqlc.arg(effective_from_at), sqlc.arg(weekday_rate_thb), sqlc.arg(holiday_rate_thb),
  sqlc.arg(pay_standby), sqlc.arg(helper_day_rate_thb), sqlc.arg(fuel_incentive_tiers), sqlc.arg(fuel_min_refuels_per_month),
  sqlc.arg(trip_volume_tiers), sqlc.arg(sso_rate_percent), sqlc.arg(sso_base_existing_thb), sqlc.arg(sso_base_new_thb),
  sqlc.arg(sso_existing_hired_before_year), sqlc.arg(sso_max_age_inclusive), sqlc.arg(sso_probation_months),
  sqlc.narg(created_by))
RETURNING *;

-- name: InsertPenaltyType :exec
INSERT INTO penalty_types (config_id, code, name_en, name_th, default_amount_thb)
VALUES (sqlc.arg(config_id), sqlc.arg(code), sqlc.arg(name_en), sqlc.arg(name_th), sqlc.arg(default_amount_thb));

-- name: ListPenalties :many
-- GET /v1/penalties: keyset (incurred_at, id), newest first; driver_name follows lib/driverName.ts.
SELECT sqlc.embed(p),
       coalesce(nullif(btrim(d.full_name_th), ''), nullif(btrim(d.first_name || ' ' || d.last_name), ''), '')::text
         AS driver_name,
       ev.object_key AS evidence_key
FROM driver_penalties p
LEFT JOIN drivers d ON d.id = p.driver_id
LEFT JOIN file_objects ev ON ev.id = p.evidence_file_id
WHERE (sqlc.arg(all_tenants)::boolean OR p.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(driver_id)::uuid IS NULL OR p.driver_id = sqlc.narg(driver_id)::uuid)
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR p.status = ANY (sqlc.arg(statuses)::text[]))
  AND (sqlc.narg(after_at)::timestamptz IS NULL
       OR (p.incurred_at, p.id) < (sqlc.narg(after_at)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY p.incurred_at DESC, p.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetPenalty :one
SELECT sqlc.embed(p),
       coalesce(nullif(btrim(d.full_name_th), ''), nullif(btrim(d.first_name || ' ' || d.last_name), ''), '')::text
         AS driver_name,
       ev.object_key AS evidence_key
FROM driver_penalties p
LEFT JOIN drivers d ON d.id = p.driver_id
LEFT JOIN file_objects ev ON ev.id = p.evidence_file_id
WHERE p.id = sqlc.arg(id);

-- name: GetDriver :one
-- The driver of a new penalty, in the caller's reach: its tenant stamps the row (tenant_source 'driver').
SELECT id, tenant_id FROM drivers
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]));

-- name: InsertPenalty :one
INSERT INTO driver_penalties (id, tenant_id, tenant_source, driver_id, type_code, type_name_snapshot, total_thb, remaining_thb,
  installments_total, installments_paid, reason, evidence_file_id, status, incurred_at, created_by)
VALUES (sqlc.arg(id), sqlc.arg(tenant_id), 'driver', sqlc.arg(driver_id), sqlc.arg(type_code), sqlc.narg(type_name),
  sqlc.arg(total_thb), sqlc.arg(total_thb), sqlc.arg(installments_total), 0, sqlc.narg(reason), sqlc.narg(evidence_file_id),
  'pending', sqlc.arg(incurred_at), sqlc.narg(created_by))
RETURNING *;

-- name: LockPenalty :one
SELECT * FROM driver_penalties
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
FOR UPDATE;

-- name: CancelPenalty :exec
UPDATE driver_penalties SET status = 'cancelled' WHERE id = sqlc.arg(id);

-- name: ListRuns :many
-- GET /v1/payroll: keyset (period_end, id), newest first.
SELECT sqlc.embed(r),
       coalesce(nullif(btrim(d.full_name_th), ''), nullif(btrim(d.first_name || ' ' || d.last_name), ''), '')::text
         AS driver_name
FROM payroll_runs r
LEFT JOIN drivers d ON d.id = r.driver_id
WHERE (sqlc.arg(all_tenants)::boolean OR r.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(pay_period)::text IS NULL OR r.pay_period = sqlc.narg(pay_period)::text)
  AND (sqlc.narg(pay_round)::text IS NULL OR r.pay_round = sqlc.narg(pay_round)::text)
  AND (sqlc.narg(status)::text IS NULL OR r.status = sqlc.narg(status)::text)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR r.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(after_end)::timestamptz IS NULL
       OR (r.period_end, r.id) < (sqlc.narg(after_end)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY r.period_end DESC, r.id DESC
LIMIT sqlc.arg(row_limit);

-- name: CountRunsByStatus :many
-- meta.statusCounts of the first page: every run matching the other filters, per status (the page's summary cards).
SELECT r.status, count(*)::bigint AS n
FROM payroll_runs r
WHERE (sqlc.arg(all_tenants)::boolean OR r.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(pay_period)::text IS NULL OR r.pay_period = sqlc.narg(pay_period)::text)
  AND (sqlc.narg(pay_round)::text IS NULL OR r.pay_round = sqlc.narg(pay_round)::text)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR r.driver_id = sqlc.narg(driver_id)::uuid)
GROUP BY r.status;

-- name: GetRun :one
SELECT sqlc.embed(r),
       coalesce(nullif(btrim(d.full_name_th), ''), nullif(btrim(d.first_name || ' ' || d.last_name), ''), '')::text
         AS driver_name
FROM payroll_runs r
LEFT JOIN drivers d ON d.id = r.driver_id
WHERE r.id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR r.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]));

-- name: ListRunLines :many
SELECT * FROM payroll_line_items WHERE payroll_run_id = sqlc.arg(run_id) ORDER BY line_no;

-- name: ListRunApplications :many
SELECT * FROM payroll_penalty_applications WHERE payroll_run_id = sqlc.arg(run_id) ORDER BY penalty_id;

-- name: LockRun :one
SELECT * FROM payroll_runs
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
FOR UPDATE;

-- name: LockPenalties :many
-- The penalties an approval commits, locked in id order.
SELECT * FROM driver_penalties WHERE id = ANY (sqlc.arg(ids)::uuid[]) ORDER BY id FOR UPDATE;

-- name: ApplyPenalty :exec
-- One installment of an approved run (fn:driverCompensation.ts:363-376): the balance never below zero, the count at
-- most installments_total (the CHECK; a balance carried past the last installment keeps deducting), cleared at zero.
UPDATE driver_penalties
SET remaining_thb     = GREATEST(0, remaining_thb - sqlc.arg(applied_thb)::numeric),
    installments_paid = LEAST(installments_paid + 1, installments_total),
    status            = CASE WHEN remaining_thb - sqlc.arg(applied_thb)::numeric <= 0 THEN 'cleared' ELSE 'partially_deducted' END
WHERE id = sqlc.arg(id);

-- name: InsertPayout :one
-- The payout ledger row of an approval: dated on the Bangkok calendar of the approval (fixes the UTC date of
-- fn:driverCompensation.ts:384, §6.18 #11); created_at is the same transaction instant.
INSERT INTO transactions (tenant_id, tenant_source, tx_type, sub_type, amount_thb, tx_date, tx_date_source, driver_id,
  payroll_run_id, pay_period, pay_round, performed_by_user_id)
VALUES (sqlc.arg(tenant_id), 'driver', 'driver_payout', 'Driver Compensation', sqlc.arg(amount_thb), bkk_date(now()), 'bangkok',
  sqlc.narg(driver_id), sqlc.arg(run_id), sqlc.arg(pay_period), sqlc.arg(pay_round), sqlc.narg(performed_by))
RETURNING id, tx_date;

-- name: ApproveRun :exec
UPDATE payroll_runs
SET status = 'approved', approved_by = sqlc.narg(approved_by), approved_at = now(), ledger_transaction_id = sqlc.arg(ledger_id)
WHERE id = sqlc.arg(id);

-- name: SetRunStatus :exec
-- POST /v1/payroll/{id}/status: paid stamps the payment date (now unless given); a method or remarks are kept unless
-- given.
UPDATE payroll_runs
SET status         = sqlc.arg(status),
    payment_date   = CASE WHEN sqlc.arg(status) = 'paid' THEN coalesce(sqlc.narg(payment_date), now()) ELSE payment_date END,
    payment_method = coalesce(sqlc.narg(payment_method), payment_method),
    remarks        = coalesce(sqlc.narg(remarks), remarks)
WHERE id = sqlc.arg(id);

-- --- the payroll.run consumer (db.WithSystem, the job's tenant) ------------------------------------------------

-- name: LockPayrollPeriod :exec
-- Serialises two generations of one (tenant, period, round) even when lock:job has expired.
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(key)::text, 0));

-- name: ListPayrollDrivers :many
-- Every driver of the tenant gets a run (keyed by drivers.id, §6.18 #21; legacy skipped drivers without an auth uid).
SELECT id, hire_date, birth_date, probation_passed FROM drivers WHERE tenant_id = sqlc.arg(tenant_id) ORDER BY id;

-- name: ListHolidayDates :many
-- The holidays visible to the tenant: public (tenant_id NULL) and its own, every type and status (legacy read the whole
-- collection, fn:driverCompensation.ts:108-115).
SELECT DISTINCT holiday_date FROM holidays WHERE tenant_id IS NULL OR tenant_id = sqlc.arg(tenant_id) ORDER BY holiday_date;

-- name: ListMonthTrips :many
-- Delivered trips of the drivers in the month by delivered_at, whatever tenant ran them.
SELECT driver_id::uuid AS driver_id, delivered_at::timestamptz AS delivered_at, is_multi_delivery
FROM trip_records
WHERE status = 'delivered' AND driver_id = ANY (sqlc.arg(driver_ids)::uuid[])
  AND delivered_at >= sqlc.arg(from_at) AND delivered_at < sqlc.arg(to_at)
ORDER BY driver_id, delivered_at, id;

-- name: ListOwnTasks :many
-- The drivers' own tasks planned in the widened round (tasks.driver_id: the fix of fn:driverCompensation.ts:199-204,
-- which matched the auth uid), every status as the legacy query read them.
SELECT driver_id::uuid AS driver_id, check_in_at, plan_at
FROM tasks
WHERE driver_id = ANY (sqlc.arg(driver_ids)::uuid[]) AND plan_at >= sqlc.arg(from_at) AND plan_at < sqlc.arg(to_at)
ORDER BY driver_id, plan_at, id;

-- name: ListHelperTasks :many
-- The tasks the drivers rode on as the helper (helper_driver_id, ADR 0011), same window.
SELECT helper_driver_id::uuid AS driver_id, check_in_at, plan_at
FROM tasks
WHERE helper_driver_id = ANY (sqlc.arg(driver_ids)::uuid[]) AND plan_at >= sqlc.arg(from_at) AND plan_at < sqlc.arg(to_at)
ORDER BY helper_driver_id, plan_at, id;

-- name: ListOpenPenalties :many
-- Open penalties in deduction order (incurred_at, id; legacy: Firestore document order).
SELECT id, driver_id::uuid AS driver_id, type_code, type_name_snapshot, total_thb, remaining_thb, installments_total,
       installments_paid
FROM driver_penalties
WHERE driver_id = ANY (sqlc.arg(driver_ids)::uuid[]) AND status IN ('pending', 'partially_deducted')
ORDER BY driver_id, incurred_at, id;

-- name: UpsertDraftRun :one
-- A driver's run of the round: created as draft, or rewritten while it is still draft in the same tenant (created_at
-- kept); a run that left draft (or belongs to another tenant) returns no row and is left alone.
INSERT INTO payroll_runs (tenant_id, tenant_source, driver_id, pay_period, pay_round, period_start, period_end, status,
  total_earnings_thb, total_deductions_thb, net_pay_thb)
VALUES (sqlc.arg(tenant_id), 'driver', sqlc.arg(driver_id), sqlc.arg(pay_period), sqlc.arg(pay_round), sqlc.arg(period_start),
  sqlc.arg(period_end), 'draft', sqlc.arg(total_earnings_thb), sqlc.arg(total_deductions_thb), sqlc.arg(net_pay_thb))
ON CONFLICT (driver_id, pay_period, pay_round) DO UPDATE
SET period_start         = EXCLUDED.period_start,
    period_end           = EXCLUDED.period_end,
    total_earnings_thb   = EXCLUDED.total_earnings_thb,
    total_deductions_thb = EXCLUDED.total_deductions_thb,
    net_pay_thb          = EXCLUDED.net_pay_thb
WHERE payroll_runs.status = 'draft' AND payroll_runs.tenant_id = EXCLUDED.tenant_id
RETURNING id, (xmax = 0)::boolean AS inserted;

-- name: DeleteRunLines :exec
DELETE FROM payroll_line_items WHERE payroll_run_id = sqlc.arg(run_id);

-- name: InsertRunLine :batchexec
INSERT INTO payroll_line_items (payroll_run_id, line_no, item_type, category, name, amount_thb, description, reference_id,
  quantity, unit_rate_thb, meta)
VALUES (sqlc.arg(run_id), sqlc.arg(line_no), sqlc.arg(item_type), sqlc.arg(category), sqlc.arg(name), sqlc.arg(amount_thb),
  sqlc.narg(description), sqlc.narg(reference_id), sqlc.narg(quantity), sqlc.narg(unit_rate_thb), sqlc.narg(meta));

-- name: DeleteRunApplications :exec
DELETE FROM payroll_penalty_applications WHERE payroll_run_id = sqlc.arg(run_id);

-- name: InsertRunApplication :batchexec
INSERT INTO payroll_penalty_applications (payroll_run_id, penalty_id, applied_thb)
VALUES (sqlc.arg(run_id), sqlc.arg(penalty_id), sqlc.arg(applied_thb));
