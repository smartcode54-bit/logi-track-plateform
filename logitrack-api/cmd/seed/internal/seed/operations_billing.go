package seed

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// Prices of the generated operations rows (T35; Appendix D §D.1.4: prices are never typed). Once every generator has
// run, priceOperations reads the plan's rate tables, fuel rounds, service fees, standby rates and hub names exactly as
// CheckEngine reads them from the database (loadEngineInputs) and runs the billing engine (internal/billing/compute)
// over every generated delivered trip and standby record; the load's engine check then recomputes them from the
// database and rolls the load back on any difference, so the two readers cannot drift apart unnoticed. A later
// generator that adds rate rows (T43) runs before this pass, and the prices follow.

// opsResult are the rows the operations generator leaves for the pricing pass.
type opsResult struct {
	trips   []*opsTrip
	standby []*opsStandby
}

// priceOperations writes the billing snapshot of every generated delivered trip (priced, or with the unpriced reason
// the engine returns, R62), its multi-drop breakdown, the trip's billing party and billing date, and the inline price
// of every generated standby record.
func priceOperations(p *Plan) error {
	if p.ops == nil {
		return nil
	}
	in, err := planEngineInputs(p)
	if err != nil {
		return fmt.Errorf("seed: pricing inputs: %w", err)
	}
	own := p.Symbols.MustID(ownFleetSymbol).String()
	for _, ot := range p.ops.trips {
		if ot.trip.Str("status") != "delivered" {
			continue
		}
		if err := priceTrip(p, in, ot, own); err != nil {
			return fmt.Errorf("seed: price %s: %w", ot.trip.Str("trip_no"), err)
		}
	}
	// The billing carrier of a priced standby record (0015, R61): the tenant of the standby rate it names, else the own
	// fleet, the only billing carrier today (the rule of the 0015 backfill and of operations.BillingVisible).
	carrier := map[string]string{}
	for _, r := range p.Tables["standby_rate_entries"] {
		carrier[r.Str("id")] = r.Str("tenant_id")
	}
	for _, sb := range p.ops.standby {
		if err := priceStandby(in, sb, carrier, own); err != nil {
			return fmt.Errorf("seed: price standby %s: %w", sb.row.Str("id"), err)
		}
	}
	return nil
}

// tripInput is the engine input of a generated trip, built as loadSnapshots builds it from the database.
func tripInput(ot *opsTrip) (compute.TripInput, error) {
	k, t := ot.task, ot.trip
	var in compute.TripInput
	var err error
	in.Task = compute.TaskInput{TaskParties: compute.TaskParties{BillingPartyID: k.Str("billing_party_id"),
		SourceLinkedPartyID: k.Str("source_linked_party_id"), DestinationLinkedPartyID: k.Str("destination_linked_party_id")},
		SourceHub: k.Str("source_hub_raw"), Destination: k.Str("destination_raw"), TruckType: k.Str("truck_type")}
	in.JobCategory = k.Str("job_category")
	if in.PlanAt, err = time.Parse(time.RFC3339, k.Str("plan_at")); err != nil {
		return in, err
	}
	if in.DeliveredAt, err = time.Parse(time.RFC3339, t.Str("delivered_at")); err != nil {
		return in, err
	}
	if in.CreatedAt, err = time.Parse(time.RFC3339, t.Str("created_at")); err != nil {
		return in, err
	}
	in.IsMultiDelivery = t["is_multi_delivery"] == true
	stops := append([]Row(nil), ot.stops...)
	sort.SliceStable(stops, func(i, j int) bool { // completed_seq NULLS LAST, stop_index
		ci, iok := intOf(stops[i]["completed_seq"])
		cj, jok := intOf(stops[j]["completed_seq"])
		if iok != jok {
			return iok
		}
		if iok && ci != cj {
			return ci < cj
		}
		si, _ := intOf(stops[i]["stop_index"])
		sj, _ := intOf(stops[j]["stop_index"])
		return si < sj
	})
	for _, s := range stops {
		in.StopProgressCount++
		if s.Str("status") == "delivered" && s.Str("destination_raw") != "" {
			in.DeliveredStops = append(in.DeliveredStops, s.Str("destination_raw"))
		}
	}
	return in, nil
}

