//go:build integration

package operations_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// The read routes of T30 (issue #50; Appendix B §B.2.11-§B.2.13, main spec §5.4) against PostgreSQL 18 as
// logitrack_app (db.WithPrincipal; the scope principals through the scope_* views), behind the route guards. The
// principals carry the capability sets RBAC resolves for their role (authz.TenantDefaults / ScopeDefaults); the files
// are signed by a stand-in, the hub master is read from the database.

// fakeFiles signs every committed file of the fixture with a recognisable URL.
type fakeFiles struct{}

func (fakeFiles) SignedFiles(_ context.Context, ids []uuid.UUID, ttl time.Duration) (map[uuid.UUID]storage.Signed, error) {
	out := map[uuid.UUID]storage.Signed{}
	for _, id := range ids {
		out[id] = storage.Signed{Key: "k/" + id.String(), URL: fmt.Sprintf("https://files.test/%s?ttl=%s", id, ttl)}
	}
	return out, nil
}

// dbHubs reads the hub master of the fixture (the api reads it through hubs.Service).
type dbHubs struct{ w *world }

func (h dbHubs) Hubs(ctx context.Context, _ *authz.Principal) ([]operations.Hub, error) {
	rows, err := h.w.etl.Query(ctx, `SELECT id, source_id::text, name_th, name_en FROM hubs ORDER BY source_id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (operations.Hub, error) {
		var x operations.Hub
		err := r.Scan(&x.ID, &x.Code, &x.NameTh, &x.NameEn)
		return x, err
	})
}

type readWorld struct {
	*world
	svc        *operations.Service
	app        *fiber.App
	principals map[string]*authz.Principal
	D          uuid.UUID // the dispatcher's organisation (a carrier)
}

func caps(sets ...authz.CapSet) authz.CapSet {
	var out authz.CapSet
	for _, s := range sets {
		out = out.Union(s)
	}
	return out
}

func newReadWorld(t *testing.T) *readWorld {
	t.Helper()
	w := newWorld(t)
	r := &readWorld{world: w}
	var err error
	r.svc, err = operations.New(operations.Deps{Pool: w.pool, Log: zerolog.Nop(), Now: func() time.Time { return now },
		Files: fakeFiles{}, Hubs: dbHubs{w}})
	if err != nil {
		t.Fatal(err)
	}
	r.D = w.id(`INSERT INTO tenants (kind, name_th, code, legal_type) VALUES ('carrier', 'TTP', 'TTP', 'company') RETURNING id`)
	clone := func(p *authz.Principal, set authz.CapSet) *authz.Principal {
		c := *p
		c.Caps = set
		return &c
	}
	admin := authz.TenantDefaults(authz.TenantAdmin)
	r.principals = map[string]*authz.Principal{
		"staffO":   clone(w.staffO, admin),
		"operator": clone(w.staffO, authz.TenantDefaults(authz.Operator)), // no accounting:view_income
		"staffA":   clone(w.staffA, admin),
		"fmOnly": {UserID: uuid.New(), TenantID: &w.O, TenantRole: authz.User, TenantKind: "own_fleet",
			SubtenantIDs: []uuid.UUID{w.A}, Caps: authz.NewCapSet(authz.OperationsViewFirstMile)},
		"driverO1": clone(w.driverO1, authz.TenantDefaults(authz.Driver)),
		"dispatcher": {UserID: uuid.New(), TenantID: &r.D, TenantRole: authz.OperationStaff, TenantKind: "carrier",
			Dispatcher: true, PartyIDs: []uuid.UUID{w.partyX},
			Caps: caps(authz.TenantDefaults(authz.OperationStaff), authz.ScopeDefaults(authz.ScopeDispatcher))},
		"customerX": {UserID: uuid.New(), PartyIDs: []uuid.UUID{w.partyX}, Caps: authz.ScopeDefaults(authz.ScopeCustomer)},
		"customerY": {UserID: uuid.New(), PartyIDs: []uuid.UUID{w.partyY}, Caps: authz.ScopeDefaults(authz.ScopeCustomer)},
		"all":       clone(w.all, caps(admin, authz.PlatformDefaults(authz.PlatformAdmin))),
	}
	r.app = fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	auth := func(c fiber.Ctx) error {
		authz.SetPrincipal(c, r.principals[c.Get("X-Test-Principal")])
		return c.Next()
	}
	for _, g := range operations.Groups(r.svc, auth) {
		g.Mount(r.app.Group(g.Prefix))
	}
	return r
}

// get calls a route as who and decodes the envelope.
func (r *readWorld) get(who, path string) (int, map[string]any) {
	r.t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("X-Test-Principal", who)
	res, err := r.app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second})
	if err != nil {
		r.t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		r.t.Fatalf("GET %s as %s: %d %s", path, who, res.StatusCode, b)
	}
	return res.StatusCode, m
}

// ok calls a route that must answer 200 and returns data, nextCursor and meta.
func (r *readWorld) ok(who, path string) (data any, next string, meta map[string]any) {
	r.t.Helper()
	status, m := r.get(who, path)
	if status != 200 {
		r.t.Fatalf("GET %s as %s: %d %v", path, who, status, m)
	}
	next, _ = m["nextCursor"].(string)
	meta, _ = m["meta"].(map[string]any)
	return m["data"], next, meta
}

func rows(data any) []map[string]any {
	list, _ := data.([]any)
	out := make([]map[string]any, len(list))
	for i, x := range list {
		out[i], _ = x.(map[string]any)
	}
	return out
}

func ids(list []map[string]any) []string {
	out := make([]string, len(list))
	for i, x := range list {
		out[i], _ = x["id"].(string)
	}
	return out
}

func respCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	if d, ok := e["details"].(map[string]any); ok {
		if f, ok := d["fields"].([]any); ok && len(f) > 0 {
			f0, _ := f[0].(map[string]any)
			return fmt.Sprintf("%v %v", f0["field"], f0["reason"])
		}
		if c, ok := d["missingCapability"]; ok {
			return fmt.Sprintf("%v %v", e["code"], c)
		}
	}
	code, _ := e["code"].(string)
	return code
}

// keys collects every JSON key of a payload, nested objects and arrays included.
func keys(v any, into map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			into[k] = true
			keys(val, into)
		}
	case []any:
		for _, val := range x {
			keys(val, into)
		}
	}
}

// camelToSnake turns a JSON key into the column form scope.IsHidden reads.
func camelToSnake(k string) string {
	var b strings.Builder
	for i, c := range k {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(c + 'a' - 'A')
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// trip inserts a trip row of tenant (as logitrack_etl) and returns its id.
type tripRow struct {
	tenant                uuid.UUID
	no, status            string
	task, driver, truck   *uuid.UUID
	originRaw             *string
	originHub             *uuid.UUID
	destinationRaw        *string
	destinationHub        *uuid.UUID
	destinationSoc        *string
	plate, partner, class *string
	party                 *uuid.UUID
	multi                 bool
	createdAt             time.Time
}

func (r *readWorld) trip(x tripRow) uuid.UUID {
	r.t.Helper()
	if x.status == "" {
		x.status = "in_transit"
	}
	return r.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, task_id, driver_id, truck_id,
		origin_raw, origin_hub_id, destination_raw, destination_hub_id, destination_soc_key, truck_license_plate_snapshot,
		partner_code, vehicle_class, billing_party_id, is_multi_delivery, created_at)
		VALUES ($1, 'task', $2, $3, 'first_mile', $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) RETURNING id`,
		x.tenant, x.no, x.status, x.task, x.driver, x.truck, x.originRaw, x.originHub, x.destinationRaw, x.destinationHub,
		x.destinationSoc, x.plate, x.partner, x.class, x.party, x.multi, x.createdAt)
}

