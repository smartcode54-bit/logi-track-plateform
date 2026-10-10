package scheduler

import (
	"slices"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

func TestCronRunsInBangkok(t *testing.T) {
	s, err := parser.Parse("0 4 * * *")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-10-10 20:59 UTC is 03:59 on the 11th in Bangkok: the next 04:00 Bangkok is one minute later.
	next := s.Next(time.Date(2026, 10, 10, 20, 59, 0, 0, time.UTC).In(clock.Bangkok))
	if !next.Equal(time.Date(2026, 10, 10, 21, 0, 0, 0, time.UTC)) {
		t.Fatalf("next 04:00 Bangkok = %s", next.UTC())
	}
}

func TestTableAndPending(t *testing.T) {
	table := Table(nil, nil)
	var names []string
	for _, j := range table {
		if _, err := parser.Parse(j.Spec); err != nil || j.Kind != Local || j.Run == nil {
			t.Fatalf("%s: %v", j.Name, err)
		}
		names = append(names, j.Name)
	}
	want := []string{"auth.token-cleanup", "outbox.prune", "inbox.prune", "jobs.prune", "idempotency.prune", "rtlog.trim"}
	if !slices.Equal(names, want) {
		t.Fatalf("live table %v, want %v", names, want)
	}
	for _, p := range Pending {
		if _, err := parser.Parse(p.Spec); err != nil || slices.Contains(names, p.Name) || p.Issue == "" {
			t.Fatalf("pending %+v", p)
		}
	}
	if len(table)+len(Pending) != 12 {
		t.Fatalf("§7.4 lists 12 scheduled jobs besides the relay; table %d + pending %d", len(table), len(Pending))
	}
	if _, err := NewCron(nil, nil, []Job{{Name: "x.y", Spec: "every day", Kind: Command}}, zerolog.Nop(), nil); err == nil {
		t.Fatal("a bad spec was accepted")
	}
	if _, err := NewCron(nil, nil, []Job{{Name: "x.y", Spec: "@hourly", Kind: Local}}, zerolog.Nop(), nil); err == nil {
		t.Fatal("a local job without Run was accepted")
	}
}
