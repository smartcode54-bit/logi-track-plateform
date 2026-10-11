package etl

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// Driver compensation, penalties, payroll, the ledger, leave and holidays (Appendix A §A.3.10).

// wholeTHB is a NOT NULL whole-baht column (CHECK x = trunc(x)): absent -> def; a fraction or a bad value rejects.
func (c *docCtx) wholeTHB(field string, def int64) (int64, bool, error) {
	n, res := c.moneyCents(field, true)
	switch {
	case res == moneyAbsent:
		return def * 100, true, nil
	case res == moneyNegative:
		return 0, false, c.reject(field, ReasonNegativeMoney, "must be >= 0", c.get(field))
	case res != moneyOK || n%100 != 0:
		return 0, false, c.reject(field, ReasonBadNumber, "a whole-baht amount", c.get(field))
	}
	return n, true, nil
}

// loadCompensationConfig: driver_compensation_config -> driver_compensation_configs + penalty_types (§A.3.10):
// tiers as jsonb, sso{} into the sso_* columns, penaltyTypes[] into rows; effectiveFrom -> effective_from_at. The
// tenant is the own fleet (form, R61).
func loadCompensationConfig(c *docCtx) error {
	at := c.ts("effectiveFrom")
	if at == nil {
		return c.reject("effectiveFrom", ReasonMissingRequired, "effective_from_at is NOT NULL", c.get("effectiveFrom"))
	}
	r := newRow("driver_compensation_configs").set("tenant_id", c.ownFleet()).set("tenant_source", "form").set("effective_from_at", at)
	for _, x := range []struct {
		field, col string
		def        int64
	}{{"weekdayRateThb", "weekday_rate_thb", 300}, {"holidayRateThb", "holiday_rate_thb", 350}} {
		n, ok, err := c.wholeTHB(x.field, x.def)
		if !ok {
			return err
		}
		r.set(x.col, numeric2(n))
	}
	helper := int64(40000) // the form's default 400 THB
	if c.has("helperDayRateThb") {
		n, ok, err := c.requiredMoney("helperDayRateThb", "helper_day_rate_thb", true)
		if !ok {
			return err
		}
		helper = n
	}
	r.set("helper_day_rate_thb", numeric2(helper)).setPresent("pay_standby", c.boolean("payStandby")).
		setPresent("fuel_min_refuels_per_month", c.integer("fuelMinRefuelsPerMonth"))
	for f, col := range map[string]string{"fuelIncentiveTiers": "fuel_incentive_tiers", "tripVolumeTiers": "trip_volume_tiers"} {
		tiers, _ := plain(c.get(f)).([]any)
		if tiers == nil {
			tiers = []any{}
		}
		r.set(col, tiers)
	}
	if sso, ok := c.get("sso").(map[string]any); ok {
		s := c.sub(sso)
		r.setPresent("sso_rate_percent", s.float("ratePercent")).setPresent("sso_base_existing_thb", s.money("baseExistingThb", true)).
			setPresent("sso_base_new_thb", s.money("baseNewThb", true)).setPresent("sso_existing_hired_before_year", s.integer("existingHiredBeforeYear")).
			setPresent("sso_max_age_inclusive", s.integer("maxAgeInclusive")).setPresent("sso_probation_months", s.integer("probationMonths"))
		c.adopt(s, "sso.")
	}
	by, err := c.user("createdBy", c.str("createdBy"))
	if err != nil {
		return err
	}
	r.set("created_by", by).setPresent("created_at", c.ts("createdAt"))
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM penalty_types WHERE config_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	types, _ := c.get("penaltyTypes").([]any)
	seen := map[string]bool{}
	for i, t := range types {
		m, _ := t.(map[string]any)
		s := c.sub(m)
		code, en, th := s.str("code"), s.str("nameEn"), s.str("nameTh")
		amount, res := s.moneyCents("defaultAmountThb", true)
		if res == moneyAbsent {
			amount, res = 0, moneyOK
		}
		if code == "" || en == "" || th == "" || seen[code] || res != moneyOK {
			c.find(fmt.Sprintf("penaltyTypes[%d]", i), ReasonMissingRequired, "a penalty type needs a unique code, both names and an amount >= 0; not loaded", t)
			continue
		}
		seen[code] = true
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO penalty_types (config_id, code, name_en, name_th, default_amount_thb) VALUES ($1, $2, $3, $4, $5)`,
			id, code, en, th, numeric2(amount)); err != nil {
			return fmt.Errorf("etl: %s: penalty type: %w", c.doc.Path, err)
		}
	}
	c.tenantSource = tenancy.SourceForm
	c.done("driver_compensation_configs", id)
	return nil
}

// requireDriverOutsideQuarantine is the CHECK (driver_id IS NOT NULL OR tenant_source = 'quarantine') of the
// driver-keyed tables: only a re-applied row whose tenant is frozen gets here without a driver (main spec §13.5), and
// it stays as loaded before.
func (c *docCtx) requireDriverOutsideQuarantine(drv driverMatch) (bool, error) {
	if c.tenantSource != tenancy.SourceQuarantine && !drv.ok() {
		return false, c.reject("driverId", ReasonDriverUnresolved, "the row keeps its tenant and needs a driver there; fix at source and retry", c.get("driverId"))
	}
	return true, nil
}

// loadPenalty: driver_penalties (§A.3.10): driverId an auth uid; whole-baht totals; remainingThb > totalThb (the CHECK
// cannot hold) rejects the row with bad_number (fix at source).
func loadPenalty(c *docCtx) error {
	typeCode := c.text("typeCode")
	if typeCode == nil {
		return c.reject("typeCode", ReasonMissingRequired, "driver_penalties.type_code is NOT NULL", nil)
	}
	total, ok, err := c.requiredMoney("totalThb", "driver_penalties.total_thb", true)
	if !ok {
		return err
	}
	remaining, ok, err := c.requiredMoney("remainingThb", "driver_penalties.remaining_thb", true)
	if !ok {
		return err
	}
	if total%100 != 0 {
		return c.reject("totalThb", ReasonBadNumber, "a whole-baht amount", c.get("totalThb"))
	}
	if remaining > total {
		return c.reject("remainingThb", ReasonBadNumber, "remainingThb > totalThb; the CHECK cannot hold", c.get("remainingThb"))
	}
	instTotal, instPaid := int64(1), int64(0)
	if n := c.integer("installmentsTotal"); n != nil {
		instTotal = *n
	}
	if n := c.integer("installmentsPaid"); n != nil {
		instPaid = *n
	}
	if instTotal < 1 || instPaid < 0 || instPaid > instTotal {
		return c.reject("installmentsPaid", ReasonBadNumber, "0 <= installmentsPaid <= installmentsTotal, installmentsTotal >= 1", c.get("installmentsPaid"))
	}
	status, ok := c.vocab("status", "pending", "partially_deducted", "cleared", "cancelled")
	if !ok {
		return c.reject("status", ReasonStatusOutOfVocab, "no penalty status", c.get("status"))
	}
	if status == "" {
		status = "pending"
	}
	incurred := c.ts("incurredAt")
	if incurred == nil {
		return c.reject("incurredAt", ReasonMissingRequired, "driver_penalties.incurred_at is NOT NULL", c.get("incurredAt"))
	}
	drv, err := c.driverField("driverId")
	if err != nil {
		return err
	}
	tenant, err := c.stamp(tenancy.DriverPenalties, map[string]any{"driverId": drv.raw})
	if err != nil {
		return err
	}
	if ok, err := c.requireDriverOutsideQuarantine(drv); !ok {
		return err
	}
	by, err := c.user("createdBy", c.str("createdBy"))
	if err != nil {
		return err
	}
	id, err := c.rowID("driver_penalties")
	if err != nil {
		return err
	}
	r := newRow("driver_penalties").set("id", id).set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).
		set("driver_id", drv.idPtr()).set("legacy_driver_ref", drv.rawPtr()).set("driver_ref_match", drv.match).set("type_code", typeCode).
		set("type_name_snapshot", c.text("typeName")).set("total_thb", numeric2(total)).set("remaining_thb", numeric2(remaining)).
		set("installments_total", instTotal).set("installments_paid", instPaid).set("reason", c.text("reason")).set("status", status).
		set("incurred_at", incurred).set("created_by", by)
	if err := c.files(r, id, tenant, "penalty", [][3]string{{"evidenceUrl", "penalty_evidence", "evidence_file_id"}}); err != nil {
		return err
	}
	c.times(r)
	if _, err := upsertLegacy(c, r); err != nil {
		return err
	}
	c.done("driver_penalties", id)
	return nil
}

var payrollDocID = regexp.MustCompile(`^(.+)_([0-9]{4}-(?:0[1-9]|1[0-2]))_(R[12])$`)

// loadPayroll: payroll/{authUid}_{YYYY-MM}_{R1|R2} -> payroll_runs + payroll_line_items + payroll_penalty_applications
// (§A.3.10): the doc id gives pay_period / pay_round (the fields are cross-checked; a disagreement keeps the doc id's
// values and is reported), the driver is keyed by driver_id (fixes driverCompensation.ts:199-204), status
// lower_snake, lineItems[] -> line items (line_no = array index), penaltyApplications[] -> applications. The line
// items of a run that has left draft are frozen (trg_payroll_lines_frozen), so the run is written as draft, its lines
// replaced, and its final status set last, all in the load transaction (a non-draft run's updated_at is the load
// time). ledgerTransactionId is linked after the transactions load (afterTransactions).
func loadPayroll(c *docCtx) error {
	m := payrollDocID.FindStringSubmatch(c.doc.ID)
	period, round := c.str("payPeriod"), c.str("round")
	switch {
	case m != nil:
		if period != "" && period != m[2] {
			c.find("payPeriod", ReasonBadNumber, "differs from the doc id; the doc id's period is kept", period)
		}
		if round != "" && round != m[3] {
			c.find("round", ReasonBadNumber, "differs from the doc id; the doc id's round is kept", round)
		}
		period, round = m[2], m[3]
	case !monthKey.MatchString(period) || (round != "R1" && round != "R2"):
		return c.reject("payPeriod", ReasonMissingRequired, "neither the doc id nor the fields give pay_period and pay_round", c.doc.ID)
	}
	start, end := c.ts("periodStart"), c.ts("periodEnd")
	if start == nil || end == nil {
		return c.reject("periodStart", ReasonMissingRequired, "period_start and period_end are NOT NULL", nil)
	}
	if !end.After(*start) {
		return c.reject("periodEnd", ReasonBadTimestamp, "period_end must be after period_start", c.get("periodEnd"))
	}
	status, ok := c.vocab("status", "draft", "pending_approval", "approved", "paid", "cancelled")
	if !ok {
		return c.reject("status", ReasonStatusOutOfVocab, "no payroll status", c.get("status"))
	}
	if status == "" {
		status = "draft"
	}
	ref := c.str("driverId")
	if ref == "" && m != nil {
		ref = m[1]
	}
	drv, err := c.driverByRef(ref, "")
	if err != nil {
		return err
	}
	if drv.raw != "" && !drv.ok() {
		c.find("driverId", ReasonDriverUnresolved, "matches no drivers.legacy_doc_id or legacy_auth_uid", drv.raw)
	}
	tenant, err := c.stamp(tenancy.Payroll, map[string]any{"driverId": drv.raw})
	if err != nil {
		return err
	}
	if ok, err := c.requireDriverOutsideQuarantine(drv); !ok {
		return err
	}
	if drv.ok() {
		var other string
		err := c.tx.QueryRow(c.ctx, `SELECT coalesce(legacy_doc_id, id::text) FROM payroll_runs WHERE driver_id = $1 AND pay_period = $2
			AND pay_round = $3 AND legacy_doc_id IS DISTINCT FROM $4`, drv.id, period, round, c.doc.ID).Scan(&other)
		if err == nil {
			return c.reject("driverId", ReasonDuplicateNaturalKey, "the driver's run of this period and round is already "+other, c.doc.ID)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
	}
	if cur := c.str("currency"); cur != "" && cur != "THB" {
		c.find("currency", ReasonStatusOutOfVocab, "payroll is in THB; THB kept", cur)
	}
	approver, err := c.user("approvedBy", c.str("approvedBy"))
	if err != nil {
		return err
	}
	r := newRow("payroll_runs").set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).set("driver_id", drv.idPtr()).
		set("legacy_driver_ref", drv.rawPtr()).set("driver_ref_match", drv.match).set("pay_period", period).set("pay_round", round).
		set("period_start", start).set("period_end", end).set("status", "draft").
		setPresent("total_earnings_thb", c.money("totalEarnings", false)).setPresent("total_deductions_thb", c.money("totalDeductions", false)).
		setPresent("net_pay_thb", c.money("netPay", false)).set("payment_date", c.ts("paymentDate")).set("payment_method", c.text("paymentMethod")).
		set("remarks", c.text("remarks")).set("approved_by", approver).set("approved_at", c.ts("approvedAt"))
	c.times(r)
	if _, err := c.tx.Exec(c.ctx, `UPDATE payroll_runs SET status = 'draft' WHERE legacy_doc_id = $1 AND status <> 'draft'`, c.doc.ID); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	if err := c.payrollLines(id); err != nil {
		return err
	}
	if status != "draft" {
		if _, err := c.tx.Exec(c.ctx, `UPDATE payroll_runs SET status = $2 WHERE id = $1`, id, status); err != nil {
			return fmt.Errorf("etl: %s: status: %w", c.doc.Path, err)
		}
	}
	c.done("payroll_runs", id)
	return nil
}

// payrollLines replaces the line items and penalty applications of a draft run.
func (c *docCtx) payrollLines(id uuid.UUID) error {
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM payroll_line_items WHERE payroll_run_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	lines, _ := c.get("lineItems").([]any)
	for i, l := range lines {
		m, _ := l.(map[string]any)
		s := c.sub(m)
		field := fmt.Sprintf("lineItems[%d]", i)
		typ, ok := lowerVocab(m["type"], "earning", "deduction")
		name, cat := s.text("name"), s.text("category")
		amount, res := s.moneyCents("amount", false)
		if !ok || typ == "" || name == nil || cat == nil || res != moneyOK {
			c.find(field, ReasonMissingRequired, "a line item needs type EARNING|DEDUCTION, category, name and amount; not loaded", l)
			continue
		}
		unit := s.money("unitRateThb", false)
		if unit == nil {
			unit = s.money("unitRate", false)
		}
		var meta any
		if v, ok := m["meta"]; ok && v != nil {
			meta = plain(v)
		}
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO payroll_line_items (payroll_run_id, line_no, item_type, category, name, amount_thb,
			description, reference_id, quantity, unit_rate_thb, meta) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			id, i, typ, *cat, *name, numeric2(amount), s.text("description"), s.text("referenceId"), s.float("quantity"), unit, meta); err != nil {
			return fmt.Errorf("etl: %s: line item: %w", c.doc.Path, err)
		}
		c.adopt(s, field+".")
	}
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM payroll_penalty_applications WHERE payroll_run_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	apps, _ := c.get("penaltyApplications").([]any)
	for i, a := range apps {
		m, _ := a.(map[string]any)
		s := c.sub(m)
		pid, err := c.legacyRef("driver_penalties", s.str("penaltyId"))
		if err != nil {
			return err
		}
		applied, res := s.moneyCents("appliedThb", true)
		if pid == nil || res != moneyOK {
			c.find(fmt.Sprintf("penaltyApplications[%d]", i), ReasonMissingRequired, "the penalty did not load or appliedThb is not >= 0; not loaded", a)
			continue
		}
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO payroll_penalty_applications (payroll_run_id, penalty_id, applied_thb) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, id, *pid, numeric2(applied)); err != nil {
			return fmt.Errorf("etl: %s: penalty application: %w", c.doc.Path, err)
		}
	}
	return nil
}

// loadTransaction: the append-only ledger (§A.3.10), two legacy shapes. Renewal {truckId, type tax|insurance,
// subType, amount, paymentMethod, date 'yyyy-MM-dd', receiptUrl, performedBy display/email, notes}: tenant from the
// truck, tx_date_source bangkok. Payout {type driver_payout, date UTC yyyy-MM-dd, driverId auth uid, payoutId,
// payPeriod, round, performedBy uid}: the billing carrier's (the own fleet, form, R61), tx_date_source legacy_utc
// (date kept as written, divergence #11), payroll_run_id by the payroll doc id. A re-applied document leaves its row.
func loadTransaction(c *docCtx) error {
	typ, ok := lowerVocab(c.get("type"), "tax", "insurance", "driver_payout")
	switch {
	case !ok:
		return c.reject("type", ReasonStatusOutOfVocab, "neither tax, insurance nor driver_payout", c.get("type"))
	case typ == "":
		return c.reject("type", ReasonMissingRequired, "transactions.tx_type is NOT NULL", nil)
	}
	amount, ok, err := c.requiredMoney("amount", "transactions.amount_thb", false)
	if !ok {
		return err
	}
	date := c.date("date")
	if date == nil {
		return c.reject("date", ReasonMissingRequired, "transactions.tx_date is NOT NULL", c.get("date"))
	}
	var existing, curTenant uuid.UUID
	var curSource string
	err = c.tx.QueryRow(c.ctx, `SELECT id, tenant_id, tenant_source FROM transactions WHERE legacy_doc_id = $1`, c.doc.ID).
		Scan(&existing, &curTenant, &curSource)
	switch {
	case err == nil:
		// Immutable ledger row (transactions_immutable): the document already loaded and keeps its row.
		c.tenantSource = tenancy.Source(curSource)
		if curTenant == c.e.quar {
			c.outcome = Quarantined
			c.find("", ReasonTenantUnresolved, "the ledger row stays in the quarantine tenant (immutable: corrected by a new entry, never moved)", nil)
		}
		c.done("transactions", existing)
		return nil
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	r := newRow("transactions").set("legacy_doc_id", c.doc.ID).set("tx_type", typ).set("sub_type", c.text("subType")).
		set("amount_thb", numeric2(amount)).set("payment_method", c.text("paymentMethod")).set("tx_date", date).set("notes", c.text("notes")).
		setPresent("created_at", c.ts("createdAt"))
	if typ == "driver_payout" {
		period, round := c.str("payPeriod"), c.str("round")
		if !monthKey.MatchString(period) || (round != "R1" && round != "R2") {
			return c.reject("payPeriod", ReasonMissingRequired, "a payout needs payPeriod YYYY-MM and round R1|R2", nil)
		}
		drv, err := c.driverField("driverId")
		if err != nil {
			return err
		}
		run, err := c.legacyRef("payroll_runs", c.str("payoutId"))
		if err != nil {
			return err
		}
		by, err := c.user("performedBy", c.str("performedBy"))
		if err != nil {
			return err
		}
		cur, found, err := c.stampedRow("transactions")
		if err != nil {
			return err
		}
		tenant := c.settle(cur, found, c.ownFleet(), tenancy.SourceForm, "")
		r.set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).set("tx_date_source", "legacy_utc").
			set("driver_id", drv.idPtr()).set("legacy_driver_ref", drv.rawPtr()).set("driver_ref_match", drv.match).
			set("payroll_run_id", run).set("pay_period", period).set("pay_round", round).set("performed_by_user_id", by)
	} else {
		tr, err := c.truck("truckId")
		if err != nil {
			return err
		}
		tenant, err := c.stamp(tenancy.Transactions, map[string]any{"truckId": c.str("truckId")})
		if err != nil {
			return err
		}
		r.set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).set("tx_date_source", "bangkok").
			set("truck_id", refID(tr)).set("performed_by_label", c.text("performedBy"))
		owner, ownerID := "", uuid.Nil
		if tr.ok() {
			owner, ownerID = "truck", tr.id
		}
		fid, err := c.fileRef("receiptUrl", c.get("receiptUrl"), "truck_receipt", owner, ownerID, tenant)
		if err != nil {
			return err
		}
		r.set("receipt_file_id", fid)
	}
	var id uuid.UUID
	if err := c.tx.QueryRow(c.ctx, r.insertSQL()+` RETURNING id`, r.vals...).Scan(&id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	c.done("transactions", id)
	return nil
}

// afterTransactions links payroll_runs.ledger_transaction_id (ledgerTransactionId, by transactions doc id) for the
// payroll or transaction documents this run applied.
func afterTransactions(r *runState, _ string) error {
	runs, err := r.appliedDocsOr("payroll", []string{"ledgerTransactionId"}, docIDs(r, "transactions"))
	if err != nil {
		return err
	}
	for _, id := range sortedDocKeys(runs) {
		ref, _ := runs[id]["ledgerTransactionId"].(string)
		if _, err := r.tx.Exec(r.ctx, `UPDATE payroll_runs p SET ledger_transaction_id = t.id
			FROM (SELECT (SELECT id FROM transactions WHERE legacy_doc_id = nullif($2, '')) AS id) t
			WHERE p.legacy_doc_id = $1 AND p.ledger_transaction_id IS DISTINCT FROM t.id`, id, strings.TrimSpace(ref)); err != nil {
			return fmt.Errorf("payroll %s: ledger transaction: %w", id, err)
		}
	}
	return nil
}

// loadLeaveRequest: leave_requests + leave_request_attachments (§A.3.10): the driver by doc id first, then auth uid
// (schemas report UNCERTAIN); start_date / end_date := bkk_date(...); lower vocabularies; approverId -> user,
// approvedAt -> decided_at; attachments[] -> files (leave_evidence).
func loadLeaveRequest(c *docCtx) error {
	start, end := c.date("startDate"), c.date("endDate")
	if start == nil || end == nil {
		return c.reject("startDate", ReasonMissingRequired, "start_date and end_date are NOT NULL", nil)
	}
	if end.Before(*start) {
		return c.reject("endDate", ReasonBadTimestamp, "end_date before start_date; the CHECK cannot hold", c.get("endDate"))
	}
	typ, ok := c.vocab("type", "sick", "business")
	if !ok {
		return c.reject("type", ReasonStatusOutOfVocab, "neither SICK nor BUSINESS", c.get("type"))
	}
	if typ == "" {
		typ = "sick"
	}
	status, ok := c.vocab("status", "pending", "approved", "rejected", "cancelled")
	if !ok {
		return c.reject("status", ReasonStatusOutOfVocab, "no leave status", c.get("status"))
	}
	if status == "" {
		status = "pending"
	}
	drv, err := c.driverField("driverId")
	if err != nil {
		return err
	}
	tenant, err := c.stamp(tenancy.LeaveRequests, map[string]any{"driverId": drv.raw})
	if err != nil {
		return err
	}
	if ok, err := c.requireDriverOutsideQuarantine(drv); !ok {
		return err
	}
	approver, err := c.user("approverId", c.str("approverId"))
	if err != nil {
		return err
	}
	reason := ""
	if s := c.text("reason"); s != nil {
		reason = *s
	}
	r := newRow("leave_requests").set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).set("driver_id", drv.idPtr()).
		set("legacy_driver_ref", drv.rawPtr()).set("driver_ref_match", drv.match).set("leave_type", typ).set("status", status).
		set("start_date", start).set("end_date", end).set("reason", reason).set("approver_user_id", approver).
		set("decided_at", c.ts("approvedAt")).set("rejection_reason", c.text("rejectionReason"))
	c.times(r)
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM leave_request_attachments WHERE leave_request_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	list, _ := c.get("attachments").([]any)
	for i, u := range list {
		fid, err := c.fileRef(fmt.Sprintf("attachments[%d]", i), u, "leave_evidence", "leave", id, tenant)
		if err != nil {
			return err
		}
		if fid == nil {
			continue
		}
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO leave_request_attachments (leave_request_id, file_id, position) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, id, *fid, i); err != nil {
			return fmt.Errorf("etl: %s: attachment: %w", c.doc.Path, err)
		}
	}
	c.done("leave_requests", id)
	return nil
}

// loadHoliday: holidays (§A.3.10): holiday_date := bkk_date(date); type PUBLIC -> tenant NULL, COMPANY / OTHER -> the
// own fleet; lower vocabularies; the id field saveHoliday writes into the doc is ignored. A second document of one
// (tenant, date, type) is rejected with duplicate_natural_key (the upsert key of holidays.ts:98-113).
func loadHoliday(c *docCtx) error {
	day := c.date("date")
	if day == nil {
		return c.reject("date", ReasonMissingRequired, "holidays.holiday_date is NOT NULL", c.get("date"))
	}
	name := c.text("name", "holidayNameTH", "holidayNameEN")
	if name == nil {
		return c.reject("name", ReasonMissingRequired, "holidays.name is NOT NULL", nil)
	}
	typ, ok := c.vocab("type", "public", "company", "other")
	if !ok {
		return c.reject("type", ReasonStatusOutOfVocab, "neither PUBLIC, COMPANY nor OTHER", c.get("type"))
	}
	if typ == "" {
		typ = "public"
	}
	status, ok := c.vocab("status", "draft", "published", "archived")
	if !ok {
		return c.reject("status", ReasonStatusOutOfVocab, "neither DRAFT, PUBLISHED nor ARCHIVED", c.get("status"))
	}
	if status == "" {
		status = "draft"
	}
	var tenant *uuid.UUID
	if typ != "public" {
		t := c.ownFleet()
		tenant = &t
	}
	var other string
	err := c.tx.QueryRow(c.ctx, `SELECT coalesce(legacy_doc_id, id::text) FROM holidays WHERE tenant_id IS NOT DISTINCT FROM $1
		AND holiday_date = $2 AND holiday_type = $3 AND legacy_doc_id IS DISTINCT FROM $4`, tenant, *day, typ, c.doc.ID).Scan(&other)
	if err == nil {
		return c.reject("date", ReasonDuplicateNaturalKey, "the day already has a "+typ+" holiday ("+other+")", c.get("date"))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	by, err := c.user("createdBy", c.str("createdBy"))
	if err != nil {
		return err
	}
	r := newRow("holidays").set("tenant_id", tenant).set("holiday_date", day).set("holiday_type", typ).set("status", status).
		set("name", name).set("name_en", c.text("holidayNameEN")).set("name_th", c.text("holidayNameTH")).
		set("description", c.text("description")).set("description_en", c.text("descriptionEn")).set("description_th", c.text("descriptionTh")).
		set("is_recurring", c.isTrue("isRecurring")).set("created_by", by)
	c.times(r)
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	c.done("holidays", id)
	return nil
}
