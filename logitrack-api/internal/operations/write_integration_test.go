//go:build integration

package operations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The write routes of T31 (issue #51; Appendix B §B.2.11-§B.2.13) against PostgreSQL 18 as logitrack_app, behind
// the route guards, with the real T28 mover and the real trip renamer (iam). The principals carry the capability sets
// RBAC resolves for their role. PostgreSQL-owned path unless a test builds the write-back.

// writeHubs is the hub master with each hub's linked party (the write's hub-derived customer link); the fixture
// connection is shared, so concurrent requests take turns.
type writeHubs struct {
	w  *world
	mu *sync.Mutex
}

func (h writeHubs) Hubs(ctx context.Context, _ *authz.Principal) ([]operations.Hub, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rows, err := h.w.etl.Query(ctx, `SELECT id, source_id::text, name_th, name_en, linked_party_id FROM hubs ORDER BY source_id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (operations.Hub, error) {
		var x operations.Hub
		err := r.Scan(&x.ID, &x.Code, &x.NameTh, &x.NameEn, &x.LinkedPartyID)
		return x, err
	})
}

// fakeCommits commits the fixture's files by key ("file:<n>" = w.files[n]); any other key is not found.
type fakeCommits struct{ w *world }

func (f fakeCommits) Commit(_ context.Context, _ pgx.Tx, in storage.CommitInput) (storage.Committed, error) {
	var n int
	if _, err := fmt.Sscanf(in.Key, "file:%d", &n); err != nil || n < 0 || n >= len(f.w.files) {
		return storage.Committed{}, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: in.Field, Reason: "not_found"})
	}
	return storage.Committed{ID: f.w.files[n], Key: in.Key}, nil
}

type writeWorld struct {
	*world
	svc *operations.Service
	app *fiber.App
	ps  map[string]*authz.Principal
	D   uuid.UUID // the dispatcher's organisation (a carrier)
}

type writeOptions struct {
	pgOwned   bool
	writeBack *operations.WriteBack
}

func newWriteWorld(t *testing.T, opt writeOptions) *writeWorld {
	t.Helper()
	w := newWorld(t)
	r := &writeWorld{world: w}
	mover, err := iam.NewTaskMover(iam.TaskMoverDeps{Pool: w.pool, Log: zerolog.Nop(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	renamer, err := iam.NewTripRenamer(iam.TripRenamerDeps{Pool: w.pool, Log: zerolog.Nop(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	owners, err := iam.NewTaskOwners(w.pool)
	if err != nil {
		t.Fatal(err)
	}
	r.svc, err = operations.New(operations.Deps{Pool: w.pool, Log: zerolog.Nop(), Now: func() time.Time { return now },
		Files: fakeFiles{}, Hubs: writeHubs{w, &sync.Mutex{}}, Reassigner: mover, Renamer: renamer, Commits: fakeCommits{w},
		PGOwned: opt.pgOwned, WriteBack: opt.writeBack, Owners: owners})
	if err != nil {
		t.Fatal(err)
	}
	r.D = w.id(`INSERT INTO tenants (kind, name_th, code, legal_type) VALUES ('carrier', 'TTP', 'TTP', 'company') RETURNING id`)
	clone := func(p *authz.Principal, set authz.CapSet) *authz.Principal {
		c := *p
		c.Caps = set
		return &c
	}
	as := func(p *authz.Principal, role authz.TenantRole) *authz.Principal {
		c := *p
		c.TenantRole, c.Caps = role, authz.TenantDefaults(role)
		return &c
	}
	admin := authz.TenantDefaults(authz.TenantAdmin)
	platform := authz.PlatformDefaults(authz.PlatformAdmin)
	r.ps = map[string]*authz.Principal{
		"staffO":   clone(w.staffO, admin),
		"operator": as(w.staffO, authz.Operator), // no operations:create_standby, not tenant_admin
		"user":     as(w.staffO, authz.User),     // reads only
		"staffA":   clone(w.staffA, admin),
		"staffC":   clone(w.staffC, authz.TenantDefaults(authz.Manager)),
		"dispatcher": {UserID: uuid.New(), TenantID: &r.D, TenantRole: authz.OperationStaff, TenantKind: "carrier",
			Dispatcher: true, PartyIDs: []uuid.UUID{w.partyX},
			Caps: caps(authz.TenantDefaults(authz.OperationStaff), authz.ScopeDefaults(authz.ScopeDispatcher))},
		"platform": {UserID: uuid.New(), Platform: []authz.PlatformRole{authz.PlatformAdmin}, Caps: platform},
		"platformO": {UserID: uuid.New(), Platform: []authz.PlatformRole{authz.PlatformAdmin}, ActOnTenant: &w.O,
			TenantKind: "own_fleet", Steward: true, SubtenantIDs: []uuid.UUID{w.A}, Caps: caps(admin, platform)},
		"apikey": {UserID: uuid.New(), AMR: authz.AMRAPIKey, TenantID: &w.O, Caps: admin},
		// Drivers acting as themselves (the shim targets below /v1/mobile, T32).
		"driverO1": clone(w.driverO1, authz.TenantDefaults(authz.Driver)),
		"driverO2": clone(w.driverO2, authz.TenantDefaults(authz.Driver)),
	}
	// Every principal is a login (created_by, renamed_by and the audit rows reference users).
	for name, p := range r.ps {
		if p.UserID == w.userO || p.UserID == w.userA {
			continue
		}
		w.exec(`INSERT INTO users (id, email, display_name) VALUES ($1, $2, $3)`, p.UserID, name+"@example.test", name)
	}
	r.app = fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.New(testWriter{t}))})
	auth := func(c fiber.Ctx) error {
		authz.SetPrincipal(c, r.ps[c.Get("X-Test-Principal")])
		return c.Next()
	}
	for _, g := range operations.Groups(r.svc, auth) {
		g.Mount(r.app.Group(g.Prefix))
	}
	operations.MobileRoutes(r.svc, auth)(r.app.Group("/v1/mobile"))
	return r
}

// testWriter logs the error handler's lines (a 500's cause) into the test log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	if bytes.Contains(b, []byte(`"level":"error"`)) {
		w.t.Log(strings.TrimSpace(string(b)))
	}
	return len(b), nil
}

// call sends one request as who and decodes the envelope.
func (r *writeWorld) call(who, method, path string, body any) (int, map[string]any) {
	r.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			r.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Principal", who)
	res, err := r.app.Test(req, fiber.TestConfig{Timeout: 60 * time.Second})
	if err != nil {
		r.t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		r.t.Fatalf("%s %s as %s: %d %s", method, path, who, res.StatusCode, raw)
	}
	return res.StatusCode, m
}

// must sends a request that must answer status and returns its data object.
func (r *writeWorld) must(status int, who, method, path string, body any) map[string]any {
	r.t.Helper()
	got, m := r.call(who, method, path, body)
	if got != status {
		r.t.Fatalf("%s %s as %s: %d %v, want %d", method, path, who, got, m, status)
	}
	data, _ := m["data"].(map[string]any)
	return data
}

// refused sends a request that must be refused with status and the reason (a field violation "field reason", else
// details.reason, else the code).
func (r *writeWorld) refused(status int, want, who, method, path string, body any) {
	r.t.Helper()
	got, m := r.call(who, method, path, body)
	if got != status || reasonOf(m) != want {
		r.t.Fatalf("%s %s as %s: %d %s (%v), want %d %s", method, path, who, got, reasonOf(m), m, status, want)
	}
}

func reasonOf(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	if e == nil {
		return ""
	}
	d, _ := e["details"].(map[string]any)
	if f, ok := d["fields"].([]any); ok && len(f) > 0 {
		var parts []string
		for _, x := range f {
			v, _ := x.(map[string]any)
			parts = append(parts, fmt.Sprintf("%v %v", v["field"], v["reason"]))
		}
		return strings.Join(parts, ", ")
	}
	if s, ok := d["reason"].(string); ok {
		return s
	}
	c, _ := e["code"].(string)
	return c
}

// event is one outbox row.
type event struct {
	route   string
	agg     string
	topics  []string
	payload map[string]any
	headers map[string]any
}

// events are the outbox rows of an aggregate id, oldest first.
func (r *writeWorld) events(aggID string) []event {
	r.t.Helper()
	rows, err := r.etl.Query(context.Background(), `SELECT routing_key, aggregate_type, realtime_topics, payload, headers
		FROM outbox_events WHERE aggregate_id = $1 ORDER BY id`, aggID)
	if err != nil {
		r.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (event, error) {
		var e event
		var payload, headers []byte
		err := row.Scan(&e.route, &e.agg, &e.topics, &payload, &headers)
		_ = json.Unmarshal(payload, &e.payload)
		_ = json.Unmarshal(headers, &e.headers)
		return e, err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

func routes(evs []event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.route
	}
	return out
}

// visiblePushes counts the events notify.fcm turns into a visible push (task.assigned, task.reassigned,
// task.cancelled) among evs.
func visiblePushes(evs []event) int {
	n := 0
	for _, e := range evs {
		if e.route == "task.assigned" || e.route == "task.reassigned" || e.route == "task.cancelled" {
			n++
		}
	}
	return n
}

func (r *writeWorld) count(sql string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.etl.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		r.t.Fatalf("%v\n%s", err, sql)
	}
	return n
}

// taskBody is a valid POST /v1/tasks body (first mile, plan day 2026-10-12, billed to X).
func (r *writeWorld) taskBody(extra map[string]any) map[string]any {
	b := map[string]any{"taskType": "first_mile", "date": "2026-10-12", "time": "09:30", "sourceHub": "ALANG-A",
		"destination": "SOCE", "billingPartyId": r.partyX.String()}
	for k, v := range extra {
		if v == nil {
			delete(b, k)
			continue
		}
		b[k] = v
	}
	return b
}

// --- POST /v1/tasks ---------------------------------------------------------------------------------------------

func TestCreateTaskRoute(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	// Assigned: the driver's tenant ('driver'), the number from the allocator, the end of the driver's queue, the
	// truck's plate and class, the hubs resolved from the text; task.created and exactly one task.assigned.
	d := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dO1.String(),
		"truckId": r.truckO.String(), "jobCategory": "SUPPLEMENTARY"}))
	if d["taskNo"] != "FM-12102026-001" || d["status"] != "assigned" || d["runOrder"] != 1.0 ||
		d["tenantSource"] != "driver" || d["tenantId"] != r.O.String() || d["licensePlate"] != "1กข-1234" ||
		d["truckType"] != "6WH" || d["sourceHubId"] != r.hubSrc.String() || d["destinationSocKey"] != "SOCE" ||
		d["jobCategory"] != "SUPPLEMENTARY" || d["planTime"] != "09:30" || d["planDate"] != "2026-10-12" {
		t.Fatalf("created: %v", d)
	}
	id := d["id"].(string)
	evs := r.events(id)
	if got := routes(evs); !slices.Equal(got, []string{"task.created", "task.assigned"}) {
		t.Fatalf("events %v", got)
	}
	if visiblePushes(evs) != 1 || evs[1].payload["driverId"] != r.dO1.String() || evs[1].headers["origin"] != nil {
		t.Fatalf("task.assigned %v %v", evs[1].payload, evs[1].headers)
	}
	for _, want := range []string{"tenant:" + r.O.String() + ":tasks", "driver:" + r.dO1.String(),
		"dispatch:" + r.partyX.String() + ":tasks"} {
		if !slices.Contains(evs[0].topics, want) {
			t.Fatalf("topics %v miss %s", evs[0].topics, want)
		}
	}
	// The same day: the next number; the same driver: the next queue position.
	d = r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dO1.String()}))
	if d["taskNo"] != "FM-12102026-002" || d["runOrder"] != 2.0 || d["jobCategory"] != "PRIMARY" {
		t.Fatalf("second: %v", d)
	}
	// Driverless: pending, the caller's tenant ('form'), no assignment, no push.
	d = r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(nil))
	if d["status"] != "pending" || d["tenantSource"] != "form" || d["tenantId"] != r.O.String() || d["runOrder"] != nil {
		t.Fatalf("driverless: %v", d)
	}
	if got := routes(r.events(d["id"].(string))); !slices.Equal(got, []string{"task.created"}) {
		t.Fatalf("driverless events %v", got)
	}
	// The other type has its own counter; a carrier's driver from the own fleet's staff lands in the carrier.
	d = r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"taskType": "LINE_HAUL", "driverId": r.dA1.String()}))
	if d["taskNo"] != "LH-12102026-001" || d["tenantId"] != r.A.String() || d["tenantSource"] != "driver" {
		t.Fatalf("line haul: %v", d)
	}
	// Multi-drop: the planned stops, stop 1 first.
	d = r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"deliveryStops": []any{
		map[string]any{"destination": "SOCE"}, map[string]any{"destination": "BKK-1"}}}))
	if d["isMultiDelivery"] != true || len(d["stops"].([]any)) != 2 {
		t.Fatalf("multi-drop: %v", d)
	}

	// Refusals, nothing written.
	before := r.count(`SELECT count(*) FROM tasks`)
	for name, c := range map[string]struct {
		who    string
		body   any
		status int
		reason string
	}{
		"no capability":          {"user", r.taskBody(nil), 403, "permission_denied"},
		"an API key":             {"apikey", r.taskBody(nil), 403, "permission_denied"},
		"platform without act":   {"platform", r.taskBody(nil), 403, "permission_denied"},
		"no billing party":       {"staffO", r.taskBody(map[string]any{"billingPartyId": nil}), 422, "billingPartyId required"},
		"no date":                {"staffO", r.taskBody(map[string]any{"date": nil}), 422, "date required"},
		"a bad type":             {"staffO", r.taskBody(map[string]any{"taskType": "x"}), 422, "taskType invalid"},
		"an unknown field":       {"staffO", r.taskBody(map[string]any{"status": "completed"}), 422, "status not_supported"},
		"a bad job category":     {"staffO", r.taskBody(map[string]any{"jobCategory": "เสริม"}), 422, "jobCategory invalid"},
		"a bad time":             {"staffO", r.taskBody(map[string]any{"time": "9:3"}), 422, "time invalid"},
		"an unreadable driver":   {"staffC", r.taskBody(map[string]any{"driverId": r.dO1.String()}), 422, "driverId not_found"},
		"the helper is a driver": {"staffO", r.taskBody(map[string]any{"driverId": r.dO1.String(), "helperDriverId": r.dO1.String()}), 422, "helperDriverId same_as_driver"},
		"an unreadable truck":    {"staffC", r.taskBody(map[string]any{"truckId": r.truckO.String()}), 422, "truckId not_found"},
		"an unknown party":       {"staffO", r.taskBody(map[string]any{"billingPartyId": uuid.NewString()}), 422, "billingPartyId not_found"},
		"one stop":               {"staffO", r.taskBody(map[string]any{"deliveryStops": []any{map[string]any{"destination": "A"}}}), 422, "deliveryStops too_few"},
		"repeated stops": {"staffO", r.taskBody(map[string]any{"deliveryStops": []any{map[string]any{"destination": "a "},
			map[string]any{"destination": "A"}}}), 409, "duplicate_destination"},
		"not an object": {"staffO", "[1]", 400, "bad_request"},
	} {
		got, m := r.call(c.who, "POST", "/v1/tasks", c.body)
		if got != c.status || reasonOf(m) != c.reason {
			t.Errorf("%s: %d %s, want %d %s", name, got, reasonOf(m), c.status, c.reason)
		}
	}
	if n := r.count(`SELECT count(*) FROM tasks`); n != before {
		t.Fatalf("%d tasks written by refused requests", n-before)
	}
}

// T28's handoff (#48's first criterion end to end): a dispatcher's driverless POST /v1/tasks lands in its
// organisation's tenant with tenant_source 'form'.
func TestDispatcherCallOutLandsInItsTenant(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	d := r.must(201, "dispatcher", "POST", "/v1/tasks", r.taskBody(nil))
	id := d["id"].(string)
	var tenant uuid.UUID
	var source string
	if err := r.etl.QueryRow(context.Background(), `SELECT tenant_id, tenant_source FROM tasks WHERE id = $1`, id).Scan(&tenant, &source); err != nil {
		t.Fatal(err)
	}
	if tenant != r.D || source != "form" {
		t.Fatalf("call-out in %s (%s), want %s form", tenant, source, r.D)
	}
	// The dispatcher's payload is the projection: no party or stamping field.
	if _, ok := d["billingPartyId"]; ok {
		t.Fatalf("dispatcher payload carries staff fields: %v", d)
	}
}

// R10 (AC): task numbers are allocated atomically inside the insert transaction, never count()+1: concurrent creations
// of one type and day get distinct consecutive numbers.
func TestTaskNumbersConcurrent(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	const n = 12
	var wg sync.WaitGroup
	nos := make([]string, n)
	errs := make([]string, n)
	for i := range n {
		wg.Go(func() {
			got, m := r.call("staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"date": "2026-10-20"}))
			if got != 201 {
				errs[i] = fmt.Sprint(got, m)
				return
			}
			nos[i] = m["data"].(map[string]any)["taskNo"].(string)
		})
	}
	wg.Wait()
	for _, e := range errs {
		if e != "" {
			t.Fatal(e)
		}
	}
	sort.Strings(nos)
	for i, no := range nos {
		if want := fmt.Sprintf("FM-20102026-%03d", i+1); no != want {
			t.Fatalf("numbers %v", nos)
		}
	}
}

// --- PATCH /v1/tasks/{id} ---------------------------------------------------------------------------------------

func TestPatchTask(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	id := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(nil))["id"].(string)
	path := "/v1/tasks/" + id
	eventsAfter := func(k int) []event { return r.events(id)[k:] }

	// AC: an update never resets status or run order unless requested (tasks.ts:120-137).
	d := r.must(200, "staffO", "PATCH", path, map[string]any{"jobCategory": "supplementary"})
	if d["status"] != "pending" || d["runOrder"] != nil || d["jobCategory"] != "SUPPLEMENTARY" {
		t.Fatalf("job category: %v", d)
	}
	if got := routes(eventsAfter(1)); !slices.Equal(got, []string{"task.updated"}) {
		t.Fatalf("events %v", got)
	}
	// AC: assigning a task produces exactly one visible push through the outbox.
	k := len(r.events(id))
	d = r.must(200, "staffO", "PATCH", path, map[string]any{"driverId": r.dO1.String()})
	if d["status"] != "assigned" || d["runOrder"] != 1.0 || d["driverId"] != r.dO1.String() {
		t.Fatalf("assigned: %v", d)
	}
	if evs := eventsAfter(k); visiblePushes(evs) != 1 || evs[0].route != "task.assigned" || evs[0].payload["driverId"] != r.dO1.String() {
		t.Fatalf("assignment events %v", routes(evs))
	}
	k = len(r.events(id))
	d = r.must(200, "staffO", "PATCH", path, map[string]any{"jobCategory": "PRIMARY", "time": "11:00"})
	if d["status"] != "assigned" || d["runOrder"] != 1.0 || d["planTime"] != "11:00" {
		t.Fatalf("an assigned task's update: %v", d)
	}
	if evs := eventsAfter(k); visiblePushes(evs) != 0 || len(evs) != 1 {
		t.Fatalf("update events %v", routes(evs))
	}
	// Another driver: task.reassigned (the previous driver loses it); the end of the new driver's queue.
	other := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dO2.String()}))
	if other["runOrder"] != 1.0 {
		t.Fatalf("other: %v", other)
	}
	k = len(r.events(id))
	d = r.must(200, "staffO", "PATCH", path, map[string]any{"driverId": r.dO2.String()})
	if d["runOrder"] != 2.0 || d["status"] != "assigned" {
		t.Fatalf("reassigned: %v", d)
	}
	if evs := eventsAfter(k); len(evs) != 1 || evs[0].route != "task.reassigned" || evs[0].payload["previousDriverId"] != r.dO1.String() {
		t.Fatalf("reassignment events %v", eventsAfter(k))
	}
	// Explicit run order; then no driver: pending, out of the queue.
	if d = r.must(200, "staffO", "PATCH", path, map[string]any{"runOrder": 7}); d["runOrder"] != 7.0 {
		t.Fatalf("run order: %v", d)
	}
	if d = r.must(200, "staffO", "PATCH", path, map[string]any{"driverId": nil}); d["status"] != "pending" || d["runOrder"] != nil {
		t.Fatalf("unassigned: %v", d)
	}
	// The plan day moves: task.updated and task.plan_date_changed.
	k = len(r.events(id))
	r.must(200, "staffO", "PATCH", path, map[string]any{"date": "2026-10-13"})
	if got := routes(eventsAfter(k)); !slices.Equal(got, []string{"task.updated", "task.plan_date_changed"}) {
		t.Fatalf("plan day events %v", got)
	}
	// Nothing changes: no event.
	k = len(r.events(id))
	r.must(200, "staffO", "PATCH", path, map[string]any{"date": "2026-10-13", "jobCategory": "PRIMARY"})
	if n := len(r.events(id)); n != k {
		t.Fatalf("a no-op wrote %d events", n-k)
	}
	// The task's own tenant in the body is the ordinary update (T28 answered 422 same_tenant).
	if d = r.must(200, "staffO", "PATCH", path, map[string]any{"tenantId": r.O.String(), "driverId": r.dO1.String()}); d["driverId"] != r.dO1.String() {
		t.Fatalf("own tenant: %v", d)
	}
	r.refused(422, "status status_needs_driver", "staffO", "PATCH", "/v1/tasks/"+other["id"].(string),
		map[string]any{"driverId": nil, "status": "assigned"})
	r.refused(422, "status not_settable", "staffO", "PATCH", path, map[string]any{"status": "completed"})
	r.refused(422, "taskType not_supported", "staffO", "PATCH", path, map[string]any{"taskType": "line_haul"})

	// A checked-in task keeps its driver.
	r.exec(`UPDATE tasks SET status = 'checked_in' WHERE id = $1`, id)
	r.refused(409, operations.ReasonTaskInProgress, "staffO", "PATCH", path, map[string]any{"driverId": r.dO2.String()})

	// Appendix C §C.9.2 #12: a dispatcher on another carrier's task changes 0 rows (404); a platform admin needs
	// X-Act-On-Tenant; with it the update runs as that tenant's admin.
	var updated time.Time
	_ = r.etl.QueryRow(context.Background(), `SELECT updated_at FROM tasks WHERE id = $1`, id).Scan(&updated)
	r.refused(404, "not_found", "dispatcher", "PATCH", path, map[string]any{"jobCategory": "SUPPLEMENTARY"})
	r.refused(404, "not_found", "dispatcher", "PATCH", path, map[string]any{"tenantId": r.O.String(), "jobCategory": "SUPPLEMENTARY"})
	r.refused(403, operations.ReasonActOnTenantRequired, "platform", "PATCH", path, map[string]any{"jobCategory": "SUPPLEMENTARY"})
	var after time.Time
	_ = r.etl.QueryRow(context.Background(), `SELECT updated_at FROM tasks WHERE id = $1`, id).Scan(&after)
	if !after.Equal(updated) {
		t.Fatal("a refused update changed the task")
	}
	if d = r.must(200, "platformO", "PATCH", path, map[string]any{"jobCategory": "SUPPLEMENTARY"}); d["jobCategory"] != "SUPPLEMENTARY" {
		t.Fatalf("platform with X-Act-On-Tenant: %v", d)
	}
	r.refused(403, "permission_denied", "user", "PATCH", path, map[string]any{"jobCategory": "PRIMARY"})
}

// T31 review (Appendix C §C.1.7, §C.9.2 #12): the ordinary writes never link a driver, helper or truck of another
// tenant. Readable is wider than writable: once carrier A's driver and truck have recent work for party X, the
// dispatcher reads them through its scope, yet its call-out takes them only through the audited move (PATCH with
// tenantId). A task's driver is of the task's tenant; a helper or a truck may come from a tenant linked by
// contractor_tenant_id, within the writer's own reach. Refusals write nothing and emit nothing.
func TestWriteLinksStayInTheTenant(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	r.must(201, "staffA", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dA1.String(), "truckId": r.truckA.String()}))
	callout := r.must(201, "dispatcher", "POST", "/v1/tasks", r.taskBody(nil))["id"].(string)
	path := "/v1/tasks/" + callout
	k := len(r.events(callout))
	for name, c := range map[string]struct {
		method, path string
		body         any
		reason       string
	}{
		"PATCH a carrier's driver":     {"PATCH", path, map[string]any{"driverId": r.dA1.String()}, "driverId not_in_tenant"},
		"PATCH a carrier's truck":      {"PATCH", path, map[string]any{"truckId": r.truckA.String()}, "truckId not_in_tenant"},
		"PATCH a carrier's helper":     {"PATCH", path, map[string]any{"helperDriverId": r.dA1.String()}, "helperDriverId not_in_tenant"},
		"POST with a carrier's helper": {"POST", "/v1/tasks", r.taskBody(map[string]any{"helperDriverId": r.dA1.String()}), "helperDriverId not_in_tenant"},
		"POST with a carrier's truck":  {"POST", "/v1/tasks", r.taskBody(map[string]any{"truckId": r.truckA.String()}), "truckId not_in_tenant"},
		"a call-out's standby": {"POST", "/v1/standby/backfill", map[string]any{"taskId": callout, "driverId": r.dA1.String(),
			"truckId": r.truckA.String(), "startLocation": "A", "endLocation": "B", "startedAt": "2026-10-10T01:00:00Z",
			"endedAt": "2026-10-10T02:00:00Z"}, "driverId not_in_tenant, truckId not_in_tenant"},
	} {
		tasks, records := r.count(`SELECT count(*) FROM tasks`), r.count(`SELECT count(*) FROM standby_records`)
		r.refused(422, c.reason, "dispatcher", c.method, c.path, c.body)
		if r.count(`SELECT count(*) FROM tasks`) != tasks || r.count(`SELECT count(*) FROM standby_records`) != records {
			t.Fatalf("%s: a refused write wrote a row", name)
		}
	}
	if r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND driver_id IS NULL AND truck_id IS NULL AND helper_driver_id IS NULL
		AND tenant_id = $2`, callout, r.D) != 1 || len(r.events(callout)) != k {
		t.Fatal("a refused update changed the call-out or emitted an event")
	}
	// The audited move still takes the call-out to the carrier with its driver.
	if d := r.must(200, "dispatcher", "PATCH", path, map[string]any{"tenantId": r.A.String(), "driverId": r.dA1.String()}); d["moved"] != true {
		t.Fatalf("move: %v", d)
	}

	// The own fleet's staff: an own-fleet task does not take a carrier's driver by an update (that is a move), but a
	// carrier's task takes an own-fleet helper and truck (the carrier's contractor) in the staff's reach.
	own := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(nil))["id"].(string)
	r.refused(422, "driverId not_in_tenant", "staffO", "PATCH", "/v1/tasks/"+own, map[string]any{"driverId": r.dA1.String()})
	carried := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dA1.String(),
		"helperDriverId": r.dO1.String(), "truckId": r.truckO.String()}))
	if carried["tenantId"] != r.A.String() || carried["helperDriverId"] != r.dO1.String() || carried["truckId"] != r.truckO.String() {
		t.Fatalf("a carrier's task with the contractor's helper and truck: %v", carried)
	}
	r.must(200, "staffO", "PATCH", "/v1/tasks/"+carried["id"].(string), map[string]any{"helperDriverId": r.dO2.String()})

	// An admin incident on an own-fleet trip naming another carrier's driver; the contractor-linked truck is fine.
	_, trip := r.tripOf("SPXTH-LINK1")
	r.refused(422, "driverId not_in_tenant", "staffO", "POST", "/v1/incidents", map[string]any{"tripId": trip,
		"driverId": r.dA1.String(), "description": "x"})
	r.must(201, "staffO", "POST", "/v1/incidents", map[string]any{"tripId": trip, "truckId": r.truckA.String(), "description": "x"})
}

