package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/billingdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/inbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// The billing.compute queue of T38 (main spec §6.13 compute-on-delivery, R17; Appendix B §B.5.3: group billing,
// prefetch 8). A committed producer transaction (the FS->PG mirror until P7b, the Go write paths after) appends
// trip.delivered, trip.delivered_at_changed, task.plan_date_changed, standby.completed or standby.customer_assigned; the
// relay publishes it; this consumer prices in one db.WithSystem transaction that starts with the consumer_inbox claim
// (a redelivery is acked), locks the trip (standby record) FOR UPDATE and drops a message whose outbox id is not above
// the record's last_event_id. The 15-minute job.billing.safety-net sweeps what no event priced. Permanent outcomes
// (unpriced reasons, multi_insufficient_stops, a missing task, a sent or paid period) are acked and counted in
// billing_compute_total{result}; a database failure is retried by the ladder and dead-lettered after the last rung.

// QueueCompute is the work queue of compute-on-delivery.
const QueueCompute = "billing.compute"

// Routing keys billing.compute consumes (Appendix B §B.5.2).
const (
	RouteTripDelivered         = "trip.delivered"
	RouteTripDeliveredAtChange = "trip.delivered_at_changed"
	RouteTaskPlanDateChanged   = "task.plan_date_changed"
	RouteStandbyCompleted      = "standby.completed"
	RouteSafetyNet             = "job." + jobs.TypeBillingSafetyNet
)

// Safety net (main spec §6.13: delivered trips and completed standby of the last 30 minutes, at most 100 each, as the
// legacy sweeps autoComputeBillingOnDelivery and autoComputeStandbyBilling).
const (
	SafetyNetWindow = 30 * time.Minute
	SafetyNetLimit  = 100
)

// ComputeJob is the billing.compute consumer of the worker.
type ComputeJob struct {
	d     JobDeps
	total *prometheus.CounterVec
}

// NewComputeJob builds the consumer; reg receives billing_compute_total{result} (nil: not exported).
func NewComputeJob(d JobDeps, reg prometheus.Registerer) (*ComputeJob, error) {
	if d.Pool == nil {
		return nil, errors.New("billing: the compute job needs a pool")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	total := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "billing_compute_total",
		Help: "Pricing runs of billing.compute by outcome (priced, repriced, restamped, unpriced, skipped, stale, blocked, " +
			"task_missing, multi_insufficient_stops, not_delivered, not_found)."}, []string{"result"})
	if reg != nil {
		if err := reg.Register(total); err != nil {
			if are, ok := errors.AsType[prometheus.AlreadyRegisteredError](err); ok {
				total = are.ExistingCollector.(*prometheus.CounterVec)
			} else {
				return nil, err
			}
		}
	}
	return &ComputeJob{d: d, total: total}, nil
}

// Registration is the consumer of queue billing.compute.
func (j *ComputeJob) Registration() mq.Registration {
	return mq.Registration{Queue: QueueCompute, Handler: j.Handle, Timeout: 5 * time.Minute}
}

// eventRef is what billing.compute reads of a thin payload: the record's id (consumers re-read state, §7.1).
type eventRef struct {
	ID uuid.UUID `json:"id"`
}

// Handle prices what one message names. trip.delivered runs ModeAuto; a moved delivery time or plan date reprices a
// priced trip (a frozen one only moves its billing date) and prices an unpriced one (ModeForce, states 3 and 6);
// standby events price an unpriced record (§6.13: events run auto).
func (j *ComputeJob) Handle(ctx context.Context, d *mq.Delivery) error {
	if d.RoutingKey == RouteSafetyNet {
		return j.safetyNet(ctx, d)
	}
	var ref eventRef
	if err := d.Decode(&ref); err != nil {
		return err
	}
	if ref.ID == uuid.Nil {
		return mq.Permanent(fmt.Errorf("billing.compute: %s without an id", d.RoutingKey))
	}
	ev := eventID(d.MessageID)
	_, err := inbox.Run(cache.MoneyPath(ctx), j.d.Pool, d, func(tx pgx.Tx) error {
		switch d.RoutingKey {
		case RouteTripDelivered:
			return j.trip(ctx, tx, ref.ID, TripRequest{Mode: ModeAuto, EventID: ev, At: j.d.Now()})
		case RouteTripDeliveredAtChange:
			return j.trip(ctx, tx, ref.ID, TripRequest{Mode: ModeForce, ComputedBy: ComputedByAdminForce, EventID: ev, At: j.d.Now()})
		case RouteTaskPlanDateChanged:
			ids, err := billingdb.New(tx).TaskDeliveredTrips(ctx, ref.ID)
			if err != nil {
				return err
			}
			for _, id := range ids {
				if err := j.trip(ctx, tx, id, TripRequest{Mode: ModeForce, ComputedBy: ComputedByAdminForce, EventID: ev, At: j.d.Now()}); err != nil {
					return err
				}
			}
			return nil
		case RouteStandbyCompleted, RouteStandbyCustomerAssigned:
			out, err := PriceStandby(ctx, tx, ref.ID, StandbyRequest{EventID: ev, At: j.d.Now()})
			if errors.Is(err, ErrRecordNotFound) {
				j.count(OutcomeNotFound)
				return nil
			}
			if err != nil {
				return err
			}
			j.count(out.Outcome)
			j.log(d, ref.ID, out.Outcome, string(out.Reason))
			return nil
		}
		return mq.Permanent(fmt.Errorf("billing.compute: unexpected routing key %s", d.RoutingKey))
	})
	return err
}

