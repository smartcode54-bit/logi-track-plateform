package scheduler

import (
	"context"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
)

// ReplayPollInterval is how often the leader looks for queued queue.replay jobs.
const ReplayPollInterval = 2 * time.Second

// Scheduler is the leader's work: the cron table, and on one RabbitMQ connection the outbox relay
// and the queue replays. Every part is optional (nil), so tests can run one of them.
type Scheduler struct {
	Leader   *Leader
	Cron     *Cron
	Dial     func() (*amqp.Connection, error)
	NewRelay func(conn *amqp.Connection) *outbox.Relay
	Replayer *jobs.Replayer
	// AMQP, when set, holds the leader's broker connection while a session runs (readiness).
	AMQP *mq.ConnProbe
	Log  zerolog.Logger
}

// Run campaigns for leadership until ctx ends and runs the leader's work while it holds the lock.
func (s *Scheduler) Run(ctx context.Context) {
	s.Leader.Run(ctx, s.lead)
}

func (s *Scheduler) lead(ctx context.Context) {
	var wg sync.WaitGroup
	if s.Cron != nil {
		wg.Go(func() { s.Cron.Run(ctx) })
	}
	if s.Dial != nil && (s.NewRelay != nil || s.Replayer != nil) {
		wg.Go(func() {
			mq.Supervise(ctx, s.Log, s.Dial, func(cctx context.Context, conn *amqp.Connection) error {
				if s.AMQP != nil {
					s.AMQP.Set(conn)
					defer s.AMQP.Clear()
				}
				var inner sync.WaitGroup
				if s.NewRelay != nil {
					relay := s.NewRelay(conn)
					inner.Go(func() { _ = relay.Run(cctx) })
				}
				if s.Replayer != nil {
					inner.Go(func() { s.Replayer.Run(cctx, conn, ReplayPollInterval) })
				}
				inner.Wait()
				return nil
			})
		})
	}
	wg.Wait()
}
