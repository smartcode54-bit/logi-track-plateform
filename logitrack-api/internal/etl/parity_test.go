package etl

import (
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// The parity classifier of `reconcile --finance` (T41) on synthetic inputs: every allow-list case of main spec §6.17
// is confirmed by the data or stays a mismatch, and every other difference is a mismatch with its reason.

func bkk(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, clock.Bangkok).UTC()
}

func f64(v float64) *float64 { return &v }

const (
	pDelivered = "party-delivered"
	pPlan      = "party-plan"
)

// testParity: a delivered-basis party with two PRIMARY rounds of HUBA -> SOCE 6WH (1000 from 1 Jul, 1100 from 1 Aug,
// both stored at UTC midnight = 07:00 ICT), a 4WJ card, a SUPPLEMENTARY card, a voided 1200 card of 5 Aug, a 5% fuel
// round from 1 Jul and a flat extra-stop fee of 50; a plan-basis party with 1000 from 1 Jul and 1200 from 1 Aug.
func testParity() *parityEngine {
	rate := func(id, party, dest, class string, cat compute.JobCategory, thb float64, eff time.Time, voided bool) compute.RateEntry {
		return compute.RateEntry{ID: id, LegacyDocID: id, PartyID: party, ImportID: "imp-" + id, HubCode: "HUBA", DestinationCode: dest,
			VehicleClass: class, RateTHB: thb, EffectiveFrom: eff, JobCategory: cat, Voided: voided}
	}
	jul, aug := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	return &parityEngine{
		hubs: compute.NewHubMaps([]compute.Hub{{Code: "SOCE", NameTH: "บัวโรย"}, {Code: "HUBB", NameTH: "บางนา"}}),
		tables: map[string]parityTables{
			pDelivered: {basis: compute.BasisDelivered,
				rates: []compute.RateEntry{
					rate("r1", pDelivered, "SOCE", "6WH", compute.Primary, 1000, jul, false),
					rate("r2", pDelivered, "SOCE", "6WH", compute.Primary, 1100, aug, false),
					rate("r3", pDelivered, "SOCE", "6WH", compute.Primary, 1200, aug.AddDate(0, 0, 4), true),
					rate("r4", pDelivered, "SOCE", "4WJ", compute.Primary, 900, jul, false),
					rate("r5", pDelivered, "SOCE", "6WH", compute.Supplementary, 800, jul, false),
					rate("r6", pDelivered, "HUBB", "6WH", compute.Primary, 400, jul, false),
					rate("r7", pDelivered, "SPK", "6WH", compute.Primary, 600, jul, false),
				},
				fuel:         []compute.FuelAdjustment{{ID: "f1", LegacyDocID: "f1", PartyID: pDelivered, EffectiveFrom: jul, RateMultiplier: 1.05}},
				extraStopFee: f64(50),
				standby: []compute.StandbyRate{{ID: "s1", LegacyDocID: "s1", PartyID: pDelivered, RateTHB: 300, EffectiveFrom: jul},
					{ID: "s2", LegacyDocID: "s2", PartyID: pDelivered, RateTHB: 350, EffectiveFrom: aug.AddDate(0, 0, 9), Voided: true}},
				standbyFee: f64(250)},
			pPlan: {basis: compute.BasisPlan, rates: []compute.RateEntry{
				rate("q1", pPlan, "SOCE", "6WH", compute.Primary, 1000, jul, false),
				rate("q2", pPlan, "SOCE", "6WH", compute.Primary, 1200, aug, false),
			}},
		},
	}
}

// trip is a delivered single trip of the delivered-basis party, HUBA -> SOCE 6WH, on 10 Aug 10:00 ICT, task category
// absent (legacy); stored is what Go prices it at unless the case changes it.
func trip(t *testing.T, e *parityEngine, mutate func(*parityTrip)) parityTrip {
	t.Helper()
	return tripWith(t, e, nil, mutate)
}

