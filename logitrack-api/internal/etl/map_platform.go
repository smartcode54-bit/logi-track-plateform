package etl

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// Platform rows (Appendix A §A.3.10): settings and metadata, security events, the anonymous landing forms.

var (
	settingKey   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	strictSemver = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	buildNumber  = regexp.MustCompile(`^[0-9]+$`)
	sha256Hex    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// putSetting upserts settings(key) with a plain-JSON value.
func (c *docCtx) putSetting(key string, value any) error {
	at := c.ts("updatedAt")
	if at == nil {
		t := c.doc.UpdateTime.Truncate(time.Microsecond)
		at = &t
	}
	if _, err := c.tx.Exec(c.ctx, `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value, *at); err != nil {
		return fmt.Errorf("etl: %s: setting %s: %w", c.doc.Path, key, err)
	}
	c.table, c.target = "settings", key
	return nil
}

// loadMetadata: metadata/{key} -> settings(key) (§A.3.3: metadata/distances_last_calculated). A document whose id is
// not a settings key is dropped with unknown_collection (outside the mapping).
func loadMetadata(c *docCtx) error {
	if !settingKey.MatchString(c.doc.ID) {
		c.outcome = Dropped
		c.find("", ReasonUnknownCollection, "metadata/"+c.doc.ID+" is not a settings key of the mapping", nil)
		return nil
	}
	return c.putSetting(c.doc.ID, plain(c.f))
}

// loadSetting: settings/{id} (§A.3.10). settings/tenancy names the own fleet (read by ensureOwnFleet before any
// collection) and maps to the own-fleet tenant; settings/mobile_app is split between its two writers (ADR 0007): the
// floor half and the release half in settings('mobile_app'), and one mobile_app_releases row for the release the
// document describes, whose APK becomes a public file_objects row in S3_PUBLIC_BUCKET (purpose apk, the only public
// purpose). apkDownloadUrl keeps the legacy URL until `etl rewrite-urls --apply` points it at S3_PUBLIC_BASE_URL. Any
// other document is settings(id) as plain JSON.
func loadSetting(c *docCtx) error {
	switch c.doc.ID {
	case "tenancy":
		c.table, c.target, c.tenantSource = "tenants", c.ownFleet().String(), tenancy.SourceSelf
		return nil
	case "mobile_app":
		return loadMobileAppSettings(c)
	}
	if !settingKey.MatchString(c.doc.ID) {
		c.outcome = Dropped
		c.find("", ReasonUnknownCollection, "settings/"+c.doc.ID+" is not a settings key of the mapping", nil)
		return nil
	}
	return c.putSetting(c.doc.ID, plain(c.f))
}

func loadMobileAppSettings(c *docCtx) error {
	v := map[string]any{}
	str := func(k string) {
		if s := c.text(k); s != nil {
			v[k] = *s
		}
	}
	at := func(k string) *time.Time {
		t := c.ts(k)
		if t != nil {
			v[k] = t.UTC().Format(time.RFC3339Nano)
		}
		return t
	}
	str("minAllowedVersion")
	at("minAllowedVersionSetAt")
	str("minAllowedVersionSetBy")
	str("latestVersion")
	str("latestBuildNumber")
	str("apkDownloadUrl")
	str("apkSha256")
	str("releaseNotes")
	str("releasedBy")
	released := at("releasedAt")
	size := c.integer("apkSizeBytes")
	if size != nil && *size > 0 {
		v["apkSizeBytes"] = *size
	}
	flavor, ok := lowerVocab(c.get("flavor"), "dev", "prod")
	if !ok {
		c.find("flavor", ReasonStatusOutOfVocab, "neither dev nor prod", c.get("flavor"))
		flavor = ""
	}
	if flavor != "" {
		v["flavor"] = flavor
	}
	for _, k := range []string{"minAllowedVersion", "latestVersion"} {
		if s, ok := v[k].(string); ok && !strictSemver.MatchString(s) {
			c.find(k, ReasonStatusOutOfVocab, "not strict MAJOR.MINOR.PATCH (pub_semver gate)", s)
		}
	}
	if err := c.putSetting("mobile_app", v); err != nil {
		return err
	}
	// The release row needs the whole release half; a document from before the first publish has none.
	version, _ := v["latestVersion"].(string)
	build, _ := v["latestBuildNumber"].(string)
	sum, _ := v["apkSha256"].(string)
	by, _ := v["releasedBy"].(string)
	url := c.str("apkDownloadUrl")
	if !strictSemver.MatchString(version) || !buildNumber.MatchString(build) || !sha256Hex.MatchString(sum) || size == nil ||
		*size <= 0 || released == nil || flavor == "" || url == "" || by == "" {
		return nil
	}
	var relID uuid.UUID
	err := c.tx.QueryRow(c.ctx, `SELECT id FROM mobile_app_releases WHERE flavor = $1 AND version = $2`, flavor, version).Scan(&relID)
	switch {
	case err == nil:
		return nil // a published release is never replaced
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	if relID, err = uuid.NewV7(); err != nil {
		return err
	}
	fid, err := c.publicFileRef("apkDownloadUrl", url, "release", relID)
	if err != nil || fid == nil {
		return err
	}
	if _, err := c.tx.Exec(c.ctx, `INSERT INTO mobile_app_releases (id, flavor, version, build_number, apk_file_id, apk_size_bytes, apk_sha256,
		release_notes, released_at, released_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		relID, flavor, version, build, *fid, *size, sum, c.text("releaseNotes"), *released, by); err != nil {
		return fmt.Errorf("etl: %s: release: %w", c.doc.Path, err)
	}
	return nil
}

// publicFileRef registers an APK under S3_PUBLIC_BUCKET (visibility public, purpose apk, no tenant, R23).
func (c *docCtx) publicFileRef(field, raw, ownerKind string, ownerID uuid.UUID) (*uuid.UUID, error) {
	_, key, legacy, ok := ParseStorageURL(raw)
	if !ok {
		c.find(field, ReasonURLUnparseable, "not a Firebase Storage download URL", raw)
		return nil, nil
	}
	var id uuid.UUID
	err := c.tx.QueryRow(c.ctx, `WITH ins AS (
		  INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, owner_kind, owner_id, visibility, status, legacy_url, storage_backend)
		  VALUES ($1, $2, NULL, 'apk', $3, $4, 'public', 'missing_at_source', $5, 's3')
		  ON CONFLICT (bucket, object_key) DO NOTHING RETURNING id)
		SELECT id FROM ins UNION ALL SELECT id FROM file_objects WHERE bucket = $1 AND object_key = $2 LIMIT 1`,
		c.e.cfg.PublicBucket, key, ownerKind, ownerID, legacy).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("etl: %s: register %s: %w", c.doc.Path, field, err)
	}
	return &id, nil
}

// loadSecurityEvent: security_events (§A.3.10): append-only, so a re-applied document leaves its row; actorUid ->
// actor_user_id, else actor_legacy_uid + user_unresolved; details.targetUid -> target_user_id when it resolves;
// type -> event_type; tenant_id NULL (legacy rows are platform-level).
func loadSecurityEvent(c *docCtx) error {
	typ := c.str("type")
	if typ == "" {
		typ = c.str("eventType")
	}
	if typ == "" {
		return c.reject("type", ReasonMissingRequired, "security_events.event_type is NOT NULL", nil)
	}
	if !settingKey.MatchString(typ) {
		return c.reject("type", ReasonStatusOutOfVocab, "event_type is snake_case", typ)
	}
	sev, ok := lowerVocab(c.get("severity"), "info", "warning", "critical")
	if !ok {
		return c.reject("severity", ReasonStatusOutOfVocab, "neither info, warning nor critical", c.get("severity"))
	}
	if sev == "" {
		sev = "info"
	}
	summary := c.text("summary")
	if summary == nil {
		s := typ
		summary = &s
	}
	actor, err := c.user("actorUid", c.str("actorUid"))
	if err != nil {
		return err
	}
	var legacyActor *string
	if actor == nil {
		legacyActor = c.text("actorUid")
	}
	details, _ := plain(c.get("details")).(map[string]any)
	if details == nil {
		details = map[string]any{}
	}
	var target *uuid.UUID
	if t, ok := details["targetUid"].(string); ok {
		if target, err = c.userByUID(t); err != nil {
			return err
		}
	}
	at := c.ts("createdAt")
	if at == nil {
		at = c.ts("timestamp")
	}
	if at == nil {
		t := c.doc.CreateTime
		if t.IsZero() {
			t = c.doc.UpdateTime
		}
		t = t.Truncate(time.Microsecond)
		at = &t
	}
	var id uuid.UUID
	err = c.tx.QueryRow(c.ctx, `WITH ins AS (
		  INSERT INTO security_events (legacy_doc_id, created_at, event_type, severity, summary, details, actor_user_id, actor_email,
		    actor_legacy_uid, target_user_id, tenant_id)
		  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULL)
		  ON CONFLICT (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL DO NOTHING RETURNING id)
		SELECT id FROM ins UNION ALL SELECT id FROM security_events WHERE legacy_doc_id = $1 LIMIT 1`,
		c.doc.ID, *at, typ, sev, *summary, details, actor, c.text("actorEmail"), legacyActor, target).Scan(&id)
	if err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	c.done("security_events", id)
	return nil
}

// loadWaitlist: waitlist (§A.3.10; anonymous landing form). email and name are NOT NULL.
func loadWaitlist(c *docCtx) error {
	email, name := c.text("email"), c.text("name")
	if email == nil {
		return c.reject("email", ReasonMissingRequired, "waitlist.email is NOT NULL", nil)
	}
	if name == nil {
		return c.reject("name", ReasonMissingRequired, "waitlist.name is NOT NULL", nil)
	}
	r := newRow("waitlist").set("email", strings.TrimSpace(*email)).set("name", name).setPresent("country_code", c.text("countryCode")).
		set("phone", c.text("phone")).setPresent("created_at", c.ts("createdAt"))
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	c.done("waitlist", id)
	return nil
}

// loadPartnerInterest: partner-interest -> partner_interest(payload) (§A.3.10): the field set is UNVERIFIED (in no
// schema), so the whole document is the payload, as plain JSON.
func loadPartnerInterest(c *docCtx) error {
	r := newRow("partner_interest").set("payload", plain(c.f)).setPresent("created_at", c.ts("createdAt"))
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	c.done("partner_interest", id)
	return nil
}