// T31 review: the driver and the helper a task already holds are not checked again. A driver deactivated since the
// assignment does not block the task's other edits when a form sends its full state back; a new inactive driver or
// helper is still 409 driver_inactive.
func TestPatchTaskKeepsAnInactiveDriver(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	id := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dO1.String(),
		"helperDriverId": r.dO2.String()}))["id"].(string)
	path := "/v1/tasks/" + id
	r.exec(`UPDATE drivers SET status = 'inactive' WHERE id = ANY($1)`, []uuid.UUID{r.dO1, r.dO2})
	k := len(r.events(id))
	d := r.must(200, "staffO", "PATCH", path, map[string]any{"driverId": r.dO1.String(), "helperDriverId": r.dO2.String(), "time": "10:00"})
	if d["planTime"] != "10:00" || d["driverId"] != r.dO1.String() || d["helperDriverId"] != r.dO2.String() {
		t.Fatalf("full-state edit: %v", d)
	}
	if got := routes(r.events(id)[k:]); !slices.Equal(got, []string{"task.updated"}) {
		t.Fatalf("events %v, want task.updated only", got)
	}
	r.must(200, "staffO", "PATCH", path, map[string]any{"driverId": r.dO1.String()}) // nothing changes
	other := r.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile, status)
		VALUES ($1, 'self', 'O3', 'T', '0800000003', 'inactive') RETURNING id`, r.O)
	r.refused(409, operations.ReasonDriverInactive, "staffO", "PATCH", path, map[string]any{"driverId": other.String()})
	r.refused(409, operations.ReasonDriverInactive, "staffO", "PATCH", path, map[string]any{"helperDriverId": other.String()})
}

// T31 review (Appendix B §B.2.11 "helper change checks payroll lock", the legacy EditTripDetailsDialog check): a change
// of a task's helper, or a move of its plan day out of the round, is 409 payroll_locked when the affected helper's
// payroll run for that Bangkok period and round is approved or paid; a draft or pending one is no lock, and an update
// that leaves the helper's day where it is never reads it. The runs are read by a SECURITY DEFINER helper (0020): a
// principal holding operations:manage_tasks alone, and a carrier admin whose old helper is the own fleet's (a run its
// RLS hides), are refused alike.
func TestPatchTaskHelperPayrollLock(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	tasksOnly := *r.ps["staffO"]
	tasksOnly.Caps = authz.NewCapSet(authz.OperationsManageTasks)
	r.ps["tasksOnly"] = &tasksOnly
	dO3 := r.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile) VALUES ($1, 'self', 'O3', 'T', '0800000003') RETURNING id`, r.O)
	run := func(driver, tenant uuid.UUID, period, round, status string) {
		r.exec(`INSERT INTO payroll_runs (tenant_id, driver_id, pay_period, pay_round, period_start, period_end, status)
			VALUES ($1, $2, $3, $4, '2026-09-30T17:00:00Z', '2026-10-15T17:00:00Z', $5)
			ON CONFLICT DO NOTHING`, tenant, driver, period, round, status)
		r.exec(`UPDATE payroll_runs SET status = $4 WHERE driver_id = $1 AND pay_period = $2 AND pay_round = $3`, driver, period, round, status)
	}
	// Plan day 2026-10-12: period 2026-10, round R1.
	id := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dO1.String(),
		"helperDriverId": r.dO2.String()}))["id"].(string)
	path := "/v1/tasks/" + id
	for _, status := range []string{"approved", "paid"} {
		run(r.dO2, r.O, "2026-10", "R1", status)
		got, m := r.call("tasksOnly", "PATCH", path, map[string]any{"helperDriverId": dO3.String()})
		d, _ := m["error"].(map[string]any)["details"].(map[string]any)
		if got != 409 || reasonOf(m) != operations.CodePayrollLocked || d["driverId"] != r.dO2.String() || d["status"] != status ||
			d["period"] != "2026-10" || d["round"] != "R1" {
			t.Fatalf("the old helper's %s run: %d %v", status, got, m)
		}
	}
	if r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND helper_driver_id = $2`, id, r.dO2) != 1 {
		t.Fatal("a locked helper change was written")
	}
	// Updates that leave the helper's day where it is: not read.
	r.must(200, "tasksOnly", "PATCH", path, map[string]any{"jobCategory": "SUPPLEMENTARY", "helperDriverId": r.dO2.String()})
	r.must(200, "tasksOnly", "PATCH", path, map[string]any{"date": "2026-10-13"}) // the same round
	// A draft or pending run is no lock.
	for _, status := range []string{"draft", "pending_approval"} {
		run(r.dO2, r.O, "2026-10", "R1", status)
	}
	if d := r.must(200, "tasksOnly", "PATCH", path, map[string]any{"helperDriverId": dO3.String()}); d["helperDriverId"] != dO3.String() {
		t.Fatalf("a pending run: %v", d)
	}
	// The new helper's approved run.
	run(r.dO2, r.O, "2026-10", "R1", "approved")
	r.refused(409, operations.CodePayrollLocked, "tasksOnly", "PATCH", path, map[string]any{"helperDriverId": r.dO2.String()})
	// The plan day moves the helper's day into an approved round (2026-10 R2).
	run(dO3, r.O, "2026-10", "R2", "approved")
	r.refused(409, operations.CodePayrollLocked, "tasksOnly", "PATCH", path, map[string]any{"date": "2026-10-20"})

	// A carrier's task with the own fleet's helper: the carrier's admin does not read the own fleet's run, the check does.
	carried := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dA1.String(),
		"helperDriverId": r.dO1.String()}))["id"].(string)
	dA2 := r.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile) VALUES ($1, 'self', 'A2', 'T', '0800000004') RETURNING id`, r.A)
	run(r.dO1, r.O, "2026-10", "R1", "paid")
	r.refused(409, operations.CodePayrollLocked, "staffA", "PATCH", "/v1/tasks/"+carried, map[string]any{"helperDriverId": dA2.String()})

	// The write-back path refuses before the Firestore commit.
	mem := writeback.NewMemory()
	w := newWriteWorld(t, writeOptions{writeBack: &operations.WriteBack{Writer: mem}})
	wt := w.createTask(w.staffO, func() operations.NewTask {
		n := w.newTask(w.O, time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC))
		n.LegacyDocID, n.DriverID, n.HelperDriverID, n.Status = ptr("task_pay_doc"), &w.dO1, &w.dO2, operations.TaskAssigned
		return n
	}())
	mem.Put("tasks", "task_pay_doc", map[string]any{"status": "Assigned"}, now)
	w.exec(`INSERT INTO payroll_runs (tenant_id, driver_id, pay_period, pay_round, period_start, period_end, status)
		VALUES ($1, $2, '2026-10', 'R1', '2026-09-30T17:00:00Z', '2026-10-15T17:00:00Z', 'approved')`, w.O, w.dO2)
	before := len(mem.Writes())
	w.refused(409, operations.CodePayrollLocked, "staffO", "PATCH", "/v1/tasks/"+wt.ID.String(), map[string]any{"helperDriverId": nil})
	if len(mem.Writes()) != before {
		t.Fatal("a locked write-back helper change wrote Firestore")
	}
}

