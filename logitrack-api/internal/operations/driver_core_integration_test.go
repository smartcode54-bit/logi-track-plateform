//go:build integration

package operations_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The APK 4.x core loop below /v1/mobile (T55, spec mobile-go-core-loop: R19, R22-R25, AC3-AC6, AC9, AC19) against
// PostgreSQL 18 as logitrack_app under the driver's RLS: the queue, the queue gate of the check-in, the check-in with
// its truck and evidence, the loading with the duplicate checks, the delivery, the resume reads, and the refusals.
// The Idempotency-Key middleware has its own tests (internal/platform/httpx/idempotency); here it is a pass-through and
// the replays exercised are the state-based ones of the services (a replay after IDEMPOTENCY_TTL).
func TestDriverCoreLoop(t *testing.T) {
	r := newWriteWorld(t, writeOptions{pgOwned: true})
	auth := func(c fiber.Ctx) error {
		authz.SetPrincipal(c, r.ps[c.Get("X-Test-Principal")])
		return c.Next()
	}
	next := func(c fiber.Ctx) error { return c.Next() }
	operations.DriverRoutes(r.svc, auth, next)(r.app.Group("/v1/mobile"))
	fs, err := fleet.New(fleet.Deps{Pool: r.pool, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	fleet.DriverRoutes(fs, auth)(r.app.Group("/v1/mobile"))
	const me = "driverO1"

	task := func(planAt, created time.Time) uuid.UUID {
		t.Helper()
		return r.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw,
			source_hub_id, destination_hub_id, driver_id, truck_id, truck_type, license_plate_snapshot, billing_party_id,
			created_at)
			VALUES ($1, 'form', 'first_mile', 'assigned', $2, 'ALANG-A', 'SOCE', $3, $4, $5, $6, '6WH', '1กข-1234', $7, $8)
			RETURNING id`, r.O, planAt, r.hubSrc, r.hubDst, r.dO1, r.truckO, r.partyX, created)
	}
	first := task(now.Add(-2*time.Hour), now.Add(-48*time.Hour))
	second := task(now.Add(2*time.Hour), now.Add(-24*time.Hour))
	other := r.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw,
		driver_id) VALUES ($1, 'form', 'first_mile', 'assigned', $2, 'ALANG-A', 'SOCE', $3) RETURNING id`, r.O, now, r.dO2)

	// R19: the open queue in ADR 0028 order.
	got, m := r.call(me, http.MethodGet, "/v1/mobile/tasks", nil)
	items, _ := m["data"].([]any)
	if got != http.StatusOK || len(items) != 2 || items[0].(map[string]any)["id"] != first.String() {
		t.Fatalf("queue: %d %v", got, m)
	}
	r.refused(http.StatusForbidden, authz.CodePermissionDenied, "staffO", http.MethodGet, "/v1/mobile/tasks", nil)

	at := now.Format(time.RFC3339)
	checkIn := func(truck uuid.UUID, when string) map[string]any {
		return map[string]any{"truckId": truck, "checkInAt": when, "lat": 13.7, "lng": 100.5, "photoKey": "file:0",
			"appScreenshotKey": "file:1"}
	}
	path := func(id uuid.UUID, rest string) string { return "/v1/mobile/tasks/" + id.String() + rest }

	// AC19 / Q2: the later task waits for the earlier one.
	got, m = r.call(me, http.MethodPost, path(second, "/check-in"), checkIn(r.truckO, at))
	if got != http.StatusConflict || reasonOf(m) != operations.ReasonQueueBlocked {
		t.Fatalf("queue gate: %d %v", got, m)
	}
	if e := m["error"].(map[string]any)["details"].(map[string]any); e["blockingTaskId"] != first.String() {
		t.Fatalf("queue gate names %v, want %s", e["blockingTaskId"], first)
	}
	// Refusals: another driver's task, a truck the driver cannot see, a helper, a missing screenshot (ADR 0019).
	r.refused(http.StatusForbidden, operations.CodeNotTaskOwner, me, http.MethodPost, path(other, "/check-in"),
		checkIn(r.truckO, at))
	r.refused(http.StatusUnprocessableEntity, "truckId not_found", me, http.MethodPost, path(first, "/check-in"),
		checkIn(r.truckA, at))
	withHelper := checkIn(r.truckO, at)
	withHelper["helperDriverId"] = r.dO2.String()
	r.refused(http.StatusUnprocessableEntity, "helperDriverId not_supported", me, http.MethodPost, path(first, "/check-in"),
		withHelper)
	noApp := checkIn(r.truckO, at)
	delete(noApp, "appScreenshotKey")
	r.refused(http.StatusUnprocessableEntity, "appScreenshotKey required", me, http.MethodPost, path(first, "/check-in"), noApp)
	unclassed := r.id(`INSERT INTO trucks (tenant_id, tenant_source, license_plate, province, status, brand, model, year, color,
		type_raw) VALUES ($1, 'self', '3กข-0001', 'กรุงเทพมหานคร', 'active', 'ISUZU', 'X', 2020, 'white', 'Mystery') RETURNING id`, r.O)
	r.refused(http.StatusUnprocessableEntity, "truckId "+operations.ReasonUnknownVehicleClass, me, http.MethodPost,
		path(first, "/check-in"), checkIn(unclassed, at))
	// The check-in picker (GET /v1/mobile/trucks): the driver's own tenant (a driver has no staff tenant reach), only
	// trucks with a vehicle class, never another tenant's.
	got, m = r.call(me, http.MethodGet, "/v1/mobile/trucks", nil)
	picked := map[string]bool{}
	if list, _ := m["data"].([]any); got == http.StatusOK {
		for _, it := range list {
			picked[it.(map[string]any)["id"].(string)] = true
		}
	}
	if got != http.StatusOK || !picked[r.truckO.String()] || picked[unclassed.String()] || picked[r.truckA.String()] {
		t.Fatalf("trucks picker: %d %v", got, m)
	}

	// AC4: the check-in.
	d := r.must(http.StatusOK, me, http.MethodPost, path(first, "/check-in"), checkIn(r.truckO, at))
	if d["status"] != "checked_in" {
		t.Fatalf("check-in: %v", d)
	}
	var activeTask, activeTruck *uuid.UUID
	if err := r.etl.QueryRow(context.Background(), `SELECT active_task_id, active_truck_id FROM drivers WHERE id = $1`,
		r.dO1).Scan(&activeTask, &activeTruck); err != nil {
		t.Fatal(err)
	}
	if activeTask == nil || *activeTask != first || activeTruck == nil || *activeTruck != r.truckO {
		t.Fatalf("active job: task %v truck %v", activeTask, activeTruck)
	}
	var checkedInEvents int
	if err := r.etl.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE routing_key = 'task.checked_in'
		AND aggregate_id = $1`, first.String()).Scan(&checkedInEvents); err != nil {
		t.Fatal(err)
	}
	if checkedInEvents != 1 {
		t.Fatalf("task.checked_in events: %d", checkedInEvents)
	}
	// A replay at the same instant answers 200; another instant is task_not_open.
	r.must(http.StatusOK, me, http.MethodPost, path(first, "/check-in"), checkIn(r.truckO, at))
	r.refused(http.StatusConflict, operations.ReasonTaskNotOpen, me, http.MethodPost, path(first, "/check-in"),
		checkIn(r.truckO, now.Add(time.Minute).Format(time.RFC3339)))

	// AC5: the loading (single stop); the check-in screenshot becomes the trip's checkin_app photo.
	load := func(taskID uuid.UUID, tripNo, seal string) map[string]any {
		return map[string]any{"tripNo": tripNo, "taskId": taskID.String(), "sealCode": seal, "parcelCount": 120,
			"photos": []any{map[string]any{"type": "pre_close", "key": "file:2"}}}
	}
	r.refused(http.StatusConflict, operations.ReasonTaskNotCheckedIn, me, http.MethodPost, "/v1/mobile/trips",
		load(second, "ZXDRV0000002", "SEAL-2"))
	tr := r.must(http.StatusCreated, me, http.MethodPost, "/v1/mobile/trips", load(first, "ZXDRV0000001", "SEAL-1"))
	photos := map[string]bool{}
	for _, p := range tr["photos"].([]any) {
		photos[p.(map[string]any)["type"].(string)] = true
	}
	if tr["status"] != "in_transit" || !photos["pre_close"] || !photos["checkin_app"] || tr["origin"] != "ALANG-A" {
		t.Fatalf("trip: %v", tr)
	}
	r.refused(http.StatusConflict, operations.ReasonTaskNotCheckedIn, me, http.MethodPost, "/v1/mobile/trips",
		load(first, "ZXDRV0000003", "SEAL-3")) // the task is in transit now
	pending := r.must(http.StatusOK, me, http.MethodGet, "/v1/mobile/trips/pending-in-transit", nil)
	if pending["tripNo"] != "ZXDRV0000001" {
		t.Fatalf("pending: %v", pending)
	}

	// AC6: the delivery by trip number; a replay at the same instant is 200, another instant trip_not_open.
	deliver := map[string]any{"deliveredAt": now.Add(3 * time.Hour).Format(time.RFC3339),
		"photos": []any{map[string]any{"type": "arrived", "key": "file:3"}}}
	dv := r.must(http.StatusOK, me, http.MethodPost, "/v1/mobile/trips/ZXDRV0000001/deliver", deliver)
	if dv["status"] != "delivered" {
		t.Fatalf("deliver: %v", dv)
	}
	var via string
	var taskStatus string
	if err := r.etl.QueryRow(context.Background(), `SELECT r.delivered_via, t.status FROM trip_records r JOIN tasks t
		ON t.id = r.task_id WHERE r.trip_no = 'ZXDRV0000001'`).Scan(&via, &taskStatus); err != nil {
		t.Fatal(err)
	}
	if via != operations.DeliveredViaMobile || taskStatus != "completed" {
		t.Fatalf("delivered via %s, task %s", via, taskStatus)
	}
	if err := r.etl.QueryRow(context.Background(), `SELECT active_task_id FROM drivers WHERE id = $1`, r.dO1).
		Scan(&activeTask); err != nil || activeTask != nil {
		t.Fatalf("active job after delivery: %v %v", activeTask, err)
	}
	r.must(http.StatusOK, me, http.MethodPost, "/v1/mobile/trips/ZXDRV0000001/deliver", deliver)
	other4 := map[string]any{"deliveredAt": now.Add(4 * time.Hour).Format(time.RFC3339)}
	r.refused(http.StatusConflict, operations.ReasonTripNotOpen, me, http.MethodPost, "/v1/mobile/trips/ZXDRV0000001/deliver", other4)
	if byNo := r.must(http.StatusOK, me, http.MethodGet, "/v1/mobile/trips/by-no/ZXDRV0000001", nil); byNo["status"] != "delivered" {
		t.Fatalf("by-no: %v", byNo)
	}
	r.refused(http.StatusNotFound, "not_found", me, http.MethodGet, "/v1/mobile/trips/by-no/NOSUCHTRIP", nil)

	// The queue moved on: the second task checks in; its loading meets the taken number and the taken seal.
	r.must(http.StatusOK, me, http.MethodPost, path(second, "/check-in"), checkIn(r.truckO, now.Add(5*time.Hour).Format(time.RFC3339)))
	r.refused(http.StatusConflict, operations.CodeDuplicateTripID, me, http.MethodPost, "/v1/mobile/trips",
		load(second, "ZXDRV0000001", "SEAL-9"))
	got, m = r.call(me, http.MethodPost, "/v1/mobile/trips", load(second, "ZXDRV0000009", "SEAL-1"))
	if got != http.StatusConflict {
		t.Fatalf("duplicate seal: %d %v", got, m)
	}
}

// R27 / AC9: while operations is Firestore-owned the driver writes are refused before any write (no write-back).
func TestDriverCoreLoopRefusesWriteBack(t *testing.T) {
	r := newWriteWorld(t, writeOptions{writeBack: &operations.WriteBack{Writer: writeback.NewMemory()}})
	ctx := context.Background()
	p := r.ps["driverO1"]
	for name, call := range map[string]func() error{
		"check-in": func() error {
			_, err := r.svc.CheckInForDriver(ctx, p, "rid", uuid.NewString(), []byte(`{}`))
			return err
		},
		"loading": func() error {
			_, err := r.svc.CreateTripForDriver(ctx, p, "rid", "", []byte(`{}`))
			return err
		},
		"deliver": func() error {
			_, err := r.svc.DeliverTripForDriver(ctx, p, "rid", "ZX1", []byte(`{}`))
			return err
		},
	} {
		if code, detail := errCode(call()); code != "invalid_argument" || detail != "photos "+operations.ReasonWriteBackUnsupported {
			t.Errorf("%s in write-back mode: %s %s", name, code, detail)
		}
	}
}
