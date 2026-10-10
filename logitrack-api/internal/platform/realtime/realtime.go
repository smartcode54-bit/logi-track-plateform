// Package realtime is the Redis side of SSE (main spec §8, Appendix B §B.4): the topic catalogue, the
// writer the outbox relay uses to fan an event out (T10), and the read side of one api replica (T12):
// the Hub (one PSUBSCRIBE rt:* loop dispatching to local streams by topic), the replay Reader
// (Last-Event-ID over the rtlog: streams) and the SSE event names (EventName). Its keys and channels in
// the rt: and rtlog: namespaces are built by cache.Keyspace (RealtimeSeq, RealtimeLog, RealtimeMarks,
// Channel), like every Redis key of the module (Appendix B §B.6.1). The HTTP endpoints are
// internal/sse.
//
// Wire format (Appendix B §B.4.4):
//
//	INCR  lt:{env}:rtlog:seq                       one global sequence n per event (R52)
//	XADD  lt:{env}:rtlog:{topic} MAXLEN ~ RTLOG_MAXLEN {n}-0 type <event_type> event_id <uuid> data <payload>
//	                                               non-ephemeral topics only; PEXPIRE RTLOG_TTL after each add
//	ZADD  lt:{env}:rtlog:marks <minute> n          only when the Redis-clock minute has no mark yet; marks
//	                                               older than RTLOG_TTL + 1 h are removed (Reader, expiry)
//	PUBLISH lt:{env}:rt:{topic} {"id":n,"topic":"<topic>","type":"<event_type>","eventId":"<uuid>","data":<payload>}
//
// All of it runs in one Lua script per event, so the sequence and the stream ids can never interleave
// even if two relays ran at once, and a stream whose top id is ahead of the counter (a restore from
// an older snapshot) moves the counter past it instead of failing every later XADD.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
)

// Defaults of RTLOG_MAXLEN and RTLOG_TTL (main spec §16.1, Appendix B §B.6.2).
const (
	DefaultMaxLen = 1000
	DefaultTTL    = 24 * time.Hour
)

const uuidRx = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`

// topicRx is the closed topic catalogue of Appendix B §B.4.2 (R51): no other topic exists. The
// dispatcher topics are per billing party (dispatch:{partyId}:{tasks|trips}), so a dispatcher's stream
// reaches no further than its grant (Appendix C §C.1.7).
var topicRx = regexp.MustCompile(`^(?:(?:user|driver|chat):` + uuidRx +
	`|tenant:` + uuidRx + `:(?:tasks|trips|chats|fleet|hr|expenses|billing|vehicle_locations|config)` +
	`|dispatch:` + uuidRx + `:(?:tasks|trips)` +
	`|global|platform:security)$`)

// ValidTopic reports whether t is a topic of the catalogue (uuids in lower case).
func ValidTopic(t string) bool { return topicRx.MatchString(t) }

// Ephemeral reports whether t is published without a replay log (tenant:{tid}:vehicle_locations).
func Ephemeral(t string) bool { return strings.HasSuffix(t, ":vehicle_locations") }

// Event is one outbox row with realtime topics.
type Event struct {
	Type    string          // outbox_events.event_type
	EventID string          // outbox_events.event_id
	Topics  []string        // outbox_events.realtime_topics
	Data    json.RawMessage // outbox_events.payload
}

// Message is the PUBLISH body on rt:{topic}; ID is the SSE id line.
type Message struct {
	ID      int64           `json:"id"`
	Topic   string          `json:"topic"`
	Type    string          `json:"type"`
	EventID string          `json:"eventId"`
	Data    json.RawMessage `json:"data"`
}

// Writer appends events to the replay logs and publishes them. Only the outbox relay writes.
type Writer struct {
	rdb    redis.UniversalClient
	ks     cache.Keyspace
	maxLen int64
	ttl    time.Duration
}

// NewWriter builds a writer on the keyspace cache.Open returns (rtlog:seq, rtlog:{topic},
// rt:{topic}); maxLen and ttl are RTLOG_MAXLEN and RTLOG_TTL (0 = the defaults).
func NewWriter(rdb redis.UniversalClient, ks cache.Keyspace, maxLen int64, ttl time.Duration) *Writer {
	if maxLen <= 0 {
		maxLen = DefaultMaxLen
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Writer{rdb: rdb, ks: ks, maxLen: maxLen, ttl: ttl}
}

// publishScript: KEYS = [seq, marks, stream...]; ARGV = [maxlen, ttl ms, type, data, event id, mark
// minutes, (channel, message-tail)...]. Returns the sequence number used. The mark of the Redis-clock
// minute records the first sequence number of that minute; marks older than the mark minutes go, and
// the set itself expires when no event came for that long (Reader.Snapshot reads it).
var publishScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
for i = 3, #KEYS do
  local last = redis.call('XREVRANGE', KEYS[i], '+', '-', 'COUNT', 1)
  if #last > 0 then
    local top = tonumber(string.match(last[1][1], '^(%d+)'))
    if top and top >= n then
      n = top + 1
      redis.call('SET', KEYS[1], string.format('%d', n))
    end
  end
end
local id = string.format('%d', n)
for i = 3, #KEYS do
  redis.call('XADD', KEYS[i], 'MAXLEN', '~', ARGV[1], id .. '-0', 'type', ARGV[3], 'event_id', ARGV[5], 'data', ARGV[4])
  redis.call('PEXPIRE', KEYS[i], ARGV[2])
end
local keep = tonumber(ARGV[6])
local minute = math.floor(tonumber(redis.call('TIME')[1]) / 60)
if redis.call('ZCOUNT', KEYS[2], minute, minute) == 0 then
  redis.call('ZADD', KEYS[2], string.format('%d', minute), id)
end
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', '(' .. string.format('%d', minute - keep))
redis.call('PEXPIRE', KEYS[2], string.format('%d', keep * 60000))
for i = 7, #ARGV, 2 do
  redis.call('PUBLISH', ARGV[i], '{"id":' .. id .. ',' .. ARGV[i + 1])
end
return n
`)

