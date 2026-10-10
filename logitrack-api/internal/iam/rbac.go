// Package iam owns identity and access management beyond sign-in: tenants, memberships, roles, scopes
// and users (main spec §2.3). From T07 it holds the per-request authorization of every authenticated
// request (RBAC): the capability resolution of Appendix C §C.2 with role overrides cached under the
// matrix version rbac:ver, the steward flag and contractor reach of §C.3.4 (R60), and cross-tenant
// acting through X-Act-On-Tenant with its audit row (§C.3.9). The users administration API joins
// with T19, the role matrix API with T51.
//
// iam reads the platform-only identity tables, so it is one of the packages allowed to call
// db.WithSystem (tools/analyzers/withsystem).
package iam

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// CapsTTL is the lifetime of a cached role set rbac:caps:{tenant}:{role}:{ver} (Appendix C §C.2.5).
// The matrix version is part of the key, so a matrix save is effective on the next request without
// deleting anything; the TTL only bounds what an INCR lost to a Redis outage can leave behind.
const CapsTTL = 10 * time.Minute

// redisTimeout bounds the rbac:ver round trip; a slow Redis costs a request at most this long before
// the set is computed from PostgreSQL.
const redisTimeout = 300 * time.Millisecond

// RBAC completes authenticated principals (Authorize, Resolve) and serves GET /v1/me its capabilities.
// It implements auth.Authorizer and auth.CapabilityResolver.
type RBAC struct {
	pool  db.Beginner
	cache *cache.Cache
	rdb   redis.UniversalClient
	ks    cache.Keyspace
	log   zerolog.Logger
	now   func() time.Time
}

// Deps are the collaborators of the RBAC service.
type Deps struct {
	// Pool is the logitrack_app pool; the inputs are read in db.WithSystem transactions.
	Pool db.Beginner
	// Cache serves cache:tenant:own_fleet, cache:tenant:subtenants:{id} and the rbac:caps sets
	// (read-through, PostgreSQL when Redis fails). Optional: without it every request reads PostgreSQL.
	Cache *cache.Cache
	// Redis holds rbac:ver (GET per request, INCR by BumpVersion); required with Cache.
	Redis redis.UniversalClient
	Log   zerolog.Logger
	Now   func() time.Time // optional, defaults to time.Now
}

// NewRBAC builds the service.
func NewRBAC(d Deps) (*RBAC, error) {
	if d.Pool == nil {
		return nil, errors.New("iam: a pool is required")
	}
	if (d.Cache == nil) != (d.Redis == nil) {
		return nil, errors.New("iam: Cache and Redis go together")
	}
	r := &RBAC{pool: d.Pool, cache: d.Cache, rdb: d.Redis, log: d.Log, now: d.Now}
	if d.Cache != nil {
		r.ks = d.Cache.Keys()
	}
	if r.now == nil {
		r.now = time.Now
	}
	return r, nil
}

// Capabilities implements auth.CapabilityResolver for GET /v1/me: the effective set, resolved now
// when the request did not run through Authorize.
func (r *RBAC) Capabilities(ctx context.Context, p *authz.Principal) ([]string, error) {
	if !p.Resolved() {
		if err := r.Resolve(ctx, p); err != nil {
			return nil, err
		}
	}
	return p.Caps.Strings(), nil
}

// Resolve completes p for its current state (Appendix C §C.2.4, §C.3.4): the kind of the effective
// tenant, the steward flag (R60), contractor reach for staff, and the effective capability set = the
// role set of the effective tenant (catalog default, platform-wide and tenant overrides, global keys
// only for stewards) ∪ the scope sets ∪ the platform sets. A machine principal (API key) holds its
// key's capabilities only. Errors are infrastructure failures (PostgreSQL unreachable); a Redis
// failure falls back to PostgreSQL.
func (r *RBAC) Resolve(ctx context.Context, p *authz.Principal) error {
	tid := p.EffectiveTenant()
	kind := ""
	if tid != nil {
		var err error
		if kind, err = r.tenantKind(ctx, p, *tid); err != nil {
			return err
		}
	}
	p.TenantKind = kind
	p.Steward = authz.IsSteward(p, kind)
	p.SubtenantIDs = nil
	var roleCaps authz.CapSet
	if role := p.EffectiveRole(); tid != nil && role != "" && !p.IsMachine() {
		if role.IsStaff() {
			subs, err := r.subtenants(ctx, *tid)
			if err != nil {
				return err
			}
			p.SubtenantIDs = subs
		}
		set, err := r.roleSet(ctx, *tid, role)
		if err != nil {
			return err
		}
		roleCaps = authz.ApplySteward(set, p.Steward)
	}
	p.Caps = authz.Effective(p, roleCaps)
	p.MarkResolved()
	return nil
}

// tenantKind is the kind of the effective tenant: what Authorize read for an X-Act-On-Tenant target,
// else own_fleet when the tenant is the own-fleet row (cache:tenant:own_fleet) and carrier otherwise
// (quarantine holds no memberships, C.1.8).
func (r *RBAC) tenantKind(ctx context.Context, p *authz.Principal, tid uuid.UUID) (string, error) {
	if p.ActOnTenant != nil && p.TenantKind != "" {
		return p.TenantKind, nil
	}
	own, ok, err := r.ownFleet(ctx)
	if err != nil {
		return "", err
	}
	if ok && own == tid {
		return authz.TenantKindOwnFleet, nil
	}
	return authz.TenantKindCarrier, nil
}

