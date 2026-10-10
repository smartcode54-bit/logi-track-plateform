package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// Password-reset token purposes (password_reset_tokens.purpose).
const (
	PurposeReset  = "reset"
	PurposeInvite = "invite"
	// InviteTTL is the life of an invite link (C.4.9: code constant).
	InviteTTL = 72 * time.Hour
)

// ForgotInput is the body of POST /v1/auth/password/forgot.
type ForgotInput struct {
	Email  string `json:"email"`
	Locale string `json:"locale"`

	IP        string `json:"-"`
	RequestID string `json:"-"`
}

// resetRequestedPayload is the payload of outbox auth.password_reset_requested. The token is created
// by the notify.email consumer (IssuePasswordResetToken), never by the api and never in the outbox.
type resetRequestedPayload struct {
	UserID      uuid.UUID `json:"userId"`
	Purpose     string    `json:"purpose"`
	Locale      string    `json:"locale"`
	RequestedIP string    `json:"requestedIp,omitempty"`
}

// Forgot is POST /v1/auth/password/forgot: it never reveals whether the account exists (always 202,
// R4). For an active or reset_required user it queues auth.password_reset_requested for notify.email
// and the password_reset_requested security event. Over the forgot_email / forgot_ip budget it still
// answers 202 and sends nothing.
func (s *Service) Forgot(ctx context.Context, in ForgotInput) error {
	email := normalizeEmail(in.Email)
	if email == "" || len(email) > 320 {
		return nil
	}
	if s.limit(ctx, ratelimit.ForgotIP, ipSubject(in.IP)) != nil ||
		s.limit(ctx, ratelimit.ForgotEmail, email) != nil {
		return nil
	}
	locale := "th"
	if in.Locale == "en" {
		locale = "en"
	}
	return s.system(ctx, func(q *authdb.Queries) error {
		u, err := q.GetUserForLogin(ctx, email)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if u.Status != "active" && u.Status != "reset_required" {
			return nil
		}
		if err := insertOutbox(ctx, q, outboxEvent{
			RoutingKey: RoutePasswordResetAsked, AggregateType: "user", AggregateID: u.ID.String(),
			Payload:   resetRequestedPayload{UserID: u.ID, Purpose: PurposeReset, Locale: locale, RequestedIP: in.IP},
			RequestID: in.RequestID,
		}); err != nil {
			return err
		}
		return emitSecurityEvent(ctx, q, security.Event{
			EventType: "password_reset_requested", Severity: security.SeverityInfo, Summary: "password reset link requested",
			Details: map[string]any{"ip": in.IP}, TargetUserID: &u.ID, RequestID: in.RequestID, OccurredAt: s.clock(),
		})
	})
}

// IssuePasswordResetToken creates a reset or invite token for the notify.email consumer (T10) inside
// its transaction: 32 random bytes, only sha256(token) stored, expiry PASSWORD_RESET_TTL (reset) or
// InviteTTL (invite). The caller mails {PUBLIC_WEB_BASE_URL}/reset-password#token=<token> and commits.
func (s *Service) IssuePasswordResetToken(ctx context.Context, tx pgx.Tx, userID uuid.UUID, purpose string,
	requestedIP string, requestedBy *uuid.UUID) (string, error) {
	ttl := s.cfg.PasswordResetTTL
	switch purpose {
	case PurposeReset:
	case PurposeInvite:
		ttl = InviteTTL
	default:
		return "", errors.New("auth: unknown password reset purpose")
	}
	tok, err := newSecret()
	if err != nil {
		return "", err
	}
	sum, _, _ := secretHash(tok)
	now := s.clock()
	if _, err := authdb.New(tx).InsertPasswordResetToken(ctx, authdb.InsertPasswordResetTokenParams{
		UserID: userID, Purpose: purpose, TokenHash: sum, ExpiresAt: now.Add(ttl),
		RequestedIp: parseIP(requestedIP), RequestedBy: requestedBy, CreatedAt: now,
	}); err != nil {
		return "", err
	}
	return tok, nil
}

