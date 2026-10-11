package etl

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// The parity dry run of `etl reconcile --finance` (T41, issue #61; main spec §6.17 (2), R72): every delivered trip and
// completed standby record of the last closed Bangkok months is priced again by the Go engine (internal/billing/compute,
// the pure core every Go price writer wraps) from the rows the load wrote, and compared with the stored legacy price
// and its provenance (importId, multiplier, add, round date, band). Nothing is written and nothing is locked: the
// inputs are read without FOR SHARE in the reconcile transaction, whose only write is its etl.reconciliation_runs row.
//
// A difference passes only as a case of the owner's allow-list (§6.17), confirmed where the data allows by a second
// price under the condition that explains it (the voided rows live again, the stored billing date as the plan date,
// the legacy 4WJ default) or by the trace the explaining path leaves in the stored record (setTripJobCategory): a case
// the data does not confirm stays a mismatch. Every other difference is a mismatch with its reason; some
// reasons carry an explanation (a legacy defect fixed since, an owner decision still open) but fail all the same,
// because only the owner can widen the allow-list. This file is pure; parity_load.go reads the inputs.

// Parity outcomes of one record.
const (
	ParityExact    = "exact"    // the recompute equals the stored price and provenance (legacy multi-drop: within 0.005 THB)
	ParityAllowed  = "allowed"  // a difference on the allow-list; Case names it
	ParityMismatch = "mismatch" // any other difference; Case names its reason
)

// Allow-list cases (§6.17) and the multi-drop tolerance (R20).
const (
	CaseFrozen             = "frozen"                           // not recomputed; the snapshot equals the dump
	CaseNoVehicleClass     = "no_vehicle_class"                 // legacy 4WJ price vs no_vehicle_class (R15)
	CaseSuppPricedWithFuel = "supp_priced_with_fuel"            // SUPPLEMENTARY priced with fuel by the removed browser writer
	CaseSinceVoided        = "since_voided"                     // priced against a rate entry or fuel round voided since
	CasePlanDateDrifted    = "plan_date_drifted"                // the plan date moved after pricing
	CaseStaleProvenance    = "set_trip_job_category_provenance" // amount equal, provenance left stale (§6.18 #2)
	CaseMultiDropTolerance = "multi_drop_tolerance"             // legacy multi-drop float sum within 0.005 THB
)

// Mismatch reasons.
const (
	MismatchAmount          = "amount_differs"
	MismatchProvenance      = "provenance_differs"
	MismatchNotComputed     = "not_computed"              // legacy left it unpriced; the rules price it
	MismatchGoUnpriced      = "go_unpriced"               // legacy priced it; the rules do not
	MismatchTaskMissing     = "task_missing"              // the trip's task did not load (task_unresolved, task_ambiguous)
	MismatchMultiStops      = "multi_insufficient_stops"  // a multi-drop trip with fewer than two delivered stops
	MismatchParty           = "party_differs"             // the record resolves another party than the one stamped
	MismatchClassOutOfEnum  = "vehicle_class_out_of_enum" // the task's truck type is outside the enum; legacy folded it
	MismatchMissingBillDate = "missing_billing_date"      // D7: a plan-basis trip priced on its delivery (§19 Q17)
	MismatchSwitchDay       = "switch_day_boundary"       // priced under the previous round by the pre-ADR-0009 07:00 boundary
	MismatchFeeChanged      = "fee_changed"               // the party's extra_stop / standby fee is not the one priced (D5, an edit)
	MismatchStandbySource   = "rate_source_differs"       // a standby record priced from the other source
)

// price is one side of a comparison: a stored legacy price or a Go recompute.
type price struct {
	priced   bool
	reason   string // the unpriced reason ("" when a legacy record stores none)
	party    string // the stamped billing party; Go's resolved party ("" when absent)
	fuelID   string // Go: the fuel round it applied ("" when none; the stored one is on the parityTrip snapshot)
	estimate float64
	multi    bool
	importID string // "" when absent
	mult     float64
	add      float64
	round    string // yyyy-MM-dd, "" when absent
	bandLo   *float64
	bandHi   *float64
}

