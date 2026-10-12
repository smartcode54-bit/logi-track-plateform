//go:build integration

package notify_test

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
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
	e.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'tenant_admin')`, admin, e.tenant)
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

// The assigned admin's push of a driver message (NOTIFY_ADMIN_ON_DRIVER_MESSAGE) carries the chat's text, so it goes
// only to an active staff member who reaches the chat's tenant (its own, or the contractor that tenant works for) with
// chat:view there, overrides included: a revoked membership, a disabled account, the user role or an override that
// removes chat:view sends nothing. A write-back event of a chat the mirror has not applied is checked against the
// tenant in its payload (T47 review).
func TestFCMChatAdminPushNeedsAccess(t *testing.T) {
	e := newFCMEnv(t)
	drv, ud := e.driver("acc-d@logitrack.test", "fsAccDrv")
	admin := e.user("acc-admin@logitrack.test")
	e.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'operator')`, admin, e.tenant)
	e.device(admin, "web-acc", "tok-acc", "web")
	chat := scan[uuid.UUID](e, `INSERT INTO chats (tenant_id, driver_id, legacy_doc_id, assigned_admin_user_id, status)
		VALUES ($1, $2, 'fsAcc', $3, 'in_progress') RETURNING id`, e.tenant, drv, admin)
	consumer := &notify.FCM{Pool: e.app, Sender: e.fcm.Client(0), Redis: e.rdb, Keys: e.ks, Enabled: true,
		NotifyAdminOnDriverMessage: true, Log: zerolog.Nop()}
	n := 0
	say := func(chat uuid.UUID, want int, why string) {
		t.Helper()
		n++
		m := scan[uuid.UUID](e, `INSERT INTO chat_messages (chat_id, sender_user_id, sender_role, text, legacy_doc_id)
			VALUES ($1, $2, 'driver', 'ลูกค้าไม่รับของ โทร 0812345678', $3) RETURNING id`, chat, ud, fmt.Sprintf("accMsg%d", n))
		if err := consumer.Handle(e.ctx, e.delivery(notify.RouteChatMessageCreated, map[string]any{"id": m})); err != nil {
			t.Fatal(err)
		}
		if got := len(e.fcm.SentTo("tok-acc")); got != want {
			t.Fatalf("%s: %d pushes to the assignee, want %d", why, got, want)
		}
	}
	say(chat, 1, "an operator of the chat's tenant")
	e.exec(`INSERT INTO role_capability_overrides (tenant_id, role, capability, allowed, updated_by)
		VALUES ($1, 'operator', 'chat:view', false, $2)`, e.tenant, admin)
	say(chat, 1, "an override removed chat:view")
	e.exec(`DELETE FROM role_capability_overrides`)
	say(chat, 2, "the override is gone")
	system := func(sql string, args ...any) { // a status change is platform-only (the users column guard)
		t.Helper()
		if err := db.WithSystem(e.ctx, e.app, nil, func(tx pgx.Tx) error {
			_, err := tx.Exec(e.ctx, sql, args...)
			return err
		}); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	system(`UPDATE users SET status = 'disabled', disabled_at = now() WHERE id = $1`, admin)
	say(chat, 2, "a disabled account")
	system(`UPDATE users SET status = 'active', disabled_at = NULL WHERE id = $1`, admin)
	e.exec(`UPDATE memberships SET role = 'user' WHERE user_id = $1`, admin)
	say(chat, 2, "the user role holds no chat:view")
	e.exec(`DELETE FROM memberships WHERE user_id = $1`, admin)
	say(chat, 2, "a revoked membership")

	// Contractor reach: a carrier that works for this tenant; the assignee is staff of the contractor.
	e.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'operator')`, admin, e.tenant)
	carrier := scan[uuid.UUID](e, `INSERT INTO tenants (kind, name_th, name_en, legal_type, contractor_tenant_id)
		VALUES ('carrier', 'ผู้รับเหมา', 'Carrier', 'company', $1) RETURNING id`, e.tenant)
	drv2, _ := e.driver("acc-d2@logitrack.test", "fsAccDrv2")
	chatC := scan[uuid.UUID](e, `INSERT INTO chats (tenant_id, driver_id, legacy_doc_id, assigned_admin_user_id, status)
		VALUES ($1, $2, 'fsAccC', $3, 'in_progress') RETURNING id`, carrier, drv2, admin)
	say(chatC, 3, "staff of the contractor of the chat's tenant")
	other := scan[uuid.UUID](e, `INSERT INTO tenants (kind, name_th, name_en, legal_type) VALUES ('carrier', 'อื่น', 'Other', 'company') RETURNING id`)
	drv3, _ := e.driver("acc-d3@logitrack.test", "fsAccDrv3")
	chatO := scan[uuid.UUID](e, `INSERT INTO chats (tenant_id, driver_id, legacy_doc_id, assigned_admin_user_id, status)
		VALUES ($1, $2, 'fsAccO', $3, 'in_progress') RETURNING id`, other, drv3, admin)
	say(chatO, 3, "a tenant outside the assignee's reach")

	// A write-back event of a chat the mirror has not applied: the tenant of its payload decides.
	wb := func(tenant any, ref string) {
		t.Helper()
		d := e.delivery(notify.RouteChatMessageCreated, map[string]any{"writeBack": true, "chatRef": "fsAccNew", "messageRef": ref,
			"senderRole": "driver", "text": "จากแอป", "driverId": drv, "assignedAdminUserId": admin, "tenantId": tenant})
		d.Headers = map[string]any{outbox.HeaderOrigin: "writeback"}
		if err := consumer.Handle(e.ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	wb(e.tenant, "wbAcc1")
	if got := len(e.fcm.SentTo("tok-acc")); got != 4 {
		t.Fatalf("write-back event in reach: %d pushes", got)
	}
	wb(other, "wbAcc2")
	wb(nil, "wbAcc3")
	if got := len(e.fcm.SentTo("tok-acc")); got != 4 {
		t.Fatalf("write-back events outside reach: %d pushes", got)
	}
}

// One chat message is pushed once per device however many events name it (a write-back's replay appends its event
// again): the first event that pushes it holds idem:push:chat:{chatRef}:{messageRef}, the others log deduplicated; a
// redelivery of the first still sends to a device it has not reached (T47 review).
func TestFCMChatPushOncePerMessage(t *testing.T) {
	e := newFCMEnv(t)
	drv, ud := e.driver("once-d@logitrack.test", "fsOnceDrv")
	e.device(ud, "inst-once", "tok-once", "android")
	chat := scan[uuid.UUID](e, `INSERT INTO chats (tenant_id, driver_id, legacy_doc_id, status) VALUES ($1, $2, 'fsOnce', 'open') RETURNING id`,
		e.tenant, drv)
	ev := func(ref string) *mq.Delivery {
		d := e.delivery(notify.RouteChatMessageCreated, map[string]any{"writeBack": true, "chatId": chat, "chatRef": "fsOnce",
			"messageRef": ref, "senderRole": "admin", "text": "ไปรับของ", "tenantId": e.tenant})
		d.Headers = map[string]any{outbox.HeaderOrigin: "writeback"}
		return d
	}
	first := ev("once-1")
	e.handle(first)
	e.handle(ev("once-1"))
	e.handle(ev("once-1"))
	e.handle(first) // a redelivery of the first
	if got := e.fcm.SentTo("tok-once"); len(got) != 1 || got[0].Message.Data["messageId"] != "once-1" {
		t.Fatalf("%d pushes for one message", len(got))
	}
	e.handle(ev("once-2"))
	if got := e.fcm.SentTo("tok-once"); len(got) != 2 {
		t.Fatalf("another message: %d pushes in all", len(got))
	}
	rows := e.rows()
	if sent, dedup := count(rows, "chat", notify.StatusSent), count(rows, "chat", notify.StatusDeduplicated); sent != 2 || dedup != 2 {
		t.Fatalf("delivery rows: %d sent, %d deduplicated (%+v)", sent, dedup, rows)
	}
}

func ptr[T any](v T) *T { return &v }
