package iam

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/apikey"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// Security event types of the API keys administration (Appendix C §C.4.13), each in the transaction of its
// change.
const (
	EventAPIKeyCreated = "api_key_created"
	EventAPIKeyRevoked = "api_key_revoked"
)

// details.reason values of the API keys administration.
const (
	ReasonAPIKeysOff        = "api_keys_off"        // 503: API_KEY_PEPPER is unset
	ReasonPlatformAdminOnly = "platform_admin_only" // 403: a platform key (tenantId null)
	ReasonTenantOutOfReach  = "tenant_out_of_reach" // 403: a key of another tenant
	ReasonCapabilityNotHeld = "capability_not_held" // 403: granting a capability the caller lacks
	ReasonTenantInactive    = "tenant_inactive"     // 409: the key's tenant is pending or suspended
	ReasonMachineCaller     = "machine_not_allowed" // 403: an API key cannot manage keys
)

const (
	maxAPIKeyName          = 100 // runes
	prefixCollisionRetries = 3   // a 40-bit prefix collides about never; retried anyway
	prefixConstraint       = "api_keys_key_prefix_key"
)

// APIKeyScopes are the values of the CHECK on api_keys.scope (R82), in that order.
var APIKeyScopes = []authz.APIKeyScope{authz.KeyIntegration, authz.KeyScript, authz.KeyCFShim, authz.KeyReleasePublisher}

// APIKey is one key of GET /v1/api-keys and of the POST response (Appendix B §B.2.6). The secret and its hash
// are never part of it.
type APIKey struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	Scope          string     `json:"scope"`
	KeyPrefix      string     `json:"keyPrefix"`
	Capabilities   []string   `json:"capabilities"`
	TenantID       *uuid.UUID `json:"tenantId"` // null = platform key
	TenantNameTh   *string    `json:"tenantNameTh"`
	TenantNameEn   *string    `json:"tenantNameEn"`
	RatePerMin     int32      `json:"ratePerMin"`
	Status         string     `json:"status"` // active | expired | revoked
	CreatedBy      uuid.UUID  `json:"createdBy"`
	CreatedByEmail string     `json:"createdByEmail"`
	CreatedAt      time.Time  `json:"createdAt"`
	ExpiresAt      *time.Time `json:"expiresAt"`
	LastUsedAt     *time.Time `json:"lastUsedAt"`
	RevokedAt      *time.Time `json:"revokedAt"`
	RevokedBy      *uuid.UUID `json:"revokedBy"`
}

// CreatedAPIKey is the body of POST /v1/api-keys: the key and its secret, shown this once (R49).
type CreatedAPIKey struct {
	APIKey
	Secret string `json:"secret"`
}

// CreateAPIKeyInput is the body of POST /v1/api-keys.
type CreateAPIKeyInput struct {
	Name         string   `json:"name"`
	Scope        string   `json:"scope"`
	Capabilities []string `json:"capabilities"`
	ExpiresAt    *string  `json:"expiresAt"` // RFC 3339, in the future; null = no expiry
	TenantID     *string  `json:"tenantId"`  // null or absent = platform key (platform_admin only)
}

// APIKeys is the API keys administration (T32 slice T32K, Appendix B §B.2.6, Appendix C §C.4.11, §C.8): GET,
// POST and DELETE /v1/api-keys on the internal listener behind security:manage_api_keys. The secret is shown
// once and stored as sha256(secret ‖ API_KEY_PEPPER) (internal/auth/apikey); internal/auth verifies it per
// request, so a revocation is effective on the next request.
//
// Reach. A tenant caller (tenant_admin, or a role an override grants the key) manages the keys of its effective
// tenant; a platform_admin manages every key and creates the platform keys (tenant_id NULL; the only ones that
// may hold global and mobile:* capabilities, and the only form of the cf_shim and release_publisher scopes).
// Without X-Act-On-Tenant a platform_admin lists the platform keys and those of its own tenant; with
// X-Act-On-Tenant: <uuid> that tenant's, with * every key. Keys are managed by people only: an API key never
// reaches these routes (created_by and revoked_by name a users row).
type APIKeys struct {
	pool   db.Beginner
	pepper apikey.Pepper
	log    zerolog.Logger
	now    func() time.Time
}

// APIKeysDeps are the collaborators of the API keys administration.
type APIKeysDeps struct {
	Pool   db.Beginner   // the logitrack_app pool; api_keys is read and written in db.WithSystem
	Pepper apikey.Pepper // API_KEY_PEPPER; unset = no key can be created (503)
	Log    zerolog.Logger
	Now    func() time.Time // optional, defaults to time.Now
}

