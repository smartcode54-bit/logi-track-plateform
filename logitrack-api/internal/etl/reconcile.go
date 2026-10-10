package etl

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// Reconciliation (main spec §13.10, Appendix A §A.3.11): the source is a dump (the reproducible copy of Firestore);
// counts must be exact, money exact in satang except the legacy multi-drop total (0.005 THB, R20). The run is
// stored in etl.reconciliation_runs; a mismatch is exit 2 for the caller.

// Check is one compared value.
type Check struct {
	Name   string `json:"name"`   // counts | targets | trip_money | trip_unpriced | standby_money | expense_money | findings | quarantine_rows
	Key    string `json:"key"`    // collection, table or "party period"
	Source string `json:"source"` // the dump side
	Target string `json:"target"` // the database side
	OK     bool   `json:"ok"`
	Note   string `json:"note,omitempty"`
}

// Reconciliation is one run.
type Reconciliation struct {
	Period     string    `json:"period,omitempty"`
	ExportedAt time.Time `json:"dumpExportedAt"`
	Match      bool      `json:"match"`
	Checks     []Check   `json:"checks"`
}

// Mismatches lists the failed checks.
func (r *Reconciliation) Mismatches() []Check {
	var out []Check
	for _, c := range r.Checks {
		if !c.OK {
			out = append(out, c)
		}
	}
	return out
}

func (r *Reconciliation) add(name, key string, src, tgt any, ok bool, note string) {
	r.Checks = append(r.Checks, Check{Name: name, Key: key, Source: fmt.Sprint(src), Target: fmt.Sprint(tgt), OK: ok, Note: note})
	if !ok {
		r.Match = false
	}
}

// reconcileTables are the target tables whose rows the counts check, by collection.
var reconcileTables = map[string]string{"subcontractors": "tenants", "customers": "customers", "trucks": "trucks", "drivers": "drivers",
	"tasks": "tasks", "trip_records": "trip_records", "standby_records": "standby_records", "incidentReport": "incident_reports",
	"vehicle_expenses": "vehicle_expenses", "maintenance": "maintenance_records"}

// Reconcile compares the dump with the database. period (YYYY-MM, optional) limits the money checks to one
// Bangkok month.
func (e *Engine) Reconcile(ctx context.Context, d *dump.Dump, period string) (*Reconciliation, error) {
	if period != "" {
		if _, err := time.Parse("2006-01", period); err != nil {
			return nil, errors.New("etl: --period must be YYYY-MM")
		}
	}
	rec := &Reconciliation{Period: period, ExportedAt: d.Manifest.ExportedAt, Match: true}
	docs := map[string][]dump.Doc{}
	for _, n := range d.Names() {
		ds, err := d.Read(n)
		if err != nil {
			return nil, err
		}
		docs[n] = ds
	}
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		status, err := e.reconcileCounts(ctx, tx, d, docs, rec)
		if err != nil {
			return err
		}
		if err := reconcileTrips(ctx, tx, docs, status, period, rec); err != nil {
			return err
		}
		if err := reconcileStandby(ctx, tx, docs, status, period, rec); err != nil {
			return err
		}
		if err := reconcileExpenses(ctx, tx, docs, status, period, rec); err != nil {
			return err
		}
		if err := reconcileFindings(ctx, tx, rec); err != nil {
			return err
		}
		report, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		outcome := "match"
		if !rec.Match {
			outcome = "mismatch"
		}
		var p *string
		if period != "" {
			p = &period
		}
		_, err = tx.Exec(ctx, `INSERT INTO etl.reconciliation_runs (period, finished_at, outcome, report) VALUES ($1, now(), $2, $3)`, p, outcome, report)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("etl: reconcile: %w", err)
	}
	return rec, nil
}

type docStatus map[string]string // collection + "\x00" + path -> status

func (s docStatus) of(coll, path string) string { return s[coll+"\x00"+path] }

