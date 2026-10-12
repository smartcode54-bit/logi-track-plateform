package cache

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// periodPattern is a Bangkok billing period, YYYY-MM.
var periodPattern = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

// ErrBadPeriod is returned when a period-lock loader yields something other than YYYY-MM.
var ErrBadPeriod = errors.New("cache: period locks must be YYYY-MM")

// PeriodLocks is the read-through UI hint of the sent/paid periods (YYYY-MM, sorted) of a billing
// party: badges and warnings in the billing UI only (R89, main spec §6.11). No write ever consults
// it: a reprice reads billing_statements FOR SHARE in its transaction (R17), and under MoneyPath this
// returns ErrMoneyPath. Dropped on statement.created and statement.status_changed.
func (c *Cache) PeriodLocks(ctx context.Context, billingPartyID string, load func(context.Context) ([]string, error)) ([]string, error) {
	if IsMoneyPath(ctx) {
		return nil, ErrMoneyPath
	}
	key := c.ks.PeriodLocks(billingPartyID)
	mark := c.l1.mark()
	if v, ok := c.l1.get(key); ok {
		if s, ok := v.([]string); ok {
			c.metrics.lookups.WithLabelValues("l1").Inc()
			return slices.Clone(s), nil
		}
	}
	gen := c.genKey(key)
	var members *redis.StringSliceCmd
	var seen *redis.StringCmd
	err := c.call(ctx, func(ctx context.Context) error {
		_, err := c.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
			members = p.SMembers(ctx, key)
			seen = c.readGen(ctx, p, gen)
			return nil
		})
		return err
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		c.redisFailed(ctx, "smembers", err)
		c.metrics.lookups.WithLabelValues("error").Inc()
		return loadPeriods(ctx, load)
	}
	if got := members.Val(); slices.Contains(got, hashSentinel) {
		out := slices.DeleteFunc(got, func(m string) bool { return m == hashSentinel })
		slices.Sort(out)
		c.metrics.lookups.WithLabelValues("hit").Inc()
		c.l1.put(mark, key, slices.Clone(out))
		return out, nil
	}
	c.metrics.lookups.WithLabelValues("miss").Inc()
	periods, err := loadPeriods(ctx, load)
	if err != nil {
		return nil, err
	}
	args := make([]any, 0, len(periods)+1)
	args = append(args, hashSentinel)
	for _, p := range periods {
		args = append(args, p)
	}
	stored, err := c.storeIfCurrent(ctx, gen, genValue(seen), func(ctx context.Context, p redis.Pipeliner) {
		p.Del(ctx, key)
		p.SAdd(ctx, key, args...)
		p.PExpire(ctx, key, PeriodLocksTTL)
	})
	if err != nil {
		c.redisFailed(ctx, "sadd", err)
		return periods, nil
	}
	if stored {
		c.l1.put(mark, key, slices.Clone(periods))
	}
	return periods, nil
}

func loadPeriods(ctx context.Context, load func(context.Context) ([]string, error)) ([]string, error) {
	periods, err := load(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range periods {
		if !periodPattern.MatchString(p) {
			return nil, ErrBadPeriod
		}
	}
	periods = slices.Clone(periods)
	slices.Sort(periods)
	return slices.Compact(periods), nil
}

// Subtenants is the read-through contractor reach of a tenant: the ids of the carrier tenants whose
// contractor_tenant_id is tenantID (R60, Appendix C §C.3.4). WithPrincipal (T07) puts them into the
// GUC app.subtenant_ids; tenant.created / tenant.updated drop the key (OnEvent).
func (c *Cache) Subtenants(ctx context.Context, tenantID string, load func(context.Context) ([]string, error)) ([]string, error) {
	ids, err := GetJSON(ctx, c, c.ks.TenantSubtenants(tenantID), SubtenantsTTL, load)
	return slices.Clone(ids), err
}

// SetMirrorAck records that the Firestore -> PostgreSQL mirror applied a document change with this
// UpdateTime (read-your-writes, P2-P7a, R53). The stored value only moves forward, so a late,
// older change never hides a newer ack. Stored as Unix microseconds (Firestore's precision) for 10
// minutes.
func (c *Cache) SetMirrorAck(ctx context.Context, collection, docID string, updateTime time.Time) error {
	if IsMoneyPath(ctx) {
		return ErrMoneyPath
	}
	return c.call(ctx, func(ctx context.Context) error {
		return raiseScript.Run(ctx, c.rdb, []string{c.ks.MirrorAck(collection, docID)},
			updateTime.UnixMicro(), MirrorAckTTL.Milliseconds()).Err()
	})
}

// MirrorAck returns the last UpdateTime the mirror applied to a document, and false when none is
// recorded (expired, or never mirrored).
func (c *Cache) MirrorAck(ctx context.Context, collection, docID string) (time.Time, bool, error) {
	if IsMoneyPath(ctx) {
		return time.Time{}, false, ErrMoneyPath
	}
	var at time.Time
	var ok bool
	err := c.call(ctx, func(ctx context.Context) (err error) {
		at, ok, err = ReadMirrorAck(ctx, c.rdb, c.ks, collection, docID)
		return err
	})
	return at, ok, err
}

// ReadMirrorAck reads cache:mirror_ack:{collection}:{docID} on a plain client: the last UpdateTime the
// mirror applied to the document, false when none is recorded (expired after MirrorAckTTL, never
// mirrored, or unreadable). The notify.fcm consumer reads it to hold the push of a Go write-back until
// the mirror applied that write to PostgreSQL (T31).
func ReadMirrorAck(ctx context.Context, rdb redis.UniversalClient, ks Keyspace, collection, docID string) (time.Time, bool, error) {
	v, err := rdb.Get(ctx, ks.MirrorAck(collection, docID)).Result()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	us, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}, false, nil // unreadable entry: as if absent
	}
	return time.UnixMicro(us).UTC(), true, nil
}

