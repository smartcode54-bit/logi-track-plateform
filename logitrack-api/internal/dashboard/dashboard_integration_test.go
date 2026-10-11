//go:build integration

package dashboard_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/locations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cartrack"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// AC: the dashboard summary is one request (it replaces ~10.7k document reads, DashboardStats.tsx:175-178): every block
// the caller's capabilities allow, each figure kept to the caller's reach by RLS.
func TestSummaryIsOneRequestPerCapabilities(t *testing.T) {
	w := newWorld(t, worldOpts{})
	r := w.get("/v1/dashboard/summary", w.login("ta.o@example.test"))
	expect(t, r, http.StatusOK, "")
	d := r.data()
	if p := obj(d["period"]); p["from"] != "2026-10-05" || p["to"] != "2026-10-11" || num(p["days"]) != 7 {
		t.Fatalf("period %v", p)
	}
	// Own fleet O reads O and its carrier A (contractor reach), never C.
	trips := obj(d["trips"])
	if num(trips["delivered"]) != 4 || num(trips["parcels"]) != 38 || num(trips["withIncident"]) != 1 ||
		num(trips["onTimePct"]) != 75 || num(trips["delayPct"]) != 25 {
		t.Fatalf("trips %v", trips)
	}
	act := obj(d["activity"])
	days, _ := act["days"].([]any)
	if len(days) != 7 || num(act["total"]) != 4 || num(act["previousTotal"]) != 1 || num(act["trendPct"]) != 300 {
		t.Fatalf("activity %v", act)
	}
	if day := obj(days[5]); day["date"] != "2026-10-10" || num(day["delivered"]) != 2 {
		t.Fatalf("10-10 %v", day)
	}
	if inc := obj(d["incidents"]); num(inc["inPeriod"]) != 1 {
		t.Fatalf("incidents %v", inc)
	}
	if dr := obj(d["drivers"]); num(dr["active"]) != 2 || num(dr["onDuty"]) != 1 || num(dr["inactive"]) != 1 || num(dr["total"]) != 4 {
		t.Fatalf("drivers %v", dr)
	}
	if ex := obj(d["expenses"]); num(ex["pendingFuel"]) != 2 || num(ex["pendingOther"]) != 1 {
		t.Fatalf("expenses %v", ex)
	}
	if ch := obj(d["chats"]); num(ch["unassigned"]) != 2 || num(ch["urgent"]) != 1 {
		t.Fatalf("chats %v", ch)
	}
	comp := obj(d["compliance"])
	items, _ := comp["items"].([]any)
	if num(comp["taxDue"]) != 1 || num(comp["serviceKmDue"]) != 1 || num(comp["insuranceDue"]) != 0 || num(comp["itemsTotal"]) != 2 ||
		len(items) != 2 || obj(items[0])["kind"] != "tax" || num(obj(items[0])["daysRemaining"]) != 5 ||
		num(obj(items[1])["kmRemaining"]) != 500 {
		t.Fatalf("compliance %v", comp)
	}
	for _, it := range items {
		if obj(it)["truckId"] != w.truckO.String() {
			t.Fatalf("compliance lists another tenant's truck: %v", it)
		}
	}
	recent, _ := d["recent"].([]any)
	if len(recent) != 8 {
		t.Fatalf("recent %d items", len(recent))
	}
	types := map[string]bool{}
	var last time.Time
	for i, it := range recent {
		m := obj(it)
		types[m["type"].(string)] = true
		at, err := time.Parse(time.RFC3339Nano, m["at"].(string))
		if err != nil || (i > 0 && at.After(last)) {
			t.Fatalf("recent not newest first at %d: %v", i, recent)
		}
		last = at
	}
	for _, want := range []string{"delivered", "driver", "incident", "maintenance"} {
		if !types[want] {
			t.Fatalf("recent lacks %s: %v", want, recent)
		}
	}

	// An operator holds no analytics or expense key; a user role only trucks and drivers.
	op := w.get("/v1/dashboard/summary", w.login("op.o@example.test")).data()
	for k, want := range map[string]bool{"trips": true, "incidents": true, "drivers": true, "chats": true, "compliance": true,
		"activity": false, "expenses": false} {
		if _, ok := op[k]; ok != want {
			t.Fatalf("operator: block %s present %v", k, ok)
		}
	}
	us := w.get("/v1/dashboard/summary", w.login("us.o@example.test")).data()
	for k, want := range map[string]bool{"drivers": true, "compliance": true, "trips": false, "activity": false, "incidents": false,
		"expenses": false, "chats": false} {
		if _, ok := us[k]; ok != want {
			t.Fatalf("user: block %s present %v", k, ok)
		}
	}
	for _, it := range us["recent"].([]any) {
		if obj(it)["type"] != "driver" {
			t.Fatalf("user sees a %v update", obj(it)["type"])
		}
	}
	// An independent carrier reads its own rows only; the carrier working for O its own, and its own due truck.
	c := w.get("/v1/dashboard/summary", w.login("ta.c@example.test")).data()
	if tr := obj(c["trips"]); num(tr["delivered"]) != 2 || num(tr["parcels"]) != 200 || num(tr["onTimePct"]) != 50 {
		t.Fatalf("C trips %v", tr)
	}
	if ex := obj(c["expenses"]); num(ex["pendingFuel"]) != 0 || num(ex["pendingOther"]) != 1 {
		t.Fatalf("C expenses %v", ex)
	}
	if comp := obj(c["compliance"]); num(comp["itemsTotal"]) != 0 {
		t.Fatalf("C compliance %v", comp)
	}
	a := w.get("/v1/dashboard/summary", w.login("ta.a@example.test")).data()
	if tr := obj(a["trips"]); num(tr["delivered"]) != 1 {
		t.Fatalf("A trips %v", tr)
	}
	if comp := obj(a["compliance"]); num(comp["taxDue"]) != 1 || obj(comp["items"].([]any)[0])["truckId"] != w.truckA.String() {
		t.Fatalf("A compliance %v", comp)
	}

	// An explicit period, and the refusals.
	r = w.get("/v1/dashboard/summary?from=2026-10-01&to=2026-10-03", w.login("ta.o@example.test"))
	if tr := obj(r.data()["trips"]); num(tr["delivered"]) != 1 || tr["onTimePct"].(float64) != 100 {
		t.Fatalf("explicit period %v", tr)
	}
	expect(t, w.get("/v1/dashboard/summary?from=2026-10-12&to=2026-10-01", w.login("ta.o@example.test")), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/dashboard/summary?period=week", w.login("ta.o@example.test")), http.StatusBadRequest, "bad_request")
	expect(t, w.get("/v1/dashboard/summary", ""), http.StatusUnauthorized, "")
	expect(t, w.call(w.public, http.MethodGet, "/v1/dashboard/summary", w.login("ta.o@example.test"), nil), http.StatusNotFound, "not_found")
}

// A customer-scope principal gets the operations blocks over the scope_* projections only: the trips of its party.
func TestSummaryOfAScopePrincipal(t *testing.T) {
	w := newWorld(t, worldOpts{})
	d := w.get("/v1/dashboard/summary", w.login("cs@example.test")).data()
	if tr := obj(d["trips"]); num(tr["delivered"]) != 1 || num(tr["parcels"]) != 10 || num(tr["withIncident"]) != 0 {
		t.Fatalf("scope trips %v", tr)
	}
	if inc := obj(d["incidents"]); num(inc["inPeriod"]) != 0 {
		t.Fatalf("scope incidents %v", inc)
	}
	for _, k := range []string{"activity", "drivers", "expenses", "chats", "compliance"} {
		if _, ok := d[k]; ok {
			t.Fatalf("a scope principal got block %s", k)
		}
	}
	recent, _ := d["recent"].([]any)
	if len(recent) != 1 || obj(recent[0])["type"] != "delivered" {
		t.Fatalf("scope recent %v", recent)
	}
}

// GET /v1/badges: each counter for its key (waitlist: stewards only, R60).
func TestBadges(t *testing.T) {
	w := newWorld(t, worldOpts{})
	w.exec(`INSERT INTO waitlist (email, name) VALUES ('a@example.test', 'A'), ('b@example.test', 'B')`)
	r := w.get("/v1/badges", w.login("ta.o@example.test"))
	expect(t, r, http.StatusOK, "")
	if b := r.data(); num(b["waitlist"]) != 2 || num(b["pendingExpenses"]) != 3 || num(b["unassignedChats"]) != 2 || num(b["urgentChats"]) != 1 {
		t.Fatalf("own fleet %v", b)
	}
	if b := w.get("/v1/badges", w.login("ta.a@example.test")).data(); b["waitlist"] != nil || num(b["pendingExpenses"]) != 1 ||
		num(b["unassignedChats"]) != 0 {
		t.Fatalf("carrier A %v", b)
	}
	if b := w.get("/v1/badges", w.login("us.o@example.test")).data(); len(b) != 0 {
		t.Fatalf("user %v", b)
	}
	if b := w.get("/v1/badges", w.login("pa@example.test")).data(); num(b["waitlist"]) != 2 || len(b) != 1 {
		t.Fatalf("platform admin %v", b)
	}
	if b := w.get("/v1/badges", w.login("cs@example.test")).data(); len(b) != 0 {
		t.Fatalf("customer scope %v", b)
	}
}

// AC: the 6th waitlist post per IP within the window is rejected (429), and the form routes answer 404 on the public
// listener. The email bucket refuses a second post of one address; the honeypot stores nothing.
func TestWaitlistFormRateLimits(t *testing.T) {
	w := newWorld(t, worldOpts{rateLimit: true, formLimit: ratelimit.Limit{Count: 5, Window: time.Hour}})
	post := func(path, ip string, body map[string]any) resp {
		return w.call(w.internal, http.MethodPost, path, "", body, "X-Forwarded-For", ip)
	}
	waitlist := func(i int) map[string]any {
		return map[string]any{"email": "visitor" + string(rune('a'+i)) + "@example.test", "name": "Visitor", "phone": "081 234 5678"}
	}
	for i := range 5 {
		r := post("/v1/waitlist", "203.0.113.7", waitlist(i))
		expect(t, r, http.StatusCreated, "")
		if r.data()["received"] != true {
			t.Fatalf("answer %s", r.raw)
		}
	}
	sixth := post("/v1/waitlist", "203.0.113.7", waitlist(5))
	expect(t, sixth, http.StatusTooManyRequests, "resource_exhausted")
	if sixth.detail("bucket") != "public_form_ip" || sixth.header.Get("Retry-After") == "" {
		t.Fatalf("6th post %s", sixth.raw)
	}
	// The partner-interest form spends the same per-IP budget.
	expect(t, post("/v1/partner-interest", "203.0.113.7", map[string]any{"companyName": "X", "contactName": "Y", "email": "p@example.test"}),
		http.StatusTooManyRequests, "resource_exhausted")
	// Another visitor has its own budget; one address posts once a day.
	expect(t, post("/v1/waitlist", "198.51.100.9", waitlist(6)), http.StatusCreated, "")
	again := post("/v1/waitlist", "192.0.2.44", map[string]any{"email": "VisitorA@Example.test", "name": "Again"})
	expect(t, again, http.StatusTooManyRequests, "resource_exhausted")
	if again.detail("bucket") != "public_form_email" {
		t.Fatalf("email bucket %s", again.raw)
	}
	if n := w.count(`SELECT count(*) FROM waitlist`); n != 6 {
		t.Fatalf("%d rows, want the 6 accepted posts", n)
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'waitlist.created' AND realtime_topics = ARRAY['platform:security']`); n != 6 {
		t.Fatalf("%d waitlist.created events", n)
	}
	// The public listener has no form route at all.
	for _, path := range []string{"/v1/waitlist", "/v1/partner-interest", "/public/v1/waitlist"} {
		expect(t, w.call(w.public, http.MethodPost, path, "", waitlist(7), "X-Forwarded-For", "192.0.2.99"), http.StatusNotFound, "not_found")
	}
}

func TestFormsStoreAndValidate(t *testing.T) {
	w := newWorld(t, worldOpts{})
	post := func(path string, body map[string]any) resp {
		return w.call(w.internal, http.MethodPost, path, "", body)
	}
	expect(t, post("/v1/waitlist", map[string]any{"email": "  person@example.test ", "name": " คนไทย ", "countryCode": "+65"}), http.StatusCreated, "")
	var cc, name string
	var phone *string
	if err := w.etl.QueryRow(context.Background(), `SELECT country_code, name, phone FROM waitlist WHERE email = 'PERSON@example.test'`).
		Scan(&cc, &name, &phone); err != nil || cc != "+65" || name != "คนไทย" || phone != nil {
		t.Fatalf("stored %q %q %v %v", cc, name, phone, err)
	}
	r := post("/v1/waitlist", map[string]any{"email": "not-an-email", "name": "", "countryCode": "66", "phone": "<script>"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	fields := map[string]bool{}
	for _, f := range r.detail("fields").([]any) {
		fields[obj(f)["field"].(string)] = true
	}
	if !fields["email"] || !fields["name"] || !fields["countryCode"] || !fields["phone"] {
		t.Fatalf("fields %v", fields)
	}
	expect(t, w.call(w.internal, http.MethodPost, "/v1/waitlist", "", "[1]"), http.StatusBadRequest, "bad_request")
	// The honeypot answers like a stored post and stores nothing.
	expect(t, post("/v1/waitlist", map[string]any{"email": "bot@example.test", "name": "Bot", "website": "http://spam.example"}), http.StatusCreated, "")
	if n := w.count(`SELECT count(*) FROM waitlist WHERE email = 'bot@example.test'`); n != 0 {
		t.Fatal("the honeypot post was stored")
	}
	expect(t, post("/v1/partner-interest", map[string]any{"companyName": "บริษัท ขนส่ง จำกัด", "contactName": "คุณเอ",
		"email": "fleet@example.test", "fleetSize": "10-20", "vehicleType": "6WH", "unknown": "dropped"}), http.StatusCreated, "")
	var payload map[string]any
	if err := w.etl.QueryRow(context.Background(), `SELECT payload FROM partner_interest`).Scan(&payload); err != nil ||
		payload["status"] != "new" || payload["companyName"] != "บริษัท ขนส่ง จำกัด" || payload["unknown"] != nil || payload["serviceArea"] != "" {
		t.Fatalf("partner payload %v %v", payload, err)
	}
	expect(t, post("/v1/partner-interest", map[string]any{"companyName": "X"}), http.StatusUnprocessableEntity, "invalid_argument")
}

// GET /v1/waitlist and DELETE /v1/waitlist/{id}: waitlist:view, a steward key (R60): own-fleet staff and the platform
// admin, never a carrier's admin.
func TestWaitlistReadAndDelete(t *testing.T) {
	w := newWorld(t, worldOpts{})
	for i, at := range []string{"2026-10-01T01:00:00Z", "2026-10-02T01:00:00Z", "2026-10-03T01:00:00Z"} {
		w.exec(`INSERT INTO waitlist (email, name, created_at) VALUES ($1, 'N', $2)`, "w"+string(rune('0'+i))+"@example.test", at)
	}
	ta := w.login("ta.o@example.test")
	r := w.get("/v1/waitlist?limit=2", ta)
	expect(t, r, http.StatusOK, "")
	page := r.list()
	if len(page) != 2 || obj(page[0])["email"] != "w2@example.test" || r.body["nextCursor"] == nil || num(obj(r.body["meta"])["total"]) != 3 {
		t.Fatalf("page 1 %s", r.raw)
	}
	r = w.get("/v1/waitlist?limit=2&cursor="+r.body["nextCursor"].(string), ta)
	if page := r.list(); len(page) != 1 || obj(page[0])["email"] != "w0@example.test" || r.body["nextCursor"] != nil {
		t.Fatalf("page 2 %s", r.raw)
	}
	expect(t, w.get("/v1/waitlist?cursor=garbage", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/waitlist?limit=501", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/waitlist", w.login("ta.a@example.test")), http.StatusForbidden, "permission_denied")
	expect(t, w.get("/v1/waitlist", w.login("op.o@example.test")), http.StatusForbidden, "permission_denied")
	if l := w.get("/v1/waitlist", w.login("pa@example.test")).list(); len(l) != 3 {
		t.Fatalf("platform admin reads %d", len(l))
	}
	id := obj(page[0])["id"].(string)
	expect(t, w.call(w.internal, http.MethodDelete, "/v1/waitlist/"+id, w.login("ta.a@example.test"), nil), http.StatusForbidden, "permission_denied")
	if r := w.call(w.internal, http.MethodDelete, "/v1/waitlist/"+id, ta, nil); r.status != http.StatusNoContent {
		t.Fatalf("delete %d %s", r.status, r.raw)
	}
	expect(t, w.call(w.internal, http.MethodDelete, "/v1/waitlist/"+id, ta, nil), http.StatusNotFound, "not_found")
	expect(t, w.call(w.internal, http.MethodDelete, "/v1/waitlist/nope", ta, nil), http.StatusNotFound, "not_found")
	if n := w.count(`SELECT count(*) FROM waitlist`); n != 2 {
		t.Fatalf("%d rows after the delete", n)
	}
}

// GET /v1/vehicle-locations: fleet:view_live_map | dispatch:view_operations; RLS keeps each tenant to its reach.
func TestVehicleLocations(t *testing.T) {
	w := newWorld(t, worldOpts{})
	w.location(w.truckO, w.O, "1001", 13.1)
	w.location(w.truckA, w.A, "2002", 13.2)
	w.location(w.truckC, w.C, "3003", 13.3)
	trucks := func(email, query string) []string {
		r := w.get("/v1/vehicle-locations"+query, w.login(email))
		expect(t, r, http.StatusOK, "")
		var out []string
		for _, it := range r.list() {
			out = append(out, obj(it)["truckId"].(string))
		}
		slices.Sort(out)
		return out
	}
	sorted := func(ids ...uuid.UUID) []string {
		var out []string
		for _, id := range ids {
			out = append(out, id.String())
		}
		slices.Sort(out)
		return out
	}
	for email, want := range map[string][]string{
		"ta.o@example.test": sorted(w.truckO, w.truckA), "op.o@example.test": sorted(w.truckO, w.truckA),
		"ta.a@example.test": sorted(w.truckA), "ta.c@example.test": sorted(w.truckC),
	} {
		if got := trucks(email, ""); !slices.Equal(got, want) {
			t.Fatalf("%s reads %v, want %v", email, got, want)
		}
	}
	if got := trucks("ta.o@example.test", "?truckId="+w.truckA.String()+","+w.truckC.String()); !slices.Equal(got, sorted(w.truckA)) {
		t.Fatalf("filtered %v", got)
	}
	if got := trucks("ta.o@example.test", "?truckId[]="+w.truckO.String()); !slices.Equal(got, sorted(w.truckO)) {
		t.Fatalf("bracket form %v", got)
	}
	r := w.get("/v1/vehicle-locations?truckId="+w.truckO.String(), w.login("ta.o@example.test"))
	if it := obj(r.list()[0]); num(it["lat"]) != 13.1 || it["gpsVehicleId"] != "1001" || it["lastGpsAt"] == nil || it["tenantId"] != w.O.String() {
		t.Fatalf("row %v", it)
	}
	expect(t, w.get("/v1/vehicle-locations?truckId=x", w.login("ta.o@example.test")), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/vehicle-locations", w.login("us.o@example.test")), http.StatusForbidden, "permission_denied")
	expect(t, w.get("/v1/vehicle-locations", w.login("cs@example.test")), http.StatusForbidden, "permission_denied")
	expect(t, w.call(w.public, http.MethodGet, "/v1/vehicle-locations", w.login("ta.o@example.test"), nil), http.StatusNotFound, "not_found")
}

// fakeCartrack answers like Cartrack would.
type fakeCartrack struct{ st cartrack.Status }

func (f fakeCartrack) VehicleStatus(context.Context) (cartrack.Status, error) { return f.st, nil }

// worker plays the worker's cartrack.sync consumer: it takes each job.cartrack.sync command from the outbox (as the relay
// would publish it) and runs the consumer once.
func (w *world) worker(ctx context.Context, client cartrack.Client) {
	consumer, err := locations.NewSyncJob(locations.SyncDeps{Pool: w.pool, Cartrack: client, Locks: jobs.NewRedisLocker(w.rdb, w.ks), Log: zerolog.Nop()})
	if err != nil {
		w.t.Error(err)
		return
	}
	var last int64
	for ctx.Err() == nil {
		var id int64
		var body []byte
		err := w.pool.QueryRow(ctx, `SELECT id, payload::text::bytea FROM outbox_events WHERE routing_key = 'job.cartrack.sync' AND id > $1
			ORDER BY id LIMIT 1`, last).Scan(&id, &body)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		last = id
		if err := consumer.Handle(ctx, &mq.Delivery{Queue: locations.QueueCartrack, RoutingKey: locations.RouteCartrackJob, Body: body}); err != nil {
			w.t.Error(err)
		}
	}
}

// GET /v1/admin/cartrack/probe (platform): the worker runs a probe of cartrack.sync (the api holds no Cartrack
// credential) and the route answers the HTTP status and vehicle count; a probe still running is a 202 with its job,
// and a second probe meanwhile is 409.
func TestCartrackProbe(t *testing.T) {
	w := newWorld(t, worldOpts{probeWait: 5 * time.Second})
	pa := w.login("pa@example.test")
	expect(t, w.get("/v1/admin/cartrack/probe", w.login("ta.o@example.test")), http.StatusForbidden, "permission_denied")
	expect(t, w.call(w.public, http.MethodGet, "/v1/admin/cartrack/probe", pa, nil), http.StatusNotFound, "not_found")

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() {
		w.worker(ctx, fakeCartrack{st: cartrack.Status{HTTPStatus: 200, Vehicles: []cartrack.Vehicle{{VehicleID: "1"}, {VehicleID: "2"}, {VehicleID: "3"}}}})
	})
	r := w.get("/v1/admin/cartrack/probe", pa)
	cancel()
	wg.Wait()
	expect(t, r, http.StatusOK, "")
	if d := r.data(); d["status"] != "succeeded" || num(d["httpStatus"]) != 200 || num(d["vehicleCount"]) != 3 || d["jobId"] == nil {
		t.Fatalf("probe %s", r.raw)
	}
	if strings.Contains(string(r.raw), "vehicleId") {
		t.Fatal("the probe answers counts only")
	}
	if n := w.count(`SELECT count(*) FROM vehicle_locations`); n != 0 {
		t.Fatal("a probe wrote positions")
	}

	// Without a worker the probe is still queued when the wait ends: 202 with its job id, readable by the platform.
	short := newWorld(t, worldOpts{probeWait: 300 * time.Millisecond})
	spa := short.login("pa@example.test")
	r = short.get("/v1/admin/cartrack/probe", spa)
	expect(t, r, http.StatusAccepted, "")
	jobID, _ := r.data()["jobId"].(string)
	if r.data()["status"] != "queued" || jobID == "" {
		t.Fatalf("pending probe %s", r.raw)
	}
	held := short.get("/v1/admin/cartrack/probe", spa)
	expect(t, held, http.StatusConflict, "already_exists")
	if held.detail("jobId") != jobID {
		t.Fatalf("held lock %s", held.raw)
	}
	if j := short.get("/v1/jobs/"+jobID, spa); j.status != http.StatusOK || j.data()["type"] != "cartrack.sync" {
		t.Fatalf("job %d %s", j.status, j.raw)
	}
}
