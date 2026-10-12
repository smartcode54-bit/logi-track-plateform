package seed

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// The billing statements of the demo profile (T43, issue #63; Appendix D §D.1.2 "Demo billing and finance", §D.2 #6):
// every billing party with priced rows in a closed full calendar month of the window gets the statement POST /v1/billing/statements
// would make (internal/billing CreateStatement): its invoice number from the party's counter, the invoice set of
// InsertStatementLines as lines (never repriced), the exact totals, the issuing company's withholding rate (R18) and
// due date. A month the plan already holds a statement for (the smoke ST1-ST4) is left alone, and so is a month whose
// set holds a row run by a tenant outside the own fleet's reach (the service refuses it, rows_outside_reach: TTP is no
// contractor of the own fleet, R60). The oldest month's first statement is issued twice (the first one cancelled), the
// newest month's last one is still a draft, the rest of the newest month are sent and the older months paid, so every
// status is present and the sent and paid ones lock their period (main spec §6.11).
//
// The plan builds the lines from its own rows (planInvoiceSet, the Go form of the service's invoice-set query). The
// load derives them again from the database in the same transaction, just before the statements are written
// (statementTables), and checks the count and the sum against the service's own query (billingdb.StatementInvoiceSet):
// an insert compares them with the plan's rows and rolls back on a difference, as the engine check does for prices; an
// upsert into a shared database (the staging VM) writes what that database holds, skipping a month it already numbered
// or whose rows cannot be invoiced as planned (Appendix D §D.1.7).

// stmtKey is a statement's party and Bangkok month: the key of billing_counters.
type stmtKey struct {
	party       string
	year, month int
}

// stmtSpec is one generated statement: its number and the lifecycle of the planned status.
type stmtSpec struct {
	key                              stmtKey
	seq                              int
	id, invoice                      string
	status                           string
	generated, sent, paid, cancelled time.Time // zero = not reached
	note                             string
}

// finResult are the generated statements, for the load's re-derivation.
type finResult struct {
	statements []*stmtSpec
	issuer     string    // companies.id of the invoice issuer (the own fleet's owner company)
	by         string    // users.id of the creator (generated_by)
	ns         uuid.UUID // SEED_NAMESPACE of the plan (the line ids)
}

// stmtParty is what a statement snapshots of its billing party (billingdb.ResolveParty).
type stmtParty struct {
	id        string
	code      string
	name      *string
	planBasis bool
	terms     *int // payment_terms_days of a customer party
}

// stmtLine is one row of an invoice set, with the tenant that ran it (the reach check) and when its price was written
// (invariant 5).
type stmtLine struct {
	rowType   string // trip | multidrop_stop | standby
	tripID    string
	stop      int
	standbyID string
	amount    int64 // satang
	tenant    string
	pricedAt  time.Time
}

func sortLines(lines []stmtLine) {
	slices.SortFunc(lines, func(a, b stmtLine) int {
		return cmp.Or(cmp.Compare(a.rowType, b.rowType), cmp.Compare(a.tripID, b.tripID), cmp.Compare(a.stop, b.stop),
			cmp.Compare(a.standbyID, b.standbyID))
	})
}

// satangOf reads a NUMERIC(14,2) money text exactly ("1130.00", "-70.5").
func satangOf(s string) (int64, error) {
	neg := strings.HasPrefix(s, "-")
	whole, frac, _ := strings.Cut(strings.TrimPrefix(s, "-"), ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("money %q has more than two decimals", s)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("money %q: %w", s, err)
	}
	f := int64(0)
	if frac != "" {
		if f, err = strconv.ParseInt((frac + "00")[:2], 10, 64); err != nil {
			return 0, fmt.Errorf("money %q: %w", s, err)
		}
	}
	v := w*100 + f
	if neg {
		v = -v
	}
	return v, nil
}

// baht writes satang as NUMERIC(14,2) text.
func baht(v int64) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}

// bkkMonth is the Bangkok month [from, to) of a statement period (internal/billing bangkokMonth).
func bkkMonth(year, month int) (from, to time.Time) {
	from = time.Date(year, time.Month(month), 1, 0, 0, 0, 0, clock.Bangkok)
	return from, from.AddDate(0, 1, 0)
}

// invoiceNo is {code}-{YYYYMM}-{seq %03d} (internal/billing invoiceNumber).
func invoiceNo(code string, year, month, seq int) string {
	return fmt.Sprintf("%s-%04d%02d-%03d", code, year, month, seq)
}

// --- plan ---------------------------------------------------------------------------------------------------

