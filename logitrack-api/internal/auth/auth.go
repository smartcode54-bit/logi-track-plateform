// Package auth owns identity from P0 (main spec §4, Appendix C §C.4-§C.5): password login with Argon2id
// and the verify-then-rehash path for imported Firebase scrypt hashes, Ed25519 access JWTs, sessions with
// rotating refresh-token families and a 30 s reuse grace (R37), revocation by session and auth_version
// (R50, R78), the must-change-password ticket (R79), forgot / reset / change password, Google sign-in
// with GIS / google_sign_in ID tokens (T06, C.4.10), the current principal endpoints (/v1/me*) and the
// per-request RequireAuth middleware.
//
// Go returns tokens in JSON bodies and never sets cookies: the web BFF does (R38). Every database
// statement runs inside db.WithSystem (R12); every security-relevant change appends its security_events
// row (through security.Append) and outbox events in the same transaction (C.4.13, R85). Redis holds a
// hot index plus the revocation markers, whose post-commit writes are retried until they no longer
// matter (Apply).
//
// Lock order: every transaction that writes sessions or refresh_tokens locks the user's row first
// (LockUser), then sessions, then refresh_tokens, so refreshes, logins, tenant switches, password
// changes and revocations of one user serialise instead of deadlocking (C.4.4). RevokeInTx callers keep
// that order.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/google"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Fixed rules of Appendix C (code constants; changing one is a code change reviewed with the security
// tests, C.4.12).
const (
	// ReuseGrace is the window in which a rotated refresh token still yields a sibling pair (R37).
	ReuseGrace = 30 * time.Second
	// WebAbsoluteTTL caps a web session however often it slides (C.4.4).
	WebAbsoluteTTL = 30 * 24 * time.Hour
	// PasswordChangeTicketTTL is the life of a must-change-password ticket (R79).
	PasswordChangeTicketTTL = 10 * time.Minute
	// SSETicketTTL is the life of a mobile SSE ticket (C.4.6).
	SSETicketTTL = 60 * time.Second
	// LockoutThreshold failed sign-ins per email within LockoutWindow lock the email for LockoutWindow.
	LockoutThreshold = 5
	LockoutWindow    = 15 * time.Minute
)

// Limit is a fixed-window request budget.
type Limit struct {
	Count  int
	Window time.Duration
}

// Request buckets of Appendix B §B.6.3 used by the auth routes. login_ip comes from RATE_LIMIT_LOGIN;
// the others are code constants.
var (
	limitRefreshSession = Limit{60, time.Minute}
	limitForgotEmail    = Limit{3, time.Hour}
	limitForgotIP       = Limit{20, time.Hour}
	limitResetIP        = Limit{10, time.Hour}
	limitSSETicket      = Limit{30, time.Minute}
	limitGoogleIP       = Limit{30, time.Minute}
	limitGoogleNonceIP  = Limit{60, time.Minute}
)

// Config is the auth part of the api configuration (JWT_*, REFRESH_TOKEN_TTL_*, PASSWORD_*, ARGON2_*,
// FIREBASE_SCRYPT_*, RATE_LIMIT_*; main spec §16.1).
type Config struct {
	RefreshTTLWeb    time.Duration          // REFRESH_TOKEN_TTL_WEB: sliding, capped by WebAbsoluteTTL
	RefreshTTLMobile time.Duration          // REFRESH_TOKEN_TTL_MOBILE: absolute, no sliding
	PasswordResetTTL time.Duration          // PASSWORD_RESET_TTL
	Scrypt           *firebasescrypt.Params // nil: legacy hashes cannot be verified
	RateLimitEnabled bool                   // RATE_LIMIT_ENABLED: the request buckets (the lockout always applies)
	LoginIP          Limit                  // RATE_LIMIT_LOGIN
}

// CapabilityResolver returns the effective capability keys of a principal for GET /v1/me. The catalog,
// role defaults and overrides are T07 (internal/authz); until then GET /v1/me lists none.
type CapabilityResolver interface {
	Capabilities(ctx context.Context, p *authz.Principal) ([]string, error)
}

