// Package realtime is the Redis side of SSE (main spec §8, Appendix B §B.4): the topic catalogue,
// the keys of the rt: and rtlog: namespaces, and the writer the outbox relay uses to fan an event out.
// The SSE endpoint, its PSUBSCRIBE loop and Last-Event-ID replay read what this package writes
// (issue T12).
//
// Wire format (Appendix B §B.4.4):
//
//	INCR  lt:{env}:rtlog:seq                       one global sequence n per event (R52)
//	XADD  lt:{env}:rtlog:{topic} MAXLEN ~ RTLOG_MAXLEN {n}-0 type <event_type> event_id <uuid> data <payload>
//	                                               non-ephemeral topics only; PEXPIRE RTLOG_TTL after each add
//	PUBLISH lt:{env}:rt:{topic} {"id":n,"topic":"<topic>","type":"<event_type>","eventId":"<uuid>","data":<payload>}
//
// All three run in one Lua script per event, so the sequence and the stream ids can never interleave
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
)

// Defaults of RTLOG_MAXLEN and RTLOG_TTL (main spec §16.1, Appendix B §B.6.2).
const (
	DefaultMaxLen = 1000
	DefaultTTL    = 24 * time.Hour
)

const uuidRx = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`

// topicRx is the closed topic catalogue of Appendix B §B.4.2 (R51): no other topic exists.
var topicRx = regexp.MustCompile(`^(?:(?:user|driver|chat):` + uuidRx +
	`|tenant:` + uuidRx + `:(?:tasks|trips|chats|fleet|hr|expenses|billing|vehicle_locations|config)` +
	`|global|platform:security|dispatch:(?:tasks|trips))$`)

// ValidTopic reports whether t is a topic of the catalogue (uuids in lower case).
func ValidTopic(t string) bool { return topicRx.MatchString(t) }

// Ephemeral reports whether t is published without a replay log (tenant:{tid}:vehicle_locations).
func Ephemeral(t string) bool { return strings.HasSuffix(t, ":vehicle_locations") }

// Keys builds the realtime keys under one prefix lt:{APP_ENV}: (R26).
type Keys struct{ prefix string }

// NewKeys returns the keys of a REDIS_KEY_PREFIX value ("lt:{APP_ENV}:").
func NewKeys(prefix string) Keys { return Keys{prefix: prefix} }

// Seq is the global event sequence rtlog:seq (never reset).
func (k Keys) Seq() string { return k.prefix + "rtlog:seq" }

// Stream is the replay log rtlog:{topic}.
func (k Keys) Stream(topic string) string { return k.prefix + "rtlog:" + topic }

// Channel is the pub/sub channel rt:{topic}.
func (k Keys) Channel(topic string) string { return k.prefix + "rt:" + topic }

// StreamPattern matches every replay log (and the sequence key, which SCAN ... TYPE stream skips).
func (k Keys) StreamPattern() string { return k.prefix + "rtlog:*" }

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
	keys   Keys
	maxLen int64
	ttl    time.Duration
}

// NewWriter builds a writer; maxLen and ttl are RTLOG_MAXLEN and RTLOG_TTL (0 = the defaults).
func NewWriter(rdb redis.UniversalClient, keys Keys, maxLen int64, ttl time.Duration) *Writer {
	if maxLen <= 0 {
		maxLen = DefaultMaxLen
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Writer{rdb: rdb, keys: keys, maxLen: maxLen, ttl: ttl}
}

// publishScript: KEYS = [seq, stream...]; ARGV = [maxlen, ttl ms, type, data, event id,
// (channel, message-tail)...]. Returns the sequence number used.
var publishScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
for i = 2, #KEYS do
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
for i = 2, #KEYS do
  redis.call('XADD', KEYS[i], 'MAXLEN', '~', ARGV[1], id .. '-0', 'type', ARGV[3], 'event_id', ARGV[5], 'data', ARGV[4])
  redis.call('PEXPIRE', KEYS[i], ARGV[2])
end
for i = 6, #ARGV, 2 do
  redis.call('PUBLISH', ARGV[i], '{"id":' .. id .. ',' .. ARGV[i + 1])
end
return n
`)

// ErrInvalidTopic is returned for a topic outside the catalogue.
var ErrInvalidTopic = errors.New("realtime: topic is not in the catalogue of Appendix B §B.4.2")

// Publish takes the next sequence number n, appends the event as {n}-0 to the replay log of each
// non-ephemeral topic and publishes it on each topic's channel, in one script. It returns n.
func (w *Writer) Publish(ctx context.Context, e Event) (int64, error) {
	if len(e.Topics) == 0 {
		return 0, errors.New("realtime: event without topics")
	}
	data := e.Data
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	keys := []string{w.keys.Seq()}
	args := []any{w.maxLen, w.ttl.Milliseconds(), e.Type, string(data), e.EventID}
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
			keys = append(keys, w.keys.Stream(t))
		}
		tail, err := messageTail(t, e.Type, e.EventID, data)
		if err != nil {
			return 0, err
		}
		args = append(args, w.keys.Channel(t), tail)
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
// expiry. It returns the number of streams seen.
func (w *Writer) Trim(ctx context.Context) (int, error) {
	var cursor uint64
	n := 0
	for {
		keys, next, err := w.rdb.ScanType(ctx, cursor, w.keys.StreamPattern(), 200, "stream").Result()
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
