package tenancy_test

// Port of functions/src/core/tenantResolve.test.ts (branch origin/feat/multi-tenant-carrier-isolation, R32):
// every case keeps its name and expectation. Two cases of the TypeScript file change on purpose and say why:
// isTenantCollection now accepts the chains main spec §13.5 adds (R75), and a driver or partner truck whose
// subcontractor maps to nothing is an orphan (the TypeScript has no carrier lookup). The R75 chains have
// their own cases at the end.

import (
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

const (
	tenantWanpen = "sub_wanpen"
	tenantA      = "sub_alpha"
	tenantB      = "sub_bravo"
)

type maps struct {
	tasks, trips, drivers, trucks map[string]string
	ownFleet                      string
}

// lookups are backed by plain maps, so each test states exactly what the database "knows".
func lookups(m maps) tenancy.Lookups {
	get := func(src map[string]string) func(string) string {
		return func(k string) string { return src[k] }
	}
	return tenancy.Lookups{
		TenantOfTask: get(m.tasks), TenantOfTrip: get(m.trips), TenantOfDriver: get(m.drivers),
		TenantOfTruck: get(m.trucks), OwnFleetTenantID: m.ownFleet,
	}
}

func resolved(t *testing.T, got tenancy.Resolution, ok bool, tenant string, source tenancy.Source) {
	t.Helper()
	if !ok || got.TenantID != tenant || got.Source != source {
		t.Fatalf("got %+v ok=%v, want {%s %s}", got, ok, tenant, source)
	}
}

func unresolved(t *testing.T, got tenancy.Resolution, ok bool) {
	t.Helper()
	if ok {
		t.Fatalf("got %+v, want no tenant (orphan)", got)
	}
}

func doc(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func TestTasks(t *testing.T) {
	t.Run("resolves from the assigned driver", func(t *testing.T) {
		r, ok := tenancy.Resolve("tasks", doc("driverId", "drv1"), lookups(maps{drivers: map[string]string{"drv1": tenantA}}))
		resolved(t, r, ok, tenantA, tenancy.SourceDriver)
	})
	t.Run("returns undefined for an unassigned task rather than guessing", func(t *testing.T) {
		r, ok := tenancy.Resolve("tasks", doc(), lookups(maps{}))
		unresolved(t, r, ok)
	})
}

func TestDriverIDMayBeDocIDOrAuthUID(t *testing.T) {
	t.Run("resolves when driverId is the driver doc id", func(t *testing.T) {
		r, ok := tenancy.Resolve("tasks", doc("driverId", "driverDoc123"), lookups(maps{drivers: map[string]string{"driverDoc123": tenantA}}))
		resolved(t, r, ok, tenantA, tenancy.SourceDriver)
	})
	t.Run("resolves when driverId is the Auth UID", func(t *testing.T) {
		r, ok := tenancy.Resolve("tasks", doc("driverId", "authUid456"), lookups(maps{drivers: map[string]string{"authUid456": tenantB}}))
		resolved(t, r, ok, tenantB, tenancy.SourceDriver)
	})
}

func TestTripRecords(t *testing.T) {
	t.Run("prefers the task link over the driver fallback", func(t *testing.T) {
		// The task link was frozen when the work happened; the driver's subcontractor is only current.
		r, ok := tenancy.Resolve("trip_records", doc("taskId", "t1", "driverId", "drv1"),
			lookups(maps{tasks: map[string]string{"t1": tenantA}, drivers: map[string]string{"drv1": tenantB}}))
		resolved(t, r, ok, tenantA, tenancy.SourceTask)
	})
	t.Run("falls back to the driver when the task is not yet stamped", func(t *testing.T) {
		r, ok := tenancy.Resolve("trip_records", doc("taskId", "t1", "driverId", "drv1"),
			lookups(maps{tasks: map[string]string{}, drivers: map[string]string{"drv1": tenantB}}))
		resolved(t, r, ok, tenantB, tenancy.SourceDriver)
	})
	t.Run("falls back to the driver when the task id points at nothing", func(t *testing.T) {
		r, ok := tenancy.Resolve("trip_records", doc("taskId", "missing", "driverId", "drv1"),
			lookups(maps{drivers: map[string]string{"drv1": tenantA}}))
		resolved(t, r, ok, tenantA, tenancy.SourceDriver)
	})
	t.Run("returns undefined when neither link resolves", func(t *testing.T) {
		r, ok := tenancy.Resolve("trip_records", doc("taskId", "t1", "driverId", "drv1"), lookups(maps{}))
		unresolved(t, r, ok)
	})
}

func TestStandbyRecords(t *testing.T) {
	t.Run("prefers task over trip over driver", func(t *testing.T) {
		r, ok := tenancy.Resolve("standby_records", doc("taskId", "t1", "tripId", "r1", "driverId", "drv1"),
			lookups(maps{tasks: map[string]string{"t1": tenantA}, trips: map[string]string{"r1": tenantB},
				drivers: map[string]string{"drv1": tenantWanpen}}))
		resolved(t, r, ok, tenantA, tenancy.SourceTask)
	})
	t.Run("uses the trip when the task is unstamped", func(t *testing.T) {
		r, ok := tenancy.Resolve("standby_records", doc("taskId", "t1", "tripId", "r1", "driverId", "drv1"),
			lookups(maps{trips: map[string]string{"r1": tenantB}, drivers: map[string]string{"drv1": tenantWanpen}}))
		resolved(t, r, ok, tenantB, tenancy.SourceTrip)
	})
	t.Run("uses the driver when neither link is stamped", func(t *testing.T) {
		r, ok := tenancy.Resolve("standby_records", doc("taskId", "t1", "tripId", "r1", "driverId", "drv1"),
			lookups(maps{drivers: map[string]string{"drv1": tenantWanpen}}))
		resolved(t, r, ok, tenantWanpen, tenancy.SourceDriver)
	})
	t.Run("resolves a standby with no task/trip link at all via the driver", func(t *testing.T) {
		r, ok := tenancy.Resolve("standby_records", doc("driverId", "drv1"), lookups(maps{drivers: map[string]string{"drv1": tenantA}}))
		resolved(t, r, ok, tenantA, tenancy.SourceDriver)
	})
}

func TestIncidentReport(t *testing.T) {
	t.Run("prefers the trip link over the driver", func(t *testing.T) {
		r, ok := tenancy.Resolve("incidentReport", doc("tripId", "r1", "driverId", "drv1"),
			lookups(maps{trips: map[string]string{"r1": tenantA}, drivers: map[string]string{"drv1": tenantB}}))
		resolved(t, r, ok, tenantA, tenancy.SourceTrip)
	})
	t.Run("ignores taskId — an incident links to a trip, not a task", func(t *testing.T) {
		r, ok := tenancy.Resolve("incidentReport", doc("taskId", "t1", "driverId", "drv1"),
			lookups(maps{tasks: map[string]string{"t1": tenantA}, drivers: map[string]string{"drv1": tenantB}}))
		resolved(t, r, ok, tenantB, tenancy.SourceDriver)
	})
}

func TestDrivers(t *testing.T) {
	t.Run("uses its own subcontractorId", func(t *testing.T) {
		r, ok := tenancy.Resolve("drivers", doc("subcontractorId", tenantA), lookups(maps{ownFleet: tenantWanpen}))
		resolved(t, r, ok, tenantA, tenancy.SourceSelf)
	})
	t.Run("falls back to the own-fleet tenant when the driver has no subcontractor", func(t *testing.T) {
		r, ok := tenancy.Resolve("drivers", doc(), lookups(maps{ownFleet: tenantWanpen}))
		resolved(t, r, ok, tenantWanpen, tenancy.SourceSelf)
	})
	t.Run("treats an empty-string subcontractorId as absent", func(t *testing.T) {
		r, ok := tenancy.Resolve("drivers", doc("subcontractorId", "  "), lookups(maps{ownFleet: tenantWanpen}))
		resolved(t, r, ok, tenantWanpen, tenancy.SourceSelf)
	})
	// Go addition (§13.5): with a carrier lookup, a subcontractorId pointing at a missing subcontractors
	// doc is an orphan; it never falls back to the own fleet.
	t.Run("a subcontractorId that maps to no carrier is an orphan, not the own fleet", func(t *testing.T) {
		l := lookups(maps{ownFleet: tenantWanpen})
		l.TenantOfCarrier = func(string) string { return "" }
		r, ok := tenancy.Resolve("drivers", doc("subcontractorId", "gone"), l)
		unresolved(t, r, ok)
	})
}

func TestTrucks(t *testing.T) {
	t.Run("uses subcontractorId for a partner truck", func(t *testing.T) {
		r, ok := tenancy.Resolve("trucks", doc("ownershipType", "subcontractor", "subcontractorId", tenantA), lookups(maps{ownFleet: tenantWanpen}))
		resolved(t, r, ok, tenantA, tenancy.SourceSelf)
	})
	t.Run("uses the own-fleet tenant for ownershipType 'own'", func(t *testing.T) {
		r, ok := tenancy.Resolve("trucks", doc("ownershipType", "own"), lookups(maps{ownFleet: tenantWanpen}))
		resolved(t, r, ok, tenantWanpen, tenancy.SourceSelf)
	})
	t.Run("treats a missing ownershipType as own fleet", func(t *testing.T) {
		r, ok := tenancy.Resolve("trucks", doc(), lookups(maps{ownFleet: tenantWanpen}))
		resolved(t, r, ok, tenantWanpen, tenancy.SourceSelf)
	})
	t.Run("does NOT fall back to own fleet for a partner truck missing its subcontractorId", func(t *testing.T) {
		// Mislabelling a partner truck as ours is worse than leaving it an orphan we can count.
		r, ok := tenancy.Resolve("trucks", doc("ownershipType", "subcontractor"), lookups(maps{ownFleet: tenantWanpen}))
		unresolved(t, r, ok)
	})
}

func TestNeverGuesses(t *testing.T) {
	t.Run("returns undefined for drivers/trucks when ownFleetTenantId is unconfigured", func(t *testing.T) {
		r, ok := tenancy.Resolve("drivers", doc(), lookups(maps{}))
		unresolved(t, r, ok)
		r, ok = tenancy.Resolve("trucks", doc("ownershipType", "own"), lookups(maps{}))
		unresolved(t, r, ok)
	})
	t.Run("ignores non-string link fields instead of coercing them", func(t *testing.T) {
		r, ok := tenancy.Resolve("trip_records", doc("taskId", 123, "driverId", "drv1"), lookups(maps{drivers: map[string]string{"drv1": tenantA}}))
		resolved(t, r, ok, tenantA, tenancy.SourceDriver)
	})
	t.Run("ignores a lookup that returns a blank string", func(t *testing.T) {
		r, ok := tenancy.Resolve("trip_records", doc("taskId", "t1", "driverId", "drv1"),
			lookups(maps{tasks: map[string]string{"t1": "   "}, drivers: map[string]string{"drv1": tenantA}}))
		resolved(t, r, ok, tenantA, tenancy.SourceDriver)
	})
}

func TestIsTenantCollection(t *testing.T) {
	t.Run("accepts the six in-scope collections", func(t *testing.T) {
		for _, c := range []string{"tasks", "trip_records", "standby_records", "incidentReport", "drivers", "trucks"} {
			if !tenancy.IsCollection(c) {
				t.Errorf("%s: want true", c)
			}
		}
	})
	// Changed on purpose: the TypeScript rejected vehicle_expenses as "phase 2"; main spec §13.5 (R75) gives
	// it the chain driver -> truck, so it is in scope now. The misspelling stays rejected.
	t.Run("rejects collections without a chain", func(t *testing.T) {
		for _, c := range []string{"incident_reports", "customers", "rate_entries", ""} {
			if tenancy.IsCollection(c) {
				t.Errorf("%q: want false", c)
			}
		}
		if !tenancy.IsCollection("vehicle_expenses") {
			t.Error("vehicle_expenses: R75 gives it a chain")
		}
	})
	t.Run("Resolve panics on a collection without a chain", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("want a panic")
			}
		}()
		tenancy.Resolve("customers", doc(), lookups(maps{}))
	})
}

