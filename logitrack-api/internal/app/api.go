package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	fiberlog "github.com/gofiber/fiber/v3/log"
	recoverer "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/rs/zerolog"
	"github.com/valyala/fasthttp"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/health"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/telemetry"
)

// HeaderActOnTenant is the platform cross-tenant header; it is refused on the
// public listener (Appendix B §B.1.5) and applied by iam.RBAC on the internal one.
const HeaderActOnTenant = authz.HeaderActOnTenant

// API is the api process: two Fiber listeners built from one route registry
// plus the private metrics server.
type API struct {
	cfg     *APIConfig
	log     zerolog.Logger
	Health  *health.State
	metrics *telemetry.Metrics

	internal *fiber.App
	public   *fiber.App

	internalLn net.Listener
	publicLn   net.Listener
	metricsSrv *telemetry.MetricsServer
}

// BaseGroups are the route groups every api build has: liveness on both
// listeners, readiness and startup on the internal one only.
func BaseGroups(h *health.State) []ingress.Group {
	return []ingress.Group{
		{Prefix: "/healthz", Public: true, Mount: func(r fiber.Router) { r.Get("", h.Healthz) }},
		{Prefix: "/readyz", Mount: func(r fiber.Router) { r.Get("", h.Readyz) }},
		{Prefix: "/startupz", Mount: func(r fiber.Router) { r.Get("", h.Startupz) }},
	}
}

// NewAPI builds both listeners. Extra groups (the domain routes of later
// tasks, or test routes) are mounted after the base groups.
func NewAPI(cfg *APIConfig, log zerolog.Logger, extra ...ingress.Group) (*API, error) {
	a := &API{
		cfg:     cfg,
		log:     log,
		Health:  health.NewState(),
		metrics: telemetry.NewMetrics("api"),
	}
	groups := append(BaseGroups(a.Health), extra...)
	if err := ingress.Validate(groups); err != nil {
		return nil, err
	}
	// Fiber's own log lines (rare: listener and error-handler failures) go
	// through the redacting zerolog writer as JSON instead of plain stderr.
	fiberlog.SetOutput(fiberLogWriter{log: log})
	a.internal = a.newFiber(ingress.Internal)
	a.public = a.newFiber(ingress.Public)
	ingress.Mount(a.internal, ingress.Internal, groups, nil)
	ingress.Mount(a.public, ingress.Public, groups, cfg.PublicRouteGroups)
	a.uploadLimit(a.internal, ingress.Uploads(ingress.Internal, groups, nil))
	a.uploadLimit(a.public, ingress.Uploads(ingress.Public, groups, cfg.PublicRouteGroups))
	return a, nil
}

// DefaultBodyLimit is the API's request body limit (Appendix B §B.1.5: 413 payload_too_large above it).
const DefaultBodyLimit = 4 << 20

// UploadReadTimeout replaces the 30 s read timeout of a signed upload: a 10 MB photo over a slow mobile link
// takes longer than that.
const UploadReadTimeout = 5 * time.Minute

// uploadLimit gives a PUT below an upload route (the local storage backend's uploads, T11) its own body limit and
// read timeout when the route's check passes on the request head: fasthttp calls HeaderReceived after the headers
// and before it reads (or allocates for) the body. The check verifies the upload signature, which covers the
// declared size, so the limit is that size (at most UPLOAD_MAX_BYTES). Every other request, an unsigned or
// tampered upload included, keeps the 4 MiB / 30 s defaults and is refused with 413 above 4 MiB before its body
// is buffered; the handler verifies the signature again.
func (a *API) uploadLimit(app *fiber.App, uploads []ingress.Upload) {
	if len(uploads) == 0 || a.cfg.UploadMaxBytes <= 0 {
		return
	}
	app.Server().HeaderReceived = func(h *fasthttp.RequestHeader) fasthttp.RequestConfig {
		if !h.IsPut() {
			return fasthttp.RequestConfig{}
		}
		p, q := requestTarget(h.RequestURI())
		for _, u := range uploads {
			if !strings.HasPrefix(p, u.Prefix) {
				continue
			}
			n := u.Limit(ingress.UploadHead{Path: p, Query: q, ContentType: string(h.ContentType()), ContentLength: h.ContentLength()})
			if n > 0 && n <= a.cfg.UploadMaxBytes {
				return fasthttp.RequestConfig{MaxRequestBodySize: int(n), ReadTimeout: UploadReadTimeout}
			}
			return fasthttp.RequestConfig{}
		}
		return fasthttp.RequestConfig{}
	}
}

