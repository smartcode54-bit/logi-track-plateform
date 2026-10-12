package billing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// The pricing transaction of T38 (main spec §6.10, §6.13; port of tryWriteBillingSnapshotFromTripData,
// fn:tripBillingOnDelivered.ts:273-604, and tryWriteStandbyBillingSnapshot, fn:standbyBilling.ts:134-209). PriceTrip and
// PriceStandby are the only price writers outside the ETL: the billing.compute and billing.backfill consumers and the
// billing endpoints call them, each in a db.WithSystem transaction they own (every billing carrier is readable there,
// R61), and nothing else writes trip_billing_snapshots or the billing_* columns of standby_records.
//
// Inside the transaction, in this order: the trip (standby record) FOR UPDATE; its snapshot; its task FOR SHARE; the
// hub maps, the party's basis and its rate tables of the billing carrier FOR SHARE (LoadPricingTables, R17: never
// Redis); the period locks of a write that may move a priced period (PeriodLock: the old period, then the new one);
// then the writes and one outbox event. The event's id becomes the snapshot's last_event_id (R57): producers of the
// record's events append while holding its row lock, so the ids of one record follow commit order and a message whose
// id is not above it was already reflected in what this transaction read (dropped as stale).

// Outbox routing keys of the pricing (Appendix B §B.4.3, §B.5.2).
const (
	RouteTripPriced    = "trip.priced"
	RouteTripRepriced  = "trip.repriced"
	RouteStandbyPriced = "standby.priced"
)

// trip_billing_snapshots.computed_by values (0005) of the Go price writers.
const (
	ComputedByMobile         = "mobile_delivery"
	ComputedByScheduler      = "scheduler"
	ComputedByAdminForce     = "admin_force"
	ComputedBySetJobCategory = "set_job_category"
	ComputedByManualEdit     = "manual_edit"
)

// Outcomes of one pricing run (the result label of billing_compute_total and of the backfill counts).
const (
	OutcomeStale        = "stale"         // the message is not newer than the record's last_event_id
	OutcomeNotDelivered = "not_delivered" // trip not delivered (§6.10 state 1)
	OutcomeSkipped      = "skipped"       // already priced (auto), frozen without a date to move, or nothing changes
	OutcomeRestamped    = "restamped"     // frozen: billing_date moved, price kept (state 3)
	OutcomeBlocked      = "blocked"       // a sent or paid period (§6.11)
	OutcomeTaskMissing  = "task_missing"  // state 4
	OutcomeMultiStops   = ReasonMultiInsufficientStops
	OutcomeUnpriced     = "unpriced" // state 5 (the reason is in Reason)
	OutcomePriced       = "priced"   // first price (state 7)
	OutcomeRepriced     = "repriced" // a priced row rewritten
	// OutcomeNotOwned is a record the FS->PG mirror writes while billing is Firestore-owned (TripRequest.SkipMirrored):
	// the legacy pricer's, nothing written.
	OutcomeNotOwned = "not_owned"
)

// ReasonBillingNotOwned is the answer's reason of OutcomeNotOwned.
const ReasonBillingNotOwned = "billing_not_owned"

// TripMode selects the path of PriceTrip (main spec §6.10).
type TripMode int

const (
	// ModeAuto prices an unpriced trip; a priced one is returned as stored (state 2). Events, the safety net, the
	// plain compute, the mobile shim and a backfill without forceRecompute.
	ModeAuto TripMode = iota
	// ModeForce reprices a priced trip (state 6, period locks old then new); a frozen one only moves its billing date
	// (state 3). accounting:recompute_force, and the date-change events on a priced trip.
	ModeForce
	// ModeJobCategory reprices for an explicit target category, frozen rows included, with no fallback; a target that
	// prices nothing writes nothing (POST /v1/trips/{id}/job-category).
	ModeJobCategory
	// ModeManual stores an admin's price (PUT /v1/trips/{id}/billing/manual).
	ModeManual
)

