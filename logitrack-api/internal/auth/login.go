package auth

import (
	"bytes"
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// AMRPassword is the amr claim of a password session.
const AMRPassword = "pwd"

// LoginInput is the body of POST /v1/auth/login (R83) plus the request facts the handler adds.
type LoginInput struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	Platform   string `json:"platform"`
	InstallID  string `json:"installId"`
	AppVersion string `json:"appVersion"`
	Geo        *Geo   `json:"geo"`

	IP        string `json:"-"`
	UserAgent string `json:"-"`
	RequestID string `json:"-"`
}

// Geo is a login location (users.last_login_*); source is gps or ip.
type Geo struct {
	Lat       *float64 `json:"lat"`
	Lng       *float64 `json:"lng"`
	Source    string   `json:"source"`
	AccuracyM *float64 `json:"accuracyM"`
}

// LoginResult is the 200 body of POST /v1/auth/login.
type LoginResult struct {
	TokenPair
	Tenants         []Tenant   `json:"tenants"`
	DefaultTenantID *uuid.UUID `json:"defaultTenantId"`
}

// pwchgTicket is the value of auth:pwchg:{ticket} (R79). AuthVersion binds it to the credential state
// it was issued for: a reset, an admin action or a new temporary password bumps users.auth_version
// and voids every ticket issued before.
type pwchgTicket struct {
	UserID      uuid.UUID `json:"userId"`
	AuthVersion int32     `json:"authVersion"`
}

// credential is the stored credential a login verified. The session is opened (and a legacy or weaker
// hash replaced) only while the row still holds it, so a password set after the check, by a reset or
// a change that commits while the hash runs, wins over the racing login.
type credential struct {
	hash   *string
	legacy []byte
}

func credentialOf(u *authdb.GetUserForLoginRow) credential {
	return credential{hash: u.PasswordHash, legacy: u.LegacyScryptHash}
}

