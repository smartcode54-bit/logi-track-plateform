package chats

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats/chatsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The class C write-back of the chat writes (main spec §11.10, §12.1, §13.7-§13.8). While comms is not in
// PG_OWNED_DOMAINS the installed 3.x APKs and the legacy web keep writing chats in Firestore, so Firestore stays the
// only writer: a chat write Go serves goes to the Firestore documents first, in the legacy shape (legacy.go), through
// the write-back port of T31 (writeback.Writer: one documents:commit per request, all of its documents or none), and
// the FS->PG mirror fills PostgreSQL. The handler then waits up to WriteBack.Wait (3 s) for the mirror's
// read-your-writes acknowledgement (cache:mirror_ack:{collection}:{docId}, R53) and answers like the PostgreSQL path,
// else 202 with the document it wrote (WriteBackEcho). The chats and messages collections have listeners
// (etl.ListenFields), so the acknowledgement normally comes within the listener's interval.
//
//   - A message is one commit: the message document (create-only; its id is the client's message id) and its chat's
//     last-message fields, auto-assignment and reopening (update-only). A replay of the clientMessageId lands on the same
//     document, before or after the mirror loaded it (the mirror stores clientMessageId): the same message is answered
//     as stored, another one under the same id is 409 idempotency_conflict, and nothing is written. A message with an
//     image uploaded to Go's storage is 422 write_back_unsupported (the 3.x app reads Firebase Storage URLs only).
//   - The push is the writer's: the handler appends its own chat.message_created after the commit (header
//     origin=writeback, no realtime topic, the message in the payload, since its row exists only once the mirror
//     applied it); the mirror's event of the same message (origin=etl.sync) feeds SSE and pushes nothing
//     (internal/notify). A replay younger than ReplayPushWindow appends it again, so a push whose append failed after
//     the commit still goes out; notify.fcm sends one message once per device whatever the number of its events (the
//     first event that pushes it holds idem:push:chat:{chatRef}:{messageRef}).
//   - The driver's chat is found or created under the driver's advisory lock (the PostgreSQL path's lock): the open chat
//     the mirror holds, else a chat document of the driver that is not closed and that the mirror has not applied yet
//     (writeback.Writer.Find on driverId: an earlier attempt, a concurrent open, the 3.x app's), else a new document (a
//     minted uuidv7 as its id, R25). Retries and concurrent opens land on one chat document while the mirror lags (one
//     open chat per driver, R14), and a driver's message goes into that document at once.
//   - The legacy writers store assignedAdminId, assignedAt and closedAt as null and the legacy queue reads
//     where('assignedAdminId', '==', null), which matches only a stored null: a nil of the encoders is written as
//     writeback.Null, never as a deleted field.
//   - A precondition refused for another reason (the chat document is gone) is 409 failed_precondition
//     (details.reason document_changed); any other Firestore failure is 503 unavailable (details.reason write_back),
//     and the client retries with the same clientMessageId.
//
// Reads always come from PostgreSQL (the mirror). Switching the write-back on in an environment is an operator step
// with the coexistence runbook; the staging VM has no Firestore and runs with comms in PG_OWNED_DOMAINS.

// DefaultMirrorWait is how long a write-back handler waits for the mirror's acknowledgement (main spec §13.7, R53).
const DefaultMirrorWait = 3 * time.Second

// ReplayPushWindow bounds the replays that append their push again: a send replayed within it (a client's retry of a
// send whose answer or push event was lost) appends chat.message_created once more, which notify.fcm deduplicates per
// message for cache.ChatPushTTL (24 h); an older replay appends nothing, so a stale message is never pushed late.
const ReplayPushWindow = time.Hour

// MirrorAcks waits for the mirror's acknowledgement (cache.Cache.WaitMirrorAck): true once the mirror applied the
// document at or after want, false when the timeout passed first.
type MirrorAcks interface {
	WaitMirrorAck(ctx context.Context, collection, docID string, want time.Time, timeout time.Duration) (bool, error)
}