// T31 review: a class-only PATCH of a task that keeps a truck of known class must be that truck's class (the truck's
// class wins, as on create); clearing the truck in the same body sets any class, and a truck of unknown class takes one.
func TestPatchTaskTruckClass(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	id := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"truckId": r.truckO.String()}))["id"].(string)
	path := "/v1/tasks/" + id
	r.refused(422, "truckType not_truck_class", "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"truckId": r.truckO.String(), "truckType": "4WJ"}))
	r.refused(422, "truckType not_truck_class", "staffO", "PATCH", path, map[string]any{"truckType": "4WJ"})
	if d := r.must(200, "staffO", "PATCH", path, map[string]any{"truckType": "6WH"}); d["truckType"] != "6WH" || d["truckId"] != r.truckO.String() {
		t.Fatalf("the truck's own class: %v", d)
	}
	if d := r.must(200, "staffO", "PATCH", path, map[string]any{"truckId": nil, "truckType": "4WJ"}); d["truckType"] != "4WJ" || d["truckId"] != nil {
		t.Fatalf("truck cleared, class set: %v", d)
	}
	r.exec(`UPDATE trucks SET vehicle_class = NULL WHERE id = $1`, r.truckO)
	r.must(200, "staffO", "PATCH", path, map[string]any{"truckId": r.truckO.String()})
	if d := r.must(200, "staffO", "PATCH", path, map[string]any{"truckType": "10WH"}); d["truckType"] != "10WH" {
		t.Fatalf("a truck of unknown class: %v", d)
	}
}

