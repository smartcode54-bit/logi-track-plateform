package coexist

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// The mirror's listeners (main spec §13.7, T29): on the worker-sync replica, next to the etl.sync consumer, one loop
// polls the operations collections every ETL_LISTEN_INTERVAL (etl.Engine.MirrorChanges) while it holds the lease
// lock:mirror:listen, so a second replica started by mistake waits instead of applying the same changes twice. The
// sweep stays the catch-up behind it (every ETL_SYNC_INTERVAL): a listener that stops, loses its lease or meets a
// write it cannot see costs latency, never a row.

// ListenLeaseTTL is the life of lock:mirror:listen: the holder renews it every poll, a holder that died frees it within
// this time (another replica's listeners then take over; the sweep mirrors meanwhile).
const ListenLeaseTTL = 30 * time.Second

// Acker records the mirror's read-your-writes ack (cache.Cache.SetMirrorAck, R53): cache:mirror_ack:{collection}:{doc_id}
// holds the newest Firestore update time applied, which the write-back handlers wait for (up to 3 s, else 202).
type Acker interface {
	SetMirrorAck(ctx context.Context, collection, docID string, updateTime time.Time) error
}

// Lease is a single-holder lease (RedisLease).
type Lease interface {
	// Acquire takes the lease, or renews it when this holder has it; false when another holder has it.
	Acquire(ctx context.Context) (bool, error)
	// Release gives it up when this holder has it.
	Release(ctx context.Context) error
}

// ListenConfig wires the listener loop.
type ListenConfig struct {
	// Interval is ETL_LISTEN_INTERVAL; 0 turns the listeners off (the sweep alone mirrors).
	Interval time.Duration
	// ETLPool is the logitrack_etl pool of ETL_DATABASE_URL, the sweep's (R66).
	ETLPool ETLPool
	Source  etl.ChangeSource
	Plan    etl.MirrorPlan
	// Bucket and PublicBucket are S3_BUCKET and S3_PUBLIC_BUCKET (the load transforms register file rows).
	Bucket, PublicBucket string
	Lease                Lease
	Acks                 Acker
	Log                  zerolog.Logger
	Metrics              *Metrics
	Now                  func() time.Time
}

// Listener is the polling loop of the mirror's listeners.
type Listener struct {
	c       ListenConfig
	colls   []string
	lastLog time.Time
}

// NewListener checks the configuration; a nil Listener (and no error) means the listeners are off.
func NewListener(c ListenConfig) (*Listener, error) {
	if c.Interval <= 0 {
		return nil, nil
	}
	colls := etl.ListenCollections(c.Plan)
	if len(colls) == 0 {
		return nil, nil
	}
	if c.ETLPool == nil || c.Source == nil || c.Lease == nil {
		return nil, errors.New("coexist: the listeners need the ETL pool, the Firestore client and the lease")
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Listener{c: c, colls: colls}, nil
}

// Collections are the collections the listener polls.
func (l *Listener) Collections() []string { return l.colls }

// Run polls until ctx ends, then gives the lease up.
func (l *Listener) Run(ctx context.Context) {
	t := time.NewTicker(l.c.Interval)
	defer t.Stop()
	var eng *etl.Engine
	var cursors map[string]time.Time
	holding := false
	release := func() {
		// Release is a compare-and-delete on this holder's id, so it is safe whether or not the lease is held: an
		// Acquire whose answer was lost (a cancelled context) may have set it.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := l.c.Lease.Release(rctx); err != nil {
			l.c.Log.Warn().Err(err).Msg("mirror listeners: release the lease")
		}
	}
	defer release()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if ctx.Err() != nil {
			return
		}
		ok, err := l.c.Lease.Acquire(ctx)
		if err != nil || !ok {
			if holding {
				l.c.Log.Warn().Err(err).Msg("mirror listeners: the lease was lost; the sweep keeps mirroring until it is held again")
			}
			holding, cursors = false, nil
			continue
		}
		holding = true
		if cursors == nil {
			if eng, cursors, err = l.start(ctx); err != nil {
				l.throttled(err, "mirror listeners: not started")
				cursors = nil
				continue
			}
		}
		next, err := l.Poll(ctx, eng, cursors)
		if err != nil {
			l.throttled(err, "mirror listeners: a poll failed; the next one retries from the same cursors")
			continue
		}
		cursors = next
	}
}

// throttled logs a repeating failure at most once a minute.
func (l *Listener) throttled(err error, msg string) {
	if now := l.c.Now(); now.Sub(l.lastLog) >= time.Minute {
		l.lastLog = now
		l.c.Log.Error().Err(err).Msg(msg)
	}
}

