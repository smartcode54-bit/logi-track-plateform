package iam

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// The role matrix of T51 (Appendix B §B.2.6, Appendix C §C.2.5): GET and PUT /v1/roles/matrix over
// role_capability_overrides, which replaces permissions_config/{role}. A matrix is one layer of overrides: a
// tenant's (tenant_id = the tenant) or the platform-wide one (tenant_id NULL, platform_admin only). Resolution per
// role and key: catalog default -> platform-wide override -> tenant override -> the steward rule (global keys only
// for own-fleet staff, R60), as authz.RoleCaps computes it for every request.

// EventRoleMatrixSaved is the security_events type of a matrix save (Appendix C §C.4.13): the full diff of the
// layer's override rows, in the transaction of the save.
const EventRoleMatrixSaved = "role_matrix_saved"

// RouteRolesChanged is the realtime-only outbox event of a matrix save: SSE roles.changed on tenant:{tid}:config
// (Appendix B §B.4.3; every tenant's topic for a platform-wide save). Clients refetch ['me'] and ['roles'], and an
// open stream on that topic ends with reconnect roles_changed (internal/sse).
const RouteRolesChanged = "roles.changed"

// CodeCapabilityNotOverridable is the 422 of a matrix row the layer cannot hold (Appendix B §B.1.5).
const CodeCapabilityNotOverridable = "capability_not_overridable"

// Matrix layers.
const (
	LayerTenant   = "tenant"
	LayerPlatform = "platform"
)

// Reasons a key is not overridable for a role in a layer (details.overrides[].reason of 422
// capability_not_overridable; MatrixCell.Overridable is false for the same cells).
const (
	ReasonTenantAdminFixed = "tenant_admin"       // tenant_admin is never overridden (no tenant can lock itself out)
	ReasonNotGrantable     = "not_grantable"      // platform:* and dispatch:* (classes platform and scope)
	ReasonPlatformWideOnly = "platform_wide_only" // users:assign_role: only in a platform-wide row
	ReasonGlobalOwnFleet   = "global_own_fleet"   // a global key: only for the own fleet or platform-wide (R60)
	ReasonRoleClass        = "role_class"         // mobile:* only for driver; staff keys never for driver
)

// MaxMatrixOverrides bounds the body of PUT /v1/roles/matrix: 5 overridable roles x 76 keys fit with room.
const MaxMatrixOverrides = 500

// Matrix is GET /v1/roles/matrix (and the response of PUT): every tenant role with every key a tenant role can
// hold (classes tenant, global, self; 76 keys), each with its catalog default, the effective value in the layer,
// whether the layer overrides it and whether the layer may.
type Matrix struct {
	Layer      string       `json:"layer"` // tenant | platform
	TenantID   *uuid.UUID   `json:"tenantId"`
	TenantKind *string      `json:"tenantKind"` // own_fleet | carrier; null for the platform-wide layer
	Roles      []MatrixRole `json:"roles"`
}

// MatrixRole is one tenant role of a matrix. Overridable is false for tenant_admin.
type MatrixRole struct {
	Role         string                `json:"role"`
	Overridable  bool                  `json:"overridable"`
	Capabilities map[string]MatrixCell `json:"capabilities"`
}

// MatrixCell is one key of one role (Appendix C §C.2.5: {default, effective, overridden}, so the UI can diff and
// "discard" restores the loaded state). Effective is what the role holds in the layer: in a tenant layer after the
// platform-wide and tenant overrides and the steward rule (a global key is effective only for the staff of the own
// fleet); in the platform-wide layer after the platform-wide overrides and before the steward rule. Overridden says
// the layer holds a row that applies; PlatformOverride, in a tenant layer only, is the platform-wide row the tenant
// inherits (absent when there is none).
type MatrixCell struct {
	Default          bool  `json:"default"`
	Effective        bool  `json:"effective"`
	Overridden       bool  `json:"overridden"`
	Overridable      bool  `json:"overridable"`
	PlatformOverride *bool `json:"platformOverride,omitempty"`
}

// matrixLayer is the layer a request reads or writes.
type matrixLayer struct {
	tenant *uuid.UUID // nil: the platform-wide layer
	kind   string     // tenants.kind of a tenant layer
}

func (l matrixLayer) name() string {
	if l.tenant == nil {
		return LayerPlatform
	}
	return LayerTenant
}

