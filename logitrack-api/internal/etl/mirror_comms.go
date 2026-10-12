package etl

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// Chat events of the mirror (T47, issue #67; main spec §8.3, §13.7, Appendix B §B.4.3). While comms is Firestore-owned
// the installed 3.x APKs and the legacy web write chats and their messages in Firestore, so the FS->PG mirror is the
// producer of the chat events the staff console and the driver's chat room follow (SSE from the mirror): each chat or
// message document a sweep or a listener applies is compared with its rows before and after, and what changed is
// appended to the outbox in the mirror's transaction (a re-applied unchanged version emits nothing):
//
//   - chats: chat.created (a new row, or one leaving the quarantine tenant), chat.updated (the status, the assignment, the
//     priority or the close time changed; the last message alone is the message's own event), chat.read for every
//     reader whose read state moved forward (lastReadByAdmin.{uid}, lastReadByDriver);
//   - messages (chats/*/messages): chat.message_created for a message the mirror inserts, with the whole message.
//
// The payloads and topics are the ones internal/chats appends on the PostgreSQL path (the relay projects the copies:
// the whole message on chat:{id} only), plus header origin=etl.sync and source=mirror: notify.fcm pushes nothing for
// them, because the Firestore-side writer (the legacy callable notifyChatMessageCreated, or the Go write-back handler,
// which appends its own push event) owns the push. A row in the quarantine tenant never emits.

// CommsCollections are the comms collections whose mirrored writes emit chat events (and that the listeners poll).
var CommsCollections = []string{"chats", "messages"}

// commsState is a chat as one version of its document left it (chats), or whether a message row existed (messages).
type commsState struct {
	id                     uuid.UUID
	tenant                 uuid.UUID
	status, priority       string
	assigned               *uuid.UUID
	assignedAt, closedAt   *time.Time
	reads                  map[uuid.UUID]time.Time
	messageExisted, isChat bool
}

// commsBefore reads the row a chat or message document is about to update (nothing for another collection).
func commsBefore(c *docCtx) (commsState, error) {
	switch c.coll {
	case "chats":
		var id uuid.UUID
		err := c.tx.QueryRow(c.ctx, `SELECT id FROM chats WHERE legacy_doc_id = $1`, c.doc.ID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return commsState{isChat: true}, nil
		}
		if err != nil {
			return commsState{}, fmt.Errorf("etl: %s: the chat before: %w", c.doc.Path, err)
		}
		return readChatState(c, id)
	case "messages":
		chatDoc, ok := strings.CutPrefix(c.doc.ParentPath(), "chats/")
		if !ok {
			return commsState{}, nil
		}
		var exists bool
		err := c.tx.QueryRow(c.ctx, `SELECT EXISTS (SELECT 1 FROM chat_messages m JOIN chats ch ON ch.id = m.chat_id
			WHERE ch.legacy_doc_id = $1 AND m.legacy_doc_id = $2)`, chatDoc, c.doc.ID).Scan(&exists)
		if err != nil {
			return commsState{}, fmt.Errorf("etl: %s: the message before: %w", c.doc.Path, err)
		}
		return commsState{messageExisted: exists}, nil
	}
	return commsState{}, nil
}

func readChatState(c *docCtx, id uuid.UUID) (commsState, error) {
	s := commsState{id: id, isChat: true, reads: map[uuid.UUID]time.Time{}}
	err := c.tx.QueryRow(c.ctx, `SELECT tenant_id, status, priority, assigned_admin_user_id, assigned_at, closed_at FROM chats
		WHERE id = $1`, id).Scan(&s.tenant, &s.status, &s.priority, &s.assigned, &s.assignedAt, &s.closedAt)
	if err != nil {
		return commsState{}, fmt.Errorf("etl: %s: read chat: %w", c.doc.Path, err)
	}
	rows, err := c.tx.Query(c.ctx, `SELECT user_id, last_read_at FROM chat_read_state WHERE chat_id = $1`, id)
	if err != nil {
		return commsState{}, fmt.Errorf("etl: %s: read state: %w", c.doc.Path, err)
	}
	defer rows.Close()
	for rows.Next() {
		var u uuid.UUID
		var at time.Time
		if err := rows.Scan(&u, &at); err != nil {
			return commsState{}, err
		}
		s.reads[u] = at
	}
	return s, rows.Err()
}

