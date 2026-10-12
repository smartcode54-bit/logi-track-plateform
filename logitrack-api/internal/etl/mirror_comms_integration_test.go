//go:build integration

package etl

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// The chat events of the mirror (T47): what a 3.x app or the legacy web writes in Firestore reaches the staff console
// and the driver's room through the events the mirror appends (origin etl.sync, so notify.fcm pushes nothing): a new
// chat, a changed assignment or status, a read state that moved, a new message (the whole message on chat:{id}); a
// re-applied unchanged version emits nothing. The message's clientMessageId (the Go write-back's) is loaded.
func TestMirrorChatEvents(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	tenant := h.id(`SELECT tenant_id FROM chats WHERE legacy_doc_id = 'chat_1'`).String()
	chat := h.id(`SELECT id FROM chats WHERE legacy_doc_id = 'chat_1'`).String()
	mark := h.lastEvent()

	// A message from the app.
	at := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	cid := uuid.NewString()
	h.fs.Set("chats/chat_1/messages/m_new", map[string]any{"senderId": "uid_own",
		"senderRole": "driver", "text": "ถึงแล้ว", "createdAt": dump.Timestamp{Time: at}, "clientMessageId": cid})
	h.sweep(p)
	evs := h.eventsAfter(mark)
	if len(evs) != 1 || evs[0].key != "chat.message_created" || evs[0].origin != "etl.sync" || evs[0].aggregate != chat {
		t.Fatalf("events %v", keys(evs))
	}
	if !slices.Contains(evs[0].topics, "chat:"+chat) || !slices.Contains(evs[0].topics, "tenant:"+tenant+":chats") {
		t.Fatalf("topics %v", evs[0].topics)
	}
	if pl := evs[0].payload; pl["text"] != "ถึงแล้ว" || pl["senderRole"] != "driver" || pl["clientMessageId"] != cid ||
		pl["legacyDocId"] != "m_new" || pl["source"] != "mirror" {
		t.Fatalf("payload %v", pl)
	}
	if got := h.scalar(`SELECT client_message_id::text FROM chat_messages WHERE legacy_doc_id = 'm_new'`); got != cid {
		t.Fatalf("client_message_id %v", got)
	}
	mark = h.lastEvent()
	h.sweep(p) // nothing changed
	if evs := h.eventsAfter(mark); len(evs) != 0 {
		t.Fatalf("a re-applied version emitted %v", keys(evs))
	}

	// A staff message: chat:{id} is the driver's topic too, so the event names no staff user (senderName null), as the
	// REST messages page answers a driver reader.
	h.exec(`INSERT INTO users (email, display_name, legacy_auth_uid) VALUES ('sn.staff@example.test', 'Staff Name', 'uid_sn_staff')`)
	h.fs.Set("chats/chat_1/messages/m_staff", map[string]any{"senderId": "uid_sn_staff", "senderRole": "admin",
		"text": "รับทราบ", "createdAt": dump.Timestamp{Time: at.Add(time.Minute)}})
	h.sweep(p)
	evs = h.eventsAfter(mark)
	if len(evs) != 1 || evs[0].payload["senderUserId"] == nil {
		t.Fatalf("staff message events %v", evs)
	}
	if v, ok := evs[0].payload["senderName"]; !ok || v != nil {
		t.Fatalf("a staff message's event names its sender: %v", evs[0].payload)
	}
	mark = h.lastEvent()

	// The legacy room takes the chat in progress and reads it; the app reads it too (the fixture's admin uid matches no
	// user, so its assignment was never loaded).
	fields := h.doc("chats/chat_1")
	fields["status"] = "in_progress"
	fields["assignedAdminId"] = nil
	fields["lastReadByAdmin"] = map[string]any{"uid_admin": dump.Timestamp{Time: at.Add(time.Hour)}}
	fields["lastReadByDriver"] = dump.Timestamp{Time: at.Add(2 * time.Hour)}
	delete(fields, "assignedAdminId")
	h.fs.Set("chats/chat_1", fields)
	h.sweep(p)
	evs = h.eventsAfter(mark)
	got := keys(evs)
	slices.Sort(got)
	if !slices.Contains(got, "chat.updated") {
		t.Fatalf("events %v", got)
	}
	for _, e := range evs {
		switch e.key {
		case "chat.updated":
			ch := anyList(e.payload["changed"])
			if !slices.Contains(ch, "status") || e.payload["status"] != "in_progress" ||
				!slices.Equal(e.topics, []string{"tenant:" + tenant + ":chats"}) {
				t.Fatalf("chat.updated %v %v", e.payload, e.topics)
			}
		case "chat.read":
			if !slices.Equal(e.topics, []string{"chat:" + chat}) || e.payload["userId"] == nil {
				t.Fatalf("chat.read %v", e)
			}
		default:
			t.Fatalf("unexpected %s", e.key)
		}
	}
	// A new chat document: chat.created.
	mark = h.lastEvent()
	h.fs.Set("chats/chat_new", map[string]any{"driverId": "uid_alpha", "status": "open",
		"createdAt": dump.Timestamp{Time: at}, "updatedAt": dump.Timestamp{Time: at}})
	h.sweep(p)
	evs = h.eventsAfter(mark)
	if len(evs) != 1 || evs[0].key != "chat.created" || evs[0].payload["legacyDocId"] != "chat_new" {
		t.Fatalf("new chat events %v", keys(evs))
	}
}

