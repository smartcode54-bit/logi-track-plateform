// Package health serves liveness (/healthz), readiness (/readyz) and startup
// (/startupz) probes. Readiness turns 503 as soon as shutdown begins so traffic
// drains before the listeners close.
package health

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Checker is a dependency probe (database, Redis, RabbitMQ, ...). Later tasks
// register one per dependency; T01 has none.
type Checker interface {
	Name() string
	Check(ctx context.Context) error
}

// CheckTimeout bounds every readiness probe run.
const CheckTimeout = 2 * time.Second

// State tracks the process lifecycle seen by the probes.
type State struct {
	started  atomic.Bool
	draining atomic.Bool

	mu       sync.RWMutex
	checkers []Checker
}

// NewState returns a state that is neither started nor draining.
func NewState() *State { return &State{} }

// Register adds readiness checkers.
func (s *State) Register(c ...Checker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkers = append(s.checkers, c...)
}

// MarkStarted flips /startupz to 200 once the listeners are bound.
func (s *State) MarkStarted() { s.started.Store(true) }

// BeginDrain flips /readyz to 503; it is irreversible.
func (s *State) BeginDrain() { s.draining.Store(true) }

// Draining reports whether shutdown has begun.
func (s *State) Draining() bool { return s.draining.Load() }

// Healthz is liveness: 200 while the process can serve HTTP at all.
func (s *State) Healthz(c fiber.Ctx) error {
	return httpx.JSON(c, http.StatusOK, map[string]string{"status": "ok"})
}

// Startupz is 200 once startup completed, 503 before.
func (s *State) Startupz(c fiber.Ctx) error {
	if !s.started.Load() {
		return httpx.ErrUnavailable("starting").WithDetails(map[string]any{"reason": "starting"})
	}
	return httpx.JSON(c, http.StatusOK, map[string]string{"status": "started"})
}

// Readyz is 200 when started, not draining and every checker passes.
func (s *State) Readyz(c fiber.Ctx) error {
	if err := s.Ready(c.Context()); err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, map[string]string{"status": "ready"})
}

// Ready is the decision of Readyz without HTTP, for the processes that answer /readyz on
// METRICS_ADDR (worker, scheduler): nil when ready, else a 503 unavailable *httpx.Error whose
// details give the reason (draining, starting, or dependencies with the failed checks).
func (s *State) Ready(ctx context.Context) error {
	if s.draining.Load() {
		return httpx.ErrUnavailable("draining").WithDetails(map[string]any{"reason": "draining"})
	}
	if !s.started.Load() {
		return httpx.ErrUnavailable("starting").WithDetails(map[string]any{"reason": "starting"})
	}
	if failed := s.run(ctx); len(failed) > 0 {
		return httpx.ErrUnavailable("dependency check failed").
			WithDetails(map[string]any{"reason": "dependencies", "checks": failed})
	}
	return nil
}

// run executes all checkers concurrently and returns name -> error text for
// the failures.
func (s *State) run(parent context.Context) map[string]string {
	s.mu.RLock()
	checkers := append([]Checker(nil), s.checkers...)
	s.mu.RUnlock()
	if len(checkers) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, CheckTimeout)
	defer cancel()

	var mu sync.Mutex
	failed := map[string]string{}
	var wg sync.WaitGroup
	for _, ch := range checkers {
		wg.Go(func() {
			if err := ch.Check(ctx); err != nil {
				mu.Lock()
				failed[ch.Name()] = err.Error()
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return failed
}
