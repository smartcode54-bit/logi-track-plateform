package iam

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/supportprojection"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/searchtext"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

func PlatformProjection(pool db.Beginner) fiber.Handler { return supportprojection.Middleware(pool) }

func platformRead(p *authz.Principal, cap authz.Cap) error {
	if p == nil || p.IsMachine() || !p.IsPlatform() || !authz.PlatformCaps(p).Has(cap) {
		return authz.ErrPermissionDenied("missing platform read capability", cap)
	}
	return nil
}
func userOwner(u User) string {
	if u.projectionOwner != nil {
		return *u.projectionOwner
	}
	if len(u.Memberships) == 1 {
		return u.Memberships[0].TenantID.String()
	}
	if len(u.Memberships) == 0 && len(u.Scopes) == 0 && hasPlatformPlaneRole(u.PlatformRoles) {
		return supportprojection.PlatformOwner
	}
	return ""
}

func hasPlatformPlaneRole(roles []string) bool {
	return slices.Contains(roles, "platform_admin") || slices.Contains(roles, "support")
}

func (a *Admin) PlatformUsers(c call, in ListUsersInput) ([]User, string, error) {
	if err := platformRead(c.p, authz.UsersView); err != nil {
		return nil, "", err
	}
	if in.Limit > 100 {
		return nil, "", errInvalid(violation("limit", "out_of_range"))
	}
	if in.Q != "" {
		return nil, "", httpx.NewError(403, httpx.CodeSupportAccessMasked, "masked search unavailable")
	}
	return a.listUsers(c.ctx, c.p, reach{all: true}, in)
}
func (a *Admin) PlatformUser(c call, id uuid.UUID) (*User, error) {
	if err := platformRead(c.p, authz.UsersView); err != nil {
		return nil, err
	}
	return a.loadUser(c.ctx, c.p, reach{all: true}, id)
}

// PlatformEvent deliberately omits untyped snapshots and foreign/global identity joins.
// The underlying legacy details may contain multiple tenants, even when tenant_id is set.
type PlatformEvent struct {
	ID             uuid.UUID             `json:"id"`
	CreatedAt      time.Time             `json:"createdAt"`
	Type           string                `json:"type"`
	Severity       string                `json:"severity"`
	Summary        string                `json:"summary"`
	ActorUserID    *uuid.UUID            `json:"actorUserId"`
	ActorEmail     *string               `json:"actorEmail"`
	TargetUserID   *uuid.UUID            `json:"targetUserId"`
	TenantID       *uuid.UUID            `json:"tenantId"`
	RequestID      *string               `json:"requestId"`
	Details        *PlatformEventDetails `json:"details"`
	ActorLegacyUID *string               `json:"actorLegacyUid"`
	actorOwner     string
}

