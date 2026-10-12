package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/apikey"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// HeaderAPIKey carries an api_keys secret (Appendix C §C.4.11). It is not Authorization, because a shim
// request (M2) also carries the end user's Firebase ID token as its bearer.
const HeaderAPIKey = "X-Api-Key"

// details.reason of a 401 invalid_token for a genuine key that may not be used, and of the 403 of a key
// used outside its scope (R82). An unknown, malformed or wrong secret gets no reason.
const (
	ReasonKeyRevoked        = "revoked"
	ReasonKeyExpired        = "expired"
	ReasonKeyTenantInactive = "tenant_inactive"
	ReasonMultipleCreds     = "multiple_credentials"
	ReasonKeyScope          = "api_key_scope"
)

// apiKeyTouchInterval bounds the writes of api_keys.last_used_at: at most one per key and interval.
const apiKeyTouchInterval = time.Minute

// AuthOption configures one RequireAuth handler.
type AuthOption func(*authPolicy)

// authPolicy is what a route admits besides a Go access token.
type authPolicy struct {
	keys []authz.APIKeyScope // machine principals of these scopes; none by default
	// shim is a callable shim target (RequireShim): a cf_shim key on the public listener, with a Firebase ID token.
	shim bool
}

// AllowAPIKeys admits machine principals (X-Api-Key) of the given scopes on the route, on the internal
// listener only (Appendix C §C.4.11, R82): integration and script keys on the routes their capabilities
// allow, release_publisher only on the two APK publish routes (T52). A route admits no API key unless its
// registration says so (Appendix B §B.1.4: "or an API key where stated"), because most routes write rows
// that name a users row (created_by, actor_user_id) a key does not have. cf_shim keys never pass here:
// they authenticate an end user together with a Firebase ID token on the public shim targets (RequireShim,
// first used by T38; T32 mounts the other targets); naming the scope panics at registration, as does an
// unknown scope.
func AllowAPIKeys(scopes ...authz.APIKeyScope) AuthOption {
	for _, s := range scopes {
		switch s {
		case authz.KeyIntegration, authz.KeyScript, authz.KeyReleasePublisher:
		case authz.KeyCFShim:
			panic("auth: cf_shim keys are served by the shim targets (RequireShim), not by AllowAPIKeys")
		default:
			panic("auth: AllowAPIKeys with an unknown scope " + string(s))
		}
	}
	return func(p *authPolicy) {
		for _, s := range scopes {
			if !slices.Contains(p.keys, s) {
				p.keys = append(p.keys, s)
			}
		}
	}
}

// apiKeyUsage writes api_keys.last_used_at in the background (Appendix C §C.4.11: never on the request
// path), at most once per key and apiKeyTouchInterval, and waits for the writes in flight at Close.
type apiKeyUsage struct {
	mu       sync.Mutex
	closed   bool
	wg       sync.WaitGroup
	inFlight map[uuid.UUID]bool
}

// authenticateAPIKey turns X-Api-Key into a machine principal (Appendix C §C.4.3 (3), §C.4.11):
//
//   - 401 invalid_token: a repeated header, API keys off (API_KEY_PEPPER unset), a value that is not a
//     secret of the ltk_ format, an unknown prefix or a wrong secret (no details, nothing to probe); for a
//     genuine key also when it is revoked, expired or its tenant is not active (details.reason), or when an
//     Authorization header came with it (exactly one credential per request, C.4.3);
//   - 429 resource_exhausted: the key's own bucket rl:apikey:{id} at api_keys.rate_per_min (B.6.3);
//   - 403 permission_denied (details.reason api_key_scope): a cf_shim key anywhere but a shim target of the
//     public listener (RequireShim), any other key on the public listener, and a key whose scope the route does
//     not admit (AllowAPIKeys).
//
// The secret never reaches a log line: rejections name the key's id and prefix only. RequireShim runs the same
// steps in another order (lookupAPIKey, admit, then the Firebase ID token, then the buckets), so a caller Go refuses
// never spends the budget a cf_shim key shares between every driver.
func (s *Service) authenticateAPIKey(c fiber.Ctx, vals [][]byte, withAuthorization bool, pol authPolicy) (*authz.Principal, error) {
	k, err := s.lookupAPIKey(c, vals, withAuthorization)
	if err != nil {
		return nil, err
	}
	if err := s.limitAPIKey(c, k.row.ID, k.row.RatePerMin); err != nil {
		return nil, err
	}
	if err := k.admit(c, pol); err != nil {
		return nil, err
	}
	return s.useAPIKey(k), nil
}

