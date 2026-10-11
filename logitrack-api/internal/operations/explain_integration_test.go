//go:build integration

package operations_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
)

// opsTables are the tables of 0004_operations this layer reads and writes: a plan may never read one of them by a
// sequential scan.
var opsTables = []string{"tasks", "task_delivery_stops", "task_number_counters", "trip_records", "trip_no_history",
	"trip_delivery_stops", "trip_photos", "standby_records", "standby_photos", "incident_reports"}

// volume loads a few thousand rows over three tenants and four months (as logitrack_etl) and analyses them, so the
// planner has statistics of the shape production has (mostly completed work, a few active rows per driver).
func (w *world) volume() {
	w.exec(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile)
		SELECT (ARRAY[$1, $2, $3]::uuid[])[1 + i % 3], 'self', 'D' || i, 'X', '08' || lpad(i::text, 8, '0')
		FROM generate_series(1, 30) i`, w.O, w.A, w.C)
	w.exec(`INSERT INTO standby_rate_entries (tenant_id, billing_party_id, rate_thb, effective_from_date, effective_from_at)
		VALUES ($1, $2, 1500, '2026-06-01', '2026-05-31T17:00:00Z')`, w.O, w.partyX)
	w.exec(`INSERT INTO customer_service_fees (tenant_id, billing_party_id, fee_type, amount_thb, unit)
		VALUES ($1, $2, 'standby', 900, 'per_trip')`, w.O, w.partyY)
	w.exec(`
WITH dr AS (SELECT array_agg(id ORDER BY id) AS ids, array_agg(tenant_id ORDER BY id) AS ts, count(*)::int AS n FROM drivers),
gen AS (
  SELECT i, 1 + i % dr.n AS k, dr.ids, dr.ts, dr.n,
         timestamptz '2026-06-15 01:00Z' + (i % 120) * interval '1 day' + (i % 9) * interval '1 hour' AS plan
  FROM generate_series(1, 6000) i, dr)
INSERT INTO tasks (tenant_id, tenant_source, task_no, task_type, job_category, status, plan_at, plan_time, source_hub_raw,
                   source_hub_id, destination_raw, destination_hub_id, billing_party_id, source_linked_party_id,
                   destination_linked_party_id, truck_id, truck_type, license_plate_snapshot, driver_id,
                   helper_driver_id, run_order, legacy_doc_id, client_op_id, created_at)
SELECT ts[k], 'driver',
       CASE WHEN i % 2 = 0 THEN 'FM' ELSE 'LH' END || '-' || to_char(plan AT TIME ZONE 'Asia/Bangkok', 'DDMMYYYY') || '-'
         || CASE WHEN i % 50 = 0 THEN (i / 2 % 40 + 1)::text ELSE lpad((i / 2 % 40 + 1)::text, 3, '0') END,
       CASE WHEN i % 2 = 0 THEN 'first_mile' ELSE 'line_haul' END,
       CASE WHEN i % 5 = 0 THEN 'SUPPLEMENTARY' ELSE 'PRIMARY' END,
       CASE WHEN i % 20 < 16 THEN 'completed' WHEN i % 20 = 16 THEN 'pending' WHEN i % 20 = 17 THEN 'assigned'
            WHEN i % 20 = 18 THEN 'checked_in' ELSE 'in_transit' END,
       plan, '09:00', 'ALANG-A', $1::uuid, 'SOCE', $2::uuid, CASE WHEN i % 3 = 0 THEN $3::uuid ELSE $4::uuid END, $3::uuid, $4::uuid,
       CASE WHEN i % 4 = 0 THEN $5::uuid WHEN i % 4 = 1 THEN $6::uuid END, '6WH', '1กข-' || (i % 9000), ids[k],
       CASE WHEN i % 7 = 0 THEN ids[1 + (k + 2) % n] END, i / 30 + 1, 'task-doc-' || i,
       CASE WHEN i % 11 = 0 THEN uuidv7() END, plan - interval '1 day'
FROM gen`, w.hubSrc, w.hubDst, w.partyX, w.partyY, w.truckO, w.truckA)
	w.exec(`INSERT INTO task_delivery_stops (task_id, stop_index, destination_raw, destination_linked_party_id, sequence)
		SELECT t.id, s, 'STOP-' || s, $1, s - 1 FROM (SELECT id FROM tasks ORDER BY id LIMIT 400) t, generate_series(1, 3) s`, w.partyX)
	w.exec(`UPDATE tasks SET is_multi_delivery = true WHERE id IN (SELECT task_id FROM task_delivery_stops)`)
	w.exec(`
INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, job_category, task_id, driver_id,
                          origin_raw, origin_hub_id, destination_raw, destination_hub_id, seal_code, truck_id,
                          truck_license_plate_snapshot, billing_party_id, billing_date, delivered_at, delivered_via,
                          is_multi_delivery, legacy_doc_id, evidence_token, created_at)
SELECT t.tenant_id, 'task', 'TRIP-' || n,
       CASE WHEN t.status = 'completed' THEN (CASE WHEN n % 40 = 0 THEN 'cancelled' WHEN n % 41 = 0 THEN 'standby' ELSE 'delivered' END)
            WHEN n % 3 = 0 THEN 'incident' ELSE 'in_transit' END,
       t.task_type, t.job_category, t.id, t.driver_id, t.source_hub_raw, t.source_hub_id, t.destination_raw,
       t.destination_hub_id, 'SEAL-' || n, t.truck_id, t.license_plate_snapshot, t.billing_party_id,
       CASE WHEN n % 3 = 0 THEN t.plan_at END,
       CASE WHEN t.status = 'completed' THEN t.plan_at + interval '5 hours' END,
       CASE WHEN t.status = 'completed' THEN 'mobile' END, t.is_multi_delivery, 'trip-doc-' || n,
       CASE WHEN n % 5 = 0 THEN 'ev' || n END, t.plan_at + interval '1 hour'
