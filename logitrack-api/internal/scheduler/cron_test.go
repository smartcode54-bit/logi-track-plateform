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
	table := Table(nil, nil, Gates{})
	var names []string
	for _, j := range table {
		// Local jobs run in the scheduler; command jobs (storage.gc, T11) only publish job.{name}.
		if _, err := parser.Parse(j.Spec); err != nil || (j.Kind == Local) != (j.Run != nil) {
			t.Fatalf("%s: %v", j.Name, err)
		}
		names = append(names, j.Name)
	}
	want := []string{"storage.gc", "tenancy.orphan-scan", "auth.token-cleanup", "outbox.prune", "inbox.prune", "jobs.prune",
		"idempotency.prune", "rtlog.trim"}
	if table[0].Kind != Command || table[0].Spec != "0 * * * *" {
		t.Fatalf("storage.gc is the hourly command cron of Appendix B §B.5.6: %+v", table[0])
	}
	if table[1].Kind != Command || table[1].Spec != "0 3 * * *" {
		t.Fatalf("tenancy.orphan-scan is the daily 03:00 command cron of Appendix C §C.3.10 (T28): %+v", table[1])
	}
	if !slices.Equal(names, want) {
		t.Fatalf("live table %v, want %v", names, want)
	}
	for _, p := range Pending {
		if _, err := parser.Parse(p.Spec); err != nil || slices.Contains(names, p.Name) || p.Issue == "" {
			t.Fatalf("pending %+v", p)
		}
	}
	for _, g := range Gated {
		if _, err := parser.Parse(g.Spec); err != nil || slices.Contains(names, g.Name) || g.Gate == "" {
			t.Fatalf("gated %+v", g)
		}
	}
	if len(table)+len(Gated)+len(Pending) != 13 {
		t.Fatalf("§7.4 lists 13 scheduled jobs besides the relay; table %d + gated %d + pending %d", len(table), len(Gated), len(Pending))
	}
	// FUEL_MONTHLY_SNAPSHOT_ENABLED adds bangchak.snapshot at 05:00 Asia/Bangkok as a command cron (T37, R70).
	on := Table(nil, nil, Gates{FuelMonthlySnapshot: true})
	last := on[len(on)-1]
	if len(on) != len(table)+1 || last.Name != "bangchak.snapshot" || last.Spec != "0 5 * * *" || last.Kind != Command {
		t.Fatalf("gated table: %+v", last)
	}
	// ETL_SYNC_ENABLED adds the etl.sync sweep at ETL_SYNC_INTERVAL (every 5 minutes by default) (T24S) and the daily
	// coexistence reconciliation etl.reconcile at 02:30 (T29).
	for spec, want := range map[string]string{"": "*/5 * * * *", "*/15 * * * *": "*/15 * * * *"} {
		on = Table(nil, nil, Gates{ETLSync: true, ETLSyncSpec: spec})
		sweep, rec := on[len(on)-2], on[len(on)-1]
		if len(on) != len(table)+2 || sweep.Name != "etl.sync" || sweep.Spec != want || sweep.Kind != Command {
			t.Fatalf("etl.sync %q: %+v", spec, sweep)
		}
		if rec.Name != "etl.reconcile" || rec.Spec != "30 2 * * *" || rec.Kind != Command {
			t.Fatalf("etl.reconcile: %+v", rec)
		}
	}
	// CARTRACK_SYNC_ENABLED adds cartrack.sync every three minutes as a command cron (T53, R70).
	on = Table(nil, nil, Gates{CartrackSync: true})
	last = on[len(on)-1]
	if len(on) != len(table)+1 || last.Name != "cartrack.sync" || last.Spec != "*/3 * * * *" || last.Kind != Command {
		t.Fatalf("cartrack.sync: %+v", last)
	}
	if slices.ContainsFunc(Pending, func(p struct{ Name, Spec, Issue, Gate string }) bool { return p.Name == "cartrack.sync" }) {
		t.Fatal("cartrack.sync's consumer has landed (T53): it is gated, not pending")
	}
	if _, err := NewCron(nil, nil, []Job{{Name: "x.y", Spec: "every day", Kind: Command}}, zerolog.Nop(), nil); err == nil {
		t.Fatal("a bad spec was accepted")
	}
	if _, err := NewCron(nil, nil, []Job{{Name: "x.y", Spec: "@hourly", Kind: Local}}, zerolog.Nop(), nil); err == nil {
		t.Fatal("a local job without Run was accepted")
	}
}