// genuineKey is an X-Api-Key whose secret matched a key that may be used (lookupAPIKey): not revoked, not expired,
// of an active tenant (or a platform key), and the request's only credential unless it is a cf_shim key. Nothing has
// been charged to its bucket yet.
type genuineKey struct {
	row authdb.GetAPIKeyByPrefixRow
	log zerolog.Logger // carries the key's id, prefix and scope, never the secret
}

// lookupAPIKey is the 401 part of authenticateAPIKey: the secret checked against its stored hash, then the key's
// state (revoked, expired, tenant not active) and the one-credential rule (an Authorization header with any key but
// a cf_shim key). It spends no rate-limit budget.
func (s *Service) lookupAPIKey(c fiber.Ctx, vals [][]byte, withAuthorization bool) (genuineKey, error) {
	ctx := c.Context()
	if len(vals) != 1 || !s.cfg.APIKeyPepper.Set() {
		return genuineKey{}, errInvalidToken()
	}
	secret := strings.TrimSpace(string(vals[0]))
	prefix, ok := apikey.Prefix(secret)
	if !ok {
		return genuineKey{}, errInvalidToken()
	}
	var row authdb.GetAPIKeyByPrefixRow
	err := s.system(ctx, func(q *authdb.Queries) (err error) {
		row, err = q.GetAPIKeyByPrefix(ctx, prefix)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return genuineKey{}, errInvalidToken()
	}
	if err != nil {
		return genuineKey{}, fmt.Errorf("auth: api key lookup: %w", err)
	}
	if !s.cfg.APIKeyPepper.Matches(secret, row.KeyHash) {
		return genuineKey{}, errInvalidToken()
	}
	// The caller holds a genuine key from here on.
	base := zerolog.Ctx(ctx)
	if base.GetLevel() == zerolog.Disabled {
		base = &s.log
	}
	k := genuineKey{row: row, log: base.With().Str("api_key_id", row.ID.String()).Str("api_key_prefix", prefix).
		Str("api_key_scope", row.Scope).Logger()}
	refuse := func(reason string) error {
		k.log.Warn().Str("reason", reason).Msg("auth: api key refused")
		return errInvalidToken().WithDetails(map[string]any{"reason": reason})
	}
	switch now := s.clock(); {
	case row.RevokedAt != nil:
		return genuineKey{}, refuse(ReasonKeyRevoked)
	case row.ExpiresAt != nil && !now.Before(*row.ExpiresAt):
		return genuineKey{}, refuse(ReasonKeyExpired)
	case row.TenantID != nil && (row.TenantStatus == nil || *row.TenantStatus != "active"):
		return genuineKey{}, refuse(ReasonKeyTenantInactive)
	case withAuthorization && k.scope() != authz.KeyCFShim:
		return genuineKey{}, refuse(ReasonMultipleCreds)
	}
	return k, nil
}

func (k genuineKey) scope() authz.APIKeyScope { return authz.APIKeyScope(k.row.Scope) }

// admit is the 403 part of authenticateAPIKey: whether the key's scope may use this route on this listener (R82).
func (k genuineKey) admit(c fiber.Ctx, pol authPolicy) error {
	if msg := keyScopeRefusal(k.scope(), httpx.ListenerFrom(c), pol); msg != "" {
		k.log.Warn().Str("reason", ReasonKeyScope).Str("path", c.Path()).Msg("auth: api key used outside its scope")
		return authz.ErrPermissionDenied(msg).WithDetails(map[string]any{"reason": ReasonKeyScope, "scope": k.row.Scope})
	}
	return nil
}

// useAPIKey is the machine principal of an admitted key; it records the use (last_used_at, in the background).
func (s *Service) useAPIKey(k genuineKey) *authz.Principal {
	row := k.row
	id := row.ID
	p := &authz.Principal{UserID: id, AMR: authz.AMRAPIKey, APIKeyID: &id, APIKeyScope: k.scope(),
		APIKeyCaps: make([]authz.Cap, 0, len(row.Capabilities))}
	if row.TenantID != nil {
		tid := *row.TenantID
		p.TenantID = &tid
	}
	for _, c := range row.Capabilities {
		p.APIKeyCaps = append(p.APIKeyCaps, authz.Cap(c))
	}
	s.touchAPIKey(id, row.LastUsedAt, s.clock())
	return p
}

// keyScopeRefusal is why a key of scope may not use this route ("" = it may): R82, Appendix C §C.4.11.
func keyScopeRefusal(scope authz.APIKeyScope, listener string, pol authPolicy) string {
	switch {
	case pol.shim && scope == authz.KeyCFShim && listener == ingress.Public:
		return ""
	case pol.shim && scope == authz.KeyCFShim:
		return "cf_shim keys are accepted on the public listener only"
	case pol.shim:
		return "the callable shim targets accept cf_shim keys only"
	case scope == authz.KeyCFShim:
		return "cf_shim keys serve only the callable shim targets, together with a Firebase ID token"
	case listener != ingress.Internal:
		return string(scope) + " keys are accepted on the internal listener only"
	case !slices.Contains(pol.keys, scope):
		return "this route does not accept " + string(scope) + " keys"
	}
	return ""
}

// limitAPIKey counts the request against the key's bucket rl:apikey:{id} at api_keys.rate_per_min
// (Appendix B §B.6.3). Like the other request buckets it fails open on a Redis error and is switched by
// RATE_LIMIT_ENABLED; over budget is 429 resource_exhausted with Retry-After.
func (s *Service) limitAPIKey(c fiber.Ctx, id uuid.UUID, perMin int32) error {
	if perMin < 1 {
		return nil
	}
	return s.limitRequest(c, ratelimit.APIKey, id.String(), ratelimit.Limit{Count: int(perMin), Window: time.Minute})
}

// limitShimUser counts a callable shim request against the end user's own bucket rl:shim_user:{key id}:{user id}
// (Appendix B §B.6.3, Appendix C §C.6.5): one user's calls through a cf_shim key cannot spend the budget the key
// shares between every driver (rl:apikey:{id}, then only an overall ceiling).
func (s *Service) limitShimUser(c fiber.Ctx, keyID, userID uuid.UUID) error {
	return s.limitRequest(c, ratelimit.ShimUser, keyID.String()+":"+userID.String(), ratelimit.ShimUser.Default)
}

// limitRequest counts one request of subject against bucket b at l, as limitAPIKey describes; over budget sets
// Retry-After on the response.
func (s *Service) limitRequest(c fiber.Ctx, b ratelimit.Bucket, subject string, l ratelimit.Limit) error {
	if !s.cfg.RateLimitEnabled {
		return nil
	}
	d, err := s.limiter.Allow(c.Context(), b.Name, subject, l)
	if errors.Is(err, ratelimit.ErrInvalidLimit) {
		return err
	}
	if err != nil {
		s.log.Warn().Err(err).Str("bucket", b.Name).Msg("auth: rate limit unavailable, request allowed")
		return nil
	}
	if !d.Allowed {
		e := errRateLimited(b.Name, d)
		if v, ok := retryAfterHeader(e); ok {
			c.Set(fiber.HeaderRetryAfter, v)
		}
		return e
	}
	return nil
}

// touchAPIKey records the use of a key in the background when last_used_at is older than
// apiKeyTouchInterval; one write per key is in flight at a time.
func (s *Service) touchAPIKey(id uuid.UUID, last *time.Time, now time.Time) {
	if last != nil && now.Sub(*last) < apiKeyTouchInterval {
		return
	}
	u := &s.keyUsage
	u.mu.Lock()
	if u.closed || u.inFlight[id] {
		u.mu.Unlock()
		return
	}
	if u.inFlight == nil {
		u.inFlight = map[uuid.UUID]bool{}
	}
	u.inFlight[id] = true
	u.wg.Add(1)
	u.mu.Unlock()
	go func() {
		defer func() {
			u.mu.Lock()
			delete(u.inFlight, id)
			u.mu.Unlock()
			u.wg.Done()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := s.system(ctx, func(q *authdb.Queries) error {
			return q.TouchAPIKey(ctx, authdb.TouchAPIKeyParams{ID: id, UsedAt: now})
		})
		if err != nil {
			s.log.Warn().Err(err).Str("api_key_id", id.String()).Msg("auth: api_keys.last_used_at not written")
		}
	}()
}

// closeKeyUsage stops new last_used_at writes and waits for those in flight.
func (s *Service) closeKeyUsage() {
	s.keyUsage.mu.Lock()
	s.keyUsage.closed = true
	s.keyUsage.mu.Unlock()
	s.keyUsage.wg.Wait()
}
