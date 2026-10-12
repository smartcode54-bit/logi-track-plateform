package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The chat writes go to PostgreSQL only while PG_OWNED_DOMAINS holds comms (T47); anything else (empty, other domains,
// an invalid value that config validation refuses anyway) keeps them on the write-back path.
func TestCommsOwned(t *testing.T) {
	for v, want := range map[string]bool{"": false, "all": true, "comms": true, "auth, comms": true, "auth,operations": false,
		"comms,comms": false, "nope": false} {
		if got := CommsOwned(v); got != want {
			t.Errorf("%q: %v", v, got)
		}
	}
}

// The chat write-back (T47) is one of the api's write-backs over the one Firestore client (buildWriteBacks): built
// while comms is not PostgreSQL-owned and a project is set, sharing the client of the operations and leave writes; none
// while comms is owned; without a project the chat writes answer 503 write_back_unconfigured (buildChats).
func TestChatWriteBack(t *testing.T) {
	caches := testCaches(t)
	key := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(key, gcptest.New(t, "logitrack-writeback-test").ServiceAccountJSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	project := "WRITEBACK_FIRESTORE_PROJECT_ID=logitrack-writeback-test"
	build := func(extra ...string) (writeBacks, string) {
		t.Helper()
		var logs bytes.Buffer
		wbs, err := buildWriteBacks(writeBackConfig(t, extra...), caches, zerolog.New(&logs))
		if err != nil {
			t.Fatal(err)
		}
		return wbs, logs.String()
	}
	wbs, logs := build("PG_OWNED_DOMAINS=", project, "GOOGLE_APPLICATION_CREDENTIALS="+key)
	if wbs.Chats == nil || wbs.Operations == nil || !strings.Contains(logs, "chat writes: Firestore write-back") {
		t.Fatalf("comms Firestore-owned: %+v %s", wbs, logs)
	}
	cw, ok := wbs.Chats.Writer.(writeback.Firestore)
	ow, ok2 := wbs.Operations.Writer.(writeback.Firestore)
	if !ok || !ok2 || cw.FS == nil || cw.FS != ow.FS || wbs.Chats.Acks != chats.MirrorAcks(caches) ||
		wbs.Chats.Wait != chats.DefaultMirrorWait {
		t.Fatalf("not the shared client: %#v %#v", wbs.Chats, wbs.Operations)
	}
	if wbs, _ := build("PG_OWNED_DOMAINS=auth,masterdata,operations,comms", project, "GOOGLE_APPLICATION_CREDENTIALS="+key); wbs.Chats != nil || wbs.Leave == nil {
		t.Fatalf("comms owned: %+v", wbs)
	}
	wbs, logs = build("PG_OWNED_DOMAINS=operations")
	if wbs.Chats != nil || !strings.Contains(logs, "chat writes off") {
		t.Fatalf("no project: %+v %s", wbs, logs)
	}
	svc, err := buildChats(writeBackConfig(t, "PG_OWNED_DOMAINS=comms"), noPool{}, nil, &chats.WriteBack{Writer: writeback.NewMemory()},
		nil, zerolog.Nop())
	if err != nil || svc == nil {
		t.Fatalf("comms owned: %v", err)
	}
}
