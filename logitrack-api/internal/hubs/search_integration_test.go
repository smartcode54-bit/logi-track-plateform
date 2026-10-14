//go:build integration

package hubs_test

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func codes(items []map[string]any) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = str(it["sourceId"])
	}
	return out
}

func (r resp) next() string { s, _ := r.body["nextCursor"].(string); return s }

// UI2B (issue #142): q of GET /v1/hubs is folded as the web's lib/searchText.ts folds (internal/platform/searchtext):
// Thai tone marks, Latin accents and Thai digits fold away, and every word of q must be in the code or a name, in any
// order. SOCs stay in the list with the hubs (stationType narrows to one kind).
func TestHubFoldedSearch(t *testing.T) {
	w := newWorld(t)
	w.hub("SPK-GW", "J&T EXPRESS บางปู", "HUB", "SPK", nil, nil, &w.partyX)
	w.hub("SPK890174", "ห้วยขวาง10", "HUB", "SPK", nil, nil, nil)
	cafe := w.hub("XSPK", "คลังสินค้า", "HUB", "SPX", nil, nil, nil)
	w.exec(`UPDATE hubs SET name_en = 'Café Dépôt' WHERE id = $1`, cafe)
	w.hub("SOCE (บัวโรย)", "ศูนย์คัดแยกตะวันออก", "SOC", "SPX", nil, nil, nil)
	w.hub("SOCN", "ศูนย์คัดแยกเหนือ", "SOC", "SPX", nil, nil, nil)
	op := w.login("op.o")
	for query, want := range map[string][]string{
		"q=" + url.QueryEscape("บางปู j&t"):             {"SPK-GW"},    // every word, any order (lib/searchText.test.ts)
		"q=" + url.QueryEscape("หวยขวาง"):               {"SPK890174"}, // a missing tone mark still finds the row
		"q=" + url.QueryEscape("ห้วยขวาง๑๐"):            {"SPK890174"}, // Thai digits in q
		"q=" + url.QueryEscape("depot CAFE"):            {"XSPK"},      // Latin accents of the English name
		"q=spk":                                         {"SPK-GW", "SPK890174", "XSPK"},
		"q=" + url.QueryEscape("ศูนย์คัดแยก"):           {"SOCE (บัวโรย)", "SOCN"},
		"stationType=hub&q=" + url.QueryEscape("ศูนย์"): nil,
		"q=" + url.QueryEscape("spk nowhere"):           nil,
		"q=" + url.QueryEscape("่"):                     {"SOCE (บัวโรย)", "SOCN", "SPK-GW", "SPK890174", "XSPK"}, // folds to no word
	} {
		r := w.get("/v1/hubs?"+query, op)
		expect(t, r, http.StatusOK, "")
		if got := codes(r.list()); !slices.Equal(got, want) || r.next() != "" {
			t.Errorf("%s: %v (next %q), want %v", query, got, r.next(), want)
		}
	}
	expect(t, w.get("/v1/hubs?q="+url.QueryEscape(strings.Repeat("ก", 101)), op), http.StatusUnprocessableEntity, "invalid_argument")
}