// reconcileCounts: per collection, dump count = loaded + quarantined + rejected + dropped, and every loaded or
// quarantined document's row exists (quarantined rows carry tenant_source 'quarantine').
func (e *Engine) reconcileCounts(ctx context.Context, tx pgx.Tx, d *dump.Dump, docs map[string][]dump.Doc, rec *Reconciliation) (docStatus, error) {
	status := docStatus{}
	for _, n := range d.Names() {
		rows, err := tx.Query(ctx, `SELECT doc_path, status, coalesce(target_table, ''), coalesce(target_id, '') FROM etl.source_docs WHERE collection = $1`, n)
		if err != nil {
			return nil, err
		}
		type sd struct{ status, table, target string }
		stored := map[string]sd{}
		for rows.Next() {
			var p string
			var x sd
			if err := rows.Scan(&p, &x.status, &x.table, &x.target); err != nil {
				return nil, err
			}
			stored[p] = x
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		counts := map[string]int{}
		inDump := map[string]bool{}
		var targets, quarantined []string
		for _, doc := range docs[n] {
			inDump[doc.Path] = true
			x, ok := stored[doc.Path]
			if !ok {
				counts["missing"]++
				continue
			}
			status[n+"\x00"+doc.Path] = x.status
			counts[x.status]++
			if (x.status == string(Loaded) || x.status == string(Quarantined)) && x.target != "" {
				targets = append(targets, x.target)
				if x.status == string(Quarantined) {
					quarantined = append(quarantined, x.target)
				}
			}
		}
		accounted := counts[string(Loaded)] + counts[string(Quarantined)] + counts[string(Rejected)] + counts[string(Dropped)]
		note := fmt.Sprintf("loaded %d, quarantined %d, rejected %d, dropped %d, pending %d, missing %d",
			counts[string(Loaded)], counts[string(Quarantined)], counts[string(Rejected)], counts[string(Dropped)], counts[string(Pending)], counts["missing"])
		pendingOK := counts[string(Pending)] > 0 && laterCollections[n] && accounted+counts[string(Pending)] == len(docs[n])
		if pendingOK {
			note += " (collection loads with a later task)"
		}
		rec.add("counts", n, len(docs[n]), accounted, accounted == len(docs[n]) || pendingOK, note)
		gone := 0
		for p := range stored {
			if !inDump[p] {
				gone++
			}
		}
		if gone > 0 {
			rec.add("deleted_at_source", n, 0, gone, true, "documents in etl.source_docs that the dump no longer has (reported, never deleted)")
		}
		table, ok := reconcileTables[n]
		if !ok || len(targets) == 0 {
			continue
		}
		var have, haveQ int
		q := `SELECT count(*), 0 FROM ` + pgx.Identifier{table}.Sanitize() + ` WHERE id = ANY($1::uuid[])`
		if tenantStamped[table] {
			q = `SELECT count(*), count(*) FILTER (WHERE tenant_source = 'quarantine' AND id = ANY($2::uuid[]))
				FROM ` + pgx.Identifier{table}.Sanitize() + ` WHERE id = ANY($1::uuid[])`
		}
		args := []any{targets}
		if tenantStamped[table] {
			args = append(args, quarantined)
		}
		if err := tx.QueryRow(ctx, q, args...).Scan(&have, &haveQ); err != nil {
			return nil, err
		}
		rec.add("targets", table, len(targets), have, have == len(targets), "rows behind loaded and quarantined documents")
		if tenantStamped[table] {
			rec.add("quarantine_rows", table, len(quarantined), haveQ, haveQ == len(quarantined), "quarantined documents in the quarantine tenant")
		}
	}
	return status, nil
}

// sums is money per key in satang, with the multi-drop tolerance flag.
type sums struct {
	cents map[string]int64
	multi map[string]bool
	count map[string]int
}

func newSums() sums {
	return sums{cents: map[string]int64{}, multi: map[string]bool{}, count: map[string]int{}}
}

func (s sums) add(key string, c int64, multi bool) {
	s.cents[key] += c
	s.count[key]++
	if multi {
		s.multi[key] = true
	}
}

func compareSums(rec *Reconciliation, name string, src, tgt sums) {
	keys := map[string]bool{}
	for k := range src.cents {
		keys[k] = true
	}
	for k := range tgt.cents {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	slices.Sort(sorted)
	for _, k := range sorted {
		diff := src.cents[k] - tgt.cents[k]
		ok := diff == 0 || ((src.multi[k] || tgt.multi[k]) && diff >= -1 && diff <= 1) // 0.005 THB rounds to one satang
		note := fmt.Sprintf("%d / %d rows", src.count[k], tgt.count[k])
		rec.add(name, k, thb(src.cents[k]), thb(tgt.cents[k]), ok, note)
	}
}

func thb(c int64) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s%d.%02d", sign, c/100, c%100)
}

func bkkPeriod(t time.Time) string {
	if t.IsZero() {
		return "none"
	}
	y, m := clock.YearMonth(t)
	return fmt.Sprintf("%04d-%02d", y, m)
}

func docTime(f map[string]any, key string) time.Time {
	if t, ok := toTime(f[key]); ok && present(f[key]) {
		return t
	}
	return time.Time{}
}

func docCents(f map[string]any, key string) (int64, bool) {
	c := &docCtx{f: f}
	n, r := c.moneyCents(key, false)
	return n, r == moneyOK
}

func numericCents(n pgtype.Numeric) int64 {
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return cents(f.Float64)
}

// partyBases are billingDateBasis per customers / subcontractors doc id of the dump.
func partyBases(docs map[string][]dump.Doc) map[string]string {
	out := map[string]string{}
	for _, coll := range []string{"customers", "subcontractors"} {
		for _, d := range docs[coll] {
			b, _ := d.Fields["billingDateBasis"].(string)
			if strings.TrimSpace(b) == "plan" {
				out[d.ID] = "plan"
			} else {
				out[d.ID] = "delivered"
			}
		}
	}
	return out
}

// reconcileTrips: Σ billingEstimateThb per (party, Bangkok period on the party's axis: billingDate for plan basis,
// deliveredTimestamp otherwise) and the unpriced snapshots per party.
func reconcileTrips(ctx context.Context, tx pgx.Tx, docs map[string][]dump.Doc, status docStatus, period string, rec *Reconciliation) error {
	bases := partyBases(docs)
	src, unpricedSrc := newSums(), map[string]int{}
	for _, d := range docs["trip_records"] {
		if s := status.of("trip_records", d.Path); s != string(Loaded) && s != string(Quarantined) {
			continue
		}
		party, _ := d.Fields["billingCustomerId"].(string)
		axis := docTime(d.Fields, "deliveredTimestamp")
		if bases[party] == "plan" {
			axis = docTime(d.Fields, "billingDate")
		}
		p := bkkPeriod(axis)
		if period != "" && p != period {
			continue
		}
		c, priced := docCents(d.Fields, "billingEstimateThb")
		switch {
		case priced:
			multi, _ := d.Fields["billingIsMultiDelivery"].(bool)
			src.add(party+" "+p, c, multi)
		case hasBillingEvidence("trip_records", d.Fields) && hasStamp(d.Fields):
			unpricedSrc[party]++
		}
	}
	rows, err := tx.Query(ctx, `SELECT coalesce(cu.legacy_doc_id, t.legacy_doc_id, ''), coalesce(bp.billing_date_basis, 'delivered'),
		  r.billing_date, r.delivered_at, s.estimate_thb, s.is_multi_delivery
		FROM trip_billing_snapshots s JOIN trip_records r ON r.id = s.trip_id
		LEFT JOIN billing_parties bp ON bp.id = r.billing_party_id
		LEFT JOIN customers cu ON cu.id = bp.customer_id LEFT JOIN tenants t ON t.id = bp.tenant_id`)
	if err != nil {
		return err
	}
	tgt, unpricedTgt := newSums(), map[string]int{}
	for rows.Next() {
		var party, basis string
		var billingDate, deliveredAt *time.Time
		var est pgtype.Numeric
		var multi bool
		if err := rows.Scan(&party, &basis, &billingDate, &deliveredAt, &est, &multi); err != nil {
			return err
		}
		axis := deliveredAt
		if basis == "plan" {
			axis = billingDate
		}
		var at time.Time
		if axis != nil {
			at = *axis
		}
		p := bkkPeriod(at)
		if period != "" && p != period {
			continue
		}
		if !est.Valid {
			unpricedTgt[party]++
			continue
		}
		tgt.add(party+" "+p, numericCents(est), multi)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	compareSums(rec, "trip_money", src, tgt)
	parties := map[string]bool{}
	for k := range unpricedSrc {
		parties[k] = true
	}
	for k := range unpricedTgt {
		parties[k] = true
	}
	for _, k := range sortedKeys(parties) {
		rec.add("trip_unpriced", k, unpricedSrc[k], unpricedTgt[k], unpricedSrc[k] == unpricedTgt[k], "stamped snapshots without a price")
	}
	return nil
}

// hasStamp mirrors tripSnapshot: a snapshot exists for any billing* field other than the party and date stamps.
func hasStamp(f map[string]any) bool {
	for k, v := range f {
		if strings.HasPrefix(k, "billing") && v != nil && k != "billingCustomerId" && k != "billingDate" {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// reconcileStandby: Σ billingEstimateThb per (party, Bangkok month of endedAt).
func reconcileStandby(ctx context.Context, tx pgx.Tx, docs map[string][]dump.Doc, status docStatus, period string, rec *Reconciliation) error {
	src := newSums()
	for _, d := range docs["standby_records"] {
		if s := status.of("standby_records", d.Path); s != string(Loaded) && s != string(Quarantined) {
			continue
		}
		c, priced := docCents(d.Fields, "billingEstimateThb")
		p := bkkPeriod(docTime(d.Fields, "endedAt"))
		if !priced || (period != "" && p != period) {
			continue
		}
		party, _ := d.Fields["billingCustomerId"].(string)
		if party == "" {
			party, _ = d.Fields["customerId"].(string)
		}
		src.add(party+" "+p, c, false)
	}
	rows, err := tx.Query(ctx, `SELECT coalesce(cu.legacy_doc_id, t.legacy_doc_id, ''), s.ended_at, s.billing_estimate_thb
		FROM standby_records s LEFT JOIN billing_parties bp ON bp.id = coalesce(s.billing_party_id, s.customer_party_id)
		LEFT JOIN customers cu ON cu.id = bp.customer_id LEFT JOIN tenants t ON t.id = bp.tenant_id
		WHERE s.billing_estimate_thb IS NOT NULL`)
	if err != nil {
		return err
	}
	tgt := newSums()
	for rows.Next() {
		var party string
		var ended *time.Time
		var est pgtype.Numeric
		if err := rows.Scan(&party, &ended, &est); err != nil {
			return err
		}
		var at time.Time
		if ended != nil {
			at = *ended
		}
		p := bkkPeriod(at)
		if period != "" && p != period {
			continue
		}
		tgt.add(party+" "+p, numericCents(est), false)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	compareSums(rec, "standby_money", src, tgt)
	return nil
}

// reconcileExpenses: Σ amount per (type, status, Bangkok month) of the loaded expenses.
func reconcileExpenses(ctx context.Context, tx pgx.Tx, docs map[string][]dump.Doc, status docStatus, period string, rec *Reconciliation) error {
	src := newSums()
	for _, d := range docs["vehicle_expenses"] {
		if s := status.of("vehicle_expenses", d.Path); s != string(Loaded) && s != string(Quarantined) {
			continue
		}
		c, ok := docCents(d.Fields, "amount")
		p := bkkPeriod(docTime(d.Fields, "date"))
		if !ok || (period != "" && p != period) {
			continue
		}
		typ, _ := d.Fields["type"].(string)
		st, _ := d.Fields["status"].(string)
		if st == "" {
			st = "pending"
		}
		src.add(canon(typ)+" "+canon(st)+" "+p, c, false)
	}
	rows, err := tx.Query(ctx, `SELECT expense_type, status, expense_at, amount_thb FROM vehicle_expenses WHERE legacy_doc_id IS NOT NULL`)
	if err != nil {
		return err
	}
	tgt := newSums()
	for rows.Next() {
		var typ, st string
		var at time.Time
		var amount pgtype.Numeric
		if err := rows.Scan(&typ, &st, &at, &amount); err != nil {
			return err
		}
		p := bkkPeriod(at)
		if period != "" && p != period {
			continue
		}
		tgt.add(typ+" "+st+" "+p, numericCents(amount), false)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	compareSums(rec, "expense_money", src, tgt)
	return nil
}

// reconcileFindings: open findings per reason code, against the previous run. A code the previous run did not
// have blocks the cut-over step (main spec §13.9); the first run only reports.
func reconcileFindings(ctx context.Context, tx pgx.Tx, rec *Reconciliation) error {
	rows, err := tx.Query(ctx, `SELECT reason_code, count(*) FROM etl.quarantine WHERE resolved_at IS NULL GROUP BY reason_code`)
	if err != nil {
		return err
	}
	now := map[string]int{}
	for rows.Next() {
		var code string
		var n int
		if err := rows.Scan(&code, &n); err != nil {
			return err
		}
		now[code] = n
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var prevRaw []byte
	err = tx.QueryRow(ctx, `SELECT report FROM etl.reconciliation_runs WHERE outcome IS NOT NULL ORDER BY started_at DESC, id DESC LIMIT 1`).Scan(&prevRaw)
	var prev map[string]int
	if err == nil {
		var p Reconciliation
		if json.Unmarshal(prevRaw, &p) == nil {
			prev = map[string]int{}
			for _, c := range p.Checks {
				if c.Name == "findings" {
					n, _ := strconv.Atoi(c.Target)
					prev[c.Key] = n
				}
			}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	codes := map[string]bool{}
	for k := range now {
		codes[k] = true
	}
	for _, k := range sortedKeys(codes) {
		ok, note := true, "first reconciliation: reported only"
		if prev != nil {
			pn, seen := prev[k]
			ok = seen
			note = fmt.Sprintf("previous run: %d", pn)
			if !seen {
				note = "a reason code the previous run did not have"
			}
		}
		rec.add("findings", k, "-", now[k], ok, note)
	}
	return nil
}

// WriteMarkdown prints the run as a markdown report.
func (r *Reconciliation) WriteMarkdown(w io.Writer) error {
	var b strings.Builder
	outcome := "match"
	if !r.Match {
		outcome = "MISMATCH"
	}
	fmt.Fprintf(&b, "# ETL reconciliation: %s\n\nDump exported %s", outcome, r.ExportedAt.UTC().Format(time.RFC3339))
	if r.Period != "" {
		fmt.Fprintf(&b, ", money for %s (Asia/Bangkok)", r.Period)
	}
	b.WriteString(".\n\n| check | key | source | target | ok | note |\n|---|---|---|---|---|---|\n")
	for _, c := range r.Checks {
		ok := "yes"
		if !c.OK {
			ok = "**no**"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", c.Name, mdEscape(c.Key), c.Source, c.Target, ok, mdEscape(c.Note))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func mdEscape(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// WriteCSV prints the checks as CSV.
func (r *Reconciliation) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"check", "key", "source", "target", "ok", "note"}); err != nil {
		return err
	}
	for _, c := range r.Checks {
		if err := cw.Write([]string{c.Name, c.Key, c.Source, c.Target, strconv.FormatBool(c.OK), c.Note}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
