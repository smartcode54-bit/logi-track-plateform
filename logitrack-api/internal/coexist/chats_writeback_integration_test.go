//go:build integration

// The chat write-back of T47 end to end against the coexistence it was built for: with comms Firestore-owned the chat
// writes go to the Firestore documents first (the gcptest fake, through the chats.GCPWriter adapter), the mirror's
// listeners (chats, and the messages collection group) apply them to PostgreSQL, emit the chat events (SSE from the
// mirror) and raise cache:mirror_ack, and the handler, which waited for that ack, answers with the row as GET reads it.
// PostgreSQL is never written by the handler itself; the push is the handler's own event (origin=writeback), the
// mirror's event of the same message (origin=etl.sync) pushes nothing.
package coexist

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
)

func TestChatWriteBackThroughTheMirror(t *testing.T) {
	f := newFixture(t, true)
	p := plan(t, "auth,masterdata")
	rdb, ks := cachetest.NewClient(t)
	acks := cache.New(rdb, ks)
	l, err := NewListener(ListenConfig{Interval: 50 * time.Millisecond, ETLPool: f.etl, Source: f.fs.Firestore(), Plan: p.Mirror,
		Bucket: "logitrack", PublicBucket: "logitrack-public", Lease: NewRedisLease(rdb, ks.MirrorListenLock(), ListenLeaseTTL),
		Acks: acks, Log: zerolog.Nop(), Metrics: NewMetrics(nil)})
	if err != nil || l == nil {
		t.Fatalf("listener %v %v", l, err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	svc, err := chats.New(chats.Deps{Pool: f.app, Log: zerolog.Nop(),
		WriteBack: &chats.WriteBack{Writer: chats.GCPWriter{FS: f.fs.Firestore()}, Acks: acks, Wait: 10 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	own := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	var staffID uuid.UUID
	if err := f.etl.QueryRow(f.ctx, `INSERT INTO users (email, display_name, legacy_auth_uid) VALUES ('wb.staff@example.test',
		'WB Staff', 'uid_wb_staff') RETURNING id`).Scan(&staffID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.etl.Exec(f.ctx, `INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'tenant_admin')`, staffID, own); err != nil {
		t.Fatal(err)
	}
	staff := &authz.Principal{UserID: staffID, TenantID: &own, TenantRole: authz.TenantAdmin, TenantKind: "own_fleet",
		Steward: true, Caps: authz.TenantDefaults(authz.TenantAdmin)}
	var chat uuid.UUID
	if err := f.etl.QueryRow(f.ctx, `SELECT id FROM chats WHERE legacy_doc_id = 'chat_1'`).Scan(&chat); err != nil {
		t.Fatal(err)
	}
	cid := uuid.New()
	res, err := svc.Send(f.ctx, staff, "rid-1", chat, chats.SendInput{Text: ptr("จากเว็บผ่าน Go"), ClientMessageID: cid.String()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusCreated {
		t.Fatalf("not acknowledged within the wait: %d %+v", res.Status, res.Data)
	}
	msg := res.Data.(chats.Message)
	if msg.ChatID != chat || msg.Text == nil || *msg.Text != "จากเว็บผ่าน Go" || msg.ClientMessageID == nil || *msg.ClientMessageID != cid ||
		msg.SenderUserID == nil || *msg.SenderUserID != staffID {
		t.Fatalf("the mirrored message %+v", msg)
	}
	d, ok := f.fs.Doc("chats/chat_1/messages/" + cid.String())
	if !ok || d.Fields["senderId"] != "uid_wb_staff" || d.Fields["senderRole"] != "admin" || d.Fields["clientMessageId"] != cid.String() {
		t.Fatalf("message document %v %v", ok, d.Fields)
	}
	cd, _ := f.fs.Doc("chats/chat_1")
	if cd.Fields["lastMessage"] != "จากเว็บผ่าน Go" || cd.Fields["lastMessageBy"] != "uid_wb_staff" {
		t.Fatalf("chat document %v", cd.Fields)
	}
	if reads, ok := cd.Fields["lastReadByAdmin"].(map[string]any); !ok || reads["uid_wb_staff"] == nil {
		t.Fatalf("lastReadByAdmin %v", cd.Fields["lastReadByAdmin"])
	}
	// One push event of the writer (no topic), one mirror event with the realtime topics; nothing else.
	var wb, mirrored, plain int
	_ = f.etl.QueryRow(f.ctx, `SELECT count(*) FILTER (WHERE headers->>'origin' = 'writeback' AND cardinality(realtime_topics) = 0),
		count(*) FILTER (WHERE headers->>'origin' = 'etl.sync' AND cardinality(realtime_topics) > 0),
		count(*) FILTER (WHERE headers->>'origin' IS NULL)
		FROM outbox_events WHERE routing_key = 'chat.message_created' AND aggregate_id = $1`, chat.String()).Scan(&wb, &mirrored, &plain)
	if wb != 1 || mirrored != 1 || plain != 0 {
		t.Fatalf("events: write-back %d, mirror %d, plain %d", wb, mirrored, plain)
	}
	// The replay is answered from the mirrored row; nothing is written again.
	writes := len(f.fs.Writes())
	res, err = svc.Send(f.ctx, staff, "rid-2", chat, chats.SendInput{Text: ptr("จากเว็บผ่าน Go"), ClientMessageID: cid.String()})
	if err != nil || res.Status != http.StatusOK || res.Data.(chats.Message).ID != msg.ID || len(f.fs.Writes()) != writes {
		t.Fatalf("replay %d %v (%d writes)", res.Status, err, len(f.fs.Writes())-writes)
	}
	// Back to the queue: the chat document loses its assignment; the mirror's chat.updated follows.
	res, err = svc.Assign(f.ctx, staff, "rid-3", chat, chats.AssignInput{AdminUserID: chats.Field[string]{Set: true, Null: true}})
	if err != nil || res.Status != http.StatusOK {
		t.Fatalf("assign %d %v", res.Status, err)
	}
	if det := res.Data.(chats.ChatDetail); det.Status != chats.StatusOpen || det.AssignedAdminUserID != nil {
		t.Fatalf("returned to the queue %+v", det.Chat)
	}
	if cd, _ := f.fs.Doc("chats/chat_1"); cd.Fields["assignedAdminId"] != nil || cd.Fields["status"] != "open" {
		t.Fatalf("chat document after the assign %v", cd.Fields)
	}
	if n := countEvents(t, f, "chat.updated", chat); n == 0 {
		t.Fatal("the mirror emitted no chat.updated")
	}
	// A new chat: the document is created with the driver's legacy uid and answered as the mirror loaded it.
	var driver uuid.UUID
	var ref string
	if err := f.etl.QueryRow(f.ctx, `SELECT d.id, d.legacy_auth_uid FROM drivers d WHERE d.tenant_id = $1 AND d.legacy_auth_uid IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM chats c WHERE c.driver_id = d.id AND c.status <> 'closed') ORDER BY d.id LIMIT 1`, own).
		Scan(&driver, &ref); err != nil {
		t.Skipf("no own-fleet driver without an open chat in the fixture: %v", err)
	}
	res, err = svc.Open(f.ctx, staff, "rid-4", chats.OpenInput{DriverID: driver.String()})
	if err != nil || res.Status != http.StatusCreated {
		t.Fatalf("open %d %v %+v", res.Status, err, res.Data)
	}
	det := res.Data.(chats.ChatDetail)
	if det.DriverID == nil || *det.DriverID != driver || det.LegacyDocID == nil {
		t.Fatalf("new chat %+v", det.Chat)
	}
	if nd, ok := f.fs.Doc("chats/" + *det.LegacyDocID); !ok || nd.Fields["driverId"] != ref || nd.Fields["status"] != "open" {
		t.Fatalf("new chat document %v %v", ok, nd.Fields)
	}
	if n := countEvents(t, f, "chat.created", det.ID); n != 1 {
		t.Fatalf("%d chat.created", n)
	}
	// A driver's 3.x message written straight to Firestore reaches PostgreSQL through the group listener, with its event.
	f.fs.Set("chats/chat_1/messages/m_app", map[string]any{"senderId": "uid_own", "senderRole": "driver",
		"text": "จากแอป 3.x", "createdAt": dump.Timestamp{Time: time.Now().UTC()}})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		_ = f.etl.QueryRow(f.ctx, `SELECT count(*) FROM chat_messages WHERE chat_id = $1 AND legacy_doc_id = 'm_app'`, chat).Scan(&n)
		if n == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	var mirroredApp int
	_ = f.etl.QueryRow(f.ctx, `SELECT count(*) FROM outbox_events WHERE routing_key = 'chat.message_created' AND headers->>'origin' = 'etl.sync'
		AND payload->>'legacyDocId' = 'm_app'`).Scan(&mirroredApp)
	if mirroredApp != 1 {
		t.Fatalf("the 3.x message: %d mirror events", mirroredApp)
	}
}

func countEvents(t *testing.T, f *fixture, key string, chat uuid.UUID) int {
	t.Helper()
	var n int
	if err := f.etl.QueryRow(f.ctx, `SELECT count(*) FROM outbox_events WHERE routing_key = $1 AND aggregate_id = $2`, key, chat.String()).
		Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func ptr[T any](v T) *T { return &v }
