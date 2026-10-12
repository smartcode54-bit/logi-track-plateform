package chats

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats/chatsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func ptr[T any](v T) *T { return &v }

func fields(t *testing.T, err error) []httpx.FieldViolation {
	t.Helper()
	e := httpx.AsError(err)
	if e == nil || e.Status != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %v", err)
	}
	f, _ := e.Details["fields"].([]httpx.FieldViolation)
	return f
}

func TestSendInputCheck(t *testing.T) {
	id := uuid.NewString()
	v, err := SendInput{Text: ptr("  hello  "), ClientMessageID: id}.check()
	if err != nil || *v.text != "hello" || v.clientID.String() != id || v.imageKey != "" {
		t.Fatalf("%+v %v", v, err)
	}
	if v, err = (SendInput{ImageKey: ptr("chats/x/1.jpg"), ClientMessageID: id}).check(); err != nil || v.text != nil ||
		v.imageKey != "chats/x/1.jpg" {
		t.Fatalf("image only: %+v %v", v, err)
	}
	for name, c := range map[string]struct {
		in   SendInput
		want string
	}{
		"no client id":    {SendInput{Text: ptr("x")}, "clientMessageId:required"},
		"bad client id":   {SendInput{Text: ptr("x"), ClientMessageID: "nope"}, "clientMessageId:invalid"},
		"nothing to send": {SendInput{Text: ptr("   "), ClientMessageID: id}, "text:required"},
		"too long":        {SendInput{Text: ptr(strings.Repeat("ก", MaxTextLength+1)), ClientMessageID: id}, "text:too_long"},
		"not utf-8":       {SendInput{Text: ptr("a\xffb"), ClientMessageID: id}, "text:invalid"},
	} {
		_, err := c.in.check()
		f := fields(t, err)
		if len(f) == 0 || f[0].Field+":"+f[0].Reason != c.want {
			t.Errorf("%s: %+v", name, f)
		}
	}
	if _, err := (SendInput{Text: ptr(strings.Repeat("ก", MaxTextLength)), ClientMessageID: id}).check(); err != nil {
		t.Errorf("the longest text: %v", err)
	}
}

func TestPreview(t *testing.T) {
	if preview(nil) != ImagePreview || preview(ptr("")) != ImagePreview || preview(ptr("hi")) != "hi" {
		t.Fatal("preview")
	}
	long := strings.Repeat("ข", MaxPreviewLength+10)
	if got := preview(&long); len([]rune(got)) != MaxPreviewLength {
		t.Fatalf("preview cut %d", len([]rune(got)))
	}
}

func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 11, 3, 4, 5, 123456000, time.UTC)
	id := uuid.New()
	for _, c := range []cursor{{T: &at, ID: id}, {ID: id}} {
		got, ok := decodeCursor(c.encode())
		if !ok || got.ID != id || (c.T == nil) != (got.T == nil) || (c.T != nil && !got.T.Equal(*c.T)) {
			t.Fatalf("%+v -> %+v %v", c, got, ok)
		}
	}
	for _, bad := range []string{"", "!!", "e30", "eyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCJ9"} {
		if _, ok := decodeCursor(bad); ok {
			t.Errorf("%q decoded", bad)
		}
	}
}

func TestParseLimit(t *testing.T) {
	for raw, want := range map[string]int{"": DefaultLimit, "1": 1, "50": 50, "500": 500} {
		if n, err := parseLimit(raw); err != nil || n != want {
			t.Errorf("%q: %d %v", raw, n, err)
		}
	}
	for _, raw := range []string{"0", "501", "-1", "x", "99999999999999999999"} {
		if _, err := parseLimit(raw); err == nil {
			t.Errorf("%q accepted", raw)
		}
	}
}

func TestChangedFields(t *testing.T) {
	u := uuid.New()
	at := time.Now()
	before := chatsdb.Chat{Status: StatusOpen, Priority: PriorityNormal}
	after := before
	if got := changedFields(before, after); len(got) != 0 {
		t.Fatalf("no change: %v", got)
	}
	after.Status, after.AssignedAdminUserID, after.AssignedAt, after.Priority = StatusInProgress, &u, &at, PriorityUrgent
	got := changedFields(before, after)
	if strings.Join(got, ",") != "assignedAdminUserId,assignedAt,priority,status" {
		t.Fatalf("%v", got)
	}
}

