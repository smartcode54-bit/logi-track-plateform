// Package sse serves the realtime streams of main spec §8 (Appendix B §B.4, issue T12):
//
//   - GET /v1/events on the internal listener: the web stream, reached through the BFF passthrough with
//     the access token as a bearer (auth.RequireAuth, so iam.RBAC resolved the principal); never a
//     ticket;
//   - GET /v1/mobile/events?ticket= on the public listener (and the internal one): the driver-app
//     stream, authenticated by a single-use ticket from POST /v1/auth/sse-ticket (never a bearer in the
//     query string, R4); 404 unless MOBILE_SSE_ENABLED.
//
// A stream subscribes to the implicit topics of its principal (WebTopics, MobileTopics) plus explicit
// chat:{id} topics of ?topics= that the principal may read, holds a lease in rl:sse_conns:{userId}
// (at most SSE_MAX_CONN_PER_USER per user, else 429), replays what it missed after Last-Event-ID from
// the rtlog: streams (or sends resync), then relays the live events of its replica's realtime.Hub.
// It sends ": ping" every SSE_PING_INTERVAL and ends with event: reconnect 30 s before the access
// token expires, on shutdown and when it falls behind; session.revoked for its own session (or with
// reason claims_changed) and roles.changed on its tenant end it so the client reopens with fresh claims,
// including when they were published while the stream was connecting (serve).
//
// Wire format: "retry: 3000" first; every event is
//
//	id: <seq>
//	event: <name>        (realtime.EventName)
//	data: {"type":"<name>","topic":"<topic>","eventId":"<uuid>","data":<payload>}
//
// The control events reconnect and resync carry {"reason": ...}; resync and the frame after a replay
// carry id: <rtlog:seq at connect>, so the browser's Last-Event-ID moves past events of other topics.
package sse

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	rt "github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/sse/ssedb"
)

// Fixed rules of main spec §8 (code constants).
const (
	// Retry is the retry: field, the browser's reconnect delay.
	Retry = 3 * time.Second
	// ExpiryLead: a stream ends with reconnect this long before its access token expires (§8.1).
	ExpiryLead = 30 * time.Second
	// WriteTimeout bounds one write to a client that stopped reading; the stream then ends.
	WriteTimeout = 10 * time.Second
	// MaxExplicitTopics caps ?topics=.
	MaxExplicitTopics = 20
	// LimitRetryAfter is the Retry-After of the 429 past SSE_MAX_CONN_PER_USER: the tab falls back to
	// refetch on focus and a 60 s poll (§8.2).
	LimitRetryAfter = 60 * time.Second
	// UnavailableRetryAfter is the Retry-After of a 503 while the fan-out or Redis is down.
	UnavailableRetryAfter = 3 * time.Second
	// DefaultLife bounds a stream whose principal carries no token expiry.
	DefaultLife = 15 * time.Minute
)

// Control events and the reasons of event: reconnect.
const (
	EventReconnect = "reconnect"
	EventResync    = "resync"

	ReasonTokenExpiring   = "token_expiring"
	ReasonRolesChanged    = "roles_changed"
	ReasonConnectionLimit = "connection_limit"
	// The hub's reasons: realtime.EndShutdown, realtime.EndLagging, realtime.EndInterrupted.

	// endSessionRevoked and endClientGone end a stream without a reconnect event (metric label only).
	endSessionRevoked = "session_revoked"
	endClientGone     = "client_gone"
)

// Stream kinds (metric label).
const (
	KindWeb    = "web"
	KindMobile = "mobile"
)

// HeaderLastEventID is the reconnect position the browser sends (forwarded by the BFF).
const HeaderLastEventID = "Last-Event-ID"

// QueryLastEventID is the query form of the position on GET /v1/events, read only without the header:
// the web provider recreates the stream with ?lastEventId= (main spec §10.8), and the browser's own
// reconnect of that URL sends a newer header.
const QueryLastEventID = "lastEventId"

// Config is the SSE part of the api configuration (main spec §16.1).
type Config struct {
	PingInterval   time.Duration // SSE_PING_INTERVAL
	MaxConnPerUser int           // SSE_MAX_CONN_PER_USER
	MobileEnabled  bool          // MOBILE_SSE_ENABLED
}