// notOverridable is "" when role may carry an override of key in layer l, else the reason (Appendix C §C.2.5): the
// DDL forbids platform and scope keys everywhere and users:assign_role in a tenant row; the API also refuses
// tenant_admin, a global key in a tenant other than the own fleet, and a key of the other role class. Every cell it
// admits is one authz.Overridable admits, so a stored row always takes effect.
func notOverridable(role authz.TenantRole, key authz.Cap, l matrixLayer) string {
	if role == authz.TenantAdmin {
		return ReasonTenantAdminFixed
	}
	switch class := authz.ClassOf(key); class {
	case authz.ClassSelf:
		if role != authz.Driver {
			return ReasonRoleClass
		}
	case authz.ClassTenant, authz.ClassGlobal:
		switch {
		case role == authz.Driver:
			return ReasonRoleClass
		case key == authz.UsersAssignRole && l.tenant != nil:
			return ReasonPlatformWideOnly
		case class == authz.ClassGlobal && l.tenant != nil && l.kind != authz.TenantKindOwnFleet:
			return ReasonGlobalOwnFleet
		}
	default:
		return ReasonNotGrantable
	}
	return ""
}

// matrixKeys are the keys a tenant role can hold: classes tenant, global and self, in catalog order.
func matrixKeys() []authz.Cap {
	var out []authz.Cap
	for _, e := range authz.Catalog() {
		switch e.Class {
		case authz.ClassTenant, authz.ClassGlobal, authz.ClassSelf:
			out = append(out, e.Key)
		}
	}
	return out
}

type cellKey struct {
	role authz.TenantRole
	key  authz.Cap
}

// overrideRows maps the rows of one layer by role and key (rows naming an unknown role or key are kept: they are
// stray and a save removes them).
func overrideRows(rows []iamdb.LayerOverridesRow) map[cellKey]bool {
	out := make(map[cellKey]bool, len(rows))
	for _, r := range rows {
		out[cellKey{authz.TenantRole(r.Role), authz.Cap(r.Capability)}] = r.Allowed
	}
	return out
}

// roleOverrides are the overrides of one role as authz.RoleCaps takes them: the platform-wide rows, then the rows
// of tenant (nil: none).
func roleOverrides(role authz.TenantRole, platform, tenantRows map[cellKey]bool, tenant *uuid.UUID) []authz.Override {
	var out []authz.Override
	add := func(rows map[cellKey]bool, tid *uuid.UUID) {
		for k, v := range rows {
			if k.role == role && authz.Known(k.key) {
				out = append(out, authz.Override{TenantID: tid, Cap: k.key, Allowed: v})
			}
		}
	}
	add(platform, nil)
	if tenant != nil {
		add(tenantRows, tenant)
	}
	// RoleCaps applies the rows in order within each layer; sort for a deterministic set.
	slices.SortFunc(out, func(a, b authz.Override) int { return cmp.Compare(a.Cap, b.Cap) })
	return out
}

// inherited is what role holds in layer l without the layer's own rows, before the steward rule: the catalog
// default in the platform-wide layer, default + platform-wide rows in a tenant layer. A requested value equal to
// it needs no row.
func inherited(role authz.TenantRole, l matrixLayer, platform map[cellKey]bool) authz.CapSet {
	if l.tenant == nil {
		return authz.TenantDefaults(role)
	}
	return authz.RoleCaps(role, true, roleOverrides(role, platform, nil, nil))
}

// buildMatrix computes the matrix of layer l from the platform-wide rows and the rows of a tenant layer (ignored
// for the platform-wide layer).
func buildMatrix(l matrixLayer, platform, tenantRows map[cellKey]bool) Matrix {
	m := Matrix{Layer: l.name(), TenantID: l.tenant, Roles: make([]MatrixRole, 0, len(authz.TenantRoles))}
	if l.tenant != nil {
		k := l.kind
		m.TenantKind = &k
	}
	layerRows := platform
	if l.tenant != nil {
		layerRows = tenantRows
	}
	keys := matrixKeys()
	for _, role := range authz.TenantRoles {
		steward := l.tenant == nil || (l.kind == authz.TenantKindOwnFleet && role.IsStaff())
		effective := authz.RoleCaps(role, steward, roleOverrides(role, platform, tenantRows, l.tenant))
		defaults := authz.TenantDefaults(role)
		mr := MatrixRole{Role: string(role), Overridable: role != authz.TenantAdmin, Capabilities: make(map[string]MatrixCell, len(keys))}
		for _, k := range keys {
			can := notOverridable(role, k, l) == ""
			_, has := layerRows[cellKey{role, k}]
			cell := MatrixCell{Default: defaults.Has(k), Effective: effective.Has(k), Overridden: has && can, Overridable: can}
			if l.tenant != nil {
				if v, ok := platform[cellKey{role, k}]; ok && authz.Overridable(role, k, false) {
					cell.PlatformOverride = &v
				}
			}
			mr.Capabilities[string(k)] = cell
		}
		m.Roles = append(m.Roles, mr)
	}
	return m
}