// NewAPIKeys builds the administration.
func NewAPIKeys(d APIKeysDeps) (*APIKeys, error) {
	if d.Pool == nil {
		return nil, errors.New("iam: the API keys administration needs a pool")
	}
	k := &APIKeys{pool: d.Pool, pepper: d.Pepper, log: d.Log, now: d.Now}
	if k.now == nil {
		k.now = time.Now
	}
	return k, nil
}

func (k *APIKeys) clock() time.Time { return k.now().UTC().Truncate(time.Microsecond) }

func (k *APIKeys) read(ctx context.Context, fn func(q *iamdb.Queries) error) error {
	return mapDBError(db.WithSystem(ctx, k.pool, nil, func(tx pgx.Tx) error { return fn(iamdb.New(tx)) }))
}

// keyStatus derives the status of a key at now.
func keyStatus(revoked, expires *time.Time, now time.Time) string {
	switch {
	case revoked != nil:
		return "revoked"
	case expires != nil && !now.Before(*expires):
		return "expired"
	}
	return "active"
}

func apiKeyOf(r iamdb.ListAPIKeysRow, now time.Time) APIKey {
	caps := r.Capabilities
	if caps == nil {
		caps = []string{}
	}
	return APIKey{ID: r.ID, Name: r.Name, Scope: r.Scope, KeyPrefix: r.KeyPrefix, Capabilities: caps, TenantID: r.TenantID,
		TenantNameTh: r.TenantNameTh, TenantNameEn: r.TenantNameEn, RatePerMin: r.RatePerMin,
		Status: keyStatus(r.RevokedAt, r.ExpiresAt, now), CreatedBy: r.CreatedBy, CreatedByEmail: r.CreatedByEmail,
		CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, LastUsedAt: r.LastUsedAt, RevokedAt: r.RevokedAt, RevokedBy: r.RevokedBy}
}

// keyReach is the set of keys a request lists: every key under X-Act-On-Tenant: *; the platform keys for a
// platform_admin not acting as a tenant; the keys of the effective tenant.
type keyReach struct {
	all      bool
	platform bool
	tenants  []uuid.UUID
}

func listReach(p *authz.Principal) keyReach {
	if p.ActOnAll {
		return keyReach{all: true}
	}
	r := keyReach{platform: p.HasPlatform(authz.PlatformAdmin) && p.ActOnTenant == nil, tenants: []uuid.UUID{}}
	if t := p.EffectiveTenant(); t != nil {
		r.tenants = append(r.tenants, *t)
	}
	return r
}

// mayManage reports whether p writes (creates or revokes) a key of tenant (nil = a platform key): a
// platform_admin every key, anyone else the keys of its effective tenant.
func mayManage(p *authz.Principal, tenant *uuid.UUID) bool {
	if p.IsMachine() {
		return false
	}
	if p.HasPlatform(authz.PlatformAdmin) {
		return true
	}
	eff := p.EffectiveTenant()
	return tenant != nil && eff != nil && *eff == *tenant
}

// List is GET /v1/api-keys, newest first (created_at DESC, id DESC), keyset paged (Appendix B §B.1.5).
func (k *APIKeys) List(c call, limit int, cur string) ([]APIKey, string, error) {
	var bad []httpx.FieldViolation
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		bad = append(bad, violation("limit", "out_of_range"))
	}
	var pos cursor
	if cur != "" {
		var ok bool
		if pos, ok = decodeCursor(cur, false); !ok {
			bad = append(bad, violation("cursor", "invalid"))
		}
	}
	if len(bad) > 0 {
		return nil, "", errInvalid(bad...)
	}
	r := listReach(c.p)
	now := k.clock()
	var out []APIKey
	var next string
	err := k.read(c.ctx, func(q *iamdb.Queries) error {
		rows, err := q.ListAPIKeys(c.ctx, iamdb.ListAPIKeysParams{AllKeys: r.all, PlatformKeys: r.platform,
			TenantIds: r.tenants, AfterCreated: pos.T, AfterID: pos.ID, RowLimit: int32(limit) + 1})
		if err != nil {
			return err
		}
		if len(rows) > limit {
			rows = rows[:limit]
			last := rows[limit-1]
			t := last.CreatedAt
			next = encodeCursor(&t, last.ID)
		}
		out = make([]APIKey, 0, len(rows))
		for _, row := range rows {
			out = append(out, apiKeyOf(row, now))
		}
		return nil
	})
	return out, next, err
}

// apiKeyRequest is a validated POST body.
type apiKeyRequest struct {
	name    string
	scope   authz.APIKeyScope
	caps    []authz.Cap
	tenant  *uuid.UUID
	expires *time.Time
}

