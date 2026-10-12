package chats

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats/chatsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// ListInput is the query of GET /v1/chats.
type ListInput struct {
	Filter   string // queue (default) | mine | all
	Status   string
	DriverID string
	Limit    int
	Cursor   string
}

// List is GET /v1/chats: the caller's reach, newest message first, keyset paged (Appendix B §B.2.16).
func (s *Service) List(ctx context.Context, p *authz.Principal, in ListInput) ([]Chat, string, error) {
	var bad []httpx.FieldViolation
	filter := in.Filter
	if filter == "" {
		filter = FilterQueue
	}
	if !validFilter(filter) {
		bad = append(bad, violation("filter", "invalid"))
	}
	var status *string
	if in.Status != "" {
		if !validStatus(in.Status) {
			bad = append(bad, violation("status", "invalid"))
		}
		status = &in.Status
	}
	var driver *uuid.UUID
	if in.DriverID != "" {
		id, err := uuid.Parse(in.DriverID)
		if err != nil {
			bad = append(bad, violation("driverId", "invalid"))
		}
		driver = &id
	}
	var after cursor
	hasCursor := in.Cursor != ""
	if hasCursor {
		c, ok := decodeCursor(in.Cursor)
		if !ok {
			bad = append(bad, violation("cursor", "invalid"))
		}
		after = c
	}
	if len(bad) > 0 {
		return nil, "", httpx.ErrInvalidArgument(bad...)
	}
	limit := in.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	all, tenants := p.TenantReach()
	var rows []chatsdb.ListChatsRow
	err := s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
		var err error
		rows, err = q.ListChats(ctx, chatsdb.ListChatsParams{OtherRole: otherRole(p), UserID: p.UserID, AllTenants: all,
			TenantIds: tenants, Filter: filter, Status: status, DriverID: driver, HasCursor: hasCursor, CursorAt: after.T,
			CursorID: after.ID, RowLimit: int32(limit + 1)})
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1].Chat
		next = cursor{T: last.LastMessageAt, ID: last.ID}.encode()
	}
	out := make([]Chat, 0, len(rows))
	for _, r := range rows {
		out = append(out, chatOf(r.Chat, r.DriverName, r.DriverUserID, r.DriverLegacyUid, r.AssignedAdminName, r.MyLastReadAt, r.Unread))
	}
	return out, next, nil
}

func validFilter(f string) bool { return slices.Contains(Filters, f) }

// Get is GET /v1/chats/{id}: the chat and its read state (404 outside the caller's reach).
func (s *Service) Get(ctx context.Context, p *authz.Principal, id uuid.UUID) (ChatDetail, error) {
	var out ChatDetail
	err := s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
		var err error
		out, err = detail(ctx, q, p, id)
		return err
	})
	return out, err
}

// detail reads a chat as GET /v1/chats/{id} answers it, in the caller's transaction.
func detail(ctx context.Context, q *chatsdb.Queries, p *authz.Principal, id uuid.UUID) (ChatDetail, error) {
	r, err := q.GetChatView(ctx, chatsdb.GetChatViewParams{OtherRole: otherRole(p), UserID: p.UserID, ID: id})
	if err != nil {
		return ChatDetail{}, notFound(err)
	}
	reads, err := q.ChatReadStates(ctx, id)
	if err != nil {
		return ChatDetail{}, err
	}
	out := ChatDetail{Chat: chatOf(r.Chat, r.DriverName, r.DriverUserID, r.DriverLegacyUid, r.AssignedAdminName, r.MyLastReadAt,
		r.Unread), Reads: make([]Read, 0, len(reads))}
	for _, rs := range reads {
		out.Reads = append(out.Reads, Read{UserID: rs.UserID, LastReadAt: rs.LastReadAt, IsDriver: rs.IsDriver})
		if rs.IsDriver {
			at := rs.LastReadAt
			out.DriverLastReadAt = &at
		}
	}
	return out, nil
}

