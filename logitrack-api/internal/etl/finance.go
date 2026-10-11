package etl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// The finance cut-over checks of `etl reconcile --finance` (T41, issue #61; main spec §6.17, §13.9 step 3 for the
// billing domain, Appendix A §A.3.11). They run after the common checks, against the same dump, before `billing` joins
// PG_OWNED_DOMAINS and its web flag flips: the rate tables (Bangkok effective dates, voids), the D5 fee dedupe, the
// legacy statements as stored (WHT 0.0100), the counters (numbering continues at lastSeq + 1), the fuel snapshots, and
// the parity dry run of the last closed months (parity.go). Every check reads; nothing is written or locked.

// FinanceRun is the finance part of a run.
type FinanceRun struct {
	Window FinanceWindow `json:"window"`
	// Parity is every recomputed record of the window (parity-{ts}.csv); the report keeps the checks only.
	Parity []ParityRow `json:"-"`
}

// reconcileFinance runs the finance checks in one transaction with the common ones.
func (e *Engine) reconcileFinance(ctx context.Context, tx pgx.Tx, docs map[string][]dump.Doc, status docStatus, w FinanceWindow,
	rec *Reconciliation) error {
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
		func() error { return reconcileRateTables(ctx, tx, loaded, rec) },
		func() error { return e.reconcileServiceFees(ctx, tx, loaded("customer_service_fees"), rec) },
		func() error { return reconcileStatements(ctx, tx, loaded("billing_statements"), rec) },
		func() error { return reconcileCounters(ctx, tx, loaded("billing_counters"), rec) },
		func() error { return reconcileFuelSnapshots(ctx, tx, loaded, rec) },
		func() error { return e.reconcileParity(ctx, tx, docs, status, w, rec) },
	} {
		if err := step(); err != nil {
			return fmt.Errorf("etl: finance reconcile: %w", err)
		}
	}
	return nil
}

// announcement is a rate-table row as stored, by legacy doc id.
type announcement struct {
	day, at          time.Time
	amount           float64 // rate_thb, or add_thb_per_trip of a fuel round
	mult             float64
	ref              *float64
	voided           bool
	importID, hub    string
	dest, class, cat string
}

