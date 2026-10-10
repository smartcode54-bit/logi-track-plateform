package app_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/logx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/telemetry"
)

// syncBuffer collects log lines written concurrently by both listeners.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Lines() []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// testGroups adds routes that exercise the ingress policy: an internal-only
// staff route answering 401, and public mobile routes that echo the resolved
// client IP, log a sensitive field and panic.
func testGroups() []ingress.Group {
	return []ingress.Group{
		{Prefix: "/v1/staff-probe", Mount: func(r fiber.Router) {
			r.Get("", func(c fiber.Ctx) error { return httpx.ErrUnauthenticated() })
			r.Get("/slow", slow)
		}},
		{Prefix: "/v1/mobile", Public: true, Mount: func(r fiber.Router) {
			r.Get("/whoami", func(c fiber.Ctx) error {
				logx.FromContext(c.Context()).Info().Str("password", "hunter2").Msg("handler line")
				return httpx.JSON(c, 200, map[string]string{"ip": httpx.ClientIPFrom(c)})
			})
			r.Get("/panic", func(c fiber.Ctx) error { panic("boom") })
			r.Get("/slow", slow)
			r.Post("/echo", func(c fiber.Ctx) error { return c.SendStatus(204) })
		}},
	}
}

// slow sleeps for ?ms= milliseconds, ignoring cancellation, like a handler
// finishing a database write during shutdown.
func slow(c fiber.Ctx) error {
	d, _ := time.ParseDuration(c.Query("ms", "0") + "ms")
	time.Sleep(d)
	return httpx.JSON(c, 200, map[string]string{"slept": d.String()})
}

type harness struct {
	api              *app.API
	logs             *syncBuffer
	internal, public string
	metrics          string
	cancel           context.CancelFunc
	done             chan error
}

func start(t *testing.T, trusted ...string) *harness {
	t.Helper()
	app.DrainGrace = 100 * time.Millisecond
	logs := &syncBuffer{}
	log, err := logx.New(logx.Options{Level: "debug", Format: "json", Service: "api", Env: "local", Out: logs})
	if err != nil {
		t.Fatal(err)
	}
	var prefixes []netip.Prefix
	for _, s := range trusted {
		prefixes = append(prefixes, netip.MustParsePrefix(s))
	}
	cfg := &app.APIConfig{
		AppEnv: "local", LogLevel: "debug", LogFormat: "json", OTelSamplerArg: 1,
		MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 5 * time.Second,
		InternalAddr:      "127.0.0.1:0",
		PublicAddr:        "127.0.0.1:0",
		PublicRouteGroups: ingress.PublicPrefixes,
		TrustedProxies:    prefixes,
	}
	if _, err := telemetry.SetupTracing(context.Background(), telemetry.TracingOptions{}); err != nil {
		t.Fatal(err)
	}
	a, err := app.NewAPI(cfg, log, testGroups()...)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{api: a, logs: logs, cancel: cancel, done: make(chan error, 1)}
	h.internal, h.public, h.metrics = a.Addrs()
	go func() { h.done <- a.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(10 * time.Second):
			t.Error("shutdown did not finish")
		}
	})
	waitReady(t, h.internal)
	return h
}

func waitReady(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get("http://" + addr + "/healthz"); err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("listener did not come up")
}

func get(t *testing.T, addr, path string, hdr map[string]string) (int, map[string]any, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	return resp.StatusCode, body, resp.Header
}