// A body whose tenantId differs from the task's is still T28's move (iam.TaskMover).
func TestPatchTaskMoveStaysR13(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	id := r.must(201, "dispatcher", "POST", "/v1/tasks", r.taskBody(nil))["id"].(string)
	d := r.must(200, "dispatcher", "PATCH", "/v1/tasks/"+id, map[string]any{"tenantId": r.A.String(), "driverId": r.dA1.String()})
	if d["moved"] != true || d["tenantId"] != r.A.String() || d["previousTenantId"] != r.D.String() {
		t.Fatalf("move: %v", d)
	}
	r.refused(422, "jobCategory not_supported", "dispatcher", "PATCH", "/v1/tasks/"+id,
		map[string]any{"tenantId": r.D.String(), "jobCategory": "PRIMARY"})
}

// --- cancel and stops -------------------------------------------------------------------------------------------

func TestCancelTaskAndStops(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	id := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dO1.String()}))["id"].(string)
	k := len(r.events(id))
	d := r.must(200, "staffO", "POST", "/v1/tasks/"+id+"/cancel", map[string]any{"reason": "ลูกค้ายกเลิก"})
	if d["status"] != "cancelled" || d["cancelReason"] != "ลูกค้ายกเลิก" || d["cancelledAt"] == nil {
		t.Fatalf("cancelled: %v", d)
	}
	if evs := r.events(id)[k:]; len(evs) != 1 || evs[0].route != "task.cancelled" {
		t.Fatalf("cancel events %v", routes(evs))
	}
	k = len(r.events(id))
	r.must(200, "staffO", "POST", "/v1/tasks/"+id+"/cancel", nil)
	if len(r.events(id)) != k {
		t.Fatal("a second cancel wrote an event")
	}
	// A new driver reopens a cancelled task (the legacy edit's Cancelled -> Assigned).
	if d = r.must(200, "staffO", "PATCH", "/v1/tasks/"+id, map[string]any{"driverId": r.dO2.String()}); d["status"] != "assigned" || d["cancelledAt"] != nil {
		t.Fatalf("reopened: %v", d)
	}
	done := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(nil))["id"].(string)
	r.exec(`UPDATE tasks SET status = 'completed' WHERE id = $1`, done)
	r.refused(409, operations.ReasonTaskClosed, "staffO", "POST", "/v1/tasks/"+done+"/cancel", nil)
	r.refused(404, "not_found", "staffC", "POST", "/v1/tasks/"+id+"/cancel", nil)
	r.refused(422, "foo not_supported", "staffO", "POST", "/v1/tasks/"+id+"/cancel", map[string]any{"foo": 1})

	// Stops: stop 1 is the task's destination, the new one is upper-cased, a repeat is 409.
	s := r.must(200, "staffO", "POST", "/v1/tasks/"+id+"/stops", map[string]any{"destination": "bkk-9", "isCustom": true})
	stops, _ := s["stops"].([]any)
	if len(stops) != 2 || stops[0].(map[string]any)["destination"] != "SOCE" || stops[1].(map[string]any)["destination"] != "BKK-9" ||
		stops[1].(map[string]any)["isCustom"] != true {
		t.Fatalf("stops: %v", s)
	}
	r.refused(409, "duplicate_destination", "staffO", "POST", "/v1/tasks/"+id+"/stops", map[string]any{"destination": " BKK-9"})
	r.refused(422, "destination required", "staffO", "POST", "/v1/tasks/"+id+"/stops", map[string]any{})
	if task := r.must(200, "staffO", "PATCH", "/v1/tasks/"+id, map[string]any{}); task["isMultiDelivery"] != true {
		t.Fatalf("multi-drop flag: %v", task)
	}
}

// --- POST /v1/tasks/import --------------------------------------------------------------------------------------

func TestImportTasks(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	row := func(extra map[string]any) map[string]any {
		b := map[string]any{"date": "2026-10-14", "sourceHub": "ALANG-A", "destination": "SOCE", "billingPartyId": r.partyX.String()}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	body := map[string]any{"taskType": "first_mile", "rows": []any{
		row(nil), // 1: PRIMARY (blank cell)
		row(map[string]any{"jobCategory": "เสริม"}),      // 2: SUPPLEMENTARY
		row(map[string]any{"jobCategory": "เสิรม"}),      // 3: misspelled: refused, never PRIMARY
		row(map[string]any{"plate": "1กข 1234"}),         // 4: the fleet truck (normalised plate)
		row(map[string]any{"plate": "9ZZ-0000"}),         // 5: not in the fleet
		row(map[string]any{"taskNo": "FM-14102026-007"}), // 6: the sheet's number kept
		row(map[string]any{"date": nil}),                 // 7: no date
		map[string]any{"date": "2026-10-14"},             // 8: no places, no party
	}}
	dry := map[string]any{"taskType": "first_mile", "dryRun": true, "rows": body["rows"]}
	d := r.must(200, "staffO", "POST", "/v1/tasks/import", dry)
	if len(d["created"].([]any)) != 4 || r.count(`SELECT count(*) FROM tasks`) != 0 {
		t.Fatalf("dry run: %v", d)
	}
	d = r.must(200, "staffO", "POST", "/v1/tasks/import", body)
	created, errs := d["created"].([]any), d["errors"].([]any)
	if len(created) != 4 || len(errs) != 4 {
		t.Fatalf("import: %v", d)
	}
	byRow := map[float64]map[string]any{}
	for _, c := range created {
		m := c.(map[string]any)
		byRow[m["row"].(float64)] = m
	}
	reasons := map[float64]string{}
	for _, e := range errs {
		m := e.(map[string]any)
		reasons[m["row"].(float64)] = reasonOf(map[string]any{"error": map[string]any{"details": map[string]any{"fields": m["fields"]}}})
	}
	if reasons[3] != "jobCategory invalid" || reasons[5] != "plate not_in_fleet" || reasons[7] != "date required" ||
		!strings.Contains(reasons[8], "sourceHub required") {
		t.Fatalf("errors %v", reasons)
	}
	if byRow[6]["taskNo"] != "FM-14102026-007" || byRow[1]["taskNo"] != "FM-14102026-001" || byRow[2]["taskNo"] != "FM-14102026-002" {
		t.Fatalf("numbers %v", byRow)
	}
	var cat, plate, status string
	if err := r.etl.QueryRow(context.Background(), `SELECT job_category, coalesce(license_plate_snapshot, ''), status FROM tasks WHERE id = $1`,
		byRow[4]["id"]).Scan(&cat, &plate, &status); err != nil || cat != "PRIMARY" || plate != "1กข-1234" || status != "pending" {
		t.Fatalf("row 4: %s %s %s %v", cat, plate, status, err)
	}
	if err := r.etl.QueryRow(context.Background(), `SELECT job_category FROM tasks WHERE id = $1`, byRow[2]["id"]).Scan(&cat); err != nil || cat != "SUPPLEMENTARY" {
		t.Fatalf("row 2: %s %v", cat, err)
	}
	// The sheet's canonical number moved the day's counter: the next allocation is past it.
	d = r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"date": "2026-10-14"}))
	if d["taskNo"] != "FM-14102026-008" {
		t.Fatalf("after the sheet's number: %v", d["taskNo"])
	}
	r.refused(422, "notify not_supported", "staffO", "POST", "/v1/tasks/import", map[string]any{"taskType": "first_mile",
		"notify": true, "rows": []any{row(nil)}})
	r.refused(422, "rows required", "staffO", "POST", "/v1/tasks/import", map[string]any{"taskType": "first_mile"})
	r.refused(403, "permission_denied", "user", "POST", "/v1/tasks/import", body)
}

