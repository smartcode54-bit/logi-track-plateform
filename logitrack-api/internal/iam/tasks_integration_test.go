//go:build integration

package iam_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

// The R13 task reassignment of T28 (issue #48; Appendix C §C.1.7, §C.3.6, §C.9.2 #12): iam.TaskMover behind
// operations.Service.ReassignTask and PATCH /v1/tasks/{id}, against PostgreSQL 18 (pgtest) as logitrack_app.
// Principals are built here (their capability sets are what RBAC would resolve), fixture rows written as
// logitrack_etl. Fixture: own fleet O; carriers A and B working for O; independent carrier C; the dispatcher
// organisation D (TTP) whose operation_staff member holds a dispatcher grant over customer X; a suspended carrier S.

var tmNow = time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)

type tmWorld struct {
	t    *testing.T
	pool *pgxpool.Pool
	etl  *pgx.Conn
	svc  *operations.Service

	O, A, B, C, D, S              uuid.UUID
	partyX, partyY                uuid.UUID
	dA1, dB1, dB2, dC1, dD1       uuid.UUID // dB2 is inactive
	truckB, truckC                uuid.UUID
	disp, staffA, staffB, pa, paR *authz.Principal
}

func newTMWorld(t *testing.T) *tmWorld {
	t.Helper()
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := &tmWorld{t: t, pool: d.Pool(t, db.RoleApp)}
	var err error
	if w.etl, err = pgx.Connect(ctx, d.URL(db.RoleETL)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.etl.Close(context.Background()) })
	mover, err := iam.NewTaskMover(iam.TaskMoverDeps{Pool: w.pool, Log: zerolog.Nop(), Now: func() time.Time { return tmNow }})
	if err != nil {
		t.Fatal(err)
	}
	if w.svc, err = operations.New(operations.Deps{Pool: w.pool, Log: zerolog.Nop(), Reassigner: mover}); err != nil {
		t.Fatal(err)
	}
	carrier := func(code string, contractor *uuid.UUID, status string) uuid.UUID {
		return w.id(`INSERT INTO tenants (kind, name_th, code, legal_type, contractor_tenant_id, status) VALUES ('carrier', $1::text, $1::text, 'company', $2, $3) RETURNING id`,
			code, contractor, status)
	}
	w.O = w.id(`INSERT INTO tenants (kind, name_th, code) VALUES ('own_fleet', 'OWN', 'OWN') RETURNING id`)
	w.A, w.B = carrier("CA", &w.O, "active"), carrier("CB", &w.O, "active")
	w.C, w.D, w.S = carrier("CC", nil, "active"), carrier("TTP", nil, "active"), carrier("CS", nil, "suspended")
	party := func(code string) uuid.UUID {
		c := w.id(`INSERT INTO customers (code, name) VALUES ($1::text, $1::text) RETURNING id`, code)
		return w.id(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id`, c)
	}
	w.partyX, w.partyY = party("CJSF"), party("SPX")
	driver := func(tenant uuid.UUID, name, status string) uuid.UUID {
		return w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile, status) VALUES ($1, 'self', $2, 'T', '0800000000', $3) RETURNING id`,
			tenant, name, status)
	}
	w.dA1, w.dB1, w.dB2 = driver(w.A, "A1", "active"), driver(w.B, "B1", "active"), driver(w.B, "B2", "inactive")
	w.dC1, w.dD1 = driver(w.C, "C1", "active"), driver(w.D, "D1", "active")
	truck := func(tenant uuid.UUID, plate string) uuid.UUID {
		return w.id(`INSERT INTO trucks (tenant_id, tenant_source, license_plate, province, status, brand, model, year, color, type_raw, vehicle_class)
			VALUES ($1, 'self', $2, 'กรุงเทพมหานคร', 'active', 'ISUZU', 'FRR', 2020, 'white', '6 Wheels', '6WH') RETURNING id`, tenant, plate)
	}
	w.truckB, w.truckC = truck(w.B, "1กข-2222"), truck(w.C, "2กข-3333")
	user := func(email string) uuid.UUID {
		return w.id(`INSERT INTO users (email, display_name) VALUES ($1::text, $1::text) RETURNING id`, email)
	}
	manage := authz.NewCapSet(authz.OperationsManageTasks)
	w.disp = &authz.Principal{UserID: user("dispatch@ttp.test"), TenantID: &w.D, TenantRole: authz.OperationStaff,
		TenantKind: "carrier", Dispatcher: true, PartyIDs: []uuid.UUID{w.partyX}, Caps: manage}
	w.staffA = &authz.Principal{UserID: user("admin@a.test"), TenantID: &w.A, TenantRole: authz.TenantAdmin, TenantKind: "carrier", Caps: manage}
	w.staffB = &authz.Principal{UserID: user("admin@b.test"), TenantID: &w.B, TenantRole: authz.TenantAdmin, TenantKind: "carrier", Caps: manage}
	w.pa = &authz.Principal{UserID: user("pa@platform.test"), Platform: []authz.PlatformRole{authz.PlatformAdmin},
		Caps: authz.NewCapSet(authz.PlatformCrossTenantRead, authz.PlatformCrossTenantWrite)}
	w.paR = &authz.Principal{UserID: user("support@platform.test"), Platform: []authz.PlatformRole{authz.PlatformAdmin},
		Caps: authz.NewCapSet(authz.PlatformCrossTenantRead)}
	return w
}

