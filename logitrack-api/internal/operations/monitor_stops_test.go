package operations

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestWithPlannedStops(t *testing.T) {
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	plan := func(i int32) MonitorStop { return MonitorStop{Index: i, Status: "pending", Planned: true} }
	done := func(i int32) MonitorStop { return MonitorStop{Index: i, Status: "delivered", DeliveredAt: &at} }
	idx := func(stops []MonitorStop) (out []string) {
		for _, s := range stops {
			kind := "done"
			if s.Planned {
				kind = "plan"
			}
			out = append(out, string(rune('0'+s.Index))+kind)
		}
		return out
	}
	cases := []struct {
		name           string
		progress, plan []MonitorStop
		want           []string
	}{
		{"freshly loaded: the whole plan, pending", nil, []MonitorStop{plan(1), plan(2), plan(3)}, []string{"1plan", "2plan", "3plan"}},
		{"half done: progress replaces its planned stop", []MonitorStop{done(1)}, []MonitorStop{plan(1), plan(2)}, []string{"1done", "2plan"}},
		{"a stop the driver added is kept, in index order", []MonitorStop{done(3), done(1)}, []MonitorStop{plan(1), plan(2)}, []string{"1done", "2plan", "3done"}},
		{"no multi-drop plan: progress as is", []MonitorStop{done(1)}, []MonitorStop{plan(1)}, []string{"1done"}},
		{"no plan, no progress", nil, nil, nil},
	}
	for _, c := range cases {
		if got := idx(withPlannedStops(c.progress, c.plan)); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTaskIDsOfAndPlanOf(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	ids := taskIDsOf([]*uuid.UUID{&a, nil, &b, &a})
	if len(ids) != 2 || ids[0] != a || ids[1] != b {
		t.Fatalf("taskIDsOf: got %v", ids)
	}
	byTask := map[uuid.UUID][]MonitorStop{a: {{Index: 1}, {Index: 2}}}
	if planOf(byTask, nil) != nil || len(planOf(byTask, &a)) != 2 || planOf(byTask, &b) != nil {
		t.Fatal("planOf: a trip without a task has no plan; a task's plan is its stops")
	}
}
