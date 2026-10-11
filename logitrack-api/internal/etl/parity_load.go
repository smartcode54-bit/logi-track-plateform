package etl

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// FinanceWindow is the parity window of `reconcile --finance`: closed Bangkok months, oldest first (main spec §6.17:
// the last 3 closed months by default).
type FinanceWindow struct {
	Months []string `json:"months"`
	from   time.Time
	to     time.Time
}

// ErrBadWindow is a parity window that cannot be built (an exit-2 configuration error of `reconcile --finance`).
var ErrBadWindow = errors.New("etl: bad finance window")

// newFinanceWindow is the months closed months ending at through (YYYY-MM; "" = the Bangkok month before now).
func newFinanceWindow(through string, months int, now time.Time) (FinanceWindow, error) {
	if months < 1 || months > 24 {
		return FinanceWindow{}, fmt.Errorf("%w: --months must be 1-24", ErrBadWindow)
	}
	y, m := clock.YearMonth(now)
	cur := y*12 + m - 1
	last := cur - 1
	if through != "" {
		t, err := time.Parse("2006-01", through)
		if err != nil {
			return FinanceWindow{}, fmt.Errorf("%w: --through must be YYYY-MM", ErrBadWindow)
		}
		last = t.Year()*12 + int(t.Month()) - 1
		if last >= cur {
			return FinanceWindow{}, fmt.Errorf("%w: --through=%s is not a closed month (the Bangkok month is %04d-%02d)", ErrBadWindow, through, y, m)
		}
	}
	w := FinanceWindow{}
	for k := last - months + 1; k <= last; k++ {
		w.Months = append(w.Months, fmt.Sprintf("%04d-%02d", k/12, k%12+1))
	}
	first := last - months + 1
	w.from = time.Date(first/12, time.Month(first%12+1), 1, 0, 0, 0, 0, clock.Bangkok).UTC()
	w.to = time.Date((last+1)/12, time.Month((last+1)%12+1), 1, 0, 0, 0, 0, clock.Bangkok).UTC()
	return w, nil
}

func (w FinanceWindow) has(t time.Time) bool {
	return !t.IsZero() && !t.Before(w.from) && t.Before(w.to)
}

// ParityRow is one recomputed record of the window: a line of parity-{ts}.csv.
type ParityRow struct {
	Kind       string // trip | standby
	Key        string // trip_no (legacy doc id when it differs) | the standby record's legacy doc id
	Party      string // customers / subcontractors doc id of the billing party
	Period     string // the Bangkok month on the party's axis
	Stored     string
	Go         string
	Outcome    string // exact | allowed | mismatch
	Case       string // the allow-list case or the mismatch reason
	Note       string
	Divergence string // "1" or "9" when the record is one a preserved divergence decides
}