// MatrixOverride is one row of PUT /v1/roles/matrix.
type MatrixOverride struct {
	Role       string `json:"role"`
	Capability string `json:"capability"`
	Allowed    *bool  `json:"allowed"`
}

// SaveMatrixInput is the body of PUT /v1/roles/matrix: the complete set of overrides of the layer. A row absent
// from it is removed (the role falls back to the inherited value), and a row whose value equals the inherited value
// is not stored, so the stored set is always the minimal one. overrides is required ([] resets the layer).
type SaveMatrixInput struct {
	Overrides *[]MatrixOverride `json:"overrides"`
}

// MatrixChange is one entry of the role_matrix_saved diff: the layer's row before and after (null = no row).
type MatrixChange struct {
	Role       string `json:"role"`
	Capability string `json:"capability"`
	From       *bool  `json:"from"`
	To         *bool  `json:"to"`
}

// refusal is one row of a 422 capability_not_overridable.
type refusal struct {
	Index      int    `json:"index"`
	Role       string `json:"role"`
	Capability string `json:"capability"`
	Reason     string `json:"reason"`
}

// desiredRows validates the body against layer l and returns the rows the layer must hold afterwards.
func desiredRows(in SaveMatrixInput, l matrixLayer, platform map[cellKey]bool) (map[cellKey]bool, error) {
	if in.Overrides == nil {
		return nil, errInvalid(violation("overrides", "required"))
	}
	list := *in.Overrides
	if len(list) > MaxMatrixOverrides {
		return nil, errInvalid(httpx.FieldViolation{Field: "overrides", Reason: "too_many", Params: map[string]any{"max": MaxMatrixOverrides}})
	}
	var bad []httpx.FieldViolation
	var refused []refusal
	seen := map[cellKey]int{}
	want := map[cellKey]bool{}
	inh := map[authz.TenantRole]authz.CapSet{}
	for i, o := range list {
		field := "overrides[" + strconv.Itoa(i) + "]"
		role, key := authz.TenantRole(strings.TrimSpace(o.Role)), authz.Cap(strings.TrimSpace(o.Capability))
		ok := true
		if !role.Valid() {
			bad, ok = append(bad, violation(field+".role", "invalid")), false
		}
		if !authz.Known(key) {
			bad, ok = append(bad, violation(field+".capability", "unknown")), false
		}
		if o.Allowed == nil {
			bad, ok = append(bad, violation(field+".allowed", "required")), false
		}
		if !ok {
			continue
		}
		ck := cellKey{role, key}
		if j, dup := seen[ck]; dup {
			bad = append(bad, httpx.FieldViolation{Field: field, Reason: "duplicate", Params: map[string]any{"index": j}})
			continue
		}
		seen[ck] = i
		if r := notOverridable(role, key, l); r != "" {
			refused = append(refused, refusal{Index: i, Role: string(role), Capability: string(key), Reason: r})
			continue
		}
		set, cached := inh[role]
		if !cached {
			set = inherited(role, l, platform)
			inh[role] = set
		}
		if *o.Allowed != set.Has(key) {
			want[ck] = *o.Allowed
		}
	}
	if len(bad) > 0 {
		return nil, errInvalid(bad...)
	}
	if len(refused) > 0 {
		return nil, httpx.NewError(http.StatusUnprocessableEntity, CodeCapabilityNotOverridable,
			"the layer cannot override these capabilities").WithDetails(map[string]any{"layer": l.name(), "overrides": refused})
	}
	return want, nil
}