// errNoOwnFleet keeps a database without an own-fleet row (before seed / ETL) out of the cache.
var errNoOwnFleet = errors.New("iam: no own-fleet tenant")

// ownFleet is the id of tenants.kind='own_fleet' (R7), read through cache:tenant:own_fleet; ok is false
// while the row does not exist (nobody is a steward then but platform_admin).
func (r *RBAC) ownFleet(ctx context.Context) (uuid.UUID, bool, error) {
	load := func(ctx context.Context) (string, error) {
		var id uuid.UUID
		err := r.system(ctx, func(q *iamdb.Queries) (err error) {
			id, err = q.OwnFleetTenant(ctx)
			return err
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errNoOwnFleet
		}
		if err != nil {
			return "", err
		}
		return id.String(), nil
	}
	var s string
	var err error
	if r.cache != nil {
		s, err = cache.GetJSON(ctx, r.cache, r.ks.TenantOwnFleet(), cache.OwnFleetTTL, load)
	} else {
		s, err = load(ctx)
	}
	if errors.Is(err, errNoOwnFleet) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("iam: own-fleet tenant: %w", err)
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("iam: own-fleet tenant: %w", err)
	}
	return id, true, nil
}

// subtenants is the contractor reach of tid (R60, C.3.4), read through cache:tenant:subtenants:{tid},
// which tenant.created / tenant.updated drop.
func (r *RBAC) subtenants(ctx context.Context, tid uuid.UUID) ([]uuid.UUID, error) {
	load := func(ctx context.Context) ([]string, error) {
		var ids []uuid.UUID
		err := r.system(ctx, func(q *iamdb.Queries) (err error) {
			ids, err = q.Subtenants(ctx, tid)
			return err
		})
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = id.String()
		}
		return out, err
	}
	var raw []string
	var err error
	if r.cache != nil {
		raw, err = r.cache.Subtenants(ctx, tid.String(), load)
	} else {
		raw, err = load(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("iam: contractor reach: %w", err)
	}
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("iam: contractor reach: %w", err)
		}
		out = append(out, id)
	}
	return out, nil
}

// roleSet is the set of role in tenant tid before the steward rule: the catalog default with the
// platform-wide and tenant overrides applied. tenant_admin is not overridable and needs no lookup;
// other roles are cached as rbac:caps:{tid}:{role}:{rbac:ver}, so a matrix save (overrides + INCR
// rbac:ver, T51) is effective on the next request. When rbac:ver cannot be read the set comes from
// PostgreSQL and is not cached.
func (r *RBAC) roleSet(ctx context.Context, tid uuid.UUID, role authz.TenantRole) (authz.CapSet, error) {
	if role == authz.TenantAdmin {
		return authz.RoleCaps(role, true, nil), nil
	}
	load := func(ctx context.Context) (authz.CapSet, error) {
		ov, err := r.overrides(ctx, tid, role)
		if err != nil {
			return authz.CapSet{}, err
		}
		return authz.RoleCaps(role, true, ov), nil
	}
	if r.cache == nil {
		return load(ctx)
	}
	ver, err := r.Version(ctx)
	if err != nil {
		r.log.Warn().Err(err).Msg("iam: rbac:ver unreadable; role set computed from PostgreSQL")
		return load(ctx)
	}
	return cache.GetJSON(ctx, r.cache, r.ks.RBACCapabilities(tid.String(), string(role), ver), CapsTTL, load)
}

// overrides reads the platform-wide and tenant rows of role_capability_overrides for role in tid.
func (r *RBAC) overrides(ctx context.Context, tid uuid.UUID, role authz.TenantRole) ([]authz.Override, error) {
	var rows []iamdb.RoleOverridesRow
	err := r.system(ctx, func(q *iamdb.Queries) (err error) {
		rows, err = q.RoleOverrides(ctx, iamdb.RoleOverridesParams{Role: string(role), TenantID: tid})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("iam: role overrides: %w", err)
	}
	out := make([]authz.Override, 0, len(rows))
	for _, row := range rows {
		k := authz.Cap(row.Capability)
		if !authz.Known(k) {
			r.log.Warn().Str("capability", row.Capability).Str("role", string(role)).
				Msg("iam: override names a key outside the catalog; ignored")
			continue
		}
		out = append(out, authz.Override{TenantID: row.TenantID, Cap: k, Allowed: row.Allowed})
	}
	return out, nil
}

// Version is the role-matrix version rbac:ver (0 before the first save).
func (r *RBAC) Version(ctx context.Context) (int64, error) {
	if r.rdb == nil {
		return 0, errors.New("iam: no Redis")
	}
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()
	v, err := r.rdb.Get(ctx, r.ks.RBACVersion()).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return v, err
}

// BumpVersion is INCR rbac:ver, called after a transaction that changed role_capability_overrides
// commits (PUT /v1/roles/matrix, T51; ETL auth-rbac): the next request of every role reads a new
// rbac:caps key and so the new set (Appendix C §C.2.5). It returns the new version.
func (r *RBAC) BumpVersion(ctx context.Context) (int64, error) {
	if r.rdb == nil {
		return 0, nil // no cache: every request reads PostgreSQL already
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return r.rdb.Incr(ctx, r.ks.RBACVersion()).Result()
}

// system runs fn in a WithSystem transaction with the generated queries bound to it.
func (r *RBAC) system(ctx context.Context, fn func(q *iamdb.Queries) error) error {
	return db.WithSystem(ctx, r.pool, nil, func(tx pgx.Tx) error { return fn(iamdb.New(tx)) })
}