// reconcileRateTables checks every loaded rate entry, fuel round and standby rate against its dump document: the
// Bangkok effective date (effectiveFromDateStr, else the Bangkok day of effectiveFrom: the never-run
// normalizeAnnouncementEffectiveFrom, billingRoundMigration.ts:219, applied as a load transform), the stored instant
// untouched, the amounts, and the void (a voided legacy row that is live in PostgreSQL would be selectable by
// pricing). Every standby rate is listed for review (browser-local midnight, UNVERIFIED, Appendix A §A.3.9).
func reconcileRateTables(ctx context.Context, tx pgx.Tx, loaded func(string) []dump.Doc, rec *Reconciliation) error {
	type table struct{ coll, sql string }
	for _, t := range []table{
		{"customer_rate_entries", `SELECT legacy_doc_id, effective_from_date, effective_from_at, rate_thb::float8, 1::float8, NULL::float8,
			voided, import_id, hub_code, destination_code, vehicle_class, job_category FROM customer_rate_entries WHERE legacy_doc_id IS NOT NULL`},
		{"customer_fuel_rate_adjustments", `SELECT legacy_doc_id, effective_from_date, effective_from_at, add_thb_per_trip::float8,
			rate_multiplier::float8, reference_fuel_price_thb::float8, voided, '', '', '', '', '' FROM customer_fuel_rate_adjustments
			WHERE legacy_doc_id IS NOT NULL`},
		{"standby_rate_entries", `SELECT legacy_doc_id, effective_from_date, effective_from_at, rate_thb::float8, 1::float8, NULL::float8,
			voided_at IS NOT NULL, '', '', '', '', '' FROM standby_rate_entries WHERE legacy_doc_id IS NOT NULL`},
	} {
		docs := loaded(t.coll)
		if len(docs) == 0 {
			continue
		}
		rows, err := tx.Query(ctx, t.sql)
		if err != nil {
			return err
		}
		stored := map[string]announcement{}
		for rows.Next() {
			var id string
			var a announcement
			if err := rows.Scan(&id, &a.day, &a.at, &a.amount, &a.mult, &a.ref, &a.voided, &a.importID, &a.hub, &a.dest, &a.class, &a.cat); err != nil {
				return err
			}
			stored[id] = a
		}
		if err := rows.Err(); err != nil {
			return err
		}
		bad, voided := 0, 0
		for _, d := range docs {
			c := &docCtx{f: d.Fields, doc: d}
			got, ok := stored[d.ID]
			if !ok {
				bad++
				rec.add("rate_tables", t.coll+"/"+d.ID, "loaded", "no row", false, "the loaded document has no row (etl status, etl quarantine list)")
				continue
			}
			var diffs []string
			day, at, _ := c.effectiveFrom()
			if day == nil || !day.Equal(time.Date(got.day.Year(), got.day.Month(), got.day.Day(), 0, 0, 0, 0, time.UTC)) {
				diffs = append(diffs, "effective_from_date")
			}
			if at == nil || !at.Equal(got.at) {
				diffs = append(diffs, "effective_from_at")
			}
			want := c.isTrue("voided")
			if t.coll == "standby_rate_entries" {
				want = false // no legacy void concept (R20)
			}
			if want {
				voided++
			}
			if want != got.voided {
				note := "voided in PostgreSQL but not in Firestore"
				if want {
					note = "voided in Firestore but live in PostgreSQL, where pricing would select it: the void is not applied " +
						"(its voidedBy resolves to no user, user_unresolved); create the user, then `etl quarantine resolve --action=retry`"
				}
				bad++
				rec.add("rate_tables", t.coll+"/"+d.ID, strconv.FormatBool(want), strconv.FormatBool(got.voided), false, "voided: "+note)
				continue
			}
			switch t.coll {
			case "customer_rate_entries":
				if n, r := c.moneyCents("rateThb", true); r != moneyOK || n != cents(got.amount) {
					diffs = append(diffs, "rate_thb")
				}
				class, _ := compute.FoldVehicleClass(c.str("vehicleClass"))
				cat := strings.ToUpper(c.str("jobCategory"))
				if cat == "" {
					cat = "PRIMARY"
				}
				for _, f := range [][3]string{{"import_id", str(c.text("importId")), got.importID}, {"hub_code", strings.ToUpper(c.str("hubId")), got.hub},
					{"destination_code", c.str("destinationCode"), got.dest}, {"vehicle_class", class, got.class}, {"job_category", cat, got.cat}} {
					if f[1] != f[2] {
						diffs = append(diffs, f[0])
					}
				}
			case "customer_fuel_rate_adjustments":
				if m := c.float("rateMultiplier"); m == nil || strconv.FormatFloat(*m, 'f', 6, 64) != strconv.FormatFloat(got.mult, 'f', 6, 64) {
					diffs = append(diffs, "rate_multiplier")
				}
				if n, _ := c.moneyCents("addThbPerTrip", false); n != cents(got.amount) {
					diffs = append(diffs, "add_thb_per_trip")
				}
				n, r := c.moneyCents("referenceFuelPriceThbPerLitre", false)
				if (r == moneyOK) != (got.ref != nil) || (got.ref != nil && n != cents(*got.ref)) {
					diffs = append(diffs, "reference_fuel_price_thb")
				}
			case "standby_rate_entries":
				if n, r := c.moneyCents("rateThb", true); r != moneyOK || n != cents(got.amount) {
					diffs = append(diffs, "rate_thb")
				}
				note := "browser-local midnight: the Bangkok day is the intended one only if the admin browser ran in ICT (UNVERIFIED)"
				if got.at.Equal(time.Date(got.day.Year(), got.day.Month(), got.day.Day(), 0, 0, 0, 0, clock.Bangkok)) {
					note = "Bangkok midnight"
				}
				rec.add("standby_rate_review", d.ID, got.at.UTC().Format(time.RFC3339), got.day.Format("2006-01-02"), true, note)
			}
			if len(diffs) > 0 {
				bad++
				rec.add("rate_tables", t.coll+"/"+d.ID, "dump", "database", false, "differs in "+strings.Join(diffs, ", ")+
					": an announcement is immutable (ADR 0009 §1); a change at source after the load is a void and a new row there, "+
					"which `etl load --since=watermark` brings over")
			}
		}
		rec.add("rate_tables", t.coll, len(docs), len(docs)-bad, bad == 0, fmt.Sprintf("%d voided; Bangkok effective dates, instants and amounts as loaded", voided))
	}
	return nil
}

