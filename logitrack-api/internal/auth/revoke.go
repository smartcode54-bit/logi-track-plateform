package auth

import (
	"context"
	"encoding/hex"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
)

// PostCommit collects the Redis writes that follow a committed transaction (Appendix C §C.4.7
// post-commit hook): raised auth versions, revoked-session markers and refresh-token index entries.
// Apply them only after COMMIT, so Redis never announces a change PostgreSQL rolled back.
type PostCommit struct {
	versions map[uuid.UUID]int32 // bumps: retried until they land
	issued   map[uuid.UUID]int32 // the version a token was just issued at: best effort, never retried
	sessions []uuid.UUID
	drop     []string
	put      map[string]rtEntry
}

func newPostCommit() *PostCommit {
	return &PostCommit{versions: map[uuid.UUID]int32{}, issued: map[uuid.UUID]int32{}, put: map[string]rtEntry{}}
}

// version records a committed auth_version bump.
func (p *PostCommit) version(uid uuid.UUID, v int32) {
	if v > p.versions[uid] {
		p.versions[uid] = v
	}
}

// issue records the version a new token carries: raising the cache to it heals a bump whose own raise
// was lost. A failure is harmless (the next newer token or miss reads PostgreSQL), so it is not retried.
func (p *PostCommit) issue(uid uuid.UUID, v int32) {
	if v > p.issued[uid] {
		p.issued[uid] = v
	}
}

func (p *PostCommit) revoked(sids ...uuid.UUID) { p.sessions = append(p.sessions, sids...) }

func (p *PostCommit) dropHashes(sums ...[]byte) {
	for _, s := range sums {
		h := hex.EncodeToString(s)
		delete(p.put, h)
		p.drop = append(p.drop, h)
	}
}

func (p *PostCommit) putRefresh(hexSum string, e rtEntry) { p.put[hexSum] = e }

// Post-commit retry schedule: the security-relevant writes that failed are retried in the background
// with capped exponential backoff until they land or no longer matter.
const (
	retryFirst = 100 * time.Millisecond
	retryMax   = 5 * time.Second
)

// Apply performs the post-commit writes. A failure never fails the request: PostgreSQL is the source
// of truth and has committed. auth:rt index entries and the raise to an issued version are best effort
// (Refresh always decides in PostgreSQL; a newer token or a miss reads it). The version bumps and the
// revoked markers are security-relevant (Store): the ones that failed join one background retrier,
// which merges them per user and session and retries until they land or until the access tokens they
// judge have expired (JWT_ACCESS_TTL + leeway). Every failure counts in
// auth_postcommit_failures_total{op}. Until a retry lands, a token newer than the cached version and a
// version miss still read PostgreSQL (Authenticate). A process that dies first loses the retry: the
// outbox relay re-applies the hook from user.sessions_revoked (Appendix C §C.4.7).
func (s *Service) Apply(ctx context.Context, p *PostCommit) {
	if p == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	until := time.Now().Add(s.keys.TTL() + token.Leeway)
	failed := newPostCommit()
	for uid, v := range p.versions {
		if err := s.store.raiseVersion(ctx, uid, v); err != nil {
			s.postCommitFailed.WithLabelValues("version").Inc()
			s.log.Error().Err(err).Str("user_id", uid.String()).Msg("auth: raise cached auth_version failed; retrying")
			failed.version(uid, v)
		}
	}
	for uid, v := range p.issued {
		if v <= p.versions[uid] {
			continue
		}
		if err := s.store.raiseVersion(ctx, uid, v); err != nil {
			s.postCommitFailed.WithLabelValues("issue").Inc()
			s.log.Warn().Err(err).Str("user_id", uid.String()).Msg("auth: raise cached auth_version to the issued one failed")
		}
	}
	if err := s.store.markRevoked(ctx, s.keys.TTL()+token.Leeway, p.sessions...); err != nil {
		s.postCommitFailed.WithLabelValues("revoked").Inc()
		s.log.Error().Err(err).Int("sessions", len(p.sessions)).Msg("auth: mark revoked sessions failed; retrying")
		failed.revoked(p.sessions...)
	}
	if err := s.store.delRefresh(ctx, p.drop...); err != nil {
		s.postCommitFailed.WithLabelValues("rt_drop").Inc()
		s.log.Warn().Err(err).Msg("auth: drop refresh index entries failed")
	}
	now := s.clock()
	for h, e := range p.put {
		if err := s.store.putRefresh(ctx, h, e, now); err != nil {
			s.postCommitFailed.WithLabelValues("rt_put").Inc()
			s.log.Warn().Err(err).Msg("auth: write refresh index entry failed")
		}
	}
	if len(failed.versions) > 0 || len(failed.sessions) > 0 {
		s.retry.enqueue(s, failed, until)
	}
}

