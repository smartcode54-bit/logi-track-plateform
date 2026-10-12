//go:build integration

// The chat write-back (comms Firestore-owned) over writeback.Memory without a mirror acknowledgement: every write
// answers 202 and PostgreSQL holds only what a test writes as the mirror, so these tests exercise the window before the
// mirror loads a write (T47 review panel).
package chats_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// mirrorChat plays the mirror for an open chat of driver: its Firestore document (legacy shape, driverId the driver's
// Firebase uid) and its PostgreSQL row.
func (w *world) mirrorChat(driver, ref, doc string) string {
	w.t.Helper()
	at := time.Now().UTC()
	w.fs.Put("chats", doc, map[string]any{"driverId": ref, "driverDisplayName": "Driver", "status": "open",
		"assignedAdminId": nil, "assignedAt": nil, "closedAt": nil, "priority": "normal", "lastReadByAdmin": map[string]any{},
		"lastMessage": "", "lastMessageAt": at, "lastMessageBy": "", "createdAt": at, "updatedAt": at}, at)
	return w.id(`INSERT INTO chats (tenant_id, driver_id, legacy_doc_id, status)
		SELECT tenant_id, id, $2, 'open' FROM drivers WHERE id = $1 RETURNING id::text`, driver, doc)
}

// raw is call without t.Fatal, for requests sent from several goroutines: the status (-1 when the request failed) and
// the data object.
func (w *world) raw(base, method, path, bearer string, body any) (int, map[string]any) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(method, base+path, bytes.NewReader(b))
	if err != nil {
		return -1, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1, nil
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	var out struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out.Data
}

// messageWrites counts the accepted writes of message documents under one chat document.
func (w *world) messageWrites(chatDoc string) int {
	n := 0
	for _, wr := range w.fs.Writes() {
		if wr.Collection == "chats/"+chatDoc+"/messages" {
			n++
		}
	}
	return n
}

// AC (write-back): a staff message pushes exactly one FCM message per device, whatever the replays of its
// clientMessageId before the mirror loaded it. The replay is answered from the stored message document (one commit
// wrote it with its chat update), writes nothing and appends its push event again, and notify.fcm sends the message
// once (idem:push:chat); concurrent replays too. Another message under the same id is 409 idempotency_conflict and
// changes neither the chat document nor the outbox.
func TestWriteBackReplayBeforeTheMirror(t *testing.T) {
	w := newWorld(t, worldOpts{writeBack: true})
	ta := w.login("ta.o@example.test")
	chat := w.mirrorChat(w.dO, "uid_"+w.uDO, "fsChatA")
	cid := uuid.NewString()
	body := map[string]any{"text": "ไปรับของ", "clientMessageId": cid}
	r := w.post("/v1/chats/"+chat+"/messages", ta, body)
	expect(t, r, http.StatusAccepted, "")
	if r.data()["docId"] != cid || r.data()["pending"] != true || r.data()["collection"] != "chats/fsChatA/messages" {
		t.Fatalf("send %s", r.raw)
	}
	cd, _ := w.fs.Doc("chats", "fsChatA")
	if cd["lastMessage"] != "ไปรับของ" || cd["assignedAdminId"] != w.taO || cd["status"] != "in_progress" {
		t.Fatalf("chat document %v", cd)
	}
	writes := len(w.fs.Writes())

	r = w.post("/v1/chats/"+chat+"/messages", ta, body)
	expect(t, r, http.StatusAccepted, "")
	if r.data()["docId"] != cid {
		t.Fatalf("replay %s", r.raw)
	}
	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Go(func() { codes[i], _ = w.raw(w.internal, http.MethodPost, "/v1/chats/"+chat+"/messages", ta, body) })
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusAccepted {
			t.Fatalf("concurrent replay %d: %d", i, c)
		}
	}
	if n := len(w.fs.Writes()); n != writes {
		t.Fatalf("the replays wrote %d documents", n-writes)
	}

	r = w.post("/v1/chats/"+chat+"/messages", ta, map[string]any{"text": "อย่างอื่น", "clientMessageId": cid})
	expect(t, r, http.StatusConflict, "idempotency_conflict")
	if cd, _ := w.fs.Doc("chats", "fsChatA"); cd["lastMessage"] != "ไปรับของ" || len(w.fs.Writes()) != writes {
		t.Fatalf("a conflicting replay changed the chat: %v", cd)
	}
	m, _ := w.fs.Doc("chats/fsChatA/messages", cid)
	if m["text"] != "ไปรับของ" {
		t.Fatalf("message document %v", m)
	}

	ev := keys(w.events(chat), "chat.message_created")
	if len(ev) != 7 {
		t.Fatalf("%d push events, want the send's and one per replay", len(ev))
	}
	for _, e := range ev {
		if e.Headers["origin"] != "writeback" || len(e.Topics) != 0 || e.Payload["messageRef"] != cid {
			t.Fatalf("event %+v", e)
		}
	}
	p := w.pusher(false)
	for _, e := range ev {
		w.deliver(p, e)
	}
	if got := p.fcm.SentTo("tok-" + w.uDO); len(p.fcm.Sent()) != 1 || len(got) != 1 || got[0].Message.Data["messageId"] != cid {
		t.Fatalf("%d pushes, want exactly one to the driver's device", len(p.fcm.Sent()))
	}
	if n := w.count(`SELECT count(*) FROM notification_deliveries WHERE kind = 'chat' AND status = 'deduplicated'`); n != 6 {
		t.Fatalf("%d deduplicated delivery rows", n)
	}
}