// T31 review: the import matches plates as normalizePlate did, dropping JavaScript's \s (NBSP, the Zs spaces, BOM, VT)
// and dashes on both sides: a cell pasted with a non-breaking space finds its truck, and so does a cell with a dash for
// a truck stored with one.
func TestImportPlateKeys(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	stored := r.id(`INSERT INTO trucks (tenant_id, tenant_source, license_plate, province, status, brand, model, year, color, type_raw, vehicle_class)
		VALUES ($1, 'self', $2, 'กรุงเทพมหานคร', 'active', 'ISUZU', 'FRR', 2020, 'white', '6 Wheels', '6WH') RETURNING id`, r.O, "3กข\u00a05678")
	row := func(plate string) map[string]any {
		return map[string]any{"date": "2026-10-14", "sourceHub": "ALANG-A", "destination": "SOCE", "billingPartyId": r.partyX.String(), "plate": plate}
	}
	d := r.must(200, "staffO", "POST", "/v1/tasks/import", map[string]any{"taskType": "first_mile", "rows": []any{
		row("1กข\u00a01234"), row("1กข\u30001234"), row("\ufeff1กข\v1234"), row("3กข-5678"), row("3กข 5678")}})
	if len(d["created"].([]any)) != 5 || len(d["errors"].([]any)) != 0 {
		t.Fatalf("import %v", d)
	}
	if n := r.count(`SELECT count(*) FROM tasks WHERE truck_id = $1`, r.truckO); n != 3 {
		t.Fatalf("%d tasks on 1กข-1234", n)
	}
	if n := r.count(`SELECT count(*) FROM tasks WHERE truck_id = $1`, stored); n != 2 {
		t.Fatalf("%d tasks on the truck stored with a NBSP", n)
	}
}

// T31 review: the counters behind the task numbers are global across tenants (R10, R66). A sheet's canonical number
// moves the counter of the type and day it names, so it must be of the import's type and of its row's plan day and
// within the allocator's three digits; an unrelated carrier's manager cannot push a day's counter to the int ceiling and
// stop every tenant's creations of that day. A kept number past operations.MaxBumpSeq moves nothing, and a counter at
// the ceiling answers 409 task_counter_exhausted, never 500.
func TestImportTaskNoBounds(t *testing.T) {
	mem := writeback.NewMemory()
	for _, wb := range []bool{false, true} {
		opt := writeOptions{pgOwned: true}
		if wb {
			opt = writeOptions{writeBack: &operations.WriteBack{Writer: mem}}
		}
		r := newWriteWorld(t, opt)
		r.exec(`UPDATE customers SET legacy_doc_id = 'cust_cjsf' WHERE code = 'CJSF'`)
		row := func(date, no string) map[string]any {
			return map[string]any{"date": date, "sourceHub": "ALANG-A", "destination": "SOCE", "billingPartyId": r.partyX.String(), "taskNo": no}
		}
		before := len(mem.Writes())
		d := r.must(map[bool]int{false: 200, true: 202}[wb], "staffC", "POST", "/v1/tasks/import", map[string]any{"taskType": "first_mile",
			"rows": []any{
				row("2026-10-12", "FM-20102026-2147483647"), // 1: another day
				row("2026-10-20", "FM-20102026-2147483647"), // 2: past three digits
				row("2026-10-20", "LH-20102026-005"),        // 3: another type
				row("2026-10-20", "FM-20102026-1000"),       // 4: past three digits
				row("2026-10-20", "SHP-778899"),             // 5: a shipment id: kept, moves nothing
				row("2026-10-20", "FM-20102026-7"),          // 6: unpadded: kept, moves nothing
			}})
		reasons := map[float64]string{}
		for _, e := range d["errors"].([]any) {
			m := e.(map[string]any)
			reasons[m["row"].(float64)] = reasonOf(map[string]any{"error": map[string]any{"details": map[string]any{"fields": m["fields"]}}})
		}
		if len(d["created"].([]any)) != 2 || reasons[1] != "taskNo mismatch" || reasons[2] != "taskNo out_of_range" ||
			reasons[3] != "taskNo mismatch" || reasons[4] != "taskNo out_of_range" {
			t.Fatalf("write-back %v: import %v", wb, d)
		}
		if wb && len(mem.Writes()) != before+2 { // the two valid rows' documents
			t.Fatalf("write-back import writes %d", len(mem.Writes())-before)
		}
		if n := r.count(`SELECT count(*) FROM task_number_counters WHERE plan_date = '2026-10-20'`); n != 0 {
			t.Fatalf("write-back %v: %d counters moved", wb, n)
		}
		if wb {
			continue
		}
		// Every tenant still numbers that day from 001.
		if no := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"date": "2026-10-20"}))["taskNo"]; no != "FM-20102026-001" {
			t.Fatalf("after the refused sheet: %v", no)
		}
		// A number kept by CreateTask (a mirrored or legacy number) past MaxBumpSeq moves nothing either.
		n := r.newTask(r.O, time.Date(2026, 10, 21, 2, 0, 0, 0, time.UTC))
		n.TaskNo = ptr("FM-21102026-2147483647")
		r.createTask(r.staffO, n)
		if no := r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"date": "2026-10-21"}))["taskNo"]; no != "FM-21102026-001" {
			t.Fatalf("after a kept number past the bound: %v", no)
		}
		// A counter already at the ceiling (a planted legacy number before this rule) is a 409, not a 500.
		r.exec(`INSERT INTO task_number_counters (task_type, plan_date, last_seq) VALUES ('first_mile', '2026-10-22', 2147483647)`)
		r.refused(409, operations.ReasonTaskCounterExhausted, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"date": "2026-10-22"}))
	}
}

// --- trips ------------------------------------------------------------------------------------------------------

// tripOf creates an assigned task of dO1 and its in-transit trip.
func (r *writeWorld) tripOf(no string) (task, trip string) {
	r.t.Helper()
	task = r.must(201, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dO1.String()}))["id"].(string)
	var tk operations.Task
	if err := r.tx(r.staffO, func(tx pgx.Tx) error {
		var err error
		tk, err = operationsdbTask(tx, task)
		return err
	}); err != nil {
		r.t.Fatal(err)
	}
	return task, r.createTrip(r.staffO, r.newTrip(tk, no)).ID.String()
}

func operationsdbTask(tx pgx.Tx, id string) (operations.Task, error) {
	var t operations.Task
	err := tx.QueryRow(context.Background(), `SELECT id, tenant_id, task_type, source_hub_raw, destination_raw, driver_id, billing_party_id
		FROM tasks WHERE id = $1`, id).Scan(&t.ID, &t.TenantID, &t.TaskType, &t.SourceHubRaw, &t.DestinationRaw, &t.DriverID, &t.BillingPartyID)
	return t, err
}

func TestTripWrites(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	task, trip := r.tripOf("SPXTH-0001")
	path := "/v1/trips/" + trip

	d := r.must(200, "staffO", "PATCH", path, map[string]any{"sealCode": "S-1", "partnerCode": "P1", "needsAdminReview": true,
		"reviewStatus": "pending_review", "ocrData": map[string]any{"partnerCode": "P1", "n": 3}, "destination": "SOCN",
		"photos": []any{map[string]any{"type": "seal", "key": "file:0", "lat": 13.7}}})
	if d["sealCode"] != "S-1" || d["partnerCode"] != "P1" || d["needsAdminReview"] != true || len(d["photos"].([]any)) != 1 {
		t.Fatalf("patched: %v", d)
	}
	if got := routes(r.events(trip)); !slices.Equal(got, []string{"trip.updated"}) {
		t.Fatalf("patch events %v", got)
	}
	r.refused(422, "reviewStatus invalid", "staffO", "PATCH", path, map[string]any{"reviewStatus": "approved"})
	r.refused(422, "photos.key not_found", "staffO", "PATCH", path, map[string]any{"photos": []any{map[string]any{"type": "seal", "key": "nope"}}})
	r.refused(403, "permission_denied", "user", "PATCH", path, map[string]any{"sealCode": "x"})

	// The admin close: delivered (admin_web), the task completed, the driver's active job cleared; trip.delivered.
	r.exec(`UPDATE drivers SET active_task_id = $1, active_truck_id = $2, active_truck_plate = 'x' WHERE id = $3`, task, r.truckO, r.dO1)
	at := "2026-10-11T08:00:00Z"
	d = r.must(200, "staffO", "POST", path+"/deliver", map[string]any{"deliveredAt": at, "lat": 13.1, "lng": 100.2})
	if delivered, _ := time.Parse(time.RFC3339, fmt.Sprint(d["deliveredAt"])); d["status"] != "delivered" ||
		d["deliveredVia"] != "admin_web" || delivered.UTC().Format(time.RFC3339) != at {
		t.Fatalf("delivered: %v", d)
	}
	if r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND status = 'completed'`, task) != 1 ||
		r.count(`SELECT count(*) FROM drivers WHERE id = $1 AND active_task_id IS NULL AND active_truck_id IS NULL`, r.dO1) != 1 {
		t.Fatal("deliver side effects")
	}
	if got := routes(r.events(trip)); !slices.Equal(got, []string{"trip.updated", "trip.delivered"}) {
		t.Fatalf("deliver events %v", got)
	}
	r.refused(409, operations.ReasonTripNotOpen, "staffO", "POST", path+"/deliver", nil)
	r.refused(409, operations.ReasonTripDelivered, "staffO", "POST", path+"/cancel", nil)

	// R17: a delivery time moved into or out of a sent period is 409 billing_period_locked.
	r.exec(`INSERT INTO billing_statements (tenant_id, billing_party_id, invoice_number, period_year, period_month,
		total_amount, withholding_tax_rate, withholding_tax, net_amount, status, generated_at)
		VALUES ($1, $2, 'CJSF-202609-001', 2026, 9, 100, 0.01, 1, 99, 'sent', now())`, r.O, r.partyX)
	got, m := r.call("staffO", "PATCH", path, map[string]any{"deliveredAt": "2026-09-20T03:00:00Z"})
	if got != 409 || m["error"].(map[string]any)["code"] != "billing_period_locked" {
		t.Fatalf("locked period: %d %v", got, m)
	}
	k := len(r.events(trip))
	r.must(200, "staffO", "PATCH", path, map[string]any{"deliveredAt": "2026-10-11T09:00:00Z"})
	if got := routes(r.events(trip)[k:]); !slices.Equal(got, []string{"trip.updated", "trip.delivered_at_changed"}) {
		t.Fatalf("delivery time events %v", got)
	}

	// Cancel: the trip and its open task; bulk: the in-transit ones only.
	task2, trip2 := r.tripOf("SPXTH-0002")
	if d = r.must(200, "staffO", "POST", "/v1/trips/"+trip2+"/cancel", nil); d["status"] != "cancelled" {
		t.Fatalf("cancelled trip: %v", d)
	}
	if r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND status = 'cancelled'`, task2) != 1 {
		t.Fatal("the task of a cancelled trip stays open")
	}
	_, trip3 := r.tripOf("SPXTH-0003")
	_, trip4 := r.tripOf("SPXTH-0004")
	unknown := uuid.NewString()
	d = r.must(200, "staffO", "POST", "/v1/trips/bulk-cancel", map[string]any{"ids": []string{trip3, trip4, trip, unknown, trip3}})
	if c := d["cancelled"].([]any); len(c) != 2 || len(d["failed"].([]any)) != 2 {
		t.Fatalf("bulk: %v", d)
	}
	for _, f := range d["failed"].([]any) {
		m := f.(map[string]any)
		if (m["id"] == trip && m["reason"] != operations.ReasonNotInTransit) || (m["id"] == unknown && m["reason"] != "not_found") {
			t.Fatalf("failed %v", m)
		}
	}
	r.refused(422, "ids required", "staffO", "POST", "/v1/trips/bulk-cancel", map[string]any{"ids": []string{}})
	r.refused(404, "not_found", "staffC", "POST", "/v1/trips/"+trip4+"/cancel", nil)
}

