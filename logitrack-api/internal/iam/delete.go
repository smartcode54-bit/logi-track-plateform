package iam

import (
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
)

// DeleteUser is DELETE /v1/users/{id} (platform_admin, T51; replaces onUserDeleted, fn:triggers.ts:35): a soft
// delete in one transaction. The users row stays with status deleted and deleted_at (history, audit rows and the
// legacy uid keep pointing at it; its email leaves the unique index, so the address can go to a new account); every
// grant goes (memberships, scopes, platform roles), and so does its Google identity (the firebase_legacy row stays),
// a linked drivers row is unlinked so the driver can get another account, every session is revoked with reason
// disabled (auth_version++, SSE session.revoked, and the Firebase account is disabled while the bridge mirrors,
// Appendix C §C.4.7), and user_deleted records what was removed. Without any grant the account leaves every tenant's
// and steward's read reach: only a platform principal under X-Act-On-Tenant: * lists it (C.8 "Reach"). Not self
// (403); an unknown or already deleted user is 404.
func (a *Admin) DeleteUser(c call, id uuid.UUID) error {
	return a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		u, err := a.target(c, q, id) // locks the users row first (C.4.4)
		if err != nil {
			return err
		}
		details := map[string]any{"previousStatus": u.Status}
		drv, err := q.DriverOfUser(c.ctx, id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		default:
			if err := q.SetDriverUser(c.ctx, iamdb.SetDriverUserParams{UserID: nil, ID: drv.ID}); err != nil {
				return err
			}
			details["driverId"] = drv.ID
		}
		ms, err := q.DeleteUserMemberships(c.ctx, id)
		if err != nil {
			return err
		}
		memberships := make([]map[string]any, len(ms))
		for i, m := range ms {
			memberships[i] = map[string]any{"tenantId": m.TenantID, "role": m.Role}
		}
		ss, err := q.DeleteUserScopes(c.ctx, id)
		if err != nil {
			return err
		}
		scopes := make([]map[string]any, len(ss))
		for i, s := range ss {
			scopes[i] = map[string]any{"kind": s.Kind, "billingPartyId": s.BillingPartyID}
		}
		prs, err := q.DeleteUserPlatformRoles(c.ctx, id)
		if err != nil {
			return err
		}
		// The Google link goes too: it would keep resolving the Google account to this deleted user (403 no_account
		// for ever), and a new account given the freed address could never link it (C.4.10).
		gi, err := q.DeleteUserGoogleIdentity(c.ctx, id)
		if err != nil {
			return err
		}
		details["googleIdentityRemoved"] = gi > 0
		if err := q.SoftDeleteUser(c.ctx, iamdb.SoftDeleteUserParams{At: a.clock(), ID: id}); err != nil {
			return err
		}
		actor := c.p.UserID
		pc, sids, err := a.auth.RevokeInTx(c.ctx, tx, auth.Revocation{
			UserID: id, Reason: auth.RevokeDisabled, RevokedBy: &actor, BumpVersion: true, RequestID: c.rid,
		})
		if err != nil {
			return err
		}
		keep(pc)
		if sids == nil {
			sids = []uuid.UUID{}
		}
		details["memberships"], details["scopes"], details["platformRoles"], details["sessionIds"] = memberships, scopes, prs, sids
		return a.audit(c, tx, EventUserDeleted, "user deleted by a platform admin", &id, nil, details)
	})
}
