//go:build integration

package realtime_test

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

func TestMain(m *testing.M) {
	cache.RouteDriverLogs(zerolog.Nop())
	os.Exit(cachetest.Main(m))
}

// The writer's script dates the sequence: rtlog:marks keeps the first sequence of each Redis-clock
// minute, drops marks older than RTLOG_TTL + 1 h and expires with them (T12).
func TestWriterMarksTheFirstSequenceOfEachMinute(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	ctx := context.Background()
	w := realtime.NewWriter(rdb, ks, 1000, time.Hour)
	now, err := rdb.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	minute := now.Unix() / 60
	stale := float64(minute - 60 - 61) // older than RTLOG_TTL (60 min) + 60 min
	kept := float64(minute - 100)
	rdb.ZAdd(ctx, ks.RealtimeMarks(), redis.Z{Score: stale, Member: "1"}, redis.Z{Score: kept, Member: "2"})

	publish := func() int64 {
		n, err := w.Publish(ctx, realtime.Event{Type: "hubs.changed", EventID: "e", Topics: []string{"global"}, Data: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	first := publish()
	publish()
	marks, err := rdb.ZRangeWithScores(ctx, ks.RealtimeMarks(), 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	// A publish that crossed into the next minute adds its own mark; the first one must be this minute's.
	if len(marks) < 2 || marks[0].Score != kept || marks[1].Member != strconv.FormatInt(first, 10) || int64(marks[1].Score) < minute {
		t.Fatalf("marks %+v (first %d, minute %d)", marks, first, minute)
	}
	if ttl, _ := rdb.PTTL(ctx, ks.RealtimeMarks()).Result(); ttl <= time.Hour || ttl > 2*time.Hour+time.Minute {
		t.Fatalf("marks TTL %v, want RTLOG_TTL + 1 h", ttl)
	}
	// rtlog.trim leaves the marks and the sequence alone (SCAN TYPE stream).
	if n, err := w.Trim(ctx); err != nil || n != 1 {
		t.Fatalf("trim saw %d streams (%v), want rtlog:global only", n, err)
	}
	if n, _ := rdb.Exists(ctx, ks.RealtimeMarks(), ks.RealtimeSeq()).Result(); n != 2 {
		t.Fatal("trim removed the marks or the sequence")
	}
}

// A Reader on a log that was never trimmed and still holds the id replays it exactly; the same reader
// reports a log trimmed by MAXLEN past the id as trimmed, which XINFO max-deleted-entry-id does not.
func TestReaderDetectsMaxlenTrimming(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	ctx := context.Background()
	w := realtime.NewWriter(rdb, ks, 10, time.Hour)
	r := realtime.NewReader(rdb, ks, time.Hour)
	tasks := realtime.DispatchTopic("0199c000-0000-7000-8000-0000000000b1", realtime.FamilyTasks)
	var ids []int64
	for range 30 {
		n, err := w.Publish(ctx, realtime.Event{Type: "task.updated", EventID: "e", Topics: []string{tasks}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, n)
	}
	if _, err := w.Trim(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := rdb.XInfoStream(ctx, ks.RealtimeLog(tasks)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if info.MaxDeletedEntryID != "0-0" {
		t.Logf("max-deleted-entry-id is %s (Redis now records MAXLEN trimming)", info.MaxDeletedEntryID)
	}
	snap, err := r.Snapshot(ctx, realtime.Request{Topics: []string{tasks}, After: ids[5], Replay: true})
	if err != nil || snap.Resync != realtime.ResyncTrimmed {
		t.Fatalf("after a trimmed id: %+v %v", snap, err)
	}
	snap, err = r.Snapshot(ctx, realtime.Request{Topics: []string{tasks}, After: ids[24], Replay: true})
	if err != nil || snap.Resync != "" || len(snap.Events) != 5 || snap.Events[0].ID != ids[25] || snap.Seq != ids[29] {
		t.Fatalf("inside the kept tail: %+v %v", snap, err)
	}
	snap, err = r.Snapshot(ctx, realtime.Request{Topics: []string{tasks}})
	if err != nil || snap.Resync != "" || len(snap.Events) != 0 || snap.Seq != ids[29] {
		t.Fatalf("no replay: %+v %v", snap, err)
	}
}

// The control window: Snapshot returns the events of the control topics after Since and up to Seq,
// with or without a replay and whatever the replay's verdict, and nothing of the other topics or from
// before Since. Seq reads the same sequence.
func TestSnapshotReadsTheControlWindow(t *testing.T) {
	rdb, ks := cachetest.NewClient(t)
	ctx := context.Background()
	w := realtime.NewWriter(rdb, ks, 1000, time.Hour)
	r := realtime.NewReader(rdb, ks, time.Hour)
	const id = "0199c000-0000-7000-8000-0000000000c1"
	user, config := realtime.UserTopic(id), realtime.TenantTopic(id, realtime.FamilyConfig)
	publish := func(typ string, topics ...string) int64 {
		n, err := w.Publish(ctx, realtime.Event{Type: typ, EventID: "e", Topics: topics, Data: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	publish("roles.changed", config) // before Since
	since, err := r.Seq(ctx)
	if err != nil || since == 0 {
		t.Fatalf("Seq %d %v", since, err)
	}
	revoked := publish("user.sessions_revoked", user)
	publish("hubs.changed", "global") // not a control topic
	roles := publish("roles.changed", config)
	topics := []string{"global", config, user}
	for _, replay := range []bool{false, true} {
		snap, err := r.Snapshot(ctx, realtime.Request{Topics: topics, After: since + 1000, Replay: replay,
			Control: []string{user, config}, Since: since})
		if err != nil {
			t.Fatal(err)
		}
		if snap.Seq != roles || len(snap.Control) != 2 || snap.Control[0].ID != revoked || snap.Control[0].Topic != user ||
			snap.Control[1].ID != roles || snap.Control[1].Type != "roles.changed" {
			t.Fatalf("replay %v: %+v", replay, snap)
		}
		if replay && snap.Resync != realtime.ResyncUnknownID {
			t.Fatalf("the window is read whatever the replay's verdict: %+v", snap)
		}
	}
	// A control topic the stream does not follow is not read.
	snap, err := r.Snapshot(ctx, realtime.Request{Topics: []string{"global"}, Control: []string{user}, Since: since})
	if err != nil || len(snap.Control) != 0 {
		t.Fatalf("%+v %v", snap, err)
	}
}
