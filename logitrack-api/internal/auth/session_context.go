package auth

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/token"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

const (
	AccessModeMember  = "member"
	AccessModeSupport = "support"
)

var supportSwitchBucket = ratelimit.Bucket{Name: "support_switch", Default: ratelimit.Limit{Count: 10, Window: time.Minute}}

type sessionContext struct {
	mode                 string
	version              int64
	tenant, returnTenant *uuid.UUID
	platform, amr        string
	expires              time.Time
	revoked              *time.Time
}

// The user lock precedes this session lock on every context transition.
func readSessionContext(ctx context.Context, tx pgx.Tx, uid, sid uuid.UUID, lock bool) (sessionContext, error) {
	var st sessionContext
	sql := supportReadSessionContextSQL
	if lock {
		sql = supportLockSessionContextSQL
	}
	err := tx.QueryRow(ctx, sql, sid, uid).Scan(&st.mode, &st.version, &st.tenant, &st.returnTenant, &st.platform, &st.amr, &st.expires, &st.revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, errInvalidToken()
	}
	return st, err
}

func equalTenant(a, b *uuid.UUID) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// checkContext deliberately has no negative cache: even old member tokens read live state.
func (s *Service) checkContext(ctx context.Context, p *authz.Principal) error {
	return s.systemTx(ctx, func(tx pgx.Tx, q *authdb.Queries) error {
		st, err := readSessionContext(ctx, tx, p.UserID, p.SessionID, false)
		if err != nil {
			return err
		}
		if st.revoked != nil {
			// Live context admission can reject before the cache pipeline runs.
			// Repair a lost post-commit marker from this authoritative read too.
			if err := s.store.markRevoked(ctx, s.keys.TTL()+token.Leeway, p.SessionID); err != nil {
				s.log.Warn().Err(err).Msg("auth: rebuild revoked marker")
			}
			return errSessionRevoked()
		}
		if !st.expires.After(s.clock()) {
			return errSessionRevoked()
		}
		var status string
		var ver int32
		if err := tx.QueryRow(ctx, supportReadUserStateSQL, p.UserID).Scan(&status, &ver); err != nil {
			return err
		}
		if status != "active" {
			return errSessionRevoked()
		}
		if p.AuthVersion < ver {
			return errTokenExpired(ReasonClaimsChanged)
		}
		if p.AuthVersion > ver {
			return errInvalidToken()
		}
		mode := p.AccessMode
		if mode == "" {
			mode = AccessModeMember
		}
		if st.version != p.ContextVersion || st.mode != mode || !equalTenant(st.tenant, p.TenantID) {
			return errTokenExpired(ReasonClaimsChanged)
		}
		a, err := loadAxes(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		if st.mode == AccessModeSupport {
			if st.platform != PlatformWeb || !containsAdmin(a.platform) || p.TenantRole != authz.SupportReadOnly {
				return errTokenExpired(ReasonClaimsChanged)
			}
			var enabled bool
			var version int64
			if err := tx.QueryRow(ctx, supportReadTenantConsentSQL, st.tenant).Scan(&p.TenantKind, &enabled, &version); err != nil {
				return err
			}
			if version != p.SupportAccessVersion {
				return errTokenExpired(ReasonClaimsChanged)
			}
			var visitID uuid.UUID
			if err := tx.QueryRow(ctx, supportReadOpenVisitIDSQL, p.SessionID).Scan(&visitID); err != nil {
				return errTokenExpired(ReasonClaimsChanged)
			}
			p.SupportAccessEnabled, p.SupportVisitID = enabled, visitID
		} else if st.tenant != nil {
			m := a.pickTenant(st.tenant)
			if m == nil || m.TenantID != *st.tenant || m.Role != string(p.TenantRole) {
				return errTokenExpired(ReasonClaimsChanged)
			}
		} else if p.TenantRole != "" {
			return errTokenExpired(ReasonClaimsChanged)
		}
		return nil
	})
}

func containsAdmin(roles []string) bool {
	for _, r := range roles {
		if r == string(authz.PlatformAdmin) {
			return true
		}
	}
	return false
}

func installSupportAudience(c fiber.Ctx, p *authz.Principal) {
	if !authz.IsSupport(p) {
		return
	}
	a := httpx.ProjectionAudience{Support: true, Platform: p.IsPlatform(), Actor: p.UserID.String(), Session: p.SessionID.String(),
		ContextVersion: strconv.FormatInt(p.ContextVersion, 10), ConsentVersion: strconv.FormatInt(p.SupportAccessVersion, 10), Consents: map[string]httpx.Consent{}}
	if p.TenantID != nil {
		a.Tenant = p.TenantID.String()
		a.Consents[a.Tenant] = httpx.Consent{Enabled: p.SupportAccessEnabled, Version: a.ConsentVersion}
	}
	httpx.SetProjectionAudience(c, a)
}

func writeSessionContext(ctx context.Context, tx pgx.Tx, sid uuid.UUID, st *sessionContext, mode string, tenant, ret *uuid.UUID) error {
	if st.mode == mode && equalTenant(st.tenant, tenant) {
		return nil
	}
	err := tx.QueryRow(ctx, supportWriteSessionContextSQL, sid, mode, tenant, ret).Scan(&st.version)
	if err == nil {
		st.mode, st.tenant, st.returnTenant = mode, tenant, ret
	}
	return err
}

func appendVisitEvent(ctx context.Context, tx pgx.Tx, v db.SupportVisit, entered bool, requestID string) error {
	name, summary, at := "platform_support_left", "platform support visit ended", v.EndedAt
	details := map[string]any{"visitId": v.ID, "sessionId": v.SessionID, "mode": AccessModeSupport, "outcome": "accepted"}
	if entered {
		name, summary, at = "platform_support_entered", "platform support visit started", &v.StartedAt
		details["consentVersion"] = strconv.FormatInt(v.ConsentVersionAtEntry, 10)
		var enabled bool
		if err := tx.QueryRow(ctx, supportReadConsentEnabledSQL, v.TenantID).Scan(&enabled); err != nil {
			return err
		}
		details["consentEnabled"] = enabled
	} else {
		details["reason"], details["endedAt"], details["recordedAt"] = v.EndReason, v.EndedAt, v.EndRecordedAt
		details["durationSeconds"] = int64(v.EndedAt.Sub(v.StartedAt).Seconds())
	}
	return security.Append(ctx, tx, security.Event{EventType: name, Severity: security.SeverityInfo, Summary: summary, ActorUserID: &v.ActorUserID, TenantID: &v.TenantID, RequestID: requestID, OccurredAt: *at, Details: details})
}

// CloseSupportVisit appends an exit only for the transaction that actually closed it.
func CloseSupportVisit(ctx context.Context, tx pgx.Tx, sid uuid.UUID, reason, requestID string, effectiveAt, recordedAt time.Time) error {
	v, err := db.CloseSupportVisit(ctx, tx, sid, reason, effectiveAt, recordedAt)
	if err != nil || v == nil {
		return err
	}
	return appendVisitEvent(ctx, tx, *v, false, requestID)
}

// contextClaims preserves support across refresh and restores a real membership after role loss.
func (s *Service) contextClaims(ctx context.Context, tx pgx.Tx, q *authdb.Queries, uid, sid uuid.UUID, ver int32, a axes, st *sessionContext, lastTenant *uuid.UUID, now time.Time) (token.Claims, error) {
	validSupport := st.mode == AccessModeSupport && containsAdmin(a.platform) && st.platform == PlatformWeb
	if validSupport {
		if _, err := db.OpenSupportVisit(ctx, tx, sid); errors.Is(err, pgx.ErrNoRows) {
			validSupport = false
		} else if err != nil {
			return token.Claims{}, err
		}
	}
	if validSupport {
		var version int64
		if err := tx.QueryRow(ctx, supportReadConsentVersionSQL, st.tenant).Scan(&version); err != nil {
			return token.Claims{}, err
		}
		return token.Claims{SessionID: sid.String(), Version: ver, AMR: st.amr, TenantID: st.tenant.String(), Role: string(authz.SupportReadOnly), Platform: a.platform,
			AccessMode: AccessModeSupport, ContextVersion: strconv.FormatInt(st.version, 10), SupportAccessVersion: strconv.FormatInt(version, 10)}, nil
	}
	preferred := st.tenant
	if st.mode == AccessModeSupport {
		effective, reason := now, db.SupportVisitRoleRemoved
		if st.expires.Before(effective) {
			effective, reason = st.expires, db.SupportVisitExpired
		}
		if err := CloseSupportVisit(ctx, tx, sid, reason, "", effective, now); err != nil {
			return token.Claims{}, err
		}
		preferred = st.returnTenant
		if preferred == nil || !sameTenant(a.pickTenant(preferred), preferred) {
			preferred = lastTenant
		}
	}
	m := a.pickTenant(preferred)
	if err := writeSessionContext(ctx, tx, sid, st, AccessModeMember, tenantOf(m), nil); err != nil {
		return token.Claims{}, err
	}
	c, err := a.claims(sid, ver, st.amr, m)
	c.ContextVersion = strconv.FormatInt(st.version, 10)
	return c, err
}

// ChangeTenantContext is the session operation behind POST /v1/auth/tenant.
func (s *Service) ChangeTenantContext(ctx context.Context, p *authz.Principal, mode string, tenantID *uuid.UUID, requestID string) (*AccessToken, error) {
	if p == nil || p.IsMachine() || p.AMR == AMRFirebase || p.SessionID == uuid.Nil {
		return nil, errPermissionDenied("a human Go session is required")
	}
	if mode == AccessModeSupport {
		if !s.cfg.SupportEntryEnabled || s.fb.Mode.Web() {
			return nil, httpx.ErrSupportViewUnavailable()
		}
		if err := s.limit(ctx, supportSwitchBucket, p.UserID.String()); err != nil {
			return nil, err
		}
	}
	var out *AccessToken
	pc := newPostCommit()
	err := s.systemTx(ctx, func(tx pgx.Tx, q *authdb.Queries) error {
		now := s.clock()
		u, err := q.LockUser(ctx, p.UserID)
		if err != nil {
			return err
		}
		st, err := readSessionContext(ctx, tx, p.UserID, p.SessionID, true)
		if err != nil {
			return err
		}
		if st.revoked != nil || u.Status != "active" || !st.expires.After(now) {
			return errSessionRevoked()
		}
		a, err := loadAxes(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		lastTenant, err := q.LastActiveTenant(ctx, p.UserID)
		if err != nil {
			return err
		}
		var claims token.Claims
		switch mode {
		case AccessModeSupport:
			if st.platform != PlatformWeb || !containsAdmin(a.platform) {
				return errPermissionDenied("support entry needs a web platform_admin session")
			}
			if tenantID == nil {
				return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "tenantId", Reason: "required"})
			}
			var consentVersion int64
			err := tx.QueryRow(ctx, supportLockConsentVersionSQL, tenantID).Scan(&consentVersion)
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.ErrNotFound()
			}
			if err != nil {
				return err
			}
			if st.mode != mode || !equalTenant(st.tenant, tenantID) {
				ret := st.returnTenant
				if st.mode == AccessModeMember {
					ret = st.tenant
				}
				if err := CloseSupportVisit(ctx, tx, p.SessionID, db.SupportVisitSwitch, requestID, now, now); err != nil {
					return err
				}
				if err := writeSessionContext(ctx, tx, p.SessionID, &st, mode, tenantID, ret); err != nil {
					return err
				}
				v, err := db.CreateSupportVisit(ctx, tx, p.SessionID, p.UserID, *tenantID, now, consentVersion)
				if err != nil {
					return err
				}
				if err := appendVisitEvent(ctx, tx, v, true, requestID); err != nil {
					return err
				}
			}
		case AccessModeMember, "leave":
			if mode == "leave" && st.mode != AccessModeSupport {
				break
			}
			preferred := tenantID
			reason := db.SupportVisitSwitch
			if mode == "leave" {
				reason, preferred = db.SupportVisitLeave, st.returnTenant
				if preferred == nil || !sameTenant(a.pickTenant(preferred), preferred) {
					preferred = lastTenant
				}
			}
			m := a.pickTenant(preferred)
			if mode == AccessModeMember && (tenantID == nil || !sameTenant(m, tenantID)) {
				return errPermissionDenied("not an active member of that tenant")
			}
			if err := CloseSupportVisit(ctx, tx, p.SessionID, reason, requestID, now, now); err != nil {
				return err
			}
			changed := st.mode != AccessModeMember || !equalTenant(st.tenant, tenantOf(m))
			if err := writeSessionContext(ctx, tx, p.SessionID, &st, AccessModeMember, tenantOf(m), nil); err != nil {
				return err
			}
			if mode == AccessModeMember && m != nil {
				if err := q.SetLastTenant(ctx, authdb.SetLastTenantParams{TenantID: m.TenantID, ID: p.UserID}); err != nil {
					return err
				}
				if changed {
					if err := security.Append(ctx, tx, security.Event{EventType: EventTenantSwitched, Severity: security.SeverityInfo, Summary: "active tenant switched", ActorUserID: &p.UserID, TargetUserID: &p.UserID, TenantID: &m.TenantID, RequestID: requestID, OccurredAt: now, Details: map[string]any{"tenantId": m.TenantID, "role": m.Role, "sessionId": p.SessionID}}); err != nil {
						return err
					}
				}
			}
		default:
			return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "mode", Reason: "enum"})
		}
		claims, err = s.contextClaims(ctx, tx, q, p.UserID, p.SessionID, u.AuthVersion, a, &st, lastTenant, now)
		if err != nil {
			return err
		}
		access, err := s.signAccess(claims, p.UserID, now)
		if err != nil {
			return err
		}
		pc.issue(p.UserID, u.AuthVersion)
		out = &AccessToken{AccessToken: access, ExpiresIn: s.expiresIn()}
		return nil
	})
	if err != nil {
		if mode == AccessModeSupport {
			if auditErr := s.supportEntryDenied(ctx, p, tenantID, requestID, err); auditErr != nil {
				return nil, auditErr
			}
		}
		return nil, err
	}
	s.Apply(ctx, pc)
	return out, nil
}

