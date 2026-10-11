//go:build integration

package operations_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

var errRollback = errors.New("rollback")

// wantAPIError fails unless err is the API error status/code (and, when field is set, carries that field).
func wantAPIError(t *testing.T, err error, status int, code, field string) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != status || he.Code != code {
		t.Fatalf("got %v, want %d %s", err, status, code)
	}
	if field == "" {
		return
	}
	if f, ok := he.Details["field"]; ok && f == field {
		return
	}
	if fs, ok := he.Details["fields"].([]httpx.FieldViolation); ok {
		for _, f := range fs {
			if f.Field == field {
				return
			}
		}
	}
	t.Fatalf("%v: details %v lack field %s", err, he.Details, field)
}

// AC: two concurrent task inserts of one type and day get distinct numbers, global across tenants (R10), and
// logitrack_app holds no privilege on task_number_counters (R66, R67).
func TestTaskNumbersGlobalConcurrentAndUnreachable(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	for _, priv := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
		var has bool
		if err := w.owner.QueryRow(ctx, `SELECT has_table_privilege('logitrack_app', 'task_number_counters', $1)`, priv).Scan(&has); err != nil {
			t.Fatal(err)
		}
		if has {
			t.Errorf("logitrack_app holds %s on task_number_counters", priv)
		}
	}
	var definer string
	if err := w.owner.QueryRow(ctx, `SELECT format('%s %s', pg_get_userbyid(proowner), prosecdef)
		FROM pg_proc WHERE oid = 'next_task_seq(text,date)'::regprocedure`).Scan(&definer); err != nil {
		t.Fatal(err)
	}
	if definer != "logitrack_rls_definer t" {
		t.Errorf("next_task_seq: %s, want SECURITY DEFINER owned by logitrack_rls_definer", definer)
	}
	for _, sql := range []string{`SELECT last_seq FROM task_number_counters`,
		`UPDATE task_number_counters SET last_seq = 0`,
		`INSERT INTO task_number_counters (task_type, plan_date, last_seq) VALUES ('first_mile', '2026-10-12', 99)`} {
		err := w.tx(w.staffO, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, sql); return err })
		wantAPIError(t, err, http.StatusForbidden, "permission_denied", "")
	}

	// 16 creations at once, half by the own fleet and half by an unrelated carrier, one type and Bangkok day: every
	// transaction holds the counter row until it commits, so the numbers are 001..016 with no repeat across tenants.
	planAt := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC) // 08:00 on 12 October in Bangkok
	const n = 16
	var (
		mu    sync.Mutex
		got   []string
		errs  []error
		start = make(chan struct{})
		wg    sync.WaitGroup
	)
	for i := range n {
		p, tenant := w.staffO, w.O
		if i%2 == 1 {
			p, tenant = w.staffC, w.C
		}
		wg.Go(func() {
			<-start
			err := w.tx(p, func(tx pgx.Tx) error {
				task, _, err := operations.CreateTask(ctx, tx, w.newTask(tenant, planAt))
				if err != nil {
					return err
				}
				time.Sleep(15 * time.Millisecond) // hold the counter row while the others queue on it
				mu.Lock()
				got = append(got, *task.TaskNo)
				mu.Unlock()
				return nil
			})
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	close(start)
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent creations failed: %v", errs)
	}
	slices.Sort(got)
	var want []string
	for i := 1; i <= n; i++ {
		want = append(want, operations.FormatTaskNo(operations.FirstMile, operations.PlanDate(planAt), i))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("numbers %v, want %v", got, want)
	}
	if want[0] != "FM-12102026-001" {
		t.Fatalf("first number %s", want[0])
	}

	// The counter is per (type, day): line haul starts again at 001; a rolled-back creation leaves no gap.
	lh := w.newTask(w.O, planAt)
	lh.TaskType = operations.LineHaul
	if no := *w.createTask(w.staffO, lh).TaskNo; no != "LH-12102026-001" {
		t.Errorf("line haul number %s", no)
	}
	err := w.tx(w.staffO, func(tx pgx.Tx) error {
		if _, _, err := operations.CreateTask(ctx, tx, w.newTask(w.O, planAt)); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatal(err)
	}
	if no := *w.createTask(w.staffO, w.newTask(w.O, planAt)).TaskNo; no != "FM-12102026-017" {
		t.Errorf("after a rollback: %s, want FM-12102026-017", no)
	}
	var last int
	if err := w.etl.QueryRow(ctx, `SELECT last_seq FROM task_number_counters WHERE task_type = 'first_mile' AND plan_date = '2026-10-12'`).Scan(&last); err != nil || last != 17 {
		t.Errorf("counter %d %v, want 17", last, err)
	}

	// A plan instant late at night UTC is the next Bangkok day: the number and plan_date agree.
	late := w.createTask(w.staffO, w.newTask(w.O, time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)))
	if *late.TaskNo != "FM-13102026-001" || late.PlanDate.Format(time.DateOnly) != "2026-10-13" {
		t.Errorf("late plan: %s on %s", *late.TaskNo, late.PlanDate)
	}

	// Concurrent assignments to one driver get distinct queue positions (max + 1 under the per-driver lock).
	var orders []int32
	start2 := make(chan struct{})
	for range 8 {
		wg.Go(func() {
			<-start2
			in := w.newTask(w.O, planAt)
			in.DriverID, in.Status = &w.dO1, operations.TaskAssigned
			_ = w.tx(w.staffO, func(tx pgx.Tx) error {
				task, _, err := operations.CreateTask(ctx, tx, in)
				if err != nil {
					return err
				}
				time.Sleep(10 * time.Millisecond)
				mu.Lock()
				orders = append(orders, *task.RunOrder)
				mu.Unlock()
				return nil
			})
		})
	}
	close(start2)
	wg.Wait()
	slices.Sort(orders)
	if !slices.Equal(orders, []int32{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Errorf("run orders %v, want 1..8", orders)
	}
}

// Legacy numbers are kept verbatim (not unique, unpadded); a number of record is never re-formatted.
func TestLegacyTaskNumbersKeptVerbatim(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	plan := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	for range 2 {
		in := w.newTask(w.O, plan)
		in.TaskNo = ptr("FM-01102026-7") // mobile check_in_page.dart numbering: unpadded, and repeated
		w.createTask(w.staffO, in)
	}
	got, err := w.svc.TasksByNumber(ctx, w.staffO, "FM-01102026-7")
	if err != nil || len(got) != 2 {
		t.Fatalf("by legacy number: %d %v", len(got), err)
	}
	if got, err := w.svc.TasksByNumber(ctx, w.staffO, "FM-01102026-007"); err != nil || len(got) != 0 {
		t.Fatalf("the padded form matched a legacy number: %d %v", len(got), err)
	}
	if got, err := w.svc.TasksByNumber(ctx, w.staffC, "FM-01102026-7"); err != nil || len(got) != 0 {
		t.Fatalf("another tenant read the numbers: %d %v", len(got), err)
	}
	// Explicit numbers do not move the counter (ETL and cmd/seed seed it past the legacy suffixes, Appendix A §A.3.5).
	if no := *w.createTask(w.staffO, w.newTask(w.O, plan)).TaskNo; no != "FM-01102026-001" {
		t.Errorf("allocated %s", no)
	}
}

// AC: trip_no is UNIQUE and mutable, with the charset CHECK of core/tripDocId.ts.
func TestTripNoUniqueMutableAndChecked(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	in := w.newTask(w.O, now)
	in.DriverID = &w.dO1
	task := w.createTask(w.staffO, in)
	trip := w.createTrip(w.staffO, w.newTrip(task, " ZXZB26011192271 ")) // trimmed, as renameTripRecord trims
	if trip.TripNo != "ZXZB26011192271" {
		t.Fatalf("trip_no %q", trip.TripNo)
	}

	// UNIQUE: a second trip with the number is 409 duplicate_trip_id; the Go rule is 422 before any write.
	err := w.tx(w.staffO, func(tx pgx.Tx) error {
		_, err := operations.CreateTrip(ctx, tx, w.newTrip(task, "ZXZB26011192271"))
		return err
	})
	wantAPIError(t, err, http.StatusConflict, operations.CodeDuplicateTripID, "tripNo")
	err = w.tx(w.staffO, func(tx pgx.Tx) error {
		_, err := operations.CreateTrip(ctx, tx, w.newTrip(task, "ZXZB/1"))
		return err
	})
	wantAPIError(t, err, http.StatusUnprocessableEntity, "invalid_argument", "tripNo")

	// The CHECK holds the same rule for every writer (the ETL, a raw statement): exactly what validateTripDocId refuses.
	for _, c := range []struct {
		no string
		ok bool
	}{
		{"", false}, {"a/b", false}, {".", false}, {"..", false}, {"__reserved__", false},
		{strings.Repeat("A", 1501), false}, {strings.Repeat("ก", 501), false},
		{"__name", true}, {"...", true}, {strings.Repeat("ก", 500), true}, {"SPX-123_456", true},
	} {
		err := w.tx(w.staffO, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type)
				VALUES ($1, 'driver', $2, 'in_transit', 'first_mile')`, w.O, c.no); err != nil {
				return err
			}
			return errRollback
		})
		if c.ok != errors.Is(err, errRollback) || (c.ok == (operations.ValidateTripNo(c.no) != "")) {
			t.Errorf("trip_no %.12q: %v (Go rule %q)", c.no, err, operations.ValidateTripNo(c.no))
		}
		if !c.ok {
			wantAPIError(t, err, http.StatusUnprocessableEntity, "invalid_argument", "tripNo")
		}
	}

	// Mutable: a rename is refused while the trip runs (the driver's app still holds it), then changes the number in
	// place with one history row; the id, and so photos and incidents, stay.
	rename := func(id uuid.UUID, no string) (operations.Trip, operations.TripRename, error) {
		var (
			tr operations.Trip
			h  operations.TripRename
		)
		err := w.tx(w.staffO, func(tx pgx.Tx) error {
			var err error
			tr, h, err = operations.RenameTrip(ctx, tx, id, no, &w.userO, now)
			return err
		})
		return tr, h, err
	}
	_, _, err = rename(trip.ID, "ZXZB26011192272")
	wantAPIError(t, err, http.StatusConflict, "failed_precondition", "")
	if err := w.tx(w.staffO, func(tx pgx.Tx) error {
		_, err := operationsdb.New(tx).DeliverTrip(ctx, operationsdb.DeliverTripParams{ID: trip.ID, DeliveredAt: now, DeliveredVia: "admin_web"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	renamed, h, err := rename(trip.ID, " ZXZB26011192272 ")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != trip.ID || renamed.TripNo != "ZXZB26011192272" || h.OldTripNo != "ZXZB26011192271" ||
		h.NewTripNo != "ZXZB26011192272" || h.RenamedBy == nil || *h.RenamedBy != w.userO || !h.RenamedAt.Equal(now) {
		t.Fatalf("rename: %+v %+v", renamed, h)
	}
	_, _, err = rename(trip.ID, "ZXZB26011192272")
	wantAPIError(t, err, http.StatusUnprocessableEntity, "invalid_argument", "tripNo")

	d, isOld, err := w.svc.TripByNo(ctx, w.staffO, "ZXZB26011192272")
	if err != nil || isOld || d.Trip.ID != trip.ID || len(d.Renames) != 1 {
		t.Fatalf("by the new number: %+v %t %v", d.Trip.TripNo, isOld, err)
	}
	d, isOld, err = w.svc.TripByNo(ctx, w.staffO, "ZXZB26011192271")
	if err != nil || !isOld || d.Trip.ID != trip.ID {
		t.Fatalf("by the pre-rename number: %t %v", isOld, err)
	}
	if _, _, err := w.svc.TripByNo(ctx, w.staffC, "ZXZB26011192272"); err == nil {
		t.Fatal("another tenant found the trip by number")
	}

	// UNIQUE covers current numbers only: the old number is free for another trip, and then names that trip; a rename
	// onto a held number is 409.
	other := w.createTrip(w.staffO, w.newTrip(task, "ZXZB26011192271"))
	if d, isOld, err := w.svc.TripByNo(ctx, w.staffO, "ZXZB26011192271"); err != nil || isOld || d.Trip.ID != other.ID {
		t.Fatalf("the freed number: %t %v", isOld, err)
	}
	_, _, err = rename(trip.ID, "ZXZB26011192271")
	wantAPIError(t, err, http.StatusConflict, operations.CodeDuplicateTripID, "tripNo")

	// trip_no_history is append-only for the app role.
	for _, sql := range []string{`UPDATE trip_no_history SET new_trip_no = 'X'`, `DELETE FROM trip_no_history`} {
		err := w.tx(w.staffO, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, sql); return err })
		wantAPIError(t, err, http.StatusForbidden, "permission_denied", "")
	}
}

// AC: stop_{i}_* photo types are accepted, unknown types stored with photo_type_known = false (R19); photos merge by
// type and read back in the ADR 0018 workflow order.
func TestTripPhotoTypes(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	in := w.newTask(w.O, now)
	in.DriverID = &w.dO1
	task := w.createTask(w.staffO, in)
	trip := w.createTrip(w.staffO, w.newTrip(task, "36601950"))

	types := []string{"seal", "checkin_app", "stop_1_arrived", "stop_12_opening", "stop_3_runsheet_received",
		"runsheet_extra_2", "selfie", "stop_1_seal", "Seal", "stop_x_arrived"}
	// The trip's driver writes its photos (trip_photos: driver writes, R28).
	if err := w.tx(w.driverO1, func(tx pgx.Tx) error {
		for i, typ := range types {
			if _, err := operations.PutTripPhoto(ctx, tx, trip.ID, operations.PhotoInput{Type: typ, FileID: w.files[i%len(w.files)]}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Another driver may not.
	err := w.tx(w.driverO2, func(tx pgx.Tx) error {
		_, err := operations.PutTripPhoto(ctx, tx, trip.ID, operations.PhotoInput{Type: "seal", FileID: w.files[0]})
		return err
	})
	wantAPIError(t, err, http.StatusForbidden, "permission_denied", "")

	detail, err := w.svc.GetTrip(ctx, w.staffO, trip.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Photos) != len(types) {
		t.Fatalf("%d photos, want %d", len(detail.Photos), len(types))
	}
	var unknown []string
	for _, ph := range detail.Photos {
		if ph.PhotoTypeKnown == nil || *ph.PhotoTypeKnown != operations.PhotoTypeKnown(ph.PhotoType) {
			t.Errorf("%s: photo_type_known %v, Go %t", ph.PhotoType, ph.PhotoTypeKnown, operations.PhotoTypeKnown(ph.PhotoType))
		}
		if !*ph.PhotoTypeKnown {
			unknown = append(unknown, ph.PhotoType)
		}
	}
	slices.Sort(unknown)
	if want := []string{"Seal", "selfie", "stop_1_seal", "stop_x_arrived"}; !slices.Equal(unknown, want) {
		t.Errorf("unknown types %v, want %v", unknown, want)
	}
	var order []string
	for _, ph := range detail.Photos {
		order = append(order, ph.PhotoType)
	}
	// stop_1_seal is a stop photo of an unknown subtype: after the stop's known subtypes, before stop 3 (3019).
	if want := []string{"checkin_app", "runsheet_extra_2", "seal", "stop_1_arrived", "stop_1_seal",
		"stop_3_runsheet_received", "stop_12_opening", "selfie", "Seal", "stop_x_arrived"}; !slices.Equal(order, want) {
		t.Errorf("photo order %v, want %v", order, want)
	}

	// Merge by type: a second seal replaces the first (one row, the new file, moved to the end of the stored order).
	if err := w.tx(w.staffO, func(tx pgx.Tx) error {
		_, err := operations.PutTripPhoto(ctx, tx, trip.ID, operations.PhotoInput{Type: "seal", FileID: w.files[7]})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var rows []operations.TripPhoto
	var unknownRows []operations.TripPhoto
	if err := w.tx(w.staffO, func(tx pgx.Tx) error {
		var err error
		q := operationsdb.New(tx)
		if rows, err = q.TripPhotos(ctx, trip.ID); err != nil {
			return err
		}
		unknownRows, err = q.UnknownTripPhotos(ctx, trip.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	seals := slices.DeleteFunc(slices.Clone(rows), func(p operations.TripPhoto) bool { return p.PhotoType != "seal" })
	if len(rows) != len(types) || len(seals) != 1 || seals[0].FileID != w.files[7] || rows[len(rows)-1].PhotoType != "seal" {
		t.Errorf("merge by type: %d rows, seals %+v", len(rows), seals)
	}
	if len(unknownRows) != 4 {
		t.Errorf("%d unknown rows", len(unknownRows))
	}

	// An empty type is refused by Go (422) and by the CHECK (photo_type <> '').
	err = w.tx(w.staffO, func(tx pgx.Tx) error {
		_, err := operations.PutTripPhoto(ctx, tx, trip.ID, operations.PhotoInput{Type: "  ", FileID: w.files[0]})
		return err
	})
	wantAPIError(t, err, http.StatusUnprocessableEntity, "invalid_argument", "photos.type")
	err = w.tx(w.staffO, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO trip_photos (trip_id, photo_type, file_id) VALUES ($1, '', $2)`, trip.ID, w.files[0])
		return err
	})
	wantAPIError(t, err, http.StatusUnprocessableEntity, "invalid_argument", "")
}

