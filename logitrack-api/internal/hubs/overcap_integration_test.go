//go:build integration

package hubs_test

import (
	"fmt"
	"net/http"
	"testing"
)

// TW8 (issue #88, acceptance "caps removed and verified with over-cap fixtures"; Appendix E §E.10 row 7): the task
// boards' hub filter listed the first 50 hubs (`hubs.slice(0, 50)`). Its Go source, `GET /v1/hubs`, is one uncapped
// list: with the load seed's 404 hubs and SOCs (Appendix D) every one comes back, the station filter and the search
// reach the ones past the old cap, and the name maps cover every code.
func TestOverCapHubListHasNoCap(t *testing.T) {
	w := newWorld(t)
	const n = 404
	w.exec(`INSERT INTO hubs (source_id, name_th, name_en, station_type, network)
		SELECT 'HUB-' || lpad(i::text, 4, '0'), 'ฮับ ' || i, 'Hub ' || i, CASE WHEN i % 10 = 0 THEN 'SOC' ELSE 'HUB' END, 'SPX'
		FROM generate_series(0, $1::int - 1) i`, n)
	op := w.login("op.o")

	r := w.get("/v1/hubs", op)
	expect(t, r, http.StatusOK, "")
	got := byCode(r.list())
	if len(got) != n {
		t.Fatalf("GET /v1/hubs listed %d hubs, want %d (old cap 50)", len(got), n)
	}
	for _, code := range []string{"HUB-0000", "HUB-0050", "HUB-0399", "HUB-0403"} {
		if got[code] == nil {
			t.Errorf("%s missing from the list", code)
		}
	}
	if r := w.get("/v1/hubs?stationType=HUB", op); len(r.list()) != n-n/10-1 {
		t.Errorf("stationType=HUB: %d hubs, want %d", len(r.list()), n-n/10-1)
	}
	if r := w.get("/v1/hubs?q=hub-0399", op); len(r.list()) != 1 || r.list()[0]["sourceNameTh"] != "ฮับ 399" {
		t.Errorf("search past the old cap: %s", r.raw)
	}
	r = w.get("/v1/hubs/maps", op)
	expect(t, r, http.StatusOK, "")
	codeToName, _ := r.data()["codeToName"].(map[string]any)
	if len(codeToName) != n || codeToName["HUB-0403"] != "ฮับ 403" {
		t.Errorf("codeToName has %d codes, want %d: %v", len(codeToName), n, fmt.Sprint(codeToName["HUB-0403"]))
	}
}
