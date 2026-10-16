package seed

import (
	"slices"
	"strings"
	"testing"
)

func isolationTables() []rlsTable {
	tables := make([]rlsTable, 0, len(ownershipRules)+len(sharedTables)+len(systemOnlyTables)+1)
	for name := range ownershipRules {
		tables = append(tables, rlsTable{name: name})
	}
	for _, name := range sharedTables {
		tables = append(tables, rlsTable{name: name})
	}
	for _, name := range systemOnlyTables {
		tables = append(tables, rlsTable{name: name, tenantID: true})
	}
	tables = append(tables, rlsTable{name: "trucks", tenantID: true})
	return tables
}

func TestClassifySystemOnlyTables(t *testing.T) {
	plan := classify(isolationTables())
	if len(plan.problems) != 0 {
		t.Fatalf("system-only RLS tables should be classified explicitly: %v", plan.problems)
	}
	if !slices.Equal(plan.systemOnly, systemOnlyTables) {
		t.Fatalf("system-only tables %v, want %v", plan.systemOnly, systemOnlyTables)
	}
	if len(plan.checks) != len(ownershipRules)+len(systemOnlyTables)+1 {
		t.Fatalf("carrier role-play has %d checks, want %d", len(plan.checks), len(ownershipRules)+len(systemOnlyTables)+1)
	}
	for _, name := range systemOnlyTables {
		i := slices.IndexFunc(plan.checks, func(ch isolationCheck) bool { return ch.table == name })
		if i < 0 || plan.checks[i].own.tenant != "false" || plan.checks[i].own.self != "" {
			t.Errorf("%s must report every visible row as a leak, even in the carrier's own tenant", name)
		}
	}
	if plan.stamped != 1 || plan.owned != len(ownershipRules) || len(plan.shared) != len(sharedTables) {
		t.Fatalf("unexpected isolation classification: %+v", plan)
	}
}

func TestClassifyMissingSystemOnlyTable(t *testing.T) {
	for _, name := range systemOnlyTables {
		t.Run(name, func(t *testing.T) {
			tables := slices.DeleteFunc(isolationTables(), func(table rlsTable) bool { return table.name == name })
			plan := classify(tables)
			if len(plan.problems) != 1 || !strings.Contains(plan.problems[0], "isolation rule for "+name+",") {
				t.Fatalf("missing system-only table must report a stale rule: %v", plan.problems)
			}
		})
	}
}

func TestClassifyDiscoveredSystemOnlyTable(t *testing.T) {
	// A future system table may have no tenant_id (e.g. support request limits).
	plan := classify(append(isolationTables(), rlsTable{name: "support_request_limits", systemOnly: true}))
	if len(plan.problems) != 0 {
		t.Fatalf("a table whose only policy is p_system must be classified: %v", plan.problems)
	}
	i := slices.IndexFunc(plan.checks, func(ch isolationCheck) bool { return ch.table == "support_request_limits" })
	if i < 0 || plan.checks[i].own.tenant != "false" || plan.checks[i].own.self != "" ||
		!slices.Contains(plan.systemOnly, "support_request_limits") {
		t.Fatal("a discovered system-only table must reject every visible row")
	}
}