// The legacy shapes the 3.x app and the legacy web read (lib/chat.ts, with-driver/page.tsx, chat_repository.dart).
func TestLegacyEncoders(t *testing.T) {
	at := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	c := LegacyNewChat("uidD", "", at)
	if c[fDriverID] != "uidD" || c[fDriverDisplayName] != "Driver" || c[fStatus] != "open" || c[fPriority] != "normal" ||
		c[fLastMessage] != "" || c[fLastMessageAt] != at || c[fCreatedAt] != at || c[fAssignedAdminID] != nil {
		t.Fatalf("new chat %v", c)
	}
	if m, ok := c[fLastReadByAdmin].(map[string]any); !ok || len(m) != 0 {
		t.Fatalf("lastReadByAdmin %v", c[fLastReadByAdmin])
	}
	cid := uuid.New()
	msg := LegacyMessage("uidA", RoleAdmin, ptr("hi"), at, cid)
	if msg[fSenderID] != "uidA" || msg[fSenderRole] != "admin" || msg[fText] != "hi" || msg[fCreatedAt] != at ||
		msg[fClientMessageID] != cid.String() || len(msg) != 5 {
		t.Fatalf("message %v", msg)
	}
	if LegacyMessage("uidD", RoleDriver, nil, at, cid)[fText] != "" {
		t.Fatal("an image without text keeps text empty (chat_repository.dart:150)")
	}

	reads := map[string]time.Time{"uidB": at.Add(-time.Hour), "uidA": at}
	first := LegacyLastMessage(RoleAdmin, "uidA", ptr("hello"), at, reads, true, false)
	if first[fLastMessage] != "hello" || first[fLastMessageBy] != "uidA" || first[fAssignedAdminID] != "uidA" ||
		first[fStatus] != StatusInProgress || first[fAssignedAt] != at || first[fUpdatedAt] != at {
		t.Fatalf("first admin reply %v", first)
	}
	if m := first[fLastReadByAdmin].(map[string]any); len(m) != 2 || m["uidA"] != at {
		t.Fatalf("lastReadByAdmin %v", m)
	}
	reopen := LegacyLastMessage(RoleAdmin, "uidA", nil, at, reads, false, true)
	if v, ok := reopen[fClosedAt]; !ok || v != nil || reopen[fStatus] != StatusInProgress || reopen[fLastMessage] != ImagePreview {
		t.Fatalf("reopening reply %v", reopen)
	}
	if _, ok := reopen[fAssignedAdminID]; ok {
		t.Fatal("an assigned chat keeps its assignment")
	}
	driver := LegacyLastMessage(RoleDriver, "uidD", ptr("ok"), at, nil, false, false)
	if driver[fLastReadByDriver] != at || driver[fLastReadByAdmin] != nil || driver[fStatus] != nil {
		t.Fatalf("driver message %v", driver)
	}

	u := uuid.New()
	refs := map[uuid.UUID]string{u: "uidA"}
	ch := LegacyChatChange(legacyChat{status: StatusOpen, priority: PriorityNormal},
		legacyChat{status: StatusInProgress, priority: PriorityNormal, assigned: &u, assignedAt: &at}, refs, at)
	if len(ch) != 4 || ch[fStatus] != StatusInProgress || ch[fAssignedAdminID] != "uidA" || ch[fAssignedAt] != at || ch[fUpdatedAt] != at {
		t.Fatalf("assign %v", ch)
	}
	back := LegacyChatChange(legacyChat{status: StatusInProgress, priority: PriorityNormal, assigned: &u, assignedAt: &at},
		legacyChat{status: StatusOpen, priority: PriorityNormal}, refs, at)
	if v, ok := back[fAssignedAdminID]; !ok || v != nil || back[fStatus] != StatusOpen {
		t.Fatalf("return to queue %v", back)
	}
	if len(LegacyChatChange(legacyChat{status: StatusOpen, priority: PriorityNormal}, legacyChat{status: StatusOpen, priority: PriorityNormal}, refs, at)) != 0 {
		t.Fatal("no change writes nothing")
	}
	if r := LegacyRead(RoleDriver, at, nil); r[fLastReadByDriver] != at || len(r) != 2 {
		t.Fatalf("driver read %v", r)
	}
	if r := LegacyRead(RoleAdmin, at, reads); len(r[fLastReadByAdmin].(map[string]any)) != 2 {
		t.Fatalf("admin read %v", r)
	}
}