func (r *readWorld) task(tenant uuid.UUID, typ string, driver *uuid.UUID, party *uuid.UUID, planAt time.Time) uuid.UUID {
	r.t.Helper()
	return r.id(`INSERT INTO tasks (tenant_id, tenant_source, task_no, task_type, status, plan_at, source_hub_raw, source_hub_id,
		destination_raw, destination_hub_id, billing_party_id, driver_id, license_plate_snapshot, created_at)
		VALUES ($1, 'form', $2, $3, CASE WHEN $4::uuid IS NULL THEN 'pending' ELSE 'assigned' END, $5, 'ALANG-A', $6, 'SOCE', $7,
		        $8, $4, '1กข-1234', $5)
		RETURNING id`, tenant, "T-"+uuid.NewString()[:8], typ, driver, planAt, r.hubSrc, r.hubDst, party)
}

// AC: pages of at most 200 rows with the cursor (created_at, id); every row reached once, newest first, no cap.
func TestMonitorPagesOf200(t *testing.T) {
	r := newReadWorld(t)
	r.exec(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, origin_raw, origin_hub_id, created_at)
		SELECT $1, 'task', 'PG-' || i, 'in_transit', 'first_mile', 'ALANG-A', $2,
		       timestamptz '2026-09-01 01:00Z' + (i % 100) * interval '7 hours'
		FROM generate_series(1, 450) i`, r.O, r.hubSrc)
	// Another tenant's trips are outside the reach of O's staff.
	r.exec(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, created_at)
		SELECT $1, 'task', 'C-' || i, 'in_transit', 'first_mile', timestamptz '2026-09-05 01:00Z' FROM generate_series(1, 20) i`, r.C)
	base := "/v1/trips/monitor?from=2026-09-01&to=2026-10-31"
	var all []map[string]any
	cursor := ""
	pages := 0
	for {
		path := base
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		data, next, meta := r.ok("staffO", path)
		page := rows(data)
		pages++
		if len(page) > operations.MonitorLimit {
			t.Fatalf("page %d has %d rows", pages, len(page))
		}
		if (cursor == "") != (meta != nil) {
			t.Fatalf("page %d: facets %v (only the first page carries them)", pages, meta != nil)
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		cursor = next
	}
	if pages != 3 || len(all) != 450 {
		t.Fatalf("%d pages, %d rows; want 3 pages of 200, 200, 50 = 450", pages, len(all))
	}
	seen := map[string]bool{}
	for i, row := range all {
		id, _ := row["id"].(string)
		if seen[id] {
			t.Fatalf("row %s twice", id)
		}
		seen[id] = true
		if i > 0 {
			prev, cur := all[i-1]["createdAt"].(string), row["createdAt"].(string)
			if cur > prev || (cur == prev && id > all[i-1]["id"].(string)) {
				t.Fatalf("rows %d and %d out of (created_at, id) DESC order", i-1, i)
			}
		}
		if strings.HasPrefix(row["tripNo"].(string), "C-") {
			t.Fatal("a trip of another tenant reached O's monitor")
		}
	}
	// The facets count the whole range, not the page.
	_, _, meta := r.ok("staffO", base)
	if f := meta["facets"].(map[string]any); f["total"].(float64) != 450 {
		t.Errorf("facets total %v, want 450", f["total"])
	}
	for _, c := range []struct{ query, want string }{
		{"&limit=201", "limit out_of_range"},
		{"&cursor=bm9wZQ", "cursor invalid"},
		{"&month=2026-09", "month exclusive"},
	} {
		status, m := r.get("staffO", base+c.query)
		if status != 422 || respCode(m) != c.want {
			t.Errorf("%s: %d %s, want 422 %s", c.query, status, respCode(m), c.want)
		}
	}
	for _, c := range []struct{ path, want string }{
		{"/v1/trips/monitor", "from required"},
		{"/v1/trips/monitor?from=2025-01-01&to=2026-12-31", "to range_too_long"},
		{"/v1/trips/monitor?from=2026-09-02&to=2026-09-01", "to before_from"},
	} {
		status, m := r.get("staffO", c.path)
		if status != 422 || respCode(m) != c.want {
			t.Errorf("%s: %d %s, want 422 %s", c.path, status, respCode(m), c.want)
		}
	}
	if status, m := r.get("staffO", base+"&sort=x"); status != 400 {
		t.Errorf("unknown parameter: %d %v", status, m)
	}
	// A month is the Bangkok month; the generic trips list pages its own axis the same way.
	data, _, _ := r.ok("staffO", "/v1/trips/monitor?month=2026-09&facets=false&limit=5")
	if len(rows(data)) != 5 {
		t.Errorf("month page: %d rows", len(rows(data)))
	}
	n, cursor := 0, ""
	for {
		path := "/v1/trips?axis=created&limit=500&from=2026-09-01&to=2026-10-31"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		data, next, _ := r.ok("staffO", path)
		n += len(rows(data))
		if next == "" {
			break
		}
		cursor = next
	}
	if n != 450 {
		t.Errorf("GET /v1/trips listed %d trips, want 450", n)
	}
}