// WriteParityCSV prints every recomputed record of the run.
func (r *Reconciliation) WriteParityCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"kind", "key", "party", "period", "stored", "go", "outcome", "case", "divergence", "note"}); err != nil {
		return err
	}
	if r.Finance != nil {
		for _, p := range r.Finance.Parity {
			if err := cw.Write([]string{p.Kind, p.Key, p.Party, p.Period, p.Stored, p.Go, p.Outcome, p.Case, p.Divergence, p.Note}); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	return cw.Error()
}

// loadPeriodLocks reads every sent or paid statement (the period locks of §6.11), without a lock.
func loadPeriodLocks(ctx context.Context, tx pgx.Tx) (compute.PeriodLocks, error) {
	rows, err := tx.Query(ctx, `SELECT billing_party_id::text, period_year, period_month, invoice_number, status, generated_at
		FROM billing_statements WHERE status IN ('sent', 'paid')`)
	if err != nil {
		return compute.PeriodLocks{}, err
	}
	var out []compute.LockedPeriod
	for rows.Next() {
		var l compute.LockedPeriod
		if err := rows.Scan(&l.PartyID, &l.Year, &l.Month, &l.InvoiceNumber, &l.Status, &l.GeneratedAt); err != nil {
			return compute.PeriodLocks{}, err
		}
		out = append(out, l)
	}
	return compute.NewPeriodLocks(out), rows.Err()
}

// partyRef is a billing party as the parity reads it.
type partyRef struct {
	basis compute.BillingDateBasis
	label string
}

func loadParties(ctx context.Context, tx pgx.Tx) (map[string]partyRef, error) {
	rows, err := tx.Query(ctx, `SELECT bp.id::text, bp.billing_date_basis,
		  coalesce(cu.legacy_doc_id, t.legacy_doc_id, cu.code::text, t.code::text, bp.id::text)
		FROM billing_parties bp LEFT JOIN customers cu ON cu.id = bp.customer_id LEFT JOIN tenants t ON t.id = bp.tenant_id`)
	if err != nil {
		return nil, err
	}
	out := map[string]partyRef{}
	for rows.Next() {
		var id, basis, label string
		if err := rows.Scan(&id, &basis, &label); err != nil {
			return nil, err
		}
		out[id] = partyRef{basis: compute.BillingDateBasis(basis), label: label}
	}
	return out, rows.Err()
}

// loadParityEngine reads the hub maps and every rate table of the billing carrier (the own fleet, R61), voided rows
// included, without a lock.
func (e *Engine) loadParityEngine(ctx context.Context, tx pgx.Tx, parties map[string]partyRef) (*parityEngine, error) {
	pe := &parityEngine{tables: map[string]parityTables{}}
	for id, p := range parties {
		pe.tables[id] = parityTables{basis: p.basis}
	}
	upd := func(party string, f func(*parityTables)) {
		t := pe.tables[party]
		f(&t)
		pe.tables[party] = t
	}
	// Hubs in load order (first writer wins, fn:tripBillingOnDelivered.ts:124-141), as the billing dry run reads them.
	hubRows, err := tx.Query(ctx, `SELECT h.id::text, h.source_id::text, h.name_th, coalesce(h.name_en, '') FROM hubs h
		ORDER BY h.legacy_doc_id COLLATE "C" NULLS LAST, h.created_at, h.id`)
	if err != nil {
		return nil, err
	}
	var hubs []compute.Hub
	hubIndex := map[string]int{}
	for hubRows.Next() {
		var id string
		var h compute.Hub
		if err := hubRows.Scan(&id, &h.Code, &h.NameTH, &h.NameEN); err != nil {
			return nil, err
		}
		hubIndex[id] = len(hubs)
		hubs = append(hubs, h)
	}
	if err := hubRows.Err(); err != nil {
		return nil, err
	}
	aliasRows, err := tx.Query(ctx, `SELECT a.hub_id::text, a.alias::text FROM hub_name_aliases a ORDER BY a.created_at, a.alias`)
	if err != nil {
		return nil, err
	}
	for aliasRows.Next() {
		var hub, alias string
		if err := aliasRows.Scan(&hub, &alias); err != nil {
			return nil, err
		}
		if i, ok := hubIndex[hub]; ok {
			hubs[i].Aliases = append(hubs[i].Aliases, alias)
		}
	}
	if err := aliasRows.Err(); err != nil {
		return nil, err
	}
	pe.hubs = compute.NewHubMaps(hubs)

	carrier := e.cfg.OwnFleetTenantID
	rates, err := tx.Query(ctx, `SELECT id::text, coalesce(legacy_doc_id, ''), created_at, billing_party_id::text, import_id, hub_code,
		  destination_code, vehicle_class, rate_thb::float8, effective_from_at, job_category, voided
		FROM customer_rate_entries WHERE tenant_id = $1`, carrier)
	if err != nil {
		return nil, err
	}
	for rates.Next() {
		var r compute.RateEntry
		var cat string
		if err := rates.Scan(&r.ID, &r.LegacyDocID, &r.CreatedAt, &r.PartyID, &r.ImportID, &r.HubCode, &r.DestinationCode,
			&r.VehicleClass, &r.RateTHB, &r.EffectiveFrom, &cat, &r.Voided); err != nil {
			return nil, err
		}
		r.JobCategory = compute.RateJobCategory(cat)
		upd(r.PartyID, func(t *parityTables) { t.rates = append(t.rates, r) })
	}
	if err := rates.Err(); err != nil {
		return nil, err
	}
	fuel, err := tx.Query(ctx, `SELECT id::text, coalesce(legacy_doc_id, ''), created_at, billing_party_id::text, effective_from_at,
		  rate_multiplier::float8, add_thb_per_trip::float8, reference_fuel_price_thb::float8, voided
		FROM customer_fuel_rate_adjustments WHERE tenant_id = $1`, carrier)
	if err != nil {
		return nil, err
	}
	for fuel.Next() {
		var a compute.FuelAdjustment
		if err := fuel.Scan(&a.ID, &a.LegacyDocID, &a.CreatedAt, &a.PartyID, &a.EffectiveFrom, &a.RateMultiplier, &a.AddTHBPerTrip,
			&a.ReferenceFuelPriceTHB, &a.Voided); err != nil {
			return nil, err
		}
		upd(a.PartyID, func(t *parityTables) { t.fuel = append(t.fuel, a) })
	}
	if err := fuel.Err(); err != nil {
		return nil, err
	}
	standby, err := tx.Query(ctx, `SELECT id::text, coalesce(legacy_doc_id, ''), created_at, billing_party_id::text, rate_thb::float8,
		  effective_from_at, voided_at IS NOT NULL FROM standby_rate_entries WHERE tenant_id = $1`, carrier)
	if err != nil {
		return nil, err
	}
	for standby.Next() {
		var r compute.StandbyRate
		if err := standby.Scan(&r.ID, &r.LegacyDocID, &r.CreatedAt, &r.PartyID, &r.RateTHB, &r.EffectiveFrom, &r.Voided); err != nil {
			return nil, err
		}
		upd(r.PartyID, func(t *parityTables) { t.standby = append(t.standby, r) })
	}
	if err := standby.Err(); err != nil {
		return nil, err
	}
	fees, err := loadFees(ctx, tx, carrier)
	if err != nil {
		return nil, err
	}
	for party, byType := range fees {
		upd(party, func(t *parityTables) {
			t.extraStopFee = feeWinner(byType["extra_stop"], false)
			t.standbyFee = feeWinner(byType["standby"], true)
		})
	}
	return pe, nil
}

// feeRow is a customer_service_fees row of the extra_stop or standby type.
type feeRow struct {
	id, legacy string
	amount     float64
	created    time.Time
}

// loadFees reads the carrier's extra_stop and standby fees per party and type, legacy rows by legacy doc id (bytes),
// then rows created in Go by created_at, id.
func loadFees(ctx context.Context, tx pgx.Tx, carrier uuid.UUID) (map[string]map[string][]feeRow, error) {
	rows, err := tx.Query(ctx, `SELECT billing_party_id::text, fee_type, id::text, coalesce(legacy_doc_id, ''), amount_thb::float8, created_at
		FROM customer_service_fees WHERE tenant_id = $1 AND fee_type IN ('extra_stop', 'standby')
		ORDER BY legacy_doc_id COLLATE "C" NULLS LAST, created_at, id`, carrier)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string][]feeRow{}
	for rows.Next() {
		var party, typ string
		var f feeRow
		if err := rows.Scan(&party, &typ, &f.id, &f.legacy, &f.amount, &f.created); err != nil {
			return nil, err
		}
		if out[party] == nil {
			out[party] = map[string][]feeRow{}
		}
		out[party][typ] = append(out[party][typ], f)
	}
	return out, rows.Err()
}

