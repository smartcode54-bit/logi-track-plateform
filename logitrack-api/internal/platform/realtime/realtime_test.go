package realtime

import (
	"encoding/json"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
)

func TestTopicCatalogue(t *testing.T) {
	const id = "0199c000-0000-7000-8000-000000000001"
	for _, ok := range []string{
		"user:" + id, "driver:" + id, "chat:" + id, "tenant:" + id + ":tasks", "tenant:" + id + ":trips",
		"tenant:" + id + ":chats", "tenant:" + id + ":fleet", "tenant:" + id + ":hr", "tenant:" + id + ":expenses",
		"tenant:" + id + ":billing", "tenant:" + id + ":vehicle_locations", "tenant:" + id + ":config",
		"global", "platform:security", "dispatch:tasks", "dispatch:trips",
	} {
		if !ValidTopic(ok) {
			t.Errorf("%s rejected", ok)
		}
	}
	for _, bad := range []string{"", "seq", "user:abc", "tenant:" + id, "tenant:" + id + ":payroll", "global:x",
		"dispatch:chats", "users:" + id, "rt:global", "tenant:" + id + ":tasks\nPUBLISH"} {
		if ValidTopic(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if !Ephemeral("tenant:"+id+":vehicle_locations") || Ephemeral("tenant:"+id+":trips") {
		t.Fatal("only vehicle_locations is ephemeral")
	}
}

func TestKeysAndMessageTail(t *testing.T) {
	// The wire names of Appendix B §B.4.4, built by cache.Keyspace.
	k, err := cache.NewKeyspace("dev")
	if err != nil {
		t.Fatal(err)
	}
	if k.RealtimeSeq() != "lt:dev:rtlog:seq" || k.RealtimeLog("global") != "lt:dev:rtlog:global" ||
		k.Channel("global") != "lt:dev:rt:global" || k.Pattern(cache.NSRealtimeLog) != "lt:dev:rtlog:*" {
		t.Fatal(k.RealtimeSeq(), k.RealtimeLog("global"), k.Channel("global"), k.Pattern(cache.NSRealtimeLog))
	}
	tail, err := messageTail("global", "hubs.changed", "e1", json.RawMessage(`{"ids":["a"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var m Message
	if err := json.Unmarshal([]byte(`{"id":7,`+tail), &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != 7 || m.Topic != "global" || m.Type != "hubs.changed" || m.EventID != "e1" || string(m.Data) != `{"ids":["a"]}` {
		t.Fatalf("%+v", m)
	}
}
