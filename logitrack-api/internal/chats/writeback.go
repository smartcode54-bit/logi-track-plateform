package chats

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats/chatsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox"
)

// The class C write-back of the chat writes (main spec §11.10, §12.1, §13.7-§13.8). While comms is not in
// PG_OWNED_DOMAINS the installed 3.x APKs and the legacy web keep writing chats in Firestore, so Firestore stays the
// only writer: a chat write Go serves goes to the Firestore documents first, in the legacy shape (legacy.go), and the
// FS->PG mirror fills PostgreSQL. The handler then waits up to WriteBack.Wait (3 s) for the mirror's read-your-writes
// acknowledgement (cache:mirror_ack:{collection}:{docId}, R53) and answers like the PostgreSQL path, else 202 with
// the document it wrote (WriteBackEcho). The chats and messages collections have listeners (etl.ListenFields), so
// the acknowledgement normally comes within the listener's interval.
//
//   - A message document's id is the client's message id: a replay writes the same document (create-only: a second
//     write of it is a replay, never a second message), and the mirror stores clientMessageId, so a later replay is
//     answered from PostgreSQL. A message with an image uploaded to Go's storage is 422 write_back_unsupported (the 3.x
//     app reads Firebase Storage URLs only).
//   - The push is the writer's: the handler appends its own chat.message_created (header origin=writeback, no realtime
//     topic, the message in the payload, since its row exists only once the mirror applied it); the mirror's event of
//     the same message (origin=etl.sync) feeds SSE and pushes nothing (internal/notify).
//   - The documents of one request are written in order (the message, then its chat), each with its precondition; a
//     failure answers 503 unavailable (details.reason write_back) and the client retries with the same
//     clientMessageId. writeback.Writer.Commit (T31) makes them one atomic commit once it lands.
//
// Reads always come from PostgreSQL (the mirror). Switching the write-back on in an environment is an operator step
// with the coexistence runbook; the staging VM has no Firestore and runs with comms in PG_OWNED_DOMAINS.

// DefaultMirrorWait is how long a write-back handler waits for the mirror's acknowledgement (main spec §13.7, R53).
const DefaultMirrorWait = 3 * time.Second

// OriginWriteBack is the outbox header origin of the push event a write-back handler appends (the mirror's own event
// carries origin=etl.sync and pushes nothing).
const OriginWriteBack = "writeback"

// MirrorAcks waits for the mirror's acknowledgement (cache.Cache.WaitMirrorAck): true once the mirror applied the
// document at or after want, false when the timeout passed first.
type MirrorAcks interface {
	WaitMirrorAck(ctx context.Context, collection, docID string, want time.Time, timeout time.Duration) (bool, error)
}

// WriteBack is the Firestore-first path of the chat writes.
type WriteBack struct {
	// Writer writes the Firestore documents (GCPWriter over the REST client; Memory in tests).
	Writer DocWriter
	// Acks is the mirror acknowledgement; nil answers every write-back 202.
	Acks MirrorAcks
	// Wait bounds the wait for the acknowledgement; 0 = DefaultMirrorWait.
	Wait time.Duration
}

// Doc is one document write: a masked merge of exactly Fields (a nil value deletes the field). Create writes only when
// the document does not exist (ErrDocExists), Update only when it does (ErrDocMissing).
type Doc struct {
	Path   string
	Fields map[string]any
	Create bool
	Update bool
}

// DocWriter writes one document and returns its update time.
type DocWriter interface {
	Write(ctx context.Context, d Doc) (time.Time, error)
}

// Errors of a precondition.
var (
	ErrDocExists  = errors.New("chats: the document exists")
	ErrDocMissing = errors.New("chats: the document does not exist")
)

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

func errWriteBack(cause error) error {
	return httpx.NewError(http.StatusServiceUnavailable, httpx.CodeUnavailable, "the Firestore write-back failed").
		WithDetails(map[string]any{"reason": "write_back"}).Wrap(cause)
}

