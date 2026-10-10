//go:build integration

package sse_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/sse"
)

type nopPublisher struct{}

func (nopPublisher) Publish(context.Context, string, string, amqp.Publishing) error { return nil }

// relay drains the outbox the way the scheduler leader does (RabbitMQ stubbed, the realtime step real).
func (w *world) relay() {
	w.t.Helper()
	r := outbox.NewRelay(w.pool, func() (outbox.Publisher, error) { return nopPublisher{}, nil }, w.writer,
		outbox.RelayOptions{Log: zerolog.Nop(), Hooks: map[string]outbox.Hook{
			auth.RouteSessionsRevoked: auth.RevocationHook(w.pool, auth.NewStore(w.rdb, w.ks.Prefix()), 15*time.Minute, zerolog.Nop()),
		}})
	if _, err := r.Drain(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

// AC: an event published via replica A reaches a client on replica B. The producer is a real request on
// replica A (DELETE /v1/me/sessions/{sid}, then logout-all): its outbox row goes through the relay to
// Redis, and the stream held on replica B gets session.revoked; another session's logout leaves the
// stream open, its own ends it (R22, R50).
func TestEventPublishedViaReplicaAReachesReplicaB(t *testing.T) {
	w := newWorld(t)
	a, b := w.replica(sse.Config{}), w.replica(sse.Config{})
	uid := w.member("ta@example.test", w.own, "tenant_admin")
	s1 := w.login(a.internal, "ta@example.test", "web")
	s2 := w.login(a.internal, "ta@example.test", "web")

	onB, r := w.open(b.internal+"/v1/events", "Authorization", "Bearer "+s1.access)
	if onB == nil {
		t.Fatalf("open on B: %d %s", r.status, r.raw)
	}
	if ct := onB.header.Get("Content-Type"); ct != "text/event-stream" || onB.header.Get("Cache-Control") != "no-cache, no-transform" ||
		onB.header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers %v", onB.header)
	}
	onB.start()
	onA, _ := w.open(a.internal+"/v1/events", "Authorization", "Bearer "+s1.access)
	onA.start()

	// Another session's logout: the event arrives, the stream stays.
	if r := w.call(http.MethodDelete, a.internal+"/v1/me/sessions/"+s2.sid, s1.access, nil); r.status != http.StatusNoContent {
		t.Fatalf("revoke s2: %d %s", r.status, r.raw)
	}
	w.relay()
	for _, c := range []*client{onB, onA} {
		f := c.event()
		typ, topic, data := f.envelope(t)
		if f.Event != realtime.EventSessionRevoked || typ != f.Event || topic != "user:"+uid || data["reason"] != "logout" {
			t.Fatalf("frame %+v", f)
		}
		if _, err := strconv.ParseInt(f.ID, 10, 64); err != nil {
			t.Fatalf("id %q is not a sequence number", f.ID)
		}
	}
	gid := w.publish("hubs.changed", `{"ids":["h1"]}`, "global")
	if f := onB.event(); f.Event != "hubs.changed" || f.ID != strconv.FormatInt(gid, 10) {
		t.Fatalf("after another session's logout the stream on B should go on: %+v", f)
	}

	// logout-all on replica A revokes s1 itself: the stream on B gets session.revoked and ends.
	if r := w.call(http.MethodPost, a.internal+"/v1/auth/logout-all", s1.access, nil); r.status != http.StatusNoContent {
		t.Fatalf("logout-all: %d %s", r.status, r.raw)
	}
	w.relay()
	f := onB.event()
	if _, _, data := f.envelope(t); f.Event != realtime.EventSessionRevoked || data["reason"] != "logout_all" {
		t.Fatalf("frame %+v", f)
	}
	onB.ends()
	// A revoked session cannot reopen.
	_, r = w.open(b.internal+"/v1/events", "Authorization", "Bearer "+s1.access)
	expect(t, r, http.StatusUnauthorized, auth.CodeSessionRevoked)
}

// Implicit topics follow the principal: the own fleet's tenant topics reach its staff and not a carrier's
// staff; a carrier's events do not reach the own fleet without contractor reach; the event names are
// mapped per topic; a channel outside the catalogue is never relayed (R51).
func TestImplicitTopicsAndEventNames(t *testing.T) {
	w := newWorld(t)
	r := w.replica(sse.Config{})
	w.member("ta@example.test", w.own, "tenant_admin")
	w.member("tc@example.test", w.other, "tenant_admin")
	own, _ := w.open(r.internal+"/v1/events", "Authorization", "Bearer "+w.login(r.internal, "ta@example.test", "web").access)
	own.start()
	other, _ := w.open(r.internal+"/v1/events", "Authorization", "Bearer "+w.login(r.internal, "tc@example.test", "web").access)
	other.start()

	tasks := realtime.TenantTopic(w.own, realtime.FamilyTasks)
	w.publish("task.assigned", `{"taskType":"first_mile","planDate":"2026-10-10"}`, tasks)
	// A rogue publisher on a channel outside the catalogue reaches nobody.
	if err := w.rdb.Publish(context.Background(), w.ks.Channel("tenant:"+w.own+":payroll"),
		`{"id":999999,"topic":"tenant:`+w.own+`:payroll","type":"payroll.approved","eventId":"e","data":{}}`).Err(); err != nil {
		t.Fatal(err)
	}
	w.publish("trip.delivered", `{}`, realtime.TenantTopic(w.other, realtime.FamilyTrips))
	w.publish("settings.changed", `{"key":"mobile_app"}`, realtime.TopicGlobal)

	f := own.event()
	if typ, topic, data := f.envelope(t); f.Event != "task.assigned" || typ != "task.assigned" || topic != tasks || data["taskType"] != "first_mile" {
		t.Fatalf("own fleet: %+v", f)
	}
	if f := own.event(); f.Event != realtime.EventMobileSettingsChanged {
		t.Fatalf("own fleet should next get the global settings event (no payroll, no carrier trip): %+v", f)
	}
	if f := other.event(); f.Event != "trip.delivered" {
		t.Fatalf("carrier: %+v", f)
	}
	if f := other.event(); f.Event != realtime.EventMobileSettingsChanged {
		t.Fatalf("carrier should next get the global event: %+v", f)
	}

	// roles.changed on the stream's tenant ends it so the implicit topics are recomputed.
	w.publish("roles.changed", `{}`, realtime.TenantTopic(w.own, realtime.FamilyConfig))
	if f := own.event(); f.Event != "roles.changed" {
		t.Fatalf("%+v", f)
	}
	if f := own.event(); f.Event != sse.EventReconnect || f.reason(t) != sse.ReasonRolesChanged {
		t.Fatalf("%+v", f)
	}
	own.ends()
}

// AC: a reconnect with Last-Event-ID replays the missed events of every subscribed topic in id order,
// then continues live without duplicates; an id from before the trim gets resync, and so do an id the
// server never issued, an id older than RTLOG_TTL on a log that expired, and one more than 500 events
// behind on a topic.
func TestLastEventIDReplayAndResync(t *testing.T) {
	w := newWorld(t)
	r := w.replica(sse.Config{})
	uid := w.member("ta@example.test", w.own, "tenant_admin")
	bearer := "Bearer " + w.login(r.internal, "ta@example.test", "web").access
	tasks, trips := realtime.TenantTopic(w.own, realtime.FamilyTasks), realtime.TenantTopic(w.own, realtime.FamilyTrips)
	vehicles := realtime.TenantTopic(w.own, realtime.FamilyVehicleLocations)

	c := w.mustOpen(r.internal+"/v1/events", "Authorization", bearer)
	last := c.start()
	c.close()
	missed := []int64{
		w.publish("task.created", `{}`, tasks),
		w.publish("job.updated", `{"id":"j","status":"running"}`, "user:"+uid),
	}
	w.publish("task.created", `{}`, realtime.TenantTopic(w.other, realtime.FamilyTasks)) // not this principal's
	missed = append(missed, w.publish("trip.delivered", `{}`, trips, tasks))             // one event, two topics
	missed = append(missed, w.publish("hubs.changed", `{}`, "global"))
	w.publish("vehicle_locations.updated", `[]`, vehicles) // ephemeral: never replayed

	c = w.mustOpen(r.internal+"/v1/events", "Authorization", bearer, "Last-Event-ID", last)
	if f := c.next(); f.Retry != "3000" {
		t.Fatalf("%+v", f)
	}
	want := []struct {
		id    int64
		event string
		topic string
	}{
		{missed[0], "task.created", tasks},
		{missed[1], "job.updated", "user:" + uid},
		{missed[2], "trip.delivered", tasks},
		{missed[3], "hubs.changed", "global"},
	}
	for _, x := range want {
		f := c.next()
		if _, topic, _ := f.envelope(t); f.ID != strconv.FormatInt(x.id, 10) || f.Event != x.event || topic != x.topic {
			t.Fatalf("replayed %+v, want %+v", f, x)
		}
	}
	seq, _ := w.rdb.Get(context.Background(), w.ks.RealtimeSeq()).Int64()
	if f := c.next(); f.Event != "" || f.ID != strconv.FormatInt(seq, 10) {
		t.Fatalf("after the replay %+v, want id %d", f, seq)
	}
	live := w.publish("task.updated", `{}`, tasks)
	if f := c.event(); f.ID != strconv.FormatInt(live, 10) || f.Event != "task.updated" {
		t.Fatalf("live %+v", f)
	}
	c.close()

	resync := func(lastID, want string) {
		t.Helper()
		c, r := w.open(r.internal+"/v1/events", "Authorization", bearer, "Last-Event-ID", lastID)
		if c == nil {
			t.Fatalf("open: %d %s", r.status, r.raw)
		}
		c.next() // retry
		f := c.next()
		seq, _ := w.rdb.Get(context.Background(), w.ks.RealtimeSeq()).Int64()
		if f.Event != sse.EventResync || f.reason(t) != want || f.ID != strconv.FormatInt(seq, 10) {
			t.Fatalf("Last-Event-ID %s: %+v, want resync %s at id %d", lastID, f, want, seq)
		}
		// Live events follow the resync.
		n := w.publish("hubs.changed", `{}`, "global")
		if f := c.event(); f.ID != strconv.FormatInt(n, 10) {
			t.Fatalf("after resync %+v", f)
		}
		c.close()
	}
	resync(strconv.FormatInt(seq+1000, 10), realtime.ResyncUnknownID)
	resync("not-a-number", realtime.ResyncUnknownID)

	// Trimmed: RTLOG_MAXLEN 10 and the rtlog.trim job cut the tasks log to its last 10 entries.
	small := realtime.NewWriter(w.rdb, w.ks, 10, time.Hour)
	before := strconv.FormatInt(live, 10)
	var ids []int64
	for range 30 {
		n, err := small.Publish(context.Background(), realtime.Event{Type: "task.updated", EventID: uuid.NewString(), Topics: []string{tasks}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, n)
	}
	if _, err := small.Trim(context.Background()); err != nil {
		t.Fatal(err)
	}
	resync(before, realtime.ResyncTrimmed)
	// An id inside the kept tail still replays.
	c = w.mustOpen(r.internal+"/v1/events", "Authorization", bearer, "Last-Event-ID", strconv.FormatInt(ids[25], 10))
	c.next()
	for _, id := range ids[26:] {
		if f := c.next(); f.ID != strconv.FormatInt(id, 10) {
			t.Fatalf("tail replay %+v, want %d", f, id)
		}
	}
	c.close()

	// Expired. A log that never had an event (the tenant's chats) is no gap while the id is inside the
	// RTLOG_TTL window...
	ctx := context.Background()
	old := w.publish("hubs.changed", `{}`, "global")
	c = w.mustOpen(r.internal+"/v1/events", "Authorization", bearer, "Last-Event-ID", strconv.FormatInt(old, 10))
	c.next()
	if f := c.next(); f.Event != "" {
		t.Fatalf("a log without events inside the window resynced: %+v", f)
	}
	c.close()
	// ...but once an event after the id sat on a log that expired since, and rtlog:marks dates the id
	// before the window, the replay cannot be complete.
	w.publish("job.updated", `{}`, "user:"+uid)
	if err := w.rdb.Del(ctx, w.ks.RealtimeLog("user:"+uid)).Err(); err != nil {
		t.Fatal(err)
	}
	now, err := w.rdb.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	seq, _ = w.rdb.Get(ctx, w.ks.RealtimeSeq()).Int64()
	// The window's first event is after the lost one: rtlog:marks dates it before RTLOG_TTL.
	w.rdb.Del(ctx, w.ks.RealtimeMarks())
	w.rdb.ZAdd(ctx, w.ks.RealtimeMarks(), redis.Z{Score: float64(now.Unix() / 60), Member: strconv.FormatInt(seq+1, 10)})
	resync(strconv.FormatInt(old, 10), realtime.ResyncExpired)

	// Too far behind: more than 500 events on one topic after the id.
	from := w.publish("hubs.changed", `{}`, "global")
	for range realtime.MaxReplayPerTopic + 1 {
		w.publish("task.updated", `{}`, tasks)
	}
	resync(strconv.FormatInt(from, 10), realtime.ResyncTooFarBehind)
}
