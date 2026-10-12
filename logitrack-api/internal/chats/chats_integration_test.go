//go:build integration

package chats_test

import (
	"bytes"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
)

// AC: an admin message pushes exactly one FCM message (per device of the driver), from the outbox event its
// transaction appended; a redelivery of that event sends nothing more, and a replay of the send appends no event.
func TestAdminMessagePushesExactlyOnce(t *testing.T) {
	w := newWorld(t)
	ta, drv := w.login("ta.o@example.test"), w.login("d.o@example.test")
	chat := w.openChat(ta, w.dO)
	cid := uuid.NewString()
	r := w.post("/v1/chats/"+chat+"/messages", ta, map[string]any{"text": "ไปรับของที่ฮับ A", "clientMessageId": cid})
	expect(t, r, http.StatusCreated, "")
	msg := str(r.data()["id"])
	// The replay: same clientMessageId, same message -> 200, the stored message, no second row, no second event.
	r = w.post("/v1/chats/"+chat+"/messages", ta, map[string]any{"text": "ไปรับของที่ฮับ A", "clientMessageId": cid})
	expect(t, r, http.StatusOK, "")
	if str(r.data()["id"]) != msg {
		t.Fatalf("replay answered another message %s", r.raw)
	}
	created := keys(w.events(chat), "chat.message_created")
	if len(created) != 1 || w.count(`SELECT count(*) FROM chat_messages WHERE chat_id = $1`, chat) != 1 {
		t.Fatalf("%d events for one admin message", len(created))
	}
	p := w.pusher(false)
	w.deliver(p, created[0])
	w.deliver(p, created[0]) // a redelivery
	sent := p.fcm.Sent()
	if len(sent) != 1 || len(p.fcm.SentTo("tok-"+w.uDO)) != 1 {
		t.Fatalf("%d pushes, want exactly one to the driver's device", len(sent))
	}
	m := sent[0].Message
	if m.Data["type"] != "chat" || m.Data["chatId"] != chat || m.Data["messageId"] != msg ||
		m.Notification.Title != "New message from Admin" || m.Notification.Body != "ไปรับของที่ฮับ A" ||
		m.Android.Notification.ChannelID != notify.ChannelChat {
		t.Fatalf("push %s", sent[0].Raw)
	}
	if n := w.count(`SELECT count(*) FROM notification_deliveries WHERE kind = 'chat' AND status = 'sent'`); n != 1 {
		t.Fatalf("%d delivery rows", n)
	}

	// A driver message pushes nothing by default; with NOTIFY_ADMIN_ON_DRIVER_MESSAGE the assigned admin's devices.
	expect(t, w.driverSays(drv, "รับทราบครับ"), http.StatusCreated, "")
	ev := keys(w.events(chat), "chat.message_created")
	if len(ev) != 2 || ev[1].Payload["senderRole"] != "driver" {
		t.Fatalf("driver message events %+v", ev)
	}
	w.deliver(p, ev[1])
	if n := len(p.fcm.Sent()); n != 1 {
		t.Fatalf("a driver message pushed %d", n-1)
	}
	w.exec(`INSERT INTO device_tokens (user_id, install_id, token, platform) VALUES ($1, 'web-ta', 'tok-web-ta', 'web')`, w.taO)
	admin := w.pusher(true)
	w.deliver(admin, ev[1])
	got := admin.fcm.SentTo("tok-web-ta")
	if len(admin.fcm.Sent()) != 1 || len(got) != 1 || got[0].Message.Notification.Title != "New message from สมชาย ใจดี" ||
		got[0].Message.Data["chatId"] != chat {
		t.Fatalf("admin push %+v", admin.fcm.Sent())
	}
}

