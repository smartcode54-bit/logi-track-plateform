package etl

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// The HR cut-over checks of `etl reconcile --hr` (T46, issue #66; main spec §13.9 step 3 for the hr domain, §13.10).
// They run after the common checks, against the same dump, before `hr` joins PG_OWNED_DOMAINS and its web flag
// flips: the operator dumps everything, applies the changes at source since the P1 load (`load --since=watermark`) and
// reconciles. A payroll run, a penalty or a compensation config version changed in Firestore after its last load and
// not loaded again is caught here (the counts check sees only that the document was loaded once). Every check reads;
// nothing is written or locked.
//
//   - hr_inputs: every collection of HRCollections is in the dump's manifest (a narrow dump would leave its changes at
//     source unloaded and unchecked);
//   - hr_config: every loaded compensation config version against its document: the effective instant, the rates,
//     the incentive minimum, the social-security rule and the number of valid penalty types (the fields the document
//     holds; an absent field took its column default at the load);
//   - payroll_earnings, payroll_deductions, payroll_net: the totals of the loaded runs per (pay period, round,
//     status), exact in satang (the run's own totals, not recomputed: legacy runs are history, §6.18);
//   - payroll_ledger: the approved and paid runs whose document names its payout ledger row, against the runs linked
//     to one (the link is made after the ledger loads; a ledger document left in quarantine leaves the run unlinked);
//   - penalty_total, penalty_remaining, penalty_installments: the loaded penalties per status, exact.
//
// --period narrows the payroll sums to one pay period.

// HRCollections are the collections `reconcile --hr` reads: the HR tables of the cut-over (compensation config,
// penalties, payroll and its payout ledger rows in transactions, holidays) and the drivers they are keyed by. Each must
// be in the dump's manifest; `etl dump --collections=all` always lists them, an empty one included.
var HRCollections = []string{"driver_compensation_config", "driver_penalties", "drivers", "holidays", "payroll", "transactions"}

// reconcileHR runs the HR checks in the reconcile transaction. docs holds every collection of the dump's manifest.
func reconcileHR(ctx context.Context, tx pgx.Tx, docs map[string][]dump.Doc, status docStatus, period string, rec *Reconciliation) error {
	for _, coll := range HRCollections {
		if _, ok := docs[coll]; !ok {
			rec.add("hr_inputs", coll, "in dump", "absent", false, "the hr gate needs a full dump (`etl dump --collections=all`); "+
				"a narrow dump leaves this collection unchecked and its changes at source unloaded")
		}
	}
	loaded := func(coll string) []dump.Doc {
		var out []dump.Doc
		for _, d := range docs[coll] {
			if s := status.of(coll, d.Path); s == string(Loaded) || s == string(Quarantined) {
				out = append(out, d)
			}
		}
		return out
	}
	for _, step := range []func() error{
		func() error { return reconcileHRConfigs(ctx, tx, loaded("driver_compensation_config"), rec) },
		func() error { return reconcilePayroll(ctx, tx, loaded("payroll"), period, rec) },
		func() error { return reconcilePenalties(ctx, tx, loaded("driver_penalties"), rec) },
	} {
		if err := step(); err != nil {
			return fmt.Errorf("etl: hr reconcile: %w", err)
		}
	}
	return nil
}

// storedConfig is a loaded compensation config version as stored, by legacy doc id.
type storedConfig struct {
	at                                      time.Time
	weekday, holiday, helper                float64
	payStandby                              bool
	refuels                                 int64
	ssoRate, ssoExisting, ssoNew            float64
	ssoHiredBefore, ssoMaxAge, ssoProbation int64
	types                                   int64
}

