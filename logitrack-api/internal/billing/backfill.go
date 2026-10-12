package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// The billing.backfill queue of T38 (main spec §6.13, Appendix B §B.5.3: group billing, prefetch 1): the trip and
// standby backfills (backfillTripBillingSnapshots, fn:tripBillingOnDelivered.ts:990-1180; backfillStandbyBillingSnapshots,
// fn:standbyBilling.ts:362-466) and the read-only impact report (billingImpactReport, fn:billingRoundMigration.ts:69),
// which held a 540 s callable open and are now jobs followed through GET /v1/jobs/{id}.

// QueueBackfill is the work queue of the three jobs.
const QueueBackfill = "billing.backfill"

// Bounds of a backfill or an impact report (the legacy callables' defaults and caps; Appendix B §B.2.20).
const (
	DefaultBackfillScan  = 500
	MaxBackfillScan      = 2000
	DefaultBackfillWrite = 200
	MaxBackfillWrite     = 500
	DefaultImpactScan    = 1000
	MaxImpactScan        = 5000
	// MaxBackfillFailures bounds result.failures (the legacy 25).
	MaxBackfillFailures = 25
)

// BillingJobParams is the body of POST /v1/jobs/billing.{backfill-trips,backfill-standby,impact-report} and, with the
// defaults filled in and PartyID resolved, the params of the job.
type BillingJobParams struct {
	FromDateStr    string `json:"fromDateStr"`
	ToDateStr      string `json:"toDateStr"`
	MaxScan        *int   `json:"maxScan,omitempty"`
	MaxWrite       *int   `json:"maxWrite,omitempty"`
	ForceRecompute bool   `json:"forceRecompute,omitempty"`
	CustomerID     string `json:"customerId,omitempty"`
	// PartyID is the billing party customerId names (a billing party, or the customer or carrier it bills), resolved at
	// submission under the caller's RLS; absent = every party.
	PartyID *uuid.UUID `json:"partyId,omitempty"`
}

func intPtr(n int) *int { return &n }

// SubmitBillingJob is POST /v1/jobs/billing.{backfill-trips,backfill-standby,impact-report} -> 202 {jobId}: one job of
// the own fleet (the billing carrier, R61) under lock:job:{type}:{partyId|all}, so a second run of the same type and
// scope is 409 already_exists with its jobId while the trips and the standby backfill of one month run side by side.
// fromDateStr / toDateStr are Bangkok days (both included); maxScan 1..2000 (default 500; impact report 1..5000,
// default 1000), maxWrite 1..500 (default 200); forceRecompute needs accounting:recompute_force. The impact report
// takes the dates and maxScan only.
func (s *Service) SubmitBillingJob(ctx context.Context, p *authz.Principal, typ string, in BillingJobParams) (jobs.Job, error) {
	tenant, err := billingTenantOf(p)
	if err != nil {
		return jobs.Job{}, err
	}
	var ck checker
	from, to := ck.date("fromDateStr", in.FromDateStr), ck.date("toDateStr", in.ToDateStr)
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		ck.add("toDateStr", "before_from")
	}
	impact := typ == jobs.TypeBillingImpactReport
	scanDefault, scanMax := DefaultBackfillScan, MaxBackfillScan
	if impact {
		scanDefault, scanMax = DefaultImpactScan, MaxImpactScan
		for field, set := range map[string]bool{"maxWrite": in.MaxWrite != nil, "forceRecompute": in.ForceRecompute,
			"customerId": strings.TrimSpace(in.CustomerID) != ""} {
			if set {
				ck.add(field, "not_allowed")
			}
		}
	}
	bound := func(field string, v *int, def, max int) *int {
		if v == nil {
			return intPtr(def)
		}
		if *v < 1 || *v > max {
			ck.addParams(field, "out_of_range", map[string]any{"min": 1, "max": max})
		}
		return v
	}
	params := BillingJobParams{FromDateStr: strings.TrimSpace(in.FromDateStr), ToDateStr: strings.TrimSpace(in.ToDateStr),
		MaxScan: bound("maxScan", in.MaxScan, scanDefault, scanMax), ForceRecompute: in.ForceRecompute}
	if !impact {
		params.MaxWrite = bound("maxWrite", in.MaxWrite, DefaultBackfillWrite, MaxBackfillWrite)
	}
	var ref uuid.UUID
	if c := strings.TrimSpace(in.CustomerID); c != "" && c != "all" && !impact {
		ref = ck.party("customerId", c)
	}
	if err := ck.err(); err != nil {
		return jobs.Job{}, err
	}
	if in.ForceRecompute && !p.Can(authz.AccountingRecomputeForce) {
		return jobs.Job{}, authz.ErrPermissionDenied("forceRecompute requires accounting:recompute_force", authz.AccountingRecomputeForce)
	}
	scope := "all"
	if ref != uuid.Nil {
		if err := s.tx(ctx, p, func(_ pgx.Tx, q *billingdb.Queries) error {
			r, err := writeParty(ctx, q, ref)
			if err == nil {
				id := r.ID
				params.PartyID, params.CustomerID, scope = &id, strings.TrimSpace(in.CustomerID), id.String()
			}
			return err
		}); err != nil {
			return jobs.Job{}, err
		}
	}
	if s.jobs == nil {
		return jobs.Job{}, httpx.ErrUnavailable("jobs are not available")
	}
	return s.jobs.Submit(ctx, jobs.SubmitInput{
		NewInput: jobs.NewInput{Type: typ, OwnerUserID: actor(p), TenantID: &tenant, Params: params, Scope: scope},
		Command:  true,
	})
}

