//go:build integration

package operations_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// The driver's queue service of GET /v1/mobile/tasks (operations.Service.DriverQueue, T57 wires the route) against
// PostgreSQL 18 as logitrack_app: the ADR 0028 order (compareTasksForDriverQueue, task_repository.dart:16-70) applied
// in SQL before the LIMIT, the open queue by default, and keyset pages instead of a silent cut at MaxLimit (TW8).
func TestDriverQueue(t *testing.T) {
	r := newReadWorld(t)
	ctx := context.Background()
	driver := r.principals["driverO1"]
	bkk := func(month time.Month, day, hour int) time.Time {
		return time.Date(2026, month, day, hour, 0, 0, 0, clock.Bangkok)
	}
	hhmm := func(v string) *string { return &v }
	run := func(v int) *int { return &v }
	type queued struct {
		status         string
		driver, helper *uuid.UUID
		planAt         time.Time
		planTime       *string
		actual         *time.Time
		run            *int
		created        time.Time
	}
	task := func(x queued) uuid.UUID {
		t.Helper()
		return r.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, plan_time, actual_pickup_at,
			source_hub_raw, destination_raw, driver_id, helper_driver_id, run_order, created_at)
			VALUES ($1, 'form', 'first_mile', $2, $3, $4, $5, 'ALANG-A', 'SOCE', $6, $7, $8, $9) RETURNING id`,
			r.O, x.status, x.planAt, x.planTime, x.actual, x.driver, x.helper, x.run, x.created)
	}

	// Six months of history, about three tasks a day: 510 completed tasks planned before today.
	r.exec(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, plan_time, source_hub_raw,
			destination_raw, driver_id, run_order, created_at)
		SELECT $1, 'form', 'first_mile', 'completed', timestamptz '2026-03-01 01:00Z' + i * interval '8 hours', '08:00',
		       'ALANG-A', 'SOCE', $2, i, timestamptz '2026-02-28 01:00Z' + i * interval '8 hours'
		FROM generate_series(1, 510) i`, r.O, r.dO1)
	// Today's queue (2026-10-11 in Bangkok) and tomorrow's helper task.
	ts := func(month time.Month, day int) time.Time { return time.Date(2026, month, day, 9, 0, 0, 0, time.UTC) }
	actualB := bkk(10, 11, 8)
	F := task(queued{status: "assigned", driver: &r.dO1, planAt: bkk(10, 11, 6), created: ts(9, 20)}) // no plan_time: 00:00
	A := task(queued{status: "assigned", driver: &r.dO1, planAt: bkk(10, 11, 14), planTime: hhmm("14:00"), run: run(2),
		created: ts(9, 21)})
	// ADR 0028: the plan date is a billing tag of next month; the driver goes at the actual pickup, today 08:00.
	B := task(queued{status: "assigned", driver: &r.dO1, planAt: bkk(11, 5, 8), planTime: hhmm("08:00"), actual: &actualB,
		run: run(3), created: ts(9, 22)})
	// Three tasks dispatched at the same instant: no run_order first, then run_order, then created_at. The plan
	// instant's own time (11:00) is not the key: the plan day at plan_time (10:00) is.
	C := task(queued{status: "checked_in", driver: &r.dO1, planAt: bkk(10, 11, 11), planTime: hhmm("10:00"),
		created: ts(10, 3)})
	D := task(queued{status: "assigned", driver: &r.dO1, planAt: bkk(10, 11, 10), planTime: hhmm("10:00"), run: run(1),
		created: ts(10, 1)})
	E := task(queued{status: "in_transit", driver: &r.dO1, planAt: bkk(10, 11, 10), planTime: hhmm("10:00"), run: run(1),
		created: ts(10, 2)})
	H := task(queued{status: "assigned", driver: &r.dO2, helper: &r.dO1, planAt: bkk(10, 12, 9), planTime: hhmm("09:00"),
		run: run(1), created: ts(10, 4)}) // a helper task (R28)
	cancelled := task(queued{status: "cancelled", driver: &r.dO1, planAt: bkk(10, 11, 7), planTime: hhmm("07:00"),
		created: ts(10, 5)})
	task(queued{status: "assigned", driver: &r.dO2, planAt: bkk(10, 11, 5), planTime: hhmm("05:00"), created: ts(10, 6)}) // not O1's

	idsOf := func(items []operations.QueueTask) []uuid.UUID {
		out := make([]uuid.UUID, len(items))
		for i, x := range items {
			out[i] = x.ID
		}
		return out
	}
	queue := func(in operations.DriverQueueInput) operations.Page[operations.QueueTask] {
		t.Helper()
		pg, err := r.svc.DriverQueue(ctx, driver, in)
		if err != nil {
			t.Fatal(err)
		}
		return pg
	}

	// Without statuses: the open queue, every task of it, in the ADR 0028 order, the newest work included.
	want := []uuid.UUID{F, B, C, D, E, A, H}
	pg := queue(operations.DriverQueueInput{})
	if got := idsOf(pg.Items); !slices.Equal(got, want) || pg.NextCursor != "" {
		t.Fatalf("open queue %v (next %q), want %v", got, pg.NextCursor, want)
	}
	if !pg.Items[1].QueueAt.Equal(actualB) || !pg.Items[0].QueueAt.Equal(bkk(10, 11, 0)) ||
		!pg.Items[2].QueueAt.Equal(bkk(10, 11, 10)) {
		t.Errorf("queue instants %v %v %v", pg.Items[0].QueueAt, pg.Items[1].QueueAt, pg.Items[2].QueueAt)
	}
	if pg.Items[6].HelperDriverID == nil || *pg.Items[6].HelperDriverID != r.dO1 {
		t.Errorf("the helper task %+v", pg.Items[6].TaskBase)
	}

	// Pages of three follow the same order through the cursor.
	var walked []uuid.UUID
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 3 {
			t.Fatal("the open queue never ends")
		}
		pg := queue(operations.DriverQueueInput{Limit: 3, Cursor: cursor})
		walked = append(walked, idsOf(pg.Items)...)
		if pg.NextCursor == "" {
			break
		}
		cursor = pg.NextCursor
	}
	if !slices.Equal(walked, want) {
		t.Errorf("paged open queue %v, want %v", walked, want)
	}

	// Every status: more than MaxLimit rows page through a cursor instead of being cut.
	pg = queue(operations.DriverQueueInput{Statuses: operations.TaskStatuses})
	if len(pg.Items) != operations.MaxLimit || pg.NextCursor == "" {
		t.Fatalf("first page of the whole queue: %d rows, next %q", len(pg.Items), pg.NextCursor)
	}
	all := idsOf(pg.Items)
	last := pg.Items[len(pg.Items)-1]
	pg2 := queue(operations.DriverQueueInput{Statuses: operations.TaskStatuses, Cursor: pg.NextCursor})
	if pg2.NextCursor != "" || len(pg2.Items) != 510+8-operations.MaxLimit {
		t.Fatalf("second page: %d rows, next %q", len(pg2.Items), pg2.NextCursor)
	}
	if pg2.Items[0].QueueAt.Before(last.QueueAt) {
		t.Errorf("the second page starts before the first ends: %v < %v", pg2.Items[0].QueueAt, last.QueueAt)
	}
	all = append(all, idsOf(pg2.Items)...)
	seen := map[uuid.UUID]bool{}
	for _, id := range all {
		if seen[id] {
			t.Fatalf("task %s twice", id)
		}
		seen[id] = true
	}
	for _, id := range append(slices.Clone(want), cancelled) {
		if !seen[id] {
			t.Errorf("task %s missing from the whole queue", id)
		}
	}
	// The last eight rows are today's and tomorrow's work, in queue order (the cancelled task at 07:00 included).
	if got, wantTail := all[len(all)-8:], []uuid.UUID{F, cancelled, B, C, D, E, A, H}; !slices.Equal(got, wantTail) {
		t.Errorf("tail of the whole queue %v, want %v", got, wantTail)
	}

	// A status filter.
	pg = queue(operations.DriverQueueInput{Statuses: []string{"cancelled"}})
	if got := idsOf(pg.Items); !slices.Equal(got, []uuid.UUID{cancelled}) {
		t.Errorf("cancelled %v", got)
	}

	// Refusals.
	otherAxis := base64.RawURLEncoding.EncodeToString([]byte(`{"a":"c","t":"2026-10-11T00:00:00Z","id":"` + F.String() + `"}`))
	for _, c := range []struct {
		in    operations.DriverQueueInput
		field string
	}{
		{operations.DriverQueueInput{Limit: operations.MaxLimit + 1}, "limit"},
		{operations.DriverQueueInput{Cursor: "bm9wZQ"}, "cursor"},
		{operations.DriverQueueInput{Cursor: otherAxis}, "cursor"},
		{operations.DriverQueueInput{Statuses: []string{"lost"}}, "status"},
	} {
		_, err := r.svc.DriverQueue(ctx, driver, c.in)
		wantAPIError(t, err, http.StatusUnprocessableEntity, "invalid_argument", c.field)
	}
	_, err := r.svc.DriverQueue(ctx, r.principals["staffO"], operations.DriverQueueInput{})
	wantAPIError(t, err, http.StatusForbidden, "permission_denied", "")
}
