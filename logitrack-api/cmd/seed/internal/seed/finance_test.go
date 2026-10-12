package seed

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// The billing and finance generator (T43, issue #63) on the plans seed --profile demo loads: the golden vectors of
// Appendix D §D.4.3-§D.4.4 still hold over the demo's rate tables, the new rate rounds, fuel rounds, tie pair, voided
// cards and standby round price the generated rows as the engine does, every statement is what CreateStatement would
// write (totals, withholding, counters, locks), invariants 4 and 5 hold, and the expenses and maintenance records keep
// the rules of the services that write them. The integration tests load the same plans, run --verify and force a
// recompute into a sent and a paid period.

// engineResult prices a plan trip with the plan's pricing inputs, as CheckEngine prices it from the database.
func engineResult(t *testing.T, p *Plan, in *engineInputs, tripID string) compute.TripResult {
	t.Helper()
	tr := rowByID(p, "trip_records", tripID)
	k := rowByID(p, "tasks", tr.Str("task_id"))
	var stops []Row
	for _, s := range p.Tables["trip_delivery_stops"] {
		if s.Str("trip_id") == tripID {
			stops = append(stops, s)
		}
	}
	tin, err := tripInput(&opsTrip{trip: tr, task: k, stops: stops})
	if err != nil {
		t.Fatalf("trip %s: %v", tr.Str("trip_no"), err)
	}
	party, _ := compute.ResolveTaskParty(tin.Task.TaskParties)
	res, err := compute.PriceTrip(tin, in.tables(party))
	if err != nil {
		t.Fatalf("trip %s: %v", tr.Str("trip_no"), err)
	}
	return res
}

// AC "golden values asserted (e.g. 1000 x 1.1 + 50 = 1150)": the trip and standby vectors of Appendix D §D.4.3 and
// §D.4.4, recomputed by the engine over the smoke rate tables and over the demo's (the rounds T43 adds price no smoke
// row), the legacy and manual rows included (V02 is the etl row TR02: the engine still gives 1000 x 1.1 + 50 = 1150, the
// golden of lib/billingCompute.test.ts:279; V12 is what a forced recompute writes over the corrupted 910; V13 is the
// engine price under the manual 1500), and the statement totals of §D.4.4.
func TestBillingGoldenVectors(t *testing.T) {
	trips := map[string]string{"TR01": "1130.00", "TR02": "1150.00", "TR03": "1250.00", "TR04": "1150.00", "TR05": "2250.00",
		"TR06": "1150.00", "TR07": "1130.00", "TR08": "2100.00", "TR09": "no_rate", "TR10": "no_vehicle_class", "TR11": "950.00",
		"TR12": "950.00", "TR13": "1160.00", "TR14": "1160.00", "TR15": "no_customer", "TR16": "1700.00", "TR21": "1800.00"}
	standby := map[string]string{"SB01": "800.00", "SB02": "850.00", "SB03": "500.00", "SB04": "no_rate"}
	statements := map[string][3]string{"ST1": {"1930.00", "19.30", "1910.70"}, "ST2": {"2400.00", "24.00", "2376.00"},
		"ST3": {"6350.00", "63.50", "6286.50"}, "ST4": {"2600.00", "26.00", "2574.00"}}
	for _, prof := range []Profile{ProfileSmoke, ProfileDemo} {
		t.Run(string(prof), func(t *testing.T) {
			p := plan(t, prof)
			in, err := planEngineInputs(p)
			if err != nil {
				t.Fatal(err)
			}
			for sym, want := range trips {
				res := engineResult(t, p, in, p.Symbols.MustID(sym).String())
				got := string(res.Reason)
				if res.Price != nil {
					got = money(res.Price.EstimateTHB)
				}
				if got != want {
					t.Errorf("%s: engine %s, golden %s", sym, got, want)
				}
			}
			tr02 := engineResult(t, p, in, p.Symbols.MustID("TR02").String()).Price
			if tr02 == nil || tr02.BaseRateTHB != 1000 || tr02.RateMultiplier != 1.1 || tr02.AddTHBPerTrip != 50 ||
				compute.FinalRateTHB(1000, 1.1, 50) != 1150 {
				t.Errorf("V02 is not round2(1000 x 1.1 + 50) = 1150: %+v", tr02)
			}
			if tr12 := engineResult(t, p, in, p.Symbols.MustID("TR12").String()).Price; tr12 == nil || !tr12.ManualOverride ||
				tr12.FuelAdjustmentID != "" {
				t.Errorf("V12: a forced recompute must write 950.00 frozen without fuel: %+v", tr12)
			}
			for sym, want := range standby {
				sb := rowByID(p, "standby_records", p.Symbols.MustID(sym).String())
				res := priceStandbyRow(t, p, in, sb)
				got := string(res.Reason)
				if res.Price != nil {
					got = money(res.Price.EstimateTHB)
				}
				if got != want {
					t.Errorf("%s: engine %s, golden %s", sym, got, want)
				}
			}
			for sym, want := range statements {
				st := rowByID(p, "billing_statements", p.Symbols.MustID(sym).String())
				got := [3]string{st.Str("total_amount"), st.Str("withholding_tax"), st.Str("net_amount")}
				total, _ := satangOf(st.Str("total_amount"))
				wht := money(compute.WithholdingTHB(float64(total)/100, 0.01))
				if got != want || wht != want[1] {
					t.Errorf("%s: %v (withholding of the total %s), golden %v", sym, got, wht, want)
				}
			}
		})
	}
}