// feeWinner is the fee the legacy engine priced with while D5 duplicates remain (Appendix A §A.3.9): extra_stop the
// first by doc id (`.find`, recompute-trip-billing.js:93), standby the last (buildStandbyServiceFeeMap,
// standbyBilling.ts:49-59). After the owner's sign-off (0010) a party has one row of each type and both rules pick it.
// The pricing functions apply the legacy guard on the amount (a negative fee is no fee).
func feeWinner(rows []feeRow, last bool) *float64 {
	if len(rows) == 0 {
		return nil
	}
	f := rows[0]
	if last {
		f = rows[len(rows)-1]
	}
	a := f.amount
	return &a
}

// parityCounts sum one (kind, party, period) of the window.
type parityCounts struct{ exact, allowed, mismatch int }

// divergenceCounts sum the records a preserved divergence (§6.18 #1, #9) decides.
type divergenceCounts struct{ rows, exact, allowed, mismatch int }

// reconcileParity is the dry-run recompute of the window (main spec §6.17 (2)): every delivered trip on its party's
// axis and every completed standby record by ended_at.
func (e *Engine) reconcileParity(ctx context.Context, tx pgx.Tx, docs map[string][]dump.Doc, status docStatus, w FinanceWindow,
	rec *Reconciliation) error {
	parties, err := loadParties(ctx, tx)
	if err != nil {
		return err
	}
	pe, err := e.loadParityEngine(ctx, tx, parties)
	if err != nil {
		return err
	}
	tripDocs := map[string]map[string]any{}
	for _, d := range docs["trip_records"] {
		if s := status.of("trip_records", d.Path); s == string(Loaded) || s == string(Quarantined) {
			tripDocs[d.ID] = d.Fields
		}
	}
	trips, err := e.loadParityTrips(ctx, tx, pe, parties, tripDocs, w, rec)
	if err != nil {
		return err
	}
	standby, err := loadParityStandby(ctx, tx, parties, w)
	if err != nil {
		return err
	}
	locks, err := loadPeriodLocks(ctx, tx)
	if err != nil {
		return err
	}
	// lockNote says what a difference means for the money already invoiced: a sent or paid period is never repriced
	// (the period lock, §6.11), an open one is repriced by a forced recompute after the flip.
	lockNote := func(row *ParityRow, party string, at time.Time) {
		if row.Outcome == ParityExact || row.Case == CaseFrozen || party == "" || at.IsZero() {
			return
		}
		if l, ok := locks.LockFor(party, at); ok {
			row.Note += "; period " + l.Status + " (" + l.InvoiceNumber + "): never repriced"
		} else {
			row.Note += "; open period: a forced recompute after the flip reprices it"
		}
	}
	summary := map[string]*parityCounts{}
	count := func(kind, party, period, outcome string) {
		k := kind + " " + party + " " + period
		if summary[k] == nil {
			summary[k] = &parityCounts{}
		}
		switch outcome {
		case ParityExact:
			summary[k].exact++
		case ParityAllowed:
			summary[k].allowed++
		default:
			summary[k].mismatch++
		}
	}
	div := map[string]*divergenceCounts{"1": {}, "9": {}}
	collisions := map[string]map[string]bool{}
	for _, t := range trips {
		v := pe.classifyTrip(t)
		row := ParityRow{Kind: "trip", Key: t.key, Party: t.party, Period: t.period, Stored: t.stored.String(), Go: v.got.String(),
			Outcome: v.outcome, Case: v.kase, Note: v.note}
		if v.kase == CaseFrozen || v.kase == MismatchTaskMissing || v.kase == MismatchMultiStops {
			row.Go = "-"
		}
		if t.divergence1 {
			row.Divergence = "1"
		}
		at := t.billingDate
		if at.IsZero() {
			at = t.in.DeliveredAt
		}
		lockNote(&row, t.stampedParty, at)
		addParityRow(rec, row, count, div)
		for _, d := range parityDestinations(t, pe.hubs) {
			if key, ok := collapsesOnDash(d); ok {
				if collisions[key] == nil {
					collisions[key] = map[string]bool{}
				}
				collisions[key][compute.UpperJS(compute.TrimJS(d))] = true
			}
		}
	}
	for _, s := range standby {
		v := pe.classifyStandby(s)
		row := ParityRow{Kind: "standby", Key: s.key, Party: s.party, Period: s.period, Stored: s.stored.String(), Go: v.got.String(),
			Outcome: v.outcome, Case: v.kase, Note: v.note}
		if s.divergence9() {
			row.Divergence = "9"
		}
		lockNote(&row, s.partyID, s.in.EndedAt)
		addParityRow(rec, row, count, div)
	}
	keys := make([]string, 0, len(summary))
	for k := range summary {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		c := summary[k]
		rec.add("parity_summary", k, c.exact+c.allowed+c.mismatch, c.exact, c.mismatch == 0,
			fmt.Sprintf("%d exact, %d allow-listed, %d mismatched", c.exact, c.allowed, c.mismatch))
	}
	for _, n := range []string{"1", "9"} {
		d := div[n]
		name := map[string]string{"1": "SPK dash collision (§6.18 #1)", "9": "standby ignores the task's billing party (§6.18 #9)"}[n]
		rec.add("divergence_"+n, name, d.rows, d.mismatch, d.mismatch == 0,
			fmt.Sprintf("%d record(s) decided by the preserved divergence: %d exact, %d allow-listed for another cause, %d mismatched",
				d.rows, d.exact, d.allowed, d.mismatch))
	}
	ckeys := make([]string, 0, len(collisions))
	for k := range collisions {
		ckeys = append(ckeys, k)
	}
	slices.Sort(ckeys)
	for _, k := range ckeys {
		raw := sortedKeys(collisions[k])
		if len(raw) > 1 {
			rec.add("spk_collisions", k, strings.Join(raw, " "), k, true,
				"distinct destinations that one rate-card key prices (§6.18 #1, preserved; reported for a later ADR and re-import)")
		}
	}
	rec.add("parity_window", strings.Join(w.Months, " "), len(trips), len(standby), true,
		"delivered trips (source) and completed standby records (target) recomputed; billing carrier "+e.ownFleet)
	return nil
}