// WriteBack is the Firestore-first path of the chat writes.
type WriteBack struct {
	// Writer writes the Firestore documents (writeback.Firestore over the REST client; writeback.Memory in tests).
	Writer writeback.Writer
	// Acks is the mirror acknowledgement; nil answers every write-back 202.
	Acks MirrorAcks
	// Wait bounds the wait for the acknowledgement; 0 = DefaultMirrorWait.
	Wait time.Duration
}

// WriteBackEcho is the 202 data of a write-back the mirror did not acknowledge within the wait (main spec §11.10): the
// PostgreSQL id when the row is known, the Firestore document written, its update time and the fields written in the
// legacy shape.
type WriteBackEcho struct {
	Pending    bool           `json:"pending"`
	ID         uuid.UUID      `json:"id"`
	Collection string         `json:"collection"`
	DocID      string         `json:"docId"`
	UpdateTime time.Time      `json:"updateTime"`
	Document   map[string]any `json:"document"`
}

// writeBackError maps a failed commit: a refused precondition is 409 document_changed, anything else 503 write_back.
func writeBackError(err error) error {
	if errors.Is(err, writeback.ErrConflict) {
		return errDocumentChanged().Wrap(err)
	}
	return writeback.ErrUnavailable(err)
}

// errPushPending is a message committed in Firestore whose push event could not be queued: the client retries the
// send with the same clientMessageId, and the replay appends it (ReplayPushWindow).
func errPushPending(cause error) error {
	return httpx.NewError(http.StatusServiceUnavailable, httpx.CodeUnavailable,
		"the message is written; its notification is not queued yet: retry the send").
		WithDetails(map[string]any{"reason": "write_back"}).Wrap(cause)
}

func (w *WriteBack) wait(ctx context.Context, collection, docID string, at time.Time) bool {
	if w.Acks == nil || at.IsZero() {
		return false
	}
	d := w.Wait
	if d <= 0 {
		d = DefaultMirrorWait
	}
	ok, err := w.Acks.WaitMirrorAck(ctx, collection, docID, at, d)
	return err == nil && ok
}

// legacyDoc is a document of the encoders as the write-back commits it: a nil value is a Firestore null
// (writeback.Null), since every legacy chat writer stores null and none deletes a chat field.
func legacyDoc(f map[string]any) map[string]any {
	out := make(map[string]any, len(f))
	for k, v := range f {
		if v == nil {
			out[k] = writeback.Null
			continue
		}
		out[k] = v
	}
	return out
}

// echoFields is the legacy document as JSON (times RFC 3339).
func echoFields(f map[string]any) map[string]any {
	out := make(map[string]any, len(f))
	for k, v := range f {
		switch x := v.(type) {
		case time.Time:
			out[k] = x.UTC()
		case dump.Timestamp:
			out[k] = x.UTC()
		case map[string]any:
			out[k] = echoFields(x)
		default:
			out[k] = v
		}
	}
	return out
}

// docTime reads a timestamp field of a document as the Firestore client (dump.Timestamp) or writeback.Memory
// (time.Time) holds it.
func docTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case dump.Timestamp:
		return x.Time, true
	}
	return time.Time{}, false
}

// messagesOf is the messages collection of a chat document.
func messagesOf(chatDoc string) string { return ChatPath(chatDoc) + "/" + CollMessages }

// wbState is what the write-back reads from PostgreSQL (the mirror) before it writes: the chat, the legacy ids of its
// driver and users, and the staff read states (lastReadByAdmin).
type wbState struct {
	chat       chatsdb.Chat
	docID      string
	driverUser *uuid.UUID
	refs       map[uuid.UUID]string
	adminReads map[string]time.Time
}