// TripRequest is one pricing run of a trip.
type TripRequest struct {
	Mode TripMode
	// Category is the target of ModeJobCategory and the row's category of ModeManual.
	Category compute.JobCategory
	// ManualTHB is the price of ModeManual (> 0, two decimals).
	ManualTHB float64
	// ComputedBy is trip_billing_snapshots.computed_by; "" derives it (mobile_delivery for a trip the app delivered,
	// scheduler otherwise).
	ComputedBy string
	// EventID is the outbox id of the message being applied (billing.compute); 0 for a request or a job.
	EventID int64
	// At is the clock of computed_at.
	At time.Time
	// RequestID travels on the outbox event (headers.requestId).
	RequestID string
	// SkipMirrored leaves a trip the FS->PG mirror writes (legacy_doc_id set) alone: the deployment runs the mirror
	// while billing is not PostgreSQL-owned (JobDeps.FirestorePricing, P1-P2), so Firestore's Cloud Functions still price
	// it and the mirror carries that price into PostgreSQL. A trip born in PostgreSQL is priced either way.
	SkipMirrored bool
}

// ComputeResult is the answer of POST /v1/trips/{id}/billing/compute and of every trip pricing run, in the shape of the
// computeTripBillingSnapshot callable (fn:tripBillingOnDelivered.ts:183-192): ok, skipped, billingEstimateThb, error,
// blockedInvoiceNumber, billingDateMoved. Reason adds the stable code of an unpriced or failed run (no_customer,
// no_rate, no_vehicle_class, no_billing_date, task_missing, multi_insufficient_stops, not_delivered).
type ComputeResult struct {
	OK                   bool     `json:"ok"`
	Skipped              bool     `json:"skipped,omitempty"`
	BillingEstimateTHB   *float64 `json:"billingEstimateThb,omitempty"`
	Error                string   `json:"error,omitempty"`
	BlockedInvoiceNumber string   `json:"blockedInvoiceNumber,omitempty"`
	BillingDateMoved     bool     `json:"billingDateMoved,omitempty"`
	Reason               string   `json:"reason,omitempty"`
}

// TripOutcome is what a pricing run did.
type TripOutcome struct {
	Result  ComputeResult
	Outcome string                 // Outcome*
	Reason  compute.UnpricedReason // set with OutcomeUnpriced
	Lock    *compute.LockedPeriod  // set with OutcomeBlocked
	// TripTenant is the tenant that ran the trip; Carrier the billing carrier of its price (R61).
	TripTenant, Carrier uuid.UUID
	// WasPriced reports a price before the run; Written that the run wrote something.
	WasPriced, Written bool
}

// ErrRecordNotFound: no trip or standby record has the id.
var ErrRecordNotFound = errors.New("billing: no such trip or standby record")

func money2(x float64) string { return strconv.FormatFloat(jsmath.Round2(x), 'f', 2, 64) }

func money2Ptr(x *float64) *string {
	if x == nil {
		return nil
	}
	s := money2(*x)
	return &s
}

func round2Ptr(x float64) *float64 {
	v := jsmath.Round2(x)
	return &v
}

func uuidPtr(s string) *uuid.UUID {
	if s == "" {
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &id
}

func textPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func instant(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func presentInstant(t time.Time) bool { return !t.IsZero() && t.UnixMilli() > 0 }

// errorText is the legacy message of an unpriced or failed run (the pages show it).
func errorText(reason string, hub, dest, class string, multi bool) string {
	switch reason {
	case string(compute.NoCustomer):
		return "Task has no linked customer"
	case string(compute.NoRate):
		if multi {
			return "Could not compute multi-delivery billing"
		}
		return fmt.Sprintf("No rate: %s → %s (%s)", hub, dest, class)
	case string(compute.NoVehicleClass):
		return "Task has no vehicle class (truck type)"
	case string(compute.NoBillingDate):
		return "Trip has no billing date"
	case string(compute.NoEndedAt):
		return "Standby record has no end time"
	case ReasonMultiInsufficientStops:
		return "Multi-delivery trip has < 2 delivered stops"
	}
	return reason
}

// lockedText is the message of a refusal by the period lock (fn:tripBillingOnDelivered.ts:216-222).
func lockedText(l compute.LockedPeriod) string {
	return "Period already invoiced (" + l.InvoiceNumber + ") — cancel or credit-note it first"
}

// tripEvent appends a pricing event of a trip on its tenant's and its billing carrier's trips topics (billing fields
// stay out of the payload, Appendix B §B.4.3) and returns its outbox id.
func tripEvent(ctx context.Context, tx pgx.Tx, key string, tr billingdb.LockTripForPricingRow, carrier uuid.UUID,
	payload map[string]any, rid string) (int64, error) {
	topics := []string{realtime.TenantTopic(tr.TenantID.String(), realtime.FamilyTrips)}
	if carrier != uuid.Nil && carrier != tr.TenantID {
		topics = append(topics, realtime.TenantTopic(carrier.String(), realtime.FamilyTrips))
	}
	p := map[string]any{"entity": "trip", "id": tr.ID.String(), "tripNo": tr.TripNo, "tenantId": tr.TenantID.String()}
	if tr.TaskID != nil {
		p["taskId"] = tr.TaskID.String()
	}
	if tr.LegacyDocID != nil {
		p["legacyDocId"] = *tr.LegacyDocID
	}
	for k, v := range payload {
		p[k] = v
	}
	var headers map[string]string
	if rid != "" {
		headers = map[string]string{"requestId": rid}
	}
	t := tr.TenantID
	a, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: key, AggregateType: "trip", AggregateID: tr.ID.String(),
		TenantID: &t, Payload: p, Topics: topics, Headers: headers})
	return a.ID, err
}

