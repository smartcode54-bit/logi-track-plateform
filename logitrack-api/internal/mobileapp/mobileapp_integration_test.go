//go:build integration

package mobileapp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/mobileapp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/release"
)

// heartbeat posts POST /v1/mobile/heartbeat on the public listener (the driver app's only listener).
func (w *world) heartbeat(tok, install, version string) resp {
	w.t.Helper()
	return w.call(w.public, http.MethodPost, "/v1/mobile/heartbeat", tok,
		map[string]any{"installId": install, "platform": "android", "appVersion": version, "buildNumber": "1", "flavor": "prod"})
}

func TestHeartbeat(t *testing.T) {
	w := newWorld(t)
	d1 := w.login("d1@example.test")
	expect(t, w.heartbeat(d1, "inst-d1", "3.5.0"), http.StatusNoContent, "")
	if n := w.count(`SELECT count(*) FROM mobile_installations WHERE driver_id = $1 AND install_id = 'inst-d1' AND tenant_id = $2
		AND app_version = '3.5.0' AND tenant_source = 'driver'`, w.D1, w.O); n != 1 {
		t.Fatalf("row of D1: %d", n)
	}
	first := w.str(`SELECT first_seen_at::text FROM mobile_installations WHERE install_id = 'inst-d1'`)
	expect(t, w.heartbeat(d1, "inst-d1", "3.6.0"), http.StatusNoContent, "")
	if v := w.str(`SELECT app_version FROM mobile_installations WHERE install_id = 'inst-d1'`); v != "3.6.0" ||
		w.str(`SELECT first_seen_at::text FROM mobile_installations WHERE install_id = 'inst-d1'`) != first {
		t.Fatalf("update: %s", v)
	}
	// Validation, no token, a staff token.
	r := w.call(w.public, http.MethodPost, "/v1/mobile/heartbeat", d1, map[string]any{"installId": "bad id", "platform": "web",
		"appVersion": "", "flavor": "staging"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	for _, f := range []string{"installId", "platform", "appVersion", "flavor"} {
		if !strings.Contains(string(r.raw), `"field":"`+f+`"`) {
			t.Errorf("no violation of %s: %s", f, r.raw)
		}
	}
	expect(t, w.heartbeat("", "inst-x", "3.5.0"), http.StatusUnauthorized, "unauthenticated")
	expect(t, w.heartbeat(w.login("ta.o@example.test"), "inst-x", "3.5.0"), http.StatusForbidden, "driver_profile_required")
}

// Issue #72: "A driver cannot overwrite another driver's installation row." The row is keyed (driver_id, install_id)
// and driver_id comes from the token (R33); RLS refuses a direct write of another driver's row as well.
func TestDriverCannotOverwriteAnotherDriversRow(t *testing.T) {
	w := newWorld(t)
	d1, d2 := w.login("d1@example.test"), w.login("d2@example.test")
	expect(t, w.heartbeat(d1, "shared-install", "3.5.0"), http.StatusNoContent, "")
	// D2 sends D1's install id: it gets a row of its own, D1's is untouched.
	expect(t, w.heartbeat(d2, "shared-install", "9.9.9"), http.StatusNoContent, "")
	if v := w.str(`SELECT app_version FROM mobile_installations WHERE driver_id = $1 AND install_id = 'shared-install'`, w.D1); v != "3.5.0" {
		t.Fatalf("D1's row now says %s", v)
	}
	if n := w.count(`SELECT count(*) FROM mobile_installations WHERE driver_id = $1 AND install_id = 'shared-install' AND tenant_id = $2
		AND app_version = '9.9.9'`, w.D2, w.A); n != 1 {
		t.Fatal("D2 has no row of its own")
	}
	// RLS alone: D2's principal updates none of D1's rows and cannot insert one for D1.
	r := w.call(w.internal, http.MethodPost, "/v1/mobiletest/write-other?driver="+w.D1+"&tenant="+w.O, d2, nil)
	expect(t, r, http.StatusOK, "")
	if r.data()["updated"] != float64(0) || r.data()["insertState"] != "42501" {
		t.Fatalf("direct write of D1's row as D2: %s", r.raw)
	}
	if v := w.str(`SELECT app_version FROM mobile_installations WHERE driver_id = $1`, w.D1); v != "3.5.0" {
		t.Fatalf("D1's row now says %s", v)
	}
}

func TestHeartbeatCapsNewInstallIDs(t *testing.T) {
	w := newWorld(t)
	for i := range mobileapp.MaxInstallationsPerDriver {
		w.installation(w.D1, w.O, fmt.Sprintf("old-%02d", i), "3.4.0", "prod", w.now.Add(-time.Hour))
	}
	d1 := w.login("d1@example.test")
	r := w.heartbeat(d1, "one-too-many", "3.5.0")
	if expect(t, r, http.StatusConflict, "failed_precondition"); r.reason() != "too_many_installations" {
		t.Fatalf("reason %s", r.reason())
	}
	expect(t, w.heartbeat(d1, "old-07", "3.5.0"), http.StatusNoContent, "") // an existing install still reports
}

func TestHeartbeatRateLimit(t *testing.T) {
	w := newWorldOpts(t, worldOpts{rateLimit: true})
	d1, d2 := w.login("d1@example.test"), w.login("d2@example.test")
	expect(t, w.heartbeat(d1, "inst-1", "3.5.0"), http.StatusNoContent, "")
	r := w.heartbeat(d1, "inst-1", "3.5.0")
	if expect(t, r, http.StatusTooManyRequests, "resource_exhausted"); r.header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	// Another driver sending the same install id spends its own budget (driver id + install id).
	expect(t, w.heartbeat(d2, "inst-1", "3.5.0"), http.StatusNoContent, "")
}

// Issue #72: "GET /v1/mobile/settings answers without a token and returns no sensitive field."
func TestSettingsAnonymous(t *testing.T) {
	w := newWorld(t)
	w.settingsRow(map[string]any{"minAllowedVersion": "3.4.0", "minAllowedVersionSetAt": "2026-09-15T10:00:00+07:00",
		"minAllowedVersionSetBy": "platform.admin@logitrack.test", "latestVersion": "3.5.0", "latestBuildNumber": "1",
		"apkDownloadUrl": "https://media.example/app_releases/prod/logitrack-prod-v3.5.0.apk", "apkSha256": strings.Repeat("a", 64),
		"apkSizeBytes": 1048576, "flavor": "prod", "releasedAt": "2026-09-10T18:00:00+07:00", "releasedBy": "apikey:release-vm"})
	r := w.call(w.public, http.MethodGet, "/v1/mobile/settings", "", nil)
	expect(t, r, http.StatusOK, "")
	want := []string{"apkDownloadUrl", "apkSha256", "apkSizeBytes", "flavor", "latestBuildNumber", "latestVersion",
		"minAllowedVersion", "releaseNotes", "releasedAt"}
	var got []string
	for k := range r.data() {
		got = append(got, k)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("fields %v, want %v", got, want)
	}
	if strings.Contains(string(r.raw), "platform.admin") || strings.Contains(string(r.raw), "release-vm") {
		t.Fatalf("a sensitive value leaked: %s", r.raw)
	}
	if d := r.data(); d["minAllowedVersion"] != "3.4.0" || d["latestVersion"] != "3.5.0" || d["flavor"] != "prod" || d["releaseNotes"] != nil {
		t.Fatalf("body %s", r.raw)
	}
	etag := r.header.Get("ETag")
	if etag == "" || r.header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("headers %v", r.header)
	}
	if r := w.call(w.public, http.MethodGet, "/v1/mobile/settings", "", nil, "If-None-Match", etag); r.status != http.StatusNotModified || len(r.raw) != 0 {
		t.Fatalf("If-None-Match: %d %s", r.status, r.raw)
	}
	if r := w.call(w.public, http.MethodGet, "/v1/mobile/settings?flavor=prod", "", nil); r.header.Get("ETag") != etag {
		t.Fatal("?flavor=prod is the same document")
	}
	// Another flavor has no gate here: no floor without its own download link.
	r = w.call(w.public, http.MethodGet, "/v1/mobile/settings?flavor=dev", "", nil)
	if d := r.data(); r.status != 200 || d["flavor"] != "dev" || d["minAllowedVersion"] != nil || d["apkDownloadUrl"] != nil {
		t.Fatalf("dev: %s", r.raw)
	}
	expect(t, w.call(w.public, http.MethodGet, "/v1/mobile/settings?flavor=beta", "", nil), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.call(w.public, http.MethodGet, "/v1/mobile/settings?x=1", "", nil), http.StatusBadRequest, "bad_request")
	// The web reads the same document through the internal listener.
	expect(t, w.get("/v1/mobile/settings", ""), http.StatusOK, "")
}

func TestSettingsRateLimitedPerAddressOnThePublicListener(t *testing.T) {
	w := newWorldOpts(t, worldOpts{rateLimit: true})
	for i := range 60 {
		if r := w.call(w.public, http.MethodGet, "/v1/mobile/settings", "", nil); r.status != http.StatusOK {
			t.Fatalf("request %d: %d", i, r.status)
		}
	}
	expect(t, w.call(w.public, http.MethodGet, "/v1/mobile/settings", "", nil), http.StatusTooManyRequests, "resource_exhausted")
	// The internal listener (the BFF: every web user behind one address) is not limited by public_ip.
	expect(t, w.get("/v1/mobile/settings", ""), http.StatusOK, "")
}

// Issue #72: "Every release and installation-admin route returns 404 on the public listener."
func TestStaffRoutesAreNotOnThePublicListener(t *testing.T) {
	w := newWorld(t)
	pa := w.login("pa@example.test")
	key := w.releaseKey(pa)
	for _, rt := range [][2]string{
		{http.MethodGet, "/v1/app-installations"}, {http.MethodGet, "/v1/app-installations/stats"},
		{http.MethodGet, "/v1/app-releases"}, {http.MethodPut, "/v1/app-releases/floor"},
		{http.MethodPost, "/v1/app-releases/presign"}, {http.MethodPost, "/v1/app-releases"},
	} {
		for _, cred := range []string{"", pa, "key:" + key} {
			r := w.call(w.public, rt[0], rt[1], cred, map[string]any{})
			if r.status != http.StatusNotFound || r.code() != "not_found" {
				t.Fatalf("%s %s on the public listener: %d %s", rt[0], rt[1], r.status, r.raw)
			}
		}
		// The internal listener serves them (401 without a credential).
		if r := w.call(w.internal, rt[0], rt[1], "", map[string]any{}); r.status != http.StatusUnauthorized {
			t.Fatalf("%s %s on the internal listener: %d", rt[0], rt[1], r.status)
		}
	}
}

// writeAPK writes a placeholder APK (a ZIP header and n bytes) and returns its path and sha256.
func writeAPK(t *testing.T, n int, fill byte) (string, string) {
	t.Helper()
	body := append([]byte("PK\x03\x04"), bytes.Repeat([]byte{fill}, n)...)
	p := filepath.Join(t.TempDir(), "app-release.apk")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return p, hex.EncodeToString(sum[:])
}

// publishWith runs the client of cmd/release (internal/release) against the internal listener with the key: the CLI's
// whole flow, including the HEAD of the public URL through /media on the public listener.
func (w *world) publishWith(key string, b release.Build) (*release.Result, string, error) {
	w.t.Helper()
	c, err := release.NewClient(release.Config{GoAPIInternalURL: w.internal, ReleaseAPIKey: key})
	if err != nil {
		w.t.Fatal(err)
	}
	var out bytes.Buffer
	res, err := c.Publish(context.Background(), b, &out)
	return res, out.String(), err
}

// Issue #72: "Release publishing never writes minAllowedVersion"; cmd/release needs only GO_API_INTERNAL_URL and
// RELEASE_API_KEY (no Firebase credential, no public route).
func TestPublishWithTheReleaseCLI(t *testing.T) {
	w := newWorld(t)
	pa := w.login("pa@example.test")
	w.settingsRow(map[string]any{"minAllowedVersion": "3.4.0", "minAllowedVersionSetAt": "2026-09-15T10:00:00+07:00",
		"minAllowedVersionSetBy": "platform.admin@logitrack.test", "flavor": "dev", "latestVersion": "3.5.0",
		"apkDownloadUrl": "https://old.example/app.apk"})
	key := w.releaseKey(pa)
	apk, sum := writeAPK(t, 3<<20, 9) // above UPLOAD_MAX_BYTES (1 MiB): an APK may be
	notes := "แก้ไขการส่งรูป"
	res, out, err := w.publishWith(key, release.Build{Flavor: "dev", Version: "3.6.0", BuildNumber: "7", APK: apk, Notes: &notes})
	if err != nil {
		t.Fatalf("publish: %v\n%s\n%s", err, out, w.logs.String())
	}
	const apkKey = "app_releases/dev/logitrack-dev-v3.6.0.apk"
	if !res.Latest || res.Replayed || !strings.HasSuffix(res.APKURL, "/media/"+apkKey) || res.SHA256 != sum {
		t.Fatalf("result %+v", res)
	}
	doc := w.settingsDoc()
	// The floor half is exactly as it was: publishing announces, a person enforces (ADR 0007).
	if doc["minAllowedVersion"] != "3.4.0" || doc["minAllowedVersionSetAt"] != "2026-09-15T10:00:00+07:00" ||
		doc["minAllowedVersionSetBy"] != "platform.admin@logitrack.test" {
		t.Fatalf("the floor moved: %v", doc)
	}
	if doc["latestVersion"] != "3.6.0" || doc["latestBuildNumber"] != "7" || doc["apkDownloadUrl"] != res.APKURL ||
		doc["apkSha256"] != sum || doc["releasedBy"] != "apikey:release-vm" || doc["releaseNotes"] != notes || doc["flavor"] != "dev" {
		t.Fatalf("release half %v", doc)
	}
	if n := w.count(`SELECT count(*) FROM mobile_app_releases r JOIN file_objects f ON f.id = r.apk_file_id
		WHERE r.flavor = 'dev' AND r.version = '3.6.0' AND r.build_number = '7' AND r.apk_sha256 = $1 AND r.released_by = 'apikey:release-vm'
		AND f.object_key = $2 AND f.status = 'committed' AND f.visibility = 'public' AND f.purpose = 'apk' AND f.owner_id = r.id
		AND f.uploaded_by IS NULL AND f.tenant_id IS NULL`, sum, apkKey); n != 1 {
		t.Fatal("release row and its committed public APK")
	}
	if n := w.count(`SELECT count(*) FROM security_events WHERE event_type = 'mobile_release_published' AND actor_user_id IS NULL
		AND actor_email = 'apikey:release-vm' AND tenant_id IS NULL AND details->>'version' = '3.6.0' AND (details->>'latest')::boolean`); n != 1 {
		t.Fatal("mobile_release_published")
	}
	if n := w.count(`SELECT count(*) FROM outbox_events WHERE routing_key = 'settings.changed' AND aggregate_id = 'mobile_app'
		AND realtime_topics = ARRAY['global'] AND payload->>'flavor' = 'dev'`); n != 1 {
		t.Fatal("outbox settings.changed")
	}
	// The gate shows the new release at once (the write dropped cache:settings:mobile_app).
	g := w.call(w.public, http.MethodGet, "/v1/mobile/settings", "", nil).data()
	if g["latestVersion"] != "3.6.0" || g["minAllowedVersion"] != "3.4.0" || g["apkDownloadUrl"] != res.APKURL {
		t.Fatalf("gate %v", g)
	}
	// The APK downloads from the public listener without a signature, immutable.
	dl, err := http.Get(res.APKURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()
	got := sha256.Sum256(body)
	if dl.StatusCode != 200 || hex.EncodeToString(got[:]) != sum || dl.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("download %d %v", dl.StatusCode, dl.Header)
	}

	// A retried publish of the same build replays; the same version with other bytes is immutable.
	res, _, err = w.publishWith(key, release.Build{Flavor: "dev", Version: "3.6.0", BuildNumber: "7", APK: apk, SkipURLCheck: true})
	if err == nil || !strings.Contains(err.Error(), "published") {
		t.Fatalf("presign of a published version: %v %+v", err, res)
	}
	r := w.call(w.internal, http.MethodPost, "/v1/app-releases", "key:"+key, map[string]any{"flavor": "dev", "version": "3.6.0",
		"buildNumber": "7", "sha256": sum, "sizeBytes": 3<<20 + 4})
	if expect(t, r, http.StatusOK, ""); r.meta()["replayed"] != true {
		t.Fatalf("replay %s", r.raw)
	}
	other, _ := writeAPK(t, 3<<20, 8)
	if _, _, err := w.publishWith(key, release.Build{Flavor: "dev", Version: "3.6.0", BuildNumber: "7", APK: other, Force: true}); err == nil ||
		!strings.Contains(err.Error(), "published") {
		t.Fatalf("force over a published version: %v", err)
	}

	// An older version is recorded without becoming the latest; the other flavor is refused unless it switches.
	old, _ := writeAPK(t, 1000, 1)
	if res, _, err = w.publishWith(key, release.Build{Flavor: "dev", Version: "3.5.9", BuildNumber: "6", APK: old}); err != nil || res.Latest {
		t.Fatalf("older version: %+v %v", res, err)
	}
	if v := w.settingsDoc()["latestVersion"]; v != "3.6.0" {
		t.Fatalf("latest moved back to %v", v)
	}
	prod, _ := writeAPK(t, 1000, 2)
	if _, _, err = w.publishWith(key, release.Build{Flavor: "prod", Version: "3.6.0", BuildNumber: "7", APK: prod}); err == nil ||
		!strings.Contains(err.Error(), "flavor_mismatch") {
		t.Fatalf("prod into a dev deployment: %v", err)
	}
	if res, _, err = w.publishWith(key, release.Build{Flavor: "prod", Version: "3.6.0", BuildNumber: "7", APK: prod, SwitchFlavor: true}); err != nil || !res.Latest {
		t.Fatalf("switch: %+v %v", res, err)
	}
	if doc := w.settingsDoc(); doc["flavor"] != "prod" || doc["minAllowedVersion"] != "3.4.0" {
		t.Fatalf("after the switch: %v", doc)
	}
	// The key never appears in the logs.
	if strings.Contains(w.logs.String(), key) {
		t.Fatal("the release key was logged")
	}
}

func TestPresignAndPublishRefusals(t *testing.T) {
	w := newWorld(t)
	pa := w.login("pa@example.test")
	key := w.releaseKey(pa)
	apk, sum := writeAPK(t, 2000, 5)
	body := map[string]any{"flavor": "prod", "version": "3.6.0", "buildNumber": "4", "sha256": sum, "sizeBytes": 2004}
	r := w.call(w.internal, http.MethodPost, "/v1/app-releases/presign", "key:"+key, body)
	expect(t, r, http.StatusOK, "")
	if d := r.data(); d["key"] != "app_releases/prod/logitrack-prod-v3.6.0.apk" || !strings.HasPrefix(d["url"].(string), "/v1/uploads/local/") ||
		!strings.HasSuffix(d["publicUrl"].(string), "/media/app_releases/prod/logitrack-prod-v3.6.0.apk") || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("presign %s", r.raw)
	}
	// A second presign of the unpublished upload needs force.
	r = w.call(w.internal, http.MethodPost, "/v1/app-releases/presign", "key:"+key, body)
	if expect(t, r, http.StatusConflict, "already_exists"); r.reason() != "upload_pending" {
		t.Fatalf("reason %s", r.reason())
	}
	// Publishing before the upload: 422 upload_missing, nothing written.
	r = w.call(w.internal, http.MethodPost, "/v1/app-releases", "key:"+key, body)
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if !strings.Contains(string(r.raw), "upload_missing") || w.count(`SELECT count(*) FROM mobile_app_releases`) != 0 {
		t.Fatalf("publish without upload: %s", r.raw)
	}
	// The publish must describe the uploaded bytes.
	if _, _, err := w.publishWith(key, release.Build{Flavor: "prod", Version: "3.6.0", BuildNumber: "4", APK: apk, Force: true}); err != nil {
		t.Fatal(err)
	}
	if w.count(`SELECT count(*) FROM settings WHERE key = 'mobile_app' AND value->>'flavor' = 'prod'`) != 1 {
		t.Fatal("the first publish of a deployment sets its flavor")
	}
	// Validation.
	r = w.call(w.internal, http.MethodPost, "/v1/app-releases/presign", "key:"+key, map[string]any{"flavor": "beta", "version": "03.6.0",
		"buildNumber": "x", "sha256": "ABC", "sizeBytes": 0, "minAllowedVersion": "9.9.9"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	for _, f := range []string{"flavor", "version", "buildNumber", "sha256", "sizeBytes"} {
		if !strings.Contains(string(r.raw), `"field":"`+f+`"`) {
			t.Errorf("no violation of %s", f)
		}
	}
	// Other API keys and people without the capability.
	integration := w.key(pa, map[string]any{"name": "ops-script", "scope": "integration", "capabilities": []string{"security:view_status"}})
	r = w.call(w.internal, http.MethodPost, "/v1/app-releases/presign", "key:"+integration, body)
	if expect(t, r, http.StatusForbidden, "permission_denied"); r.reason() != "api_key_scope" {
		t.Fatalf("integration key: %s", r.raw)
	}
	expect(t, w.call(w.internal, http.MethodPost, "/v1/app-releases/presign", w.login("ta.a@example.test"), body),
		http.StatusForbidden, "permission_denied") // a carrier's tenant_admin: global key, not a steward
}

// Issue #72: "a release_publisher key gets 403 on PUT /v1/app-releases/floor"; "Set-floor refused while the APK URL
// is empty".
func TestFloor(t *testing.T) {
	w := newWorld(t)
	pa := w.login("pa@example.test")
	key := w.releaseKey(pa)
	floor := func(auth string, v any) resp {
		return w.call(w.internal, http.MethodPut, "/v1/app-releases/floor", auth, map[string]any{"minAllowedVersion": v})
	}
	r := floor("key:"+key, "3.6.0")
	if expect(t, r, http.StatusForbidden, "permission_denied"); r.reason() != "api_key_scope" {
		t.Fatalf("release key on the floor: %s", r.raw)
	}
	// No release yet: a floor would trap every blocked driver on a dialog without a button.
	r = floor(pa, "3.6.0")
	if expect(t, r, http.StatusConflict, "failed_precondition"); r.reason() != "no_apk_url" {
		t.Fatalf("reason %s", r.reason())
	}
	w.settingsRow(map[string]any{"flavor": "prod", "latestVersion": "3.6.0", "apkDownloadUrl": ""})
	if r = floor(pa, "3.6.0"); r.reason() != "no_apk_url" {
		t.Fatalf("an empty link: %s", r.raw)
	}
	apk, _ := writeAPK(t, 500, 3)
	if _, _, err := w.publishWith(key, release.Build{Flavor: "prod", Version: "3.6.0", BuildNumber: "1", APK: apk}); err != nil {
		t.Fatal(err)
	}
	// Installations seen this week: two would be blocked by 3.6.0 (3.5.0, 2.10.0), not the unparseable one.
	for i, v := range []string{"3.5.0", "2.10.0", "3.6.0", "3.7.0", "garbage"} {
		w.installation(w.D1, w.O, fmt.Sprintf("i%d", i), v, "prod", w.now.Add(-time.Hour))
	}
	w.installation(w.D2, w.A, "stale", "1.0.0", "prod", w.now.Add(-30*24*time.Hour))
	expect(t, floor(pa, "3.7.0"), http.StatusUnprocessableEntity, "invalid_argument") // above the latest release
	expect(t, floor(pa, "3.6"), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.call(w.internal, http.MethodPut, "/v1/app-releases/floor", pa, map[string]any{}), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, floor(w.login("ta.a@example.test"), "3.6.0"), http.StatusForbidden, "permission_denied")
	expect(t, floor(w.login("op.o@example.test"), "3.6.0"), http.StatusForbidden, "permission_denied")
	before := w.settingsDoc()
	ta := w.login("ta.o@example.test") // own-fleet tenant_admin: a steward
	r = floor(ta, "3.6.0")
	expect(t, r, http.StatusOK, "")
	if r.meta()["changed"] != true || r.meta()["blocked7d"] != float64(2) || r.data()["minAllowedVersion"] != "3.6.0" ||
		r.data()["minAllowedVersionSetBy"] != "ta.o@example.test" {
		t.Fatalf("floor %s", r.raw)
	}
	after := w.settingsDoc()
	for _, k := range []string{"latestVersion", "latestBuildNumber", "apkDownloadUrl", "apkSha256", "releasedBy", "releasedAt", "flavor"} {
		if fmt.Sprint(after[k]) != fmt.Sprint(before[k]) {
			t.Fatalf("the floor writer changed %s: %v -> %v", k, before[k], after[k])
		}
	}
	if n := w.count(`SELECT count(*) FROM security_events WHERE event_type = 'mobile_floor_changed' AND severity = 'warning'
		AND actor_email = 'ta.o@example.test' AND tenant_id = $1 AND details->>'to' = '3.6.0' AND (details->>'blocked7d')::int = 2`, w.O); n != 1 {
		t.Fatal("mobile_floor_changed")
	}
	if g := w.call(w.public, http.MethodGet, "/v1/mobile/settings", "", nil).data(); g["minAllowedVersion"] != "3.6.0" {
		t.Fatalf("the gate still shows %v", g["minAllowedVersion"])
	}
	// The same floor again writes nothing.
	events := w.count(`SELECT count(*) FROM security_events WHERE event_type = 'mobile_floor_changed'`)
	if r = floor(ta, "3.6.0"); r.status != 200 || r.meta()["changed"] != false ||
		w.count(`SELECT count(*) FROM security_events WHERE event_type = 'mobile_floor_changed'`) != events {
		t.Fatalf("a no-op: %s", r.raw)
	}
	// null removes the floor (the lever is two-way, ADR 0007 §6), no link needed for that.
	if r = floor(pa, nil); r.status != 200 || r.data()["minAllowedVersion"] != nil {
		t.Fatalf("remove: %s", r.raw)
	}
	if v, ok := w.settingsDoc()["minAllowedVersion"]; !ok || v != nil {
		t.Fatalf("stored floor %v", v)
	}
}

// Issue #72: "Blocked count by semver (ported lib/mobileVersion.test.ts)": the stats classify with the port, the
// list filters with the same rule in SQL (2.10.0 is not below 2.9.3).
func TestInstallationsStatsAndBelowVersion(t *testing.T) {
	w := newWorld(t)
	w.settingsRow(map[string]any{"flavor": "prod", "minAllowedVersion": "2.9.0", "latestVersion": "2.9.3", "apkDownloadUrl": "https://x/a.apk"})
	for i, v := range []string{"2.8.0", "2.9.2", "2.9.3", "2.10.0", "", "not-a-version", "2.9.3+4", " 2.9.1 ", "9007199254740992.0.0"} {
		w.installation(w.D1, w.O, fmt.Sprintf("i%d", i), v, "prod", w.now.Add(-time.Duration(i+1)*time.Hour))
	}
	w.installation(w.D2, w.A, "dev-build", "2.10.0", "dev", w.now.Add(-30*time.Minute))
	w.installation(w.D2, w.A, "gone", "1.0.0", "prod", w.now.Add(-10*24*time.Hour))
	pa := w.login("pa@example.test")
	all := []string{"X-Act-On-Tenant", "*"}
	r := w.get("/v1/app-installations/stats?belowVersion=2.9.3", pa, all...)
	expect(t, r, http.StatusOK, "")
	d := r.data()
	byStatus := d["byStatus"].(map[string]any)
	// Seen in 7 days: 10 (the 10-day-old one is out). blocked: 2.8.0; outdated: 2.9.2, 2.9.1; current: 2.9.3, 2.9.3+4;
	// ahead: 2.10.0 x2; unknown: "", not-a-version, 2^53.
	if d["total"] != float64(10) || byStatus["blocked"] != float64(1) || byStatus["outdated"] != float64(2) ||
		byStatus["current"] != float64(2) || byStatus["ahead"] != float64(2) || byStatus["unknown"] != float64(3) {
		t.Fatalf("stats %s", r.raw)
	}
	if b := d["belowVersion"].(map[string]any); b["version"] != "2.9.3" || b["count"] != float64(3) { // 2.8.0, 2.9.2, 2.9.1
		t.Fatalf("belowVersion %v", b)
	}
	if d["byFlavor"].(map[string]any)["dev"] != float64(1) {
		t.Fatalf("byFlavor %v", d["byFlavor"])
	}
	// The list's SQL filter agrees with the port.
	r = w.get("/v1/app-installations?belowVersion=2.9.3&limit=50", pa, all...)
	expect(t, r, http.StatusOK, "")
	var versions []string
	for _, it := range r.list() {
		versions = append(versions, strings.TrimSpace(it.(map[string]any)["appVersion"].(string)))
	}
	slices.Sort(versions)
	if !slices.Equal(versions, []string{"1.0.0", "2.8.0", "2.9.1", "2.9.2"}) { // no since: the 10-day-old row too
		t.Fatalf("below 2.9.3: %v", versions)
	}
	// Each row carries its status and the meta the references.
	r = w.get("/v1/app-installations?since=7d&flavor=dev", pa, all...)
	if l := r.list(); len(l) != 1 || l[0].(map[string]any)["versionStatus"] != "ahead" || l[0].(map[string]any)["driverName"] != "สมหญิง ใจงาม" ||
		r.meta()["latestVersion"] != "2.9.3" || r.meta()["minAllowedVersion"] != "2.9.0" {
		t.Fatalf("dev rows %s", r.raw)
	}
	expect(t, w.get("/v1/app-installations?belowVersion=v2", pa, all...), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/app-installations?since=yesterday", pa, all...), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/app-installations?sort=x", pa, all...), http.StatusBadRequest, "bad_request")
}

func TestInstallationsReachAndPaging(t *testing.T) {
	w := newWorld(t)
	for i := range 5 {
		w.installation(w.D1, w.O, fmt.Sprintf("o%d", i), "3.5.0", "prod", w.now.Add(-time.Duration(i)*time.Minute))
	}
	w.installation(w.D2, w.A, "a0", "3.5.0", "prod", w.now.Add(-10*time.Minute))
	w.installation(w.D3, w.C, "c0", "3.5.0", "prod", w.now.Add(-11*time.Minute))
	tenantsOf := func(tok string, headers ...string) map[string]int {
		t.Helper()
		r := w.get("/v1/app-installations", tok, headers...)
		expect(t, r, http.StatusOK, "")
		out := map[string]int{}
		for _, it := range r.list() {
			out[it.(map[string]any)["tenantId"].(string)]++
		}
		return out
	}
	// RLS: own-fleet staff reach their carriers, a carrier its own tenant, nobody another carrier.
	if got := tenantsOf(w.login("ta.o@example.test")); got[w.O] != 5 || got[w.A] != 1 || got[w.C] != 0 {
		t.Fatalf("own fleet: %v", got)
	}
	if got := tenantsOf(w.login("ta.a@example.test")); len(got) != 1 || got[w.A] != 1 {
		t.Fatalf("carrier A: %v", got)
	}
	if got := tenantsOf(w.login("ta.c@example.test")); len(got) != 1 || got[w.C] != 1 {
		t.Fatalf("carrier C: %v", got)
	}
	// ?tenantId narrows within the reach and never widens it.
	ta := w.login("ta.a@example.test")
	if r := w.get("/v1/app-installations?tenantId="+w.O, ta); len(r.list()) != 0 {
		t.Fatalf("carrier A asking for O: %s", r.raw)
	}
	pa := w.login("pa@example.test")
	expect(t, w.get("/v1/app-installations", pa), http.StatusForbidden, "tenant_required")
	if got := tenantsOf(pa, "X-Act-On-Tenant", "*"); got[w.O] != 5 || got[w.A] != 1 || got[w.C] != 1 {
		t.Fatalf("platform: %v", got)
	}
	expect(t, w.get("/v1/app-installations", w.login("op.o@example.test")), http.StatusForbidden, "permission_denied")
	expect(t, w.get("/v1/app-installations", w.login("d1@example.test")), http.StatusForbidden, "permission_denied")
	// Keyset pages, newest heartbeat first, no row twice.
	var seen []string
	cursor := ""
	for range 10 {
		path := "/v1/app-installations?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := w.get(path, pa, "X-Act-On-Tenant", "*")
		expect(t, r, http.StatusOK, "")
		for _, it := range r.list() {
			seen = append(seen, it.(map[string]any)["installId"].(string))
		}
		c, _ := r.body["nextCursor"].(string)
		if c == "" {
			break
		}
		cursor = c
	}
	if !slices.Equal(seen, []string{"o0", "o1", "o2", "o3", "o4", "a0", "c0"}) {
		t.Fatalf("pages %v", seen)
	}
	expect(t, w.get("/v1/app-installations?cursor=zzz", pa, "X-Act-On-Tenant", "*"), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/app-installations?limit=501", pa, "X-Act-On-Tenant", "*"), http.StatusUnprocessableEntity, "invalid_argument")
}

func TestReleasesList(t *testing.T) {
	w := newWorld(t)
	pa := w.login("pa@example.test")
	key := w.releaseKey(pa)
	for i, v := range []string{"3.4.0", "3.5.0", "3.6.0"} {
		apk, _ := writeAPK(t, 100+i, byte(i))
		if _, _, err := w.publishWith(key, release.Build{Flavor: "prod", Version: v, BuildNumber: "1", APK: apk}); err != nil {
			t.Fatal(err)
		}
	}
	r := w.get("/v1/app-releases?limit=2", pa)
	expect(t, r, http.StatusOK, "")
	l := r.list()
	if len(l) != 2 || l[0].(map[string]any)["version"] != "3.6.0" || l[0].(map[string]any)["latest"] != true ||
		l[0].(map[string]any)["releasedBy"] != "apikey:release-vm" || l[1].(map[string]any)["latest"] != false {
		t.Fatalf("page 1 %s", r.raw)
	}
	if s := r.meta()["settings"].(map[string]any); s["latestVersion"] != "3.6.0" || s["releasedBy"] != "apikey:release-vm" {
		t.Fatalf("meta %v", s)
	}
	next, _ := r.body["nextCursor"].(string)
	if r = w.get("/v1/app-releases?limit=2&cursor="+next, pa); len(r.list()) != 1 || r.list()[0].(map[string]any)["version"] != "3.4.0" {
		t.Fatalf("page 2 %s", r.raw)
	}
	// A carrier's tenant_admin (security:view_mobile_clients) reads them without the people fields.
	r = w.get("/v1/app-releases", w.login("ta.a@example.test"))
	expect(t, r, http.StatusOK, "")
	if r.list()[0].(map[string]any)["releasedBy"] != nil || r.meta()["settings"].(map[string]any)["releasedBy"] != nil {
		t.Fatalf("names leaked: %s", r.raw)
	}
	if r = w.get("/v1/app-releases?flavor=dev", pa); len(r.list()) != 0 {
		t.Fatalf("dev: %s", r.raw)
	}
	expect(t, w.get("/v1/app-releases", w.login("op.o@example.test")), http.StatusForbidden, "permission_denied")
	r = w.get("/v1/app-releases", "key:"+key)
	if expect(t, r, http.StatusForbidden, "permission_denied"); r.reason() != "api_key_scope" {
		t.Fatalf("the release key reads nothing but its two routes: %s", r.raw)
	}
}
