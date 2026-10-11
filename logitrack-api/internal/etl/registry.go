package etl

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// mapper loads one document; it sets the outcome, the target row and the findings of its docCtx.
type mapper func(c *docCtx) error

// collectionSpec is one collection of the load order. prepare (optional) runs before the collection's documents
// with every document of the run, so a mapper can decide across documents (the hub that wins a duplicated
// source_id); full is true when docs is the whole collection as the source holds it now (a load: a dump always
// reads whole collections, and --since / the watermark filter only after prepare), false for one stored document
// (quarantine resolve --action=retry). reapply (optional) names documents prepare decided must be applied even when
// their _updateTime is unchanged or before the --since mark (a hub duplicate that takes the row over once the
// winner was deleted at source). after (optional) runs once the collection's documents are applied, with the
// documents this run applied, for the links that point forward in the load order (drivers.current_assignment_id
// after the truck assignments, the task number counters after the tasks).
type collectionSpec struct {
	name    string
	mapper  mapper
	prepare func(c *docCtx, docs []dump.Doc, full bool) error
	reapply func(r *runState, path string) bool
	after   func(r *runState, coll string) error
	// rowNow (field-level mirrors) says whether the row a rejected document lacked exists now: such a document is
	// applied again although Firestore did not change it, so a rejection that was only early heals.
	rowNow func(ctx context.Context, tx pgx.Tx, doc dump.Doc) (bool, error)
}

// mappedCollections are the collections this release loads, in the load order of Appendix A §A.3.0: tenants,
// customers, companies and the rate tables before everything that links to them (snapshot FKs point at the rate
// rows); hubs and their distances before tasks; trucks before drivers before the assignments and installations;
// tasks -> trips -> standby -> incidents so links are stamped before fallbacks (tenantResolve.ts:88-93); payroll
// before the payout transactions, chats before their messages; the platform rows last.
func mappedCollections() []collectionSpec {
	return []collectionSpec{
		{name: "subcontractors", mapper: loadSubcontractor},
		{name: "customers", mapper: loadCustomer},
		{name: "companies", mapper: loadCompany},
		{name: "customer_rate_entries", mapper: loadRateEntry},
		{name: "customer_fuel_rate_adjustments", mapper: loadFuelAdjustment},
		{name: "customer_service_fees", mapper: loadServiceFee},
		{name: "standby_rate_entries", mapper: loadStandbyRate},
		{name: "fuel_daily_snapshots", mapper: loadFuelDaily},
		{name: "fuel_monthly_snapshots", mapper: loadFuelMonthly},
		{name: "hubs", mapper: loadHub, prepare: prepareHubs, reapply: hubTakesOver},
		{name: "hub_soc_distances", mapper: loadDistance},
		{name: "soc_hub_distances", mapper: loadDistance},
		{name: "metadata", mapper: loadMetadata},
		{name: "trucks", mapper: loadTruck},
		{name: "drivers", mapper: loadDriver},
		{name: "truckAssignment", mapper: loadTruckAssignment, after: afterTruckAssignments},
		{name: "mobile_installations", mapper: loadInstallation},
		{name: "tasks", mapper: loadTask, after: afterTasks},
		{name: "trip_records", mapper: loadTrip},
		{name: "standby_records", mapper: loadStandby},
		{name: "incidentReport", mapper: loadIncident},
		{name: "billing_statements", mapper: loadStatement, after: afterStatements},
		{name: "billing_counters", mapper: loadCounter},
		{name: "vehicle_expenses", mapper: loadExpense},
		{name: "maintenance", mapper: loadMaintenance, after: afterMaintenance},
		{name: "driver_compensation_config", mapper: loadCompensationConfig},
		{name: "driver_penalties", mapper: loadPenalty},
		{name: "payroll", mapper: loadPayroll},
		{name: "transactions", mapper: loadTransaction, after: afterTransactions},
		{name: "chats", mapper: loadChat},
		{name: "messages", mapper: loadChatMessage},
		{name: "broadcasts", mapper: loadBroadcast},
		{name: "leave_requests", mapper: loadLeaveRequest},
		{name: "holidays", mapper: loadHoliday},
		{name: "security_events", mapper: loadSecurityEvent},
		{name: "vehicle_locations", mapper: loadVehicleLocation},
		{name: "waitlist", mapper: loadWaitlist},
		{name: "partner-interest", mapper: loadPartnerInterest},
		{name: "settings", mapper: loadSetting},
	}
}

func specOf(name string) (collectionSpec, bool) {
	for _, s := range mappedCollections() {
		if s.name == name {
			return s, true
		}
	}
	return collectionSpec{}, false
}

func mapperOf(name string) (mapper, bool) {
	s, ok := specOf(name)
	return s.mapper, ok
}

// MappedCollections lists the collections this release loads, in load order.
func MappedCollections() []string {
	var out []string
	for _, s := range mappedCollections() {
		out = append(out, s.name)
	}
	return out
}

// droppedCollections are intentionally not migrated (Appendix A §A.3.10): kept only in etl.source_docs.
var droppedCollections = map[string]bool{"checkin": true}

