package mq

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"
)

// Backoff bounds of Supervise: the wait doubles from Min to Max while the broker is down and resets
// after a connection lived longer than Max.
const (
	BackoffMin = time.Second
	BackoffMax = 30 * time.Second
)

// ConnProbe tells readiness whether a supervised session holds an open connection: the session sets
// it once the connection is usable and clears it when the session ends.
type ConnProbe struct {
	conn atomic.Pointer[amqp.Connection]
}

// Set records the connection of the running session.
func (p *ConnProbe) Set(c *amqp.Connection) { p.conn.Store(c) }

// Clear forgets it.
func (p *ConnProbe) Clear() { p.conn.Store(nil) }

// Check fails while no session holds an open connection (health.Checker style).
func (p *ConnProbe) Check(context.Context) error {
	if c := p.conn.Load(); c == nil || c.IsClosed() {
		return errors.New("mq: no open connection")
	}
	return nil
}

// Supervise keeps one connection open until ctx ends: it dials, runs fn with a context that ends
// when the connection closes, and dials again with backoff after a failure. fn returns when its
// context ends; Supervise closes the connection afterwards. It returns when ctx ends.
func Supervise(ctx context.Context, log zerolog.Logger, dial func() (*amqp.Connection, error),
	fn func(ctx context.Context, conn *amqp.Connection) error) {
	wait := BackoffMin
	for ctx.Err() == nil {
		start := time.Now()
		conn, err := dial()
		if err != nil {
			log.Warn().Err(err).Dur("retry_in", wait).Msg("rabbitmq unavailable")
		} else {
			cctx, cancel := context.WithCancel(ctx)
			closed := conn.NotifyClose(make(chan *amqp.Error, 1))
			go func() {
				select {
				case e := <-closed:
					if e != nil {
						log.Warn().Str("reason", e.Reason).Int("code", e.Code).Msg("rabbitmq connection closed")
					}
					cancel()
				case <-cctx.Done():
				}
			}()
			if err := fn(cctx, conn); err != nil && ctx.Err() == nil {
				log.Warn().Err(err).Msg("rabbitmq session ended")
			}
			cancel()
			_ = conn.Close()
			if time.Since(start) > BackoffMax {
				wait = BackoffMin
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, BackoffMax)
	}
}
