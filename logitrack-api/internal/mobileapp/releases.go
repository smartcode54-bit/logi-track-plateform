package mobileapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/mobileapp/mobileappdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Security events of this package (Appendix C §C.4.13): the floor change (warning: a fleet-wide kill switch) and a
// publish (info), both in the change's transaction.
const (
	EventFloorChanged     = "mobile_floor_changed"
	EventReleasePublished = "mobile_release_published"
)

// PurposeAPK is the file_objects purpose of an APK (the only public purpose).
const PurposeAPK = "apk"

// MaxReleaseNotes bounds releaseNotes (characters).
const MaxReleaseNotes = 2000

// APKKey is the immutable public key of a release (main spec §9.2, §9.7): app_releases/{flavor}/logitrack-{flavor}-v{version}.apk.
func APKKey(flavor, version string) string {
	return storage.PublicPrefix + flavor + "/logitrack-" + flavor + "-v" + version + ".apk"
}

var (
	buildNumberStrictRx = regexp.MustCompile(`^[0-9]{1,9}$`)
	sha256Rx            = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ReleaseInput is the body of POST /v1/app-releases/presign and POST /v1/app-releases: the build cmd/release
// publishes. Force (presign) replaces an upload of the same version that was never published; SwitchFlavor (both)
// makes this flavor the deployment's gate flavor; ReleaseNotes (publish) are shown on the mobile-release page and to
// the driver.
type ReleaseInput struct {
	Flavor       string  `json:"flavor"`
	Version      string  `json:"version"`
	BuildNumber  string  `json:"buildNumber"`
	SHA256       string  `json:"sha256"`
	SizeBytes    int64   `json:"sizeBytes"`
	ReleaseNotes *string `json:"releaseNotes"`
	Force        bool    `json:"force"`
	SwitchFlavor bool    `json:"switchFlavor"`
}

func (in ReleaseInput) validate() error {
	var v []httpx.FieldViolation
	if !validFlavor(in.Flavor) {
		v = append(v, violation("flavor", "one_of", map[string]any{"allowed": []string{FlavorDev, FlavorProd}}))
	}
	if !ValidReleaseVersion(in.Version) {
		v = append(v, violation("version", "invalid", map[string]any{"pattern": releaseVersionRx.String()}))
	}
	if !buildNumberStrictRx.MatchString(in.BuildNumber) {
		v = append(v, violation("buildNumber", "invalid", map[string]any{"pattern": buildNumberStrictRx.String()}))
	}
	if !sha256Rx.MatchString(in.SHA256) {
		v = append(v, violation("sha256", "invalid", nil))
	}
	if in.SizeBytes < 1 || in.SizeBytes > storage.MaxReleaseBytes {
		v = append(v, violation("sizeBytes", "out_of_range", map[string]any{"min": 1, "max": storage.MaxReleaseBytes}))
	}
	if n := in.ReleaseNotes; n != nil && (utf8.RuneCountInString(*n) > MaxReleaseNotes || !utf8.ValidString(*n) || strings.ContainsRune(*n, 0)) {
		v = append(v, violation("releaseNotes", "invalid", map[string]any{"max": MaxReleaseNotes}))
	}
	if len(v) > 0 {
		return httpx.ErrInvalidArgument(v...)
	}
	return nil
}

// gateRefusal is the 409 of a publish into the other flavor of the deployment (ADR 0007 §3: the legacy script
// refused a build of the other project); "" when cur allows flavor.
func gateRefusal(cur Settings, flavor string, switchFlavor bool) error {
	if cur.Flavor == "" || cur.Flavor == flavor || switchFlavor {
		return nil
	}
	return errConflict(CodeFailedPrecondition, "flavor_mismatch", map[string]any{"flavor": cur.Flavor, "requested": flavor})
}

func errPublished(flavor, version string) error {
	return errConflict(CodeAlreadyExists, "published", map[string]any{"flavor": flavor, "version": version})
}

// actor is who calls a release route: a person (users row, its email) or a release_publisher API key (its name).
type actor struct {
	user  *uuid.UUID
	email string
	key   *uuid.UUID
	label string // released_by / minAllowedVersionSetBy: the email, or "apikey:{name}"
}

func (s *Service) actorOf(ctx context.Context, q *mobileappdb.Queries, p *authz.Principal) (actor, error) {
	if p.IsMachine() {
		id := p.UserID
		if p.APIKeyID != nil {
			id = *p.APIKeyID
		}
		name, err := q.APIKeyName(ctx, id)
		if err != nil {
			return actor{}, fmt.Errorf("mobileapp: api key name: %w", err)
		}
		return actor{key: &id, label: "apikey:" + name}, nil
	}
	uid := p.UserID
	email, err := q.UserEmail(ctx, uid)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return actor{}, err
	}
	label := email
	if label == "" {
		label = uid.String()
	}
	return actor{user: &uid, email: email, label: label}, nil
}

