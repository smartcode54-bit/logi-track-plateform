// Package release is the client side of APK publishing (main spec §9.7, ADR 0007, R43, R82): what cmd/release runs
// on the private network (the VM, or a CI job over SSH) against the api's internal listener. It replaces
// logitrack-web/scripts/publish-mobile-release.mjs and needs exactly two names of §16.1: GO_API_INTERNAL_URL (the
// internal listener) and RELEASE_API_KEY (an api_keys secret of scope release_publisher holding
// security:manage_mobile_release, sent as X-Api-Key). No Firebase credential, no public route.
//
// A publish is: hash the APK, POST /v1/app-releases/presign, PUT the bytes to the signed URL (an absolute S3 URL, or
// a path on the internal listener for the local storage backend), check that the public URL serves them, then POST
// /v1/app-releases. It announces a build and never touches the version floor: a person raises that on the Security
// Center's mobile-release page.
package release

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

// HeaderAPIKey carries RELEASE_API_KEY (internal/auth HeaderAPIKey).
const HeaderAPIKey = "X-Api-Key"

// ProdApplicationID is the applicationId of the prod Gradle flavor (ADR 0007 §1: a "prod" APK built without
// --flavor prod carries the dev id).
const ProdApplicationID = "com.wrt.logitrack"

// MaxAPKBytes mirrors storage.MaxReleaseBytes: the api refuses a larger APK at presign.
const MaxAPKBytes = 200 << 20

// Config is the environment of cmd/release: the only two names it reads.
type Config struct {
	GoAPIInternalURL string `env:"GO_API_INTERNAL_URL,required"`
	ReleaseAPIKey    string `env:"RELEASE_API_KEY,required"`
}

// Validate implements config.Validator; it never echoes a value.
func (c *Config) Validate() error {
	var bad []string
	if _, err := baseURL(c.GoAPIInternalURL); err != nil {
		bad = append(bad, config.Invalidf("GO_API_INTERNAL_URL", "must be an http(s) URL without credentials, query or fragment"))
	}
	if !strings.HasPrefix(strings.TrimSpace(c.ReleaseAPIKey), "ltk_") {
		bad = append(bad, config.Invalidf("RELEASE_API_KEY", "must be an api_keys secret (ltk_...) of scope release_publisher"))
	}
	if len(bad) > 0 {
		return &config.Error{Invalid: bad}
	}
	return nil
}

func baseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("not an http(s) base URL")
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u, nil
}

var (
	versionRx = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
	buildRx   = regexp.MustCompile(`^[0-9]{1,9}$`)
)

// Build is one publish: the APK file and what it is.
type Build struct {
	Flavor      string
	Version     string
	BuildNumber string
	APK         string
	Notes       *string
	// Force replaces an upload of this version that was never published (a crash between upload and publish).
	Force bool
	// SwitchFlavor makes Flavor the deployment's gate flavor.
	SwitchFlavor bool
	// SkipURLCheck publishes without checking that the public URL serves the APK (an operator who cannot reach the
	// public host from the private network).
	SkipURLCheck bool
	// DryRun hashes and prints the plan and the current gate, and writes nothing.
	DryRun bool
}

// Validate checks the build before any request.
func (b Build) Validate() error {
	var bad []string
	if b.Flavor != "dev" && b.Flavor != "prod" {
		bad = append(bad, "--flavor must be dev or prod")
	}
	if !versionRx.MatchString(b.Version) {
		bad = append(bad, "--version must be MAJOR.MINOR.PATCH without leading zeros or a build suffix")
	}
	if !buildRx.MatchString(b.BuildNumber) {
		bad = append(bad, "--build-number must be the pubspec build number (digits)")
	}
	if b.APK == "" {
		bad = append(bad, "--apk is required")
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(bad, "; "))
	}
	return nil
}

// Metadata is the part of Gradle's output-metadata.json the legacy script checked (ADR 0007 §3).
type Metadata struct {
	ApplicationID string `json:"applicationId"`
	VariantName   string `json:"variantName"`
	Elements      []struct {
		Type        string            `json:"type"`
		Filters     []json.RawMessage `json:"filters"`
		VersionCode json.Number       `json:"versionCode"`
		VersionName string            `json:"versionName"`
		OutputFile  string            `json:"outputFile"`
	} `json:"elements"`
}

