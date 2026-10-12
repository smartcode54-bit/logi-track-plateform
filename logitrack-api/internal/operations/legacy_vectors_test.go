package operations

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/vehicleclass"
)

// The legacy filter vectors of the driver monitor, one Go case per Vitest `it` (main spec §6.16: lib/placeFilter.test.ts
// (23), lib/truckPlate.test.ts (17), lib/vehicleClass.test.ts (9) -> the monitor/rows filters). The `it` name is the
// subtest name; legacy_vectors_integration_test.go holds the rest, so the two files port all 49.
//
// Where Go differs by design, the case says so:
//   - Places resolve at load, not in the browser (main spec §13.4): the ETL and the mirror store the hub nameToCode or
//     a code pass-through resolves (etl.docCtx.hub over hubs.source_id and hub_name_aliases), and the monitor reads the
//     stored hub. A case whose legacy input is raw text a hub name or code resolves is therefore stated with the hub
//     the load stores for it (`loaded` below); its raw -> code half is the ETL resolver's (main spec §6.16 notes it).
//   - The option and matching cases (placeKeysOf, buildPlaceFilterOptions, rowMatchesPlaceFilter, buildPlateFilterOptions,
//     rowMatchesPlateFilter, buildVehicleClassOptions, rowMatchesVehicleClass) run in SQL behind GET /v1/trips/monitor:
//     they are the integration file's.
//   - A vehicle class folds at write (vehicleclass.Of, the ETL's and the API's one mapper) and an unknown class is no
//     class (R15), where the legacy filter kept "FORKLIFT" as its own key.

// legacyMaps is the hub master of placeFilter.test.ts: SPK890146 (ประเวศ18 / Prawet 18) and SPK-GW (J&T EXPRESS บางปู /
// JNT Bangpu); the SOC keys carry no hub row, so their labels come from SOC_DESTINATIONS (socLabels).
func legacyMaps() (d directory, spk, gw uuid.UUID, loaded map[string]uuid.UUID) {
	spk, gw = uuid.New(), uuid.New()
	enSpk, enGw := "Prawet 18", "JNT Bangpu"
	d = directory{byID: map[uuid.UUID]Hub{}, byCode: map[string][]uuid.UUID{}}
	for _, h := range []Hub{{ID: spk, Code: "SPK890146", NameTh: "ประเวศ18", NameEn: &enSpk},
		{ID: gw, Code: "SPK-GW", NameTh: "J&T EXPRESS บางปู", NameEn: &enGw}} {
		d.byID[h.ID] = h
		d.byCode[h.Code] = append(d.byCode[h.Code], h.ID)
	}
	// What the load stores for the legacy spellings: the code itself, and the TH and EN names through nameToCode.
	loaded = map[string]uuid.UUID{"SPK890146": spk, "ประเวศ18": spk, "Prawet 18": spk, "SPK-GW": gw,
		"J&T EXPRESS บางปู": gw, "JNT Bangpu": gw}
	return d, spk, gw, loaded
}

// storedPlace is the place of a legacy value as the monitor reads it after the load: the raw text verbatim with the
// hub the load resolved it to (none when nothing did).
func storedPlace(d directory, loaded map[string]uuid.UUID, v *string) Place {
	if v == nil {
		return d.place(nil, nil, nil)
	}
	if id, ok := loaded[strings.TrimSpace(*v)]; ok {
		return d.place(v, &id, nil)
	}
	return d.place(v, nil, nil)
}

