package chats

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats/chatsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
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

// The legacy writers store null (with-driver/page.tsx:52-54, room/page.tsx:236, chat_repository.dart:33-35) and the
// legacy queue reads where('assignedAdminId', '==', null): every nil of the encoders is committed as a Firestore null,
// in the Firestore REST adapter of the write-back port as in its Memory, never as a deleted field.
func TestLegacyNullsAreStoredNulls(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	u := uuid.New()
	refs := map[uuid.UUID]string{u: "uidA"}
	back := LegacyChatChange(legacyChat{status: StatusInProgress, priority: PriorityNormal, assigned: &u, assignedAt: &at},
		legacyChat{status: StatusOpen, priority: PriorityNormal}, refs, at)
	if d := legacyDoc(LegacyNewChat("uidD", "สมชาย", at)); d[fAssignedAdminID] != writeback.Null || d[fClosedAt] != writeback.Null ||
		d[fStatus] != StatusOpen {
		t.Fatalf("legacyDoc %v", d)
	}
	b := gcptest.New(t, "chats-test")
	for name, w := range map[string]writeback.Writer{"firestore": writeback.Firestore{FS: b.Firestore()}, "memory": writeback.NewMemory()} {
		if _, err := w.Commit(ctx, writeback.DocWrite{Collection: CollChats, DocID: "c1-" + name, MustNotExist: true,
			Fields: legacyDoc(LegacyNewChat("uidD", "สมชาย", at))}); err != nil {
			t.Fatalf("%s: create %v", name, err)
		}
		doc, _, ok, err := w.Get(ctx, CollChats, "c1-"+name)
		if err != nil || !ok {
			t.Fatalf("%s: get %v %v", name, ok, err)
		}
		for _, k := range []string{fAssignedAdminID, fAssignedAt, fClosedAt} {
			if v, ok := doc[k]; !ok || v != nil {
				t.Errorf("%s: new chat %s = %v (present %v), want a stored null", name, k, v, ok)
			}
		}
		if _, err := w.Commit(ctx, writeback.DocWrite{Collection: CollChats, DocID: "c1-" + name, MustExist: true,
			Fields: legacyDoc(LegacyLastMessage(RoleAdmin, "uidA", ptr("hi"), at, nil, true, false))}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Commit(ctx, writeback.DocWrite{Collection: CollChats, DocID: "c1-" + name, MustExist: true,
			Fields: legacyDoc(back)}); err != nil {
			t.Fatal(err)
		}
		doc, _, _, _ = w.Get(ctx, CollChats, "c1-"+name)
		if v, ok := doc[fAssignedAdminID]; !ok || v != nil || doc[fStatus] != StatusOpen || doc[fDriverID] != "uidD" {
			t.Errorf("%s: back to the queue %v", name, doc)
		}
	}
	// The PATCH body itself names the field with a null value.
	var named bool
	for _, wr := range b.Writes() {
		if v, ok := wr.Fields[fAssignedAdminID]; ok && v == nil {
			named = true
		}
	}
	if !named {
		t.Fatal("no commit wrote assignedAdminId as a null")
	}
}

// A replay is the same message only with the same sender, role, text and client message id, and no image.
func TestSameLegacyMessage(t *testing.T) {
	at := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	cid := uuid.New()
	doc := LegacyMessage("uidA", RoleAdmin, ptr("hi"), at, cid)
	v := send{text: ptr("hi"), clientID: cid}
	if !sameLegacyMessage(doc, "uidA", RoleAdmin, v) {
		t.Fatal("the same message")
	}
	for name, c := range map[string]struct {
		sender, role string
		v            send
	}{
		"another sender":    {"uidB", RoleAdmin, v},
		"another role":      {"uidA", RoleDriver, v},
		"another text":      {"uidA", RoleAdmin, send{text: ptr("ho"), clientID: cid}},
		"no text":           {"uidA", RoleAdmin, send{clientID: cid}},
		"another client id": {"uidA", RoleAdmin, send{text: ptr("hi"), clientID: uuid.New()}},
	} {
		if sameLegacyMessage(doc, c.sender, c.role, c.v) {
			t.Errorf("%s: taken for the same message", name)
		}
	}
	doc[fImageURL] = "https://firebasestorage.example.test/x.jpg"
	if sameLegacyMessage(doc, "uidA", RoleAdmin, v) {
		t.Error("a stored image is another message")
	}
}
