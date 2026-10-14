//go:build integration

package iam_test

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
)

// UI2B (issue #142): q of GET /v1/users is folded as the web's lib/searchText.ts folds (search_fold, 0026): Thai tone
// marks, Latin accents and Thai digits fold away, and every word of q must be in the e-mail or the display name, in
// any order; a LIKE wildcard in q is a plain character. Both orders search the same way, inside the caller's reach.
func TestUsersFoldedSearch(t *testing.T) {
	w := newWorld(t)
	add := func(email, name string) string {
		id := w.id(`INSERT INTO users (email, display_name) VALUES ($1::text, $2) RETURNING id::text`, email, name)
		w.member(id, w.O, "operator")
		return id
	}
	jose := add("jose.dupont@example.test", "José Dépôt")
	thai := add("somchai@example.test", "สมศักดิ์ ห้วยขวาง ๑๒")
	under := add("a_b@example.test", "Under Score")
	outside := w.id(`INSERT INTO users (email, display_name) VALUES ('jose.c@example.test', 'José Outside') RETURNING id::text`)
	w.member(outside, w.C, "operator")
	ta := w.login("ta.o@example.test")
	ids := func(sort, q string) []string {
		t.Helper()
		r := w.get("/v1/users?sort="+sort+"&q="+url.QueryEscape(q), ta)
		expect(t, r, http.StatusOK, "")
		var out []string
		for _, it := range r.data().([]any) {
			out = append(out, it.(map[string]any)["id"].(string))
		}
		slices.Sort(out)
		return out
	}
	for q, want := range map[string][]string{
		"jose depot":     {jose},  // Latin accents, two words (not C's José: outside the reach)
		"DÉPÔT josé":     {jose},  // any order, any case
		"หวยขวาง สมศกด":  nil,     // a vowel sign (mai han-akat) stays: สมศกด is not สมศักด
		"หวยขวาง สมศักด": {thai},  // tone marks and thanthakhat fold away
		"12 somchai":     {thai},  // Thai digits in the display name, the e-mail
		"a_b":            {under}, // _ is a plain character, not a LIKE wildcard
		"%":              nil,
		"jose nowhere":   nil, // every word must match
	} {
		for _, sort := range []string{"created_at", "last_login_at"} {
			if got := ids(sort, q); !slices.Equal(got, want) {
				t.Errorf("sort=%s q=%q: %v, want %v", sort, q, got, want)
			}
		}
	}
}
