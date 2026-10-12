// Package chats serves the admin-driver chat of T47 (issue #67; main spec §5, §8, Appendix A §A.2.6, Appendix B
// §B.2.16, §B.2.21): the staff console /v1/chats* (the queue, my chats, the room's messages as keyset pages, read state,
// assignment, close, reopen, priority) and the driver app's own chat /v1/mobile/chat*.
//
// They replace the Firestore listeners and writes of the web console (ConversationsPanel.tsx:127-163, room/page.tsx,
// with-driver/page.tsx) and of the app (chat_repository.dart), and the unauthenticated callable notifyChatMessageCreated
// (fn:chat.ts:77): a message, its chat's last-message fields, the auto-assignment of the first admin reply and the
// sender's read state are one transaction, and the push to the driver is the outbox event chat.message_created that
// notify.fcm turns into exactly one FCM message per device (main spec §7.6). The client's clientMessageId is unique per
// chat (R63): a replayed send stores nothing and emits nothing. Unread counts are computed by the server (the legacy
// clients compared lastMessageAt with lastReadBy* themselves), and the read state is a row per (chat, user) written only
// by POST /v1/chats/{id}/read (the legacy room wrote lastReadByAdmin inside its snapshot callback,
// room/page.tsx:108-113).
//
// Every statement runs in db.WithPrincipal, so RLS decides (Appendix C §C.3.5): staff read and write the chats of their
// tenant reach, a driver its own; messages and read state follow their chat, and each sender writes only its own
// message and read state. Capabilities decide the verbs: chat:view reads, chat:send writes (staff), mobile:chat is the
// driver's own chat.
//
// Which store (main spec §12.1, §13.8): chats are class C, written in Firestore by the installed 3.x APKs until P7b.
// With comms in PG_OWNED_DOMAINS (the staging VM, and every environment from P7b) the writes go to PostgreSQL in the
// request transaction with their events. Otherwise they are written back to Firestore first (WriteBack, writeback.go)
// and the FS->PG mirror fills PostgreSQL; without a Firestore writer they answer 503 write_back_unconfigured and
// PostgreSQL is never written in Firestore's place.
package chats

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats/chatsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Chat statuses and priorities (chats CHECKs; the legacy literals are the same).
const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusClosed     = "closed"

	PriorityNormal = "normal"
	PriorityUrgent = "urgent"
)

// Sender roles (chat_messages.sender_role).
const (
	RoleAdmin  = "admin"
	RoleDriver = "driver"
)

// List filters of GET /v1/chats.
const (
	FilterQueue = "queue"
	FilterMine  = "mine"
	FilterAll   = "all"
)

var (
	// Statuses is the vocabulary of chats.status.
	Statuses = []string{StatusOpen, StatusInProgress, StatusClosed}
	// Priorities is the vocabulary of chats.priority.
	Priorities = []string{PriorityNormal, PriorityUrgent}
	// Filters is the vocabulary of ?filter.
	Filters = []string{FilterQueue, FilterMine, FilterAll}
)

// Paging (Appendix B §B.1.5): 50 per page by default (W9: a chat room loads 50 messages, then older pages).
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// MaxTextLength bounds a message's text (characters). The legacy clients had no bound.
const MaxTextLength = 4000

// ImagePreview is last_message_preview of an image without text (chat_repository.dart:150).
const ImagePreview = "[Image]"

// MaxPreviewLength bounds last_message_preview (characters): the lists show one line of it.
const MaxPreviewLength = 500

// Error codes and reasons of this package (Appendix B §B.1.5).
const (
	// ReasonOpenChatExists: the driver has another chat that is not closed (one open chat per driver, R14, D5).
	ReasonOpenChatExists = "open_chat_exists"
	// ReasonChatClosed: assigning a closed chat (reopen it first).
	ReasonChatClosed = "chat_closed"
	// ReasonWriteBackUnconfigured: comms is Firestore-owned and the api has no Firestore writer.
	ReasonWriteBackUnconfigured = "write_back_unconfigured"
	// ReasonWriteBackUnsupported: a field the write-back cannot carry (an image uploaded to Go's storage: the 3.x app
	// reads Firebase Storage URLs only, main spec §13.8).
	ReasonWriteBackUnsupported = "write_back_unsupported"
	// CodeIdempotencyConflict: a clientMessageId replayed with another message.
	CodeIdempotencyConflict = "idempotency_conflict"
	// CodeDriverProfileRequired: a driver-app route called by a principal without a driver profile.
	CodeDriverProfileRequired = "driver_profile_required"
)

