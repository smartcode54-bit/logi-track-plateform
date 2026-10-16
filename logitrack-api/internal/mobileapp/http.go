package mobileapp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/supportprojection"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// Route prefixes of the staff routes (internal listener only, R43).
const (
	InstallationsPrefix = "/v1/app-installations"
	ReleasesPrefix      = "/v1/app-releases"
)

// MobileOptions wire the driver-app routes: Auth is auth.Service.RequireAuth() (a driver's Go token), SettingsLimit
// the public_ip bucket of the anonymous gate and HeartbeatLimit the heartbeat_install bucket (nil = none).
type MobileOptions struct {
	Auth           fiber.Handler
	SettingsLimit  fiber.Handler
	HeartbeatLimit fiber.Handler
}

// MountMobile registers the driver-app routes on a /v1/mobile router (the public group, which also mounts the
// mobile SSE stream; ingress allows one group per prefix): GET /settings, anonymous (R42), and POST /heartbeat.
// It only registers handlers, so cmd/api routes may pass a nil *Service.
func (s *Service) MountMobile(r fiber.Router, o MobileOptions) {
	h := mobileHTTP{s}
	// Fiber runs a route's handlers in the order given: the rate limit, then the handler.
	if o.SettingsLimit != nil {
		r.Get("/settings", o.SettingsLimit, h.settings)
	} else {
		r.Get("/settings", h.settings)
	}
	heartbeat := []any{}
	if o.HeartbeatLimit != nil {
		heartbeat = append(heartbeat, o.HeartbeatLimit)
	}
	r.Post("/heartbeat", o.Auth, append(heartbeat, h.heartbeat)...)
}

// StaffOptions wire the staff routes: Auth admits people only (auth.Service.RequireAuth()); PublisherAuth also admits
// release_publisher API keys (RequireAuth(auth.AllowAPIKeys(authz.KeyReleasePublisher))) and guards only the two
// publish routes (R82).
type StaffOptions struct {
	Auth          fiber.Handler
	PublisherAuth fiber.Handler
}

// Groups are the staff route groups of the Security Center pages mobile-clients and mobile-release and of cmd/release
// (Appendix B §B.2.18): internal listener only, so every one of them is 404 on the public listener (R43).
func (s *Service) Groups(o StaffOptions) []ingress.Group {
	h := mobileHTTP{s}
	view := authz.RequireCap(authz.SecurityViewMobileClients)
	manage := authz.RequireCap(authz.SecurityManageMobileRel)
	groups := []ingress.Group{
		{Prefix: InstallationsPrefix, Mount: func(r fiber.Router) {
			p := supportprojection.Policy(http.MethodGet, InstallationsPrefix, "mobileapp.installation")
			p.MetaSchemaID = "mobileapp.installation-meta"
			p.ValidateCursor = func(s string, _ httpx.ProjectionAudience) bool { _, ok := decodeInstallCursor(s); return ok }
			ingress.Bind(r, "", p, o.Auth, h.projection, authz.RequireTenant(), view, h.installations)
			ingress.Bind(r, "/stats", supportprojection.Policy(http.MethodGet, InstallationsPrefix+"/stats", "mobileapp.installation-stats"), o.Auth, h.projection, authz.RequireTenant(), view, h.stats)
		}},
		{Prefix: ReleasesPrefix, Mount: func(r fiber.Router) {
			r.Get("", o.Auth, authz.RequireCap(authz.SecurityManageMobileRel, authz.SecurityViewMobileClients), h.releases)
			r.Put("/floor", o.Auth, manage, h.floor)
			r.Post("/presign", o.PublisherAuth, manage, h.presign)
			r.Post("", o.PublisherAuth, manage, h.publish)
		}},
	}
	groups = append(groups, ingress.Group{Prefix: "/v1/platform/app-installations", Mount: func(r fiber.Router) {
		p := supportprojection.Policy(http.MethodGet, "/v1/platform/app-installations", "mobileapp.installation")
		p.MetaSchemaID = "mobileapp.installation-meta"
		p.ValidateCursor = func(s string, _ httpx.ProjectionAudience) bool { _, ok := decodePlatformInstallCursor(s); return ok }
		ingress.Bind(r, "", p, o.Auth, h.projection, platformInstallRead, h.installations)
		ingress.Bind(r, "/stats", supportprojection.Policy(http.MethodGet, "/v1/platform/app-installations/stats", "mobileapp.installation-stats"), o.Auth, h.projection, platformInstallRead, h.stats)
	}})
	return groups
}

