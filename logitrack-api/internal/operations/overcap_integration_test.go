//go:build integration

package operations_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// TW8 (issue #88, acceptance "caps removed and verified with over-cap fixtures"; Appendix E §E.10 rows 4, 5, 6;
// developer-spec.md §10.10): the Firestore pages stopped at a fixed window (income 500 trips, standby 300 records,
// incidents 200 reports) and filtered dates in the browser over it. The Go lists they read now are keyset pages with
// the range on the server: with more rows than each old cap in the range, a walk with the page size the web asks for
// reaches every row once, newest first, and nothing outside the range or the tenant.

const (
	oldIncomeCap    = 500
	oldStandbyCap   = 300
	oldIncidentsCap = 200
)

// walk pages a list to its last row as who with the web's page size, checking that each page holds at most limit
// rows, that every row comes once and that the order is (axis, id) DESC on the given field.
func (r *readWorld) walk(who, base string, limit int, axis string) []map[string]any {
	r.t.Helper()
	var all []map[string]any
	seen := map[string]bool{}
	cursor := ""
	for pages := 1; ; pages++ {
		path := base + "&limit=" + strconv.Itoa(limit)
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		data, next, _ := r.ok(who, path)
		page := rows(data)
		if len(page) > limit {
			r.t.Fatalf("%s: page %d has %d rows, limit %d", base, pages, len(page), limit)
		}
		for _, row := range page {
			id, _ := row["id"].(string)
			if seen[id] {
				r.t.Fatalf("%s: row %s twice", base, id)
			}
			seen[id] = true
			if n := len(all); n > 0 {
				prev, cur := str(all[n-1][axis]), str(row[axis])
				if cur > prev || (cur == prev && id > str(all[n-1]["id"])) {
					r.t.Fatalf("%s: rows %d and %d out of (%s, id) DESC order", base, n-1, n, axis)
				}
			}
			all = append(all, row)
		}
		if next == "" {
			return all
		}
		if pages > 100 {
			r.t.Fatalf("%s: more than 100 pages", base)
		}
		cursor = next
	}
}

func str(v any) string { s, _ := v.(string); return s }