func (w *WriteBack) wait(ctx context.Context, collection, docID string, at time.Time) bool {
	if w.Acks == nil {
		return false
	}
	d := w.Wait
	if d <= 0 {
		d = DefaultMirrorWait
	}
	ok, err := w.Acks.WaitMirrorAck(ctx, collection, docID, at, d)
	return err == nil && ok
}

// echoFields is the legacy document as JSON (times RFC 3339).
func echoFields(f map[string]any) map[string]any {
	out := make(map[string]any, len(f))
	for k, v := range f {
		switch x := v.(type) {
		case time.Time:
			out[k] = x.UTC()
		case map[string]any:
			out[k] = echoFields(x)
		default:
			out[k] = v
		}
	}
	return out
}

// wbState is what the write-back reads from PostgreSQL (the mirror) before it writes: the chat, the legacy ids of its
// driver and users, and the staff read states (lastReadByAdmin).
type wbState struct {
	chat       chatsdb.Chat
	docID      string
	driverRef  string
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

func (st wbState) ref(id uuid.UUID) string {
	if r, ok := st.refs[id]; ok {
		return r
	}
	return id.String()
}

// wbOpen is Open on a Firestore-owned domain: the driver's open chat when the mirror holds one, else a new chat
// document (a minted uuidv7 as its id, R25).
func (s *Service) wbOpen(ctx context.Context, p *authz.Principal, driver uuid.UUID) (WriteResult, error) {
	var existing uuid.UUID
	var ref, name string
	err := s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
		d, err := q.GetDriver(ctx, driver)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrInvalidArgument(violation("driverId", "not_found"))
		}
		if err != nil {
			return err
		}
		name = d.Name
		if ref, err = q.DriverRef(ctx, driver); err != nil {
			return err
		}
		existing, err = openChat(ctx, q, driver)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	if existing != uuid.Nil {
		out, err := s.Get(ctx, p, existing)
		return WriteResult{Status: http.StatusOK, Data: out}, err
	}
	if ref == "" {
		return WriteResult{}, httpx.ErrInvalidArgument(violation("driverId", "no_login"))
	}
	id, err := uuid.NewV7()
	if err != nil {
		return WriteResult{}, err
	}
	at := time.Now().UTC()
	fields := LegacyNewChat(ref, name, at)
	ut, err := s.wb.Writer.Write(ctx, Doc{Path: ChatPath(id.String()), Fields: fields, Create: true})
	if err != nil {
		return WriteResult{}, errWriteBack(err)
	}
	if s.wb.wait(ctx, CollChats, id.String(), ut) {
		var pg uuid.UUID
		err := s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
			var err error
			pg, err = q.ChatIDByLegacyDoc(ctx, id.String())
			return err
		})
		if err == nil {
			out, err := s.Get(ctx, p, pg)
			return WriteResult{Status: http.StatusCreated, Data: out}, err
		}
	}
	return WriteResult{Status: http.StatusAccepted, Data: WriteBackEcho{Pending: true, ID: id, Collection: CollChats,
		DocID: id.String(), UpdateTime: ut, Document: echoFields(fields)}}, nil
}