// priceStandbyRow prices a plan standby record with the plan's inputs.
func priceStandbyRow(t *testing.T, p *Plan, in *engineInputs, r Row) compute.StandbyResult {
	t.Helper()
	pr := &opsPrices{set: map[string]Row{}}
	var task Row
	if id := r.Str("task_id"); id != "" {
		task = rowByID(p, "tasks", id)
	}
	if err := priceStandby(pr, in, &opsStandby{row: r, task: task}, p.Symbols.MustID(ownFleetSymbol).String()); err != nil {
		t.Fatal(err)
	}
	set := pr.set[r.Str("id")]
	if v := set.Str("billing_estimate_thb"); v != "" {
		f, _ := strconv.ParseFloat(v, 64)
		return compute.StandbyResult{Price: &compute.StandbyPrice{EstimateTHB: f, RateEntryID: set.Str("billing_rate_entry_id"),
			RateSource: set.Str("billing_rate_source")}}
	}
	return compute.StandbyResult{Reason: compute.UnpricedReason(set.Str("billing_unpriced_reason"))}
}

// The rounds T43 adds price the generated rows as the engine does (Appendix D §D.2 #8): an SPX demo-hub trip takes the
// round-1 card under FA05 (x1.1 +50) before round 2 and the round-2 card under the SPX round 2 (x1.05 +30) from it; a
// trip to SOCW stays the "need a rate card" row; the CJSF-BPL tie (R16) prices the first-created card and never its
// twin, and no voided card prices anything; the SPK band round (reference 36.80: floor 36, (36 - 41) x 10 = -50) gives
// RE08 1100 - 50 = 1050.00 with the band 36.01-37.00; a CJSF wait after the mid-month standby round prices 900.
func TestFinanceRoundVectors(t *testing.T) {
	p := plan(t, ProfileDemo)
	g := newFinGen(p, Options{Anchor: DefaultAnchor, Namespace: DefaultNamespace})
	tasks, trips := byID(p.Tables["tasks"]), byID(p.Tables["trip_records"])
	cards := byID(p.Tables["customer_rate_entries"])
	fuel := map[string]Row{}
	for _, f := range p.Tables["customer_fuel_rate_adjustments"] {
		fuel[f.Str("id")] = f
	}
	voided := map[string]bool{}
	for _, c := range p.Tables["customer_rate_entries"] {
		if c["voided"] == true {
			voided[c.Str("id")] = true
		}
	}
	tie := map[string]int{}
	seen := map[string]int{}
	for _, s := range generated(t, p, "trip_billing_snapshots") {
		tr := trips[s.Str("trip_id")]
		k := tasks[tr.Str("task_id")]
		if voided[s.Str("rate_entry_id")] {
			t.Errorf("trip %s priced by a voided card", tr.Str("trip_no"))
		}
		bill := parseTS(t, firstNonEmpty(tr.Str("billing_date"), tr.Str("delivered_at")))
		late := clock.Day(bill) >= clock.Day(g.round2)
		src, dst := k.Str("source_hub_raw"), k.Str("destination_raw")
		switch {
		case slices.Contains(spxDemoHubs, src) && dst == "SOCW" && k.Str("job_category") == "PRIMARY":
			seen["socw"]++
			if s.Str("unpriced_reason") != "no_rate" {
				t.Errorf("SOCW trip %s: %s/%s, want the no_rate row", tr.Str("trip_no"), s.Str("estimate_thb"), s.Str("unpriced_reason"))
			}
		case slices.Contains(spxDemoHubs, src) && (dst == "SOCE" || dst == "SOCN") && k.Str("truck_type") != "":
			card, adj := cards[s.Str("rate_entry_id")], fuel[s.Str("fuel_adjustment_id")]
			if card == nil || adj == nil {
				t.Errorf("SPX demo trip %s: card %s, fuel %s", tr.Str("trip_no"), s.Str("rate_entry_id"), s.Str("fuel_adjustment_id"))
				continue
			}
			h := slices.Index(spxDemoHubs, src)
			want := spxCardBase[k.Str("truck_type")][dst] + 10*h
			mult, add := "1.100000", "50.00"
			if late {
				want += spxRound2[k.Str("truck_type")]
				mult, add = "1.050000", "30.00"
				seen["spx-r2"]++
			} else {
				seen["spx-r1"]++
			}
			rate, _ := strconv.ParseFloat(card.Str("rate_thb"), 64)
			m, _ := strconv.ParseFloat(mult, 64)
			a, _ := strconv.ParseFloat(add, 64)
			if card.Str("rate_thb") != fmt.Sprintf("%d.00", want) || adj.Str("rate_multiplier") != mult || adj.Str("add_thb_per_trip") != add ||
				s.Str("estimate_thb") != money(compute.FinalRateTHB(rate, m, a)) {
				t.Errorf("SPX demo trip %s (%s, late %v): %s by card %s and fuel x%s %s, want card %d, x%s %s",
					tr.Str("trip_no"), clock.DateString(bill), late, s.Str("estimate_thb"), card.Str("rate_thb"),
					adj.Str("rate_multiplier"), adj.Str("add_thb_per_trip"), want, mult, add)
			}
		case src == "CJSF-BPL" && late:
			seen["tie"]++
			tie[cards[s.Str("rate_entry_id")].Str("rate_thb")]++
		case src == "SPK-GW" && dst == "SPK890146" && k.Str("billing_party_id") == "" && s["is_multi_delivery"] != true && late:
			seen["band"]++
			if s.Str("estimate_thb") != "1050.00" || s.Str("fuel_band_lower_thb") != "36.01" || s.Str("fuel_band_upper_thb") != "37.00" ||
				s.Str("round_effective_from_date") != clock.DateString(g.round2) {
				t.Errorf("SPK band trip %s: %s, band %s-%s, round %s; want 1050.00, 36.01-37.00, %s", tr.Str("trip_no"),
					s.Str("estimate_thb"), s.Str("fuel_band_lower_thb"), s.Str("fuel_band_upper_thb"),
					s.Str("round_effective_from_date"), clock.DateString(g.round2))
			}
		}
	}
	if len(tie) != 1 || tie["1800.00"] == 0 {
		t.Errorf("CJSF-BPL round-2 trips priced by %v, want only the first-created 1800.00 card (R16)", tie)
	}
	for _, v := range []string{"socw", "spx-r1", "spx-r2", "tie", "band"} {
		if seen[v] == 0 {
			t.Errorf("the demo has no %s vector", v)
		}
	}
	mid := g.round2.AddDate(0, 0, 15)
	n900 := 0
	for _, r := range generated(t, p, "standby_records") {
		if r.Str("billing_party_id") == p.Symbols.MustID("BP_CJSF").String() && r.Str("ended_at") != "" {
			want := "900.00"
			if parseTS(t, r.Str("ended_at")).Before(mid) {
				continue
			}
			n900++
			if r.Str("billing_estimate_thb") != want {
				t.Errorf("CJSF standby %s after the mid-month round: %s, want %s", r.Str("id"), r.Str("billing_estimate_thb"), want)
			}
		}
	}
	if n900 == 0 {
		t.Error("no CJSF standby after the mid-month round")
	}
}