// TicketAuthenticator redeems a driver-app SSE ticket (auth.Service.SSETicketPrincipal).
type TicketAuthenticator interface {
	SSETicketPrincipal(ctx context.Context, ticket string) (*authz.Principal, error)
}

// SessionChecker re-checks a principal's session once its stream has subscribed
// (auth.Service.CheckSession): revoked is 401 session_revoked, a stale AuthVersion 401 token_expired
// with details.reason claims_changed.
type SessionChecker interface {
	CheckSession(ctx context.Context, p *authz.Principal) error
}

// Deps are the collaborators of the service.
type Deps struct {
	Hub     *rt.Hub
	Reader  *rt.Reader
	Conns   *ratelimit.ConnLimiter
	Tickets TicketAuthenticator
	// Sessions re-checks the principal after the stream subscribed (serve).
	Sessions SessionChecker
	// Pool is the logitrack_app pool: explicit chat topics are authorised by reading the chat under
	// db.WithPrincipal, so RLS decides.
	Pool db.Beginner
	Log  zerolog.Logger
}

// Service serves the two stream routes.
type Service struct {
	cfg      Config
	hub      *rt.Hub
	reader   *rt.Reader
	conns    *ratelimit.ConnLimiter
	tickets  TicketAuthenticator
	sessions SessionChecker
	pool     db.Beginner
	log      zerolog.Logger

	open    *prometheus.GaugeVec
	ends    *prometheus.CounterVec
	resyncs *prometheus.CounterVec
	refused *prometheus.CounterVec
}

// New builds the service.
func New(cfg Config, d Deps) (*Service, error) {
	if d.Hub == nil || d.Reader == nil || d.Conns == nil || d.Tickets == nil || d.Sessions == nil || d.Pool == nil {
		return nil, errors.New("sse: hub, reader, connection limiter, ticket authenticator, session checker and pool are required")
	}
	if cfg.PingInterval <= 0 || cfg.MaxConnPerUser < 1 {
		return nil, errors.New("sse: SSE_PING_INTERVAL and SSE_MAX_CONN_PER_USER must be positive")
	}
	return &Service{
		cfg: cfg, hub: d.Hub, reader: d.Reader, conns: d.Conns, tickets: d.Tickets, sessions: d.Sessions, pool: d.Pool, log: d.Log,
		open: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "sse_streams",
			Help: "Open SSE streams of this replica, by kind (web, mobile).",
		}, []string{"kind"}),
		ends: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sse_stream_ends_total",
			Help: "SSE streams that ended, by reason (client_gone, token_expiring, shutdown, lagging, interrupted, session_revoked, roles_changed, connection_limit).",
		}, []string{"reason"}),
		resyncs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sse_resyncs_total",
			Help: "Reconnects whose Last-Event-ID could not be replayed, by reason (unknown_id, trimmed, expired, too_far_behind).",
		}, []string{"reason"}),
		refused: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sse_refused_total",
			Help: "SSE connections refused before streaming, by reason (connection_limit, unavailable).",
		}, []string{"reason"}),
	}, nil
}