func errCode(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func TestHealthOnBothListenersReadinessInternalOnly(t *testing.T) {
	h := start(t)
	if st, _, _ := get(t, h.internal, "/healthz", nil); st != 200 {
		t.Fatalf("internal /healthz = %d", st)
	}
	if st, _, _ := get(t, h.public, "/healthz", nil); st != 200 {
		t.Fatalf("public /healthz = %d", st)
	}
	if st, _, _ := get(t, h.internal, "/readyz", nil); st != 200 {
		t.Fatalf("internal /readyz = %d", st)
	}
	if st, _, _ := get(t, h.internal, "/startupz", nil); st != 200 {
		t.Fatalf("internal /startupz = %d", st)
	}
	for _, p := range []string{"/readyz", "/startupz"} {
		st, body, _ := get(t, h.public, p, nil)
		if st != 404 || errCode(body) != "not_found" {
			t.Fatalf("public %s = %d %v, want 404 not_found", p, st, body)
		}
	}
}

func TestRouteOutsideAllowListIs404OnPublic(t *testing.T) {
	h := start(t)
	st, body, _ := get(t, h.internal, "/v1/staff-probe", nil)
	if st != 401 || errCode(body) != "unauthenticated" {
		t.Fatalf("internal staff route = %d %v, want 401 unauthenticated", st, body)
	}
	st, body, _ = get(t, h.public, "/v1/staff-probe", nil)
	if st != 404 || errCode(body) != "not_found" {
		t.Fatalf("public staff route = %d %v, want 404 not_found (not 401)", st, body)
	}
	// Allow-listed group is reachable on both.
	for _, addr := range []string{h.internal, h.public} {
		if st, _, _ := get(t, addr, "/v1/mobile/whoami", nil); st != 200 {
			t.Fatalf("%s /v1/mobile/whoami = %d", addr, st)
		}
	}
}

func TestPublicAllowListNarrowsGroups(t *testing.T) {
	logs := &syncBuffer{}
	log, _ := logx.New(logx.Options{Level: "info", Format: "json", Out: logs})
	cfg := &app.APIConfig{
		AppEnv: "local", LogLevel: "info", LogFormat: "json", OTelSamplerArg: 1,
		MetricsAddr: "127.0.0.1:0", ShutdownTimeout: 3 * time.Second,
		InternalAddr:      "127.0.0.1:0",
		PublicAddr:        "127.0.0.1:0",
		PublicRouteGroups: []string{"/healthz"}, // mobile not allowed
	}
	a, err := app.NewAPI(cfg, log, testGroups()...)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()
	defer func() { cancel(); <-done }()
	in, pub, _ := a.Addrs()
	waitReady(t, in)
	if st, _, _ := get(t, pub, "/v1/mobile/whoami", nil); st != 404 {
		t.Fatalf("mobile group must be hidden when not allow-listed, got %d", st)
	}
	if st, _, _ := get(t, in, "/v1/mobile/whoami", nil); st != 200 {
		t.Fatalf("internal still serves it, got %d", st)
	}
}

func TestPublicGroupOutsideFixedListIsRejected(t *testing.T) {
	log, _ := logx.New(logx.Options{Level: "info", Format: "json", Out: io.Discard})
	cfg := &app.APIConfig{PublicRouteGroups: ingress.PublicPrefixes}
	_, err := app.NewAPI(cfg, log, ingress.Group{Prefix: "/v1/me", Public: true, Mount: func(fiber.Router) {}})
	if err == nil || !strings.Contains(err.Error(), "marked public") {
		t.Fatalf("want ingress policy error, got %v", err)
	}
}

// The route table behind `api routes` (go-ci gen-check): the public listener holds only the
// allow-listed groups, the internal one every route; automatic HEAD routes are not listed.
func TestRoutesPerListener(t *testing.T) {
	log, _ := logx.New(logx.Options{Level: "info", Format: "json", Out: io.Discard})
	cfg := &app.APIConfig{PublicRouteGroups: ingress.PublicPrefixes}
	a, err := app.NewAPI(cfg, log, testGroups()...)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CheckRoutes(); err != nil {
		t.Fatal(err)
	}
	render := func(rs []ingress.Route) string {
		var parts []string
		for _, r := range rs {
			parts = append(parts, r.Method+" "+r.Path)
		}
		return strings.Join(parts, ", ")
	}
	routes := a.Routes()
	if got, want := render(routes[ingress.Public]),
		"GET /healthz, POST /v1/mobile/echo, GET /v1/mobile/panic, GET /v1/mobile/slow, GET /v1/mobile/whoami"; got != want {
		t.Fatalf("public routes:\n got %s\nwant %s", got, want)
	}
	if got, want := render(routes[ingress.Internal]),
		"GET /healthz, GET /readyz, GET /startupz, POST /v1/mobile/echo, GET /v1/mobile/panic, GET /v1/mobile/slow, "+
			"GET /v1/mobile/whoami, GET /v1/staff-probe, GET /v1/staff-probe/slow"; got != want {
		t.Fatalf("internal routes:\n got %s\nwant %s", got, want)
	}
}

func TestXForwardedForOnlyFromTrustedProxies(t *testing.T) {
	ip := func(h *harness, xff string) string {
		_, body, _ := get(t, h.public, "/v1/mobile/whoami", map[string]string{"X-Forwarded-For": xff})
		return body["data"].(map[string]any)["ip"].(string)
	}
	untrusted := start(t, "10.0.0.0/8")
	if got := ip(untrusted, "203.0.113.7"); got != "127.0.0.1" {
		t.Fatalf("untrusted peer: XFF honoured, ip = %s", got)
	}
	trusted := start(t, "127.0.0.1/32")
	if got := ip(trusted, "203.0.113.7"); got != "203.0.113.7" {
		t.Fatalf("trusted peer: ip = %s, want 203.0.113.7", got)
	}
	// Right-to-left walk: spoofed leftmost entries behind a trusted hop are ignored.
	if got := ip(trusted, "198.51.100.1, 203.0.113.7, 127.0.0.1"); got != "203.0.113.7" {
		t.Fatalf("chain: ip = %s, want 203.0.113.7", got)
	}
	if got := ip(trusted, "not-an-ip"); got != "127.0.0.1" {
		t.Fatalf("malformed XFF must fall back to the peer, got %s", got)
	}
}

func TestEveryRequestLogLineCarriesRequestIDAndIsRedacted(t *testing.T) {
	h := start(t)
	const rid = "0199c2f4-1111-7222-8333-444455556666"
	st, _, hdr := get(t, h.public, "/v1/mobile/whoami", map[string]string{
		httpx.HeaderRequestID: rid,
		"Authorization":       "Bearer secret-token-value",
	})
	if st != 200 || hdr.Get(httpx.HeaderRequestID) != rid {
		t.Fatalf("status %d, X-Request-Id %q", st, hdr.Get(httpx.HeaderRequestID))
	}
	var scoped []map[string]any
	for _, l := range h.logs.Lines() {
		if l["message"] == "handler line" || (l["message"] == "request" && l["route"] == "/v1/mobile/whoami") {
			scoped = append(scoped, l)
		}
	}
	if len(scoped) != 2 {
		t.Fatalf("want handler line + access line, got %d: %v", len(scoped), scoped)
	}
	for _, l := range scoped {
		if l["request_id"] != rid {
			t.Fatalf("log line without request_id %s: %v", rid, l)
		}
	}
	all, _ := json.Marshal(h.logs.Lines())
	if strings.Contains(string(all), "hunter2") || strings.Contains(string(all), "secret-token-value") {
		t.Fatalf("sensitive value reached the logs: %s", all)
	}
}

func TestPanicBecomes500EnvelopeWithMatchingRequestID(t *testing.T) {
	h := start(t)
	st, body, hdr := get(t, h.public, "/v1/mobile/panic", nil)
	if st != 500 || errCode(body) != "internal" {
		t.Fatalf("panic = %d %v", st, body)
	}
	e := body["error"].(map[string]any)
	if len(e) != 4 || e["requestId"] != hdr.Get(httpx.HeaderRequestID) {
		t.Fatalf("envelope %v vs header %q", e, hdr.Get(httpx.HeaderRequestID))
	}
	if strings.Contains(e["message"].(string), "boom") {
		t.Fatalf("panic value leaked: %v", e)
	}
}

func TestActOnTenantRejectedOnPublicListener(t *testing.T) {
	h := start(t)
	st, body, _ := get(t, h.public, "/v1/mobile/whoami", map[string]string{app.HeaderActOnTenant: "*"})
	if st != 400 || errCode(body) != "header_not_allowed" {
		t.Fatalf("public with X-Act-On-Tenant = %d %v", st, body)
	}
	if st, _, _ := get(t, h.internal, "/v1/mobile/whoami", map[string]string{app.HeaderActOnTenant: "*"}); st != 200 {
		t.Fatalf("internal must not reject the header itself (authz lands in T07), got %d", st)
	}
}

func TestMetricsServedOnSeparateAddressWithRouteTemplates(t *testing.T) {
	h := start(t)
	get(t, h.public, "/v1/mobile/whoami", nil)
	get(t, h.public, "/does/not/exist/abc123", nil)
	if st, _, _ := get(t, h.public, "/metrics", nil); st != 404 {
		t.Fatalf("/metrics must not be on the public listener, got %d", st)
	}
	resp, err := http.Get("http://" + h.metrics + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	text := string(b)
	if !strings.Contains(text, `route="/v1/mobile/whoami"`) || !strings.Contains(text, `route="unmatched"`) {
		t.Fatalf("route templates missing from metrics")
	}
	if strings.Contains(text, "abc123") {
		t.Fatalf("raw path leaked into metric labels")
	}
}

func TestShutdownTurnsReadinessUnavailableThenDrains(t *testing.T) {
	app.DrainGrace = 400 * time.Millisecond
	h := start(t)
	app.DrainGrace = 400 * time.Millisecond
	h.cancel()
	time.Sleep(100 * time.Millisecond) // inside the drain grace
	st, body, _ := get(t, h.internal, "/readyz", nil)
	if st != 503 || errCode(body) != "unavailable" {
		t.Fatalf("readyz during drain = %d %v", st, body)
	}
	if d := body["error"].(map[string]any)["details"].(map[string]any); d["reason"] != "draining" {
		t.Fatalf("reason = %v", d)
	}
	if st, _, _ := get(t, h.internal, "/healthz", nil); st != 200 {
		t.Fatalf("liveness must stay 200 while draining, got %d", st)
	}
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
		h.done <- nil // let cleanup observe completion
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not complete")
	}
	if _, err := http.Get("http://" + h.internal + "/healthz"); err == nil {
		t.Fatal("internal listener still accepting after shutdown")
	}
}

func rawRequest(t *testing.T, addr, raw string) (status int, header http.Header, body map[string]any) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &body)
	return resp.StatusCode, resp.Header, body
}

