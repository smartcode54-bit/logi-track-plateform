package app_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/listentest"
)

// metricsListener binds METRICS_ADDR for a worker or scheduler up front and names it in the env: the test
// probes /readyz there while the process starts, and the process serves the listener as it is (FLAKE3: a port
// picked, released and bound again later can be taken in between).
func metricsListener(t *testing.T) net.Listener {
	t.Helper()
	ln := listentest.Loopback(t)
	t.Setenv("METRICS_ADDR", ln.Addr().String())
	return ln
}

type readiness struct {
	status int
	checks map[string]any
}

// probeReady asks /readyz on addr once; ctx bounds the request. A listener bound before the process serves it
// (metricsListener) takes the connection into its backlog, so a request without a deadline would wait for as
// long as the process takes to build, or forever if it hangs there.
func probeReady(ctx context.Context, addr string) (readiness, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/readyz", nil)
	if err != nil {
		return readiness{}, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return readiness{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	r := readiness{status: resp.StatusCode}
	if resp.StatusCode != http.StatusOK && body.Error.Code != "unavailable" {
		return r, false
	}
	r.checks, _ = body.Error.Details["checks"].(map[string]any)
	return r, true
}

// pollReadiness polls /readyz until ok accepts an answer (true) or within has passed (false, with the last
// answer); within bounds each request too.
func pollReadiness(addr string, within time.Duration, ok func(readiness) bool) (readiness, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	var last readiness
	for ctx.Err() == nil {
		if r, answered := probeReady(ctx, addr); answered {
			last = r
			if ok(r) {
				return r, true
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	return last, false
}

// waitReadiness polls /readyz until ok accepts the answer, and fails the test once within has passed.
func waitReadiness(t *testing.T, addr string, within time.Duration, ok func(readiness) bool) readiness {
	t.Helper()
	r, done := pollReadiness(addr, within, ok)
	if !done {
		t.Fatalf("/readyz on %s: last answer %d %v", addr, r.status, r.checks)
	}
	return r
}

// The deadline of waitReadiness holds while nothing serves the bound listener: the process may hang before it
// serves METRICS_ADDR, and the test must then fail at its deadline, not at go test's -timeout.
func TestWaitReadinessKeepsItsDeadline(t *testing.T) {
	ln := listentest.Loopback(t) // bound, never served: connections wait in the backlog
	const within = 300 * time.Millisecond
	type result struct {
		done    bool
		elapsed time.Duration
	}
	res := make(chan result, 1)
	go func() {
		start := time.Now()
		_, done := pollReadiness(ln.Addr().String(), within, func(readiness) bool { return true })
		res <- result{done, time.Since(start)}
	}()
	select {
	case r := <-res:
		if r.done {
			t.Fatal("pollReadiness accepted an answer nothing gave")
		}
		if r.elapsed > within+2*time.Second {
			t.Fatalf("pollReadiness returned after %v, deadline %v", r.elapsed, within)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("pollReadiness still waits after 10s on an unserved listener, deadline %v", within)
	}
}

// startProcess runs main with /metrics on metrics until the returned stop function, which waits for exit
// code 0.
func startProcess(t *testing.T, main func(context.Context, net.Listener, io.Writer, io.Writer) int, metrics net.Listener) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // a test that fails before stop does not leave the process running
	code := make(chan int, 1)
	go func() { code <- main(ctx, metrics, io.Discard, io.Discard) }()
	return func() {
		t.Helper()
		cancel()
		select {
		case c := <-code:
			if c != app.ExitOK {
				t.Fatalf("process exit %d", c)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("the process did not stop")
		}
	}
}

// The worker's and the scheduler's /readyz on METRICS_ADDR check their dependencies (Appendix B
// §B.2.1): unreachable PostgreSQL, RabbitMQ or Redis answer 503 unavailable with details.checks (the
// worker's Redis holds notify.fcm's idem: keys, T13). A
// scheduler that is not the leader holds no broker connection, so RabbitMQ is not among its checks.
// Placeholder URLs on closed loopback ports: nothing is reached.
func TestBackgroundReadinessReportsDependencies(t *testing.T) {
	for k, v := range map[string]string{
		"APP_ENV": "local", "LOG_LEVEL": "error", "SHUTDOWN_TIMEOUT": "2s",
		"DATABASE_URL": "postgres://placeholder@127.0.0.1:1/placeholder?sslmode=disable&connect_timeout=1",
		"RABBITMQ_URL": "amqp://127.0.0.1:2/", "REDIS_URL": "redis://127.0.0.1:3/0",
		"WORKER_CONSUMERS": "notify",
		"S3_ENDPOINT":      "http://127.0.0.1:4", "S3_REGION": "us-east-1", "S3_ACCESS_KEY_ID": "placeholder",
		"S3_SECRET_ACCESS_KEY": "placeholder",
	} {
		t.Setenv(k, v)
	}
	unreachable := func(names ...string) func(readiness) bool {
		return func(r readiness) bool {
			if r.status != http.StatusServiceUnavailable || len(r.checks) != len(names) {
				return false
			}
			for _, n := range names {
				if r.checks[n] != "unreachable" {
					return false
				}
			}
			return true
		}
	}

	ln := metricsListener(t)
	stop := startProcess(t, app.RunWorkerOn, ln)
	waitReadiness(t, ln.Addr().String(), 10*time.Second, unreachable("postgres", "redis", "rabbitmq"))
	stop()

	ln = metricsListener(t)
	stop = startProcess(t, app.RunSchedulerOn, ln)
	waitReadiness(t, ln.Addr().String(), 10*time.Second, unreachable("postgres", "redis"))
	stop()
}