// tripWith applies prep to the trip before the stored price is taken from Go, mutate after.
func tripWith(t *testing.T, e *parityEngine, prep, mutate func(*parityTrip)) parityTrip {
	t.Helper()
	tr := parityTrip{key: "T", hasTask: true, stampedParty: pDelivered, hasSnapshot: true,
		in: compute.TripInput{Task: compute.TaskInput{TaskParties: compute.TaskParties{BillingPartyID: pDelivered}, SourceHub: "HUBA - Hub A",
			Destination: "SOCE", TruckType: "6WH"}, PlanAt: bkk(2026, 8, 10, 8, 0), DeliveredAt: bkk(2026, 8, 10, 10, 0), CreatedAt: bkk(2026, 8, 10, 6, 0)}}
	if prep != nil {
		prep(&tr)
	}
	got, err := e.priceTrip(tr.in, variant{})
	if err != nil {
		t.Fatal(err)
	}
	tr.stored = got
	if mutate != nil {
		mutate(&tr)
	}
	mult, add := tr.stored.mult, tr.stored.add
	tr.snapshot.RateMultiplier, tr.snapshot.AddTHBPerTrip = &mult, &add
	return tr
}

func expect(t *testing.T, name string, v verdict, outcome, kase string) {
	t.Helper()
	if v.outcome != outcome || v.kase != kase {
		t.Errorf("%s: %s %s (%s), want %s %s", name, v.outcome, v.kase, v.note, outcome, kase)
	}
}

