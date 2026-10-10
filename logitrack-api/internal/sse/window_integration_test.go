//go:build integration

package sse_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/sse"
)

// The connect window (main spec §8.2, refinement 6): a stream checks its credential before it subscribes
// and before the replay snapshot fixes the sequence it goes live from. A revocation, a claims change or
// a role-matrix change published in between must still end it (or refuse it). Without the re-check and
// the control window such an event was lost: it came before the subscription, or its id was at or below
// the snapshot's sequence, and a revoked session's stream relayed events until its token expired.

// hookPool runs fn once, at the first transaction after it was armed: the chat-visibility check of
// ?topics=chat:{id}, after the credential was checked and before the stream subscribes.
type hookPool struct {
	db.Beginner
	armed atomic.Bool
	fn    func()
}

func (h *hookPool) Begin(ctx context.Context) (pgx.Tx, error) {
	if h.armed.CompareAndSwap(true, false) {
		h.fn()
	}
	return h.Beginner.Begin(ctx)
}

// snapshotHook runs fn once, right before the replay snapshot's MULTI (the pipeline that reads
// rtlog:seq): after the stream subscribed and took its lease.
type snapshotHook struct {
	seqKey string
	armed  atomic.Bool
	fn     func()
}

func (h *snapshotHook) DialHook(next redis.DialHook) redis.DialHook          { return next }
func (h *snapshotHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (h *snapshotHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, c := range cmds {
			if a := c.Args(); len(a) == 2 && a[0] == "get" && a[1] == h.seqKey && h.armed.CompareAndSwap(true, false) {
				h.fn()
				break
			}
		}
		return next(ctx, cmds)
	}
}

// windowWorld is a world with one replica whose connect window can be entered at both points.
type windowWorld struct {
	*world
	r    *replica
	pool *hookPool
	snap *snapshotHook
	chat string // a chat of the own fleet's driver: staff and that driver may follow it
}

func newWindowWorld(t *testing.T) *windowWorld {
	t.Helper()
	w := newWorld(t)
	hooked := redis.NewClient(w.rdb.Options())
	t.Cleanup(func() { _ = hooked.Close() })
	ww := &windowWorld{world: w, pool: &hookPool{Beginner: w.pool}, snap: &snapshotHook{seqKey: w.ks.RealtimeSeq()}}
	hooked.AddHook(ww.snap)
	ww.r = w.replicaWith(sse.Config{MobileEnabled: true}, replicaDeps{pool: ww.pool, reader: hooked})
	return ww
}

// at arms the hook of the given point to run fn inside the next stream's connect window.
func (ww *windowWorld) at(point string, fn func()) {
	switch point {
	case "before_subscribe":
		ww.pool.fn = fn
		ww.pool.armed.Store(true)
	case "before_snapshot":
		ww.snap.fn = fn
		ww.snap.armed.Store(true)
	default:
		ww.t.Fatalf("unknown point %s", point)
	}
}

// fired fails the test when the armed hook never ran (the stream did not pass the point).
func (ww *windowWorld) fired() {
	ww.t.Helper()
	if ww.pool.armed.Load() || ww.snap.armed.Load() {
		ww.t.Fatal("the connect-window hook never ran")
	}
}

// do is a call made from inside a hook (the server's goroutine): failures are reported, not fatal.
func (ww *windowWorld) do(method, path, bearer string, want int) {
	var body any
	if method == http.MethodPost {
		body = map[string]any{}
	}
	if r := ww.call(method, ww.r.internal+path, bearer, body); r.status != want {
		ww.t.Errorf("%s %s: %d %s", method, path, r.status, r.raw)
	}
}

func (ww *windowWorld) chatTopic(t *testing.T, did string) string {
	t.Helper()
	if ww.chat == "" {
		ww.chat = ww.id(`INSERT INTO chats (tenant_id, tenant_source, driver_id) VALUES ($1, 'driver', $2) RETURNING id::text`, ww.own, did)
	}
	return realtime.ChatTopic(ww.chat)
}

var points = []string{"before_subscribe", "before_snapshot"}

