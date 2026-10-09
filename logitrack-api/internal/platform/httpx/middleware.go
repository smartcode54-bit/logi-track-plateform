package httpx

import (
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/valyala/fasthttp"
)

// HeaderRequestID is echoed on every response and carried into logs, traces
// and outbox headers (main spec §2.5).
const HeaderRequestID = "X-Request-Id"

type localKey int

const (
	keyRequestID localKey = iota
	keyClientIP
	keyListener
	keyFrameworkError
)

// validRequestID accepts ids minted by the BFF or another trusted hop
// (uuid-like tokens). Anything else is replaced so a client cannot inject
// arbitrary text into logs.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,128}$`)

// RequestID reuses a well-formed incoming X-Request-Id or mints a UUIDv7, stores
// it for handlers and the logger, and echoes it on the response.
func RequestID() fiber.Handler {
	return func(c fiber.Ctx) error {
		id := RequestIDOrNew(c.Get(HeaderRequestID))
		c.Locals(keyRequestID, id)
		c.Set(HeaderRequestID, id)
		return c.Next()
	}
}

// RequestIDOrNew returns a copy of incoming when it is a well-formed id, or a
// fresh UUIDv7. The copy matters: Fiber header strings alias reusable buffers.
func RequestIDOrNew(incoming string) string {
	if validRequestID.MatchString(incoming) {
		return strings.Clone(incoming)
	}
	return newID()
}

func newID() string {
	if u, err := uuid.NewV7(); err == nil {
		return u.String()
	}
	return uuid.NewString()
}

// RequestIDFrom returns the id stored by RequestID, or "" outside a request.
func RequestIDFrom(c fiber.Ctx) string {
	if v, ok := c.Locals(keyRequestID).(string); ok {
		return v
	}
	return ""
}

// ClientIP resolves the real client address. X-Forwarded-For is honoured only
// when the TCP peer is inside trusted (Caddy on the public listener, the web
// container on the internal one); the header is then walked right to left and
// the first hop outside trusted wins. Untrusted peers get their own address.
func ClientIP(trusted []netip.Prefix) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Locals(keyClientIP, resolveClientIP(c, trusted))
		return c.Next()
	}
}

func resolveClientIP(c fiber.Ctx, trusted []netip.Prefix) string {
	peer, ok := netip.AddrFromSlice(c.RequestCtx().RemoteIP())
	if !ok {
		return ""
	}
	peer = peer.Unmap()
	if !inPrefixes(peer, trusted) {
		return peer.String()
	}
	// RFC 9110 §5.3: several X-Forwarded-For field lines form one ordered list.
	var hops []string
	for _, line := range c.Request().Header.PeekAll(fiber.HeaderXForwardedFor) {
		hops = append(hops, strings.Split(string(line), ",")...)
	}
	if len(hops) == 0 {
		return peer.String()
	}
	leftmost := ""
	for _, hop := range slices.Backward(hops) {
		addr, err := netip.ParseAddr(strings.TrimSpace(hop))
		if err != nil {
			// A malformed hop ends the trusted chain: fall back to the peer.
			return peer.String()
		}
		addr = addr.Unmap()
		leftmost = addr.String()
		if !inPrefixes(addr, trusted) {
			return leftmost
		}
	}
	return leftmost
}

func inPrefixes(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ClientIPFrom returns the address stored by ClientIP.
func ClientIPFrom(c fiber.Ctx) string {
	if v, ok := c.Locals(keyClientIP).(string); ok {
		return v
	}
	return ""
}

// Listener tags the request with the listener name ("internal" or "public").
func Listener(name string) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Locals(keyListener, name)
		return c.Next()
	}
}

// ListenerFrom returns the listener name stored by Listener.
func ListenerFrom(c fiber.Ctx) string {
	if v, ok := c.Locals(keyListener).(string); ok {
		return v
	}
	return ""
}

// RouteTemplate is the matched route pattern ("/v1/trips/:id"), or "unmatched".
// Logs and metrics use it instead of the raw path, which may carry evidence
// tokens or ids.
func RouteTemplate(c fiber.Ctx) string {
	r := c.Route()
	if r == nil || r.Path == "" || !c.Matched() {
		return "unmatched"
	}
	return r.Path
}

// Logger attaches a request-scoped logger (with request_id and listener) to the
// request context and writes one access line per request. It must run after
// RequestID and ClientIP and before handlers that log.
func Logger(base zerolog.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		if IsFrameworkError(c) {
			// The edge handler writes the access line once the final status is known.
			return c.Next()
		}
		start := time.Now()
		l := base.With().
			Str("request_id", RequestIDFrom(c)).
			Str("listener", ListenerFrom(c)).
			Logger()
		c.SetContext(l.WithContext(c.Context()))

		err := c.Next()
		status := c.Response().StatusCode()
		if err != nil {
			// The error handler has not run yet; report the status it will use.
			status = AsError(err).Status
		}
		ev := l.Info()
		switch {
		case status >= 500 && status != fiber.StatusServiceUnavailable:
			ev = l.Error()
		case status >= 400:
			ev = l.Warn() // includes 503 unavailable, e.g. readiness while draining
		}
		ev.Str("method", c.Method()).
			Str("route", RouteTemplate(c)).
			Int("status", status).
			Dur("latency", time.Since(start)).
			Str("ip", ClientIPFrom(c)).
			Msg("request")
		return err
	}
}

// RejectHeader answers 400 header_not_allowed when the header is present. The
// public listener uses it for X-Act-On-Tenant (Appendix B §B.1.5).
func RejectHeader(name string) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Presence, not value: an empty or duplicated header must not slip through.
		if len(c.Request().Header.PeekAll(name)) > 0 {
			return NewError(400, CodeHeaderNotAllowed, "header not allowed on this listener").
				WithDetails(map[string]any{"header": name})
		}
		return c.Next()
	}
}

// MarkFrameworkError flags a request that fasthttp rejected before routing
// (body too large, header too large, timeout). Fiber then replays the Use
// middleware without a status; Logger and the metrics/tracing middleware skip
// such requests and the edge handler records them after the error response.
func MarkFrameworkError(rc *fasthttp.RequestCtx) { rc.SetUserValue(keyFrameworkError, true) }

// IsFrameworkError reports whether MarkFrameworkError flagged this request.
func IsFrameworkError(c fiber.Ctx) bool {
	v, _ := c.RequestCtx().UserValue(keyFrameworkError).(bool)
	return v
}