// Role removal ends all affected contexts in the same transaction as the grant change.
func (s *Service) endSupportAfterRoleRemoval(ctx context.Context, tx pgx.Tx, q *authdb.Queries, uid uuid.UUID, now time.Time) error {
	a, err := loadAxes(ctx, q, uid)
	if err != nil || containsAdmin(a.platform) {
		return err
	}
	u, err := q.GetUser(ctx, uid)
	if err != nil {
		return err
	}
	lastTenant, err := q.LastActiveTenant(ctx, uid)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, supportLockUserSessionsSQL, uid)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, sid := range ids {
		st, err := readSessionContext(ctx, tx, uid, sid, true)
		if err != nil {
			return err
		}
		if _, err := s.contextClaims(ctx, tx, q, uid, sid, u.AuthVersion, a, &st, lastTenant, now); err != nil {
			return err
		}
	}
	return nil
}

// finalizeSupportAudience rereads live identity/context without locking them. Ordered tenant
// FOR SHARE locks serialize disclosure with consent changes, through JSON serialization.
func (s *Service) finalizeSupportAudience(p *authz.Principal) func(context.Context, []string, func(map[string]httpx.Consent) error) error {
	return func(ctx context.Context, owners []string, fn func(map[string]httpx.Consent) error) error {
		if len(owners) > 100 {
			return httpx.ErrSupportViewUnavailable()
		}
		sort.Strings(owners)
		return s.systemTx(ctx, func(tx pgx.Tx, q *authdb.Queries) error {
			u, err := q.GetUser(ctx, p.UserID)
			if err != nil {
				return err
			}
			st, err := readSessionContext(ctx, tx, p.UserID, p.SessionID, false)
			if err != nil {
				return err
			}
			if u.Status != "active" || u.AuthVersion != p.AuthVersion || st.revoked != nil || !st.expires.After(s.clock()) || st.mode != AccessModeSupport || st.version != p.ContextVersion || !equalTenant(st.tenant, p.TenantID) {
				return errTokenExpired(ReasonClaimsChanged)
			}
			a, err := loadAxes(ctx, q, p.UserID)
			if err != nil {
				return err
			}
			if !containsAdmin(a.platform) {
				return errTokenExpired(ReasonClaimsChanged)
			}
			consents := map[string]httpx.Consent{}
			for _, owner := range owners {
				id, err := uuid.Parse(owner)
				if err != nil {
					return httpx.ErrSupportViewUnavailable()
				}
				var c httpx.Consent
				var version int64
				if err := tx.QueryRow(ctx, supportLockTenantConsentSQL, id).Scan(&c.Enabled, &version); err != nil {
					return err
				}
				c.Version = strconv.FormatInt(version, 10)
				consents[owner] = c
				if expected, ok := p.SupportLabelVersions[owner]; ok && expected != version {
					return errTokenExpired(ReasonClaimsChanged)
				}
			}
			if c, ok := consents[p.TenantID.String()]; ok && c.Version != strconv.FormatInt(p.SupportAccessVersion, 10) {
				return errTokenExpired(ReasonClaimsChanged)
			}
			return fn(consents)
		})
	}
}

