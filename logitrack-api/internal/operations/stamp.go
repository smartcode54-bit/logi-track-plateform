package operations

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// Tables Stamp resolves: the four operations tables.
const (
	TableTasks     = "tasks"
	TableTrips     = "trip_records"
	TableStandby   = "standby_records"
	TableIncidents = "incident_reports"
)

// stampCollections maps a table to the collection whose chain tenancy.Resolve runs (the legacy spelling of
// tenantResolve.ts: incidentReport).
var stampCollections = map[string]string{
	TableTasks: tenancy.Tasks, TableTrips: tenancy.TripRecords, TableStandby: tenancy.StandbyRecords,
	TableIncidents: tenancy.IncidentReport,
}

// CodeTenantOrphan is the 422 of an API row whose tenant chain runs out (Appendix B §B.1.5): the API never writes a row
// into the quarantine tenant, that is the ETL's and the mirror's outcome (R11).
const CodeTenantOrphan = "tenant_orphan"

// ReasonTenantLinkMismatch is details.reason of the 403 of a row whose links resolve to a tenant outside the writer's
// reach (Appendix C §C.3.10: a tenant_admin cannot create a trip on another tenant's task).
const ReasonTenantLinkMismatch = "tenant_link_mismatch"

// Links are the references of a new operations row that its tenant chain reads (main spec §13.5): the task it runs, the
// trip it belongs to, the driver who ran it. Nil = absent.
type Links struct {
	TaskID   *uuid.UUID
	TripID   *uuid.UUID
	DriverID *uuid.UUID
}

// Stamp resolves tenant_id and tenant_source of a row the API writes into table (TableTasks, TableTrips, TableStandby,
// TableIncidents) in p's request transaction tx (T28, Appendix C §C.3.10). It runs the chains of tenancy.Resolve, the
// port of tenantResolve.ts, over the tenants of the linked rows as the writer can read them:
//
//   - tasks: the driver's tenant ('driver'), else the writer's active tenant ('form', R13: a dispatcher's driverless
//     call-out lands in the dispatcher organisation's tenant); no active tenant is 403 tenant_required;
//   - trip_records: the task's ('task'), else the driver's ('driver');
//   - standby_records: the task's ('task'), else the trip's ('trip'), else the driver's ('driver');
//   - incident_reports: the trip's ('trip'), else the driver's ('driver'): an incident never follows a task.
//
// Every link given must name a row the writer can read (RLS decides; a row of the quarantine tenant counts as absent):
// otherwise 422 invalid_argument with the link's field (taskId, tripId, driverId) not_found, so an unreadable parent never
// stamps a tenant and the deferred tenant triggers never meet it at COMMIT. A chain that runs out is 422 tenant_orphan:
// the API never quarantines. The resolved tenant must be one the writer writes in (a staff principal's tenant reach:
// its tenant and contractor reach, R60; a driver's own tenant), else 403 permission_denied with details.reason
// tenant_link_mismatch and the field of the deciding link (a dispatcher assigning a driver of another carrier moves the
// call-out through the R13 reassignment instead). The source is always one of the seven tenant_source values of the
// CHECK (tenancy.Sources).
func Stamp(ctx context.Context, tx pgx.Tx, p *authz.Principal, table string, l Links) (tenancy.Resolution, error) {
	collection, ok := stampCollections[table]
	if !ok {
		return tenancy.Resolution{}, fmt.Errorf("operations: Stamp has no chain for table %s", table)
	}
	if p == nil {
		return tenancy.Resolution{}, httpx.ErrUnauthenticated()
	}
	tenants := map[string]string{} // link field -> tenant id
	doc := map[string]any{}
	for _, link := range []struct {
		field, table string
		id           *uuid.UUID
	}{{"taskId", "tasks", l.TaskID}, {"tripId", "trip_records", l.TripID}, {"driverId", "drivers", l.DriverID}} {
		if link.id == nil {
			continue
		}
		tenant, found, err := tenancy.TenantOf(ctx, tx, link.table, *link.id)
		if err != nil {
			return tenancy.Resolution{}, MapError(err)
		}
		if !found {
			return tenancy.Resolution{}, httpx.ErrInvalidArgument(violation(link.field, "not_found"))
		}
		tenants[link.field] = tenant.String()
		doc[link.field] = link.id.String()
	}
	lookup := func(field string) func(string) string { return func(string) string { return tenants[field] } }
	res, resolved := tenancy.Resolve(collection, doc, tenancy.Lookups{
		TenantOfTask: lookup("taskId"), TenantOfTrip: lookup("tripId"), TenantOfDriver: lookup("driverId"),
	})
	if !resolved {
		if table != TableTasks {
			return tenancy.Resolution{}, httpx.NewError(http.StatusUnprocessableEntity, CodeTenantOrphan,
				"the row links to no task, trip or driver its tenant could follow").
				WithDetails(map[string]any{"table": table})
		}
		form := p.EffectiveTenant()
		if form == nil {
			return tenancy.Resolution{}, authz.ErrTenantRequired()
		}
		res = tenancy.Resolution{TenantID: form.String(), Source: tenancy.SourceForm}
	}
	tenant, err := uuid.Parse(res.TenantID)
	if err != nil {
		return tenancy.Resolution{}, fmt.Errorf("operations: resolved tenant %q: %w", res.TenantID, err)
	}
	if !writable(p, tenant) {
		field := map[tenancy.Source]string{tenancy.SourceTask: "taskId", tenancy.SourceTrip: "tripId",
			tenancy.SourceDriver: "driverId"}[res.Source]
		return tenancy.Resolution{}, authz.ErrPermissionDenied("the row's links resolve to a tenant outside the writer's reach").
			WithDetails(map[string]any{"reason": ReasonTenantLinkMismatch, "field": field})
	}
	return res, nil
}

// writable reports whether p writes rows of tenant: a staff principal in its tenant reach (the tenants p_tenant_staff
// admits), a driver in its own tenant.
func writable(p *authz.Principal, tenant uuid.UUID) bool {
	if p.EffectiveRole() == authz.Driver {
		t := p.EffectiveTenant()
		return t != nil && *t == tenant
	}
	all, reach := p.TenantReach()
	return all || slices.Contains(reach, tenant)
}