// T31 review (main spec §6.10): moving the plan day of a task whose trip is delivered and billed on the plan axis
// reads the period lock in the update's transaction, like the trip's deliveredAt: into or out of a sent period it is
// 409 billing_period_locked (nothing written, no event; on the write-back path before the Firestore commit); within
// open periods, or for a party billed on the delivery axis, it is the ordinary update with task.plan_date_changed.
func TestPatchTaskPlanDateLock(t *testing.T) {
	statement := func(r *writeWorld) {
		r.exec(`INSERT INTO billing_statements (tenant_id, billing_party_id, invoice_number, period_year, period_month,
			total_amount, withholding_tax_rate, withholding_tax, net_amount, status, generated_at)
			VALUES ($1, $2, 'CJSF-202609-001', 2026, 9, 100, 0.01, 1, 99, 'sent', now())`, r.O, r.partyX)
	}
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	r.exec(`UPDATE billing_parties SET billing_date_basis = 'plan' WHERE id = $1`, r.partyX)
	task, trip := r.tripOf("SPXTH-PLAN1")
	path := "/v1/tasks/" + task
	r.must(200, "staffO", "PATCH", path, map[string]any{"date": "2026-09-30"})
	r.must(200, "staffO", "POST", "/v1/trips/"+trip+"/deliver", map[string]any{"deliveredAt": "2026-09-30T05:00:00Z"})
	statement(r)
	k := len(r.events(task))
	for _, day := range []string{"2026-10-01", "2026-09-29"} {
		got, m := r.call("staffO", "PATCH", path, map[string]any{"date": day})
		if got != 409 || m["error"].(map[string]any)["code"] != "billing_period_locked" ||
			m["error"].(map[string]any)["details"].(map[string]any)["blockedInvoiceNumber"] != "CJSF-202609-001" {
			t.Fatalf("plan day %s: %d %v", day, got, m)
		}
	}
	if len(r.events(task)) != k || r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND plan_date = '2026-09-30'`, task) != 1 {
		t.Fatal("a locked plan-day move wrote")
	}
	// The other fields of the same task still change; a delivered-axis party is not read.
	r.must(200, "staffO", "PATCH", path, map[string]any{"time": "07:00"})
	r.exec(`UPDATE billing_parties SET billing_date_basis = 'delivered' WHERE id = $1`, r.partyX)
	k = len(r.events(task))
	r.must(200, "staffO", "PATCH", path, map[string]any{"date": "2026-10-01"})
	if got := routes(r.events(task)[k:]); !slices.Equal(got, []string{"task.updated", "task.plan_date_changed"}) {
		t.Fatalf("delivered axis: %v", got)
	}
	// Plan axis between open periods.
	r.exec(`UPDATE billing_parties SET billing_date_basis = 'plan' WHERE id = $1`, r.partyX)
	r.exec(`UPDATE trip_records SET billing_date = '2026-10-01T00:00:00+07:00' WHERE id = $1`, trip)
	r.must(200, "staffO", "PATCH", path, map[string]any{"date": "2026-10-02"})

	// The write-back path refuses before the Firestore commit.
	mem := writeback.NewMemory()
	w := newWriteWorld(t, writeOptions{writeBack: &operations.WriteBack{Writer: mem}})
	w.exec(`UPDATE billing_parties SET billing_date_basis = 'plan' WHERE id = $1`, w.partyX)
	wt := w.createTask(w.staffO, func() operations.NewTask {
		n := w.newTask(w.O, time.Date(2026, 9, 29, 17, 0, 0, 0, time.UTC)) // 2026-09-30 Bangkok
		n.LegacyDocID = ptr("task_plan_doc")
		return n
	}())
	in := w.newTrip(wt, "SPXTH-PLAN2")
	in.LegacyDocID = ptr("SPXTH-PLAN2")
	tr := w.createTrip(w.staffO, in)
	w.exec(`UPDATE trip_records SET status = 'delivered', delivered_at = '2026-09-30T05:00:00Z' WHERE id = $1`, tr.ID)
	mem.Put("tasks", "task_plan_doc", map[string]any{"status": "Pending"}, now)
	statement(w)
	before := len(mem.Writes())
	got, m := w.call("staffO", "PATCH", "/v1/tasks/"+wt.ID.String(), map[string]any{"date": "2026-10-01"})
	if got != 409 || m["error"].(map[string]any)["code"] != "billing_period_locked" || len(mem.Writes()) != before {
		t.Fatalf("write-back plan-day move: %d %v, %d writes", got, m, len(mem.Writes())-before)
	}
}

func TestRenameTrip(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	_, trip := r.tripOf("SPXTH-OLD")
	path := "/v1/trips/" + trip + "/rename"
	r.refused(409, "not_delivered", "staffO", "POST", path, map[string]any{"newTripNo": "SPXTH-NEW"})
	r.must(200, "staffO", "POST", "/v1/trips/"+trip+"/deliver", nil)
	r.refused(403, operations.ReasonTenantAdminOnly, "operator", "POST", path, map[string]any{"newTripNo": "SPXTH-NEW"})
	r.refused(422, "newTripNo required", "staffO", "POST", path, map[string]any{})
	r.refused(422, "tripNo slash", "staffO", "POST", path, map[string]any{"newTripNo": "a/b"})
	_, other := r.tripOf("SPXTH-TAKEN")
	_ = other
	r.refused(409, "duplicate_trip_id", "staffO", "POST", path, map[string]any{"newTripNo": "SPXTH-TAKEN"})
	k := len(r.events(trip))
	d := r.must(200, "staffO", "POST", path, map[string]any{"newTripNo": "SPXTH-NEW"})
	if d["tripNo"] != "SPXTH-NEW" {
		t.Fatalf("renamed: %v", d)
	}
	if r.count(`SELECT count(*) FROM trip_no_history WHERE trip_id = $1 AND old_trip_no = 'SPXTH-OLD' AND new_trip_no = 'SPXTH-NEW'`, trip) != 1 ||
		r.count(`SELECT count(*) FROM security_events WHERE event_type = 'trip_record_renamed' AND tenant_id = $1`, r.O) != 1 {
		t.Fatal("rename history or audit")
	}
	evs := r.events(trip)[k:]
	if len(evs) != 1 || evs[0].route != "trip.updated" || evs[0].payload["previousTripNo"] != "SPXTH-OLD" {
		t.Fatalf("rename events %v", evs)
	}
	r.refused(404, "not_found", "staffA", "POST", path, map[string]any{"newTripNo": "SPXTH-X"})
}

// --- standby and incidents ----------------------------------------------------------------------------------------

func TestStandbyBackfillAndIncident(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	task, trip := r.tripOf("SPXTH-SB1")
	body := map[string]any{"driverId": r.dO1.String(), "taskId": task, "tripId": trip, "startLocation": "ALANG-A",
		"endLocation": "SOCE", "startedAt": "2026-10-10T01:00:00Z", "endedAt": "2026-10-10T03:30:00Z", "note": "รอโหลด",
		"photos": []any{map[string]any{"type": "site_photo", "key": "file:1"}}}
	d := r.must(201, "staffO", "POST", "/v1/standby/backfill", body)
	if d["tenantSource"] != "task" || d["backfilledByAdmin"] != true || d["durationMinutes"] != 150.0 ||
		d["status"] != "completed" || len(d["photos"].([]any)) != 1 {
		t.Fatalf("standby: %v", d)
	}
	if r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND status = 'completed'`, task) != 1 ||
		r.count(`SELECT count(*) FROM trip_records WHERE id = $1 AND status = 'standby'`, trip) != 1 {
		t.Fatal("standby side effects")
	}
	if got := routes(r.events(d["id"].(string))); !slices.Equal(got, []string{"standby.completed"}) {
		t.Fatalf("standby events %v", got)
	}
	// Driver only: the driver's tenant.
	only := map[string]any{"driverId": r.dA1.String(), "startLocation": "A", "endLocation": "B",
		"startedAt": "2026-10-10T01:00:00Z", "endedAt": "2026-10-10T02:00:00Z"}
	if d = r.must(201, "staffO", "POST", "/v1/standby/backfill", only); d["tenantId"] != r.A.String() || d["tenantSource"] != "driver" {
		t.Fatalf("driver-only standby: %v", d)
	}
	r.refused(403, "permission_denied", "operator", "POST", "/v1/standby/backfill", body)
	r.refused(422, "endedAt before_start", "staffO", "POST", "/v1/standby/backfill", map[string]any{"driverId": r.dO1.String(),
		"startLocation": "A", "endLocation": "B", "startedAt": "2026-10-10T03:00:00Z", "endedAt": "2026-10-10T02:00:00Z"})
	r.refused(422, "driverId not_found", "staffC", "POST", "/v1/standby/backfill", only)

	// The admin incident: on the trip (its tenant), the reporter recorded, incident.created.
	inc := r.must(201, "staffO", "POST", "/v1/incidents", map[string]any{"tripId": trip, "delayCause": "incident_cause_tire",
		"description": "ยางแตก", "lat": 13.5, "lng": 100.5, "truckId": r.truckO.String(),
		"photoKeys": map[string]any{"map": "file:2"}})
	if inc["tenantSource"] != "trip" || inc["reportedByKind"] != "admin" || inc["tripNo"] != "SPXTH-SB1" ||
		inc["truckLicensePlate"] != "1กข-1234" || inc["mapPhoto"] == nil || inc["driverId"] != r.dO1.String() {
		t.Fatalf("incident: %v", inc)
	}
	if got := routes(r.events(inc["id"].(string))); !slices.Equal(got, []string{"incident.created"}) {
		t.Fatalf("incident events %v", got)
	}
	r.refused(422, "description required", "staffO", "POST", "/v1/incidents", map[string]any{"tripId": trip})
	r.refused(422, "tripId required", "staffO", "POST", "/v1/incidents", map[string]any{"description": "x"})
	r.refused(403, "permission_denied", "user", "POST", "/v1/incidents", map[string]any{"tripId": trip, "description": "x"})
}