// ApplyMetadata cross-checks b against the build metadata at path and fills what b leaves empty: the universal APK
// (a split-per-ABI build is refused: one ABI's APK as "the" download bricks every other device), its versionName
// (must equal --version), its versionCode (the pubspec build number), and the APK path. The applicationId must match
// the flavor (the prod id only for prod).
func ApplyMetadata(b *Build, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("--metadata: %w", err)
	}
	var m Metadata
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&m); err != nil {
		return fmt.Errorf("--metadata: not Gradle output metadata: %w", err)
	}
	idx := -1
	for i, e := range m.Elements {
		if len(e.Filters) == 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("--metadata: the build is split per ABI (%d artifacts, none universal); rebuild without --split-per-abi", len(m.Elements))
	}
	e := m.Elements[idx]
	switch {
	case b.Flavor == "prod" && m.ApplicationID != ProdApplicationID:
		return fmt.Errorf("--metadata: applicationId %q is not the prod id %q: this build was not made with --flavor prod", m.ApplicationID, ProdApplicationID)
	case b.Flavor == "dev" && m.ApplicationID == ProdApplicationID:
		return errors.New("--metadata: the APK carries the prod applicationId but --flavor is dev")
	}
	if e.VersionName != "" {
		if b.Version == "" {
			b.Version = e.VersionName
		} else if b.Version != e.VersionName {
			return fmt.Errorf("--metadata: the APK is version %s but --version is %s (rebuild after editing pubspec.yaml)", e.VersionName, b.Version)
		}
	}
	if code := e.VersionCode.String(); code != "" {
		if b.BuildNumber == "" {
			b.BuildNumber = code
		} else if b.BuildNumber != code {
			return fmt.Errorf("--metadata: the APK is build %s but --build-number is %s", code, b.BuildNumber)
		}
	}
	if b.APK == "" && e.OutputFile != "" {
		b.APK = filepath.Join(filepath.Dir(path), filepath.Base(e.OutputFile))
	}
	return nil
}

// Result is a finished publish (or the plan of a dry run).
type Result struct {
	Flavor, Version, BuildNumber string
	SHA256                       string
	SizeBytes                    int64
	APKURL                       string
	Latest, Replayed             bool
}

