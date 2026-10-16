package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Groups are the route groups of this package (Appendix B §B.2.2, §B.2.3):
//
//   - /v1/auth (public listener and internal): login, google/nonce and google (T06), refresh, logout,
//     logout-all, tenant, password forgot / reset / change, sse-ticket. The session exchange (T08, P7a)
//     joins this group.
//   - /v1/me (internal only): GET/PATCH /v1/me, GET /v1/me/tenants, GET /v1/me/sessions,
//     DELETE /v1/me/sessions/{sid}, PUT /v1/me/devices and DELETE /v1/me/devices/{installId} (T13). The
//     driver-app aliases under /v1/mobile/me* come with the mobile group (T55; the handlers are shared).
//   - /v1/bridge (internal only, T08): POST /v1/bridge/firebase-token, 404 unless the Firebase bridge
//     mode includes web (removed with TW7).
//   - /.well-known (internal only, TW3): GET /.well-known/jwks.json, the public keys the web edge gate
//     (proxy.ts) verifies lt_at with (Appendix C §C.4.2). On the public listener it is 404 like every
//     other internal route.
//
// Groups and the mount functions only register handlers and never read s, so cmd/api `routes` builds
// the route table from a nil *Service (no database, Redis or key); keep it that way.
func (s *Service) Groups() []ingress.Group {
	return []ingress.Group{
		{Prefix: "/v1/auth", Public: true, Mount: s.mountAuth},
		{Prefix: "/v1/me", Mount: s.mountMe},
		{Prefix: "/v1/bridge", Mount: s.mountBridge},
		{Prefix: "/.well-known", Mount: s.mountWellKnown},
	}
}

func (s *Service) mountWellKnown(r fiber.Router) {
	r.Get("/jwks.json", s.handleJWKS)
}

// handleJWKS serves the active and, during a rotation, the previous public key (C.4.2). The document is
// the bare RFC 7517 JWK Set, not the {"data"} envelope, so standard JWKS clients (jose's
// createRemoteJWKSet in proxy.ts) read it as is. max-age=300 is the web's JWKS cache period of the
// rotation runbook; an unknown kid makes the web refetch sooner (30 s cooldown).
func (s *Service) handleJWKS(c fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "public, max-age=300")
	return c.JSON(s.keys.JWKS())
}

func (s *Service) mountAuth(r fiber.Router) {
	r.Use(noStore)
	r.Post("/login", s.handleLogin)
	r.Get("/google/nonce", s.handleGoogleNonce)
	r.Post("/google", s.handleGoogle)
	r.Post("/refresh", s.handleRefresh)
	bindOwn(r, http.MethodPost, "/logout", "/v1/auth/logout", "empty", httpx.PolicySession, s.refuseAPIKeys, s.handleLogout)
	bindOwn(r, http.MethodPost, "/logout-all", "/v1/auth/logout-all", "empty", httpx.PolicySession, s.RequireAuth(), s.handleLogoutAll)
	bindOwn(r, http.MethodPost, "/tenant", "/v1/auth/tenant", "auth.access", httpx.PolicySession, s.RequireAuth(), s.handleTenant)
	r.Post("/password/forgot", s.handleForgot)
	r.Post("/password/reset", s.handleReset)
	bindOwn(r, http.MethodPost, "/password/change", "/v1/auth/password/change", "empty", httpx.PolicySession, s.refuseAPIKeys, s.handleChange)
	bindOwn(r, http.MethodPost, "/sse-ticket", "/v1/auth/sse-ticket", "auth.sse_ticket", httpx.PolicySession, s.RequireAuth(), s.handleSSETicket)
}