// Messages is GET /v1/chats/{id}/messages?before&limit: newest first, keyset (created_at, id); 404 when the chat is
// outside the caller's reach.
func (s *Service) Messages(ctx context.Context, p *authz.Principal, chatID uuid.UUID, before string, limit int) ([]Message, string, error) {
	var at cursor
	if before != "" {
		c, ok := decodeCursor(before)
		if !ok || c.T == nil {
			return nil, "", httpx.ErrInvalidArgument(violation("before", "invalid"))
		}
		at = c
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	var rows []chatsdb.ListMessagesRow
	err := s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
		if _, err := q.GetChatView(ctx, chatsdb.GetChatViewParams{OtherRole: otherRole(p), UserID: p.UserID, ID: chatID}); err != nil {
			return notFound(err)
		}
		var err error
		rows, err = q.ListMessages(ctx, chatsdb.ListMessagesParams{ChatID: chatID, BeforeAt: at.T, BeforeID: at.ID,
			RowLimit: int32(limit + 1)})
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return s.page(ctx, rows, limit)
}

// page turns message rows into a page (rows holds one more than limit when there is a next page) and signs the image
// URLs, after the request transaction closed (storage.Service.SignedFiles).
func (s *Service) page(ctx context.Context, rows []chatsdb.ListMessagesRow, limit int) ([]Message, string, error) {
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1].ChatMessage
		at := last.CreatedAt
		next = cursor{T: &at, ID: last.ID}.encode()
	}
	out := make([]Message, 0, len(rows))
	var images []uuid.UUID
	for _, r := range rows {
		out = append(out, messageOf(r.ChatMessage, r.SenderName))
		if r.ChatMessage.ImageFileID != nil {
			images = append(images, *r.ChatMessage.ImageFileID)
		}
	}
	if err := s.signImages(ctx, out, images); err != nil {
		return nil, "", err
	}
	return out, next, nil
}

// signImages sets imageUrl of the messages whose image is committed (a missing file keeps a null URL).
func (s *Service) signImages(ctx context.Context, msgs []Message, ids []uuid.UUID) error {
	if s.files == nil || len(ids) == 0 {
		return nil
	}
	signed, err := s.files.SignedFiles(ctx, ids, 0)
	if err != nil {
		return err
	}
	for i := range msgs {
		if id := msgs[i].ImageFileID; id != nil {
			if f, ok := signed[*id]; ok {
				u := f.URL
				msgs[i].ImageURL = &u
			}
		}
	}
	return nil
}

// --- the driver's own chat (/v1/mobile/chat*) --------------------------------------------------------------------

// MyChat is GET /v1/mobile/chat: the driver's chat that is not closed, with its read state; 404 when none.
func (s *Service) MyChat(ctx context.Context, p *authz.Principal) (ChatDetail, error) {
	driver, err := driverOf(p)
	if err != nil {
		return ChatDetail{}, err
	}
	var out ChatDetail
	err = s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
		c, err := openChat(ctx, q, driver)
		if err != nil {
			return notFound(err)
		}
		out, err = detail(ctx, q, p, c)
		return err
	})
	return out, err
}

// openChat is the id of the driver's chat that is not closed (pgx.ErrNoRows when none).
func openChat(ctx context.Context, q *chatsdb.Queries, driver uuid.UUID) (uuid.UUID, error) {
	return q.OpenChatID(ctx, driver)
}

// MyMessages is GET /v1/mobile/chat/messages?before&limit: the messages of the driver's open chat (an empty page when
// the driver has none).
func (s *Service) MyMessages(ctx context.Context, p *authz.Principal, before string, limit int) ([]Message, string, error) {
	driver, err := driverOf(p)
	if err != nil {
		return nil, "", err
	}
	var chatID uuid.UUID
	err = s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
		var err error
		chatID, err = openChat(ctx, q, driver)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return []Message{}, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return s.Messages(ctx, p, chatID, before, limit)
}

// MyUnread is GET /v1/mobile/chat/unread-count.
func (s *Service) MyUnread(ctx context.Context, p *authz.Principal) (UnreadCount, error) {
	driver, err := driverOf(p)
	if err != nil {
		return UnreadCount{}, err
	}
	var out UnreadCount
	err = s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
		r, err := q.DriverUnread(ctx, chatsdb.DriverUnreadParams{UserID: p.UserID, DriverID: driver})
		out = UnreadCount{Count: r.Chats, Messages: r.Messages}
		return err
	})
	return out, err
}