func priceTrip(p *Plan, in *engineInputs, ot *opsTrip, own string) error {
	tin, err := tripInput(ot)
	if err != nil {
		return err
	}
	party, _ := compute.ResolveTaskParty(tin.Task.TaskParties)
	res, err := compute.PriceTrip(tin, in.tables(party))
	if err != nil {
		return err
	}
	t := ot.trip
	if res.PartyID != "" {
		t["billing_party_id"] = res.PartyID
	}
	if !res.BillingDate.IsZero() {
		t["billing_date"] = ts(res.BillingDate.In(clock.Bangkok))
	}
	at := tin.DeliveredAt.Add(time.Minute)
	by := "mobile_delivery"
	switch {
	case ot.legacy:
		by = "etl" // stands for the legacy server snapshot the ETL loads: inserted as written, never recomputed
	case t.Str("delivered_via") == "admin_web":
		by = "scheduler" // the office closed the trip; the safety-net job priced it
	}
	version := json.Number("1")
	if ot.legacy {
		version = json.Number("0")
	}
	s := Row{"trip_id": t.Str("id"), "tenant_id": own, "tenant_source": "form", "rate_multiplier": "1.000000",
		"add_thb_per_trip": "0.00", "is_multi_delivery": false, "manual_override": false, "computed_by": by,
		"compute_version": version, "computed_at": ts(at.In(clock.Bangkok)), "updated_at": ts(at.In(clock.Bangkok))}
	if cat, ok := compute.ExplicitJobCategory(tin.JobCategory); ok {
		s["job_category_at_pricing"] = string(cat)
	}
	if res.Price == nil {
		s["unpriced_reason"] = string(res.Reason)
		if res.Reason == compute.NoRate && !tin.IsMultiDelivery { // the codes the lookup found no card for
			s["lookup_hub_code"] = compute.ExtractHubID(in.hubs.ResolveNameToCode(tin.Task.SourceHub))
			s["lookup_destination_code"] = compute.NormalizeDestinationCode(in.hubs.ResolveNameToCode(tin.Task.Destination))
		}
		p.Tables["trip_billing_snapshots"] = append(p.Tables["trip_billing_snapshots"], s)
		return nil
	}
	pr := res.Price
	s["estimate_thb"], s["base_rate_thb"] = money(pr.EstimateTHB), money(pr.BaseRateTHB)
	s["rate_entry_id"], s["rate_import_id"] = pr.RateEntryID, pr.RateImportID
	s["lookup_hub_code"], s["lookup_destination_code"] = pr.LookupHubCode, pr.LookupDestinationCode
	s["rate_multiplier"] = strconv.FormatFloat(pr.RateMultiplier, 'f', 6, 64)
	s["add_thb_per_trip"] = money(pr.AddTHBPerTrip)
	if pr.FuelAdjustmentID != "" {
		s["fuel_adjustment_id"], s["fuel_effective_from_date"] = pr.FuelAdjustmentID, pr.FuelEffectiveFromDate
	}
	s["round_effective_from_date"] = pr.RoundEffectiveFromDate
	if pr.FuelBandLowerTHB != nil {
		s["fuel_band_lower_thb"], s["fuel_band_upper_thb"] = money(*pr.FuelBandLowerTHB), money(*pr.FuelBandUpperTHB)
	}
	if pr.ReferenceFuelPriceTHB != nil {
		s["reference_fuel_price_thb"] = money(*pr.ReferenceFuelPriceTHB)
	}
	s["is_multi_delivery"], s["manual_override"] = pr.IsMultiDelivery, pr.ManualOverride
	s["job_category_at_pricing"] = string(pr.JobCategory)
	if pr.IsMultiDelivery {
		s["stop_charge_thb"] = money(pr.StopChargeTHB)
	}
	if tin.JobCategory == "" { // a legacy task: the trip holds the category billing resolved (invariant 3)
		t["job_category"] = string(pr.JobCategory)
	}
	p.Tables["trip_billing_snapshots"] = append(p.Tables["trip_billing_snapshots"], s)
	for _, st := range pr.Stops {
		p.Tables["trip_billing_stop_breakdown"] = append(p.Tables["trip_billing_stop_breakdown"], Row{"trip_id": t.Str("id"),
			"stop_index": intNum(st.StopIndex), "destination_code": st.DestinationCode, "base_rate_thb": money(st.BaseRateTHB),
			"final_rate_thb": money(st.FinalRateTHB)})
	}
	return nil
}

