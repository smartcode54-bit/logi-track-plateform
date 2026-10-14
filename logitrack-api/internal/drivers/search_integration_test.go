//go:build integration

package drivers_test

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
)

// UI2B (issue #142): q of GET /v1/drivers is folded as the web's lib/searchText.ts folds (search_fold, 0026): Thai
// tone marks and thanthakhat, Latin accents and Thai digits fold away, and every word of q must be somewhere in the
// row (names, licence number, mobile, e-mail), in any order. The counts follow the search; a dispatcher's scope list
// folds the same way.
func TestDriversFoldedSearch(t *testing.T) {
	w := newWorld(t)
	cafe := w.driver(w.O, "Café", "last_name", "'Dépôt'", "full_name_th", "'ห้วยขวาง สมศักดิ์'",
		"truck_license_id", "'ท ๗๐๑๒๓๔'", "status", "'inactive'")
	plain := w.driver(w.O, "Plain", "full_name_th", "'หวยขวาง ทดสอบ'", "mobile", "'0899999999'")
	other := w.driver(w.O, "Other", "full_name_th", "'บางนา'", "email", "'x_y@example.test'")
	ta := w.login("ta.o")
	ids := func(q string) []string {
		t.Helper()
		r := w.get("/v1/drivers?q="+url.QueryEscape(q), ta)
		expect(t, r, http.StatusOK, "")
		var out []string
		for _, it := range r.list() {
			out = append(out, str(it["id"]))
		}
		slices.Sort(out)
		return out
	}
	both := []string{cafe, plain}
	slices.Sort(both)
	for q, want := range map[string][]string{
		"หวยขวาง":      both,    // no tone mark finds the row with one, and the row without
		"ห้วยขวาง":     both,    // the tone mark in q folds away too
		"สมศักดิ":      {cafe},  // thanthakhat (การันต์) folds away
		"cafe depot":   {cafe},  // Latin accents, two words
		"DEPOT café":   {cafe},  // any order, any case
		"701234":       {cafe},  // Thai digits in the row
		"๐๘๙๙":         {plain}, // Thai digits in q (the mobile 0899...)
		"ขวาง plain":   {plain}, // a Thai word and a Latin word of different columns
		"cafe nowhere": nil,     // every word must match
		"_":            {other}, // a LIKE wildcard is a plain character (the e-mail x_y@)
		"%":            nil,
	} {
		if got := ids(q); !slices.Equal(got, want) {
			t.Errorf("q=%q: %v, want %v", q, got, want)
		}
	}
	// The counts follow the search (meta.statusCounts over every matching row).
	r := w.get("/v1/drivers?q="+url.QueryEscape("หวยขวาง"), ta)
	if c := r.meta()["statusCounts"].(map[string]any); c["inactive"] != 1.0 || c["active"] != 1.0 {
		t.Errorf("statusCounts %v", c)
	}
	// fields=minimal (the pickers) searches the same way.
	r = w.get("/v1/drivers?fields=minimal&q="+url.QueryEscape("ทดสอบ หวย"), ta)
	if len(r.list()) != 1 || r.list()[0]["id"] != plain {
		t.Errorf("minimal: %s", r.raw)
	}
	// A q of tone marks only folds to nothing: no search.
	if r := w.get("/v1/drivers?q="+url.QueryEscape("่"), ta); len(r.list()) != 3 {
		t.Errorf("q of a tone mark only: %d rows", len(r.list()))
	}

	// A dispatcher reads scope_drivers, folded the same way.
	in := w.driver(w.A, "InScope", "full_name_th", "'ห้วยขวาง สกอป'")
	w.exec(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw,
		billing_party_id, driver_id) VALUES ($1, 'form', 'first_mile', 'pending', now(), 'SRC', 'DST', $2, $3)`, w.A, w.partyX, in)
	ds := w.login("ds")
	for q, n := range map[string]int{"หวยขวาง": 1, "สกอป inscope": 1, "หวยขวาง nowhere": 0} {
		r := w.get("/v1/drivers?q="+url.QueryEscape(q), ds)
		expect(t, r, http.StatusOK, "")
		if len(r.list()) != n || (n == 1 && r.list()[0]["id"] != in) {
			t.Errorf("dispatcher q=%q: %s", q, r.raw)
		}
		if c := r.meta()["statusCounts"].(map[string]any); c["active"] != float64(n) {
			t.Errorf("dispatcher q=%q counts %v", q, c)
		}
	}
}
