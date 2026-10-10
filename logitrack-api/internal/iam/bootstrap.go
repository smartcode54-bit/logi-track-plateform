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
// role, and without a user; Refused lists the users of a listed address the bootstrap may not grant (see
// BootstrapRefusal); Outside lists platform_admin holders whose address is not in the list (reported, never
// revoked: a later grant through POST /v1/users/{id}/platform-roles is legitimate).
type PlatformAdminsResult struct {
	Granted, Held, Missing, Outside []string
	Refused                         []Refusal
}

// Refusal is a listed address whose user the bootstrap did not grant, and why (a Refuse* reason).
type Refusal struct {
	Email  string
	UserID uuid.UUID
	Reason string
}

// Why the bootstrap refuses platform_admin to the user of a listed address (Appendix C §C.5.5): a platform admin
// verifies the account and grants it through POST /v1/users/{id}/platform-roles instead.
const (
	// RefuseAddressUnproven: the address was not imported from Firebase (the firebase_legacy identity's address),
	// set by the bootstrap or set by a platform admin; whoever created or renamed the account set it.
	RefuseAddressUnproven = "address_unproven"
	// RefuseOtherTenant: the user belongs to a tenant other than the own fleet (a carrier admin manages it).
	RefuseOtherTenant = "carrier_membership"
	// RefuseScope: the user holds a customer scope or a dispatcher grant.
	RefuseScope = "scope_holder"
)

// BootstrapPlatformAdmins grants platform_admin (granted_by NULL) to the users of emails (cmd/seed
// bootstrap-platform-admins, idempotent) that are eligible (bootstrapGrant). Each grant bumps auth_version,
// announces claims_changed on user:{uid} through outbox user.sessions_revoked (the relay re-applies the cached
// version) and records platform_role_granted {source: bootstrap} in the same transaction.
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
		out, refusal, err := bootstrapGrant(ctx, tx, q, id, now)
		switch {
		case err != nil:
			return res, err
		case out == roleGranted:
			res.Granted = append(res.Granted, e)
		case out == roleHeld:
			res.Held = append(res.Held, e)
		default:
			res.Refused = append(res.Refused, Refusal{Email: e, UserID: id, Reason: refusal})
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

type roleOutcome int

const (
	roleGranted roleOutcome = iota
	roleHeld
	roleRefused
)

// bootstrapGrant inserts platform_admin for id unless it is held or the user is not eligible (refusal names why,
// BootstrapEligibility): the user is locked first, so its address and grants cannot change under the check. A grant
// changes the user's claims.
func bootstrapGrant(ctx context.Context, tx pgx.Tx, q *iamdb.Queries, id uuid.UUID, now time.Time) (roleOutcome, string, error) {
	if _, err := q.LockAdminUser(ctx, id); err != nil {
		return roleRefused, "", err
	}
	el, err := q.BootstrapEligibility(ctx, id)
	if err != nil {
		return roleRefused, "", err
	}
	switch {
	case el.Held:
		return roleHeld, "", nil
	case !el.AddressProven:
		return roleRefused, RefuseAddressUnproven, nil
	case el.OtherTenant:
		return roleRefused, RefuseOtherTenant, nil
	case el.HasScope:
		return roleRefused, RefuseScope, nil
	}
	n, err := q.InsertPlatformRole(ctx, iamdb.InsertPlatformRoleParams{UserID: id, Role: string(authz.PlatformAdmin)})
	if err != nil || n == 0 {
		return roleHeld, "", err
	}
	if err := bootstrapClaimsChanged(ctx, tx, q, id); err != nil {
		return roleRefused, "", err
	}
	return roleGranted, "", security.Append(ctx, tx, security.Event{
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
	// RoleRefused is the Refuse* reason when the address is in PLATFORM_ADMIN_EMAILS but the account is not eligible
	// (an existing account whose address someone else set, or that belongs to a carrier or holds a scope).
	RoleRefused string
	// RoleOutsideList: the address is not in PLATFORM_ADMIN_EMAILS but the user holds platform_admin (kept and
	// reported, never revoked, as bootstrap-platform-admins does).
	RoleOutsideList bool
}

// ApplyBootstrapAdmin creates or updates the bootstrap super admin (idempotent: a second run with the same
// password changes nothing). The account is active, holds the Argon2id hash, no legacy hash and no
// must-change flag (the operator chose the password); a changed password revokes every session (reason
// password_reset) and records password_changed {source: bootstrap}. platform_admin is granted only when
// PlatformAdmin is true (the email is in PLATFORM_ADMIN_EMAILS) and the account is eligible (bootstrapGrant: an
// existing account whose address a non-platform admin set is refused); a holder outside the list is reported.
// Every change appends its security event in the same transaction.
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
	if !in.PlatformAdmin {
		roles, err := q.ListUserPlatformRoles(ctx, []uuid.UUID{res.UserID})
		if err != nil {
			return res, err
		}
		for _, r := range roles {
			res.RoleOutsideList = res.RoleOutsideList || r.Role == string(authz.PlatformAdmin)
		}
		return res, nil
	}
	out, refusal, err := bootstrapGrant(ctx, tx, q, res.UserID, in.Now)
	if err != nil {
		return res, err
	}
	res.RoleGranted, res.RoleHeld, res.RoleRefused = out == roleGranted, out == roleHeld, refusal
	return res, nil
}
