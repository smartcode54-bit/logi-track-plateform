package iam

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// The platform bootstrap (Appendix C §C.1.6, §C.5.5; owner addition to T19, 2026-10-10). cmd/seed runs these
// functions in its own db.WithSystem transaction; nothing here reads the environment, prints or logs, so a
// password never leaves the caller's memory. Platform roles come only from PLATFORM_ADMIN_EMAILS, never at
// login and never from the users ETL.

// ParseEmails splits a comma list of addresses (PLATFORM_ADMIN_EMAILS), lower-cased, trimmed and deduplicated;
// bad holds the entries that are not addresses.
func ParseEmails(list string) (emails, bad []string) {
	for _, s := range strings.Split(list, ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		e, ok := normalizeEmail(s)
		if !ok {
			bad = append(bad, strings.TrimSpace(s))
			continue
		}
		if !contains(emails, e) {
			emails = append(emails, e)
		}
	}
	return emails, bad
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// PlatformAdminsResult is the outcome of BootstrapPlatformAdmins: addresses granted now, already holding the
// role, and without a user; Outside lists platform_admin holders whose address is not in the list (reported,
// never revoked: a later grant through POST /v1/users/{id}/platform-roles is legitimate).
type PlatformAdminsResult struct {
	Granted, Held, Missing, Outside []string
}

// BootstrapPlatformAdmins grants platform_admin (granted_by NULL) to the users of emails (cmd/seed
// bootstrap-platform-admins, idempotent). Each grant bumps auth_version, announces claims_changed on
// user:{uid} through outbox user.sessions_revoked (the relay re-applies the cached version) and records
// platform_role_granted {source: bootstrap} in the same transaction.
func BootstrapPlatformAdmins(ctx context.Context, tx pgx.Tx, emails []string, now time.Time) (PlatformAdminsResult, error) {
	var res PlatformAdminsResult
	q := iamdb.New(tx)
	users, err := q.UsersByEmails(ctx, emails)
	if err != nil {
		return res, err
	}
	found := map[string]uuid.UUID{}
	for _, u := range users {
		found[strings.ToLower(u.Email)] = u.ID
	}
	for _, e := range emails {
		id, ok := found[e]
		if !ok {
			res.Missing = append(res.Missing, e)
			continue
		}
		granted, err := grantBootstrapRole(ctx, tx, q, id, now)
		if err != nil {
			return res, err
		}
		if granted {
			res.Granted = append(res.Granted, e)
		} else {
			res.Held = append(res.Held, e)
		}
	}
	outside, err := q.PlatformAdminsOutside(ctx, emails)
	if err != nil {
		return res, err
	}
	for _, o := range outside {
		label := o.Email
		if label == "" {
			label = o.ID.String()
		}
		res.Outside = append(res.Outside, label)
	}
	return res, nil
}

// grantBootstrapRole inserts platform_admin for id unless it is held; a grant changes the user's claims.
func grantBootstrapRole(ctx context.Context, tx pgx.Tx, q *iamdb.Queries, id uuid.UUID, now time.Time) (bool, error) {
	if _, err := q.LockAdminUser(ctx, id); err != nil {
		return false, err
	}
	n, err := q.InsertPlatformRole(ctx, iamdb.InsertPlatformRoleParams{UserID: id, Role: string(authz.PlatformAdmin)})
	if err != nil || n == 0 {
		return false, err
	}
	if err := bootstrapClaimsChanged(ctx, tx, q, id); err != nil {
		return false, err
	}
	return true, security.Append(ctx, tx, security.Event{
		EventType: EventPlatformRoleGranted, Severity: security.SeverityWarning, Summary: "platform_admin granted by the bootstrap",
		Details: map[string]any{"role": string(authz.PlatformAdmin), "source": "bootstrap"}, TargetUserID: &id, OccurredAt: now,
	})
}

// bootstrapClaimsChanged bumps auth_version and queues user.sessions_revoked {reason: claims_changed}, the
// row auth.RevokeInTx writes for a claims change; the outbox relay raises the cached version from it.
func bootstrapClaimsChanged(ctx context.Context, tx pgx.Tx, q *iamdb.Queries, id uuid.UUID) error {
	if _, err := q.BumpUserVersion(ctx, id); err != nil {
		return err
	}
	return sessionsRevoked(ctx, tx, id, []uuid.UUID{}, auth.RevokeClaimsChanged)
}

func sessionsRevoked(ctx context.Context, tx pgx.Tx, id uuid.UUID, sids []uuid.UUID, reason string) error {
	_, err := outbox.Append(ctx, tx, outbox.Event{
		RoutingKey: auth.RouteSessionsRevoked, AggregateType: "user", AggregateID: id.String(),
		Payload: map[string]any{"userId": id, "sessionIds": sids, "reason": reason}, Topics: []string{"user:" + id.String()},
	})
	return err
}

// BootstrapAdmin is the super admin of a fresh deployment (seed --bootstrap-admin): BOOTSTRAP_ADMIN_EMAIL with
// the Argon2id hash of BOOTSTRAP_ADMIN_PASSWORD, computed by the caller before the transaction.
type BootstrapAdmin struct {
	Email string
	// Hash is the Argon2id PHC string of the password.
	Hash string
	// Matches reports whether a stored Argon2id hash is the same password: then the hash, auth_version and
	// the sessions stay as they are, so running the bootstrap again signs nobody out.
	Matches func(phc string) (bool, error)
	// PlatformAdmin: the email is in PLATFORM_ADMIN_EMAILS, so the user gets platform_admin.
	PlatformAdmin bool
	Now           time.Time
}

// BootstrapAdminResult says what the bootstrap changed.
type BootstrapAdminResult struct {
	UserID      uuid.UUID
	Created     bool // a new user
	PasswordSet bool // the stored password was another one (or none): set, sessions revoked
	Reactivated bool // the account was disabled, reset_required or flagged must-change: usable again
	RoleGranted bool // platform_admin granted now
	RoleHeld    bool // platform_admin held already
}

// ApplyBootstrapAdmin creates or updates the bootstrap super admin (idempotent: a second run with the same
// password changes nothing). The account is active, holds the Argon2id hash, no legacy hash and no
// must-change flag (the operator chose the password); a changed password revokes every session (reason
// password_reset) and records password_changed {source: bootstrap}. platform_admin is granted only when
// PlatformAdmin is true (the email is in PLATFORM_ADMIN_EMAILS). Every change appends its security event in
// the same transaction.
func ApplyBootstrapAdmin(ctx context.Context, tx pgx.Tx, in BootstrapAdmin) (BootstrapAdminResult, error) {
	var res BootstrapAdminResult
	email, ok := normalizeEmail(in.Email)
	if !ok || in.Hash == "" || in.Matches == nil {
		return res, errors.New("iam: the bootstrap admin needs an email address and a password hash")
	}
	q := iamdb.New(tx)
	u, err := q.BootstrapUserByEmail(ctx, email)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		name, _, _ := strings.Cut(email, "@")
		if res.UserID, err = q.InsertBootstrapUser(ctx, iamdb.InsertBootstrapUserParams{Email: email, DisplayName: name,
			PasswordHash: in.Hash, ChangedAt: in.Now}); err != nil {
			return res, err
		}
		res.Created = true
		if err := security.Append(ctx, tx, security.Event{EventType: EventUserCreated, Severity: security.SeverityWarning,
			Summary: "bootstrap admin created", Details: map[string]any{"source": "bootstrap", "platformAdmin": in.PlatformAdmin},
			TargetUserID: &res.UserID, OccurredAt: in.Now}); err != nil {
			return res, err
		}
	case err != nil:
		return res, err
	default:
		res.UserID = u.ID
		same := false
		if u.PasswordHash != nil {
			if same, err = in.Matches(*u.PasswordHash); err != nil {
				return res, err
			}
		}
		switch {
		case !same:
			if _, err := q.SetBootstrapPassword(ctx, iamdb.SetBootstrapPasswordParams{PasswordHash: in.Hash, ChangedAt: in.Now, ID: u.ID}); err != nil {
				return res, err
			}
			sids, err := q.RevokeAllSessions(ctx, iamdb.RevokeAllSessionsParams{At: in.Now, Reason: auth.RevokePasswordReset, UserID: u.ID})
			if err != nil {
				return res, err
			}
			if len(sids) > 0 {
				if err := q.RevokeTokensOfSessions(ctx, iamdb.RevokeTokensOfSessionsParams{At: in.Now, Reason: auth.RevokePasswordReset,
					SessionIds: sids}); err != nil {
					return res, err
				}
			}
			if err := sessionsRevoked(ctx, tx, u.ID, sids, auth.RevokePasswordReset); err != nil {
				return res, err
			}
			res.PasswordSet = true
			if err := security.Append(ctx, tx, security.Event{EventType: EventPasswordChanged, Severity: security.SeverityWarning,
				Summary: "bootstrap admin password set", Details: map[string]any{"source": "bootstrap", "sessionsRevoked": len(sids)},
				TargetUserID: &u.ID, OccurredAt: in.Now}); err != nil {
				return res, err
			}
		case u.Status != "active" || u.MustChangePassword:
			if _, err := q.ReactivateBootstrapUser(ctx, u.ID); err != nil {
				return res, err
			}
			if err := sessionsRevoked(ctx, tx, u.ID, []uuid.UUID{}, auth.RevokeClaimsChanged); err != nil {
				return res, err
			}
			res.Reactivated = true
			if err := security.Append(ctx, tx, security.Event{EventType: EventUserEnabled, Severity: security.SeverityWarning,
				Summary: "bootstrap admin made usable", Details: map[string]any{"source": "bootstrap", "previousStatus": u.Status},
				TargetUserID: &u.ID, OccurredAt: in.Now}); err != nil {
				return res, err
			}
		}
	}
	if in.PlatformAdmin {
		granted, err := grantBootstrapRole(ctx, tx, q, res.UserID, in.Now)
		if err != nil {
			return res, err
		}
		res.RoleGranted, res.RoleHeld = granted, !granted
	}
	return res, nil
}