func priceStandby(in *engineInputs, sb *opsStandby, carrier map[string]string, own string) error {
	r := sb.row
	var sin compute.StandbyInput
	var err error
	sin.CustomerPartyID = r.Str("customer_party_id")
	if sb.task != nil {
		sin.TaskSourceLinkedPartyID = sb.task.Str("source_linked_party_id")
		sin.TaskDestinationLinkedPartyID = sb.task.Str("destination_linked_party_id")
	}
	if sin.CreatedAt, err = time.Parse(time.RFC3339, r.Str("created_at")); err != nil {
		return err
	}
	if v := r.Str("started_at"); v != "" {
		if sin.StartedAt, err = time.Parse(time.RFC3339, v); err != nil {
			return err
		}
	}
	at := sin.CreatedAt
	if v := r.Str("ended_at"); v != "" {
		if sin.EndedAt, err = time.Parse(time.RFC3339, v); err != nil {
			return err
		}
		at = sin.EndedAt
	}
	pid, _ := compute.StandbyParty(sin)
	var rates []compute.StandbyRate
	var fee *float64
	if pt, ok := in.parties[pid]; ok {
		rates = pt.standbys
		if f, ok := pt.fees["standby"]; ok {
			fee = &f
		}
	}
	res := compute.PriceStandby(sin, rates, fee)
	r["billing_computed_at"] = ts(at.Add(time.Minute).In(clock.Bangkok))
	if res.Price == nil {
		r["billing_unpriced_reason"] = string(res.Reason)
		return nil
	}
	r["billing_estimate_thb"], r["billing_party_id"] = money(res.Price.EstimateTHB), res.Price.PartyID
	r["billing_rate_source"], r["billing_tenant_id"] = res.Price.RateSource, own
	if res.Price.RateEntryID != "" {
		r["billing_rate_entry_id"] = res.Price.RateEntryID
		if t := carrier[res.Price.RateEntryID]; t != "" {
			r["billing_tenant_id"] = t
		}
	}
	if res.Price.EffectiveFromDate != "" {
		r["billing_effective_from_date"] = res.Price.EffectiveFromDate
	}
	return nil
}