// BackfillFailure is one failed row of a backfill (result.failures, at most 25).
type BackfillFailure struct {
	TripID    string `json:"tripId,omitempty"`
	StandbyID string `json:"standbyId,omitempty"`
	Error     string `json:"error,omitempty"`
}

// BackfillResult is the result of a finished backfill (the legacy callables' answer): scanned rows read, eligible
// (unpriced, or every row with forceRecompute), written, skipped (already priced, frozen, nothing to move), failed
// (unpriced or broken: the reason in failures), blocked (a sent or paid period, the invoice numbers in blockedInvoices)
// and capped (eligible rows left for another run by maxWrite).
type BackfillResult struct {
	Scanned         int               `json:"scanned"`
	Eligible        int               `json:"eligible"`
	Written         int               `json:"written"`
	Skipped         int               `json:"skipped"`
	Failed          int               `json:"failed"`
	Blocked         int               `json:"blocked"`
	BlockedInvoices []string          `json:"blockedInvoices"`
	Failures        []BackfillFailure `json:"failures"`
	Capped          bool              `json:"capped"`
}

func (r *BackfillResult) block(invoice string) {
	r.Blocked++
	if invoice != "" && !slices.Contains(r.BlockedInvoices, invoice) {
		r.BlockedInvoices = append(r.BlockedInvoices, invoice)
	}
}

func (r *BackfillResult) fail(f BackfillFailure) {
	r.Failed++
	if len(r.Failures) < MaxBackfillFailures {
		r.Failures = append(r.Failures, f)
	}
}

// JobDeps are the collaborators of the billing consumers.
type JobDeps struct {
	// Pool is the logitrack_app pool: the consumers price in db.WithSystem.
	Pool db.Beginner
	// Locks releases lock:job:{type}:{scope} when a backfill ends; optional.
	Locks jobs.Locker
	// Now is the clock of computed_at and of the safety net's window (default time.Now).
	Now func() time.Time
	Log zerolog.Logger
	// FirestorePricing reports a deployment whose FS->PG mirror runs (ETL_SYNC_ENABLED) while billing is not in
	// PG_OWNED_DOMAINS (P1-P2, main spec §6.13 coexistence): Firestore's Cloud Functions still price the trips and standby
	// records the mirror writes, and the mirror carries those prices into PostgreSQL, so billing.compute, the safety net
	// and the backfills leave them alone (OutcomeNotOwned, nothing written) and price only the records born in
	// PostgreSQL. False on the staging VM (PostgreSQL only, no mirror) and from P3 on, when Go is the only pricer.
	FirestorePricing bool
}

// BackfillJob is the billing.backfill consumer of the worker.
type BackfillJob struct {
	d JobDeps
}