// fromTripPrice is the comparable part of a Go price.
func fromTripPrice(r compute.TripResult) price {
	if r.Price == nil {
		return price{reason: string(r.Reason), party: r.PartyID}
	}
	p := r.Price
	return price{priced: true, party: p.PartyID, fuelID: p.FuelAdjustmentID, estimate: p.EstimateTHB, multi: p.IsMultiDelivery, importID: p.RateImportID,
		mult: p.RateMultiplier, add: p.AddTHBPerTrip, round: p.RoundEffectiveFromDate, bandLo: p.FuelBandLowerTHB, bandHi: p.FuelBandUpperTHB}
}

func money(f float64) string { return strconv.FormatFloat(float64(cents(f))/100, 'f', 2, 64) }

func (p price) String() string {
	if !p.priced {
		if p.reason == "" {
			return "unpriced"
		}
		return "unpriced (" + p.reason + ")"
	}
	var b strings.Builder
	b.WriteString(strconv.FormatFloat(p.estimate, 'f', -1, 64))
	if p.multi {
		b.WriteString(" multi")
	}
	if p.importID != "" {
		b.WriteString(" import " + p.importID)
	}
	fmt.Fprintf(&b, " x%s +%s", strconv.FormatFloat(p.mult, 'f', -1, 64), money(p.add))
	if p.round != "" {
		b.WriteString(" round " + p.round)
	}
	if p.bandLo != nil && p.bandHi != nil {
		b.WriteString(" band " + money(*p.bandLo) + "-" + money(*p.bandHi))
	}
	return b.String()
}

// diff lists the fields where got differs from the stored price, and whether the amounts are equal only within the
// legacy multi-drop tolerance. Provenance is compared where the stored record carries it: the billing party when
// stamped, the import id when stored, the round date when stored (ADR 0009, 2026-08-09) and the band whenever the round
// date is (the legacy writer stores an explicit null band then); a missing multiplier is 1 and a missing add 0 (the
// column defaults).
func (stored price) diff(got price) (fields []string, tolerance bool) {
	if stored.priced != got.priced {
		return []string{"amount"}, false
	}
	if !stored.priced {
		return nil, false
	}
	switch {
	case cents(stored.estimate) == cents(got.estimate):
	case stored.multi && math.Abs(stored.estimate-got.estimate) <= 0.005+1e-9:
		tolerance = true
	default:
		fields = append(fields, "amount")
	}
	if stored.party != "" && got.party != "" && stored.party != got.party {
		fields = append(fields, "party")
	}
	if stored.importID != "" && stored.importID != got.importID {
		fields = append(fields, "importId")
	}
	if strconv.FormatFloat(stored.mult, 'f', 6, 64) != strconv.FormatFloat(got.mult, 'f', 6, 64) {
		fields = append(fields, "multiplier")
	}
	if cents(stored.add) != cents(got.add) {
		fields = append(fields, "add")
	}
	if stored.round != "" {
		if stored.round != got.round {
			fields = append(fields, "roundDate")
		}
		if !sameMoney(stored.bandLo, got.bandLo) || !sameMoney(stored.bandHi, got.bandHi) {
			fields = append(fields, "band")
		}
	}
	return fields, tolerance
}

func sameMoney(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return cents(*a) == cents(*b)
}

// staleFields are the provenance fields setTripJobCategory leaves as the earlier pricing wrote them
// (fn:tripBillingOnDelivered.ts:849-902): the round and band always; on a multi-drop trip also the add and the import id.
func staleFields(multi bool) []string {
	if multi {
		return []string{"roundDate", "band", "add", "importId"}
	}
	return []string{"roundDate", "band"}
}

// parityTables are one party's pricing inputs of the billing carrier, voided rows included (the selectors skip them).
type parityTables struct {
	basis        compute.BillingDateBasis
	rates        []compute.RateEntry
	fuel         []compute.FuelAdjustment
	standby      []compute.StandbyRate
	extraStopFee *float64 // the D5 legacy winner while duplicates remain: the first by legacy doc id
	standbyFee   *float64 // the D5 legacy winner: the last by legacy doc id
}