// reconcileServiceFees checks the D5 dedupe (main spec §19 Q16, Appendix A §A.4): 0010 built
// customer_service_fees_one_per_type and no party keeps two fees of a non-custom type; where the owner kept another
// fee than the one the legacy engine priced with (extra_stop first, standby last by doc id), the parity lists the
// trips and standby records priced with the old one (fee_changed).
func (e *Engine) reconcileServiceFees(ctx context.Context, tx pgx.Tx, docs []dump.Doc, rec *Reconciliation) error {
	var valid *bool
	err := tx.QueryRow(ctx, `SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = $1`, d5ServiceFeeTypes).Scan(&valid)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	switch {
	case valid == nil:
		rec.add("service_fees_d5", d5ServiceFeeTypes, "valid", "missing", false,
			"0010_d5_unique_constraints is not applied: the owner's D5 sign-off and `migrate up` come first (P1 runbook, §13.9)")
	case !*valid:
		rec.add("service_fees_d5", d5ServiceFeeTypes, "valid", "INVALID", false, "a duplicate survived the sign-off (0010 INVALID index)")
	default:
		rec.add("service_fees_d5", d5ServiceFeeTypes, "valid", "valid", true, "one fee per party and type")
	}
	rows, err := tx.Query(ctx, `SELECT coalesce(cu.legacy_doc_id, t.legacy_doc_id, f.billing_party_id::text), f.fee_type, count(*),
		  string_agg(coalesce(f.legacy_doc_id, f.id::text), ' ' ORDER BY f.legacy_doc_id COLLATE "C" NULLS LAST, f.created_at, f.id)
		FROM customer_service_fees f JOIN billing_parties bp ON bp.id = f.billing_party_id
		LEFT JOIN customers cu ON cu.id = bp.customer_id LEFT JOIN tenants t ON t.id = bp.tenant_id
		WHERE f.fee_type <> 'custom' GROUP BY 1, 2 HAVING count(*) > 1 ORDER BY 1, 2`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var party, typ, ids string
		var n int
		if err := rows.Scan(&party, &typ, &n, &ids); err != nil {
			return err
		}
		rec.add("service_fees_d5", party+" "+typ, 1, n, false, "D5 duplicates "+ids+": the owner's winner decision (§19 Q16) is not applied")
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// The legacy winner of every (party, type) the dump has several fees of, against the fee PostgreSQL keeps.
	type key struct{ party, typ string }
	byKey := map[key][]dump.Doc{}
	for _, d := range docs {
		c := &docCtx{f: d.Fields}
		typ, _ := lowerVocab(c.get("feeType"), "extra_stop", "standby")
		if typ != "" {
			k := key{c.str("customerId"), typ}
			byKey[k] = append(byKey[k], d)
		}
	}
	keys := make([]key, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b key) int { return strings.Compare(a.party+" "+a.typ, b.party+" "+b.typ) })
	for _, k := range keys {
		ds := byKey[k]
		if len(ds) < 2 {
			continue
		}
		slices.SortFunc(ds, func(a, b dump.Doc) int { return strings.Compare(a.ID, b.ID) })
		winner := ds[0]
		if k.typ == "standby" {
			winner = ds[len(ds)-1]
		}
		var kept []string
		krows, err := tx.Query(ctx, `SELECT coalesce(f.legacy_doc_id, f.id::text) FROM customer_service_fees f
			JOIN billing_parties bp ON bp.id = f.billing_party_id LEFT JOIN customers cu ON cu.id = bp.customer_id
			LEFT JOIN tenants t ON t.id = bp.tenant_id
			WHERE coalesce(cu.legacy_doc_id, t.legacy_doc_id) = $1 AND f.fee_type = $2 AND f.tenant_id = $3 ORDER BY 1`,
			k.party, k.typ, e.cfg.OwnFleetTenantID)
		if err != nil {
			return err
		}
		if kept, err = pgx.CollectRows(krows, pgx.RowTo[string]); err != nil {
			return err
		}
		note := "the owner kept the legacy winner"
		if !slices.Equal(kept, []string{winner.ID}) {
			note = "the owner kept another fee than the legacy winner: records priced with " + winner.ID + " show as fee_changed in the parity"
		}
		rec.add("service_fees_d5_winner", k.party+" "+k.typ, winner.ID, strings.Join(kept, " "), true, note)
	}
	return nil
}