// A write-back message whose push event could not be appended after the commit (503: the message is stored) still
// pushes, once: the client's retry before the mirror loaded it is answered from the stored document, the retry after
// it from the row, and each appends the event again, which notify.fcm sends once.
func TestWriteBackPushAfterALostAppend(t *testing.T) {
	w := newWorld(t, worldOpts{writeBack: true})
	ta := w.login("ta.o@example.test")
	chat := w.mirrorChat(w.dO, "uid_"+w.uDO, "fsChatB")
	cid := uuid.NewString()
	body := map[string]any{"text": "ไปรับของ", "clientMessageId": cid}
	// The send's transactions: the read of the mirrored chat (1), the push event (2).
	w.hook.set(func(n int) error {
		if n == 2 {
			return errors.New("connection reset by peer")
		}
		return nil
	})
	r := w.post("/v1/chats/"+chat+"/messages", ta, body)
	w.hook.set(nil)
	expect(t, r, http.StatusServiceUnavailable, "unavailable")
	if r.detail("reason") != "write_back" {
		t.Fatalf("reason %s", r.raw)
	}
	if _, ok := w.fs.Doc("chats/fsChatB/messages", cid); !ok {
		t.Fatal("the message document is not stored")
	}
	if n := len(keys(w.events(chat), "chat.message_created")); n != 0 {
		t.Fatalf("%d events after the failed append", n)
	}
	p := w.pusher(false)

	// The retry before the mirror loaded the message: the stored document answers; the event is appended.
	r = w.post("/v1/chats/"+chat+"/messages", ta, body)
	expect(t, r, http.StatusAccepted, "")
	ev := keys(w.events(chat), "chat.message_created")
	if len(ev) != 1 {
		t.Fatalf("%d events after the retry", len(ev))
	}
	w.deliver(p, ev[0])
	if n := len(p.fcm.SentTo("tok-" + w.uDO)); n != 1 {
		t.Fatalf("%d pushes after the retry", n)
	}

	// The mirror loads it; a later retry is answered from the row, and its event sends nothing more.
	w.exec(`INSERT INTO chat_messages (chat_id, sender_user_id, sender_role, text, legacy_doc_id, client_message_id)
		VALUES ($1, $2, 'admin', 'ไปรับของ', $3::text, $3::uuid)`, chat, w.taO, cid)
	r = w.post("/v1/chats/"+chat+"/messages", ta, body)
	expect(t, r, http.StatusOK, "")
	if r.data()["clientMessageId"] != cid || r.data()["legacyDocId"] != cid {
		t.Fatalf("answered from the row %s", r.raw)
	}
	ev = keys(w.events(chat), "chat.message_created")
	if len(ev) != 2 {
		t.Fatalf("%d events after the mirrored retry", len(ev))
	}
	w.deliver(p, ev[1])
	if n := len(p.fcm.Sent()); n != 1 {
		t.Fatalf("%d pushes in all, want one", n)
	}

	// Lost before any event, a push is still sent once a retry reaches the row: a fresh message, its append failed,
	// mirrored, then retried.
	cid2 := uuid.NewString()
	w.hook.set(func(n int) error {
		if n == 2 {
			return errors.New("connection reset by peer")
		}
		return nil
	})
	expect(t, w.post("/v1/chats/"+chat+"/messages", ta, map[string]any{"text": "อีกเรื่อง", "clientMessageId": cid2}),
		http.StatusServiceUnavailable, "unavailable")
	w.hook.set(nil)
	w.exec(`INSERT INTO chat_messages (chat_id, sender_user_id, sender_role, text, legacy_doc_id, client_message_id)
		VALUES ($1, $2, 'admin', 'อีกเรื่อง', $3::text, $3::uuid)`, chat, w.taO, cid2)
	expect(t, w.post("/v1/chats/"+chat+"/messages", ta, map[string]any{"text": "อีกเรื่อง", "clientMessageId": cid2}),
		http.StatusOK, "")
	ev = keys(w.events(chat), "chat.message_created")
	if len(ev) != 3 || ev[2].Payload["messageRef"] != cid2 {
		t.Fatalf("events %+v", ev)
	}
	w.deliver(p, ev[2])
	if got := p.fcm.SentTo("tok-" + w.uDO); len(got) != 2 || got[1].Message.Data["messageId"] != cid2 {
		t.Fatalf("the second message's push %+v", got)
	}
}

