// Package mobileapp is the driver app's fleet administration (T52, security-center slice pulled forward at the
// owner's request; main spec §9.7, §11.5, ADR 0007, Appendix B §B.2.18, §B.2.21): the installations heartbeat
// (mobile_installations, PK (driver_id, install_id), R33), the anonymous version gate GET /v1/mobile/settings, the
// staff routes of the Security Center pages mobile-clients and mobile-release (internal listener only, R43), and
// the two APK publish routes that cmd/release calls with a release_publisher API key (R82).
//
// settings('mobile_app') keeps the two writers of ADR 0007 apart: PUT /v1/app-releases/floor (people only) writes
// only the floor half (minAllowedVersion*), POST /v1/app-releases only the release half; publishing a build never
// moves the floor. Every write of the document, a release and its APK commit run in one db.WithSystem transaction
// (settings and mobile_app_releases are exempt-service-layer tables, file_objects of the APK a platform object, and
// security_events written by internal/security), after Go authorised the caller; the heartbeat and the staff reads
// of mobile_installations run under db.WithPrincipal, so RLS decides those rows.
package mobileapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/mobileapp/mobileappdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// SettingsKey is the settings row of the version gate.
const SettingsKey = "mobile_app"

// RouteSettingsChanged is the outbox event of a floor change and of a publish (Appendix B §B.4.3): it drops
// cache:settings:mobile_app, reaches open web tabs as SSE mobile_settings.changed on the global topic, and feeds the
// Firestore projection of settings/mobile_app for installed 3.x APKs once the firestore.shadow consumer exists (T24).
const RouteSettingsChanged = "settings.changed"

// Flavors of the driver app (mobile_app_releases.flavor, mobile_installations.flavor).
const (
	FlavorDev  = "dev"
	FlavorProd = "prod"
)

func validFlavor(f string) bool { return f == FlavorDev || f == FlavorProd }

// Stable error codes of this package (Appendix B §B.1.5).
const (
	CodeAlreadyExists         = "already_exists"
	CodeFailedPrecondition    = "failed_precondition"
	CodeDriverProfileRequired = "driver_profile_required"
)

// Storage is what this package needs of internal/storage: the public APK upload and its commit inside the release
// transaction, and the public URL of a committed APK.
type Storage interface {
	PresignPublic(ctx context.Context, in storage.PublicUpload) (*storage.PublicPresigned, error)
	CommitPublic(ctx context.Context, tx pgx.Tx, key, purpose string, ownerID uuid.UUID) (storage.PublicCommitted, error)
	PublicObjectURL(backend, bucket, key string) (string, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool.
	Pool db.Beginner
	// Storage holds the APKs (public bucket, app_releases/); nil refuses the publish routes with 503.
	Storage Storage
	// Cache reads settings('mobile_app') through cache:settings:mobile_app and drops it after a write; optional (the
	// outbox relay drops it too).
	Cache *cache.Cache
	Log   zerolog.Logger
}

// Service serves the routes of this package.
type Service struct {
	pool    db.Beginner
	storage Storage
	cache   *cache.Cache
	log     zerolog.Logger
	now     func() time.Time
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("mobileapp: the service needs a pool")
	}
	return &Service{pool: d.Pool, storage: d.Storage, cache: d.Cache, log: d.Log, now: time.Now}, nil
}

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// --- settings('mobile_app') ------------------------------------------------------------------------------------------