// markMinutes is how long the marks of a writer with RTLOG_TTL ttl are kept: the TTL in whole minutes
// plus an hour, so a reader whose clock or TTL differs a little still finds the window's first mark.
func markMinutes(ttl time.Duration) int64 {
	return int64((ttl+time.Minute-1)/time.Minute) + 60
}

// ErrInvalidTopic is returned for a topic outside the catalogue.
var ErrInvalidTopic = errors.New("realtime: topic is not in the catalogue of Appendix B §B.4.2")

// Publish takes the next sequence number n, appends the event as {n}-0 to the replay log of each
// non-ephemeral topic, marks the minute and publishes it on each topic's channel, in one script. It
// returns n.
func (w *Writer) Publish(ctx context.Context, e Event) (int64, error) {
	if len(e.Topics) == 0 {
		return 0, errors.New("realtime: event without topics")
	}
	data := e.Data
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	keys := []string{w.ks.RealtimeSeq(), w.ks.RealtimeMarks()}
	args := []any{w.maxLen, w.ttl.Milliseconds(), e.Type, string(data), e.EventID, markMinutes(w.ttl)}
	seen := map[string]bool{}
	for _, t := range e.Topics {
		if !ValidTopic(t) {
			return 0, fmt.Errorf("%w: %q", ErrInvalidTopic, t)
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		if !Ephemeral(t) {
			keys = append(keys, w.ks.RealtimeLog(t))
		}
		tail, err := messageTail(t, e.Type, e.EventID, data)
		if err != nil {
			return 0, err
		}
		args = append(args, w.ks.Channel(t), tail)
	}
	n, err := publishScript.Run(ctx, w.rdb, keys, args...).Int64()
	if err != nil {
		return 0, fmt.Errorf("realtime: publish: %w", err)
	}
	return n, nil
}

// messageTail renders Message without its leading {"id":n, (the script prepends it).
func messageTail(topic, typ, eventID string, data json.RawMessage) (string, error) {
	b, err := json.Marshal(struct {
		Topic   string          `json:"topic"`
		Type    string          `json:"type"`
		EventID string          `json:"eventId"`
		Data    json.RawMessage `json:"data"`
	}{topic, typ, eventID, data})
	if err != nil {
		return "", fmt.Errorf("realtime: encode message: %w", err)
	}
	return string(b[1:]), nil
}

// Trim is the rtlog.trim housekeeping job (Appendix B §B.5.6): every replay log is cut to exactly
// RTLOG_MAXLEN entries (XADD trims only approximately, by whole nodes) and gets RTLOG_TTL if it has no
// expiry. It returns the number of streams seen. The scan pattern covers the rtlog: namespace, the
// sequence and the marks included, which SCAN ... TYPE stream skips.
func (w *Writer) Trim(ctx context.Context) (int, error) {
	pattern := w.ks.Pattern(cache.NSRealtimeLog)
	var cursor uint64
	n := 0
	for {
		keys, next, err := w.rdb.ScanType(ctx, cursor, pattern, 200, "stream").Result()
		if err != nil {
			return n, fmt.Errorf("realtime: scan replay logs: %w", err)
		}
		for _, k := range keys {
			n++
			if err := w.rdb.XTrimMaxLen(ctx, k, w.maxLen).Err(); err != nil {
				return n, fmt.Errorf("realtime: trim: %w", err)
			}
			if ttl, err := w.rdb.PTTL(ctx, k).Result(); err == nil && ttl < 0 {
				if err := w.rdb.PExpire(ctx, k, w.ttl).Err(); err != nil {
					return n, fmt.Errorf("realtime: expire: %w", err)
				}
			}
		}
		if cursor = next; cursor == 0 {
			return n, nil
		}
	}
}