// addParityRow records one record: every record in the parity report, every one that is not exact as a check (an
// allow-listed case passes, anything else fails), and its counts.
func addParityRow(rec *Reconciliation, row ParityRow, count func(kind, party, period, outcome string),
	div map[string]*divergenceCounts) {
	rec.Finance.Parity = append(rec.Finance.Parity, row)
	count(row.Kind, row.Party, row.Period, row.Outcome)
	if d := div[row.Divergence]; d != nil {
		d.rows++
		switch row.Outcome {
		case ParityExact:
			d.exact++
		case ParityAllowed:
			d.allowed++
		default:
			d.mismatch++
		}
	}
	if row.Outcome == ParityExact {
		return
	}
	note := row.Case
	if row.Outcome == ParityAllowed {
		note = "allow-list " + row.Case
	}
	rec.add("parity_"+row.Kind, row.Key+" ("+row.Party+" "+row.Period+")", row.Stored, row.Go, row.Outcome == ParityAllowed, note+": "+row.Note)
}

// parityDestinations are the destinations a trip's lookup normalised: the resolved task destination of a single
// trip, every delivered stop of a multi-drop trip (stops are not name -> code resolved, §6.8).
func parityDestinations(t parityTrip, hubs compute.HubMaps) []string {
	if t.in.IsMultiDelivery && t.in.StopProgressCount >= 2 {
		return t.in.DeliveredStops
	}
	if d := hubs.ResolveNameToCode(t.in.Task.Destination); d != "" {
		return []string{d}
	}
	return nil
}

