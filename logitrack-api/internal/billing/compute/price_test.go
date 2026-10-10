package compute_test

import (
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

var (
	jan2026  = time.Date(2026, 1, 1, 0, 0, 0, 0, clock.Bangkok)
	aug2026  = time.Date(2026, 8, 1, 0, 0, 0, 0, clock.Bangkok)
	sep10    = time.Date(2026, 9, 10, 12, 0, 0, 0, clock.Bangkok)
	sep30    = time.Date(2026, 9, 30, 0, 0, 0, 0, clock.Bangkok)
	oct1Late = time.Date(2026, 10, 1, 1, 30, 0, 0, clock.Bangkok)
)

func card(id, hub, dest, class string, rate float64, eff time.Time, cat compute.JobCategory) compute.RateEntry {
	return compute.RateEntry{ID: id, PartyID: "cjsf", ImportID: "imp-" + id, HubCode: hub, DestinationCode: dest,
		VehicleClass: class, RateTHB: rate, EffectiveFrom: eff, JobCategory: cat}
}

func fuelRound(id string, eff time.Time, mult, add float64) compute.FuelAdjustment {
	return compute.FuelAdjustment{ID: id, PartyID: "cjsf", EffectiveFrom: eff, RateMultiplier: mult, AddTHBPerTrip: add}
}

func baseTrip() compute.TripInput {
	return compute.TripInput{
		Task: compute.TaskInput{
			TaskParties: compute.TaskParties{SourceLinkedPartyID: "cjsf"},
			SourceHub:   "SPK-GW", Destination: "SPK890103", TruckType: "4WJ",
		},
		DeliveredAt: sep10,
		CreatedAt:   sep10.Add(-6 * time.Hour),
	}
}

func mustPrice(t *testing.T, in compute.TripInput, tb compute.Tables) compute.TripResult {
	t.Helper()
	res, err := compute.PriceTrip(in, tb)
	if err != nil {
		t.Fatalf("PriceTrip: %v", err)
	}
	return res
}

func TestPriceTripJobCategory(t *testing.T) {
	primary := card("p", "SPK-GW", "SPK890103", "4WJ", 1200, aug2026, compute.Primary)
	supp := card("s", "SPK-GW", "SPK890103", "4WJ", 950, aug2026, compute.Supplementary)
	fuel := []compute.FuelAdjustment{fuelRound("f1", aug2026, 1, -40)}

	for _, tc := range []struct {
		name         string
		taskCategory string
		rates        []compute.RateEntry
		wantPrice    float64
		wantCategory compute.JobCategory
		wantReason   compute.UnpricedReason
	}{
		{"explicit SUPPLEMENTARY prices the เสริม card without fuel", "SUPPLEMENTARY", []compute.RateEntry{primary, supp}, 950, compute.Supplementary, ""},
		{"explicit PRIMARY never falls back to เสริม", "PRIMARY", []compute.RateEntry{supp}, 0, "", compute.NoRate},
		{"explicit PRIMARY prices with fuel", "PRIMARY", []compute.RateEntry{primary, supp}, 1160, compute.Primary, ""},
		{"legacy task tries PRIMARY first", "", []compute.RateEntry{primary, supp}, 1160, compute.Primary, ""},
		{"legacy task falls back to SUPPLEMENTARY", "", []compute.RateEntry{supp}, 950, compute.Supplementary, ""},
		{"a stray value is a legacy task", "supplementary", []compute.RateEntry{supp}, 950, compute.Supplementary, ""},
		{"no card in either category", "", nil, 0, "", compute.NoRate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := baseTrip()
			in.JobCategory = tc.taskCategory
			res := mustPrice(t, in, compute.Tables{Rates: tc.rates, Fuel: fuel})
			if res.Reason != tc.wantReason {
				t.Fatalf("reason %q, want %q", res.Reason, tc.wantReason)
			}
			if tc.wantReason != "" {
				if res.Price != nil || res.PartyID != "cjsf" {
					t.Fatalf("unpriced result %+v", res)
				}
				return
			}
			p := res.Price
			if p.EstimateTHB != tc.wantPrice || p.JobCategory != tc.wantCategory {
				t.Fatalf("price %v %s, want %v %s", p.EstimateTHB, p.JobCategory, tc.wantPrice, tc.wantCategory)
			}
			if p.ManualOverride != (tc.wantCategory == compute.Supplementary) {
				t.Fatalf("ManualOverride %v for %s", p.ManualOverride, p.JobCategory)
			}
			if tc.wantCategory == compute.Supplementary && (p.FuelAdjustmentID != "" || p.RateMultiplier != 1 || p.AddTHBPerTrip != 0) {
				t.Fatalf("SUPPLEMENTARY carries fuel: %+v", p)
			}
		})
	}
}