// heldBy reports whether the locked row still holds the verified credential.
func (c credential) heldBy(l authdb.LockUserRow) bool {
	if (l.PasswordHash == nil) != (c.hash == nil) || (c.hash != nil && *l.PasswordHash != *c.hash) {
		return false
	}
	return bytes.Equal(l.LegacyScryptHash, c.legacy)
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func validateLogin(in *LoginInput) []httpx.FieldViolation {
	var v []httpx.FieldViolation
	in.Email = strings.TrimSpace(in.Email)
	if in.Email == "" || len(in.Email) > 320 || !utf8.ValidString(in.Email) {
		v = append(v, httpx.FieldViolation{Field: "email", Reason: "required"})
	}
	if in.Password == "" {
		v = append(v, httpx.FieldViolation{Field: "password", Reason: "required"})
	}
	switch in.Platform {
	case PlatformWeb, PlatformAndroid, PlatformIOS, PlatformScript:
	default:
		v = append(v, httpx.FieldViolation{Field: "platform", Reason: "one_of",
			Params: map[string]any{"values": []string{PlatformWeb, PlatformAndroid, PlatformIOS, PlatformScript}}})
	}
	if len(in.InstallID) > 200 || !utf8.ValidString(in.InstallID) {
		v = append(v, httpx.FieldViolation{Field: "installId", Reason: "too_long"})
	}
	if len(in.AppVersion) > 64 || !utf8.ValidString(in.AppVersion) {
		v = append(v, httpx.FieldViolation{Field: "appVersion", Reason: "too_long"})
	}
	return append(v, validateGeo("geo", in.Geo)...)
}

func validateGeo(field string, g *Geo) []httpx.FieldViolation {
	if g == nil {
		return nil
	}
	var v []httpx.FieldViolation
	if g.Lat == nil || math.IsNaN(*g.Lat) || *g.Lat < -90 || *g.Lat > 90 {
		v = append(v, httpx.FieldViolation{Field: field + ".lat", Reason: "out_of_range"})
	}
	if g.Lng == nil || math.IsNaN(*g.Lng) || *g.Lng < -180 || *g.Lng > 180 {
		v = append(v, httpx.FieldViolation{Field: field + ".lng", Reason: "out_of_range"})
	}
	if g.Source != "gps" && g.Source != "ip" {
		v = append(v, httpx.FieldViolation{Field: field + ".source", Reason: "one_of",
			Params: map[string]any{"values": []string{"gps", "ip"}}})
	}
	if g.AccuracyM != nil && (math.IsNaN(*g.AccuracyM) || *g.AccuracyM < 0) {
		v = append(v, httpx.FieldViolation{Field: field + ".accuracyM", Reason: "out_of_range"})
	}
	return v
}

// limit counts one request of subject against bucket b in the shared GCRA limiter (Appendix B
// §B.6.3, Appendix C §C.4.12): login_ip at RATE_LIMIT_LOGIN, every other auth bucket at its design
// default. Over budget is 429 resource_exhausted with Retry-After and details.bucket, as from
// ratelimit.Middleware. Redis errors fail open (logged and counted by the limiter): the request
// buckets protect capacity, not credentials. An invalid limit is a bug and is returned as one (New
// checks RATE_LIMIT_LOGIN, the defaults are valid by construction).
func (s *Service) limit(ctx context.Context, b ratelimit.Bucket, subject string) error {
	if !s.cfg.RateLimitEnabled {
		return nil
	}
	l := b.Default
	if b.Name == ratelimit.LoginIP.Name {
		l = s.cfg.LoginIP
	}
	d, err := s.limiter.Allow(ctx, b.Name, subject, l)
	if errors.Is(err, ratelimit.ErrInvalidLimit) {
		return err
	}
	if err != nil {
		s.log.Warn().Err(err).Str("bucket", b.Name).Msg("auth: rate limit unavailable, request allowed")
		return nil
	}
	if !d.Allowed {
		return errRateLimited(b.Name, d)
	}
	return nil
}

// ipSubject is the rate-limit subject of a client address (ratelimit.IPSubject: IPv6 by its /64). A
// request without one still counts, against a shared "unknown" subject, so it never skips the limit.
func ipSubject(ip string) string {
	if s := ratelimit.IPSubject(ip); s != "" {
		return s
	}
	return "unknown"
}

// Login is POST /v1/auth/login (Appendix C §C.5.4, R79, R83).
//
// The attempt is counted against the email before the password is checked (rl:login_fail, atomic), so
// concurrent attempts cannot all pass the lockout; a success clears the count. Every failed check costs
// the same memory-hard work whichever credential the account holds (verify). The session is opened,
// and a legacy or weaker hash replaced, only while the users row still holds the credential that was
// verified (credential.heldBy under LockUser).
func (s *Service) Login(ctx context.Context, in LoginInput) (*LoginResult, error) {
	if v := validateLogin(&in); len(v) > 0 {
		return nil, httpx.ErrInvalidArgument(v...)
	}
	if err := s.limit(ctx, ratelimit.LoginIP, ipSubject(in.IP)); err != nil {
		return nil, err
	}
	subject := emailSubject(in.Email)
	attempt, ttl, err := s.store.reserveAttempt(ctx, subject, LockoutThreshold, LockoutWindow)
	reserved := err == nil
	switch {
	case err != nil:
		s.log.Warn().Err(err).Msg("auth: lockout state unavailable")
	case attempt > LockoutThreshold:
		return nil, errLocked(ttl)
	}
	// release takes the attempt back when the request ends before the password was judged.
	release := func() {
		if reserved {
			if err := s.store.releaseAttempt(context.WithoutCancel(ctx), subject); err != nil {
				s.log.Warn().Err(err).Msg("auth: release sign-in attempt")
			}
		}
	}

	var u *authdb.GetUserForLoginRow
	if err := s.system(ctx, func(q *authdb.Queries) error {
		row, err := q.GetUserForLogin(ctx, normalizeEmail(in.Email))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		u = &row
		return nil
	}); err != nil {
		release()
		return nil, err
	}
	ok, rehash, err := s.verify(ctx, in.Password, u)
	if err != nil {
		release()
		return nil, kdfError(err)
	}
	if u != nil && u.Status == "reset_required" {
		ok = false // the 180-day tail: no usable credential, generic answer (C.5.8)
	}
	if !ok {
		return nil, s.loginFailed(ctx, in, attempt, u)
	}
	if err := s.store.clearFailures(ctx, subject); err != nil {
		s.log.Warn().Err(err).Msg("auth: clear failed sign-ins")
	}
	cred := credentialOf(u)
	if rehash != "" {
		// Verify-then-rehash (C.5.4): the plaintext is unchanged, so no version bump, no
		// password_changed_at and no Firebase mirror; the Firebase hash stays valid for APK 3.x. Only
		// while the row still holds the verified credential: a password set meanwhile is never undone.
		if err := s.system(ctx, func(q *authdb.Queries) error {
			l, err := q.LockUser(ctx, u.ID)
			if err != nil {
				return err
			}
			if !cred.heldBy(l) {
				return errInvalidCredentials() // the password changed after it was verified
			}
			return q.RehashPassword(ctx, authdb.RehashPasswordParams{PasswordHash: rehash, ID: u.ID})
		}); err != nil {
			return nil, err
		}
		cred = credential{hash: &rehash}
	}
	if u.Status == "disabled" {
		return nil, errAccountDisabled()
	}
	if u.MustChangePassword {
		return nil, s.passwordChangeTicket(ctx, u.ID, u.AuthVersion)
	}
	g := grant{amr: AMRPassword, cred: &cred}
	res, err := s.openSession(ctx, u.ID, g, in)
	if uniqueViolation(err, "sessions_user_install_live") {
		res, err = s.openSession(ctx, u.ID, g, in) // a concurrent login of the same install won the insert
	}
	return res, err
}

// uniqueViolation reports a 23505 on one of the named constraints.
func uniqueViolation(err error, constraints ...string) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23505" && slices.Contains(constraints, pgErr.ConstraintName)
}

