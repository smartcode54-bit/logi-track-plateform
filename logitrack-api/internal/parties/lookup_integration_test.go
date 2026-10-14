//go:build integration

package parties_test

import (
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// codesOf are the codes of a customer list answer, in its order.
func codesOf(r resp) []string {
	var out []string
	for _, it := range r.list() {
		out = append(out, str(it["code"]))
	}
	return out
}

// UI8 (issue #184): GET /v1/customers?fields=minimal&ids= names customers by a customer id or a billing party id, with
// the reach and projection of the minimal list, for every authenticated reader of that list (operation_staff and
// operators get the names of Income's "all customers" view and the rate card without fleet:manage_customers).
func TestCustomersByIDs(t *testing.T) {
	w := newWorld(t)
	w.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'operation_staff')`, w.user("os.o@example.test"), w.O)
	unknown := uuid.NewString()
	lookup := func(bearer string, ids ...string) resp {
		t.Helper()
		return w.get("/v1/customers?fields=minimal&ids="+url.QueryEscape(strings.Join(ids, ",")), bearer)
	}
	taO := w.login("ta.o")

	// By customer id, by billing party id, both mixed with an unknown id and a carrier's party (no customer): the
	// matches once each, by code, one answer without nextCursor.
	for _, c := range []struct {
		name string
		ids  []string
		want []string
	}{
		{"customer id", []string{w.custX}, []string{"X"}},
		{"party id", []string{w.partyY}, []string{"Y"}},
		{"mixed", []string{w.partyY, unknown, w.custX, w.partyA}, []string{"X", "Y"}},
		{"one customer twice", []string{w.custX, w.partyX, w.custX}, []string{"X"}},
		{"unknown only", []string{unknown, w.partyC}, nil},
	} {
		r := lookup(taO, c.ids...)
		expect(t, r, http.StatusOK, "")
		if got := codesOf(r); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v (%s)", c.name, got, c.want, r.raw)
		}
		if _, ok := r.body["nextCursor"]; ok {
			t.Errorf("%s: a lookup carries nextCursor: %s", c.name, r.raw)
		}
	}
	// The minimal projection, the billing party included whichever id asked.
	r := lookup(taO, w.partyX)
	items := r.list()
	if len(items) != 1 || items[0]["id"] != w.custX || items[0]["billingPartyId"] != w.partyX || items[0]["name"] != "X Co." {
		t.Fatalf("by party: %s", r.raw)
	}
	for k := range items[0] {
		if !slices.Contains([]string{"id", "billingPartyId", "code", "name"}, k) {
			t.Errorf("the minimal lookup carries %s", k)
		}
	}
	// The parameter repeated, and the full record for a steward (fields omitted), driverIdTypes included.
	r = w.get("/v1/customers?fields=minimal&ids="+w.custY+"&ids="+w.partyX, taO)
	expect(t, r, http.StatusOK, "")
	if got := codesOf(r); !slices.Equal(got, []string{"X", "Y"}) {
		t.Errorf("repeated ids: %v", got)
	}
	r = w.get("/v1/customers?ids="+w.partyY, taO)
	expect(t, r, http.StatusOK, "")
	if items := r.list(); len(items) != 1 || len(items[0]["driverIdTypes"].([]any)) != 1 || items[0]["billingDateBasis"] != "delivered" {
		t.Fatalf("full lookup: %s", r.raw)
	}

	// The cap: 100 distinct ids are one request (the URI is about 3.9 KiB next to the bearer, app.ReadBufferSize);
	// 101 are 422 too_many (duplicates do not count).
	many := []string{w.custX}
	for len(many) < 100 {
		many = append(many, uuid.NewString())
	}
	r = lookup(taO, many...)
	expect(t, r, http.StatusOK, "")
	if got := codesOf(r); !slices.Equal(got, []string{"X"}) {
		t.Errorf("100 ids: %v", got)
	}
	expect(t, lookup(taO, append(many, w.custX, many[1])...), http.StatusOK, "")
	r = lookup(taO, append(many, uuid.NewString())...)
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if f := r.fields(); f["ids"] != "too_many" || len(f) != 1 {
		t.Errorf("101 ids: %s", r.raw)
	}
	// Bad ids and the parameters a lookup excludes: 422 with field errors.
	for query, want := range map[string]map[string]string{
		"ids=":                                {"ids": "required"},
		"ids=,":                               {"ids": "required"},
		"ids=nope":                            {"ids": "invalid"},
		"ids=" + w.custX + ",nope":            {"ids": "invalid"},
		"ids=" + w.custX + "&q=x":             {"q": "exclusive"},
		"ids=" + w.custX + "&limit=10":        {"limit": "exclusive"},
		"ids=" + w.custX + "&cursor=abc":      {"cursor": "exclusive"},
		"ids=" + w.custX + "&fields=all":      {"fields": "invalid"},
		"ids=nope&q=x&fields=minimal&limit=1": {"ids": "invalid", "q": "exclusive", "limit": "exclusive"},
	} {
		r := w.get("/v1/customers?"+query, taO)
		expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
		if got := r.fields(); !maps.Equal(got, want) {
			t.Errorf("%s: fields %v, want %v", query, got, want)
		}
	}
	expect(t, w.get("/v1/customers?fields=minimal&id="+w.custX, taO), http.StatusBadRequest, "bad_request")

	// Any authenticated reader of the minimal list: own-fleet operation_staff and operators, carrier operators.
	for _, u := range []string{"os.o", "op.o", "op.a"} {
		tok := w.login(u)
		r := lookup(tok, w.partyX, w.custY)
		expect(t, r, http.StatusOK, "")
		if got := codesOf(r); !slices.Equal(got, []string{"X", "Y"}) {
			t.Errorf("%s: %v", u, got)
		}
		// The full record stays steward-only, ids or not.
		r = w.get("/v1/customers?ids="+w.custX, tok)
		expect(t, r, http.StatusForbidden, "permission_denied")
	}
	expect(t, w.get("/v1/customers?fields=minimal&ids="+w.custX, ""), http.StatusUnauthorized, "")

	// A customer-scope principal over X: only its own party, by either id; Y is absent, not an error.
	cu := w.login("cu")
	r = lookup(cu, w.custX, w.custY, w.partyY)
	expect(t, r, http.StatusOK, "")
	if got := codesOf(r); !slices.Equal(got, []string{"X"}) {
		t.Errorf("customer scope: %v", got)
	}
	r = lookup(cu, w.partyY)
	expect(t, r, http.StatusOK, "")
	if len(r.list()) != 0 {
		t.Errorf("customer scope reads another party: %s", r.raw)
	}
	expect(t, w.get("/v1/customers?ids="+w.custX, cu), http.StatusForbidden, "permission_denied")
}
