package etl

import (
	"fmt"
	"slices"
	"strings"
)

// Coexistence ownership (developer-spec.md §12.1, §13.7, §13.8; R25, R71). Between P1 and P7b each Firestore collection
// has exactly one writer per field: a collection whose domains are all in PG_OWNED_DOMAINS is written by PostgreSQL
// and, when installed APKs or Cloud Functions read it, projected PG -> Firestore (firestore.shadow); every other
// collection stays written by Firestore and is mirrored FS -> PG (etl.sync) through the load transforms. The class B
// collections PostgreSQL owns keep a field-level mirror of what the 3.x app writes on them (drivers: activeTruck,
// fcmToken, currentTruckId; hubs: the hubs drivers create), and the projection never names those fields, so a
// projected write never re-enters PostgreSQL and a mirrored field is never overwritten (no echo loop).

// The mirror modes of a collection.
const (
	// MirrorFull applies whole documents through the load mapper (the collection is Firestore-owned).
	MirrorFull = "full"
	// MirrorFields applies only the fields the 3.x app owns on a PostgreSQL-owned class B collection.
	MirrorFields = "fields"
)

// collectionDomains are the coexistence domains of each collection `load` maps (§12.1 classes, §12.2 phase table): a
// class A or B collection is PostgreSQL-owned once every domain it lists is in PG_OWNED_DOMAINS. transactions holds
// both the truck-renewal ledger (masterdata, P1) and the payout ledger (hr, P4), so it is mirrored until both are
// owned. The class C collections (classC) are written in Firestore by the installed 3.x APKs until P7b whatever their
// domain flag says, so their domain never stops their mirror. mobile_installations has no domain: it is class C too.
// users are loaded by their own step (auth-import, P0) and PostgreSQL-owned from then on (auth): only the fields the
// 3.x app keeps writing on them (lastLogin*, fcmTokens; class C) are mirrored, field by field. permissions_config is
// auth-rbac's and never mirrored.
var collectionDomains = func() map[string][]string {
	m := map[string][]string{"mobile_installations": nil, "transactions": {"masterdata", "hr"}, "users": {"auth"},
		// The billing fields of trips and standby records (T38): projected once billing is PostgreSQL-owned (P3), while
		// the records themselves stay mirrored (class C).
		ProjectionTripBilling: {"billing"}, ProjectionStandbyBilling: {"billing"}}
	for domain, names := range map[string][]string{
		"masterdata": {"subcontractors", "customers", "companies", "hubs", "hub_soc_distances", "soc_hub_distances", "metadata",
			"trucks", "drivers", "truckAssignment"},
		"operations": {"tasks", "trip_records", "standby_records", "incidentReport"},
		"billing": {"customer_rate_entries", "customer_fuel_rate_adjustments", "customer_service_fees", "standby_rate_entries",
			"fuel_daily_snapshots", "fuel_monthly_snapshots", "billing_statements", "billing_counters", "vehicle_expenses", "maintenance"},
		"hr":             {"driver_compensation_config", "driver_penalties", "payroll", "leave_requests", "holidays"},
		"comms":          {"chats", "messages", "broadcasts"},
		"security":       {"security_events"},
		"mobile_release": {"settings"},
		"dashboard":      {"vehicle_locations", "waitlist", "partner-interest"},
	} {
		for _, n := range names {
			m[n] = []string{domain}
		}
	}
	return m
}()

// classC are the collections the installed 3.x APKs write in Firestore until P7b (§12.1 class C: tasks, trips, standby
// records, incidents, expenses, maintenance, chats and their messages, leave requests, and the installations): the
// mirror carries them whole from P1 until the sync is switched off at P7b, whatever PG_OWNED_DOMAINS says. A domain
// flag moves only the class A and B collections of that domain; the Go writes of a class C collection reach Firestore
// as synchronous write-backs (§12.2 P2-P7a), and the mirror brings them back like any other write.
var classC = map[string]bool{"tasks": true, "trip_records": true, "standby_records": true, "incidentReport": true,
	"vehicle_expenses": true, "maintenance": true, "chats": true, "messages": true, "leave_requests": true, "mobile_installations": true}

