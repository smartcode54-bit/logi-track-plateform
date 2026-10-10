package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Store is the auth state in Redis (Appendix B §B.6.2, Appendix C §C.4.14). Every key carries the
// prefix lt:{APP_ENV}: (R26). PostgreSQL stays the source of truth for sessions, refresh tokens and
// auth_version:
//
//	auth:rt:{sha256hex}        hash   {sessionId, familyId, userId, exp}, TTL = remaining refresh life
//	auth:sess:revoked:{sid}    string "1", TTL = JWT_ACCESS_TTL + leeway
//	auth:user:ver:{userId}     string users.auth_version, TTL 1 h, only ever raised
//	auth:pwchg:{ticket}        string {"userId","authVersion"}, 10 min, single use (GETDEL)
//	auth:sse:{ticket}          string {"userId","sessionId"}, 60 s, single use (GETDEL)
//	rl:{bucket}:{subject}      fixed-window counters of the auth buckets (Appendix B §B.6.3)
//
// Not every key is a disposable cache. auth:rt entries are a pure index (Refresh always decides in
// PostgreSQL) and the rl counters protect capacity, so losing them is harmless. auth:sess:revoked and
// auth:user:ver are security-relevant until the access tokens they judge expire: while the version key
// is cached, the revoked marker is the only per-request evidence of a session that ended without a
// version bump, and a version raise that never lands keeps the old claims valid. Their post-commit
// writes are therefore retried (Service.Apply); a token newer than the cached version, and a version
// miss, re-read PostgreSQL (sessions.revoked_at and users.auth_version) and rebuild both keys.
type Store struct {
	rdb    redis.UniversalClient
	prefix string
}

// callTimeout bounds every Redis call of the request path: an unreachable or slow Redis costs a
// request at most this long before the caller falls back (revocation check), fails open (rate limits)
// or answers 503 (tickets).
const callTimeout = 500 * time.Millisecond

// NewStore wraps a client; prefix is the REDIS_KEY_PREFIX value ("lt:{APP_ENV}:").
func NewStore(rdb redis.UniversalClient, prefix string) *Store {
	return &Store{rdb: rdb, prefix: prefix}
}

func bound(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, callTimeout)
}

// verTTL is the lifetime of a cached auth_version (Appendix B §B.6.2).
const verTTL = time.Hour

func (s *Store) key(parts ...string) string {
	k := s.prefix
	for i, p := range parts {
		if i > 0 {
			k += ":"
		}
		k += p
	}
	return k
}

func (s *Store) rtKey(hashHex string) string { return s.key("auth", "rt", hashHex) }
func (s *Store) revokedKey(sid uuid.UUID) string {
	return s.key("auth", "sess", "revoked", sid.String())
}
func (s *Store) verKey(uid uuid.UUID) string { return s.key("auth", "user", "ver", uid.String()) }

// raiseScript sets the cached auth_version only upward, so a request that filled the cache from an old
// read can never overwrite the value a revocation wrote after its commit.
var raiseScript = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]))
local v = tonumber(ARGV[1])
if (not cur) or v > cur then
  redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
  return v
end
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return cur`)

// hitScript is a fixed-window counter: the first hit opens the window.
var hitScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
local ttl = redis.call('PTTL', KEYS[1])
if n == 1 or ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
end
return {n, ttl}`)

// reserveScript counts a sign-in attempt before its password is checked, so concurrent attempts cannot
// all pass a lockout check made before any of them failed: the first attempt opens the window and the
// attempt that reaches the threshold restarts it, so the lock lasts the full window from that attempt.
// It returns the count, including this attempt, and the time until the record expires.
var reserveScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 or n == tonumber(ARGV[1]) then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  ttl = tonumber(ARGV[2])
end
return {n, ttl}`)

// unreserveScript returns an attempt that never reached the password check (a database error, every
// hashing slot busy). It never creates the key or drops its TTL.
var unreserveScript = redis.NewScript(`
local n = tonumber(redis.call('GET', KEYS[1]))
if n and n > 0 then
  redis.call('DECR', KEYS[1])
