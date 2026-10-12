package chats

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats/chatsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// Field is an optional JSON field of a request body: Set when the key is present, Null when its value is null.
type Field[T any] = iam.Field[T]

type writeMode int

const (
	modePG writeMode = iota + 1
	modeWriteBack
)

// mode is the path of the writes: PostgreSQL when comms is PostgreSQL-owned, the Firestore write-back when a writer is
// configured, else 503 write_back_unconfigured (PostgreSQL is never written in Firestore's place, main spec §12.1).
func (s *Service) mode() (writeMode, error) {
	switch {
	case s.pgOwned:
		return modePG, nil
	case s.wb != nil:
		return modeWriteBack, nil
	}
	return 0, errWriteBackUnconfigured()
}

// WriteResult is the answer of a write: its status (200, 201, or 202 for a write-back the mirror has not acknowledged
// within the wait) and its data.
type WriteResult struct {
	Status int
	Data   any
}

// --- open (get-or-create) ------------------------------------------------------------------------------------------

// OpenInput is the body of POST /v1/chats.
type OpenInput struct {
	DriverID string `json:"driverId"`
}

// Open is POST /v1/chats {driverId} (with-driver/page.tsx:32-62): the driver's chat that is not closed, created when it
// has none (201; an existing one is 200). Concurrent calls for one driver hold a transaction-scoped advisory lock, so
// they never create two open chats (chats_one_open_per_driver holds below the API where 0010 is applied).
func (s *Service) Open(ctx context.Context, p *authz.Principal, rid string, in OpenInput) (WriteResult, error) {
	driver, err := uuid.Parse(strings.TrimSpace(in.DriverID))
	if err != nil || driver == uuid.Nil {
		return WriteResult{}, httpx.ErrInvalidArgument(violation("driverId", "invalid"))
	}
	return s.open(ctx, p, rid, driver)
}

// MyOpen is POST /v1/mobile/chat: the driver's own chat, created when it has none.
func (s *Service) MyOpen(ctx context.Context, p *authz.Principal, rid string) (WriteResult, error) {
	driver, err := driverOf(p)
	if err != nil {
		return WriteResult{}, err
	}
	return s.open(ctx, p, rid, driver)
}

func (s *Service) open(ctx context.Context, p *authz.Principal, rid string, driver uuid.UUID) (WriteResult, error) {
	m, err := s.mode()
	if err != nil {
		return WriteResult{}, err
	}
	if m == modeWriteBack {
		return s.wbOpen(ctx, p, driver)
	}
	var out ChatDetail
	created := false
	err = s.tx(ctx, p, func(tx pgx.Tx, q *chatsdb.Queries) error {
		d, err := q.GetDriver(ctx, driver)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrInvalidArgument(violation("driverId", "not_found"))
		}
		if err != nil {
			return err
		}
		if err := q.LockDriverChats(ctx, driver); err != nil {
			return err
		}
		c, err := q.OpenChatOfDriver(ctx, driver)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			if c, err = q.InsertChat(ctx, chatsdb.InsertChatParams{ID: id, TenantID: d.TenantID, DriverID: driver}); err != nil {
				return err
			}
			created = true
			if err := chatEvent(ctx, tx, rid, RouteChatCreated, c, nil); err != nil {
				return err
			}
		case err != nil:
			return err
		}
		out, err = detail(ctx, q, p, c.ID)
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return WriteResult{Status: status, Data: out}, nil
}

// --- send ----------------------------------------------------------------------------------------------------------

// SendInput is the body of POST /v1/chats/{id}/messages and POST /v1/mobile/chat/messages: text and/or an image key
// from POST /v1/uploads/presign (purpose chat_image, entityId the chat), and the client's message id (a uuid, unique
// per chat, R63).
type SendInput struct {
	Text            *string `json:"text"`
	ImageKey        *string `json:"imageKey"`
	ClientMessageID string  `json:"clientMessageId"`
}

