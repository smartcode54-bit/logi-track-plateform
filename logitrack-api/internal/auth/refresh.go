package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// RefreshInput is the body of POST /v1/auth/refresh plus request facts.
type RefreshInput struct {
	RefreshToken string `json:"refreshToken"`

	IP        string `json:"-"`
	RequestID string `json:"-"`
}

// Refresh is POST /v1/auth/refresh (Appendix C §C.4.4, R37, R83): it rotates the refresh token inside
// its family and issues an access token with the current claims, the session's active tenant
// (falling back to another active membership when that one is gone) and the current auth_version.
//
// A rotated token presented again is a reuse unless it was rotated less than ReuseGrace ago and its
// successor was never presented (two tabs refreshing at once, a retried mobile request): then a sibling
// successor is issued and the earlier successor revoked. Any other reuse revokes the family and the
// session, bumps auth_version, writes security_events refresh_token_reuse (critical) and outbox
// user.sessions_revoked in one transaction, and answers 401 session_revoked.
func (s *Service) Refresh(ctx context.Context, in RefreshInput) (*TokenPair, error) {
	sum, hexSum, ok := secretHash(in.RefreshToken)
	if !ok {
		return nil, errInvalidToken()
	}
	// Hot index: reject an expired or revoked token without opening a transaction.
	if e, found, err := s.store.getRefresh(ctx, hexSum); err == nil && found {
		if !e.Exp.After(s.clock()) {
			return nil, errInvalidToken()
		}
		if revoked, _, _, err := s.store.revocationState(ctx, e.SessionID, e.UserID); err == nil && revoked {
			return nil, errSessionRevoked()
		}
	}

	pc := newPostCommit()
	var res *TokenPair
	reused := false
	err := s.systemTx(ctx, func(tx pgx.Tx, q *authdb.Queries) error {
		now := s.clock()
		// Lock order users -> sessions -> refresh_tokens (C.4.4): find the token's user without a lock
		// (sessions.user_id never changes), lock the user, then the token and its session.
		uid, err := q.RefreshTokenUser(ctx, sum)
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalidToken()
		}
		if err != nil {
			return err
		}
		user, err := q.LockUser(ctx, uid)
		if err != nil {
			return err
		}
		rt, err := q.LockRefreshToken(ctx, sum)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && rt.UserID != uid) {
			return errInvalidToken() // removed by the clean-up job in between
		}
		if err != nil {
			return err
		}
		switch {
		case rt.SessionRevokedAt != nil:
			return errSessionRevoked()
		case rt.RevokedAt != nil: // a superseded sibling successor
			return errInvalidToken()
		case !rt.ExpiresAt.After(now) || !rt.AbsoluteExpiresAt.After(now):
			return errInvalidToken()
		}
		if err := s.limit(ctx, ratelimit.RefreshSession, rt.SessionID.String()); err != nil {
			return err
		}
		if user.Status != "active" {
			return errSessionRevoked() // disabled or deleted users have no live session; never re-issue
		}
		ver := user.AuthVersion
		pc.issue(uid, ver) // the cache follows every issued version (a lost post-commit raise heals here)

		if rt.RotatedAt != nil {
			succ, err := q.LockRefreshTokenByID(ctx, *rt.ReplacedBy)
			if err != nil {
				return err
			}
			if now.Sub(*rt.RotatedAt) >= ReuseGrace || succ.RotatedAt != nil || succ.RevokedAt != nil {
				reused = true
				return s.reuseDetected(ctx, tx, q, rt, in, pc)
			}
			// Grace (R37): revoke the unused successor and issue a sibling in the same family.
			old, err := q.RevokeRefreshToken(ctx, authdb.RevokeRefreshTokenParams{At: now, Reason: "superseded", ID: succ.ID})
			if err != nil {
				return err
			}
			pc.dropHashes(old)
		}
		refresh, nextID, err := s.insertRefresh(ctx, q, rt.SessionID, rt.FamilyID, rt.UserID, rt.Platform, now, rt.AbsoluteExpiresAt, pc)
		if err != nil {
			return err
		}
		if rt.RotatedAt != nil {
			err = q.SetSuccessor(ctx, authdb.SetSuccessorParams{ReplacedBy: nextID, ID: rt.ID})
		} else {
			err = q.RotateRefreshToken(ctx, authdb.RotateRefreshTokenParams{At: now, ReplacedBy: nextID, ID: rt.ID})
			pc.dropHashes(sum)
		}
		if err != nil {
			return err
		}
		if err := q.TouchSession(ctx, authdb.TouchSessionParams{At: now, Ip: parseIP(in.IP), ID: rt.SessionID}); err != nil {
			return err
		}
		a, err := loadAxes(ctx, q, rt.UserID)
		if err != nil {
			return err
		}
		m := a.pickTenant(rt.ActiveTenantID)
		if !sameTenant(m, rt.ActiveTenantID) {
			if err := q.SetActiveTenant(ctx, authdb.SetActiveTenantParams{TenantID: tenantOf(m), ID: rt.SessionID}); err != nil {
				return err
			}
		}
		c, err := a.claims(rt.SessionID, ver, rt.Amr, m)
		if err != nil {
			return err
		}
		access, err := s.signAccess(c, rt.UserID, now)
		if err != nil {
			return err
		}
		res = &TokenPair{AccessToken: access, ExpiresIn: s.expiresIn(), RefreshToken: refresh}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.Apply(ctx, pc)
	if reused {
		return nil, errSessionRevoked()
	}
	return res, nil
}

// reuseDetected revokes the family and the session of a reused refresh token in the current
// transaction, which then commits; the caller answers session_revoked.
func (s *Service) reuseDetected(ctx context.Context, tx pgx.Tx, q *authdb.Queries, rt authdb.LockRefreshTokenRow, in RefreshInput, pc *PostCommit) error {
	now := s.clock()
	fam, err := q.RevokeFamily(ctx, authdb.RevokeFamilyParams{At: now, Reason: RevokeRefreshReuse, FamilyID: rt.FamilyID})
	if err != nil {
		return err
	}
	pc.dropHashes(fam...)
	if _, err := s.revokeTx(ctx, q, Revocation{
		UserID: rt.UserID, Reason: RevokeRefreshReuse, SessionIDs: []uuid.UUID{rt.SessionID}, BumpVersion: true,
		RequestID: in.RequestID,
	}, now, pc); err != nil {
		return err
	}
	return security.Append(ctx, tx, security.Event{
		EventType: "refresh_token_reuse", Severity: security.SeverityCritical,
		Summary: "a rotated refresh token was presented again; family and session revoked",
		Details: map[string]any{
			"sessionId": rt.SessionID, "familyId": rt.FamilyID, "platform": rt.Platform, "ip": in.IP,
			"rotatedSeconds": int(now.Sub(*rt.RotatedAt).Seconds()),
		},
		TargetUserID: &rt.UserID, RequestID: in.RequestID, OccurredAt: now,
	})
}

func tenantOf(m *authdb.ListMembershipsRow) *uuid.UUID {
	if m == nil {
		return nil
	}
	id := m.TenantID
	return &id
}

func sameTenant(m *authdb.ListMembershipsRow, stored *uuid.UUID) bool {
	switch {
	case m == nil:
		return stored == nil
	case stored == nil:
		return false
	default:
		return m.TenantID == *stored
	}
}