// planInvoiceSet is the invoice set of a party and month over the plan's rows, the rules of InsertStatementLines with
// every type and category toggle on: delivered trips of the party on its axis (billing_date for a plan-basis party,
// else delivered_at) priced by the billing tenant with an estimate other than 0, a multi-drop trip with a breakdown as
// one line per stop whose final rate is not 0; completed standby records of the party by ended_at priced other than 0
// by the billing tenant (stored, else the tenant of the standby rate it names, else the own fleet).
func planInvoiceSet(p *Plan, party stmtParty, own string, from, to time.Time) ([]stmtLine, error) {
	snaps := map[string]Row{}
	for _, s := range p.Tables["trip_billing_snapshots"] {
		snaps[s.Str("trip_id")] = s
	}
	stops := map[string][]Row{}
	for _, b := range p.Tables["trip_billing_stop_breakdown"] {
		stops[b.Str("trip_id")] = append(stops[b.Str("trip_id")], b)
	}
	rateTenant := map[string]string{}
	for _, r := range p.Tables["standby_rate_entries"] {
		rateTenant[r.Str("id")] = r.Str("tenant_id")
	}
	in := func(v string) (time.Time, bool, error) {
		if v == "" {
			return time.Time{}, false, nil
		}
		t, err := time.Parse(time.RFC3339, v)
		return t, err == nil && !t.Before(from) && t.Before(to), err
	}
	var out []stmtLine
	for _, tr := range p.Tables["trip_records"] {
		if tr.Str("status") != "delivered" || tr.Str("billing_party_id") != party.id {
			continue
		}
		s := snaps[tr.Str("id")]
		if s == nil || s.Str("tenant_id") != own || s.Str("estimate_thb") == "" {
			continue
		}
		est, err := satangOf(s.Str("estimate_thb"))
		if err != nil || est == 0 {
			if err != nil {
				return nil, err
			}
			continue
		}
		axis := tr.Str("delivered_at")
		if party.planBasis {
			axis = tr.Str("billing_date")
		}
		if _, ok, err := in(axis); err != nil || !ok {
			if err != nil {
				return nil, err
			}
			continue
		}
		priced, err := time.Parse(time.RFC3339, s.Str("updated_at"))
		if err != nil {
			return nil, fmt.Errorf("snapshot of %s: updated_at: %w", tr.Str("trip_no"), err)
		}
		multi := s["is_multi_delivery"] == true
		if multi && len(stops[tr.Str("id")]) > 0 {
			for _, b := range stops[tr.Str("id")] {
				v, err := satangOf(b.Str("final_rate_thb"))
				if err != nil {
					return nil, err
				}
				n, _ := intOf(b["stop_index"])
				if v != 0 {
					out = append(out, stmtLine{rowType: "multidrop_stop", tripID: tr.Str("id"), stop: n, amount: v,
						tenant: tr.Str("tenant_id"), pricedAt: priced})
				}
			}
			continue
		}
		out = append(out, stmtLine{rowType: "trip", tripID: tr.Str("id"), amount: est, tenant: tr.Str("tenant_id"), pricedAt: priced})
	}
	for _, sb := range p.Tables["standby_records"] {
		if sb.Str("status") != "completed" || sb.Str("billing_party_id") != party.id || sb.Str("billing_estimate_thb") == "" {
			continue
		}
		if _, ok, err := in(sb.Str("ended_at")); err != nil || !ok {
			if err != nil {
				return nil, err
			}
			continue
		}
		carrier := sb.Str("billing_tenant_id")
		if carrier == "" {
			carrier = rateTenant[sb.Str("billing_rate_entry_id")]
		}
		if carrier == "" {
			carrier = own
		}
		v, err := satangOf(sb.Str("billing_estimate_thb"))
		if err != nil {
			return nil, err
		}
		if carrier != own || v == 0 {
			continue
		}
		priced, err := time.Parse(time.RFC3339, sb.Str("billing_computed_at"))
		if err != nil {
			return nil, fmt.Errorf("standby %s: billing_computed_at: %w", sb.Str("id"), err)
		}
		out = append(out, stmtLine{rowType: "standby", standbyID: sb.Str("id"), amount: v, tenant: sb.Str("tenant_id"), pricedAt: priced})
	}
	sortLines(out)
	return out, nil
}

// rowsByID indexes rows by id.
func rowsByID(rows []Row) map[string]Row {
	out := make(map[string]Row, len(rows))
	for _, r := range rows {
		out[r.Str("id")] = r
	}
	return out
}

// reachOf is the tenants the own fleet's staff reach (Appendix C, R60): the own fleet and the carriers working for it.
func reachOf(tenants []Row, own string) map[string]bool {
	out := map[string]bool{own: true}
	for _, t := range tenants {
		if t.Str("contractor_tenant_id") == own {
			out[t.Str("id")] = true
		}
	}
	return out
}

