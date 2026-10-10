package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
)

// Why the hub ended a subscription; the SSE layer sends them as the reason of event: reconnect.
const (
	// EndShutdown: the replica drains (main spec §8.2: readiness 503, then reconnect, then close).
	EndShutdown = "shutdown"
	// EndLagging: the stream did not keep up and its buffer filled; the client reconnects with
	// Last-Event-ID and the replay covers what it missed.
	EndLagging = "lagging"
	// EndInterrupted: the PSUBSCRIBE connection was re-established, so messages published meanwhile
	// may be lost to this replica; the replay covers them.
	EndInterrupted = "interrupted"
)

// Hub errors of Subscribe.
var (
	// ErrNotReady: the PSUBSCRIBE is not confirmed yet (Redis down or still connecting).
	ErrNotReady = errors.New("realtime: the fan-out subscription is not ready")
	// ErrDraining: the replica is shutting down.
	ErrDraining = errors.New("realtime: draining")
)

// DefaultBuffer is the number of messages a subscription holds before the hub ends it as lagging.
const DefaultBuffer = 1024

// Hub is one api replica's side of the fan-out (Appendix B §B.4.4): a single PSUBSCRIBE {prefix}rt:*
// loop (Run) that dispatches each message to the local subscriptions of its topic. Channels outside
// the catalogue (rt:cache) are ignored. Dispatch never blocks: a subscription whose buffer is full is
// ended as lagging. Every (re)confirmation of the PSUBSCRIBE ends the subscriptions that existed
// before it as interrupted, because Redis does not keep messages for a disconnected subscriber.
type Hub struct {
	rdb    redis.UniversalClient
	ks     cache.Keyspace
	prefix string // {prefix}rt:
	log    zerolog.Logger
	buffer int

	mu       sync.Mutex
	byTopic  map[string]map[*Subscription]struct{}
	all      map[*Subscription]struct{}
	ready    bool
	draining bool

	ended    *prometheus.CounterVec
	received prometheus.Counter
	streams  prometheus.Gauge
}

// NewHub builds a hub on the process's Redis client and keyspace; buffer <= 0 means DefaultBuffer.
func NewHub(rdb redis.UniversalClient, ks cache.Keyspace, log zerolog.Logger, buffer int) *Hub {
	if buffer <= 0 {
		buffer = DefaultBuffer
	}
	return &Hub{
		rdb: rdb, ks: ks, prefix: ks.Key(cache.NSRealtime) + ":", log: log, buffer: buffer,
		byTopic: map[string]map[*Subscription]struct{}{}, all: map[*Subscription]struct{}{},
		ended: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "realtime_subscriptions_ended_total",
			Help: "Local SSE subscriptions the hub ended, by reason (shutdown, lagging, interrupted).",
		}, []string{"reason"}),
		received: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "realtime_messages_received_total",
			Help: "Messages of catalogue topics received on the PSUBSCRIBE rt:* connection.",
		}),
		streams: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "realtime_subscriptions",
			Help: "Local SSE subscriptions of this replica.",
		}),
	}
}

// Register adds the hub's metrics to reg.
func (h *Hub) Register(reg prometheus.Registerer) error {
	for _, c := range []prometheus.Collector{h.ended, h.received, h.streams} {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}

// Subscription is one stream's view of the hub.
type Subscription struct {
	hub    *Hub
	topics []string
	c      chan Message
	done   chan struct{}
	reason string
	once   sync.Once
}

// C delivers the messages of the subscription's topics in publish order.
func (s *Subscription) C() <-chan Message { return s.c }

// Done is closed when the hub ended the subscription; Reason then says why.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Reason is why the hub ended the subscription (End* constants); "" while it runs or after Close.
func (s *Subscription) Reason() string {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return s.reason
}

// Close removes the subscription; it is safe to call more than once and after the hub ended it.
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	s.hub.removeLocked(s, "")
}