// load reads a chat and the legacy ids the write-back needs, in the caller's rights (404 outside them).
func (s *Service) load(ctx context.Context, p *authz.Principal, chatID uuid.UUID, fn func(q *chatsdb.Queries, st *wbState) error) (wbState, error) {
	var st wbState
	err := s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
		v, err := q.GetChatView(ctx, chatsdb.GetChatViewParams{OtherRole: otherRole(p), UserID: p.UserID, ID: chatID})
		if err != nil {
			return notFound(err)
		}
		st.chat, st.driverUser = v.Chat, v.DriverUserID
		st.docID = st.chat.ID.String()
		if st.chat.LegacyDocID != nil {
			st.docID = *st.chat.LegacyDocID
		}
		reads, err := q.ChatReadStates(ctx, chatID)
		if err != nil {
			return err
		}
		ids := []uuid.UUID{p.UserID}
		for _, r := range reads {
			ids = append(ids, r.UserID)
		}
		if st.chat.AssignedAdminUserID != nil {
			ids = append(ids, *st.chat.AssignedAdminUserID)
		}
		refs, err := q.UserRefs(ctx, ids)
		if err != nil {
			return err
		}
		st.refs = map[uuid.UUID]string{}
		for _, r := range refs {
			st.refs[r.ID] = r.Ref
		}
		st.adminReads = map[string]time.Time{}
		for _, r := range reads {
			if ref, ok := st.refs[r.UserID]; ok && !r.IsDriver {
				st.adminReads[ref] = r.LastReadAt
			}
		}
		if fn != nil {
			return fn(q, &st)
		}
		return nil
	})
	return st, err
}

func (st wbState) ref(id uuid.UUID) string { return st.target().ref(id) }

// target is the mirrored chat as a message write-back writes to it.
func (st wbState) target() wbChat {
	c := st.chat
	return wbChat{id: c.ID, docID: st.docID, tenant: c.TenantID, driver: c.DriverID, assigned: c.AssignedAdminUserID,
		reads: st.adminReads, refs: st.refs}
}

// wbChat is the chat a write-back message goes to: its row (uuid.Nil while the mirror has not applied the document),
// its document, tenant and driver, the assignee, the staff read states the mirror holds (lastReadByAdmin) and the
// users' legacy ids.
type wbChat struct {
	id       uuid.UUID
	docID    string
	tenant   uuid.UUID
	driver   *uuid.UUID
	assigned *uuid.UUID
	reads    map[string]time.Time
	refs     map[uuid.UUID]string
}

func (c wbChat) ref(id uuid.UUID) string {
	if r, ok := c.refs[id]; ok {
		return r
	}
	return id.String()
}

// aggregate is the outbox aggregate id of the chat: its row id, else (not mirrored yet) its document id, which is the
// row id the mirror will keep for a document Go created (R25).
func (c wbChat) aggregate() string {
	if c.id != uuid.Nil {
		return c.id.String()
	}
	return c.docID
}

// driverChat is the driver's chat a write-back open or driver send writes to.
type driverChat struct {
	row        uuid.UUID      // the open chat the mirror holds (uuid.Nil: none)
	tenant     uuid.UUID      // the driver's tenant
	docID      string         // else the chat document,
	fields     map[string]any // its fields as read or written,
	updateTime time.Time      // its update time,
	created    bool           // and whether this call created it
	sender     string         // the caller's legacy id (senderId)
}

