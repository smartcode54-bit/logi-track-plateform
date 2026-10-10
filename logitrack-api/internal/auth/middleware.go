package auth

import (
	"context"
	"errors"
	"slices"
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

// RequireAuth accepts exactly a Go access token as `Authorization: Bearer` (Appendix C §C.4.3), then
// lets the Authorizer (iam.RBAC, T07) complete the principal: X-Act-On-Tenant, steward flag, contractor
// reach and capabilities. The principal is stored for authz.PrincipalFrom. No credential is 401
// unauthenticated; a Firebase ID token or any other non-Go token is 401 invalid_token on every route.
// (API keys and the cf_shim Firebase path join in T32; this middleware lives in auth rather than httpx
// because httpx cannot import auth.)
func (s *Service) RequireAuth() fiber.Handler {
	return func(c fiber.Ctx) error {
		raw, present := bearer(c)
		if !present {
			return httpx.ErrUnauthenticated()
		}
		p, err := s.Authenticate(c.Context(), raw)
		if err != nil {
			return err
		}
		setPrincipal(c, p)
		if s.gate != nil {
			if err := s.gate.Authorize(c, p); err != nil {
				return err
			}
		}
		return c.Next()
	}
}

func setPrincipal(c fiber.Ctx, p *authz.Principal) {
	authz.SetPrincipal(c, p)
	l := zerolog.Ctx(c.Context()).With().Str("user_id", p.UserID.String()).Str("session_id", p.SessionID.String()).Logger()
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

// Authenticate verifies an access token and its revocation state (C.4.3): signature, iss, aud and exp in
// memory, then one Redis pipeline (auth:sess:revoked:{sid}, auth:user:ver:{sub}). A version miss, and
// a token whose ver is newer than the cached version (the cache lags a bump whose post-commit raise
// was lost or is still in flight), read sessions.revoked_at and users.auth_version in PostgreSQL and
// rebuild both keys; with Redis unreachable the same read decides and auth_revocation_fallback_total
// counts it: the check never fails open.
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
	revoked, current, err := s.revocation(ctx, p)
	if err != nil {
		return nil, err
	}
	switch {
	case revoked:
		return nil, errSessionRevoked()
	case p.AuthVersion < current:
		return nil, errTokenExpired(ReasonClaimsChanged)
	case p.AuthVersion > current:
		return nil, errInvalidToken() // never issued: versions only grow
	}
	return p, nil
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
	if err1 != nil || err2 != nil || c.ID == "" || c.Version < 1 || (c.AMR != AMRPassword && c.AMR != "google") {
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
		if err != nil || !slices.Contains(tenantRoles, role) {
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
	return p, true
}