// AC "invariants 4-5 pass" on every demo plan (two seeds, two anchors): every priced SUPPLEMENTARY trip is frozen
// without fuel and the corrupted TR12 is not (#4); no price was written after its period was sent, last_seq counts the
// statements of each party and month, and every invoice number is {CODE}-{YYYYMM}-{NNN} (#5). Beside them, every
// statement is what CreateStatement writes: its lines are the invoice set (re-derived here), its totals their exact
// sums, the withholding round2(total x 0.0100) of the owner company's 1 %, and every status is present.
func TestFinanceInvariantsAndStatements(t *testing.T) {
	for name, p := range opsPlans(t) {
		t.Run(name, func(t *testing.T) {
			trips := byID(p.Tables["trip_records"])
			for _, s := range p.Tables["trip_billing_snapshots"] {
				tr := trips[s.Str("trip_id")]
				if s.Str("estimate_thb") == "" || tr.Str("job_category") != "SUPPLEMENTARY" {
					continue
				}
				frozen := s["manual_override"] == true && s["fuel_adjustment_id"] == nil && s.Str("rate_multiplier") == "1.000000" &&
					s.Str("add_thb_per_trip") == "0.00"
				if tr.Str("trip_no") == "ZXJB26090700112" {
					carries := s["fuel_adjustment_id"] != nil || s.Str("rate_multiplier") != "1.000000" || s.Str("add_thb_per_trip") != "0.00"
					if s["manual_override"] == true || !carries {
						t.Error("#4 the corrupted fixture TR12 must not be frozen")
					}
				} else if !frozen {
					t.Errorf("#4 SUPPLEMENTARY trip %s is not frozen", tr.Str("trip_no"))
				}
			}
			checkLocks(t, p)
			checkStatementLocks(t, p)
			checkStatements(t, p)
		})
	}
}