// OutcomeNotFound counts a message whose record no longer exists (acked).
const OutcomeNotFound = "not_found"

func (j *ComputeJob) trip(ctx context.Context, tx pgx.Tx, id uuid.UUID, req TripRequest) error {
	out, err := PriceTrip(ctx, tx, id, req)
	if errors.Is(err, ErrRecordNotFound) {
		j.count(OutcomeNotFound)
		return nil
	}
	if err != nil {
		return err
	}
	j.count(out.Outcome)
	reason := string(out.Reason)
	if out.Lock != nil {
		reason = out.Lock.InvoiceNumber
	}
	j.d.Log.Debug().Str("trip_id", id.String()).Int64("event_id", req.EventID).Str("result", out.Outcome).Str("reason", reason).
		Msg("billing.compute")
	return nil
}

func (j *ComputeJob) count(outcome string) {
	if outcome != "" {
		j.total.WithLabelValues(outcome).Inc()
	}
}

func (j *ComputeJob) log(d *mq.Delivery, id uuid.UUID, outcome, reason string) {
	j.d.Log.Debug().Str("routing_key", d.RoutingKey).Str("id", id.String()).Str("message_id", d.MessageID).
		Str("result", outcome).Str("reason", reason).Msg("billing.compute")
}

// SafetyNetResult is the result of a safety-net run: per record kind, the rows read and what pricing them did.
type SafetyNetResult struct {
	Trips   map[string]int `json:"trips"`
	Standby map[string]int `json:"standby"`
}

// safetyNet runs one job.billing.safety-net command (the scheduler's */15 cron, §7.4): the jobs row goes running, the
// unpriced delivered trips and completed standby records of the last SafetyNetWindow (at most SafetyNetLimit each) are
// priced one transaction each in ModeAuto, and the row ends succeeded with the counts. A duplicate of a finished command
// is acked.
func (j *ComputeJob) safetyNet(ctx context.Context, d *mq.Delivery) error {
	cmd, err := jobs.DecodeCommand(d)
	if err != nil {
		return err
	}
	if err := db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		_, err := jobs.Start(ctx, tx, cmd.JobID)
		return err
	}); errors.Is(err, jobs.ErrNotActive) {
		return nil
	} else if err != nil {
		return err
	}
	since := j.d.Now().Add(-SafetyNetWindow)
	var trips, standby []uuid.UUID
	if err := readOnly(ctx, j.d.Pool, func(q *billingdb.Queries) error {
		var err error
		if trips, err = q.SafetyNetTrips(ctx, billingdb.SafetyNetTripsParams{Since: since, RowLimit: SafetyNetLimit}); err != nil {
			return err
		}
		standby, err = q.SafetyNetStandby(ctx, billingdb.SafetyNetStandbyParams{Since: since, RowLimit: SafetyNetLimit})
		return err
	}); err != nil {
		return err
	}
	res := SafetyNetResult{Trips: map[string]int{"scanned": len(trips)}, Standby: map[string]int{"scanned": len(standby)}}
	for _, id := range trips {
		var out TripOutcome
		err := db.WithSystem(cache.MoneyPath(ctx), j.d.Pool, nil, func(tx pgx.Tx) error {
			var err error
			out, err = PriceTrip(ctx, tx, id, TripRequest{Mode: ModeAuto, ComputedBy: ComputedByScheduler, At: j.d.Now()})
			return err
		})
		switch {
		case errors.Is(err, ErrRecordNotFound):
			out.Outcome = OutcomeNotFound
		case err != nil:
			return err
		}
		res.Trips[out.Outcome]++
		j.count(out.Outcome)
	}
	for _, id := range standby {
		var out StandbyOutcome
		err := db.WithSystem(cache.MoneyPath(ctx), j.d.Pool, nil, func(tx pgx.Tx) error {
			var err error
			out, err = PriceStandby(ctx, tx, id, StandbyRequest{At: j.d.Now()})
			return err
		})
		switch {
		case errors.Is(err, ErrRecordNotFound):
			out.Outcome = OutcomeNotFound
		case err != nil:
			return err
		}
		res.Standby[out.Outcome]++
		j.count(out.Outcome)
	}
	j.d.Log.Info().Int("trips", len(trips)).Int("standby", len(standby)).Msg("billing.safety-net")
	return db.WithSystem(ctx, j.d.Pool, nil, func(tx pgx.Tx) error {
		_, err := jobs.Succeed(ctx, tx, cmd.JobID, res)
		if errors.Is(err, jobs.ErrNotActive) {
			return nil
		}
		return err
	})
}
