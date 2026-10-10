package realtime

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
)

// MaxReplayPerTopic caps the events one reconnect replays per topic (main spec §8.2); a connection
// further behind gets resync instead of a partial replay.
const MaxReplayPerTopic = 500

// Resync reasons: why a Last-Event-ID cannot be replayed (main spec §8.2). The client then refetches
// its realtime-backed queries.
const (
	// ResyncUnknownID: the id is not a sequence number this Redis issued (above rtlog:seq, or not a
	// number at all), for example after Redis lost data.
	ResyncUnknownID = "unknown_id"
	// ResyncTrimmed: RTLOG_MAXLEN removed events after the id from one of the connection's logs.
	ResyncTrimmed = "trimmed"
	// ResyncExpired: the id is older than RTLOG_TTL and one of the connection's logs expired since.
	ResyncExpired = "expired"
	// ResyncTooFarBehind: more than MaxReplayPerTopic events after the id on one topic.
	ResyncTooFarBehind = "too_far_behind"
)

// Reader reads the replay side of the logs the Writer appends (Appendix B §B.4.1).
type Reader struct {
	rdb redis.UniversalClient
	ks  cache.Keyspace
	ttl time.Duration
}

// NewReader builds a reader; ttl is RTLOG_TTL (0 = DefaultTTL), the same value the writer uses.
func NewReader(rdb redis.UniversalClient, ks cache.Keyspace, ttl time.Duration) *Reader {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Reader{rdb: rdb, ks: ks, ttl: ttl}
}

// Seq reads rtlog:seq, the id of the last event published (0 before the first).
func (r *Reader) Seq(ctx context.Context) (int64, error) {
	n, err := r.rdb.Get(ctx, r.ks.RealtimeSeq()).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("realtime: read rtlog:seq: %w", err)
	}
	return n, nil
}

// Request is what a stream asks Snapshot for.
type Request struct {
	// Topics are the connection's topics.
	Topics []string
	// After and Replay come from Last-Event-ID: with Replay, the events after After on the
	// non-ephemeral topics are replayed (Snapshot.Events).
	After  int64
	Replay bool
	// Control topics (a subset of Topics; ephemeral ones are ignored) are read over (Since, Seq] whether
	// or not the connection replays (Snapshot.Control): internal/sse reads Since = rtlog:seq before it
	// checks the stream's credential, so these are the events published while the stream was being
	// authorised and subscribed, which its credential cannot reflect (session.revoked, roles.changed).
	Control []string
	Since   int64
}

// Snapshot is the state a stream starts from.
type Snapshot struct {
	// Seq is rtlog:seq when the snapshot was taken: every event of the connection's non-ephemeral
	// topics with an id at or below it is in Events, was before the requested id, or made Resync
	// non-empty; every later one is live. Live events at or below Seq are therefore dropped.
	Seq int64
	// Events are the events after the requested id, ascending by id; an event on two topics of the
	// connection appears once per topic, in the order the topics were given.
	Events []Message
	// Resync is non-empty when the requested id cannot be replayed (Resync* reasons); Events is then
	// empty.
	Resync string
	// Control are the events of Request.Control with an id in (Since, Seq], ascending by id (at most
	// MaxReplayPerTopic per topic: the window lasts milliseconds), whatever Resync says.
	Control []Message
}

