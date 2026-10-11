//go:build integration

package operations_test

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/vehicleclass"
)

// The option and matching cases of the legacy monitor filters, one subtest per Vitest `it` (main spec §6.16:
// lib/placeFilter.test.ts, lib/truckPlate.test.ts, lib/vehicleClass.test.ts; the resolve and key cases are
// legacy_vectors_test.go). The legacy modules built the options and matched rows in the browser over the loaded
// trips; Go does both in SQL behind GET /v1/trips/monitor, so each case seeds its rows on a day of its own and reads
// the facets and the filters of that day as staff. A legacy row is seeded as the load stores it: the raw text
// verbatim with the hub nameToCode or a code pass-through resolves it to (etl.docCtx.hub; `loaded`), a multi-drop
// trip with its stops, a truck as its id, a vehicle class folded by vehicleclass.Of.
func TestLegacyMonitorFilterVectors(t *testing.T) {
	r := newReadWorld(t)
	spk := r.id(`INSERT INTO hubs (source_id, name_th, name_en, network) VALUES ('SPK890146', 'ประเวศ18', 'Prawet 18', 'SPK') RETURNING id`)
	gw := r.id(`INSERT INTO hubs (source_id, name_th, name_en, network) VALUES ('SPK-GW', 'J&T EXPRESS บางปู', 'JNT Bangpu', 'SPK') RETURNING id`)
	loaded := map[string]uuid.UUID{"SPK890146": spk, "ประเวศ18": spk, "Prawet 18": spk, "SPK-GW": gw,
		"J&T EXPRESS บางปู": gw, "JNT Bangpu": gw}
	truck2 := r.id(`INSERT INTO trucks (tenant_id, tenant_source, license_plate, province, status, brand, model, year, color, type_raw, vehicle_class)
		VALUES ($1, 'self', '70-1234', 'ชลบุรี', 'active', 'HINO', 'FC', 2021, 'white', '6 Wheels', '6WH') RETURNING id`, r.O)
	s := func(v string) *string { return &v }

	// Every case reads its own September day.
	nextDay := 0
	newDay := func() (string, time.Time) {
		nextDay++
		at := time.Date(2026, 9, nextDay, 3, 0, 0, 0, time.UTC) // 10:00 in Bangkok
		return at.Format(time.DateOnly), at
	}
	seq := 0
	tripOn := func(at time.Time, x tripRow) uuid.UUID {
		seq++
		x.tenant, x.no, x.createdAt = r.O, fmt.Sprintf("LV-%03d", seq), at.Add(time.Duration(seq)*time.Minute)
		return r.trip(x)
	}
	// destinations seeds one trip per legacy row of places: one value is the trip's destination, more are the stops
	// of a multi-drop trip (stop 1 the trip's destination), none is a trip without a destination.
	destinations := func(at time.Time, rowsOfValues ...[]*string) []uuid.UUID {
		var out []uuid.UUID
		for _, values := range rowsOfValues {
			x := tripRow{}
			hubOf := func(v *string) *uuid.UUID {
				if v == nil {
					return nil
				}
				if id, ok := loaded[strings.TrimSpace(*v)]; ok {
					return &id
				}
				return nil
			}
			if len(values) > 0 {
				x.destinationRaw, x.destinationHub = values[0], hubOf(values[0])
			}
			x.multi = len(values) > 1
			id := tripOn(at, x)
			if x.multi {
				for i, v := range values {
					r.exec(`INSERT INTO trip_delivery_stops (trip_id, stop_index, destination_raw, destination_hub_id) VALUES ($1, $2, $3, $4)`,
						id, i+1, *v, hubOf(v))
				}
			}
			out = append(out, id)
		}
		return out
	}
	facets := func(day string) map[string]any {
		t.Helper()
		_, _, meta := r.ok("staffO", "/v1/trips/monitor?from="+day+"&to="+day)
		return meta["facets"].(map[string]any)
	}
	options := func(f map[string]any, name string) []map[string]any { return rows(f[name]) }
	counts := func(list []map[string]any) map[string]float64 {
		out := map[string]float64{}
		for _, o := range list {
			out[o["value"].(string)] = o["count"].(float64)
		}
		return out
	}
	filter := func(day, query string) []string {
		t.Helper()
		data, _, _ := r.ok("staffO", "/v1/trips/monitor?from="+day+"&to="+day+"&facets=false&"+query)
		got := ids(rows(data))
		slices.Sort(got)
		return got
	}
	want := func(list ...uuid.UUID) []string {
		out := make([]string, len(list))
		for i, id := range list {
			out[i] = id.String()
		}
		slices.Sort(out)
		return out
	}
	q := url.QueryEscape
	one := func(v string) []*string { return []*string{s(v)} }

	// --- placeFilter.test.ts: placeKeysOf
	t.Run("placeKeysOf/dedupes two stops to the same place", func(t *testing.T) {
		day, at := newDay()
		destinations(at, []*string{s("ประเวศ18"), s("SPK890146")})
		if got := counts(options(facets(day), "destinations")); !mapsEqual(got, map[string]float64{"code:SPK890146": 1}) {
			t.Errorf("%v", got)
		}
	})
	t.Run("placeKeysOf/returns one key per distinct place", func(t *testing.T) {
		day, at := newDay()
		destinations(at, []*string{s("ประเวศ18"), s("SOCE")})
		if got := counts(options(facets(day), "destinations")); !mapsEqual(got, map[string]float64{"code:SPK890146": 1, "code:SOCE": 1}) {
			t.Errorf("%v", got)
		}
	})
	t.Run("placeKeysOf/treats an empty list as absent", func(t *testing.T) {
		day, at := newDay()
		destinations(at, []*string{})
		if got := counts(options(facets(day), "destinations")); !mapsEqual(got, map[string]float64{"__none__": 1}) {
			t.Errorf("%v", got)
		}
	})

	// --- placeFilter.test.ts: buildPlaceFilterOptions, over the six legacy rows
	rowsDay, rowsAt := newDay()
	sixRows := destinations(rowsAt, one("ประเวศ18"), one("SPK890146"), one("Prawet 18"), one("ALANG-A - วังทองหลาง"),
		one("SOCE (บัวโรย)"), []*string{nil})
	sixOptions := options(facets(rowsDay), "destinations")
	t.Run("buildPlaceFilterOptions/collapses every spelling of one place into a single option", func(t *testing.T) {
		n := 0
		for _, o := range sixOptions {
			if o["value"] == "code:SPK890146" {
				n++
				if o["count"] != 3.0 || o["label"] != "ประเวศ18" {
					t.Errorf("%v", o)
				}
			}
		}
		if n != 1 {
			t.Errorf("%d options for SPK890146", n)
		}
	})
	t.Run("buildPlaceFilterOptions/gives each unresolved string its own badged option", func(t *testing.T) {
		day, at := newDay()
		destinations(at, one("ALANG-A - วังทองหลาง"), one("BLANG-B - ห้วยขวาง"))
		got := options(facets(day), "destinations")
		if len(got) != 2 {
			t.Fatalf("%v", got)
		}
		for _, o := range got {
			if o["isUnresolved"] != true {
				t.Errorf("%v", o)
			}
		}
	})
	t.Run("buildPlaceFilterOptions/counts trips, not stops — a trip with two stops to one place counts once", func(t *testing.T) {
		day, at := newDay()
		destinations(at, []*string{s("ประเวศ18"), s("SPK890146")})
		got := options(facets(day), "destinations")
		if len(got) != 1 || got[0]["count"] != 1.0 {
			t.Errorf("%v", got)
		}
	})
	t.Run("buildPlaceFilterOptions/accounts for every trip exactly once across all options", func(t *testing.T) {
		total := 0.0
		for _, o := range sixOptions {
			total += o["count"].(float64)
		}
		if total != float64(len(sixRows)) {
			t.Errorf("%v trips over the options, want %d", total, len(sixRows))
		}
	})
	t.Run("buildPlaceFilterOptions/sorts resolved places first, then unresolved, with the no-value bucket last", func(t *testing.T) {
		if sixOptions[len(sixOptions)-1]["value"] != "__none__" {
			t.Errorf("last option %v", sixOptions[len(sixOptions)-1])
		}
		var ranked []int
		for _, o := range sixOptions[:len(sixOptions)-1] {
			if o["isUnresolved"] == true {
				ranked = append(ranked, 1)
			} else {
				ranked = append(ranked, 0)
			}
		}
		if !slices.IsSorted(ranked) || !slices.Contains(ranked, 0) || !slices.Contains(ranked, 1) {
			t.Errorf("resolved and unresolved interleave: %v", ranked)
		}
	})
	t.Run("buildPlaceFilterOptions/offers no option that matches zero trips", func(t *testing.T) {
		for _, o := range sixOptions {
			got := filter(rowsDay, "destination="+q(o["value"].(string)))
			if float64(len(got)) != o["count"] || len(got) == 0 {
				t.Errorf("%v: %d trips", o, len(got))
			}
		}
	})

	// --- placeFilter.test.ts: rowMatchesPlaceFilter
	t.Run("rowMatchesPlaceFilter/matches everything when no filter is applied", func(t *testing.T) {
		for _, query := range []string{"destination=all", "destination="} {
			if got := filter(rowsDay, query); !slices.Equal(got, want(sixRows...)) {
				t.Errorf("%s: %v", query, got)
			}
		}
	})
	t.Run("rowMatchesPlaceFilter/matches a multi-drop trip on ANY of its stops (ADR 0006 §5)", func(t *testing.T) {
		day, at := newDay()
		trip := destinations(at, []*string{s("SPK-GW"), s("SOCN"), s("ประเวศ18")})[0]
		for value, match := range map[string]bool{"code:SPK890146": true, "code:SOCN": true, "code:SOCW": false} {
			if got := filter(day, "destination="+q(value)); slices.Equal(got, want(trip)) != match {
				t.Errorf("%s: %v", value, got)
			}
		}
	})
	t.Run("rowMatchesPlaceFilter/matches the no-value bucket only for trips with no place", func(t *testing.T) {
		day, at := newDay()
		trips := destinations(at, []*string{nil}, one(""), one("SOCE"))
		if got := filter(day, "destination=__none__"); !slices.Equal(got, want(trips[0], trips[1])) {
			t.Errorf("%v", got)
		}
	})
	t.Run("rowMatchesPlaceFilter/matches an unresolved selection only on its exact raw string", func(t *testing.T) {
		day, at := newDay()
		trips := destinations(at, one("ALANG-A - วังทองหลาง"), one("ALANG-A - ลาดกระบัง"))
		if got := filter(day, "destination="+q("raw:ALANG-A - วังทองหลาง")); !slices.Equal(got, want(trips[0])) {
			t.Errorf("%v", got)
		}
	})

	// --- truckPlate.test.ts: buildPlateFilterOptions
	plates := func(at time.Time, rows ...tripRow) []uuid.UUID {
		var out []uuid.UUID
		for _, x := range rows {
			out = append(out, tripOn(at, x))
		}
		return out
	}
	idKey := func(id uuid.UUID) string { return "id:" + id.String() }
	t.Run("buildPlateFilterOptions/groups the same truckId under one option even when plate spellings differ", func(t *testing.T) {
		day, at := newDay()
		plates(at, tripRow{truck: &r.truckO, plate: s("70-1234")}, tripRow{truck: &r.truckO, plate: s("70-1234 กรุงเทพมหานคร")},
			tripRow{truck: &r.truckO, plate: s("70 - 1234")})
		got := options(facets(day), "plates")
		if len(got) != 1 || got[0]["value"] != idKey(r.truckO) || got[0]["count"] != 3.0 || got[0]["isOrphan"] == true {
			t.Errorf("%v", got)
		}
	})
	t.Run("buildPlateFilterOptions/does NOT merge two different trucks that share a plate string", func(t *testing.T) {
		day, at := newDay()
		plates(at, tripRow{truck: &r.truckO, plate: s("70-1234")}, tripRow{truck: &truck2, plate: s("70-1234")})
		got := counts(options(facets(day), "plates"))
		if !mapsEqual(got, map[string]float64{idKey(r.truckO): 1, idKey(truck2): 1}) {
			t.Errorf("%v", got)
		}
	})
	t.Run("buildPlateFilterOptions/gives an orphan plate its own reachable option", func(t *testing.T) {
		day, at := newDay()
		plates(at, tripRow{truck: &r.truckO, plate: s("70-1234")}, tripRow{plate: s("70-9999")}, tripRow{plate: s("70-9999")})
		var orphan map[string]any
		for _, o := range options(facets(day), "plates") {
			if o["value"] == "plate:70-9999" {
				orphan = o
			}
		}
		if orphan == nil || orphan["isOrphan"] != true || orphan["count"] != 2.0 {
			t.Errorf("%v", orphan)
		}
	})
	t.Run("buildPlateFilterOptions/collects plateless rows into the no-plate bucket, sorted last", func(t *testing.T) {
		day, at := newDay()
		plates(at, tripRow{}, tripRow{truck: &r.truckO, plate: s("70-1234")}, tripRow{})
		got := options(facets(day), "plates")
		if len(got) != 2 {
			t.Fatalf("%v", got)
		}
		if none := got[len(got)-1]; none["value"] != "__none__" || none["label"] != nil || none["count"] != 2.0 {
			t.Errorf("%v", none)
		}
	})
	t.Run("buildPlateFilterOptions/recovers a label from a later row when the first row of a truck lacks the plate", func(t *testing.T) {
		day, at := newDay()
		plates(at, tripRow{truck: &r.truckO}, tripRow{truck: &r.truckO, plate: s("70-1234")})
		got := options(facets(day), "plates")
		if len(got) != 1 || got[0]["label"] != "70-1234" {
			t.Errorf("%v", got)
		}
	})
	t.Run("buildPlateFilterOptions/returns an empty list for no rows", func(t *testing.T) {
		day, _ := newDay()
		if got := options(facets(day), "plates"); len(got) != 0 {
			t.Errorf("%v", got)
		}
	})

	// --- truckPlate.test.ts: rowMatchesPlateFilter
	plateDay, plateAt := newDay()
	plateRows := plates(plateAt, tripRow{truck: &r.truckO, plate: s("70-1234")}, tripRow{plate: s("70-9999")}, tripRow{})
	withID, orphan, plateless := plateRows[0], plateRows[1], plateRows[2]
	t.Run("rowMatchesPlateFilter/matches everything under PLATE_FILTER_ALL", func(t *testing.T) {
		if got := filter(plateDay, "plate=all"); !slices.Equal(got, want(plateRows...)) {
			t.Errorf("%v", got)
		}
	})
	t.Run("rowMatchesPlateFilter/matches by truck identity regardless of plate spelling", func(t *testing.T) {
		day, at := newDay()
		trips := plates(at, tripRow{truck: &r.truckO, plate: s("70 - 1234")}, tripRow{truck: &truck2, plate: s("70-1234")})
		if got := filter(day, "plate="+q(idKey(r.truckO))); !slices.Equal(got, want(trips[0])) {
			t.Errorf("%v", got)
		}
	})
	t.Run("rowMatchesPlateFilter/only ever matches a plate: selection against rows with no truckId", func(t *testing.T) {
		day, at := newDay()
		trips := plates(at, tripRow{plate: s("70-9999")}, tripRow{truck: &r.truckO, plate: s("70-9999")})
		if got := filter(day, "plate="+q("plate:70-9999")); !slices.Equal(got, want(trips[0])) {
			t.Errorf("%v", got)
		}
		if got := filter(plateDay, "plate="+q("plate:70-9999")); !slices.Equal(got, want(orphan)) {
			t.Errorf("%v", got)
		}
	})
	t.Run("rowMatchesPlateFilter/isolates the no-plate bucket", func(t *testing.T) {
		if got := filter(plateDay, "plate=__none__"); !slices.Equal(got, want(plateless)) || slices.Contains(got, withID.String()) {
			t.Errorf("%v", got)
		}
	})
	t.Run("rowMatchesPlateFilter/treats an empty selection as no filter", func(t *testing.T) {
		if got := filter(plateDay, "plate="); !slices.Equal(got, want(plateRows...)) {
			t.Errorf("%v", got)
		}
	})

	// --- vehicleClass.test.ts: buildVehicleClassOptions and rowMatchesVehicleClass
	classes := func(at time.Time, raws ...*string) []uuid.UUID {
		var out []uuid.UUID
		for _, raw := range raws {
			x := tripRow{}
			if raw != nil {
				x.class = vehicleclass.Of(*raw) // what the load and the API store
			}
			out = append(out, tripOn(at, x))
		}
		return out
	}
	t.Run("buildVehicleClassOptions/dedups by resolved key and counts rows", func(t *testing.T) {
		day, at := newDay()
		classes(at, s("6 Wheels"), s("6WH"), s("4WJ"), s("6W"))
		if got := counts(options(facets(day), "vehicleClasses")); !mapsEqual(got, map[string]float64{"6WH": 3, "4WJ": 1}) {
			t.Errorf("%v", got)
		}
	})
	t.Run("buildVehicleClassOptions/sorts the no-class bucket last", func(t *testing.T) {
		day, at := newDay()
		classes(at, s("6WH"), s(""), s("4WJ"))
		got := options(facets(day), "vehicleClasses")
		if len(got) != 3 || got[len(got)-1]["value"] != "__none__" {
			t.Errorf("%v", got)
		}
	})
	t.Run("buildVehicleClassOptions/gives the no-class bucket an empty label for the caller to i18n", func(t *testing.T) {
		day, at := newDay()
		classes(at, nil)
		got := options(facets(day), "vehicleClasses")
		if len(got) != 1 || got[0]["value"] != "__none__" || got[0]["label"] != "" || got[0]["count"] != 1.0 {
			t.Errorf("%v", got)
		}
	})
	classDay, classAt := newDay()
	classRows := classes(classAt, s("6 Wheels"), s("4WJ"), s(""))
	t.Run("rowMatchesVehicleClass/matches everything under ALL", func(t *testing.T) {
		if got := filter(classDay, "vehicleClass=all"); !slices.Equal(got, want(classRows...)) {
			t.Errorf("%v", got)
		}
	})
	t.Run("rowMatchesVehicleClass/matches a class regardless of spelling", func(t *testing.T) {
		// "6 Wheels" was folded to 6WH when it was stored; a lower-case filter value is folded to the enum too.
		for _, v := range []string{"6WH", "6wh"} {
			if got := filter(classDay, "vehicleClass="+v); !slices.Equal(got, want(classRows[0])) {
				t.Errorf("%s: %v", v, got)
			}
		}
	})
	t.Run("rowMatchesVehicleClass/matches the no-class bucket only for rows with no class", func(t *testing.T) {
		if got := filter(classDay, "vehicleClass=__none__"); !slices.Equal(got, want(classRows[2])) {
			t.Errorf("%v", got)
		}
	})
}