// retrier is the single background worker of the failed security-relevant post-commit writes. Entries
// are merged (the highest version per user, one marker per session), so an outage costs one goroutine
// and one Redis attempt per round, whatever the number of failed requests.
type retrier struct {
	mu       sync.Mutex
	versions map[uuid.UUID]pendingVersion
	sessions map[uuid.UUID]time.Time // until
	running  bool
	closed   bool
	stop     chan struct{}
	done     sync.WaitGroup
}

type pendingVersion struct {
	v     int32
	until time.Time
}

func newRetrier() *retrier {
	return &retrier{versions: map[uuid.UUID]pendingVersion{}, sessions: map[uuid.UUID]time.Time{}, stop: make(chan struct{})}
}

func (r *retrier) enqueue(s *Service, p *PostCommit, until time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		s.log.Error().Int("sessions", len(p.sessions)).Int("users", len(p.versions)).
			Msg("auth: post-commit writes lost at shutdown; the outbox relay re-applies them")
		return
	}
	for uid, v := range p.versions {
		if cur, ok := r.versions[uid]; !ok || v >= cur.v {
			r.versions[uid] = pendingVersion{v: v, until: until}
		}
	}
	for _, sid := range p.sessions {
		r.sessions[sid] = until
	}
	if !r.running {
		r.running = true
		r.done.Add(1)
		go r.loop(s)
	}
}

// loop retries until nothing is pending (then exits), or until close.
func (r *retrier) loop(s *Service) {
	defer r.done.Done()
	delay := retryFirst
	for {
		t := time.NewTimer(delay)
		select {
		case <-r.stop:
			t.Stop()
			return
		case <-t.C:
		}
		versions, sessions := r.due(s, time.Now())
		ok := true
		ctx := context.Background()
		for uid, pv := range versions {
			if err := s.store.raiseVersion(ctx, uid, pv.v); err != nil {
				ok = false
				s.postCommitFailed.WithLabelValues("version").Inc()
				continue
			}
			r.mu.Lock()
			if cur, found := r.versions[uid]; found && cur.v == pv.v {
				delete(r.versions, uid)
			}
			r.mu.Unlock()
		}
		if len(sessions) > 0 {
			sids := make([]uuid.UUID, 0, len(sessions))
			for sid := range sessions {
				sids = append(sids, sid)
			}
			if err := s.store.markRevoked(ctx, s.keys.TTL()+token.Leeway, sids...); err != nil {
				ok = false
				s.postCommitFailed.WithLabelValues("revoked").Inc()
			} else {
				r.mu.Lock()
				for sid, until := range sessions {
					if r.sessions[sid].Equal(until) {
						delete(r.sessions, sid)
					}
				}
				r.mu.Unlock()
			}
		}
		r.mu.Lock()
		if len(r.versions) == 0 && len(r.sessions) == 0 {
			r.running = false
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
		if ok {
			delay = retryFirst
		} else {
			delay = min(delay*2, retryMax)
		}
	}
}

// due drops the entries that no longer matter (every access token they judge has expired) and returns
// a copy of the rest.
func (r *retrier) due(s *Service, now time.Time) (map[uuid.UUID]pendingVersion, map[uuid.UUID]time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	gaveUp := 0
	versions := make(map[uuid.UUID]pendingVersion, len(r.versions))
	for uid, pv := range r.versions {
		if now.After(pv.until) {
			delete(r.versions, uid)
			gaveUp++
			continue
		}
		versions[uid] = pv
	}
	sessions := make(map[uuid.UUID]time.Time, len(r.sessions))
	for sid, until := range r.sessions {
		if now.After(until) {
			delete(r.sessions, sid)
			gaveUp++
			continue
		}
		sessions[sid] = until
	}
	if gaveUp > 0 {
		s.log.Error().Int("writes", gaveUp).Msg("auth: gave up retrying post-commit writes; the access tokens they judged have expired")
	}
	return versions, sessions
}

// close stops the worker and waits for it; later failures are only logged.
func (r *retrier) close(s *Service) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	pending := len(r.versions) + len(r.sessions)
	r.mu.Unlock()
	close(r.stop)
	r.done.Wait()
	if pending > 0 {
		s.log.Error().Int("writes", pending).Msg("auth: post-commit writes still pending at shutdown; the outbox relay re-applies them")
	}
}