// reconcileStatements checks every loaded legacy statement as stored (Appendix A §A.3.9, R18, D8): invoice number,
// status, period, totals in satang (netAmount within 0.005 THB of the raw value, R20), withholding rate 0.0100 and no
// lines (never recomputed). The totals against the subtotals and the priced rows of the period are reported, not
// failed: the legacy browser computed them over its toggles and the rows may have been edited since (§6.17 (3)).
func reconcileStatements(ctx context.Context, tx pgx.Tx, docs []dump.Doc, rec *Reconciliation) error {
	if len(docs) == 0 {
		return nil
	}
	type st struct {
		inv, status, rate, party            string
		year, month, lines                  int
		total, wht, net, trip, standby, mdr float64
		plan                                bool
	}
	rows, err := tx.Query(ctx, `SELECT s.legacy_doc_id, s.invoice_number, s.status, s.period_year, s.period_month, s.total_amount::float8,
		  s.withholding_tax::float8, s.net_amount::float8, s.withholding_tax_rate::text, s.trip_subtotal::float8,
		  s.standby_subtotal::float8, s.multi_drop_subtotal::float8, s.billing_party_id::text, bp.billing_date_basis = 'plan',
		  (SELECT count(*) FROM billing_statement_lines l WHERE l.statement_id = s.id)
		FROM billing_statements s JOIN billing_parties bp ON bp.id = s.billing_party_id WHERE s.legacy_doc_id IS NOT NULL`)
	if err != nil {
		return err
	}
	stored := map[string]st{}
	for rows.Next() {
		var id string
		var s st
		if err := rows.Scan(&id, &s.inv, &s.status, &s.year, &s.month, &s.total, &s.wht, &s.net, &s.rate, &s.trip, &s.standby, &s.mdr,
			&s.party, &s.plan, &s.lines); err != nil {
			return err
		}
		stored[id] = s
	}
	if err := rows.Err(); err != nil {
		return err
	}
	bad := 0
	for _, d := range docs {
		c := &docCtx{f: d.Fields}
		s, ok := stored[d.ID]
		if !ok {
			bad++
			rec.add("statements", d.ID, "loaded", "no row", false, "the loaded statement has no row")
			continue
		}
		var diffs []string
		inv := c.str("invoiceNumber")
		if inv == "" {
			inv = d.ID
		}
		if inv != s.inv {
			diffs = append(diffs, "invoice_number")
		}
		status, _ := c.vocab("status", "draft", "sent", "paid", "cancelled")
		if status == "" {
			status = "draft"
		}
		if status != s.status {
			diffs = append(diffs, "status ("+status+" at source)")
		}
		per, _ := c.get("period").(map[string]any)
		y, _ := toFloat(per["year"])
		m, _ := toFloat(per["month"])
		if int(y) != s.year || int(m) != s.month {
			diffs = append(diffs, "period")
		}
		if n, _ := c.moneyCents("totalAmount", false); n != cents(s.total) {
			diffs = append(diffs, "total_amount")
		}
		if n, _ := c.moneyCents("withholdingTax", false); n != cents(s.wht) {
			diffs = append(diffs, "withholding_tax")
		}
		if raw, ok := toFloat(c.get("netAmount")); !ok || math.Abs(raw-s.net) > 0.005+1e-9 {
			diffs = append(diffs, "net_amount (beyond 0.005 THB of the raw value)")
		}
		if s.rate != "0.0100" {
			diffs = append(diffs, "withholding_tax_rate "+s.rate+" (legacy 0.0100, R18)")
		}
		if s.lines != 0 {
			diffs = append(diffs, "lines (a legacy statement has none: never recomputed)")
		}
		if len(diffs) > 0 {
			bad++
			rec.add("statements", d.ID+" "+s.inv, "dump", "database", false, "differs in "+strings.Join(diffs, ", ")+
				": a change at source after the load needs `etl load --since=watermark`")
		}
		// Reported only: the totals against the subtotals and against the priced rows of the period now.
		from := time.Date(s.year, time.Month(s.month), 1, 0, 0, 0, 0, clock.Bangkok).UTC()
		to := time.Date(s.year, time.Month(s.month)+1, 1, 0, 0, 0, 0, clock.Bangkok).UTC()
		var trips, multi, standby float64
		if err := tx.QueryRow(ctx, `SELECT
			  coalesce(sum(sn.estimate_thb) FILTER (WHERE NOT sn.is_multi_delivery), 0)::float8,
			  coalesce(sum(sn.estimate_thb) FILTER (WHERE sn.is_multi_delivery), 0)::float8
			FROM trip_records tr JOIN trip_billing_snapshots sn ON sn.trip_id = tr.id
			WHERE tr.status = 'delivered' AND tr.billing_party_id = $1::uuid AND sn.estimate_thb <> 0
			  AND CASE WHEN $2 THEN tr.billing_date ELSE tr.delivered_at END >= $3
			  AND CASE WHEN $2 THEN tr.billing_date ELSE tr.delivered_at END < $4`, s.party, s.plan, from, to).Scan(&trips, &multi); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(billing_estimate_thb), 0)::float8 FROM standby_records
			WHERE status = 'completed' AND billing_party_id = $1::uuid AND billing_estimate_thb <> 0 AND ended_at >= $2 AND ended_at < $3`,
			s.party, from, to).Scan(&standby); err != nil {
			return err
		}
		sub := cents(s.trip) + cents(s.standby) + cents(s.mdr)
		note := fmt.Sprintf("%s; subtotals trip %s + multi-drop %s + standby %s", s.status, money(s.trip), money(s.mdr), money(s.standby))
		if sub != cents(s.total) {
			note += " differ from the total"
		}
		note += fmt.Sprintf("; priced rows of the period now: trip %s, multi-drop %s, standby %s (reported only: browser toggles and later edits)",
			money(trips), money(multi), money(standby))
		rec.add("statement_totals", d.ID+" "+s.inv, money(s.total), thb(sub), true, note)
	}
	rec.add("statements", "billing_statements", len(docs), len(docs)-bad, bad == 0, "legacy statements as stored, WHT 0.0100, no lines")
	return nil
}

// invoiceSeq parses the sequence of an invoice number {code}-{YYYYMM}-{seq} (0 when it is not of that form).
func invoiceSeq(inv, code string, year, month int) int {
	prefix := fmt.Sprintf("%s-%04d%02d-", code, year, month)
	if !strings.HasPrefix(inv, prefix) {
		return 0
	}
	n, err := strconv.Atoi(inv[len(prefix):])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

var digitsOnly = regexp.MustCompile(`^[0-9]+$`)

// reconcileCounters checks the counters (main spec §6.14, §6.17 (5)): every loaded counter as at source, and for every
// party and month that has statements or a counter, the number the next statement takes ({code}-{YYYYMM}-{lastSeq+1}
// with the party's code, as POST /v1/billing/statements formats it) is free and no stored number of the month is
// beyond the counter.
func reconcileCounters(ctx context.Context, tx pgx.Tx, docs []dump.Doc, rec *Reconciliation) error {
	type ckey struct {
		party       string
		year, month int
	}
	type counter struct {
		seq  int
		code string
	}
	counters := map[ckey]counter{}
	rows, err := tx.Query(ctx, `SELECT billing_party_id::text, period_year, period_month, last_seq, customer_code FROM billing_counters`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k ckey
		var c counter
		if err := rows.Scan(&k.party, &k.year, &k.month, &c.seq, &c.code); err != nil {
			return err
		}
		counters[k] = c
	}
	if err := rows.Err(); err != nil {
		return err
	}
	codes := map[string]string{}
	labels := map[string]string{}
	rows, err = tx.Query(ctx, `SELECT bp.id::text, coalesce(nullif(btrim(cu.code::text), ''), nullif(btrim(t.code::text), ''), bp.id::text),
		  coalesce(cu.legacy_doc_id, t.legacy_doc_id, bp.id::text)
		FROM billing_parties bp LEFT JOIN customers cu ON cu.id = bp.customer_id LEFT JOIN tenants t ON t.id = bp.tenant_id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, code, label string
		if err := rows.Scan(&id, &code, &label); err != nil {
			return err
		}
		codes[id], labels[id] = code, label
	}
	if err := rows.Err(); err != nil {
		return err
	}
	byLabel := map[string]string{}
	for id, l := range labels {
		byLabel[l] = id
	}
	bad := 0
	for _, d := range docs {
		c := &docCtx{f: d.Fields}
		i := strings.LastIndexByte(d.ID, '_')
		if i <= 0 || !digitsOnly.MatchString(d.ID[i+1:]) || len(d.ID)-i-1 != 6 {
			continue
		}
		y, _ := strconv.Atoi(d.ID[i+1 : i+5])
		m, _ := strconv.Atoi(d.ID[i+5:])
		party := byLabel[d.ID[:i]]
		got, ok := counters[ckey{party, y, m}]
		want := int64(0)
		if n := c.integer("lastSeq"); n != nil {
			want = *n
		}
		if !ok || int64(got.seq) != want {
			bad++
			rec.add("counters", d.ID, want, got.seq, false, "lastSeq differs from the source: `etl load --since=watermark` first")
		}
	}
	type month struct {
		invoices []string
	}
	months := map[ckey]*month{}
	for k := range counters {
		months[k] = &month{}
	}
	rows, err = tx.Query(ctx, `SELECT billing_party_id::text, period_year, period_month, invoice_number FROM billing_statements`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k ckey
		var inv string
		if err := rows.Scan(&k.party, &k.year, &k.month, &inv); err != nil {
			return err
		}
		if months[k] == nil {
			months[k] = &month{}
		}
		months[k].invoices = append(months[k].invoices, inv)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	all := map[string]bool{}
	for _, mo := range months {
		for _, inv := range mo.invoices {
			all[inv] = true
		}
	}
	keys := make([]ckey, 0, len(months))
	for k := range months {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b ckey) int {
		return strings.Compare(fmt.Sprintf("%s %04d%02d", labels[a.party], a.year, a.month), fmt.Sprintf("%s %04d%02d", labels[b.party], b.year, b.month))
	})
	for _, k := range keys {
		cnt := counters[k]
		code := codes[k.party]
		next := fmt.Sprintf("%s-%04d%02d-%03d", code, k.year, k.month, cnt.seq+1)
		maxSeq := 0
		for _, inv := range months[k].invoices {
			maxSeq = max(maxSeq, invoiceSeq(inv, code, k.year, k.month))
		}
		key := fmt.Sprintf("%s %04d-%02d", labels[k.party], k.year, k.month)
		switch {
		case all[next]:
			bad++
			rec.add("counters", key, cnt.seq, next, false, "the next statement would take "+next+", which a statement already holds: "+
				"POST /v1/billing/statements would fail on billing_statements_invoice_no; the counter is behind the stored numbers")
		case maxSeq > cnt.seq:
			bad++
			rec.add("counters", key, cnt.seq, maxSeq, false, "a stored invoice number of the month is beyond the counter's lastSeq")
		default:
			note := "numbering continues at " + next
			if cnt.code != "" && cnt.code != code {
				note += " (the counter's stored prefix is " + cnt.code + "; numbers take the party's code " + code + ")"
			}
			rec.add("counters_next", key, cnt.seq, next, true, note)
		}
	}
	rec.add("counters", "billing_counters", len(docs), len(docs), bad == 0, "lastSeq as at source; the next number of every month is free")
	return nil
}