func TestParityTripCases(t *testing.T) {
	e := testParity()
	if got := trip(t, e, nil).stored; got.estimate != 1155 || got.importID != "imp-r2" || got.round != "2026-07-01" {
		t.Fatalf("baseline price: %+v", got) // the fuel round's date is the round (§6.6)
	}

	expect(t, "exact", e.classifyTrip(trip(t, e, nil)), ParityExact, "")

	expect(t, "manual override is frozen", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.stored.estimate, tr.snapshot.ManualOverride = 1500, true
	})), ParityAllowed, CaseFrozen)

	expect(t, "clean SUPPLEMENTARY price is frozen", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.tripCategory = "SUPPLEMENTARY"
		tr.stored.estimate, tr.stored.mult = 999, 1
	})), ParityAllowed, CaseFrozen)

	blank := e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.in.Task.TruckType = ""
		tr.stored.estimate, tr.stored.importID = 945, "imp-r4"
	}))
	expect(t, "blank class", blank, ParityAllowed, CaseNoVehicleClass)
	if !strings.Contains(blank.note, "4WJ default reproduces it") {
		t.Errorf("blank class evidence: %s", blank.note)
	}
	expect(t, "class outside the enum", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.in.Task.TruckType, tr.legacyTruckType = "", "TRAILER"
	})), ParityMismatch, MismatchClassOutOfEnum)

	fid := "f1"
	expect(t, "SUPPLEMENTARY priced with fuel", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.tripCategory, tr.in.JobCategory = "SUPPLEMENTARY", "SUPPLEMENTARY"
		tr.stored.estimate, tr.stored.importID = 840, "imp-r5"
		tr.snapshot.FuelAdjustmentID = &fid
	})), ParityAllowed, CaseSuppPricedWithFuel)

	voided := func(tr *parityTrip) {
		tr.stored.estimate, tr.stored.importID, tr.stored.round = 1260, "imp-r3", "2026-07-01"
		tr.voided = []string{"rate entry r3"}
	}
	expect(t, "priced against a card voided since", e.classifyTrip(trip(t, e, voided)), ParityAllowed, CaseSinceVoided)
	expect(t, "a voided card that does not explain the price", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		voided(tr)
		tr.stored.estimate = 1261
	})), ParityMismatch, MismatchAmount)
	expect(t, "a different card without a void", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		voided(tr)
		tr.voided = nil
	})), ParityMismatch, MismatchAmount)

	planTrip := func(tr *parityTrip) {
		tr.stampedParty = pPlan
		tr.in.Task.BillingPartyID = pPlan
		tr.in.PlanAt = bkk(2026, 8, 2, 8, 0)
	}
	expect(t, "plan date drifted", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		planTrip(tr)
		tr.billingDate = bkk(2026, 7, 30, 8, 0)
		tr.stored = price{priced: true, party: pPlan, estimate: 1000, importID: "imp-q1", mult: 1, round: "2026-07-01"}
	})), ParityAllowed, CasePlanDateDrifted)
	expect(t, "plan-basis trip without billingDate priced on its delivery (D7)", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		planTrip(tr)
		tr.in.DeliveredAt = bkk(2026, 7, 31, 10, 0)
		tr.stored = price{priced: true, party: pPlan, estimate: 1000, importID: "imp-q1", mult: 1}
	})), ParityMismatch, MismatchMissingBillDate)

	expect(t, "stale provenance after setTripJobCategory", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.tripCategory = "PRIMARY"
		tr.stored.round = "2026-06-01"
	})), ParityAllowed, CaseStaleProvenance)
	expect(t, "stale provenance without an explicit category", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.stored.round = "2026-06-01"
	})), ParityMismatch, MismatchProvenance)
	expect(t, "a stale import id on a single trip is not setTripJobCategory's", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.tripCategory = "PRIMARY"
		tr.stored.importID = "imp-other"
	})), ParityMismatch, MismatchProvenance)

	expect(t, "legacy unpriced, Go prices", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.stored = price{}
	})), ParityMismatch, MismatchNotComputed)
	expect(t, "both unpriced", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.in.Task.Destination = "NOWHERE"
		tr.stored = price{}
	})), ParityExact, "")
	expect(t, "legacy priced, Go finds no card", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.in.Task.Destination = "NOWHERE"
	})), ParityMismatch, MismatchGoUnpriced)

	expect(t, "pre-ADR-0009 switch-day boundary", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.in.DeliveredAt = bkk(2026, 8, 1, 3, 0)
		tr.stored.estimate, tr.stored.importID = 1050, "imp-r1"
	})), ParityMismatch, MismatchSwitchDay)

	expect(t, "the task resolves another party", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.stampedParty, tr.stored.party = pPlan, pPlan
		tr.stored.estimate = 1
	})), ParityMismatch, MismatchParty)

	expect(t, "task missing", e.classifyTrip(trip(t, e, func(tr *parityTrip) { tr.hasTask = false })), ParityMismatch, MismatchTaskMissing)
	expect(t, "multi-drop with one delivered stop", e.classifyTrip(trip(t, e, func(tr *parityTrip) {
		tr.in.IsMultiDelivery, tr.in.StopProgressCount, tr.in.DeliveredStops = true, 2, []string{"SOCE"}
	})), ParityMismatch, MismatchMultiStops)
}

func TestParityMultiDrop(t *testing.T) {
	e := testParity()
	multi := func(tr *parityTrip) {
		tr.in.IsMultiDelivery, tr.in.StopProgressCount, tr.in.DeliveredStops = true, 2, []string{"HUBB", "SOCE"}
	}
	mtrip := func(mutate func(*parityTrip)) parityTrip { return tripWith(t, e, multi, mutate) }
	base := mtrip(nil)
	if !base.stored.multi || base.stored.estimate != 1205 {
		t.Fatalf("multi-drop baseline: %+v", base.stored) // 1100 * 1.05 + the flat fee 50
	}
	expect(t, "exact", e.classifyTrip(base), ParityExact, "")
	expect(t, "within 0.005 THB of the raw float sum", e.classifyTrip(mtrip(func(tr *parityTrip) {
		tr.stored.estimate = 1205.005
	})), ParityAllowed, CaseMultiDropTolerance)
	expect(t, "beyond the tolerance", e.classifyTrip(mtrip(func(tr *parityTrip) {
		tr.stored.estimate = 1205.02
	})), ParityMismatch, MismatchAmount)
	expect(t, "priced with the fee the owner's D5 decision replaced", e.classifyTrip(mtrip(func(tr *parityTrip) {
		tr.stored.estimate, tr.storedFee = 1215, f64(60)
	})), ParityMismatch, MismatchFeeChanged)
	expect(t, "multi-drop stale add and import id", e.classifyTrip(mtrip(func(tr *parityTrip) {
		tr.tripCategory = "PRIMARY"
		tr.stored.add, tr.stored.importID = -10, "imp-old"
	})), ParityAllowed, CaseStaleProvenance)
}

