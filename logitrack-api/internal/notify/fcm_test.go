package notify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/push"
)

type nopSender struct{}

func (nopSender) Send(context.Context, push.Message) error { return nil }

// Every routing key bound to notify.fcm is handled: the task and session events now; the chat,
// broadcast, maintenance and leave events fail transiently (dead-letter, replay once their planner
// lands) instead of being acked and lost; a key that is not bound is a permanent error. FCM_ENABLED
// false acks everything without sending. None of these paths touch PostgreSQL or Redis.
func TestFCMRouting(t *testing.T) {
	ctx := context.Background()
	q, ok := mq.Default.Queue(notify.QueueFCM)
	if !ok {
		t.Fatal("notify.fcm is not in the topology")
	}
	bound := map[string]bool{}
	for _, b := range q.Bindings {
		bound[b.Key] = true
	}
	planned := []string{notify.RouteTaskAssigned, notify.RouteTaskReassigned, notify.RouteTaskCancelled, notify.RouteTaskUpdated,
		notify.RouteTaskCheckedIn, notify.RouteTaskPlanDateChanged, notify.RouteSessionsRevoked}
	pending := map[string]string{notify.RouteChatMessageCreated: "T47", notify.RouteBroadcastCreated: "T48",
		notify.RouteMaintenanceCreated: "T40", notify.RouteMaintenanceReminder: "T40", notify.RouteLeaveDecided: "T45"}
	if len(bound) != len(planned)+len(pending) {
		t.Fatalf("notify.fcm binds %d keys, the consumer knows %d", len(bound), len(planned)+len(pending))
	}
	for _, k := range planned {
		if !bound[k] {
			t.Fatalf("%s is not bound to notify.fcm", k)
		}
	}
	f := &notify.FCM{Enabled: true, Sender: nopSender{}}
	for k, issue := range pending {
		if !bound[k] {
			t.Fatalf("%s is not bound to notify.fcm", k)
		}
		err := f.Handle(ctx, &mq.Delivery{Queue: notify.QueueFCM, MessageID: "1", RoutingKey: k, Body: []byte(`{}`)})
		if err == nil || mq.IsPermanent(err) || !strings.Contains(err.Error(), issue) {
			t.Errorf("%s: %v, want a transient error naming %s", k, err, issue)
		}
	}
	if err := f.Handle(ctx, &mq.Delivery{Queue: notify.QueueFCM, MessageID: "1", RoutingKey: "trip.delivered"}); !mq.IsPermanent(err) {
		t.Errorf("unbound key: %v", err)
	}
	if err := (&notify.FCM{Enabled: true}).Handle(ctx, &mq.Delivery{RoutingKey: notify.RouteTaskUpdated}); err == nil || mq.IsPermanent(err) {
		t.Errorf("enabled without a sender: %v", err)
	}
	off := &notify.FCM{}
	for _, k := range append(planned, notify.RouteChatMessageCreated, "trip.delivered") {
		if err := off.Handle(ctx, &mq.Delivery{RoutingKey: k, Body: []byte(`not json`)}); err != nil {
			t.Errorf("FCM_ENABLED=false, %s: %v", k, err)
		}
	}
}