// voidedLive is the tables as they stood before the voids: every voided row selectable again.
func (t parityTables) voidedLive() parityTables {
	out := t
	out.rates = slices.Clone(t.rates)
	for i := range out.rates {
		out.rates[i].Voided = false
	}
	out.fuel = slices.Clone(t.fuel)
	for i := range out.fuel {
		out.fuel[i].Voided = false
	}
	out.standby = slices.Clone(t.standby)
	for i := range out.standby {
		out.standby[i].Voided = false
	}
	return out
}

// parityEngine prices records of the window again.
type parityEngine struct {
	hubs   compute.HubMaps
	tables map[string]parityTables // by billing party id
}

// variant changes one input of a recompute (the explanation it tests).
type variant struct {
	voidedLive bool
	basis      compute.BillingDateBasis // "" keeps the party's
	in         *compute.TripInput       // nil keeps the record's input
	extraFee   *float64                 // non-nil replaces the party's extra_stop fee
}

func (e *parityEngine) priceTrip(in compute.TripInput, v variant) (price, error) {
	if v.in != nil {
		in = *v.in
	}
	party, _ := compute.ResolveTaskParty(in.Task.TaskParties)
	t := e.tables[party]
	if v.voidedLive {
		t = t.voidedLive()
	}
	basis := t.basis
	if v.basis != "" {
		basis = v.basis
	}
	fee := t.extraStopFee
	if v.extraFee != nil {
		fee = v.extraFee
	}
	r, err := compute.PriceTrip(in, compute.Tables{Basis: basis, Rates: t.rates, Fuel: t.fuel, ExtraStopFeeTHB: fee, Hubs: e.hubs})
	if err != nil {
		return price{}, err
	}
	return fromTripPrice(r), nil
}

// parityTrip is one delivered trip of the window as the load wrote it.
type parityTrip struct {
	key, party, period string // report labels
	stampedParty       string // trip_records.billing_party_id ("" when NULL)
	billingDate        time.Time
	tripCategory       string // trip_records.job_category ("" when NULL)
	hasTask            bool
	legacyTruckType    string // tasks.legacy_truck_type: a class outside the enum the legacy engine folded
	in                 compute.TripInput
	hasSnapshot        bool
	snapshot           compute.Snapshot
	stored             price
	storedFuelDate     string   // billingEffectiveFromDateStr as loaded (yyyy-MM-dd, "" when absent)
	overrideFalse      bool     // the dump document stores billingManualOverride = false (setTripJobCategory -> PRIMARY)
	voided             []string // legacy ids of the voided rate entry / fuel round the snapshot names
	storedFee          *float64 // the flat extra-stop fee of the stored breakdown (lines 2+ with base = final)
	divergence1        bool     // a destination the SPK dash collision truncates (§6.18 #1)
}

// verdict is the classification of one record.
type verdict struct {
	outcome, kase, note string
	got                 price
}

