package auth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
)

// Me is the body of GET /v1/me (Appendix C §C.8). photoUrl stays null until the storage service (T11)
// can presign the profile photo; legacyAuthUid is added by the Firebase bridge (T08); capabilities come
// from the CapabilityResolver (T07).
type Me struct {
	ID                 uuid.UUID       `json:"id"`
	Email              *string         `json:"email"`
	DisplayName        *string         `json:"displayName"`
	PhotoURL           *string         `json:"photoUrl"`
	Tenant             *Tenant         `json:"tenant"`
	Tenants            []Tenant        `json:"tenants"`
	PlatformRoles      []string        `json:"platformRoles"`
	Dispatcher         bool            `json:"dispatcher"`
	Steward            bool            `json:"steward"`
	Driver             *DriverRef      `json:"driver"`
	CustomerScopes     []CustomerScope `json:"customerScopes"`
	Capabilities       []string        `json:"capabilities"`
	MustChangePassword bool            `json:"mustChangePassword"`
}

// DriverRef is the linked driver of a driver principal.
type DriverRef struct {
	ID uuid.UUID `json:"id"`
}

// CustomerScope is one customer-scope party of the user.
type CustomerScope struct {
	BillingPartyID uuid.UUID `json:"billingPartyId"`
	Name           string    `json:"name"`
}