// separateCollections are in the Appendix A mapping but loaded by their own, signed-off step instead of `load`:
// the users documents by `etl auth-import` (T19), which joins them with the Firebase Auth export (Appendix C §C.5;
// the users rows belong to PostgreSQL from P0, R49), and the permissions_config documents by `etl auth-rbac` (T51,
// Appendix C §C.2.6). `load` records them pending (no finding, no watermark) and reconcile accepts pending for them
// only.
var separateCollections = map[string]bool{"users": true, "permissions_config": true}

// docCtx is the state of one document while its mapper runs.
type docCtx struct {
	ctx          context.Context
	tx           pgx.Tx
	e            *Engine
	coll         string
	doc          dump.Doc
	f            map[string]any
	findings     []Finding
	outcome      Outcome
	table        string
	target       string
	tenantSource tenancy.Source
	billable     bool
	// leaving is the resolved tenant of a row that exists in the quarantine tenant and whose chain resolves now
	// (a fix at source): after the upsert its children and files follow it (Engine.follow).
	leaving uuid.UUID
	// run is the state of the load (or retry) the document belongs to.
	run *runState
	// findingScope, when not nil, limits the open findings record replaces to these fields (and their sub-fields): a
	// field-level mirror re-reads only the fields it applies, so the findings the P1 load raised on the other fields of
	// the document stay open. A row-level finding the mapper raises replaces the row-level ones too.
	findingScope []string
}

// sub is a context over a nested map of the document (activeTruck, a photo's geocoding, a stop): its findings stay
// on the sub context until the caller copies them with prefix.
func (c *docCtx) sub(f map[string]any) *docCtx {
	return &docCtx{ctx: c.ctx, tx: c.tx, e: c.e, coll: c.coll, doc: c.doc, f: f, run: c.run}
}

// adopt copies the findings of a sub context onto c, their fields prefixed.
func (c *docCtx) adopt(sub *docCtx, prefix string) {
	for _, f := range sub.findings {
		c.find(prefix+f.Field, f.Reason, f.Detail, f.Raw)
	}
}

// find records a finding.
func (c *docCtx) find(field string, r Reason, detail string, raw any) {
	c.findings = append(c.findings, Finding{Field: field, Reason: r, Detail: detail, Raw: raw})
}

// reject marks the document rejected with a row-level finding. A document that carries billing evidence is never
// rejected (R19): the run aborts instead, so the source can be fixed first.
func (c *docCtx) reject(field string, r Reason, detail string, raw any) error {
	if c.billable {
		return &AbortError{Collection: c.coll, Path: c.doc.Path, Field: field, Reason: r,
			Detail: fmt.Sprintf("%s (field %q) on a document with billing evidence; fix it at the source and dump again (R19)", detail, field)}
	}
	c.outcome = Rejected
	c.table, c.target, c.tenantSource, c.leaving = "", "", "", uuid.Nil
	c.findings = append(c.findings, Finding{Field: field, Reason: r, Detail: detail, Raw: raw})
	return nil
}

// billableSet knows which documents carry billing evidence (R19).
type billableSet struct {
	tasks map[string]bool // task doc ids and business numbers referenced by trips with billing evidence
}

// hasBillingEvidence: a billingEstimateThb number, any billing* field, deliveredTimestamp on a delivered trip,
// or a standby endedAt (Appendix A §A.3.0 guard). A sent or paid billing statement is billing evidence too: it is
// the period lock pricing reads (R17), so a statement that cannot load stops the run instead of unlocking a period.
func hasBillingEvidence(coll string, f map[string]any) bool {
	if coll == "billing_statements" {
		s, _ := f["status"].(string)
		s = canon(s)
		return s == "sent" || s == "paid"
	}
	for k, v := range f {
		if strings.HasPrefix(k, "billing") && v != nil {
			return true
		}
	}
	switch coll {
	case "trip_records":
		s, _ := f["status"].(string)
		return strings.EqualFold(strings.TrimSpace(s), "delivered") && present(f["deliveredTimestamp"])
	case "standby_records":
		return present(f["endedAt"])
	}
	return false
}

func (b billableSet) has(coll string, d dump.Doc) bool {
	switch coll {
	case "trip_records", "standby_records", "billing_statements":
		return hasBillingEvidence(coll, d.Fields)
	case "tasks":
		no, _ := d.Fields["taskId"].(string)
		return b.tasks[d.ID] || (no != "" && b.tasks[no])
	}
	return false
}

// billableRefs reads the trips of the dump once to know which tasks a priced or stamped trip references.
func billableRefs(d *dump.Dump) (billableSet, error) {
	if _, ok := d.Collection("trip_records"); !ok {
		return billableFrom(nil), nil
	}
	trips, err := d.Read("trip_records")
	if err != nil {
		return billableSet{}, err
	}
	return billableFrom(trips), nil
}

// billableFrom is the billable set of a collection of trips (a dump's, or the mirror's read of Firestore).
func billableFrom(trips []dump.Doc) billableSet {
	b := billableSet{tasks: map[string]bool{}}
	for _, t := range trips {
		if ref, _ := t.Fields["taskId"].(string); ref != "" && hasBillingEvidence("trip_records", t.Fields) {
			b.tasks[strings.TrimSpace(ref)] = true
		}
	}
	return b
}
