package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/release"
)

// testKey is a placeholder of the release_publisher secret's shape (never a real key).
var testKey = "ltk_testtest_" + strings.Repeat("x", 43)

// fakeAPI plays the internal listener: presign answers a local-backend upload path (or an absolute "S3" URL),
// the upload, the public URL and the publish are recorded.
type fakeAPI struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex

	s3            bool   // presign answers an absolute URL (the S3 backend)
	putStatus     int    // 0 = 200
	publicStatus  int    // 0 = 200
	refusePresign string // a reason: the presign answers 409 with it
	refusePublish string // a reason: the publish answers 409 with it
	replay        bool   // the publish answers 200 meta.replayed (the version is published with this build)
	calls         []string
	uploaded      []byte
	putHeaders    http.Header
	presignBody   map[string]any
	publishBody   map[string]any
	keys          []string // X-Api-Key values seen

	// The deployment's gate (settings('mobile_app')), served by GET /v1/mobile/settings as mobileapp.Settings.Public
	// does: another flavor than gateFlavor gets an empty gate.
	gateFlavor, gateLatest, gateFloor string
}

func newFake(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, gateFlavor: "dev", gateLatest: "3.5.0", gateFloor: "3.4.0"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if k := r.Header.Get("X-Api-Key"); k != "" {
		f.keys = append(f.keys, k)
	}
	body, _ := io.ReadAll(r.Body)
	reply := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/mobile/settings":
		orNil := func(v string) any {
			if v == "" {
				return nil
			}
			return v
		}
		flavor := r.URL.Query().Get("flavor")
		if flavor == "" {
			flavor = f.gateFlavor
		}
		g := map[string]any{"flavor": orNil(flavor), "latestVersion": nil, "minAllowedVersion": nil}
		if f.gateFlavor == "" || flavor == f.gateFlavor {
			g["latestVersion"], g["minAllowedVersion"] = orNil(f.gateLatest), orNil(f.gateFloor)
		}
		reply(200, map[string]any{"data": g})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/app-releases/presign":
		_ = json.Unmarshal(body, &f.presignBody)
		if f.refusePresign != "" {
			reply(409, refusal(f.refusePresign))
			return
		}
		u := "/v1/uploads/local/app_releases/dev/logitrack-dev-v3.6.0.apk?X-LT-Expires=1&X-LT-Signature=sig"
		if f.s3 {
			u = f.srv.URL + "/s3/logitrack-public/app_releases/dev/logitrack-dev-v3.6.0.apk?X-Amz-Signature=sig"
		}
		reply(200, map[string]any{"data": map[string]any{"key": "app_releases/dev/logitrack-dev-v3.6.0.apk", "url": u, "method": "PUT",
			"headers":   map[string]string{"Content-Type": "application/vnd.android.package-archive"},
			"publicUrl": f.srv.URL + "/media/app_releases/dev/logitrack-dev-v3.6.0.apk"}})
	case r.Method == http.MethodPut:
		f.uploaded, f.putHeaders = body, r.Header.Clone()
		if f.putStatus != 0 {
			w.WriteHeader(f.putStatus)
			return
		}
		w.WriteHeader(200)
	case r.Method == http.MethodHead && strings.HasPrefix(r.URL.Path, "/media/"):
		if f.publicStatus != 0 {
			w.WriteHeader(f.publicStatus)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(f.uploaded)))
		w.WriteHeader(200)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/app-releases":
		_ = json.Unmarshal(body, &f.publishBody)
		if f.refusePublish != "" {
			reply(409, refusal(f.refusePublish))
			return
		}
		status := 201
		if f.replay {
			status = 200
		}
		reply(status, map[string]any{"data": map[string]any{"apkUrl": f.srv.URL + "/media/app_releases/dev/logitrack-dev-v3.6.0.apk"},
			"meta": map[string]any{"latest": true, "replayed": f.replay}})
	default:
		w.WriteHeader(404)
	}
}

// refusal is the R48 envelope of a 409 of the release routes with reason r.
func refusal(r string) map[string]any {
	code := "failed_precondition"
	if r == "published" || r == "upload_pending" {
		code = "already_exists"
	}
	return map[string]any{"error": map[string]any{"code": code, "message": "conflict", "requestId": "req-1",
		"details": map[string]any{"reason": r, "flavor": "prod", "requested": "dev", "minAllowedVersion": "3.4.0", "version": "3.6.0"}}}
}