// requestTarget splits a request target (origin form, or absolute form "http://host/path?query") into the raw
// path Fiber routes on and the raw query.
func requestTarget(uri []byte) (path, query string) {
	s := string(uri)
	if i := strings.Index(s, "://"); i >= 0 && !strings.HasPrefix(s, "/") {
		s = s[i+3:]
		if j := strings.IndexAny(s, "/?#"); j >= 0 && s[j] == '/' {
			s = s[j:]
		} else if j >= 0 {
			s = "/" + s[j:]
		} else {
			s = "/"
		}
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	path, query, _ = strings.Cut(s, "?")
	return path, query
}

// Routes returns the routes each listener serves, keyed by ingress.Internal and ingress.Public.
func (a *API) Routes() map[string][]ingress.Route {
	return map[string][]ingress.Route{
		ingress.Internal: ingress.Routes(a.internal),
		ingress.Public:   ingress.Routes(a.public),
	}
}

// CheckRoutes applies the route-level ingress check to the public listener.
func (a *API) CheckRoutes() error {
	return ingress.CheckPublicRoutes(ingress.Routes(a.public))
}

// RootMiddleware returns, per listener, the number of handlers registered with Use at "/"
// (ingress.RootMiddleware): the chain newFiber installs, which no route check can see into.
func (a *API) RootMiddleware() map[string]int {
	return map[string]int{
		ingress.Internal: ingress.RootMiddleware(a.internal),
		ingress.Public:   ingress.RootMiddleware(a.public),
	}
}

func (a *API) newFiber(listener string) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "logitrack-api-" + listener,
		ErrorHandler: httpx.ErrorHandler(a.log),
		ReadTimeout:  30 * time.Second,
		// No write timeout: SSE responses stream for minutes (main spec §8).
		IdleTimeout: 120 * time.Second,
		BodyLimit:   DefaultBodyLimit,
		// Header and path strings are copied, so values captured by logs and
		// spans (exported after the request) never alias reused buffers.
		Immutable: true,
	})
	// Listener-wide middleware runs for every path, so the route check cannot see what it serves:
	// never add one that answers paths of its own (pprof, expvar, static files, a proxy). A test
	// pins the number of these handlers (TestRootMiddlewareIsPinned).
	app.Use(
		httpx.RequestID(),
		httpx.Listener(listener),
		httpx.ClientIP(a.cfg.TrustedProxies),
		httpx.Logger(a.log),
		telemetry.Tracing(listener),
		a.metrics.Middleware(listener),
		recoverer.New(recoverer.Config{EnableStackTrace: true, StackTraceHandler: a.logPanic}),
	)
	if listener == ingress.Public {
		app.Use(httpx.RejectHeader(HeaderActOnTenant))
	}
	a.wrapEdge(app, listener)
	return app
}

// wrapEdge covers the two paths where fasthttp answers before Fiber's router:
//
//   - an HTTP method Fiber does not know (PROPFIND, BREW, ...) would get a bare
//     "501 Not Implemented"; it gets the 405 method_not_allowed envelope;
//   - a request fasthttp rejects while reading (413 body too large, 431 header
//     too large, 408 timeout) runs the Use chain before its status exists; the
//     middleware skips it and the access line and metric are written here.
//
// fiber.New installs both handlers on the fasthttp server and Listener keeps
// them, so wrapping after New is stable.
func (a *API) wrapEdge(app *fiber.App, listener string) {
	srv := app.Server()
	methods := app.Config().RequestMethods
	next := srv.Handler
	srv.Handler = func(rc *fasthttp.RequestCtx) {
		if slices.Contains(methods, string(rc.Method())) {
			next(rc)
			return
		}
		start := time.Now()
		id := httpx.RequestIDOrNew(string(rc.Request.Header.Peek(httpx.HeaderRequestID)))
		httpx.WriteRawError(rc, httpx.NewError(fiber.StatusMethodNotAllowed, httpx.CodeMethodNotAllowed, "method not allowed"), id)
		a.edgeRecord(rc, listener, id, start)
	}
	onErr := srv.ErrorHandler
	srv.ErrorHandler = func(rc *fasthttp.RequestCtx, err error) {
		start := time.Now()
		httpx.MarkFrameworkError(rc)
		onErr(rc, err)
		a.edgeRecord(rc, listener, string(rc.Response.Header.Peek(httpx.HeaderRequestID)), start)
	}
}

// edgeRecord writes the access line and metric for a request answered at the
// fasthttp edge; the route is always "unmatched".
func (a *API) edgeRecord(rc *fasthttp.RequestCtx, listener, requestID string, start time.Time) {
	status := rc.Response.StatusCode()
	method := string(rc.Method())
	if len(method) > 16 {
		method = method[:16] // bound metric label cardinality for junk methods
	}
	ev := a.log.Warn()
	if status >= 500 {
		ev = a.log.Error()
	}
	ev.Str("request_id", requestID).Str("listener", listener).Str("method", method).
		Str("route", "unmatched").Int("status", status).Dur("latency", time.Since(start)).
		Str("ip", rc.RemoteIP().String()).Msg("request")
	a.metrics.Observe(listener, method, "unmatched", status, time.Since(start))
}