// UI2B: ?limit and ?cursor page GET /v1/hubs by code (keyset, the B.1.5 conventions); without them the whole master
// answers in one response, as the maps, the distance panels and the master-data pages read it.
func TestHubPaging(t *testing.T) {
	w := newWorld(t)
	for i := range 30 {
		w.hub(fmt.Sprintf("H-%03d", i), fmt.Sprintf("ฮับ %d", i), "HUB", "SPX", nil, nil, nil)
	}
	w.hub("SOCE", "ศูนย์คัดแยกตะวันออก", "SOC", "SPX", nil, nil, nil)
	op := w.login("op.o")
	full := w.get("/v1/hubs", op)
	expect(t, full, http.StatusOK, "")
	all := codes(full.list())
	if len(all) != 31 || full.next() != "" || !slices.IsSorted(all) {
		t.Fatalf("the whole master: %v (next %q)", all, full.next())
	}
	collect := func(query string) ([]string, int) {
		t.Helper()
		var got []string
		pages, cur := 0, ""
		for {
			path := "/v1/hubs?" + query
			if cur != "" {
				path += "&cursor=" + url.QueryEscape(cur)
			}
			r := w.get(path, op)
			expect(t, r, http.StatusOK, "")
			pages++
			got = append(got, codes(r.list())...)
			if cur = r.next(); cur == "" {
				return got, pages
			}
			if pages > 50 {
				t.Fatal("no end")
			}
		}
	}
	for _, c := range []struct {
		query string
		want  []string
		pages int
	}{
		{"limit=10", all, 4},
		{"limit=31", all, 1},
		{"limit=500", all, 1},
		{"limit=7&q=" + url.QueryEscape("ฮับ"), all[:30], 5},
		{"limit=7&stationType=SOC", []string{"SOCE"}, 1},
	} {
		got, pages := collect(c.query)
		if !slices.Equal(got, c.want) || pages != c.pages {
			t.Errorf("%s: %d codes in %d pages, want %d in %d", c.query, len(got), pages, len(c.want), c.pages)
		}
	}
	// A cursor alone continues with the default page (50).
	p1 := w.get("/v1/hubs?limit=5", op)
	rest := w.get("/v1/hubs?cursor="+url.QueryEscape(p1.next()), op)
	if got := append(codes(p1.list()), codes(rest.list())...); !slices.Equal(got, all) || rest.next() != "" {
		t.Errorf("cursor without limit: %v", got)
	}
	// A page is a master-data read too: ETag and 304.
	etag := p1.header.Get("ETag")
	if r := w.get("/v1/hubs?limit=5", op, "If-None-Match", etag); etag == "" || r.status != http.StatusNotModified {
		t.Errorf("page revalidation: %q %d", etag, r.status)
	}
	for _, q := range []string{"limit=0", "limit=501", "limit=-1", "limit=x", "cursor=bad", "cursor=" + url.QueryEscape("eyJjIjoiIn0")} {
		expect(t, w.get("/v1/hubs?"+q, op), http.StatusUnprocessableEntity, "invalid_argument")
	}
	expect(t, w.get("/v1/hubs?page=2", op), http.StatusBadRequest, "bad_request")
}

// UI2B: GET /v1/hubs/{id} is one hub by uuid or code (case-insensitive, percent-encoded Thai and spaces), else the SOC
// a key such as SOCE names; masked as the list is; 404 for nothing. /maps keeps its own route.
func TestHubLookup(t *testing.T) {
	w := newWorld(t)
	id := w.hub("SPK890103-ลาดกระบัง26", "ลาดกระบัง", "HUB", "SPK", nil, nil, &w.partyY)
	soc := w.hub("SOCE (บัวโรย)", "ศูนย์คัดแยกตะวันออก", "SOC", "SPX", nil, nil, nil)
	op := w.login("op.o")
	for ref, want := range map[string]string{
		url.PathEscape("SPK890103-ลาดกระบัง26"): id,
		url.PathEscape("spk890103-ลาดกระบัง26"): id,
		id: id,
		url.PathEscape("SOCE (บัวโรย)"): soc,
		"SOCE": soc, // the SOC key (normalizeSocIdToKey)
		"soce": soc,
	} {
		r := w.get("/v1/hubs/"+ref, op)
		expect(t, r, http.StatusOK, "")
		if r.data()["id"] != want {
			t.Errorf("%s: %s", ref, r.raw)
		}
	}
	r := w.get("/v1/hubs/"+id, op)
	if h := r.data(); h["sourceNameTh"] != "ลาดกระบัง" || h["linkedCustomerName"] != "TTP — TTP Co." || h["stationType"] != "HUB" {
		t.Fatalf("payload: %s", r.raw)
	}
	etag := r.header.Get("ETag")
	if r2 := w.get("/v1/hubs/"+id, op, "If-None-Match", etag); etag == "" || r2.status != http.StatusNotModified {
		t.Errorf("revalidation: %q %d", etag, r2.status)
	}
	// Masked as the list: the customer-scope principal over X does not see Y's party behind the hub.
	if h := w.get("/v1/hubs/"+id, w.login("cu")).data(); h["linkedCustomerName"] != nil || h["linkedCustomerId"] != nil ||
		h["linkedPartyId"] != w.partyY {
		t.Errorf("customer scope reads %v", h)
	}
	for _, ref := range []string{"NOPE", "SOCW", "00000000-0000-7000-8000-000000000001"} {
		expect(t, w.get("/v1/hubs/"+ref, op), http.StatusNotFound, "not_found")
	}
	expect(t, w.getRaw("/v1/hubs/%ZZ", op), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/hubs/"+id+"?x=1", op), http.StatusBadRequest, "bad_request")
	expect(t, w.get("/v1/hubs/"+id, ""), http.StatusUnauthorized, "unauthenticated")
	if m := w.get("/v1/hubs/maps", op); m.status != http.StatusOK || m.data()["nameToCode"] == nil {
		t.Errorf("maps: %s", m.raw)
	}
}
