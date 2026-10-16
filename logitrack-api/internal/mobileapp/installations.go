package mobileapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/mobileapp/mobileappdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// MaxInstallationsPerDriver bounds the installation rows of one driver: a heartbeat with a new install id beyond it is
// 409 failed_precondition (too_many_installations), so a driver token cannot fill the table with made-up ids. A
// phone that is reinstalled gets a new id; twenty leaves room for years of reinstalls.
const MaxInstallationsPerDriver = 20

// ActiveWindow is the default window of the installation statistics: an installation not seen for a week is treated
// as gone, not as a driver about to be blocked (the mobile-release page's ACTIVE_WINDOW_DAYS, ADR 0007 §4).
const ActiveWindow = 7 * 24 * time.Hour

// Platforms of mobile_installations.platform.
var platforms = []string{"android", "ios", "other"}

var (
	installIDRx = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	// appVersionRx: what PackageInfo reports, printable ASCII without white space. The version need not parse: an
	// unparseable one is listed as unknown, which is the anomaly the page exists to show.
	appVersionRx  = regexp.MustCompile(`^[\x21-\x7e]{1,32}$`)
	buildNumberRx = regexp.MustCompile(`^[\x21-\x7e]{0,32}$`)
)

// HeartbeatInput is the body of POST /v1/mobile/heartbeat (Appendix B §B.2.21).
type HeartbeatInput struct {
	InstallID   string `json:"installId"`
	Platform    string `json:"platform"`
	AppVersion  string `json:"appVersion"`
	BuildNumber string `json:"buildNumber"`
	Flavor      string `json:"flavor"`
}