func (s *Service) mountMe(r fiber.Router) {
	r.Use(noStore, s.RequireAuth())
	bindOwn(r, "GET", "", "/v1/me", "auth.me", httpx.PolicyOwnAccount, s.handleMe)
	bindOwn(r, "PATCH", "", "/v1/me", "auth.me", httpx.PolicyOwnAccount, s.handlePatchMe)
	bindOwn(r, "GET", "/tenants", "/v1/me/tenants", "auth.tenant_label", httpx.PolicyOwnAccount, s.handleMyTenants)
	bindOwn(r, "GET", "/sessions", "/v1/me/sessions", "auth.sessions", httpx.PolicyOwnAccount, s.handleMySessions)
	bindOwn(r, "DELETE", "/sessions/:sid", "/v1/me/sessions/:sid", "empty", httpx.PolicyOwnAccount, s.handleRevokeMySession)
	bindOwn(r, http.MethodPut, "/devices", "/v1/me/devices", "empty", httpx.PolicyOwnAccount, s.handlePutDevice)
	bindOwn(r, http.MethodDelete, "/devices/:installId", "/v1/me/devices/:installId", "empty", httpx.PolicyOwnAccount, s.handleDeleteDevice)
}

// noStore keeps tokens and identity out of every cache, and sets Retry-After on 423 and 429.
func noStore(c fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	err := c.Next()
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) {
			if v, ok := retryAfterHeader(he); ok {
				c.Set(fiber.HeaderRetryAfter, v)
			}
		}
	}
	return err
}

func (s *Service) handleLogin(c fiber.Ctx) error {
	var in LoginInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	in.IP, in.UserAgent, in.RequestID = httpx.ClientIPFrom(c), c.Get(fiber.HeaderUserAgent), httpx.RequestIDFrom(c)
	res, err := s.Login(c.Context(), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, res)
}

func (s *Service) handleGoogleNonce(c fiber.Ctx) error {
	res, err := s.GoogleNonce(c.Context(), httpx.ClientIPFrom(c))
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, res)
}

func (s *Service) handleGoogle(c fiber.Ctx) error {
	var in GoogleInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	in.IP, in.UserAgent, in.RequestID = httpx.ClientIPFrom(c), c.Get(fiber.HeaderUserAgent), httpx.RequestIDFrom(c)
	res, err := s.GoogleSignIn(c.Context(), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, res)
}

func (s *Service) handleRefresh(c fiber.Ctx) error {
	var in RefreshInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	if in.RefreshToken == "" {
		return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "refreshToken", Reason: "required"})
	}
	in.IP, in.RequestID = httpx.ClientIPFrom(c), httpx.RequestIDFrom(c)
	res, err := s.Refresh(c.Context(), in)
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, res)
}

// handleLogout accepts the bearer, the refresh token, or both; a bearer that is expired or whose
// session is already revoked does not block a logout through the refresh token.
func (s *Service) handleLogout(c fiber.Ctx) error {
	var in LogoutInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	in.RequestID = httpx.RequestIDFrom(c)
	var p *authz.Principal
	if raw, present := bearer(c); present {
		var err error
		p, err = s.Authenticate(c.Context(), raw)
		if err != nil {
			var he *httpx.Error
			if !errors.As(err, &he) || he.Status != http.StatusUnauthorized {
				return err
			}
			if he.Code == CodeSessionRevoked && in.RefreshToken == "" {
				return completedSessionEmpty(c) // already signed out
			}
			if in.RefreshToken == "" {
				return err
			}
			p = nil
		} else {
			setPrincipal(c, p)
		}
	}
	if err := s.Logout(c.Context(), p, in); err != nil {
		return err
	}
	return completedSessionEmpty(c)
}

func (s *Service) handleLogoutAll(c fiber.Ctx) error {
	if err := s.LogoutAll(c.Context(), PrincipalFrom(c), httpx.RequestIDFrom(c)); err != nil {
		return err
	}
	return completedSessionEmpty(c)
}

func (s *Service) handleTenant(c fiber.Ctx) error {
	in, err := httpx.StrictBody[tenantContextBody](c)
	if err != nil {
		if he, ok := errors.AsType[*httpx.Error](err); ok && he.Message == "unknown request field" {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "body", Reason: "unknown_field"})
		}
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(c.Body(), &fields); err != nil {
		return httpx.ErrBadRequest("malformed JSON body")
	}
	if _, ok := fields["mode"]; ok && (in.Mode == nil || *in.Mode == "") {
		return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "mode", Reason: "enum"})
	}
	mode := AccessModeMember
	if in.Mode != nil {
		mode = *in.Mode
	}
	var tid *uuid.UUID
	switch mode {
	case "leave":
		if _, supplied := fields["tenantId"]; supplied {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "tenantId", Reason: "not_allowed"})
		}
	case AccessModeMember, AccessModeSupport:
		if in.TenantID == nil {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "tenantId", Reason: "required"})
		}
		id, err := uuid.Parse(*in.TenantID)
		if err != nil || id == uuid.Nil {
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "tenantId", Reason: "uuid"})
		}
		tid = &id
	default:
		return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "mode", Reason: "enum"})
	}
	res, err := s.ChangeTenantContext(c.Context(), PrincipalFrom(c), mode, tid, httpx.RequestIDFrom(c))
	if err != nil {
		return err
	}
	if err := s.publishContext(c, res.AccessToken); err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, res)
}