// APIError is an error envelope of the api (R48).
type APIError struct {
	Status    int
	Code      string
	Message   string
	Details   map[string]any
	RequestID string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("%d %s", e.Status, e.Code)
	if r, ok := e.Details["reason"].(string); ok {
		msg += " (" + r + ")"
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if e.RequestID != "" {
		msg += " [request " + e.RequestID + "]"
	}
	return msg
}

// Client calls the internal listener with the release key.
type Client struct {
	base *url.URL
	key  string
	http *http.Client
}

// NewClient builds a client of cfg. The key is held in memory only and sent as X-Api-Key; it is never printed.
func NewClient(cfg Config) (*Client, error) {
	u, err := baseURL(cfg.GoAPIInternalURL)
	if err != nil {
		return nil, &config.Error{Invalid: []string{config.Invalidf("GO_API_INTERNAL_URL", "must be an http(s) URL")}}
	}
	return &Client{base: u, key: strings.TrimSpace(cfg.ReleaseAPIKey), http: &http.Client{}}, nil
}

// resolve turns a URL of an answer into an absolute one: an absolute URL as it is (S3), a path on the api (the local
// backend's upload route) under GO_API_INTERNAL_URL.
func (c *Client) resolve(ref string) (string, error) {
	r, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	if r.IsAbs() {
		return r.String(), nil
	}
	if !strings.HasPrefix(r.Path, "/") {
		return "", fmt.Errorf("unexpected relative URL %q", ref)
	}
	u := *c.base
	u.Path = c.base.Path + r.Path
	u.RawPath = ""
	if r.RawPath != "" {
		u.RawPath = c.base.EscapedPath() + r.RawPath
	}
	u.RawQuery = r.RawQuery
	return u.String(), nil
}

func (c *Client) call(ctx context.Context, method, path string, body, out any, withKey bool) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	u := *c.base
	p, q, _ := strings.Cut(path, "?")
	u.Path, u.RawQuery = c.base.Path+p, q
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if withKey {
		req.Header.Set(HeaderAPIKey, c.key)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s %s: %w", method, p, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return res.StatusCode, err
	}
	if res.StatusCode >= 300 {
		var env struct {
			Error struct {
				Code      string         `json:"code"`
				Message   string         `json:"message"`
				Details   map[string]any `json:"details"`
				RequestID string         `json:"requestId"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		return res.StatusCode, &APIError{Status: res.StatusCode, Code: env.Error.Code, Message: env.Error.Message,
			Details: env.Error.Details, RequestID: env.Error.RequestID}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return res.StatusCode, fmt.Errorf("%s %s: unexpected answer: %w", method, p, err)
		}
	}
	return res.StatusCode, nil
}

// Gate is the version gate as GET /v1/mobile/settings shows it to a device.
type Gate struct {
	Flavor            *string `json:"flavor"`
	MinAllowedVersion *string `json:"minAllowedVersion"`
	LatestVersion     *string `json:"latestVersion"`
	LatestBuildNumber *string `json:"latestBuildNumber"`
	APKDownloadURL    *string `json:"apkDownloadUrl"`
}

// Gate reads the anonymous version gate of flavor through the internal listener (no key needed).
func (c *Client) Gate(ctx context.Context, flavor string) (Gate, error) {
	var out struct {
		Data Gate `json:"data"`
	}
	path := "/v1/mobile/settings"
	if flavor != "" {
		path += "?flavor=" + url.QueryEscape(flavor)
	}
	_, err := c.call(ctx, http.MethodGet, path, nil, &out, false)
	return out.Data, err
}

// HashFile returns the size and sha256 of the APK, streamed (an APK is about 80 MB), and refuses a file that is not a
// ZIP archive (an APK is one) or that is larger than the api takes.
func HashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return 0, "", err
	}
	if !st.Mode().IsRegular() || st.Size() < 4 {
		return 0, "", fmt.Errorf("%s is not an APK file", path)
	}
	if st.Size() > MaxAPKBytes {
		return 0, "", fmt.Errorf("%s is %d bytes, above the %d the api takes", path, st.Size(), MaxAPKBytes)
	}
	br := bufio.NewReader(f)
	magic, err := br.Peek(4)
	if err != nil || !bytes.Equal(magic, []byte("PK\x03\x04")) {
		return 0, "", fmt.Errorf("%s is not an APK (no ZIP header)", path)
	}
	h := sha256.New()
	n, err := io.Copy(h, br)
	if err != nil {
		return 0, "", err
	}
	if n != st.Size() {
		return 0, "", fmt.Errorf("%s changed while it was read", path)
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

type presigned struct {
	Key       string            `json:"key"`
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expiresAt"`
	PublicURL string            `json:"publicUrl"`
}

type releaseBody struct {
	Flavor       string  `json:"flavor"`
	Version      string  `json:"version"`
	BuildNumber  string  `json:"buildNumber"`
	SHA256       string  `json:"sha256"`
	SizeBytes    int64   `json:"sizeBytes"`
	ReleaseNotes *string `json:"releaseNotes,omitempty"`
	Force        bool    `json:"force,omitempty"`
	SwitchFlavor bool    `json:"switchFlavor,omitempty"`
}

// Publish runs one publish and reports each step to out. It never sends minAllowedVersion: the publish routes take
// none, and the floor route refuses API keys.
func (c *Client) Publish(ctx context.Context, b Build, out io.Writer) (*Result, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	size, sum, err := HashFile(b.APK)
	if err != nil {
		return nil, err
	}
	res := &Result{Flavor: b.Flavor, Version: b.Version, BuildNumber: b.BuildNumber, SHA256: sum, SizeBytes: size}
	_, _ = fmt.Fprintf(out, "APK      %s\nRelease  %s %s+%s\nSize     %.1f MB (%d bytes)\nSHA-256  %s\nAPI      %s (internal listener)\n",
		b.APK, b.Flavor, b.Version, b.BuildNumber, float64(size)/(1<<20), size, sum, c.base.Redacted())
	if b.DryRun {
		g, err := c.Gate(ctx, b.Flavor)
		if err != nil {
			return nil, fmt.Errorf("read the current gate: %w", err)
		}
		_, _ = fmt.Fprintf(out, "Gate     flavor %s, latest %s+%s, floor %s, link %s\nDry run: nothing was uploaded or published.\n",
			str(g.Flavor), str(g.LatestVersion), str(g.LatestBuildNumber), str(g.MinAllowedVersion), str(g.APKDownloadURL))
		return res, nil
	}
	body := releaseBody{Flavor: b.Flavor, Version: b.Version, BuildNumber: b.BuildNumber, SHA256: sum, SizeBytes: size,
		Force: b.Force, SwitchFlavor: b.SwitchFlavor}
	var ps struct {
		Data presigned `json:"data"`
	}
	if _, err := c.call(ctx, http.MethodPost, "/v1/app-releases/presign", body, &ps, true); err != nil {
		return nil, fmt.Errorf("presign: %w%s", err, hint(err))
	}
	if err := c.upload(ctx, ps.Data, b.APK, size); err != nil {
		return nil, fmt.Errorf("upload: %w (rerun with --force: the upload is not published)", err)
	}
	_, _ = fmt.Fprintf(out, "Uploaded %s\n", ps.Data.Key)
	if !b.SkipURLCheck {
		if err := c.checkPublic(ctx, ps.Data.PublicURL, size); err != nil {
			return nil, fmt.Errorf("the public URL does not serve the APK, so nothing was published: %w (fix the host, then rerun with --force, or pass --skip-url-check)", err)
		}
		_, _ = fmt.Fprintf(out, "Serves   %s\n", ps.Data.PublicURL)
	}
	body.Force, body.ReleaseNotes = false, b.Notes
	var pub struct {
		Data struct {
			APKURL *string `json:"apkUrl"`
		} `json:"data"`
		Meta struct {
			Latest   bool `json:"latest"`
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if _, err := c.call(ctx, http.MethodPost, "/v1/app-releases", body, &pub, true); err != nil {
		return nil, fmt.Errorf("publish: %w%s", err, hint(err))
	}
	res.APKURL, res.Latest, res.Replayed = str(pub.Data.APKURL), pub.Meta.Latest, pub.Meta.Replayed
	return res, nil
}

// upload sends the APK to the signed URL with exactly the signed headers and its Content-Length. A 412 from S3 means
// a retried PUT found its own earlier upload in place (the URL creates once): the publish verifies the bytes.
func (c *Client) upload(ctx context.Context, p presigned, path string, size int64) error {
	target, err := c.resolve(p.URL)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	method := p.Method
	if method == "" {
		method = http.MethodPut
	}
	req, err := http.NewRequestWithContext(ctx, method, target, f)
	if err != nil {
		return err
	}
	req.ContentLength = size
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300, res.StatusCode == http.StatusPreconditionFailed:
		return nil
	}
	return fmt.Errorf("PUT answered %d", res.StatusCode)
}

// checkPublic HEADs the public URL: it must answer 200 with the APK's length (the legacy script's check before it
// announced a release).
func (c *Client) checkPublic(ctx context.Context, u string, size int64) error {
	if u == "" {
		return errors.New("the api returned no public URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HEAD %s answered %d", u, res.StatusCode)
	}
	if n, err := strconv.ParseInt(res.Header.Get("Content-Length"), 10, 64); err == nil && n != size {
		return fmt.Errorf("HEAD %s reports %d bytes, the APK has %d", u, n, size)
	}
	return nil
}

// hint adds the operator's next step to a refusal of the api.
func hint(err error) string {
	var ae *APIError
	if !errors.As(err, &ae) {
		return ""
	}
	r, _ := ae.Details["reason"].(string)
	switch {
	case ae.Status == http.StatusUnauthorized:
		return "; RELEASE_API_KEY is unknown, revoked or expired: issue a new release_publisher key (POST /v1/api-keys)"
	case ae.Status == http.StatusForbidden:
		return "; the key must be a platform key of scope release_publisher holding security:manage_mobile_release"
	case r == "published":
		return "; a published version is immutable: bump the version in pubspec.yaml and rebuild"
	case r == "upload_pending":
		return "; an earlier upload of this version was never published: rerun with --force to replace it"
	case r == "flavor_mismatch":
		return fmt.Sprintf("; this deployment serves flavor %v: publish that flavor, or pass --switch-flavor to make this one the deployment's", ae.Details["flavor"])
	}
	return ""
}

func str(p *string) string {
	if p == nil || *p == "" {
		return "-"
	}
	return *p
}