// Settings is the document settings('mobile_app'): the shape of the legacy Firestore settings/mobile_app (Appendix A
// §A.3, Appendix D §D.4.12), which the coexistence projection writes back for installed 3.x APKs (T24). The floor
// half is written only by PUT /v1/app-releases/floor, the release half only by POST /v1/app-releases.
//
// Flavor is the deployment's gate flavor: the flavor of the release half, taken by the first publish of a deployment
// and changed only by a publish that asks for it (switchFlavor). A publish of the other flavor is refused, which
// carries over the legacy script's refusal to publish a build into the other flavor's project (ADR 0007 §3).
type Settings struct {
	MinAllowedVersion      string     `json:"minAllowedVersion,omitempty"`
	MinAllowedVersionSetAt *time.Time `json:"minAllowedVersionSetAt,omitempty"`
	MinAllowedVersionSetBy string     `json:"minAllowedVersionSetBy,omitempty"`

	Flavor            string     `json:"flavor,omitempty"`
	LatestVersion     string     `json:"latestVersion,omitempty"`
	LatestBuildNumber string     `json:"latestBuildNumber,omitempty"`
	APKDownloadURL    string     `json:"apkDownloadUrl,omitempty"`
	APKSha256         string     `json:"apkSha256,omitempty"`
	APKSizeBytes      int64      `json:"apkSizeBytes,omitempty"`
	ReleaseNotes      string     `json:"releaseNotes,omitempty"`
	ReleasedAt        *time.Time `json:"releasedAt,omitempty"`
	ReleasedBy        string     `json:"releasedBy,omitempty"`

	// UpdatedAt is settings.updated_at (not part of the document).
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

// floorKeys are the fields of the floor half; the release writer never names one (ADR 0007).
var floorKeys = []string{"minAllowedVersion", "minAllowedVersionSetAt", "minAllowedVersionSetBy"}

// decodeSettings reads the document tolerantly, as the web's toSettings does: a field of another JSON type (a
// hand-edited or legacy value) reads as absent instead of failing the gate.
func decodeSettings(raw []byte) Settings {
	var m map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return Settings{}
	}
	str := func(k string) string {
		v, _ := m[k].(string)
		return v
	}
	ts := func(k string) *time.Time {
		v, ok := m[k].(string)
		if !ok {
			return nil
		}
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return nil
		}
		t = t.UTC()
		return &t
	}
	var size int64
	if f, ok := m["apkSizeBytes"].(float64); ok && f > 0 && f < 1<<53 {
		size = int64(f)
	}
	return Settings{
		MinAllowedVersion: str("minAllowedVersion"), MinAllowedVersionSetAt: ts("minAllowedVersionSetAt"),
		MinAllowedVersionSetBy: str("minAllowedVersionSetBy"),
		Flavor:                 str("flavor"), LatestVersion: str("latestVersion"), LatestBuildNumber: str("latestBuildNumber"),
		APKDownloadURL: str("apkDownloadUrl"), APKSha256: str("apkSha256"), APKSizeBytes: size,
		ReleaseNotes: str("releaseNotes"), ReleasedAt: ts("releasedAt"), ReleasedBy: str("releasedBy"),
	}
}

// loadSettings reads the document in q's transaction; a missing row is the zero document.
func loadSettings(ctx context.Context, q *mobileappdb.Queries) (Settings, error) {
	row, err := q.GetMobileAppSettings(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	st := decodeSettings(row.Value)
	at := row.UpdatedAt.UTC()
	st.UpdatedAt = &at
	return st, nil
}

// lockSettings reads the document under FOR UPDATE (the two writers serialise on the row); a missing row is the
// zero document (the INSERT of the merge then conflicts with a concurrent first write and waits for it).
func lockSettings(ctx context.Context, q *mobileappdb.Queries) (Settings, error) {
	raw, err := q.LockMobileAppSettings(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	return decodeSettings(raw), nil
}

// Settings returns the document through cache:settings:mobile_app (CACHE_TTL_SETTINGS; dropped by settings.changed).
// The anonymous gate and the staff pages read it; the writers read it under their lock instead.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	load := func(ctx context.Context) (Settings, error) {
		var st Settings
		err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
			var err error
			st, err = loadSettings(ctx, mobileappdb.New(tx))
			return err
		})
		return st, err
	}
	if s.cache == nil {
		return load(ctx)
	}
	return cache.GetJSON(ctx, s.cache, s.cache.Keys().Settings(SettingsKey), s.cache.TTLs().Settings, load)
}