// planParties are the billing parties of the plan as ResolveParty reads them: the customer's code and name (else the
// carrier's), the customer's payment terms and the billing date basis.
func planParties(p *Plan) []stmtParty {
	customers, tenants := rowsByID(p.Tables["customers"]), rowsByID(p.Tables["tenants"])
	var out []stmtParty
	for _, bp := range p.Tables["billing_parties"] {
		sp := stmtParty{id: bp.Str("id"), code: bp.Str("id"), planBasis: bp.Str("billing_date_basis") == "plan"}
		if c := customers[bp.Str("customer_id")]; c != nil {
			if code := strings.TrimSpace(c.Str("code")); code != "" {
				sp.code = code
			}
			if name := c.Str("name"); name != "" {
				sp.name = &name
			}
			if n, ok := intOf(c["payment_terms_days"]); ok {
				sp.terms = &n
			}
		} else if t := tenants[bp.Str("tenant_id")]; t != nil {
			if code := strings.TrimSpace(t.Str("code")); code != "" {
				sp.code = code
			}
			if name := t.Str("name_th"); name != "" {
				sp.name = &name
			}
		}
		out = append(out, sp)
	}
	slices.SortStableFunc(out, func(a, b stmtParty) int { return cmp.Or(cmp.Compare(a.code, b.code), cmp.Compare(a.id, b.id)) })
	return out
}

// withholdingRate is the issuing company's withholding rate as the statement stores it: the percent as a fraction,
// numeric(5,4) text (InsertStatement, R18).
func withholdingRate(percent string) (string, error) {
	v, err := strconv.ParseFloat(percent, 64)
	if err != nil {
		return "", fmt.Errorf("withholding_tax_rate %q: %w", percent, err)
	}
	return strconv.FormatFloat(math.Round(v*100)/1e4, 'f', 4, 64), nil
}