// GoogleVerifier checks a Google ID token (internal/auth/google.Verifier): signature, issuer, expiry, aud
// in GOOGLE_OIDC_ALLOWED_CLIENT_IDS and email_verified. A rejected token is a *google.InvalidError, an
// unreachable Google wraps google.ErrUnavailable.
type GoogleVerifier interface {
	Verify(ctx context.Context, rawIDToken string) (*google.Identity, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	Pool         db.Beginner
	Store        *Store
	Keys         *token.KeySet
	Hasher       *password.Hasher
	Policy       password.Policy
	Log          zerolog.Logger
	Capabilities CapabilityResolver // optional
	Google       GoogleVerifier     // optional: nil (GOOGLE_OIDC_ALLOWED_CLIENT_IDS unset) turns Google sign-in off (404)
	Now          func() time.Time   // optional, defaults to time.Now
}

// Service implements the auth use cases.
type Service struct {
	cfg      Config
	pool     db.Beginner
	store    *Store
	keys     *token.KeySet
	hasher   *password.Hasher
	policy   password.Policy
	log      zerolog.Logger
	caps     CapabilityResolver
	google   GoogleVerifier
	now      func() time.Time
	fallback prometheus.Counter
	// postCommitFailed counts post-commit Redis writes that failed (op: version, revoked, rt_drop,
	// rt_put); the security-relevant ones are retried in the background (Apply).
	postCommitFailed *prometheus.CounterVec
	retry            *retrier
	// kdf, when set (tests), observes every memory-hard computation of the login path.
	kdf func(kind string)
}

// New builds the service.
func New(cfg Config, d Deps) (*Service, error) {
	if d.Pool == nil || d.Store == nil || d.Keys == nil || d.Hasher == nil {
		return nil, errors.New("auth: pool, store, keys and hasher are required")
	}
	if d.Policy.MinLength < 8 {
		return nil, errors.New("auth: a password policy (password.NewPolicy) is required")
	}
	if cfg.RefreshTTLWeb <= 0 || cfg.RefreshTTLMobile <= 0 || cfg.PasswordResetTTL <= 0 {
		return nil, errors.New("auth: token lifetimes must be positive")
	}
	if cfg.RateLimitEnabled && (cfg.LoginIP.Count < 1 || cfg.LoginIP.Window <= 0) {
		return nil, errors.New("auth: RATE_LIMIT_LOGIN must be count/window")
	}
	s := &Service{
		cfg: cfg, pool: d.Pool, store: d.Store, keys: d.Keys, hasher: d.Hasher, policy: d.Policy,
		log: d.Log, caps: d.Capabilities, google: d.Google, now: d.Now,
		fallback: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "auth_revocation_fallback_total",
			Help: "Per-request revocation checks answered from PostgreSQL because Redis was unreachable.",
		}),
		postCommitFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "auth_postcommit_failures_total",
			Help: "Post-commit Redis writes of auth that failed (version and revoked are retried until the access-token lifetime ends).",
		}, []string{"op"}),
		retry: newRetrier(),
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Register adds the service's metrics (auth_revocation_fallback_total, auth_postcommit_failures_total)
// to reg.
func (s *Service) Register(reg prometheus.Registerer) error {
	if err := reg.Register(s.fallback); err != nil {
		return err
	}
	return reg.Register(s.postCommitFailed)
}

// Close stops the background retries of post-commit writes and waits for them; call it before the
// Redis client is closed.
func (s *Service) Close() { s.retry.close(s) }

// clock returns the current instant at PostgreSQL precision.
func (s *Service) clock() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

// system runs fn in a WithSystem transaction with the generated queries bound to it.
func (s *Service) system(ctx context.Context, fn func(q *authdb.Queries) error) error {
	return db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error { return fn(authdb.New(tx)) })
}

// systemTx is system for transactions that also append a security_events row (security.Append needs
// the transaction itself).
func (s *Service) systemTx(ctx context.Context, fn func(tx pgx.Tx, q *authdb.Queries) error) error {
	return db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error { return fn(tx, authdb.New(tx)) })
}

// kdfError maps a hashing failure that is not about the credential: every hashing slot busy is 503
// unavailable, a cancelled request its context error, anything else stays internal.
func kdfError(err error) error {
	if errors.Is(err, password.ErrBusy) {
		return httpx.ErrUnavailable("password hashing is saturated; retry shortly").Wrap(err)
	}
	return err
}

// newSecret returns 32 random bytes, base64url without padding (43 characters): refresh tokens,
// password-reset tokens and single-use tickets.
func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// secretHash is sha256 of a presented secret; ok is false when the value cannot be one of ours.
func secretHash(v string) (sum []byte, hexSum string, ok bool) {
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || len(b) != 32 {
		return nil, "", false
	}
	h := sha256.Sum256([]byte(v))
	return h[:], hex.EncodeToString(h[:]), true
}

// emailSubject keys the per-email buckets by sha256 of the lower-cased address, so Redis never holds
// the email itself.
func emailSubject(email string) string {
	h := sha256.Sum256([]byte(normalizeEmail(email)))
	return hex.EncodeToString(h[:])
}
