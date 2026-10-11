//go:build integration

package operations_test

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// Tenant stamping of the Go write paths (T28, issue #48; Appendix C §C.3.10, main spec §13.5): operations.Stamp runs
// the chains of tenantResolve.ts over the links the writer can read, under its RLS, and the four create functions
// accept the result.

// dispatcherOf adds the dispatcher organisation D (a carrier tenant without contractor link, TTP in Appendix D) with a
// staff member holding a dispatcher grant over customer X (R13: the membership in its own organisation gives it a tid).
func (w *world) dispatcherOf() (uuid.UUID, *authz.Principal) {
	d := w.id(`INSERT INTO tenants (kind, name_th, code, legal_type) VALUES ('carrier', 'ผู้จัดงาน D', 'CD', 'company') RETURNING id`)
	user := w.id(`INSERT INTO users (email, display_name) VALUES ('dispatch@example.test', 'Dispatcher D') RETURNING id`)
	return d, &authz.Principal{UserID: user, TenantID: &d, TenantRole: authz.OperationStaff, TenantKind: "carrier",
		Dispatcher: true, PartyIDs: []uuid.UUID{w.partyX}}
}

// stamp runs Stamp in a request transaction of p.
func (w *world) stamp(p *authz.Principal, table string, l operations.Links) (tenancy.Resolution, error) {
	var res tenancy.Resolution
	err := w.tx(p, func(tx pgx.Tx) error {
		var err error
		res, err = operations.Stamp(context.Background(), tx, p, table, l)
		return err
	})
	return res, err
}