// A session revoked inside the window (another session of the user revokes it, the relay publishes
// session.revoked): the stream is refused with 401 session_revoked instead of relaying events until the
// token expires; with Last-Event-ID too.
func TestRevocationInsideTheConnectWindowRefusesTheStream(t *testing.T) {
	for _, point := range points {
		for _, withLastID := range []bool{false, true} {
			t.Run(point+"/lastEventId="+strconv.FormatBool(withLastID), func(t *testing.T) {
				ww := newWindowWorld(t)
				_, did := ww.driver("drv@example.test", ww.own)
				ww.member("ta@example.test", ww.own, "tenant_admin")
				s1 := ww.login(ww.r.internal, "ta@example.test", "web")
				s2 := ww.login(ww.r.internal, "ta@example.test", "web")
				headers := []string{"Authorization", "Bearer " + s1.access}
				if withLastID {
					headers = append(headers, "Last-Event-ID", strconv.FormatInt(ww.publish("hubs.changed", `{}`, "global"), 10))
				}
				ww.at(point, func() {
					ww.do(http.MethodDelete, "/v1/me/sessions/"+s1.sid, s2.access, http.StatusNoContent)
					ww.relay()
				})
				_, r := ww.open(ww.r.internal+"/v1/events?topics="+url.QueryEscape(ww.chatTopic(t, did)), headers...)
				ww.fired()
				expect(t, r, http.StatusUnauthorized, auth.CodeSessionRevoked)
			})
		}
	}
}

// The same on the driver app's ticket stream: the session logs out while its stream connects.
func TestRevocationInsideTheConnectWindowRefusesTheTicketStream(t *testing.T) {
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			ww := newWindowWorld(t)
			_, did := ww.driver("drv@example.test", ww.own)
			mob := ww.login(ww.r.public, "drv@example.test", "android")
			tk := ww.ticket(ww.r.public, mob)
			ww.at(point, func() {
				ww.do(http.MethodPost, "/v1/auth/logout", mob.access, http.StatusNoContent)
				ww.relay()
			})
			_, r := ww.open(ww.r.public + "/v1/mobile/events?topics=" + url.QueryEscape(ww.chatTopic(t, did)) +
				"&ticket=" + url.QueryEscape(tk))
			ww.fired()
			expect(t, r, http.StatusUnauthorized, auth.CodeSessionRevoked)
		})
	}
}

// A claims change inside the window (auth_version++ and session.revoked claims_changed through the
// relay): 401 token_expired with details.reason claims_changed, so the client refreshes and reopens with
// the new claims instead of keeping the old implicit topics.
func TestClaimsChangeInsideTheConnectWindowRefusesTheStream(t *testing.T) {
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			ww := newWindowWorld(t)
			_, did := ww.driver("drv@example.test", ww.own)
			uid := ww.member("ta@example.test", ww.own, "tenant_admin")
			s := ww.login(ww.r.internal, "ta@example.test", "web")
			ww.at(point, func() {
				ctx := context.Background()
				var pc *auth.PostCommit
				if err := db.WithSystem(ctx, ww.pool.Beginner, nil, func(tx pgx.Tx) error {
					var err error
					pc, _, err = ww.auth.RevokeInTx(ctx, tx, auth.Revocation{UserID: uuid.MustParse(uid), Reason: auth.RevokeClaimsChanged, BumpVersion: true})
					return err
				}); err != nil {
					ww.t.Errorf("claims change: %v", err)
					return
				}
				ww.auth.Apply(ctx, pc)
				ww.relay()
			})
			_, r := ww.open(ww.r.internal+"/v1/events?topics="+url.QueryEscape(ww.chatTopic(t, did)), "Authorization", "Bearer "+s.access)
			ww.fired()
			expect(t, r, http.StatusUnauthorized, auth.CodeTokenExpired)
			if r.detail("reason") != auth.ReasonClaimsChanged {
				t.Fatalf("%s", r.raw)
			}
		})
	}
}