// Register adds the service's metrics to reg.
func (s *Service) Register(reg prometheus.Registerer) error {
	for _, c := range []prometheus.Collector{s.open, s.ends, s.resyncs, s.refused} {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}

// Drain ends every stream of the replica with event: reconnect {"reason":"shutdown"} and refuses new
// ones (503); the api calls it after the readiness grace, before the listeners stop.
func (s *Service) Drain() { s.hub.Drain() }

// Groups are the route groups of the two streams: /v1/events (internal only) behind requireAuth, and
// /v1/mobile with GET /v1/mobile/events (public). ingress allows one group per prefix: the driver-app
// API group (T55) takes MountMobile into its own /v1/mobile group when it lands. Groups only registers
// handlers and never reads s, so cmd/api `routes` can pass a nil *Service.
func (s *Service) Groups(requireAuth fiber.Handler) []ingress.Group {
	return []ingress.Group{
		{Prefix: "/v1/events", Mount: func(r fiber.Router) { r.Get("", s.markSince, requireAuth, s.handleWeb) }},
		{Prefix: "/v1/mobile", Public: true, Mount: s.MountMobile},
	}
}

// sinceKey is the Locals key of the rtlog:seq markSince read.
type sinceKey struct{}

// since is rtlog:seq read before the credential was checked, or why it could not be.
type since struct {
	seq int64
	err error
}

// markSince reads rtlog:seq before requireAuth checks the credential. The events after it are the ones
// the credential may not reflect: serve reads the stream's control topics from there (main spec §8.2,
// refinement 6). A failure is kept for handleWeb, so a request without a valid credential is still 401.
func (s *Service) markSince(c fiber.Ctx) error {
	if c.Method() != fiber.MethodHead {
		seq, err := s.reader.Seq(c.Context())
		c.Locals(sinceKey{}, since{seq: seq, err: err})
	}
	return c.Next()
}

// MountMobile registers GET /events on a /v1/mobile router.
func (s *Service) MountMobile(r fiber.Router) { r.Get("/events", s.handleMobile) }

// handleWeb is GET /v1/events: bearer only (RequireAuth ran), ?topics= optional, never a ticket.
func (s *Service) handleWeb(c fiber.Ctx) error {
	if c.Method() == fiber.MethodHead {
		return headOnly(c)
	}
	if err := checkQuery(c, "topics", QueryLastEventID); err != nil {
		return err
	}
	p := authz.PrincipalFrom(c)
	if p == nil {
		return httpx.ErrUnauthenticated()
	}
	mark, ok := c.Locals(sinceKey{}).(since)
	if !ok {
		mark.err = errors.New("sse: rtlog:seq was not read before authentication")
	}
	if mark.err != nil {
		s.refused.WithLabelValues("unavailable").Inc()
		return errUnavailable(c, mark.err)
	}
	topics, err := s.topics(c.Context(), p, WebTopics(p), c.Query("topics"))
	if err != nil {
		return err
	}
	return s.serve(c, p, KindWeb, topics, mark.seq)
}

// handleMobile is GET /v1/mobile/events?ticket=[&topics=]: the ticket is the only credential.
func (s *Service) handleMobile(c fiber.Ctx) error {
	if !s.cfg.MobileEnabled {
		return httpx.ErrNotFound()
	}
	if c.Method() == fiber.MethodHead {
		return headOnly(c)
	}
	if err := checkQuery(c, "ticket", "topics"); err != nil {
		return err
	}
	explicit, err := splitTopics(c.Query("topics"))
	if err != nil {
		return err // before the ticket is spent
	}
	// Before the ticket is redeemed, for the same reason as markSince.
	seq, err := s.reader.Seq(c.Context())
	if err != nil {
		s.refused.WithLabelValues("unavailable").Inc()
		return errUnavailable(c, err)
	}
	p, err := s.tickets.SSETicketPrincipal(c.Context(), c.Query("ticket"))
	if err != nil {
		return err
	}
	topics, err := s.authorizeTopics(c.Context(), p, MobileTopics(p), explicit)
	if err != nil {
		return err
	}
	return s.serve(c, p, KindMobile, topics, seq)
}

// headOnly answers HEAD without opening a stream.
func headOnly(c fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, fiber.MIMETextEventStream)
	c.Set(fiber.HeaderCacheControl, "no-cache, no-transform")
	return c.SendStatus(http.StatusOK)
}

// checkQuery refuses query parameters outside allowed (400 bad_request, Appendix B §B.1.5); a ticket
// is accepted on /v1/mobile/events only, lastEventId on /v1/events only.
func checkQuery(c fiber.Ctx, allowed ...string) error {
	for k := range c.Queries() {
		if slices.Contains(allowed, k) {
			continue
		}
		if k == "ticket" {
			return httpx.ErrBadRequest("SSE tickets are accepted on GET /v1/mobile/events only").
				WithDetails(map[string]any{"reason": "ticket_not_accepted"})
		}
		return httpx.ErrBadRequest("unknown query parameter " + k)
	}
	return nil
}