// Snapshot reads the global sequence, the control window and, when replay is set, everything after
// id `after` on the non-ephemeral topics, in one MULTI so the sequence and the logs agree (the
// writer's script is atomic, so no event can fall between them). The id is unusable (Resync) when it
// is above the sequence; when a log was trimmed past it (RTLOG_MAXLEN: XINFO entries-added above the
// length and the first entry after the id; or max-deleted-entry-id after it); when a log that could
// have held later events expired and the id is older than RTLOG_TTL (rtlog:marks dates it); or when a
// topic has more than MaxReplayPerTopic events after it.
func (r *Reader) Snapshot(ctx context.Context, req Request) (Snapshot, error) {
	after, replay := req.After, req.Replay
	var logs, control []string
	for _, t := range req.Topics {
		if Ephemeral(t) {
			continue
		}
		if replay {
			logs = append(logs, t)
		}
		if slices.Contains(req.Control, t) && !slices.Contains(control, t) {
			control = append(control, t)
		}
	}
	infos := make([]*redis.XInfoStreamCmd, len(logs))
	ranges := make([]*redis.XMessageSliceCmd, len(logs))
	windows := make([]*redis.XMessageSliceCmd, len(control))
	var seqCmd *redis.StringCmd
	var timeCmd *redis.TimeCmd
	start := "(" + strconv.FormatInt(after, 10) + "-0"
	since := "(" + strconv.FormatInt(max(req.Since, 0), 10) + "-0"
	// The aggregate error is the first failed command's; each command is checked below instead, since a
	// missing log answers XINFO with an error that is no failure.
	_, _ = r.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		seqCmd = p.Get(ctx, r.ks.RealtimeSeq())
		timeCmd = p.Time(ctx)
		for i, t := range logs {
			key := r.ks.RealtimeLog(t)
			infos[i] = p.XInfoStream(ctx, key)
			ranges[i] = p.XRangeN(ctx, key, start, "+", MaxReplayPerTopic+1)
		}
		for i, t := range control {
			windows[i] = p.XRangeN(ctx, r.ks.RealtimeLog(t), since, "+", MaxReplayPerTopic)
		}
		return nil
	})
	seq, err := seqCmd.Int64()
	if errors.Is(err, redis.Nil) {
		seq, err = 0, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("realtime: read rtlog:seq: %w", err)
	}
	if err := timeCmd.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("realtime: read the Redis clock: %w", err)
	}
	snap := Snapshot{Seq: seq}
	for i, t := range control {
		msgs, err := windows[i].Result()
		if err != nil {
			return Snapshot{}, fmt.Errorf("realtime: XRANGE %s: %w", t, err)
		}
		for _, x := range msgs {
			if m, ok := fromStream(t, x); ok && m.ID <= seq {
				snap.Control = append(snap.Control, m)
			}
		}
	}
	slices.SortStableFunc(snap.Control, func(a, b Message) int { return cmp.Compare(a.ID, b.ID) })
	if !replay {
		return snap, nil
	}
	if after > seq {
		snap.Resync = ResyncUnknownID
		return snap, nil
	}
	type entry struct {
		order int
		m     Message
	}
	var (
		all               []entry
		trimmed, tooFar   bool
		expiryGapPossible bool
	)
	for i, t := range logs {
		info, ierr := infos[i].Result()
		exists := ierr == nil
		if ierr != nil && !isNoSuchKey(ierr) {
			return Snapshot{}, fmt.Errorf("realtime: XINFO %s: %w", t, ierr)
		}
		msgs, err := ranges[i].Result()
		if err != nil {
			return Snapshot{}, fmt.Errorf("realtime: XRANGE %s: %w", t, err)
		}
		var maxDeleted, first, last, length, added int64
		if exists {
			maxDeleted, first, last = streamSeq(info.MaxDeletedEntryID), streamSeq(info.FirstEntry.ID), streamSeq(info.LastGeneratedID)
			length, added = info.Length, info.EntriesAdded
		}
		switch {
		case !exists:
			// The log expired (or never existed): events after the id may have gone with it.
			expiryGapPossible = true
		case maxDeleted > after:
			trimmed = true // XDEL removed an entry after the id
		case added > length && (length == 0 && last > after || length > 0 && first > after+1):
			// MAXLEN trims from the head (XINFO max-deleted-entry-id does not record it): this log lost
			// entries and its oldest remaining one is after the id, so lost ones may be after it too.
			trimmed = true
		case length > 0 && first > after+1:
			// Nothing was ever removed from this log and it starts after the id: it may be a new log that
			// replaced an expired one.
			expiryGapPossible = true
		}
		if len(msgs) > MaxReplayPerTopic {
			tooFar = true
		}
		for _, x := range msgs {
			m, ok := fromStream(t, x)
			if ok {
				all = append(all, entry{order: i, m: m})
			}
		}
	}
	switch {
	case trimmed:
		snap.Resync = ResyncTrimmed
	case expiryGapPossible && after < seq:
		old, err := r.olderThanWindow(ctx, after, timeCmd.Val())
		if err != nil {
			return Snapshot{}, err
		}
		if old {
			snap.Resync = ResyncExpired
		}
	}
	if snap.Resync == "" && tooFar {
		snap.Resync = ResyncTooFarBehind
	}
	if snap.Resync != "" {
		return snap, nil
	}
	slices.SortStableFunc(all, func(a, b entry) int {
		return cmp.Or(cmp.Compare(a.m.ID, b.m.ID), cmp.Compare(a.order, b.order))
	})
	snap.Events = make([]Message, len(all))
	for i, e := range all {
		snap.Events[i] = e.m
	}
	return snap, nil
}

// olderThanWindow reports whether an event after id `after` may be older than RTLOG_TTL: the first
// sequence number of the earliest marked minute inside the window (now - RTLOG_TTL, now] is above
// after + 1, or no minute inside the window has an event at all. The window starts at the first whole
// minute after now - RTLOG_TTL, so the answer errs towards yes by at most a minute.
func (r *Reader) olderThanWindow(ctx context.Context, after int64, now time.Time) (bool, error) {
	cutoff := (now.Unix()-int64(r.ttl/time.Second))/60 + 1
	marks, err := r.rdb.ZRangeArgsWithScores(ctx, redis.ZRangeArgs{
		Key: r.ks.RealtimeMarks(), Start: strconv.FormatInt(cutoff, 10), Stop: "+inf", ByScore: true, Count: 1,
	}).Result()
	if err != nil {
		return false, fmt.Errorf("realtime: read rtlog:marks: %w", err)
	}
	if len(marks) == 0 {
		return true, nil
	}
	member, _ := marks[0].Member.(string)
	first, err := strconv.ParseInt(member, 10, 64)
	if err != nil {
		return true, nil // a damaged mark: assume the worst
	}
	return after+1 < first, nil
}

// fromStream decodes one log entry (fields type, event_id, data; id {n}-0).
func fromStream(topic string, x redis.XMessage) (Message, bool) {
	id := streamSeq(x.ID)
	if id <= 0 {
		return Message{}, false
	}
	str := func(k string) string {
		v, _ := x.Values[k].(string)
		return v
	}
	data := json.RawMessage(str("data"))
	if !json.Valid(data) {
		data = json.RawMessage("{}")
	}
	return Message{ID: id, Topic: topic, Type: str("type"), EventID: str("event_id"), Data: data}, true
}

// streamSeq is the sequence part of a stream id "{n}-0"; 0 for "0-0", "" and anything unparsable.
func streamSeq(id string) int64 {
	n, _, _ := strings.Cut(id, "-")
	v, err := strconv.ParseInt(n, 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// isNoSuchKey reports the error XINFO STREAM answers for a key that does not exist.
func isNoSuchKey(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such key")
}