// --- the write-back -------------------------------------------------------------------------------------------------

// A Firestore-owned domain without a writer never falls back to PostgreSQL.
func TestWriteBackUnconfigured(t *testing.T) {
	r := newWriteWorld(t, writeOptions{})
	r.refused(503, operations.ReasonWriteBackUnconfigured, "staffO", "POST", "/v1/tasks", r.taskBody(nil))
	if r.count(`SELECT count(*) FROM tasks`) != 0 || r.count(`SELECT count(*) FROM outbox_events`) != 0 {
		t.Fatal("PostgreSQL was written in place of Firestore")
	}
}

// acks is a stand-in of the mirror's acknowledgement: the update time the mirror applied per document.
type acks struct {
	mu  sync.Mutex
	got map[string]time.Time
}

func (a *acks) WaitMirrorAck(_ context.Context, collection, docID string, want time.Time, _ time.Duration) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	at, ok := a.got[collection+"/"+docID]
	return ok && !at.Before(want), nil
}

// upTo acknowledges the document up to at; set acknowledges every later write too.
func (a *acks) upTo(collection, docID string, at time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.got == nil {
		a.got = map[string]time.Time{}
	}
	a.got[collection+"/"+docID] = at
}

func (a *acks) set(collection, docID string) {
	a.upTo(collection, docID, time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC))
}

func TestWriteBackTasks(t *testing.T) {
	mem := writeback.NewMemory()
	ack := &acks{}
	r := newWriteWorld(t, writeOptions{writeBack: &operations.WriteBack{Writer: mem, Acks: ack}})
	// The masters as the projection left them (legacy_doc_id minted, the driver's Firebase uid).
	r.exec(`UPDATE drivers SET legacy_doc_id = 'drv_o1', legacy_auth_uid = 'uid_o1', full_name_th = 'สมชาย ใจดี' WHERE id = $1`, r.dO1)
	r.exec(`UPDATE drivers SET legacy_doc_id = 'drv_o2', legacy_auth_uid = 'uid_o2' WHERE id = $1`, r.dO2)
	r.exec(`UPDATE trucks SET legacy_doc_id = 'trk_o' WHERE id = $1`, r.truckO)
	r.exec(`UPDATE customers SET legacy_doc_id = 'cust_cjsf' WHERE code = 'CJSF'`)

	// Create, not acknowledged within the wait: 202 with the document; nothing in PostgreSQL; the writer's push queued.
	d := r.must(202, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dO1.String(), "truckId": r.truckO.String()}))
	id := d["id"].(string)
	doc, ok := mem.Doc("tasks", id)
	if !ok || d["docId"] != id || d["pending"] != true || doc["taskId"] != "FM-12102026-001" || doc["taskType"] != "FIRST_MILE" ||
		doc["status"] != "Assigned" || doc["driverId"] != "drv_o1" || doc["driverName"] != "สมชาย ใจดี" ||
		doc["dateStr"] != "12102026" || doc["time"] != "09:30" || doc["billingCustomerId"] != "cust_cjsf" ||
		doc["billingCustomerCode"] != "CJSF" || doc["truckId"] != "trk_o" || doc["licensePlate"] != "1กข-1234" ||
		doc["truckType"] != "6WH" || doc["runOrder"] != int64(1) || doc["jobCategory"] != "PRIMARY" ||
		doc["sourceHub"] != "ALANG-A" || doc["isMultiDelivery"] != false {
		t.Fatalf("document %v\nanswer %v", doc, d)
	}
	if w := mem.Writes(); len(w) != 1 {
		t.Fatalf("writes %v", w)
	}
	if r.count(`SELECT count(*) FROM tasks`) != 0 {
		t.Fatal("the write-back wrote PostgreSQL")
	}
	evs := r.events(id)
	if len(evs) != 1 || evs[0].route != "task.assigned" || evs[0].headers["origin"] != "writeback" || len(evs[0].topics) != 0 ||
		evs[0].payload["driverId"] != r.dO1.String() {
		t.Fatalf("write-back events %v", evs)
	}
	// The event names the document and the update time of the commit: notify.fcm holds the push until the mirror
	// acknowledged it (T31 review).
	if at, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(evs[0].payload["writeUpdateTime"])); evs[0].payload["legacyDocId"] != id ||
		at.IsZero() || fmt.Sprint(d["updateTime"]) == "" || !strings.HasPrefix(fmt.Sprint(d["updateTime"]), at.Format("2006-01-02T15:04:05")) {
		t.Fatalf("write-back event payload %v, echo updateTime %v", evs[0].payload, d["updateTime"])
	}

	// The mirror loaded it (here: the row the load transforms would write, under the minted id) and acknowledged it:
	// the answer is the task as GET reads it.
	created, _ := time.Parse(time.RFC3339Nano, d["updateTime"].(string))
	mirror := func(docID string) {
		r.exec(`INSERT INTO tasks (id, legacy_doc_id, tenant_id, tenant_source, task_no, task_type, status, plan_at, source_hub_raw,
			destination_raw, billing_party_id, driver_id, run_order, job_category)
			VALUES ($1::uuid, $5::text, $2, 'driver', 'FM-12102026-001', 'first_mile', 'assigned', '2026-10-11T17:00:00Z', 'ALANG-A', 'SOCE', $3, $4, 1, 'PRIMARY')`,
			docID, r.O, r.partyX, r.dO1, docID)
		ack.upTo("tasks", docID, created)
	}
	mirror(id)
	path := "/v1/tasks/" + id

	// PATCH: only the changed fields reach the document; PostgreSQL is not written.
	before := len(mem.Writes())
	r.must(202, "staffO", "PATCH", path, map[string]any{"jobCategory": "SUPPLEMENTARY"})
	w := mem.Writes()[before]
	if len(w.Fields) != 2 || w.Fields["jobCategory"] != "SUPPLEMENTARY" || w.Fields["updatedAt"] == nil {
		t.Fatalf("patch write %v", w.Fields)
	}
	if r.count(`SELECT count(*) FROM tasks WHERE id = $1 AND job_category = 'PRIMARY'`, id) != 1 {
		t.Fatal("the write-back changed PostgreSQL")
	}
	// The ack of the patched document: 200 with the row.
	ack.set("tasks", id)
	before = len(mem.Writes())
	got, m := r.call("staffO", "PATCH", path, map[string]any{"driverId": r.dO2.String()})
	if got != 200 {
		t.Fatalf("acknowledged patch: %d %v", got, m)
	}
	w = mem.Writes()[before]
	if _, status := w.Fields["status"]; w.Fields["driverId"] != "drv_o2" || status || w.Fields["runOrder"] == nil {
		t.Fatalf("driver change %v", w.Fields)
	}
	if evs := r.events(id); evs[len(evs)-1].route != "task.reassigned" || evs[len(evs)-1].headers["origin"] != "writeback" ||
		evs[len(evs)-1].payload["legacyDocId"] != id || evs[len(evs)-1].payload["writeUpdateTime"] == nil {
		t.Fatalf("reassignment event %v", evs[len(evs)-1])
	}
	// Cancel and stops write their documents.
	r.must(200, "staffO", "POST", path+"/cancel", map[string]any{"reason": "x"})
	if doc, _ := mem.Doc("tasks", id); doc["status"] != "Cancelled" || doc["cancelReason"] != "x" {
		t.Fatalf("cancel document %v", doc)
	}
	r.exec(`UPDATE tasks SET status = 'assigned' WHERE id = $1`, id)
	r.must(200, "staffO", "POST", path+"/stops", map[string]any{"destination": "bkk-2"})
	if doc, _ := mem.Doc("tasks", id); doc["isMultiDelivery"] != true || len(doc["deliveryStops"].([]any)) != 2 {
		t.Fatalf("stops document %v", doc)
	}
	r.refused(409, "duplicate_destination", "staffO", "POST", path+"/stops", map[string]any{"destination": "BKK-2"})
	// A master row without a Firestore id is refused before anything is written.
	before = len(mem.Writes())
	r.refused(409, operations.ReasonNotProjected, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dA1.String()}))
	if len(mem.Writes()) != before {
		t.Fatal("a refused write-back wrote Firestore")
	}
	// Firestore down: 503, nothing appended.
	mem.Fail(fmt.Errorf("firestore down"))
	r.refused(503, "write_back", "staffO", "POST", "/v1/tasks", r.taskBody(nil))
	mem.Fail(nil)
}

// T31 review: a write-back import is one Firestore commit, all of it or nothing: more than operations.WriteBackBatch
// rows is 422 rows too_many (details.max) before anything is numbered, and a failed commit leaves no document, so the
// retry the 503 invites imports nothing twice.
func TestWriteBackImportIsOneCommit(t *testing.T) {
	mem := writeback.NewMemory()
	r := newWriteWorld(t, writeOptions{writeBack: &operations.WriteBack{Writer: mem}})
	r.exec(`UPDATE customers SET legacy_doc_id = 'cust_cjsf' WHERE code = 'CJSF'`)
	rows := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = map[string]any{"date": "2026-10-14", "sourceHub": "ALANG-A", "destination": "SOCE", "billingPartyId": r.partyX.String()}
		}
		return out
	}
	got, m := r.call("staffO", "POST", "/v1/tasks/import", map[string]any{"taskType": "first_mile", "rows": rows(operations.WriteBackBatch + 1)})
	fs, _ := m["error"].(map[string]any)["details"].(map[string]any)["fields"].([]any)
	if got != 422 || reasonOf(m) != "rows too_many" || len(fs) != 1 || fs[0].(map[string]any)["params"].(map[string]any)["max"] != float64(operations.WriteBackBatch) {
		t.Fatalf("a write-back import past one commit: %d %v", got, m)
	}
	if len(mem.Writes()) != 0 || r.count(`SELECT count(*) FROM task_number_counters`) != 0 {
		t.Fatal("a refused import wrote or numbered")
	}
	mem.Fail(fmt.Errorf("firestore down"))
	r.refused(503, "write_back", "staffO", "POST", "/v1/tasks/import", map[string]any{"taskType": "first_mile", "rows": rows(operations.WriteBackBatch)})
	mem.Fail(nil)
	if len(mem.Writes()) != 0 {
		t.Fatal("a failed commit left documents")
	}
	d := r.must(202, "staffO", "POST", "/v1/tasks/import", map[string]any{"taskType": "first_mile", "rows": rows(operations.WriteBackBatch)})
	if len(d["created"].([]any)) != operations.WriteBackBatch || len(mem.Writes()) != operations.WriteBackBatch {
		t.Fatalf("the retry: %d created, %d documents", len(d["created"].([]any)), len(mem.Writes()))
	}
}

