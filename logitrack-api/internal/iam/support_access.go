package iam

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

const (
	CodeSupportAccessChanged   = "support_access_changed"
	CodeRatificationNotAllowed = "ratification_not_allowed"
	CodeMembershipGrantChanged = "membership_grant_changed"
)

type SupportAccess struct {
	Enabled          bool       `json:"enabled"`
	Version          string     `json:"version"`
	ChangedAt        *time.Time `json:"changedAt"`
	ChangedBy        *uuid.UUID `json:"changedBy"`
	EligibleToToggle bool       `json:"eligibleToToggle"`
}

type SupportAccessInput struct {
	Enabled         *bool  `json:"enabled"`
	ExpectedVersion string `json:"expectedVersion"`
}

type RatifyInput struct {
	ExpectedGrantVersion string `json:"expectedGrantVersion"`
}

type Ratification struct {
	GrantVersion string     `json:"grantVersion"`
	RatifiedAt   *time.Time `json:"ratifiedAt"`
}

func decimalVersion(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n >= 0 && strconv.FormatInt(n, 10) == s
}

func eligibilityBasis(m iamdb.GetSupportMembershipAuthorityRow) string {
	if m.Role != string(authz.TenantAdmin) || m.Status != MembershipActive || m.UserStatus != "active" || m.HasPlatformRole || !m.CredentialEligible {
		return "pending"
	}
	if !m.GrantedByPlatform {
		return "member"
	}
	if m.RatifiedAt != nil {
		return "ratified"
	}
	if m.FirstAdminSelfChosen {
		return "first_admin_self_chosen"
	}
	return "pending"
}

func consentMember(p *authz.Principal, tenant uuid.UUID) bool {
	return p != nil && !p.IsMachine() && !authz.IsSupport(p) && !p.IsPlatform() && p.TenantID != nil && *p.TenantID == tenant
}

// Lock users before tenants and memberships, matching credential/session writers.
func lockSupportUsers(c call, q *iamdb.Queries, ids ...uuid.UUID) error {
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	for _, id := range slices.Compact(ids) {
		if _, err := q.LockAdminUser(c.ctx, id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.ErrNotFound()
			}
			return err
		}
	}
	return nil
}

func (a *Admin) SupportAccess(c call, tenant uuid.UUID, in *SupportAccessInput) (*SupportAccess, error) {
	if c.p == nil || c.p.IsMachine() || c.p.TenantID == nil || *c.p.TenantID != tenant {
		return nil, authz.ErrPermissionDenied("tenant membership required")
	}
	if in != nil {
		if err := authz.RequireTenantWrite(c.p); err != nil {
			return nil, err
		}
		if !consentMember(c.p, tenant) {
			return nil, authz.ErrPermissionDenied("consent requires an eligible member")
		}
	}
	if in == nil {
		return a.readSupportAccess(c, tenant)
	}
	var expected int64
	if in != nil {
		var ok bool
		expected, ok = decimalVersion(in.ExpectedVersion)
		if !ok || in.Enabled == nil {
			return nil, errInvalid(violation("expectedVersion", "invalid"), violation("enabled", "required"))
		}
	}
	var out *SupportAccess
	err := a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, _ func(*auth.PostCommit)) error {
		if in != nil {
			if err := liveIAMContext(c, tx); err != nil {
				return err
			}
		}
		if err := lockSupportUsers(c, q, c.p.UserID); err != nil {
			return err
		}
		t, err := q.LockSupportAccess(c.ctx, tenant)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		eligible := false
		if !authz.IsSupport(c.p) {
			m, e := q.GetSupportMembershipAuthority(c.ctx, iamdb.GetSupportMembershipAuthorityParams{UserID: c.p.UserID, TenantID: tenant})
			if errors.Is(e, pgx.ErrNoRows) || e == nil && m.Status != MembershipActive {
				return httpx.ErrNotFound()
			}
			if e != nil {
				return e
			}
			eligible = consentMember(c.p, tenant) && eligibilityBasis(m) != "pending"
		}
		out = &SupportAccess{Enabled: t.SupportAccessEnabled, Version: strconv.FormatInt(t.SupportAccessVersion, 10), ChangedAt: t.SupportAccessChangedAt, ChangedBy: t.SupportAccessChangedBy, EligibleToToggle: eligible}
		if in == nil {
			return nil
		}
		if !eligible {
			return authz.ErrPermissionDenied("consent requires an eligible member")
		}
		if t.SupportAccessVersion != expected {
			return httpx.NewError(http.StatusConflict, CodeSupportAccessChanged, "support access changed").WithDetails(map[string]any{"version": out.Version})
		}
		if t.SupportAccessEnabled == *in.Enabled {
			return nil
		}
		at := a.clock()
		changed, e := q.CompareAndSwapSupportAccess(c.ctx, iamdb.CompareAndSwapSupportAccessParams{TenantID: tenant, Enabled: *in.Enabled, ExpectedVersion: expected, ChangedAt: at, ActorUserID: c.p.UserID})
		if e != nil {
			return e
		}
		out.Enabled, out.Version, out.ChangedAt, out.ChangedBy = changed.SupportAccessEnabled, strconv.FormatInt(changed.SupportAccessVersion, 10), changed.SupportAccessChangedAt, changed.SupportAccessChangedBy
		event := "support_access_disabled"
		if out.Enabled {
			event = "support_access_enabled"
		}
		if e := a.audit(c, tx, event, "support access consent changed", nil, &tenant, map[string]any{"previousEnabled": t.SupportAccessEnabled, "enabled": out.Enabled, "version": out.Version}); e != nil {
			return e
		}
		return emitTopics(c, tx, "tenant.support_access_changed", "tenant", tenant.String(), &tenant, map[string]any{"tenantId": tenant, "enabled": out.Enabled, "version": out.Version}, []string{realtime.TenantTopic(tenant.String(), realtime.FamilyConfig)})
	})
	return out, err
}