// AC: the chat is auto-assigned to the first admin who replies (in_progress), once; a later reply of another admin
// keeps the assignment. The queue and "mine" follow.
func TestAutoAssignOnFirstAdminReply(t *testing.T) {
	w := newWorld(t)
	ta, op, drv := w.login("ta.o@example.test"), w.login("op.o@example.test"), w.login("d.o@example.test")
	expect(t, w.driverSays(drv, "สวัสดีครับ"), http.StatusCreated, "") // the driver's message creates its chat
	r := w.get("/v1/chats?filter=queue", ta)
	expect(t, r, http.StatusOK, "")
	if len(r.list()) != 1 {
		t.Fatalf("queue %s", r.raw)
	}
	chat := str(r.list()[0].(map[string]any)["id"])
	if c := r.list()[0].(map[string]any); c["status"] != "open" || c["assignedAdminUserId"] != nil || c["driverName"] != "สมชาย ใจดี" ||
		c["lastMessagePreview"] != "สวัสดีครับ" {
		t.Fatalf("queued chat %v", c)
	}
	expect(t, w.say(op, chat, "สวัสดีค่ะ"), http.StatusCreated, "")
	c := w.get("/v1/chats/"+chat, ta).data()
	if c["assignedAdminUserId"] != w.opO || c["status"] != "in_progress" || c["assignedAt"] == nil {
		t.Fatalf("after the first reply %v", c)
	}
	updated := keys(w.events(chat), "chat.updated")
	if len(updated) != 1 || !slices.Contains(anyStrings(updated[0].Payload["changed"]), "assignedAdminUserId") ||
		updated[0].Payload["assignedAdminUserId"] != w.opO || !slices.Contains(updated[0].Topics, "tenant:"+w.O+":chats") {
		t.Fatalf("chat.updated %+v", updated)
	}
	expect(t, w.say(ta, chat, "ขอเสริม"), http.StatusCreated, "")
	if c := w.get("/v1/chats/"+chat, ta).data(); c["assignedAdminUserId"] != w.opO {
		t.Fatalf("a second admin took the chat %v", c)
	}
	if n := len(keys(w.events(chat), "chat.updated")); n != 1 {
		t.Fatalf("%d chat.updated, the assignment changed once", n)
	}
	if l := w.get("/v1/chats?filter=queue", ta).list(); len(l) != 0 {
		t.Fatalf("still queued %v", l)
	}
	if l := w.get("/v1/chats?filter=mine", op).list(); len(l) != 1 || str(l[0].(map[string]any)["id"]) != chat {
		t.Fatalf("mine of the operator %v", l)
	}
	if l := w.get("/v1/chats?filter=mine", ta).list(); len(l) != 0 {
		t.Fatalf("mine of the admin %v", l)
	}
}

// AC: unread counts are computed by the server: the other side's messages after the reader's read state, per reader.
func TestUnreadCountsServerSide(t *testing.T) {
	w := newWorld(t)
	ta, op, drv := w.login("ta.o@example.test"), w.login("op.o@example.test"), w.login("d.o@example.test")
	for _, s := range []string{"1", "2", "3"} {
		expect(t, w.driverSays(drv, "ข้อความ "+s), http.StatusCreated, "")
	}
	r := w.get("/v1/chats?filter=all", ta)
	c := r.list()[0].(map[string]any)
	chat := str(c["id"])
	if num(c["unreadForMe"]) != 3 || c["lastReadAt"] != nil {
		t.Fatalf("unread before any read %v", c)
	}
	// The driver has read its own messages; nothing from staff is unread for it.
	if u := w.call(w.public, http.MethodGet, "/v1/mobile/chat/unread-count", drv, nil).data(); num(u["count"]) != 0 || num(u["messages"]) != 0 {
		t.Fatalf("driver unread %v", u)
	}
	// Reading up to the second message leaves one unread; reading never moves back.
	page := w.get("/v1/chats/"+chat+"/messages", ta).list()
	second := str(page[1].(map[string]any)["createdAt"])
	expect(t, w.post("/v1/chats/"+chat+"/read", ta, map[string]any{"lastReadAt": second}), http.StatusNoContent, "")
	if c := w.get("/v1/chats/"+chat, ta).data(); num(c["unreadForMe"]) != 1 || c["lastReadAt"] == nil {
		t.Fatalf("after reading two %v", c)
	}
	first := str(page[2].(map[string]any)["createdAt"])
	expect(t, w.post("/v1/chats/"+chat+"/read", ta, map[string]any{"lastReadAt": first}), http.StatusNoContent, "")
	if c := w.get("/v1/chats/"+chat, ta).data(); num(c["unreadForMe"]) != 1 {
		t.Fatalf("a read moved back %v", c)
	}
	reads := keys(w.events(chat), "chat.read")
	expect(t, w.post("/v1/chats/"+chat+"/read", ta, nil), http.StatusNoContent, "")
	if c := w.get("/v1/chats/"+chat, ta).data(); num(c["unreadForMe"]) != 0 {
		t.Fatalf("after reading all %v", c)
	}
	after := keys(w.events(chat), "chat.read")
	if len(after) != len(reads)+1 || after[len(after)-1].Payload["userId"] != w.taO ||
		!slices.Equal(after[len(after)-1].Topics, []string{"chat:" + chat}) {
		t.Fatalf("chat.read events %+v", after)
	}
	// Each reader has its own state: the operator has read nothing.
	if c := w.get("/v1/chats/"+chat, op).data(); num(c["unreadForMe"]) != 3 {
		t.Fatalf("operator unread %v", c)
	}
	// A staff reply: unread for the driver (one chat, one message) until the driver reads.
	expect(t, w.say(op, chat, "ได้รับแล้ว"), http.StatusCreated, "")
	if u := w.call(w.public, http.MethodGet, "/v1/mobile/chat/unread-count", drv, nil).data(); num(u["count"]) != 1 || num(u["messages"]) != 1 {
		t.Fatalf("driver unread after a reply %v", u)
	}
	if c := w.call(w.public, http.MethodGet, "/v1/mobile/chat", drv, nil).data(); num(c["unreadForMe"]) != 1 {
		t.Fatalf("driver chat %v", c)
	}
	expect(t, w.call(w.public, http.MethodPost, "/v1/mobile/chat/read", drv, map[string]any{}), http.StatusNoContent, "")
	if u := w.call(w.public, http.MethodGet, "/v1/mobile/chat/unread-count", drv, nil).data(); num(u["count"]) != 0 {
		t.Fatalf("driver unread after reading %v", u)
	}
	// The room's read receipts: the driver's read state is reported apart.
	d := w.get("/v1/chats/"+chat, op).data()
	if d["driverLastReadAt"] == nil || len(d["reads"].([]any)) != 3 {
		t.Fatalf("read receipts %v", d)
	}
	// A sender's own message is read by its sender.
	if c := w.get("/v1/chats/"+chat, op).data(); num(c["unreadForMe"]) != 0 {
		t.Fatalf("the operator replied after the driver's messages: %v", c)
	}
	expect(t, w.post("/v1/chats/"+chat+"/read", ta, map[string]any{"lastReadAt": "yesterday"}), http.StatusUnprocessableEntity, "invalid_argument")
}

