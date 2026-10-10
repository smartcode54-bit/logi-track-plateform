package seed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
)

// Row is one row to insert: column -> value. Fixture values keep their JSON shape (nil, bool, json.Number,
// string, []any, map[string]any); generated rows may also hold []byte (bytea). Keys starting with "_" are
// annotations (Appendix D §D.4.1) and are never inserted.
type Row map[string]any

// clone copies the top level of r.
func (r Row) clone() Row {
	out := make(Row, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

// Columns lists the insertable columns of r (annotations excluded), sorted.
func (r Row) Columns() []string {
	out := make([]string, 0, len(r))
	for k := range r {
		if !strings.HasPrefix(k, "_") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Str returns a string column ("" when absent or not a string).
func (r Row) Str(col string) string {
	s, _ := r[col].(string)
	return s
}

// LoadOrder is the table order of Appendix D §D.1.5: parents before children, the forward references
// written NULL first and back-filled (Deferred). task_number_counters is derived after the load.
var LoadOrder = []string{
	"tenants", "users", "auth_identities", "file_objects", "customers", "billing_parties", "companies",
	"memberships", "user_platform_roles", "user_scopes", "role_capability_overrides",
	"hubs", "hub_name_aliases", "hub_soc_distances", "trucks", "drivers", "truck_assignments", "maintenance_records",
	"customer_rate_entries", "customer_fuel_rate_adjustments", "customer_service_fees", "standby_rate_entries",
	"tasks", "task_delivery_stops", "trip_records", "trip_no_history", "trip_delivery_stops", "trip_photos",
	"standby_records", "incident_reports", "outbox_events",
	"trip_billing_snapshots", "trip_billing_stop_breakdown", "billing_statements", "billing_statement_lines",
	"billing_counters", "statement_documents",
	"vehicle_expenses", "driver_compensation_configs", "penalty_types", "driver_penalties",
	"payroll_runs", "payroll_line_items", "payroll_penalty_applications", "transactions", "driver_advances",
	"chats", "chat_messages", "broadcasts", "broadcast_reads", "leave_requests", "holidays",
	"mobile_app_releases", "mobile_installations", "device_tokens", "settings", "jobs", "security_events",
}

// DerivedTable is computed from the loaded tasks (R10) instead of being listed in a fixture.
const DerivedTable = "task_number_counters"

// Deferred are the columns written after every table is loaded (Appendix D §D.1.5): the four forward
// references of the schema, and the payroll run columns that must wait for the run's lines (a run's lines
// are writable only while it is draft). The value is what the insert writes instead (nil = NULL).
var Deferred = map[string]map[string]any{
	"drivers":      {"current_assignment_id": nil, "active_task_id": nil},
	"trucks":       {"active_maintenance_id": nil},
	"payroll_runs": {"status": "draft", "approved_by": nil, "approved_at": nil, "ledger_transaction_id": nil, "payment_date": nil, "payment_method": nil},
}

// ReadFixture reads <dir>/<table>.json for every table of the load order. A file for a table outside
// the order is refused, so a renamed table cannot be skipped silently.
func ReadFixture(fsys fs.FS, dir string) (map[string][]Row, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("seed: fixture %s: %w", dir, err)
	}
	out := map[string][]Row{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			return nil, fmt.Errorf("seed: fixture %s: unexpected entry %s", dir, name)
		}
		table := strings.TrimSuffix(name, ".json")
		if !slices.Contains(LoadOrder, table) {
			return nil, fmt.Errorf("seed: fixture %s: %s is not a table of the load order", dir, name)
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var rows []Row
		if err := dec.Decode(&rows); err != nil {
			return nil, fmt.Errorf("seed: fixture %s: %w", name, err)
		}
		out[table] = rows
	}
	return out, nil
}

// resolveValue replaces @SYM references in every string of v, recursively through JSON arrays and
// objects (object keys, realtime topics and payloads carry ids too).
func resolveValue(v any, s Symbols) (any, error) {
	switch x := v.(type) {
	case string:
		return s.Resolve(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			r, err := resolveValue(e, s)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			r, err := resolveValue(e, s)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

// computed reports whether v is a "$seed:" placeholder (computed at load time, §D.1.9).
func computed(v any) (string, bool) {
	s, ok := v.(string)
	if !ok || !strings.HasPrefix(s, "$seed:") {
		return "", false
	}
	return strings.TrimPrefix(s, "$seed:"), true
}
