//go:build integration

package sse_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/sse"
)

// AC: the 6th stream of a user is refused with 429, across replicas (rl:sse_conns:{userId} is shared),
// and a closed stream frees its slot.
func TestSixthConnectionOfAUserIsRefused(t *testing.T) {
	w := newWorld(t)
	a, b := w.replica(sse.Config{}), w.replica(sse.Config{})
	w.member("ta@example.test", w.own, "tenant_admin")
	w.member("op@example.test", w.own, "operator")
	bearer := "Bearer " + w.login(a.internal, "ta@example.test", "web").access
	var open []*client
	for i := range 5 {
		base := a.internal
		if i >= 3 {
			base = b.internal
		}
		c, r := w.open(base+"/v1/events", "Authorization", bearer)
		if c == nil {
			t.Fatalf("stream %d: %d %s", i+1, r.status, r.raw)
		}
		c.start()
		open = append(open, c)
	}
	_, r := w.open(b.internal+"/v1/events", "Authorization", bearer)
	expect(t, r, http.StatusTooManyRequests, httpx.CodeResourceExhaust)
	if r.header.Get("Retry-After") != "60" || r.detail("bucket") != "sse_conns" || r.detail("limit") != float64(5) {
		t.Fatalf("429: %v %s", r.header, r.raw)
	}
	// Another user is not affected.
	other, r := w.open(a.internal+"/v1/events", "Authorization", "Bearer "+w.login(a.internal, "op@example.test", "web").access)
	if other == nil {
		t.Fatalf("another user: %d %s", r.status, r.raw)
	}
	// Closing one stream frees its slot once the server notices (next heartbeat).
	open[0].close()
	deadline := time.Now().Add(wait)
	for {
		c, r := w.open(a.internal+"/v1/events", "Authorization", bearer)
		if c != nil {
			c.start()
			break
		}
		if r.status != http.StatusTooManyRequests || time.Now().After(deadline) {
			t.Fatalf("after a close: %d %s", r.status, r.raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// AC: tickets. A ticket is refused on /v1/events and not spent there; /v1/events is 404 on the public
// listener; /v1/mobile/events without a valid ticket is 401; a ticket opens one stream only; the
// driver's stream carries user:{uid} and driver:{did} with the driver-topic event names; MOBILE_SSE_ENABLED
// off is 404.
func TestMobileTicketStream(t *testing.T) {
	w := newWorld(t)
	r := w.replica(sse.Config{MobileEnabled: true})
	off := w.replica(sse.Config{})
	_, did := w.driver("drv@example.test", w.own)
	w.member("ta@example.test", w.own, "tenant_admin")
	mob := w.login(r.public, "drv@example.test", "android")
	web := w.login(r.internal, "ta@example.test", "web")
	tk := w.ticket(r.public, mob)

	_, resp := w.open(r.internal + "/v1/events?ticket=" + url.QueryEscape(tk))
	expect(t, resp, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	_, resp = w.open(r.internal+"/v1/events?ticket="+url.QueryEscape(tk), "Authorization", "Bearer "+web.access)
	expect(t, resp, http.StatusBadRequest, httpx.CodeBadRequest)
	if resp.detail("reason") != "ticket_not_accepted" {
		t.Fatalf("%s", resp.raw)
	}
	_, resp = w.open(r.public+"/v1/events", "Authorization", "Bearer "+web.access)
	expect(t, resp, http.StatusNotFound, httpx.CodeNotFound)
	_, resp = w.open(r.public + "/v1/mobile/events")
	expect(t, resp, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	_, resp = w.open(r.public+"/v1/mobile/events", "Authorization", "Bearer "+mob.access) // never a bearer
	expect(t, resp, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	unknown := make([]byte, 32)
	_, _ = rand.Read(unknown)
	_, resp = w.open(r.public + "/v1/mobile/events?ticket=" + base64.RawURLEncoding.EncodeToString(unknown))
	expect(t, resp, http.StatusUnauthorized, auth.CodeInvalidToken)
	_, resp = w.open(r.public + "/v1/mobile/events?access_token=" + url.QueryEscape(mob.access))
	expect(t, resp, http.StatusBadRequest, httpx.CodeBadRequest)
	_, resp = w.open(off.public + "/v1/mobile/events?ticket=" + url.QueryEscape(tk))
	expect(t, resp, http.StatusNotFound, httpx.CodeNotFound)

	// The ticket survived every refusal above and opens exactly one stream.
	c, resp := w.open(r.public + "/v1/mobile/events?ticket=" + url.QueryEscape(tk))
	if c == nil {
		t.Fatalf("mobile stream: %d %s", resp.status, resp.raw)
	}
	c.start()
	_, resp = w.open(r.public + "/v1/mobile/events?ticket=" + url.QueryEscape(tk))
	expect(t, resp, http.StatusUnauthorized, auth.CodeInvalidToken)

	w.publish("task.assigned", `{"taskType":"line_haul"}`, realtime.TenantTopic(w.own, realtime.FamilyTasks), realtime.DriverTopic(did))
	w.publish("hubs.changed", `{}`, realtime.TopicGlobal) // staff only
	w.publish("leave.decided", `{"status":"approved"}`, realtime.TenantTopic(w.own, realtime.FamilyHR), realtime.DriverTopic(did))
	if f := c.event(); f.Event != realtime.EventTasksChanged {
		t.Fatalf("%+v", f)
	}
	if f := c.event(); f.Event != realtime.EventLeaveChanged {
		t.Fatalf("the driver got %+v; want leave.changed (no global, no tenant topic)", f)
	}

	// A ticket of a session revoked meanwhile is refused.
	tk2 := w.ticket(r.public, mob)
	if r := w.call(http.MethodPost, r.public+"/v1/auth/logout", mob.access, map[string]any{}); r.status != http.StatusNoContent {
		t.Fatalf("logout: %d %s", r.status, r.raw)
	}
	_, resp = w.open(r.public + "/v1/mobile/events?ticket=" + url.QueryEscape(tk2))
	expect(t, resp, http.StatusUnauthorized, auth.CodeSessionRevoked)
}

// Explicit chat:{id} topics are checked against chat visibility: the chat's own driver and staff with
// chat:view in reach may follow it, anyone else gets 403; message.created carries the full message.
func TestExplicitChatTopic(t *testing.T) {
	w := newWorld(t)
	r := w.replica(sse.Config{MobileEnabled: true})
	_, did := w.driver("drv@example.test", w.own)
	w.member("ta@example.test", w.own, "tenant_admin")
	w.member("tc@example.test", w.other, "tenant_admin")
	w.member("us@example.test", w.own, "user") // no chat:view
	chat := w.id(`INSERT INTO chats (tenant_id, tenant_source, driver_id) VALUES ($1, 'driver', $2) RETURNING id::text`, w.own, did)
	topic := realtime.ChatTopic(chat)

	staff, resp := w.open(r.internal+"/v1/events?topics="+url.QueryEscape(topic), "Authorization",
		"Bearer "+w.login(r.internal, "ta@example.test", "web").access)
	if staff == nil {
		t.Fatalf("staff: %d %s", resp.status, resp.raw)
	}
	staff.start()
	tk := w.ticket(r.public, w.login(r.public, "drv@example.test", "android"))
	driver, resp := w.open(r.public + "/v1/mobile/events?topics=" + url.QueryEscape(topic) + "&ticket=" + url.QueryEscape(tk))
	if driver == nil {
		t.Fatalf("driver: %d %s", resp.status, resp.raw)
	}
	driver.start()
	for _, email := range []string{"tc@example.test", "us@example.test"} {
		_, resp := w.open(r.internal+"/v1/events?topics="+url.QueryEscape(topic), "Authorization",
			"Bearer "+w.login(r.internal, email, "web").access)
		expect(t, resp, http.StatusForbidden, authz.CodePermissionDenied)
	}
	_, resp = w.open(r.internal+"/v1/events?topics=tenant:"+w.other+":tasks", "Authorization",
		"Bearer "+w.login(r.internal, "ta@example.test", "web").access)
	expect(t, resp, http.StatusForbidden, authz.CodePermissionDenied)
	_, resp = w.open(r.internal+"/v1/events?topics=tenant:"+w.own+":payroll", "Authorization",
		"Bearer "+w.login(r.internal, "ta@example.test", "web").access)
	expect(t, resp, http.StatusBadRequest, httpx.CodeBadRequest)

	w.publish("chat.message_created", `{"id":"m1","text":"hello"}`, topic, realtime.TenantTopic(w.own, realtime.FamilyChats))
	for _, c := range []*client{staff, driver} {
		f := c.event()
		if _, tp, data := f.envelope(t); f.Event != realtime.EventMessageCreated || tp != topic || data["text"] != "hello" {
			t.Fatalf("%+v", f)
		}
	}
	// Staff also follow the thin tenant chats event (same id, another name).
	if f := staff.event(); f.Event != "chat.message_created" {
		t.Fatalf("%+v", f)
	}
}

// A web stream ends with reconnect 30 s before its access token expires; on shutdown every stream gets
// reconnect {"reason":"shutdown"}; when the fan-out subscription is re-established, streams reconnect
// (interrupted) so the replay covers what the replica may have missed.
func TestReconnectReasons(t *testing.T) {
	w := newWorld(t)
	r := w.replica(sse.Config{})
	w.member("ta@example.test", w.own, "tenant_admin")
	s := w.login(r.internal, "ta@example.test", "web")

	// A token with 20 s left: under the 30 s lead, so the stream says reconnect at once.
	c, err := w.keys.Parse(s.access, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	short, err := w.keys.Sign(*c, time.Now().Add(-(w.keys.TTL() - 20*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	cl, resp := w.open(r.internal+"/v1/events", "Authorization", "Bearer "+short)
	if cl == nil {
		t.Fatalf("%d %s", resp.status, resp.raw)
	}
	cl.start()
	if f := cl.event(); f.Event != sse.EventReconnect || f.reason(t) != sse.ReasonTokenExpiring {
		t.Fatalf("%+v", f)
	}
	cl.ends()

	// Interrupted: kill the replica's pub/sub connection; go-redis resubscribes and the stream reconnects.
	cl, _ = w.open(r.internal+"/v1/events", "Authorization", "Bearer "+s.access)
	cl.start()
	if err := w.rdb.ClientKillByFilter(context.Background(), "TYPE", "pubsub").Err(); err != nil {
		t.Fatal(err)
	}
	if f := cl.event(); f.Event != sse.EventReconnect || f.reason(t) != realtime.EndInterrupted {
		t.Fatalf("%+v", f)
	}
	cl.ends()
	deadline := time.Now().Add(wait)
	for !r.hub.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("the hub did not resubscribe")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Shutdown: readiness 503, then reconnect, then the listeners close.
	cl, resp = w.open(r.internal+"/v1/events", "Authorization", "Bearer "+s.access)
	if cl == nil {
		t.Fatalf("%d %s", resp.status, resp.raw)
	}
	cl.start()
	done := make(chan struct{})
	start := time.Now()
	go func() { r.stop(); close(done) }()
	if f := cl.event(); f.Event != sse.EventReconnect || f.reason(t) != realtime.EndShutdown {
		t.Fatalf("%+v", f)
	}
	cl.ends()
	select {
	case <-done:
	case <-time.After(wait):
		t.Fatal("shutdown did not finish")
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("shutdown took %v: the streams held the drain", d)
	}
}