func apk(t *testing.T) (string, []byte) {
	t.Helper()
	data := append([]byte("PK\x03\x04"), bytes.Repeat([]byte{7}, 4096)...)
	p := filepath.Join(t.TempDir(), "app-dev-release.apk")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, data
}

func (f *fakeAPI) env() []string {
	return []string{"GO_API_INTERNAL_URL=" + f.srv.URL, "RELEASE_API_KEY=" + testKey,
		// Names a Firebase-era publish needed: cmd/release reads none of them.
		"GOOGLE_APPLICATION_CREDENTIALS=/nonexistent/sa.json", "FIREBASE_STORAGE_BUCKET=x"}
}

func runRelease(t *testing.T, env []string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, env, &out, &errb)
	return code, out.String(), errb.String()
}

func TestPublishLocalBackend(t *testing.T) {
	f := newFake(t)
	path, data := apk(t)
	code, out, errOut := runRelease(t, f.env(), "publish", "--flavor=dev", "--apk="+path, "--version=3.6.0", "--build-number=4", "--notes=hotfix")
	if code != app.ExitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	want := []string{"POST /v1/app-releases/presign", "PUT /v1/uploads/local/app_releases/dev/logitrack-dev-v3.6.0.apk",
		"HEAD /media/app_releases/dev/logitrack-dev-v3.6.0.apk", "POST /v1/app-releases"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls %v, want %v", f.calls, want)
	}
	if !bytes.Equal(f.uploaded, data) || f.putHeaders.Get("Content-Type") != "application/vnd.android.package-archive" ||
		f.putHeaders.Get("X-Api-Key") != "" {
		t.Fatalf("upload: %d bytes, headers %v (the signed URL needs no key)", len(f.uploaded), f.putHeaders)
	}
	sum := sha256.Sum256(data)
	for _, b := range []map[string]any{f.presignBody, f.publishBody} {
		if b["flavor"] != "dev" || b["version"] != "3.6.0" || b["buildNumber"] != "4" || b["sha256"] != hex.EncodeToString(sum[:]) ||
			b["sizeBytes"] != float64(len(data)) {
			t.Fatalf("body %v", b)
		}
		if _, ok := b["minAllowedVersion"]; ok {
			t.Fatal("a publish never sends the floor (ADR 0007)")
		}
	}
	if f.publishBody["releaseNotes"] != "hotfix" {
		t.Fatalf("publish body %v", f.publishBody)
	}
	if len(f.keys) != 2 || f.keys[0] != testKey || f.keys[1] != testKey {
		t.Fatalf("X-Api-Key sent %d times", len(f.keys))
	}
	if strings.Contains(out+errOut, testKey) || strings.Contains(out+errOut, "ltk_") {
		t.Fatal("the key was printed")
	}
	if !strings.Contains(out, "as the latest release") || !strings.Contains(out, "floor is unchanged") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestPublishS3TakesItsOwnEarlierUpload(t *testing.T) {
	f := newFake(t)
	f.s3, f.putStatus = true, http.StatusPreconditionFailed // a retried PUT: the object is already there
	path, _ := apk(t)
	code, out, errOut := runRelease(t, f.env(), "publish", "--flavor=dev", "--apk="+path, "--version=3.6.0", "--build-number=4")
	if code != app.ExitOK || f.calls[1] != "PUT /s3/logitrack-public/app_releases/dev/logitrack-dev-v3.6.0.apk" {
		t.Fatalf("exit %d, calls %v\n%s%s", code, f.calls, out, errOut)
	}
}

func TestPublishStopsWhenThePublicURLDoesNotServe(t *testing.T) {
	f := newFake(t)
	f.publicStatus = http.StatusNotFound
	path, _ := apk(t)
	code, _, errOut := runRelease(t, f.env(), "publish", "--flavor=dev", "--apk="+path, "--version=3.6.0", "--build-number=4")
	if code != app.ExitRuntimeError || f.publishBody != nil || !strings.Contains(errOut, "nothing was published") {
		t.Fatalf("exit %d, published %v, stderr %s", code, f.publishBody, errOut)
	}
	// --skip-url-check publishes anyway.
	code, _, _ = runRelease(t, f.env(), "publish", "--flavor=dev", "--apk="+path, "--version=3.6.0", "--build-number=4", "--skip-url-check")
	if code != app.ExitOK || f.publishBody == nil {
		t.Fatalf("exit %d with --skip-url-check", code)
	}
}

func TestPublishRefusalsCarryTheNextStep(t *testing.T) {
	path, _ := apk(t)
	for reason, hint := range map[string]string{"upload_pending": "--force", "flavor_mismatch": "--switch-flavor",
		"floor_set": "removes it first", "floor_above_version": "at or above the floor"} {
		f := newFake(t)
		f.refusePresign = reason
		code, _, errOut := runRelease(t, f.env(), "publish", "--flavor=dev", "--apk="+path, "--version=3.6.0", "--build-number=4")
		if code != app.ExitRuntimeError || !strings.Contains(errOut, reason) || !strings.Contains(errOut, hint) || len(f.calls) != 1 {
			t.Fatalf("%s: exit %d, calls %v, stderr %s", reason, code, f.calls, errOut)
		}
	}
}

// A rerun after a publish whose answer was lost: the presign says the version is published, and the publish route
// replays the same build (200 meta.replayed) without anything uploaded again.
func TestRerunAfterALostAnswerReplays(t *testing.T) {
	f := newFake(t)
	f.refusePresign, f.replay = "published", true
	path, _ := apk(t)
	code, out, errOut := runRelease(t, f.env(), "publish", "--flavor=dev", "--apk="+path, "--version=3.6.0", "--build-number=4", "--force", "--notes=n")
	if code != app.ExitOK || !strings.Contains(out, "Already published: dev 3.6.0+4") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if want := []string{"POST /v1/app-releases/presign", "POST /v1/app-releases"}; !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls %v, want %v (no upload, no URL check)", f.calls, want)
	}
	if f.publishBody["force"] != nil || f.publishBody["sha256"] != f.presignBody["sha256"] || f.publishBody["releaseNotes"] != "n" {
		t.Fatalf("publish body %v", f.publishBody)
	}
	// Other bytes under the published version: the publish route refuses them and the operator is told why.
	f = newFake(t)
	f.refusePresign, f.refusePublish = "published", "published"
	code, _, errOut = runRelease(t, f.env(), "publish", "--flavor=dev", "--apk="+path, "--version=3.6.0", "--build-number=4")
	if code != app.ExitRuntimeError || !strings.Contains(errOut, "publish: 409 already_exists (published)") ||
		!strings.Contains(errOut, "different build") || len(f.calls) != 2 {
		t.Fatalf("exit %d, calls %v, stderr %s", code, f.calls, errOut)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	f := newFake(t)
	path, _ := apk(t)
	code, out, _ := runRelease(t, f.env(), "publish", "--flavor=dev", "--apk="+path, "--version=3.6.0", "--build-number=4", "--dry-run")
	if code != app.ExitOK || !reflect.DeepEqual(f.calls, []string{"GET /v1/mobile/settings"}) || !strings.Contains(out, "Dry run") ||
		!strings.Contains(out, "becomes the latest dev release; the version floor stays 3.4.0") {
		t.Fatalf("exit %d, calls %v\n%s", code, f.calls, out)
	}
}

// The dry run reads the deployment's own gate (not the empty gate another flavor's devices see) and reports the
// refusal the publish would meet, as the api decides it (internal/mobileapp gateRefusal).
func TestDryRunShowsTheDeploymentsGate(t *testing.T) {
	path, _ := apk(t)
	dry := func(f *fakeAPI, extra ...string) (int, string, string) {
		args := append([]string{"publish", "--flavor=dev", "--apk=" + path, "--version=3.6.0", "--build-number=4", "--dry-run"}, extra...)
		return runRelease(t, f.env(), args...)
	}
	// The staging demo seed: a prod gate with the floor 3.4.0.
	f := newFake(t)
	f.gateFlavor = "prod"
	code, out, errOut := dry(f)
	if code != app.ExitRuntimeError || !strings.Contains(out, "Gate     flavor prod, latest 3.5.0+-, floor 3.4.0") ||
		!strings.Contains(errOut, "flavor_mismatch") || !strings.Contains(errOut, "--switch-flavor") || !strings.Contains(errOut, "nothing was uploaded") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	code, _, errOut = dry(f, "--switch-flavor")
	if code != app.ExitRuntimeError || !strings.Contains(errOut, "floor_set") || !strings.Contains(errOut, "prod fleet has the version floor 3.4.0") {
		t.Fatalf("switch with a floor: exit %d\n%s", code, errOut)
	}
	if !reflect.DeepEqual(f.calls, []string{"GET /v1/mobile/settings", "GET /v1/mobile/settings"}) || len(f.keys) != 0 {
		t.Fatalf("calls %v, keys %d", f.calls, len(f.keys))
	}
	// Once a person removed the floor, the switch goes through.
	f.gateFloor = ""
	if code, out, errOut = dry(f, "--switch-flavor"); code != app.ExitOK || !strings.Contains(out, "switches the gate from prod to dev") {
		t.Fatalf("switch: exit %d\n%s\n%s", code, out, errOut)
	}
	// A floor above the new latest (a legacy document), and an older version (recorded only).
	f = newFake(t)
	f.gateFloor = "3.7.0"
	if code, _, errOut = dry(f); code != app.ExitRuntimeError || !strings.Contains(errOut, "floor_above_version") {
		t.Fatalf("floor above: exit %d\n%s", code, errOut)
	}
	f.gateFloor, f.gateLatest = "3.4.0", "3.10.0"
	if code, out, _ = dry(f); code != app.ExitOK || !strings.Contains(out, "older than the latest 3.10.0: recorded only") {
		t.Fatalf("older: exit %d\n%s", code, out)
	}
}

// The environment of cmd/release is exactly GO_API_INTERNAL_URL and RELEASE_API_KEY (issue #72 acceptance): no
// Firebase credential, nothing else.
func TestConfigNamesOnlyTheTwoVariables(t *testing.T) {
	var names []string
	rt := reflect.TypeFor[release.Config]()
	for i := range rt.NumField() {
		names = append(names, rt.Field(i).Tag.Get("env"))
	}
	if !reflect.DeepEqual(names, []string{"GO_API_INTERNAL_URL,required", "RELEASE_API_KEY,required"}) {
		t.Fatalf("release.Config reads %v", names)
	}
	path, _ := apk(t)
	code, _, errOut := runRelease(t, []string{"PATH=/usr/bin"}, "publish", "--flavor=dev", "--apk="+path, "--version=3.6.0", "--build-number=4")
	if code != app.ExitConfigError || !strings.Contains(errOut, "GO_API_INTERNAL_URL") || !strings.Contains(errOut, "RELEASE_API_KEY") {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	// A bad value is named, never printed.
	code, _, errOut = runRelease(t, []string{"GO_API_INTERNAL_URL=http://u:secret-pass@h:1", "RELEASE_API_KEY=not-a-key-sentinel-7c1"},
		"publish", "--flavor=dev", "--apk="+path, "--version=3.6.0", "--build-number=4")
	if code != app.ExitConfigError || strings.Contains(errOut, "secret-pass") || strings.Contains(errOut, "sentinel-7c1") {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
}

func TestBuildChecks(t *testing.T) {
	f := newFake(t)
	path, _ := apk(t)
	for name, args := range map[string][]string{
		"flavor":        {"--flavor=staging", "--apk=" + path, "--version=3.6.0", "--build-number=4"},
		"leading zero":  {"--flavor=dev", "--apk=" + path, "--version=03.6.0", "--build-number=4"},
		"build suffix":  {"--flavor=dev", "--apk=" + path, "--version=3.6.0+4", "--build-number=4"},
		"build number":  {"--flavor=dev", "--apk=" + path, "--version=3.6.0", "--build-number=x"},
		"no apk":        {"--flavor=dev", "--version=3.6.0", "--build-number=4"},
		"extra":         {"--flavor=dev", "--apk=" + path, "--version=3.6.0", "--build-number=4", "now"},
		"unknown flag":  {"--flavor=dev", "--apk=" + path, "--version=3.6.0", "--build-number=4", "--min-allowed-version=3.6.0"},
		"no subcommand": nil,
	} {
		code, _, _ := runRelease(t, f.env(), append([]string{"publish"}, args...)...)
		if name == "no subcommand" {
			code, _, _ = runRelease(t, f.env())
		}
		if code != app.ExitConfigError || len(f.calls) != 0 {
			t.Fatalf("%s: exit %d, calls %v", name, code, f.calls)
		}
	}
	// Not an APK: no ZIP header.
	bad := filepath.Join(t.TempDir(), "x.apk")
	if err := os.WriteFile(bad, []byte("not a zip archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runRelease(t, f.env(), "publish", "--flavor=dev", "--apk="+bad, "--version=3.6.0", "--build-number=4"); code != app.ExitRuntimeError ||
		!strings.Contains(errOut, "not an APK") || len(f.calls) != 0 {
		t.Fatalf("exit %d, %s", code, errOut)
	}
}

func writeMetadata(t *testing.T, dir, appID, versionName string, filters string) string {
	t.Helper()
	m := `{"applicationId":"` + appID + `","variantName":"prodRelease","elements":[{"type":"SINGLE","filters":` + filters +
		`,"versionCode":4,"versionName":"` + versionName + `","outputFile":"app-prod-release.apk"}]}`
	p := filepath.Join(dir, "output-metadata.json")
	if err := os.WriteFile(p, []byte(m), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMetadataChecks(t *testing.T) {
	dir := t.TempDir()
	data := append([]byte("PK\x03\x04"), 1, 2, 3)
	if err := os.WriteFile(filepath.Join(dir, "app-prod-release.apk"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFake(t)
	// The flavor bug of ADR 0007 §1: a "prod" build without --flavor prod carries the dev applicationId.
	m := writeMetadata(t, dir, "com.wrt.logitrack.dev", "3.6.0", "[]")
	if code, _, errOut := runRelease(t, f.env(), "publish", "--flavor=prod", "--metadata="+m); code != app.ExitConfigError || !strings.Contains(errOut, "--flavor prod") {
		t.Fatalf("exit %d, %s", code, errOut)
	}
	m = writeMetadata(t, dir, release.ProdApplicationID, "3.6.0", `[{"filterType":"ABI","value":"arm64-v8a"}]`)
	if code, _, errOut := runRelease(t, f.env(), "publish", "--flavor=prod", "--metadata="+m); code != app.ExitConfigError || !strings.Contains(errOut, "split per ABI") {
		t.Fatalf("exit %d, %s", code, errOut)
	}
	m = writeMetadata(t, dir, release.ProdApplicationID, "3.6.1", "[]")
	if code, _, errOut := runRelease(t, f.env(), "publish", "--flavor=prod", "--metadata="+m, "--version=3.6.0"); code != app.ExitConfigError || !strings.Contains(errOut, "rebuild") {
		t.Fatalf("exit %d, %s", code, errOut)
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v", f.calls)
	}
	// Version, build number and the APK come from the metadata.
	b := release.Build{Flavor: "prod"}
	if err := release.ApplyMetadata(&b, m); err != nil || b.Version != "3.6.1" || b.BuildNumber != "4" || b.APK != filepath.Join(dir, "app-prod-release.apk") {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestStatus(t *testing.T) {
	f := newFake(t)
	code, out, errOut := runRelease(t, []string{"GO_API_INTERNAL_URL=" + f.srv.URL}, "status", "--flavor=dev")
	if code != app.ExitOK || !strings.Contains(out, "latest 3.5.0") || !strings.Contains(out, "floor  3.4.0") || len(f.keys) != 0 ||
		strings.Contains(out, "gate ") {
		t.Fatalf("exit %d, keys %d\n%s%s", code, len(f.keys), out, errOut)
	}
	// The other flavor: its devices see no gate, and status says which flavor the deployment serves.
	f.gateFlavor = "prod"
	code, out, _ = runRelease(t, []string{"GO_API_INTERNAL_URL=" + f.srv.URL}, "status", "--flavor=dev")
	if code != app.ExitOK || !strings.Contains(out, "flavor dev\nlatest -+-\nfloor  -") ||
		!strings.Contains(out, "gate   this deployment serves prod (latest 3.5.0+-, floor 3.4.0)") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if code, out, _ = runRelease(t, []string{"GO_API_INTERNAL_URL=" + f.srv.URL}, "status"); code != app.ExitOK ||
		!strings.Contains(out, "flavor prod\nlatest 3.5.0") || strings.Contains(out, "gate ") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}
