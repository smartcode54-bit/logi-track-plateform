package iam

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// EventDriverTenantMoved is the security event of a broker-carrier move (Appendix C §C.4.13), written in the move's
// transaction.
const EventDriverTenantMoved = "driver_tenant_moved"

// RouteDriverUpdated is the outbox key a move emits (Appendix B §B.5.2; internal/drivers emits it for its writes).
const RouteDriverUpdated = "driver.updated"

// ReasonDriverHoldsTruck is the 409 failed_precondition reason of a move refused while the driver still holds a truck
// (an active truck assignment, or the truck of a running job): revoke the assignment or end the job first.
const ReasonDriverHoldsTruck = "driver_holds_truck"

// MoveDriver is the broker-carrier move of PATCH /v1/drivers/{id} {tenantId} (Appendix C §C.1.4; internal/drivers
// hands it over, the route already checked drivers:edit). It is identity work, so it runs in one system transaction
// after Go authorization:
//
//   - the caller is a platform_admin or a steward (own-fleet staff, R60) and a person (403 steward_only otherwise);
//   - the driver and the target tenant are in the caller's write reach (a platform_admin reaches every tenant; a
//     steward its tenant and the carriers working for it): a driver outside it is 404, a target outside it
//     422 tenantId not_in_reach, an unknown or quarantine target 422 tenantId not_found, a pending or suspended one
//     409 failed_precondition tenant_inactive;
//   - the driver holds no truck: no active truck assignment and no active truck (409 failed_precondition
//     driver_holds_truck; an assignment and a running job belong to the former tenant's trucks);
//   - a linked user moves its driver membership in the same transaction (409 failed_precondition role_in_tenant when
//     it holds another role in the target tenant), with auth_version++ and claims_changed (its sessions re-pick the
//     tenant at the next refresh);
//   - tenancy.MoveDriver moves the row and its files under app.tenant_move; driver_tenant_moved (tenant: the target)
//     and outbox driver.updated (changed: tenantId; the fleet topics of both tenants) are written in the transaction.
//
// It reports false, writing nothing, when the driver is already in the target tenant.
func (a *Admin) MoveDriver(ctx context.Context, p *authz.Principal, rid string, driverID, to uuid.UUID) (bool, error) {
	c := call{ctx: ctx, p: p, rid: rid}
	if p.IsMachine() || (!p.Steward && !p.HasPlatform(authz.PlatformAdmin)) {
		return false, authz.ErrPermissionDenied("a driver moves to another tenant only by a steward or a platform admin").
			WithDetails(map[string]any{"reason": ReasonStewardOnly})
	}
	r := writeReach(p)
	if !r.hasTenant(to) {
		return false, errInvalid(violation("tenantId", "not_in_reach"))
	}
	moved := false
	err := a.tx(ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		t, err := q.TenantForMembership(ctx, to)
		switch {
		case errors.Is(err, pgx.ErrNoRows), err == nil && t.Kind == authz.TenantKindQuarantine:
			return errInvalid(violation("tenantId", "not_found"))
		case err != nil:
			return err
		case t.Status != "active":
			return errFailedPrecondition("tenant_inactive", "the target tenant is not active").
				WithDetails(map[string]any{"reason": "tenant_inactive", "status": t.Status})
		}
		// Lock order users -> drivers (PUT /v1/users/{id}/driver-link takes the same): the login first, then the driver,
		// whose link must not have changed in between.
		user, err := q.DriverUser(ctx, driverID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if user != nil {
			if _, err := q.LockAdminUser(ctx, *user); err != nil {
				return err
			}
		}
		d, err := q.LockMoveDriver(ctx, driverID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !r.hasTenant(d.TenantID)) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if !uuidPtrEqual(user, d.UserID) {
			return errFailedPrecondition("driver_link_changed", "the driver's login changed meanwhile; retry")
		}
		from := d.TenantID
		if from == to {
			return nil
		}
		if d.HasActiveAssignment || d.CurrentAssignmentID != nil || d.ActiveTruckID != nil {
			return errFailedPrecondition(ReasonDriverHoldsTruck,
				"the driver still holds a truck assignment or an active truck; revoke it or end the job first")
		}
		details := map[string]any{"driverId": driverID, "from": from, "to": to}
		if d.UserID != nil {
			uid := *d.UserID
			m, err := q.GetMembership(ctx, iamdb.GetMembershipParams{UserID: uid, TenantID: to})
			switch {
			case errors.Is(err, pgx.ErrNoRows), err == nil && m.Role == string(authz.Driver):
			case err != nil:
				return err
			default:
				return errFailedPrecondition("role_in_tenant", "the driver's user holds another role in the target tenant").
					WithDetails(map[string]any{"reason": "role_in_tenant", "role": m.Role})
			}
			old, err := q.GetMembership(ctx, iamdb.GetMembershipParams{UserID: uid, TenantID: from})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return err
			case old.Role == string(authz.Driver):
				if _, err := q.DeleteMembership(ctx, iamdb.DeleteMembershipParams{UserID: uid, TenantID: from}); err != nil {
					return err
				}
			}
			actor := p.UserID
			if err := q.UpsertMembership(ctx, iamdb.UpsertMembershipParams{UserID: uid, TenantID: to,
				Role: string(authz.Driver), CreatedBy: &actor}); err != nil {
				return err
			}
			if err := a.claimsChanged(c, tx, uid, keep); err != nil {
				return err
			}
			details["userId"] = uid
		}
		files, err := tenancy.MoveDriver(ctx, tx, driverID, from, to)
		if err != nil {
			return err
		}
		details["files"] = len(files)
		if err := a.audit(c, tx, EventDriverTenantMoved, "driver moved to another tenant", d.UserID, &to, details); err != nil {
			return err
		}
		moved = true
		return emitTopics(c, tx, RouteDriverUpdated, "driver", driverID.String(), &to,
			map[string]any{"entity": "driver", "id": driverID.String(), "changed": []string{"tenantId"}},
			[]string{realtime.TenantTopic(to.String(), realtime.FamilyFleet), realtime.TenantTopic(from.String(), realtime.FamilyFleet)})
	})
	return moved, err
}

func uuidPtrEqual(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