// The chains main spec §13.5 adds (R75).
func TestR75Chains(t *testing.T) {
	l := lookups(maps{drivers: map[string]string{"drv1": tenantA}, trucks: map[string]string{"trk1": tenantB}})
	t.Run("vehicle_expenses: driver, then truck", func(t *testing.T) {
		r, ok := tenancy.Resolve("vehicle_expenses", doc("driverId", "drv1", "truckId", "trk1"), l)
		resolved(t, r, ok, tenantA, tenancy.SourceDriver)
		r, ok = tenancy.Resolve("vehicle_expenses", doc("driverId", "gone", "truckId", "trk1"), l)
		resolved(t, r, ok, tenantB, tenancy.SourceTruck)
		r, ok = tenancy.Resolve("vehicle_expenses", doc("driverId", "gone"), l)
		unresolved(t, r, ok)
	})
	t.Run("maintenance, vehicle locations and renewal transactions: truck only", func(t *testing.T) {
		for _, c := range []string{"maintenance", "vehicle_locations", "transactions"} {
			r, ok := tenancy.Resolve(c, doc("truckId", "trk1", "driverId", "drv1"), l)
			resolved(t, r, ok, tenantB, tenancy.SourceTruck)
			r, ok = tenancy.Resolve(c, doc("driverId", "drv1"), l)
			unresolved(t, r, ok)
		}
	})
	t.Run("penalties, payroll, leave, chats, installations: driver only", func(t *testing.T) {
		for _, c := range []string{"driver_penalties", "payroll", "leave_requests", "chats", "mobile_installations"} {
			r, ok := tenancy.Resolve(c, doc("driverId", "drv1", "truckId", "trk1"), l)
			resolved(t, r, ok, tenantA, tenancy.SourceDriver)
			r, ok = tenancy.Resolve(c, doc("truckId", "trk1"), l)
			unresolved(t, r, ok)
		}
	})
	t.Run("the sources are the tenant_source CHECK vocabulary", func(t *testing.T) {
		want := []tenancy.Source{"task", "trip", "driver", "truck", "self", "form", "quarantine"}
		if len(tenancy.Sources) != len(want) {
			t.Fatalf("Sources = %v", tenancy.Sources)
		}
		for i := range want {
			if tenancy.Sources[i] != want[i] {
				t.Fatalf("Sources = %v, want %v", tenancy.Sources, want)
			}
		}
	})
}
