package chats

import (
	"time"

	"github.com/google/uuid"
)

// The legacy Firestore shape of chats/{id} and chats/{id}/messages/{mid} (lib/chat.ts, with-driver/page.tsx:48-62,
// room/page.tsx:165-238, chat_repository.dart:28-160), written by the class C write-back while comms is Firestore-owned
// (main spec §13.8): the field names and literals the installed 3.x APKs and the legacy web read, and the inverse of
// the ETL's loadChat / loadChatMessage (Appendix A §A.3.10). Users are named by the id the 3.x app knows (their
// Firebase uid, legacy_auth_uid, else the uuid the bridge gives a user born in Go); times are Firestore timestamps
// (time.Time); a nil value deletes the field (the legacy null writes become absent fields, which every reader treats
// alike). A change writes only the fields it changed, so the fields the 3.x app writes on the same document
// (lastReadByDriver, the last message of its own sends) stay the app's.

// Legacy field names.
const (
	fDriverID          = "driverId"
	fDriverDisplayName = "driverDisplayName"
	fStatus            = "status"
	fAssignedAdminID   = "assignedAdminId"
	fAssignedAt        = "assignedAt"
	fClosedAt          = "closedAt"
	fPriority          = "priority"
	fLastReadByAdmin   = "lastReadByAdmin"
	fLastReadByDriver  = "lastReadByDriver"
	fLastMessage       = "lastMessage"
	fLastMessageAt     = "lastMessageAt"
	fLastMessageBy     = "lastMessageBy"
	fLastMessageType   = "lastMessageType"
	fCreatedAt         = "createdAt"
	fUpdatedAt         = "updatedAt"

	fSenderID        = "senderId"
	fSenderRole      = "senderRole"
	fText            = "text"
	fClientMessageID = "clientMessageId"
)

// Firestore collections of the write-back (legacy names).
const (
	CollChats    = "chats"
	CollMessages = "messages"
)

// ChatPath is the document path of a chat; MessagePath that of one of its messages.
func ChatPath(chatDoc string) string { return CollChats + "/" + chatDoc }
func MessagePath(chatDoc, msgDoc string) string {
	return ChatPath(chatDoc) + "/" + CollMessages + "/" + msgDoc
}

// LegacyNewChat is a new chat document (with-driver/page.tsx:48-62, chat_repository.dart:28-43): open, unassigned,
// normal, no read state, an empty last message dated now (so it sorts into the lists).
func LegacyNewChat(driverRef, driverName string, at time.Time) map[string]any {
	if driverName == "" {
		driverName = "Driver"
	}
	return map[string]any{
		fDriverID: driverRef, fDriverDisplayName: driverName, fStatus: StatusOpen, fAssignedAdminID: nil, fAssignedAt: nil,
		fClosedAt: nil, fPriority: PriorityNormal, fLastReadByAdmin: map[string]any{}, fLastMessage: "", fLastMessageAt: at,
		fLastMessageBy: "", fCreatedAt: at, fUpdatedAt: at,
	}
}

// LegacyMessage is a message document (room/page.tsx:174-179, chat_repository.dart:107-112) with the client's message
// id (clientMessageId, R63; the document id is that id too, so a replayed write lands on the same document).
func LegacyMessage(senderRef, role string, text *string, at time.Time, clientID uuid.UUID) map[string]any {
	t := ""
	if text != nil {
		t = *text
	}
	return map[string]any{fSenderID: senderRef, fSenderRole: role, fText: t, fCreatedAt: at, fClientMessageID: clientID.String()}
}

// LegacyChatChange is the masked update of a chat document from one version of its row to the next (refs maps user
// ids to their legacy ids): only the fields whose value changed, plus updatedAt. Empty when nothing a legacy reader
// shows changed.
func LegacyChatChange(before, after legacyChat, refs map[uuid.UUID]string, at time.Time) map[string]any {
	out := map[string]any{}
	if before.status != after.status {
		out[fStatus] = after.status
	}
	if !eqUUID(before.assigned, after.assigned) {
		out[fAssignedAdminID] = refOrNil(after.assigned, refs)
	}
	if !eqTime(before.assignedAt, after.assignedAt) {
		out[fAssignedAt] = timeOrNil(after.assignedAt)
	}
	if !eqTime(before.closedAt, after.closedAt) {
		out[fClosedAt] = timeOrNil(after.closedAt)
	}
	if before.priority != after.priority {
		out[fPriority] = after.priority
	}
	if len(out) > 0 {
		out[fUpdatedAt] = at
	}
	return out
}

// legacyChat is what a chat document's mutable fields say, in PostgreSQL terms.
type legacyChat struct {
	status, priority     string
	assigned             *uuid.UUID
	assignedAt, closedAt *time.Time
}

// LegacyLastMessage is the update of a chat document a new message makes (room/page.tsx:181-211,
// chat_repository.dart:113-119): the last message (the preview, "[Image]" for an image alone), who wrote it, the
// sender's read time (lastReadByAdmin, the whole map with the sender's entry; lastReadByDriver for the driver), and,
// for a staff reply, the auto-assignment and the reopening of a closed chat.
func LegacyLastMessage(role string, senderRef string, text *string, at time.Time, adminReads map[string]time.Time,
	assign bool, reopen bool) map[string]any {
	out := map[string]any{fLastMessage: preview(text), fLastMessageAt: at, fLastMessageBy: senderRef,
		fLastMessageType: "normal", fUpdatedAt: at}
	if role == RoleDriver {
		out[fLastReadByDriver] = at
		return out
	}
	out[fLastReadByAdmin] = readMap(adminReads)
	if assign {
		out[fAssignedAdminID], out[fAssignedAt], out[fStatus] = senderRef, at, StatusInProgress
	}
	if reopen {
		out[fStatus], out[fClosedAt] = StatusInProgress, nil
	}
	return out
}

// LegacyRead is the update of a chat document a read makes: lastReadByDriver for the driver, else the whole
// lastReadByAdmin map (the REST update mask names top-level fields only, so the map is written whole from the read
// states the mirror holds plus the reader's; a staff read the mirror has not applied yet is the only one it can
// overwrite, and the next read of that user restores it).
func LegacyRead(role string, at time.Time, adminReads map[string]time.Time) map[string]any {
	if role == RoleDriver {
		return map[string]any{fLastReadByDriver: at, fUpdatedAt: at}
	}
	return map[string]any{fLastReadByAdmin: readMap(adminReads), fUpdatedAt: at}
}

func readMap(reads map[string]time.Time) map[string]any {
	out := make(map[string]any, len(reads))
	for k, v := range reads {
		out[k] = v
	}
	return out
}

func refOrNil(id *uuid.UUID, refs map[uuid.UUID]string) any {
	if id == nil {
		return nil
	}
	if r, ok := refs[*id]; ok {
		return r
	}
	return id.String()
}

func timeOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}