// HeartbeatSubject is the heartbeat_install rate-limit subject: the caller's driver id and the body's install id, so
// one driver cannot spend another driver's budget by sending its install id ("" skips the rule: the handler answers
// the request that has no driver or no install id).
func HeartbeatSubject(c fiber.Ctx) string {
	p := authz.PrincipalFrom(c)
	if p == nil || p.DriverID == nil {
		return ""
	}
	var in struct {
		InstallID string `json:"installId"`
	}
	if json.Unmarshal(c.Body(), &in) != nil || !installIDRx.MatchString(in.InstallID) {
		return ""
	}
	return p.DriverID.String() + ":" + in.InstallID
}

type mobileHTTP struct{ s *Service }

func (h mobileHTTP) projection(c fiber.Ctx) error { return supportprojection.Middleware(h.s.pool)(c) }
func platformInstallRead(c fiber.Ctx) error {
	p := authz.PrincipalFrom(c)
	if p == nil || p.IsMachine() || !p.IsPlatform() || !authz.PlatformCaps(p).Has(authz.SecurityViewMobileClients) {
		return authz.ErrPermissionDenied("missing platform read capability")
	}
	return c.Next()
}

func onlyQuery(c fiber.Ctx, allowed ...string) error {
	for k := range c.Queries() {
		ok := false
		for _, a := range allowed {
			ok = ok || k == a
		}
		if !ok {
			return httpx.ErrBadRequest("unknown query parameter " + k)
		}
	}
	return nil
}

func noStore(c fiber.Ctx) { c.Set(fiber.HeaderCacheControl, "no-store") }

// settings is GET /v1/mobile/settings[?flavor]: anonymous (the gate runs before login), the non-sensitive fields of
// the gate, an ETag over the body and 304 for a matching If-None-Match. Cache-Control: no-cache, so a device
// revalidates every time it checks (login, resume) and a floor change reaches it at once.
func (h mobileHTTP) settings(c fiber.Ctx) error {
	if err := onlyQuery(c, "flavor"); err != nil {
		return err
	}
	flavor := c.Query("flavor")
	if flavor != "" && !validFlavor(flavor) {
		return httpx.ErrInvalidArgument(violation("flavor", "one_of", map[string]any{"allowed": []string{FlavorDev, FlavorProd}}))
	}
	st, err := h.s.Settings(c.Context())
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"data": st.Public(flavor)})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	etag := `"` + base64.RawURLEncoding.EncodeToString(sum[:18]) + `"`
	c.Set(fiber.HeaderETag, etag)
	c.Set(fiber.HeaderCacheControl, "no-cache")
	if etagMatches(c.Get(fiber.HeaderIfNoneMatch), etag) {
		return c.SendStatus(http.StatusNotModified)
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Status(http.StatusOK).Send(body)
}

// etagMatches is the weak comparison of If-None-Match (RFC 9110 §13.1.2): "*" or any listed tag, W/ ignored.
func etagMatches(header, etag string) bool {
	for t := range strings.SplitSeq(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == "*" || t == etag {
			return true
		}
	}
	return false
}