// classifyTrip prices one trip again and classifies the result (§6.17).
func (e *parityEngine) classifyTrip(t parityTrip) verdict {
	if t.hasSnapshot && t.stored.priced && compute.IsFrozen(t.snapshot, compute.JobCategory(t.tripCategory)) {
		return verdict{outcome: ParityAllowed, kase: CaseFrozen, note: "frozen snapshot (manual override or a clean SUPPLEMENTARY price): not recomputed"}
	}
	// Neither engine prices a trip without its task (fn:tripBillingOnDelivered.ts:300-317, §6.10 state 4) nor a
	// multi-drop trip with fewer than two delivered stops (legacy throws): an unpriced pair is equal, a legacy price is
	// a mismatch.
	if !t.hasTask {
		got := price{reason: MismatchTaskMissing}
		if !t.stored.priced {
			return verdict{outcome: ParityExact, got: got}
		}
		return verdict{outcome: ParityMismatch, kase: MismatchTaskMissing, got: got, note: "legacy priced it; its task no longer loads " +
			"(a blank taskId, task_unresolved or task_ambiguous): Go cannot price it; fix the task link at source"}
	}
	got, err := e.priceTrip(t.in, variant{})
	if err != nil {
		got := price{reason: MismatchMultiStops}
		if !t.stored.priced {
			return verdict{outcome: ParityExact, got: got}
		}
		return verdict{outcome: ParityMismatch, kase: MismatchMultiStops, got: got, note: "legacy priced it; a multi-drop trip with fewer " +
			"than two delivered stops prices nothing (legacy throws on a recompute)"}
	}
	v := verdict{got: got}
	fields, tolerance := t.stored.diff(got)
	if len(fields) == 0 {
		v.outcome = ParityExact
		if tolerance {
			v.outcome, v.kase, v.note = ParityAllowed, CaseMultiDropTolerance, "legacy multi-drop float sum within 0.005 THB (R20)"
		}
		return v
	}
	same := func(alt variant) bool {
		p, err := e.priceTrip(t.in, alt)
		if err != nil {
			return false
		}
		f, _ := t.stored.diff(p)
		return len(f) == 0
	}
	allowed := func(kase, note string) verdict {
		v.outcome, v.kase, v.note = ParityAllowed, kase, note
		return v
	}
	explained := func(kase, note string) verdict {
		v.outcome, v.kase, v.note = ParityMismatch, kase, note
		return v
	}
	planDrift := t.stored.priced && e.tables[got.party].basis == compute.BasisPlan && !t.billingDate.IsZero() &&
		!t.in.PlanAt.IsZero() && clock.DateString(t.billingDate) != clock.DateString(t.in.PlanAt)
	var asPriced *compute.TripInput
	if planDrift {
		in := t.in
		in.PlanAt = t.billingDate
		asPriced = &in
	}

	// The display category (the trip's, else the task's; ADR 0010): the removed browser writer never stamped the
	// trip's own category, so its SUPPLEMENTARY trips may carry the label on their task only.
	display, _ := compute.ResolveDisplayJobCategory(t.tripCategory, t.in.JobCategory)
	switch {
	case t.stored.priced && !got.priced && got.reason == string(compute.NoVehicleClass):
		if t.legacyTruckType != "" {
			return explained(MismatchClassOutOfEnum, "the task's truck type "+strconv.Quote(t.legacyTruckType)+
				" is outside the task enum (vehicle_class_out_of_enum): legacy priced it, Go never guesses a class; fix the task at source")
		}
		return e.classifyBlankClass(t, got)
	case t.stored.priced && display == compute.Supplementary && t.hasSnapshot && compute.CarriesFuel(t.snapshot) &&
		!t.snapshot.ManualOverride:
		// Allowed on the data condition alone: the browser writer dropped the rate's category, so its card choice is
		// not reproducible; the owner's rule repairs the row by a forced recompute after the cut-over.
		return allowed(CaseSuppPricedWithFuel, "SUPPLEMENTARY priced with fuel by the removed browser writer: a forced recompute after the cut-over repairs it")
	case len(t.voided) > 0 && (same(variant{voidedLive: true}) || (asPriced != nil && same(variant{voidedLive: true, in: asPriced}))):
		return allowed(CaseSinceVoided, "priced against "+strings.Join(t.voided, ", ")+", voided since")
	case planDrift && same(variant{in: asPriced}):
		return allowed(CasePlanDateDrifted, "the plan date moved from "+clock.DateString(t.billingDate)+" to "+
			clock.DateString(t.in.PlanAt)+" after pricing; the stored billing date reproduces the price")
	}
	if t.stored.priced {
		if basis := e.tables[got.party].basis; basis == compute.BasisPlan && t.billingDate.IsZero() &&
			same(variant{basis: compute.BasisDelivered}) {
			return explained(MismatchMissingBillDate, "a plan-basis trip without billingDate priced on its delivery (D7, §19 Q17): "+
				"restamp it in Firestore before the final dump, or let the Go restamp repair it after the flip")
		}
		if alt, ok := switchDayInput(t.in, e.tables[got.party].basis); ok && same(variant{in: &alt}) {
			return explained(MismatchSwitchDay, "delivered before 07:00 ICT on a round's switch day and priced under the previous round "+
				"by the pre-ADR-0009 07:00 boundary (billingImpactReport): not repaired retroactively (owner, 2026-08-04)")
		}
		if t.stored.multi && t.storedFee != nil && same(variant{extraFee: t.storedFee}) {
			return explained(MismatchFeeChanged, "priced with an extra_stop fee of "+money(*t.storedFee)+
				"; the party's fee is now another (the owner's D5 winner or a later edit)")
		}
	}
	if t.stored.priced && got.priced && !slices.Contains(fields, "amount") && subset(fields, staleFields(t.stored.multi)) &&
		(t.stampedParty == "" || got.party == t.stampedParty) && e.staleSignature(t, got) {
		return allowed(CaseStaleProvenance, "amount equal; "+strings.Join(fields, ", ")+" left stale by setTripJobCategory (§6.18 #2), "+
			"which the stored record shows: provenance excluded")
	}
	switch {
	case !t.stored.priced && got.priced:
		return explained(MismatchNotComputed, "legacy left it unpriced; the rules price it "+money(got.estimate)+
			": run the legacy backfill before the final dump, or the Go backfill after the flip")
	case t.stored.priced && !got.priced:
		return explained(MismatchGoUnpriced, "legacy priced it; Go answers "+got.reason)
	case slices.Contains(fields, "party"):
		return explained(MismatchParty, "the task now resolves another billing party than the one stamped at pricing; differs in "+
			strings.Join(fields, ", "))
	case slices.Contains(fields, "amount"):
		return explained(MismatchAmount, "differs in "+strings.Join(fields, ", "))
	}
	return explained(MismatchProvenance, "amount equal; differs in "+strings.Join(fields, ", "))
}