func (w *tmWorld) id(sql string, args ...any) uuid.UUID {
	w.t.Helper()
	var id uuid.UUID
	if err := w.etl.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		w.t.Fatalf("fixture: %v\n%s", err, sql)
	}
	return id
}

func (w *tmWorld) count(sql string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.etl.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		w.t.Fatalf("%v\n%s", err, sql)
	}
	return n
}

// tmTask is a task row of the fixture.
type tmTask struct {
	tenant          uuid.UUID
	source, status  string
	party           *uuid.UUID
	driver, helper  *uuid.UUID
	truck           *uuid.UUID
	runOrder        *int32
	legacyDriverRef *string
}

func (w *tmWorld) task(f tmTask) uuid.UUID {
	if f.source == "" {
		f.source = "form"
	}
	if f.status == "" {
		f.status = "pending"
	}
	return w.id(`INSERT INTO tasks (tenant_id, tenant_source, task_no, task_type, status, plan_at, plan_time, source_hub_raw, destination_raw,
			billing_party_id, driver_id, helper_driver_id, truck_id, run_order, legacy_driver_ref)
		VALUES ($1, $2, 'FM-11102026-' || lpad((floor(random() * 900) + 100)::text, 3, '0'), 'first_mile', $3, $4, '09:00', 'ALANG-A', 'SOCE',
			$5, $6, $7, $8, $9, $10) RETURNING id`,
		f.tenant, f.source, f.status, tmNow, f.party, f.driver, f.helper, f.truck, f.runOrder, f.legacyDriverRef)
}

func (w *tmWorld) reassign(p *authz.Principal, task uuid.UUID, in operations.Reassign) (operations.Reassigned, error) {
	return w.svc.ReassignTask(context.Background(), p, "req-t28", task, in)
}

func tmCode(err error) (code, detail string) {
	var he *httpx.Error
	if !errors.As(err, &he) {
		return "", ""
	}
	if r, ok := he.Details["reason"].(string); ok {
		return he.Code, r
	}
	if f, ok := he.Details["fields"].([]httpx.FieldViolation); ok && len(f) > 0 {
		return he.Code, f[0].Field + " " + f[0].Reason
	}
	return he.Code, ""
}

func idp(id uuid.UUID) *uuid.UUID { return &id }

