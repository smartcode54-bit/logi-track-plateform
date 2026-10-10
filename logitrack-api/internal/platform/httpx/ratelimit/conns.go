package ratelimit

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
)

// ConnLimiter caps the open SSE streams of one user at SSE_MAX_CONN_PER_USER across every api replica
// (main spec §8.2, Appendix B §B.6.2 rl:sse_conns:{userId}). The key is a sorted set of leases: one
// member per open stream, scored with its lease expiry in milliseconds of the Redis clock. A stream
// renews its lease with every heartbeat and drops it when it ends; a stream of a replica that crashed
// stops renewing, so its slot frees itself when the lease runs out instead of counting forever, as a
// plain counter decremented on disconnect would.
type ConnLimiter struct {
	rdb   redis.UniversalClient
	ks    cache.Keyspace
	max   int
	lease time.Duration
}

// NewConnLimiter builds the limiter; lease is how long a stream holds its slot without a renewal.
func NewConnLimiter(rdb redis.UniversalClient, ks cache.Keyspace, maxPerUser int, lease time.Duration) *ConnLimiter {
	return &ConnLimiter{rdb: rdb, ks: ks, max: maxPerUser, lease: lease}
}

// Max is the cap, SSE_MAX_CONN_PER_USER.
func (l *ConnLimiter) Max() int { return l.max }

// leaseScript: KEYS = [rl:sse_conns:{userId}]; ARGV = [max, lease ms, stream id]. Expired leases go
// first; an existing lease is renewed whatever the count, a new one is refused at the cap. Returns 1
// when the stream holds a lease, 0 when it was refused.
var leaseScript = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
if not redis.call('ZSCORE', KEYS[1], ARGV[3]) then
  if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[1]) then
    return 0
  end
end
local lease = tonumber(ARGV[2])
redis.call('ZADD', KEYS[1], string.format('%d', now + lease), ARGV[3])
redis.call('PEXPIRE', KEYS[1], string.format('%d', lease))
return 1`)

// Acquire takes (or renews) the lease of stream streamID of user userID; ok is false at the cap. Errors
// are Redis failures, bounded like every rate-limit call.
func (l *ConnLimiter) Acquire(ctx context.Context, userID, streamID string) (bool, error) {
	if l.max < 1 || l.lease <= 0 {
		return false, errors.New("ratelimit: the SSE connection limit needs a cap and a lease")
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	n, err := leaseScript.Run(ctx, l.rdb, []string{l.ks.SSEConnections(userID)}, l.max, l.lease.Milliseconds(), streamID).Int64()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Renew extends the lease of a running stream (Acquire for a lease that exists: never refused unless
// the lease already ran out and the cap filled up meanwhile).
func (l *ConnLimiter) Renew(ctx context.Context, userID, streamID string) (bool, error) {
	return l.Acquire(ctx, userID, streamID)
}

// Release drops the lease of a stream that ended.
func (l *ConnLimiter) Release(ctx context.Context, userID, streamID string) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return l.rdb.ZRem(ctx, l.ks.SSEConnections(userID), streamID).Err()
}
