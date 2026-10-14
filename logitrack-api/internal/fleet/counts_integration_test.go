//go:build integration

package fleet_test

import (
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// statusCounts reads meta.statusCounts of a GET /v1/trucks answer.
func statusCounts(t *testing.T, r resp) map[string]int {
	t.Helper()
	meta, _ := r.body["meta"].(map[string]any)
	raw, ok := meta["statusCounts"].(map[string]any)
	if !ok {
		t.Fatalf("no meta.statusCounts: %s", r.raw)
	}
	out := map[string]int{}
	for k, v := range raw {
		out[k] = int(v.(float64))
	}
	return out
}

// counts is a full statusCounts map: every truck status, 0 unless given.
func counts(set map[string]int) map[string]int {
	out := map[string]int{"active": 0, "inactive": 0, "maintenance": 0, "insurance_claim": 0, "sold": 0}
	maps.Copy(out, set)
	return out
}

// UI8 (issue #184): GET /v1/trucks carries meta.statusCounts over every row matching the filters other than status
// (and the cursor), not just the page, in the caller's reach, so the truck-assignment page's "total trucks" (own
// trucks not in maintenance) reads a one-row page instead of every truck. A dispatcher's counts are its projection's.
func TestTruckStatusCounts(t *testing.T) {
	w := newWorld(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0
	add := func(tenant, status string, extra ...string) string {
		n++
		return w.truck(tenant, fmt.Sprintf("นน-%d", 1000+n), base.Add(time.Duration(n)*time.Minute),
			append([]string{"status", "'" + status + "'"}, extra...)...)
	}
	for range 3 {
		add(w.O, "active")
	}
	add(w.O, "maintenance")
	add(w.O, "maintenance", "type_raw", "'6 Wheels'", "vehicle_class", "'6WH'")
	add(w.O, "sold")
	add(w.A, "active") // a carrier working for O: subcontractor trucks in O's reach
	add(w.A, "inactive")
	add(w.C, "active") // outside O's reach
	taO := w.login("ta.o")

	cases := []struct {
		query string
		want  map[string]int
	}{
		{"ownership=own&limit=1", counts(map[string]int{"active": 3, "maintenance": 2, "sold": 1})},
		{"limit=1", counts(map[string]int{"active": 4, "inactive": 1, "maintenance": 2, "sold": 1})},
		{"ownership=subcontractor", counts(map[string]int{"active": 1, "inactive": 1})},
		{"tenantId=" + w.A, counts(map[string]int{"active": 1, "inactive": 1})},
		{"ownership=own&type=6wh", counts(map[string]int{"maintenance": 1})},
		{"q=" + url.QueryEscape("นน-1006"), counts(map[string]int{"sold": 1})},
		// status filters the page, not the counts (the drivers list's rule): every status stays countable.
		{"ownership=own&status=maintenance", counts(map[string]int{"active": 3, "maintenance": 2, "sold": 1})},
	}
	for _, c := range cases {
		r := w.get("/v1/trucks?"+c.query, taO)
		expect(t, r, http.StatusOK, "")
		if got := statusCounts(t, r); !maps.Equal(got, c.want) {
			t.Errorf("%s: statusCounts %v, want %v", c.query, got, c.want)
		}
	}
	// One row is the page; the counts are the whole set's, and the next page carries the same counts.
	p1 := w.get("/v1/trucks?ownership=own&limit=1", taO)
	if len(p1.list()) != 1 || p1.next() == "" {
		t.Fatalf("one-row page: %s", p1.raw)
	}
	p2 := w.get("/v1/trucks?ownership=own&limit=1&cursor="+url.QueryEscape(p1.next()), taO)
	expect(t, p2, http.StatusOK, "")
	if !maps.Equal(statusCounts(t, p2), statusCounts(t, p1)) {
		t.Errorf("second page counts %v, first %v", statusCounts(t, p2), statusCounts(t, p1))
	}
	// The status filter is still checked; a carrier counts its own trucks only.
	expect(t, w.get("/v1/trucks?status=parked", taO), http.StatusUnprocessableEntity, "invalid_argument")
	if got := statusCounts(t, w.get("/v1/trucks", w.login("ta.c"))); !maps.Equal(got, counts(map[string]int{"active": 1})) {
		t.Errorf("carrier C counts %v", got)
	}

	// A dispatcher counts the scope_trucks projection: its own organisation's trucks and those of in-scope work.
	inScope := add(w.A, "maintenance")
	add(w.D, "active")
	w.exec(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw,
		billing_party_id, truck_id) VALUES ($1, 'form', 'first_mile', 'pending', now(), 'SRC', 'DST', $2, $3)`, w.A, w.partyX, inScope)
	r := w.get("/v1/trucks?limit=1", w.login("ds"))
	expect(t, r, http.StatusOK, "")
	if got := statusCounts(t, r); !maps.Equal(got, counts(map[string]int{"active": 1, "maintenance": 1})) {
		t.Errorf("dispatcher counts %v", got)
	}
}