// validateCreate checks the body of POST /v1/api-keys (422 invalid_argument, Appendix C §C.4.11):
//
//   - name: 1 to 100 characters; scope: one of the CHECK list (R82);
//   - capabilities: at least one, every one a catalog key, never platform:* or dispatch:*; a tenant key holds
//     tenant-class keys only, a platform key global-class and mobile:* keys only;
//   - cf_shim and release_publisher are platform keys (tenantId null); cf_shim holds only mobile:* keys,
//     release_publisher exactly security:manage_mobile_release;
//   - tenantId: a uuid or null; expiresAt: RFC 3339 in the future, or null.
func validateCreate(in CreateAPIKeyInput, now time.Time) (apiKeyRequest, []httpx.FieldViolation) {
	var bad []httpx.FieldViolation
	var r apiKeyRequest
	r.name = strings.TrimSpace(in.Name)
	switch n := utf8.RuneCountInString(r.name); {
	case n == 0:
		bad = append(bad, violation("name", "required"))
	case n > maxAPIKeyName:
		bad = append(bad, violation("name", "too_long"))
	}
	r.scope = authz.APIKeyScope(in.Scope)
	scopeOK := slices.Contains(APIKeyScopes, r.scope)
	if !scopeOK {
		values := make([]string, len(APIKeyScopes))
		for i, s := range APIKeyScopes {
			values[i] = string(s)
		}
		bad = append(bad, httpx.FieldViolation{Field: "scope", Reason: "one_of", Params: map[string]any{"values": values}})
	}
	if in.TenantID != nil && strings.TrimSpace(*in.TenantID) != "" {
		id, err := uuid.Parse(strings.TrimSpace(*in.TenantID))
		if err != nil || id == uuid.Nil {
			bad = append(bad, violation("tenantId", "invalid"))
		} else {
			r.tenant = &id
		}
	}
	if in.ExpiresAt != nil && strings.TrimSpace(*in.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*in.ExpiresAt))
		switch {
		case err != nil:
			bad = append(bad, violation("expiresAt", "invalid"))
		case !t.After(now):
			bad = append(bad, violation("expiresAt", "in_past"))
		default:
			t = t.UTC().Truncate(time.Microsecond)
			r.expires = &t
		}
	}
	if len(in.Capabilities) == 0 {
		bad = append(bad, violation("capabilities", "required"))
	}
	platformKey := r.tenant == nil
	if scopeOK && !platformKey && (r.scope == authz.KeyCFShim || r.scope == authz.KeyReleasePublisher) {
		bad = append(bad, violation("tenantId", "platform_key_required"))
	}
	capViolation := func(key, reason string) {
		bad = append(bad, httpx.FieldViolation{Field: "capabilities", Reason: reason, Params: map[string]any{"key": key}})
	}
	for _, s := range in.Capabilities {
		key := authz.Cap(strings.TrimSpace(s))
		if slices.Contains(r.caps, key) {
			continue
		}
		class := authz.ClassOf(key)
		switch {
		case class == "":
			capViolation(string(key), "unknown")
			continue
		case class == authz.ClassPlatform || class == authz.ClassScope:
			capViolation(string(key), "not_allowed")
			continue
		case !platformKey && class != authz.ClassTenant:
			capViolation(string(key), "not_allowed_for_tenant_key")
			continue
		case platformKey && class != authz.ClassGlobal && class != authz.ClassSelf:
			capViolation(string(key), "not_allowed_for_platform_key")
			continue
		case scopeOK && r.scope == authz.KeyCFShim && class != authz.ClassSelf,
			scopeOK && r.scope == authz.KeyReleasePublisher && key != authz.SecurityManageMobileRel:
			capViolation(string(key), "not_allowed_for_scope")
			continue
		}
		r.caps = append(r.caps, key)
	}
	slices.Sort(r.caps)
	return r, bad
}