// R15: a NULL or blank truck_type is unpriced, single and multi-drop alike —
// never priced as 4WJ.
func TestPriceTripNoVehicleClass(t *testing.T) {
	rates := []compute.RateEntry{card("p", "SPK-GW", "SPK890103", "4WJ", 1200, aug2026, compute.Primary)}
	for _, truckType := range []string{"", "  ", "\u00a0"} {
		in := baseTrip()
		in.Task.TruckType = truckType
		res := mustPrice(t, in, compute.Tables{Rates: rates})
		if res.Price != nil || res.Reason != compute.NoVehicleClass || res.PartyID != "cjsf" {
			t.Fatalf("truckType %q: %+v, want no_vehicle_class", truckType, res)
		}
		if !res.BillingDate.Equal(sep10) {
			t.Fatalf("unpriced trip must still carry its billing date axis, got %v", res.BillingDate)
		}
		in.IsMultiDelivery, in.StopProgressCount, in.DeliveredStops = true, 2, []string{"SPK890103", "SPK2"}
		res = mustPrice(t, in, compute.Tables{Rates: rates, ExtraStopFeeTHB: ptr(300.0)})
		if res.Price != nil || res.Reason != compute.NoVehicleClass {
			t.Fatalf("multi-drop truckType %q: %+v, want no_vehicle_class", truckType, res)
		}
	}
}

// R19: no plan, delivery or creation instant is no_billing_date, never the
// wall clock.
func TestPriceTripNoBillingDate(t *testing.T) {
	in := baseTrip()
	in.DeliveredAt, in.CreatedAt = time.Time{}, time.Time{}
	rates := []compute.RateEntry{card("p", "SPK-GW", "SPK890103", "4WJ", 1200, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), compute.Primary)}
	res := mustPrice(t, in, compute.Tables{Rates: rates})
	if res.Price != nil || res.Reason != compute.NoBillingDate || !res.BillingDate.IsZero() {
		t.Fatalf("%+v, want no_billing_date and a NULL axis", res)
	}
	// A plan date does not help a delivered-basis party, but prices a plan-basis one.
	in.PlanAt = sep30
	if res := mustPrice(t, in, compute.Tables{Rates: rates}); res.Reason != compute.NoBillingDate {
		t.Fatalf("delivered basis used the plan date: %+v", res)
	}
	res = mustPrice(t, in, compute.Tables{Basis: compute.BasisPlan, Rates: rates})
	if res.Price == nil || !res.BillingDate.Equal(sep30) {
		t.Fatalf("plan basis: %+v", res)
	}
	// The epoch is the legacy "0": absent too.
	in = baseTrip()
	in.DeliveredAt, in.CreatedAt = time.UnixMilli(0), time.UnixMilli(0)
	if res := mustPrice(t, in, compute.Tables{Rates: rates}); res.Reason != compute.NoBillingDate {
		t.Fatalf("epoch instants priced: %+v", res)
	}
}

func TestPriceTripNoCustomer(t *testing.T) {
	in := baseTrip()
	in.Task.TaskParties = compute.TaskParties{BillingPartyID: " ", SourceLinkedPartyID: "\ufeff"}
	res := mustPrice(t, in, compute.Tables{})
	if res.Reason != compute.NoCustomer || res.PartyID != "" || !res.BillingDate.Equal(sep10) {
		t.Fatalf("%+v, want no_customer with the delivered axis", res)
	}
}

