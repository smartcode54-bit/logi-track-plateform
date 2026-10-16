package auth

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// RequireAuth accepts exactly one credential (Appendix C §C.4.3): a Go access token as
// `Authorization: Bearer`, or an api_keys secret as X-Api-Key on a route whose options admit its scope
// (AllowAPIKeys; none by default, so every route without the option refuses keys with 403
// permission_denied). Then the Authorizer (iam.RBAC, T07) refuses the retired tenant header and completes the principal:
// steward flag, contractor reach and capabilities (a machine principal holds its key's capabilities
// only). The principal is stored for authz.PrincipalFrom. No credential is 401 unauthenticated; a
// Firebase ID token or any other non-Go token is 401 invalid_token on every route. (The cf_shim
// Firebase path joins with the shims, T32 M2; this middleware lives in auth rather than httpx because
// httpx cannot import auth.)
//
// RequireAuth never reads s while building the handler (cmd/api `routes` passes a nil *Service).
func (s *Service) RequireAuth(opts ...AuthOption) fiber.Handler {
	var pol authPolicy
	for _, o := range opts {
		o(&pol)
	}
	return func(c fiber.Ctx) error {
		var p *authz.Principal
		var err error
		if keys := c.Request().Header.PeekAll(HeaderAPIKey); len(keys) > 0 {
			withAuthorization := strings.TrimSpace(c.Get(fiber.HeaderAuthorization)) != ""
			p, err = s.authenticateAPIKey(c, keys, withAuthorization, pol)
		} else {
			raw, present := bearer(c)
			if !present {
				return httpx.ErrUnauthenticated()
			}
			p, err = s.Authenticate(c.Context(), raw)
		}
		if err != nil {
			return err
		}
		setPrincipal(c, p)
		if authz.IsSupport(p) {
			a := httpx.ProjectionAudienceFrom(c)
			a.Finalize = s.finalizeSupportAudience(p)
			httpx.SetProjectionAudience(c, a)
		}
		if s.gate != nil {
			if err := s.gate.Authorize(c, p); err != nil {
				return err
			}
		}
		if err := authz.RefuseTenantHeader(c); err != nil {
			return err
		}
		return c.Next()
	}
}

// refuseAPIKeys guards the two /v1/auth routes that authenticate their caller themselves instead of
// through RequireAuth (POST /v1/auth/logout, POST /v1/auth/password/change): a request that carries
// X-Api-Key gets exactly the answer RequireAuth() without AllowAPIKeys gives (Appendix B §B.1.4,
// Appendix C §C.4.3 (3)) before the handler reads the bearer: 401 invalid_token for an unknown, dead or
// repeated key or a key sent with an Authorization header (details.reason multiple_credentials), 403
// permission_denied (api_key_scope) for a genuine key alone. Without the guard the key was ignored and
// the bearer's user signed out or changed the password. Anonymous routes ignore the header.
func (s *Service) refuseAPIKeys(c fiber.Ctx) error {
	keys := c.Request().Header.PeekAll(HeaderAPIKey)
	if len(keys) == 0 {
		return c.Next()
	}
	withAuthorization := strings.TrimSpace(c.Get(fiber.HeaderAuthorization)) != ""
	if _, err := s.authenticateAPIKey(c, keys, withAuthorization, authPolicy{}); err != nil {
		return err
	}
	// Not reached: a policy without key scopes admits no key.
	return authz.ErrPermissionDenied("this route does not accept API keys").WithDetails(map[string]any{"reason": ReasonKeyScope})
}

func setPrincipal(c fiber.Ctx, p *authz.Principal) {
	authz.SetPrincipal(c, p)
	installSupportAudience(c, p)

	lc := zerolog.Ctx(c.Context()).With()
	if p.IsMachine() && p.APIKeyID != nil {
		lc = lc.Str("api_key_id", p.APIKeyID.String()).Str("api_key_scope", string(p.APIKeyScope))
	} else {
		lc = lc.Str("user_id", p.UserID.String()).Str("session_id", p.SessionID.String())
	}
	l := lc.Logger()
	c.SetContext(l.WithContext(c.Context()))
}

// PrincipalFrom returns the principal stored by RequireAuth, or nil (the same as authz.PrincipalFrom).
func PrincipalFrom(c fiber.Ctx) *authz.Principal { return authz.PrincipalFrom(c) }