// Presign is POST /v1/app-releases/presign: the upload of an APK into the public bucket under its immutable key.
// Refused with 409: a version that is already published (already_exists, published; never replaced, force or not),
// an unpublished upload of it without force (already_exists, upload_pending), the other flavor of the deployment
// (failed_precondition, flavor_mismatch). The answer is the upload (method, url, headers) and the URL the APK is
// served at once uploaded (publicUrl), which cmd/release checks before it publishes.
func (s *Service) Presign(ctx context.Context, p *authz.Principal, in ReleaseInput) (*storage.PublicPresigned, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if s.storage == nil {
		return nil, httpx.ErrUnavailable("object storage is not configured")
	}
	var uploader *uuid.UUID
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		q := mobileappdb.New(tx)
		cur, err := loadSettings(ctx, q)
		if err != nil {
			return err
		}
		if err := gateRefusal(cur, in.Flavor, in.SwitchFlavor); err != nil {
			return err
		}
		if _, err := q.GetRelease(ctx, mobileappdb.GetReleaseParams{Flavor: in.Flavor, Version: in.Version}); err == nil {
			return errPublished(in.Flavor, in.Version)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !p.IsMachine() {
			uid := p.UserID
			uploader = &uid
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out, err := s.storage.PresignPublic(ctx, storage.PublicUpload{
		Key: APKKey(in.Flavor, in.Version), Purpose: PurposeAPK, ContentType: storage.ReleaseContentType,
		SizeBytes: in.SizeBytes, SHA256: in.SHA256, UploadedBy: uploader, Replace: in.Force,
		Metadata: map[string]string{"sha256": in.SHA256, "build-number": in.BuildNumber, "flavor": in.Flavor, "version": in.Version},
	})
	var pending *storage.PendingUploadError
	switch {
	case errors.Is(err, storage.ErrCommitted):
		return nil, errPublished(in.Flavor, in.Version)
	case errors.As(err, &pending):
		return nil, errConflict(CodeAlreadyExists, "upload_pending", map[string]any{"flavor": in.Flavor, "version": in.Version,
			"expiresAt": pending.ExpiresAt})
	case err != nil:
		return nil, err
	}
	return out, nil
}

// Release is one row of mobile_app_releases as the release routes return it. APKURL is the public URL of the APK on
// its own backend; Latest says it is the gate's current release; ReleasedBy is null for a caller without
// security:manage_mobile_release.
type Release struct {
	ID             uuid.UUID `json:"id"`
	Flavor         string    `json:"flavor"`
	Version        string    `json:"version"`
	BuildNumber    string    `json:"buildNumber"`
	APKKey         string    `json:"apkKey"`
	APKURL         *string   `json:"apkUrl"`
	APKSizeBytes   int64     `json:"apkSizeBytes"`
	APKSha256      string    `json:"apkSha256"`
	ReleaseNotes   *string   `json:"releaseNotes"`
	ReleasedAt     time.Time `json:"releasedAt"`
	ReleasedBy     *string   `json:"releasedBy"`
	StorageBackend string    `json:"storageBackend"`
	Latest         bool      `json:"latest"`
}

// releaseRow is the shape GetRelease and ListReleases share.
type releaseRow struct {
	ID                                   uuid.UUID
	Flavor, Version, BuildNumber         string
	ApkSizeBytes                         int64
	ApkSha256                            string
	ReleaseNotes                         *string
	ReleasedAt                           time.Time
	ReleasedBy, ObjectKey, Bucket, Store string
}

func (s *Service) release(r releaseRow, gate Settings, names bool) Release {
	out := Release{ID: r.ID, Flavor: r.Flavor, Version: r.Version, BuildNumber: r.BuildNumber, APKKey: r.ObjectKey,
		APKSizeBytes: r.ApkSizeBytes, APKSha256: r.ApkSha256, ReleaseNotes: r.ReleaseNotes, ReleasedAt: r.ReleasedAt.UTC(),
		StorageBackend: r.Store, Latest: gate.Flavor == r.Flavor && gate.LatestVersion == r.Version}
	if s.storage != nil {
		if u, err := s.storage.PublicObjectURL(r.Store, r.Bucket, r.ObjectKey); err == nil {
			out.APKURL = &u
		}
	}
	if names {
		out.ReleasedBy = ptr(r.ReleasedBy)
	}
	return out
}

// PublishResult is the answer of POST /v1/app-releases: the release, whether it became the gate's latest (an older
// version, or another flavor's, is recorded without moving the gate) and whether the request replayed a publish that
// had already committed (the same build: 200 instead of 201, nothing written).
type PublishResult struct {
	Release  Release
	Latest   bool
	Replayed bool
}

// Publish is POST /v1/app-releases: in one system transaction, after the caller was authorised, it verifies the
// uploaded APK (HEAD: size and type; sha256 from the local sidecar, or read back from S3) and commits its public
// file_objects row, inserts the mobile_app_releases row, moves the release half of settings('mobile_app') when the
// version is the gate flavor's newest, and appends mobile_release_published and the outbox settings.changed. It
// never writes minAllowedVersion (ADR 0007): the floor moves only through PUT /v1/app-releases/floor, which takes no
// API key, so a person always presses that button.
func (s *Service) Publish(ctx context.Context, p *authz.Principal, in ReleaseInput, requestID string) (*PublishResult, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if s.storage == nil {
		return nil, httpx.ErrUnavailable("object storage is not configured")
	}
	now := s.now().UTC()
	key := APKKey(in.Flavor, in.Version)
	var res PublishResult
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		q := mobileappdb.New(tx)
		cur, err := lockSettings(ctx, q)
		if err != nil {
			return err
		}
		if err := gateRefusal(cur, in.Flavor, in.SwitchFlavor); err != nil {
			return err
		}
		names := true
		if old, err := q.GetRelease(ctx, mobileappdb.GetReleaseParams{Flavor: in.Flavor, Version: in.Version}); err == nil {
			// A retried publish whose first answer was lost: the same build is a replay, anything else a conflict.
			if old.ApkSha256 != in.SHA256 || old.BuildNumber != in.BuildNumber || old.ApkSizeBytes != in.SizeBytes {
				return errPublished(in.Flavor, in.Version)
			}
			res.Replayed = true
			res.Release = s.release(releaseRow{ID: old.ID, Flavor: old.Flavor, Version: old.Version, BuildNumber: old.BuildNumber,
				ApkSizeBytes: old.ApkSizeBytes, ApkSha256: old.ApkSha256, ReleaseNotes: old.ReleaseNotes, ReleasedAt: old.ReleasedAt,
				ReleasedBy: old.ReleasedBy, ObjectKey: old.ObjectKey, Bucket: old.Bucket, Store: old.StorageBackend}, cur, names)
			res.Latest = res.Release.Latest
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		who, err := s.actorOf(ctx, q, p)
		if err != nil {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		f, err := s.storage.CommitPublic(ctx, tx, key, PurposeAPK, id)
		if err != nil {
			return err
		}
		// The upload was declared at presign; the publish must describe the same bytes.
		var v []httpx.FieldViolation
		if f.SHA256 != in.SHA256 {
			v = append(v, violation("sha256", "checksum_mismatch", map[string]any{"key": key}))
		}
		if f.SizeBytes != in.SizeBytes {
			v = append(v, violation("sizeBytes", "size_mismatch", map[string]any{"key": key, "actual": f.SizeBytes}))
		}
		if len(v) > 0 {
			return httpx.ErrInvalidArgument(v...)
		}
		url, err := s.storage.PublicObjectURL(f.StorageBackend, f.Bucket, key)
		if err != nil {
			return httpx.ErrUnavailable("the storage backend has no public base URL").
				WithDetails(map[string]any{"reason": "public_url_unconfigured"}).Wrap(err)
		}
		if err := q.InsertRelease(ctx, mobileappdb.InsertReleaseParams{
			ID: id, Flavor: in.Flavor, Version: in.Version, BuildNumber: in.BuildNumber, ApkFileID: f.ID,
			ApkSizeBytes: f.SizeBytes, ApkSha256: f.SHA256, ReleaseNotes: in.ReleaseNotes, ReleasedAt: now, ReleasedBy: who.label,
		}); err != nil {
			return err
		}
		res.Latest = becomesLatest(cur, in)
		switched := res.Latest && cur.Flavor != "" && cur.Flavor != in.Flavor
		if res.Latest {
			half := releaseHalf(in, url, f.SizeBytes, now, who.label)
			b, err := json.Marshal(half)
			if err != nil {
				return err
			}
			if err := q.MergeMobileAppSettings(ctx, mobileappdb.MergeMobileAppSettingsParams{Half: b, UpdatedBy: who.user}); err != nil {
				return err
			}
			cur.Flavor, cur.LatestVersion = in.Flavor, in.Version
		}
		details := map[string]any{"flavor": in.Flavor, "version": in.Version, "buildNumber": in.BuildNumber,
			"sha256": f.SHA256, "sizeBytes": f.SizeBytes, "key": key, "storageBackend": f.StorageBackend, "latest": res.Latest,
			"flavorSwitched": switched}
		if who.key != nil {
			details["apiKeyId"] = who.key.String()
			details["apiKeyName"] = strings.TrimPrefix(who.label, "apikey:")
		}
		// The row carries the actor's effective tenant (own-fleet staff read it in their audit feed, as T51's IAM rows);
		// a platform key's or a tenant-less platform admin's publish is platform-level (X-Act-On-Tenant: *).
		ev := security.Event{EventType: EventReleasePublished, Severity: security.SeverityInfo,
			Summary: fmt.Sprintf("driver app %s %s (build %s) published", in.Flavor, in.Version, in.BuildNumber),
			Details: details, ActorUserID: who.user, TenantID: p.EffectiveTenant(), RequestID: requestID, OccurredAt: now}
		if who.key != nil {
			ev.ActorEmail = &who.label
		}
		if err := security.Append(ctx, tx, ev); err != nil {
			return err
		}
		if err := emitChanged(ctx, tx, in.Flavor, requestID); err != nil {
			return err
		}
		res.Release = s.release(releaseRow{ID: id, Flavor: in.Flavor, Version: in.Version, BuildNumber: in.BuildNumber,
			ApkSizeBytes: f.SizeBytes, ApkSha256: f.SHA256, ReleaseNotes: in.ReleaseNotes, ReleasedAt: now, ReleasedBy: who.label,
			ObjectKey: key, Bucket: f.Bucket, Store: f.StorageBackend}, cur, names)
		return nil
	})
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "23505" {
		return nil, errPublished(in.Flavor, in.Version) // a concurrent publish of the same version won
	}
	if err != nil {
		return nil, err
	}
	if !res.Replayed {
		s.invalidate(ctx)
	}
	return &res, nil
}

// becomesLatest decides whether a new release moves the gate's release half: a release of the gate flavor (or the
// first of a deployment) whose version is not below the gate's latest, or any release that switches the gate's
// flavor. An older version of the gate flavor (a late hotfix of an older line) is recorded without becoming the
// download every driver is sent to. The same version as the gate's latest can only be published when the document
// names a release this database has no row of (a legacy or hand-made document): its row takes over.
func becomesLatest(cur Settings, in ReleaseInput) bool {
	if cur.Flavor != "" && cur.Flavor != in.Flavor {
		return in.SwitchFlavor
	}
	c, ok := CompareVersions(in.Version, cur.LatestVersion)
	return !ok || c >= 0
}

// releaseHalf is the release writer's half of settings('mobile_app'): never a floor field (ADR 0007; a test pins it).
func releaseHalf(in ReleaseInput, url string, size int64, at time.Time, by string) map[string]any {
	notes := ""
	if in.ReleaseNotes != nil {
		notes = *in.ReleaseNotes
	}
	return map[string]any{
		"flavor": in.Flavor, "latestVersion": in.Version, "latestBuildNumber": in.BuildNumber, "apkDownloadUrl": url,
		"apkSha256": in.SHA256, "apkSizeBytes": size, "releaseNotes": notes, "releasedAt": at.UTC().Format(time.RFC3339Nano),
		"releasedBy": by,
	}
}

// emitChanged appends the outbox settings.changed of the gate (Appendix B §B.4.3: {key, flavor}).
func emitChanged(ctx context.Context, tx pgx.Tx, flavor, requestID string) error {
	var headers map[string]string
	if requestID != "" {
		headers = map[string]string{"requestId": requestID}
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{
		RoutingKey: RouteSettingsChanged, AggregateType: "settings", AggregateID: SettingsKey,
		Payload: map[string]any{"key": SettingsKey, "flavor": flavor}, Topics: []string{realtime.TopicGlobal}, Headers: headers,
	})
	return err
}

// releaseCursor is the keyset position (released_at, id), opaque base64url JSON.
type releaseCursor struct {
	T  time.Time `json:"t"`
	ID uuid.UUID `json:"id"`
}

// ReleasesMeta is the meta of GET /v1/app-releases: the gate document (staff view).
type ReleasesMeta struct {
	Settings GateView `json:"settings"`
}

// Releases is GET /v1/app-releases (security:manage_mobile_release | security:view_mobile_clients): the published
// builds, newest first, with the gate document in meta. Releases are platform-level, so no tenant context is needed;
// who published and who set the floor are shown to security:manage_mobile_release holders only.
func (s *Service) Releases(ctx context.Context, p *authz.Principal, flavor string, limit int, cursor string) ([]Release, string, ReleasesMeta, error) {
	n, err := pageLimit(limit)
	if err != nil {
		return nil, "", ReleasesMeta{}, err
	}
	if flavor != "" && !validFlavor(flavor) {
		return nil, "", ReleasesMeta{}, httpx.ErrInvalidArgument(violation("flavor", "one_of", map[string]any{"allowed": []string{FlavorDev, FlavorProd}}))
	}
	params := mobileappdb.ListReleasesParams{Flavor: ptr(flavor), RowLimit: int32(n + 1)}
	if cursor != "" {
		var c releaseCursor
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(b, &c) != nil || c.T.IsZero() || c.ID == uuid.Nil {
			return nil, "", ReleasesMeta{}, httpx.ErrInvalidArgument(violation("cursor", "invalid", nil))
		}
		params.AfterAt, params.AfterID = &c.T, &c.ID
	}
	names := p.Can(authz.SecurityManageMobileRel)
	var rows []mobileappdb.ListReleasesRow
	var gate Settings
	err = db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		q := mobileappdb.New(tx)
		var err error
		if gate, err = loadSettings(ctx, q); err != nil {
			return err
		}
		rows, err = q.ListReleases(ctx, params)
		return err
	})
	if err != nil {
		return nil, "", ReleasesMeta{}, err
	}
	next := ""
	if len(rows) > n {
		rows = rows[:n]
		last := rows[n-1]
		b, _ := json.Marshal(releaseCursor{T: last.ReleasedAt, ID: last.ID})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	out := make([]Release, len(rows))
	for i, r := range rows {
		out[i] = s.release(releaseRow{ID: r.ID, Flavor: r.Flavor, Version: r.Version, BuildNumber: r.BuildNumber,
			ApkSizeBytes: r.ApkSizeBytes, ApkSha256: r.ApkSha256, ReleaseNotes: r.ReleaseNotes, ReleasedAt: r.ReleasedAt,
			ReleasedBy: r.ReleasedBy, ObjectKey: r.ObjectKey, Bucket: r.Bucket, Store: r.StorageBackend}, gate, names)
	}
	return out, next, ReleasesMeta{Settings: gate.View(names)}, nil
}

// FloorInput is the body of PUT /v1/app-releases/floor: a canonical version, or null to remove the floor (devices that
// read no floor clear their cached one, ADR 0007 §6, so the lever is two-way).
type FloorInput struct {
	MinAllowedVersion json.RawMessage `json:"minAllowedVersion"`
}

// FloorResult is the answer of PUT /v1/app-releases/floor: the gate, whether it changed, and how many installations
// seen in the last 7 days (every tenant) the new floor locks out.
type FloorResult struct {
	Settings  GateView
	Changed   bool
	Blocked7d int
}

// SetFloor is PUT /v1/app-releases/floor (security:manage_mobile_release, a steward key; people only: an API key is
// refused, so publishing a build can never move the floor, ADR 0007). A floor needs the gate's download link (a floor
// without one leaves every blocked driver on a dialog with no button, ADR 0007 §4: 409 failed_precondition
// no_apk_url) and may not exceed the latest published version (every device, the newest included, would be blocked
// and sent to an APK that is itself below the floor: 422 above_latest). Only the floor half of settings('mobile_app')
// is written, with mobile_floor_changed (the impact in details) and the outbox settings.changed, in one transaction.
func (s *Service) SetFloor(ctx context.Context, p *authz.Principal, in FloorInput, requestID string) (*FloorResult, error) {
	if p == nil {
		return nil, httpx.ErrUnauthenticated()
	}
	if p.IsMachine() {
		return nil, authz.ErrPermissionDenied("the version floor is set by a person").WithDetails(map[string]any{"reason": "api_key_not_allowed"})
	}
	var target string
	switch strings.TrimSpace(string(in.MinAllowedVersion)) {
	case "":
		return nil, httpx.ErrInvalidArgument(violation("minAllowedVersion", "required", nil))
	case "null":
	default:
		if json.Unmarshal(in.MinAllowedVersion, &target) != nil || !ValidReleaseVersion(target) {
			return nil, httpx.ErrInvalidArgument(violation("minAllowedVersion", "invalid", map[string]any{"pattern": releaseVersionRx.String()}))
		}
	}
	now := s.now().UTC()
	var res FloorResult
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		q := mobileappdb.New(tx)
		cur, err := lockSettings(ctx, q)
		if err != nil {
			return err
		}
		if target != "" {
			if strings.TrimSpace(cur.APKDownloadURL) == "" {
				return errConflict(CodeFailedPrecondition, "no_apk_url", map[string]any{"flavor": cur.Flavor})
			}
			if c, ok := CompareVersions(target, cur.LatestVersion); ok && c > 0 {
				return httpx.ErrInvalidArgument(violation("minAllowedVersion", "above_latest", map[string]any{"latestVersion": cur.LatestVersion}))
			}
		}
		if res.Blocked7d, err = blockedSince(ctx, q, now.Add(-ActiveWindow), target); err != nil {
			return err
		}
		if target == cur.MinAllowedVersion {
			return nil // nothing to write, no event
		}
		who, err := s.actorOf(ctx, q, p)
		if err != nil {
			return err
		}
		var floor any
		if target != "" {
			floor = target
		}
		b, err := json.Marshal(map[string]any{"minAllowedVersion": floor, "minAllowedVersionSetAt": now.Format(time.RFC3339Nano),
			"minAllowedVersionSetBy": who.label})
		if err != nil {
			return err
		}
		if err := q.MergeMobileAppSettings(ctx, mobileappdb.MergeMobileAppSettingsParams{Half: b, UpdatedBy: who.user}); err != nil {
			return err
		}
		res.Changed = true
		summary := fmt.Sprintf("driver app version floor set to %s", target)
		if target == "" {
			summary = "driver app version floor removed"
		}
		if err := security.Append(ctx, tx, security.Event{EventType: EventFloorChanged, Severity: security.SeverityWarning,
			Summary: summary, ActorUserID: who.user, TenantID: p.EffectiveTenant(), RequestID: requestID, OccurredAt: now,
			Details: map[string]any{"from": ptr(cur.MinAllowedVersion), "to": ptr(target), "flavor": ptr(cur.Flavor),
				"latestVersion": ptr(cur.LatestVersion), "apkDownloadUrl": ptr(cur.APKDownloadURL), "blocked7d": res.Blocked7d}}); err != nil {
			return err
		}
		return emitChanged(ctx, tx, cur.Flavor, requestID)
	})
	if err != nil {
		return nil, err
	}
	if res.Changed {
		s.invalidate(ctx)
	}
	st, err := s.readSettings(ctx)
	if err != nil {
		return nil, err
	}
	res.Settings = st.View(true)
	return &res, nil
}

// readSettings reads the document past the cache (the writer's own answer reflects its write).
func (s *Service) readSettings(ctx context.Context) (Settings, error) {
	var st Settings
	err := db.WithSystem(ctx, s.pool, nil, func(tx pgx.Tx) error {
		var err error
		st, err = loadSettings(ctx, mobileappdb.New(tx))
		return err
	})
	return st, err
}