// NewBackfillJob builds the consumer.
func NewBackfillJob(d JobDeps) (*BackfillJob, error) {
	if d.Pool == nil {
		return nil, errors.New("billing: the backfill job needs a pool")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &BackfillJob{d: d}, nil
}

// Registration is the consumer of queue billing.backfill (one job at a time).
func (j *BackfillJob) Registration() mq.Registration {
	return mq.Registration{Queue: QueueBackfill, Handler: j.Handle, Timeout: 30 * time.Minute}
}

// Handle runs one job command: the jobs row goes running, the rows are priced one transaction each (progress every 50
// rows), the job ends succeeded with its result, or failed with a cause when its parameters cannot run; lock:job is
// released at the end. A database failure outside the per-row transactions is retried by the ladder (a rerun skips the
// rows already priced, or reprices them identically when forced).
func (j *BackfillJob) Handle(ctx context.Context, d *mq.Delivery) error {
	typ := strings.TrimPrefix(d.RoutingKey, "job.")
	switch typ {
	case jobs.TypeBillingBackfillTrips, jobs.TypeBillingBackfillStandby, jobs.TypeBillingImpactReport:
	default:
		return mq.Permanent(fmt.Errorf("billing.backfill: unexpected routing key %s", d.RoutingKey))
	}
	cmd, err := jobs.DecodeCommand(d)
	if err != nil {
		return err
	}
	var job jobs.Job
	if err := db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		var err error
		job, err = jobs.Start(ctx, tx, cmd.JobID)
		return err
	}); errors.Is(err, jobs.ErrNotActive) {
		j.release(ctx, typ, cmd)
		return nil // a duplicate of a finished command
	} else if err != nil {
		return err
	}
	var params BillingJobParams
	var result any
	cause := ""
	if err := json.Unmarshal(job.Params, &params); err != nil {
		cause = "the job's parameters are not {fromDateStr, toDateStr, ...}"
	} else if from, to, ok := dayBounds(params.FromDateStr, params.ToDateStr); !ok {
		cause = "fromDateStr and toDateStr must be yyyy-MM-dd days, from on or before to"
	} else {
		switch typ {
		case jobs.TypeBillingBackfillTrips:
			result, err = j.backfillTrips(ctx, job, params, from, to)
		case jobs.TypeBillingBackfillStandby:
			result, err = j.backfillStandby(ctx, job, params, from, to)
		default:
			result, err = j.impactReport(ctx, params, from, to)
		}
		if err != nil {
			return err
		}
	}
	err = db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		var err error
		if cause != "" {
			_, err = jobs.Fail(ctx, tx, job.ID, cause, nil)
		} else {
			_, err = jobs.Succeed(ctx, tx, job.ID, result)
		}
		return err
	})
	if errors.Is(err, jobs.ErrNotActive) {
		err = nil
	}
	if err == nil {
		j.release(ctx, typ, cmd)
		j.d.Log.Info().Str("job_id", job.ID.String()).Str("type", typ).Str("failure", cause).Msg("billing.backfill")
	}
	return err
}

func (j *BackfillJob) release(ctx context.Context, typ string, cmd jobs.Command) {
	if j.d.Locks == nil || cmd.Scope == "" {
		return
	}
	if err := j.d.Locks.Release(context.WithoutCancel(ctx), j.d.Locks.JobKey(typ, cmd.Scope), cmd.JobID.String()); err != nil {
		j.d.Log.Warn().Err(err).Str("type", typ).Msg("billing.backfill: lock release failed; it expires on its own")
	}
}

// dayBounds is [from 00:00, the day after to 00:00) in Bangkok (the legacy T00:00:00+07:00 .. T23:59:59.999+07:00).
func dayBounds(fromStr, toStr string) (time.Time, time.Time, bool) {
	from, ok1 := clock.MidnightFromDateString(strings.TrimSpace(fromStr))
	to, ok2 := clock.MidnightFromDateString(strings.TrimSpace(toStr))
	if !ok1 || !ok2 || to.Before(from) {
		return time.Time{}, time.Time{}, false
	}
	return from, to.AddDate(0, 0, 1), true
}

// readOnly runs fn in a READ ONLY system transaction (the scans: a backfill reads its rows without a lock, each row is
// then locked by its own pricing transaction).
func readOnly(ctx context.Context, pool db.Beginner, fn func(q *billingdb.Queries) error) error {
	return db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			return err
		}
		return fn(billingdb.New(tx))
	})
}

