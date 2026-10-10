package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs/jobsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// ReplayKeyPrefix is the first word of the lt.requeue routing key of a replayed message: the work
// queue's own binding *.{queue} receives it, and no other queue does.
const ReplayKeyPrefix = "replay"

// finishTimeout bounds the transaction that records a replay's outcome after its context ended.
const finishTimeout = 30 * time.Second

// Replayer runs queue.replay jobs in the leader scheduler (main spec §7.2, Appendix B §B.5.4): it moves
// the messages that were in {queue}.dead when the job started back to {queue} through lt.requeue with
// a fresh retry budget, publishing each with a confirm before acking it in the dead queue.
type Replayer struct {
	pool     db.Beginner
	topology mq.Topology
	locks    Locker // releases lock:job:queue.replay:{queue} when a job ends; nil = none
	log      zerolog.Logger
}

// NewReplayer builds a replayer for the topology's queues.
func NewReplayer(pool db.Beginner, t mq.Topology, locks Locker, log zerolog.Logger) *Replayer {
	return &Replayer{pool: pool, topology: t, locks: locks, log: log}
}

// Run polls for queued replay jobs every interval until ctx ends.
func (r *Replayer) Run(ctx context.Context, conn *amqp.Connection, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		for {
			ran, err := r.RunOnce(ctx, conn)
			if err != nil && ctx.Err() == nil {
				r.log.Warn().Err(err).Msg("queue replay failed")
			}
			if !ran || err != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// RunOnce claims the oldest queued replay job and runs it; ran is false when there was none.
func (r *Replayer) RunOnce(ctx context.Context, conn *amqp.Connection) (ran bool, err error) {
	var job Job
	err = db.WithSystem(ctx, r.pool, nil, func(tx pgx.Tx) error {
		row, err := jobsdb.New(tx).ClaimQueuedJob(ctx, TypeQueueReplay)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ran = true
		job, err = Start(ctx, tx, row.ID)
		return err
	})
	if err != nil || !ran {
		return ran, err
	}
	moved, runErr := r.move(ctx, conn, job)
	result := map[string]any{"moved": moved}
	// Record the outcome even when the leader's context ended mid-run (SIGTERM, step-down, or the
	// broker connection closing under Supervise): the row must not stay running, because nothing
	// claims a running replay again. Every statement of the finishing transaction uses fctx.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	err = db.WithSystem(fctx, r.pool, nil, func(tx pgx.Tx) error {
		if runErr != nil {
			_, err := Fail(fctx, tx, job.ID, runErr.Error(), result)
			return err
		}
		_, err := Succeed(fctx, tx, job.ID, result)
		return err
	})
	if r.locks != nil {
		var p ReplayParams
		if json.Unmarshal(job.Params, &p) == nil {
			_ = r.locks.Release(fctx, r.locks.JobKey(TypeQueueReplay, p.Queue), job.ID.String())
		}
	}
	r.log.Info().Str("job_id", job.ID.String()).Int("moved", moved).AnErr("error", runErr).Msg("queue replay finished")
	return true, errors.Join(runErr, err)
}

func (r *Replayer) move(ctx context.Context, conn *amqp.Connection, job Job) (int, error) {
	var p ReplayParams
	if err := json.Unmarshal(job.Params, &p); err != nil {
		return 0, fmt.Errorf("replay params: %w", err)
	}
	if _, ok := r.topology.Queue(p.Queue); !ok {
		return 0, fmt.Errorf("replay: %q is not a work queue", p.Queue)
	}
	ch, err := conn.Channel()
	if err != nil {
		return 0, err
	}
	defer func() { _ = ch.Close() }()
	pub, err := mq.NewPublisher(conn)
	if err != nil {
		return 0, err
	}
	defer func() { _ = pub.Close() }()
	dead := mq.DeadQueue(p.Queue)
	info, err := ch.QueueDeclarePassive(dead, true, false, false, false, nil)
	if err != nil {
		return 0, fmt.Errorf("replay: inspect %s: %w", dead, err)
	}
	// Only what was there at the start: a message that fails again comes back after its new retries.
	total, moved := info.Messages, 0
	for moved < total {
		d, ok, err := ch.Get(dead, false)
		if err != nil {
			return moved, fmt.Errorf("replay: get from %s: %w", dead, err)
		}
		if !ok {
			break
		}
		if err := pub.Publish(ctx, mq.ExchangeRequeue, ReplayKeyPrefix+"."+p.Queue, replayed(d)); err != nil {
			_ = d.Nack(false, true)
			return moved, fmt.Errorf("replay: publish: %w", err)
		}
		if err := d.Ack(false); err != nil {
			return moved, fmt.Errorf("replay: ack: %w", err)
		}
		moved++
		if moved%ProgressEvery == 0 {
			if err := db.WithSystem(ctx, r.pool, nil, func(tx pgx.Tx) error {
				_, err := SetProgress(ctx, tx, job.ID, Progress{Done: moved, Total: total})
				return err
			}); err != nil {
				return moved, err
			}
		}
	}
	return moved, nil
}

// replayed copies a dead message for its work queue: the retry budget starts again (x-attempts and
// the broker's x-death history are dropped) and the original exchange and routing key stay in
// headers, recovered from x-death when the message never went through a retry.
func replayed(d amqp.Delivery) amqp.Publishing {
	h := amqp.Table{}
	for k, v := range d.Headers {
		h[k] = v
	}
	if _, ok := h[mq.HeaderOriginalRoutingKey]; !ok {
		if ex, key, ok := firstDeath(d.Headers); ok {
			h[mq.HeaderOriginalExchange], h[mq.HeaderOriginalRoutingKey] = ex, key
		}
	}
	delete(h, mq.HeaderAttempts)
	delete(h, "x-death")
	delete(h, "x-first-death-exchange")
	delete(h, "x-first-death-queue")
	delete(h, "x-first-death-reason")
	delete(h, "x-last-death-exchange")
	delete(h, "x-last-death-queue")
	delete(h, "x-last-death-reason")
	h["x-replayed-at"] = time.Now().UTC().Format(time.RFC3339)
	return amqp.Publishing{
		Headers: h, ContentType: d.ContentType, MessageId: d.MessageId, Timestamp: d.Timestamp,
		Type: d.Type, AppId: d.AppId, Body: d.Body,
	}
}

// firstDeath reads the exchange and routing key a message had before its first dead-lettering.
func firstDeath(h amqp.Table) (string, string, bool) {
	deaths, ok := h["x-death"].([]any)
	if !ok || len(deaths) == 0 {
		return "", "", false
	}
	last, ok := deaths[len(deaths)-1].(amqp.Table) // x-death is newest first
	if !ok {
		return "", "", false
	}
	ex, _ := last["exchange"].(string)
	keys, _ := last["routing-keys"].([]any)
	if ex == "" || len(keys) == 0 {
		return "", "", false
	}
	key, _ := keys[0].(string)
	return ex, key, key != ""
}