// PriceTrip runs one pricing of trip id in tx, a db.WithSystem transaction of the caller (main spec §6.10). It returns
// ErrRecordNotFound for an unknown trip; every other outcome, a refusal by the period lock included, is in the
// TripOutcome (the caller maps it to its answer: an HTTP status, a job count, an ack).
func PriceTrip(ctx context.Context, tx pgx.Tx, id uuid.UUID, req TripRequest) (TripOutcome, error) {
	q := billingdb.New(tx)
	tr, err := q.LockTripForPricing(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return TripOutcome{}, ErrRecordNotFound
	}
	if err != nil {
		return TripOutcome{}, err
	}
	out := TripOutcome{TripTenant: tr.TenantID}
	if req.SkipMirrored && tr.LegacyDocID != nil {
		out.Outcome, out.Result = OutcomeNotOwned, ComputeResult{OK: true, Skipped: true, Reason: ReasonBillingNotOwned}
		return out, nil
	}
	snap, err := q.TripSnapshotForPricing(ctx, id)
	hasSnap := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if req.EventID > 0 && hasSnap && snap.LastEventID != nil && req.EventID <= *snap.LastEventID {
		out.Outcome, out.Result = OutcomeStale, ComputeResult{OK: true, Skipped: true, Reason: OutcomeStale}
		return out, nil
	}
	out.WasPriced = hasSnap && snap.EstimateThb != nil
	if tr.Status != "delivered" {
		out.Outcome = OutcomeNotDelivered
		out.Result = ComputeResult{OK: true, Skipped: true, Error: "Trip is not delivered yet", Reason: OutcomeNotDelivered}
		return out, nil
	}
	stored := func() *float64 {
		if !out.WasPriced {
			return nil
		}
		v := *snap.EstimateThb
		return &v
	}
	// keep records the message on a priced snapshot the run leaves unchanged, so a redelivery is stale.
	keep := func() error {
		if req.EventID > 0 && hasSnap {
			return q.SetSnapshotLastEvent(ctx, billingdb.SetSnapshotLastEventParams{LastEventID: req.EventID, TripID: id})
		}
		return nil
	}
	if req.Mode == ModeAuto && out.WasPriced {
		out.Outcome, out.Result = OutcomeSkipped, ComputeResult{OK: true, Skipped: true, BillingEstimateTHB: stored()}
		return out, keep()
	}
	if req.Mode == ModeForce && out.WasPriced && compute.IsFrozen(snapshotOf(snap), compute.JobCategory(deref(tr.JobCategory))) {
		return restampAxis(ctx, tx, q, tr, snap, req, out)
	}

	if tr.TaskID == nil {
		out.Outcome = OutcomeTaskMissing
		out.Result = ComputeResult{OK: false, Error: "Trip missing taskId", Reason: OutcomeTaskMissing}
		return out, keep()
	}
	task, err := q.LockTaskForPricing(ctx, *tr.TaskID)
	if errors.Is(err, pgx.ErrNoRows) {
		out.Outcome = OutcomeTaskMissing
		out.Result = ComputeResult{OK: false, Error: "Task not found", Reason: OutcomeTaskMissing}
		return out, keep()
	}
	if err != nil {
		return out, err
	}
	if req.Mode == ModeJobCategory && deref(task.JobCategory) == string(req.Category) && deref(tr.JobCategory) == string(req.Category) {
		// Nothing to recompute: the task and the trip carry the target already (fn:tripBillingOnDelivered.ts:722-731).
		out.Outcome, out.Result = OutcomeSkipped, ComputeResult{OK: true, Skipped: true, BillingEstimateTHB: stored()}
		return out, nil
	}

	in := compute.TripInput{
		Task: compute.TaskInput{TaskParties: compute.TaskParties{BillingPartyID: idStr(task.BillingPartyID),
			SourceLinkedPartyID: idStr(task.SourceLinkedPartyID), DestinationLinkedPartyID: idStr(task.DestinationLinkedPartyID)},
			SourceHub: task.SourceHubRaw, Destination: task.DestinationRaw, TruckType: deref(task.TruckType)},
		JobCategory: deref(task.JobCategory), PlanAt: task.PlanAt, DeliveredAt: instant(tr.DeliveredAt), CreatedAt: tr.CreatedAt,
		IsMultiDelivery: tr.IsMultiDelivery,
	}
	if req.Mode == ModeJobCategory || req.Mode == ModeManual {
		in.JobCategory = string(req.Category)
	}
	if tr.IsMultiDelivery {
		stops, err := q.TripStopsForPricing(ctx, id)
		if err != nil {
			return out, err
		}
		in.StopProgressCount = len(stops)
		for _, st := range stops {
			if st.Status == "delivered" && trimJS(st.DestinationRaw) != "" {
				in.DeliveredStops = append(in.DeliveredStops, st.DestinationRaw)
			}
		}
	}
	hubs, err := pricingHubMaps(ctx, q)
	if err != nil {
		return out, err
	}
	carrier, err := BillingCarrier(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("billing: the billing carrier (own fleet): %w", err)
	}
	out.Carrier = carrier
	tables := compute.Tables{Hubs: hubs}
	if party, ok := compute.ResolveTaskParty(in.Task.TaskParties); ok {
		pid, err := uuid.Parse(party)
		if err != nil {
			return out, fmt.Errorf("billing: task %s party %q: %w", task.ID, party, err)
		}
		basis, err := q.PartyBasis(ctx, pid)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		pt, err := LoadPricingTables(ctx, tx, pid, carrier)
		if err != nil {
			return out, err
		}
		tables.Basis, tables.Rates, tables.Fuel, tables.ExtraStopFeeTHB = compute.BillingDateBasis(basis), pt.Rates, pt.Fuel, pt.ExtraStopFeeTHB
	}
	res, err := compute.PriceTrip(in, tables)
	if errors.Is(err, compute.ErrMultiInsufficientStops) && req.Mode == ModeManual {
		err = nil // a manual price needs no reference: res holds the party and the billing date, no price
	}
	if errors.Is(err, compute.ErrMultiInsufficientStops) {
		out.Outcome = OutcomeMultiStops
		out.Result = ComputeResult{OK: false, Error: errorText(ReasonMultiInsufficientStops, "", "", "", true), Reason: ReasonMultiInsufficientStops}
		return out, keep()
	}
	if err != nil {
		return out, err
	}
	hub := compute.ExtractHubID(hubs.ResolveNameToCode(in.Task.SourceHub))
	dest := compute.NormalizeDestinationCode(hubs.ResolveNameToCode(in.Task.Destination))
	class, _ := compute.TripVehicleClass(in.Task.TruckType)
	multi := in.IsMultiDelivery && in.StopProgressCount >= 2

	if req.Mode == ModeManual {
		return writeManual(ctx, tx, q, tr, snap, hasSnap, req, res, carrier, hub, dest, out)
	}
	if res.Price == nil {
		out.Outcome, out.Reason = OutcomeUnpriced, res.Reason
		out.Result = ComputeResult{OK: false, Error: errorText(string(res.Reason), hub, dest, class, multi), Reason: string(res.Reason)}
		if req.Mode == ModeJobCategory {
			return out, nil // no rate for the explicit target: nothing written, the caller answers 422
		}
		return writeUnpriced(ctx, tx, q, tr, snap, hasSnap, req, res, carrier, hub, dest, multi, out)
	}

	// A priced row may move only out of and into open periods; so may any explicit admin write (§6.10 state 6).
	if out.WasPriced || req.Mode == ModeJobCategory {
		if l, ok, err := checkLocks(ctx, tx, tr, out.WasPriced, res.PartyID, res.BillingDate); err != nil {
			return out, err
		} else if ok {
			out.Outcome, out.Lock = OutcomeBlocked, &l
			out.Result = ComputeResult{OK: false, BlockedInvoiceNumber: l.InvoiceNumber, Error: lockedText(l), Reason: CodeBillingPeriodLocked}
			return out, keep()
		}
	}
	computedBy := req.ComputedBy
	if computedBy == "" {
		computedBy = defaultComputedBy(tr)
	}
	if req.Mode == ModeJobCategory {
		computedBy = ComputedBySetJobCategory
		if err := q.SetTaskJobCategory(ctx, billingdb.SetTaskJobCategoryParams{JobCategory: string(req.Category), ID: task.ID}); err != nil {
			return out, err
		}
	}
	p := res.Price
	if err := writePriced(ctx, tx, q, tr, req, res.PartyID, res.BillingDate, p, p.ManualOverride, computedBy, carrier, &out); err != nil {
		return out, err
	}
	return out, nil
}