// AC: replaying a message with the same clientMessageId creates no second row, whether in sequence or concurrently;
// the same id with another message is 409 idempotency_conflict.
func TestClientMessageIDReplay(t *testing.T) {
	w := newWorld(t)
	ta, drv := w.login("ta.o@example.test"), w.login("d.o@example.test")
	chat := w.openChat(ta, w.dO)
	cid := uuid.NewString()
	body := map[string]any{"text": "ส่งซ้ำ", "clientMessageId": cid}
	var wg sync.WaitGroup
	statuses := make([]int, 10)
	ids := make([]string, 10)
	for i := range 10 {
		wg.Go(func() {
			r := w.post("/v1/chats/"+chat+"/messages", ta, body)
			statuses[i], ids[i] = r.status, str(r.data()["id"])
		})
	}
	wg.Wait()
	created := 0
	for i, s := range statuses {
		switch s {
		case http.StatusCreated:
			created++
		case http.StatusOK:
		default:
			t.Fatalf("send %d: %d", i, s)
		}
		if ids[i] != ids[0] {
			t.Fatalf("different answers %v", ids)
		}
	}
	if created != 1 || w.count(`SELECT count(*) FROM chat_messages WHERE chat_id = $1 AND client_message_id = $2`, chat, cid) != 1 ||
		len(keys(w.events(chat), "chat.message_created")) != 1 {
		t.Fatalf("%d created of 10 concurrent replays", created)
	}
	r := w.post("/v1/chats/"+chat+"/messages", ta, map[string]any{"text": "อีกข้อความ", "clientMessageId": cid})
	expect(t, r, http.StatusConflict, "idempotency_conflict")
	// The driver app's offline outbox replays too (the same id, unique per chat).
	dcid := uuid.NewString()
	for range 3 {
		r := w.call(w.public, http.MethodPost, "/v1/mobile/chat/messages", drv, map[string]any{"text": "ออฟไลน์", "clientMessageId": dcid})
		if r.status != http.StatusCreated && r.status != http.StatusOK {
			t.Fatalf("driver replay %d %s", r.status, r.raw)
		}
	}
	if n := w.count(`SELECT count(*) FROM chat_messages WHERE client_message_id = $1`, dcid); n != 1 {
		t.Fatalf("%d driver rows", n)
	}
	// A replay that arrives after staff closed the chat answers the stored message: no new chat, no second copy.
	expect(t, w.post("/v1/chats/"+chat+"/close", ta, nil), http.StatusOK, "")
	r = w.call(w.public, http.MethodPost, "/v1/mobile/chat/messages", drv, map[string]any{"text": "ออฟไลน์", "clientMessageId": dcid})
	expect(t, r, http.StatusOK, "")
	if str(r.data()["chatId"]) != chat {
		t.Fatalf("replay after close %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM chat_messages WHERE client_message_id = $1`, dcid); n != 1 {
		t.Fatalf("%d driver rows after close", n)
	}
	if n := w.count(`SELECT count(*) FROM chats WHERE driver_id = $1 AND status <> 'closed'`, w.dO); n != 0 {
		t.Fatalf("the replay opened %d chats", n)
	}
	expect(t, w.call(w.public, http.MethodPost, "/v1/mobile/chat/messages", drv, map[string]any{"text": "อื่น", "clientMessageId": dcid}),
		http.StatusConflict, "idempotency_conflict")
	expect(t, w.post("/v1/chats/"+chat+"/reopen", ta, nil), http.StatusOK, "")
	// Another admin replaying the same id is another message, not a replay.
	op := w.login("op.o@example.test")
	expect(t, w.post("/v1/chats/"+chat+"/messages", op, body), http.StatusConflict, "idempotency_conflict")
	for _, bad := range []map[string]any{{"text": "x"}, {"text": "x", "clientMessageId": "1"}, {"clientMessageId": uuid.NewString()}} {
		expect(t, w.post("/v1/chats/"+chat+"/messages", ta, bad), http.StatusUnprocessableEntity, "invalid_argument")
	}
}

// One open chat per driver (R14, D5): concurrent opens answer one chat; a closed chat is not reopened while another is
// open (409 already_exists); the guard holds without the 0010 index too (the advisory lock).
func TestOneOpenChatPerDriver(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o@example.test")
	open := func(driver string) []string {
		var wg sync.WaitGroup
		ids := make([]string, 8)
		for i := range 8 {
			wg.Go(func() {
				r := w.post("/v1/chats", ta, map[string]any{"driverId": driver})
				if r.status != http.StatusOK && r.status != http.StatusCreated {
					t.Errorf("open %d %s", r.status, r.raw)
				}
				ids[i] = str(r.data()["id"])
			})
		}
		wg.Wait()
		return ids
	}
	ids := open(w.dO)
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("concurrent opens answered %v", ids)
		}
	}
	if n := w.count(`SELECT count(*) FROM chats WHERE driver_id = $1 AND status <> 'closed'`, w.dO); n != 1 {
		t.Fatalf("%d open chats", n)
	}
	first := ids[0]
	if n := len(keys(w.events(first), "chat.created")); n != 1 {
		t.Fatalf("%d chat.created", n)
	}
	r := w.post("/v1/chats/"+first+"/close", ta, nil)
	expect(t, r, http.StatusOK, "")
	if r.data()["status"] != "closed" || r.data()["closedAt"] == nil {
		t.Fatalf("closed %s", r.raw)
	}
	r = w.post("/v1/chats", ta, map[string]any{"driverId": w.dO})
	expect(t, r, http.StatusCreated, "")
	second := str(r.data()["id"])
	r = w.post("/v1/chats/"+first+"/reopen", ta, nil)
	expect(t, r, http.StatusConflict, "already_exists")
	if r.detail("reason") != "open_chat_exists" || r.detail("chatId") != second {
		t.Fatalf("reopen conflict %s", r.raw)
	}
	// A staff reply to the closed chat would reopen it: refused the same way.
	expect(t, w.say(ta, first, "ยังอยู่ไหม"), http.StatusConflict, "already_exists")
	expect(t, w.post("/v1/chats/"+second+"/close", ta, nil), http.StatusOK, "")
	r = w.post("/v1/chats/"+first+"/reopen", ta, nil)
	expect(t, r, http.StatusOK, "")
	if r.data()["status"] != "open" || r.data()["closedAt"] != nil {
		t.Fatalf("reopened %s", r.raw)
	}
	// Without the index (before the P1 sign-off builds it) the advisory lock still serialises the opens.
	if _, err := w.owner.Exec(w.ctx, `DROP INDEX chats_one_open_per_driver`); err != nil {
		t.Fatal(err)
	}
	ids = open(w.dO2)
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("concurrent opens without the index answered %v", ids)
		}
	}
	if n := w.count(`SELECT count(*) FROM chats WHERE driver_id = $1 AND status <> 'closed'`, w.dO2); n != 1 {
		t.Fatalf("%d open chats without the index", n)
	}
	expect(t, w.post("/v1/chats", ta, map[string]any{"driverId": uuid.NewString()}), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.post("/v1/chats", ta, map[string]any{"driverId": "x"}), http.StatusUnprocessableEntity, "invalid_argument")
}