// The reach: RLS and the named predicates agree (contractor reach one way only, a driver's own and helper tasks), the
// pages walk the whole list, and the standby billing columns reach only the billing carrier (Appendix C §C.3.4).
func TestReachPagesAndStandbyProjection(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 5, 2, 0, 0, 0, time.UTC)
	var mine []uuid.UUID
	for i := range 7 {
		in := w.newTask(w.O, base.Add(time.Duration(i)*24*time.Hour))
		mine = append(mine, w.createTask(w.staffO, in).ID)
	}
	inA := w.newTask(w.A, base)
	inA.DriverID, inA.Status = &w.dA1, operations.TaskAssigned
	taskA := w.createTask(w.staffA, inA)
	taskC := w.createTask(w.staffC, w.newTask(w.C, base))
	helped := w.newTask(w.O, base)
	helped.DriverID, helped.HelperDriverID, helped.Status = &w.dO2, &w.dO1, operations.TaskAssigned
	helpedTask := w.createTask(w.staffO, helped)

	if _, err := w.svc.GetTask(ctx, w.staffO, taskA.ID); err != nil {
		t.Errorf("own fleet reads its carrier's task: %v", err)
	}
	for _, c := range []struct {
		p  *authz.Principal
		id uuid.UUID
	}{{w.staffA, mine[0]}, {w.staffC, taskA.ID}, {w.staffA, taskC.ID}, {w.driverO1, mine[0]}} {
		_, err := w.svc.GetTask(ctx, c.p, c.id)
		wantAPIError(t, err, http.StatusNotFound, "not_found", "")
	}
	if d, err := w.svc.GetTask(ctx, w.driverO1, helpedTask.ID); err != nil || d.Task.ID != helpedTask.ID {
		t.Errorf("the helper reads the task it helps on (R28): %v", err)
	}

	// Keyset pages of 3 over the own fleet's 8 tasks + the carrier's 1: every row once, newest first.
	var seen []uuid.UUID
	cur := ""
	for range 10 {
		pg, err := w.svc.ListTasks(ctx, w.staffO, operations.TaskFilter{TaskType: operations.FirstMile, Limit: 3, Cursor: cur})
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range pg.Items {
			seen = append(seen, task.ID)
		}
		if cur = pg.NextCursor; cur == "" {
			break
		}
	}
	if len(seen) != 9 || len(slices.Compact(slices.Clone(seen))) != 9 || !slices.Contains(seen, taskA.ID) || slices.Contains(seen, taskC.ID) {
		t.Errorf("pages: %d rows %v", len(seen), seen)
	}
	pgA, err := w.svc.ListTasks(ctx, w.staffA, operations.TaskFilter{})
	if err != nil || len(pgA.Items) != 1 || pgA.Items[0].ID != taskA.ID {
		t.Errorf("carrier list: %v %v", pgA.Items, err)
	}
	// The plan axis: three Bangkok days, latest first, cursor of the plan axis only.
	from, to := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	pg, err := w.svc.ListTasks(ctx, w.staffO, operations.TaskFilter{From: &from, To: &to, Limit: 2})
	if err != nil || len(pg.Items) != 2 || pg.Items[0].PlanDate.Format(time.DateOnly) != "2026-10-08" || pg.NextCursor == "" {
		t.Fatalf("plan page: %v %v", pg, err)
	}
	pg2, err := w.svc.ListTasks(ctx, w.staffO, operations.TaskFilter{From: &from, To: &to, Limit: 2, Cursor: pg.NextCursor})
	if err != nil || len(pg2.Items) != 1 || pg2.Items[0].PlanDate.Format(time.DateOnly) != "2026-10-06" || pg2.NextCursor != "" {
		t.Fatalf("plan page 2: %v %v", pg2, err)
	}
	_, err = w.svc.ListTasks(ctx, w.staffO, operations.TaskFilter{Cursor: pg.NextCursor})
	wantAPIError(t, err, http.StatusUnprocessableEntity, "invalid_argument", "cursor")

	// The driver's queue: own and helper tasks.
	if err := w.tx(w.driverO1, func(tx pgx.Tx) error {
		rows, err := operationsdb.New(tx).DriverTasks(ctx, operationsdb.DriverTasksParams{DriverID: w.dO1, RowLimit: 50})
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].ID != helpedTask.ID {
			t.Errorf("driver queue %v", rows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Standby billing: a record of carrier A priced by the own fleet's standby rate, and an unpriced own-fleet record.
	rate := w.id(`INSERT INTO standby_rate_entries (tenant_id, billing_party_id, rate_thb, effective_from_date, effective_from_at)
		VALUES ($1, $2, 1500, '2026-10-01', '2026-09-30T17:00:00Z') RETURNING id`, w.O, w.partyX)
	var stA, stO operations.Standby
	if err := w.tx(w.staffA, func(tx pgx.Tx) error {
		var err error
		stA, _, err = operations.CreateStandby(ctx, tx, operations.NewStandby{StandbyInsert: operations.StandbyInsert{
			TenantID: w.A, TenantSource: "task", DriverID: &w.dA1, TaskID: &taskA.ID, StartedAt: ptr(base),
			EndedAt: ptr(base.Add(3 * time.Hour)), CustomerPartyID: &w.partyX, CustomerResolved: true},
			Photos: []operations.StandbyPhotoInput{{Type: "site_photo", FileID: w.files[0]}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.tx(w.staffO, func(tx pgx.Tx) error {
		var err error
		stO, _, err = operations.CreateStandby(ctx, tx, operations.NewStandby{StandbyInsert: operations.StandbyInsert{
			TenantID: w.O, TenantSource: "task", TaskID: &mine[0], StartedAt: ptr(base)}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	w.exec(`UPDATE standby_records SET billing_estimate_thb = 1500, billing_party_id = $2, billing_rate_source = 'standby_rate',
		billing_rate_entry_id = $3, billing_computed_at = now() WHERE id = $1`, stA.ID, w.partyX, rate)
	w.exec(`UPDATE standby_records SET billing_unpriced_reason = 'no_ended_at', billing_computed_at = now() WHERE id = $1`, stO.ID)

	for _, c := range []struct {
		name    string
		p       *authz.Principal
		id      uuid.UUID
		billing bool
	}{
		{"own fleet, its rate", w.staffO, stA.ID, true},
		{"carrier A, the own fleet's rate", w.staffA, stA.ID, false},
		{"own fleet, unpriced", w.staffO, stO.ID, true},
	} {
		v, photos, err := w.svc.GetStandby(ctx, c.p, c.id)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if v.Billing != c.billing || (v.Standby.BillingRateSource != nil) != (c.billing && c.id == stA.ID) {
			t.Errorf("%s: billing %t %+v", c.name, v.Billing, v.Standby.BillingRateSource)
		}
		if c.id == stA.ID && len(photos) != 1 {
			t.Errorf("%s: %d photos", c.name, len(photos))
		}
	}
	list, err := w.svc.ListStandby(ctx, w.staffA, operations.StandbyFilter{})
	if err != nil || len(list.Items) != 1 || list.Items[0].Billing || list.Items[0].Standby.BillingEstimateThb != nil {
		t.Errorf("carrier standby list: %+v %v", list.Items, err)
	}
	ended, err := w.svc.ListStandby(ctx, w.staffO, operations.StandbyFilter{Sort: operations.StandbyByEnded})
	if err != nil || len(ended.Items) != 1 || ended.Items[0].Standby.ID != stA.ID || !ended.Items[0].Billing {
		t.Errorf("own fleet ended axis: %+v %v", ended.Items, err)
	}
}

// Delivery stops (addDeliveryStop) and the offline replays of driver-created rows (R63).
func TestStopsAndReplays(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	in := w.newTask(w.O, now)
	in.DriverID, in.Status = &w.dO1, operations.TaskAssigned
	task := w.createTask(w.staffO, in)

	add := func(p *authz.Principal, dest string) ([]operations.TaskStop, error) {
		var stops []operations.TaskStop
		err := w.tx(p, func(tx pgx.Tx) error {
			var err error
			stops, err = operations.AddTaskStop(ctx, tx, task.ID, operations.NewStop{Destination: dest, SourceID: ptr(" SPK-GW ")}, now)
			return err
		})
		return stops, err
	}
	// The assigned driver adds a stop mid-trip (the shim target's caller): stop 1 is the task's own destination.
	stops, err := add(w.driverO1, " spk-gw ")
	if err != nil {
		t.Fatal(err)
	}
	if len(stops) != 2 || stops[0].DestinationRaw != "SOCE" || stops[0].StopIndex != 1 || stops[1].DestinationRaw != "SPK-GW" ||
		stops[1].StopIndex != 2 || *stops[1].Sequence != 1 || *stops[1].SourceID != "SPK-GW" {
		t.Fatalf("stops %+v", stops)
	}
	d, err := w.svc.GetTask(ctx, w.staffO, task.ID)
	if err != nil || !d.Task.IsMultiDelivery || len(d.Stops) != 2 {
		t.Fatalf("task after the first stop: %+v %v", d.Task.IsMultiDelivery, err)
	}
	for _, dup := range []string{"SPK-GW", "soce"} {
		_, err := add(w.staffO, dup)
		wantAPIError(t, err, http.StatusConflict, operations.CodeDuplicateDestination, "destination")
	}
	if _, err := add(w.driverO2, "OTHER"); err == nil {
		t.Fatal("another driver added a stop")
	}

	// A planned multi-drop task.
	multi := w.newTask(w.O, now)
	multi.IsMultiDelivery = true
	multi.Stops = []operations.NewStop{{Destination: "SOCE"}, {Destination: "SOCN"}, {Destination: "SOCW"}}
	mt := w.createTask(w.staffO, multi)
	if d, err := w.svc.GetTask(ctx, w.staffO, mt.ID); err != nil || len(d.Stops) != 3 || d.Stops[2].DestinationRaw != "SOCW" {
		t.Fatalf("planned stops: %+v %v", d.Stops, err)
	}

	// The driver's manual task: a replay of the same client op returns the stored task and allocates nothing.
	op := uuid.Must(uuid.NewV7())
	manual := w.newTask(w.O, now)
	manual.TenantSource, manual.DriverID, manual.ClientOpID, manual.CreatedBy = "driver", &w.dO1, &op, nil
	var first, again operations.Task
	var replayed bool
	for i, dst := range []*operations.Task{&first, &again} {
		if err := w.tx(w.driverO1, func(tx pgx.Tx) error {
			var err error
			*dst, replayed, err = operations.CreateTask(ctx, tx, manual)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if replayed != (i == 1) {
			t.Errorf("call %d replayed = %t", i+1, replayed)
		}
	}
	if first.ID != again.ID || *first.TaskNo != *again.TaskNo {
		t.Errorf("replay created another task: %s %s", *first.TaskNo, *again.TaskNo)
	}
	if next := *w.createTask(w.staffO, w.newTask(w.O, now)).TaskNo; next != operations.FormatTaskNo(operations.FirstMile, operations.PlanDate(now), 4) {
		t.Errorf("a replay consumed a number: next %s", next)
	}

	// Standby and incident replays.
	trip := w.createTrip(w.driverO1, w.newTrip(task, "SPX-77"))
	sop, iop := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var s1, s2 operations.Standby
	var i1, i2 operations.Incident
	for k := range 2 {
		if err := w.tx(w.driverO1, func(tx pgx.Tx) error {
			s, rs, err := operations.CreateStandby(ctx, tx, operations.NewStandby{StandbyInsert: operations.StandbyInsert{
				TenantID: w.O, TenantSource: "task", DriverID: &w.dO1, TaskID: &task.ID, TripID: &trip.ID, ClientOpID: &sop}})
			if err != nil {
				return err
			}
			inc, ri, err := operations.CreateIncident(ctx, tx, operations.IncidentInsert{TenantID: w.O, TenantSource: "trip",
				DriverID: &w.dO1, TripID: &trip.ID, DelayCause: ptr("incident_cause_traffic"), ReportedByKind: "driver", ClientOpID: &iop})
			if err != nil {
				return err
			}
			if rs != (k == 1) || ri != (k == 1) {
				t.Errorf("call %d replayed: standby %t incident %t", k+1, rs, ri)
			}
			if k == 0 {
				s1, i1 = s, inc
			} else {
				s2, i2 = s, inc
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if s1.ID != s2.ID || i1.ID != i2.ID {
		t.Error("a replay created another row")
	}
	inc, err := w.svc.ListIncidents(ctx, w.staffO, operations.IncidentFilter{TripID: &trip.ID})
	if err != nil || len(inc.Items) != 1 {
		t.Errorf("incidents of the trip: %d %v", len(inc.Items), err)
	}
}

// The deferred tenant triggers refuse a child off its parent's tenant at COMMIT (409 failed_precondition), even in a
// system transaction that RLS does not restrict.
func TestChildStaysOnParentTenant(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	task := w.createTask(w.staffO, w.newTask(w.O, now))
	in := w.newTrip(task, "OFF-TENANT")
	in.TenantID = w.C
	err := operations.MapError(db.WithSystem(ctx, w.pool, nil, func(tx pgx.Tx) error {
		_, err := operations.CreateTrip(ctx, tx, in)
		return err
	}))
	wantAPIError(t, err, http.StatusConflict, "failed_precondition", "")
	// Staff cannot even write it: the C row is outside the writer's reach (RLS WITH CHECK).
	err = w.tx(w.staffO, func(tx pgx.Tx) error {
		_, err := operations.CreateTrip(ctx, tx, in)
		return err
	})
	wantAPIError(t, err, http.StatusForbidden, "permission_denied", "")
}