FROM (SELECT t.*, row_number() OVER (ORDER BY t.id) AS n FROM tasks t WHERE t.status IN ('completed', 'in_transit')) t`)
	w.exec(`INSERT INTO trip_no_history (trip_id, old_trip_no, new_trip_no, renamed_at)
		SELECT id, 'OLD-' || trip_no, trip_no, created_at + interval '1 day' FROM trip_records WHERE status = 'delivered' ORDER BY id LIMIT 300`)
	w.exec(`INSERT INTO trip_delivery_stops (trip_id, stop_index, destination_raw, status, completed_seq)
		SELECT r.id, s, 'STOP-' || s, CASE WHEN s = 1 THEN 'delivered' ELSE 'pending' END, CASE WHEN s = 1 THEN 1 END
		FROM (SELECT id FROM trip_records WHERE is_multi_delivery ORDER BY id LIMIT 300) r, generate_series(1, 3) s`)
	w.exec(`INSERT INTO trip_photos (trip_id, photo_type, file_id, position)
		SELECT r.id, (ARRAY['seal', 'closing', 'stop_1_arrived', 'selfie'])[p], $1, p
		FROM (SELECT id FROM trip_records ORDER BY id LIMIT 2000) r, generate_series(1, 4) p`, w.files[0])
	w.exec(`
INSERT INTO standby_records (tenant_id, tenant_source, driver_id, task_id, customer_party_id, customer_resolved,
                             start_location_raw, end_location_raw, started_at, ended_at, client_op_id,
                             billing_estimate_thb, billing_party_id, billing_rate_source, billing_rate_entry_id,
                             billing_unpriced_reason, created_at)
SELECT t.tenant_id, 'task', t.driver_id, t.id, t.billing_party_id, true, 'A', 'B', t.plan_at,
       CASE WHEN n % 9 <> 0 THEN t.plan_at + interval '3 hours' END, CASE WHEN n % 13 = 0 THEN uuidv7() END,
       CASE WHEN n % 3 = 0 THEN 1500 WHEN n % 3 = 1 THEN 900 END,
       CASE WHEN n % 3 = 2 THEN NULL ELSE t.billing_party_id END,
       CASE WHEN n % 3 = 0 THEN 'standby_rate' WHEN n % 3 = 1 THEN 'service_fee' END,
       CASE WHEN n % 3 = 0 THEN (SELECT id FROM standby_rate_entries LIMIT 1) END,
       CASE WHEN n % 3 = 2 THEN 'no_rate' END, t.plan_at + interval '4 hours'
FROM (SELECT t.*, row_number() OVER (ORDER BY t.id) AS n FROM tasks t WHERE t.status = 'completed' ORDER BY t.id LIMIT 900) t`)
	w.exec(`INSERT INTO standby_photos (standby_id, photo_type, file_id) SELECT id, 'site_photo', $1 FROM standby_records`, w.files[1])
	w.exec(`
INSERT INTO incident_reports (tenant_id, tenant_source, driver_id, trip_id, delay_cause, lat, lng, truck_id, client_op_id,
                              legacy_doc_id, created_at)
SELECT r.tenant_id, 'trip', r.driver_id, r.id, 'incident_cause_traffic', 13.7, 100.5, r.truck_id,
       CASE WHEN n % 7 = 0 THEN uuidv7() END, 'inc-doc-' || n, r.created_at + interval '2 hours'