// emitCommsEvents appends the chat events of one applied document (c after mirrorApply) and returns how many.
func emitCommsEvents(c *docCtx, before commsState) (int, error) {
	chat := c.coll == "chats" && c.table == "chats"
	message := c.coll == "messages" && c.table == "chat_messages" && !before.messageExisted
	if c.outcome != Loaded || c.target == "" || (!chat && !message) {
		return 0, nil
	}
	id, err := uuid.Parse(c.target)
	if err != nil {
		return 0, fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	if chat {
		return emitChatEvents(c, before, id)
	}
	return emitMessageEvent(c, id)
}

func mirrorEvent(c *docCtx, key string, chat, tenant uuid.UUID, payload map[string]any, topics []string) error {
	payload["source"] = "mirror"
	_, err := outbox.Append(c.ctx, c.tx, outbox.Event{RoutingKey: key, AggregateType: "chat", AggregateID: chat.String(),
		TenantID: &tenant, Payload: payload, Topics: topics, Headers: map[string]string{outbox.HeaderOrigin: outbox.OriginMirror}})
	if err != nil {
		return fmt.Errorf("etl: %s: %s: %w", c.doc.Path, key, err)
	}
	return nil
}

func emitChatEvents(c *docCtx, before commsState, id uuid.UUID) (int, error) {
	after, err := readChatState(c, id)
	if err != nil {
		return 0, err
	}
	quarantine := c.e.quar
	if after.tenant == quarantine {
		return 0, nil
	}
	if before.id != uuid.Nil && before.id != after.id {
		before = commsState{isChat: true}
	}
	at := c.doc.UpdateTime.UTC()
	var driver *uuid.UUID
	if err := c.tx.QueryRow(c.ctx, `SELECT driver_id FROM chats WHERE id = $1`, id).Scan(&driver); err != nil {
		return 0, fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	payload := func(changed []string) map[string]any {
		if changed == nil {
			changed = []string{}
		}
		return map[string]any{"entity": "chat", "id": id, "tenantId": after.tenant, "at": at, "legacyDocId": c.doc.ID,
			"status": after.status, "priority": after.priority, "assignedAdminUserId": after.assigned, "driverId": driver,
			"changed": changed}
	}
	list := []string{realtime.TenantTopic(after.tenant.String(), realtime.FamilyChats)}
	n := 0
	if before.id == uuid.Nil || before.tenant == quarantine {
		if err := mirrorEvent(c, "chat.created", id, after.tenant, payload(nil), list); err != nil {
			return 0, err
		}
		return 1, nil
	}
	var changed []string
	add := func(name string, differ bool) {
		if differ {
			changed = append(changed, name)
		}
	}
	add("assignedAdminUserId", !sameUUID(before.assigned, after.assigned))
	add("assignedAt", !sameTime(before.assignedAt, after.assignedAt))
	add("closedAt", !sameTime(before.closedAt, after.closedAt))
	add("priority", before.priority != after.priority)
	add("status", before.status != after.status)
	if len(changed) > 0 {
		if err := mirrorEvent(c, "chat.updated", id, after.tenant, payload(changed), list); err != nil {
			return 0, err
		}
		n++
	}
	users := make([]uuid.UUID, 0, len(after.reads))
	for u := range after.reads {
		users = append(users, u)
	}
	slices.SortFunc(users, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, u := range users {
		read := after.reads[u]
		if prev, ok := before.reads[u]; ok && !read.After(prev) {
			continue
		}
		p := map[string]any{"entity": "chat", "id": id, "chatId": id, "tenantId": after.tenant, "at": read.UTC(), "userId": u,
			"lastReadAt": read.UTC()}
		if err := mirrorEvent(c, "chat.read", id, after.tenant, p, []string{realtime.ChatTopic(id.String())}); err != nil {
			return 0, err
		}
		n++
	}
	return n, nil
}

func emitMessageEvent(c *docCtx, id uuid.UUID) (int, error) {
	var chat, tenant uuid.UUID
	var sender, image, client, driverUser *uuid.UUID
	var role, typ string
	var text, senderName *string
	var created time.Time
	err := c.tx.QueryRow(c.ctx, `SELECT m.chat_id, ch.tenant_id, m.sender_user_id, m.sender_role, m.text, m.image_file_id,
			m.message_type, m.client_message_id, m.created_at, u.display_name, d.user_id
		FROM chat_messages m JOIN chats ch ON ch.id = m.chat_id
		LEFT JOIN users u ON u.id = m.sender_user_id
		LEFT JOIN drivers d ON d.id = ch.driver_id
		WHERE m.id = $1`, id).Scan(&chat, &tenant, &sender, &role, &text, &image, &typ, &client, &created, &senderName, &driverUser)
	if err != nil {
		return 0, fmt.Errorf("etl: %s: read message: %w", c.doc.Path, err)
	}
	if tenant == c.e.quar {
		return 0, nil
	}
	if role != "driver" {
		// chat:{id} is the driver's topic too, and a driver never reads staff users (RLS): a staff message carries no
		// name, as internal/chats' own event (chats.EventSenderName).
		senderName = nil
	}
	legacy := c.doc.ID
	payload := map[string]any{"entity": "chat_message", "id": id, "chatId": chat, "tenantId": tenant, "at": created.UTC(),
		"legacyDocId": legacy, "senderUserId": sender, "senderRole": role, "senderName": senderName, "text": text,
		"imageFileId": image, "messageType": typ, "clientMessageId": client, "createdAt": created.UTC()}
	topics := []string{realtime.ChatTopic(chat.String()), realtime.TenantTopic(tenant.String(), realtime.FamilyChats)}
	if driverUser != nil {
		topics = append(topics, realtime.UserTopic(driverUser.String()))
	}
	if err := mirrorEvent(c, "chat.message_created", chat, tenant, payload, topics); err != nil {
		return 0, err
	}
	return 1, nil
}