// classifyBlankClass classifies a trip whose task has a blank vehicle class, which legacy priced with its 4WJ default
// and Go answers no_vehicle_class (R15): the allow-list holds that difference only, so the record is classified again
// with the legacy default and passes only as that classification passes. One level: with 4WJ the class is never blank.
func (e *parityEngine) classifyBlankClass(t parityTrip, got price) verdict {
	legacy := t
	legacy.in.Task.TruckType = "4WJ"
	v := e.classifyTrip(legacy)
	v.got = got
	switch {
	case v.outcome == ParityExact || v.kase == CaseMultiDropTolerance:
		note := "blank vehicle class: legacy priced it, Go answers no_vehicle_class (R15); the legacy 4WJ default reproduces it"
		if v.kase == CaseMultiDropTolerance {
			note += " within 0.005 THB (R20)"
		}
		v.outcome, v.kase, v.note = ParityAllowed, CaseNoVehicleClass, note
	case v.outcome == ParityAllowed:
		v.note = "blank vehicle class (no_vehicle_class, R15); with the legacy 4WJ default: " + v.note
	default:
		v.note = "blank vehicle class (no_vehicle_class, R15) and the legacy 4WJ default does not reproduce it: " + v.note
	}
	return v
}

// staleSignature reports whether the stored record carries the trace setTripJobCategory alone leaves
// (fn:tripBillingOnDelivered.ts:849-920, §6.18 #2) and Go selects the fuel round that path selected. The path writes
// billingManualOverride = false explicitly (a flip to PRIMARY; one to SUPPLEMENTARY freezes the trip), which the
// delivery writer never writes and a browser writer writes only as true. It rewrites the fuel round id on both shapes,
// and billingEffectiveFromDateStr on a single trip, but never the round date or band (nor, on a multi-drop trip, the
// add, the import id or billingEffectiveFromDateStr). The delivery writer stamps the round as the applied fuel round's
// Bangkok date (else the card's), the band from that round's reference price and the fuel date with the fuel id, so a
// record whose rewritten fields contradict the ones left is that path's trace. A record without a stored round (before
// ADR 0009) or without the trace is not confirmed.
func (e *parityEngine) staleSignature(t parityTrip, got price) bool {
	if !t.overrideFalse || t.tripCategory == "" || t.stored.round == "" {
		return false
	}
	storedID := ""
	if t.snapshot.FuelAdjustmentID != nil {
		storedID = strings.TrimSpace(*t.snapshot.FuelAdjustmentID)
	}
	var row *compute.FuelAdjustment
	if storedID != "" {
		fuel := e.tables[got.party].fuel
		for i := range fuel {
			if fuel[i].ID == storedID || fuel[i].LegacyDocID == storedID {
				row = &fuel[i]
				break
			}
		}
		if row == nil {
			return false
		}
	}
	if (row == nil) != (got.fuelID == "") || (row != nil && row.ID != got.fuelID) {
		return false // Go selects another fuel round than the path did: a selection difference, not staleness
	}
	fuelDate := ""
	if row != nil {
		fuelDate = clock.DateString(row.EffectiveFrom)
	}
	switch {
	case row != nil && fuelDate != t.stored.round: // the round is not the stored fuel round's date
		return true
	case row != nil && t.stored.bandLo == nil && row.ReferenceFuelPriceTHB != nil: // that round has a band the record lacks
		return true
	case row == nil && t.stored.bandLo != nil: // a band without a fuel round
		return true
	case t.stored.multi && t.storedFuelDate != fuelDate: // multi-drop: the fuel date of another round than the id
		return true
	}
	return false
}