func (a *Admin) Ratify(c call, tenant, user uuid.UUID, in RatifyInput) (*Ratification, error) {
	if err := authz.RequireTenantWrite(c.p); err != nil {
		return nil, err
	}
	denied := func() error {
		return httpx.NewError(http.StatusForbidden, CodeRatificationNotAllowed, "ratification not allowed")
	}
	if !consentMember(c.p, tenant) || user == c.p.UserID {
		return nil, denied()
	}
	expected, ok := decimalVersion(in.ExpectedGrantVersion)
	if !ok {
		return nil, errInvalid(violation("expectedGrantVersion", "invalid"))
	}
	var out *Ratification
	err := a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, _ func(*auth.PostCommit)) error {
		if err := liveIAMContext(c, tx); err != nil {
			return err
		}
		if err := lockSupportUsers(c, q, c.p.UserID, user); err != nil {
			return err
		}
		if _, err := q.LockSupportAccess(c.ctx, tenant); err != nil {
			return err
		}
		actor, err := q.GetSupportMembershipAuthority(c.ctx, iamdb.GetSupportMembershipAuthorityParams{UserID: c.p.UserID, TenantID: tenant})
		if errors.Is(err, pgx.ErrNoRows) {
			return denied()
		}
		if err != nil {
			return err
		}
		if eligibilityBasis(actor) == "pending" {
			return denied()
		}
		target, err := q.GetSupportRatificationTarget(c.ctx, iamdb.GetSupportRatificationTargetParams{UserID: user, TenantID: tenant})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if target.GrantVersion != expected {
			return httpx.NewError(http.StatusConflict, CodeMembershipGrantChanged, "membership grant changed")
		}
		if !target.GrantedByPlatform || target.RatifiedAt != nil || target.PlatformGrantedAt == nil || !actor.CreatedAt.Before(*target.PlatformGrantedAt) {
			return denied()
		}
		r, err := q.RatifySupportMembership(c.ctx, iamdb.RatifySupportMembershipParams{ActorUserID: c.p.UserID, UserID: user, TenantID: tenant, ExpectedGrantVersion: expected, RatifiedAt: a.clock()})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(http.StatusConflict, CodeMembershipGrantChanged, "membership grant changed")
		}
		if err != nil {
			return err
		}
		out = &Ratification{GrantVersion: strconv.FormatInt(r.GrantVersion, 10), RatifiedAt: r.RatifiedAt}
		return a.audit(c, tx, "platform_membership_ratified", "platform membership ratified", &user, &tenant, map[string]any{"grantVersion": out.GrantVersion})
	})
	return out, err
}

func (a *Admin) memberEligibility(c call, tenant uuid.UUID, members []Member) error {
	return db.WithSystem(c.ctx, a.pool, nil, func(tx pgx.Tx) error {
		ids := []uuid.UUID{c.p.UserID}
		for _, r := range members {
			ids = append(ids, r.User.ID)
		}
		facts, err := supportMembershipFacts(c.ctx, tx, tenant, ids)
		if err != nil {
			return err
		}
		owners, err := attributableUserOwners(c.ctx, tx, ids)
		if err != nil {
			return err
		}
		actor := facts[c.p.UserID]
		for i := range members {
			row := &members[i]
			m, ok := facts[row.User.ID]
			if !ok {
				return httpx.ErrNotFound()
			}
			row.User.owner = owners[row.User.ID]
			row.GrantedByPlatform, row.RatifiedAt, row.GrantVersion = m.GrantedByPlatform, m.RatifiedAt, strconv.FormatInt(m.GrantVersion, 10)
			row.EligibilityBasis = eligibilityBasis(m)
			row.EligibleToRatify = consentMember(c.p, tenant) && eligibilityBasis(actor) != "pending" && row.User.ID != c.p.UserID && m.GrantedByPlatform && m.RatifiedAt == nil && m.PlatformGrantedAt != nil && actor.CreatedAt.Before(*m.PlatformGrantedAt)
		}
		return nil
	})
}
func (a *Admin) readSupportAccess(c call, tenant uuid.UUID) (*SupportAccess, error) {
	var out *SupportAccess
	err := db.WithSystem(c.ctx, a.pool, nil, func(tx pgx.Tx) error {
		t, err := iamdb.New(tx).GetSupportAccess(c.ctx, tenant)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		eligible := false
		if !authz.IsSupport(c.p) {
			facts, err := supportMembershipFacts(c.ctx, tx, tenant, []uuid.UUID{c.p.UserID})
			if err != nil {
				return err
			}
			m, ok := facts[c.p.UserID]
			if !ok || m.Status != MembershipActive {
				return httpx.ErrNotFound()
			}
			eligible = consentMember(c.p, tenant) && eligibilityBasis(m) != "pending"
		}
		out = &SupportAccess{Enabled: t.SupportAccessEnabled, Version: strconv.FormatInt(t.SupportAccessVersion, 10), ChangedAt: t.SupportAccessChangedAt, ChangedBy: t.SupportAccessChangedBy, EligibleToToggle: eligible}
		return nil
	})
	return out, err
}