// addStatements appends the statements of the closed full calendar months of the window (demo and load), their lines
// and counters, once every price is in (after priceOperations). A month the window covers in part is left out: whether
// a party has priced rows in its few days depends on the drawn routes, and so would the statements' count.
func addStatements(p *Plan, o Options) error {
	own := p.Symbols.MustID(ownFleetSymbol).String()
	var issuer, percent string
	for _, c := range p.Tables["companies"] {
		if c.Str("tenant_id") == own && c.Str("company_type") == "owner" {
			issuer, percent = c.Str("id"), c.Str("withholding_tax_rate")
		}
	}
	if issuer == "" {
		return errors.New("seed: statements: the own fleet has no owner company (the invoice issuer)")
	}
	rate, err := withholdingRate(percent)
	if err != nil {
		return fmt.Errorf("seed: statements: %w", err)
	}
	fin := &finResult{issuer: issuer, by: p.Symbols.MustID("U_WRT_MGR").String(), ns: o.Namespace}
	anchor := time.Date(o.Anchor.Year(), o.Anchor.Month(), o.Anchor.Day(), 0, 0, 0, 0, clock.Bangkok)
	start := anchor.AddDate(0, 0, -(opsDays - 1))
	reach := reachOf(p.Tables["tenants"], own)
	taken := map[stmtKey]bool{}
	for _, b := range p.Tables["billing_statements"] {
		y, _ := intOf(b["period_year"])
		m, _ := intOf(b["period_month"])
		taken[stmtKey{b.Str("billing_party_id"), y, m}] = true
	}
	type cand struct {
		party stmtParty
		lines []stmtLine
	}
	type month struct {
		year, month int
		cands       []cand
	}
	var months []month
	parties := planParties(p)
	if p.labels == nil {
		p.labels = map[string]string{}
	}
	for m := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, clock.Bangkok); ; m = m.AddDate(0, 1, 0) {
		next := m.AddDate(0, 1, 0)
		if next.AddDate(0, 0, 3).After(anchor) { // invoiced on the 2nd, sent on the 3rd: the month is closed by the anchor
			break
		}
		if m.Before(start) {
			continue // a partial first month: whether a party has rows in its few days is the stream's draw
		}
		mo := month{year: m.Year(), month: int(m.Month())}
		for _, pt := range parties {
			if taken[stmtKey{pt.id, mo.year, mo.month}] {
				continue
			}
			lines, err := planInvoiceSet(p, pt, own, m, next)
			if err != nil {
				return fmt.Errorf("seed: statements: %s %d-%02d: %w", pt.code, mo.year, mo.month, err)
			}
			if len(lines) == 0 || slices.ContainsFunc(lines, func(l stmtLine) bool { return !reach[l.tenant] }) {
				continue // nothing to invoice, or rows_outside_reach
			}
			mo.cands = append(mo.cands, cand{pt, lines})
		}
		if len(mo.cands) > 0 {
			months = append(months, mo)
		}
	}
	for mi, mo := range months {
		first := time.Date(mo.year, time.Month(mo.month), 1, 0, 0, 0, 0, clock.Bangkok).AddDate(0, 1, 0)
		for pi, c := range mo.cands {
			step := time.Duration(pi) * 15 * time.Minute
			key := stmtKey{c.party.id, mo.year, mo.month}
			var specs []*stmtSpec
			spec := func(seq int, status string, generated time.Time) *stmtSpec {
				no := invoiceNo(c.party.code, mo.year, mo.month, seq)
				s := &stmtSpec{key: key, seq: seq, invoice: no, id: ID(o.Namespace, "billing_statements", no).String(),
					status: status, generated: generated}
				switch status {
				case "cancelled":
					s.cancelled, s.note = first.AddDate(0, 0, 1).Add(15*time.Hour+step), "ยอดไม่ครบ ยกเลิกเพื่อออกใหม่"
				case "sent", "paid":
					s.sent = first.AddDate(0, 0, 2).Add(9*time.Hour + step)
					if status == "paid" {
						s.paid = first.AddDate(0, 0, 24).Add(14*time.Hour + step)
					}
				}
				specs = append(specs, s)
				return s
			}
			seq, generated := 1, first.AddDate(0, 0, 1).Add(10*time.Hour+step)
			if mi == 0 && pi == 0 { // issued twice: the first cancelled the same afternoon, then issued again
				spec(1, "cancelled", generated)
				seq, generated = 2, first.AddDate(0, 0, 1).Add(16*time.Hour+step)
			}
			status := "paid"
			if mi == len(months)-1 {
				status = "sent"
				if pi == len(mo.cands)-1 && len(mo.cands) > 1 {
					status = "draft"
				}
			}
			if s := spec(seq, status, generated); seq == 2 {
				s.note = "ออกแทน " + specs[0].invoice
			}
			for _, s := range specs {
				if !s.sent.IsZero() && slices.ContainsFunc(c.lines, func(l stmtLine) bool { return l.pricedAt.After(s.sent) }) {
					return fmt.Errorf("seed: statement %s: a row is priced after its sent_at (invariant 5)", s.invoice)
				}
				st, lines := statementRows(o.Namespace, s, c.party, rate, c.lines, own, fin.issuer, fin.by)
				p.Tables["billing_statements"] = append(p.Tables["billing_statements"], st)
				p.Tables["billing_statement_lines"] = append(p.Tables["billing_statement_lines"], lines...)
				fin.statements = append(fin.statements, s)
				p.labels[s.id] = s.invoice
			}
			p.Tables["billing_counters"] = append(p.Tables["billing_counters"], counterRow(key, c.party.code, own, specs))
		}
	}
	p.fin = fin
	return nil
}

// statementRows are a statement and its lines as CreateStatement leaves them: the totals are exact sums of the lines,
// the withholding Round2(total x rate) of the stored rate, net = total - withholding (UpdateStatementTotals).
func statementRows(ns uuid.UUID, s *stmtSpec, party stmtParty, rate string, lines []stmtLine, own, issuer, by string) (Row, []Row) {
	var total, tripSum, standbySum, multiSum int64
	var trips, standbys, multis int
	out := make([]Row, 0, len(lines))
	for _, l := range lines {
		total += l.amount
		ref := l.tripID
		switch l.rowType {
		case "trip":
			trips++
			tripSum += l.amount
		case "standby":
			standbys++
			standbySum += l.amount
			ref = l.standbyID
		case "multidrop_stop":
			multis++
			multiSum += l.amount
			ref += ":" + strconv.Itoa(l.stop)
		}
		r := Row{"id": ID(ns, "billing_statement_lines", s.invoice+":"+l.rowType+":"+ref).String(), "statement_id": s.id,
			"row_type": l.rowType, "amount_thb": baht(l.amount), "created_at": ts(s.generated)}
		if l.rowType == "standby" {
			r["standby_id"] = l.standbyID
		} else {
			r["trip_id"] = l.tripID
		}
		if l.rowType == "multidrop_stop" {
			r["stop_index"] = intNum(l.stop)
		}
		out = append(out, r)
	}
	fraction, _ := strconv.ParseFloat(rate, 64)
	wht := int64(math.Round(compute.WithholdingTHB(float64(total)/100, fraction) * 100))
	updated := s.generated
	r := Row{"id": s.id, "tenant_id": own, "tenant_source": "form", "billing_party_id": party.id, "issuer_company_id": issuer,
		"invoice_number": s.invoice, "customer_code_snapshot": party.code, "period_year": intNum(s.key.year),
		"period_month": intNum(s.key.month), "total_amount": baht(total), "withholding_tax_rate": rate,
		"withholding_tax": baht(wht), "net_amount": baht(total - wht), "trip_count": intNum(len(lines)),
		"trip_only_count": intNum(trips), "trip_subtotal": baht(tripSum), "standby_count": intNum(standbys),
		"standby_subtotal": baht(standbySum), "multi_drop_count": intNum(multis), "multi_drop_subtotal": baht(multiSum),
		"status": s.status, "generated_at": ts(s.generated), "generated_by": by, "created_at": ts(s.generated)}
	if party.name != nil {
		r["customer_name_snapshot"] = *party.name
	}
	if party.terms != nil {
		r["due_date"] = ts(s.generated.AddDate(0, 0, *party.terms))
	}
	for col, at := range map[string]time.Time{"sent_at": s.sent, "paid_at": s.paid, "cancelled_at": s.cancelled} {
		if !at.IsZero() {
			r[col] = ts(at)
			if at.After(updated) {
				updated = at
			}
		}
	}
	if s.note != "" {
		r["note"] = s.note
	}
	r["updated_at"] = ts(updated)
	return r, out
}