FROM (SELECT r.*, row_number() OVER (ORDER BY r.id) AS n FROM trip_records r ORDER BY r.id LIMIT 900) r`)
	if _, err := w.owner.Exec(context.Background(), `ANALYZE tasks, task_delivery_stops, trip_records, trip_no_history,
		trip_delivery_stops, trip_photos, standby_records, standby_photos, incident_reports, drivers, tenants`); err != nil {
		w.t.Fatal(err)
	}
}

// sample is one existing row id (or value) of the volume, for realistic arguments.
func (w *world) sample(sql string, args ...any) uuid.UUID { return w.id(sql, args...) }

func (w *world) sampleText(sql string, args ...any) string {
	w.t.Helper()
	var s string
	if err := w.etl.QueryRow(context.Background(), sql, args...).Scan(&s); err != nil {
		w.t.Fatalf("sample: %v", err)
	}
	return s
}

// shape is one live query shape: a generated method called with representative arguments (or a raw statement of a
// sibling domain), the principal whose RLS applies, and the indexes that may serve it (nil: a write without a read).
type shape struct {
	name string
	p    *authz.Principal
	call func(ctx context.Context, q *operationsdb.Queries) error
	want []string
}

// AC: EXPLAIN of each live query shape uses an index. Every method of operationsdb.Querier is planned (reflection keeps
// the table complete) under a request principal's RLS, with enable_seqscan off so the small fixture plans like a large
// table: no plan reads an operations table sequentially, and each read uses one of the indexes its comment names.
func TestExplainEveryQueryShapeUsesAnIndex(t *testing.T) {
	w := newWorld(t)
	w.volume()
	ctx := context.Background()

	reachO := []uuid.UUID{w.O, w.A}
	task := w.sample(`SELECT id FROM tasks WHERE tenant_id = $1 ORDER BY id LIMIT 1`, w.O)
	trip := w.sample(`SELECT id FROM trip_records WHERE tenant_id = $1 AND status = 'delivered' ORDER BY id LIMIT 1`, w.O)
	standby := w.sample(`SELECT id FROM standby_records WHERE tenant_id = $1 ORDER BY id LIMIT 1`, w.O)
	incident := w.sample(`SELECT id FROM incident_reports WHERE tenant_id = $1 ORDER BY id LIMIT 1`, w.O)
	drv := w.sample(`SELECT driver_id FROM tasks WHERE tenant_id = $1 AND driver_id IS NOT NULL ORDER BY id LIMIT 1`, w.O)
	taskNo := w.sampleText(`SELECT task_no FROM tasks ORDER BY id LIMIT 1`)
	tripNo := w.sampleText(`SELECT trip_no FROM trip_records ORDER BY id LIMIT 1`)
	oldNo := w.sampleText(`SELECT old_trip_no FROM trip_no_history ORDER BY id LIMIT 1`)
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	dayEnd := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	from := time.Date(2026, 7, 31, 17, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	cursorAt := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	trimSet := " \t\n\r\u00a0\u3000\ufeff" // the monitor's trim argument (operations.trimChars; the value does not change a plan)
	opID := uuid.Must(uuid.NewV7())
	ids := []uuid.UUID{task, uuid.Must(uuid.NewV7())}
	tripIDs := []uuid.UUID{trip, uuid.Must(uuid.NewV7())}
	staff := w.staffO
	driver := &authz.Principal{UserID: uuid.New(), TenantID: &w.O, TenantRole: authz.Driver, DriverID: &drv, TenantKind: "own_fleet"}
	all := w.all

	shapes := []shape{
		// --- tasks
		{"NextTaskSeq", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.NextTaskSeq(ctx, operationsdb.NextTaskSeqParams{TaskType: "first_mile", PlanDate: day})
			return err
		}, nil}, // the allocator's INSERT ... ON CONFLICT runs on the counter's primary key inside the definer function
		{"BumpTaskSeq", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.BumpTaskSeq(ctx, operationsdb.BumpTaskSeqParams{TaskType: "first_mile", PlanDate: day, Seq: 5})
			return err
		}, nil}, // the counter's INSERT ... ON CONFLICT on its primary key, inside the definer function
		{"LockDriverQueue", staff, func(ctx context.Context, q *operationsdb.Queries) error { return q.LockDriverQueue(ctx, drv) }, nil},
		{"NextRunOrder", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.NextRunOrder(ctx, drv)
			return err
		},
			[]string{"tasks_driver_run"}},
		{"InsertTask", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.InsertTask(ctx, operationsdb.InsertTaskParams{TenantID: w.O, TenantSource: "form", TaskType: "first_mile",
				Status: "pending", PlanAt: from, SourceHubRaw: "A", DestinationRaw: "B"})
			return err
		}, nil},
		{"LockTask", staff, func(ctx context.Context, q *operationsdb.Queries) error { _, err := q.LockTask(ctx, task); return err },
			[]string{"tasks_pkey"}},
		{"UpdateTask", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.UpdateTask(ctx, operationsdb.UpdateTaskParams{ID: task, Status: "assigned", PlanAt: from, SourceHubRaw: "A", DestinationRaw: "B"})
			return err
		}, []string{"tasks_pkey"}},
		{"CheckInTask", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.CheckInTask(ctx, operationsdb.CheckInTaskParams{ID: task, CheckInAt: from})
			return err
		}, []string{"tasks_pkey"}},
		{"SetTaskStatus", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.SetTaskStatus(ctx, operationsdb.SetTaskStatusParams{ID: task, Status: "completed"})
			return err
		}, []string{"tasks_pkey"}},
		{"CancelTask", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.CancelTask(ctx, operationsdb.CancelTaskParams{ID: task, CancelledAt: from})
			return err
		}, []string{"tasks_pkey"}},
		{"SetTaskMultiDelivery", staff, func(ctx context.Context, q *operationsdb.Queries) error { return q.SetTaskMultiDelivery(ctx, task) },
			[]string{"tasks_pkey"}},
		{"InsertTaskStop", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.InsertTaskStop(ctx, operationsdb.InsertTaskStopParams{TaskID: task, StopIndex: 9, DestinationRaw: "X"})
			return err
		}, nil},
		{"TaskStops", staff, func(ctx context.Context, q *operationsdb.Queries) error { _, err := q.TaskStops(ctx, task); return err },
			[]string{"task_delivery_stops_task_id_stop_index_key", "task_delivery_stops_task_id_destination_raw_key"}},
		{"TaskStopsOf", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TaskStopsOf(ctx, ids)
			return err
		},
			[]string{"task_delivery_stops_task_id_stop_index_key", "task_delivery_stops_task_id_destination_raw_key"}},
		{"SetTaskStopStatus", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.SetTaskStopStatus(ctx, operationsdb.SetTaskStopStatusParams{TaskID: task, StopIndex: 1, Status: "delivered"})
			return err
		}, []string{"task_delivery_stops_task_id_stop_index_key"}},
		{"GetTask", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.GetTask(ctx, operationsdb.GetTaskParams{ID: task, TenantIds: reachO})
			return err
		}, []string{"tasks_pkey"}},
		{"TaskByLegacyDocID", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TaskByLegacyDocID(ctx, operationsdb.TaskByLegacyDocIDParams{LegacyDocID: "task-doc-10", TenantIds: reachO})
			return err
		}, []string{"tasks_legacy"}},
		{"TasksByNumber", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TasksByNumber(ctx, operationsdb.TasksByNumberParams{TaskNo: taskNo, TenantIds: reachO})
			return err
		}, []string{"tasks_task_no"}},
		{"TaskByClientOp", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TaskByClientOp(ctx, operationsdb.TaskByClientOpParams{DriverID: drv, ClientOpID: opID})
			return err
		}, []string{"tasks_client_op"}},
		{"ListTasksByCreated", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTasksByCreated(ctx, operationsdb.ListTasksByCreatedParams{TenantIds: reachO, TaskType: ptr("first_mile"),
				AfterCreated: &cursorAt, AfterID: task, RowLimit: 51})
			return err
		}, []string{"tasks_tenant_type_created"}},
		{"ListTasksByCreated/all", all, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTasksByCreated(ctx, operationsdb.ListTasksByCreatedParams{AllTenants: true, TenantIds: []uuid.UUID{}, RowLimit: 51})
			return err
		}, []string{"tasks_created"}},
		{"ListTasksByCreated/driver", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTasksByCreated(ctx, operationsdb.ListTasksByCreatedParams{TenantIds: reachO, DriverID: &drv, RowLimit: 51})
			return err
		}, []string{"tasks_driver_status", "tasks_driver_plan", "tasks_driver_run", "tasks_helper_plan", "tasks_tenant_type_created"}},
		{"ListTasksByCreated/truck", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTasksByCreated(ctx, operationsdb.ListTasksByCreatedParams{TenantIds: reachO, TruckID: &w.truckO, RowLimit: 51})
			return err
		}, []string{"tasks_truck", "tasks_tenant_type_created"}},
		{"ListTasksByPlan", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTasksByPlan(ctx, operationsdb.ListTasksByPlanParams{TenantIds: reachO, FromDate: day, ToDate: day,
				TaskType: ptr("first_mile"), Statuses: []string{"pending", "assigned"}, RowLimit: 51})
			return err
		}, []string{"tasks_tenant_type_plan"}},
		{"ListTasksByPlan/all", all, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTasksByPlan(ctx, operationsdb.ListTasksByPlanParams{AllTenants: true, TenantIds: []uuid.UUID{},
				FromDate: day, ToDate: dayEnd, AfterPlan: &dayEnd, AfterID: task, RowLimit: 51})
			return err
		}, []string{"tasks_plan_date", "tasks_tenant_type_plan"}},
		{"CountTasksOnDay", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.CountTasksOnDay(ctx, operationsdb.CountTasksOnDayParams{TenantIds: reachO, PlanDate: day})
			return err
		}, []string{"tasks_tenant_type_plan", "tasks_plan_date"}},
		{"DriverTasks", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.DriverTasks(ctx, operationsdb.DriverTasksParams{DriverID: drv, Statuses: []string{"assigned", "checked_in", "in_transit"}, RowLimit: 200})
			return err
		}, []string{"tasks_driver_status", "tasks_helper_plan", "tasks_driver_plan"}},
		{"DriverTasks/page", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.DriverTasks(ctx, operationsdb.DriverTasksParams{DriverID: drv, Statuses: []string{"completed", "cancelled"},
				AfterQueueAt: &cursorAt, AfterRunOrder: 3, AfterCreatedAt: cursorAt, AfterID: task, RowLimit: 501})
			return err
		}, []string{"tasks_driver_status", "tasks_helper_plan", "tasks_driver_plan"}},
		{"DriverHasTaskInStatus", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.DriverHasTaskInStatus(ctx, operationsdb.DriverHasTaskInStatusParams{DriverID: drv, Status: "checked_in"})
			return err
		}, []string{"tasks_driver_status"}},
		{"ActiveTaskDrivers", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ActiveTaskDrivers(ctx, operationsdb.ActiveTaskDriversParams{TenantIds: reachO})
			return err
		}, []string{"tasks_active_status"}},

		// --- trips
		{"InsertTrip", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.InsertTrip(ctx, operationsdb.InsertTripParams{TenantID: w.O, TenantSource: "task", TripNo: "NEW-1", Status: "in_transit", JobType: "first_mile"})
			return err
		}, nil},
		{"LockTrip", staff, func(ctx context.Context, q *operationsdb.Queries) error { _, err := q.LockTrip(ctx, trip); return err },
			[]string{"trip_records_pkey"}},
		{"RenameTripNo", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.RenameTripNo(ctx, operationsdb.RenameTripNoParams{ID: trip, NewTripNo: "RENAMED"})
			return err
		}, []string{"trip_records_pkey"}},
		{"InsertTripNoHistory", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.InsertTripNoHistory(ctx, operationsdb.InsertTripNoHistoryParams{TripID: trip, OldTripNo: "A", NewTripNo: "B"})
			return err
		}, nil},
		{"TripNoHistory", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TripNoHistory(ctx, trip)
			return err
		},
			[]string{"trip_no_history_trip"}},
		{"UpdateTripDetails", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.UpdateTripDetails(ctx, operationsdb.UpdateTripDetailsParams{ID: trip})
			return err
		}, []string{"trip_records_pkey"}},
		{"DeliverTrip", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.DeliverTrip(ctx, operationsdb.DeliverTripParams{ID: trip, DeliveredAt: from, DeliveredVia: "mobile"})
			return err
		}, []string{"trip_records_pkey"}},
		{"SetTripStatus", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.SetTripStatus(ctx, operationsdb.SetTripStatusParams{ID: trip, Status: "standby"})
			return err
		}, []string{"trip_records_pkey"}},
		{"CancelInTransitTrips", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.CancelInTransitTrips(ctx, tripIDs)
			return err
		}, []string{"trip_records_pkey", "trip_records_pending_driver"}},
		{"MarkTripResubmitted", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.MarkTripResubmitted(ctx, operationsdb.MarkTripResubmittedParams{ID: trip, ResubmittedAt: from})
			return err
		}, []string{"trip_records_pkey"}},
		{"UpsertTripPhoto", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.UpsertTripPhoto(ctx, operationsdb.UpsertTripPhotoParams{TripID: trip, PhotoType: "seal", FileID: w.files[0]})
			return err
		}, []string{"trip_photos_trip_id_photo_type_key"}},
		{"DeleteTripPhoto", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.DeleteTripPhoto(ctx, operationsdb.DeleteTripPhotoParams{TripID: trip, PhotoType: "seal"})
			return err
		}, []string{"trip_photos_trip_id_photo_type_key"}},
		{"TripPhotos", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TripPhotos(ctx, trip)
			return err
		},
			[]string{"trip_photos_trip_id_photo_type_key"}},
		{"TripPhotosOf", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TripPhotosOf(ctx, tripIDs)
			return err
		},
			[]string{"trip_photos_trip_id_photo_type_key"}},
		{"UnknownTripPhotos", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.UnknownTripPhotos(ctx, trip)
			return err
		},
			[]string{"trip_photos_unknown_type", "trip_photos_trip_id_photo_type_key"}},
		{"InsertTripStop", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.InsertTripStop(ctx, operationsdb.InsertTripStopParams{TripID: trip, StopIndex: 9, DestinationRaw: "X"})
			return err
		}, nil},
		{"DeliverTripStop", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.DeliverTripStop(ctx, operationsdb.DeliverTripStopParams{TripID: trip, StopIndex: 2, DeliveredAt: from})
			return err
		}, []string{"trip_delivery_stops_trip_id_stop_index_key"}},
		{"TripStops", staff, func(ctx context.Context, q *operationsdb.Queries) error { _, err := q.TripStops(ctx, trip); return err },
			[]string{"trip_delivery_stops_trip_id_stop_index_key"}},
		{"GetTrip", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.GetTrip(ctx, operationsdb.GetTripParams{ID: trip, TenantIds: reachO})
			return err
		}, []string{"trip_records_pkey"}},
		{"TripByNo", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TripByNo(ctx, operationsdb.TripByNoParams{TripNo: tripNo, TenantIds: reachO})
			return err
		}, []string{"trip_records_trip_no"}},
		{"TripByOldNo", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TripByOldNo(ctx, operationsdb.TripByOldNoParams{TripNo: oldNo, TenantIds: reachO})
			return err
		}, []string{"trip_no_history_old_no"}},
		{"TripByLegacyDocID", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TripByLegacyDocID(ctx, operationsdb.TripByLegacyDocIDParams{LegacyDocID: "trip-doc-10", TenantIds: reachO})
			return err
		}, []string{"trip_records_legacy"}},
		{"TripsOfTasks", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TripsOfTasks(ctx, operationsdb.TripsOfTasksParams{TaskIds: ids, TenantIds: reachO})
			return err
		}, []string{"trip_records_task"}},
		{"ListTripsByCreated", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTripsByCreated(ctx, operationsdb.ListTripsByCreatedParams{TenantIds: reachO, FromAt: &from, ToAt: &to,
				AfterCreated: &cursorAt, AfterID: trip, RowLimit: 201})
			return err
		}, []string{"trip_records_tenant_created"}},
		{"ListTripsByCreated/all", all, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTripsByCreated(ctx, operationsdb.ListTripsByCreatedParams{AllTenants: true, TenantIds: []uuid.UUID{}, RowLimit: 201})
			return err
		}, []string{"trip_records_created"}},
		{"ListTripsByCreated/truck", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTripsByCreated(ctx, operationsdb.ListTripsByCreatedParams{TenantIds: reachO, TruckID: &w.truckO, RowLimit: 201})
			return err
		}, []string{"trip_records_truck_created", "trip_records_tenant_created"}},
		{"ListTripsByCreated/driver", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTripsByCreated(ctx, operationsdb.ListTripsByCreatedParams{TenantIds: reachO, DriverID: &drv, RowLimit: 201})
			return err
		}, []string{"trip_records_driver_created", "trip_records_tenant_created"}},
		{"ListTripsByDelivered", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTripsByDelivered(ctx, operationsdb.ListTripsByDeliveredParams{TenantIds: reachO, FromAt: &from, ToAt: &to, RowLimit: 51})
			return err
		}, []string{"trip_records_delivered"}},
		{"ListTripsByDelivered/customer", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTripsByDelivered(ctx, operationsdb.ListTripsByDeliveredParams{TenantIds: reachO, FromAt: &from, ToAt: &to,
				BillingPartyID: &w.partyX, AfterDelivered: &cursorAt, AfterID: trip, RowLimit: 51})
			return err
		}, []string{"trip_records_party_status_del"}},
		{"ListTripsByDelivered/driver", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTripsByDelivered(ctx, operationsdb.ListTripsByDeliveredParams{TenantIds: reachO, DriverID: &drv, FromAt: &from, ToAt: &to, RowLimit: 51})
			return err
		}, []string{"trip_records_delivered_driver"}},
		{"ListTripsByBillingAxis", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTripsByBillingAxis(ctx, operationsdb.ListTripsByBillingAxisParams{TenantIds: reachO, FromDate: &day, ToDate: &dayEnd,
				BillingPartyID: &w.partyX, RowLimit: 51})
			return err
		}, []string{"trip_records_party_axis"}},
		{"ListTripsByBillingAxis/all-customers", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListTripsByBillingAxis(ctx, operationsdb.ListTripsByBillingAxisParams{TenantIds: reachO, FromDate: &day, ToDate: &dayEnd,
				AfterAxis: &dayEnd, AfterID: trip, RowLimit: 51})
			return err
		}, []string{"trip_records_party_axis"}},
		{"DriverTrips", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.DriverTrips(ctx, operationsdb.DriverTripsParams{DriverID: drv, AfterCreated: &cursorAt, AfterID: trip, RowLimit: 51})
			return err
		}, []string{"trip_records_driver_created"}},
		{"PendingInTransitTrip", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.PendingInTransitTrip(ctx, drv)
			return err
		}, []string{"trip_records_pending_driver"}},
		{"TripSealTaken", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TripSealTaken(ctx, operationsdb.TripSealTakenParams{SealCode: "SEAL-10"})
			return err
		}, nil}, // the lookup runs inside the SECURITY DEFINER function: planned below as "duplicate-seal check (0014)"
		{"TripIDsBySeal", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TripIDsBySeal(ctx, operationsdb.TripIDsBySealParams{SealCode: "SEAL-10", ExcludeID: &trip, TenantIds: reachO})
			return err
		}, []string{"trip_records_seal"}},
		{"TripIDsBySeal/driver", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TripIDsBySeal(ctx, operationsdb.TripIDsBySealParams{SealCode: "SEAL-10", TenantIds: []uuid.UUID{}, ReachDriver: &drv})
			return err
		}, []string{"trip_records_seal"}},

		// --- the monitor and the read joins of T30
		{"MonitorTrips", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.MonitorTrips(ctx, operationsdb.MonitorTripsParams{TrimChars: trimSet, WithPrice: true, TenantIds: reachO, FromAt: from,
				ToAt: to, OriginHubs: []uuid.UUID{}, DestinationHubs: []uuid.UUID{}, AfterCreated: &cursorAt, AfterID: trip,
				RowLimit: 201})
			return err
		}, []string{"trip_records_tenant_created", "trip_records_created"}},
		{"MonitorTrips/filters", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.MonitorTrips(ctx, operationsdb.MonitorTripsParams{TrimChars: trimSet, TenantIds: reachO, FromAt: from, ToAt: to,
				Statuses: []string{"incident"}, IncidentStatus: true, JobType: ptr("first_mile"), PlateRaw: ptr("1กข-10"),
				VehicleClass: ptr("6WH"), OriginOn: true, OriginHubs: []uuid.UUID{w.hubSrc}, DestinationOn: true,
				DestinationHubs: []uuid.UUID{}, DestinationSoc: ptr("SOCE"), Q: ptr("TRIP-1"), RowLimit: 201})
			return err
		}, []string{"trip_records_tenant_created", "trip_records_created"}},
		{"MonitorTrips/all", all, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.MonitorTrips(ctx, operationsdb.MonitorTripsParams{TrimChars: trimSet, AllTenants: true, TenantIds: []uuid.UUID{}, FromAt: from,
				ToAt: to, OriginHubs: []uuid.UUID{}, DestinationHubs: []uuid.UUID{}, RowLimit: 201})
			return err
		}, []string{"trip_records_created", "trip_records_tenant_created"}},
		{"MonitorFacets", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.MonitorFacets(ctx, operationsdb.MonitorFacetsParams{TrimChars: trimSet, TenantIds: reachO, FromAt: from, ToAt: to})
			return err
		}, []string{"trip_records_tenant_created", "trip_records_created"}},
		{"TripStopsOf", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TripStopsOf(ctx, tripIDs)
			return err
		}, []string{"trip_delivery_stops_trip_id_stop_index_key"}},
		{"DriverNamesOf", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.DriverNamesOf(ctx, []uuid.UUID{drv, w.dO2})
			return err
		}, []string{"drivers_pkey"}},
		{"TripNumbersOf", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.TripNumbersOf(ctx, operationsdb.TripNumbersOfParams{Ids: tripIDs, TenantIds: reachO})
			return err
		}, []string{"trip_records_pkey"}},

		// --- standby
		{"InsertStandby", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.InsertStandby(ctx, operationsdb.InsertStandbyParams{TenantID: w.O, TenantSource: "task", DriverID: &drv, ClientOpID: &opID})
			return err
		}, nil},
		{"StandbyByClientOp", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.StandbyByClientOp(ctx, operationsdb.StandbyByClientOpParams{DriverID: drv, ClientOpID: opID})
			return err
		}, []string{"standby_records_client_op"}},
		{"UpsertStandbyPhoto", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.UpsertStandbyPhoto(ctx, operationsdb.UpsertStandbyPhotoParams{StandbyID: standby, PhotoType: "site_photo", FileID: w.files[0]})
			return err
		}, nil},
		{"StandbyPhotos", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.StandbyPhotos(ctx, standby)
			return err
		},
			[]string{"standby_photos_standby_id_photo_type_key"}},
		{"StandbyPhotosOf", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.StandbyPhotosOf(ctx, []uuid.UUID{standby})
			return err
		}, []string{"standby_photos_standby_id_photo_type_key"}},
		{"GetStandby", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.GetStandby(ctx, operationsdb.GetStandbyParams{ID: standby, TenantIds: reachO})
			return err
		}, []string{"standby_records_pkey"}},
		{"StandbyByLegacyDocID", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.StandbyByLegacyDocID(ctx, operationsdb.StandbyByLegacyDocIDParams{LegacyDocID: "standby-doc-1", TenantIds: reachO})
			return err
		}, []string{"standby_records_legacy"}},
		{"StandbyOfTasks", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.StandbyOfTasks(ctx, operationsdb.StandbyOfTasksParams{TaskIds: ids, TenantIds: reachO})
			return err
		}, []string{"standby_records_task"}},
		{"StandbyOfTrip", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.StandbyOfTrip(ctx, operationsdb.StandbyOfTripParams{TripID: trip, TenantIds: reachO})
			return err
		}, []string{"standby_records_trip"}},
		{"ListStandbyByCreated", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListStandbyByCreated(ctx, operationsdb.ListStandbyByCreatedParams{TenantIds: reachO, FromAt: &from, ToAt: &to,
				AfterCreated: &cursorAt, AfterID: standby, RowLimit: 51})
			return err
		}, []string{"standby_records_tenant_created"}},
		{"ListStandbyByCreated/all", all, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListStandbyByCreated(ctx, operationsdb.ListStandbyByCreatedParams{AllTenants: true, TenantIds: []uuid.UUID{}, RowLimit: 51})
			return err
		}, []string{"standby_records_created"}},
		{"ListStandbyByEnded", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListStandbyByEnded(ctx, operationsdb.ListStandbyByEndedParams{TenantIds: reachO, FromAt: &from, ToAt: &to,
				AfterEnded: &cursorAt, AfterID: standby, RowLimit: 51})
			return err
		}, []string{"standby_records_ended"}},
		{"DriverStandby", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.DriverStandby(ctx, operationsdb.DriverStandbyParams{DriverID: drv, RowLimit: 51})
			return err
		}, []string{"standby_records_driver"}},

		// --- incidents
		{"InsertIncident", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.InsertIncident(ctx, operationsdb.InsertIncidentParams{TenantID: w.O, TenantSource: "trip", DriverID: &drv,
				TripID: &trip, ReportedByKind: "driver", ClientOpID: &opID})
			return err
		}, nil},
		{"IncidentByClientOp", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.IncidentByClientOp(ctx, operationsdb.IncidentByClientOpParams{DriverID: drv, ClientOpID: opID})
			return err
		}, []string{"incident_reports_client_op"}},
		{"GetIncident", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.GetIncident(ctx, operationsdb.GetIncidentParams{ID: incident, TenantIds: reachO})
			return err
		}, []string{"incident_reports_pkey"}},
		{"IncidentByLegacyDocID", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.IncidentByLegacyDocID(ctx, operationsdb.IncidentByLegacyDocIDParams{LegacyDocID: "inc-doc-3", TenantIds: reachO})
			return err
		}, []string{"incident_reports_legacy"}},
		{"IncidentsOfTrip", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.IncidentsOfTrip(ctx, operationsdb.IncidentsOfTripParams{TripID: trip, TenantIds: reachO})
			return err
		}, []string{"incident_reports_trip"}},
		{"CountIncidentsOfTrips", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.CountIncidentsOfTrips(ctx, operationsdb.CountIncidentsOfTripsParams{TripIds: tripIDs, TenantIds: reachO})
			return err
		}, []string{"incident_reports_trip"}},
		{"ListIncidents", staff, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListIncidents(ctx, operationsdb.ListIncidentsParams{TenantIds: reachO, FromAt: &from, ToAt: &to,
				AfterCreated: &cursorAt, AfterID: incident, RowLimit: 51})
			return err
		}, []string{"incident_reports_tenant_created"}},
		{"ListIncidents/all", all, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.ListIncidents(ctx, operationsdb.ListIncidentsParams{AllTenants: true, TenantIds: []uuid.UUID{}, RowLimit: 51})
			return err
		}, []string{"incident_reports_created"}},
		{"DriverIncidents", driver, func(ctx context.Context, q *operationsdb.Queries) error {
			_, err := q.DriverIncidents(ctx, operationsdb.DriverIncidentsParams{DriverID: drv, TripID: &trip, RowLimit: 51})
			return err
		}, []string{"incident_reports_driver", "incident_reports_trip"}},
	}

	// Every generated query is planned at least once (the Querier interface enumerates them), and nothing else.
	var methods []string
	qt := reflect.TypeFor[operationsdb.Querier]()
	for i := range qt.NumMethod() {
		methods = append(methods, qt.Method(i).Name)
	}
	covered := map[string]bool{}
	for _, s := range shapes {
		covered[strings.SplitN(s.name, "/", 2)[0]] = true
	}
	for _, m := range methods {
		if !covered[m] {
			t.Errorf("query %s has no EXPLAIN shape", m)
		}
	}
	for m := range covered {
		if !slices.Contains(methods, m) {
			t.Errorf("shape %s names no query", m)
		}
	}

	var report []string
	for _, s := range shapes {
		plan, err := w.explain(ctx, s.p, s.call)
		if err != nil {
			t.Errorf("%s: %v", s.name, err)
			continue
		}
		used := plan.Indexes()
		report = append(report, s.name+": "+strings.Join(used, ", "))
		if testing.Verbose() {
			t.Logf("%s:\n%s", s.name, plan)
		}
		if seq := plan.SeqScans(opsTables...); len(seq) > 0 {
			t.Errorf("%s reads %v sequentially:\n%s", s.name, seq, plan)
		}
		if s.want != nil && !slices.ContainsFunc(used, func(ix string) bool { return slices.Contains(s.want, ix) }) {
			t.Errorf("%s uses %v, want one of %v:\n%s", s.name, used, s.want, plan)
		}
	}

	// Shapes of the sibling domains over these tables (billing, payroll, evidence, the dispatcher's projection), as
	// Appendix A §A.2.3 maps them to indexes; their packages plan the same SQL. Also the lookup a SECURITY DEFINER
	// helper of 0014 runs, which EXPLAIN of the generated call cannot show.
	for _, raw := range []struct {
		name string
		p    *authz.Principal
		sql  string
		args []any
		want []string
	}{
		{"payroll: delivered trips of a driver in a month (I1)", staff, `SELECT id FROM trip_records
			WHERE driver_id = $1 AND status = 'delivered' AND delivered_at >= $2 AND delivered_at < $3`, []any{drv, from, to},
			[]string{"trip_records_delivered_driver"}},
		{"payroll: own and helper tasks of a driver (I3)", staff, `SELECT id FROM tasks
			WHERE (driver_id = $1 OR helper_driver_id = $1) AND plan_at >= $2 AND plan_at < $3`, []any{drv, from, to},
			[]string{"tasks_driver_plan", "tasks_helper_plan"}},
		{"billing rows of a party by axis (GET /v1/billing/rows)", staff, `SELECT id FROM trip_records
			WHERE billing_party_id = $1 AND status = 'delivered' AND billing_axis_date BETWEEN $2 AND $3`, []any{w.partyX, day, dayEnd},
			[]string{"trip_records_party_axis"}},
		{"billing rows plan basis (I23)", staff, `SELECT id FROM trip_records
			WHERE billing_party_id = $1 AND status = 'delivered' AND billing_date >= $2 AND billing_date < $3`, []any{w.partyX, from, to},
			[]string{"trip_records_party_billdate"}},
		{"billing rows delivered basis (I22)", staff, `SELECT id FROM trip_records
			WHERE billing_party_id = $1 AND status = 'delivered' AND delivered_at >= $2 AND delivered_at < $3`, []any{w.partyX, from, to},
			[]string{"trip_records_party_status_del"}},
		{"billing safety net (I21)", staff, `SELECT id FROM trip_records
			WHERE status = 'delivered' AND delivered_at >= $1 AND delivered_at < $2`, []any{from, to},
			[]string{"trip_records_delivered"}},
		{"income page created axis (I20)", staff, `SELECT id FROM trip_records
			WHERE status = 'delivered' ORDER BY created_at DESC, id DESC LIMIT 51`, nil,
			[]string{"trip_records_created_delivered"}},
		{"standby billing by party axis", staff, `SELECT id FROM standby_records
			WHERE billing_party_id = $1 AND billing_axis_date BETWEEN $2 AND $3`, []any{w.partyX, day, dayEnd},
			[]string{"standby_records_party_axis"}},
		{"standby diagnostics by customer axis", staff, `SELECT id FROM standby_records
			WHERE customer_party_id = $1 AND billing_axis_date BETWEEN $2 AND $3`, []any{w.partyX, day, dayEnd},
			[]string{"standby_records_customer_axis"}},
		{"standby safety net (I34)", staff, `SELECT id FROM standby_records
			WHERE status = 'completed' AND ended_at >= $1 AND ended_at < $2`, []any{from, to},
			[]string{"standby_records_ended"}},
		// trip_seal_taken() (0014) runs this as logitrack_rls_definer (BYPASSRLS): the bypass principal plans it alike.
		{"duplicate-seal check inside trip_seal_taken (0014)", all, `SELECT EXISTS (SELECT 1 FROM trip_records t
			WHERE t.seal_code = $1 AND t.id IS DISTINCT FROM $2)`, []any{"SEAL-10", trip},
			[]string{"trip_records_seal"}},
		{"evidence gallery: trip token", all, `SELECT id FROM trip_records WHERE evidence_token = $1`, []any{"ev10"},
			[]string{"trip_records_evidence"}},
		{"dispatcher day list (scope_tasks)", all, `SELECT id FROM scope_tasks WHERE plan_date = $1 AND task_type = 'first_mile'`, []any{day},
			[]string{"tasks_plan_date", "tasks_tenant_type_plan"}},
		{"scope predicate: tasks of a billing party", all, `SELECT id FROM tasks WHERE billing_party_id = $1`, []any{w.partyY},
			[]string{"tasks_billing_party"}},
	} {
		plan, err := w.explain(ctx, raw.p, nil, append([]any{raw.sql}, raw.args...)...)
		if err != nil {
			t.Errorf("%s: %v", raw.name, err)
			continue
		}
		used := plan.Indexes()
		report = append(report, raw.name+": "+strings.Join(used, ", "))
		if seq := plan.SeqScans(opsTables...); len(seq) > 0 {
			t.Errorf("%s reads %v sequentially:\n%s", raw.name, seq, plan)
		}
		if !slices.ContainsFunc(used, func(ix string) bool { return slices.Contains(raw.want, ix) }) {
			t.Errorf("%s uses %v, want one of %v:\n%s", raw.name, used, raw.want, plan)
		}
	}
	// Negative control: the check bites. No index covers partner_code, so its plan reads trip_records sequentially even
	// with enable_seqscan off.
	ctl, err := w.explain(ctx, staff, nil, `SELECT id FROM trip_records WHERE partner_code = $1`, "P1")
	if err != nil || !slices.Equal(ctl.SeqScans(opsTables...), []string{"trip_records"}) {
		t.Errorf("negative control: %v\n%s", err, ctl)
	}

	sort.Strings(report)
	t.Logf("query shape -> indexes (%d shapes):\n%s", len(report), strings.Join(report, "\n"))
}

// explain plans one shape in a request transaction of p (rolled back) with sequential scans disabled: the generated
// call through a pgtest.Explainer, or, when raw is set, that statement with args.
func (w *world) explain(ctx context.Context, p *authz.Principal, call func(context.Context, *operationsdb.Queries) error,
	raw ...any) (pgtest.Plan, error) {
	var plan pgtest.Plan
	err := db.WithPrincipal(ctx, w.pool, p, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
			return err
		}
		ex := &pgtest.Explainer{Tx: tx}
		if len(raw) > 0 {
			sql, _ := raw[0].(string)
			if _, err := ex.Exec(ctx, sql, raw[1:]...); err != nil {
				return err
			}
		} else if err := call(ctx, operationsdb.New(ex)); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if len(ex.Plans) != 1 {
			return fmt.Errorf("expected one planned statement, got %d", len(ex.Plans))
		}
		plan = ex.Last()
		return errRollback
	})
	if errors.Is(err, errRollback) {
		err = nil
	}
	return plan, err
}