// send is a validated SendInput.
type send struct {
	text     *string
	imageKey string
	clientID uuid.UUID
}

func (in SendInput) check() (send, error) {
	var out send
	var bad []httpx.FieldViolation
	id, err := uuid.Parse(strings.TrimSpace(in.ClientMessageID))
	switch {
	case strings.TrimSpace(in.ClientMessageID) == "":
		bad = append(bad, violation("clientMessageId", "required"))
	case err != nil || id == uuid.Nil:
		bad = append(bad, violation("clientMessageId", "invalid"))
	}
	out.clientID = id
	if in.Text != nil {
		t, reason := cleanText(*in.Text)
		if reason != "" {
			p := httpx.FieldViolation{Field: "text", Reason: reason}
			if reason == "too_long" {
				p.Params = map[string]any{"max": MaxTextLength}
			}
			bad = append(bad, p)
		} else if t != "" {
			out.text = &t
		}
	}
	if in.ImageKey != nil {
		out.imageKey = strings.TrimSpace(*in.ImageKey)
	}
	if out.text == nil && out.imageKey == "" && len(bad) == 0 {
		bad = append(bad, violation("text", "required"))
	}
	if len(bad) > 0 {
		return send{}, httpx.ErrInvalidArgument(bad...)
	}
	return out, nil
}

// Send is POST /v1/chats/{id}/messages (staff, room/page.tsx:165-194 + notifyChatMessageCreated): one transaction stores
// the message, the chat's last-message fields, the auto-assignment of the first staff reply (assigned to the sender,
// in_progress, fn:chat.ts:104-116), the reopening of a closed chat by a staff reply (room/page.tsx:208-211) and the
// sender's read state, and appends chat.message_created (the one push to the driver, notify.fcm). A replay of the same
// clientMessageId answers the stored message (200) and writes nothing; the same id with another message is 409
// idempotency_conflict.
func (s *Service) Send(ctx context.Context, p *authz.Principal, rid string, chatID uuid.UUID, in SendInput) (WriteResult, error) {
	v, err := in.check()
	if err != nil {
		return WriteResult{}, err
	}
	if !staff(p) {
		return WriteResult{}, authz.ErrPermissionDenied("a staff role is required to write as an admin")
	}
	return s.send(ctx, p, rid, chatID, RoleAdmin, v)
}