func (in HeartbeatInput) validate() error {
	var v []httpx.FieldViolation
	if !installIDRx.MatchString(in.InstallID) {
		v = append(v, violation("installId", "invalid", map[string]any{"pattern": installIDRx.String()}))
	}
	if !contains(platforms, in.Platform) {
		v = append(v, violation("platform", "one_of", map[string]any{"allowed": platforms}))
	}
	if !appVersionRx.MatchString(in.AppVersion) {
		v = append(v, violation("appVersion", "invalid", nil))
	}
	if !buildNumberRx.MatchString(in.BuildNumber) {
		v = append(v, violation("buildNumber", "invalid", nil))
	}
	if !validFlavor(in.Flavor) {
		v = append(v, violation("flavor", "one_of", map[string]any{"allowed": []string{FlavorDev, FlavorProd}}))
	}
	if len(v) > 0 {
		return httpx.ErrInvalidArgument(v...)
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// driverOf returns the driver of a driver principal: the drv claim in the principal's own tenant (C.1.4). Anything
// else (staff, a customer scope, an API key) is 403 driver_profile_required.
func driverOf(p *authz.Principal) (driver, tenant uuid.UUID, err error) {
	if p == nil {
		return uuid.Nil, uuid.Nil, httpx.ErrUnauthenticated()
	}
	if p.IsMachine() || p.TenantRole != authz.Driver || p.DriverID == nil || p.TenantID == nil {
		return uuid.Nil, uuid.Nil, errForbidden(CodeDriverProfileRequired, "")
	}
	return *p.DriverID, *p.TenantID, nil
}

// Heartbeat is POST /v1/mobile/heartbeat: an upsert of the caller's own installation row (driver_id and tenant_id come
// from the token, never from the body; R33), in the driver's request transaction, so RLS (p_driver_insert,
// p_driver_update) refuses any row of another driver. No realtime event: the staff list polls (W7).
func (s *Service) Heartbeat(ctx context.Context, p *authz.Principal, in HeartbeatInput) error {
	driver, tenant, err := driverOf(p)
	if err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	var build *string
	if in.BuildNumber != "" {
		build = &in.BuildNumber
	}
	err = db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error {
		q := mobileappdb.New(tx)
		own, err := q.DriverInstallations(ctx, mobileappdb.DriverInstallationsParams{DriverID: driver, InstallID: in.InstallID})
		if err != nil {
			return err
		}
		if !own.Present && own.Others >= MaxInstallationsPerDriver {
			return errConflict(CodeFailedPrecondition, "too_many_installations", map[string]any{"max": MaxInstallationsPerDriver})
		}
		return q.UpsertInstallation(ctx, mobileappdb.UpsertInstallationParams{
			DriverID: driver, InstallID: in.InstallID, TenantID: tenant, Platform: in.Platform, AppVersion: in.AppVersion,
			BuildNumber: build, Flavor: in.Flavor, SeenAt: s.now(),
		})
	})
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "42501" {
		// RLS refused the row: the principal is not the driver it claims in this tenant (cannot happen with a token
		// the api issued; kept fail-closed).
		return errForbidden(CodeDriverProfileRequired, "row_level_security").Wrap(err)
	}
	return err
}

// --- staff reads ---------------------------------------------------------------------------------------------------

// InstallationQuery is the query of GET /v1/app-installations (and, without the paging fields, of /stats).
type InstallationQuery struct {
	TenantID     *uuid.UUID
	Since        *time.Time
	Flavor       string
	BelowVersion string // a version ParseVersion accepts
	Limit        int    // 1-500, 0 = 50
	Cursor       string
}

// Installation is one row of GET /v1/app-installations. VersionStatus compares the app version with the gate's
// latest release and floor (StatusOf); DriverName is the Thai full name, else first and last name (the priority of
// the web's driverDisplayName), null when the driver row is not readable.
type Installation struct {
	DriverID      uuid.UUID     `json:"driverId"`
	DriverName    *string       `json:"driverName"`
	InstallID     string        `json:"installId"`
	TenantID      uuid.UUID     `json:"tenantId"`
	TenantName    *string       `json:"tenantName"`
	Platform      string        `json:"platform"`
	AppVersion    *string       `json:"appVersion"`
	BuildNumber   *string       `json:"buildNumber"`
	Flavor        string        `json:"flavor"`
	FirstSeenAt   time.Time     `json:"firstSeenAt"`
	LastSeenAt    time.Time     `json:"lastSeenAt"`
	VersionStatus VersionStatus `json:"versionStatus"`
}

// InstallationMeta is the meta of GET /v1/app-installations: the references VersionStatus was computed against.
type InstallationMeta struct {
	Flavor            *string `json:"flavor"`
	MinAllowedVersion *string `json:"minAllowedVersion"`
	LatestVersion     *string `json:"latestVersion"`
}

// installCursor is the keyset position (last_seen_at, driver_id, install_id), opaque base64url JSON.
type installCursor struct {
	T time.Time `json:"t"`
	D uuid.UUID `json:"d"`
	I string    `json:"i"`
}

func (c installCursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeInstallCursor(s string) (installCursor, bool) {
	var c installCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(b, &c) != nil || c.T.IsZero() || c.D == uuid.Nil || c.I == "" {
		return installCursor{}, false
	}
	return c, true
}

// Page sizes of the keyset lists (Appendix B §B.1.5).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

func pageLimit(n int) (int, error) {
	switch {
	case n == 0:
		return DefaultLimit, nil
	case n < 1 || n > MaxLimit:
		return 0, httpx.ErrInvalidArgument(violation("limit", "out_of_range", map[string]any{"min": 1, "max": MaxLimit}))
	}
	return n, nil
}

// versionParts is a parsed version as the text[] the list query compares numerically.
func versionParts(raw string) ([]string, bool) {
	v, ok := ParseVersion(raw)
	if !ok {
		return nil, false
	}
	return []string{strconv.FormatInt(v[0], 10), strconv.FormatInt(v[1], 10), strconv.FormatInt(v[2], 10)}, true
}

// driverName is the display name of a driver row (fullNameTh, then first and last name).
func driverName(full, first, last *string) *string {
	if full != nil && strings.TrimSpace(*full) != "" {
		n := strings.TrimSpace(*full)
		return &n
	}
	n := strings.TrimSpace(strings.TrimSpace(deref(first)) + " " + strings.TrimSpace(deref(last)))
	return ptr(n)
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func tenantName(th, en *string) *string {
	if n := strings.TrimSpace(deref(th)); n != "" {
		return &n
	}
	return ptr(strings.TrimSpace(deref(en)))
}

// Installations is GET /v1/app-installations (security:view_mobile_clients, tenant context): the installations the
// principal reaches under RLS (a carrier's tenant_admin its own tenant, own-fleet staff their carriers too, a
// platform principal every row under X-Act-On-Tenant: *), newest heartbeat first.
func (s *Service) Installations(ctx context.Context, p *authz.Principal, in InstallationQuery) ([]Installation, string, InstallationMeta, error) {
	if p.IsPlatform() {
		return s.platformInstallations(ctx, p, in)
	}
	limit, err := pageLimit(in.Limit)
	if err != nil {
		return nil, "", InstallationMeta{}, err
	}
	params := mobileappdb.ListInstallationsParams{TenantID: in.TenantID, Since: in.Since, Flavor: ptr(in.Flavor), RowLimit: int32(limit + 1)}
	if in.BelowVersion != "" {
		parts, ok := versionParts(in.BelowVersion)
		if !ok {
			return nil, "", InstallationMeta{}, httpx.ErrInvalidArgument(violation("belowVersion", "invalid", nil))
		}
		params.BelowVersion = parts
	}
	if in.Cursor != "" {
		c, ok := decodeInstallCursor(in.Cursor)
		if !ok {
			return nil, "", InstallationMeta{}, httpx.ErrInvalidArgument(violation("cursor", "invalid", nil))
		}
		params.AfterSeen, params.AfterDriver, params.AfterInstall = &c.T, &c.D, &c.I
	}
	gate, err := s.Settings(ctx)
	if err != nil {
		return nil, "", InstallationMeta{}, err
	}
	var rows []mobileappdb.ListInstallationsRow
	read := func(fn func(pgx.Tx) error) error {
		if p.IsPlatform() && !p.IsMachine() {
			return db.WithSystem(ctx, s.pool, nil, fn)
		}
		return db.WithPrincipal(ctx, s.pool, p, fn)
	}
	err = read(func(tx pgx.Tx) error {
		rows, err = mobileappdb.New(tx).ListInstallations(ctx, params)
		return err
	})
	if err != nil {
		return nil, "", InstallationMeta{}, err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = installCursor{T: last.LastSeenAt, D: last.DriverID, I: last.InstallID}.encode()
	}
	out := make([]Installation, len(rows))
	for i, r := range rows {
		out[i] = Installation{
			DriverID: r.DriverID, DriverName: driverName(r.FullNameTh, r.FirstName, r.LastName), InstallID: r.InstallID,
			TenantID: r.TenantID, TenantName: tenantName(r.TenantNameTh, r.TenantNameEn), Platform: r.Platform,
			AppVersion: r.AppVersion, BuildNumber: r.BuildNumber, Flavor: r.Flavor,
			FirstSeenAt: r.FirstSeenAt.UTC(), LastSeenAt: r.LastSeenAt.UTC(),
			VersionStatus: StatusOf(deref(r.AppVersion), gate.LatestVersion, gate.MinAllowedVersion),
		}
	}
	meta := InstallationMeta{Flavor: ptr(gate.Flavor), MinAllowedVersion: ptr(gate.MinAllowedVersion), LatestVersion: ptr(gate.LatestVersion)}
	return out, next, meta, nil
}

// InstallationStats is GET /v1/app-installations/stats: the installations in reach seen since Since, classified
// against the gate (StatusOf; blocked before outdated), and, with ?belowVersion, how many a floor of that version
// would lock out (CountBlockedByFloor: unparseable versions are never counted).
type InstallationStats struct {
	Since             time.Time      `json:"since"`
	Total             int            `json:"total"`
	ByStatus          StatusCounts   `json:"byStatus"`
	ByFlavor          map[string]int `json:"byFlavor"`
	ByPlatform        map[string]int `json:"byPlatform"`
	BelowVersion      *BelowCount    `json:"belowVersion"`
	Flavor            *string        `json:"flavor"`
	MinAllowedVersion *string        `json:"minAllowedVersion"`
	LatestVersion     *string        `json:"latestVersion"`
}

// BelowCount is the impact of a candidate floor.
type BelowCount struct {
	Version string `json:"version"`
	Count   int    `json:"count"`
}

// Stats is GET /v1/app-installations/stats (security:view_mobile_clients, tenant context): one grouped read under the
// principal, classified in Go.
func (s *Service) Stats(ctx context.Context, p *authz.Principal, since time.Time, tenantID *uuid.UUID, below string) (*InstallationStats, error) {
	if below != "" {
		if _, ok := ParseVersion(below); !ok {
			return nil, httpx.ErrInvalidArgument(violation("belowVersion", "invalid", nil))
		}
	}
	gate, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	var rows []mobileappdb.InstallationVersionCountsRow
	if p.IsPlatform() && !authz.PlatformCaps(p).Has(authz.SecurityViewMobileClients) {
		return nil, authz.ErrPermissionDenied("missing platform read capability")
	}
	read := func(fn func(pgx.Tx) error) error {
		if p.IsPlatform() && !p.IsMachine() {
			return db.WithSystem(ctx, s.pool, nil, fn)
		}
		return db.WithPrincipal(ctx, s.pool, p, fn)
	}
	err = read(func(tx pgx.Tx) error {
		rows, err = mobileappdb.New(tx).InstallationVersionCounts(ctx, mobileappdb.InstallationVersionCountsParams{Since: since, TenantID: tenantID})
		return err
	})
	if err != nil {
		return nil, err
	}
	out := &InstallationStats{Since: since.UTC(), ByFlavor: map[string]int{FlavorDev: 0, FlavorProd: 0}, ByPlatform: map[string]int{},
		Flavor: ptr(gate.Flavor), MinAllowedVersion: ptr(gate.MinAllowedVersion), LatestVersion: ptr(gate.LatestVersion)}
	for _, pl := range platforms {
		out.ByPlatform[pl] = 0
	}
	if below != "" {
		out.BelowVersion = &BelowCount{Version: below}
	}
	for _, r := range rows {
		n := int(r.Installs)
		out.Total += n
		out.ByFlavor[r.Flavor] += n
		out.ByPlatform[r.Platform] += n
		out.ByStatus.Add(StatusOf(r.AppVersion, gate.LatestVersion, gate.MinAllowedVersion), n)
		if out.BelowVersion != nil && BlockedByFloor(r.AppVersion, below) {
			out.BelowVersion.Count += n
		}
	}
	return out, nil
}

// blockedSince counts, in a system transaction (every tenant), the installations seen since since that a floor of
// target would lock out: the impact recorded with mobile_floor_changed.
func blockedSince(ctx context.Context, q *mobileappdb.Queries, since time.Time, target string) (int, error) {
	if target == "" {
		return 0, nil
	}
	rows, err := q.InstallationVersionCounts(ctx, mobileappdb.InstallationVersionCountsParams{Since: since})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		if BlockedByFloor(r.AppVersion, target) {
			n += int(r.Installs)
		}
	}
	return n, nil
}