// planEngineInputs reads the pricing inputs from the plan's rows as loadEngineInputs reads them from the database:
// every party's basis, rate entries, fuel rounds and standby rates ordered by (party, created_at, id), the first
// service fee of each type, and the hub names in (created_at, id) order with their aliases.
func planEngineInputs(p *Plan) (*engineInputs, error) {
	in := &engineInputs{parties: map[string]*partyTables{}}
	party := func(id string) *partyTables {
		pt, ok := in.parties[id]
		if !ok {
			pt = &partyTables{fees: map[string]float64{}}
			in.parties[id] = pt
		}
		return pt
	}
	for _, r := range p.Tables["billing_parties"] {
		party(r.Str("id")).basis = compute.BillingDateBasis(r.Str("billing_date_basis"))
	}
	var errs []error
	at := func(r Row, col string) time.Time {
		t, err := time.Parse(time.RFC3339, r.Str(col))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", col, r.Str("id"), err))
		}
		return t
	}
	f := func(r Row, col string) float64 {
		v, ok := numberOf(r[col])
		if !ok {
			errs = append(errs, fmt.Errorf("%s %s: not a number", col, r.Str("id")))
		}
		return v
	}
	for _, r := range sortedRows(p.Tables["customer_rate_entries"]) {
		e := compute.RateEntry{ID: r.Str("id"), LegacyDocID: r.Str("legacy_doc_id"), CreatedAt: at(r, "created_at"),
			PartyID: r.Str("billing_party_id"), ImportID: r.Str("import_id"), HubCode: r.Str("hub_code"),
			DestinationCode: r.Str("destination_code"), VehicleClass: r.Str("vehicle_class"), RateTHB: f(r, "rate_thb"),
			EffectiveFrom: at(r, "effective_from_at"), JobCategory: compute.JobCategory(r.Str("job_category")), Voided: r["voided"] == true}
		pt := party(e.PartyID)
		pt.rates = append(pt.rates, e)
	}
	for _, r := range sortedRows(p.Tables["customer_fuel_rate_adjustments"]) {
		a := compute.FuelAdjustment{ID: r.Str("id"), LegacyDocID: r.Str("legacy_doc_id"), CreatedAt: at(r, "created_at"),
			PartyID: r.Str("billing_party_id"), EffectiveFrom: at(r, "effective_from_at"), RateMultiplier: f(r, "rate_multiplier"),
			AddTHBPerTrip: f(r, "add_thb_per_trip"), Voided: r["voided"] == true}
		if r["reference_fuel_price_thb"] != nil {
			v := f(r, "reference_fuel_price_thb")
			a.ReferenceFuelPriceTHB = &v
		}
		pt := party(a.PartyID)
		pt.fuel = append(pt.fuel, a)
	}
	for _, r := range sortedRows(p.Tables["customer_service_fees"]) {
		pt := party(r.Str("billing_party_id"))
		if _, dup := pt.fees[r.Str("fee_type")]; !dup {
			pt.fees[r.Str("fee_type")] = f(r, "amount_thb")
		}
	}
	for _, r := range sortedRows(p.Tables["standby_rate_entries"]) {
		s := compute.StandbyRate{ID: r.Str("id"), LegacyDocID: r.Str("legacy_doc_id"), CreatedAt: at(r, "created_at"),
			PartyID: r.Str("billing_party_id"), RateTHB: f(r, "rate_thb"), EffectiveFrom: at(r, "effective_from_at"),
			Voided: r["voided_at"] != nil}
		pt := party(s.PartyID)
		pt.standbys = append(pt.standbys, s)
	}
	aliases := map[string][]Row{}
	for _, r := range p.Tables["hub_name_aliases"] {
		aliases[r.Str("hub_id")] = append(aliases[r.Str("hub_id")], r)
	}
	hubRows := append([]Row(nil), p.Tables["hubs"]...)
	sort.SliceStable(hubRows, func(i, j int) bool { return lessCreated(hubRows[i], hubRows[j]) })
	list := make([]compute.Hub, 0, len(hubRows))
	for _, h := range hubRows {
		as := aliases[h.Str("id")]
		sort.SliceStable(as, func(i, j int) bool {
			ti, tj := at(as[i], "created_at"), at(as[j], "created_at")
			if !ti.Equal(tj) {
				return ti.Before(tj)
			}
			return as[i].Str("alias") < as[j].Str("alias")
		})
		hub := compute.Hub{Code: h.Str("source_id"), NameTH: h.Str("name_th"), NameEN: h.Str("name_en")}
		for _, a := range as {
			hub.Aliases = append(hub.Aliases, a.Str("alias"))
		}
		list = append(list, hub)
	}
	in.hubs = compute.NewHubMaps(list)
	return in, errors.Join(errs...)
}

// sortedRows orders rows as the engine queries do: (billing_party_id, created_at, id).
func sortedRows(rows []Row) []Row {
	out := append([]Row(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := out[i].Str("billing_party_id"), out[j].Str("billing_party_id"); a != b {
			return a < b
		}
		return lessCreated(out[i], out[j])
	})
	return out
}

// lessCreated orders by (created_at, id); an unparsable instant sorts first (planEngineInputs reports it).
func lessCreated(a, b Row) bool {
	ta, _ := time.Parse(time.RFC3339, a.Str("created_at"))
	tb, _ := time.Parse(time.RFC3339, b.Str("created_at"))
	if !ta.Equal(tb) {
		return ta.Before(tb)
	}
	return a.Str("id") < b.Str("id")
}

// numberOf reads a numeric column of a plan row: money is a JSON string (R20), other numbers json.Number.
func numberOf(v any) (float64, bool) {
	if s, ok := v.(string); ok {
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	return floatOf(v)
}

// intOf reads an integer column of a plan row.
func intOf(v any) (int, bool) {
	switch x := v.(type) {
	case json.Number:
		n, err := strconv.Atoi(x.String())
		return n, err == nil
	case int:
		return x, true
	}
	return 0, false
}