// ADR 0027: a plan-basis party prices and files the trip on its plan date.
func TestPriceTripPlanBasis(t *testing.T) {
	rates := []compute.RateEntry{
		card("sep", "SPK-GW", "SPK890103", "4WJ", 1000, time.Date(2026, 9, 1, 0, 0, 0, 0, clock.Bangkok), compute.Primary),
		card("oct", "SPK-GW", "SPK890103", "4WJ", 1100, time.Date(2026, 10, 1, 0, 0, 0, 0, clock.Bangkok), compute.Primary),
	}
	in := baseTrip()
	in.PlanAt, in.DeliveredAt = sep30, oct1Late
	plan := mustPrice(t, in, compute.Tables{Basis: compute.BasisPlan, Rates: rates})
	if plan.Price.EstimateTHB != 1000 || !plan.BillingDate.Equal(sep30) {
		t.Fatalf("plan basis: %v on %v, want 1000 on 30 Sep", plan.Price.EstimateTHB, plan.BillingDate)
	}
	delivered := mustPrice(t, in, compute.Tables{Basis: compute.BasisDelivered, Rates: rates})
	if delivered.Price.EstimateTHB != 1100 || !delivered.BillingDate.Equal(oct1Late) {
		t.Fatalf("delivered basis: %v on %v, want 1100 on 1 Oct", delivered.Price.EstimateTHB, delivered.BillingDate)
	}
}