func uuidStr(u *uuid.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// loadParityTrips reads the delivered trips of the window with their snapshot, task and stops, and checks that every
// loaded snapshot still equals its dump document (parity_load: a frozen row must be byte-equal after the load, and a
// document changed at source since the last load needs `etl load --since` first).
func (e *Engine) loadParityTrips(ctx context.Context, tx pgx.Tx, pe *parityEngine, parties map[string]partyRef,
	tripDocs map[string]map[string]any, w FinanceWindow, rec *Reconciliation) ([]parityTrip, error) {
	rows, err := tx.Query(ctx, `SELECT tr.id, coalesce(tr.legacy_doc_id, ''), tr.trip_no, tr.billing_party_id, tr.billing_date,
		  tr.delivered_at, tr.created_at, coalesce(tr.job_category, ''), tr.is_multi_delivery,
		  (s.trip_id IS NOT NULL), s.estimate_thb::float8, s.unpriced_reason, s.base_rate_thb::float8, s.stop_charge_thb::float8,
		  s.rate_import_id, s.lookup_hub_code, s.lookup_destination_code, s.fuel_adjustment_id::text, (s.rate_entry_id IS NULL),
		  coalesce(s.rate_multiplier, 1)::float8, coalesce(s.add_thb_per_trip, 0)::float8, s.fuel_effective_from_date,
		  s.round_effective_from_date, s.fuel_band_lower_thb::float8, s.fuel_band_upper_thb::float8,
		  s.reference_fuel_price_thb::float8, coalesce(s.is_multi_delivery, false), coalesce(s.manual_override, false),
		  re.legacy_doc_id, coalesce(re.voided, false), fa.legacy_doc_id, coalesce(fa.voided, false),
		  (tk.id IS NOT NULL), tk.billing_party_id, tk.source_linked_party_id, tk.destination_linked_party_id,
		  coalesce(tk.source_hub_raw, ''), coalesce(tk.destination_raw, ''), coalesce(tk.truck_type, ''),
		  coalesce(tk.legacy_truck_type, ''), tk.plan_at, coalesce(tk.job_category, '')
		FROM trip_records tr
		LEFT JOIN trip_billing_snapshots s ON s.trip_id = tr.id
		LEFT JOIN customer_rate_entries re ON re.id = s.rate_entry_id
		LEFT JOIN customer_fuel_rate_adjustments fa ON fa.id = s.fuel_adjustment_id
		LEFT JOIN tasks tk ON tk.id = tr.task_id
		WHERE tr.status = 'delivered'
		  AND ((tr.billing_date >= $1 AND tr.billing_date < $2) OR (tr.delivered_at >= $1 AND tr.delivered_at < $2))
		ORDER BY tr.trip_no COLLATE "C", tr.id`, w.from, w.to)
	if err != nil {
		return nil, err
	}
	type scanned struct {
		t        parityTrip
		id       uuid.UUID
		legacy   string
		snap     loadedSnapshot
		inWindow bool
		// rateEntryNull: the snapshot's import id did not resolve to one rate row at load.
		rateEntryNull bool
	}
	var all []scanned
	for rows.Next() {
		var x scanned
		var tripNo string
		var rateEntryNull bool
		var stamped, taskParty, srcParty, dstParty *uuid.UUID
		var billingDate, deliveredAt, planAt *time.Time
		var createdAt time.Time
		var estimate *float64
		var unpricedReason *string
		var reLegacy, faLegacy *string
		var reVoided, faVoided, hasTask bool
		var srcHub, dest, truckType string
		var taskCategory string
		s := &x.snap
		if err := rows.Scan(&x.id, &x.legacy, &tripNo, &stamped, &billingDate, &deliveredAt, &createdAt, &x.t.tripCategory,
			&x.t.in.IsMultiDelivery, &x.t.hasSnapshot, &estimate, &unpricedReason, &s.base, &s.stopCharge, &s.importID, &s.lookupHub,
			&s.lookupDest, &s.fuelAdjID, &rateEntryNull, &s.mult, &s.add, &s.fuelDate, &s.round, &s.bandLo, &s.bandHi, &s.refPrice, &s.multi, &s.manual,
			&reLegacy, &reVoided, &faLegacy, &faVoided, &hasTask, &taskParty, &srcParty, &dstParty, &srcHub, &dest, &truckType,
			&x.t.legacyTruckType, &planAt, &taskCategory); err != nil {
			return nil, err
		}
		x.rateEntryNull = rateEntryNull
		s.estimate, s.unpriced = estimate, unpricedReason
		t := &x.t
		t.hasTask = hasTask
		t.stampedParty = uuidStr(stamped)
		t.in.Task = compute.TaskInput{TaskParties: compute.TaskParties{BillingPartyID: uuidStr(taskParty),
			SourceLinkedPartyID: uuidStr(srcParty), DestinationLinkedPartyID: uuidStr(dstParty)},
			SourceHub: srcHub, Destination: dest, TruckType: truckType}
		t.in.JobCategory = taskCategory
		t.in.CreatedAt = createdAt
		if planAt != nil {
			t.in.PlanAt = *planAt
		}
		if deliveredAt != nil {
			t.in.DeliveredAt = *deliveredAt
		}
		if billingDate != nil {
			t.billingDate = *billingDate
		}
		party := t.stampedParty
		if party == "" {
			party, _ = compute.ResolveTaskParty(t.in.Task.TaskParties)
		}
		ref := parties[party]
		t.party = ref.label
		if t.party == "" {
			t.party = "(no party)"
		}
		axis := t.in.DeliveredAt
		if ref.basis == compute.BasisPlan && !t.billingDate.IsZero() {
			axis = t.billingDate
		}
		x.inWindow = w.has(axis)
		if x.inWindow {
			t.period = bkkPeriod(axis)
		}
		t.key = tripNo
		if x.legacy != "" && x.legacy != tripNo {
			t.key += " (" + x.legacy + ")"
		}
		if t.hasSnapshot {
			t.stored = price{priced: estimate != nil, reason: str(unpricedReason), party: t.stampedParty, multi: s.multi,
				importID: str(s.importID), mult: s.mult, add: s.add, bandLo: s.bandLo, bandHi: s.bandHi}
			if estimate != nil {
				t.stored.estimate = *estimate
			}
			if s.round != nil {
				t.stored.round = clock.DateString(*s.round)
			}
			fuelID := s.fuelAdjID
			if fuelID == nil {
				if f, ok := tripDocs[x.legacy]["billingFuelAdjustmentId"].(string); ok && strings.TrimSpace(f) != "" {
					fuelID = &f
				}
			}
			mult, add := s.mult, s.add
			t.snapshot = compute.Snapshot{FuelAdjustmentID: fuelID, RateMultiplier: &mult, AddTHBPerTrip: &add, ManualOverride: s.manual}
			if reVoided && reLegacy != nil {
				t.voided = append(t.voided, "rate entry "+*reLegacy)
			}
			if faVoided && faLegacy != nil {
				t.voided = append(t.voided, "fuel round "+*faLegacy)
			}
			// Legacy multi-drop totals are float sums: the tolerance compares the recompute with the raw Firestore value
			// (R20) when the loaded amount is that value rounded.
			if f := tripDocs[x.legacy]; f != nil && estimate != nil {
				if raw, ok := toFloat(f["billingEstimateThb"]); ok && cents(raw) == cents(*estimate) {
					t.stored.estimate = raw
				}
			}
		}
		all = append(all, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(all))
	for _, x := range all {
		if x.inWindow {
			ids = append(ids, x.id)
		}
	}
	stops := map[uuid.UUID][]string{}
	stopCount := map[uuid.UUID]int{}
	srows, err := tx.Query(ctx, `SELECT trip_id, destination_raw, status FROM trip_delivery_stops WHERE trip_id = ANY ($1)
		ORDER BY trip_id, completed_seq NULLS LAST, stop_index`, ids)
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var id uuid.UUID
		var dest, st string
		if err := srows.Scan(&id, &dest, &st); err != nil {
			return nil, err
		}
		stopCount[id]++
		if st == "delivered" && strings.TrimSpace(dest) != "" {
			stops[id] = append(stops[id], dest)
		}
	}
	if err := srows.Err(); err != nil {
		return nil, err
	}
	fees := map[uuid.UUID]float64{}
	brows, err := tx.Query(ctx, `SELECT trip_id, base_rate_thb::float8, final_rate_thb::float8 FROM trip_billing_stop_breakdown
		WHERE trip_id = ANY ($1) AND stop_index >= 2 ORDER BY trip_id, stop_index`, ids)
	if err != nil {
		return nil, err
	}
	for brows.Next() {
		var id uuid.UUID
		var base, final float64
		if err := brows.Scan(&id, &base, &final); err != nil {
			return nil, err
		}
		if _, seen := fees[id]; !seen && cents(base) == cents(final) {
			fees[id] = base
		}
	}
	if err := brows.Err(); err != nil {
		return nil, err
	}
	var out []parityTrip
	for _, x := range all {
		if !x.inWindow {
			continue
		}
		t := x.t
		t.in.DeliveredStops = stops[x.id]
		t.in.StopProgressCount = stopCount[x.id]
		if f, ok := fees[x.id]; ok {
			t.storedFee = &f
		}
		if t.hasSnapshot && x.rateEntryNull && x.snap.estimate != nil && str(x.snap.importID) != "" {
			// The import id did not resolve to one row at load: a voided card of that import and lookup still explains it.
			for _, r := range pe.tables[t.stampedParty].rates {
				if r.Voided && r.ImportID == *x.snap.importID && r.HubCode == compute.UpperJS(str(x.snap.lookupHub)) &&
					r.DestinationCode == str(x.snap.lookupDest) && !slices.Contains(t.voided, "rate entry "+r.LegacyDocID) {
					t.voided = append(t.voided, "rate entry "+r.LegacyDocID)
				}
			}
		}
		for _, d := range parityDestinations(t, pe.hubs) {
			if _, ok := collapsesOnDash(d); ok {
				t.divergence1 = true
			}
		}
		if f := tripDocs[x.legacy]; f != nil {
			if fields := x.snap.differsFromDump(t.hasSnapshot, f); len(fields) > 0 {
				rec.add("parity_load", t.key, "dump", "database", false, "the loaded snapshot differs from the dump in "+
					strings.Join(fields, ", ")+": run `etl load --since=watermark` (or --force after a mapping change) before the parity")
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// loadedSnapshot is a trip_billing_snapshots row as the parity reads it.
type loadedSnapshot struct {
	estimate         *float64
	unpriced         *string
	base, stopCharge *float64
	importID         *string
	lookupHub        *string
	lookupDest       *string
	fuelAdjID        *string
	mult, add        float64
	fuelDate, round  *time.Time
	bandLo, bandHi   *float64
	refPrice         *float64
	multi, manual    bool
}

// differsFromDump lists the snapshot fields whose stored value is not what the load writes from the dump document
// (tripSnapshot, map_ops.go): amounts in satang, the multiplier to 6 places, dates as yyyy-MM-dd.
func (s loadedSnapshot) differsFromDump(has bool, f map[string]any) []string {
	c := &docCtx{f: f}
	stamped := false
	for k, v := range f {
		if strings.HasPrefix(k, "billing") && v != nil && k != "billingCustomerId" && k != "billingDate" {
			stamped = true
		}
	}
	if stamped != has {
		return []string{"snapshot presence"}
	}
	if !has {
		return nil
	}
	var out []string
	cmpMoney := func(name, key string, got *float64) {
		n, r := c.moneyCents(key, false)
		switch {
		case r != moneyOK && got != nil, r == moneyOK && (got == nil || cents(*got) != n):
			out = append(out, name)
		}
	}
	cmpMoney("estimate", "billingEstimateThb", s.estimate)
	cmpMoney("baseRate", "billingBaseRateThb", s.base)
	cmpMoney("stopCharge", "billingStopChargeThb", s.stopCharge)
	cmpMoney("bandLower", "billingFuelBandLowerThb", s.bandLo)
	cmpMoney("bandUpper", "billingFuelBandUpperThb", s.bandHi)
	cmpMoney("referencePrice", "billingReferenceFuelPriceThb", s.refPrice)
	if str(c.text("billingRateImportId")) != str(s.importID) {
		out = append(out, "importId")
	}
	mult := 1.0
	if m := c.float("billingRateMultiplier"); m != nil {
		mult = *m
	}
	if strconv.FormatFloat(mult, 'f', 6, 64) != strconv.FormatFloat(s.mult, 'f', 6, 64) {
		out = append(out, "multiplier")
	}
	add, _ := c.moneyCents("billingAddThbPerTrip", false)
	if add != cents(s.add) {
		out = append(out, "add")
	}
	date := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.Format("2006-01-02")
	}
	if date(c.date("billingRoundEffectiveFromDateStr")) != date(s.round) {
		out = append(out, "roundDate")
	}
	if date(c.date("billingEffectiveFromDateStr")) != date(s.fuelDate) {
		out = append(out, "fuelEffectiveFrom")
	}
	if c.isTrue("billingManualOverride") != s.manual {
		out = append(out, "manualOverride")
	}
	if c.isTrue("billingIsMultiDelivery") != s.multi {
		out = append(out, "isMultiDelivery")
	}
	return out
}

// loadParityStandby reads the completed standby records that ended inside the window.
func loadParityStandby(ctx context.Context, tx pgx.Tx, parties map[string]partyRef, w FinanceWindow) ([]parityStandby, error) {
	rows, err := tx.Query(ctx, `SELECT coalesce(sr.legacy_doc_id, sr.id::text), sr.ended_at, sr.started_at, sr.created_at,
		  sr.customer_party_id, sr.billing_party_id, sr.billing_estimate_thb::float8, coalesce(sr.billing_rate_source, ''),
		  sre.id::text, coalesce(sre.legacy_doc_id, sre.id::text, ''), coalesce(sre.voided_at IS NOT NULL, false),
		  sr.billing_effective_from_date, coalesce(sr.billing_unpriced_reason, ''),
		  tk.billing_party_id, tk.source_linked_party_id, tk.destination_linked_party_id
		FROM standby_records sr
		LEFT JOIN standby_rate_entries sre ON sre.id = sr.billing_rate_entry_id
		LEFT JOIN tasks tk ON tk.id = sr.task_id
		WHERE sr.status = 'completed' AND sr.ended_at >= $1 AND sr.ended_at < $2
		ORDER BY sr.ended_at, sr.id`, w.from, w.to)
	if err != nil {
		return nil, err
	}
	var out []parityStandby
	for rows.Next() {
		var s parityStandby
		var ended time.Time
		var started *time.Time
		var own, billed, taskParty, srcParty, dstParty *uuid.UUID
		var estimate *float64
		var entryID *string
		var effDate *time.Time
		if err := rows.Scan(&s.key, &ended, &started, &s.in.CreatedAt, &own, &billed, &estimate, &s.stored.source, &entryID,
			&s.entryLegacy, &s.voided, &effDate, &s.stored.reason, &taskParty, &srcParty, &dstParty); err != nil {
			return nil, err
		}
		s.in.EndedAt = ended
		if started != nil {
			s.in.StartedAt = *started
		}
		s.in.CustomerPartyID = uuidStr(own)
		s.in.TaskSourceLinkedPartyID, s.in.TaskDestinationLinkedPartyID = uuidStr(srcParty), uuidStr(dstParty)
		s.taskBillingParty = uuidStr(taskParty)
		if estimate != nil {
			s.stored.priced, s.stored.estimate = true, *estimate
		}
		s.stored.entryID = str(entryID)
		if effDate != nil {
			s.stored.entryDate = effDate.Format("2006-01-02")
		}
		party := uuidStr(billed)
		if party == "" {
			party, _ = compute.StandbyParty(s.in)
		}
		s.partyID = party
		s.party = parties[party].label
		if s.party == "" {
			s.party = "(no party)"
		}
		s.period = bkkPeriod(ended)
		out = append(out, s)
	}
	return out, rows.Err()
}