func subset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// switchDayInput moves the trip's billing instant to the end of the previous Bangkok day when it falls before 07:00
// ICT: the rounds a rate or fuel row stored at 07:00 ICT (UTC midnight) selected before ADR 0009 compared instants.
func switchDayInput(in compute.TripInput, basis compute.BillingDateBasis) (compute.TripInput, bool) {
	dates := compute.BillingDates{Basis: basis, PlanAt: in.PlanAt, DeliveredAt: in.DeliveredAt, CreatedAt: in.CreatedAt}
	bill, ok := dates.For()
	if !ok || bill.In(clock.Bangkok).Hour() >= 7 {
		return in, false
	}
	b := bill.In(clock.Bangkok)
	prev := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, clock.Bangkok).Add(-time.Millisecond).UTC()
	out := in
	switch {
	case basis == compute.BasisPlan && bill.Equal(in.PlanAt):
		out.PlanAt = prev
	case bill.Equal(in.DeliveredAt):
		out.DeliveredAt = prev
	default:
		out.CreatedAt = prev
	}
	return out, true
}

// standbyPrice is one side of a standby comparison.
type standbyPrice struct {
	priced    bool
	reason    string
	party     string // the stamped billing party; Go's resolved party ("" when absent)
	estimate  float64
	source    string // standby_rate | service_fee
	entryID   string // standby_rate_entries.id
	entryDate string // yyyy-MM-dd
}

func (p standbyPrice) String() string {
	if !p.priced {
		if p.reason == "" {
			return "unpriced"
		}
		return "unpriced (" + p.reason + ")"
	}
	s := money(p.estimate) + " " + p.source
	if p.entryDate != "" {
		s += " " + p.entryDate
	}
	return s
}

// parityStandby is one completed standby record of the window.
type parityStandby struct {
	key, party, period string
	partyID            string // the billing party (stamped, else the standby rule's): the report label and the lock
	in                 compute.StandbyInput
	stored             standbyPrice
	entryLegacy        string // the stored rate entry's legacy doc id (the report label)
	voided             bool   // the stored rate entry is voided now
	taskBillingParty   string // the task's explicit billing party, which standby ignores (§6.18 #9)
}

// divergence9 reports whether the record is one the preserved divergence #9 decides: its task names an explicit
// billing party other than the party the standby rule resolves.
func (s parityStandby) divergence9() bool {
	if s.taskBillingParty == "" {
		return false
	}
	party, _ := compute.StandbyParty(s.in)
	return party != s.taskBillingParty
}

type standbyVerdict struct {
	outcome, kase, note string
	got                 standbyPrice
}