// §6.4 / §6.8: names resolve to codes (never codes to names), and a single
// trip whose card is keyed by the Thai name retries once with it.
func TestPriceTripHubNames(t *testing.T) {
	hubs := compute.NewHubMaps([]compute.Hub{
		{Code: "SPK-GW", NameTH: "J&T EXPRESS บางปู", NameEN: "SPK-GW"},
		{Code: "SPK890174", NameTH: "ห้วยขวาง10"},
		{Code: "SPK890146", NameTH: "ประเวศ18", Aliases: []string{"Prawet 18"}},
	})
	t.Run("display names resolve to codes", func(t *testing.T) {
		in := baseTrip()
		in.Task.SourceHub, in.Task.Destination = " J&T EXPRESS บางปู ", "Prawet 18"
		rates := []compute.RateEntry{card("p", "SPK-GW", "SPK890146", "4WJ", 1300, jan2026, compute.Primary)}
		res := mustPrice(t, in, compute.Tables{Rates: rates, Hubs: hubs})
		if res.Price == nil || res.Price.LookupHubCode != "SPK-GW" || res.Price.LookupDestinationCode != "SPK890146" {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("a code is never translated back to a name", func(t *testing.T) {
		in := baseTrip()
		in.Task.Destination = "SPK890174"
		rates := []compute.RateEntry{card("p", "SPK-GW", "SPK890174", "4WJ", 1400, jan2026, compute.Primary)}
		res := mustPrice(t, in, compute.Tables{Rates: rates, Hubs: hubs})
		if res.Price == nil || res.Price.EstimateTHB != 1400 || res.Price.LookupDestinationCode != "SPK890174" {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("cards keyed by the display name price on the retry", func(t *testing.T) {
		in := baseTrip()
		in.Task.Destination = "SPK890174"
		rates := []compute.RateEntry{card("n", "SPK-GW", "ห้วยขวาง10", "4WJ", 1450, jan2026, compute.Primary)}
		res := mustPrice(t, in, compute.Tables{Rates: rates, Hubs: hubs})
		if res.Price == nil || res.Price.EstimateTHB != 1450 || res.Price.LookupDestinationCode != "ห้วยขวาง10" {
			t.Fatalf("%+v", res)
		}
		// Without hub maps (the web estimate) there is no retry.
		if res := mustPrice(t, in, compute.Tables{Rates: rates}); res.Reason != compute.NoRate {
			t.Fatalf("retried without hub maps: %+v", res)
		}
	})
	t.Run("the retry also runs for the SUPPLEMENTARY fallback", func(t *testing.T) {
		in := baseTrip()
		in.Task.Destination = "SPK890174"
		rates := []compute.RateEntry{card("n", "SPK-GW", "ห้วยขวาง10", "4WJ", 900, jan2026, compute.Supplementary)}
		res := mustPrice(t, in, compute.Tables{Rates: rates, Hubs: hubs})
		if res.Price == nil || res.Price.JobCategory != compute.Supplementary || res.Price.EstimateTHB != 900 {
			t.Fatalf("%+v", res)
		}
	})
}

func TestNewHubMaps(t *testing.T) {
	m := compute.NewHubMaps([]compute.Hub{
		{Code: " A1 ", NameTH: " ชื่อ ", NameEN: "A1", LegacyName: "Shared"},
		{Code: "B2", NameTH: "Shared", NameEN: " "},
		{Code: "A1", NameTH: "อื่น"},
		{Code: "", NameTH: "orphan"},
	})
	for raw, want := range map[string]string{
		"ชื่อ":     "A1",
		" ชื่อ\t":  "A1", // trimmed lookup
		"Shared":   "A1", // first hub to claim a name wins
		"A1":       "A1", // a code passes through
		"B2":       "B2",
		"orphan":   "orphan",
		"":         "",
		"unknown ": "unknown ", // a miss returns the raw value, untrimmed (§6.18 #26)
	} {
		if got := m.ResolveNameToCode(raw); got != want {
			t.Errorf("ResolveNameToCode(%q) = %q, want %q", raw, got, want)
		}
	}
	if name, ok := m.CodeToName("A1"); !ok || name != "ชื่อ" {
		t.Errorf("CodeToName(A1) = %q, %v", name, ok)
	}
	if _, ok := m.CodeToName("B2"); !ok {
		t.Errorf("CodeToName(B2) missing")
	}
	var zero compute.HubMaps
	if zero.ResolveNameToCode("x") != "x" {
		t.Errorf("zero HubMaps must resolve nothing")
	}
}

func TestPriceTripMultiDelivery(t *testing.T) {
	rates := []compute.RateEntry{
		card("a", "HUBA", "SPK1", "4WJ", 1500, jan2026, compute.Primary),
		card("b", "HUBA", "SPK2", "4WJ", 1700, jan2026, compute.Primary),
	}
	fuel := []compute.FuelAdjustment{fuelRound("f", jan2026, 1.05, -40)}
	in := compute.TripInput{
		Task: compute.TaskInput{
			TaskParties: compute.TaskParties{SourceLinkedPartyID: "cjsf"},
			SourceHub:   "HUBA - Hub A", Destination: "SPK2", TruckType: "4WJ",
		},
		DeliveredAt:       sep10,
		IsMultiDelivery:   true,
		StopProgressCount: 3,
		DeliveredStops:    []string{"SPK1", "SPK2", "SPK3"},
	}
	res := mustPrice(t, in, compute.Tables{Rates: rates, Fuel: fuel, ExtraStopFeeTHB: ptr(300.0)})
	p := res.Price
	if p == nil || !p.IsMultiDelivery {
		t.Fatalf("%+v", res)
	}
	base := compute.FinalRateTHB(1700, 1.05, -40)
	if p.BaseRateTHB != base || p.StopChargeTHB != 600 || p.EstimateTHB != base+600 || p.LookupDestinationCode != "SPK2" {
		t.Fatalf("base %v charge %v total %v dest %s", p.BaseRateTHB, p.StopChargeTHB, p.EstimateTHB, p.LookupDestinationCode)
	}
	if len(p.Stops) != 3 || p.Stops[1].StopIndex != 2 || p.Stops[1].DestinationCode != "SPK1" || p.Stops[2].DestinationCode != "SPK3" {
		t.Fatalf("stops %+v", p.Stops)
	}
	if p.RateEntryID != "b" || p.FuelAdjustmentID != "f" || p.RoundEffectiveFromDate != "2026-01-01" {
		t.Fatalf("provenance %+v", p)
	}

	t.Run("fewer than two delivered stops is an error, not an unpriced reason", func(t *testing.T) {
		in := in
		in.DeliveredStops = []string{"SPK1"}
		if _, err := compute.PriceTrip(in, compute.Tables{Rates: rates}); !errors.Is(err, compute.ErrMultiInsufficientStops) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("one progress entry prices as a single trip", func(t *testing.T) {
		in := in
		in.StopProgressCount = 1
		res := mustPrice(t, in, compute.Tables{Rates: rates})
		if res.Price == nil || res.Price.IsMultiDelivery || res.Price.EstimateTHB != 1700 {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("no customer comes before the stop gate", func(t *testing.T) {
		in := in
		in.Task.TaskParties = compute.TaskParties{}
		in.DeliveredStops = nil
		res, err := compute.PriceTrip(in, compute.Tables{Rates: rates})
		if err != nil || res.Reason != compute.NoCustomer {
			t.Fatalf("%+v %v", res, err)
		}
	})
}

// R16: the selectors' result never depends on the order rows were loaded in.
func TestSelectionIgnoresLoadOrder(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	at := time.Date(2026, 8, 15, 17, 0, 0, 0, time.UTC)
	created := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }
	entries := []compute.RateEntry{
		{ID: "0199-c", CreatedAt: created(3), RateTHB: 1},
		{ID: "0199-a", CreatedAt: created(2), RateTHB: 2},
		{ID: "0199-b", CreatedAt: created(2), RateTHB: 3},
		{ID: "Leg-b", LegacyDocID: "b", CreatedAt: created(9), RateTHB: 4},
		{ID: "Leg-B", LegacyDocID: "B", CreatedAt: created(9), RateTHB: 5},
		{ID: "Leg-a", LegacyDocID: "a", CreatedAt: created(1), RateTHB: 6},
	}
	for i := range entries {
		entries[i].PartyID, entries[i].HubCode, entries[i].DestinationCode, entries[i].VehicleClass = "c", "H", "D", "4WJ"
		entries[i].EffectiveFrom = at
	}
	for _, bill := range []time.Time{at.Add(48 * time.Hour), at.Add(-48 * time.Hour)} { // newest, and the oldest fallback
		for i := 0; i < 200; i++ {
			rng.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
			got, ok := compute.SelectRateEntry("c", "H", "D", "4WJ", bill, entries, compute.Primary)
			if !ok || got.ID != "Leg-B" { // legacy first, by bytes: "B" < "a" < "b"
				t.Fatalf("bill %v: picked %q, want Leg-B", bill, got.ID)
			}
		}
	}
	// Without migrated rows the earliest created_at wins, then the lower id.
	var news []compute.RateEntry
	for _, e := range entries {
		if e.LegacyDocID == "" {
			news = append(news, e)
		}
	}
	for i := 0; i < 200; i++ {
		rng.Shuffle(len(news), func(i, j int) { news[i], news[j] = news[j], news[i] })
		got, ok := compute.SelectRateEntry("c", "H", "D", "4WJ", at, news, compute.Primary)
		if !ok || got.ID != "0199-a" {
			t.Fatalf("picked %q, want 0199-a", got.ID)
		}
	}
}

func TestSelectRateEntryFilters(t *testing.T) {
	e := card("x", "H", "D", "6 Wheels", 1, jan2026, "")
	for _, tc := range []struct {
		name  string
		class string
		cat   compute.JobCategory
		mut   func(*compute.RateEntry)
		want  bool
	}{
		{"folded classes match on both sides", "6W", compute.Primary, nil, true},
		{"an absent category is PRIMARY", "6WH", "", nil, true},
		{"SUPPLEMENTARY lookups skip PRIMARY rows", "6WH", compute.Supplementary, nil, false},
		{"a blank trip class matches nothing (R15)", "", compute.Primary, nil, false},
		{"a blank card class matches nothing (R15)", "4WJ", compute.Primary, func(e *compute.RateEntry) { e.VehicleClass = " " }, false},
		{"voided rows never price", "6WH", compute.Primary, func(e *compute.RateEntry) { e.Voided = true }, false},
		{"party ids compare exactly", "6WH", compute.Primary, func(e *compute.RateEntry) { e.PartyID = " cjsf" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := e
			if tc.mut != nil {
				tc.mut(&x)
			}
			_, ok := compute.SelectRateEntry("cjsf", "H", "D", tc.class, sep10, []compute.RateEntry{x}, tc.cat)
			if ok != tc.want {
				t.Fatalf("selected %v, want %v", ok, tc.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