func reconcileHRConfigs(ctx context.Context, tx pgx.Tx, docs []dump.Doc, rec *Reconciliation) error {
	if len(docs) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT legacy_doc_id, effective_from_at, weekday_rate_thb::float8, holiday_rate_thb::float8,
		  helper_day_rate_thb::float8, pay_standby, fuel_min_refuels_per_month, sso_rate_percent::float8, sso_base_existing_thb::float8,
		  sso_base_new_thb::float8, sso_existing_hired_before_year, sso_max_age_inclusive, sso_probation_months,
		  (SELECT count(*) FROM penalty_types p WHERE p.config_id = c.id)
		FROM driver_compensation_configs c WHERE legacy_doc_id IS NOT NULL`)
	if err != nil {
		return err
	}
	stored := map[string]storedConfig{}
	for rows.Next() {
		var id string
		var s storedConfig
		if err := rows.Scan(&id, &s.at, &s.weekday, &s.holiday, &s.helper, &s.payStandby, &s.refuels, &s.ssoRate, &s.ssoExisting,
			&s.ssoNew, &s.ssoHiredBefore, &s.ssoMaxAge, &s.ssoProbation, &s.types); err != nil {
			return err
		}
		stored[id] = s
	}
	if err := rows.Err(); err != nil {
		return err
	}
	bad := 0
	for _, d := range docs {
		c := &docCtx{f: d.Fields, doc: d}
		got, ok := stored[d.ID]
		if !ok {
			bad++
			rec.add("hr_config", d.ID, "loaded", "no row", false, "the loaded document has no row (etl status, etl quarantine list)")
			continue
		}
		var diffs []string
		if at := c.ts("effectiveFrom"); at == nil || !at.Equal(got.at) {
			diffs = append(diffs, "effective_from_at")
		}
		for _, m := range []struct {
			field, col string
			want       float64
		}{{"weekdayRateThb", "weekday_rate_thb", got.weekday}, {"holidayRateThb", "holiday_rate_thb", got.holiday},
			{"helperDayRateThb", "helper_day_rate_thb", got.helper}} {
			if n, r := c.moneyCents(m.field, true); r == moneyOK && n != cents(m.want) {
				diffs = append(diffs, m.col)
			}
		}
		if b := c.boolean("payStandby"); b != nil && *b != got.payStandby {
			diffs = append(diffs, "pay_standby")
		}
		if f := c.float("fuelMinRefuelsPerMonth"); f != nil && int64(*f) != got.refuels {
			diffs = append(diffs, "fuel_min_refuels_per_month")
		}
		if sso, ok := c.get("sso").(map[string]any); ok {
			s := c.sub(sso)
			for _, m := range []struct {
				field, col string
				want       float64
			}{{"ratePercent", "sso_rate_percent", got.ssoRate}, {"baseExistingThb", "sso_base_existing_thb", got.ssoExisting},
				{"baseNewThb", "sso_base_new_thb", got.ssoNew}, {"existingHiredBeforeYear", "sso_existing_hired_before_year", float64(got.ssoHiredBefore)},
				{"maxAgeInclusive", "sso_max_age_inclusive", float64(got.ssoMaxAge)}, {"probationMonths", "sso_probation_months", float64(got.ssoProbation)}} {
				if f := s.float(m.field); f != nil && cents(*f) != cents(m.want) {
					diffs = append(diffs, m.col)
				}
			}
		}
		if n := validPenaltyTypes(c); n != got.types {
			diffs = append(diffs, "penalty_types ("+strconv.FormatInt(n, 10)+" valid at source, "+strconv.FormatInt(got.types, 10)+" stored)")
		}
		if len(diffs) > 0 {
			bad++
			rec.add("hr_config", d.ID, "dump", "database", false, "differs in "+strings.Join(diffs, ", ")+
				": a version changed at source after the load; run `etl load --since=watermark` (a version is append-only in Go: "+
				"once hr is PostgreSQL-owned a change is a new version, POST /v1/payroll/config)")
		}
	}
	rec.add("hr_config", "driver_compensation_config", len(docs), len(docs)-bad, bad == 0, "versions equal to their documents")
	return nil
}

// validPenaltyTypes counts the penalty types loadCompensationConfig keeps: a unique code, both names, an amount >= 0.
func validPenaltyTypes(c *docCtx) int64 {
	types, _ := c.get("penaltyTypes").([]any)
	seen := map[string]bool{}
	var n int64
	for _, t := range types {
		m, _ := t.(map[string]any)
		s := c.sub(m)
		code := s.str("code")
		_, res := s.moneyCents("defaultAmountThb", true)
		if code == "" || s.str("nameEn") == "" || s.str("nameTh") == "" || seen[code] || (res != moneyOK && res != moneyAbsent) {
			continue
		}
		seen[code] = true
		n++
	}
	return n
}

// payrollKey is the (period, round) of a payroll document as loadPayroll reads it: the doc id first, else the fields.
func payrollKey(d dump.Doc) (string, string) {
	if m := payrollDocID.FindStringSubmatch(d.ID); m != nil {
		return m[2], m[3]
	}
	p, _ := d.Fields["payPeriod"].(string)
	r, _ := d.Fields["round"].(string)
	return strings.TrimSpace(p), strings.TrimSpace(r)
}

func reconcilePayroll(ctx context.Context, tx pgx.Tx, docs []dump.Doc, period string, rec *Reconciliation) error {
	earn, ded, net := newSums(), newSums(), newSums()
	ledgerSrc := 0
	for _, d := range docs {
		c := &docCtx{f: d.Fields, doc: d}
		p, r := payrollKey(d)
		if period != "" && p != period {
			continue
		}
		st, _ := lowerVocab(d.Fields["status"], "draft", "pending_approval", "approved", "paid", "cancelled")
		if st == "" {
			st = "draft"
		}
		k := p + " " + r + " " + st
		for _, m := range []struct {
			s     sums
			field string
		}{{earn, "totalEarnings"}, {ded, "totalDeductions"}, {net, "netPay"}} {
			n, _ := c.moneyCents(m.field, false) // absent or bad: the column default 0 (setPresent)
			m.s.add(k, n, false)
		}
		if (st == "approved" || st == "paid") && c.str("ledgerTransactionId") != "" {
			ledgerSrc++
		}
	}
	rows, err := tx.Query(ctx, `SELECT pay_period, pay_round, status, total_earnings_thb, total_deductions_thb, net_pay_thb,
		  status IN ('approved','paid') AND ledger_transaction_id IS NOT NULL
		FROM payroll_runs WHERE legacy_doc_id IS NOT NULL`)
	if err != nil {
		return err
	}
	tEarn, tDed, tNet := newSums(), newSums(), newSums()
	ledgerTgt := 0
	for rows.Next() {
		var p, r, st string
		var e, dd, n pgtype.Numeric
		var linked bool
		if err := rows.Scan(&p, &r, &st, &e, &dd, &n, &linked); err != nil {
			return err
		}
		if period != "" && p != period {
			continue
		}
		k := p + " " + r + " " + st
		tEarn.add(k, numericCents(e), false)
		tDed.add(k, numericCents(dd), false)
		tNet.add(k, numericCents(n), false)
		if linked {
			ledgerTgt++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	compareSums(rec, "payroll_earnings", earn, tEarn)
	compareSums(rec, "payroll_deductions", ded, tDed)
	compareSums(rec, "payroll_net", net, tNet)
	rec.add("payroll_ledger", "payroll", ledgerSrc, ledgerTgt, ledgerSrc == ledgerTgt,
		"approved and paid runs linked to their payout ledger row (a ledger document left in quarantine leaves its run unlinked)")
	return nil
}

func reconcilePenalties(ctx context.Context, tx pgx.Tx, docs []dump.Doc, rec *Reconciliation) error {
	total, remaining := newSums(), newSums()
	inst := map[string]int64{}
	for _, d := range docs {
		c := &docCtx{f: d.Fields, doc: d}
		st, _ := lowerVocab(d.Fields["status"], "pending", "partially_deducted", "cleared", "cancelled")
		if st == "" {
			st = "pending"
		}
		t, _ := c.moneyCents("totalThb", true)
		r, _ := c.moneyCents("remainingThb", true)
		total.add(st, t, false)
		remaining.add(st, r, false)
		paid := int64(0) // absent: the column default 0
		if f := c.float("installmentsPaid"); f != nil {
			paid = int64(*f)
		}
		inst[st] += paid
	}
	rows, err := tx.Query(ctx, `SELECT status, total_thb, remaining_thb, installments_paid FROM driver_penalties WHERE legacy_doc_id IS NOT NULL`)
	if err != nil {
		return err
	}
	tTotal, tRemaining := newSums(), newSums()
	tInst := map[string]int64{}
	for rows.Next() {
		var st string
		var t, r pgtype.Numeric
		var n int64
		if err := rows.Scan(&st, &t, &r, &n); err != nil {
			return err
		}
		tTotal.add(st, numericCents(t), false)
		tRemaining.add(st, numericCents(r), false)
		tInst[st] += n
	}
	if err := rows.Err(); err != nil {
		return err
	}
	compareSums(rec, "penalty_total", total, tTotal)
	compareSums(rec, "penalty_remaining", remaining, tRemaining)
	keys := map[string]bool{}
	for k := range inst {
		keys[k] = true
	}
	for k := range tInst {
		keys[k] = true
	}
	for _, k := range sortedKeys(keys) {
		rec.add("penalty_installments", k, inst[k], tInst[k], inst[k] == tInst[k], "installments paid, summed per status")
	}
	return nil
}