func (s *Service) handleForgot(c fiber.Ctx) error {
	var in ForgotInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	in.IP, in.RequestID = httpx.ClientIPFrom(c), httpx.RequestIDFrom(c)
	if err := s.Forgot(c.Context(), in); err != nil {
		return err
	}
	c.Status(http.StatusAccepted) // empty body: the answer is the same for every email (R4)
	return nil
}

func (s *Service) handleReset(c fiber.Ctx) error {
	var in ResetInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	in.IP, in.RequestID = httpx.ClientIPFrom(c), httpx.RequestIDFrom(c)
	if err := s.Reset(c.Context(), in); err != nil {
		return err
	}
	return completedSessionEmpty(c)
}

// handleChange serves both forms of POST /v1/auth/password/change (R79). The form is chosen from the
// body first: a passwordChangeTicket selects the ticket form and any Authorization header is ignored,
// because the BFF proxy forwards lt_at whenever the cookie exists (Appendix C §C.4.5) and a stale or
// another user's bearer must not hijack a must-change-password flow. Without a ticket the bearer form
// requires a bearer and the current password.
func (s *Service) handleChange(c fiber.Ctx) error {
	var in ChangeInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	in.IP, in.RequestID = httpx.ClientIPFrom(c), httpx.RequestIDFrom(c)
	if in.NewPassword == "" {
		return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "newPassword", Reason: "required"})
	}
	raw, present := bearer(c)
	var err error
	switch {
	case in.PasswordChangeTicket != "":
		err = s.ChangePasswordWithTicket(c.Context(), in)
	case present:
		p, aerr := s.Authenticate(c.Context(), raw)
		if aerr != nil {
			return aerr
		}
		setPrincipal(c, p)
		err = s.ChangePassword(c.Context(), p, in)
	default:
		return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "passwordChangeTicket", Reason: "required"})
	}
	if err != nil {
		return err
	}
	return completedSessionEmpty(c)
}

func (s *Service) handleSSETicket(c fiber.Ctx) error {
	res, err := s.IssueSSETicket(c.Context(), PrincipalFrom(c))
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, res)
}

func (s *Service) handleMe(c fiber.Ctx) error {
	me, err := s.Me(c.Context(), PrincipalFrom(c))
	if err != nil {
		return err
	}
	return writeMe(c, me)
}

func (s *Service) handlePatchMe(c fiber.Ctx) error {
	var in PatchMeInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	me, err := s.PatchMe(c.Context(), PrincipalFrom(c), in)
	if err != nil {
		return err
	}
	return writeMe(c, me)
}

func (s *Service) handleMyTenants(c fiber.Ctx) error {
	ts, err := s.MyTenants(c.Context(), PrincipalFrom(c))
	if err != nil {
		return err
	}
	return writeTenantLabels(c, ts)
}

func (s *Service) handleMySessions(c fiber.Ctx) error {
	ss, err := s.MySessions(c.Context(), PrincipalFrom(c))
	if err != nil {
		return err
	}
	return httpx.JSON(c, http.StatusOK, ss)
}

func (s *Service) handleRevokeMySession(c fiber.Ctx) error {
	sid, err := uuid.Parse(c.Params("sid"))
	if err != nil {
		return httpx.ErrNotFound()
	}
	if err := s.RevokeMySession(c.Context(), PrincipalFrom(c), sid, httpx.RequestIDFrom(c)); err != nil {
		return err
	}
	return completedSessionEmpty(c)
}