// wbDriverChat finds or creates the driver's chat on a Firestore-owned domain, holding the driver's advisory lock
// throughout (the PostgreSQL path's LockDriverChats): the open chat the mirror holds; else the first chat document of
// the driver (driverId = its Firebase uid) that is not closed, which the mirror has not applied yet (an earlier attempt
// of this open, a concurrent one, or the 3.x app's); else a new document. Retries and concurrent opens therefore
// converge on one chat document while the mirror lags (one open chat per driver, R14).
func (s *Service) wbDriverChat(ctx context.Context, p *authz.Principal, driver uuid.UUID) (driverChat, error) {
	var dc driverChat
	err := s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
		d, err := q.GetDriver(ctx, driver)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrInvalidArgument(violation("driverId", "not_found"))
		}
		if err != nil {
			return err
		}
		dc.tenant = d.TenantID
		if err := q.LockDriverChats(ctx, driver); err != nil {
			return err
		}
		switch id, err := openChat(ctx, q, driver); {
		case err == nil:
			dc.row = id
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		ref, err := q.DriverRef(ctx, driver)
		if err != nil {
			return err
		}
		if ref == "" {
			return httpx.ErrInvalidArgument(violation("driverId", "no_login"))
		}
		refs, err := q.UserRefs(ctx, []uuid.UUID{p.UserID})
		if err != nil {
			return err
		}
		dc.sender = p.UserID.String()
		for _, r := range refs {
			dc.sender = r.Ref
		}
		docs, err := s.wb.Writer.Find(ctx, CollChats, fDriverID, ref)
		if err != nil {
			return writeback.ErrUnavailable(err)
		}
		for _, doc := range docs {
			if st, _ := doc.Fields[fStatus].(string); st != StatusClosed {
				dc.docID, dc.fields, dc.updateTime = doc.ID, doc.Fields, doc.UpdateTime
				return nil
			}
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		fields := LegacyNewChat(ref, d.Name, time.Now().UTC())
		ut, err := s.wb.Writer.Commit(ctx, writeback.DocWrite{Collection: CollChats, DocID: id.String(), Fields: legacyDoc(fields),
			MustNotExist: true})
		if err != nil {
			return writeBackError(err)
		}
		dc.docID, dc.fields, dc.updateTime, dc.created = id.String(), fields, ut, true
		return nil
	})
	return dc, err
}

// wbOpen is Open on a Firestore-owned domain: the driver's chat wbDriverChat finds or creates, answered as the mirror
// holds it (200 an existing chat, 201 a new one), else 202 with its document.
func (s *Service) wbOpen(ctx context.Context, p *authz.Principal, driver uuid.UUID) (WriteResult, error) {
	dc, err := s.wbDriverChat(ctx, p, driver)
	if err != nil {
		return WriteResult{}, err
	}
	if dc.row != uuid.Nil {
		out, err := s.Get(ctx, p, dc.row)
		return WriteResult{Status: http.StatusOK, Data: out}, err
	}
	status := http.StatusOK
	if dc.created {
		status = http.StatusCreated
	}
	if s.wb.wait(ctx, CollChats, dc.docID, dc.updateTime) {
		var pg uuid.UUID
		err := s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
			var err error
			pg, err = q.ChatIDByLegacyDoc(ctx, dc.docID)
			return err
		})
		if err == nil {
			out, err := s.Get(ctx, p, pg)
			return WriteResult{Status: status, Data: out}, err
		}
	}
	id, _ := uuid.Parse(dc.docID)
	return WriteResult{Status: http.StatusAccepted, Data: WriteBackEcho{Pending: true, ID: id, Collection: CollChats,
		DocID: dc.docID, UpdateTime: dc.updateTime, Document: echoFields(dc.fields)}}, nil
}

// wbMySend is the driver's send on a Firestore-owned domain to the chat wbDriverChat finds or creates: the mirrored
// chat takes wbSend; a chat document the mirror has not applied yet takes the message at once (no retry loop).
func (s *Service) wbMySend(ctx context.Context, p *authz.Principal, driver uuid.UUID, v send) (WriteResult, error) {
	if v.imageKey != "" {
		return WriteResult{}, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "imageKey", Reason: ReasonWriteBackUnsupported})
	}
	dc, err := s.wbDriverChat(ctx, p, driver)
	if err != nil {
		return WriteResult{}, err
	}
	if dc.row != uuid.Nil {
		return s.wbSend(ctx, p, dc.row, RoleDriver, v)
	}
	return s.wbMessage(ctx, p, wbChat{docID: dc.docID, tenant: dc.tenant, driver: &driver,
		refs: map[uuid.UUID]string{p.UserID: dc.sender}}, RoleDriver, v, false)
}

