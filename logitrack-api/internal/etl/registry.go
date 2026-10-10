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

type collectionSpec struct {
	name   string
	mapper mapper
}

// mappedCollections are the collections this release loads, in the load order of Appendix A §A.3.0 (tenants,
// customers and parties before everything that links to them; trucks before drivers; tasks -> trips -> standby
// -> incidents so links are stamped before fallbacks, tenantResolve.ts:88-93).
func mappedCollections() []collectionSpec {
	return []collectionSpec{
		{"subcontractors", loadSubcontractor},
		{"customers", loadCustomer},
		{"trucks", loadTruck},
		{"drivers", loadDriver},
		{"tasks", loadTask},
		{"trip_records", loadTrip},
		{"standby_records", loadStandby},
		{"incidentReport", loadIncident},
		{"vehicle_expenses", loadExpense},
		{"maintenance", loadMaintenance},
	}
}

func mapperOf(name string) (mapper, bool) {
	for _, s := range mappedCollections() {
		if s.name == name {
			return s.mapper, true
		}
	}
	return nil, false
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

// laterCollections are in the Appendix A mapping but load with a later task (T19 users and RBAC, T24 the P1
// initial load of every remaining collection): their documents are recorded with status pending and no finding,
// and the first load that maps the collection applies them.
var laterCollections = map[string]bool{
	"users": true, "permissions_config": true, "companies": true, "hubs": true, "hub_soc_distances": true,
	"soc_hub_distances": true, "metadata": true, "truckAssignment": true, "mobile_installations": true,
	"customer_rate_entries": true, "customer_fuel_rate_adjustments": true, "customer_service_fees": true,
	"standby_rate_entries": true, "fuel_daily_snapshots": true, "fuel_monthly_snapshots": true,
	"billing_statements": true, "billing_counters": true, "payroll": true, "driver_penalties": true,
	"driver_compensation_config": true, "transactions": true, "holidays": true, "broadcasts": true, "chats": true,
	"messages": true, "security_events": true, "vehicle_locations": true, "waitlist": true, "partner-interest": true,
	"settings": true, "leave_requests": true,
}

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
}

// find records a finding.
func (c *docCtx) find(field string, r Reason, detail string, raw any) {
	c.findings = append(c.findings, Finding{Field: field, Reason: r, Detail: detail, Raw: raw})
}

// reject marks the document rejected with a row-level finding. A document that carries billing evidence is never
// rejected (R19): the run aborts instead, so the source can be fixed first.
func (c *docCtx) reject(field string, r Reason, detail string, raw any) error {
	if c.billable {
		return &AbortError{Collection: c.coll, Path: c.doc.Path, Reason: r,
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
// or a standby endedAt (Appendix A §A.3.0 guard).
func hasBillingEvidence(coll string, f map[string]any) bool {
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
	case "trip_records", "standby_records":
		return hasBillingEvidence(coll, d.Fields)
	case "tasks":
		no, _ := d.Fields["taskId"].(string)
		return b.tasks[d.ID] || (no != "" && b.tasks[no])
	}
	return false
}

// billableRefs reads the trips of the dump once to know which tasks a priced or stamped trip references.
func billableRefs(d *dump.Dump) (billableSet, error) {
	b := billableSet{tasks: map[string]bool{}}
	if _, ok := d.Collection("trip_records"); !ok {
		return b, nil
	}
	trips, err := d.Read("trip_records")
	if err != nil {
		return b, err
	}
	for _, t := range trips {
		if ref, _ := t.Fields["taskId"].(string); ref != "" && hasBillingEvidence("trip_records", t.Fields) {
			b.tasks[strings.TrimSpace(ref)] = true
		}
	}
	return b, nil
}
