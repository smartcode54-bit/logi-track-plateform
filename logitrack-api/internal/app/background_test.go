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
)

// freeAddr reserves a loopback port for METRICS_ADDR: the processes log their bound address, so a
// test that probes /readyz picks it beforehand.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

type readiness struct {
	status int
	checks map[string]any
}

func probeReady(addr string) (readiness, bool) {
	resp, err := http.Get("http://" + addr + "/readyz")
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

// waitReadiness polls /readyz until ok accepts the answer.
func waitReadiness(t *testing.T, addr string, within time.Duration, ok func(readiness) bool) readiness {
	t.Helper()
	deadline := time.Now().Add(within)
	var last readiness
	for time.Now().Before(deadline) {
		if r, answered := probeReady(addr); answered {
			last = r
			if ok(r) {
				return r
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("/readyz on %s: last answer %d %v", addr, last.status, last.checks)
	return last
}

// startProcess runs main until the returned stop function, which waits for exit code 0.
func startProcess(t *testing.T, main func(context.Context, io.Writer, io.Writer) int) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	code := make(chan int, 1)
	go func() { code <- main(ctx, io.Discard, io.Discard) }()
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
// §B.2.1): unreachable PostgreSQL, RabbitMQ or Redis answer 503 unavailable with details.checks. A
// scheduler that is not the leader holds no broker connection, so RabbitMQ is not among its checks.
// Placeholder URLs on closed loopback ports: nothing is reached.
func TestBackgroundReadinessReportsDependencies(t *testing.T) {
	for k, v := range map[string]string{
		"APP_ENV": "local", "LOG_LEVEL": "error", "SHUTDOWN_TIMEOUT": "2s",
		"DATABASE_URL": "postgres://placeholder@127.0.0.1:1/placeholder?sslmode=disable&connect_timeout=1",
		"RABBITMQ_URL": "amqp://127.0.0.1:2/", "REDIS_URL": "redis://127.0.0.1:3/0",
		"WORKER_CONSUMERS": "notify",
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

	addr := freeAddr(t)
	t.Setenv("METRICS_ADDR", addr)
	stop := startProcess(t, app.RunWorker)
	waitReadiness(t, addr, 10*time.Second, unreachable("postgres", "rabbitmq"))
	stop()

	addr = freeAddr(t)
	t.Setenv("METRICS_ADDR", addr)
	stop = startProcess(t, app.RunScheduler)
	waitReadiness(t, addr, 10*time.Second, unreachable("postgres", "redis"))
	stop()
}
