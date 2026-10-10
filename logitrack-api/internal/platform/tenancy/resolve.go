// Package tenancy holds the tenant rules every write path shares (ADR 0029 §4, Appendix C §C.3.10):
// Resolve, the pure port of functions/src/core/tenantResolve.ts (branch
// origin/feat/multi-tenant-carrier-isolation; ADR 0026 text is not on disk, R32) extended with the
// per-collection chains of main spec §13.5 (R75), and Rehome, the audited move of a quarantined row
// and its children.
//
// tenant_id says which carrier organisation ran a row. It is resolved once, at write or load time, and
// frozen; it is never recomputed on read. Nothing here guesses: when a chain runs out Resolve reports
// no tenant and the caller loads the row into the quarantine tenant (R11), never a default.
package tenancy

import (
	"slices"
	"strings"
)

// Source is how a tenant was derived (tenant_source, the inline CHECK of every tenant-stamped table, R58).
// task, trip, self and truck follow a link frozen at write time; driver is an approximation (it reads
// the driver's current carrier, tenantResolve.ts:27-33); form is the caller's active tenant on API rows;
// quarantine marks a row whose chain ran out.
type Source string

// The seven tenant_source values.
const (
	SourceTask       Source = "task"
	SourceTrip       Source = "trip"
	SourceDriver     Source = "driver"
	SourceTruck      Source = "truck"
	SourceSelf       Source = "self"
	SourceForm       Source = "form"
	SourceQuarantine Source = "quarantine"
)

// Sources is the CHECK vocabulary of tenant_source, in the order of the DDL.
var Sources = []Source{SourceTask, SourceTrip, SourceDriver, SourceTruck, SourceSelf, SourceForm, SourceQuarantine}

// QuarantineTenantID is the structural quarantine tenant (0002_identity, app_quarantine_tenant_id(), R56).
const QuarantineTenantID = "00000000-0000-7000-8000-00000000000f"

// Legacy collection names Resolve knows (Firestore spelling: incidentReport, maintenance).
const (
	Tasks            = "tasks"
	TripRecords      = "trip_records"
	StandbyRecords   = "standby_records"
	IncidentReport   = "incidentReport"
	Drivers          = "drivers"
	Trucks           = "trucks"
	VehicleExpenses  = "vehicle_expenses"
	Maintenance      = "maintenance"
	VehicleLocations = "vehicle_locations"
	Transactions     = "transactions" // renewal shape {truckId, ...}; payout rows take the billing carrier (form)
	DriverPenalties  = "driver_penalties"
	Payroll          = "payroll"
	LeaveRequests    = "leave_requests"
	Chats            = "chats"
	MobileInstalls   = "mobile_installations"
)

// Collections are the collections Resolve has a chain for: the six of tenantResolve.ts and the chains
// main spec §13.5 adds (R75). Rate tables, statements and payout transactions take the billing carrier
// (form, R61), companies match a carrier by tax id then name, holidays and broadcasts are platform rows:
// none of them is a chain of links, so none is here.
var Collections = []string{
	Tasks, TripRecords, StandbyRecords, IncidentReport, Drivers, Trucks,
	VehicleExpenses, Maintenance, VehicleLocations, Transactions,
	DriverPenalties, Payroll, LeaveRequests, Chats, MobileInstalls,
}

// IsCollection reports whether Resolve has a chain for collection (isTenantCollection).
func IsCollection(collection string) bool { return slices.Contains(Collections, collection) }

// Resolution is a resolved tenant and how it was found.
type Resolution struct {
	TenantID string
	Source   Source
}

// Lookups feed Resolve; the caller fetches whatever it needs, so every ordering rule is unit-testable.
// A nil function knows nothing. Every function returns "" for an unknown reference, and also for a
// parent that is itself unresolved (a quarantined task is "not stamped" in the sense of
// tenantResolve.ts).
type Lookups struct {
	// TenantOfTask is the tenant already stamped on a task (doc id or business task number).
	TenantOfTask func(taskRef string) string
	// TenantOfTrip is the tenant already stamped on a trip record.
	TenantOfTrip func(tripRef string) string
	// TenantOfDriver is the tenant of a driver; the reference may be a driver doc id or an Auth uid,
	// legacy rows store either (tenantLookups.ts:62-78).
	TenantOfDriver func(driverRef string) string
	// TenantOfTruck is the tenant of a truck (truckId).
	TenantOfTruck func(truckRef string) string
	// TenantOfCarrier maps a legacy subcontractors doc id to its tenant. In tenantResolve.ts the
	// subcontractors doc id is the tenant id itself; nil keeps that identity.
	TenantOfCarrier func(subcontractorID string) string
	// OwnFleetTenantID is the single own-fleet tenant (OWN_FLEET_TENANT_ID, R56). Empty when
	// unconfigured, which makes own-fleet rows orphans instead of mislabelled.
	OwnFleetTenantID string
}