func (a *Admin) PlatformEvents(c call, in EventsInput) ([]PlatformEvent, string, error) {
	if err := platformRead(c.p, authz.SecurityViewAudit); err != nil {
		return nil, "", err
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if in.Limit < 1 || in.Limit > 100 {
		return nil, "", errInvalid(violation("limit", "out_of_range"))
	}
	f := security.Filter{Limit: in.Limit}
	if in.Type != "" {
		if !security.KnownType(in.Type) {
			return nil, "", errInvalid(violation("type", "invalid"))
		}
		f.Types = []string{in.Type}
	}
	if in.Severity != "" {
		if !security.ValidSeverity(in.Severity) {
			return nil, "", errInvalid(violation("severity", "invalid"))
		}
		f.Severity = in.Severity
	}
	for _, v := range []struct {
		s string
		t **time.Time
	}{{in.From, &f.From}, {in.To, &f.To}} {
		if v.s != "" {
			t, e := time.Parse(time.RFC3339Nano, v.s)
			if e != nil {
				return nil, "", errInvalid(violation("from", "invalid"))
			}
			*v.t = &t
		}
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		return nil, "", errInvalid(violation("to", "before_from"))
	}
	if in.Cursor != "" {
		cur, ok := security.DecodeCursor(in.Cursor)
		if !ok {
			return nil, "", errInvalid(violation("cursor", "invalid"))
		}
		f.After = &cur
	}
	var out []PlatformEvent
	var next string
	err := db.WithSystem(c.ctx, a.pool, nil, func(tx pgx.Tx) error {
		rows, n, err := security.List(c.ctx, tx, f)
		if err != nil {
			return err
		}
		next = n
		actorIDs := []uuid.UUID{}
		for _, r := range rows {
			if r.ActorUserID != nil {
				actorIDs = append(actorIDs, *r.ActorUserID)
			}
		}
		actorOwners, err := attributableUserOwners(c.ctx, tx, actorIDs)
		if err != nil {
			return err
		}
		out = make([]PlatformEvent, 0, len(rows))
		for _, r := range rows {
			if !security.KnownType(r.Type) {
				r.Type = "unknown"
			}
			out = append(out, PlatformEvent{ID: r.ID, CreatedAt: r.CreatedAt, Type: r.Type, Severity: r.Severity, Summary: r.Type, ActorUserID: r.ActorUserID, ActorEmail: r.ActorEmail, Details: safeEventDetails(r), TargetUserID: r.TargetUserID, TenantID: r.TenantID, RequestID: r.RequestID, actorOwner: actorOwners[derefUUID(r.ActorUserID)]})
		}
		return nil
	})
	return out, next, err
}

func (a *Admin) platformSessions(c call, id uuid.UUID) ([]Session, error) {
	if err := platformRead(c.p, authz.UsersViewSessions); err != nil {
		return nil, err
	}
	var out []Session
	err := a.read(c.ctx, func(q *iamdb.Queries) error {
		u, found, err := a.getUser(c.ctx, q, reach{all: true}, id)
		if err != nil {
			return err
		} else if !found {
			return httpx.ErrNotFound()
		}
		rows, err := q.ListAdminSessions(c.ctx, iamdb.ListAdminSessionsParams{UserID: id, Now: a.clock()})
		if err != nil {
			return err
		}
		out = make([]Session, 0, len(rows))
		for _, s := range rows {
			out = append(out, Session{ID: s.ID, Platform: s.Platform, AMR: s.Amr, InstallID: s.InstallID, DeviceLabel: s.DeviceLabel, AppVersion: s.AppVersion, IP: optText(s.Ip), UserAgent: s.UserAgent, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, owner: userOwner(u)})
		}
		return nil
	})
	return out, err
}

func (a *Admin) projectedSubcontractors(c call, in ListSubcontractorsInput) (any, string, error) {
	if !authz.IsSupport(c.p) && in.Fields != "minimal" && !c.p.Can(authz.FleetManageSubcontractors) && !authz.PlatformCaps(c.p).Has(authz.PlatformManageTenants) {
		return nil, "", authz.ErrPermissionDenied("missing capability", authz.FleetManageSubcontractors, authz.PlatformManageTenants)
	}
	if in.Fields != "" && in.Fields != "minimal" {
		return nil, "", errInvalid(violation("fields", "invalid"))
	}
	if !utf8.ValidString(in.Q) || utf8.RuneCountInString(in.Q) > 100 || strings.ContainsRune(in.Q, 0) {
		return nil, "", errInvalid(violation("q", "invalid"))
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return nil, "", errInvalid(violation("limit", "out_of_range"))
	}
	var pos cursor
	if in.Cursor != "" {
		var ok bool
		pos, ok = decodeCursor(in.Cursor, true)
		if !ok {
			return nil, "", errInvalid(violation("cursor", "invalid"))
		}
	}
	if in.Status != "" && !slices.Contains(tenantStatuses, in.Status) {
		return nil, "", errInvalid(violation("status", "invalid"))
	}
	out := []SubcontractorSummary{}
	next := ""
	if c.labels == nil {
		c.labels = map[uuid.UUID]platformLabel{}
	}
	err := db.WithSystem(c.ctx, a.pool, nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(c.ctx, `WITH visible AS (
 SELECT t.*, CASE WHEN support_access_enabled OR legal_type='company' THEN name_th ELSE '****' END AS visible_name,
 CASE WHEN support_access_enabled OR legal_type='company' THEN name_th||' '||coalesce(name_en,'') ELSE '' END||' '||CASE WHEN support_access_enabled OR support_code_reviewed THEN coalesce(code::text,'') ELSE '' END AS visible_search
 FROM tenants t WHERE kind='carrier' AND (NOT $1 OR id=$2) AND ($3='' OR status=$3))
 SELECT v.id,v.code::text,v.name_th,v.name_en,v.legal_type,v.status,v.contact_person,v.phone,v.email,v.fleet_size,v.service_regions,v.line_group_id,v.contractor_tenant_id,bp.billing_date_basis,v.created_at,bp.id,v.support_access_enabled,v.support_code_reviewed,v.support_label_version
 FROM visible v LEFT JOIN billing_parties bp ON bp.tenant_id=v.id
 WHERE ($4::text[] IS NULL OR search_fold(v.visible_search) LIKE ALL($4::text[]) OR lower($5)=lower('TEN-'||v.id::text))
 AND ($6::uuid='00000000-0000-0000-0000-000000000000' OR (v.visible_name,v.id)>(SELECT visible_name,id FROM visible WHERE id=$6))
 ORDER BY v.visible_name,v.id LIMIT $7`, authz.IsSupport(c.p), c.p.TenantID, in.Status, searchtext.LikePatterns(in.Q), strings.TrimSpace(in.Q), pos.ID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r SubcontractorSummary
			var label platformLabel
			if err := rows.Scan(&r.ID, &r.Code, &r.NameTh, &r.NameEn, &r.LegalType, &r.Status, &r.ContactPerson, &r.Phone, &r.Email, &r.FleetSize, &r.ServiceRegions, &r.LineGroupID, &r.ContractorTenantID, &r.BillingDateBasis, &r.CreatedAt, &r.BillingPartyID, &label.enabled, &label.reviewed, &label.version); err != nil {
				return err
			}
			if len(out) == limit {
				next = encodeCursor(nil, out[len(out)-1].ID)
				break
			}
			label.kind = "carrier"
			label.nameTh, label.nameEn, label.code = r.NameTh, r.NameEn, r.Code
			if r.LegalType != nil {
				label.legal = *r.LegalType
			}
			c.labels[r.ID] = label
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}
	minimal := []SubcontractorMinimal{}
	for i := range out {
		r := &out[i]
		t := Tenant{ID: r.ID}
		if err := a.projectTenantLabels(c, &t); err != nil {
			return nil, "", err
		}
		r.Code, r.NameTh, r.NameEn = t.Code, t.NameTh, t.NameEn
		minimal = append(minimal, SubcontractorMinimal{ID: r.ID, Code: r.Code, NameTh: r.NameTh, NameEn: r.NameEn, Status: r.Status, BillingPartyID: r.BillingPartyID})
	}
	if in.Fields == "minimal" {
		return minimal, next, nil
	}
	return out, next, nil
}

// Arbitrary legacy details are never forwarded. Only this closed audit vocabulary is projected.
type PlatformEventDetails struct {
	Action  string `json:"action,omitempty"`
	Basis   string `json:"basis,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	Version string `json:"version,omitempty"`
}

func safeEventDetails(row security.Row) *PlatformEventDetails {
	var in PlatformEventDetails
	if json.Unmarshal(row.Details, &in) != nil {
		return nil
	}
	if !slices.Contains([]string{"membership_set", "membership_add", "membership_remove", "user_create", "user_delete", "profile", "invite", "temporary_password", "account_status", "disable", "enable", "driver_link", "driver_unlink", "platform_role_grant", "platform_role_remove", "scopes_set", "scopes_remove", "sessions_revoke"}, in.Action) {
		in.Action = ""
	}
	if in.Basis != "onboarding" && in.Basis != "consent" {
		in.Basis = ""
	}
	if in.Outcome != "allowed" && in.Outcome != "accepted" {
		in.Outcome = ""
	}
	if _, ok := decimalVersion(in.Version); !ok {
		in.Version = ""
	}
	if in == (PlatformEventDetails{}) {
		return nil
	}
	return &in
}

// Account for nested owners before returning a page to the serializer's 100-owner policy transaction.
func platformUserPageSize(users []User, p *authz.Principal) int {
	owners := map[string]bool{}
	if authz.IsSupport(p) && p.TenantID != nil {
		owners[p.TenantID.String()] = true
	}
	for i, u := range users {
		candidate := map[string]bool{}
		if owner := userOwner(u); owner != "" {
			candidate[owner] = true
		}
		for _, m := range u.Memberships {
			candidate[m.TenantID.String()] = true
		}
		if u.Driver != nil && u.Driver.owner != "" {
			candidate[u.Driver.owner] = true
		}
		count := len(owners)
		for id := range candidate {
			if !owners[id] {
				count++
			}
		}
		if count > 100 {
			return i
		}
		for id := range candidate {
			owners[id] = true
		}
	}
	return len(users)
}
