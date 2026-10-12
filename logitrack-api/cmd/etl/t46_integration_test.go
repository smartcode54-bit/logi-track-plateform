//go:build integration

// Acceptance of issue T46 (#66) for the hr cut-over (main spec §13.9 step 3, §13.10 as built T46) on postgres:18-alpine
// with the committed fixture dump (no production data): after the P1 load, changes at source to a draft payroll run, a
// penalty's balance and a compensation config version make `reconcile --hr` exit 2 naming each check until
// `load --since=watermark` applies them; then it exits 0. A run, a payout link or a penalty changed in PostgreSQL
// instead makes it exit 2 again, and a narrow dump fails hr_inputs.
package main

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// fixtureCollections reads every collection of the committed fixture dump, and which of them are collection groups.
func fixtureCollections(t *testing.T) (map[string][]dump.Doc, []string, map[string]bool) {
	t.Helper()
	sub, err := fs.Sub(fixtures, fixturesDir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := dump.Open(sub)
	if err != nil {
		t.Fatal(err)
	}
	out, groups := map[string][]dump.Doc{}, map[string]bool{}
	for _, n := range d.Names() {
		docs, err := d.Read(n)
		if err != nil {
			t.Fatal(err)
		}
		out[n] = docs
		c, _ := d.Collection(n)
		groups[n] = c.Group
	}
	return out, d.Names(), groups
}

// hrStage2 is the fixture dump after the changes at source: the draft run of uid_own's September R2 regenerated, an
// approval elsewhere paying pen_1's second installment, and cfg_2026's weekday rate edited in place (the legacy config
// page wrote versions; a changed document is what --since must catch). only, when set, keeps those collections.
func hrStage2(t *testing.T, only ...string) string {
	t.Helper()
	colls, names, groups := fixtureCollections(t)
	changed := time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)
	edit := func(coll, id string, f func(map[string]any)) {
		for i, d := range colls[coll] {
			if d.ID == id {
				f(d.Fields)
				d.UpdateTime = changed
				colls[coll][i] = d
				return
			}
		}
		t.Fatalf("fixture %s/%s not found", coll, id)
	}
	edit("payroll", "uid_own_2026-09_R2", func(f map[string]any) {
		f["totalEarnings"], f["totalDeductions"], f["netPay"] = float64(1500), float64(0), float64(1500)
		f["lineItems"] = []any{map[string]any{"type": "EARNING", "category": "TRIP_COMMISSION", "name": "Trips", "amount": float64(1500),
			"quantity": float64(5), "unitRateThb": float64(300)}}
	})
	edit("driver_penalties", "pen_1", func(f map[string]any) {
		f["remainingThb"], f["installmentsPaid"] = float64(100), float64(2)
	})
	edit("driver_compensation_config", "cfg_2026", func(f map[string]any) { f["weekdayRateThb"] = float64(320) })
	dir := t.TempDir()
	w, err := dump.NewWriter(dir, changed.Add(time.Hour), "p", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if len(only) > 0 && !slices.Contains(only, n) {
			continue
		}
		if err := w.WriteCollection(n, groups[n], colls[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHRCutoverReconcile(t *testing.T) {
	h := newHarness(t)
	h.mustRun("load", "--fixtures")
	if out := h.mustRun("reconcile", "--fixtures", "--hr"); !strings.Contains(out, "reconcile: match") {
		t.Fatalf("reconcile --hr on the P1 load:\n%s", out)
	}
	stage2 := hrStage2(t)

	// The changes at source are not loaded yet: the counts still match (each document was loaded once), the hr checks
	// do not.
	code, out, errb := h.run("reconcile", "--dump", stage2, "--hr")
	if code != 2 {
		t.Fatalf("reconcile --hr before the delta load: exit %d\n%s\n%s", code, out, errb)
	}
	for _, want := range []string{
		"MISMATCH\tpayroll_net\t2026-09 R2 draft\tsource 1500.00\ttarget 0.00",
		"MISMATCH\tpayroll_earnings\t2026-09 R2 draft\tsource 1500.00\ttarget 0.00",
		"MISMATCH\tpenalty_remaining\tpartially_deducted\tsource 100.00\ttarget 200.00",
		"MISMATCH\tpenalty_installments\tpartially_deducted\tsource 2\ttarget 1",
		"MISMATCH\thr_config\tcfg_2026\tsource dump\ttarget database\tdiffers in weekday_rate_thb",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "MISMATCH\tcounts") || strings.Contains(out, "MISMATCH\tpayroll_ledger") {
		t.Errorf("only the changed values mismatch:\n%s", out)
	}

	// load --since=watermark applies them; the same dump then reconciles.
	h.mustRun("load", "--dump", stage2, "--since", "watermark")
	if out := h.mustRun("reconcile", "--dump", stage2, "--hr"); !strings.Contains(out, "reconcile: match") {
		t.Fatalf("reconcile --hr after the delta load:\n%s", out)
	}
	if v := h.scalar(`SELECT count(*) FROM payroll_line_items l JOIN payroll_runs r ON r.id = l.payroll_run_id
		WHERE r.legacy_doc_id = 'uid_own_2026-09_R2'`); v != int64(1) {
		t.Errorf("the regenerated draft's lines: %v", v)
	}

	// A value changed in PostgreSQL (not at source) fails the gate too, naming the check.
	for _, tc := range []struct {
		name, tamper, undo, want string
	}{
		{"a run's net pay", `UPDATE payroll_runs SET net_pay_thb = net_pay_thb + 1 WHERE legacy_doc_id = 'uid_own_2026-09_R1'`,
			`UPDATE payroll_runs SET net_pay_thb = net_pay_thb - 1 WHERE legacy_doc_id = 'uid_own_2026-09_R1'`,
			"MISMATCH\tpayroll_net\t2026-09 R1 approved\tsource 2900.00\ttarget 2901.00"},
		{"a payout link", `UPDATE payroll_runs SET ledger_transaction_id = NULL WHERE legacy_doc_id = 'uid_own_2026-09_R1'`,
			`UPDATE payroll_runs SET ledger_transaction_id = (SELECT id FROM transactions WHERE legacy_doc_id = 'tx_payout_1')
			 WHERE legacy_doc_id = 'uid_own_2026-09_R1'`,
			"MISMATCH\tpayroll_ledger\tpayroll\tsource 1\ttarget 0"},
		{"a penalty's total", `UPDATE driver_penalties SET total_thb = total_thb + 100 WHERE legacy_doc_id = 'pen_1'`,
			`UPDATE driver_penalties SET total_thb = total_thb - 100 WHERE legacy_doc_id = 'pen_1'`,
			"MISMATCH\tpenalty_total\tpartially_deducted\tsource 300.00\ttarget 400.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.system(tc.tamper)
			defer h.system(tc.undo)
			code, out, errb := h.run("reconcile", "--dump", stage2, "--hr")
			if code != 2 || !strings.Contains(out, tc.want) {
				t.Errorf("exit %d, want 2 with %q\n%s\n%s", code, tc.want, out, errb)
			}
		})
	}
	if out := h.mustRun("reconcile", "--dump", stage2, "--hr"); !strings.Contains(out, "reconcile: match") {
		t.Fatalf("reconcile --hr after the undo:\n%s", out)
	}

	// A narrow dump leaves the hr collections it lacks unchecked: the gate refuses it.
	narrow := hrStage2(t, "payroll", "driver_penalties", "driver_compensation_config")
	code, out, errb = h.run("reconcile", "--dump", narrow, "--hr")
	if code != 2 || !strings.Contains(out, "MISMATCH\thr_inputs\tholidays") || !strings.Contains(out, "MISMATCH\thr_inputs\ttransactions") {
		t.Errorf("a narrow dump: exit %d\n%s\n%s", code, out, errb)
	}
	// Without --hr the same dump is not checked for the hr inputs.
	if code, out, _ := h.run("reconcile", "--dump", narrow); strings.Contains(out, "hr_inputs") {
		t.Errorf("reconcile without --hr ran the hr checks (exit %d):\n%s", code, out)
	}
}