func TestMemoryPreconditions(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	if _, err := m.Write(ctx, Doc{Path: "chats/c1", Fields: map[string]any{"status": "open"}, Update: true}); !errors.Is(err, ErrDocMissing) {
		t.Fatalf("update of a missing doc: %v", err)
	}
	t1, err := m.Write(ctx, Doc{Path: "chats/c1", Fields: map[string]any{"status": "open", "x": "y"}, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Write(ctx, Doc{Path: "chats/c1", Fields: map[string]any{"status": "open"}, Create: true}); !errors.Is(err, ErrDocExists) {
		t.Fatalf("second create: %v", err)
	}
	t2, err := m.Write(ctx, Doc{Path: "chats/c1", Fields: map[string]any{"status": "closed", "x": nil}, Update: true})
	if err != nil || !t2.After(t1) {
		t.Fatalf("update %v %v", t2, err)
	}
	if d, _ := m.Doc("chats/c1"); d["status"] != "closed" || d["x"] != nil {
		t.Fatalf("doc %v", d)
	}
	m.Fail(errors.New("down"))
	if _, err := m.Write(ctx, Doc{Path: "chats/c2", Fields: map[string]any{}}); err == nil {
		t.Fatal("Fail")
	}
	if len(m.Writes()) != 2 {
		t.Fatalf("writes %d", len(m.Writes()))
	}
}

// The REST adapter against the in-process fake Google: create-only and update-only preconditions, a masked merge that
// keeps the fields it does not name, deletes a nil field, stores instants as timestamps and maps as maps.
func TestGCPWriter(t *testing.T) {
	ctx := context.Background()
	b := gcptest.New(t, "chats-test")
	w := GCPWriter{FS: b.Firestore()}
	at := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	ut, err := w.Write(ctx, Doc{Path: "chats/c1", Fields: LegacyNewChat("uidD", "สมชาย", at), Create: true})
	if err != nil || ut.IsZero() {
		t.Fatalf("create: %v %v", ut, err)
	}
	if _, err := w.Write(ctx, Doc{Path: "chats/c1", Fields: map[string]any{"status": "open"}, Create: true}); !errors.Is(err, ErrDocExists) {
		t.Fatalf("second create: %v", err)
	}
	if _, err := w.Write(ctx, Doc{Path: "chats/nope", Fields: map[string]any{"status": "open"}, Update: true}); !errors.Is(err, ErrDocMissing) {
		t.Fatalf("update of a missing doc: %v", err)
	}
	reads := map[string]time.Time{"uidA": at}
	if _, err := w.Write(ctx, Doc{Path: "chats/c1", Update: true,
		Fields: LegacyLastMessage(RoleAdmin, "uidA", ptr("hi"), at, reads, true, false)}); err != nil {
		t.Fatal(err)
	}
	d, ok := b.Doc("chats/c1")
	if !ok || d.Fields["status"] != StatusInProgress || d.Fields["assignedAdminId"] != "uidA" || d.Fields["driverId"] != "uidD" ||
		d.Fields["driverDisplayName"] != "สมชาย" || d.Fields["lastMessage"] != "hi" {
		t.Fatalf("chat %v", d.Fields)
	}
	if ts, ok := d.Fields["lastMessageAt"].(dump.Timestamp); !ok || !ts.Equal(at) {
		t.Fatalf("lastMessageAt %#v", d.Fields["lastMessageAt"])
	}
	if m, ok := d.Fields["lastReadByAdmin"].(map[string]any); !ok || len(m) != 1 {
		t.Fatalf("lastReadByAdmin %#v", d.Fields["lastReadByAdmin"])
	}
	if _, err := w.Write(ctx, Doc{Path: "chats/c1/messages/" + uuid.NewString(), Create: true,
		Fields: LegacyMessage("uidA", RoleAdmin, ptr("hi"), at, uuid.New())}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(ctx, Doc{Path: "chats", Fields: map[string]any{}}); err == nil {
		t.Fatal("a collection path is not a document")
	}
	if _, err := w.Write(ctx, Doc{Path: "chats/c1", Fields: map[string]any{"bad": struct{}{}}}); err == nil {
		t.Fatal("an unsupported value is refused")
	}
	if _, err := (GCPWriter{}).Write(ctx, Doc{Path: "chats/c1"}); err == nil {
		t.Fatal("no client")
	}
}
