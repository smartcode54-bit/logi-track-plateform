package supportprojection

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// PlatformOwner marks account data that belongs to the platform plane rather than a tenant.
const PlatformOwner = "__platform_plane__"

// PlatformProjection installs live serialization authorization for the owned platform DTOs.
// It preserves the audience/context versions installed by the authentication boundary.
func Middleware(pool db.Beginner) fiber.Handler {
	return func(c fiber.Ctx) error {
		p := authz.PrincipalFrom(c)
		if p == nil || (!p.IsPlatform() && !authz.IsSupport(p)) {
			return c.Next()
		}
		httpx.RequireResponseProjection(c)
		audience := httpx.ProjectionAudienceFrom(c)
		audience.Platform, audience.Support, audience.Actor = p.IsPlatform(), authz.IsSupport(p), p.UserID.String()
		if p.TenantID != nil {
			audience.Tenant = p.TenantID.String()
		}
		audience.Finalize = func(ctx context.Context, owners []string, fn func(map[string]httpx.Consent) error) error {
			return db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
				var version int32
				var status string
				if err := tx.QueryRow(ctx, `SELECT auth_version,status FROM users WHERE id=$1 FOR SHARE`, p.UserID).Scan(&version, &status); err != nil {
					return err
				}
				stale := func() error {
					return httpx.NewError(401, "token_expired", "token expired").WithDetails(map[string]any{"reason": "claims_changed"})
				}
				if version != p.AuthVersion || status != "active" {
					return stale()
				}
				if p.SessionID != uuid.Nil {
					var mode string
					var scv int64
					var tenant *uuid.UUID
					var revoked *time.Time
					var expires time.Time
					err := tx.QueryRow(ctx, `SELECT access_mode,context_version,active_tenant_id,revoked_at,absolute_expires_at FROM sessions WHERE id=$1 AND user_id=$2 FOR SHARE`, p.SessionID, p.UserID).Scan(&mode, &scv, &tenant, &revoked, &expires)
					if errors.Is(err, pgx.ErrNoRows) {
						return stale()
					}
					if err != nil {
						return err
					}
					if revoked != nil || !expires.After(time.Now()) || ((mode == "support") != audience.Support) || !equalID(tenant, p.TenantID) || ((audience.ContextVersion == "" && (scv != 0 || audience.Support)) || (audience.ContextVersion != "" && audience.ContextVersion != strconv.FormatInt(scv, 10))) {
						return stale()
					}
				} else if audience.Support {
					return stale()
				}
				var roles []string
				rows, err := tx.Query(ctx, `SELECT role FROM user_platform_roles WHERE user_id=$1 ORDER BY role`, p.UserID)
				if err != nil {
					return err
				}
				for rows.Next() {
					var role string
					if err := rows.Scan(&role); err != nil {
						rows.Close()
						return err
					}
					roles = append(roles, role)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				expected := make([]string, len(p.Platform))
				for i, r := range p.Platform {
					expected[i] = string(r)
				}
				slices.Sort(expected)
				if !slices.Equal(roles, expected) {
					return stale()
				}
				if len(owners) > 100 {
					return httpx.ErrSupportProjectionUnavailable()
				}
				ids := slices.Clone(owners)
				slices.Sort(ids)
				ids = slices.Compact(ids)
				consents := map[string]httpx.Consent{}
				for _, id := range ids {
					if id == PlatformOwner {
						if !audience.Platform {
							return httpx.ErrSupportProjectionUnavailable()
						}
						consents[id] = httpx.Consent{Enabled: true}
						continue
					}
					uid, err := uuid.Parse(id)
					if err != nil {
						return httpx.ErrSupportProjectionUnavailable()
					}
					var enabled bool
					var v int64
					err = tx.QueryRow(ctx, `SELECT support_access_enabled,support_access_version FROM tenants WHERE id=$1 AND kind<>'quarantine' FOR SHARE`, uid).Scan(&enabled, &v)
					if errors.Is(err, pgx.ErrNoRows) {
						continue
					}
					if err != nil {
						return err
					}
					consents[id] = httpx.Consent{Enabled: enabled, Version: strconv.FormatInt(v, 10)}
				}
				if expected, ok := c.Locals(labelVersionsKey{}).(map[uuid.UUID]int64); ok {
					for id, version := range expected {
						if !slices.Contains(ids, id.String()) {
							return httpx.ErrSupportProjectionUnavailable()
						}
						var current int64
						if err := tx.QueryRow(ctx, `SELECT support_label_version FROM tenants WHERE id=$1`, id).Scan(&current); err != nil {
							return err
						}
						if current != version {
							return stale()
						}
					}
				}
				return fn(consents)
			})
		}
		httpx.SetProjectionAudience(c, audience)
		return c.Next()
	}
}

func equalID(a, b *uuid.UUID) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

type labelVersionsKey struct{}

// ExpectLabels also binds conditional organization/person and reviewed-code search to the final locked rows.
func LabelVersions(c fiber.Ctx) map[uuid.UUID]int64 {
	if snapshot, ok := c.Locals(labelVersionsKey{}).(map[uuid.UUID]int64); ok {
		return snapshot
	}
	snapshot := map[uuid.UUID]int64{}
	c.Locals(labelVersionsKey{}, snapshot)
	return snapshot
}
func ExpectLabels(c fiber.Ctx, versions map[uuid.UUID]int64) {
	snapshot := LabelVersions(c)
	for id, v := range versions {
		snapshot[id] = v
	}
}