// checkStatementLocks is invariant 5 over every row of a sent or paid statement's period, smoke rows included.
func checkStatementLocks(t *testing.T, p *Plan) {
	t.Helper()
	own := p.Symbols.MustID(ownFleetSymbol).String()
	seq := map[stmtKey]int{}
	invoiceRx := regexp.MustCompile(`^[A-Z0-9]+-[0-9]{6}-[0-9]{3,}$`)
	for _, b := range p.Tables["billing_statements"] {
		y, _ := intOf(b["period_year"])
		m, _ := intOf(b["period_month"])
		k := stmtKey{b.Str("billing_party_id"), y, m}
		seq[k]++
		if want := fmt.Sprintf("%s-%04d%02d-", b.Str("customer_code_snapshot"), y, m); !invoiceRx.MatchString(b.Str("invoice_number")) ||
			b.Str("invoice_number")[:len(want)] != want {
			t.Errorf("#5 invoice number %s", b.Str("invoice_number"))
		}
		if b.Str("status") != "sent" && b.Str("status") != "paid" {
			continue
		}
		sent := parseTS(t, b.Str("sent_at"))
		from, to := bkkMonth(y, m)
		party := stmtParty{id: k.party, planBasis: rowByID(p, "billing_parties", k.party).Str("billing_date_basis") == "plan"}
		lines, err := planInvoiceSet(p, party, own, from, to)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range lines {
			if l.pricedAt.After(sent) {
				t.Errorf("#5 %s: a row of its period (%s%s) was priced after it was sent", b.Str("invoice_number"), l.tripID, l.standbyID)
			}
		}
	}
	for _, c := range p.Tables["billing_counters"] {
		y, _ := intOf(c["period_year"])
		m, _ := intOf(c["period_month"])
		k := stmtKey{c.Str("billing_party_id"), y, m}
		if n, _ := intOf(c["last_seq"]); n != seq[k] {
			t.Errorf("#5 counter %v: last_seq %d, %d statements", k, n, seq[k])
		}
		delete(seq, k)
	}
	if len(seq) > 0 {
		t.Errorf("#5 statements without a counter: %v", seq)
	}
}