// diffRows compares the layer's rows before and after a save, ordered by role (catalog role order) and key.
func diffRows(before, after map[cellKey]bool) []MatrixChange {
	keys := map[cellKey]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	var out []MatrixChange
	for k := range keys {
		var from, to *bool
		if v, ok := before[k]; ok {
			from = &v
		}
		if v, ok := after[k]; ok {
			to = &v
		}
		if (from == nil) != (to == nil) || (from != nil && *from != *to) {
			out = append(out, MatrixChange{Role: string(k.role), Capability: string(k.key), From: from, To: to})
		}
	}
	slices.SortFunc(out, func(a, b MatrixChange) int {
		return cmp.Or(cmp.Compare(roleOrder(a.Role), roleOrder(b.Role)), cmp.Compare(a.Role, b.Role), cmp.Compare(a.Capability, b.Capability))
	})
	return out
}

func roleOrder(r string) int {
	if i := slices.Index(authz.TenantRoles, authz.TenantRole(r)); i >= 0 {
		return i
	}
	return len(authz.TenantRoles)
}

// --- service --------------------------------------------------------------------------------------------

// MatrixTarget are the query parameters that name the layer of a matrix request: ?tenantId (a tenant) or
// ?layer=platform (the platform-wide layer); both absent = the caller's effective tenant.
type MatrixTarget struct {
	TenantID string
	Layer    string // "", "tenant" or "platform"
}

// resolveLayer decides the layer of a matrix request (Appendix B §B.2.6): ?layer=platform is the platform-wide layer
// (platform_admin only; it excludes ?tenantId); ?tenantId names a tenant (a platform_admin names any tenant; anyone
// else only its effective tenant); without either, the effective tenant (the active tenant, or the X-Act-On-Tenant
// target), else the platform-wide layer for a platform_admin, else 403 tenant_required. A write needs a user
// (updated_by and the audit actor are users rows): an API key reads only.
func (a *Admin) resolveLayer(c call, q *iamdb.Queries, target MatrixTarget, write bool) (matrixLayer, error) {
	p := c.p
	if write && p.IsMachine() {
		return matrixLayer{}, authz.ErrPermissionDenied("the role matrix is saved by a user, not an API key").
			WithDetails(map[string]any{"reason": "user_required"})
	}
	platform := p.HasPlatform(authz.PlatformAdmin) && !p.IsMachine()
	eff := p.EffectiveTenant()
	raw := strings.TrimSpace(target.TenantID)
	switch target.Layer {
	case "", LayerTenant:
	case LayerPlatform:
		switch {
		case raw != "":
			return matrixLayer{}, errInvalid(violation("tenantId", "not_allowed"))
		case !platform:
			return matrixLayer{}, authz.ErrPermissionDenied("the platform-wide layer is for platform admins").
				WithDetails(map[string]any{"reason": "platform_only"})
		}
		return matrixLayer{}, nil
	default:
		return matrixLayer{}, errInvalid(violation("layer", "invalid"))
	}
	var tid *uuid.UUID
	switch {
	case raw != "":
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return matrixLayer{}, errInvalid(violation("tenantId", "invalid"))
		}
		if (eff == nil || *eff != id) && !platform {
			return matrixLayer{}, authz.ErrPermissionDenied("a tenantId other than the caller's tenant is for platform admins").
				WithDetails(map[string]any{"reason": "platform_only"})
		}
		tid = &id
	case eff != nil:
		t := *eff
		tid = &t
	case platform:
		return matrixLayer{}, nil // the platform-wide layer
	default:
		return matrixLayer{}, authz.ErrTenantRequired()
	}
	t, err := q.MatrixTenant(c.ctx, *tid)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && t.Kind == "quarantine") {
		return matrixLayer{}, httpx.ErrNotFound()
	}
	if err != nil {
		return matrixLayer{}, err
	}
	return matrixLayer{tenant: tid, kind: t.Kind}, nil
}

// loadMatrix reads the rows of layer l (and the platform-wide rows) in q.
func loadMatrix(ctx context.Context, q *iamdb.Queries, l matrixLayer) (Matrix, error) {
	pr, err := q.LayerOverrides(ctx, nil)
	if err != nil {
		return Matrix{}, err
	}
	var tr []iamdb.LayerOverridesRow
	if l.tenant != nil {
		if tr, err = q.LayerOverrides(ctx, l.tenant); err != nil {
			return Matrix{}, err
		}
	}
	return buildMatrix(l, overrideRows(pr), overrideRows(tr)), nil
}