// ResetInput is the body of POST /v1/auth/password/reset.
type ResetInput struct {
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`

	IP        string `json:"-"`
	RequestID string `json:"-"`
}

func errBadSecret(field string) *httpx.Error {
	return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: field, Reason: "invalid_or_expired"})
}

func policyError(v *password.Violation) *httpx.Error {
	params := map[string]any{}
	if v.Min > 0 {
		params["min"], params["max"] = v.Min, v.Max
	}
	if len(params) == 0 {
		params = nil
	}
	return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "newPassword", Reason: v.Reason, Params: params})
}

// Reset is POST /v1/auth/password/reset: a valid, unused, unexpired token of a user that is not
// disabled sets the password, clears the legacy hash and the must-change flag, re-activates a
// reset_required user, revokes every session (reason password_reset), bumps auth_version and appends
// password_reset_completed, all in one transaction. A bad token is 422 invalid_argument (field token).
// The token is checked before the new password is hashed, and the hash is computed before the
// transaction, so the users row is never held during a memory-hard computation.
func (s *Service) Reset(ctx context.Context, in ResetInput) error {
	if err := s.limit(ctx, ratelimit.ResetIP, ipSubject(in.IP)); err != nil {
		return err
	}
	sum, _, ok := secretHash(in.Token)
	if !ok {
		return errBadSecret("token")
	}
	usable := func(q *authdb.Queries, now time.Time) (authdb.LockPasswordResetTokenRow, error) {
		t, err := q.LockPasswordResetToken(ctx, sum)
		if errors.Is(err, pgx.ErrNoRows) {
			return t, errBadSecret("token")
		}
		if err != nil {
			return t, err
		}
		if t.UsedAt != nil || !t.ExpiresAt.After(now) {
			return t, errBadSecret("token")
		}
		return t, nil
	}
	var uid uuid.UUID
	if err := s.system(ctx, func(q *authdb.Queries) error {
		t, err := usable(q, s.clock())
		uid = t.UserID
		return err
	}); err != nil {
		return err
	}
	if err := s.checkPolicy(ctx, uid, in.NewPassword); err != nil {
		return err
	}
	hash, err := s.hashNew(ctx, in.NewPassword)
	if err != nil {
		return err
	}
	pc := newPostCommit()
	err = s.systemTx(ctx, func(tx pgx.Tx, q *authdb.Queries) error {
		now := s.clock()
		t, err := usable(q, now)
		if err != nil {
			return err
		}
		u, err := q.LockUser(ctx, t.UserID)
		if err != nil {
			return err
		}
		if u.Status == "disabled" || u.Status == "deleted" {
			return errBadSecret("token")
		}
		if err := s.setPassword(ctx, q, u, in.NewPassword, hash, nil, RevokePasswordReset, in.RequestID, now, pc); err != nil {
			return err
		}
		if err := q.UsePasswordResetToken(ctx, authdb.UsePasswordResetTokenParams{At: now, ID: t.ID}); err != nil {
			return err
		}
		return security.Append(ctx, tx, security.Event{
			EventType: "password_reset_completed", Severity: security.SeverityInfo, Summary: "password set through a reset link",
			Details: map[string]any{"purpose": t.Purpose, "ip": in.IP}, TargetUserID: &u.ID, ActorUserID: &u.ID,
			RequestID: in.RequestID, OccurredAt: now,
		})
	})
	if err != nil {
		return err
	}
	s.Apply(ctx, pc)
	return nil
}

// ChangeInput is the body of POST /v1/auth/password/change: passwordChangeTicket (R79), or
// currentPassword with a bearer. The ticket form wins when the body carries a ticket.
type ChangeInput struct {
	CurrentPassword      string `json:"currentPassword"`
	PasswordChangeTicket string `json:"passwordChangeTicket"`
	NewPassword          string `json:"newPassword"`

	IP        string `json:"-"`
	RequestID string `json:"-"`
}

// ChangePassword is the bearer form of POST /v1/auth/password/change: the current password is required
// and must match; every other session of the user is revoked (reason password_changed) and
// auth_version bumps, so the current session's next request refreshes once (token_expired,
// claims_changed) and stays signed in. A user without a password (Google-only) sets one through
// forgot / reset instead (C.5.6). The change applies only while the row still holds the credential the
// current password was checked against.
func (s *Service) ChangePassword(ctx context.Context, p *authz.Principal, in ChangeInput) error {
	if err := s.limit(ctx, ratelimit.ResetIP, ipSubject(in.IP)); err != nil {
		return err
	}
	if in.CurrentPassword == "" {
		return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "currentPassword", Reason: "required"})
	}
	var u authdb.GetUserForLoginRow
	if err := s.system(ctx, func(q *authdb.Queries) error {
		r, err := q.GetUser(ctx, p.UserID)
		u = authdb.GetUserForLoginRow{ID: r.ID, Email: r.Email, PasswordHash: r.PasswordHash,
			LegacyScryptHash: r.LegacyScryptHash, LegacyScryptSalt: r.LegacyScryptSalt, Status: r.Status,
			MustChangePassword: r.MustChangePassword, AuthVersion: r.AuthVersion}
		return err
	}); err != nil {
		return err
	}
	ok, _, err := s.verify(ctx, in.CurrentPassword, &u)
	if err != nil {
		return kdfError(err)
	}
	if !ok {
		return errCurrentPasswordMismatch()
	}
	if err := s.checkPolicy(ctx, p.UserID, in.NewPassword); err != nil {
		return err
	}
	hash, err := s.hashNew(ctx, in.NewPassword)
	if err != nil {
		return err
	}
	cred := credentialOf(&u)
	return s.changePassword(ctx, p.UserID, in, hash, &p.SessionID, &cred, nil)
}

func errCurrentPasswordMismatch() *httpx.Error {
	return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "currentPassword", Reason: "mismatch"})
}

// ChangePasswordWithTicket is the ticket form (R79): the single-use ticket of a must_change_password
// login replaces the bearer; no tokens are returned and the client signs in again. The policy is
// checked before the ticket is consumed, so a rejected password does not burn it. A ticket issued
// before a reset, a new temporary password or any other auth_version bump of the user is void.
func (s *Service) ChangePasswordWithTicket(ctx context.Context, in ChangeInput) error {
	if err := s.limit(ctx, ratelimit.ResetIP, ipSubject(in.IP)); err != nil {
		return err
	}
	if _, _, ok := secretHash(in.PasswordChangeTicket); !ok {
		return errBadSecret("passwordChangeTicket")
	}
	var t pwchgTicket
	found, err := s.store.peekTicket(ctx, "pwchg", in.PasswordChangeTicket, &t)
	if err != nil {
		return httpx.ErrUnavailable("password change ticket store unavailable").Wrap(err)
	}
	if !found {
		return errBadSecret("passwordChangeTicket")
	}
	if err := s.checkPolicy(ctx, t.UserID, in.NewPassword); err != nil {
		return err
	}
	hash, err := s.hashNew(ctx, in.NewPassword)
	if err != nil {
		return err
	}
	taken, err := s.store.takeTicket(ctx, "pwchg", in.PasswordChangeTicket, &t)
	if err != nil {
		return httpx.ErrUnavailable("password change ticket store unavailable").Wrap(err)
	}
	if !taken {
		return errBadSecret("passwordChangeTicket")
	}
	return s.changePassword(ctx, t.UserID, in, hash, nil, nil, &t)
}

func (s *Service) checkPolicy(ctx context.Context, uid uuid.UUID, pw string) error {
	return s.system(ctx, func(q *authdb.Queries) error {
		u, err := q.GetUser(ctx, uid)
		if err != nil {
			return err
		}
		return s.policyCheck(ctx, q, uid, deref(u.Email), pw)
	})
}

func (s *Service) policyCheck(ctx context.Context, q *authdb.Queries, uid uuid.UUID, email, pw string) error {
	var mobiles []string
	d, err := q.GetDriverForUser(ctx, uid)
	switch {
	case err == nil:
		mobiles = append(mobiles, d.Mobile)
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	if v := s.policy.Check(pw, email, mobiles...); v != nil {
		return policyError(v)
	}
	return nil
}

// hashNew applies the user-independent part of the policy (length, UTF-8, common list) and hashes a
// new password, outside any transaction. The full policy (email, mobile) runs again in the
// transaction (setPassword).
func (s *Service) hashNew(ctx context.Context, pw string) (string, error) {
	if v := s.policy.Check(pw, ""); v != nil {
		return "", policyError(v)
	}
	h, err := s.hasher.Hash(ctx, pw)
	if err != nil {
		return "", kdfError(err)
	}
	return h, nil
}

// changePassword sets hash (of in.NewPassword) for uid in one transaction. Bearer form: keep is the
// current session and cred the credential the current password was checked against; the row must
// still hold it. Ticket form: the user must still have must_change_password at the ticket's
// auth_version.
func (s *Service) changePassword(ctx context.Context, uid uuid.UUID, in ChangeInput, hash string, keep *uuid.UUID,
	cred *credential, ticket *pwchgTicket) error {
	pc := newPostCommit()
	err := s.systemTx(ctx, func(tx pgx.Tx, q *authdb.Queries) error {
		now := s.clock()
		u, err := q.LockUser(ctx, uid)
		if err != nil {
			return err
		}
		switch {
		case u.Status == "disabled" || u.Status == "deleted":
			return errAccountDisabled() // disabled after the ticket was issued (a bearer of such a user is already revoked)
		case cred != nil && !cred.heldBy(u):
			return errCurrentPasswordMismatch() // the password changed after the current one was checked
		case ticket != nil && (!u.MustChangePassword || u.AuthVersion != ticket.AuthVersion):
			return errBadSecret("passwordChangeTicket") // superseded by a reset or another password event
		}
		if err := s.setPassword(ctx, q, u, in.NewPassword, hash, keep, RevokePasswordChanged, in.RequestID, now, pc); err != nil {
			return err
		}
		return security.Append(ctx, tx, security.Event{
			EventType: "password_changed", Severity: security.SeverityInfo, Summary: "password changed by the user",
			Details:     map[string]any{"ip": in.IP, "ticket": ticket != nil},
			ActorUserID: &uid, TargetUserID: &uid, RequestID: in.RequestID, OccurredAt: now,
		})
	})
	if err != nil {
		return err
	}
	s.Apply(ctx, pc)
	return nil
}

// setPassword checks the full policy for pw, stores hash (its Argon2id hash, computed before the
// transaction; SetPassword bumps auth_version), revokes every session except keep with reason, and,
// while the Firebase bridge mirrors, sets the same password on the user's Firebase account with
// validSince = now before COMMIT (Appendix C §C.6.4): a failure there is 503 bridge_unavailable and the
// password stays unchanged in both stores.
func (s *Service) setPassword(ctx context.Context, q *authdb.Queries, u authdb.LockUserRow, pw, hash string, keep *uuid.UUID,
	reason, requestID string, now time.Time, pc *PostCommit) error {
	if err := s.policyCheck(ctx, q, u.ID, deref(u.Email), pw); err != nil {
		return err
	}
	v, err := q.SetPassword(ctx, authdb.SetPasswordParams{PasswordHash: hash, ChangedAt: now, ID: u.ID})
	if err != nil {
		return err
	}
	pc.version(u.ID, v)
	if _, err = s.revokeTx(ctx, q, Revocation{UserID: u.ID, Reason: reason, Except: keep, RequestID: requestID}, now, pc); err != nil {
		return err
	}
	return s.mirror(ctx, q, u.ID, accountChange{op: "password", password: &pw, revoke: true})
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