// snapshotOf is the stored price the frozen rule reads.
func snapshotOf(s billingdb.TripSnapshotForPricingRow) compute.Snapshot {
	var fuel *string
	if s.FuelAdjustmentID != nil {
		v := s.FuelAdjustmentID.String()
		fuel = &v
	}
	m, a := s.RateMultiplier, s.AddThbPerTrip
	return compute.Snapshot{FuelAdjustmentID: fuel, RateMultiplier: &m, AddTHBPerTrip: &a, ManualOverride: s.ManualOverride}
}

func defaultComputedBy(tr billingdb.LockTripForPricingRow) string {
	if deref(tr.DeliveredVia) == "admin_web" {
		return ComputedByScheduler
	}
	return ComputedByMobile
}

// checkLocks is the period-lock read of a write that may move a price (§6.10 state 6, §6.11): the stored party and
// axis (billing_date, else delivered_at) of a priced row first, then the new party and billing date.
func checkLocks(ctx context.Context, tx pgx.Tx, tr billingdb.LockTripForPricingRow, wasPriced bool, newParty string,
	newDate time.Time) (compute.LockedPeriod, bool, error) {
	if wasPriced && tr.BillingPartyID != nil {
		old := instant(tr.BillingDate)
		if !presentInstant(old) {
			old = instant(tr.DeliveredAt)
		}
		if l, ok, err := PeriodLock(ctx, tx, *tr.BillingPartyID, old); err != nil || ok {
			return l, ok, err
		}
	}
	if p := uuidPtr(newParty); p != nil && presentInstant(newDate) {
		return PeriodLock(ctx, tx, *p, newDate)
	}
	return compute.LockedPeriod{}, false, nil
}