// A message and its chat update are one Firestore commit: a failed commit leaves neither document changed and appends
// no event; the retry with the same clientMessageId writes both, auto-assigns the replier and appends one push event.
// A cleared assignment is a stored null (the legacy queue reads where('assignedAdminId', '==', null)).
func TestWriteBackSendIsOneCommit(t *testing.T) {
	w := newWorld(t, worldOpts{writeBack: true})
	ta := w.login("ta.o@example.test")
	chat := w.mirrorChat(w.dO, "uid_"+w.uDO, "fsChatC")
	cid := uuid.NewString()
	body := map[string]any{"text": "สวัสดีครับ", "clientMessageId": cid}
	w.fs.Fail(errors.New("firestore unavailable"))
	r := w.post("/v1/chats/"+chat+"/messages", ta, body)
	w.fs.Fail(nil)
	expect(t, r, http.StatusServiceUnavailable, "unavailable")
	if r.detail("reason") != "write_back" {
		t.Fatalf("reason %s", r.raw)
	}
	if _, ok := w.fs.Doc("chats/fsChatC/messages", cid); ok {
		t.Fatal("a failed commit stored the message")
	}
	if cd, _ := w.fs.Doc("chats", "fsChatC"); cd["lastMessage"] != "" || cd["status"] != "open" || cd["assignedAdminId"] != nil {
		t.Fatalf("a failed commit changed the chat %v", cd)
	}
	if n := len(w.events(chat)); n != 0 {
		t.Fatalf("%d events after a failed commit", n)
	}

	r = w.post("/v1/chats/"+chat+"/messages", ta, body)
	expect(t, r, http.StatusAccepted, "")
	if _, ok := w.fs.Doc("chats/fsChatC/messages", cid); !ok {
		t.Fatal("the retry stored no message")
	}
	cd, _ := w.fs.Doc("chats", "fsChatC")
	reads, _ := cd["lastReadByAdmin"].(map[string]any)
	if cd["lastMessage"] != "สวัสดีครับ" || cd["lastMessageBy"] != w.taO || cd["assignedAdminId"] != w.taO ||
		cd["status"] != "in_progress" || reads[w.taO] == nil {
		t.Fatalf("chat document %v", cd)
	}
	ev := keys(w.events(chat), "chat.message_created")
	if len(ev) != 1 || ev[0].Payload["assignedAdminUserId"] != w.taO {
		t.Fatalf("events %+v", ev)
	}

	// The mirror applies the reply; the return to the queue stores nulls.
	w.exec(`UPDATE chats SET status = 'in_progress', assigned_admin_user_id = $2, assigned_at = now() WHERE id = $1`, chat, w.taO)
	r = w.post("/v1/chats/"+chat+"/assign", ta, map[string]any{"adminUserId": nil})
	expect(t, r, http.StatusAccepted, "")
	cd, _ = w.fs.Doc("chats", "fsChatC")
	for _, k := range []string{"assignedAdminId", "assignedAt"} {
		if v, ok := cd[k]; !ok || v != nil {
			t.Fatalf("%s after the return to the queue: %v (present %v), want a stored null", k, v, ok)
		}
	}
	if cd["status"] != "open" {
		t.Fatalf("status %v", cd["status"])
	}
	// A chat whose Firestore document is gone (deleted there, the mirror behind): the commit is refused as a whole,
	// 409 document_changed, and no message document is left behind.
	gone := w.id(`INSERT INTO chats (tenant_id, driver_id, legacy_doc_id, status)
		SELECT tenant_id, id, 'fsGone', 'open' FROM drivers WHERE id = $1 RETURNING id::text`, w.dO2)
	goneID := uuid.NewString()
	r = w.post("/v1/chats/"+gone+"/messages", ta, map[string]any{"text": "x", "clientMessageId": goneID})
	expect(t, r, http.StatusConflict, "failed_precondition")
	if r.detail("reason") != "document_changed" {
		t.Fatalf("reason %s", r.raw)
	}
	if _, ok := w.fs.Doc("chats/fsGone/messages", goneID); ok {
		t.Fatal("a refused commit stored the message")
	}
}