// splitTopics parses ?topics= (comma separated); each entry must be a catalogue topic, and there are at
// most MaxExplicitTopics. A bad value of the known parameter is 422 invalid_argument with
// details.fields[0].field "topics" (Appendix B §B.1.5), reason unknown_topic or too_many.
func splitTopics(raw string) ([]string, error) {
	var out []string
	for t := range strings.SplitSeq(raw, ",") {
		if t = strings.TrimSpace(t); t == "" {
			continue
		}
		if !rt.ValidTopic(t) {
			return nil, httpx.ErrInvalidArgument(httpx.FieldViolation{
				Field: "topics", Reason: "unknown_topic", Params: map[string]any{"topic": t},
			})
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	if len(out) > MaxExplicitTopics {
		return nil, httpx.ErrInvalidArgument(httpx.FieldViolation{
			Field: "topics", Reason: "too_many", Params: map[string]any{"max": MaxExplicitTopics},
		})
	}
	return out, nil
}

func (s *Service) topics(ctx context.Context, p *authz.Principal, implicit []string, raw string) ([]string, error) {
	explicit, err := splitTopics(raw)
	if err != nil {
		return nil, err
	}
	return s.authorizeTopics(ctx, p, implicit, explicit)
}

// authorizeTopics adds the explicit topics to the implicit set: one already implicit is a no-op, a
// chat:{id} the principal can read joins, anything else is 403 permission_denied.
func (s *Service) authorizeTopics(ctx context.Context, p *authz.Principal, implicit, explicit []string) ([]string, error) {
	out := slices.Clone(implicit)
	for _, t := range explicit {
		if slices.Contains(out, t) {
			continue
		}
		ok := false
		if id, isChat := strings.CutPrefix(t, "chat:"); isChat {
			var err error
			if ok, err = s.chatVisible(ctx, p, id); err != nil {
				return nil, err
			}
		}
		if !ok {
			return nil, httpx.NewError(http.StatusForbidden, authz.CodePermissionDenied, "topic not allowed").
				WithDetails(map[string]any{"topic": t})
		}
		out = append(out, t)
	}
	slices.Sort(out)
	return out, nil
}

// chatVisible reports whether the principal may follow chat:{id}: the chat's own driver, or staff with
// chat:view whose RLS reach (tenant and contractor reach) includes the chat (Appendix B §B.4.2).
func (s *Service) chatVisible(ctx context.Context, p *authz.Principal, raw string) (bool, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return false, nil
	}
	visible := false
	err = db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error {
		row, err := ssedb.New(tx).ChatAudience(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ownDriver := p.DriverID != nil && row.DriverID != nil && *row.DriverID == *p.DriverID
		staff := p.Can(authz.ChatView) && !p.IsCustomerScope() && p.EffectiveRole() != authz.Driver
		visible = ownDriver || staff
		return nil
	})
	return visible, err
}

// errUnavailable is the 503 of a stream that cannot start (fan-out not subscribed, Redis down,
// draining).
func errUnavailable(c fiber.Ctx, cause error) error {
	c.Set(fiber.HeaderRetryAfter, strconv.Itoa(int(UnavailableRetryAfter/time.Second)))
	return httpx.ErrUnavailable("realtime stream unavailable; retry shortly").Wrap(cause)
}

// serve takes the lease, subscribes, reads the replay snapshot and starts the stream. Everything that
// can fail does so before the first byte, so a refusal is an ordinary JSON error.
//
// The principal was authenticated before the subscription existed, so a revocation or role change
// published in between would reach the stream neither live (it came before Subscribe, or its id is at
// or below the snapshot's Seq) nor as a reason to end. Two checks close that window (main spec §8.2,
// refinement 6): once subscribed and after the snapshot, the session is checked again (a revoked one is
// 401 session_revoked, stale claims 401 token_expired with claims_changed: the revocation markers and
// versions are written before session.revoked is published); and the snapshot reads the stream's
// control topics (user:{uid}, tenant:{tid}:config) from sinceSeq, rtlog:seq read before the
// credential was checked, so an ending event in (sinceSeq, Seq] ends the stream at once (roles.changed
// has no marker to re-check). Anything published after Seq arrives live.
func (s *Service) serve(c fiber.Ctx, p *authz.Principal, kind string, topics []string, sinceSeq int64) error {
	ctx := c.Context()
	after, replay, badID := parseLastEventID(lastEventID(c))
	sub, err := s.hub.Subscribe(topics)
	if err != nil {
		s.refused.WithLabelValues("unavailable").Inc()
		return errUnavailable(c, err)
	}
	streamID := uuid.NewString()
	userID := p.UserID.String()
	ok, err := s.conns.Acquire(ctx, userID, streamID)
	if err != nil || !ok {
		sub.Close()
		if err != nil {
			s.refused.WithLabelValues("unavailable").Inc()
			return errUnavailable(c, err)
		}
		s.refused.WithLabelValues(ReasonConnectionLimit).Inc()
		secs := int(LimitRetryAfter / time.Second)
		c.Set(fiber.HeaderRetryAfter, strconv.Itoa(secs))
		return httpx.NewError(http.StatusTooManyRequests, httpx.CodeResourceExhaust, "too many open realtime streams").
			WithDetails(map[string]any{"bucket": "sse_conns", "limit": s.conns.Max(), "retryAfterSeconds": secs})
	}
	st := &stream{
		s: s, kind: kind, userID: userID, streamID: streamID, sessionID: p.SessionID,
		tenantConfig: configTopic(p), sub: sub,
		conn: c.RequestCtx().Conn(), log: zerolog.Ctx(ctx).With().Str("component", "sse").Str("kind", kind).Logger(),
	}
	var control []string
	for _, t := range []string{rt.UserTopic(userID), st.tenantConfig} {
		if t != "" && slices.Contains(topics, t) {
			control = append(control, t)
		}
	}
	snap, err := s.reader.Snapshot(ctx, rt.Request{Topics: topics, After: after, Replay: replay, Control: control, Since: sinceSeq})
	if err != nil {
		sub.Close()
		s.release(userID, streamID)
		s.refused.WithLabelValues("unavailable").Inc()
		return errUnavailable(c, err)
	}
	if err := s.sessions.CheckSession(ctx, p); err != nil {
		sub.Close()
		s.release(userID, streamID)
		return err
	}
	if badID {
		snap.Resync = rt.ResyncUnknownID
	}
	st.snap = snap
	for _, m := range snap.Control {
		if end := st.ends(m); end != "" {
			st.pending, st.pendingEnd = m, end
			break
		}
	}
	st.life = time.Until(p.TokenExpiresAt) - ExpiryLead
	if p.TokenExpiresAt.IsZero() {
		st.life = DefaultLife
	}
	c.Set(fiber.HeaderContentType, fiber.MIMETextEventStream)
	c.Set(fiber.HeaderCacheControl, "no-cache, no-transform")
	c.Set("X-Accel-Buffering", "no")
	// The stream owns the connection to the end: no keep-alive reuse after it, so the write deadline left
	// on the connection (never cleared, so fasthttp's final write stays bounded too) never reaches
	// another response.
	c.RequestCtx().SetConnectionClose()
	c.Status(http.StatusOK)
	s.open.WithLabelValues(kind).Inc()
	return c.SendStreamWriter(st.run)
}

// configTopic is tenant:{tid}:config of the principal's effective tenant, "" without one.
func configTopic(p *authz.Principal) string {
	if tid := p.EffectiveTenant(); tid != nil && p.EffectiveRole() != "" {
		return rt.TenantTopic(tid.String(), rt.FamilyConfig)
	}
	return ""
}

// lastEventID is the Last-Event-ID header or, without one, ?lastEventId= (QueryLastEventID).
func lastEventID(c fiber.Ctx) string {
	if v := strings.TrimSpace(c.Get(HeaderLastEventID)); v != "" {
		return v
	}
	return c.Query(QueryLastEventID)
}

// parseLastEventID reads Last-Event-ID: absent means no replay; anything but a non-negative integer is
// an id this server never sent (resync).
func parseLastEventID(v string) (after int64, replay, bad bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, false, true
	}
	return n, true, false
}