// Create is POST /v1/api-keys (security:manage_api_keys): the key row, api_key_created in its transaction
// and the secret in the response, once. 422 for a body validateCreate refuses (a scope outside the CHECK
// list among them); 403 for a platform key without platform_admin, a key of another tenant or a
// capability the caller does not hold itself (a platform_admin grants any key the class rules allow);
// 422 for an unknown tenant or the quarantine row, 409 failed_precondition for a pending or suspended
// tenant; 503 while API_KEY_PEPPER is unset.
func (k *APIKeys) Create(c call, in CreateAPIKeyInput) (*CreatedAPIKey, error) {
	p := c.p
	if p.IsMachine() {
		return nil, authz.ErrPermissionDenied("API keys are managed by people").WithDetails(map[string]any{"reason": ReasonMachineCaller})
	}
	now := k.clock()
	req, bad := validateCreate(in, now)
	if len(bad) > 0 {
		return nil, errInvalid(bad...)
	}
	switch {
	case req.tenant == nil && !p.HasPlatform(authz.PlatformAdmin):
		return nil, authz.ErrPermissionDenied("platform keys are created by a platform admin").
			WithDetails(map[string]any{"reason": ReasonPlatformAdminOnly})
	case !mayManage(p, req.tenant):
		return nil, authz.ErrPermissionDenied("a key of another tenant").WithDetails(map[string]any{"reason": ReasonTenantOutOfReach})
	}
	if !p.HasPlatform(authz.PlatformAdmin) {
		var missing []authz.Cap
		for _, key := range req.caps {
			if !p.Can(key) {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			return nil, authz.ErrPermissionDenied("a key holds only capabilities its creator holds", missing...).
				WithDetails(map[string]any{"reason": ReasonCapabilityNotHeld})
		}
	}
	if !k.pepper.Set() {
		return nil, httpx.ErrUnavailable("API keys are off: API_KEY_PEPPER is unset").WithDetails(map[string]any{"reason": ReasonAPIKeysOff})
	}
	caps := make([]string, len(req.caps))
	for i, key := range req.caps {
		caps[i] = string(key)
	}
	for attempt := 1; ; attempt++ {
		key, err := apikey.Generate(k.pepper)
		if err != nil {
			return nil, err
		}
		var out *CreatedAPIKey
		err = db.WithSystem(c.ctx, k.pool, nil, func(tx pgx.Tx) error {
			q := iamdb.New(tx)
			if req.tenant != nil {
				t, err := q.KeyTenant(c.ctx, *req.tenant)
				switch {
				case errors.Is(err, pgx.ErrNoRows):
					return errInvalid(violation("tenantId", "not_found"))
				case err != nil:
					return err
				case t.Kind == authz.TenantKindQuarantine:
					return errInvalid(violation("tenantId", "invalid"))
				case t.Status != "active":
					return errFailedPrecondition(ReasonTenantInactive, "the tenant is not active")
				}
			}
			id, err := q.InsertAPIKey(c.ctx, iamdb.InsertAPIKeyParams{Name: req.name, Scope: string(req.scope),
				KeyPrefix: key.Prefix, KeyHash: key.Hash, TenantID: req.tenant, Capabilities: caps, CreatedBy: p.UserID,
				ExpiresAt: req.expires})
			if err != nil {
				return err
			}
			actor := p.UserID
			if err := security.Append(c.ctx, tx, security.Event{
				EventType: EventAPIKeyCreated, Severity: security.SeverityWarning,
				Summary:     "API key created: " + req.name + " (" + string(req.scope) + ")",
				ActorUserID: &actor, TenantID: req.tenant, RequestID: c.rid, OccurredAt: now,
				Details: map[string]any{"keyId": id, "keyPrefix": key.Prefix, "name": req.name, "scope": req.scope,
					"capabilities": caps, "expiresAt": req.expires, "platformKey": req.tenant == nil},
			}); err != nil {
				return err
			}
			row, err := q.GetAPIKey(c.ctx, id)
			if err != nil {
				return err
			}
			out = &CreatedAPIKey{APIKey: apiKeyOf(iamdb.ListAPIKeysRow(row), now), Secret: key.Secret}
			return nil
		})
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == prefixConstraint && attempt < prefixCollisionRetries {
			continue
		}
		if err != nil {
			return nil, mapDBError(err)
		}
		return out, nil
	}
}

// Revoke is DELETE /v1/api-keys/{id} (security:manage_api_keys): sets revoked_at and revoked_by with
// api_key_revoked in the transaction; the key is refused from its next request (401 invalid_token, reason
// revoked). A key outside the caller's reach is 404; revoking a revoked key again is a no-op (204, no event).
func (k *APIKeys) Revoke(c call, id uuid.UUID) error {
	p := c.p
	if p.IsMachine() {
		return authz.ErrPermissionDenied("API keys are managed by people").WithDetails(map[string]any{"reason": ReasonMachineCaller})
	}
	now := k.clock()
	err := db.WithSystem(c.ctx, k.pool, nil, func(tx pgx.Tx) error {
		q := iamdb.New(tx)
		row, err := q.LockAPIKey(c.ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound()
		}
		if err != nil {
			return err
		}
		if !mayManage(p, row.TenantID) {
			return httpx.ErrNotFound()
		}
		if row.RevokedAt != nil {
			return nil
		}
		if err := q.RevokeAPIKey(c.ctx, iamdb.RevokeAPIKeyParams{ID: id, RevokedAt: now, RevokedBy: &p.UserID}); err != nil {
			return err
		}
		actor := p.UserID
		return security.Append(c.ctx, tx, security.Event{
			EventType: EventAPIKeyRevoked, Severity: security.SeverityInfo,
			Summary:     "API key revoked: " + row.Name + " (" + row.Scope + ")",
			ActorUserID: &actor, TenantID: row.TenantID, RequestID: c.rid, OccurredAt: now,
			Details: map[string]any{"keyId": row.ID, "keyPrefix": row.KeyPrefix, "name": row.Name, "scope": row.Scope,
				"platformKey": row.TenantID == nil},
		})
	})
	return mapDBError(err)
}