// groupCollections are read as collection groups (drivers/*/mobile_installations, chats/*/messages; main spec §13.1).
var groupCollections = map[string]bool{"mobile_installations": true, "messages": true}

// fieldMirrored are the PostgreSQL-owned collections whose 3.x-app fields stay mirrored (§12.1 class B, and the users
// fields of class C).
var fieldMirrored = map[string]bool{"drivers": true, "hubs": true, "users": true}

// mirrorOrder is the load order of Appendix A §A.3.0 with users after drivers (a users token row names the login's
// driver): the order of a sweep.
func mirrorOrder() []string {
	var out []string
	for _, s := range mappedCollections() {
		out = append(out, s.name)
		if s.name == "drivers" {
			out = append(out, "users")
		}
	}
	return out
}

// foreignFields are fields of a collection that the flows of another domain write: the trucks fields of the PM alert
// and the maintenance pages (checkMaintenanceAlert after a fuel-log approval, functions/src/triggers.ts:449-516, and
// features/maintenance/api/maintenance.ts:118-142, 220-238: the status, the mileage, the service date and the active
// maintenance; billing, P3). While that domain is Firestore-owned they keep Firestore as their writer even on a
// PostgreSQL-owned collection: the field mirror applies Firestore's changes of them and the projection leaves them
// out, so a projected write never overwrites the maintenance flow. A truck-form edit of one of them in Go stays in
// PostgreSQL until the maintenance flow writes the field again, and reaches Firestore from P3 on.
var foreignFields = map[string]map[string][]string{
	"trucks": {"billing": {"activeMaintenanceId", "lastAlertMileage", "currentMileage", "lastServiceDate", "nextServiceMileage", "truckStatus"}},
}

// ForeignFields are the fields of coll another domain still writes in Firestore under owned.
func ForeignFields(coll string, owned map[string]bool) []string {
	var out []string
	for domain, fields := range foreignFields[coll] {
		if !owned[domain] {
			out = append(out, fields...)
		}
	}
	slices.Sort(out)
	return out
}

// MobileOwnedFields are the fields of a projected collection the 3.x app writes: the projection never writes them (they
// are outside every mask) and the field-level mirror applies them. updatedAt of a driver is written by both the app's
// merges and the legacy web, so the projection leaves it to the app as well. A hubs document a driver created
// (createdByDriver) belongs to the mirror as a whole: the projection skips those rows (§13.8).
var MobileOwnedFields = map[string][]string{
	"drivers": {"fcmToken", "activeTruck", "currentTruckId", "truckId", "updatedAt"},
}

// ProjectedCollections are the collections firestore.shadow writes in this release (T24, T52): the master data
// installed APKs read (class B) and the customers, carrier profiles and companies Cloud Functions read until P5 (R71);
// metadata is the distances_last_calculated document of the distance matrix, settings the version gate
// settings/mobile_app installed APKs read (class B, domain mobile_release: projected once that domain is
// PostgreSQL-owned, P6); trip_billing and standby_billing are the billing fields of the Firestore-written trips and
// standby records (T38, domain billing: P3; fieldProjections). holidays and broadcasts join with their phases.
var ProjectedCollections = []string{"subcontractors", "customers", "companies", "hubs", "hub_soc_distances", "soc_hub_distances",
	"metadata", "trucks", "drivers", "truckAssignment", "settings", ProjectionTripBilling, ProjectionStandbyBilling}

// MirrorEntry is one collection of a sweep.
type MirrorEntry struct {
	Name string
	Mode string // MirrorFull or MirrorFields
}