// Messages are keyset pages of 50, newest first (W9), with every message exactly once across the pages.
func TestMessagesKeyset(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o@example.test")
	chat := w.openChat(ta, w.dO)
	w.exec(`INSERT INTO chat_messages (chat_id, sender_user_id, sender_role, text, created_at)
		SELECT $1, $2, 'driver', 'm' || g, timestamptz '2026-10-10 00:00:00+00' + g * interval '1 minute'
		FROM generate_series(1, 120) g`, chat, w.uDO)
	// Two messages at one instant: the id breaks the tie.
	w.exec(`INSERT INTO chat_messages (chat_id, sender_user_id, sender_role, text, created_at)
		VALUES ($1, $2, 'driver', 'tie', timestamptz '2026-10-10 00:30:00+00')`, chat, w.uDO)
	seen := map[string]bool{}
	var last time.Time
	cursor, pages := "", 0
	for {
		path := "/v1/chats/" + chat + "/messages"
		if cursor != "" {
			path += "?before=" + cursor
		}
		r := w.get(path, ta)
		expect(t, r, http.StatusOK, "")
		pages++
		for _, it := range r.list() {
			m := it.(map[string]any)
			at, _ := time.Parse(time.RFC3339Nano, str(m["createdAt"]))
			if !last.IsZero() && at.After(last) {
				t.Fatalf("not newest first at %v", m)
			}
			last = at
			if seen[str(m["id"])] {
				t.Fatalf("message %s twice", m["id"])
			}
			seen[str(m["id"])] = true
		}
		if pages == 1 && (len(r.list()) != 50 || str(r.list()[0].(map[string]any)["text"]) != "m120") {
			t.Fatalf("first page %d", len(r.list()))
		}
		if cursor = r.next(); cursor == "" {
			break
		}
	}
	if len(seen) != 121 || pages != 3 {
		t.Fatalf("%d messages in %d pages", len(seen), pages)
	}
	expect(t, w.get("/v1/chats/"+chat+"/messages?before=nope", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/chats/"+chat+"/messages?limit=501", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/chats/"+chat+"/messages?after=x", ta), http.StatusBadRequest, "bad_request")
	if r := w.get("/v1/chats/"+chat+"/messages?limit=5", ta); len(r.list()) != 5 || r.next() == "" {
		t.Fatalf("limit 5: %d", len(r.list()))
	}
}

