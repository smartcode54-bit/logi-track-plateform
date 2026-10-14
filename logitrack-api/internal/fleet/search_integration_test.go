//go:build integration

package fleet_test

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"
)

// UI2B (issue #142): q of GET /v1/trucks is folded as the web's lib/searchText.ts folds (search_fold, 0026): Thai
// tone marks, Latin accents and Thai digits fold away, and every word of q must be somewhere in the plate, province,
// brand or model, in any order; the whole of q still finds an exact GPS id or VIN. A dispatcher's scope list (plate,
// province) folds the same way.
func TestTrucksFoldedSearch(t *testing.T) {
	w := newWorld(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	thai := w.truck(w.O, "๗๐-๑๒๓๔", base, "province", "'เชียงใหม่'", "brand", "'Škoda'", "model", "'Ünimog'")
	tone := w.truck(w.O, "กข-5678", base.Add(time.Minute), "province", "'ร้อยเอ็ด'", "gps_vehicle_id", "'GPS-77'")
	w.truck(w.O, "ขค-9999", base.Add(2*time.Minute))
	ta := w.login("ta.o")
	ids := func(bearer, q string) []string {
		t.Helper()
		r := w.get("/v1/trucks?q="+url.QueryEscape(q), bearer)
		expect(t, r, http.StatusOK, "")
		var out []string
		for _, it := range r.list() {
			out = append(out, str(it["id"]))
		}
		slices.Sort(out)
		return out
	}
	for q, want := range map[string][]string{
		"70-1234":       {thai}, // Thai digits in the plate
		"๗๐-๑๒":         {thai}, // Thai digits in q
		"skoda unimog":  {thai}, // Latin accents, two words
		"UNIMOG เชียง":  {thai}, // any order, Thai and Latin
		"รอยเอด":        {tone}, // tone marks and maitaikhu fold away
		"gps-77":        {tone}, // the whole of q equals the GPS id
		"skoda nowhere": nil,    // every word must match
	} {
		if got := ids(ta, q); !slices.Equal(got, want) {
			t.Errorf("q=%q: %v, want %v", q, got, want)
		}
	}

	// A dispatcher reads scope_trucks (plate, province), folded the same way.
	in := w.truck(w.A, "ฮห-๔๔๔๔", base, "province", "'น่าน'")
	w.exec(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw,
		billing_party_id, truck_id) VALUES ($1, 'form', 'first_mile', 'pending', now(), 'SRC', 'DST', $2, $3)`, w.A, w.partyX, in)
	ds := w.login("ds")
	for q, want := range map[string][]string{"4444 นาน": {in}, "ฮห nowhere": nil} {
		if got := ids(ds, q); !slices.Equal(got, want) {
			t.Errorf("dispatcher q=%q: %v, want %v", q, got, want)
		}
	}
}