// counterRow is the billing_counters row of a party and month after its statements (next_invoice_seq: last_seq =
// the statements numbered, invariant 5).
func counterRow(k stmtKey, code, own string, specs []*stmtSpec) Row {
	last := specs[len(specs)-1].generated
	return Row{"billing_party_id": k.party, "period_year": intNum(k.year), "period_month": intNum(k.month), "tenant_id": own,
		"tenant_source": "form", "last_seq": intNum(len(specs)), "customer_code": code, "updated_at": ts(last)}
}

// --- load -----------------------------------------------------------------------------------------------------

// dbInvoiceSet is planInvoiceSet over the database (the WHERE clauses of InsertStatementLines with every toggle on),
// returning each row with the tenant that ran it and when its price was written.
const dbInvoiceSet = `SELECT x.row_type, coalesce(x.trip_id::text, ''), coalesce(x.stop_index, 0), coalesce(x.standby_id::text, ''),
       x.amount::text, x.tenant_id::text, x.priced_at
FROM (
  SELECT 'trip'::text AS row_type, tr.id AS trip_id, NULL::int AS stop_index, NULL::uuid AS standby_id,
         s.estimate_thb AS amount, tr.tenant_id, s.updated_at AS priced_at
  FROM trip_records tr
  JOIN trip_billing_snapshots s ON s.trip_id = tr.id
  WHERE tr.status = 'delivered' AND tr.billing_party_id = $1::uuid
    AND s.tenant_id = $2::uuid
    AND s.estimate_thb IS NOT NULL AND s.estimate_thb <> 0
    AND CASE WHEN $3::boolean THEN tr.billing_date ELSE tr.delivered_at END >= $4::timestamptz
    AND CASE WHEN $3::boolean THEN tr.billing_date ELSE tr.delivered_at END < $5::timestamptz
    AND NOT (s.is_multi_delivery AND EXISTS (SELECT 1 FROM trip_billing_stop_breakdown b WHERE b.trip_id = tr.id))
  UNION ALL
  SELECT 'multidrop_stop'::text, tr.id, b.stop_index, NULL::uuid, b.final_rate_thb, tr.tenant_id, s.updated_at
  FROM trip_records tr
  JOIN trip_billing_snapshots s ON s.trip_id = tr.id
  JOIN trip_billing_stop_breakdown b ON b.trip_id = tr.id
  WHERE tr.status = 'delivered' AND tr.billing_party_id = $1::uuid
    AND s.tenant_id = $2::uuid
    AND s.estimate_thb IS NOT NULL AND s.estimate_thb <> 0 AND s.is_multi_delivery AND b.final_rate_thb <> 0
    AND CASE WHEN $3::boolean THEN tr.billing_date ELSE tr.delivered_at END >= $4::timestamptz
    AND CASE WHEN $3::boolean THEN tr.billing_date ELSE tr.delivered_at END < $5::timestamptz
  UNION ALL
  SELECT 'standby'::text, NULL::uuid, NULL::int, sr.id, sr.billing_estimate_thb, sr.tenant_id, sr.billing_computed_at
  FROM standby_records sr
  WHERE sr.status = 'completed' AND sr.billing_party_id = $1::uuid
    AND sr.ended_at >= $4::timestamptz AND sr.ended_at < $5::timestamptz
    AND sr.billing_estimate_thb IS NOT NULL AND sr.billing_estimate_thb <> 0
    AND coalesce(sr.billing_tenant_id,
                 (SELECT r.tenant_id FROM standby_rate_entries r WHERE r.id = sr.billing_rate_entry_id),
                 $2::uuid) = $2::uuid
) x`

