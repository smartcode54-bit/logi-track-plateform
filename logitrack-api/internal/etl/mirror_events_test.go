package etl

import (
	"encoding/json"
	"slices"
	"testing"
)

func opsOf(cols map[string]any) opsState {
	s := opsState{row: map[string]json.RawMessage{}}
	for k, v := range cols {
		b, _ := json.Marshal(v)
		s.row[k] = b
	}
	return s
}

// The routing keys an applied version emits (mirror_events.go, T29; Appendix B §B.5.2).
func TestOpsRoutes(t *testing.T) {
	d1, d2 := "11111111-1111-7111-8111-111111111111", "22222222-2222-7222-8222-222222222222"
	cases := []struct {
		name          string
		coll          string
		created       bool
		before, after map[string]any
		want          []string
	}{
		{"new task", "tasks", true, nil, map[string]any{"status": "pending"}, []string{"task.created"}},
		{"assigned", "tasks", false, map[string]any{"status": "pending", "driver_id": nil}, map[string]any{"status": "assigned", "driver_id": d1},
			[]string{"task.assigned"}},
		{"reassigned", "tasks", false, map[string]any{"driver_id": d1}, map[string]any{"driver_id": d2}, []string{"task.reassigned"}},
		{"unassigned", "tasks", false, map[string]any{"driver_id": d1}, map[string]any{"driver_id": nil}, []string{"task.reassigned"}},
		{"cancelled wins", "tasks", false, map[string]any{"status": "assigned", "driver_id": d1}, map[string]any{"status": "cancelled", "driver_id": nil},
			[]string{"task.cancelled"}},
		{"checked in", "tasks", false, map[string]any{"status": "assigned"}, map[string]any{"status": "checked_in"}, []string{"task.checked_in"}},
		{"plan day moved", "tasks", false, map[string]any{"plan_at": "a", "plan_date": "2026-10-01"}, map[string]any{"plan_at": "b", "plan_date": "2026-10-02"},
			[]string{"task.plan_date_changed"}},
		{"plan time moved", "tasks", false, map[string]any{"plan_at": "a", "plan_date": "2026-10-01"}, map[string]any{"plan_at": "b", "plan_date": "2026-10-01"},
			[]string{"task.updated"}},
		{"edit and move", "tasks", false, map[string]any{"run_order": 1, "plan_date": "2026-10-01"}, map[string]any{"run_order": 2, "plan_date": "2026-10-02"},
			[]string{"task.updated", "task.plan_date_changed"}},
		{"new trip", "trip_records", true, nil, map[string]any{"status": "in_transit"}, []string{"trip.created"}},
		{"first seen delivered", "trip_records", true, nil, map[string]any{"status": "delivered"}, []string{"trip.delivered"}},
		{"delivered", "trip_records", false, map[string]any{"status": "in_transit"}, map[string]any{"status": "delivered", "delivered_at": "t"},
			[]string{"trip.delivered"}},
		{"delivered edit", "trip_records", false, map[string]any{"status": "delivered", "seal_code": "a"},
			map[string]any{"status": "delivered", "seal_code": "b"}, []string{"trip.updated"}},
		{"delivery moved", "trip_records", false, map[string]any{"status": "delivered", "delivered_at": "t1"},
			map[string]any{"status": "delivered", "delivered_at": "t2"}, []string{"trip.updated", "trip.delivered_at_changed"}},
		{"cancelled", "trip_records", false, map[string]any{"status": "in_transit"}, map[string]any{"status": "cancelled"}, []string{"trip.cancelled"}},
		{"resubmitted", "trip_records", false, map[string]any{"resubmitted_at": nil}, map[string]any{"resubmitted_at": "t"}, []string{"trip.resubmitted"}},
		{"new standby", "standby_records", true, nil, map[string]any{}, []string{"standby.completed"}},
		{"standby end moved", "standby_records", false, map[string]any{"ended_at": "a"}, map[string]any{"ended_at": "b"}, []string{"standby.completed"}},
		{"customer", "standby_records", false, map[string]any{"customer_party_id": nil}, map[string]any{"customer_party_id": d1},
			[]string{"standby.customer_assigned"}},
		{"standby note", "standby_records", false, map[string]any{"note": "a"}, map[string]any{"note": "b"}, nil},
		{"new incident", "incidentReport", true, nil, map[string]any{}, []string{"incident.created"}},
		{"incident edit", "incidentReport", false, map[string]any{"description": "a"}, map[string]any{"description": "b"}, nil},
	}
	for _, c := range cases {
		before, after := opsOf(c.before), opsOf(c.after)
		var changed []string
		if !c.created {
			changed = changedColumns(opsTables[c.coll], before, after)
		}
		if got := opsRoutes(c.coll, c.created, before, after, changed); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v (changed %v)", c.name, got, c.want, changed)
		}
	}
}

// Generated columns and the outputs other producers own are no change of the row.
func TestChangedColumnsIgnoresOwnedOutputs(t *testing.T) {
	before := opsOf(map[string]any{"updated_at": "a", "billing_estimate_thb": 1, "ended_at": "x"})
	after := opsOf(map[string]any{"updated_at": "b", "billing_estimate_thb": 2, "ended_at": "x"})
	if got := changedColumns(opsTables["standby_records"], before, after); len(got) != 0 {
		t.Fatalf("standby pricing outputs counted as a change: %v", got)
	}
	tb, ta := opsOf(map[string]any{"updated_at": "a", "billing_axis_date": "d1"}), opsOf(map[string]any{"updated_at": "b", "billing_axis_date": "d2"})
	tb.digest, ta.digest = "a", "b"
	if got := changedColumns(opsTables["trip_records"], tb, ta); !slices.Equal(got, []string{"photosOrStops"}) {
		t.Fatalf("children: %v", got)
	}
	if camel("billing_axis_date") != "billingAxisDate" || camel("status") != "status" {
		t.Fatal("camel")
	}
}