func TestLegacyPlaceFilterResolvePlace(t *testing.T) {
	d, _, _, loaded := legacyMaps()
	place := func(v *string) Place { return storedPlace(d, loaded, v) }
	t.Run("passes a known code straight through", func(t *testing.T) {
		if got := place(sp("SPK890146")); got.Key != "code:SPK890146" || !got.Resolved {
			t.Errorf("%+v", got)
		}
	})
	t.Run("resolves a TH display name to its code", func(t *testing.T) {
		if got := place(sp("ประเวศ18")); got.Key != "code:SPK890146" || !got.Resolved || got.Label != "ประเวศ18" {
			t.Errorf("%+v", got)
		}
	})
	t.Run("resolves an EN display name to the SAME code as the TH name (one option, not two)", func(t *testing.T) {
		if place(sp("Prawet 18")).Key != place(sp("ประเวศ18")).Key {
			t.Error("Prawet 18 and ประเวศ18 are two keys")
		}
		if place(sp("JNT Bangpu")).Key != place(sp("J&T EXPRESS บางปู")).Key {
			t.Error("JNT Bangpu and J&T EXPRESS บางปู are two keys")
		}
	})
	t.Run("normalises SOC spellings to the canonical key", func(t *testing.T) {
		for _, v := range []string{"SOCE (บัวโรย)", "SOCE", "soce"} {
			if got := place(sp(v)); got.Key != "code:SOCE" || !got.Resolved {
				t.Errorf("%q: %+v", v, got)
			}
		}
	})
	t.Run("keeps an OCR composite string unresolved under its own key", func(t *testing.T) {
		got := place(sp("ALANG-A - วังทองหลาง"))
		if got.Key != "raw:ALANG-A - วังทองหลาง" || got.Resolved || got.Label != "ALANG-A - วังทองหลาง" {
			t.Errorf("%+v", got)
		}
	})
	t.Run("never resolves in the reverse direction — a code does not become a display-name key", func(t *testing.T) {
		got := place(sp("SPK890146"))
		if got.Key != "code:SPK890146" || strings.Contains(got.Key, "ประเวศ18") {
			t.Errorf("%+v", got)
		}
		// The filter value names the code; it never matches by the display name.
		var ck checker
		if f := d.parsePlace(&ck, "destination", "code:SPK890146"); len(f.hubs) != 1 || f.raw != nil {
			t.Errorf("code filter %+v", f)
		}
	})
	t.Run("treats empty, blank and null as absent", func(t *testing.T) {
		for _, v := range []*string{sp(""), sp("   "), nil} { // JavaScript's null and undefined are one nil here
			if got := place(v); got.Key != PlaceNone || got.Resolved {
				t.Errorf("%v: %+v", v, got)
			}
		}
	})
	t.Run("trims surrounding whitespace before resolving", func(t *testing.T) {
		if got := place(sp("  ประเวศ18  ")); got.Key != "code:SPK890146" {
			t.Errorf("%+v", got)
		}
		// Go addition: an unresolved value is trimmed with JavaScript's trim set (tab, NBSP, the Unicode spaces, BOM),
		// the set the monitor queries trim with.
		if got := place(sp("\t ALANG-A - วังทองหลาง 　")); got.Key != "raw:ALANG-A - วังทองหลาง" {
			t.Errorf("%+v", got)
		}
	})
}

func TestLegacyPlaceFilterValueMatches(t *testing.T) {
	d, _, _, loaded := legacyMaps()
	t.Run("matches a single stop for the per-stop export filter (ADR 0006 §6)", func(t *testing.T) {
		// A stop's key is its place's key (MonitorStop.place, TripStopRow.place): the export filters stops by it.
		if storedPlace(d, loaded, sp("ประเวศ18")).Key != "code:SPK890146" {
			t.Error("ประเวศ18 does not match code:SPK890146")
		}
		if storedPlace(d, loaded, sp("SOCN")).Key == "code:SPK890146" {
			t.Error("SOCN matches code:SPK890146")
		}
	})
	t.Run("passes every stop through when no filter is applied", func(t *testing.T) {
		for _, v := range []string{"all", ""} {
			var ck checker
			if f := d.parsePlace(&ck, "destination", v); f.on || ck.err() != nil {
				t.Errorf("%q: %+v %v", v, f, ck.err())
			}
		}
	})
}