func (h mobileHTTP) heartbeat(c fiber.Ctx) error {
	var in HeartbeatInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	if err := h.s.Heartbeat(c.Context(), authz.PrincipalFrom(c), in); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

// sinceRx is a relative window: a number of days or hours ("7d", "24h").
var sinceRx = regexp.MustCompile(`^([1-9][0-9]{0,3})([dh])$`)

// parseSince reads ?since: an RFC 3339 instant or a window back from now ("7d", "24h", at most 9999 units).
func parseSince(v string, now time.Time) (time.Time, bool) {
	if m := sinceRx.FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := time.Hour
		if m[2] == "d" {
			unit = 24 * time.Hour
		}
		return now.Add(-time.Duration(n) * unit), true
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	return t, err == nil
}

func queryTenant(c fiber.Ctx) (*uuid.UUID, error) {
	v := c.Query("tenantId")
	if v == "" {
		return nil, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil, httpx.ErrInvalidArgument(violation("tenantId", "invalid", nil))
	}
	return &id, nil
}

func queryLimit(c fiber.Ctx) (int, error) {
	v := c.Query("limit")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n == 0 {
		return 0, httpx.ErrInvalidArgument(violation("limit", "out_of_range", map[string]any{"min": 1, "max": MaxLimit}))
	}
	return n, nil
}

func (h mobileHTTP) installations(c fiber.Ctx) error {
	if err := onlyQuery(c, "tenantId", "since", "belowVersion", "flavor", "limit", "cursor"); err != nil {
		return err
	}
	q := InstallationQuery{BelowVersion: c.Query("belowVersion"), Flavor: c.Query("flavor"), Cursor: c.Query("cursor")}
	var err error
	if q.TenantID, err = queryTenant(c); err != nil {
		return err
	}
	if q.Flavor != "" && !validFlavor(q.Flavor) {
		return httpx.ErrInvalidArgument(violation("flavor", "one_of", map[string]any{"allowed": []string{FlavorDev, FlavorProd}}))
	}
	if v := c.Query("since"); v != "" {
		t, ok := parseSince(v, h.s.now())
		if !ok {
			return httpx.ErrInvalidArgument(violation("since", "invalid", nil))
		}
		q.Since = &t
	}
	if q.Limit, err = queryLimit(c); err != nil {
		return err
	}
	rows, next, meta, err := h.s.Installations(c.Context(), authz.PrincipalFrom(c), q)
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.Page(c, rows, next, meta)
}

func (h mobileHTTP) stats(c fiber.Ctx) error {
	if err := onlyQuery(c, "tenantId", "since", "belowVersion"); err != nil {
		return err
	}
	tenant, err := queryTenant(c)
	if err != nil {
		return err
	}
	since := h.s.now().Add(-ActiveWindow)
	if v := c.Query("since"); v != "" {
		t, ok := parseSince(v, h.s.now())
		if !ok {
			return httpx.ErrInvalidArgument(violation("since", "invalid", nil))
		}
		since = t
	}
	out, err := h.s.Stats(c.Context(), authz.PrincipalFrom(c), since, tenant, c.Query("belowVersion"))
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.JSON(c, http.StatusOK, out)
}

func (h mobileHTTP) releases(c fiber.Ctx) error {
	if err := onlyQuery(c, "flavor", "limit", "cursor"); err != nil {
		return err
	}
	limit, err := queryLimit(c)
	if err != nil {
		return err
	}
	rows, next, meta, err := h.s.Releases(c.Context(), authz.PrincipalFrom(c), c.Query("flavor"), limit, c.Query("cursor"))
	if err != nil {
		return err
	}
	noStore(c)
	return httpx.Page(c, rows, next, meta)
}

func (h mobileHTTP) floor(c fiber.Ctx) error {
	var in FloorInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.SetFloor(c.Context(), authz.PrincipalFrom(c), in, httpx.RequestIDFrom(c))
	if err != nil {
		return err
	}
	noStore(c)
	return c.Status(http.StatusOK).JSON(map[string]any{"data": out.Settings,
		"meta": map[string]any{"changed": out.Changed, "blocked7d": out.Blocked7d}})
}

func (h mobileHTTP) presign(c fiber.Ctx) error {
	var in ReleaseInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Presign(c.Context(), authz.PrincipalFrom(c), in)
	if err != nil {
		return err
	}
	noStore(c) // a signed upload URL
	return httpx.JSON(c, http.StatusOK, out)
}

func (h mobileHTTP) publish(c fiber.Ctx) error {
	var in ReleaseInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	out, err := h.s.Publish(c.Context(), authz.PrincipalFrom(c), in, httpx.RequestIDFrom(c))
	if err != nil {
		return err
	}
	status := http.StatusCreated
	if out.Replayed {
		status = http.StatusOK
	}
	noStore(c)
	return c.Status(status).JSON(map[string]any{"data": out.Release,
		"meta": map[string]any{"latest": out.Latest, "replayed": out.Replayed}})
}