// reconcileFuelSnapshots checks every loaded Bangchak snapshot (ADR 0009 §5, R70): a daily row is insert-only (its
// first capture stays; a source that captured the day again is reported), a monthly row is what the source holds,
// legacy error rows included (the error clobber is fixed in Go, not by the ETL).
func reconcileFuelSnapshots(ctx context.Context, tx pgx.Tx, loaded func(string) []dump.Doc, rec *Reconciliation) error {
	for _, coll := range []string{"fuel_daily_snapshots", "fuel_monthly_snapshots"} {
		docs := loaded(coll)
		if len(docs) == 0 {
			continue
		}
		q := `SELECT day_key::text, captured_at, source, status, '', items FROM fuel_daily_snapshots`
		if coll == "fuel_monthly_snapshots" {
			q = `SELECT month_key, captured_at, source, status, coalesce(error_message, ''), items FROM fuel_monthly_snapshots`
		}
		rows, err := tx.Query(ctx, q)
		if err != nil {
			return err
		}
		type snap struct {
			captured            time.Time
			source, status, msg string
			items               []byte
		}
		stored := map[string]snap{}
		for rows.Next() {
			var k string
			var s snap
			if err := rows.Scan(&k, &s.captured, &s.source, &s.status, &s.msg, &s.items); err != nil {
				return err
			}
			stored[k] = s
		}
		if err := rows.Err(); err != nil {
			return err
		}
		bad, errorRows := 0, 0
		for _, d := range docs {
			c := &docCtx{f: d.Fields}
			s, ok := stored[d.ID]
			if !ok {
				bad++
				rec.add("fuel_snapshots", coll+"/"+d.ID, "loaded", "no row", false, "the loaded snapshot has no row")
				continue
			}
			var diffs []string
			if at := c.ts("capturedAt"); at == nil || !at.Equal(s.captured) {
				diffs = append(diffs, "captured_at")
			}
			if str(c.text("source")) != s.source {
				diffs = append(diffs, "source")
			}
			if !sameJSON(plain(c.get("items")), s.items) {
				diffs = append(diffs, "items")
			}
			if coll == "fuel_monthly_snapshots" {
				st, _ := lowerVocab(c.get("status"), "ok", "error")
				if st == "" {
					st = "ok"
				}
				if st != s.status || str(c.text("errorMessage")) != s.msg {
					diffs = append(diffs, "status")
				}
				if s.status == "error" {
					errorRows++
				}
			}
			if len(diffs) > 0 {
				bad++
				note := "differs in " + strings.Join(diffs, ", ") + ": `etl load --since=watermark` first"
				if coll == "fuel_daily_snapshots" {
					note = "differs in " + strings.Join(diffs, ", ") + ": a daily snapshot is insert-only (ADR 0009 §5) and keeps its " +
						"first capture; the source captured the day again"
				}
				rec.add("fuel_snapshots", coll+"/"+d.ID, "dump", "database", false, note)
			}
		}
		note := "as at source"
		if errorRows > 0 {
			note = fmt.Sprintf("as at source; %d legacy fetch-error month(s) loaded as they are (R70)", errorRows)
		}
		rec.add("fuel_snapshots", coll, len(docs), len(docs)-bad, bad == 0, note)
	}
	return nil
}

// sameJSON compares a plain JSON value with a stored jsonb document (key order and number spelling ignored).
func sameJSON(v any, stored []byte) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	var a, s any
	if json.Unmarshal(b, &a) != nil || json.Unmarshal(stored, &s) != nil {
		return false
	}
	if _, isList := a.([]any); !isList {
		a = []any{} // the load stores a non-list as [] (snapshotFields)
	}
	return reflect.DeepEqual(a, s)
}