// Appendix C §C.9.2 #12 and the issue's acceptance criteria: the dispatcher D cannot update a task of another
// carrier, creates a call-out in D with tenant_source 'form', and moves it to carrier B with B's driver only through
// the tenant-move path, which writes task_tenant_reassigned in the same transaction.
func TestReassignDispatcherCallOut(t *testing.T) {
	w := newTMWorld(t)
	ctx := context.Background()

	ofA := w.task(tmTask{tenant: w.A, party: &w.partyX})
	var tag pgconn.CommandTag
	if err := db.WithPrincipal(ctx, w.pool, w.disp, func(tx pgx.Tx) error {
		var err error
		tag, err = tx.Exec(ctx, `UPDATE tasks SET plan_time = '10:00' WHERE id = $1`, ofA)
		return err
	}); err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("dispatcher updated a task of A: %v rows, %v", tag.RowsAffected(), err)
	}

	var callOut operations.Task
	if err := w.svc.Tx(ctx, w.disp, func(tx pgx.Tx) error {
		res, err := operations.Stamp(ctx, tx, w.disp, operations.TableTasks, operations.Links{})
		if err != nil {
			return err
		}
		callOut, _, err = operations.CreateTask(ctx, tx, operations.NewTask{TaskInsert: operations.TaskInsert{
			TenantID: uuid.MustParse(res.TenantID), TenantSource: string(res.Source), TaskType: operations.FirstMile,
			Status: operations.TaskPending, PlanAt: tmNow, SourceHubRaw: "TTP-HUB1", DestinationRaw: "SOCE",
			BillingPartyID: &w.partyX, CreatedBy: &w.disp.UserID}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if callOut.TenantID != w.D || callOut.TenantSource != "form" {
		t.Fatalf("call-out %s %s, want D form", callOut.TenantID, callOut.TenantSource)
	}

	out, err := w.reassign(w.disp, callOut.ID, operations.Reassign{TenantID: w.B, DriverSet: true, DriverID: &w.dB1})
	if err != nil {
		t.Fatal(err)
	}
	if out.TenantID != w.B || out.TenantSource != "form" || out.PreviousTenantID != w.D || out.Status != operations.TaskAssigned ||
		out.DriverID == nil || *out.DriverID != w.dB1 || out.PreviousDriverID != nil || out.RunOrder == nil || *out.RunOrder != 1 || !out.Moved {
		t.Fatalf("reassigned %+v", out)
	}
	var tenant uuid.UUID
	var source, status string
	var driver *uuid.UUID
	if err := w.etl.QueryRow(ctx, `SELECT tenant_id, tenant_source, status, driver_id FROM tasks WHERE id = $1`, callOut.ID).
		Scan(&tenant, &source, &status, &driver); err != nil || tenant != w.B || source != "form" || status != "assigned" ||
		driver == nil || *driver != w.dB1 {
		t.Fatalf("row %s %s %s %v %v", tenant, source, status, driver, err)
	}
	var actor, evTenant uuid.UUID
	var sev string
	var details []byte
	if err := w.etl.QueryRow(ctx, `SELECT actor_user_id, tenant_id, severity, details FROM security_events WHERE event_type = $1`,
		iam.EventTaskTenantReassigned).Scan(&actor, &evTenant, &sev, &details); err != nil {
		t.Fatalf("task_tenant_reassigned: %v", err)
	}
	var det map[string]any
	_ = json.Unmarshal(details, &det)
	if actor != w.disp.UserID || evTenant != w.B || sev != "warning" || det["from"] != w.D.String() || det["to"] != w.B.String() ||
		det["by"] != "dispatcher" || det["driverId"] != w.dB1.String() || det["taskId"] != callOut.ID.String() {
		t.Fatalf("event actor %s tenant %s %s details %s", actor, evTenant, sev, details)
	}
	var topics []string
	var payload []byte
	if err := w.etl.QueryRow(ctx, `SELECT realtime_topics, payload FROM outbox_events WHERE routing_key = 'task.reassigned' AND aggregate_id = $1`,
		callOut.ID.String()).Scan(&topics, &payload); err != nil {
		t.Fatalf("outbox task.reassigned: %v", err)
	}
	for _, want := range []string{"tenant:" + w.B.String() + ":tasks", "tenant:" + w.D.String() + ":tasks",
		"dispatch:" + w.partyX.String() + ":tasks", "driver:" + w.dB1.String()} {
		if !slices.Contains(topics, want) {
			t.Errorf("topic %s missing from %v", want, topics)
		}
	}
	var pl map[string]any
	_ = json.Unmarshal(payload, &pl)
	if pl["driverId"] != w.dB1.String() || pl["previousDriverId"] != nil || pl["id"] != callOut.ID.String() {
		t.Fatalf("payload %s", payload)
	}
	// The call-out left the dispatcher's write reach but stays in its scope (billing party X).
	if err := db.WithPrincipal(ctx, w.pool, w.disp, func(tx pgx.Tx) error {
		_, found, err := tenancy.TenantOf(ctx, tx, "tasks", callOut.ID)
		if err == nil && !found {
			err = errors.New("the dispatcher lost sight of its call-out")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// AC: an UPDATE ... SET tenant_id outside a move path fails, even for staff of the task's tenant.
	err = db.WithPrincipal(ctx, w.pool, w.staffB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tasks SET tenant_id = $1 WHERE id = $2`, w.D, callOut.ID)
		return err
	})
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23000" {
		t.Fatalf("staff B moved the task by UPDATE: %v", err)
	}
}

func TestReassignRules(t *testing.T) {
	w := newTMWorld(t)
	ctx := context.Background()
	q := uuid.MustParse(tenancy.QuarantineTenantID)
	moveTo := func(to uuid.UUID) operations.Reassign { return operations.Reassign{TenantID: to} }
	withDriver := func(to uuid.UUID, d *uuid.UUID) operations.Reassign {
		return operations.Reassign{TenantID: to, DriverSet: true, DriverID: d}
	}
	machine := &authz.Principal{UserID: uuid.New(), AMR: authz.AMRAPIKey, TenantID: &w.D, Dispatcher: true,
		Caps: authz.NewCapSet(authz.OperationsManageTasks)}

	open := w.task(tmTask{tenant: w.D, party: &w.partyX})
	outOfScope := w.task(tmTask{tenant: w.A, party: &w.partyY})
	withTrip := w.task(tmTask{tenant: w.D, party: &w.partyX, status: "assigned", driver: &w.dD1, runOrder: ptr(int32(1))})
	w.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, task_id, driver_id)
		VALUES ($1, 'task', 'ZXJB26101100099', 'in_transit', 'first_mile', $2, $3) RETURNING id`, w.D, withTrip, w.dD1)
	checkedIn := w.task(tmTask{tenant: w.D, party: &w.partyX, status: "checked_in", driver: &w.dD1})
	keepsDriver := w.task(tmTask{tenant: w.D, party: &w.partyX, status: "assigned", driver: &w.dD1})
	helperC := w.task(tmTask{tenant: w.D, party: &w.partyX, helper: &w.dC1})
	truckC := w.task(tmTask{tenant: w.D, party: &w.partyX, truck: &w.truckC})
	quarantined := w.task(tmTask{tenant: q, source: "quarantine", party: &w.partyX})

	for name, c := range map[string]struct {
		p          *authz.Principal
		task       uuid.UUID
		in         operations.Reassign
		code, what string
	}{
		"staff without a dispatcher grant":      {w.staffA, outOfScope, moveTo(w.B), authz.CodePermissionDenied, operations.ReasonDispatcherOrPlatform},
		"platform without cross-tenant write":   {w.paR, open, moveTo(w.B), authz.CodePermissionDenied, operations.ReasonDispatcherOrPlatform},
		"an API key":                            {machine, open, moveTo(w.B), authz.CodePermissionDenied, operations.ReasonDispatcherOrPlatform},
		"a task outside the dispatcher's scope": {w.disp, outOfScope, moveTo(w.B), httpx.CodeNotFound, ""},
		"an unknown task":                       {w.pa, uuid.New(), moveTo(w.B), httpx.CodeNotFound, ""},
		"a quarantined task":                    {w.pa, quarantined, moveTo(w.B), httpx.CodeNotFound, ""},
		"the quarantine tenant":                 {w.disp, open, moveTo(q), httpx.CodeInvalidArgument, "tenantId not_found"},
		"an unknown tenant":                     {w.disp, open, moveTo(uuid.New()), httpx.CodeInvalidArgument, "tenantId not_found"},
		"a suspended tenant":                    {w.disp, open, moveTo(w.S), iam.CodeFailedPrecondition, iam.ReasonTargetTenantClosed},
		"a task with a trip":                    {w.disp, withTrip, withDriver(w.B, &w.dB1), iam.CodeFailedPrecondition, iam.ReasonTaskHasTrips},
		"a checked-in task":                     {w.disp, checkedIn, withDriver(w.B, &w.dB1), iam.CodeFailedPrecondition, iam.ReasonTaskNotOpen},
		"a driver of another carrier":           {w.disp, open, withDriver(w.B, &w.dC1), httpx.CodeInvalidArgument, "driverId not_in_tenant"},
		"an unknown driver":                     {w.disp, open, withDriver(w.B, idp(uuid.New())), httpx.CodeInvalidArgument, "driverId not_found"},
		"an inactive driver":                    {w.disp, open, withDriver(w.B, &w.dB2), iam.CodeFailedPrecondition, iam.ReasonDriverInactive},
		"the current driver stays behind":       {w.disp, keepsDriver, moveTo(w.B), iam.CodeFailedPrecondition, iam.ReasonDriverNotInTenant},
		"a helper of an unrelated carrier":      {w.disp, helperC, moveTo(w.B), iam.CodeFailedPrecondition, iam.ReasonHelperNotInTenant},
		"a truck of an unrelated carrier":       {w.disp, truckC, moveTo(w.B), iam.CodeFailedPrecondition, iam.ReasonTruckNotInTenant},
	} {
		_, err := w.reassign(c.p, c.task, c.in)
		if code, what := tmCode(err); code != c.code || what != c.what {
			t.Errorf("%s: %v (%s %q), want %s %q", name, err, code, what, c.code, c.what)
		}
	}
	if n := w.count(`SELECT count(*) FROM security_events WHERE event_type = $1`, iam.EventTaskTenantReassigned); n != 0 {
		t.Fatalf("%d task_tenant_reassigned rows after refusals only", n)
	}
	if n := w.count(`SELECT count(*) FROM tasks WHERE tenant_id <> $1 AND tenant_id <> $2 AND tenant_id <> $3`, w.D, w.A, q); n != 0 {
		t.Fatalf("%d tasks moved by refused requests", n)
	}

	t.Run("platform admin unassigns while moving; a helper of a linked tenant stays", func(t *testing.T) {
		ofA := w.task(tmTask{tenant: w.A, source: "driver", status: "assigned", driver: &w.dA1, runOrder: ptr(int32(3)),
			legacyDriverRef: ptr("legacyDriverDoc"), party: &w.partyY, helper: &w.dB1}) // B is linked to O
		out, err := w.reassign(w.pa, ofA, withDriver(w.O, nil))
		if err != nil {
			t.Fatal(err)
		}
		if out.TenantID != w.O || out.Status != operations.TaskPending || out.DriverID != nil || out.RunOrder != nil ||
			out.PreviousDriverID == nil || *out.PreviousDriverID != w.dA1 {
			t.Fatalf("reassigned %+v", out)
		}
		var ref *string
		if err := w.etl.QueryRow(ctx, `SELECT legacy_driver_ref FROM tasks WHERE id = $1`, ofA).Scan(&ref); err != nil || ref != nil {
			t.Fatalf("legacy driver ref kept: %v %v", ref, err)
		}
		var det []byte
		if err := w.etl.QueryRow(ctx, `SELECT details FROM security_events WHERE event_type = $1 AND details->>'taskId' = $2`,
			iam.EventTaskTenantReassigned, ofA.String()).Scan(&det); err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(det, &m)
		if m["by"] != "platform_admin" || m["previousDriverId"] != w.dA1.String() || m["driverId"] != nil {
			t.Fatalf("details %s", det)
		}
		var payload []byte
		if err := w.etl.QueryRow(ctx, `SELECT payload FROM outbox_events WHERE routing_key = 'task.reassigned' AND aggregate_id = $1`,
			ofA.String()).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var pl map[string]any
		_ = json.Unmarshal(payload, &pl)
		if v, ok := pl["driverId"]; !ok || v != nil || pl["previousDriverId"] != w.dA1.String() {
			t.Fatalf("payload %s", payload)
		}
	})

	t.Run("nothing to change writes nothing; a driver change without a move is not audited", func(t *testing.T) {
		inB := w.task(tmTask{tenant: w.B, party: &w.partyX})
		events := w.count(`SELECT count(*) FROM outbox_events`)
		out, err := w.reassign(w.pa, inB, moveTo(w.B))
		if err != nil || out.Moved || out.TenantID != w.B {
			t.Fatalf("no-op %+v %v", out, err)
		}
		if n := w.count(`SELECT count(*) FROM outbox_events`); n != events {
			t.Fatalf("a no-op wrote %d outbox rows", n-events)
		}
		audits := w.count(`SELECT count(*) FROM security_events`)
		out, err = w.reassign(w.pa, inB, withDriver(w.B, &w.dB1))
		if err != nil || out.Moved || out.Status != operations.TaskAssigned || out.DriverID == nil || *out.DriverID != w.dB1 {
			t.Fatalf("driver change %+v %v", out, err)
		}
		if n := w.count(`SELECT count(*) FROM security_events`); n != audits {
			t.Fatal("a driver change without a tenant move was audited as a reassignment")
		}
		if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'task.reassigned' AND aggregate_id = $1`, inB.String()); n != 1 {
			t.Fatalf("%d task.reassigned for the driver change", n)
		}
	})

	t.Run("a move without a driver change is task.updated", func(t *testing.T) {
		callOut := w.task(tmTask{tenant: w.D, party: &w.partyX})
		if _, err := w.reassign(w.disp, callOut, moveTo(w.C)); err != nil {
			t.Fatal(err)
		}
		var payload []byte
		if err := w.etl.QueryRow(ctx, `SELECT payload FROM outbox_events WHERE routing_key = 'task.updated' AND aggregate_id = $1`,
			callOut.String()).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var pl struct {
			Changed []string `json:"changed"`
		}
		_ = json.Unmarshal(payload, &pl)
		if !slices.Equal(pl.Changed, []string{"tenantId"}) {
			t.Fatalf("payload %s", payload)
		}
	})
}

func ptr[T any](v T) *T { return &v }

// PATCH /v1/tasks/{id} (T28 slice): the route, its guard, the body rules and the answer.
func TestReassignHTTP(t *testing.T) {
	w := newTMWorld(t)
	principals := map[string]*authz.Principal{"disp": w.disp, "staffA": w.staffA,
		"none": {UserID: uuid.New(), TenantID: &w.A, TenantRole: authz.Operator}}
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	auth := func(c fiber.Ctx) error {
		authz.SetPrincipal(c, principals[c.Get("X-Test-Principal")])
		return c.Next()
	}
	for _, g := range operations.Groups(w.svc, auth) {
		g.Mount(app.Group(g.Prefix))
	}
	call := func(who, path, body string) (int, map[string]any) {
		req := httptest.NewRequest("PATCH", path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Principal", who)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return res.StatusCode, m
	}
	callOut := w.task(tmTask{tenant: w.D, party: &w.partyX})
	path := "/v1/tasks/" + callOut.String()
	reason := func(m map[string]any) string {
		e, _ := m["error"].(map[string]any)
		d, _ := e["details"].(map[string]any)
		if f, ok := d["fields"].([]any); ok && len(f) > 0 {
			f0, _ := f[0].(map[string]any)
			return f0["field"].(string) + " " + f0["reason"].(string)
		}
		if r, ok := d["reason"].(string); ok {
			return r
		}
		return e["code"].(string)
	}
	for name, c := range map[string]struct {
		who, path, body string
		status          int
		reason          string
	}{
		"no capability":     {"none", path, `{"tenantId":"` + w.B.String() + `"}`, 403, "permission_denied"},
		"not a dispatcher":  {"staffA", path, `{"tenantId":"` + w.B.String() + `"}`, 403, operations.ReasonDispatcherOrPlatform},
		"another field":     {"disp", path, `{"tenantId":"` + w.B.String() + `","jobCategory":"PRIMARY"}`, 422, "jobCategory not_supported"},
		"no tenantId":       {"disp", path, `{"driverId":null}`, 422, "tenantId required"},
		"a bad tenantId":    {"disp", path, `{"tenantId":"B"}`, 422, "tenantId invalid"},
		"a bad driverId":    {"disp", path, `{"tenantId":"` + w.B.String() + `","driverId":7}`, 422, "driverId invalid"},
		"not a JSON object": {"disp", path, `[1]`, 400, "bad_request"},
		"a path id":         {"disp", "/v1/tasks/FM-11102026-001", `{"tenantId":"` + w.B.String() + `"}`, 404, "not_found"},
		"a query parameter": {"disp", path + "?force=1", `{"tenantId":"` + w.B.String() + `"}`, 400, "bad_request"},
	} {
		status, m := call(c.who, c.path, c.body)
		if status != c.status || reason(m) != c.reason {
			t.Errorf("%s: %d %s, want %d %s", name, status, reason(m), c.status, c.reason)
		}
	}
	status, m := call("disp", path, `{"tenantId":"`+w.B.String()+`","driverId":"`+w.dB1.String()+`"}`)
	data, _ := m["data"].(map[string]any)
	if status != 200 || data["tenantId"] != w.B.String() || data["driverId"] != w.dB1.String() || data["moved"] != true ||
		data["tenantSource"] != "form" || data["previousTenantId"] != w.D.String() || data["status"] != "assigned" {
		t.Fatalf("PATCH: %d %v", status, m)
	}
}