// dbLines reads the invoice set of a party and month from the database and checks its count and sum against the
// service's own query (billingdb.StatementInvoiceSet, run here without RLS as CreateStatement first runs it), so the
// seed's copy of the WHERE clauses cannot drift from internal/billing's unnoticed.
func dbLines(ctx context.Context, tx pgx.Tx, party stmtParty, own string, k stmtKey) ([]stmtLine, error) {
	from, to := bkkMonth(k.year, k.month)
	rows, err := tx.Query(ctx, dbInvoiceSet, party.id, own, party.planBasis, from, to)
	if err != nil {
		return nil, err
	}
	var out []stmtLine
	var sum int64
	if err := scanAll(rows, func(r pgx.Rows) error {
		var l stmtLine
		var amount string
		var priced *time.Time
		if err := r.Scan(&l.rowType, &l.tripID, &l.stop, &l.standbyID, &amount, &l.tenant, &priced); err != nil {
			return err
		}
		v, err := satangOf(amount)
		if err != nil {
			return err
		}
		l.amount, sum = v, sum+v
		if priced != nil {
			l.pricedAt = *priced
		}
		out = append(out, l)
		return nil
	}); err != nil {
		return nil, err
	}
	pid, err := uuid.Parse(party.id)
	if err != nil {
		return nil, err
	}
	whole, err := billingdb.New(tx).StatementInvoiceSet(ctx, billingdb.StatementInvoiceSetParams{IncludeTrips: true,
		PartyID: pid, TenantID: uuid.MustParse(own), PlanBasis: party.planBasis, PeriodFrom: from, PeriodTo: to,
		Categories: []string{"PRIMARY", "SUPPLEMENTARY", "unverified"}, IncludeMultidrop: true, IncludeStandby: true, OwnFleet: true})
	if err != nil {
		return nil, err
	}
	if int(whole.LineCount) != len(out) || int64(math.Round(whole.Total*100)) != sum {
		return nil, fmt.Errorf("seed: the invoice set of %s %d-%02d is %d rows / %s here, %d rows / %.2f in internal/billing's "+
			"StatementInvoiceSet: keep dbInvoiceSet and planInvoiceSet with the service's WHERE clauses", party.code, k.year, k.month,
			len(out), baht(sum), whole.LineCount, whole.Total)
	}
	sortLines(out)
	return out, nil
}

// staleRows runs the engine check (CheckEngine) over every delivered trip of a party's month on its billing axis and
// every completed standby record of it by ended_at, priced or not, and returns the differences: what a statement
// written now would lock as the database stores it although the database's pricing inputs price it otherwise.
func staleRows(ctx context.Context, tx pgx.Tx, party stmtParty, k stmtKey) ([]Mismatch, error) {
	from, to := bkkMonth(k.year, k.month)
	ids := func(sql string, args ...any) ([]uuid.UUID, error) {
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return nil, err
		}
		out := []uuid.UUID{} // never nil: nil selects every row in CheckEngine
		err = scanAll(rows, func(r pgx.Rows) error {
			var id uuid.UUID
			if err := r.Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
			return nil
		})
		return out, err
	}
	trips, err := ids(`SELECT tr.id FROM trip_records tr JOIN tasks k ON k.id = tr.task_id
		WHERE tr.status = 'delivered'
		  AND coalesce(k.billing_party_id, k.source_linked_party_id, k.destination_linked_party_id) = $1::uuid
		  AND CASE WHEN $2::boolean THEN coalesce(tr.billing_date, k.plan_at) ELSE tr.delivered_at END >= $3::timestamptz
		  AND CASE WHEN $2::boolean THEN coalesce(tr.billing_date, k.plan_at) ELSE tr.delivered_at END < $4::timestamptz`,
		party.id, party.planBasis, from, to)
	if err != nil {
		return nil, fmt.Errorf("seed: statements: the trips of %s %d-%02d: %w", party.code, k.year, k.month, err)
	}
	standby, err := ids(`SELECT sr.id FROM standby_records sr LEFT JOIN tasks k ON k.id = sr.task_id
		WHERE sr.status = 'completed'
		  AND coalesce(sr.customer_party_id, k.source_linked_party_id, k.destination_linked_party_id) = $1::uuid
		  AND sr.ended_at >= $2::timestamptz AND sr.ended_at < $3::timestamptz`, party.id, from, to)
	if err != nil {
		return nil, fmt.Errorf("seed: statements: the standby records of %s %d-%02d: %w", party.code, k.year, k.month, err)
	}
	return CheckEngine(ctx, tx, trips, standby)
}