func (e *parityEngine) priceStandby(in compute.StandbyInput, voidedLive bool, fee *float64) standbyPrice {
	party, _ := compute.StandbyParty(in)
	t := e.tables[party]
	if voidedLive {
		t = t.voidedLive()
	}
	if fee == nil {
		fee = t.standbyFee
	}
	r := compute.PriceStandby(in, t.standby, fee)
	if r.Price == nil {
		return standbyPrice{reason: string(r.Reason), party: r.PartyID}
	}
	return standbyPrice{priced: true, party: r.PartyID, estimate: r.Price.EstimateTHB, source: r.Price.RateSource, entryID: r.Price.RateEntryID,
		entryDate: r.Price.EffectiveFromDate}
}

// standbyDiff compares a standby price: the amount always; the billing party when stamped (§6.18 #9 decides it); the
// source and rate entry when the stored record names a source; the effective date when stored.
func standbyDiff(stored, got standbyPrice) []string {
	if stored.priced != got.priced {
		return []string{"amount"}
	}
	if !stored.priced {
		return nil
	}
	var f []string
	if cents(stored.estimate) != cents(got.estimate) {
		f = append(f, "amount")
	}
	if stored.party != "" && got.party != "" && stored.party != got.party {
		f = append(f, "party")
	}
	if stored.source != "" && (stored.source != got.source || (stored.source == compute.RateSourceStandbyRate && stored.entryID != got.entryID)) {
		f = append(f, "rateSource")
	}
	if stored.entryDate != "" && stored.entryDate != got.entryDate {
		f = append(f, "effectiveFrom")
	}
	return f
}

// classifyStandby prices one standby record again (§6.9) and classifies the result.
func (e *parityEngine) classifyStandby(s parityStandby) standbyVerdict {
	got := e.priceStandby(s.in, false, nil)
	v := standbyVerdict{got: got}
	fields := standbyDiff(s.stored, got)
	if len(fields) == 0 {
		v.outcome = ParityExact
		return v
	}
	set := func(outcome, kase, note string) standbyVerdict {
		v.outcome, v.kase, v.note = outcome, kase, note
		return v
	}
	switch {
	case s.voided && len(standbyDiff(s.stored, e.priceStandby(s.in, true, nil))) == 0:
		return set(ParityAllowed, CaseSinceVoided, "priced against the standby rate "+s.entryLegacy+", voided since")
	case s.stored.priced && s.stored.source == compute.RateSourceServiceFee &&
		len(standbyDiff(s.stored, e.priceStandby(s.in, false, &s.stored.estimate))) == 0:
		return set(ParityMismatch, MismatchFeeChanged, "priced with a standby fee of "+money(s.stored.estimate)+
			"; the party's fee is now another (the owner's D5 winner or a later edit)")
	case !s.stored.priced && got.priced:
		return set(ParityMismatch, MismatchNotComputed, "legacy left it unpriced; the rules price it "+money(got.estimate))
	case s.stored.priced && !got.priced:
		return set(ParityMismatch, MismatchGoUnpriced, "legacy priced it; Go answers "+got.reason)
	case slices.Contains(fields, "party"):
		return set(ParityMismatch, MismatchParty, "the record now resolves another billing party than the one stamped at pricing "+
			"(its own party or its task's links changed); differs in "+strings.Join(fields, ", "))
	case !slices.Contains(fields, "amount"):
		return set(ParityMismatch, MismatchStandbySource, "amount equal; differs in "+strings.Join(fields, ", "))
	}
	return set(ParityMismatch, MismatchAmount, "differs in "+strings.Join(fields, ", "))
}

// collapsesOnDash reports whether NormalizeDestinationCode truncates a destination at a dash inside a code (SPK-GW ->
// SPK, the collision of §6.18 #1), as opposed to the SOC prefixes, the "CODE - Name" display suffix or a plain code.
func collapsesOnDash(dest string) (key string, ok bool) {
	u := compute.UpperJS(compute.TrimJS(dest))
	key = compute.NormalizeDestinationCode(dest)
	if u == "" || key == u {
		return key, false
	}
	for _, soc := range [...]string{"SOCE", "SOCN", "SOCW"} {
		if strings.HasPrefix(u, soc) {
			return key, false
		}
	}
	i := strings.IndexByte(u, '-')
	return key, i > 0 && i+1 < len(u) && u[i-1] != ' ' && u[i+1] != ' '
}