// A clientMessageId is unique per chat (chat_messages_client_id). Only a document written by hand or by a modified
// client can repeat another message's id (the write-back's is its document id): that one loads without it, with a
// duplicate_natural_key finding, and neither the listener poll nor the sweep fails, so every other collection keeps
// mirroring (T47 review).
func TestDuplicateClientMessageIDLoadsWithoutIt(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	cursors := h.cursors(p)
	now := h.readTime().Add(time.Second)
	cid := uuid.NewString()
	for _, id := range []string{"m_dup_a", "m_dup_b"} {
		h.fs.Set("chats/chat_1/messages/"+id, map[string]any{"senderId": "uid_own", "senderRole": "driver", "text": id,
			"createdAt": dump.Timestamp{Time: now}, "clientMessageId": cid})
	}
	h.fs.Set("trip_records/DUPCLIENT", map[string]any{"status": "in_transit", "jobType": "first_mile", "taskId": "task_1",
		"driverId": "uid_own", "createdAt": ts(now), "updatedAt": ts(now)})
	rep, _ := h.poll(h.client, p, cursors)
	if applied(rep, "messages") != 2 || applied(rep, "trip_records") != 1 {
		t.Fatalf("listener poll: %+v", rep.Collections)
	}
	check := func(when string) {
		t.Helper()
		if n := h.scalar(`SELECT count(*) FROM chat_messages WHERE legacy_doc_id IN ('m_dup_a', 'm_dup_b')`); n != int64(2) {
			t.Fatalf("%s: %v messages", when, n)
		}
		if n := h.scalar(`SELECT count(*) FROM chat_messages WHERE client_message_id = $1`, cid); n != int64(1) {
			t.Fatalf("%s: %v messages hold the client message id", when, n)
		}
		if v := h.scalar(`SELECT q.field || ' ' || q.reason_code FROM chat_messages m JOIN etl.quarantine q
			ON q.doc_path = 'chats/chat_1/messages/' || m.legacy_doc_id AND q.resolved_at IS NULL
			WHERE m.legacy_doc_id IN ('m_dup_a', 'm_dup_b') AND m.client_message_id IS NULL`); v != "clientMessageId duplicate_natural_key" {
			t.Fatalf("%s: finding %v", when, v)
		}
	}
	check("poll")
	// The sweep re-applies both documents and still commits.
	h.fs.Set("trip_records/DUPCLIENT2", map[string]any{"status": "in_transit", "jobType": "first_mile", "taskId": "task_1",
		"driverId": "uid_own", "createdAt": ts(now), "updatedAt": ts(now)})
	h.sweep(p)
	if n := h.scalar(`SELECT count(*) FROM trip_records WHERE legacy_doc_id IN ('DUPCLIENT', 'DUPCLIENT2')`); n != int64(2) {
		t.Fatalf("the trips beside the duplicate: %v", n)
	}
	check("sweep")
}

// failingGroups is a ChangeSource whose collection-group reads fail, as on a project without the collection-group index.
type failingGroups struct{ ChangeSource }

func (failingGroups) ChangedGroup(context.Context, string, string, time.Time, time.Time) ([]dump.Doc, time.Time, error) {
	return nil, time.Time{}, errors.New("FAILED_PRECONDITION: the query requires an index")
}

// The listeners poll the chats and, through a collection-group read, their messages (T47). A group read that fails (no
// collection-group index on the project) leaves that collection to the sweep and keeps polling the others; a source
// without group reads skips it the same way.
func TestListenersPollChatsAndMessages(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	h.sweep(p)
	cursors := h.cursors(p)
	for _, c := range []string{"chats", "messages"} {
		if _, ok := cursors[c]; !ok {
			t.Fatalf("no cursor for %s: %v", c, cursors)
		}
	}
	now := h.readTime().Add(time.Second)
	h.fs.Set("chats/chat_1/messages/m_poll", map[string]any{"senderId": "uid_own",
		"senderRole": "driver", "text": "poll", "createdAt": dump.Timestamp{Time: now}})
	rep, next := h.poll(failingGroups{h.client}, p, cursors)
	if len(rep.Skipped) != 1 || !next["messages"].Equal(cursors["messages"]) {
		t.Fatalf("skipped %v, cursor %v -> %v", rep.Skipped, cursors["messages"], next["messages"])
	}
	if n := h.scalar(`SELECT count(*) FROM chat_messages WHERE legacy_doc_id = 'm_poll'`); n != int64(0) {
		t.Fatalf("a skipped group was applied (%v)", n)
	}
	rep, _ = h.poll(h.client, p, cursors)
	if len(rep.Skipped) != 0 || applied(rep, "messages") != 1 {
		t.Fatalf("group poll: skipped %v, applied %d", rep.Skipped, applied(rep, "messages"))
	}
	if n := h.scalar(`SELECT count(*) FROM chat_messages WHERE legacy_doc_id = 'm_poll'`); n != int64(1) {
		t.Fatalf("the polled message %v", n)
	}
}

func anyList(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}