// dbParty resolves a party as CreateStatement does (billingdb.ResolveParty).
func dbParty(ctx context.Context, tx pgx.Tx, id string) (stmtParty, error) {
	pid, err := uuid.Parse(id)
	if err != nil {
		return stmtParty{}, err
	}
	pr, err := billingdb.New(tx).ResolveParty(ctx, pid)
	if err != nil {
		return stmtParty{}, fmt.Errorf("billing party %s: %w", id, err)
	}
	sp := stmtParty{id: id, code: id, planBasis: pr.BillingDateBasis == "plan"}
	switch {
	case pr.CustomerCode != nil && strings.TrimSpace(*pr.CustomerCode) != "":
		sp.code = strings.TrimSpace(*pr.CustomerCode)
	case pr.TenantCode != nil && strings.TrimSpace(*pr.TenantCode) != "":
		sp.code = strings.TrimSpace(*pr.TenantCode)
	}
	if pr.CustomerName != nil {
		sp.name = pr.CustomerName
	} else if pr.TenantName != nil {
		sp.name = pr.TenantName
	}
	if pr.PaymentTermsDays != nil {
		n := int(*pr.PaymentTermsDays)
		sp.terms = &n
	}
	return sp, nil
}

// statementTables derives the generated statements from the database, in the load transaction once every priced row is
// written (just before billing_statements loads). Insert mode: the derived rows must equal the plan's (else the load
// rolls back: the plan's copy of the invoice set or of the totals drifted). Upsert mode: the derived rows replace the
// plan's, and a party and month is skipped, with a note, when the database already numbered it (a statement or a
// counter), when its set is empty or holds a row outside the own fleet's reach, when a row of a sent or paid statement
// was priced after its planned sent_at (invariant 5), or when a sent or paid statement would lock a row the database's
// pricing inputs price otherwise than it stores (staleRows). tables is never modified: the result is a copy.
func statementTables(ctx context.Context, tx pgx.Tx, p *Plan, tables map[string][]Row, mode Mode) (map[string][]Row, []string, error) {
	fin := p.fin
	if fin == nil || len(fin.statements) == 0 {
		return tables, nil, nil
	}
	own := p.Symbols.MustID(ownFleetSymbol).String()
	var rate string
	if err := tx.QueryRow(ctx, `SELECT (withholding_tax_rate / 100)::numeric(5,4)::text FROM companies WHERE id = $1::uuid`,
		fin.issuer).Scan(&rate); err != nil {
		return nil, nil, fmt.Errorf("seed: statements: the issuing company %s: %w", fin.issuer, err)
	}
	reach := map[string]bool{}
	rows, err := tx.Query(ctx, `SELECT id::text FROM tenants WHERE id = $1::uuid OR contractor_tenant_id = $1::uuid`, own)
	if err != nil {
		return nil, nil, err
	}
	if err := scanAll(rows, func(r pgx.Rows) error {
		var id string
		err := r.Scan(&id)
		reach[id] = true
		return err
	}); err != nil {
		return nil, nil, err
	}
	var keys []stmtKey
	byKey := map[stmtKey][]*stmtSpec{}
	for _, s := range fin.statements {
		if byKey[s.key] == nil {
			keys = append(keys, s.key)
		}
		byKey[s.key] = append(byKey[s.key], s)
	}
	var stmts, lines, counters []Row
	var notes []string
	held := 0
	for _, k := range keys {
		specs := byKey[k]
		label := fmt.Sprintf("%s (%d-%02d)", specs[0].invoice[:strings.LastIndex(specs[0].invoice, "-")], k.year, k.month)
		if mode == ModeUpsert {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM billing_statements WHERE billing_party_id = $1::uuid
				AND period_year = $2 AND period_month = $3) OR EXISTS (SELECT 1 FROM billing_counters WHERE billing_party_id = $1::uuid
				AND period_year = $2 AND period_month = $3)`, k.party, k.year, k.month).Scan(&exists); err != nil {
				return nil, nil, err
			}
			if exists {
				held++
				continue
			}
		}
		party, err := dbParty(ctx, tx, k.party)
		if err != nil {
			return nil, nil, fmt.Errorf("seed: statements: %w", err)
		}
		set, err := dbLines(ctx, tx, party, own, k)
		if err != nil {
			return nil, nil, err
		}
		problem := ""
		switch {
		case len(set) == 0:
			problem = "nothing priced to invoice (no_billable_rows)"
		case slices.ContainsFunc(set, func(l stmtLine) bool { return !reach[l.tenant] }):
			problem = "a row runs in a tenant outside the own fleet's reach (rows_outside_reach)"
		}
		locks := false
		for _, s := range specs {
			locks = locks || !s.sent.IsZero()
			if problem == "" && !s.sent.IsZero() && slices.ContainsFunc(set, func(l stmtLine) bool { return l.pricedAt.After(s.sent) }) {
				problem = "a row was priced after the planned sent_at " + ts(s.sent.In(clock.Bangkok)) + " (invariant 5)"
			}
		}
		if problem == "" && locks && mode == ModeUpsert {
			// A sent or paid statement locks its period (main spec §6.11): a row the database's pricing inputs now price
			// differently (a stored no_rate a later card prices, a price a later round moves) could then never be repaired
			// by a forced recompute, and the backfill's first price of such a no_rate row would land after sent_at
			// (invariant 5). Such a month waits for the backfill.
			stale, err := staleRows(ctx, tx, party, k)
			if err != nil {
				return nil, nil, err
			}
			if len(stale) > 0 {
				problem = fmt.Sprintf("%d value(s) of the period's rows that the database's pricing inputs price differently "+
					"(%s); reprice them (the trip billing backfill) and reseed, so a sent or paid statement does not lock them as "+
					"stored", len(stale), stale[0])
			}
		}
		if problem != "" {
			if mode == ModeInsert {
				return nil, nil, fmt.Errorf("seed: statement %s: %s; the plan's invoice set differs from the database's", label, problem)
			}
			notes = append(notes, "statement "+label+" not written: "+problem)
			continue
		}
		for _, s := range specs {
			st, ls := statementRows(fin.ns, s, party, rate, set, own, fin.issuer, fin.by)
			stmts, lines = append(stmts, st), append(lines, ls...)
		}
		counters = append(counters, counterRow(k, party.code, own, specs))
	}
	if held > 0 {
		notes = append(notes, fmt.Sprintf("%d generated statement month(s) already numbered in the database: left as they are", held))
	}
	planned := generatedStatementRows(fin, tables)
	if mode == ModeInsert {
		for table, derived := range map[string][]Row{"billing_statements": stmts, "billing_statement_lines": lines, "billing_counters": counters} {
			if want := planned[table]; !reflect.DeepEqual(derived, want) {
				return nil, nil, fmt.Errorf("seed: the generated %s differ from what the database's invoice sets give: %s",
					table, firstRowDiff(want, derived))
			}
		}
		return tables, notes, nil
	}
	out := maps.Clone(tables)
	for table, derived := range map[string][]Row{"billing_statements": stmts, "billing_statement_lines": lines, "billing_counters": counters} {
		out[table] = append(slices.Clone(planned["rest:"+table]), derived...)
	}
	return out, notes, nil
}

// generatedStatementRows splits the statement tables into the generated rows (by table) and the rest ("rest:" + table).
func generatedStatementRows(fin *finResult, tables map[string][]Row) map[string][]Row {
	ids, keys := map[string]bool{}, map[stmtKey]bool{}
	for _, s := range fin.statements {
		ids[s.id], keys[s.key] = true, true
	}
	out := map[string][]Row{}
	for _, table := range []string{"billing_statements", "billing_statement_lines", "billing_counters"} {
		for _, r := range tables[table] {
			y, _ := intOf(r["period_year"])
			m, _ := intOf(r["period_month"])
			gen := false
			switch table {
			case "billing_statements":
				gen = ids[r.Str("id")]
			case "billing_statement_lines":
				gen = ids[r.Str("statement_id")]
			case "billing_counters":
				gen = keys[stmtKey{r.Str("billing_party_id"), y, m}]
			}
			if gen {
				out[table] = append(out[table], r)
			} else {
				out["rest:"+table] = append(out["rest:"+table], r)
			}
		}
	}
	return out
}

// firstRowDiff names the first row and column two row lists differ in (an engine-check style message, values only of
// the differing column).
func firstRowDiff(want, got []Row) string {
	for i := range max(len(want), len(got)) {
		switch {
		case i >= len(want):
			return fmt.Sprintf("the database gives an extra row %s", describe(got[i]))
		case i >= len(got):
			return fmt.Sprintf("the plan's row %s is missing", describe(want[i]))
		}
		cols := slices.Sorted(maps.Keys(want[i]))
		for c := range got[i] {
			if _, ok := want[i][c]; !ok {
				cols = append(cols, c)
			}
		}
		for _, c := range cols {
			if !reflect.DeepEqual(want[i][c], got[i][c]) {
				return fmt.Sprintf("row %d (%s): %s plan %v, database %v", i+1, describe(want[i]), c, want[i][c], got[i][c])
			}
		}
	}
	return "rows in another order"
}
