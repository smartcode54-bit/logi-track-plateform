package seed

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
)

// Prices are never typed (Appendix D §D.1.4): the billing engine recomputes every seeded snapshot whose
// writer is the engine (computed_by other than etl and manual_edit) and every completed standby record over
// the seeded rate tables. During a load a difference aborts the transaction; --verify runs the same check
// over every such row in the database (invariant 2, Go half).

// engineWriters are the snapshot writers whose rows the engine must reproduce.
const engineFilter = `s.computed_by NOT IN ('etl','manual_edit')`

// Mismatch is one row the engine prices differently from what is stored.
type Mismatch struct {
	Kind, ID, Label string
	Field           string
	Stored, Engine  string
}

func (m Mismatch) String() string {
	return fmt.Sprintf("%s %s (%s): %s stored %s, engine %s", m.Kind, m.Label, m.ID, m.Field, m.Stored, m.Engine)
}

// partyTables are the pricing inputs of one billing party, read in the caller's transaction (R17).
type partyTables struct {
	basis    compute.BillingDateBasis
	rates    []compute.RateEntry
	fuel     []compute.FuelAdjustment
	fees     map[string]float64 // fee_type -> amount
	standbys []compute.StandbyRate
}

type engineInputs struct {
	hubs    compute.HubMaps
	parties map[string]*partyTables
}