// Income (old cap 500): 650 delivered trips of September and 350 standby records ended in it, read the way
// features/accounting/api/income.go.ts reads them (200 per page); every trip carries its stored price.
func TestOverCapIncomeReachesEveryTripAndStandby(t *testing.T) {
	r := newReadWorld(t)
	const n = oldIncomeCap + 150
	r.exec(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, origin_raw, origin_hub_id,
			billing_party_id, created_at, delivered_at)
		SELECT $1, 'task', 'INC-' || i, 'delivered', 'first_mile', 'ALANG-A', $2, $3,
		       timestamptz '2026-09-01 00:00Z' + (i % 29) * interval '1 day',
		       timestamptz '2026-09-01 02:00Z' + (i % 29) * interval '1 day' + (i % 7) * interval '1 hour'
		FROM generate_series(1, $4::int) i`, r.O, r.hubSrc, r.partyX, n)
	r.exec(`INSERT INTO trip_billing_snapshots (trip_id, tenant_id, estimate_thb, base_rate_thb, job_category_at_pricing, computed_by)
		SELECT id, tenant_id, 1000 + (substr(trip_no, 5)::int), 1000, 'PRIMARY', 'etl' FROM trip_records WHERE trip_no LIKE 'INC-%'`)
	// Outside the range (October) and another tenant's delivered trips in it: never on the page.
	r.exec(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, created_at, delivered_at)
		VALUES ($1, 'task', 'OCT-1', 'delivered', 'first_mile', timestamptz '2026-10-02 02:00Z', timestamptz '2026-10-02 03:00Z')`, r.O)
	r.exec(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, created_at, delivered_at)
		SELECT $1, 'task', 'C-' || i, 'delivered', 'first_mile', timestamptz '2026-09-05 01:00Z', timestamptz '2026-09-05 02:00Z'
		FROM generate_series(1, 20) i`, r.C)

	for _, base := range []string{
		"/v1/trips?axis=delivered&from=2026-09-01&to=2026-09-30",
		"/v1/trips?axis=delivered&from=2026-09-01&to=2026-09-30&customerId=" + r.partyX.String(),
	} {
		all := r.walk("staffO", base, 200, "deliveredAt")
		if len(all) != n {
			t.Fatalf("%s: %d trips, want %d (old cap %d)", base, len(all), n, oldIncomeCap)
		}
		total := 0.0
		for _, row := range all {
			if !strings.HasPrefix(str(row["tripNo"]), "INC-") {
				t.Fatalf("%s: %v is outside the range or the tenant", base, row["tripNo"])
			}
			b, _ := row["billing"].(map[string]any)
			price, _ := b["estimateThb"].(float64)
			if b == nil || b["priced"] != true || price <= 0 {
				t.Fatalf("%s: %v has no stored price: %v", base, row["tripNo"], b)
			}
			total += price
		}
		// sum over i = 1..n of 1000 + i: the totals the page shows after "Load all" cover every trip.
		if want := float64(n*1000 + n*(n+1)/2); total != want {
			t.Errorf("%s: total %v, want %v", base, total, want)
		}
	}

	const sb = oldStandbyCap + 50
	r.exec(`INSERT INTO standby_records (tenant_id, tenant_source, driver_id, customer_party_id, customer_resolved,
			billing_estimate_thb, billing_party_id, started_at, ended_at, created_at)
		SELECT $1, 'driver', $2, $3, true, 900, $3,
		       timestamptz '2026-09-01 01:00Z' + (i % 29) * interval '1 day',
		       timestamptz '2026-09-01 04:00Z' + (i % 29) * interval '1 day' + (i % 5) * interval '1 minute',
		       timestamptz '2026-09-01 01:00Z' + (i % 29) * interval '1 day'
		FROM generate_series(1, $4::int) i`, r.O, r.dO1, r.partyX, sb)
	all := r.walk("staffO", "/v1/standby?sort=ended&from=2026-09-01&to=2026-09-30", 200, "endedAt")
	if len(all) != sb {
		t.Fatalf("income standby: %d records, want %d", len(all), sb)
	}
	for _, row := range all {
		if b, _ := row["billing"].(map[string]any); b == nil || b["estimateThb"] != 900.0 {
			t.Fatalf("income standby row without its price: %v", row)
		}
	}
}

// Standby records (old cap 300): 350 records in the range, read the way features/standby/api/standby.go.ts reads
// them (100 per page, created axis).
func TestOverCapStandbyReachesEveryRecord(t *testing.T) {
	r := newReadWorld(t)
	const n = oldStandbyCap + 50
	r.exec(`INSERT INTO standby_records (tenant_id, tenant_source, driver_id, started_at, ended_at, created_at)
		SELECT $1, 'driver', $2, at, at + interval '2 hours', at
		FROM (SELECT timestamptz '2026-09-01 01:00Z' + (i % 29) * interval '1 day' + (i % 11) * interval '1 minute' AS at
		      FROM generate_series(1, $3::int) i) s`, r.O, r.dO1, n)
	// Outside the range: never on the page; inside it with no range: on it.
	r.exec(`INSERT INTO standby_records (tenant_id, tenant_source, driver_id, started_at, created_at)
		VALUES ($1, 'driver', $2, timestamptz '2026-10-03 01:00Z', timestamptz '2026-10-03 01:00Z')`, r.O, r.dO1)

	all := r.walk("staffO", "/v1/standby?from=2026-09-01&to=2026-09-30", 100, "createdAt")
	if len(all) != n {
		t.Fatalf("standby: %d records, want %d (old cap %d)", len(all), n, oldStandbyCap)
	}
	for _, row := range all {
		if row["driverName"] != "O1 T" {
			t.Fatalf("standby row without its driver's name: %v", row)
		}
	}
	if all := r.walk("staffO", "/v1/standby?sort=created", 100, "createdAt"); len(all) != n+1 {
		t.Errorf("standby without a range: %d records, want %d", len(all), n+1)
	}
}

// Incident reports (old cap 200): 250 reports in the range, read the way
// features/incident-reports/api/incidents.go.ts reads them (100 per page).
func TestOverCapIncidentsReachEveryReport(t *testing.T) {
	r := newReadWorld(t)
	const n = oldIncidentsCap + 50
	r.exec(`INSERT INTO incident_reports (tenant_id, tenant_source, driver_id, reported_by_kind, description, created_at)
		SELECT $1, 'driver', $2, 'driver', 'เหตุ ' || i, timestamptz '2026-09-10 00:00Z' + (i % 13) * interval '1 hour'
		FROM generate_series(1, $3::int) i`, r.O, r.dO1, n)
	r.exec(`INSERT INTO incident_reports (tenant_id, tenant_source, reported_by_kind, created_at)
		VALUES ($1, 'driver', 'driver', timestamptz '2026-09-12 05:00Z')`, r.O)
	r.exec(`INSERT INTO incident_reports (tenant_id, tenant_source, reported_by_kind, created_at)
		SELECT $1, 'driver', 'driver', timestamptz '2026-09-10 02:00Z' FROM generate_series(1, 10) i`, r.C)

	all := r.walk("staffO", "/v1/incidents?from=2026-09-10&to=2026-09-10", 100, "createdAt")
	if len(all) != n {
		t.Fatalf("incidents: %d reports, want %d (old cap %d)", len(all), n, oldIncidentsCap)
	}
	for _, row := range all {
		if !strings.HasPrefix(str(row["description"]), "เหตุ ") || row["driverName"] != "O1 T" {
			t.Fatalf("incident outside the day or the tenant, or without its driver: %v", row)
		}
	}
}