// MirrorPlan is what one etl.sync sweep reads, in the load order of Appendix A §A.3.0.
type MirrorPlan struct {
	Entries []MirrorEntry
	// Owned are the PostgreSQL-owned domains the plan was derived from (the field mappers read ForeignFields of it).
	Owned map[string]bool
}

// Names lists the collections of the plan.
func (p MirrorPlan) Names() []string {
	out := make([]string, len(p.Entries))
	for i, e := range p.Entries {
		out[i] = e.Name
	}
	return out
}

// Mode returns the mode of a collection, "" when the plan does not mirror it.
func (p MirrorPlan) Mode(name string) string {
	for _, e := range p.Entries {
		if e.Name == name {
			return e.Mode
		}
	}
	return ""
}

func ownedAll(owned map[string]bool, domains []string) bool {
	if len(domains) == 0 {
		return false
	}
	for _, d := range domains {
		if !owned[d] {
			return false
		}
	}
	return true
}

// checkPin validates a pin (ETL_MIRROR_COLLECTIONS, COMPAT_PROJECTION_COLLECTIONS): every entry must be one of known.
func checkPin(pin, known []string, what string) error {
	for _, p := range pin {
		if !slices.Contains(known, p) {
			return fmt.Errorf("%s: %q is not one of %s", what, p, strings.Join(known, ", "))
		}
	}
	return nil
}

// MirrorCollections are the collection names a mirror pin may name.
func MirrorCollections() []string {
	var out []string
	for _, n := range mirrorOrder() {
		if _, ok := collectionDomains[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// PlanMirror derives the sweep from the PostgreSQL-owned domains (PG_OWNED_DOMAINS) and an optional pin
// (ETL_MIRROR_COLLECTIONS, which only narrows it): a class C collection is always mirrored whole (the 3.x APKs write it
// until P7b, when the sync is switched off), and so is any other collection whose domains are not all owned; drivers
// and hubs of an owned masterdata keep their field-level mirror, and so do trucks while billing is not owned
// (ForeignFields), and users (lastLogin*, fcmTokens) once auth is owned (P0).
func PlanMirror(owned map[string]bool, pin []string) (MirrorPlan, error) {
	if err := checkPin(pin, MirrorCollections(), "ETL_MIRROR_COLLECTIONS"); err != nil {
		return MirrorPlan{}, err
	}
	p := MirrorPlan{Owned: owned}
	for _, name := range mirrorOrder() {
		domains, known := collectionDomains[name]
		if !known || (len(pin) > 0 && !slices.Contains(pin, name)) {
			continue
		}
		switch {
		case separateCollections[name]:
			// Loaded by its own step (auth-import at P0), never whole: only its 3.x fields, once auth is PostgreSQL's.
			if ownedAll(owned, domains) {
				p.Entries = append(p.Entries, MirrorEntry{Name: name, Mode: MirrorFields})
			}
		case classC[name] || !ownedAll(owned, domains):
			p.Entries = append(p.Entries, MirrorEntry{Name: name, Mode: MirrorFull})
		case fieldMirrored[name] || len(ForeignFields(name, owned)) > 0:
			p.Entries = append(p.Entries, MirrorEntry{Name: name, Mode: MirrorFields})
		}
	}
	return p, nil
}

// PlanProjection lists the collections firestore.shadow writes: the ProjectedCollections whose domains are all
// PostgreSQL-owned (a Firestore-owned collection is never projected: that would be a second writer), narrowed by an
// optional pin (COMPAT_PROJECTION_COLLECTIONS).
func PlanProjection(owned map[string]bool, pin []string) ([]string, error) {
	if err := checkPin(pin, ProjectedCollections, "COMPAT_PROJECTION_COLLECTIONS"); err != nil {
		return nil, err
	}
	var out []string
	for _, c := range ProjectedCollections {
		if ownedAll(owned, collectionDomains[c]) && (len(pin) == 0 || slices.Contains(pin, c)) {
			out = append(out, c)
		}
	}
	return out, nil
}