// AC: unresolved places and orphan plates get their own buckets (ADR 0005, ADR 0006): every option of the facets
// filters to exactly the trips it counts, the unresolved raw values are separate options, and a multi-drop trip
// matches by any of its stops.
func TestMonitorBucketsAndFilters(t *testing.T) {
	r := newReadWorld(t)
	at := func(h int) time.Time { return time.Date(2026, 10, 1, h, 0, 0, 0, time.UTC) }
	s := func(v string) *string { return &v }
	resolved := r.trip(tripRow{tenant: r.O, no: "B-1", originRaw: s("ALANG-A"), originHub: &r.hubSrc, destinationRaw: s("SOCE"),
		destinationSoc: s("SOCE"), truck: &r.truckO, plate: s("1กข-1234"), class: s("6WH"), partner: s("SPX"), createdAt: at(1)})
	ocr := r.trip(tripRow{tenant: r.O, no: "B-2", originRaw: s("ALANG-A - วังทองหลาง"), destinationRaw: s("SOCE (บัวโรย)"),
		plate: s("ZZ-999"), createdAt: at(2)})
	junk := r.trip(tripRow{tenant: r.O, no: "B-3", originRaw: s("สถานี ???"), destinationRaw: s("ประเวศ18"), plate: s(" ZZ-999 "),
		createdAt: at(3)})
	bare := r.trip(tripRow{tenant: r.O, no: "B-4", createdAt: at(4)})
	multi := r.trip(tripRow{tenant: r.O, no: "B-5", originRaw: s("ALANG-A"), originHub: &r.hubSrc, destinationRaw: s("SOCE"),
		destinationHub: &r.hubDst, multi: true, createdAt: at(5)})
	r.exec(`INSERT INTO trip_delivery_stops (trip_id, stop_index, destination_raw, destination_hub_id)
		VALUES ($1, 1, 'SOCE', $2), ($1, 2, 'ร้าน ABC', NULL)`, multi, r.hubDst)
	inc := r.trip(tripRow{tenant: r.O, no: "B-6", status: "delivered", createdAt: at(6)})
	r.exec(`INSERT INTO incident_reports (tenant_id, tenant_source, trip_id, reported_by_kind, delay_cause) VALUES ($1, 'trip', $2, 'admin', 'x')`,
		r.O, inc)

	base := "/v1/trips/monitor?from=2026-10-01&to=2026-10-01"
	_, _, meta := r.ok("staffO", base)
	f := meta["facets"].(map[string]any)
	options := func(name string) map[string]float64 {
		out := map[string]float64{}
		for _, o := range rows(f[name]) {
			out[o["value"].(string)] = o["count"].(float64)
		}
		return out
	}
	wantOrigins := map[string]float64{"code:ALANG-A": 2, "raw:ALANG-A - วังทองหลาง": 1, "raw:สถานี ???": 1, "__none__": 2}
	if got := options("origins"); !mapsEqual(got, wantOrigins) {
		t.Errorf("origins %v, want %v", got, wantOrigins)
	}
	// SOCE three ways (the stored SOC key, a spelling, the SOC hub row) is one place; the multi-drop trip counts once
	// per place it served.
	wantDest := map[string]float64{"code:SOCE": 3, "raw:ประเวศ18": 1, "raw:ร้าน ABC": 1, "__none__": 2}
	if got := options("destinations"); !mapsEqual(got, wantDest) {
		t.Errorf("destinations %v, want %v", got, wantDest)
	}
	wantPlates := map[string]float64{"id:" + r.truckO.String(): 1, "plate:ZZ-999": 2, "__none__": 3}
	if got := options("plates"); !mapsEqual(got, wantPlates) {
		t.Errorf("plates %v, want %v", got, wantPlates)
	}
	for _, o := range rows(f["plates"]) {
		if (o["value"] == "plate:ZZ-999") != (o["isOrphan"] == true) {
			t.Errorf("plate option %v: an orphan plate is flagged, a truck is not", o)
		}
	}
	for _, o := range rows(f["origins"]) {
		if strings.HasPrefix(o["value"].(string), "raw:") != (o["isUnresolved"] == true) {
			t.Errorf("origin option %v", o)
		}
		if o["value"] == "code:ALANG-A" && o["label"] != "วังทองหลาง" {
			t.Errorf("a resolved place shows its Thai name: %v", o)
		}
	}
	if f["incident"].(float64) != 1 || f["total"].(float64) != 6 {
		t.Errorf("incident %v total %v", f["incident"], f["total"])
	}
	if got := options("partners"); !mapsEqual(got, map[string]float64{"SPX": 1, "__none__": 5}) {
		t.Errorf("partners %v", got)
	}
	if got := options("vehicleClasses"); !mapsEqual(got, map[string]float64{"6WH": 1, "__none__": 5}) {
		t.Errorf("vehicle classes %v", got)
	}

	// Each option filters to the trips it counted.
	filter := func(query string) []string {
		data, _, _ := r.ok("staffO", base+"&facets=false&"+query)
		got := ids(rows(data))
		slices.Sort(got)
		return got
	}
	want := func(list ...uuid.UUID) []string {
		out := make([]string, len(list))
		for i, id := range list {
			out[i] = id.String()
		}
		slices.Sort(out)
		return out
	}
	q := url.QueryEscape
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"origin=" + q("code:ALANG-A"), want(resolved, multi)},
		{"origin=" + q("raw:ALANG-A - วังทองหลาง"), want(ocr)},
		{"origin=" + q("raw:สถานี ???"), want(junk)},
		{"origin=__none__", want(bare, inc)},
		{"destination=" + q("code:SOCE"), want(resolved, ocr, multi)},
		{"destination=" + q("raw:ร้าน ABC"), want(multi)},
		{"destination=" + q("raw:ประเวศ18"), want(junk)},
		{"destination=__none__", want(bare, inc)},
		{"plate=" + q("id:"+r.truckO.String()), want(resolved)},
		{"plate=" + q("plate:ZZ-999"), want(ocr, junk)},
		{"plate=__none__", want(bare, multi, inc)},
		{"vehicleClass=6WH", want(resolved)},
		{"vehicleClass=__none__", want(ocr, junk, bare, multi, inc)},
		{"partner=SPX", want(resolved)},
		{"partner=__none__", want(ocr, junk, bare, multi, inc)},
		{"status=incident", want(inc)},
		{"status=delivered", want(inc)},
		{"q=B-3", want(junk)},
		{"origin=" + q("code:ALANG-A") + "&destination=" + q("raw:ร้าน ABC"), want(multi)},
		{"origin=" + q("code:NOPE"), want()},
	} {
		if got := filter(c.query); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.query, got, c.want)
		}
	}
	for _, c := range []struct{ query, want string }{
		{"origin=SOCE", "origin invalid"},
		{"plate=70-1234", "plate invalid"},
		{"vehicleClass=TRUCK", "vehicleClass invalid"},
		{"status=lost", "status invalid"},
	} {
		status, m := r.get("staffO", base+"&"+c.query)
		if status != 422 || respCode(m) != c.want {
			t.Errorf("%s: %d %s, want 422 %s", c.query, status, respCode(m), c.want)
		}
	}
	// The row carries the keys it is filtered by.
	data, _, _ := r.ok("staffO", base+"&facets=false&q=B-5")
	row := rows(data)[0]
	if row["origin"].(map[string]any)["key"] != "code:ALANG-A" || len(rows(row["stops"])) != 2 ||
		rows(row["stops"])[1]["place"].(map[string]any)["key"] != "raw:ร้าน ABC" ||
		row["truck"].(map[string]any)["key"] != "__none__" {
		t.Errorf("row %v", row)
	}
}

