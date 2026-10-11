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
// collection is PostgreSQL-owned once every domain it lists is in PG_OWNED_DOMAINS. transactions holds both the
// truck-renewal ledger (masterdata, P1) and the payout ledger (hr, P4), so it is mirrored until both are owned.
// mobile_installations is written by every installed 3.x APK until P7b whatever the domain flags say: it has no
// domain and is mirrored while the sync runs. users and permissions_config are loaded by their own steps (auth-import,
// auth-rbac) and never mirrored by etl.sync.
var collectionDomains = func() map[string][]string {
	m := map[string][]string{"mobile_installations": nil, "transactions": {"masterdata", "hr"}}
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

// groupCollections are read as collection groups (drivers/*/mobile_installations, chats/*/messages; main spec §13.1).
var groupCollections = map[string]bool{"mobile_installations": true, "messages": true}

// fieldMirrored are the PostgreSQL-owned class B collections whose 3.x-app fields stay mirrored (§12.1 class B).
var fieldMirrored = map[string]bool{"drivers": true, "hubs": true}

// foreignFields are fields of a collection that the flows of another domain write: the trucks fields of the PM alert
// and the maintenance pages (checkMaintenanceAlert after a fuel-log approval, functions/src/triggers.ts:449-516, and
// features/maintenance/api/maintenance.ts; billing, P3). While that domain is Firestore-owned they keep Firestore as
// their writer even on a PostgreSQL-owned collection: the field mirror applies them and the projection leaves them
// out, so a field always has one writer (a truck-form edit of currentMileage in Go reaches Firestore from P3 on).
var foreignFields = map[string]map[string][]string{
	"trucks": {"billing": {"activeMaintenanceId", "lastAlertMileage", "currentMileage"}},
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

// ProjectedCollections are the collections firestore.shadow writes in this release (T24): the master data installed
// APKs read (class B) and the customers, carrier profiles and companies Cloud Functions read until P5 (R71); metadata is
// the distances_last_calculated document of the distance matrix. holidays, broadcasts, settings/mobile_app and the
// billing fields of trips and standby records join with their phases.
var ProjectedCollections = []string{"subcontractors", "customers", "companies", "hubs", "hub_soc_distances", "soc_hub_distances",
	"metadata", "trucks", "drivers", "truckAssignment"}

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
	for _, s := range mappedCollections() {
		if _, ok := collectionDomains[s.name]; ok {
			out = append(out, s.name)
		}
	}
	return out
}

// PlanMirror derives the sweep from the PostgreSQL-owned domains (PG_OWNED_DOMAINS) and an optional pin
// (ETL_MIRROR_COLLECTIONS, which only narrows it): a collection whose domains are not all owned is mirrored whole;
// drivers and hubs of an owned masterdata keep their field-level mirror, and so do trucks while billing is not owned
// (ForeignFields); mobile_installations is always mirrored.
func PlanMirror(owned map[string]bool, pin []string) (MirrorPlan, error) {
	if err := checkPin(pin, MirrorCollections(), "ETL_MIRROR_COLLECTIONS"); err != nil {
		return MirrorPlan{}, err
	}
	p := MirrorPlan{Owned: owned}
	for _, s := range mappedCollections() {
		domains, known := collectionDomains[s.name]
		if !known || (len(pin) > 0 && !slices.Contains(pin, s.name)) {
			continue
		}
		switch {
		case !ownedAll(owned, domains):
			p.Entries = append(p.Entries, MirrorEntry{Name: s.name, Mode: MirrorFull})
		case fieldMirrored[s.name] || len(ForeignFields(s.name, owned)) > 0:
			p.Entries = append(p.Entries, MirrorEntry{Name: s.name, Mode: MirrorFields})
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