// fiberLogWriter turns Fiber's text log lines into zerolog JSON lines.
type fiberLogWriter struct{ log zerolog.Logger }

func (w fiberLogWriter) Write(p []byte) (int, error) {
	w.log.Warn().Str("component", "fiber").Msg(string(bytes.TrimSpace(p)))
	return len(p), nil
}

func (a *API) logPanic(c fiber.Ctx, e any) {
	l := zerolog.Ctx(c.Context())
	if l.GetLevel() == zerolog.Disabled {
		l = &a.log
	}
	l.Error().Interface("panic", e).Str("route", httpx.RouteTemplate(c)).
		Str("stack", string(debug.Stack())).Msg("handler panic")
}

// Listen binds the three addresses. Bind errors are returned before anything
// is served, so a port clash fails the process at startup.
func (a *API) Listen() error {
	var err error
	if a.internalLn, err = net.Listen("tcp", a.cfg.InternalAddr); err != nil {
		return fmt.Errorf("listen API_INTERNAL_ADDR: %w", err)
	}
	if a.publicLn, err = net.Listen("tcp", a.cfg.PublicAddr); err != nil {
		_ = a.internalLn.Close()
		return fmt.Errorf("listen API_PUBLIC_ADDR: %w", err)
	}
	if a.metricsSrv, err = telemetry.ListenMetrics(a.cfg.MetricsAddr, a.metrics.Registry, a.log, nil); err != nil {
		_ = a.internalLn.Close()
		_ = a.publicLn.Close()
		return fmt.Errorf("listen METRICS_ADDR: %w", err)
	}
	return nil
}

// Addrs returns the bound internal, public and metrics addresses.
func (a *API) Addrs() (internal, public, metrics string) {
	return a.internalLn.Addr().String(), a.publicLn.Addr().String(), a.metricsSrv.Addr()
}

// Serve runs both listeners until ctx is cancelled, then shuts down
// gracefully: readiness turns 503, a short grace lets load balancers notice,
// and in-flight requests drain within SHUTDOWN_TIMEOUT.
func (a *API) Serve(ctx context.Context) error {
	if a.internalLn == nil {
		if err := a.Listen(); err != nil {
			return err
		}
	}
	serveErr := make(chan error, 2)
	for _, s := range []struct {
		app *fiber.App
		ln  net.Listener
	}{{a.internal, a.internalLn}, {a.public, a.publicLn}} {
		go func() {
			serveErr <- s.app.Listener(s.ln, fiber.ListenConfig{DisableStartupMessage: true})
		}()
	}
	a.Health.MarkStarted()
	in, pub, met := a.Addrs()
	a.log.Info().Str("internal_addr", in).Str("public_addr", pub).Str("metrics_addr", met).
		Strs("public_route_groups", a.cfg.PublicRouteGroups).Msg("api listening")

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-serveErr:
		a.log.Error().Err(runErr).Msg("listener stopped unexpectedly")
	}
	shutdownErr := a.Shutdown()
	return errors.Join(runErr, shutdownErr)
}

// DrainGrace is how long readiness reports 503 before listeners stop
// accepting, capped at a third of SHUTDOWN_TIMEOUT.
var DrainGrace = 5 * time.Second

// Shutdown performs the graceful stop sequence. It is safe to call once.
func (a *API) Shutdown() error {
	a.Health.BeginDrain()
	grace := min(DrainGrace, a.cfg.ShutdownTimeout/3)
	a.log.Info().Dur("grace", grace).Dur("timeout", a.cfg.ShutdownTimeout).Msg("draining")
	time.Sleep(grace)

	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout-grace)
	defer cancel()
	// All three stop together: each closes its listener now and drains its
	// in-flight requests for the whole remaining budget.
	stops := []func(context.Context) error{
		a.public.ShutdownWithContext, a.internal.ShutdownWithContext, a.metricsSrv.Shutdown,
	}
	errs := make([]error, len(stops))
	var wg sync.WaitGroup
	for i, stop := range stops {
		wg.Go(func() { errs[i] = stop(ctx) })
	}
	wg.Wait()
	err := errors.Join(errs...)
	if err != nil {
		a.log.Error().Err(err).Msg("shutdown incomplete")
		return err
	}
	a.log.Info().Msg("shutdown complete")
	return nil
}
