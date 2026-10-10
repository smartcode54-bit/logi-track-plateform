package auth

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
)

// Platforms of a session (sessions.platform). Mobile sessions carry an install id and a 90-day
// absolute refresh life; web and script sessions slide (C.4.4).
const (
	PlatformWeb     = "web"
	PlatformAndroid = "android"
	PlatformIOS     = "ios"
	PlatformScript  = "script"
)

func isMobile(platform string) bool { return platform == PlatformAndroid || platform == PlatformIOS }

// Tenant is one membership as the login response and GET /v1/me/tenants show it.
type Tenant struct {
	ID     uuid.UUID `json:"id"`
	NameTh string    `json:"nameTh"`
	NameEn *string   `json:"nameEn"`
	Kind   string    `json:"kind"`
	Role   string    `json:"role"`
	Status string    `json:"status,omitempty"` // tenant status; GET /v1/me/tenants only
}

// TokenPair is the token part of the login and refresh responses (R48: camelCase, tokens in the body).
type TokenPair struct {
	AccessToken  string `json:"accessToken"`
	ExpiresIn    int    `json:"expiresIn"`
	RefreshToken string `json:"refreshToken"`
}

// axes are the rows a token is built from besides the session: memberships, platform roles, scopes and
// the linked driver (Appendix C §C.1.3, §C.4.1).
type axes struct {
	memberships []authdb.ListMembershipsRow
	platform    []string
	scopes      []authdb.ListScopesRow
	driver      *authdb.GetDriverForUserRow
}

func loadAxes(ctx context.Context, q *authdb.Queries, uid uuid.UUID) (axes, error) {
	var a axes
	var err error
	if a.memberships, err = q.ListMemberships(ctx, uid); err != nil {
		return a, err
	}
	if a.platform, err = q.ListPlatformRoles(ctx, uid); err != nil {
		return a, err
	}
	if a.scopes, err = q.ListScopes(ctx, uid); err != nil {
		return a, err
	}
	d, err := q.GetDriverForUser(ctx, uid)
	switch {
	case err == nil:
		a.driver = &d
	case !errors.Is(err, pgx.ErrNoRows):
		return a, err
	}
	return a, nil
}

// usable lists the memberships a token may carry: tenants that are not suspended.
func (a axes) usable() []authdb.ListMembershipsRow {
	out := make([]authdb.ListMembershipsRow, 0, len(a.memberships))
	for _, m := range a.memberships {
		if m.TenantStatus != "suspended" {
			out = append(out, m)
		}
	}
	return out
}

// pickTenant returns the preferred tenant when it is still a usable membership, otherwise the first
// usable one (own fleet first, then by name), or nil when the user is a member of nothing usable
// (customer-scope and platform-only principals). A removed membership therefore falls back to another
// active one on the next refresh (C.4.4).
func (a axes) pickTenant(preferred *uuid.UUID) *authdb.ListMembershipsRow {
	u := a.usable()
	if preferred != nil {
		for i := range u {
			if u[i].TenantID == *preferred {
				return &u[i]
			}
		}
	}
	if len(u) == 0 {
		return nil
	}
	return &u[0]
}

func (a axes) tenants() []Tenant {
	u := a.usable()
	out := make([]Tenant, 0, len(u))
	for _, m := range u {
		out = append(out, Tenant{ID: m.TenantID, NameTh: m.NameTh, NameEn: m.NameEn, Kind: m.Kind, Role: m.Role})
	}
	return out
}

// claims builds the private claims for a session acting in tenant m (nil: no tid / rol). A driver
// membership needs the linked drivers row of that tenant (C.1.4): otherwise 403 driver_profile_required.
func (a axes) claims(sid uuid.UUID, ver int32, amr string, m *authdb.ListMembershipsRow) (token.Claims, error) {
	c := token.Claims{SessionID: sid.String(), Version: ver, AMR: amr}
	if m != nil {
		c.TenantID, c.Role = m.TenantID.String(), m.Role
		if authz.TenantRole(m.Role) == authz.Driver {
			if a.driver == nil || a.driver.TenantID != m.TenantID {
				return c, errDriverProfileRequired()
			}
			c.DriverID = a.driver.ID.String()
		}
	}
	if len(a.platform) > 0 {
		c.Platform = slices.Clone(a.platform)
	}
	seen := map[uuid.UUID]bool{}
	for _, sc := range a.scopes {
		if sc.Kind == "dispatcher" {
			c.Dispatcher = true
		}
		if !seen[sc.BillingPartyID] {
			seen[sc.BillingPartyID] = true
			c.CustomerScopes = append(c.CustomerScopes, sc.BillingPartyID.String())
		}
	}
	return c, nil
}