func (s *Service) release(userID, streamID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.conns.Release(ctx, userID, streamID); err != nil {
		s.log.Warn().Err(err).Msg("sse: release the stream lease (it expires on its own)")
	}
}

// stream is one open response; run executes on the response's stream-writer goroutine.
type stream struct {
	s            *Service
	kind         string
	userID       string
	streamID     string
	sessionID    uuid.UUID
	tenantConfig string
	sub          *rt.Subscription
	snap         rt.Snapshot
	life         time.Duration
	conn         net.Conn
	log          zerolog.Logger
	// pending is the first event of the control window (Snapshot.Control) that ends a stream, and
	// pendingEnd why: the stream delivers it right after its opening frames and ends, as it would live.
	pending    rt.Message
	pendingEnd string

	lastID    int64
	lastNames []string
}

// envelope is the data of an event frame.
type envelope struct {
	Type    string          `json:"type"`
	Topic   string          `json:"topic"`
	EventID string          `json:"eventId"`
	Data    json.RawMessage `json:"data"`
}

func (st *stream) run(w *bufio.Writer) {
	start := time.Now()
	reason := endClientGone
	defer func() {
		st.sub.Close()
		st.s.release(st.userID, st.streamID)
		st.s.open.WithLabelValues(st.kind).Dec()
		st.s.ends.WithLabelValues(reason).Inc()
		if st.conn != nil {
			// One more bounded window for fasthttp's final write (the frames still in its pipe and the
			// terminating chunk): clearing the deadline would leave that write unbounded for a client
			// that stopped reading, holding the connection and the drain past SHUTDOWN_TIMEOUT.
			_ = st.conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
		}
		st.log.Debug().Str("reason", reason).Dur("duration", time.Since(start)).Msg("sse stream ended")
	}()
	if st.open(w) != nil {
		return
	}
	if st.pendingEnd != "" {
		replayed := slices.ContainsFunc(st.snap.Events, func(m rt.Message) bool {
			return m.ID == st.pending.ID && m.Topic == st.pending.Topic
		})
		if !replayed {
			if _, err := st.deliver(w, st.pending); err != nil {
				return
			}
		}
		reason = st.pendingEnd
		if reason == ReasonRolesChanged {
			_ = st.write(w, reconnectFrame(reason))
		}
		return
	}
	if st.life <= 0 {
		reason = ReasonTokenExpiring
		_ = st.write(w, reconnectFrame(reason))
		return
	}
	expire := time.NewTimer(st.life)
	defer expire.Stop()
	ping := time.NewTicker(st.s.cfg.PingInterval)
	defer ping.Stop()
	for {
		select {
		case m := <-st.sub.C():
			if !rt.Ephemeral(m.Topic) && m.ID <= st.snap.Seq {
				continue // in the replay snapshot, before the client's position, or judged by serve
			}
			end, err := st.deliver(w, m)
			if err != nil {
				return
			}
			if end != "" {
				reason = end
				if end == ReasonRolesChanged {
					_ = st.write(w, reconnectFrame(end))
				}
				return
			}
		case <-st.sub.Done():
			reason = st.sub.Reason()
			_ = st.write(w, reconnectFrame(reason))
			return
		case <-ping.C:
			if st.write(w, []byte(": ping\n\n")) != nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			held, err := st.s.conns.Renew(ctx, st.userID, st.streamID)
			cancel()
			if err != nil {
				st.log.Warn().Err(err).Msg("sse: renew the stream lease")
			} else if !held {
				reason = ReasonConnectionLimit
				_ = st.write(w, reconnectFrame(reason))
				return
			}
		case <-expire.C:
			reason = ReasonTokenExpiring
			_ = st.write(w, reconnectFrame(reason))
			return
		}
	}
}

