package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Locker is the lock: namespace of Redis (Appendix B §B.6.2). Holders are job ids (or a scheduler
// instance id for cron marks); only the holder releases a lock, and every lock also expires.
type Locker interface {
	// Acquire sets key to holder unless it exists (SET NX PX ttl); otherwise it reports the holder.
	Acquire(ctx context.Context, key, holder string, ttl time.Duration) (ok bool, current string, err error)
	// Release deletes key if holder still holds it.
	Release(ctx context.Context, key, holder string) error
	// JobKey is lock:job:{type}:{scope}.
	JobKey(jobType, scope string) string
	// CronKey is lock:cron:{job}:{scheduledFor}.
	CronKey(job string, scheduledFor time.Time) string
}

// RedisLocker implements Locker under the prefix lt:{APP_ENV}: (R26).
type RedisLocker struct {
	rdb    redis.UniversalClient
	prefix string
}

// NewRedisLocker builds a locker; prefix is REDIS_KEY_PREFIX.
func NewRedisLocker(rdb redis.UniversalClient, prefix string) *RedisLocker {
	return &RedisLocker{rdb: rdb, prefix: prefix}
}

// JobKey implements Locker. An empty scope is "all".
func (l *RedisLocker) JobKey(jobType, scope string) string {
	if scope == "" {
		scope = "all"
	}
	return l.prefix + "lock:job:" + jobType + ":" + scope
}

// CronKey implements Locker; scheduledFor is written in UTC with second precision.
func (l *RedisLocker) CronKey(job string, scheduledFor time.Time) string {
	return l.prefix + "lock:cron:" + job + ":" + scheduledFor.UTC().Format("20060102T150405Z")
}

// Acquire implements Locker.
func (l *RedisLocker) Acquire(ctx context.Context, key, holder string, ttl time.Duration) (bool, string, error) {
	ok, err := l.rdb.SetNX(ctx, key, holder, ttl).Result()
	if err != nil {
		return false, "", fmt.Errorf("jobs: lock %s: %w", key, err)
	}
	if ok {
		return true, holder, nil
	}
	cur, err := l.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		// Expired between SET NX and GET: try once more.
		if ok, err = l.rdb.SetNX(ctx, key, holder, ttl).Result(); err == nil && ok {
			return true, holder, nil
		}
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, "", fmt.Errorf("jobs: read lock %s: %w", key, err)
	}
	return false, cur, nil
}

var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`)

// Release implements Locker.
func (l *RedisLocker) Release(ctx context.Context, key, holder string) error {
	if err := releaseScript.Run(ctx, l.rdb, []string{key}, holder).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("jobs: release %s: %w", key, err)
	}
	return nil
}