// Files is the storage service as this package uses it (internal/storage implements it): the commit of an image runs in
// the request transaction; SignedFiles signs the image URLs of a page after it closed.
type Files interface {
	Commit(ctx context.Context, tx pgx.Tx, in storage.CommitInput) (storage.Committed, error)
	SignedFiles(ctx context.Context, ids []uuid.UUID, ttl time.Duration) (map[uuid.UUID]storage.Signed, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	// Pool is the logitrack_app pool: every request runs in db.WithPrincipal.
	Pool db.Beginner
	// Files commits and signs chat images; optional (without it an imageKey is refused and no URL is signed).
	Files Files
	// PGOwned is whether PG_OWNED_DOMAINS holds comms: the writes go to PostgreSQL.
	PGOwned bool
	// WriteBack is the Firestore-first path while comms is not PostgreSQL-owned; nil = none (the writes answer 503).
	WriteBack *WriteBack
	Log       zerolog.Logger
}

// Service serves /v1/chats* and /v1/mobile/chat*.
type Service struct {
	pool    db.Beginner
	files   Files
	pgOwned bool
	wb      *WriteBack
	log     zerolog.Logger
}

// New builds the service. It does no I/O.
func New(d Deps) (*Service, error) {
	if d.Pool == nil {
		return nil, errors.New("chats: the service needs a pool")
	}
	if d.WriteBack != nil && d.WriteBack.Writer == nil {
		return nil, errors.New("chats: a write-back needs a Firestore writer")
	}
	return &Service{pool: d.Pool, files: d.Files, pgOwned: d.PGOwned, wb: d.WriteBack, log: d.Log}, nil
}

// tx runs fn in a request transaction of p: RLS decides every row it reads or writes.
func (s *Service) tx(ctx context.Context, p *authz.Principal, fn func(tx pgx.Tx, q *chatsdb.Queries) error) error {
	return mapDBError(db.WithPrincipal(ctx, s.pool, p, func(tx pgx.Tx) error { return fn(tx, chatsdb.New(tx)) }))
}

// --- payloads ------------------------------------------------------------------------------------------------------

// Chat is one chat as the lists and the room show it. unreadForMe counts the other side's messages after the caller's
// read state (lastReadAt): driver messages for staff, staff messages for the driver.
type Chat struct {
	ID                  uuid.UUID  `json:"id"`
	TenantID            uuid.UUID  `json:"tenantId"`
	LegacyDocID         *string    `json:"legacyDocId"`
	DriverID            *uuid.UUID `json:"driverId"`
	DriverName          string     `json:"driverName"`
	DriverUserID        *uuid.UUID `json:"driverUserId"`
	DriverLegacyUID     *string    `json:"driverLegacyUid"`
	Status              string     `json:"status"`
	Priority            string     `json:"priority"`
	AssignedAdminUserID *uuid.UUID `json:"assignedAdminUserId"`
	AssignedAdminName   *string    `json:"assignedAdminName"`
	AssignedAt          *time.Time `json:"assignedAt"`
	ClosedAt            *time.Time `json:"closedAt"`
	LastMessagePreview  *string    `json:"lastMessagePreview"`
	LastMessageAt       *time.Time `json:"lastMessageAt"`
	LastMessageByUserID *uuid.UUID `json:"lastMessageByUserId"`
	LastMessageType     *string    `json:"lastMessageType"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	UnreadForMe         int64      `json:"unreadForMe"`
	LastReadAt          *time.Time `json:"lastReadAt"`
}

// Read is one reader's read state.
type Read struct {
	UserID     uuid.UUID `json:"userId"`
	LastReadAt time.Time `json:"lastReadAt"`
	IsDriver   bool      `json:"isDriver"`
}

// ChatDetail is GET /v1/chats/{id}: the chat with every reader's read state (the room's read receipts) and the driver's
// own (lastReadByDriver).
type ChatDetail struct {
	Chat
	Reads            []Read     `json:"reads"`
	DriverLastReadAt *time.Time `json:"driverLastReadAt"`
}

// Message is one message. imageUrl is a short-lived signed URL of the image (S3_PRESIGN_GET_TTL).
type Message struct {
	ID              uuid.UUID  `json:"id"`
	ChatID          uuid.UUID  `json:"chatId"`
	LegacyDocID     *string    `json:"legacyDocId"`
	SenderUserID    *uuid.UUID `json:"senderUserId"`
	SenderRole      string     `json:"senderRole"`
	SenderName      *string    `json:"senderName"`
	Text            *string    `json:"text"`
	ImageFileID     *uuid.UUID `json:"imageFileId"`
	ImageURL        *string    `json:"imageUrl"`
	MessageType     string     `json:"messageType"`
	ClientMessageID *uuid.UUID `json:"clientMessageId"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// UnreadCount is GET /v1/mobile/chat/unread-count: the driver's chats with unread staff messages (the badge the app
// computed, main_layout.dart:156-173) and those messages.
type UnreadCount struct {
	Count    int64 `json:"count"`
	Messages int64 `json:"messages"`
}

func chatOf(c chatsdb.Chat, driverName string, driverUser *uuid.UUID, legacyUID string, adminName *string,
	lastRead *time.Time, unread int64) Chat {
	out := Chat{ID: c.ID, TenantID: c.TenantID, LegacyDocID: c.LegacyDocID, DriverID: c.DriverID, DriverName: driverName,
		DriverUserID: driverUser, Status: c.Status, Priority: c.Priority, AssignedAdminUserID: c.AssignedAdminUserID,
		AssignedAdminName: adminName, AssignedAt: c.AssignedAt, ClosedAt: c.ClosedAt, LastMessagePreview: c.LastMessagePreview,
		LastMessageAt: c.LastMessageAt, LastMessageByUserID: c.LastMessageByUserID, LastMessageType: c.LastMessageType,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, UnreadForMe: unread, LastReadAt: lastRead}
	if legacyUID != "" {
		out.DriverLegacyUID = &legacyUID
	}
	return out
}

func messageOf(m chatsdb.ChatMessage, senderName *string) Message {
	return Message{ID: m.ID, ChatID: m.ChatID, LegacyDocID: m.LegacyDocID, SenderUserID: m.SenderUserID,
		SenderRole: m.SenderRole, SenderName: senderName, Text: m.Text, ImageFileID: m.ImageFileID, MessageType: m.MessageType,
		ClientMessageID: m.ClientMessageID, CreatedAt: m.CreatedAt}
}

// --- principals ----------------------------------------------------------------------------------------------------

// staff reports a person acting with a staff role (the RLS role of p: app_is_staff()).
func staff(p *authz.Principal) bool {
	return p != nil && !p.IsMachine() && p.EffectiveRole().IsStaff()
}

// driverOf is the driver of a driver-app principal: a person signed in with a driver membership and profile; anyone
// else is 403 driver_profile_required.
func driverOf(p *authz.Principal) (uuid.UUID, error) {
	if p == nil || p.IsMachine() || p.EffectiveRole() != authz.Driver || p.DriverID == nil || p.TenantID == nil {
		return uuid.Nil, httpx.NewError(http.StatusForbidden, CodeDriverProfileRequired, "a driver profile is required")
	}
	return *p.DriverID, nil
}

// otherRole is the sender role whose messages are unread for p.
func otherRole(p *authz.Principal) string {
	if p.EffectiveRole() == authz.Driver {
		return RoleAdmin
	}
	return RoleDriver
}

// --- request helpers -----------------------------------------------------------------------------------------------

func violation(field, reason string) httpx.FieldViolation {
	return httpx.FieldViolation{Field: field, Reason: reason}
}

// cleanText trims a message text; "" when blank. bad names the violation of a text that is too long or not UTF-8.
func cleanText(s string) (out, bad string) {
	if !utf8.ValidString(s) {
		return "", "invalid"
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > MaxTextLength {
		return "", "too_long"
	}
	return s, ""
}

// preview is last_message_preview of a message: its text cut to MaxPreviewLength characters, else ImagePreview.
func preview(text *string) string {
	if text == nil || *text == "" {
		return ImagePreview
	}
	r := []rune(*text)
	if len(r) > MaxPreviewLength {
		return string(r[:MaxPreviewLength])
	}
	return *text
}

func parseLimit(raw string) (int, error) {
	if raw == "" {
		return DefaultLimit, nil
	}
	n := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' || n > MaxLimit {
			n = -1
			break
		}
		n = n*10 + int(ch-'0')
	}
	if n < 1 || n > MaxLimit {
		return 0, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "limit", Reason: "out_of_range",
			Params: map[string]any{"min": 1, "max": MaxLimit}})
	}
	return n, nil
}