// errCode is the API code of err and its details.reason / first field.
func errCode(err error) (code, detail string) {
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

// AC: a task created by a dispatcher carries the dispatcher organisation's tenant and tenant_source 'form'.
func TestStampDispatcherCallOut(t *testing.T) {
	w := newWorld(t)
	d, disp := w.dispatcherOf()
	res, err := w.stamp(disp, operations.TableTasks, operations.Links{})
	if err != nil || res.TenantID != d.String() || res.Source != tenancy.SourceForm {
		t.Fatalf("dispatcher call-out stamped %+v %v, want D form", res, err)
	}
	in := w.newTask(d, now)
	in.TenantSource, in.CreatedBy = string(res.Source), &disp.UserID
	in.BillingPartyID = &w.partyX
	task := w.createTask(disp, in)
	if task.TenantID != d || task.TenantSource != "form" {
		t.Fatalf("created %s %s", task.TenantID, task.TenantSource)
	}
	// A dispatcher writes no task of another carrier: its RLS refuses the insert outright.
	other := w.newTask(w.A, now)
	if err := w.tx(disp, func(tx pgx.Tx) error {
		_, _, err := operations.CreateTask(context.Background(), tx, other)
		return err
	}); err == nil {
		t.Fatal("a dispatcher created a task in carrier A")
	}
	// Nor does a driver of another carrier stamp its call-out: the driver is outside the dispatcher's reach (RLS hides
	// it, or its tenant is not one the dispatcher writes).
	if _, err := w.stamp(disp, operations.TableTasks, operations.Links{DriverID: &w.dA1}); err == nil {
		t.Fatal("a dispatcher stamped a task with a driver of carrier A")
	} else if c, _ := errCode(err); c != httpx.CodeInvalidArgument && c != authz.CodePermissionDenied {
		t.Fatalf("dispatcher + driver of A: %v", err)
	}
}

// The chains of every operations table, the refusals, and AC: every stamped row has a tenant_source of the CHECK.
func TestStampChains(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	taskO := w.createTask(w.staffO, w.newTask(w.O, now))
	assigned := w.newTask(w.A, now)
	assigned.DriverID, assigned.TenantSource = &w.dA1, "driver"
	taskA := w.createTask(w.staffA, assigned)
	tripO := w.createTrip(w.staffO, w.newTrip(taskO, "ZXJB26101100001"))
	ofDriver := w.newTask(w.O, now)
	ofDriver.DriverID, ofDriver.TenantSource = &w.dO1, "driver"
	taskD1 := w.createTask(w.staffO, ofDriver)
	quarantined := w.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw)
		VALUES ($1, 'quarantine', 'first_mile', 'pending', $2, 'ALANG-A', 'SOCE') RETURNING id`, tenancy.QuarantineTenantID, now)

	type want struct {
		tenant uuid.UUID
		source tenancy.Source
	}
	var sources []tenancy.Source
	for name, c := range map[string]struct {
		p     *authz.Principal
		table string
		l     operations.Links
		want  want
	}{
		"task from its driver (contractor reach)": {w.staffO, operations.TableTasks, operations.Links{DriverID: &w.dA1}, want{w.A, tenancy.SourceDriver}},
		"task without a driver: the writer's":     {w.staffA, operations.TableTasks, operations.Links{}, want{w.A, tenancy.SourceForm}},
		"trip from its task":                      {w.staffO, operations.TableTrips, operations.Links{TaskID: &taskO.ID, DriverID: &w.dA1}, want{w.O, tenancy.SourceTask}},
		"trip without a task: its driver":         {w.staffO, operations.TableTrips, operations.Links{DriverID: &w.dA1}, want{w.A, tenancy.SourceDriver}},
		"driver's trip on its own task":           {w.driverO1, operations.TableTrips, operations.Links{TaskID: &taskD1.ID, DriverID: &w.dO1}, want{w.O, tenancy.SourceTask}},
		"standby from its task":                   {w.staffO, operations.TableStandby, operations.Links{TaskID: &taskA.ID, TripID: &tripO.ID}, want{w.A, tenancy.SourceTask}},
		"standby from its trip":                   {w.staffO, operations.TableStandby, operations.Links{TripID: &tripO.ID, DriverID: &w.dA1}, want{w.O, tenancy.SourceTrip}},
		"standby from its driver":                 {w.staffO, operations.TableStandby, operations.Links{DriverID: &w.dO2}, want{w.O, tenancy.SourceDriver}},
		"incident from its trip, never the task":  {w.staffO, operations.TableIncidents, operations.Links{TaskID: &taskA.ID, TripID: &tripO.ID}, want{w.O, tenancy.SourceTrip}},
		"incident from its driver":                {w.staffO, operations.TableIncidents, operations.Links{TaskID: &taskA.ID, DriverID: &w.dA1}, want{w.A, tenancy.SourceDriver}},
	} {
		res, err := w.stamp(c.p, c.table, c.l)
		if err != nil || res.TenantID != c.want.tenant.String() || res.Source != c.want.source {
			t.Errorf("%s: %+v %v, want %s %s", name, res, err, c.want.tenant, c.want.source)
		}
		sources = append(sources, res.Source)
	}

	for name, c := range map[string]struct {
		p          *authz.Principal
		table      string
		l          operations.Links
		code, what string
	}{
		"a driver the writer cannot read":   {w.staffA, operations.TableTasks, operations.Links{DriverID: &w.dO1}, httpx.CodeInvalidArgument, "driverId not_found"},
		"a task of another carrier":         {w.staffC, operations.TableTrips, operations.Links{TaskID: &taskA.ID}, httpx.CodeInvalidArgument, "taskId not_found"},
		"a quarantined task":                {w.staffO, operations.TableTrips, operations.Links{TaskID: &quarantined}, httpx.CodeInvalidArgument, "taskId not_found"},
		"an unknown trip":                   {w.staffO, operations.TableIncidents, operations.Links{TripID: ptr(uuid.New())}, httpx.CodeInvalidArgument, "tripId not_found"},
		"a trip without links":              {w.staffO, operations.TableTrips, operations.Links{}, operations.CodeTenantOrphan, ""},
		"an incident with a task only":      {w.staffO, operations.TableIncidents, operations.Links{TaskID: &taskO.ID}, operations.CodeTenantOrphan, ""},
		"a driver on another driver's task": {w.driverO2, operations.TableTrips, operations.Links{TaskID: &taskO.ID}, httpx.CodeInvalidArgument, "taskId not_found"},
		"read-only bypass writes nowhere":   {w.all, operations.TableTasks, operations.Links{}, authz.CodeTenantRequired, ""},
		"a task outside the reach":          {w.staffA, operations.TableStandby, operations.Links{TaskID: &taskO.ID}, httpx.CodeInvalidArgument, "taskId not_found"},
	} {
		_, err := w.stamp(c.p, c.table, c.l)
		if code, what := errCode(err); code != c.code || what != c.what {
			t.Errorf("%s: %v (%s %q), want %s %q", name, err, code, what, c.code, c.what)
		}
	}
	// Contractor reach is one way: own-fleet staff stamp A's rows, A's staff never O's. A row whose links resolve to a
	// tenant the writer reads but does not write is refused with the deciding field.
	scope := &authz.Principal{UserID: uuid.New(), TenantID: &w.C, TenantRole: authz.OperationStaff, TenantKind: "carrier",
		Dispatcher: true, PartyIDs: []uuid.UUID{w.partyX}}
	if _, err := w.stamp(scope, operations.TableTrips, operations.Links{TaskID: &taskO.ID}); err == nil {
		t.Fatal("a dispatcher of C stamped a trip on O's task")
	} else if code, reason := errCode(err); code != authz.CodePermissionDenied || reason != operations.ReasonTenantLinkMismatch {
		t.Fatalf("dispatcher of C on O's task: %v (%s %s)", err, code, reason)
	}

	// Every source is one of the seven of the CHECK, and the Go vocabulary is the database's.
	for _, s := range sources {
		if !slices.Contains(tenancy.Sources, s) || !slices.Contains(operations.TenantSources, string(s)) {
			t.Errorf("source %q is outside the tenant_source CHECK", s)
		}
	}
	var def string
	if err := w.owner.QueryRow(ctx, `SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c
		WHERE c.conrelid = 'tasks'::regclass AND c.contype = 'c' AND pg_get_constraintdef(c.oid) LIKE '%tenant_source%'`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	var check []string
	for _, m := range regexp.MustCompile(`'([a-z_]+)'::text`).FindAllStringSubmatch(def, -1) {
		check = append(check, m[1])
	}
	var gov []string
	for _, s := range tenancy.Sources {
		gov = append(gov, string(s))
	}
	if !slices.Equal(check, gov) || !slices.Equal(check, operations.TenantSources) {
		t.Fatalf("tenant_source CHECK %v, tenancy.Sources %v, operations.TenantSources %v", check, gov, operations.TenantSources)
	}
}