// wbSend is send on a Firestore-owned domain, to a chat the mirror holds. A replay the mirror loaded already is
// answered from its row (200) and its push appended again while it is young; anything else is wbMessage.
func (s *Service) wbSend(ctx context.Context, p *authz.Principal, chatID uuid.UUID, role string, v send) (WriteResult, error) {
	if v.imageKey != "" {
		return WriteResult{}, httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "imageKey", Reason: ReasonWriteBackUnsupported})
	}
	var stored *Message
	reopen := false
	st, err := s.load(ctx, p, chatID, func(q *chatsdb.Queries, st *wbState) error {
		c := st.chat
		if role == RoleDriver && (c.DriverID == nil || *c.DriverID != *p.DriverID) {
			return httpx.ErrNotFound()
		}
		prev, err := q.MessageByClientID(ctx, chatsdb.MessageByClientIDParams{ChatID: c.ID, ClientMessageID: v.clientID})
		switch {
		case err == nil:
			m, err := replay(ctx, q, p, prev, v)
			stored = &m
			return err
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		if c.Status == StatusClosed {
			if role != RoleAdmin {
				return errChatClosed()
			}
			if c.DriverID != nil {
				other, err := q.OtherOpenChat(ctx, chatsdb.OtherOpenChatParams{DriverID: *c.DriverID, ID: c.ID})
				if err == nil {
					return errOpenChatExists(other)
				}
				if !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
			}
			reopen = true
		}
		return nil
	})
	if err != nil {
		return WriteResult{}, err
	}
	wc := st.target()
	if stored != nil {
		if time.Since(stored.CreatedAt) < ReplayPushWindow {
			ref := stored.ID.String()
			if stored.LegacyDocID != nil {
				ref = *stored.LegacyDocID
			}
			if err := s.wbPush(ctx, p, wc, wbPushMsg{ref: ref, clientID: stored.ClientMessageID, role: stored.SenderRole,
				text: stored.Text, at: stored.CreatedAt, admin: wc.assigned}); err != nil {
				return WriteResult{}, errPushPending(err)
			}
		}
		out, err := s.signed(ctx, *stored)
		if err != nil {
			return WriteResult{}, err
		}
		return WriteResult{Status: http.StatusOK, Data: out}, nil
	}
	return s.wbMessage(ctx, p, wc, role, v, reopen)
}

// wbMessage writes a message in one commit (the message document, create-only, and its chat's update, update-only),
// then appends the writer's push. A refused commit is wbReplay.
func (s *Service) wbMessage(ctx context.Context, p *authz.Principal, wc wbChat, role string, v send, reopen bool) (WriteResult, error) {
	at := time.Now().UTC()
	sender := wc.ref(p.UserID)
	msgDoc := v.clientID.String()
	msg := LegacyMessage(sender, role, v.text, at, v.clientID)
	reads := maps.Clone(wc.reads)
	if reads == nil {
		reads = map[string]time.Time{}
	}
	if role == RoleAdmin {
		reads[sender] = at
	}
	assign := role == RoleAdmin && wc.assigned == nil
	ut, err := s.wb.Writer.Commit(ctx,
		writeback.DocWrite{Collection: messagesOf(wc.docID), DocID: msgDoc, Fields: legacyDoc(msg), MustNotExist: true},
		writeback.DocWrite{Collection: CollChats, DocID: wc.docID, MustExist: true,
			Fields: legacyDoc(LegacyLastMessage(role, sender, v.text, at, reads, assign, reopen))})
	switch {
	case errors.Is(err, writeback.ErrConflict):
		return s.wbReplay(ctx, p, wc, role, v)
	case err != nil:
		return WriteResult{}, writeback.ErrUnavailable(err)
	}
	admin := wc.assigned
	if assign {
		admin = &p.UserID
	}
	if err := s.wbPush(ctx, p, wc, wbPushMsg{ref: msgDoc, clientID: &v.clientID, role: role, text: v.text, at: at,
		admin: admin}); err != nil {
		return WriteResult{}, errPushPending(err)
	}
	return s.wbAnswer(ctx, p, wc, msgDoc, ut, msg, http.StatusCreated)
}