func TestParityDiff(t *testing.T) {
	s := price{priced: true, estimate: 100, mult: 1, importID: "a"}
	if f, tol := s.diff(price{priced: true, estimate: 100, mult: 1, importID: "a", round: "2026-08-01"}); len(f) != 0 || tol {
		t.Errorf("a round the legacy record does not store is not compared: %v", f)
	}
	s.round = "2026-08-01"
	if f, _ := s.diff(price{priced: true, estimate: 100, mult: 1, importID: "a", round: "2026-08-01", bandLo: f64(32.01), bandHi: f64(33)}); len(f) != 1 || f[0] != "band" {
		t.Errorf("with a stored round, a band the legacy record lacks differs: %v", f)
	}
	if f, _ := (price{priced: true, estimate: 100, mult: 1}).diff(price{priced: true, estimate: 100, mult: 1, importID: "x"}); len(f) != 0 {
		t.Errorf("an import id the legacy record lacks is not compared: %v", f)
	}
	if f, _ := (price{priced: true, estimate: 100.004, mult: 1.0500001}).diff(price{priced: true, estimate: 100, mult: 1.05}); len(f) != 0 {
		t.Errorf("satang and 6 places: %v", f)
	}
	if f, tol := (price{priced: true, estimate: 1205, multi: true, mult: 1}).diff(price{priced: true, estimate: 1205.004999, mult: 1}); len(f) != 0 || tol {
		t.Errorf("equal satang is exact, not the tolerance: %v %v", f, tol)
	}
}

func TestParityStandby(t *testing.T) {
	e := testParity()
	in := compute.StandbyInput{CustomerPartyID: pDelivered, EndedAt: bkk(2026, 8, 20, 10, 0), CreatedAt: bkk(2026, 8, 20, 10, 0)}
	sb := func(stored standbyPrice) parityStandby { return parityStandby{key: "S", in: in, stored: stored} }
	v := e.classifyStandby(sb(standbyPrice{priced: true, estimate: 300, source: compute.RateSourceStandbyRate, entryID: "s1", entryDate: "2026-07-01"}))
	if v.outcome != ParityExact {
		t.Errorf("exact: %+v", v)
	}
	voided := sb(standbyPrice{priced: true, estimate: 350, source: compute.RateSourceStandbyRate, entryID: "s2"})
	voided.voided, voided.entryLegacy = true, "s2"
	if v := e.classifyStandby(voided); v.outcome != ParityAllowed || v.kase != CaseSinceVoided {
		t.Errorf("voided since: %+v", v)
	}
	if v := e.classifyStandby(sb(standbyPrice{priced: true, estimate: 300, source: compute.RateSourceServiceFee})); v.kase != MismatchStandbySource {
		t.Errorf("priced from the other source: %+v", v)
	}
	if v := e.classifyStandby(sb(standbyPrice{})); v.kase != MismatchNotComputed {
		t.Errorf("legacy unpriced: %+v", v)
	}
	// No standby rate before 1 Jul: the oldest rate is selected (§6.9); without rates the fee prices it.
	noRates := testParity()
	tbl := noRates.tables[pDelivered]
	tbl.standby = nil
	noRates.tables[pDelivered] = tbl
	if v := noRates.classifyStandby(sb(standbyPrice{priced: true, estimate: 250, source: compute.RateSourceServiceFee})); v.outcome != ParityExact {
		t.Errorf("service fee: %+v", v)
	}
	if v := noRates.classifyStandby(sb(standbyPrice{priced: true, estimate: 300, source: compute.RateSourceServiceFee})); v.kase != MismatchFeeChanged {
		t.Errorf("priced with a fee the owner replaced: %+v", v)
	}
	// Divergence #9: the task names another billing party, which the standby rule ignores.
	d9 := sb(standbyPrice{})
	d9.taskBillingParty = pPlan
	if !d9.divergence9() {
		t.Error("a task billing party other than the standby party is divergence #9")
	}
	d9.taskBillingParty = pDelivered
	if d9.divergence9() {
		t.Error("the same party is not")
	}
}

