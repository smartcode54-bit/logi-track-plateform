//go:build integration

package sse

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	rt "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// deadlineConn records the write deadlines a stream sets on its connection.
type deadlineConn struct {
	net.Conn
	mu   sync.Mutex
	last time.Time
	n    int
}

func (c *deadlineConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last, c.n = t, c.n+1
	return nil
}

type unusedAuth struct{}

func (unusedAuth) SSETicketPrincipal(context.Context, string) (*authz.Principal, error) {
	return nil, errors.New("unused")
}
func (unusedAuth) CheckSession(context.Context, *authz.Principal) error { return nil }

type unusedPool struct{}

func (unusedPool) Begin(context.Context) (pgx.Tx, error) { return nil, errors.New("unused") }

// The stream's last act on its connection bounds fasthttp's final write (the frames still in the
// stream-writer pipe and the terminating chunk) instead of clearing the deadline: a cleared deadline
// left that write unbounded for a client that stopped reading, so the connection, its goroutine and the
// drain waited past SHUTDOWN_TIMEOUT.
func TestStreamLeavesItsFinalWriteBounded(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	hub := rt.NewHub(rdb, ks, zerolog.Nop(), 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = hub.Run(ctx) }()
	for deadline := time.Now().Add(10 * time.Second); !hub.Ready(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the hub never confirmed its PSUBSCRIBE")
		}
	}
	svc, err := New(Config{PingInterval: time.Hour, MaxConnPerUser: 5}, Deps{
		Hub: hub, Reader: rt.NewReader(rdb, ks, 0), Conns: ratelimit.NewConnLimiter(rdb, ks, 5, time.Minute),
		Tickets: unusedAuth{}, Sessions: unusedAuth{}, Pool: unusedPool{}, Log: zerolog.Nop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := hub.Subscribe([]string{rt.TopicGlobal})
	if err != nil {
		t.Fatal(err)
	}
	conn := &deadlineConn{}
	st := &stream{s: svc, kind: KindWeb, userID: uuid.NewString(), streamID: uuid.NewString(), sub: sub,
		life: time.Hour, conn: conn, log: zerolog.Nop()}
	svc.open.WithLabelValues(KindWeb).Inc()
	done := make(chan struct{})
	go func() { st.run(bufio.NewWriter(io.Discard)); close(done) }()
	hub.Drain() // the stream writes reconnect shutdown and ends
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream did not end on drain")
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.n < 2 || conn.last.IsZero() || !conn.last.After(time.Now()) || conn.last.After(time.Now().Add(WriteTimeout)) {
		t.Fatalf("after %d deadlines the last is %v: the final write must stay bounded by WriteTimeout", conn.n, conn.last)
	}
}