// checkStatements: the generated statements against the invoice sets and CreateStatement's arithmetic.
func checkStatements(t *testing.T, p *Plan) {
	t.Helper()
	own := p.Symbols.MustID(ownFleetSymbol).String()
	lines := map[string][]Row{}
	for _, l := range p.Tables["billing_statement_lines"] {
		lines[l.Str("statement_id")] = append(lines[l.Str("statement_id")], l)
	}
	statuses := map[string]int{}
	parties := map[string]stmtParty{}
	for _, pt := range planParties(p) {
		parties[pt.id] = pt
	}
	ttp := p.Symbols.MustID("BP_TTP").String()
	for _, b := range generated(t, p, "billing_statements") {
		statuses[b.Str("status")]++
		if b.Str("billing_party_id") == ttp {
			t.Errorf("%s: TTP's months hold rows TTP ran (outside the own fleet's reach): CreateStatement refuses them", b.Str("invoice_number"))
		}
		y, _ := intOf(b["period_year"])
		m, _ := intOf(b["period_month"])
		from, to := bkkMonth(y, m)
		set, err := planInvoiceSet(p, parties[b.Str("billing_party_id")], own, from, to)
		if err != nil {
			t.Fatal(err)
		}
		ls := lines[b.Str("id")]
		var total int64
		for _, l := range ls {
			v, _ := satangOf(l.Str("amount_thb"))
			total += v
		}
		var want int64
		for _, l := range set {
			want += l.amount
		}
		wht := int64(math.Round(compute.WithholdingTHB(float64(total)/100, 0.01) * 100))
		if len(ls) != len(set) || total != want || b.Str("total_amount") != baht(total) || b.Str("withholding_tax") != baht(wht) ||
			b.Str("net_amount") != baht(total-wht) || b.Str("withholding_tax_rate") != "0.0100" {
			t.Errorf("%s: %d lines / %s (set %d / %s), withholding %s, net %s", b.Str("invoice_number"), len(ls),
				b.Str("total_amount"), len(set), baht(want), b.Str("withholding_tax"), b.Str("net_amount"))
		}
		n, _ := intOf(b["trip_count"])
		if n != len(ls) {
			t.Errorf("%s: trip_count %d, %d lines", b.Str("invoice_number"), n, len(ls))
		}
	}
	for _, s := range []string{"draft", "sent", "paid", "cancelled"} {
		if statuses[s] == 0 {
			t.Errorf("no generated %s statement (%v)", s, statuses)
		}
	}
}

// R24 billing fixtures: the broker d13, moved TTP -> NWR on day 45, has trips it ran for NWR after its move on the
// generated statements (NWR is in the own fleet's reach), while its TTP history stays in TTP and is invoiced on none.
func TestBrokerTripsOnStatements(t *testing.T) {
	p := plan(t, ProfileDemo)
	var d13 string
	for _, d := range p.Tables["drivers"] {
		if d.Str("id_card") == thaiID("199990000013") {
			d13 = d.Str("id")
		}
	}
	trips := byID(p.Tables["trip_records"])
	nwr, ttp := p.Symbols.MustID("TN_NWR").String(), p.Symbols.MustID("TN_TTP").String()
	var onNWR, onTTP int
	for _, l := range generated(t, p, "billing_statement_lines") {
		tr := trips[l.Str("trip_id")]
		if tr == nil || tr.Str("driver_id") != d13 {
			continue
		}
		switch tr.Str("tenant_id") {
		case nwr:
			onNWR++
		case ttp:
			onTTP++
		}
	}
	if onNWR == 0 || onTTP != 0 {
		t.Errorf("d13 lines: %d NWR trips (want some), %d TTP trips (want none)", onNWR, onTTP)
	}
}