end
return 0`)

// revocationState reads the two per-request keys in one pipeline (C.4.3).
func (s *Store) revocationState(ctx context.Context, sid, uid uuid.UUID) (revoked bool, ver int32, known bool, err error) {
	ctx, cancel := bound(ctx)
	defer cancel()
	pipe := s.rdb.Pipeline()
	ex := pipe.Exists(ctx, s.revokedKey(sid))
	gv := pipe.Get(ctx, s.verKey(uid))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return false, 0, false, err
	}
	revoked = ex.Val() > 0
	v, err := gv.Result()
	if errors.Is(err, redis.Nil) {
		return revoked, 0, false, nil
	}
	if err != nil {
		return false, 0, false, err
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return revoked, 0, false, nil // unreadable entry: treat as a miss and rebuild
	}
	return revoked, int32(n), true, nil
}

func (s *Store) raiseVersion(ctx context.Context, uid uuid.UUID, ver int32) error {
	ctx, cancel := bound(ctx)
	defer cancel()
	return raiseScript.Run(ctx, s.rdb, []string{s.verKey(uid)}, ver, verTTL.Milliseconds()).Err()
}

func (s *Store) markRevoked(ctx context.Context, ttl time.Duration, sids ...uuid.UUID) error {
	ctx, cancel := bound(ctx)
	defer cancel()
	if len(sids) == 0 {
		return nil
	}
	pipe := s.rdb.Pipeline()
	for _, sid := range sids {
		pipe.Set(ctx, s.revokedKey(sid), "1", ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// rtEntry is the hash of auth:rt:{sha256hex}.
type rtEntry struct {
	SessionID uuid.UUID
	FamilyID  uuid.UUID
	UserID    uuid.UUID
	Exp       time.Time
}

func (s *Store) putRefresh(ctx context.Context, hashHex string, e rtEntry, now time.Time) error {
	ctx, cancel := bound(ctx)
	defer cancel()
	ttl := e.Exp.Sub(now)
	if ttl <= 0 {
		return nil
	}
	k := s.rtKey(hashHex)
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, k, "sessionId", e.SessionID.String(), "familyId", e.FamilyID.String(),
		"userId", e.UserID.String(), "exp", strconv.FormatInt(e.Exp.Unix(), 10))
	pipe.PExpire(ctx, k, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *Store) getRefresh(ctx context.Context, hashHex string) (rtEntry, bool, error) {
	ctx, cancel := bound(ctx)
	defer cancel()
	m, err := s.rdb.HGetAll(ctx, s.rtKey(hashHex)).Result()
	if err != nil {
		return rtEntry{}, false, err
	}
	if len(m) == 0 {
		return rtEntry{}, false, nil
	}
	var e rtEntry
	var perr error
	parse := func(v string) uuid.UUID {
		id, err := uuid.Parse(v)
		if err != nil {
			perr = err
		}
		return id
	}
	e.SessionID, e.FamilyID, e.UserID = parse(m["sessionId"]), parse(m["familyId"]), parse(m["userId"])
	exp, err := strconv.ParseInt(m["exp"], 10, 64)
	if perr != nil || err != nil {
		return rtEntry{}, false, nil // damaged entry: fall back to PostgreSQL
	}
	e.Exp = time.Unix(exp, 0)
	return e, true, nil
}

func (s *Store) delRefresh(ctx context.Context, hashHexes ...string) error {
	ctx, cancel := bound(ctx)
	defer cancel()
	if len(hashHexes) == 0 {
		return nil
	}
	keys := make([]string, len(hashHexes))
	for i, h := range hashHexes {
		keys[i] = s.rtKey(h)
	}
	return s.rdb.Del(ctx, keys...).Err()
}

// putTicket stores a single-use ticket (auth:pwchg, auth:sse).
func (s *Store) putTicket(ctx context.Context, kind, ticket string, v any, ttl time.Duration) error {
	ctx, cancel := bound(ctx)
	defer cancel()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, s.key("auth", kind, ticket), b, ttl).Err()
}

// peekTicket reads a ticket without consuming it; found is false for an unknown or expired ticket.
func (s *Store) peekTicket(ctx context.Context, kind, ticket string, v any) (bool, error) {
	ctx, cancel := bound(ctx)
	defer cancel()
	b, err := s.rdb.Get(ctx, s.key("auth", kind, ticket)).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return json.Unmarshal(b, v) == nil, nil
}

// takeTicket consumes a ticket atomically (GETDEL): of two concurrent redemptions only one finds it.
func (s *Store) takeTicket(ctx context.Context, kind, ticket string, v any) (bool, error) {
	ctx, cancel := bound(ctx)
	defer cancel()
	b, err := s.rdb.GetDel(ctx, s.key("auth", kind, ticket)).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return json.Unmarshal(b, v) == nil, nil
}

// hit counts one request in rl:{bucket}:{subject} and returns the count in the current window and the
// time until the window closes.
func (s *Store) hit(ctx context.Context, bucket, subject string, window time.Duration) (int64, time.Duration, error) {
	ctx, cancel := bound(ctx)
	defer cancel()
	res, err := hitScript.Run(ctx, s.rdb, []string{s.key("rl", bucket, subject)}, window.Milliseconds()).Int64Slice()
	if err != nil {
		return 0, 0, err
	}
	if len(res) != 2 {
		return 0, 0, errors.New("auth: unexpected rate-limit reply")
	}
	return res[0], time.Duration(res[1]) * time.Millisecond, nil
}

// reserveAttempt counts one sign-in attempt for subject (rl:login_fail) and returns the attempts in the
// current window, this one included, and the time until the window ends (C.4.8, C.4.12).
func (s *Store) reserveAttempt(ctx context.Context, subject string, threshold int, window time.Duration) (int64, time.Duration, error) {
	ctx, cancel := bound(ctx)
	defer cancel()
	res, err := reserveScript.Run(ctx, s.rdb, []string{s.key("rl", "login_fail", subject)}, threshold, window.Milliseconds()).Int64Slice()
	if err != nil {
		return 0, 0, err
	}
	if len(res) != 2 {
		return 0, 0, errors.New("auth: unexpected lockout reply")
	}
	return res[0], time.Duration(res[1]) * time.Millisecond, nil
}

// releaseAttempt takes back an attempt that ended before its password was checked.
func (s *Store) releaseAttempt(ctx context.Context, subject string) error {
	ctx, cancel := bound(ctx)
	defer cancel()
	return unreserveScript.Run(ctx, s.rdb, []string{s.key("rl", "login_fail", subject)}).Err()
}

func (s *Store) clearFailures(ctx context.Context, subject string) error {
	ctx, cancel := bound(ctx)
	defer cancel()
	return s.rdb.Del(ctx, s.key("rl", "login_fail", subject)).Err()
}

// Ping checks the connection (readiness).
func (s *Store) Ping(ctx context.Context) error { return s.rdb.Ping(ctx).Err() }