// Memory-hard computations observed by Service.kdf (tests).
const (
	kdfArgon2id = "argon2id"
	kdfScrypt   = "scrypt"
)

func (s *Service) note(kind string) {
	if s.kdf != nil {
		s.kdf(kind)
	}
}

// verify checks pw against the stored credential. rehash is a new Argon2id hash when the stored one is
// a legacy Firebase scrypt hash or has weaker parameters. err is only a hashing failure that says
// nothing about the credential (every slot busy, the request cancelled): the caller answers 503.
//
// Every failed check costs one Argon2id verification and, while FIREBASE_SCRYPT_* is configured, one
// Firebase scrypt verification, whichever credential the account holds (an unknown email, a
// Google-only or reset_required user, an Argon2id or a legacy user): response time does not reveal
// whether an account exists or whether it has signed in since the import (C.4.8). A successful check
// may cost less; only the holder of the password sees it.
func (s *Service) verify(ctx context.Context, pw string, u *authdb.GetUserForLoginRow) (ok bool, rehash string, err error) {
	var ranArgon, ranScrypt bool
	defer func() {
		if ok || err != nil {
			return
		}
		if !ranArgon {
			s.note(kdfArgon2id)
			err = s.hasher.DummyVerify(ctx, pw)
		}
		if err == nil && !ranScrypt && s.cfg.Scrypt != nil {
			s.note(kdfScrypt)
			err = s.hasher.Gate(ctx, func() { firebasescrypt.DummyVerify(truncateBytes(pw, password.MaxBytes), *s.cfg.Scrypt) })
		}
	}()
	switch {
	case u == nil:
		return false, "", nil
	case u.PasswordHash != nil:
		match, weaker, verr := s.hasher.Verify(ctx, pw, *u.PasswordHash)
		if errors.Is(verr, password.ErrBusy) || errors.Is(verr, context.Canceled) || errors.Is(verr, context.DeadlineExceeded) {
			return false, "", verr
		}
		if verr != nil {
			s.log.Error().Err(verr).Str("user_id", u.ID.String()).Msg("auth: stored password hash unreadable")
			return false, "", nil // the padding still runs a full Argon2id verification
		}
		ranArgon = true
		s.note(kdfArgon2id)
		if match && weaker {
			if rehash, err = s.rehash(ctx, pw, u.ID); err != nil {
				return false, "", err
			}
		}
		return match, rehash, nil
	case len(u.LegacyScryptHash) > 0 && s.cfg.Scrypt != nil:
		var match bool
		var serr error
		if err := s.hasher.Gate(ctx, func() {
			match, serr = firebasescrypt.Verify(pw, u.LegacyScryptSalt, u.LegacyScryptHash, *s.cfg.Scrypt)
		}); err != nil {
			return false, "", err
		}
		if serr != nil {
			s.log.Error().Err(serr).Str("user_id", u.ID.String()).Msg("auth: legacy hash verification failed")
			return false, "", nil
		}
		ranScrypt = true
		s.note(kdfScrypt)
		if match {
			if rehash, err = s.rehash(ctx, pw, u.ID); err != nil {
				return false, "", err
			}
		}
		return match, rehash, nil
	default:
		return false, "", nil
	}
}