// Revocation is one row of the revocation table of Appendix C §C.4.7. Claims changes (role, membership,
// scope, driver link, platform role) use Reason RevokeClaimsChanged with BumpVersion: no session ends,
// the next request answers 401 token_expired with details.reason claims_changed and the refresh issues
// the new claims (R50, R78). Every other reason ends sessions.
type Revocation struct {
	UserID      uuid.UUID
	Reason      string      // RevokeClaimsChanged or a sessions.revoked_reason value
	SessionIDs  []uuid.UUID // nil: every live session of the user
	Except      *uuid.UUID  // a session to keep (password change keeps the current one)
	RevokedBy   *uuid.UUID  // admin revoke
	BumpVersion bool
	RequestID   string
}

// RevokeInTx performs the database half of a revocation inside the caller's WithSystem transaction:
// it locks the user's row, then auth_version++, sessions.revoked_*, their refresh tokens, and outbox
// user.sessions_revoked on user:{uid} (SSE session.revoked; notify.fcm pushes session_revoked to the
// revoked installs). The caller appends its security_events row with security.Append in the same
// transaction and calls Apply after COMMIT. internal/iam (T19) uses it for disable, role, scope,
// driver-link and platform-role changes; such a caller locks or updates the users row before it
// touches any sessions or refresh_tokens row (lock order users -> sessions -> refresh_tokens).
func (s *Service) RevokeInTx(ctx context.Context, tx pgx.Tx, r Revocation) (*PostCommit, []uuid.UUID, error) {
	pc := newPostCommit()
	sids, err := s.revokeTx(ctx, tx, r, s.clock(), pc)
	return pc, sids, err
}

func (s *Service) revokeTx(ctx context.Context, tx pgx.Tx, r Revocation, now time.Time, pc *PostCommit) ([]uuid.UUID, error) {
	q := authdb.New(tx)
	// The users row first (lock order); a no-op when this transaction already holds it.
	if _, err := q.LockUser(ctx, r.UserID); err != nil {
		return nil, err
	}
	if r.BumpVersion {
		v, err := q.BumpAuthVersion(ctx, r.UserID)
		if err != nil {
			return nil, err
		}
		pc.version(r.UserID, v)
	}
	var sids []uuid.UUID
	if r.Reason != RevokeClaimsChanged {
		rows, err := q.RevokeSessions(ctx, authdb.RevokeSessionsParams{
			At: now, Reason: r.Reason, RevokedBy: r.RevokedBy, UserID: r.UserID,
			OnlyIds: r.SessionIDs, ExceptID: r.Except,
		})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			sids = append(sids, row.ID)
		}
		if len(sids) > 0 {
			hashes, err := q.RevokeSessionTokens(ctx, authdb.RevokeSessionTokensParams{At: now, Reason: r.Reason, SessionIds: sids})
			if err != nil {
				return nil, err
			}
			pc.revoked(sids...)
			pc.dropHashes(hashes...)
		}
	}
	if r.Reason == RevokeClaimsChanged || len(sids) > 0 {
		if err := emitSessionsRevoked(ctx, tx, r.UserID, sids, r.Reason, r.RequestID); err != nil {
			return nil, err
		}
	}
	return sids, nil
}

// Revoke runs one revocation in its own transaction and applies it (no security event: callers that
// need one use RevokeInTx).
func (s *Service) Revoke(ctx context.Context, r Revocation) ([]uuid.UUID, error) {
	pc := newPostCommit()
	var sids []uuid.UUID
	err := s.systemTx(ctx, func(tx pgx.Tx, _ *authdb.Queries) error {
		var err error
		sids, err = s.revokeTx(ctx, tx, r, s.clock(), pc)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.Apply(ctx, pc)
	return sids, nil
}