// wbSend is send on a Firestore-owned domain.
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
	if stored != nil {
		return WriteResult{Status: http.StatusOK, Data: *stored}, nil
	}
	c := st.chat
	at := time.Now().UTC()
	sender := st.ref(p.UserID)
	msgDoc := v.clientID.String()
	msg := LegacyMessage(sender, role, v.text, at, v.clientID)
	ut, err := s.wb.Writer.Write(ctx, Doc{Path: MessagePath(st.docID, msgDoc), Fields: msg, Create: true})
	switch {
	case errors.Is(err, ErrDocExists):
		// Written by an earlier attempt whose chat update failed: the chat update and the push follow again.
	case err != nil:
		return WriteResult{}, errWriteBack(err)
	}
	reads := maps.Clone(st.adminReads)
	if role == RoleAdmin {
		reads[sender] = at
	}
	assign := role == RoleAdmin && c.AssignedAdminUserID == nil
	if _, err := s.wb.Writer.Write(ctx, Doc{Path: ChatPath(st.docID), Update: true,
		Fields: LegacyLastMessage(role, sender, v.text, at, reads, assign, reopen)}); err != nil {
		return WriteResult{}, errWriteBack(err)
	}
	// The push of the writer (notify.fcm): the message is in the payload, its row exists once the mirror applied it.
	err = s.tx(ctx, p, func(tx pgx.Tx, _ *chatsdb.Queries) error {
		payload := map[string]any{"entity": EntityMessage, "chatId": c.ID, "tenantId": c.TenantID, "at": at,
			"chatRef": st.docID, "messageRef": msgDoc, "senderUserId": p.UserID, "senderRole": role, "text": v.text,
			"clientMessageId": v.clientID, "driverId": c.DriverID, "assignedAdminUserId": c.AssignedAdminUserID, "writeBack": true}
		if assign {
			payload["assignedAdminUserId"] = p.UserID
		}
		_, err := outbox.Append(ctx, tx, outbox.Event{RoutingKey: RouteMessageCreated, AggregateType: AggregateChat,
			AggregateID: c.ID.String(), TenantID: &c.TenantID, Payload: payload,
			Headers: map[string]string{outbox.HeaderOrigin: OriginWriteBack}})
		return err
	})
	if err != nil {
		return WriteResult{}, err
	}
	if !ut.IsZero() && s.wb.wait(ctx, CollMessages, msgDoc, ut) {
		var row chatsdb.ChatMessage
		err := s.tx(ctx, p, func(_ pgx.Tx, q *chatsdb.Queries) error {
			var err error
			row, err = q.MessageByLegacyDoc(ctx, chatsdb.MessageByLegacyDocParams{ChatID: c.ID, LegacyDocID: msgDoc})
			if err != nil {
				return err
			}
			r, err := q.GetMessage(ctx, row.ID)
			if err == nil {
				m := messageOf(r.ChatMessage, r.SenderName)
				stored = &m
			}
			return err
		})
		if err == nil && stored != nil {
			return WriteResult{Status: http.StatusCreated, Data: *stored}, nil
		}
	}
	return WriteResult{Status: http.StatusAccepted, Data: WriteBackEcho{Pending: true, ID: v.clientID,
		Collection: ChatPath(st.docID) + "/" + CollMessages, DocID: msgDoc, UpdateTime: ut, Document: echoFields(msg)}}, nil
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
	ut, err := s.wb.Writer.Write(ctx, Doc{Path: ChatPath(st.docID), Update: true, Fields: LegacyRead(role, when, reads)})
	if err != nil {
		return errWriteBack(err)
	}
	s.wb.wait(ctx, CollChats, st.docID, ut)
	return nil
}

// wbMutate is mutate on a Firestore-owned domain: the change is computed from the mirrored row and written to the chat
// document (only the fields it changed).
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
	ut, err := s.wb.Writer.Write(ctx, Doc{Path: ChatPath(st.docID), Update: true, Fields: fields})
	if err != nil {
		return WriteResult{}, errWriteBack(err)
	}
	if s.wb.wait(ctx, CollChats, st.docID, ut) {
		out, err := s.Get(ctx, p, chatID)
		return WriteResult{Status: http.StatusOK, Data: out}, err
	}
	return WriteResult{Status: http.StatusAccepted, Data: WriteBackEcho{Pending: true, ID: chatID, Collection: CollChats,
		DocID: st.docID, UpdateTime: ut, Document: echoFields(fields)}}, nil
}

// --- the Firestore REST adapter --------------------------------------------------------------------------------------

// GCPWriter is the DocWriter over the Firestore REST client of internal/etl/gcp (a masked PATCH per document, with
// currentDocument.exists as the precondition).
type GCPWriter struct{ FS *gcp.Firestore }

