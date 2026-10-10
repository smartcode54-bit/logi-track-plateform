package realtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
)

const tid = "0199c000-0000-7000-8000-000000000001"

func TestEventNameMapsPerTopic(t *testing.T) {
	for _, c := range []struct {
		topic, typ, want string
		ok               bool
	}{
		{"user:" + tid, "user.sessions_revoked", "session.revoked", true},
		{"user:" + tid, "job.updated", "job.updated", true},
		{"user:" + tid, "chat.message_created", "chat.message_created", true},
		{"driver:" + tid, "task.assigned", "tasks.changed", true},
		{"driver:" + tid, "task.cancelled", "tasks.changed", true},
		{"driver:" + tid, "maintenance.created", "maintenance.changed", true},
		{"driver:" + tid, "leave.decided", "leave.changed", true},
		{"driver:" + tid, "trip.resubmitted", "trip.review_changed", true},
		{"chat:" + tid, "chat.message_created", "message.created", true},
		{"chat:" + tid, "chat.read", "read.updated", true},
		{"tenant:" + tid + ":chats", "chat.message_created", "chat.message_created", true},
		{"tenant:" + tid + ":tasks", "task.assigned", "task.assigned", true},
		{"tenant:" + tid + ":trips", "trip.priced", "trip.priced", true},
		{"tenant:" + tid + ":config", "roles.changed", "roles.changed", true},
		{"global", "settings.changed", "mobile_settings.changed", true},
		{"global", "hubs.changed", "hubs.changed", true},
		{"dispatch:" + tid + ":trips", "trip.delivered", "trip.delivered", true},
		{"dispatch:" + tid + ":tasks", "task.assigned", "task.assigned", true},
		{"dispatch:" + tid + ":trips", "trip.priced", "", false},
		{"dispatch:" + tid + ":trips", "trip.repriced", "", false},
		{"dispatch:" + tid + ":trips", "standby.priced", "", false},
		{"global", "", "", false},
		{"global", "x\nevent: y", "", false},
	} {
		got, ok := EventName(c.topic, c.typ)
		if got != c.want || ok != c.ok {
			t.Errorf("EventName(%q, %q) = %q, %v; want %q, %v", c.topic, c.typ, got, ok, c.want, c.ok)
		}
	}
}

func TestTopicBuildersAreInTheCatalogue(t *testing.T) {
	for _, topic := range []string{UserTopic(tid), DriverTopic(tid), ChatTopic(tid), TopicGlobal, TopicPlatformSecurity,
		DispatchTopic(tid, FamilyTasks), DispatchTopic(tid, FamilyTrips)} {
		if !ValidTopic(topic) {
			t.Errorf("%s is not in the catalogue", topic)
		}
	}
	for _, f := range []string{FamilyTasks, FamilyTrips, FamilyChats, FamilyFleet, FamilyHR, FamilyExpenses, FamilyBilling,
		FamilyVehicleLocations, FamilyConfig} {
		if !ValidTopic(TenantTopic(tid, f)) {
			t.Errorf("family %s is not in the catalogue", f)
		}
	}
}

// The relay publishes only catalogue topics (Appendix B §B.4.2, R51): the writer refuses anything else
// before it sends a command, so no channel or log outside the 16 patterns is ever written.
func TestWriterRefusesTopicsOutsideTheCatalogue(t *testing.T) {
	ks, err := cache.NewKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	// A client that cannot connect: a refused topic must fail before any I/O.
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 50 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	w := NewWriter(rdb, ks, 0, 0)
	for _, bad := range []string{"tenant:" + tid + ":payroll", "tenants:" + tid + ":tasks", "cache", "seq", "marks",
		"global:x", "platform:audit", "dispatch:chats", "dispatch:tasks", "dispatch:" + tid + ":fleet", "user:U",
		"chat:" + tid + ":x"} {
		_, err := w.Publish(context.Background(), Event{Type: "x.y", EventID: "e", Topics: []string{"global", bad}})
		if !errors.Is(err, ErrInvalidTopic) {
			t.Errorf("%q: %v, want ErrInvalidTopic", bad, err)
		}
	}
	if _, err := w.Publish(context.Background(), Event{Type: "x.y"}); err == nil {
		t.Error("an event without topics was accepted")
	}
}

func TestStreamSeq(t *testing.T) {
	for in, want := range map[string]int64{"42-0": 42, "0-0": 0, "": 0, "x-0": 0, "-3-0": 0, "7": 7} {
		if got := streamSeq(in); got != want {
			t.Errorf("streamSeq(%q) = %d, want %d", in, got, want)
		}
	}
	m, ok := fromStream("global", redis.XMessage{ID: "9-0", Values: map[string]any{"type": "hubs.changed", "event_id": "e", "data": "not json"}})
	if !ok || m.ID != 9 || m.Type != "hubs.changed" || m.EventID != "e" || string(m.Data) != "{}" {
		t.Fatalf("%+v %v", m, ok)
	}
	if _, ok := fromStream("global", redis.XMessage{ID: "0-0"}); ok {
		t.Fatal("entry 0-0 accepted")
	}
}

func TestMarkMinutesCoverTheTTL(t *testing.T) {
	if got := markMinutes(24 * time.Hour); got != 24*60+60 {
		t.Fatalf("markMinutes(24h) = %d", got)
	}
	if got := markMinutes(90 * time.Second); got != 2+60 {
		t.Fatalf("markMinutes(90s) = %d", got)
	}
}