// GetMatrix is GET /v1/roles/matrix (security:manage_roles).
func (a *Admin) GetMatrix(c call, target MatrixTarget) (*Matrix, error) {
	var m Matrix
	err := a.read(c.ctx, func(q *iamdb.Queries) error {
		l, err := a.resolveLayer(c, q, target, false)
		if err != nil {
			return err
		}
		m, err = loadMatrix(c.ctx, q, l)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// SaveMatrix is PUT /v1/roles/matrix (security:manage_roles): the layer's override rows become exactly the body's
// (minus the rows equal to the inherited value), and when anything changed the same transaction appends
// role_matrix_saved with the full diff and the realtime event roles.changed; after COMMIT, INCR rbac:ver makes every
// role set be computed again on the next request (Appendix C §C.2.5). It returns the new matrix and the number of
// rows that changed.
func (a *Admin) SaveMatrix(c call, target MatrixTarget, in SaveMatrixInput) (*Matrix, int, error) {
	var m Matrix
	var changes []MatrixChange
	err := a.tx(c.ctx, func(tx pgx.Tx, q *iamdb.Queries, _ func(*auth.PostCommit)) error {
		l, err := a.resolveLayer(c, q, target, true)
		if err != nil {
			return err
		}
		if err := q.LockOverrideLayer(c.ctx, l.tenant); err != nil {
			return err
		}
		pr, err := q.LayerOverrides(c.ctx, nil)
		if err != nil {
			return err
		}
		platform := overrideRows(pr)
		before := platform
		if l.tenant != nil {
			tr, err := q.LayerOverrides(c.ctx, l.tenant)
			if err != nil {
				return err
			}
			before = overrideRows(tr)
		}
		after, err := desiredRows(in, l, platform)
		if err != nil {
			return err
		}
		if changes = diffRows(before, after); len(changes) == 0 {
			m, err = loadMatrix(c.ctx, q, l)
			return err
		}
		var added, removed, changed int
		for _, ch := range changes {
			if ch.To == nil {
				removed++
				err = q.DeleteOverride(c.ctx, iamdb.DeleteOverrideParams{TenantID: l.tenant, Role: ch.Role, Capability: ch.Capability})
			} else {
				if ch.From == nil {
					added++
				} else {
					changed++
				}
				err = q.UpsertOverride(c.ctx, iamdb.UpsertOverrideParams{TenantID: l.tenant, Role: ch.Role,
					Capability: ch.Capability, Allowed: *ch.To, UpdatedBy: c.p.UserID})
			}
			if err != nil {
				return err
			}
		}
		actor := c.p.UserID
		if err := security.Append(c.ctx, tx, security.Event{
			EventType: EventRoleMatrixSaved, Severity: security.SeverityWarning,
			Summary:     fmt.Sprintf("role matrix saved (%s layer): %d override row(s) changed", l.name(), len(changes)),
			ActorUserID: &actor, TenantID: l.tenant, RequestID: c.rid, OccurredAt: a.clock(),
			Details: map[string]any{"layer": l.name(), "tenantId": l.tenant, "changes": changes,
				"added": added, "removed": removed, "changed": changed, "requested": len(*in.Overrides)},
		}); err != nil {
			return err
		}
		topics, err := configTopics(c.ctx, q, l)
		if err != nil {
			return err
		}
		aggID := LayerPlatform
		if l.tenant != nil {
			aggID = l.tenant.String()
		}
		if err := emitTopics(c, tx, RouteRolesChanged, "roles", aggID, l.tenant, nil, topics); err != nil {
			return err
		}
		m, err = loadMatrix(c.ctx, q, l)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	if len(changes) > 0 && a.rbac != nil {
		if _, err := a.rbac.BumpVersion(c.ctx); err != nil {
			// The rows are committed; the cached role sets expire within CapsTTL (Appendix C §C.2.5).
			a.log.Error().Err(err).Msg("iam: role matrix saved but rbac:ver was not incremented; role sets refresh within the cache TTL")
		}
	}
	return &m, len(changes), nil
}

// configTopics are the tenant:{tid}:config topics a save of layer l concerns: the tenant's, or every tenant's for
// the platform-wide layer.
func configTopics(ctx context.Context, q *iamdb.Queries, l matrixLayer) ([]string, error) {
	if l.tenant != nil {
		return []string{realtime.TenantTopic(l.tenant.String(), realtime.FamilyConfig)}, nil
	}
	ids, err := q.ConfigTenants(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = realtime.TenantTopic(id.String(), realtime.FamilyConfig)
	}
	return out, nil
}