// The chat list pages by (last message, id), newest first, with every chat once.
func TestChatListKeyset(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o@example.test")
	for i := range 7 {
		d := w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile) VALUES ($1, 'self', 'D' || $2::text, 'X', '0800000000') RETURNING id::text`,
			w.O, strconv.Itoa(i))
		w.exec(`INSERT INTO chats (tenant_id, driver_id, last_message_at) VALUES ($1, $2, timestamptz '2026-10-10 00:00:00+00' + $3 * interval '1 hour')`,
			w.O, d, i%3)
	}
	d := w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile) VALUES ($1, 'self', 'Null', 'X', '0800000000') RETURNING id::text`, w.O)
	w.exec(`INSERT INTO chats (tenant_id, driver_id, last_message_at) VALUES ($1, $2, NULL)`, w.O, d)
	seen := map[string]bool{}
	cursor, pages, nullLast := "", 0, false
	for {
		path := "/v1/chats?filter=all&limit=3"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := w.get(path, ta)
		expect(t, r, http.StatusOK, "")
		pages++
		for _, it := range r.list() {
			c := it.(map[string]any)
			if seen[str(c["id"])] {
				t.Fatalf("chat twice %v", c)
			}
			seen[str(c["id"])] = true
			nullLast = c["lastMessageAt"] == nil
		}
		if cursor = r.next(); cursor == "" {
			break
		}
	}
	if len(seen) != 8 || pages != 3 || !nullLast {
		t.Fatalf("%d chats in %d pages (a chat without a message last: %v)", len(seen), pages, nullLast)
	}
	for _, q := range []string{"filter=nope", "status=gone", "driverId=x", "cursor=x"} {
		expect(t, w.get("/v1/chats?"+q, ta), http.StatusUnprocessableEntity, "invalid_argument")
	}
	if l := w.get("/v1/chats?filter=all&driverId="+d, ta).list(); len(l) != 1 {
		t.Fatalf("driver filter %v", l)
	}
	if l := w.get("/v1/chats?filter=all&status=closed", ta).list(); len(l) != 0 {
		t.Fatalf("status filter %v", l)
	}
}