// --- keyset cursors ------------------------------------------------------------------------------------------------

// cursor is the opaque keyset position (Appendix B §B.1.5), base64url JSON: (last_message_at, id) of a chat (a null
// last_message_at sorts last) or (created_at, id) of a message.
type cursor struct {
	T  *time.Time `json:"t,omitempty"`
	ID uuid.UUID  `json:"id"`
}

func (c cursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, false
	}
	var c cursor
	if json.Unmarshal(b, &c) != nil || c.ID == uuid.Nil || (c.T != nil && c.T.IsZero()) {
		return cursor{}, false
	}
	return c, true
}

// --- errors --------------------------------------------------------------------------------------------------------

func errOpenChatExists(other uuid.UUID) *httpx.Error {
	d := map[string]any{"reason": ReasonOpenChatExists}
	if other != uuid.Nil {
		d["chatId"] = other
	}
	return httpx.NewError(http.StatusConflict, "already_exists", "the driver already has an open chat").WithDetails(d)
}

func errChatClosed() *httpx.Error {
	return httpx.NewError(http.StatusConflict, "failed_precondition", "the chat is closed").
		WithDetails(map[string]any{"reason": ReasonChatClosed})
}

func errWriteBackUnconfigured() *httpx.Error {
	return httpx.ErrUnavailable("the comms domain is Firestore-owned and the api has no Firestore writer").
		WithDetails(map[string]any{"reason": ReasonWriteBackUnconfigured})
}