// T31 review: a write-back's task reaches PostgreSQL only when the mirror loads it, so the queue positions it hands
// out are held (cache:run_order:{driverId}): two creations of one driver before the mirror ran, and an assignment of a
// mirrored task to that driver, get 1, 2 and 3; a position set by the body raises the hold.
func TestWriteBackRunOrders(t *testing.T) {
	mem := writeback.NewMemory()
	rdb, ks := cachetest.NewClient(t)
	r := newWriteWorld(t, writeOptions{writeBack: &operations.WriteBack{Writer: mem, RunOrders: cache.New(rdb, ks)}})
	r.exec(`UPDATE drivers SET legacy_doc_id = 'drv_o1', legacy_auth_uid = 'uid_o1' WHERE id = $1`, r.dO1)
	r.exec(`UPDATE customers SET legacy_doc_id = 'cust_cjsf' WHERE code = 'CJSF'`)
	runOrder := func(d map[string]any) any {
		doc, _ := mem.Doc("tasks", d["docId"].(string))
		return doc["runOrder"]
	}
	first := r.must(202, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dO1.String()}))
	second := r.must(202, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dO1.String()}))
	if runOrder(first) != int64(1) || runOrder(second) != int64(2) {
		t.Fatalf("two creations before the mirror: %v %v", runOrder(first), runOrder(second))
	}
	mirrored := r.createTask(r.staffO, func() operations.NewTask {
		n := r.newTask(r.O, now)
		n.LegacyDocID = ptr("task_ro")
		return n
	}())
	mem.Put("tasks", "task_ro", map[string]any{"status": "Pending"}, now)
	r.must(202, "staffO", "PATCH", "/v1/tasks/"+mirrored.ID.String(), map[string]any{"driverId": r.dO1.String()})
	if doc, _ := mem.Doc("tasks", "task_ro"); doc["runOrder"] != int64(3) {
		t.Fatalf("an assignment after them: %v", doc["runOrder"])
	}
	// (the row is still driverless in PostgreSQL: the mirror has not applied the assignment)
	r.must(202, "staffO", "PATCH", "/v1/tasks/"+mirrored.ID.String(), map[string]any{"driverId": r.dO1.String(), "runOrder": 9})
	third := r.must(202, "staffO", "POST", "/v1/tasks", r.taskBody(map[string]any{"driverId": r.dO1.String()}))
	if runOrder(third) != int64(10) {
		t.Fatalf("after a set position: %v", runOrder(third))
	}
}

func TestWriteBackTripsAndRecords(t *testing.T) {
	mem := writeback.NewMemory()
	ack := &acks{}
	r := newWriteWorld(t, writeOptions{writeBack: &operations.WriteBack{Writer: mem, Acks: ack}})
	r.exec(`UPDATE drivers SET legacy_doc_id = 'drv_o1', legacy_auth_uid = 'uid_o1' WHERE id = $1`, r.dO1)
	// The admin's Firebase uid (an identity column: the system context writes it).
	r.exec(`WITH c AS (SELECT set_config('app.bypass_tenant', 'on', true))
		UPDATE users SET legacy_auth_uid = 'uid_staff' FROM c WHERE id = $1`, r.userO)
	// The mirrored rows of a running job (as the load wrote them) and their documents.
	task := r.createTask(r.staffO, func() operations.NewTask {
		n := r.newTask(r.O, now)
		n.DriverID, n.Status, n.LegacyDocID = &r.dO1, operations.TaskAssigned, ptr("task_doc")
		return n
	}())
	in := r.newTrip(task, "SPXTH-WB1")
	in.LegacyDocID = ptr("SPXTH-WB1")
	trip := r.createTrip(r.staffO, in)
	r.exec(`UPDATE drivers SET active_task_id = $1 WHERE id = $2`, task.ID, r.dO1)
	mem.Put("tasks", "task_doc", map[string]any{"status": "Assigned", "destination": "SOCE"}, now)
	mem.Put("trip_records", "SPXTH-WB1", map[string]any{"status": "in_transit", "spxTripId": "SPXTH-WB1", "driverId": "uid_o1",
		"createdAt": dump.Timestamp{Time: now}, "photos": []any{map[string]any{"type": "seal", "url": "https://x/y"}}}, now)
	mem.Put("drivers", "drv_o1", map[string]any{"activeTruck": map[string]any{"taskId": "task_doc"}}, now)
	inc := r.id(`INSERT INTO incident_reports (legacy_doc_id, tenant_id, tenant_source, trip_id, description, reported_by_kind)
		VALUES ('inc_doc', $1, 'trip', $2, 'x', 'driver') RETURNING id`, r.O, trip.ID)
	_ = inc
	mem.Put("incidentReport", "inc_doc", map[string]any{"tripId": "SPXTH-WB1"}, now)

	path := "/v1/trips/" + trip.ID.String()
	r.refused(422, "photos write_back_unsupported", "staffO", "PATCH", path, map[string]any{"photos": []any{map[string]any{"type": "seal", "key": "file:0"}}})
	d := r.must(202, "staffO", "PATCH", path, map[string]any{"sealCode": "S9"})
	if d["collection"] != "trip_records" || d["docId"] != "SPXTH-WB1" {
		t.Fatalf("patch echo %v", d)
	}
	// Deliver: the trip, its task and the driver's activeTruck in one commit.
	before := len(mem.Writes())
	r.must(202, "staffO", "POST", path+"/deliver", map[string]any{"deliveredAt": "2026-10-11T08:00:00Z"})
	ws := mem.Writes()[before:]
	if len(ws) != 3 || ws[0].Fields["status"] != "delivered" || ws[0].Fields["deliveredVia"] != "admin_web" ||
		ws[1].DocID != "task_doc" || ws[1].Fields["status"] != "Completed" || ws[2].DocID != "drv_o1" {
		t.Fatalf("deliver writes %v", ws)
	}
	if doc, _ := mem.Doc("drivers", "drv_o1"); doc["activeTruck"] != nil {
		t.Fatalf("activeTruck kept %v", doc)
	}
	if r.count(`SELECT count(*) FROM trip_records WHERE id = $1 AND status = 'in_transit'`, trip.ID) != 1 {
		t.Fatal("the write-back changed PostgreSQL")
	}
	// The rename: copy (with the provenance) + incident re-point + delete of the old document, then the audit row.
	r.exec(`UPDATE trip_records SET status = 'delivered', delivered_at = now() WHERE id = $1`, trip.ID)
	r.refused(403, operations.ReasonTenantAdminOnly, "operator", "POST", path+"/rename", map[string]any{"newTripNo": "SPXTH-WB2"})
	d = r.must(202, "staffO", "POST", path+"/rename", map[string]any{"newTripNo": "SPXTH-WB2"})
	if d["docId"] != "SPXTH-WB2" {
		t.Fatalf("rename echo %v", d)
	}
	if _, ok := mem.Doc("trip_records", "SPXTH-WB1"); ok {
		t.Fatal("the old trip document was kept")
	}
	nd, _ := mem.Doc("trip_records", "SPXTH-WB2")
	idoc, _ := mem.Doc("incidentReport", "inc_doc")
	if nd["spxTripId"] != "SPXTH-WB2" || nd["renamedFromTripId"] != "SPXTH-WB1" || nd["status"] != "delivered" || idoc["tripId"] != "SPXTH-WB2" ||
		nd["renamedBy"] != "uid_staff" ||
		nd["createdAt"] != (dump.Timestamp{Time: now}) || len(nd["photos"].([]any)) != 1 {
		t.Fatalf("renamed %v incident %v", nd, idoc)
	}
	if r.count(`SELECT count(*) FROM security_events WHERE event_type = 'trip_record_renamed'`) != 1 {
		t.Fatal("no audit row of the write-back rename")
	}
	// Standby backfill and the admin incident create their documents under minted ids.
	sb := r.must(202, "staffO", "POST", "/v1/standby/backfill", map[string]any{"driverId": r.dO1.String(), "startLocation": "A",
		"endLocation": "B", "startedAt": "2026-10-10T01:00:00Z", "endedAt": "2026-10-10T02:00:00Z"})
	if doc, ok := mem.Doc("standby_records", sb["id"].(string)); !ok || doc["driverId"] != "uid_o1" || doc["durationMinutes"] != int64(60) ||
		doc["backfilledByAdmin"] != true {
		t.Fatalf("standby document %v", doc)
	}
	ic := r.must(202, "staffO", "POST", "/v1/incidents", map[string]any{"driverId": r.dO1.String(), "description": "ยางแตก"})
	if doc, ok := mem.Doc("incidentReport", ic["id"].(string)); !ok || doc["reportedBy"] != "admin" || doc["driverId"] != "uid_o1" ||
		doc["reportedByUid"] != "uid_staff" {
		t.Fatalf("incident document %v", doc)
	}
	// A login born in Go has no Firebase uid: its users.id (the mirror resolves either).
	ic = r.must(202, "platformO", "POST", "/v1/incidents", map[string]any{"driverId": r.dO1.String(), "description": "x"})
	if doc, _ := mem.Doc("incidentReport", ic["id"].(string)); doc["reportedByUid"] != r.ps["platformO"].UserID.String() {
		t.Fatalf("incident of a Go login %v", doc)
	}
	// Bulk cancel of a mirrored in-transit trip.
	task2 := r.createTask(r.staffO, func() operations.NewTask {
		n := r.newTask(r.O, now)
		n.LegacyDocID = ptr("task_doc2")
		return n
	}())
	in2 := r.newTrip(task2, "SPXTH-WB3")
	in2.LegacyDocID = ptr("SPXTH-WB3")
	trip2 := r.createTrip(r.staffO, in2)
	mem.Put("tasks", "task_doc2", map[string]any{"status": "Pending"}, now)
	mem.Put("trip_records", "SPXTH-WB3", map[string]any{"status": "in_transit"}, now)
	bc := r.must(202, "staffO", "POST", "/v1/trips/bulk-cancel", map[string]any{"ids": []string{trip2.ID.String(), trip.ID.String()}})
	if len(bc["cancelled"].([]any)) != 1 || len(bc["failed"].([]any)) != 1 || bc["pending"] != true {
		t.Fatalf("bulk %v", bc)
	}
	if doc, _ := mem.Doc("tasks", "task_doc2"); doc["status"] != "Cancelled" {
		t.Fatalf("bulk task %v", doc)
	}
}
