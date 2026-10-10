package iam

import (
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Grants: memberships, scopes, driver links and platform roles. Each change is one transaction: the rows,
// auth_version++ with claims_changed (the user's clients refresh with the new claims and stay signed in,
// R50; the Firebase claims follow while the bridge mirrors) and the security_events row of Appendix C
// §C.4.13. A request that changes nothing writes nothing.

// Member is one row of GET /v1/tenants/{id}/members (Appendix B §B.2.4 web contract).
type Member struct {
	User        MemberUser `json:"user"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	LastLoginAt *time.Time `json:"lastLoginAt"`
}

// MemberUser identifies the member.
type MemberUser struct {
	ID          uuid.UUID `json:"id"`
	Email       *string   `json:"email"`
	DisplayName *string   `json:"displayName"`
}

// Members is GET /v1/tenants/{id}/members (users:view with the tenant in reach, or a platform principal
// through X-Act-On-Tenant: <id> or *): keyset page, newest membership first.
func (a *Admin) Members(c call, tenant uuid.UUID, role string, limit int, cur string) ([]Member, string, error) {
	var bad []httpx.FieldViolation
	var rolePtr *string
	if role != "" {
		if !authz.TenantRole(role).Valid() {
			bad = append(bad, violation("role", "invalid"))
		}
		rolePtr = &role
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		bad = append(bad, violation("limit", "out_of_range"))
	}
	var k cursor
	if cur != "" {
		var ok bool
		if k, ok = decodeCursor(cur, false); !ok {
			bad = append(bad, violation("cursor", "invalid"))
		}
	}
	if len(bad) > 0 {
		return nil, "", errInvalid(bad...)
	}
	if !readReach(c.p).hasTenant(tenant) {
		return nil, "", httpx.ErrNotFound()
	}
	var out []Member
	var next string
	err := a.read(c.ctx, func(q *iamdb.Queries) error {
		if found, err := q.TenantExists(c.ctx, tenant); err != nil {
			return err
		} else if !found {
			return httpx.ErrNotFound()
		}
		rows, err := q.ListTenantMembers(c.ctx, iamdb.ListTenantMembersParams{TenantID: tenant, Role: rolePtr,
			AfterCreated: k.T, AfterID: k.ID, RowLimit: int32(limit) + 1})
		if err != nil {
			return err
		}
		if len(rows) > limit {
			rows = rows[:limit]
			last := rows[limit-1]
			t := last.CreatedAt
			next = encodeCursor(&t, last.UserID)
		}
		out = make([]Member, 0, len(rows))
		for _, r := range rows {
			out = append(out, Member{User: MemberUser{ID: r.UserID, Email: r.Email, DisplayName: r.DisplayName},
				Role: r.Role, Status: r.Status, LastLoginAt: r.LastLoginAt})
		}
		return nil
	})
	return out, next, err
}

// MembershipResult is the response of PUT /v1/tenants/{id}/members/{userId}.
type MembershipResult struct {
	TenantID uuid.UUID `json:"tenantId"`
	UserID   uuid.UUID `json:"userId"`
	Role     string    `json:"role"`
	Status   string    `json:"status"`
}

// memberTarget checks the tenant and the user of a membership write: the tenant in the write reach (403
// otherwise) and existing (not quarantine: it holds no memberships, C.1.8), key-share locked before the user
// (the lock order of PatchTenant), then the user in reach, locked and outranked (target).
func (a *Admin) memberTarget(c call, q *iamdb.Queries, tenant, user uuid.UUID) error {
	if err := notSelf(c.p, user); err != nil {
		return err
	}
	if !writeReach(c.p).hasTenant(tenant) {
		return authz.ErrPermissionDenied("the tenant is outside the caller's reach").WithDetails(map[string]any{"tenantId": tenant})
	}
	t, err := q.TenantForMembership(c.ctx, tenant)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && t.Kind == "quarantine") {
		return httpx.ErrNotFound()
	}
	if err != nil {
		return err
	}
	_, err = a.target(c, q, user)
	return err
}

// linkedInTenant reports whether user is linked to a driver row of tenant (the driver-link invariant needs
// its driver membership there, trigger t_driver_link_membership).
func linkedInTenant(c call, q *iamdb.Queries, user, tenant uuid.UUID) (bool, error) {
	d, err := q.DriverOfUser(c.ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil && d.TenantID == tenant, err
}

// SetMember is PUT /v1/tenants/{id}/members/{userId} {role} (users:assign_role with the tenant in reach, or
// a platform_admin without X-Act-On-Tenant): adds the membership or changes its role (CanAssignRole), then
// claims_changed and user_role_changed. A linked driver keeps its driver membership (409; unlink first).
func (a *Admin) SetMember(c call, tenant, user uuid.UUID, role string) (*MembershipResult, error) {
	newRole := authz.TenantRole(role)
	if !newRole.Valid() {
		return nil, errInvalid(violation("role", "invalid"))
	}
	err := a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		if err := a.memberTarget(c, q, tenant, user); err != nil {
			return err
		}
		cur, err := q.GetMembership(c.ctx, iamdb.GetMembershipParams{UserID: user, TenantID: tenant})
		found := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := canAssign(c.p, tenant, newRole, authz.TenantRole(cur.Role)); err != nil {
			return err
		}
		if found && cur.Role == role && cur.Status == "active" {
			return nil // unchanged
		}
		if found && cur.Role == string(authz.Driver) && newRole != authz.Driver {
			linked, err := linkedInTenant(c, q, user, tenant)
			if err != nil {
				return err
			}
			if linked {
				return errFailedPrecondition("driver_linked", "the user is linked to a driver of this tenant; unlink it first")
			}
		}
		actor := c.p.UserID
		if err := q.UpsertMembership(c.ctx, iamdb.UpsertMembershipParams{UserID: user, TenantID: tenant, Role: role,
			CreatedBy: &actor}); err != nil {
			return err
		}
		if err := a.claimsChanged(c, tx, user, keep); err != nil {
			return err
		}
		details := map[string]any{"tenantId": tenant, "role": role, "previousRole": nil}
		if found {
			details["previousRole"], details["previousStatus"] = cur.Role, cur.Status
		}
		return a.audit(c, tx, EventUserRoleChanged, "tenant role changed by an admin", &user, &tenant, details)
	})
	if err != nil {
		return nil, err
	}
	return &MembershipResult{TenantID: tenant, UserID: user, Role: role, Status: "active"}, nil
}

// RemoveMember is DELETE /v1/tenants/{id}/members/{userId}: the same rules as SetMember; 404 without a
// membership.
func (a *Admin) RemoveMember(c call, tenant, user uuid.UUID) error {
	return a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		if err := a.memberTarget(c, q, tenant, user); err != nil {
			return err
		}
		cur, err := q.GetMembership(c.ctx, iamdb.GetMembershipParams{UserID: user, TenantID: tenant})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if err := canAssign(c.p, tenant, "", authz.TenantRole(cur.Role)); err != nil {
			return err
		}
		if linked, err := linkedInTenant(c, q, user, tenant); err != nil {
			return err
		} else if linked {
			return errFailedPrecondition("driver_linked", "the user is linked to a driver of this tenant; unlink it first")
		}
		if _, err := q.DeleteMembership(c.ctx, iamdb.DeleteMembershipParams{UserID: user, TenantID: tenant}); err != nil {
			return err
		}
		if err := a.claimsChanged(c, tx, user, keep); err != nil {
			return err
		}
		return a.audit(c, tx, EventUserRoleChanged, "tenant membership removed by an admin", &user, &tenant,
			map[string]any{"tenantId": tenant, "role": nil, "previousRole": cur.Role})
	})
}

// ScopeResult is the response of PUT /v1/users/{id}/scopes/{kind}.
type ScopeResult struct {
	Kind            string      `json:"kind"`
	BillingPartyIDs []uuid.UUID `json:"billingPartyIds"`
}

// checkScopeKind: only customer and dispatcher exist (R86); any other kind is 422 (issue T19 acceptance).
func checkScopeKind(kind string) error {
	if kind != string(authz.ScopeCustomer) && kind != string(authz.ScopeDispatcher) {
		return errInvalid(violation("kind", "invalid"))
	}
	return nil
}

// scopeAllowed: customer scopes need users:assign_role and a steward or platform caller (global parties,
// R60); dispatcher grants need platform:manage_platform_roles (C.1.7).
func scopeAllowed(p *authz.Principal, kind string) error {
	if kind == string(authz.ScopeDispatcher) {
		if !p.Can(authz.PlatformManageRoles) {
			return authz.ErrPermissionDenied("missing capability", authz.PlatformManageRoles)
		}
		return nil
	}
	if !p.Can(authz.UsersAssignRole) {
		return authz.ErrPermissionDenied("missing capability", authz.UsersAssignRole)
	}
	return stewardOrPlatform(p)
}

// SetScopes is PUT /v1/users/{id}/scopes/{kind} {billingPartyIds} (at most 20 parties across the user's
// scopes, else 422 too_many_scopes): replaces the user's scopes of that kind. A dispatcher grant needs a
// membership in the dispatcher's own organisation (R13, 409 otherwise).
func (a *Admin) SetScopes(c call, user uuid.UUID, kind string, raw []string) (*ScopeResult, error) {
	if err := checkScopeKind(kind); err != nil {
		return nil, err
	}
	if err := scopeAllowed(c.p, kind); err != nil {
		return nil, err
	}
	ids, ok := parseIDs(raw)
	switch {
	case !ok:
		return nil, errInvalid(violation("billingPartyIds", "invalid"))
	case len(ids) == 0:
		return nil, errInvalid(violation("billingPartyIds", "required"))
	case len(ids) > MaxScopes:
		return nil, errTooManyScopes()
	}
	err := a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		if _, err := a.target(c, q, user); err != nil {
			return err
		}
		if kind == string(authz.ScopeDispatcher) {
			has, err := q.UserHasMembership(c.ctx, user)
			if err != nil {
				return err
			}
			if !has {
				return errFailedPrecondition("membership_required", "a dispatcher holds a membership in its own organisation (R13)")
			}
		}
		found, err := q.ExistingParties(c.ctx, ids)
		if err != nil {
			return err
		}
		if len(found) != len(ids) {
			return errInvalid(violation("billingPartyIds", "not_found"))
		}
		others, err := q.CountOtherScopes(c.ctx, iamdb.CountOtherScopesParams{UserID: user, Kind: kind})
		if err != nil {
			return err
		}
		if int(others)+len(ids) > MaxScopes {
			return errTooManyScopes()
		}
		prev, err := q.ListScopeParties(c.ctx, iamdb.ListScopePartiesParams{UserID: user, Kind: kind})
		if err != nil {
			return err
		}
		if sameSet(prev, ids) {
			return nil
		}
		if err := q.DeleteScopesNotIn(c.ctx, iamdb.DeleteScopesNotInParams{UserID: user, Kind: kind, Keep: ids}); err != nil {
			return err
		}
		actor := c.p.UserID
		for _, id := range ids {
			if err := q.InsertScope(c.ctx, iamdb.InsertScopeParams{UserID: user, Kind: kind, BillingPartyID: id, CreatedBy: &actor}); err != nil {
				return err
			}
		}
		if err := a.claimsChanged(c, tx, user, keep); err != nil {
			return err
		}
		return a.audit(c, tx, EventUserScopeChanged, "scope changed by an admin", &user, nil,
			map[string]any{"kind": kind, "billingPartyIds": ids, "previous": prev})
	})
	if err != nil {
		return nil, err
	}
	return &ScopeResult{Kind: kind, BillingPartyIDs: ids}, nil
}

// DeleteScopes is DELETE /v1/users/{id}/scopes/{kind}: removes every scope of that kind.
func (a *Admin) DeleteScopes(c call, user uuid.UUID, kind string) error {
	if err := checkScopeKind(kind); err != nil {
		return err
	}
	if err := scopeAllowed(c.p, kind); err != nil {
		return err
	}
	return a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		if _, err := a.target(c, q, user); err != nil {
			return err
		}
		prev, err := q.ListScopeParties(c.ctx, iamdb.ListScopePartiesParams{UserID: user, Kind: kind})
		if err != nil {
			return err
		}
		if len(prev) == 0 {
			return nil
		}
		if err := q.DeleteScopesNotIn(c.ctx, iamdb.DeleteScopesNotInParams{UserID: user, Kind: kind, Keep: []uuid.UUID{}}); err != nil {
			return err
		}
		if err := a.claimsChanged(c, tx, user, keep); err != nil {
			return err
		}
		return a.audit(c, tx, EventUserScopeChanged, "scope removed by an admin", &user, nil,
			map[string]any{"kind": kind, "billingPartyIds": []uuid.UUID{}, "previous": prev})
	})
}

func sameSet(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// LinkDriver is PUT /v1/users/{id}/driver-link {driverId} (drivers:edit + users:manage): one transaction
// moves the link (Appendix C §C.1.4): the user's previous driver row is cleared, drivers.user_id is set, the
// driver membership in the driver's tenant is written (a user holding another role there is 409), and
// driver_linked is recorded. A driver linked to another user is 409; the driver must be in the caller's reach, and
// so must the user's current driver row (409 failed_precondition linked_in_other_tenant otherwise).
func (a *Admin) LinkDriver(c call, user, driver uuid.UUID) error {
	return a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		if _, err := a.target(c, q, user); err != nil {
			return err
		}
		d, err := q.GetLinkDriver(c.ctx, driver)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !writeReach(c.p).hasTenant(d.TenantID)) {
			return errInvalid(violation("driverId", "not_found"))
		}
		if err != nil {
			return err
		}
		if d.UserID != nil && *d.UserID == user {
			return nil // already linked
		}
		if d.UserID != nil {
			return errAlreadyExists("driverId", "linked_to_another_user")
		}
		m, err := q.GetMembership(c.ctx, iamdb.GetMembershipParams{UserID: user, TenantID: d.TenantID})
		switch {
		case errors.Is(err, pgx.ErrNoRows), err == nil && m.Role == string(authz.Driver):
		case err != nil:
			return err
		default:
			return errFailedPrecondition("role_in_tenant", "the user holds another role in the driver's tenant").
				WithDetails(map[string]any{"reason": "role_in_tenant", "role": m.Role})
		}
		var previous *uuid.UUID
		prev, err := q.DriverOfUser(c.ctx, user)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case !writeReach(c.p).hasTenant(prev.TenantID):
			// Never clear another tenant's driver row (outranks already refuses a user with a membership there,
			// which the driver-link trigger guarantees; this keeps the rule local). No ids of that tenant leak.
			return errFailedPrecondition("linked_in_other_tenant",
				"the user is linked to a driver of a tenant outside the caller's reach")
		default:
			previous = &prev.ID
			if err := q.SetDriverUser(c.ctx, iamdb.SetDriverUserParams{UserID: nil, ID: prev.ID}); err != nil {
				return err
			}
		}
		actor := c.p.UserID
		if err := q.UpsertMembership(c.ctx, iamdb.UpsertMembershipParams{UserID: user, TenantID: d.TenantID,
			Role: string(authz.Driver), CreatedBy: &actor}); err != nil {
			return err
		}
		if err := q.SetDriverUser(c.ctx, iamdb.SetDriverUserParams{UserID: &user, ID: d.ID}); err != nil {
			return err
		}
		if err := a.claimsChanged(c, tx, user, keep); err != nil {
			return err
		}
		return a.audit(c, tx, EventDriverLinked, "driver linked to a user by an admin", &user, &d.TenantID,
			map[string]any{"driverId": d.ID, "tenantId": d.TenantID, "previousDriverId": previous})
	})
}

// UnlinkDriver is DELETE /v1/users/{id}/driver-link: clears drivers.user_id (404 without a link in reach);
// the driver membership stays (a role change is a separate act); driver_unlinked is recorded.
func (a *Admin) UnlinkDriver(c call, user uuid.UUID) error {
	return a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		if _, err := a.target(c, q, user); err != nil {
			return err
		}
		d, err := q.DriverOfUser(c.ctx, user)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !writeReach(c.p).hasTenant(d.TenantID)) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if err := q.SetDriverUser(c.ctx, iamdb.SetDriverUserParams{UserID: nil, ID: d.ID}); err != nil {
			return err
		}
		if err := a.claimsChanged(c, tx, user, keep); err != nil {
			return err
		}
		return a.audit(c, tx, EventDriverUnlinked, "driver unlinked from a user by an admin", &user, &d.TenantID,
			map[string]any{"driverId": d.ID, "tenantId": d.TenantID})
	})
}

// checkPlatformRole: platform_admin or support.
func checkPlatformRole(role string) error {
	if role != string(authz.PlatformAdmin) && role != string(authz.Support) {
		return errInvalid(violation("role", "invalid"))
	}
	return nil
}

// GrantPlatformRole is POST /v1/users/{id}/platform-roles {role} (platform:manage_platform_roles, not self).
func (a *Admin) GrantPlatformRole(c call, user uuid.UUID, role string) error {
	if err := checkPlatformRole(role); err != nil {
		return err
	}
	return a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		if _, err := a.target(c, q, user); err != nil {
			return err
		}
		actor := c.p.UserID
		n, err := q.InsertPlatformRole(c.ctx, iamdb.InsertPlatformRoleParams{UserID: user, Role: role, GrantedBy: &actor})
		if err != nil || n == 0 {
			return err // held already: nothing changes
		}
		if err := a.claimsChanged(c, tx, user, keep); err != nil {
			return err
		}
		return a.audit(c, tx, EventPlatformRoleGranted, "platform role granted", &user, nil,
			map[string]any{"role": role, "source": "api"})
	})
}

// RevokePlatformRole is DELETE /v1/users/{id}/platform-roles/{role}: 404 when the user does not hold it.
func (a *Admin) RevokePlatformRole(c call, user uuid.UUID, role string) error {
	if err := checkPlatformRole(role); err != nil {
		return err
	}
	return a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, keep func(*auth.PostCommit)) error {
		if _, err := a.target(c, q, user); err != nil {
			return err
		}
		n, err := q.DeletePlatformRole(c.ctx, iamdb.DeletePlatformRoleParams{UserID: user, Role: role})
		if err != nil {
			return err
		}
		if n == 0 {
			return httpx.ErrNotFound()
		}
		if err := a.claimsChanged(c, tx, user, keep); err != nil {
			return err
		}
		return a.audit(c, tx, EventPlatformRoleRevoked, "platform role revoked", &user, nil,
			map[string]any{"role": role, "source": "api"})
	})
}