// Subscribe registers a subscription for topics. It fails with ErrNotReady until the PSUBSCRIBE is
// confirmed and with ErrDraining once Drain ran.
func (h *Hub) Subscribe(topics []string) (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.draining {
		return nil, ErrDraining
	}
	if !h.ready {
		return nil, ErrNotReady
	}
	s := &Subscription{hub: h, topics: topics, c: make(chan Message, h.buffer), done: make(chan struct{})}
	h.all[s] = struct{}{}
	for _, t := range topics {
		set := h.byTopic[t]
		if set == nil {
			set = map[*Subscription]struct{}{}
			h.byTopic[t] = set
		}
		set[s] = struct{}{}
	}
	h.streams.Set(float64(len(h.all)))
	return s, nil
}

// Ready reports whether the PSUBSCRIBE is confirmed and the hub is not draining.
func (h *Hub) Ready() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ready && !h.draining
}

// Drain refuses new subscriptions and ends every subscription with EndShutdown.
func (h *Hub) Drain() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.draining = true
	h.endAllLocked(EndShutdown)
}

// Run holds the PSUBSCRIBE {prefix}rt:* connection until ctx ends. go-redis reconnects and
// resubscribes on its own (with a health-check ping); each confirmation ends the subscriptions made
// before it.
func (h *Hub) Run(ctx context.Context) error {
	pattern := h.ks.Pattern(cache.NSRealtime)
	ps := h.rdb.PSubscribe(ctx, pattern)
	defer func() {
		h.mu.Lock()
		h.ready = false
		h.mu.Unlock()
		_ = ps.Close()
	}()
	ch := ps.ChannelWithSubscriptions(redis.WithChannelSize(4 * DefaultBuffer))
	for {
		select {
		case <-ctx.Done():
			return nil
		case m, ok := <-ch:
			if !ok {
				return nil
			}
			switch m := m.(type) {
			case *redis.Subscription:
				if m.Kind == "psubscribe" {
					h.confirmed()
				}
			case *redis.Message:
				h.dispatch(m.Channel, m.Payload)
			}
		}
	}
}

// confirmed marks the subscription live; subscriptions made before it may have missed messages while
// the connection was down, so they end as interrupted (their clients replay).
func (h *Hub) confirmed() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ready && len(h.all) > 0 {
		h.log.Warn().Int("streams", len(h.all)).Msg("realtime: fan-out subscription re-established; local streams reconnect")
	}
	h.endAllLocked(EndInterrupted)
	h.ready = true
}

// dispatch hands one channel message to the subscriptions of its topic without blocking.
func (h *Hub) dispatch(channel, payload string) {
	topic, ok := strings.CutPrefix(channel, h.prefix)
	if !ok || !ValidTopic(topic) {
		return // rt:cache and anything outside the catalogue
	}
	var m Message
	if err := json.Unmarshal([]byte(payload), &m); err != nil || m.Topic != topic || m.ID <= 0 {
		h.log.Warn().Str("topic", topic).Msg("realtime: undecodable message dropped")
		return
	}
	h.received.Inc()
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.byTopic[topic] {
		select {
		case s.c <- m:
		default:
			h.removeLocked(s, EndLagging)
		}
	}
}

func (h *Hub) endAllLocked(reason string) {
	for s := range h.all {
		h.removeLocked(s, reason)
	}
}

// removeLocked unregisters s; a non-empty reason ends it (Done closes) unless it already ended.
func (h *Hub) removeLocked(s *Subscription, reason string) {
	if _, ok := h.all[s]; !ok {
		return
	}
	delete(h.all, s)
	for _, t := range s.topics {
		if set := h.byTopic[t]; set != nil {
			delete(set, s)
			if len(set) == 0 {
				delete(h.byTopic, t)
			}
		}
	}
	h.streams.Set(float64(len(h.all)))
	if reason != "" {
		s.reason = reason
		h.ended.WithLabelValues(reason).Inc()
		s.once.Do(func() { close(s.done) })
	}
}