func TestCollapsesOnDash(t *testing.T) {
	for _, c := range []struct {
		in, key string
		ok      bool
	}{
		{"SPK-GW", "SPK", true}, {" spk-x ", "SPK", true}, {"SPK890103 - ลาดกระบัง26", "SPK890103", false},
		{"SOCE-1", "SOCE", false}, {"SOCE (บัวโรย)", "SOCE", false}, {"HUBA", "HUBA", false}, {"", "", false}, {"-X", "-X", false},
	} {
		if key, ok := collapsesOnDash(c.in); key != c.key || ok != c.ok {
			t.Errorf("%q: %q %v, want %q %v", c.in, key, ok, c.key, c.ok)
		}
	}
}

func TestFinanceWindow(t *testing.T) {
	now := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)
	w, err := newFinanceWindow("", 3, now)
	if err != nil || strings.Join(w.Months, ",") != "2026-07,2026-08,2026-09" {
		t.Fatalf("default window: %v %v", w.Months, err)
	}
	if !w.from.Equal(bkk(2026, 7, 1, 0, 0)) || !w.to.Equal(bkk(2026, 10, 1, 0, 0)) {
		t.Errorf("bounds %v %v", w.from, w.to)
	}
	if !w.has(bkk(2026, 9, 30, 23, 59)) || w.has(bkk(2026, 10, 1, 0, 0)) || w.has(bkk(2026, 6, 30, 23, 59)) {
		t.Error("Bangkok month bounds")
	}
	// 30 Sep 17:30 UTC is already October in Bangkok: September is closed.
	if w, err := newFinanceWindow("", 1, time.Date(2026, 9, 30, 17, 30, 0, 0, time.UTC)); err != nil || w.Months[0] != "2026-09" {
		t.Errorf("Bangkok now: %v %v", w.Months, err)
	}
	if w, err := newFinanceWindow("2026-01", 2, now); err != nil || strings.Join(w.Months, ",") != "2025-12,2026-01" {
		t.Errorf("across a year: %v %v", w.Months, err)
	}
	for _, bad := range []struct {
		through string
		months  int
	}{{"2026-10", 3}, {"2026-13", 3}, {"", 0}, {"", 25}} {
		if _, err := newFinanceWindow(bad.through, bad.months, now); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestFeeWinner(t *testing.T) {
	rows := []feeRow{{legacy: "a", amount: 50}, {legacy: "b", amount: 60}}
	if *feeWinner(rows, false) != 50 || *feeWinner(rows, true) != 60 || feeWinner(nil, false) != nil {
		t.Error("extra_stop takes the first by doc id, standby the last (D5 legacy winners)")
	}
	if invoiceSeq("CJSF-202609-012", "CJSF", 2026, 9) != 12 || invoiceSeq("CJSF-202609-1000", "CJSF", 2026, 9) != 1000 ||
		invoiceSeq("CJSF-202608-001", "CJSF", 2026, 9) != 0 || invoiceSeq("ST-SPX-2026-08", "SPX", 2026, 8) != 0 {
		t.Error("invoiceSeq")
	}
	if !sameJSON([]any{map[string]any{"price": 32.94}}, []byte(`[{"price": 32.94}]`)) || !sameJSON(nil, []byte(`[]`)) {
		t.Error("sameJSON")
	}
}