// signAccess sets sub and a fresh uuidv7 jti and signs.
func (s *Service) signAccess(c token.Claims, uid uuid.UUID, now time.Time) (string, error) {
	jti, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	c.Subject, c.ID = uid.String(), jti.String()
	return s.keys.Sign(c, now)
}

func (s *Service) expiresIn() int { return int(s.keys.TTL().Seconds()) }

// newSessionInput describes a session about to be created by a login.
type newSessionInput struct {
	UserID     uuid.UUID
	Platform   string
	AMR        string
	TenantID   *uuid.UUID
	InstallID  string
	AppVersion string
	IP         string
	UserAgent  string
}

// createSession revokes an older live session of the same install (device_relogin, R83), inserts the
// session and the first refresh token of its family (family_id = session id) and records the hot-index
// writes. It returns the session id and the refresh token.
func (s *Service) createSession(ctx context.Context, q *authdb.Queries, in newSessionInput, now time.Time, eff *PostCommit) (uuid.UUID, string, error) {
	var install *string
	if isMobile(in.Platform) && in.InstallID != "" {
		install = &in.InstallID
		old, err := q.RevokeInstallSessions(ctx, authdb.RevokeInstallSessionsParams{At: now, UserID: in.UserID, InstallID: in.InstallID})
		if err != nil {
			return uuid.Nil, "", err
		}
		if len(old) > 0 {
			hashes, err := q.RevokeSessionTokens(ctx, authdb.RevokeSessionTokensParams{At: now, Reason: RevokeDeviceRelogin, SessionIds: old})
			if err != nil {
				return uuid.Nil, "", err
			}
			eff.revoked(old...)
			eff.dropHashes(hashes...)
		}
	}
	absolute := now.Add(WebAbsoluteTTL)
	if isMobile(in.Platform) {
		absolute = now.Add(s.cfg.RefreshTTLMobile)
	}
	sid, err := q.InsertSession(ctx, authdb.InsertSessionParams{
		UserID: in.UserID, Platform: in.Platform, Amr: in.AMR, ActiveTenantID: in.TenantID, InstallID: install,
		AppVersion: optional(in.AppVersion), Ip: parseIP(in.IP), UserAgent: optional(truncate(in.UserAgent, 512)),
		AbsoluteExpiresAt: absolute, CreatedAt: now,
	})
	if err != nil {
		return uuid.Nil, "", err
	}
	refresh, _, err := s.insertRefresh(ctx, q, sid, sid, in.UserID, in.Platform, now, absolute, eff)
	if err != nil {
		return uuid.Nil, "", err
	}
	return sid, refresh, nil
}

// insertRefresh issues one refresh token of a family and returns it with its row id; web tokens
// slide (REFRESH_TOKEN_TTL_WEB, capped by the session's absolute expiry), mobile tokens live until it.
func (s *Service) insertRefresh(ctx context.Context, q *authdb.Queries, sid, family, uid uuid.UUID, platform string,
	now, absolute time.Time, eff *PostCommit) (string, uuid.UUID, error) {
	tok, err := newSecret()
	if err != nil {
		return "", uuid.Nil, err
	}
	sum, hexSum, _ := secretHash(tok)
	exp := absolute
	if slide := now.Add(s.cfg.RefreshTTLWeb); !isMobile(platform) && slide.Before(absolute) {
		exp = slide
	}
	id, err := q.InsertRefreshToken(ctx, authdb.InsertRefreshTokenParams{
		SessionID: sid, FamilyID: family, TokenHash: sum, IssuedAt: now, ExpiresAt: exp,
	})
	if err != nil {
		return "", uuid.Nil, err
	}
	eff.putRefresh(hexSum, rtEntry{SessionID: sid, FamilyID: family, UserID: uid, Exp: exp})
	return tok, id, nil
}

func optional(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func parseIP(v string) *netip.Addr {
	a, err := netip.ParseAddr(v)
	if err != nil {
		return nil
	}
	return &a
}

// truncate cuts v to at most n bytes on a rune boundary (PostgreSQL rejects split UTF-8).
func truncate(v string, n int) string {
	if len(v) <= n {
		return v
	}
	for n > 0 && !utf8.RuneStart(v[n]) {
		n--
	}
	return v[:n]
}