// rehash computes the replacement Argon2id hash of a verified password. A failure other than a busy
// or cancelled hasher keeps the stored hash (logged) and the login goes on.
func (s *Service) rehash(ctx context.Context, pw string, uid uuid.UUID) (string, error) {
	h, err := s.hasher.Hash(ctx, pw)
	switch {
	case err == nil:
		return h, nil
	case errors.Is(err, password.ErrBusy) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return "", err
	default:
		s.log.Error().Err(err).Str("user_id", uid.String()).Msg("auth: re-hash failed; keeping the stored hash")
		return "", nil
	}
}

// truncateBytes cuts v to at most n bytes (a dummy computation needs no rune boundary).
func truncateBytes(v string, n int) string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

// loginFailed queues the login_failed event, and login_lockout when this attempt was the one that
// reached the threshold, for the security.audit consumer (C.4.13). attempt is the reserved count (0
// when Redis was unavailable). It always answers the generic 401 invalid_credentials.
func (s *Service) loginFailed(ctx context.Context, in LoginInput, attempt int64, u *authdb.GetUserForLoginRow) error {
	now := s.clock()
	email := normalizeEmail(in.Email)
	var target *uuid.UUID
	if u != nil {
		target = &u.ID
	}
	events := []security.Event{{
		EventType: "login_failed", Severity: security.SeverityInfo, Summary: "failed password sign-in",
		Details:    map[string]any{"platform": in.Platform, "ip": in.IP, "knownUser": u != nil},
		ActorEmail: &email, TargetUserID: target, RequestID: in.RequestID, OccurredAt: now,
	}}
	if attempt == LockoutThreshold {
		events = append(events, security.Event{
			EventType: "login_lockout", Severity: security.SeverityWarning, Summary: "email locked after repeated failed sign-ins",
			Details:    map[string]any{"failures": attempt, "lockedForSeconds": int(LockoutWindow.Seconds()), "ip": in.IP},
			ActorEmail: &email, TargetUserID: target, RequestID: in.RequestID, OccurredAt: now,
		})
	}
	if err := s.systemTx(ctx, func(tx pgx.Tx, _ *authdb.Queries) error {
		for _, e := range events {
			if err := emitSecurityEvent(ctx, tx, e); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		s.log.Error().Err(err).Msg("auth: queue login security events")
	}
	return errInvalidCredentials()
}

// passwordChangeTicket answers a must_change_password login (R79): no tokens, a single-use ticket bound
// to the user's current auth_version.
func (s *Service) passwordChangeTicket(ctx context.Context, uid uuid.UUID, ver int32) error {
	t, err := newSecret()
	if err != nil {
		return err
	}
	if err := s.store.putTicket(ctx, "pwchg", t, pwchgTicket{UserID: uid, AuthVersion: ver}, PasswordChangeTicketTTL); err != nil {
		return httpx.ErrUnavailable("password change ticket store unavailable").Wrap(err)
	}
	return errPasswordChangeRequired(t, PasswordChangeTicketTTL)
}

// loggedInPayload is the payload of outbox user.logged_in (not audited, R22).
type loggedInPayload struct {
	UserID    uuid.UUID  `json:"userId"`
	SessionID uuid.UUID  `json:"sessionId"`
	Platform  string     `json:"platform"`
	AMR       string     `json:"amr"`
	TenantID  *uuid.UUID `json:"tenantId"`
}

// grant is what a sign-in proved; openSession creates the session only while it still holds.
type grant struct {
	amr string // amr claim and sessions.amr: AMRPassword or AMRGoogle
	// cred, for a password sign-in, is the verified credential: the row must still hold it (a password
	// set since wins).
	cred *credential
	// googleSub, for a Google sign-in, is the linked Google account: the link must still exist; its
	// last_used_at is written in the session transaction.
	googleSub string
}

// openSession creates the session and the first token pair in one transaction: the default tenant is
// the one the user last worked in (else own fleet first), the new session stores it as
// active_tenant_id, last_login_* is written and user.logged_in queued. The cached auth_version is raised
// to the issued one after COMMIT.
func (s *Service) openSession(ctx context.Context, uid uuid.UUID, g grant, in LoginInput) (*LoginResult, error) {
	pc := newPostCommit()
	var res *LoginResult
	err := s.systemTx(ctx, func(tx pgx.Tx, q *authdb.Queries) error {
		now := s.clock()
		user, err := q.LockUser(ctx, uid)
		if err != nil {
			return err
		}
		switch {
		case g.cred != nil && !g.cred.heldBy(user):
			return errInvalidCredentials() // the password changed after it was verified
		case user.Status == "disabled":
			return errAccountDisabled()
		case user.Status != "active" || user.MustChangePassword:
			return errInvalidCredentials() // changed since the credential check; the next attempt sees it
		}
		if g.googleSub != "" {
			n, err := q.TouchGoogleIdentity(ctx, authdb.TouchGoogleIdentityParams{At: now, Subject: g.googleSub, UserID: uid})
			if err != nil {
				return err
			}
			if n == 0 {
				return errNoAccount() // unlinked after it was resolved
			}
		}
		pc.issue(uid, user.AuthVersion)
		a, err := loadAxes(ctx, q, uid)
		if err != nil {
			return err
		}
		last, err := q.LastActiveTenant(ctx, uid)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		m := a.pickTenant(last)
		var tid *uuid.UUID
		if m != nil {
			tid = &m.TenantID
		}
		sid, refresh, err := s.createSession(ctx, q, newSessionInput{
			UserID: uid, Platform: in.Platform, AMR: g.amr, TenantID: tid, InstallID: in.InstallID,
			AppVersion: in.AppVersion, IP: in.IP, UserAgent: in.UserAgent,
		}, now, pc)
		if err != nil {
			return err
		}
		c, err := a.claims(sid, user.AuthVersion, g.amr, m)
		if err != nil {
			return err
		}
		access, err := s.signAccess(c, uid, now)
		if err != nil {
			return err
		}
		geo := authdb.TouchLastLoginParams{At: now, ID: uid}
		if in.Geo != nil {
			geo.Lat, geo.Lng, geo.GeoSource, geo.AccuracyM = in.Geo.Lat, in.Geo.Lng, &in.Geo.Source, in.Geo.AccuracyM
		}
		if err := q.TouchLastLogin(ctx, geo); err != nil {
			return err
		}
		if err := insertOutbox(ctx, tx, outboxEvent{
			RoutingKey: RouteUserLoggedIn, AggregateType: "user", AggregateID: uid.String(), TenantID: tid,
			Payload:   loggedInPayload{UserID: uid, SessionID: sid, Platform: in.Platform, AMR: g.amr, TenantID: tid},
			RequestID: in.RequestID,
		}); err != nil {
			return err
		}
		res = &LoginResult{
			TokenPair: TokenPair{AccessToken: access, ExpiresIn: s.expiresIn(), RefreshToken: refresh},
			Tenants:   a.tenants(), DefaultTenantID: tid,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.Apply(ctx, pc)
	return res, nil
}