// wbReplay answers a send whose commit was refused. When the message document exists, the send is a replay: the
// stored document is answered (its chat update was in the same commit) when it is the same message (sender, role,
// text, client message id, no image), else 409 idempotency_conflict, and nothing is written; a young replay appends
// its push again. When it does not exist, the chat document is gone: 409 document_changed.
func (s *Service) wbReplay(ctx context.Context, p *authz.Principal, wc wbChat, role string, v send) (WriteResult, error) {
	msgDoc := v.clientID.String()
	doc, ut, ok, err := s.wb.Writer.Get(ctx, messagesOf(wc.docID), msgDoc)
	switch {
	case err != nil:
		return WriteResult{}, writeback.ErrUnavailable(err)
	case !ok:
		return WriteResult{}, errDocumentChanged()
	}
	if !sameLegacyMessage(doc, wc.ref(p.UserID), role, v) {
		return WriteResult{}, errIdempotencyConflict()
	}
	at, ok := docTime(doc[fCreatedAt])
	if !ok {
		at = ut
	}
	if time.Since(at) < ReplayPushWindow {
		if err := s.wbPush(ctx, p, wc, wbPushMsg{ref: msgDoc, clientID: &v.clientID, role: role, text: v.text, at: at,
			admin: wc.assigned}); err != nil {
			return WriteResult{}, errPushPending(err)
		}
	}
	return s.wbAnswer(ctx, p, wc, msgDoc, ut, doc, http.StatusOK)
}

// sameLegacyMessage reports whether a stored message document is the send replayed: the same sender, role, text and
// client message id, and no image (the write-back sends none).
func sameLegacyMessage(doc map[string]any, sender, role string, v send) bool {
	text := ""
	if v.text != nil {
		text = *v.text
	}
	str := func(k string) string { s, _ := doc[k].(string); return s }
	return str(fSenderID) == sender && str(fSenderRole) == role && str(fText) == text &&
		str(fClientMessageID) == v.clientID.String() && str(fImageURL) == ""
}

// wbAnswer answers a written or replayed message: the mirrored row once the mirror acknowledged the message document
// within the wait, else 202 with the document.
func (s *Service) wbAnswer(ctx context.Context, p *authz.Principal, wc wbChat, msgDoc string, ut time.Time,
	doc map[string]any, status int) (WriteResult, error) {
	if s.wb.wait(ctx, CollMessages, msgDoc, ut) {
		var stored *Message
		err := s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
			chat := wc.id
			if chat == uuid.Nil {
				var err error
				if chat, err = q.ChatIDByLegacyDoc(ctx, wc.docID); err != nil {
					return err
				}
			}
			row, err := q.MessageByLegacyDoc(ctx, chatsdb.MessageByLegacyDocParams{ChatID: chat, LegacyDocID: msgDoc})
			if err != nil {
				return err
			}
			r, err := q.GetMessage(ctx, row.ID)
			if err != nil {
				return err
			}
			m := messageOf(r.ChatMessage, r.SenderName)
			stored = &m
			return nil
		})
		if err == nil && stored != nil {
			out, err := s.signed(ctx, *stored)
			return WriteResult{Status: status, Data: out}, err
		}
	}
	id, _ := uuid.Parse(msgDoc)
	return WriteResult{Status: http.StatusAccepted, Data: WriteBackEcho{Pending: true, ID: id, Collection: messagesOf(wc.docID),
		DocID: msgDoc, UpdateTime: ut, Document: echoFields(doc)}}, nil
}

// wbPushMsg is the message a write-back push names.
type wbPushMsg struct {
	ref      string     // the message document id (messageRef)
	clientID *uuid.UUID // the client's message id
	role     string
	text     *string
	at       time.Time
	admin    *uuid.UUID // the chat's assignee after the write
}

// wbPush appends the writer's chat.message_created (header origin=writeback, no realtime topic): notify.fcm pushes
// from its payload, since the message's row exists only once the mirror applied it, and pushes one message once per
// device however often it is appended (idem:push:chat).
func (s *Service) wbPush(ctx context.Context, p *authz.Principal, wc wbChat, m wbPushMsg) error {
	return s.tx(ctx, p, func(tx pgx.Tx, _ *chatsdb.Queries) error {
		payload := map[string]any{"entity": EntityMessage, "tenantId": wc.tenant, "at": m.at.UTC(), "chatRef": wc.docID,
			"messageRef": m.ref, "senderUserId": p.UserID, "senderRole": m.role, "text": m.text, "clientMessageId": m.clientID,
			"driverId": wc.driver, "assignedAdminUserId": m.admin, "writeBack": true}
		if wc.id != uuid.Nil {
			payload["chatId"] = wc.id
		}
		_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteMessageCreated, AggregateType: AggregateChat,
			AggregateID: wc.aggregate(), TenantID: &wc.tenant, Payload: payload,
			Headers: map[string]string{outbox.HeaderOrigin: outbox.OriginWriteBack}})
		return err
	})
}