// MySend is POST /v1/mobile/chat/messages (chat_repository.dart:97-160): the driver's message to its own chat, created
// when it has none.
func (s *Service) MySend(ctx context.Context, p *authz.Principal, rid string, in SendInput) (WriteResult, error) {
	driver, err := driverOf(p)
	if err != nil {
		return WriteResult{}, err
	}
	v, err := in.check()
	if err != nil {
		return WriteResult{}, err
	}
	// A replay of a send the driver made before staff closed that chat answers the stored message: it must neither open
	// a new chat nor store a second copy there (client_message_id is unique per chat only, R63).
	var prev *Message
	err = s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
		m, err := q.DriverMessageByClientID(ctx, chatsdb.DriverMessageByClientIDParams{DriverID: driver, ClientMessageID: v.clientID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out, err := replay(ctx, q, p, m, v)
		prev = &out
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	if prev != nil {
		out, err := s.signed(ctx, *prev)
		if err != nil {
			return WriteResult{}, err
		}
		return WriteResult{Status: http.StatusOK, Data: out}, nil
	}
	opened, err := s.open(ctx, p, rid, driver)
	if err != nil {
		return WriteResult{}, err
	}
	d, ok := opened.Data.(ChatDetail)
	if !ok {
		// A chat the write-back created that the mirror has not applied yet: the app's outbox retries the send.
		return WriteResult{}, httpx.ErrUnavailable("the driver's new chat is not mirrored yet").
			WithDetails(map[string]any{"reason": "write_back_pending"})
	}
	return s.send(ctx, p, rid, d.ID, RoleDriver, v)
}

func (s *Service) send(ctx context.Context, p *authz.Principal, rid string, chatID uuid.UUID, role string, v send) (WriteResult, error) {
	m, err := s.mode()
	if err != nil {
		return WriteResult{}, err
	}
	if v.imageKey != "" && s.files == nil {
		return WriteResult{}, httpx.ErrInvalidArgument(violation("imageKey", "not_found"))
	}
	if m == modeWriteBack {
		return s.wbSend(ctx, p, chatID, role, v)
	}
	var out Message
	created := false
	err = s.tx(ctx, p, func(tx pgx.Tx, q *chatsdb.Queries) error {
		c, err := q.LockChat(ctx, chatID)
		if err != nil {
			return notFound(err)
		}
		if role == RoleDriver && (c.DriverID == nil || *c.DriverID != *p.DriverID) {
			return httpx.ErrNotFound()
		}
		if prev, err := q.MessageByClientID(ctx, chatsdb.MessageByClientIDParams{ChatID: c.ID, ClientMessageID: v.clientID}); err == nil {
			out, err = replay(ctx, q, p, prev, v)
			return err
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		next := c
		if c.Status == StatusClosed {
			if role != RoleAdmin {
				return errChatClosed()
			}
			if err := reopenable(ctx, q, c); err != nil {
				return err
			}
			next.Status, next.ClosedAt = StatusInProgress, nil
		}
		var image *uuid.UUID
		if v.imageKey != "" {
			f, err := s.files.Commit(ctx, tx, storage.CommitInput{Key: v.imageKey, Field: "imageKey", Purposes: []string{"chat_image"},
				OwnerID: c.ID, UserID: p.UserID, TenantID: p.EffectiveTenant()})
			if err != nil {
				return err
			}
			image = &f.ID
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		msg, err := q.InsertMessage(ctx, chatsdb.InsertMessageParams{ID: id, ChatID: c.ID, SenderUserID: p.UserID, SenderRole: role,
			Text: v.text, ImageFileID: image, ClientMessageID: v.clientID})
		if errors.Is(err, pgx.ErrNoRows) { // a concurrent replay stored it first (the chat lock makes this rare)
			prev, err := q.MessageByClientID(ctx, chatsdb.MessageByClientIDParams{ChatID: c.ID, ClientMessageID: v.clientID})
			if err != nil {
				return err
			}
			out, err = replay(ctx, q, p, prev, v)
			return err
		}
		if err != nil {
			return err
		}
		created = true
		at := msg.CreatedAt
		pv, typ := preview(msg.Text), "normal"
		next.LastMessagePreview, next.LastMessageAt, next.LastMessageByUserID, next.LastMessageType = &pv, &at, &p.UserID, &typ
		if role == RoleAdmin && next.AssignedAdminUserID == nil {
			next.AssignedAdminUserID, next.AssignedAt, next.Status = &p.UserID, &at, StatusInProgress
		}
		after, err := setChat(ctx, q, next)
		if err != nil {
			return err
		}
		read, err := q.UpsertReadState(ctx, chatsdb.UpsertReadStateParams{ChatID: c.ID, UserID: p.UserID, LastReadAt: &at})
		if err != nil {
			return err
		}
		row, err := q.GetMessage(ctx, msg.ID)
		if err != nil {
			return err
		}
		out = messageOf(row.ChatMessage, row.SenderName)
		var driverUser *uuid.UUID
		if c.DriverID != nil {
			d, err := q.GetDriver(ctx, *c.DriverID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			driverUser = d.UserID
		}
		if err := messageEvent(ctx, tx, rid, after, driverUser, out); err != nil {
			return err
		}
		if changed := changedFields(c, after); slices.ContainsFunc(changed, statusField) {
			if err := chatEvent(ctx, tx, rid, RouteChatUpdated, after, changed); err != nil {
				return err
			}
		}
		return readEvent(ctx, tx, rid, after, p.UserID, read)
	})
	if err != nil {
		return WriteResult{}, err
	}
	if out, err = s.signed(ctx, out); err != nil {
		return WriteResult{}, err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return WriteResult{Status: status, Data: out}, nil
}

// signed is the message with its image URL signed (S3_PRESIGN_GET_TTL) when it has one.
func (s *Service) signed(ctx context.Context, m Message) (Message, error) {
	if m.ImageFileID == nil {
		return m, nil
	}
	msgs := []Message{m}
	if err := s.signImages(ctx, msgs, []uuid.UUID{*m.ImageFileID}); err != nil {
		return Message{}, err
	}
	return msgs[0], nil
}

// statusField reports a change the chat lists show apart from the last message (the status, the assignment, the
// priority): chat.updated on tenant:{tid}:chats. A new message alone is chat.message_created, which the lists follow too.
func statusField(f string) bool {
	switch f {
	case "status", "assignedAdminUserId", "priority", "closedAt", "assignedAt":
		return true
	}
	return false
}

// replay answers a send whose clientMessageId is stored already: the stored message when it is the same message (same
// sender, text and image presence), else 409 idempotency_conflict.
func replay(ctx context.Context, q *chatsdb.Queries, p *authz.Principal, prev chatsdb.ChatMessage, v send) (Message, error) {
	same := prev.SenderUserID != nil && *prev.SenderUserID == p.UserID && eqStr(prev.Text, v.text) &&
		(prev.ImageFileID != nil) == (v.imageKey != "")
	if !same {
		return Message{}, httpx.NewError(http.StatusConflict, CodeIdempotencyConflict,
			"the clientMessageId was used for another message").WithDetails(map[string]any{"field": "clientMessageId"})
	}
	row, err := q.GetMessage(ctx, prev.ID)
	if err != nil {
		return Message{}, err
	}
	return messageOf(row.ChatMessage, row.SenderName), nil
}

// reopenable refuses to reopen a chat while the driver has another chat that is not closed (409 already_exists,
// R14); the driver's chats are serialised by the advisory lock first.
func reopenable(ctx context.Context, q *chatsdb.Queries, c chatsdb.Chat) error {
	if c.DriverID == nil {
		return nil
	}
	if err := q.LockDriverChats(ctx, *c.DriverID); err != nil {
		return err
	}
	other, err := q.OtherOpenChat(ctx, chatsdb.OtherOpenChatParams{DriverID: *c.DriverID, ID: c.ID})
	switch {
	case err == nil:
		return errOpenChatExists(other)
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	}
	return err
}

func setChat(ctx context.Context, q *chatsdb.Queries, c chatsdb.Chat) (chatsdb.Chat, error) {
	return q.SetChat(ctx, chatsdb.SetChatParams{ID: c.ID, Status: c.Status, AssignedAdminUserID: c.AssignedAdminUserID,
		AssignedAt: c.AssignedAt, ClosedAt: c.ClosedAt, Priority: c.Priority, LastMessagePreview: c.LastMessagePreview,
		LastMessageAt: c.LastMessageAt, LastMessageByUserID: c.LastMessageByUserID, LastMessageType: c.LastMessageType})
}

// --- read state ----------------------------------------------------------------------------------------------------

// ReadInput is the body of POST /v1/chats/{id}/read and POST /v1/mobile/chat/read: the createdAt of the newest message
// the reader has seen (absent: now).
type ReadInput struct {
	LastReadAt *string `json:"lastReadAt"`
}

func (in ReadInput) at() (*time.Time, error) {
	if in.LastReadAt == nil || strings.TrimSpace(*in.LastReadAt) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*in.LastReadAt))
	if err != nil {
		return nil, httpx.ErrInvalidArgument(violation("lastReadAt", "invalid"))
	}
	return &t, nil
}

// MarkRead is POST /v1/chats/{id}/read: the caller's read state moves forward to lastReadAt (never past now), and
// chat.read tells the room (SSE read.updated) when it moved. It replaces the write the legacy room made in its snapshot
// callback (room/page.tsx:108-113). A read changes no chat: it is never written back to Firestore, and in write-back
// mode it stays in PostgreSQL only until the mirror applies the next lastReadByAdmin of the chat document.
func (s *Service) MarkRead(ctx context.Context, p *authz.Principal, rid string, chatID uuid.UUID, in ReadInput) error {
	at, err := in.at()
	if err != nil {
		return err
	}
	m, err := s.mode()
	if err != nil {
		return err
	}
	if m == modeWriteBack {
		return s.wbRead(ctx, p, chatID, at)
	}
	return s.tx(ctx, p, func(tx pgx.Tx, q *chatsdb.Queries) error {
		return markRead(ctx, tx, q, p, rid, chatID, at)
	})
}

// MyRead is POST /v1/mobile/chat/read (chat_repository.dart:66-74): the driver's open chat; nothing when it has none.
func (s *Service) MyRead(ctx context.Context, p *authz.Principal, rid string, in ReadInput) error {
	driver, err := driverOf(p)
	if err != nil {
		return err
	}
	var chatID uuid.UUID
	err = s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
		var err error
		chatID, err = openChat(ctx, q, driver)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.MarkRead(ctx, p, rid, chatID, in)
}

func markRead(ctx context.Context, tx pgx.Tx, q *chatsdb.Queries, p *authz.Principal, rid string, chatID uuid.UUID, at *time.Time) error {
	c, err := q.LockChat(ctx, chatID)
	if err != nil {
		return notFound(err)
	}
	prev, err := q.ReadStateOf(ctx, chatsdb.ReadStateOfParams{ChatID: c.ID, UserID: p.UserID})
	had := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	now, err := q.UpsertReadState(ctx, chatsdb.UpsertReadStateParams{ChatID: c.ID, UserID: p.UserID, LastReadAt: at})
	if err != nil {
		return err
	}
	if had && !now.After(prev) {
		return nil
	}
	return readEvent(ctx, tx, rid, c, p.UserID, now)
}

// --- assignment, close, reopen, priority ---------------------------------------------------------------------------

// AssignInput is the body of POST /v1/chats/{id}/assign: adminUserId absent = the caller, null = back to the queue
// (room/page.tsx:220-238 "return to queue"), a user id = that staff member of the chat's tenant.
type AssignInput struct {
	AdminUserID Field[string] `json:"adminUserId"`
}

// PatchInput is the body of PATCH /v1/chats/{id}.
type PatchInput struct {
	Priority Field[string] `json:"priority"`
}

// change computes the next state of a locked chat (the same row when nothing changes).
type change func(ctx context.Context, q *chatsdb.Queries, c chatsdb.Chat, now time.Time) (next chatsdb.Chat, err error)

// Assign is POST /v1/chats/{id}/assign: assigned (in_progress) or back to the queue (open, unassigned). A closed chat is
// 409 failed_precondition (reopen it first).
func (s *Service) Assign(ctx context.Context, p *authz.Principal, rid string, chatID uuid.UUID, in AssignInput) (WriteResult, error) {
	target := &p.UserID
	switch {
	case in.AdminUserID.Null:
		target = nil
	case in.AdminUserID.Set:
		id, err := uuid.Parse(strings.TrimSpace(in.AdminUserID.V))
		if err != nil || id == uuid.Nil {
			return WriteResult{}, httpx.ErrInvalidArgument(violation("adminUserId", "invalid"))
		}
		target = &id
	}
	return s.mutate(ctx, p, rid, chatID, func(ctx context.Context, q *chatsdb.Queries, c chatsdb.Chat, now time.Time) (chatsdb.Chat, error) {
		if c.Status == StatusClosed {
			return c, errChatClosed()
		}
		if target != nil && *target != p.UserID {
			ok, err := q.StaffMember(ctx, chatsdb.StaffMemberParams{UserID: *target, TenantID: c.TenantID})
			if err != nil {
				return c, err
			}
			if !ok {
				return c, httpx.ErrInvalidArgument(violation("adminUserId", "not_found"))
			}
		}
		if eqUUID(c.AssignedAdminUserID, target) {
			return c, nil
		}
		if target == nil {
			c.AssignedAdminUserID, c.AssignedAt, c.Status = nil, nil, StatusOpen
			return c, nil
		}
		c.AssignedAdminUserID, c.AssignedAt, c.Status = target, &now, StatusInProgress
		return c, nil
	})
}

// Close is POST /v1/chats/{id}/close (room/page.tsx:177-198): closed, the assignment kept; closing a closed chat changes
// nothing.
func (s *Service) Close(ctx context.Context, p *authz.Principal, rid string, chatID uuid.UUID) (WriteResult, error) {
	return s.mutate(ctx, p, rid, chatID, func(_ context.Context, _ *chatsdb.Queries, c chatsdb.Chat, now time.Time) (chatsdb.Chat, error) {
		if c.Status == StatusClosed {
			return c, nil
		}
		c.Status, c.ClosedAt = StatusClosed, &now
		return c, nil
	})
}

// Reopen is POST /v1/chats/{id}/reopen: in_progress when assigned, else open; 409 already_exists while the driver has
// another chat that is not closed (R14). Reopening an open chat changes nothing.
func (s *Service) Reopen(ctx context.Context, p *authz.Principal, rid string, chatID uuid.UUID) (WriteResult, error) {
	return s.mutate(ctx, p, rid, chatID, func(ctx context.Context, q *chatsdb.Queries, c chatsdb.Chat, _ time.Time) (chatsdb.Chat, error) {
		if c.Status != StatusClosed {
			return c, nil
		}
		if err := reopenable(ctx, q, c); err != nil {
			return c, err
		}
		c.Status, c.ClosedAt = StatusOpen, nil
		if c.AssignedAdminUserID != nil {
			c.Status = StatusInProgress
		}
		return c, nil
	})
}

// Patch is PATCH /v1/chats/{id} {priority: normal|urgent}.
func (s *Service) Patch(ctx context.Context, p *authz.Principal, rid string, chatID uuid.UUID, in PatchInput) (WriteResult, error) {
	pr := strings.TrimSpace(in.Priority.V)
	if !in.Priority.Set || in.Priority.Null || !validPriority(pr) {
		return WriteResult{}, httpx.ErrInvalidArgument(violation("priority", "invalid"))
	}
	return s.mutate(ctx, p, rid, chatID, func(_ context.Context, _ *chatsdb.Queries, c chatsdb.Chat, _ time.Time) (chatsdb.Chat, error) {
		c.Priority = pr
		return c, nil
	})
}

// mutate locks a chat, applies fn and, when the chat changed, writes it and appends chat.updated; it answers the chat
// as GET /v1/chats/{id} shows it.
func (s *Service) mutate(ctx context.Context, p *authz.Principal, rid string, chatID uuid.UUID, fn change) (WriteResult, error) {
	if !staff(p) {
		return WriteResult{}, authz.ErrPermissionDenied("a staff role is required")
	}
	m, err := s.mode()
	if err != nil {
		return WriteResult{}, err
	}
	if m == modeWriteBack {
		return s.wbMutate(ctx, p, chatID, fn)
	}
	var out ChatDetail
	err = s.tx(ctx, p, func(tx pgx.Tx, q *chatsdb.Queries) error {
		c, err := q.LockChat(ctx, chatID)
		if err != nil {
			return notFound(err)
		}
		now, err := q.TxNow(ctx)
		if err != nil {
			return err
		}
		next, err := fn(ctx, q, c, now)
		if err != nil {
			return err
		}
		if changed := changedFields(c, next); len(changed) > 0 {
			after, err := setChat(ctx, q, next)
			if err != nil {
				return err
			}
			if err := chatEvent(ctx, tx, rid, RouteChatUpdated, after, changedFields(c, after)); err != nil {
				return err
			}
		}
		out, err = detail(ctx, q, p, chatID)
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Status: http.StatusOK, Data: out}, nil
}