// roles.changed has no marker to re-check: the snapshot reads the stream's control topics from the
// sequence taken before the credential was checked, and an ending event there ends the stream at once,
// as it would live (the event, then reconnect roles_changed). With Last-Event-ID the replay already
// carries the event and it is not sent twice.
func TestRolesChangedInsideTheConnectWindowEndsTheStream(t *testing.T) {
	for _, point := range points {
		for _, withLastID := range []bool{false, true} {
			t.Run(point+"/lastEventId="+strconv.FormatBool(withLastID), func(t *testing.T) {
				ww := newWindowWorld(t)
				_, did := ww.driver("drv@example.test", ww.own)
				ww.member("ta@example.test", ww.own, "tenant_admin")
				s := ww.login(ww.r.internal, "ta@example.test", "web")
				headers := []string{"Authorization", "Bearer " + s.access}
				if withLastID {
					headers = append(headers, "Last-Event-ID", strconv.FormatInt(ww.publish("hubs.changed", `{}`, "global"), 10))
				}
				config := realtime.TenantTopic(ww.own, realtime.FamilyConfig)
				var roles int64
				ww.at(point, func() {
					n, err := ww.writer.Publish(context.Background(), realtime.Event{
						Type: "roles.changed", EventID: uuid.NewString(), Topics: []string{config}, Data: []byte(`{}`),
					})
					if err != nil {
						ww.t.Errorf("publish: %v", err)
					}
					roles = n
				})
				c := ww.mustOpen(ww.r.internal+"/v1/events?topics="+url.QueryEscape(ww.chatTopic(t, did)), headers...)
				ww.fired()
				var got []frame
				for {
					f := c.event()
					got = append(got, f)
					if f.Event == sse.EventReconnect {
						break
					}
				}
				if len(got) != 2 || got[0].Event != realtime.EventRolesChanged || got[1].reason(t) != sse.ReasonRolesChanged {
					t.Fatalf("frames %+v, want roles.changed (id %d) once, then reconnect roles_changed", got, roles)
				}
				if _, topic, _ := got[0].envelope(t); topic != config {
					t.Fatalf("roles.changed on %s", topic)
				}
				c.ends()
			})
		}
	}
}

// The window is a mechanism of its own, not only the re-check: a session.revoked naming the stream's
// session that sits in the window ends the stream after the event (as it would live) even when the
// session state says nothing (here the event is published by hand, without a revocation).
func TestSessionRevokedInsideTheConnectWindowEndsTheStream(t *testing.T) {
	ww := newWindowWorld(t)
	_, did := ww.driver("drv@example.test", ww.own)
	uid := ww.member("ta@example.test", ww.own, "tenant_admin")
	s := ww.login(ww.r.internal, "ta@example.test", "web")
	ww.at("before_snapshot", func() {
		ww.publish(realtime.TypeSessionsRevoked, `{"userId":"`+uid+`","sessionIds":["`+s.sid+`"],"reason":"admin_revoke"}`, realtime.UserTopic(uid))
	})
	c := ww.mustOpen(ww.r.internal+"/v1/events?topics="+url.QueryEscape(ww.chatTopic(t, did)), "Authorization", "Bearer "+s.access)
	ww.fired()
	if f := c.event(); f.Event != realtime.EventSessionRevoked {
		t.Fatalf("%+v", f)
	}
	c.ends()
}

// Events published before the request are reflected in the credential and never end a stream, even
// when the replay carries them; another session's logout inside the window leaves the stream open.
func TestEarlierEventsAndOtherSessionsDoNotEndTheStream(t *testing.T) {
	ww := newWindowWorld(t)
	_, did := ww.driver("drv@example.test", ww.own)
	ww.member("ta@example.test", ww.own, "tenant_admin")
	s1 := ww.login(ww.r.internal, "ta@example.test", "web")
	s2 := ww.login(ww.r.internal, "ta@example.test", "web")
	before := ww.publish("hubs.changed", `{}`, "global")
	ww.publish("roles.changed", `{}`, realtime.TenantTopic(ww.own, realtime.FamilyConfig))
	ww.at("before_subscribe", func() {
		ww.do(http.MethodDelete, "/v1/me/sessions/"+s2.sid, s1.access, http.StatusNoContent)
		ww.relay()
	})
	c := ww.mustOpen(ww.r.internal+"/v1/events?topics="+url.QueryEscape(ww.chatTopic(t, did)),
		"Authorization", "Bearer "+s1.access, "Last-Event-ID", strconv.FormatInt(before, 10))
	ww.fired()
	if f := c.event(); f.Event != realtime.EventRolesChanged {
		t.Fatalf("replayed %+v", f)
	}
	if f := c.event(); f.Event != realtime.EventSessionRevoked {
		t.Fatalf("replayed %+v", f)
	}
	live := ww.publish("hubs.changed", `{}`, "global")
	if f := c.event(); f.Event != "hubs.changed" || f.ID != strconv.FormatInt(live, 10) {
		t.Fatalf("the stream should go on: %+v", f)
	}
}