// wbRead is MarkRead on a Firestore-owned domain: lastReadByDriver, or the whole lastReadByAdmin map (LegacyRead).
// The read state only moves forward; nothing is written when it would not.
func (s *Service) wbRead(ctx context.Context, p *authz.Principal, chatID uuid.UUID, at *time.Time) error {
	role := RoleAdmin
	if p.EffectiveRole() == authz.Driver {
		role = RoleDriver
	}
	var prev *time.Time
	st, err := s.load(ctx, p, chatID, func(q *chatsdb.Queries, st *wbState) error {
		t, err := q.ReadStateOf(ctx, chatsdb.ReadStateOfParams{ChatID: st.chat.ID, UserID: p.UserID})
		if err == nil {
			prev = &t
			return nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	when := now
	if at != nil && at.Before(now) {
		when = at.UTC()
	}
	if prev != nil && !when.After(*prev) {
		return nil
	}
	reads := maps.Clone(st.adminReads)
	reads[st.ref(p.UserID)] = when
	ut, err := s.wb.Writer.Commit(ctx, writeback.DocWrite{Collection: CollChats, DocID: st.docID, MustExist: true,
		Fields: legacyDoc(LegacyRead(role, when, reads))})
	if err != nil {
		return writeBackError(err)
	}
	s.wb.wait(ctx, CollChats, st.docID, ut)
	return nil
}

// wbMutate is mutate on a Firestore-owned domain: the change is computed from the mirrored row and written to the chat
// document (only the fields it changed; a cleared assignment is a stored null).
func (s *Service) wbMutate(ctx context.Context, p *authz.Principal, chatID uuid.UUID, fn change) (WriteResult, error) {
	now := time.Now().UTC()
	var next chatsdb.Chat
	st, err := s.load(ctx, p, chatID, func(q *chatsdb.Queries, st *wbState) error {
		var err error
		next, err = fn(ctx, q, st.chat, now)
		if err != nil || next.AssignedAdminUserID == nil {
			return err
		}
		if _, ok := st.refs[*next.AssignedAdminUserID]; ok {
			return nil
		}
		refs, err := q.UserRefs(ctx, []uuid.UUID{*next.AssignedAdminUserID})
		for _, r := range refs {
			st.refs[r.ID] = r.Ref
		}
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	c := st.chat
	fields := LegacyChatChange(
		legacyChat{status: c.Status, priority: c.Priority, assigned: c.AssignedAdminUserID, assignedAt: c.AssignedAt, closedAt: c.ClosedAt},
		legacyChat{status: next.Status, priority: next.Priority, assigned: next.AssignedAdminUserID, assignedAt: next.AssignedAt, closedAt: next.ClosedAt},
		st.refs, now)
	if len(fields) == 0 {
		out, err := s.Get(ctx, p, chatID)
		return WriteResult{Status: http.StatusOK, Data: out}, err
	}
	ut, err := s.wb.Writer.Commit(ctx, writeback.DocWrite{Collection: CollChats, DocID: st.docID, Fields: legacyDoc(fields),
		MustExist: true})
	if err != nil {
		return WriteResult{}, writeBackError(err)
	}
	if s.wb.wait(ctx, CollChats, st.docID, ut) {
		out, err := s.Get(ctx, p, chatID)
		return WriteResult{Status: http.StatusOK, Data: out}, err
	}
	return WriteResult{Status: http.StatusAccepted, Data: WriteBackEcho{Pending: true, ID: chatID, Collection: CollChats,
		DocID: st.docID, UpdateTime: ut, Document: echoFields(fields)}}, nil
}
