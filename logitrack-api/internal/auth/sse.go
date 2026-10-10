package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// SSETicketPrincipal redeems a driver-app SSE ticket for GET /v1/mobile/events (Appendix C §C.4.6, T12).
// The ticket is consumed (GETDEL) whatever follows, so a ticket opens one stream at most. The session it
// names must still be live and of an android or ios platform, and its user active; the principal is
// then built from PostgreSQL the way a refresh builds the claims of that session (the session's active
// tenant, or the first usable membership), and completed by the capability resolver. TokenExpiresAt is
// the end of one access-token lifetime from now, capped at the session's absolute expiry: the stream
// ends there with event: reconnect and the app refreshes and asks for a new ticket.
//
// No ticket is 401 unauthenticated; an unknown, used or expired one is 401 invalid_token, as is a
// ticket whose session belongs to another user or a web session; a revoked or expired session, or a
// user who is no longer active, is 401 session_revoked; a ticket store that cannot answer is 503.
func (s *Service) SSETicketPrincipal(ctx context.Context, ticket string) (*authz.Principal, error) {
	if ticket == "" {
		return nil, httpx.ErrUnauthenticated()
	}
	uid, sid, ok, err := s.ConsumeSSETicket(ctx, ticket)
	if err != nil {
		return nil, httpx.ErrUnavailable("SSE ticket store unavailable").Wrap(err)
	}
	if !ok {
		return nil, errInvalidToken()
	}
	now := s.clock()
	var p *authz.Principal
	err = s.system(ctx, func(q *authdb.Queries) error {
		sess, err := q.GetSession(ctx, sid)
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalidToken()
		}
		if err != nil {
			return err
		}
		switch {
		case sess.UserID != uid || !isMobile(sess.Platform):
			return errInvalidToken()
		case sess.RevokedAt != nil || !sess.AbsoluteExpiresAt.After(now):
			return errSessionRevoked()
		}
		u, err := q.GetUser(ctx, uid)
		if err != nil {
			return err
		}
		if u.Status != "active" {
			return errSessionRevoked()
		}
		a, err := loadAxes(ctx, q, uid)
		if err != nil {
			return err
		}
		c, err := a.claims(sid, u.AuthVersion, sess.Amr, a.pickTenant(sess.ActiveTenantID))
		if err != nil {
			return err
		}
		jti, err := uuid.NewV7()
		if err != nil {
			return err
		}
		c.Subject, c.ID = uid.String(), jti.String()
		var valid bool
		if p, valid = principalFromClaims(&c); !valid {
			return errInvalidToken()
		}
		p.TokenExpiresAt = now.Add(s.keys.TTL())
		if sess.AbsoluteExpiresAt.Before(p.TokenExpiresAt) {
			p.TokenExpiresAt = sess.AbsoluteExpiresAt
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.caps != nil {
		if _, err := s.caps.Capabilities(ctx, p); err != nil {
			return nil, err
		}
	}
	return p, nil
}
