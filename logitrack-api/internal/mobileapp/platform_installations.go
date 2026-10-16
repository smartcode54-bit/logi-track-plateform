package mobileapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// Platform cursors carry a database-generated opaque row ID, never the device's install ID.
type platformInstallCursor struct {
	T  time.Time `json:"t"`
	D  uuid.UUID `json:"d"`
	ID uuid.UUID `json:"id"`
}

func (c platformInstallCursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodePlatformInstallCursor(s string) (platformInstallCursor, bool) {
	var c platformInstallCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(b, &c) != nil {
		return c, false
	}
	return c, !c.T.IsZero() && c.D != uuid.Nil && c.ID != uuid.Nil
}
func (s *Service) platformInstallations(ctx context.Context, p *authz.Principal, in InstallationQuery) ([]Installation, string, InstallationMeta, error) {
	if err := platformInstallPermission(p); err != nil {
		return nil, "", InstallationMeta{}, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > 100 {
		return nil, "", InstallationMeta{}, httpx.ErrInvalidArgument(violation("limit", "out_of_range", nil))
	}
	var after platformInstallCursor
	var seen *time.Time
	if in.Cursor != "" {
		var ok bool
		after, ok = decodePlatformInstallCursor(in.Cursor)
		if !ok {
			return nil, "", InstallationMeta{}, httpx.ErrInvalidArgument(violation("cursor", "invalid", nil))
		}
		seen = &after.T
	}
	var below []string
	if in.BelowVersion != "" {
		var ok bool
		below, ok = versionParts(in.BelowVersion)
		if !ok {
			return nil, "", InstallationMeta{}, httpx.ErrInvalidArgument(violation("belowVersion", "invalid", nil))
		}
	}
	gate, err := s.Settings(ctx)
	if err != nil {
		return nil, "", InstallationMeta{}, err
	}
	out := []Installation{}
	next := ""
	err = db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT mi.driver_id,mi.install_id,mi.tenant_id,mi.platform,mi.app_version,mi.build_number,mi.flavor,mi.first_seen_at,mi.last_seen_at,d.full_name_th,d.first_name,d.last_name,t.name_th,t.name_en,mi.support_page_id
 FROM mobile_installations mi LEFT JOIN drivers d ON d.id=mi.driver_id AND d.tenant_id=mi.tenant_id LEFT JOIN tenants t ON t.id=mi.tenant_id
 WHERE ($1::uuid IS NULL OR mi.tenant_id=$1) AND ($2::timestamptz IS NULL OR mi.last_seen_at >= $2) AND ($3='' OR mi.flavor=$3)
 AND ($4::text[] IS NULL OR coalesce((SELECT v.parts < $4::text[]::numeric[] AND 9007199254740991 >= ALL(v.parts) FROM (SELECT CASE WHEN split_part(btrim(coalesce(mi.app_version,'')),'+',1) ~ '^[0-9]+\.[0-9]+\.[0-9]+$' THEN string_to_array(split_part(btrim(mi.app_version),'+',1),'.')::numeric[] END AS parts) v),false))
 AND ($5::timestamptz IS NULL OR (mi.last_seen_at,mi.driver_id,mi.support_page_id)<($5,$6::uuid,$7::uuid))
 ORDER BY mi.last_seen_at DESC,mi.driver_id DESC,mi.support_page_id DESC LIMIT $8`, in.TenantID, in.Since, in.Flavor, below, seen, after.D, after.ID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		var last platformInstallCursor
		for rows.Next() {
			var r Installation
			var full, first, ln, th, en *string
			var id uuid.UUID
			if err := rows.Scan(&r.DriverID, &r.InstallID, &r.TenantID, &r.Platform, &r.AppVersion, &r.BuildNumber, &r.Flavor, &r.FirstSeenAt, &r.LastSeenAt, &full, &first, &ln, &th, &en, &id); err != nil {
				return err
			}
			if len(out) == limit {
				next = last.encode()
				break
			}
			r.DriverName = driverName(full, first, ln)
			r.TenantName = tenantName(th, en)
			r.VersionStatus = StatusOf(deref(r.AppVersion), gate.LatestVersion, gate.MinAllowedVersion)
			out = append(out, r)
			last = platformInstallCursor{T: r.LastSeenAt, D: r.DriverID, ID: id}
		}
		return rows.Err()
	})
	return out, next, InstallationMeta{Flavor: ptr(gate.Flavor), MinAllowedVersion: ptr(gate.MinAllowedVersion), LatestVersion: ptr(gate.LatestVersion)}, err
}
func platformInstallPermission(p *authz.Principal) error {
	if p == nil || p.IsMachine() || !p.IsPlatform() || !authz.PlatformCaps(p).Has(authz.SecurityViewMobileClients) {
		return authz.ErrPermissionDenied("missing platform read capability")
	}
	return nil
}