// Tenancy (RLS) and capabilities: staff see the chats of their reach (contractor reach included), never another
// tenant's; chat:view / chat:send decide the verbs; drivers use only their own chat through /v1/mobile/chat*.
func TestReachAndCapabilities(t *testing.T) {
	w := newWorld(t)
	ta, taA, taC, us := w.login("ta.o@example.test"), w.login("ta.a@example.test"), w.login("ta.c@example.test"),
		w.login("us.o@example.test")
	drvO, drvC := w.login("d.o@example.test"), w.login("d.c@example.test")
	chatO := w.openChat(ta, w.dO)
	chatA := w.openChat(taA, w.dA) // A's driver: O reaches it as A's contractor
	chatC := w.openChat(taC, w.dC)
	if l := w.get("/v1/chats?filter=all", ta).list(); len(l) != 2 {
		t.Fatalf("O sees %d chats, want its own and A's", len(l))
	}
	if l := w.get("/v1/chats?filter=all", taA).list(); len(l) != 1 || str(l[0].(map[string]any)["id"]) != chatA {
		t.Fatalf("A sees %v", l)
	}
	expect(t, w.get("/v1/chats/"+chatC, ta), http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/chats/"+chatO, taC), http.StatusNotFound, "not_found")
	expect(t, w.say(taC, chatO, "x"), http.StatusNotFound, "not_found")
	expect(t, w.post("/v1/chats/"+chatO+"/assign", taC, nil), http.StatusNotFound, "not_found")
	expect(t, w.post("/v1/chats", taC, map[string]any{"driverId": w.dO}), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/chats/"+chatO+"/messages", taC), http.StatusNotFound, "not_found")
	expect(t, w.say(ta, chatA, "จาก O"), http.StatusCreated, "") // contractor reach writes too
	// The user role holds no chat capability.
	expect(t, w.get("/v1/chats", us), http.StatusForbidden, "permission_denied")
	// A driver uses its own chat only.
	expect(t, w.get("/v1/chats", drvO), http.StatusForbidden, "permission_denied")
	expect(t, w.call(w.public, http.MethodGet, "/v1/chats", drvO, nil), http.StatusNotFound, "not_found") // internal only
	expect(t, w.call(w.public, http.MethodGet, "/v1/mobile/chat", ta, nil), http.StatusForbidden, "permission_denied")
	if c := w.call(w.public, http.MethodGet, "/v1/mobile/chat", drvO, nil).data(); str(c["id"]) != chatO {
		t.Fatalf("driver O's chat %v", c)
	}
	expect(t, w.call(w.public, http.MethodGet, "/v1/mobile/chat", drvC, nil), http.StatusOK, "")
	r := w.call(w.public, http.MethodGet, "/v1/mobile/chat/messages", drvO, nil)
	if len(r.list()) != 0 {
		t.Fatalf("driver O sees %v", r.list())
	}
	// A driver without an open chat: 404 on the chat, an empty page of messages, 204 on read, 0 unread.
	drvO2 := w.login("d.o2@example.test")
	expect(t, w.call(w.public, http.MethodGet, "/v1/mobile/chat", drvO2, nil), http.StatusNotFound, "not_found")
	if r := w.call(w.public, http.MethodGet, "/v1/mobile/chat/messages", drvO2, nil); r.status != http.StatusOK || len(r.list()) != 0 {
		t.Fatalf("no chat: %d %s", r.status, r.raw)
	}
	expect(t, w.call(w.public, http.MethodPost, "/v1/mobile/chat/read", drvO2, nil), http.StatusNoContent, "")
	r = w.call(w.public, http.MethodPost, "/v1/mobile/chat", drvO2, nil)
	expect(t, r, http.StatusCreated, "")
	if r.data()["driverId"] != w.dO2 || r.data()["tenantId"] != w.O {
		t.Fatalf("driver-created chat %s", r.raw)
	}
	expect(t, w.call(w.public, http.MethodPost, "/v1/mobile/chat", drvO2, nil), http.StatusOK, "")
}

// Assignment, close, reopen and priority: the transitions, the validation and their chat.updated events.
func TestAssignCloseReopenPriority(t *testing.T) {
	w := newWorld(t)
	ta, op := w.login("ta.o@example.test"), w.login("op.o@example.test")
	chat := w.openChat(ta, w.dO)
	r := w.post("/v1/chats/"+chat+"/assign", ta, nil) // default self
	expect(t, r, http.StatusOK, "")
	if r.data()["assignedAdminUserId"] != w.taO || r.data()["status"] != "in_progress" {
		t.Fatalf("self %s", r.raw)
	}
	r = w.post("/v1/chats/"+chat+"/assign", ta, map[string]any{"adminUserId": w.osO})
	expect(t, r, http.StatusOK, "")
	if r.data()["assignedAdminUserId"] != w.osO || r.data()["assignedAdminName"] != "os.o@example.test" {
		t.Fatalf("to another staff member %s", r.raw)
	}
	for _, bad := range []string{w.taC, w.uDO, uuid.NewString()} { // another tenant's staff, a driver, nobody
		expect(t, w.post("/v1/chats/"+chat+"/assign", ta, map[string]any{"adminUserId": bad}), http.StatusUnprocessableEntity, "invalid_argument")
	}
	r = w.post("/v1/chats/"+chat+"/assign", op, map[string]any{"adminUserId": nil}) // back to the queue
	expect(t, r, http.StatusOK, "")
	if r.data()["assignedAdminUserId"] != nil || r.data()["status"] != "open" || r.data()["assignedAt"] != nil {
		t.Fatalf("returned to the queue %s", r.raw)
	}
	before := len(keys(w.events(chat), "chat.updated"))
	expect(t, w.post("/v1/chats/"+chat+"/assign", op, map[string]any{"adminUserId": nil}), http.StatusOK, "")
	if n := len(keys(w.events(chat), "chat.updated")); n != before {
		t.Fatal("a change of nothing emitted chat.updated")
	}
	r = w.call(w.internal, http.MethodPatch, "/v1/chats/"+chat, ta, map[string]any{"priority": "urgent"})
	expect(t, r, http.StatusOK, "")
	if r.data()["priority"] != "urgent" {
		t.Fatalf("priority %s", r.raw)
	}
	for _, bad := range []map[string]any{{}, {"priority": nil}, {"priority": "high"}} {
		expect(t, w.call(w.internal, http.MethodPatch, "/v1/chats/"+chat, ta, bad), http.StatusUnprocessableEntity, "invalid_argument")
	}
	expect(t, w.post("/v1/chats/"+chat+"/close", ta, nil), http.StatusOK, "")
	expect(t, w.post("/v1/chats/"+chat+"/close", ta, nil), http.StatusOK, "")
	r = w.post("/v1/chats/"+chat+"/assign", ta, nil)
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "chat_closed" {
		t.Fatalf("assign closed %s", r.raw)
	}
	// A staff reply reopens a closed chat (in_progress, assigned to the replier when unassigned), as the legacy room did.
	r = w.say(op, chat, "เปิดใหม่")
	expect(t, r, http.StatusCreated, "")
	if c := w.get("/v1/chats/"+chat, ta).data(); c["status"] != "in_progress" || c["closedAt"] != nil || c["assignedAdminUserId"] != w.opO {
		t.Fatalf("reopened by a reply %v", c)
	}
	// The driver cannot write into a closed chat it does not hold: its message opens a new chat.
	expect(t, w.post("/v1/chats/"+chat+"/close", ta, nil), http.StatusOK, "")
	drv := w.login("d.o@example.test")
	r = w.driverSays(drv, "แชทใหม่")
	expect(t, r, http.StatusCreated, "")
	if str(r.data()["chatId"]) == chat {
		t.Fatalf("the driver wrote into the closed chat")
	}
	expect(t, w.post("/v1/chats/"+uuid.NewString()+"/close", ta, nil), http.StatusNotFound, "not_found")
	expect(t, w.post("/v1/chats/nope/close", ta, nil), http.StatusNotFound, "not_found")
	if l := w.get("/v1/chats?filter=all&status=closed", ta).list(); len(l) != 1 {
		t.Fatalf("closed %v", l)
	}
	ev := keys(w.events(chat), "chat.updated")
	if len(ev) < 5 || !slices.Contains(anyStrings(ev[len(ev)-1].Payload["changed"]), "status") {
		t.Fatalf("chat.updated %+v", ev)
	}
}

// The realtime topics of the events (Appendix B §B.4.2): the whole message on chat:{id}, the tenant's chat list, the
// driver's own user topic.
func TestMessageEventTopics(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o@example.test")
	chat := w.openChat(ta, w.dO)
	expect(t, w.say(ta, chat, "สวัสดี"), http.StatusCreated, "")
	ev := keys(w.events(chat), "chat.message_created")
	want := []string{"chat:" + chat, "tenant:" + w.O + ":chats", "user:" + w.uDO}
	if len(ev) != 1 || !slices.Equal(ev[0].Topics, want) {
		t.Fatalf("topics %+v", ev)
	}
	p := ev[0].Payload
	if p["text"] != "สวัสดี" || p["chatId"] != chat || p["senderRole"] != "admin" || p["senderUserId"] != w.taO ||
		p["clientMessageId"] == nil || p["entity"] != "chat_message" {
		t.Fatalf("payload %v", p)
	}
	if ev[0].Headers["requestId"] == "" {
		t.Fatal("no request id")
	}
	// chat:{id} is the driver's topic too and a driver never reads staff users (RLS; the REST page answers a driver
	// reader senderName null): a staff message's event names no sender, a driver message's names the driver.
	if v, ok := p["senderName"]; !ok || v != nil {
		t.Fatalf("a staff message's event names its sender: %v", p)
	}
	expect(t, w.driverSays(w.login("d.o@example.test"), "ครับ"), http.StatusCreated, "")
	ev = keys(w.events(chat), "chat.message_created")
	if len(ev) != 2 || ev[1].Payload["senderName"] != "d.o@example.test" {
		t.Fatalf("driver message payload %v", ev[len(ev)-1].Payload)
	}
}

// A driver's send that loses a race with staff closing its open chat (closed between the lookup of the open chat and
// the send's transaction) lands in a new chat (201), as a fresh send would, instead of 409 chat_closed.
func TestMySendAfterAConcurrentClose(t *testing.T) {
	w := newWorld(t)
	ta, drv := w.login("ta.o@example.test"), w.login("d.o@example.test")
	chat := w.openChat(ta, w.dO)
	// MySend's transactions: the replay lookup (1), the open (2), the send (3).
	w.hook.set(func(n int) error {
		if n != 3 {
			return nil
		}
		_, err := w.etl.Exec(w.ctx, `UPDATE chats SET status = 'closed', closed_at = now() WHERE id = $1`, chat)
		return err
	})
	r := w.driverSays(drv, "ถึงแล้วครับ")
	w.hook.set(nil)
	expect(t, r, http.StatusCreated, "")
	if str(r.data()["chatId"]) == chat {
		t.Fatalf("the message went to the closed chat %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM chats WHERE driver_id = $1 AND status <> 'closed'`, w.dO); n != 1 {
		t.Fatalf("%d open chats", n)
	}
	if n := w.count(`SELECT count(*) FROM chat_messages m JOIN chats c ON c.id = m.chat_id WHERE c.driver_id = $1`, w.dO); n != 1 {
		t.Fatalf("%d messages", n)
	}
}

// An assignee may read and answer the chat: a staff member who reaches its tenant (its own, or the contractor it works
// for) with chat:view and chat:send in the role set of that membership, overrides included; a member of the user
// role (no chat capability) or of a role whose override removes chat:view is 422 adminUserId not_found.
func TestAssignNeedsChatCapabilities(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o@example.test")
	chat := w.openChat(ta, w.dO)
	r := w.post("/v1/chats/"+chat+"/assign", ta, map[string]any{"adminUserId": w.usO})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.post("/v1/chats/"+chat+"/assign", ta, map[string]any{"adminUserId": w.osO}), http.StatusOK, "")
	w.exec(`INSERT INTO role_capability_overrides (tenant_id, role, capability, allowed, updated_by)
		VALUES ($1, 'operation_staff', 'chat:view', false, $2)`, w.O, w.taO)
	if err := w.rdb.Incr(w.ctx, w.ks.RBACVersion()).Err(); err != nil {
		t.Fatal(err)
	}
	expect(t, w.post("/v1/chats/"+chat+"/assign", ta, map[string]any{"adminUserId": w.osO}), http.StatusUnprocessableEntity, "invalid_argument")
	// The carrier A works for O: O's operator handles A's driver chat.
	chatA := w.openChat(ta, w.dA)
	r = w.post("/v1/chats/"+chatA+"/assign", ta, map[string]any{"adminUserId": w.opO})
	expect(t, r, http.StatusOK, "")
	if r.data()["assignedAdminUserId"] != w.opO {
		t.Fatalf("contractor reach %s", r.raw)
	}
}

// A chat image: presigned for the chat (purpose chat_image), committed with the message in its transaction, signed
// in the answer and in the room's pages; an image alone previews as "[Image]".
func TestImageMessage(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o@example.test")
	chat := w.openChat(ta, w.dO)
	key := w.upload(ta, chat)
	r := w.post("/v1/chats/"+chat+"/messages", ta, map[string]any{"imageKey": key, "clientMessageId": uuid.NewString()})
	expect(t, r, http.StatusCreated, "")
	if r.data()["imageFileId"] == nil || str(r.data()["imageUrl"]) == "" || r.data()["text"] != nil {
		t.Fatalf("image message %s", r.raw)
	}
	if c := w.get("/v1/chats/"+chat, ta).data(); c["lastMessagePreview"] != "[Image]" {
		t.Fatalf("preview %v", c["lastMessagePreview"])
	}
	if n := w.count(`SELECT count(*) FROM file_objects WHERE object_key = $1 AND status = 'committed' AND owner_kind = 'chat'
		AND owner_id = $2`, key, chat); n != 1 {
		t.Fatal("the image is not committed to the chat")
	}
	if l := w.get("/v1/chats/"+chat+"/messages", ta).list(); str(l[0].(map[string]any)["imageUrl"]) == "" {
		t.Fatalf("page %v", l)
	}
	r = w.post("/v1/chats/"+chat+"/messages", ta, map[string]any{"imageKey": "chats/" + chat + "/0.jpg", "clientMessageId": uuid.NewString()})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if n := w.count(`SELECT count(*) FROM chat_messages WHERE chat_id = $1`, chat); n != 1 {
		t.Fatalf("a refused image left %d messages", n)
	}
}

// comms Firestore-owned without a Firestore writer: every chat write is 503 write_back_unconfigured and PostgreSQL is
// never written in Firestore's place; the reads still serve PostgreSQL (the mirror).
func TestWritesUnconfiguredWhileFirestoreOwned(t *testing.T) {
	w := newWorld(t, worldOpts{notPGOwned: true})
	ta, drv := w.login("ta.o@example.test"), w.login("d.o@example.test")
	chat := w.id(`INSERT INTO chats (tenant_id, driver_id) VALUES ($1, $2) RETURNING id::text`, w.O, w.dO)
	for _, r := range []resp{
		w.post("/v1/chats", ta, map[string]any{"driverId": w.dO2}),
		w.say(ta, chat, "x"),
		w.post("/v1/chats/"+chat+"/read", ta, nil),
		w.post("/v1/chats/"+chat+"/assign", ta, nil),
		w.post("/v1/chats/"+chat+"/close", ta, nil),
		w.driverSays(drv, "x"),
	} {
		expect(t, r, http.StatusServiceUnavailable, "unavailable")
		if r.detail("reason") != "write_back_unconfigured" {
			t.Fatalf("reason %s", r.raw)
		}
	}
	if n := w.count(`SELECT (SELECT count(*) FROM chat_messages) + (SELECT count(*) FROM chat_read_state) + (SELECT count(*) FROM chats)`); n != 1 {
		t.Fatalf("PostgreSQL was written: %d rows", n)
	}
	expect(t, w.get("/v1/chats/"+chat, ta), http.StatusOK, "")
	expect(t, w.get("/v1/chats?filter=queue", ta), http.StatusOK, "")
}

func anyStrings(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		out = append(out, str(x))
	}
	return out
}

// upload presigns a chat image for the chat, sends the bytes to the signed local URL and returns the key.
func (w *world) upload(bearer, chat string) string {
	w.t.Helper()
	body := append([]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), bytes.Repeat([]byte{7}, 64)...)
	r := w.post("/v1/uploads/presign", bearer, map[string]any{"purpose": "chat_image", "entityId": chat, "contentType": "image/jpeg",
		"sizeBytes": len(body), "fileName": "photo"})
	expect(w.t, r, http.StatusOK, "")
	key, u := str(r.data()["key"]), str(r.data()["url"])
	put, err := http.NewRequest(http.MethodPut, w.internal+u, bytes.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	for k, v := range r.data()["headers"].(map[string]any) {
		put.Header.Set(k, v.(string))
	}
	res, err := http.DefaultClient.Do(put)
	if err != nil {
		w.t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		w.t.Fatalf("upload %s: %d", key, res.StatusCode)
	}
	return key
}