func TestLegacyTruckPlateResolveTripPlate(t *testing.T) {
	d, _, _, _ := legacyMaps()
	plateOf := func(trip, task *string) *string {
		return monitorRow(d, monitorCols{plate: trip, taskPlate: task}, nil).Truck.Plate
	}
	t.Run("prefers the trip's own snapshot over the task plate", func(t *testing.T) {
		if got := plateOf(sp("70-1234"), sp("71-5678")); deref2(got) != "70-1234" {
			t.Errorf("%v", deref2(got))
		}
	})
	t.Run("falls back to the task plate when the trip has no snapshot", func(t *testing.T) {
		for _, trip := range []*string{sp(""), nil} { // tripPlate "", null and absent: "" and nil here
			if got := plateOf(trip, sp("71-5678")); deref2(got) != "71-5678" {
				t.Errorf("%v: %v", trip, deref2(got))
			}
		}
	})
	t.Run("returns null rather than inventing a plate (ADR 0005 §6 — never uses activeTruck)", func(t *testing.T) {
		// monitorCols has no live-truck column at all: the trip's snapshot, else its task's, else nothing.
		for _, c := range [][2]*string{{nil, nil}, {sp(""), sp("")}, {sp("   "), nil}} {
			if got := plateOf(c[0], c[1]); got != nil {
				t.Errorf("%v %v: %q", c[0], c[1], *got)
			}
		}
	})
}

func TestLegacyTruckPlateFilterKey(t *testing.T) {
	d, _, _, _ := legacyMaps()
	truck := uuid.New()
	key := func(c monitorCols) string { return monitorRow(d, c, nil).Truck.Key }
	t.Run("keys on truckId when present", func(t *testing.T) {
		if got := key(monitorCols{truckID: &truck, plate: sp("70-1234")}); got != "id:"+truck.String() {
			t.Error(got)
		}
	})
	t.Run("falls back to the raw plate string when there is no truckId", func(t *testing.T) {
		if got := key(monitorCols{plate: sp("70-1234")}); got != "plate:70-1234" {
			t.Error(got)
		}
	})
	t.Run("returns the no-plate bucket when the row has neither", func(t *testing.T) {
		// A blank truck id cannot be stored (trip_records.truck_id is a uuid): the legacy {truckId: "  ", plate: "  "}
		// is a row with a blank plate and no truck.
		for _, c := range []monitorCols{{}, {plate: sp("  ")}} {
			if got := key(c); got != BucketNone {
				t.Errorf("%+v: %s", c, got)
			}
		}
	})
}

func TestLegacyVehicleClassKey(t *testing.T) {
	d, _, _, _ := legacyMaps()
	// The class the load or the API stores for a raw truck type (vehicleclass.Of), then the key the monitor row carries.
	key := func(raw *string) string {
		var stored *string
		if raw != nil {
			stored = vehicleclass.Of(*raw)
		}
		return monitorRow(d, monitorCols{vehicleClass: stored}, nil).Truck.VehicleClassKey
	}
	t.Run("collapses spelling variants of the same class to one key", func(t *testing.T) {
		for _, v := range []string{"6 Wheels", "6W", "6WH"} {
			if got := key(sp(v)); got != "6WH" {
				t.Errorf("%q: %s", v, got)
			}
		}
	})
	t.Run("buckets blank as NONE rather than inventing the billing 4WJ default", func(t *testing.T) {
		for _, v := range []*string{sp(""), sp("   "), nil} {
			if got := key(v); got != BucketNone {
				t.Errorf("%v: %s", v, got)
			}
		}
	})
	t.Run("keeps an unknown non-blank class as its own key, not an invented known class", func(t *testing.T) {
		// Divergence (R15): an unknown class is no class, stored NULL (the raw text stays in truck_type_raw), so the
		// row is in the none bucket; it is still never invented into a known class, the legacy rule's point.
		if got := key(sp("FORKLIFT")); got != BucketNone {
			t.Errorf("FORKLIFT: %s", got)
		}
	})
}