// bearer returns the token of an `Authorization: Bearer <token>` header. present is false when no
// Authorization header (or another scheme) was sent.
func bearer(c fiber.Ctx) (string, bool) {
	h := strings.TrimSpace(c.Get(fiber.HeaderAuthorization))
	scheme, tok, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.Clone(strings.TrimSpace(tok)), true
}

// Authenticate verifies signature, iss, aud and exp in memory, then reads live session/context and
// user state in PostgreSQL before the Redis revocation pipeline (auth:sess:revoked:{sid}, auth:user:ver:{sub}). A version miss, and
// a token whose ver is newer than the cached version (the cache lags a bump whose post-commit raise
// was lost or is still in flight), read sessions.revoked_at and users.auth_version in PostgreSQL and
// rebuild both keys; with Redis unreachable the same read decides and auth_revocation_fallback_total
// counts it. Tokens already rejected by PostgreSQL admission never reach that pipeline or counter.
//
// Outcomes: a revoked session is session_revoked; a ver older than users.auth_version is token_expired
// with details.reason claims_changed (the refresh issues the new claims, R50, R78); an expired token is
// token_expired with reason expired; anything else wrong is invalid_token, including a ver newer than
// users.auth_version itself (never issued).
func (s *Service) Authenticate(ctx context.Context, raw string) (*authz.Principal, error) {
	c, err := s.keys.Parse(raw, s.now())
	if errors.Is(err, token.ErrExpired) {
		return nil, errTokenExpired(ReasonExpired)
	}
	if err != nil {
		return nil, errInvalidToken()
	}
	p, ok := principalFromClaims(c)
	if !ok {
		return nil, errInvalidToken()
	}
	if err := s.CheckSession(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// CheckSession is the revocation half of Authenticate, for a principal built earlier: the session is
// not revoked and the principal's AuthVersion is users.auth_version (same Redis pipeline, same
// PostgreSQL fallback, same outcomes). An SSE stream calls it once it has subscribed (T12, main spec
// §8.2): the stream authenticated before its subscription was live, and a revocation published in
// between would otherwise reach neither. Both the post-commit hook and the outbox relay's
// RevocationHook write the marker and raise the version before session.revoked is published, so every
// revocation published before this call is visible to it. A ticket principal (SSETicketPrincipal)
// carries the version read at redemption.
func (s *Service) CheckSession(ctx context.Context, p *authz.Principal) error {
	if p == nil {
		return errInvalidToken()
	}
	if p.IsMachine() {
		return nil
	}
	if p.SessionID == uuid.Nil {
		if p.AMR == AMRFirebase && p.AccessMode != "support" && !authz.IsSupport(p) {
			return nil
		}
		return errInvalidToken()
	}
	if err := s.checkContext(ctx, p); err != nil {
		return err
	}
	revoked, current, err := s.revocation(ctx, p)
	if err != nil {
		return err
	}
	switch {
	case revoked:
		return errSessionRevoked()
	case p.AuthVersion < current:
		return errTokenExpired(ReasonClaimsChanged)
	case p.AuthVersion > current:
		return errInvalidToken() // never issued: versions only grow
	}
	return nil
}

// redisCheckTimeout bounds the per-request Redis pipeline: a slow or unreachable Redis falls back to
// PostgreSQL instead of holding every request.
const redisCheckTimeout = 300 * time.Millisecond

func (s *Service) revocation(ctx context.Context, p *authz.Principal) (revoked bool, ver int32, err error) {
	rctx, cancel := context.WithTimeout(ctx, redisCheckTimeout)
	rev, cached, known, rerr := s.store.revocationState(rctx, p.SessionID, p.UserID)
	cancel()
	if rerr == nil {
		if rev || (known && p.AuthVersion <= cached) {
			return rev, cached, nil
		}
		// A miss, or a token newer than the cache: PostgreSQL decides. Only genuine (signed) tokens get
		// here, and the cache is rebuilt, so the read happens once per bump.
		st, err := s.sessionAuthState(ctx, p)
		if err != nil {
			return false, 0, err
		}
		if err := s.store.raiseVersion(ctx, p.UserID, st.AuthVersion); err != nil {
			s.log.Warn().Err(err).Msg("auth: cache auth_version")
		}
		if st.RevokedAt != nil { // a revoked marker that never landed
			if err := s.store.markRevoked(ctx, s.keys.TTL()+token.Leeway, p.SessionID); err != nil {
				s.log.Warn().Err(err).Msg("auth: rebuild revoked marker")
			}
		}
		return st.RevokedAt != nil, st.AuthVersion, nil
	}
	s.fallback.Inc()
	s.log.Warn().Err(rerr).Msg("auth: Redis unreachable; revocation checked in PostgreSQL")
	st, err := s.sessionAuthState(ctx, p)
	if err != nil {
		return false, 0, err
	}
	return st.RevokedAt != nil, st.AuthVersion, nil
}

// sessionAuthState reads users.auth_version and sessions.revoked_at of the token's session; a session
// that does not exist (or is another user's) is invalid_token.
func (s *Service) sessionAuthState(ctx context.Context, p *authz.Principal) (authdb.SessionAuthStateRow, error) {
	var st authdb.SessionAuthStateRow
	err := s.system(ctx, func(q *authdb.Queries) error {
		var err error
		st, err = q.SessionAuthState(ctx, authdb.SessionAuthStateParams{SessionID: p.SessionID, UserID: p.UserID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return st, errInvalidToken()
	}
	return st, err
}

var tenantRoles = []authz.TenantRole{authz.TenantAdmin, authz.Manager, authz.OperationStaff, authz.Operator, authz.User, authz.Driver}

// principalFromClaims checks the private claims of a verified token and builds the principal.
func principalFromClaims(c *token.Claims) (*authz.Principal, bool) {
	uid, err1 := uuid.Parse(c.Subject)
	sid, err2 := uuid.Parse(c.SessionID)
	if err1 != nil || err2 != nil || uid == uuid.Nil || c.ID == "" || c.Version < 1 || (c.AMR != AMRPassword && c.AMR != "google") {
		return nil, false
	}
	p := &authz.Principal{UserID: uid, SessionID: sid, AuthVersion: c.Version, AMR: c.AMR, TokenID: c.ID,
		Dispatcher: c.Dispatcher}
	if c.ExpiresAt != nil {
		p.TokenExpiresAt = c.ExpiresAt.Time
	}
	if c.TenantID != "" {
		tid, err := uuid.Parse(c.TenantID)
		role := authz.TenantRole(c.Role)
		if err != nil || (!slices.Contains(tenantRoles, role) && !(c.AccessMode == "support" && role == authz.SupportReadOnly)) {
			return nil, false
		}
		p.TenantID, p.TenantRole = &tid, role
	} else if c.Role != "" {
		return nil, false
	}
	if c.DriverID != "" {
		did, err := uuid.Parse(c.DriverID)
		if err != nil || p.TenantRole != authz.Driver {
			return nil, false
		}
		p.DriverID = &did
	}
	for _, r := range c.Platform {
		pr := authz.PlatformRole(r)
		if pr != authz.PlatformAdmin && pr != authz.Support {
			return nil, false
		}
		p.Platform = append(p.Platform, pr)
	}
	for _, v := range c.CustomerScopes {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, false
		}
		p.PartyIDs = append(p.PartyIDs, id)
	}
	p.AccessMode = "member"
	if c.AccessMode != "" && c.AccessMode != "support" {
		return nil, false
	}
	if c.ContextVersion != "" {
		v, err := parseVersion(c.ContextVersion)
		if err != nil {
			return nil, false
		}
		p.ContextVersion = v
	}
	if c.AccessMode == "support" {
		v, err := parseVersion(c.SupportAccessVersion)
		if err != nil || c.ContextVersion == "" || p.TenantID == nil || p.TenantRole != authz.SupportReadOnly || !p.HasPlatform(authz.PlatformAdmin) || p.Dispatcher || p.DriverID != nil || len(p.PartyIDs) != 0 {
			return nil, false
		}
		p.AccessMode, p.SupportAccessVersion = "support", v
	} else if c.SupportAccessVersion != "" {
		return nil, false
	}
	return p, true
}

func parseVersion(s string) (int64, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 || strconv.FormatInt(v, 10) != s {
		return 0, errors.New("invalid context version")
	}
	return v, nil
}