func mapsEqual(a, b map[string]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// AC: a dispatcher's payloads carry no billing, party, review, evidence or HR field, on every read route, although the
// dispatcher's role holds accounting:view_income; staff with the capability see the price, staff without it do not.
func TestDispatcherPayloadHasNoBillingOrHRFields(t *testing.T) {
	r := newReadWorld(t)
	// scope_drivers shows a driver through work of the last 180 days (app_recent_work_in_scope): dates relative to now.
	planAt := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
	rng := "from=" + clock.DateString(planAt.Add(-24*time.Hour)) + "&to=" + clock.DateString(planAt.Add(48*time.Hour))
	taskX := r.task(r.O, operations.FirstMile, &r.dO1, &r.partyX, planAt)
	tripX := r.trip(tripRow{tenant: r.O, no: "X-1", status: "delivered", task: &taskX, driver: &r.dO1, party: &r.partyX,
		createdAt: planAt.Add(time.Hour)})
	r.exec(`UPDATE trip_records SET review_status = 'pending_review', needs_admin_review = true, evidence_token = 'ev-x',
		ocr_data = '{"partnerCode":"P"}' WHERE id = $1`, tripX)
	r.exec(`INSERT INTO trip_billing_snapshots (trip_id, tenant_id, estimate_thb, computed_by) VALUES ($1, $2, 1234.5, 'etl')`, tripX, r.O)
	r.exec(`INSERT INTO trip_photos (trip_id, photo_type, file_id) VALUES ($1, 'seal', $2)`, tripX, r.files[0])
	r.exec(`INSERT INTO incident_reports (tenant_id, tenant_source, trip_id, driver_id, reported_by_kind, map_photo_file_id)
		VALUES ($1, 'trip', $2, $3, 'driver', $4)`, r.O, tripX, r.dO1, r.files[1])
	r.exec(`INSERT INTO standby_records (tenant_id, tenant_source, task_id, driver_id, customer_party_id, customer_resolved,
		billing_estimate_thb, billing_party_id, started_at, ended_at)
		VALUES ($1, 'task', $2, $3, $4, true, 900, $4, $5, $6)`, r.O, taskX, r.dO1, r.partyX, planAt, planAt.Add(2*time.Hour))
	// An unrelated task and trip of customer Y: outside the dispatcher's grant.
	taskY := r.task(r.O, operations.FirstMile, &r.dO2, &r.partyY, planAt)
	r.trip(tripRow{tenant: r.O, no: "Y-1", task: &taskY, driver: &r.dO2, party: &r.partyY, createdAt: planAt.Add(time.Hour)})

	routes := []string{
		"/v1/trips/monitor?" + rng,
		"/v1/trips?" + rng,
		"/v1/trips/" + tripX.String(),
		"/v1/trips/by-no/X-1",
		"/v1/trips/" + tripX.String() + "/photos",
		"/v1/tasks?" + rng,
		"/v1/tasks/" + taskX.String(),
		"/v1/tasks/" + taskX.String() + "/trip",
		"/v1/standby?" + rng,
		"/v1/incidents?" + rng,
	}
	forbidden := []string{"billing", "price", "billingPartyId", "customerPartyId", "sourceLinkedPartyId",
		"destinationLinkedPartyId", "reviewStatus", "needsAdminReview", "ocrData", "evidenceToken", "tenantSource",
		"estimateThb", "renames", "legacyTripRef", "reportedByUserId"}
	for _, path := range routes {
		data, _, meta := r.ok("dispatcher", path)
		got := map[string]bool{}
		keys(data, got)
		keys(meta, got)
		for k := range got {
			if slices.Contains(forbidden, k) || scope.IsHidden(camelToSnake(k)) {
				t.Errorf("%s: the dispatcher's payload carries %q", path, k)
			}
		}
		if strings.Contains(fmt.Sprint(data), "Y-1") {
			t.Errorf("%s: the dispatcher reads customer Y's trip", path)
		}
	}
	// The same routes for the own fleet's admin carry the staff fields and the price.
	data, _, _ := r.ok("staffO", "/v1/trips/monitor?"+rng+"&q=X-1&facets=false")
	if p := rows(data)[0]["price"]; p == nil || p.(map[string]any)["estimateThb"] != 1234.5 {
		t.Errorf("staff with accounting:view_income: price %v", p)
	}
	detail, _, _ := r.ok("staffO", "/v1/trips/"+tripX.String())
	d := detail.(map[string]any)
	if d["billing"] == nil || d["reviewStatus"] != "pending_review" || len(rows(d["photos"])) != 1 ||
		rows(d["photos"])[0]["url"] == nil {
		t.Errorf("staff trip detail %v", d)
	}
	// Staff without accounting:view_income: no price, no billing block.
	data, _, _ = r.ok("operator", "/v1/trips/monitor?"+rng+"&q=X-1&facets=false")
	if _, has := rows(data)[0]["price"]; has {
		t.Error("an operator reads the price")
	}
	detail, _, _ = r.ok("operator", "/v1/trips/"+tripX.String())
	if _, has := detail.(map[string]any)["billing"]; has {
		t.Error("an operator reads the billing block")
	}
	// The dispatcher's projection still names the driver and signs the photos.
	detail, _, _ = r.ok("dispatcher", "/v1/trips/"+tripX.String())
	d = detail.(map[string]any)
	if d["driverName"] != "O1 T" || len(rows(d["photos"])) != 1 || rows(d["photos"])[0]["url"] == nil {
		t.Errorf("dispatcher trip detail %v", d)
	}
	// Billing filters are not available to a projection.
	for _, path := range []string{"/v1/trips?axis=billing", "/v1/trips?customerId=" + r.partyX.String(),
		"/v1/standby?customerId=" + r.partyX.String(),
		"/v1/trips/monitor?" + rng + "&customerId=" + r.partyX.String()} {
		status, m := r.get("dispatcher", path)
		if status != 422 || !strings.HasSuffix(respCode(m), "not_available") {
			t.Errorf("%s: %d %s", path, status, respCode(m))
		}
	}
}

// AC: a customer-scope user whose parties have no trip sees no incident (fixes incident-reports/page.tsx:185, where
// zero trips showed every incident); one with trips sees the incidents of those trips only.
func TestCustomerScopeIncidents(t *testing.T) {
	r := newReadWorld(t)
	at := time.Date(2026, 10, 3, 3, 0, 0, 0, time.UTC)
	tripX := r.trip(tripRow{tenant: r.O, no: "CX-1", party: &r.partyX, createdAt: at})
	other := r.trip(tripRow{tenant: r.O, no: "CN-1", createdAt: at})
	for _, trip := range []uuid.UUID{tripX, other} {
		r.exec(`INSERT INTO incident_reports (tenant_id, tenant_source, trip_id, reported_by_kind) VALUES ($1, 'trip', $2, 'driver')`,
			r.O, trip)
	}
	r.exec(`INSERT INTO incident_reports (tenant_id, tenant_source, reported_by_kind) VALUES ($1, 'driver', 'driver')`, r.O)

	data, next, _ := r.ok("customerY", "/v1/incidents")
	if len(rows(data)) != 0 || next != "" {
		t.Fatalf("a customer with no trip reads %d incidents", len(rows(data)))
	}
	data, _, _ = r.ok("customerX", "/v1/incidents")
	got := rows(data)
	if len(got) != 1 || got[0]["tripId"] != tripX.String() || got[0]["tripNo"] != "CX-1" {
		t.Fatalf("customer X reads %v", got)
	}
	// Staff read all three; the list has no cap and pages.
	all := 0
	cursor := ""
	for {
		path := "/v1/incidents?limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		data, next, _ := r.ok("staffO", path)
		all += len(rows(data))
		if next == "" {
			break
		}
		cursor = next
	}
	if all != 3 {
		t.Errorf("staff page through %d incidents, want 3", all)
	}
	// The customer reads its own trips on the monitor and nothing of the others.
	data, _, meta := r.ok("customerX", "/v1/trips/monitor?from=2026-10-01&to=2026-10-31")
	if ids := ids(rows(data)); !slices.Equal(ids, []string{tripX.String()}) {
		t.Errorf("customer X monitor %v", ids)
	}
	if meta["facets"].(map[string]any)["total"].(float64) != 1 {
		t.Errorf("customer X facets %v", meta["facets"])
	}
	data, _, _ = r.ok("customerY", "/v1/trips/monitor?from=2026-10-01&to=2026-10-31")
	if len(rows(data)) != 0 {
		t.Errorf("customer Y monitor %v", rows(data))
	}
	if status, _ := r.get("customerY", "/v1/trips/"+tripX.String()); status != 404 {
		t.Errorf("customer Y reads customer X's trip: %d", status)
	}
}

// The task boards: staff and scope pages, the plan axis, the task-type capability, driver:self (R33), by number, the
// newest trip.
func TestTaskBoards(t *testing.T) {
	r := newReadWorld(t)
	day := time.Date(2026, 10, 5, 2, 0, 0, 0, time.UTC) // 09:00 Bangkok
	fm := r.task(r.O, operations.FirstMile, &r.dO1, &r.partyX, day)
	lh := r.task(r.O, operations.LineHaul, &r.dO2, &r.partyX, day)
	carrier := r.task(r.A, operations.FirstMile, &r.dA1, &r.partyY, day)
	cTask := r.task(r.C, operations.FirstMile, nil, &r.partyX, day) // another carrier's: outside O's reach, inside X's grant
	r.exec(`INSERT INTO task_delivery_stops (task_id, stop_index, destination_raw, destination_hub_id, destination_linked_party_id)
		VALUES ($1, 1, 'SOCE', $2, $3), ($1, 2, 'ร้าน ABC', NULL, $3)`, fm, r.hubDst, r.partyX)
	r.exec(`UPDATE tasks SET is_multi_delivery = true WHERE id = $1`, fm)
	tripFM := r.trip(tripRow{tenant: r.O, no: "FM-T", task: &fm, driver: &r.dO1, createdAt: day.Add(time.Hour)})

	data, _, _ := r.ok("staffO", "/v1/tasks?date=2026-10-05")
	got := rows(data)
	if ids := ids(got); len(ids) != 3 || !slices.Contains(ids, carrier.String()) {
		t.Fatalf("O's board %v (own two and the carrier's)", ids)
	}
	for _, x := range got {
		if x["id"] == fm.String() {
			if len(rows(x["stops"])) != 2 || x["trip"].(map[string]any)["tripNo"] != "FM-T" || x["driverName"] != "O1 T" ||
				x["source"].(map[string]any)["label"] != "วังทองหลาง" || x["billingPartyId"] != r.partyX.String() {
				t.Errorf("first-mile row %v", x)
			}
		}
	}
	// A principal reading first mile only lists first mile, and is refused line haul.
	data, _, _ = r.ok("fmOnly", "/v1/tasks?date=2026-10-05")
	for _, x := range rows(data) {
		if x["taskType"] != operations.FirstMile {
			t.Errorf("fmOnly lists %v", x["taskType"])
		}
	}
	if status, m := r.get("fmOnly", "/v1/tasks?taskType=line_haul"); status != 403 ||
		respCode(m) != "permission_denied [operations:view_line_haul]" {
		t.Errorf("fmOnly line haul: %d %s", status, respCode(m))
	}
	if status, _ := r.get("fmOnly", "/v1/tasks/"+lh.String()); status != 403 {
		t.Errorf("fmOnly reads a line-haul task: %d", status)
	}
	// driver:self: the own task (no party links), never another driver's.
	detail, _, _ := r.ok("driverO1", "/v1/tasks/"+fm.String())
	if dm := detail.(map[string]any); dm["id"] != fm.String() || dm["billingPartyId"] != nil {
		t.Errorf("driver task %v", dm)
	} else if _, has := dm["billingPartyId"]; has {
		t.Error("a driver reads the task's billing party")
	}
	if status, _ := r.get("driverO1", "/v1/tasks/"+lh.String()); status != 404 {
		t.Errorf("a driver reads another driver's task: %d", status)
	}
	if status, _ := r.get("driverO1", "/v1/tasks?date=2026-10-05"); status != 403 {
		t.Errorf("a driver lists the board: %d", status)
	}
	// By number, newest trip.
	no := r.sampleText(`SELECT task_no FROM tasks WHERE id = $1`, fm)
	data, _, _ = r.ok("staffO", "/v1/tasks/by-number/"+url.PathEscape(no))
	if ids := ids(rows(data)); !slices.Equal(ids, []string{fm.String()}) {
		t.Errorf("by number %v", ids)
	}
	trip, _, _ := r.ok("staffO", "/v1/tasks/"+fm.String()+"/trip")
	if trip.(map[string]any)["id"] != tripFM.String() {
		t.Errorf("task trip %v", trip)
	}
	if status, _ := r.get("staffO", "/v1/tasks/"+lh.String()+"/trip"); status != 404 {
		t.Errorf("a task without a trip: %d", status)
	}
	// The dispatcher's board: the tasks of its grant (customer X) in every tenant, the projection's stops (no linked
	// party, no party field).
	data, _, _ = r.ok("dispatcher", "/v1/tasks?date=2026-10-05")
	for _, x := range rows(data) {
		if _, has := x["billingPartyId"]; has || len(rows(x["stops"])) > 0 && rows(x["stops"])[0]["destinationLinkedPartyId"] != nil {
			t.Errorf("dispatcher row %v", x)
		}
	}
	dispIDs := ids(rows(data))
	slices.Sort(dispIDs)
	wantIDs := []string{fm.String(), lh.String(), cTask.String()}
	slices.Sort(wantIDs)
	if !slices.Equal(dispIDs, wantIDs) {
		t.Errorf("dispatcher board %v, want %v", dispIDs, wantIDs)
	}
	// Paging on both axes.
	r.exec(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw, created_at)
		SELECT $1, 'form', 'first_mile', 'pending', timestamptz '2026-10-06 02:00Z' + (i % 3) * interval '1 day', 'A', 'B',
		       timestamptz '2026-10-01 00:00Z' + i * interval '1 minute'
		FROM generate_series(1, 120) i`, r.O)
	for _, path := range []string{"/v1/tasks?from=2026-10-06&to=2026-10-08&limit=50", "/v1/tasks?limit=50&taskType=first_mile"} {
		n, cursor := 0, ""
		for {
			p := path
			if cursor != "" {
				p += "&cursor=" + url.QueryEscape(cursor)
			}
			data, next, _ := r.ok("staffO", p)
			n += len(rows(data))
			if next == "" {
				break
			}
			cursor = next
		}
		// The plan range holds the 120 new tasks; the created axis every first-mile task of the reach (O's fm, A's and
		// the 120).
		if want := map[bool]int{true: 120, false: 122}[strings.Contains(path, "from=")]; n != want {
			t.Errorf("%s: %d tasks, want %d", path, n, want)
		}
	}
	if status, m := r.get("staffO", "/v1/tasks?date=2026-10-05&from=2026-10-05"); status != 422 || respCode(m) != "date exclusive" {
		t.Errorf("date and from: %d %s", status, respCode(m))
	}
}

// The standby list: projection of the billing carrier, signed photos, the ended axis, the scope projection.
func TestStandbyList(t *testing.T) {
	r := newReadWorld(t)
	at := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	taskX := r.task(r.O, operations.FirstMile, &r.dO1, &r.partyX, at)
	done := r.id(`INSERT INTO standby_records (tenant_id, tenant_source, task_id, driver_id, customer_party_id, customer_resolved,
		billing_estimate_thb, billing_party_id, started_at, ended_at, created_at)
		VALUES ($1, 'task', $2, $3, $4, true, 900, $4, $5, $6, $5) RETURNING id`,
		r.O, taskX, r.dO1, r.partyX, at, at.Add(3*time.Hour))
	// A record without ended_at (no_ended_at) lists on the created axis only.
	open := r.id(`INSERT INTO standby_records (tenant_id, tenant_source, task_id, driver_id, started_at, created_at)
		VALUES ($1, 'task', $2, $3, $4, $4) RETURNING id`, r.O, taskX, r.dO1, at.Add(time.Hour))
	r.exec(`INSERT INTO standby_photos (standby_id, photo_type, file_id) VALUES ($1, 'site_photo', $2)`, done, r.files[2])

	data, _, _ := r.ok("staffO", "/v1/standby")
	got := rows(data)
	if ids := ids(got); !slices.Equal(ids, []string{open.String(), done.String()}) {
		t.Fatalf("standby created axis %v", ids)
	}
	for _, x := range got {
		if x["id"] == done.String() {
			if x["billing"] == nil || len(rows(x["photos"])) != 1 || rows(x["photos"])[0]["url"] == nil || x["driverName"] != "O1 T" {
				t.Errorf("staff standby row %v", x)
			}
		}
	}
	data, _, _ = r.ok("staffO", "/v1/standby?sort=ended")
	if ids := ids(rows(data)); !slices.Equal(ids, []string{done.String()}) {
		t.Errorf("ended axis %v", ids)
	}
	data, _, _ = r.ok("operator", "/v1/standby")
	for _, x := range rows(data) {
		if _, has := x["billing"]; has {
			t.Error("an operator reads the standby billing block")
		}
	}
	data, _, _ = r.ok("customerX", "/v1/standby")
	if n := len(rows(data)); n != 2 {
		t.Errorf("customer X standby %d", n)
	}
}

// The route guards: no capability, an unknown id, an undocumented parameter.
func TestReadRouteGuards(t *testing.T) {
	r := newReadWorld(t)
	r.principals["nobody"] = &authz.Principal{UserID: uuid.New(), TenantID: &r.O, TenantRole: authz.User, TenantKind: "own_fleet"}
	for _, path := range []string{"/v1/tasks", "/v1/trips", "/v1/trips/monitor?from=2026-10-01&to=2026-10-02", "/v1/standby",
		"/v1/incidents", "/v1/trips/" + uuid.NewString(), "/v1/tasks/" + uuid.NewString()} {
		if status, m := r.get("nobody", path); status != 403 || respCode(m) == "" {
			t.Errorf("%s without a capability: %d %v", path, status, m)
		}
	}
	for _, path := range []string{"/v1/trips/" + uuid.NewString(), "/v1/trips/not-a-uuid", "/v1/tasks/" + uuid.NewString(),
		"/v1/trips/by-no/NOPE", "/v1/trips/" + uuid.NewString() + "/photos"} {
		if status, _ := r.get("staffO", path); status != 404 {
			t.Errorf("%s: %d, want 404", path, status)
		}
	}
	if status, _ := r.get("staffO", "/v1/incidents?page=2"); status != 400 {
		t.Errorf("undocumented parameter: %d", status)
	}
}