// open writes the opening frames: retry, then resync, or the replay and the id-only frame with the
// sequence at connect. Both leave the client's Last-Event-ID at that sequence, and lastID records it,
// so a late ephemeral event (relayed live even at or below it) never moves the position back.
func (st *stream) open(w *bufio.Writer) error {
	if err := st.write(w, fmt.Appendf(nil, "retry: %d\n\n", Retry.Milliseconds())); err != nil {
		return err
	}
	if st.snap.Resync != "" {
		st.s.resyncs.WithLabelValues(st.snap.Resync).Inc()
		if err := st.write(w, resyncFrame(st.snap.Seq, st.snap.Resync)); err != nil {
			return err
		}
		st.lastID, st.lastNames = st.snap.Seq, nil
		return nil
	}
	for _, m := range st.snap.Events {
		if _, err := st.deliver(w, m); err != nil {
			return err
		}
	}
	if len(st.snap.Events) == 0 || st.snap.Seq > st.lastID {
		// Sets the client's Last-Event-ID to the sequence at connect (past the events of other topics)
		// without dispatching anything, so a reconnect replays from here even before the first event.
		if err := st.write(w, fmt.Appendf(nil, "id: %d\n\n", st.snap.Seq)); err != nil {
			return err
		}
		st.lastID, st.lastNames = st.snap.Seq, nil
	}
	return nil
}