func TestUnknownMethodGetsEnvelopeLogAndMetric(t *testing.T) {
	h := start(t)
	for _, addr := range []string{h.public, h.internal} {
		for _, m := range []string{"PROPFIND", "BREW"} {
			st, hdr, body := rawRequest(t, addr, m+" /healthz HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
			if st != 405 || errCode(body) != "method_not_allowed" {
				t.Fatalf("%s %s = %d %v", addr, m, st, body)
			}
			e := body["error"].(map[string]any)
			if len(e) != 4 || e["requestId"] == "" || e["requestId"] != hdr.Get(httpx.HeaderRequestID) {
				t.Fatalf("envelope %v vs X-Request-Id %q", e, hdr.Get(httpx.HeaderRequestID))
			}
			found := false
			for _, l := range h.logs.Lines() {
				if l["message"] == "request" && l["request_id"] == e["requestId"] && l["status"] == float64(405) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no access line for %s %s", m, e["requestId"])
			}
		}
	}
	text := scrapeMetrics(t, h)
	if !strings.Contains(text, `method="PROPFIND",route="unmatched",service="api",status="405"`) {
		t.Fatalf("unknown method not counted")
	}
}

func TestBodyTooLargeRecordedWithRealStatus(t *testing.T) {
	h := start(t)
	// Declare a 5 MiB body but send none: fasthttp rejects on Content-Length
	// before reading, so the test does not race the server closing the socket.
	status, hdr, body := rawRequest(t, h.public, "POST /v1/mobile/echo HTTP/1.1\r\nHost: x\r\n"+
		"Content-Type: application/json\r\nContent-Length: 5242880\r\nConnection: close\r\n\r\n")
	if status != 413 || errCode(body) != "payload_too_large" {
		t.Fatalf("413 = %d %v", status, body)
	}
	rid := hdr.Get(httpx.HeaderRequestID)
	if rid == "" || body["error"].(map[string]any)["requestId"] != rid {
		t.Fatalf("requestId mismatch: %q vs %v", rid, body)
	}
	var lines []map[string]any
	for _, l := range h.logs.Lines() {
		if l["message"] == "request" && l["request_id"] == rid {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 || lines[0]["status"] != float64(413) {
		t.Fatalf("want exactly one access line with status 413, got %v", lines)
	}
	text := scrapeMetrics(t, h)
	if !strings.Contains(text, `status="413"`) || strings.Contains(text, `method="POST",route="unmatched",service="api",status="200"`) {
		t.Fatalf("413 not counted correctly")
	}
}

func scrapeMetrics(t *testing.T, h *harness) string {
	t.Helper()
	resp, err := http.Get("http://" + h.metrics + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestShutdownStopsBothListenersTogetherAndDrains(t *testing.T) {
	h := start(t)
	app.DrainGrace = 100 * time.Millisecond
	type result struct {
		status int
		err    error
	}
	inflight := func(addr, ms string) chan result {
		ch := make(chan result, 1)
		go func() {
			resp, err := http.Get("http://" + addr + "/v1/staff-probe/slow?ms=" + ms)
			if err != nil {
				ch <- result{err: err}
				return
			}
			_ = resp.Body.Close()
			ch <- result{status: resp.StatusCode}
		}()
		return ch
	}
	publicLong := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + h.public + "/v1/mobile/slow?ms=1500")
		if err != nil {
			publicLong <- result{err: err}
			return
		}
		_ = resp.Body.Close()
		publicLong <- result{status: resp.StatusCode}
	}()
	internalShort := inflight(h.internal, "700")
	time.Sleep(100 * time.Millisecond) // both requests are in flight
	h.cancel()

	time.Sleep(400 * time.Millisecond) // past the 100 ms grace
	if conn, err := net.DialTimeout("tcp", h.internal, 300*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("internal listener still accepts while the public one drains")
	}
	r := <-internalShort
	if r.err != nil || r.status != 200 {
		t.Fatalf("in-flight internal request must complete: %+v", r)
	}
	select {
	case err := <-h.done:
		t.Fatalf("Serve returned (%v) before the public request finished", err)
	default:
	}
	if r := <-publicLong; r.err != nil || r.status != 200 {
		t.Fatalf("in-flight public request must complete: %+v", r)
	}
	if err := <-h.done; err != nil {
		t.Fatalf("Serve returned %v", err)
	}
	h.done <- nil
}

func TestXForwardedForAcrossSeveralFieldLines(t *testing.T) {
	h := start(t, "127.0.0.1/32")
	req, _ := http.NewRequest("GET", "http://"+h.public+"/v1/mobile/whoami", nil)
	req.Header["X-Forwarded-For"] = []string{"198.51.100.66", "203.0.113.7"}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if ip := body["data"].(map[string]any)["ip"]; ip != "203.0.113.7" {
		t.Fatalf("ip = %v, want the right-most untrusted hop 203.0.113.7", ip)
	}
}

func TestEmptyOrDuplicateActOnTenantStillRejected(t *testing.T) {
	h := start(t)
	for _, hdr := range []string{"X-Act-On-Tenant:\r\n", "X-Act-On-Tenant:\r\nX-Act-On-Tenant: *\r\n", "x-act-on-tenant: *\r\n"} {
		st, _, body := rawRequest(t, h.public, "GET /v1/mobile/whoami HTTP/1.1\r\nHost: x\r\n"+hdr+"Connection: close\r\n\r\n")
		if st != 400 || errCode(body) != "header_not_allowed" {
			t.Fatalf("%q = %d %v", hdr, st, body)
		}
	}
}

func TestRootPathIsUnmatchedInMetrics(t *testing.T) {
	h := start(t)
	get(t, h.public, "/", nil)
	if text := scrapeMetrics(t, h); strings.Contains(text, `route="/"`) {
		t.Fatalf("unmatched / must be labelled route=\"unmatched\"")
	}
}

func TestPanicLogCarriesStack(t *testing.T) {
	h := start(t)
	get(t, h.public, "/v1/mobile/panic", nil)
	for _, l := range h.logs.Lines() {
		if l["message"] == "handler panic" {
			if s, _ := l["stack"].(string); !strings.Contains(s, "goroutine") {
				t.Fatalf("panic log without stack: %v", l)
			}
			return
		}
	}
	t.Fatal("no handler panic log line")
}
