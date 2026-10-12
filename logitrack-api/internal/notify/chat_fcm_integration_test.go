//go:build integration

package notify_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
)

// chat.message_created (T47): "chat" to the devices of the chat's driver for a staff message, read server-side from the
// message (the legacy callable took the ids from the browser, unauthenticated); nothing for a driver message unless
// NOTIFY_ADMIN_ON_DRIVER_MESSAGE, then the assigned admin's devices; the mirror's event of a Firestore write pushes
// nothing; a write-back handler's event (origin=writeback) pushes from its payload before the mirror has the message.
func TestFCMChatPushes(t *testing.T) {
	e := newFCMEnv(t)
	drv, ud := e.driver("chat-d@logitrack.test", "fsDrv")
	e.device(ud, "inst-d", "tok-d", "android")
	admin := e.user("chat-admin@logitrack.test")
	e.device(admin, "web-a", "tok-a", "web")
	chat := scan[uuid.UUID](e, `INSERT INTO chats (tenant_id, driver_id, legacy_doc_id, assigned_admin_user_id, status)
		VALUES ($1, $2, 'fsChat1', $3, 'in_progress') RETURNING id`, e.tenant, drv, admin)
	msg := func(role string, sender uuid.UUID, text *string, legacy string) uuid.UUID {
		var leg any
		if legacy != "" {
			leg = legacy
		}
		return scan[uuid.UUID](e, `INSERT INTO chat_messages (chat_id, sender_user_id, sender_role, text, legacy_doc_id)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, chat, sender, role, text, leg)
	}
	long := "ข้อความยาว 0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890"
	staff := msg("admin", admin, &long, "fsMsg1")
	e.handle(e.delivery(notify.RouteChatMessageCreated, map[string]any{"entity": "chat_message", "id": staff, "chatId": chat}))
	got := e.fcm.SentTo("tok-d")
	if len(e.fcm.Sent()) != 1 || len(got) != 1 {
		t.Fatalf("%d pushes", len(e.fcm.Sent()))
	}
	m := got[0].Message
	if m.Data["type"] != "chat" || m.Data["chatId"] != "fsChat1" || m.Data["messageId"] != "fsMsg1" ||
		m.Notification.Title != "New message from Admin" || []rune(m.Notification.Body)[len([]rune(m.Notification.Body))-1] != '…' {
		t.Fatalf("staff push %s", got[0].Raw)
	}
	// A driver message: nothing by default.
	driverMsg := msg("driver", ud, ptr("รับทราบ"), "")
	e.handle(e.delivery(notify.RouteChatMessageCreated, map[string]any{"id": driverMsg}))
	if n := len(e.fcm.Sent()); n != 1 {
		t.Fatalf("a driver message pushed (%d)", n)
	}
	// With NOTIFY_ADMIN_ON_DRIVER_MESSAGE: the assigned admin's devices, the driver named.
	withAdmin := &notify.FCM{Pool: e.app, Sender: e.fcm.Client(0), Redis: e.rdb, Keys: e.ks, Enabled: true,
		NotifyAdminOnDriverMessage: true, Log: zerolog.Nop()}
	if err := withAdmin.Handle(e.ctx, e.delivery(notify.RouteChatMessageCreated, map[string]any{"id": driverMsg})); err != nil {
		t.Fatal(err)
	}
	if a := e.fcm.SentTo("tok-a"); len(a) != 1 || a[0].Message.Notification.Title != "New message from สมชาย ใจดี" ||
		a[0].Message.Notification.Body != "รับทราบ" || a[0].Message.Data["chatId"] != "fsChat1" {
		t.Fatalf("admin push %+v", a)
	}
	// An image alone: the body is the lists' preview.
	img := scan[uuid.UUID](e, `INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, status, owner_kind, owner_id, storage_backend,
		committed_at) VALUES ('logitrack', 'chats/x/1.jpg', $1, 'chat_image', 'committed', 'chat', $2, 'local', now()) RETURNING id`, e.tenant, chat)
	imgMsg := scan[uuid.UUID](e, `INSERT INTO chat_messages (chat_id, sender_user_id, sender_role, image_file_id)
		VALUES ($1, $2, 'admin', $3) RETURNING id`, chat, admin, img)
	e.handle(e.delivery(notify.RouteChatMessageCreated, map[string]any{"id": imgMsg}))
	if d := e.fcm.SentTo("tok-d"); len(d) != 2 || d[1].Message.Notification.Body != "[Image]" || d[1].Message.Data["messageId"] != imgMsg.String() {
		t.Fatalf("image push %+v", d)
	}
	// The mirror's event of a Firestore write: the writer notified; nothing here.
	mirrored := e.delivery(notify.RouteChatMessageCreated, map[string]any{"id": staff})
	mirrored.Headers = map[string]any{outbox.HeaderOrigin: outbox.OriginMirror}
	e.handle(mirrored)
	// A write-back handler's event: the message is not in PostgreSQL yet; its payload carries it.
	wb := e.delivery(notify.RouteChatMessageCreated, map[string]any{"writeBack": true, "chatId": chat, "chatRef": "fsChat1",
		"messageRef": "0199-new", "senderRole": "admin", "text": "จากเว็บ", "driverId": drv})
	wb.Headers = map[string]any{outbox.HeaderOrigin: "writeback"}
	e.handle(wb)
	if d := e.fcm.SentTo("tok-d"); len(d) != 3 || d[2].Message.Data["messageId"] != "0199-new" || d[2].Message.Notification.Body != "จากเว็บ" {
		t.Fatalf("write-back push %+v", d)
	}
	// A new chat the mirror does not hold yet: the handler's driver.
	e.handle(e.delivery(notify.RouteChatMessageCreated, map[string]any{"writeBack": true, "chatId": uuid.New(), "chatRef": "fsNew",
		"messageRef": "m2", "senderRole": "admin", "text": "ใหม่", "driverId": drv}))
	if d := e.fcm.SentTo("tok-d"); len(d) != 4 || d[3].Message.Data["chatId"] != "fsNew" {
		t.Fatalf("new chat push %+v", d)
	}
	// Malformed events are permanent errors (acked, nothing sent).
	for _, p := range []map[string]any{{}, {"id": uuid.New()}, {"writeBack": true, "chatRef": "x"}} {
		err := e.consumer.Handle(e.ctx, e.delivery(notify.RouteChatMessageCreated, p))
		if !mq.IsPermanent(err) {
			t.Fatalf("%v: %v", p, err)
		}
	}
	if n := len(e.fcm.Sent()); n != 5 {
		t.Fatalf("%d pushes in all", n)
	}
}

func ptr[T any](v T) *T { return &v }
