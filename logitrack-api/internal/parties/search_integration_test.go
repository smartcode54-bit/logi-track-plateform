//go:build integration

package parties_test

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
)

// UI2B (issue #142): q of GET /v1/customers (full and fields=minimal, the customer pickers) is folded as the web's
// lib/searchText.ts folds (search_fold, 0026): Thai tone marks, Latin accents and Thai digits fold away, and every
// word of q must be in the code or the name, in any order. A customer-scope principal still reads only its own parties.
func TestCustomersFoldedSearch(t *testing.T) {
	w := newWorld(t)
	jt := w.id(`INSERT INTO customers (code, name) VALUES ('JNT', 'J&T EXPRESS บางปู') RETURNING id::text`)
	cafe := w.id(`INSERT INTO customers (code, name) VALUES ('CAFE1', 'Café ห้วยขวาง ๑๐') RETURNING id::text`)
	w.exec(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1), ('customer', $2)`, jt, cafe)
	taO := w.login("ta.o")
	codes := func(bearer, query string) []string {
		t.Helper()
		r := w.get("/v1/customers?"+query, bearer)
		expect(t, r, http.StatusOK, "")
		var out []string
		for _, it := range r.list() {
			out = append(out, str(it["code"]))
		}
		slices.Sort(out)
		return out
	}
	for q, want := range map[string][]string{
		"บางปู j&t":    {"JNT"},   // every word in any order (lib/searchText.test.ts)
		"cafe หวยขวาง": {"CAFE1"}, // a Latin accent and a tone mark fold away
		"หวยขวาง 10":   {"CAFE1"}, // Thai digits in the name
		"cafe1 ๑๐":     {"CAFE1"}, // Thai digits in q, the code
		"x co.":        {"X"},
		"j&t nowhere":  nil, // every word must match
		"%":            nil, // a LIKE wildcard is a plain character
	} {
		for _, fields := range []string{"", "fields=minimal&"} {
			if got := codes(taO, fields+"q="+url.QueryEscape(q)); !slices.Equal(got, want) {
				t.Errorf("%sq=%q: %v, want %v", fields, q, got, want)
			}
		}
	}
	// The customer-scope principal over X: the search runs inside its own parties only.
	cu := w.login("cu")
	if got := codes(cu, "fields=minimal&q="+url.QueryEscape("co.")); !slices.Equal(got, []string{"X"}) {
		t.Errorf("customer scope: %v", got)
	}
	if got := codes(cu, "fields=minimal&q="+url.QueryEscape("หวยขวาง")); got != nil {
		t.Errorf("customer scope found another party's customer: %v", got)
	}
}