func (l Lookups) task(ref string) string   { return call(l.TenantOfTask, ref) }
func (l Lookups) trip(ref string) string   { return call(l.TenantOfTrip, ref) }
func (l Lookups) driver(ref string) string { return call(l.TenantOfDriver, ref) }
func (l Lookups) truck(ref string) string  { return call(l.TenantOfTruck, ref) }
func (l Lookups) carrier(subID string) string {
	if l.TenantOfCarrier == nil {
		return subID
	}
	return l.TenantOfCarrier(subID)
}

func call(f func(string) string, ref string) string {
	if f == nil || ref == "" {
		return ""
	}
	return f(ref)
}

// str is the trimmed string, or "" for anything that is not a usable string (tenantResolve.ts str):
// a numeric taskId is ignored, never coerced.
func str(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

type candidate struct {
	source Source
	tenant string
}

// firstOf is the first non-blank tenant of the chain, tagged with how it was found.
func firstOf(cs ...candidate) (Resolution, bool) {
	for _, c := range cs {
		if id := strings.TrimSpace(c.tenant); id != "" {
			return Resolution{TenantID: id, Source: c.source}, true
		}
	}
	return Resolution{}, false
}

// Resolve resolves the tenant of one legacy document (resolveTenant). The order is per collection and
// is not interchangeable: links frozen when the work happened (task, trip) win over the driver
// fallback, which is only current. Loads therefore run tasks -> trip_records -> standby_records ->
// incidentReport (tenantResolve.ts:88-93). ok is false when the chain runs out: the caller quarantines
// the row (R11). A collection without a chain panics: a new collection must declare its own order
// instead of inheriting one.
func Resolve(collection string, doc map[string]any, l Lookups) (Resolution, bool) {
	taskID, tripID := str(doc["taskId"]), str(doc["tripId"])
	driverID, truckID := str(doc["driverId"]), str(doc["truckId"])
	viaTask := func() candidate { return candidate{SourceTask, l.task(taskID)} }
	viaTrip := func() candidate { return candidate{SourceTrip, l.trip(tripID)} }
	viaDriver := func() candidate { return candidate{SourceDriver, l.driver(driverID)} }
	viaTruck := func() candidate { return candidate{SourceTruck, l.truck(truckID)} }

	switch collection {
	case Tasks:
		return firstOf(viaDriver())
	case TripRecords:
		return firstOf(viaTask(), viaDriver())
	case StandbyRecords:
		return firstOf(viaTask(), viaTrip(), viaDriver())
	case IncidentReport:
		// An incident links to a trip, not a task: taskId is ignored.
		return firstOf(viaTrip(), viaDriver())
	case Drivers:
		// The driver doc carries its own carrier; no subcontractor = an own-fleet employee. A
		// subcontractorId that resolves to nothing is an orphan, never the own fleet (§13.5).
		if sub := str(doc["subcontractorId"]); sub != "" {
			return firstOf(candidate{SourceSelf, l.carrier(sub)})
		}
		return firstOf(candidate{SourceSelf, l.OwnFleetTenantID})
	case Trucks:
		// ownershipType + subcontractorId keep their meaning; a partner truck without its carrier is an
		// orphan (mislabelling it as ours is worse than counting it).
		if str(doc["ownershipType"]) == "subcontractor" {
			return firstOf(candidate{SourceSelf, l.carrier(str(doc["subcontractorId"]))})
		}
		return firstOf(candidate{SourceSelf, l.OwnFleetTenantID})
	case VehicleExpenses:
		return firstOf(viaDriver(), viaTruck())
	case Maintenance, VehicleLocations, Transactions:
		return firstOf(viaTruck())
	case DriverPenalties, Payroll, LeaveRequests, Chats, MobileInstalls:
		return firstOf(viaDriver())
	default:
		panic("tenancy: Resolve has no chain for collection " + collection)
	}
}