// progress records {done,total} on the job every jobs.ProgressEvery rows (and job.updated for its owner).
func (j *BackfillJob) progress(ctx context.Context, id uuid.UUID, done, total int) {
	if done%jobs.ProgressEvery != 0 && done != total {
		return
	}
	if err := db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		_, err := jobs.SetProgress(ctx, tx, id, jobs.Progress{Done: done, Total: total})
		return err
	}); err != nil && !errors.Is(err, jobs.ErrNotActive) {
		j.d.Log.Warn().Err(err).Str("job_id", id.String()).Msg("billing.backfill: progress not recorded")
	}
}

func scanLimit(p *int, def int) int32 {
	if p == nil {
		return int32(def)
	}
	return int32(*p)
}

// backfillTrips scans delivered trips on the party's axis (billing_date for a single plan-basis party, else
// delivered_at) and prices the eligible ones, at most maxWrite written.
func (j *BackfillJob) backfillTrips(ctx context.Context, job jobs.Job, p BillingJobParams, from, to time.Time) (BackfillResult, error) {
	res := BackfillResult{BlockedInvoices: []string{}, Failures: []BackfillFailure{}}
	var rows []billingdb.BackfillTripsRow
	if err := readOnly(ctx, j.d.Pool, func(q *billingdb.Queries) error {
		in := billingdb.BackfillTripsParams{PeriodFrom: from, PeriodTo: to, MaxScan: scanLimit(p.MaxScan, DefaultBackfillScan)}
		if p.PartyID != nil {
			in.HasParty, in.PartyID = true, *p.PartyID
			basis, err := q.PartyBasis(ctx, *p.PartyID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			in.PlanAxis = compute.BillingDateBasis(basis) == compute.BasisPlan
		}
		var err error
		rows, err = q.BackfillTrips(ctx, in)
		return err
	}); err != nil {
		return res, err
	}
	res.Scanned = len(rows)
	var eligible []billingdb.BackfillTripsRow
	for _, r := range rows {
		if p.ForceRecompute || !r.Priced {
			eligible = append(eligible, r)
		}
	}
	res.Eligible = len(eligible)
	maxWrite := int(scanLimit(p.MaxWrite, DefaultBackfillWrite))
	req := TripRequest{Mode: ModeAuto, ComputedBy: ComputedByScheduler, SkipMirrored: j.d.FirestorePricing}
	if p.ForceRecompute {
		req.Mode, req.ComputedBy = ModeForce, ComputedByAdminForce
	}
	attempted := 0
	for i, r := range eligible {
		if res.Written >= maxWrite {
			continue
		}
		attempted++
		req.At = j.d.Now()
		var out TripOutcome
		err := db.WithSystem(cache.MoneyPath(ctx), j.d.Pool, nil, func(tx pgx.Tx) error {
			var err error
			out, err = PriceTrip(ctx, tx, r.ID, req)
			return err
		})
		switch {
		case err != nil && ctx.Err() != nil:
			return res, err
		case err != nil:
			j.d.Log.Error().Err(err).Str("trip_id", r.ID.String()).Msg("billing.backfill-trips: row failed")
			res.fail(BackfillFailure{TripID: r.ID.String(), Error: "internal error"})
		case out.Outcome == OutcomeBlocked:
			res.block(out.Lock.InvoiceNumber)
		case out.Outcome == OutcomePriced || out.Outcome == OutcomeRepriced:
			res.Written++
		case out.Result.OK:
			res.Skipped++
		default:
			res.fail(BackfillFailure{TripID: r.ID.String(), Error: out.Result.Error})
		}
		j.progress(ctx, job.ID, i+1, len(eligible))
	}
	res.Capped = res.Eligible > attempted
	return res, nil
}

// backfillStandby scans completed standby records by ended_at and prices the eligible ones.
func (j *BackfillJob) backfillStandby(ctx context.Context, job jobs.Job, p BillingJobParams, from, to time.Time) (BackfillResult, error) {
	res := BackfillResult{BlockedInvoices: []string{}, Failures: []BackfillFailure{}}
	var rows []billingdb.BackfillStandbyRow
	if err := readOnly(ctx, j.d.Pool, func(q *billingdb.Queries) error {
		in := billingdb.BackfillStandbyParams{PeriodFrom: from, PeriodTo: to, MaxScan: scanLimit(p.MaxScan, DefaultBackfillScan)}
		if p.PartyID != nil {
			in.HasParty, in.PartyID = true, *p.PartyID
		}
		var err error
		rows, err = q.BackfillStandby(ctx, in)
		return err
	}); err != nil {
		return res, err
	}
	res.Scanned = len(rows)
	var eligible []billingdb.BackfillStandbyRow
	for _, r := range rows {
		if p.ForceRecompute || !r.Priced {
			eligible = append(eligible, r)
		}
	}
	res.Eligible = len(eligible)
	maxWrite := int(scanLimit(p.MaxWrite, DefaultBackfillWrite))
	attempted := 0
	for i, r := range eligible {
		if res.Written >= maxWrite {
			continue
		}
		attempted++
		var out StandbyOutcome
		err := db.WithSystem(cache.MoneyPath(ctx), j.d.Pool, nil, func(tx pgx.Tx) error {
			var err error
			out, err = PriceStandby(ctx, tx, r.ID, StandbyRequest{Force: p.ForceRecompute, At: j.d.Now(), SkipMirrored: j.d.FirestorePricing})
			return err
		})
		switch {
		case err != nil && ctx.Err() != nil:
			return res, err
		case err != nil:
			j.d.Log.Error().Err(err).Str("standby_id", r.ID.String()).Msg("billing.backfill-standby: row failed")
			res.fail(BackfillFailure{StandbyID: r.ID.String(), Error: "internal error"})
		case out.Outcome == OutcomeBlocked:
			res.block(out.Lock.InvoiceNumber)
		case out.Outcome == OutcomePriced || out.Outcome == OutcomeRepriced:
			res.Written++
		case out.Result.OK:
			res.Skipped++
		default:
			res.fail(BackfillFailure{StandbyID: r.ID.String(), Error: out.Result.Error})
		}
		j.progress(ctx, job.ID, i+1, len(eligible))
	}
	res.Capped = res.Eligible > attempted
	return res, nil
}

// BandImpact is a fuel round whose band surcharge the corrected rule computes differently (ADR 0009 defect 1).
type BandImpact struct {
	AdjustmentID           string  `json:"adjustmentId"`
	CustomerID             string  `json:"customerId"`
	EffectiveFromDateStr   string  `json:"effectiveFromDateStr"`
	ReferenceFuelPriceTHB  float64 `json:"referenceFuelPriceThb"`
	StoredAddTHBPerTrip    float64 `json:"storedAddThbPerTrip"`
	CorrectedAddTHBPerTrip float64 `json:"correctedAddThbPerTrip"`
	DeltaTHBPerTrip        float64 `json:"deltaThbPerTrip"`
}

// BoundaryImpact is a trip delivered in the 00:00-06:59 ICT window of a switch day (ADR 0009 defect 2).
type BoundaryImpact struct {
	TripID             string `json:"tripId"`
	CustomerID         string `json:"customerId"`
	DeliveredAtISO     string `json:"deliveredAtIso"`
	PricedUnderDateStr string `json:"pricedUnderDateStr"`
	ShouldBeDateStr    string `json:"shouldBeDateStr"`
}

// ImpactReport is the result of billing.impact-report (ImpactReportResponse, fn:billingRoundMigration.ts:58-66).
type ImpactReport struct {
	OK                   bool             `json:"ok"`
	ScannedAnnouncements int              `json:"scannedAnnouncements"`
	ScannedTrips         int              `json:"scannedTrips"`
	BandImpacts          []BandImpact     `json:"bandImpacts"`
	BoundaryImpacts      []BoundaryImpact `json:"boundaryImpacts"`
	PeriodsAffected      []string         `json:"periodsAffected"`
}

// impactReport is billingImpactReport (ADR 0009, read-only by design): one READ ONLY transaction over the billing
// carrier's fuel rounds and rate cards and the delivered trips of the window; it writes nothing (no pricing, no lock).
// Customer ids are the ids the web names customers by (the customer or carrier a party bills). The default baseline
// floor and per-baht step are the legacy 41 and 10.
func (j *BackfillJob) impactReport(ctx context.Context, p BillingJobParams, from, to time.Time) (ImpactReport, error) {
	rep := ImpactReport{OK: true, BandImpacts: []BandImpact{}, BoundaryImpacts: []BoundaryImpact{}, PeriodsAffected: []string{}}
	err := readOnly(ctx, j.d.Pool, func(q *billingdb.Queries) error {
		carrier, err := q.OwnFleetTenant(ctx)
		if err != nil {
			return fmt.Errorf("billing: the billing carrier (own fleet): %w", err)
		}
		adjs, err := q.ImpactFuelAdjustments(ctx, carrier)
		if err != nil {
			return err
		}
		rates, err := q.ImpactRateEntryDays(ctx, carrier)
		if err != nil {
			return err
		}
		trips, err := q.ImpactTrips(ctx, billingdb.ImpactTripsParams{PeriodFrom: from, PeriodTo: to,
			MaxScan: scanLimit(p.MaxScan, DefaultImpactScan)})
		if err != nil {
			return err
		}
		rep.ScannedAnnouncements, rep.ScannedTrips = len(adjs)+len(rates), len(trips)
		switchDays := map[string]bool{}
		for _, a := range adjs {
			if a.Voided {
				continue
			}
			switchDays[clock.DateString(a.EffectiveFromAt)] = true
			if !a.FuelBandEnabled || a.ReferenceFuelPriceThb == nil {
				continue
			}
			baseline, perBaht := 41.0, 10.0
			if a.FuelBandBaselineFloor != nil {
				baseline = float64(*a.FuelBandBaselineFloor)
			}
			if a.FuelBandThbPerBaht != nil {
				perBaht = *a.FuelBandThbPerBaht
			}
			corrected := compute.FuelSurchargeTHB(*a.ReferenceFuelPriceThb, baseline, perBaht)
			if corrected == a.AddThbPerTrip {
				continue
			}
			rep.BandImpacts = append(rep.BandImpacts, BandImpact{AdjustmentID: a.ID.String(), CustomerID: a.BilledID.String(),
				EffectiveFromDateStr: a.EffectiveFromDate.Format(time.DateOnly), ReferenceFuelPriceTHB: *a.ReferenceFuelPriceThb,
				StoredAddTHBPerTrip: a.AddThbPerTrip, CorrectedAddTHBPerTrip: corrected,
				DeltaTHBPerTrip: jsmath.Round2(float64(corrected - a.AddThbPerTrip))})
		}
		for _, r := range rates {
			if !r.Voided {
				switchDays[clock.DateString(r.EffectiveFromAt)] = true
			}
		}
		for _, t := range trips {
			if t.DeliveredAt == nil || !presentInstant(*t.DeliveredAt) {
				continue
			}
			day := clock.DateString(*t.DeliveredAt)
			if !switchDays[day] || t.DeliveredAt.In(clock.Bangkok).Hour() >= 7 {
				continue
			}
			customer := ""
			switch {
			case t.PartyCustomerID != nil:
				customer = t.PartyCustomerID.String()
			case t.PartyTenantID != nil:
				customer = t.PartyTenantID.String()
			case t.PartyID != nil:
				customer = t.PartyID.String()
			}
			under := "(unknown — priced before ADR 0009)"
			if t.RoundEffectiveFromDate != nil {
				under = t.RoundEffectiveFromDate.Format(time.DateOnly)
			}
			rep.BoundaryImpacts = append(rep.BoundaryImpacts, BoundaryImpact{TripID: t.ID.String(), CustomerID: customer,
				DeliveredAtISO: t.DeliveredAt.UTC().Format("2006-01-02T15:04:05.000Z"), PricedUnderDateStr: under,
				ShouldBeDateStr: day})
		}
		return nil
	})
	if err != nil {
		return rep, err
	}
	periods := map[string]bool{}
	for _, b := range rep.BandImpacts {
		periods[b.CustomerID+":"+b.EffectiveFromDateStr[:7]] = true
	}
	for _, b := range rep.BoundaryImpacts {
		periods[b.CustomerID+":"+b.DeliveredAtISO[:7]] = true
	}
	for k := range periods {
		rep.PeriodsAffected = append(rep.PeriodsAffected, k)
	}
	slices.Sort(rep.PeriodsAffected)
	return rep, nil
}