// restampAxis is §6.10 state 3 (restampFrozenBillingDate, fn:tripBillingOnDelivered.ts:228-270): a frozen price never
// moves, but its axis follows the plan date of a plan-basis party (else the delivery). Anything missing (party, task,
// target) leaves the trip as it is.
func restampAxis(ctx context.Context, tx pgx.Tx, q *billingdb.Queries, tr billingdb.LockTripForPricingRow,
	snap billingdb.TripSnapshotForPricingRow, req TripRequest, out TripOutcome) (TripOutcome, error) {
	price := *snap.EstimateThb
	out.Outcome, out.Result = OutcomeSkipped, ComputeResult{OK: true, Skipped: true, BillingEstimateTHB: &price}
	keep := func() error {
		if req.EventID > 0 {
			return q.SetSnapshotLastEvent(ctx, billingdb.SetSnapshotLastEventParams{LastEventID: req.EventID, TripID: tr.ID})
		}
		return nil
	}
	if tr.BillingPartyID == nil || tr.TaskID == nil {
		return out, keep()
	}
	task, err := q.LockTaskForPricing(ctx, *tr.TaskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, keep()
	}
	if err != nil {
		return out, err
	}
	basis, err := q.PartyBasis(ctx, *tr.BillingPartyID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	target := instant(tr.DeliveredAt)
	if compute.BillingDateBasis(basis) == compute.BasisPlan && presentInstant(task.PlanAt) {
		target = task.PlanAt
	}
	if !presentInstant(target) || (tr.BillingDate != nil && tr.BillingDate.UnixMilli() == target.UnixMilli()) {
		return out, keep()
	}
	old := instant(tr.BillingDate)
	if !presentInstant(old) {
		old = instant(tr.DeliveredAt)
	}
	if l, ok, err := PeriodLock(ctx, tx, *tr.BillingPartyID, old, target); err != nil {
		return out, err
	} else if ok {
		out.Outcome, out.Lock = OutcomeBlocked, &l
		out.Result = ComputeResult{OK: false, BlockedInvoiceNumber: l.InvoiceNumber, Error: lockedText(l), Reason: CodeBillingPeriodLocked}
		return out, keep()
	}
	if err := q.RestampTripBillingDate(ctx, billingdb.RestampTripBillingDateParams{BillingDate: target, ID: tr.ID}); err != nil {
		return out, err
	}
	carrier := snap.TenantID
	out.Carrier = carrier
	evID, err := tripEvent(ctx, tx, RouteTripRepriced, tr, carrier, map[string]any{"billingDateMoved": true, "priced": true}, req.RequestID)
	if err != nil {
		return out, err
	}
	if err := q.SetSnapshotLastEvent(ctx, billingdb.SetSnapshotLastEventParams{LastEventID: evID, TripID: tr.ID}); err != nil {
		return out, err
	}
	out.Outcome, out.Written = OutcomeRestamped, true
	out.Result.BillingDateMoved = true
	return out, nil
}

// writeUnpriced is §6.10 state 5: the reason on the snapshot of an unpriced trip (a priced one keeps its price, as the
// legacy writer did), the party and billing date stamped unless that would move the trip into (or a priced one out of)
// a sent or paid period, and one trip.priced event when anything was written.
func writeUnpriced(ctx context.Context, tx pgx.Tx, q *billingdb.Queries, tr billingdb.LockTripForPricingRow,
	snap billingdb.TripSnapshotForPricingRow, hasSnap bool, req TripRequest, res compute.TripResult, carrier uuid.UUID,
	hub, dest string, multi bool, out TripOutcome) (TripOutcome, error) {
	stamp := false
	if party := uuidPtr(res.PartyID); party != nil && presentInstant(res.BillingDate) {
		_, locked, err := checkLocks(ctx, tx, tr, out.WasPriced, res.PartyID, res.BillingDate)
		if err != nil {
			return out, err
		}
		same := tr.BillingPartyID != nil && *tr.BillingPartyID == *party && tr.BillingDate != nil &&
			tr.BillingDate.UnixMilli() == res.BillingDate.UnixMilli()
		stamp = !locked && !same
	}
	writeSnap := !out.WasPriced && (!hasSnap || deref(snap.UnpricedReason) != string(res.Reason))
	if !stamp && !writeSnap {
		if req.EventID > 0 && hasSnap {
			return out, q.SetSnapshotLastEvent(ctx, billingdb.SetSnapshotLastEventParams{LastEventID: req.EventID, TripID: tr.ID})
		}
		return out, nil
	}
	if stamp {
		bd := res.BillingDate
		if err := q.StampTripAxis(ctx, billingdb.StampTripAxisParams{PartyID: *uuidPtr(res.PartyID), BillingDate: &bd, ID: tr.ID}); err != nil {
			return out, err
		}
	}
	if out.WasPriced {
		// The price stays (the legacy writer never cleared it); only its axis moved.
		evID, err := tripEvent(ctx, tx, RouteTripRepriced, tr, snap.TenantID, map[string]any{"priced": true, "billingDateMoved": true},
			req.RequestID)
		if err != nil {
			return out, err
		}
		out.Written = true
		return out, q.SetSnapshotLastEvent(ctx, billingdb.SetSnapshotLastEventParams{LastEventID: evID, TripID: tr.ID})
	}
	evID, err := tripEvent(ctx, tx, RouteTripPriced, tr, carrier, map[string]any{"priced": false, "reason": string(res.Reason)}, req.RequestID)
	if err != nil {
		return out, err
	}
	out.Written = true
	computedBy := req.ComputedBy
	if computedBy == "" {
		computedBy = defaultComputedBy(tr)
	}
	params := billingdb.UpsertTripSnapshotParams{TripID: tr.ID, TenantID: carrier, UnpricedReason: textPtr(string(res.Reason)),
		RateMultiplier: "1.000000", AddThbPerTrip: "0.00", IsMultiDelivery: multi, ComputedBy: computedBy, LastEventID: &evID,
		ComputedAt: req.At}
	if cat, ok := compute.ExplicitJobCategory(string(req.Category)); ok {
		params.JobCategoryAtPricing = textPtr(string(cat))
	}
	if res.Reason == compute.NoRate && !multi { // the codes the lookup found no card for (the rows API shows them)
		params.LookupHubCode, params.LookupDestinationCode = textPtr(hub), textPtr(dest)
	}
	if err := q.UpsertTripSnapshot(ctx, params); err != nil {
		return out, err
	}
	return out, q.DeleteTripStopBreakdown(ctx, tr.ID)
}

// writePriced is §6.10 state 7: the full provenance set (single and multi-drop alike), the breakdown replaced, the
// trip's party, billing date and resolved หลัก/เสริม, and trip.priced (first price) or trip.repriced.
func writePriced(ctx context.Context, tx pgx.Tx, q *billingdb.Queries, tr billingdb.LockTripForPricingRow, req TripRequest,
	partyID string, billingDate time.Time, p *compute.TripPrice, manual bool, computedBy string, carrier uuid.UUID,
	out *TripOutcome) error {
	key, kind := RouteTripPriced, OutcomePriced
	if out.WasPriced {
		key, kind = RouteTripRepriced, OutcomeRepriced
	}
	evID, err := tripEvent(ctx, tx, key, tr, carrier, map[string]any{"priced": true}, req.RequestID)
	if err != nil {
		return err
	}
	party := uuidPtr(partyID)
	if party == nil {
		return fmt.Errorf("billing: trip %s priced without a party", tr.ID)
	}
	var bd *time.Time
	if presentInstant(billingDate) {
		b := billingDate
		bd = &b
	}
	cat := string(p.JobCategory)
	if err := q.StampTripAxis(ctx, billingdb.StampTripAxisParams{PartyID: *party, BillingDate: bd, JobCategory: textPtr(cat), ID: tr.ID}); err != nil {
		return err
	}
	params := billingdb.UpsertTripSnapshotParams{
		TripID: tr.ID, TenantID: carrier, EstimateThb: textPtr(money2(p.EstimateTHB)), BaseRateThb: textPtr(money2(p.BaseRateTHB)),
		RateEntryID: uuidPtr(p.RateEntryID), RateImportID: textPtr(p.RateImportID), LookupHubCode: textPtr(p.LookupHubCode),
		LookupDestinationCode: textPtr(p.LookupDestinationCode), FuelAdjustmentID: uuidPtr(p.FuelAdjustmentID),
		RateMultiplier: strconv.FormatFloat(p.RateMultiplier, 'f', 6, 64), AddThbPerTrip: money2(p.AddTHBPerTrip),
		FuelEffectiveFromDate: textPtr(p.FuelEffectiveFromDate), RoundEffectiveFromDate: textPtr(p.RoundEffectiveFromDate),
		FuelBandLowerThb: money2Ptr(p.FuelBandLowerTHB), FuelBandUpperThb: money2Ptr(p.FuelBandUpperTHB),
		ReferenceFuelPriceThb: money2Ptr(p.ReferenceFuelPriceTHB), IsMultiDelivery: p.IsMultiDelivery, ManualOverride: manual,
		JobCategoryAtPricing: textPtr(cat), ComputedBy: computedBy, LastEventID: &evID, ComputedAt: req.At,
	}
	if p.IsMultiDelivery {
		params.StopChargeThb = textPtr(money2(p.StopChargeTHB))
	}
	if err := q.UpsertTripSnapshot(ctx, params); err != nil {
		return err
	}
	if err := q.DeleteTripStopBreakdown(ctx, tr.ID); err != nil {
		return err
	}
	if len(p.Stops) > 0 {
		in := billingdb.InsertTripStopBreakdownParams{TripID: tr.ID}
		for _, st := range p.Stops {
			in.StopIndexes = append(in.StopIndexes, int32(st.StopIndex))
			in.DestinationCodes = append(in.DestinationCodes, st.DestinationCode)
			in.BaseRates = append(in.BaseRates, money2(st.BaseRateTHB))
			in.FinalRates = append(in.FinalRates, money2(st.FinalRateTHB))
		}
		if err := q.InsertTripStopBreakdown(ctx, in); err != nil {
			return err
		}
	}
	out.Outcome, out.Written, out.Carrier = kind, true, carrier
	out.Result = ComputeResult{OK: true, BillingEstimateTHB: round2Ptr(p.EstimateTHB)}
	return nil
}

// ManualTolerance is the difference from the reference price under which a manual price is not an override
// (EditBillingDialog.tsx:161).
const ManualTolerance = 0.001

// writeManual is PUT /v1/trips/{id}/billing/manual (EditBillingDialog.tsx:120-190, billing.ts:444-466): the admin's
// price, with the provenance of the reference price for the row's category when one exists. manual = no reference or a
// price further than ManualTolerance from it; manual_override = manual or SUPPLEMENTARY. A manual price on a multi-drop
// trip is a single amount (no breakdown: the old one would invoice stops the admin replaced); a price equal to the
// reference keeps the reference's breakdown.
func writeManual(ctx context.Context, tx pgx.Tx, q *billingdb.Queries, tr billingdb.LockTripForPricingRow,
	_ billingdb.TripSnapshotForPricingRow, _ bool, req TripRequest, res compute.TripResult, carrier uuid.UUID,
	hub, dest string, out TripOutcome) (TripOutcome, error) {
	if res.PartyID == "" {
		out.Outcome, out.Reason = OutcomeUnpriced, compute.NoCustomer
		out.Result = ComputeResult{OK: false, Error: errorText(string(compute.NoCustomer), "", "", "", false), Reason: string(compute.NoCustomer)}
		return out, nil
	}
	if l, ok, err := checkLocks(ctx, tx, tr, out.WasPriced, res.PartyID, res.BillingDate); err != nil {
		return out, err
	} else if ok {
		out.Outcome, out.Lock = OutcomeBlocked, &l
		out.Result = ComputeResult{OK: false, BlockedInvoiceNumber: l.InvoiceNumber, Error: lockedText(l), Reason: CodeBillingPeriodLocked}
		return out, nil
	}
	ref := res.Price
	manual := ref == nil || math.Abs(req.ManualTHB-jsmath.Round2(ref.EstimateTHB)) > ManualTolerance
	var p compute.TripPrice
	if ref != nil && !manual {
		p = *ref
	} else {
		p = compute.TripPrice{PartyID: res.PartyID, BaseRateTHB: req.ManualTHB, RateImportID: "manual", LookupHubCode: hub,
			LookupDestinationCode: dest, RateMultiplier: 1}
		if ref != nil { // the reference's provenance, the admin's amount (EditBillingDialog.tsx:166-176)
			p = *ref
			p.IsMultiDelivery, p.StopChargeTHB, p.Stops = false, 0, nil
			if ref.IsMultiDelivery {
				p.BaseRateTHB = req.ManualTHB
			}
		}
	}
	p.EstimateTHB = req.ManualTHB
	p.JobCategory = req.Category
	override := manual || req.Category == compute.Supplementary
	if err := writePriced(ctx, tx, q, tr, req, res.PartyID, res.BillingDate, &p, override, ComputedByManualEdit, carrier, &out); err != nil {
		return out, err
	}
	return out, nil
}

// pricingHubMaps reads the hub maps in the pricing transaction (main spec §6.4: PostgreSQL, never the Redis copy).
func pricingHubMaps(ctx context.Context, q *billingdb.Queries) (compute.HubMaps, error) {
	hubs, err := q.DisplayHubs(ctx)
	if err != nil {
		return compute.HubMaps{}, err
	}
	aliases, err := q.DisplayHubAliases(ctx)
	if err != nil {
		return compute.HubMaps{}, err
	}
	return hubMaps(hubs, aliases), nil
}

// trimJS trims like JavaScript's String.prototype.trim (the stops' destination test of the legacy gate).
func trimJS(s string) string { return compute.TrimJS(s) }