// The expenses (Appendix D §D.2 #16) and maintenance records (#15) keep the rules of the services that write them, on
// every demo plan: the counts and statuses of §D.2, a fuel log's odometer rising per truck with the distance since the
// last one, a unique station tax invoice per driver, the amount of its litres and price, client_op_id on the driver's
// rows and none on the office's toll import (numbered from 1 per import, imported on the next Monday or the anchor
// morning), no receipt on a rejected row, every row in its truck's tenant on a truck its driver held that day; the
// maintenance records leave the trucks as the maintenance service does.
func TestExpensesAndMaintenance(t *testing.T) {
	for name, p := range opsPlans(t) {
		t.Run(name, func(t *testing.T) {
			rows := generated(t, p, "vehicle_expenses")
			kinds := countBy(rows, func(r Row) string { return r.Str("expense_type") + "/" + r.Str("category") })
			statuses := countBy(rows, func(r Row) string { return r.Str("status") })
			if kinds["fuel/"] != demoFuelLogs || kinds["other/toll"] != demoTollRows || len(rows) != demoFuelLogs+demoTollRows+demoOtherRows ||
				statuses["pending"] != demoPendingRows || statuses["rejected"] != demoRejectedRows {
				t.Errorf("expenses %v, statuses %v", kinds, statuses)
			}
			trucks, drivers, objects := byID(p.Tables["trucks"]), byID(p.Tables["drivers"]), byID(p.Tables["file_objects"])
			held := map[string][]opsSpan{}
			for _, a := range p.Tables["truck_assignments"] {
				if a.Str("status") == "cancelled" {
					continue
				}
				sp := opsSpan{truck: trucks[a.Str("truck_id")], from: parseTS(t, a.Str("created_at"))}
				if a.Str("revoked_at") != "" {
					sp.to = parseTS(t, a.Str("revoked_at"))
				}
				held[a.Str("driver_id")] = append(held[a.Str("driver_id")], sp)
			}
			anchor := planAnchor(t, p)
			lastOdo := map[string]int{}
			taxInv := map[string]bool{}
			batches := map[string][]int{}
			for _, r := range rows {
				at := parseTS(t, r.Str("expense_at"))
				tr := trucks[r.Str("truck_id")]
				if tr == nil || tr.Str("tenant_id") != r.Str("tenant_id") || r.Str("truck_license_plate_snapshot") != tr.Str("license_plate") {
					t.Errorf("expense %s: not in its truck's tenant", r.Str("id"))
					continue
				}
				ok := false
				for _, sp := range held[r.Str("driver_id")] {
					if sp.truck.Str("id") == tr.Str("id") && !sp.from.After(at) && (sp.to.IsZero() || sp.to.After(at)) {
						ok = true
					}
				}
				if !ok {
					t.Errorf("expense %s: driver %s did not hold truck %s then", r.Str("id"), drivers[r.Str("driver_id")].Str("id_card"), tr.Str("license_plate"))
				}
				toll := r.Str("category") == "toll"
				if toll != (r["client_op_id"] == nil) || (toll && (r.Str("created_by_user_id") != p.Symbols.MustID("U_WRT_OPS").String() ||
					r.Str("status") != "approved")) {
					t.Errorf("expense %s: client_op_id on the driver's rows only, toll imports approved by the office", r.Str("id"))
				}
				if r.Str("status") == "rejected" && (r["receipt_file_id"] != nil || r.Str("admin_note") == "") {
					t.Errorf("rejected expense %s: a receipt, or no admin note", r.Str("id"))
				}
				if f := objects[r.Str("receipt_file_id")]; r["receipt_file_id"] != nil &&
					(f == nil || f.Str("owner_id") != r.Str("id") || f.Str("purpose") != "expense_receipt" || f.Str("uploaded_by") != r.Str("created_by_user_id")) {
					t.Errorf("expense %s: receipt object", r.Str("id"))
				}
				if toll {
					created := parseTS(t, r.Str("created_at"))
					if created.Before(at) || created.After(anchor.Add(9*time.Hour)) {
						t.Errorf("toll %s imported at %s for a passage at %s", r.Str("id"), r.Str("created_at"), r.Str("expense_at"))
					}
					n, _ := intOf(r["toll_import_sequence"])
					batches[r.Str("created_at")] = append(batches[r.Str("created_at")], n)
				}
				if r.Str("expense_type") != "fuel" {
					continue
				}
				odo, _ := intOf(r["odometer_km"])
				if prev, ok := lastOdo[tr.Str("id")]; ok {
					if odo <= prev || r.Str("distance_km") != fmt.Sprintf("%d.00", odo-prev) {
						t.Errorf("fuel %s on %s: odometer %d after %d, distance %s", r.Str("id"), tr.Str("license_plate"), odo, prev, r.Str("distance_km"))
					}
				} else if r["distance_km"] != nil {
					t.Errorf("fuel %s: the truck's first log has a distance", r.Str("id"))
				}
				lastOdo[tr.Str("id")] = odo
				key := r.Str("driver_id") + "|" + r.Str("tax_inv_id")
				if taxInv[key] || len(r.Str("station_tax_id")) != 13 || thaiID(r.Str("station_tax_id")[:12]) != r.Str("station_tax_id") {
					t.Errorf("fuel %s: tax invoice %s repeated or station tax id %s invalid", r.Str("id"), r.Str("tax_inv_id"), r.Str("station_tax_id"))
				}
				taxInv[key] = true
				l, _ := strconv.ParseFloat(r.Str("volume_liters"), 64)
				pr, _ := strconv.ParseFloat(r.Str("price_per_liter"), 64)
				if r.Str("amount_thb") != money(compute.FinalRateTHB(l, pr, 0)) {
					t.Errorf("fuel %s: %s L x %s = %s", r.Str("id"), r.Str("volume_liters"), r.Str("price_per_liter"), r.Str("amount_thb"))
				}
			}
			for at, seqs := range batches {
				slices.Sort(seqs)
				for i, n := range seqs {
					if n != i+1 {
						t.Errorf("toll import %s numbers its rows %v", at, seqs)
						break
					}
				}
			}
			mt := generated(t, p, "maintenance_records")
			if got := countBy(mt, func(r Row) string { return r.Str("status") }); len(mt) != 3 || got["completed"] != 1 ||
				got["scheduled"] != 1 || got["in_progress"] != 1 {
				t.Errorf("maintenance %v", got)
			}
			for _, r := range mt {
				tr := trucks[r.Str("truck_id")]
				switch r.Str("status") {
				case "completed":
					if tr.Str("last_service_date") != r.Str("end_date") || fmt.Sprint(tr["next_service_mileage"]) != fmt.Sprint(r["next_service_mileage"]) ||
						fmt.Sprint(tr["current_mileage"]) != fmt.Sprint(r["current_mileage"]) || tr["active_maintenance_id"] != nil {
						t.Errorf("completed PM on %s: the truck does not carry its service", tr.Str("license_plate"))
					}
				case "scheduled":
					if tr.Str("active_maintenance_id") != r.Str("id") || tr.Str("status") != "active" {
						t.Errorf("scheduled visit on %s: not the truck's active maintenance", tr.Str("license_plate"))
					}
				case "in_progress":
					if tr.Str("active_maintenance_id") != r.Str("id") || tr.Str("status") != "maintenance" {
						t.Errorf("repair in progress on %s: the truck is not in the shop", tr.Str("license_plate"))
					}
				}
			}
		})
	}
}