// One open chat per driver on the write-back path too: the driver's first message, its replays and a second message
// land in one new chat document while the mirror has not applied it (no 503 retry loop), and concurrent opens of one
// driver by staff and by the driver create one chat document.
func TestWriteBackOneChatPerDriver(t *testing.T) {
	w := newWorld(t, worldOpts{writeBack: true})
	ta, drv, drv2 := w.login("ta.o@example.test"), w.login("d.o@example.test"), w.login("d.o2@example.test")
	cid := uuid.NewString()
	var collection string
	for i := range 3 {
		r := w.call(w.public, http.MethodPost, "/v1/mobile/chat/messages", drv2, map[string]any{"text": "สวัสดี", "clientMessageId": cid})
		expect(t, r, http.StatusAccepted, "")
		if i == 0 {
			collection = str(r.data()["collection"])
		} else if str(r.data()["collection"]) != collection {
			t.Fatalf("attempt %d wrote to %s, the first to %s", i, r.data()["collection"], collection)
		}
	}
	r := w.call(w.public, http.MethodPost, "/v1/mobile/chat/messages", drv2, map[string]any{"text": "อยู่ไหม", "clientMessageId": uuid.NewString()})
	expect(t, r, http.StatusAccepted, "")
	if str(r.data()["collection"]) != collection {
		t.Fatalf("the second message went to %s", r.data()["collection"])
	}
	docs, err := w.fs.Find(w.ctx, "chats", "driverId", "uid_"+w.uDO2)
	if err != nil || len(docs) != 1 {
		t.Fatalf("%d chat documents for one driver (%v)", len(docs), err)
	}
	if collection != "chats/"+docs[0].ID+"/messages" || w.messageWrites(docs[0].ID) != 2 {
		t.Fatalf("messages of %s: %d", docs[0].ID, w.messageWrites(docs[0].ID))
	}
	if d := docs[0].Fields; d["lastMessage"] != "อยู่ไหม" || d["status"] != "open" {
		t.Fatalf("chat document %v", d)
	}
	if v, ok := docs[0].Fields["assignedAdminId"]; !ok || v != nil {
		t.Fatalf("a new chat's assignedAdminId %v (present %v), want a stored null", v, ok)
	}

	// Eight staff opens and the driver's own, at once.
	var wg sync.WaitGroup
	type answer struct {
		status int
		doc    string
	}
	answers := make([]answer, 9)
	for i := range answers {
		wg.Go(func() {
			var status int
			var data map[string]any
			if i == 8 {
				status, data = w.raw(w.public, http.MethodPost, "/v1/mobile/chat", drv, map[string]any{})
			} else {
				status, data = w.raw(w.internal, http.MethodPost, "/v1/chats", ta, map[string]any{"driverId": w.dO})
			}
			doc, _ := data["docId"].(string)
			answers[i] = answer{status, doc}
		})
	}
	wg.Wait()
	for i, a := range answers {
		if a.status != http.StatusAccepted || a.doc == "" || a.doc != answers[0].doc {
			t.Fatalf("open %d: %+v (first %+v)", i, a, answers[0])
		}
	}
	if docs, err := w.fs.Find(w.ctx, "chats", "driverId", "uid_"+w.uDO); err != nil || len(docs) != 1 {
		t.Fatalf("%d chat documents for one driver after concurrent opens (%v)", len(docs), err)
	}
	// Once the mirror holds it, an open answers its row.
	w.exec(`INSERT INTO chats (id, tenant_id, driver_id, legacy_doc_id, status) SELECT $2::uuid, tenant_id, id, $2, 'open'
		FROM drivers WHERE id = $1`, w.dO, answers[0].doc)
	r = w.post("/v1/chats", ta, map[string]any{"driverId": w.dO})
	expect(t, r, http.StatusOK, "")
	if str(r.data()["id"]) != answers[0].doc {
		t.Fatalf("open after the mirror %s", r.raw)
	}
	if !strings.HasPrefix(collection, "chats/") {
		t.Fatalf("collection %q", collection)
	}
}
