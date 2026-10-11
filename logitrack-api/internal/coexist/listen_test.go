package coexist

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// memLease is a lease with a TTL in memory; each view is one holder.
type memLease struct {
	mu      sync.Mutex
	holder  string
	expires time.Time
	ttl     time.Duration
	takes   []string // the holder of each acquisition of a free lease
}

type leaseView struct {
	l  *memLease
	id string
}

func (v leaseView) Acquire(context.Context) (bool, error) {
	v.l.mu.Lock()
	defer v.l.mu.Unlock()
	now := time.Now()
	if v.l.holder != "" && v.l.holder != v.id && now.Before(v.l.expires) {
		return false, nil
	}
	if v.l.holder != v.id || !now.Before(v.l.expires) {
		v.l.takes = append(v.l.takes, v.id)
	}
	v.l.holder, v.l.expires = v.id, now.Add(v.l.ttl)
	return true, nil
}

func (v leaseView) Release(context.Context) error {
	v.l.mu.Lock()
	defer v.l.mu.Unlock()
	if v.l.holder == v.id {
		v.l.holder = ""
	}
	return nil
}

// slowPool answers the login check of start() after a delay, with an error: the loop's work outlasts the lease's TTL.
type slowPool struct{ delay time.Duration }

func (p slowPool) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("slowPool: no transactions")
}
func (p slowPool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("slowPool: no statements")
}
func (p slowPool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("slowPool: no queries")
}
func (p slowPool) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	return slowRow{ctx, p.delay}
}

type slowRow struct {
	ctx   context.Context
	delay time.Duration
}

func (r slowRow) Scan(...any) error {
	select {
	case <-time.After(r.delay):
		return errors.New("slowPool: the database is slow")
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
}

type noChanges struct{}

func (noChanges) Changed(context.Context, string, string, time.Time, time.Time) ([]dump.Doc, time.Time, error) {
	return nil, time.Time{}, nil
}
func (noChanges) Lookup(context.Context, string, time.Time) (dump.Doc, bool, error) {
	return dump.Doc{}, false, nil
}

func TestLeaseTTLForCoversTheInterval(t *testing.T) {
	for _, iv := range []time.Duration{500 * time.Millisecond, 2 * time.Second, 10 * time.Second, 45 * time.Second, time.Minute} {
		if ttl := LeaseTTLFor(iv); ttl < ListenLeaseTTL || ttl < 3*iv {
			t.Errorf("interval %v: TTL %v", iv, ttl)
		}
	}
}

// A loop whose work (a poll, its start) outlasts the lease's TTL keeps the lease: it renews it while it works, so a
// second loop started by mistake never takes it while the first is alive.
func TestListenerKeepsItsLeaseThroughALongPoll(t *testing.T) {
	plan, err := etl.PlanMirror(map[string]bool{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	lease := &memLease{ttl: 150 * time.Millisecond}
	newLoop := func(id string) *Listener {
		l, err := NewListener(ListenConfig{Interval: 40 * time.Millisecond, ETLPool: slowPool{delay: 500 * time.Millisecond}, Source: noChanges{},
			Plan: plan, Lease: leaseView{lease, id}, LeaseTTL: lease.ttl, Log: zerolog.Nop()})
		if err != nil || l == nil {
			t.Fatalf("listener %v %v", l, err)
		}
		return l
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); newLoop("a").Run(ctx) }()
	time.Sleep(100 * time.Millisecond) // a takes the lease and starts its long work
	go func() { defer wg.Done(); newLoop("b").Run(ctx) }()
	time.Sleep(1500 * time.Millisecond) // three of a's long works, each three TTLs long
	cancel()
	wg.Wait()
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if len(lease.takes) != 1 || lease.takes[0] != "a" {
		t.Fatalf("the lease changed hands while its holder was alive: %v", lease.takes)
	}
}