// RunOrderHoldTTL is how long a queue position the operations write-back handed out stays held: longer than
// MirrorAckTTL, by when the mirror has loaded the task and PostgreSQL's max(run_order) of the driver holds it.
const RunOrderHoldTTL = 15 * time.Minute

// ReserveRunOrder hands out the next queue position of a driver while the operations domain is Firestore-owned
// (T31): one more than the larger of floor (PostgreSQL's max(run_order) of the driver, which the caller reads
// under the driver's queue lock) and the position held here, and holds it for RunOrderHoldTTL. A write-back's
// task reaches PostgreSQL only when the mirror loads it, so two assignments of one driver within the mirror's
// lag would otherwise both take max + 1.
func (c *Cache) ReserveRunOrder(ctx context.Context, driverID string, floor int32) (int32, error) {
	if IsMoneyPath(ctx) {
		return 0, ErrMoneyPath
	}
	var n int64
	err := c.call(ctx, func(ctx context.Context) (err error) {
		n, err = nextScript.Run(ctx, c.rdb, []string{c.ks.RunOrderHold(driverID)}, floor,
			RunOrderHoldTTL.Milliseconds()).Int64()
		return err
	})
	return int32(n), err
}

// HoldRunOrder raises the position held for a driver to at least pos (a write-back that sets a position
// itself), for RunOrderHoldTTL.
func (c *Cache) HoldRunOrder(ctx context.Context, driverID string, pos int32) error {
	if IsMoneyPath(ctx) {
		return ErrMoneyPath
	}
	return c.call(ctx, func(ctx context.Context) error {
		return raiseScript.Run(ctx, c.rdb, []string{c.ks.RunOrderHold(driverID)}, pos, RunOrderHoldTTL.Milliseconds()).Err()
	})
}

// nextScript sets KEYS[1] to max(KEYS[1], ARGV[1]) + 1 with the TTL ARGV[2] ms and returns it.
var nextScript = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1])) or 0
local floor = tonumber(ARGV[1])
if floor > cur then cur = floor end
cur = cur + 1
redis.call('SET', KEYS[1], cur, 'PX', ARGV[2])
return cur`)

// MirrorAckPoll is how often WaitMirrorAck reads the ack.
var MirrorAckPoll = 50 * time.Millisecond

// WaitMirrorAck waits up to timeout (3 s in the write-back handlers) until the mirror applied a change
// at or after want. False means the caller answers 202 with the Firestore document echoed. A Redis
// failure ends the wait early with the error; the handler treats it like a timeout.
func (c *Cache) WaitMirrorAck(ctx context.Context, collection, docID string, want time.Time, timeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	t := time.NewTicker(MirrorAckPoll)
	defer t.Stop()
	for {
		got, ok, err := c.MirrorAck(ctx, collection, docID)
		if err != nil {
			if ctx.Err() != nil {
				return false, nil
			}
			return false, err
		}
		if ok && !got.Before(want.Truncate(time.Microsecond)) {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, nil
		case <-t.C:
		}
	}
}

// raiseScript sets KEYS[1] to ARGV[1] (an integer) only when it is absent or lower, and refreshes the
// TTL (ARGV[2] ms) either way.
var raiseScript = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]))
local v = tonumber(ARGV[1])
if (not cur) or v > cur then
  redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
  return v
end
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return cur`)

// PutTicket stores a single-use ticket (auth:pwchg, auth:sse, auth:google:nonce) unless the key
// exists; false means the key was taken.
func (c *Cache) PutTicket(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if IsMoneyPath(ctx) {
		return false, ErrMoneyPath
	}
	var ok bool
	err := c.call(ctx, func(ctx context.Context) (err error) {
		ok, err = c.rdb.SetNX(ctx, key, value, ttl).Result()
		return err
	})
	return ok, err
}

// TakeTicket consumes a ticket atomically (GETDEL): of two concurrent redemptions exactly one gets
// it; false means unknown, expired or already used.
func (c *Cache) TakeTicket(ctx context.Context, key string) ([]byte, bool, error) {
	if IsMoneyPath(ctx) {
		return nil, false, ErrMoneyPath
	}
	var b []byte
	err := c.call(ctx, func(ctx context.Context) (err error) {
		b, err = c.rdb.GetDel(ctx, key).Bytes()
		return err
	})
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}