// mapDBError turns the database refusals a request can still meet into the API's codes: a second open chat of a driver
// (chats_one_open_per_driver, 0010: 409 already_exists), other unique keys (409), checks and foreign keys (422), RLS
// (42501: a row outside the caller's write reach, 403) and a deadlock or serialization failure (503 unavailable,
// details.reason contention: the transaction was rolled back and may be retried); anything else is returned as is.
func mapDBError(err error) error {
	pe, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return err
	}
	switch pe.Code {
	case "23505":
		if pe.ConstraintName == "chats_one_open_per_driver" {
			return errOpenChatExists(uuid.Nil).Wrap(err)
		}
		return httpx.NewError(http.StatusConflict, "already_exists", "already exists").
			WithDetails(map[string]any{"constraint": pe.ConstraintName}).Wrap(err)
	case "23503":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "not_found")).Wrap(err)
	case "23514":
		return httpx.ErrInvalidArgument(violation(pe.ConstraintName, "invalid")).Wrap(err)
	case "42501":
		return authz.ErrPermissionDenied("row-level security refused the write").Wrap(err)
	case "40P01", "40001":
		return httpx.NewError(http.StatusServiceUnavailable, httpx.CodeUnavailable, "the database aborted a conflicting write").
			WithDetails(map[string]any{"reason": "contention"}).Wrap(err)
	}
	return err
}

// notFound maps a missing row (or one RLS hides) to 404.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound()
	}
	return err
}

func validStatus(s string) bool   { return slices.Contains(Statuses, s) }
func validPriority(s string) bool { return slices.Contains(Priorities, s) }