// deliver writes one event (live or replayed) unless the topic never carries it to clients or the same
// event already went out under the same name; end is non-empty when a live delivery must end the stream.
func (st *stream) deliver(w *bufio.Writer, m rt.Message) (end string, err error) {
	name, ok := rt.EventName(m.Topic, m.Type)
	if !ok {
		return "", nil
	}
	if m.ID == st.lastID && slices.Contains(st.lastNames, name) {
		return "", nil
	}
	data := m.Data
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	body, err := json.Marshal(envelope{Type: name, Topic: m.Topic, EventID: m.EventID, Data: data})
	if err != nil {
		body, _ = json.Marshal(envelope{Type: name, Topic: m.Topic, EventID: m.EventID, Data: json.RawMessage("{}")})
	}
	frame := fmt.Appendf(nil, "id: %d\nevent: %s\ndata: %s\n\n", m.ID, name, body)
	if m.ID < st.lastID {
		// A late ephemeral event (vehicle_locations is relayed live even at or below the snapshot): without
		// an id line the client's Last-Event-ID stays where it is instead of moving back.
		frame = fmt.Appendf(nil, "event: %s\ndata: %s\n\n", name, body)
	}
	if err := st.write(w, frame); err != nil {
		return "", err
	}
	if m.ID > st.lastID {
		st.lastID, st.lastNames = m.ID, nil
	}
	if m.ID == st.lastID {
		st.lastNames = append(st.lastNames, name)
	}
	if m.ID <= st.snap.Seq {
		// Replayed: an event published before the credential was checked is reflected in it, and serve
		// already judged the ones published since (the control window, Snapshot.Control).
		return "", nil
	}
	return st.ends(m), nil
}

// ends reports whether event m ends the stream: a session.revoked on the stream's user topic that
// revokes it, or a roles.changed on its tenant's config topic (the implicit topics must be recomputed).
func (st *stream) ends(m rt.Message) string {
	name, ok := rt.EventName(m.Topic, m.Type)
	if !ok {
		return ""
	}
	data := m.Data
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	switch {
	case name == rt.EventSessionRevoked && m.Topic == rt.UserTopic(st.userID) && st.revokes(data):
		return endSessionRevoked
	case name == rt.EventRolesChanged && st.tenantConfig != "" && m.Topic == st.tenantConfig:
		return ReasonRolesChanged
	}
	return ""
}

// revokes reports whether a session.revoked payload ends this stream: its own session is among
// sessionIds, or the reason is claims_changed (the implicit topics must be recomputed). Another
// session's logout leaves the stream open; the client reads the event and stays signed in.
func (st *stream) revokes(data json.RawMessage) bool {
	var p struct {
		SessionIDs []uuid.UUID `json:"sessionIds"`
		Reason     string      `json:"reason"`
	}
	if json.Unmarshal(data, &p) != nil {
		return true // unreadable: end the stream; the next request decides
	}
	return p.Reason == "claims_changed" || slices.Contains(p.SessionIDs, st.sessionID)
}

// write sends one frame, bounded by WriteTimeout on the connection.
func (st *stream) write(w *bufio.Writer, frame []byte) error {
	if st.conn != nil {
		_ = st.conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
	}
	if _, err := w.Write(frame); err != nil {
		return err
	}
	return w.Flush()
}

// reconnectFrame asks the client to reopen the stream (with its Last-Event-ID).
func reconnectFrame(reason string) []byte {
	body, _ := json.Marshal(map[string]string{"reason": reason})
	return fmt.Appendf(nil, "event: %s\ndata: %s\n\n", EventReconnect, body)
}

// resyncFrame tells the client its position cannot be replayed and moves it to seq, the sequence at
// connect: everything after it arrives live.
func resyncFrame(seq int64, reason string) []byte {
	body, _ := json.Marshal(map[string]string{"reason": reason})
	return fmt.Appendf(nil, "id: %d\nevent: %s\ndata: %s\n\n", seq, EventResync, body)
}
