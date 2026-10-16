package iam

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Audit access in member mode is derived from the live membership and its overrides alone.
func (a *Admin) memberAuditPrincipal(c call) (*authz.Principal, error) {
	p := c.p
	if p == nil {
		return nil, httpx.ErrUnauthenticated()
	}
	if authz.IsSupport(p) || p.TenantID == nil || p.ActOnAll || p.ActOnTenant != nil {
		return nil, httpx.ErrSupportViewUnavailable()
	}
	var role, status, kind string
	var overrides []authz.Override
	var subs = p.SubtenantIDs[:0:0]
	err := a.read(c.ctx, func(q *iamdb.Queries) error {
		m, err := q.GetMembership(c.ctx, iamdb.GetMembershipParams{UserID: p.UserID, TenantID: *p.TenantID})
		if err != nil {
			return err
		}
		role, status = m.Role, m.Status
		kind, err = q.TenantKind(c.ctx, *p.TenantID)
		if err != nil {
			return err
		}
		rows, err := q.RoleOverrides(c.ctx, iamdb.RoleOverridesParams{Role: role, TenantID: *p.TenantID})
		if err != nil {
			return err
		}
		for _, r := range rows {
			overrides = append(overrides, authz.Override{TenantID: r.TenantID, Cap: authz.Cap(r.Capability), Allowed: r.Allowed})
		}
		if kind == authz.TenantKindOwnFleet && authz.TenantRole(role).IsStaff() {
			subs, err = q.Subtenants(c.ctx, *p.TenantID)
		}
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && status != MembershipActive {
		return nil, httpx.ErrSupportViewUnavailable()
	}
	if err != nil {
		return nil, err
	}
	member := *p
	member.Platform = nil
	member.TenantRole = authz.TenantRole(role)
	member.TenantKind = kind
	member.Steward = authz.IsSteward(&member, kind)
	member.SubtenantIDs = subs
	member.Caps = authz.RoleCaps(member.TenantRole, member.Steward, overrides)
	if !member.Can(authz.SecurityViewAudit) {
		return nil, authz.ErrPermissionDenied("missing tenant audit capability", authz.SecurityViewAudit)
	}
	return &member, nil
}