// planAnchor is Bangkok midnight of the plan's anchor: the day of its latest generated task (today's board).
func planAnchor(t *testing.T, p *Plan) time.Time {
	t.Helper()
	var last time.Time
	for _, k := range generated(t, p, "tasks") {
		if at := parseTS(t, k.Str("plan_at")); at.After(last) {
			last = at
		}
	}
	b := last.In(clock.Bangkok)
	return time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, clock.Bangkok)
}

// The T43 counts are a function of the window, never of SEED_RANDOM_SEED (Appendix D §D.1.2 states them exactly), except
// the statement lines: a line is a priced trip of the party, and the stream draws each trip's route (§D.1.2 gives their
// default-seed count as a target).
func TestFinanceCountsIgnoreTheSeed(t *testing.T) {
	a, b := plan(t, ProfileDemo), planSeed(t, ProfileDemo, integrationSeed)
	for _, table := range append(slices.Clone(finExactTables), "file_objects") {
		if len(a.Tables[table]) != len(b.Tables[table]) {
			t.Errorf("%s: %d rows with the default seed, %d with %d", table, len(a.Tables[table]), len(b.Tables[table]), integrationSeed)
		}
	}
	if reflectEqualRows(generated(t, a, "vehicle_expenses"), generated(t, b, "vehicle_expenses")) {
		t.Error("two seeds draw the same expenses: the generator ignores SEED_RANDOM_SEED")
	}
}

// Money text round-trips exactly through satang.
func TestSatang(t *testing.T) {
	for in, want := range map[string]int64{"1130.00": 113000, "-70.00": -7000, "0.05": 5, "12": 1200, "48699.5": 4869950} {
		got, err := satangOf(in)
		if err != nil || got != want {
			t.Errorf("satangOf(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if baht(-7005) != "-70.05" || baht(113000) != "1130.00" {
		t.Error("baht")
	}
	if _, err := satangOf("1.234"); err == nil {
		t.Error("three decimals accepted")
	}
}