// loadEngineInputs reads every billing party's tables and the hub maps.
func loadEngineInputs(ctx context.Context, q pgx.Tx) (*engineInputs, error) {
	in := &engineInputs{parties: map[string]*partyTables{}}
	party := func(id string) *partyTables {
		p, ok := in.parties[id]
		if !ok {
			p = &partyTables{fees: map[string]float64{}}
			in.parties[id] = p
		}
		return p
	}
	rows, err := q.Query(ctx, `SELECT id::text, billing_date_basis FROM billing_parties`)
	if err != nil {
		return nil, err
	}
	if err := scanAll(rows, func(r pgx.Rows) error {
		var id, basis string
		if err := r.Scan(&id, &basis); err != nil {
			return err
		}
		party(id).basis = compute.BillingDateBasis(basis)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("billing parties: %w", err)
	}
	rows, err = q.Query(ctx, `SELECT id::text, coalesce(legacy_doc_id, ''), created_at, billing_party_id::text, import_id,
		hub_code, destination_code, vehicle_class, rate_thb::float8, effective_from_at, job_category, voided
		FROM customer_rate_entries ORDER BY billing_party_id, created_at, id`)
	if err != nil {
		return nil, err
	}
	if err := scanAll(rows, func(r pgx.Rows) error {
		var e compute.RateEntry
		var cat string
		if err := r.Scan(&e.ID, &e.LegacyDocID, &e.CreatedAt, &e.PartyID, &e.ImportID, &e.HubCode, &e.DestinationCode,
			&e.VehicleClass, &e.RateTHB, &e.EffectiveFrom, &cat, &e.Voided); err != nil {
			return err
		}
		e.JobCategory = compute.JobCategory(cat)
		p := party(e.PartyID)
		p.rates = append(p.rates, e)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("rate entries: %w", err)
	}
	rows, err = q.Query(ctx, `SELECT id::text, coalesce(legacy_doc_id, ''), created_at, billing_party_id::text, effective_from_at,
		rate_multiplier::float8, add_thb_per_trip::float8, reference_fuel_price_thb::float8, voided
		FROM customer_fuel_rate_adjustments ORDER BY billing_party_id, created_at, id`)
	if err != nil {
		return nil, err
	}
	if err := scanAll(rows, func(r pgx.Rows) error {
		var a compute.FuelAdjustment
		if err := r.Scan(&a.ID, &a.LegacyDocID, &a.CreatedAt, &a.PartyID, &a.EffectiveFrom, &a.RateMultiplier,
			&a.AddTHBPerTrip, &a.ReferenceFuelPriceTHB, &a.Voided); err != nil {
			return err
		}
		p := party(a.PartyID)
		p.fuel = append(p.fuel, a)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("fuel adjustments: %w", err)
	}
	rows, err = q.Query(ctx, `SELECT billing_party_id::text, fee_type, amount_thb::float8 FROM customer_service_fees
		ORDER BY billing_party_id, created_at, id`)
	if err != nil {
		return nil, err
	}
	if err := scanAll(rows, func(r pgx.Rows) error {
		var id, typ string
		var amount float64
		if err := r.Scan(&id, &typ, &amount); err != nil {
			return err
		}
		if _, dup := party(id).fees[typ]; !dup {
			party(id).fees[typ] = amount // the first fee of a type wins, as the legacy query took the first doc
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("service fees: %w", err)
	}
	rows, err = q.Query(ctx, `SELECT id::text, coalesce(legacy_doc_id, ''), created_at, billing_party_id::text, rate_thb::float8,
		effective_from_at, voided_at IS NOT NULL FROM standby_rate_entries ORDER BY billing_party_id, created_at, id`)
	if err != nil {
		return nil, err
	}
	if err := scanAll(rows, func(r pgx.Rows) error {
		var s compute.StandbyRate
		if err := r.Scan(&s.ID, &s.LegacyDocID, &s.CreatedAt, &s.PartyID, &s.RateTHB, &s.EffectiveFrom, &s.Voided); err != nil {
			return err
		}
		p := party(s.PartyID)
		p.standbys = append(p.standbys, s)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("standby rates: %w", err)
	}
	rows, err = q.Query(ctx, `SELECT h.source_id::text, h.name_th, coalesce(h.name_en, ''),
		coalesce(array_agg(a.alias::text ORDER BY a.created_at, a.alias) FILTER (WHERE a.alias IS NOT NULL), '{}')
		FROM hubs h LEFT JOIN hub_name_aliases a ON a.hub_id = h.id
		GROUP BY h.id ORDER BY h.created_at, h.id`)
	if err != nil {
		return nil, err
	}
	var hubs []compute.Hub
	if err := scanAll(rows, func(r pgx.Rows) error {
		var h compute.Hub
		if err := r.Scan(&h.Code, &h.NameTH, &h.NameEN, &h.Aliases); err != nil {
			return err
		}
		hubs = append(hubs, h)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("hubs: %w", err)
	}
	in.hubs = compute.NewHubMaps(hubs)
	return in, nil
}

// tables is the engine's view of one party's tables (empty for an unknown or absent party).
func (in *engineInputs) tables(party string) compute.Tables {
	p, ok := in.parties[party]
	if !ok {
		return compute.Tables{Hubs: in.hubs}
	}
	t := compute.Tables{Basis: p.basis, Rates: p.rates, Fuel: p.fuel, Hubs: in.hubs}
	if fee, ok := p.fees["extra_stop"]; ok {
		t.ExtraStopFeeTHB = &fee
	}
	return t
}

// tripSnapshot is a stored snapshot with its trip and task inputs.
type tripSnapshot struct {
	id, tripNo                                  string
	in                                          compute.TripInput
	estimate, reason, base, stopCharge          *string
	rateEntry, fuel, lookupHub, lookupDest      *string
	roundDate, bandLower, bandUpper, jobAtPrice *string
	manual, multi                               bool
}

// CheckEngine recomputes the selected trip snapshots and standby records in q and returns every difference.
// ids nil selects every engine-written row (--verify); otherwise only those of the given trips and standby
// records (the rows a load inserted). Either way a snapshot whose stored writer is etl or manual_edit is never
// recomputed: a legacy or manually edited price is not the engine's to reproduce.
func CheckEngine(ctx context.Context, q pgx.Tx, tripIDs, standbyIDs []uuid.UUID) ([]Mismatch, error) {
	in, err := loadEngineInputs(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("seed: engine inputs: %w", err)
	}
	trips, err := loadSnapshots(ctx, q, tripIDs)
	if err != nil {
		return nil, fmt.Errorf("seed: snapshots: %w", err)
	}
	var out []Mismatch
	for _, s := range trips {
		party, _ := compute.ResolveTaskParty(s.in.Task.TaskParties)
		res, err := compute.PriceTrip(s.in, in.tables(party))
		if err != nil {
			if errors.Is(err, compute.ErrMultiInsufficientStops) {
				out = append(out, Mismatch{Kind: "trip", ID: s.id, Label: s.tripNo, Field: "multi-drop", Stored: "priced", Engine: err.Error()})
				continue
			}
			return nil, err
		}
		out = append(out, compareTrip(s, res)...)
	}
	sb, err := checkStandby(ctx, q, in, standbyIDs)
	if err != nil {
		return nil, fmt.Errorf("seed: standby: %w", err)
	}
	return append(out, sb...), nil
}

// loadSnapshots reads the snapshots to recompute with their inputs; the delivered stops come in completion
// order (completed_seq), the order the engine prices extra stops in.
func loadSnapshots(ctx context.Context, q pgx.Tx, ids []uuid.UUID) ([]*tripSnapshot, error) {
	where := engineFilter
	args := []any{}
	if ids != nil {
		where, args = `t.id = ANY($1::uuid[]) AND `+engineFilter, []any{ids}
	}
	rows, err := q.Query(ctx, `SELECT t.id::text, t.trip_no, t.delivered_at, t.created_at, t.is_multi_delivery,
		coalesce(k.billing_party_id::text, ''), coalesce(k.source_linked_party_id::text, ''), coalesce(k.destination_linked_party_id::text, ''),
		k.source_hub_raw, k.destination_raw, coalesce(k.truck_type, ''), coalesce(k.job_category, ''), k.plan_at,
		s.estimate_thb::text, s.unpriced_reason, s.base_rate_thb::text, s.stop_charge_thb::text, s.rate_entry_id::text,
		s.fuel_adjustment_id::text, s.lookup_hub_code, s.lookup_destination_code, s.round_effective_from_date::text,
		s.fuel_band_lower_thb::text, s.fuel_band_upper_thb::text, s.job_category_at_pricing, s.manual_override, s.is_multi_delivery
		FROM trip_billing_snapshots s JOIN trip_records t ON t.id = s.trip_id JOIN tasks k ON k.id = t.task_id
		WHERE `+where+` ORDER BY t.id`, args...)
	if err != nil {
		return nil, err
	}
	var out []*tripSnapshot
	byID := map[string]*tripSnapshot{}
	if err := scanAll(rows, func(r pgx.Rows) error {
		s := &tripSnapshot{}
		var delivered *time.Time
		if err := r.Scan(&s.id, &s.tripNo, &delivered, &s.in.CreatedAt, &s.in.IsMultiDelivery,
			&s.in.Task.BillingPartyID, &s.in.Task.SourceLinkedPartyID, &s.in.Task.DestinationLinkedPartyID,
			&s.in.Task.SourceHub, &s.in.Task.Destination, &s.in.Task.TruckType, &s.in.JobCategory, &s.in.PlanAt,
			&s.estimate, &s.reason, &s.base, &s.stopCharge, &s.rateEntry, &s.fuel, &s.lookupHub, &s.lookupDest,
			&s.roundDate, &s.bandLower, &s.bandUpper, &s.jobAtPrice, &s.manual, &s.multi); err != nil {
			return err
		}
		if delivered != nil {
			s.in.DeliveredAt = *delivered
		}
		out = append(out, s)
		byID[s.id] = s
		return nil
	}); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(out))
	for _, s := range out {
		keys = append(keys, s.id)
	}
	rows, err = q.Query(ctx, `SELECT trip_id::text, coalesce(destination_raw, ''), status FROM trip_delivery_stops
		WHERE trip_id = ANY($1::uuid[]) ORDER BY trip_id, completed_seq NULLS LAST, stop_index`, keys)
	if err != nil {
		return nil, err
	}
	if err := scanAll(rows, func(r pgx.Rows) error {
		var id, dest, status string
		if err := r.Scan(&id, &dest, &status); err != nil {
			return err
		}
		s := byID[id]
		s.in.StopProgressCount++
		if status == "delivered" && dest != "" {
			s.in.DeliveredStops = append(s.in.DeliveredStops, dest)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// compareTrip lists the stored fields that differ from the engine's result.
func compareTrip(s *tripSnapshot, res compute.TripResult) []Mismatch {
	var out []Mismatch
	diff := func(field string, stored *string, engine string) {
		if deref(stored) != engine {
			out = append(out, Mismatch{Kind: "trip", ID: s.id, Label: s.tripNo, Field: field, Stored: show(stored), Engine: show(&engine)})
		}
	}
	if res.Price == nil {
		diff("estimate_thb", s.estimate, "")
		diff("unpriced_reason", s.reason, string(res.Reason))
		return out
	}
	p := res.Price
	diff("estimate_thb", s.estimate, money(p.EstimateTHB))
	diff("unpriced_reason", s.reason, "")
	diff("base_rate_thb", s.base, money(p.BaseRateTHB))
	if p.IsMultiDelivery {
		diff("stop_charge_thb", s.stopCharge, money(p.StopChargeTHB))
	}
	diff("rate_entry_id", s.rateEntry, p.RateEntryID)
	diff("fuel_adjustment_id", s.fuel, p.FuelAdjustmentID)
	diff("lookup_hub_code", s.lookupHub, p.LookupHubCode)
	diff("lookup_destination_code", s.lookupDest, p.LookupDestinationCode)
	diff("round_effective_from_date", s.roundDate, p.RoundEffectiveFromDate)
	diff("fuel_band_lower_thb", s.bandLower, optMoney(p.FuelBandLowerTHB))
	diff("fuel_band_upper_thb", s.bandUpper, optMoney(p.FuelBandUpperTHB))
	diff("job_category_at_pricing", s.jobAtPrice, string(p.JobCategory))
	if s.manual != p.ManualOverride {
		out = append(out, Mismatch{Kind: "trip", ID: s.id, Label: s.tripNo, Field: "manual_override",
			Stored: strconv.FormatBool(s.manual), Engine: strconv.FormatBool(p.ManualOverride)})
	}
	if s.multi != p.IsMultiDelivery {
		out = append(out, Mismatch{Kind: "trip", ID: s.id, Label: s.tripNo, Field: "is_multi_delivery",
			Stored: strconv.FormatBool(s.multi), Engine: strconv.FormatBool(p.IsMultiDelivery)})
	}
	return out
}

// checkStandby recomputes completed standby records: priced ones and those with a stored reason.
func checkStandby(ctx context.Context, q pgx.Tx, in *engineInputs, ids []uuid.UUID) ([]Mismatch, error) {
	where := `s.status = 'completed' AND (s.billing_estimate_thb IS NOT NULL OR s.billing_unpriced_reason IS NOT NULL)`
	args := []any{}
	if ids != nil {
		where, args = `s.id = ANY($1::uuid[]) AND `+where, []any{ids}
	}
	rows, err := q.Query(ctx, `SELECT s.id::text, coalesce(s.customer_party_id::text, ''),
		coalesce(k.source_linked_party_id::text, ''), coalesce(k.destination_linked_party_id::text, ''),
		s.started_at, s.ended_at, s.created_at, s.billing_estimate_thb::text, s.billing_unpriced_reason,
		s.billing_party_id::text, s.billing_rate_source, s.billing_rate_entry_id::text, s.billing_effective_from_date::text
		FROM standby_records s LEFT JOIN tasks k ON k.id = s.task_id WHERE `+where+` ORDER BY s.id`, args...)
	if err != nil {
		return nil, err
	}
	var out []Mismatch
	err = scanAll(rows, func(r pgx.Rows) error {
		var id string
		var sin compute.StandbyInput
		var started, ended *time.Time
		var estimate, reason, party, source, entry, eff *string
		if err := r.Scan(&id, &sin.CustomerPartyID, &sin.TaskSourceLinkedPartyID, &sin.TaskDestinationLinkedPartyID,
			&started, &ended, &sin.CreatedAt, &estimate, &reason, &party, &source, &entry, &eff); err != nil {
			return err
		}
		if started != nil {
			sin.StartedAt = *started
		}
		if ended != nil {
			sin.EndedAt = *ended
		}
		pid, _ := compute.StandbyParty(sin)
		var rates []compute.StandbyRate
		var fee *float64
		if p, ok := in.parties[pid]; ok {
			rates = p.standbys
			if f, ok := p.fees["standby"]; ok {
				fee = &f
			}
		}
		res := compute.PriceStandby(sin, rates, fee)
		diff := func(field string, stored *string, engine string) {
			if deref(stored) != engine {
				out = append(out, Mismatch{Kind: "standby", ID: id, Label: id, Field: field, Stored: show(stored), Engine: show(&engine)})
			}
		}
		if res.Price == nil {
			diff("billing_estimate_thb", estimate, "")
			diff("billing_unpriced_reason", reason, string(res.Reason))
			return nil
		}
		diff("billing_estimate_thb", estimate, money(res.Price.EstimateTHB))
		diff("billing_unpriced_reason", reason, "")
		diff("billing_party_id", party, res.Price.PartyID)
		diff("billing_rate_source", source, res.Price.RateSource)
		diff("billing_rate_entry_id", entry, res.Price.RateEntryID)
		diff("billing_effective_from_date", eff, res.Price.EffectiveFromDate)
		return nil
	})
	return out, err
}

// money is the NUMERIC(14,2) text of an engine amount (R20).
func money(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func optMoney(v *float64) string {
	if v == nil {
		return ""
	}
	return money(*v)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func show(s *string) string {
	if s == nil || *s == "" {
		return "NULL"
	}
	return *s
}

// scanAll runs fn for every row and closes rows.
func scanAll(rows pgx.Rows, fn func(pgx.Rows) error) error {
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