// No existing tenant code has reviewed non-person provenance, regardless of legal type.
func supportTenantLabel(ctx context.Context, tx pgx.Tx, p *authz.Principal, id uuid.UUID, role string) (Tenant, error) {
	t := Tenant{ID: id, Role: role, Reference: "TEN-" + id.String()}
	var personal, enabled bool
	var version int64
	err := tx.QueryRow(ctx, supportReadTenantLabelSQL, id).
		Scan(&t.NameTh, &t.NameEn, &t.Kind, &t.Status, &t.Code, &personal, &enabled, &version)
	if err != nil {
		return t, err
	}
	if p.SupportLabelVersions == nil {
		p.SupportLabelVersions = map[string]int64{}
	}
	p.SupportLabelVersions[id.String()] = version
	if personal && !enabled {
		masked := "****"
		t.NameTh, t.NameEn = masked, &masked
		t.redacted = append(t.redacted, httpx.RedactedField{Path: "nameTh", Kind: httpx.Person}, httpx.RedactedField{Path: "nameEn", Kind: httpx.Person})
	}
	if !enabled {
		t.Code = nil
		t.redacted = append(t.redacted, httpx.RedactedField{Path: "code", Kind: httpx.Person})
	}
	return t, nil
}

func (s *Service) supportEntryDenied(ctx context.Context, p *authz.Principal, target *uuid.UUID, requestID string, cause error) error {
	reason := "unavailable"
	if he, ok := errors.AsType[*httpx.Error](cause); ok {
		reason = he.Code
	}
	err := s.systemTx(ctx, func(tx pgx.Tx, _ *authdb.Queries) error {
		var tenant *uuid.UUID
		if target != nil {
			var id uuid.UUID
			err := tx.QueryRow(ctx, supportReadEligibleTenantSQL, target).Scan(&id)
			if err == nil {
				tenant = &id
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		return security.Append(ctx, tx, security.Event{EventType: "platform_support_denied", Severity: security.SeverityWarning, Summary: "platform support entry refused", ActorUserID: &p.UserID, TenantID: tenant, RequestID: requestID, OccurredAt: s.clock(), Details: map[string]any{"route": "/v1/auth/tenant", "operation": "enter", "reason": reason, "outcome": "refused"}})
	})
	if err != nil {
		return httpx.ErrUnavailable("support refusal could not be audited").Wrap(err)
	}
	return nil
}
