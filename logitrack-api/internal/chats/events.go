package chats

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats/chatsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

// Outbox routing keys of the chat family (Appendix B §B.4.3, §B.5.2). The relay writes each topic's copy
// (realtime.Project): chat:{id} carries the whole message (SSE message.created) and the read state (read.updated);
// tenant:{tid}:chats and user:{driverUserId} carry the thin envelope only, never a message text.
//
//   - chat.created, chat.updated: tenant:{tid}:chats, payload {entity: chat, id, tenantId, at, status, priority,
//     assignedAdminUserId, driverId, changed[]};
//   - chat.message_created: chat:{id}, tenant:{tid}:chats and user:{driverUserId}, payload the message (entity
//     chat_message, id = the message, chatId, tenantId, at, senderUserId, senderRole, senderName, text, imageFileId,
//     messageType, clientMessageId, createdAt); senderName is the driver's name on a driver message and null on a staff
//     message, because the chat's driver subscribes to chat:{id} and never reads staff users (RLS; GET .../messages
//     gives a driver reader senderName null too); notify.fcm reads it and pushes "chat" to the driver's devices for a
//     staff message (and to the assigned admin's for a driver message when NOTIFY_ADMIN_ON_DRIVER_MESSAGE is on);
//   - chat.read: chat:{id}, payload {entity: chat, id, chatId, tenantId, at, userId, lastReadAt}.
const (
	RouteChatCreated    = "chat.created"
	RouteChatUpdated    = "chat.updated"
	RouteMessageCreated = "chat.message_created"
	RouteRead           = "chat.read"
)

// AggregateChat is the outbox aggregate of every chat event: the events of one chat follow the commit order of its
// writes (each write locks the chat first).
const AggregateChat = "chat"

// Entities of the payloads.
const (
	EntityChat    = "chat"
	EntityMessage = "chat_message"
)

func headers(rid string) map[string]string {
	if rid == "" {
		return nil
	}
	return map[string]string{"requestId": rid}
}

// chatEvent appends chat.created or chat.updated.
func chatEvent(ctx context.Context, tx pgx.Tx, rid, key string, c chatsdb.Chat, changed []string) error {
	if changed == nil {
		changed = []string{}
	}
	payload := map[string]any{"entity": EntityChat, "id": c.ID, "tenantId": c.TenantID, "at": c.UpdatedAt.UTC(),
		"status": c.Status, "priority": c.Priority, "assignedAdminUserId": c.AssignedAdminUserID, "driverId": c.DriverID,
		"changed": changed}
	_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: key, AggregateType: AggregateChat, AggregateID: c.ID.String(),
		TenantID: &c.TenantID, Payload: payload, Headers: headers(rid),
		Topics: []string{realtime.TenantTopic(c.TenantID.String(), realtime.FamilyChats)}})
	return err
}

// messageEvent appends chat.message_created: the whole message, on the chat's topic, the tenant's chat list and the
// driver's own user topic (thin there).
func messageEvent(ctx context.Context, tx pgx.Tx, rid string, c chatsdb.Chat, driverUser *uuid.UUID, m Message) error {
	payload := map[string]any{"entity": EntityMessage, "id": m.ID, "chatId": m.ChatID, "tenantId": c.TenantID,
		"at": m.CreatedAt.UTC(), "legacyDocId": m.LegacyDocID, "senderUserId": m.SenderUserID, "senderRole": m.SenderRole,
		"senderName": EventSenderName(m.SenderRole, m.SenderName), "text": m.Text, "imageFileId": m.ImageFileID, "messageType": m.MessageType,
		"clientMessageId": m.ClientMessageID, "createdAt": m.CreatedAt.UTC()}
	topics := []string{realtime.ChatTopic(c.ID.String()), realtime.TenantTopic(c.TenantID.String(), realtime.FamilyChats)}
	if driverUser != nil {
		topics = append(topics, realtime.UserTopic(driverUser.String()))
	}
	_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteMessageCreated, AggregateType: AggregateChat,
		AggregateID: c.ID.String(), TenantID: &c.TenantID, Payload: payload, Headers: headers(rid), Topics: topics})
	return err
}

// EventSenderName is the senderName of a chat.message_created payload: the sender's name on a driver message, nil on a
// staff message (chat:{id} is the driver's topic too, and a driver never reads staff users).
func EventSenderName(role string, name *string) *string {
	if role != RoleDriver {
		return nil
	}
	return name
}

// readEvent appends chat.read: a reader's read state moved (SSE read.updated on chat:{id}).
func readEvent(ctx context.Context, tx pgx.Tx, rid string, c chatsdb.Chat, user uuid.UUID, at time.Time) error {
	payload := map[string]any{"entity": EntityChat, "id": c.ID, "chatId": c.ID, "tenantId": c.TenantID, "at": at.UTC(),
		"userId": user, "lastReadAt": at.UTC()}
	_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteRead, AggregateType: AggregateChat, AggregateID: c.ID.String(),
		TenantID: &c.TenantID, Payload: payload, Headers: headers(rid), Topics: []string{realtime.ChatTopic(c.ID.String())}})
	return err
}

// changedFields lists the JSON fields of a chat that differ between two versions of its row (sorted).
func changedFields(before, after chatsdb.Chat) []string {
	var out []string
	add := func(name string, differ bool) {
		if differ {
			out = append(out, name)
		}
	}
	add("assignedAdminUserId", !eqUUID(before.AssignedAdminUserID, after.AssignedAdminUserID))
	add("assignedAt", !eqTime(before.AssignedAt, after.AssignedAt))
	add("closedAt", !eqTime(before.ClosedAt, after.ClosedAt))
	add("lastMessageAt", !eqTime(before.LastMessageAt, after.LastMessageAt))
	add("lastMessageByUserId", !eqUUID(before.LastMessageByUserID, after.LastMessageByUserID))
	add("lastMessagePreview", !eqStr(before.LastMessagePreview, after.LastMessagePreview))
	add("lastMessageType", !eqStr(before.LastMessageType, after.LastMessageType))
	add("priority", before.Priority != after.Priority)
	add("status", before.Status != after.Status)
	return out
}

func eqUUID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func eqTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func eqStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