// start checks the login (R66) and the P1 load, and reads the cursors (each collection's watermark minus the overlap).
func (l *Listener) start(ctx context.Context) (*etl.Engine, map[string]time.Time, error) {
	login, err := db.CurrentLogin(ctx, l.c.ETLPool)
	if err != nil {
		return nil, nil, err
	}
	if login.RoleName != db.RoleETL || login.Superuser {
		return nil, nil, fmt.Errorf("ETL_DATABASE_URL must log in as %s (R66), not as %s", db.RoleETL, login.RoleName)
	}
	tx := func(ctx context.Context, fn func(pgx.Tx) error) error { return db.WithETL(ctx, l.c.ETLPool, fn) }
	own, found, err := ownFleet(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, errors.New("the database has no own-fleet tenant: the mirror runs after the P1 initial load (etl load)")
	}
	eng, err := etl.New(etl.Config{Tx: tx, OwnFleetTenantID: own, Bucket: l.c.Bucket, PublicBucket: l.c.PublicBucket, Log: l.c.Log, Now: l.c.Now})
	if err != nil {
		return nil, nil, err
	}
	cursors := make(map[string]time.Time, len(l.colls))
	for _, c := range l.colls {
		if cursors[c], err = eng.ListenStart(ctx, c); err != nil {
			return nil, nil, err
		}
	}
	l.c.Log.Info().Strs("collections", l.colls).Dur("interval", l.c.Interval).Msg("mirror listeners started")
	return eng, cursors, nil
}

// Poll runs one poll and records what it applied (metrics, acks); it returns the next cursors.
func (l *Listener) Poll(ctx context.Context, eng *etl.Engine, cursors map[string]time.Time) (map[string]time.Time, error) {
	rep, next, err := eng.MirrorChanges(ctx, l.c.Source, l.c.Plan, cursors)
	if err != nil {
		return nil, err
	}
	afterApply(ctx, rep, l.c.Metrics, l.c.Acks, l.c.Now(), l.c.Log)
	return next, nil
}

func ownFleet(ctx context.Context, tx etl.TxFunc) (id uuid.UUID, found bool, err error) {
	err = tx(ctx, func(tx pgx.Tx) error {
		own, ok, err := etl.OwnFleet(ctx, tx)
		id, found = own, ok
		return err
	})
	return id, found, err
}

// afterApply records a committed sweep or poll: mirror_apply_total per outcome (held, deferred, removed too),
// mirror_lag_seconds per applied document (commit time minus its Firestore update time), mirror_events_total, and the
// read-your-writes acks of the loaded and quarantined documents (a Redis failure is logged: the write-back handler
// then times out and answers 202, nothing is lost).
func afterApply(ctx context.Context, rep *etl.Report, m *Metrics, acks Acker, now time.Time, log zerolog.Logger) {
	if m != nil {
		for _, c := range rep.Collections {
			for o, n := range c.Outcomes {
				m.mirrorApply.WithLabelValues(c.Name, string(o)).Add(float64(n))
			}
			for result, n := range map[string]int{ResultHeld: c.Held, ResultDeferred: c.Deferred, ResultRemoved: c.Removed} {
				if n > 0 {
					m.mirrorApply.WithLabelValues(c.Name, result).Add(float64(n))
				}
			}
			if c.Events > 0 {
				m.events.WithLabelValues(c.Name).Add(float64(c.Events))
			}
		}
	}
	failed := 0
	for _, d := range rep.Docs {
		if d.Outcome == etl.Pending {
			continue
		}
		if m != nil && !d.UpdateTime.IsZero() {
			m.lag.WithLabelValues(d.Collection).Observe(max(0, now.Sub(d.UpdateTime).Seconds()))
		}
		if acks == nil || (d.Outcome != etl.Loaded && d.Outcome != etl.Quarantined) || d.DocID == "" {
			continue
		}
		if err := acks.SetMirrorAck(ctx, d.Collection, d.DocID, d.UpdateTime); err != nil {
			failed++
		}
	}
	if failed > 0 {
		log.Warn().Int("documents", failed).Msg("mirror: read-your-writes acks not recorded (Redis); write-back handlers answer 202 for them")
	}
}

// RedisLease is a single-holder lease on one key: SET NX PX by the first holder, renewed by it (a Lua compare), and
// released by it alone. A holder that dies keeps it at most ttl.
type RedisLease struct {
	rdb    redis.UniversalClient
	key    string
	holder string
	ttl    time.Duration
	mu     sync.Mutex
}

// NewRedisLease builds a lease on key with a fresh holder id.
func NewRedisLease(rdb redis.UniversalClient, key string, ttl time.Duration) *RedisLease {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return &RedisLease{rdb: rdb, key: key, holder: hex.EncodeToString(b), ttl: ttl}
}

var renewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  return 1
end
return 0`)

var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`)

// Acquire implements Lease.
func (l *RedisLease) Acquire(ctx context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ok, err := l.rdb.SetNX(ctx, l.key, l.holder, l.ttl).Result()
	if err != nil || ok {
		return ok, err
	}
	n, err := renewScript.Run(ctx, l.rdb, []string{l.key}, l.holder, l.ttl.Milliseconds()).Int()
	return n == 1, err
}

// Release implements Lease.
func (l *RedisLease) Release(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return releaseScript.Run(ctx, l.rdb, []string{l.key}, l.holder).Err()
}