// invalidate drops cache:settings:mobile_app after a committed write; the outbox relay repeats it, so a Redis failure
// is only logged.
func (s *Service) invalidate(ctx context.Context) {
	if s.cache == nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{"key": SettingsKey})
	if err := s.cache.OnEvent(ctx, cache.Event{RoutingKey: RouteSettingsChanged, AggregateID: SettingsKey, Payload: payload}); err != nil {
		s.log.Warn().Err(err).Msg("mobileapp: cache invalidation failed; the outbox relay retries it")
	}
}

// PublicSettings is the body of the anonymous GET /v1/mobile/settings (R42): the non-sensitive fields of the gate for
// one flavor, null where unset. Who set the floor and who published are not in it.
type PublicSettings struct {
	Flavor            *string    `json:"flavor"`
	MinAllowedVersion *string    `json:"minAllowedVersion"`
	LatestVersion     *string    `json:"latestVersion"`
	LatestBuildNumber *string    `json:"latestBuildNumber"`
	APKDownloadURL    *string    `json:"apkDownloadUrl"`
	APKSha256         *string    `json:"apkSha256"`
	APKSizeBytes      *int64     `json:"apkSizeBytes"`
	ReleaseNotes      *string    `json:"releaseNotes"`
	ReleasedAt        *time.Time `json:"releasedAt"`
}

// GateView is the staff view of the document (GET /v1/app-releases meta.settings, PUT /v1/app-releases/floor): the
// public fields, when and by whom the floor was set, who published the latest release, and the row's updated_at.
// minAllowedVersionSetBy and releasedBy are null for a caller without security:manage_mobile_release.
type GateView struct {
	PublicSettings
	MinAllowedVersionSetAt *time.Time `json:"minAllowedVersionSetAt"`
	MinAllowedVersionSetBy *string    `json:"minAllowedVersionSetBy"`
	ReleasedBy             *string    `json:"releasedBy"`
	UpdatedAt              *time.Time `json:"updatedAt"`
}

func ptr[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

// Public is the gate as a driver app of flavor sees it ("" = the gate flavor). A flavor other than the gate's has no
// gate on this deployment: no floor and no release, so the device fails open (ADR 0007 §6) and is never shown a floor
// without its own download link (ADR 0007 §4).
func (st Settings) Public(flavor string) PublicSettings {
	if flavor == "" {
		flavor = st.Flavor
	}
	out := PublicSettings{Flavor: ptr(flavor)}
	if st.Flavor != "" && flavor != st.Flavor {
		return out
	}
	out.MinAllowedVersion, out.LatestVersion = ptr(st.MinAllowedVersion), ptr(st.LatestVersion)
	out.LatestBuildNumber, out.APKDownloadURL = ptr(st.LatestBuildNumber), ptr(st.APKDownloadURL)
	out.APKSha256, out.APKSizeBytes, out.ReleaseNotes = ptr(st.APKSha256), ptr(st.APKSizeBytes), ptr(st.ReleaseNotes)
	out.ReleasedAt = st.ReleasedAt
	return out
}

// View is the staff view; names says whether the people fields (setBy, releasedBy) are shown.
func (st Settings) View(names bool) GateView {
	v := GateView{PublicSettings: st.Public(""), MinAllowedVersionSetAt: st.MinAllowedVersionSetAt, UpdatedAt: st.UpdatedAt}
	if names {
		v.MinAllowedVersionSetBy, v.ReleasedBy = ptr(st.MinAllowedVersionSetBy), ptr(st.ReleasedBy)
	}
	return v
}

// --- errors --------------------------------------------------------------------------------------------------------

func violation(field, reason string, params map[string]any) httpx.FieldViolation {
	return httpx.FieldViolation{Field: field, Reason: reason, Params: params}
}

func errConflict(code, reason string, details map[string]any) *httpx.Error {
	d := map[string]any{"reason": reason}
	for k, v := range details {
		d[k] = v
	}
	return httpx.NewError(http.StatusConflict, code, "conflict").WithDetails(d)
}

func errForbidden(code, reason string) *httpx.Error {
	e := httpx.NewError(http.StatusForbidden, code, "permission denied")
	if reason != "" {
		e = e.WithDetails(map[string]any{"reason": reason})
	}
	return e
}