// Write implements DocWriter.
func (w GCPWriter) Write(ctx context.Context, d Doc) (time.Time, error) {
	if w.FS == nil {
		return time.Time{}, errors.New("chats: no Firestore client")
	}
	if d.Path == "" || strings.Count(d.Path, "/")%2 == 0 {
		return time.Time{}, fmt.Errorf("chats: %q is not a document path", d.Path)
	}
	fields := map[string]any{}
	mask := make([]string, 0, len(d.Fields))
	for _, k := range slices.Sorted(maps.Keys(d.Fields)) {
		mask = append(mask, k)
		v, err := dumpValue(d.Fields[k])
		if err != nil {
			return time.Time{}, fmt.Errorf("chats: %s.%s: %w", d.Path, k, err)
		}
		if v != nil {
			fields[k] = v
		}
	}
	var pre gcp.Precondition
	switch {
	case d.Create:
		no := false
		pre.Exists = &no
	case d.Update:
		yes := true
		pre.Exists = &yes
	}
	doc, err := w.FS.Patch(ctx, d.Path, fields, mask, pre)
	switch {
	case d.Create && errors.Is(err, gcp.ErrPrecondition):
		return time.Time{}, ErrDocExists
	case d.Update && (errors.Is(err, gcp.ErrNotFound) || errors.Is(err, gcp.ErrPrecondition)):
		return time.Time{}, ErrDocMissing
	case err != nil:
		return time.Time{}, err
	}
	return doc.UpdateTime, nil
}

// dumpValue maps a field value onto the value types of the Firestore client (nil = delete the field).
func dumpValue(v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case string, bool, int64, float64:
		return x, nil
	case int:
		return int64(x), nil
	case time.Time:
		return dump.Timestamp{Time: x.UTC()}, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			ev, err := dumpValue(e)
			if err != nil {
				return nil, err
			}
			if ev != nil {
				out[k] = ev
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported value type %T", v)
}

// --- the in-memory Firestore of tests --------------------------------------------------------------------------------

// Memory is the in-memory DocWriter of tests: documents by path, the accepted writes in order, a logical clock (one
// microsecond per write). OnWrite, when set, is called after each accepted write (outside the lock), so a test can play
// the mirror; Fail makes every write fail.
type Memory struct {
	mu      sync.Mutex
	docs    map[string]map[string]any
	writes  []Doc
	fail    error
	clock   time.Time
	OnWrite func(d Doc, at time.Time)
}

// NewMemory returns an empty Memory.
func NewMemory() *Memory {
	return &Memory{docs: map[string]map[string]any{}, clock: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
}

// Write implements DocWriter.
func (m *Memory) Write(_ context.Context, d Doc) (time.Time, error) {
	m.mu.Lock()
	if m.fail != nil {
		err := m.fail
		m.mu.Unlock()
		return time.Time{}, err
	}
	for k, v := range d.Fields {
		if _, err := dumpValue(v); err != nil {
			m.mu.Unlock()
			return time.Time{}, fmt.Errorf("chats: %s.%s: %w", d.Path, k, err)
		}
	}
	doc, exists := m.docs[d.Path]
	switch {
	case d.Create && exists:
		m.mu.Unlock()
		return time.Time{}, ErrDocExists
	case d.Update && !exists:
		m.mu.Unlock()
		return time.Time{}, ErrDocMissing
	}
	if doc == nil {
		doc = map[string]any{}
		m.docs[d.Path] = doc
	}
	for k, v := range d.Fields {
		if v == nil {
			delete(doc, k)
			continue
		}
		doc[k] = v
	}
	m.clock = m.clock.Add(time.Microsecond)
	at := m.clock
	m.writes = append(m.writes, Doc{Path: d.Path, Fields: maps.Clone(d.Fields), Create: d.Create, Update: d.Update})
	hook := m.OnWrite
	m.mu.Unlock()
	if hook != nil {
		hook(d, at)
	}
	return at, nil
}

// Put stores a document as it stands in Firestore (a fixture).
func (m *Memory) Put(path string, fields map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[path] = maps.Clone(fields)
}

// Doc returns a copy of the document at path.
func (m *Memory) Doc(path string) (map[string]any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[path]
	return maps.Clone(d), ok
}

// Writes returns the accepted writes in order.
func (m *Memory) Writes() []Doc {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.writes)
}

// Fail makes every following write return err (nil restores normal operation).
func (m *Memory) Fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail = err
}