// Me is GET /v1/me for the principal's active tenant.
func (s *Service) Me(ctx context.Context, p *authz.Principal) (*Me, error) {
	var out *Me
	err := s.system(ctx, func(q *authdb.Queries) error {
		u, err := q.GetUser(ctx, p.UserID)
		if err != nil {
			return err
		}
		a, err := loadAxes(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		m := &Me{
			ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Tenants: a.tenants(),
			PlatformRoles: a.platform, Dispatcher: p.Dispatcher, MustChangePassword: u.MustChangePassword,
			CustomerScopes: []CustomerScope{}, Capabilities: []string{},
		}
		if tid := p.EffectiveTenant(); tid != nil {
			for _, t := range m.Tenants {
				if t.ID == *tid {
					t.Role = string(p.TenantRole)
					m.Tenant = &t
					break
				}
			}
		}
		// Steward (R60): staff of the own-fleet tenant, or platform_admin. T07 sets the GUC from it.
		m.Steward = p.HasPlatform(authz.PlatformAdmin) ||
			(m.Tenant != nil && m.Tenant.Kind == "own_fleet" && p.TenantRole != authz.Driver)
		if p.DriverID != nil {
			m.Driver = &DriverRef{ID: *p.DriverID}
		}
		for _, sc := range a.scopes {
			if sc.Kind == "customer" {
				m.CustomerScopes = append(m.CustomerScopes, CustomerScope{BillingPartyID: sc.BillingPartyID, Name: sc.PartyName})
			}
		}
		out = m
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.caps != nil {
		caps, err := s.caps.Capabilities(ctx, p)
		if err != nil {
			return nil, err
		}
		out.Capabilities = caps
	}
	return out, nil
}

// PatchMeInput is the body of PATCH /v1/me: profile fields only; role, status and scopes are not
// self-editable (closes the self-writable users/{uid}, firestore.rules:57-60).
type PatchMeInput struct {
	DisplayName  *string `json:"displayName"`
	PhotoKey     *string `json:"photoKey"`
	LastLoginGeo *Geo    `json:"lastLoginGeo"`
}

// PatchMe updates the caller's profile and returns GET /v1/me.
func (s *Service) PatchMe(ctx context.Context, p *authz.Principal, in PatchMeInput) (*Me, error) {
	var v []httpx.FieldViolation
	if in.DisplayName != nil {
		n := strings.TrimSpace(*in.DisplayName)
		if n == "" || utf8.RuneCountInString(n) > 200 || !utf8.ValidString(n) {
			v = append(v, httpx.FieldViolation{Field: "displayName", Reason: "length", Params: map[string]any{"min": 1, "max": 200}})
		}
		in.DisplayName = &n
	}
	if in.PhotoKey != nil && (*in.PhotoKey == "" || len(*in.PhotoKey) > 1024) {
		v = append(v, httpx.FieldViolation{Field: "photoKey", Reason: "required"})
	}
	v = append(v, validateGeo("lastLoginGeo", in.LastLoginGeo)...)
	if len(v) > 0 {
		return nil, httpx.ErrInvalidArgument(v...)
	}
	err := s.system(ctx, func(q *authdb.Queries) error {
		var photo *uuid.UUID
		if in.PhotoKey != nil {
			id, err := q.FindOwnFile(ctx, authdb.FindOwnFileParams{ObjectKey: *in.PhotoKey, UserID: p.UserID})
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "photoKey", Reason: "not_found"})
			}
			if err != nil {
				return err
			}
			photo = &id
		}
		if in.DisplayName != nil || photo != nil {
			if err := q.UpdateProfile(ctx, authdb.UpdateProfileParams{DisplayName: in.DisplayName, PhotoFileID: photo, ID: p.UserID}); err != nil {
				return err
			}
		}
		if g := in.LastLoginGeo; g != nil {
			return q.UpdateLastLoginGeo(ctx, authdb.UpdateLastLoginGeoParams{
				Lat: g.Lat, Lng: g.Lng, GeoSource: &g.Source, AccuracyM: g.AccuracyM, ID: p.UserID,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Me(ctx, p)
}

// MyTenants is GET /v1/me/tenants: every active membership with the tenant status, for the switcher.
func (s *Service) MyTenants(ctx context.Context, p *authz.Principal) ([]Tenant, error) {
	var out []Tenant
	err := s.system(ctx, func(q *authdb.Queries) error {
		rows, err := q.ListMemberships(ctx, p.UserID)
		if err != nil {
			return err
		}
		out = make([]Tenant, 0, len(rows))
		for _, m := range rows {
			out = append(out, Tenant{ID: m.TenantID, NameTh: m.NameTh, NameEn: m.NameEn, Kind: m.Kind, Role: m.Role, Status: m.TenantStatus})
		}
		return nil
	})
	return out, err
}

// SessionView is one entry of GET /v1/me/sessions.
type SessionView struct {
	ID          uuid.UUID `json:"id"`
	Platform    string    `json:"platform"`
	AMR         string    `json:"amr"`
	InstallID   *string   `json:"installId"`
	DeviceLabel *string   `json:"deviceLabel"`
	AppVersion  *string   `json:"appVersion"`
	IP          *string   `json:"ip"`
	UserAgent   *string   `json:"userAgent"`
	CreatedAt   time.Time `json:"createdAt"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
	Current     bool      `json:"current"`
}

// MySessions is GET /v1/me/sessions: the caller's live sessions, most recently used first.
func (s *Service) MySessions(ctx context.Context, p *authz.Principal) ([]SessionView, error) {
	var out []SessionView
	err := s.system(ctx, func(q *authdb.Queries) error {
		rows, err := q.ListLiveSessions(ctx, authdb.ListLiveSessionsParams{UserID: p.UserID, Now: s.clock()})
		if err != nil {
			return err
		}
		out = make([]SessionView, 0, len(rows))
		for _, r := range rows {
			out = append(out, SessionView{
				ID: r.ID, Platform: r.Platform, AMR: r.Amr, InstallID: r.InstallID, DeviceLabel: r.DeviceLabel,
				AppVersion: r.AppVersion, IP: optional(r.Ip), UserAgent: r.UserAgent, CreatedAt: r.CreatedAt,
				LastSeenAt: r.LastSeenAt, Current: r.ID == p.SessionID,
			})
		}
		return nil
	})
	return out, err
}

// RevokeMySession is DELETE /v1/me/sessions/{sid}: one own live session ends (reason logout); another
// user's or an unknown session is 404.
func (s *Service) RevokeMySession(ctx context.Context, p *authz.Principal, sid uuid.UUID, requestID string) error {
	sids, err := s.Revoke(ctx, Revocation{UserID: p.UserID, Reason: RevokeLogout, SessionIDs: []uuid.UUID{sid}, RequestID: requestID})
	if err != nil {
		return err
	}
	if len(sids) == 0 {
		return httpx.ErrNotFound()
	}
	return nil
}

// LogoutInput is the body of POST /v1/auth/logout.
type LogoutInput struct {
	RefreshToken string `json:"refreshToken"`
	InstallID    string `json:"installId"`

	RequestID string `json:"-"`
}

// Logout is POST /v1/auth/logout: it ends the session of the bearer and the session of refreshToken
// (both when they belong to the same user), and deletes the push token of the install (body installId,
// else the session's). A web logout with an expired access token still works through the refresh
// token the BFF holds. Without any valid credential it is 401 unauthenticated.
func (s *Service) Logout(ctx context.Context, p *authz.Principal, in LogoutInput) error {
	pc := newPostCommit()
	err := s.systemTx(ctx, func(tx pgx.Tx, q *authdb.Queries) error {
		var uid uuid.UUID
		var sids []uuid.UUID
		install := in.InstallID
		if p != nil {
			uid, sids = p.UserID, []uuid.UUID{p.SessionID}
		}
		// Lock order users -> sessions -> refresh_tokens (C.4.4): resolve the refresh token's user
		// without a lock, lock the user, and only then the token and its session. Another user's token
		// next to a bearer is ignored and never locked.
		sum, _, haveToken := secretHash(in.RefreshToken)
		if haveToken {
			tuid, err := q.RefreshTokenUser(ctx, sum)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				haveToken = false
			case err != nil:
				return err
			case p == nil || tuid == p.UserID:
				uid = tuid
			default:
				haveToken = false
			}
		}
		if uid == uuid.Nil {
			return httpx.ErrUnauthenticated()
		}
		if _, err := q.LockUser(ctx, uid); err != nil {
			return err
		}
		if haveToken {
			rt, err := q.LockRefreshToken(ctx, sum)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return err
			case rt.UserID == uid:
				if !slices.Contains(sids, rt.SessionID) {
					sids = append(sids, rt.SessionID)
				}
				if install == "" && rt.InstallID != nil {
					install = *rt.InstallID
				}
			}
		}
		if len(sids) == 0 {
			return httpx.ErrUnauthenticated() // the token vanished (clean-up) between the two reads
		}
		if install == "" && p != nil {
			sess, err := q.GetSession(ctx, p.SessionID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil && sess.InstallID != nil {
				install = *sess.InstallID
			}
		}
		if _, err := s.revokeTx(ctx, tx, Revocation{UserID: uid, Reason: RevokeLogout, SessionIDs: sids, RequestID: in.RequestID}, s.clock(), pc); err != nil {
			return err
		}
		if install != "" {
			return q.DeleteDeviceToken(ctx, authdb.DeleteDeviceTokenParams{UserID: uid, InstallID: install})
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.Apply(ctx, pc)
	return nil
}

// LogoutAll is POST /v1/auth/logout-all: every own session ends (reason logout_all) and auth_version
// bumps; no security event (C.4.7).
func (s *Service) LogoutAll(ctx context.Context, p *authz.Principal, requestID string) error {
	_, err := s.Revoke(ctx, Revocation{UserID: p.UserID, Reason: RevokeLogoutAll, BumpVersion: true, RequestID: requestID})
	return err
}

// AccessToken is the body of POST /v1/auth/tenant.
type AccessToken struct {
	AccessToken string `json:"accessToken"`
	ExpiresIn   int    `json:"expiresIn"`
}

// SwitchTenant is POST /v1/auth/tenant: the session's active tenant becomes tenantID (an active
// membership in a tenant that is not suspended, else 403 permission_denied) and a new access token
// carries it; every later refresh re-issues it as tid (R83). The refresh token is unchanged. Locks the
// user, then the session (lock order of C.4.4).
func (s *Service) SwitchTenant(ctx context.Context, p *authz.Principal, tenantID uuid.UUID) (*AccessToken, error) {
	var out *AccessToken
	pc := newPostCommit()
	err := s.system(ctx, func(q *authdb.Queries) error {
		now := s.clock()
		u, err := q.LockUser(ctx, p.UserID)
		if err != nil {
			return err
		}
		// Read after the user lock: a revocation that held it has committed and is visible here.
		sess, err := q.GetSession(ctx, p.SessionID)
		if err != nil {
			return err
		}
		if sess.RevokedAt != nil || u.Status != "active" {
			return errSessionRevoked()
		}
		a, err := loadAxes(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		m := a.pickTenant(&tenantID)
		if m == nil || m.TenantID != tenantID {
			return errPermissionDenied("not an active member of that tenant").WithDetails(map[string]any{"tenantId": tenantID})
		}
		c, err := a.claims(p.SessionID, u.AuthVersion, sess.Amr, m)
		if err != nil {
			return err
		}
		if err := q.SetActiveTenant(ctx, authdb.SetActiveTenantParams{TenantID: &m.TenantID, ID: p.SessionID}); err != nil {
			return err
		}
		access, err := s.signAccess(c, p.UserID, now)
		if err != nil {
			return err
		}
		pc.issue(p.UserID, u.AuthVersion)
		out = &AccessToken{AccessToken: access, ExpiresIn: s.expiresIn()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.Apply(ctx, pc)
	return out, nil
}

// SSETicket is the body of POST /v1/auth/sse-ticket.
type SSETicket struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expiresIn"`
}

type sseTicket struct {
	UserID    uuid.UUID `json:"userId"`
	SessionID uuid.UUID `json:"sessionId"`
}

// IssueSSETicket is POST /v1/auth/sse-ticket (driver app only, C.4.6): 403 unless the session platform
// is android or ios; the ticket is single use and lives 60 s (auth:sse:{ticket}).
func (s *Service) IssueSSETicket(ctx context.Context, p *authz.Principal) (*SSETicket, error) {
	var platform string
	if err := s.system(ctx, func(q *authdb.Queries) error {
		sess, err := q.GetSession(ctx, p.SessionID)
		platform = sess.Platform
		return err
	}); err != nil {
		return nil, err
	}
	if !isMobile(platform) {
		return nil, errPermissionDenied("SSE tickets are issued to driver-app sessions only")
	}
	if err := s.limit(ctx, ratelimit.SSETicket, p.SessionID.String()); err != nil {
		return nil, err
	}
	t, err := newSecret()
	if err != nil {
		return nil, err
	}
	if err := s.store.putTicket(ctx, "sse", t, sseTicket{UserID: p.UserID, SessionID: p.SessionID}, SSETicketTTL); err != nil {
		return nil, httpx.ErrUnavailable("SSE ticket store unavailable").Wrap(err)
	}
	return &SSETicket{Ticket: t, ExpiresIn: int(SSETicketTTL.Seconds())}, nil
}

// ConsumeSSETicket redeems a ticket once (GETDEL) for GET /v1/mobile/events (T12). ok is false for an
// unknown, expired or already used ticket; the stream must still check that the session is live.
func (s *Service) ConsumeSSETicket(ctx context.Context, ticket string) (userID, sessionID uuid.UUID, ok bool, err error) {
	if _, _, valid := secretHash(ticket); !valid {
		return uuid.Nil, uuid.Nil, false, nil
	}
	var t sseTicket
	ok, err = s.store.takeTicket(ctx, "sse", ticket, &t)
	return t.UserID, t.SessionID, ok, err
}
