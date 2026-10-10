package realtime

import (
	"errors"
	"strconv"
	"testing"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
)

func testHub(t *testing.T, buffer int) *Hub {
	t.Helper()
	ks, err := cache.NewKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	return NewHub(nil, ks, zerolog.Nop(), buffer)
}

func msg(topic string, id int64) string {
	return `{"id":` + strconv.FormatInt(id, 10) + `,"topic":"` + topic + `","type":"x.y","eventId":"e","data":{}}`
}

func TestHubRefusesSubscriptionsUntilConfirmed(t *testing.T) {
	h := testHub(t, 4)
	if _, err := h.Subscribe([]string{"global"}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("before the PSUBSCRIBE confirmation: %v", err)
	}
	h.confirmed()
	s, err := h.Subscribe([]string{"global"})
	if err != nil || !h.Ready() {
		t.Fatalf("after: %v ready=%v", err, h.Ready())
	}
	h.Drain()
	if <-s.Done(); s.Reason() != EndShutdown {
		t.Fatalf("drain reason %q", s.Reason())
	}
	if _, err := h.Subscribe([]string{"global"}); !errors.Is(err, ErrDraining) || h.Ready() {
		t.Fatalf("after drain: %v", err)
	}
}

func TestHubDispatchesByTopicAndIgnoresForeignChannels(t *testing.T) {
	h := testHub(t, 4)
	h.confirmed()
	a, _ := h.Subscribe([]string{"user:" + tid, "global"})
	b, _ := h.Subscribe([]string{"global"})
	h.dispatch("lt:local:rt:user:"+tid, msg("user:"+tid, 1))
	h.dispatch("lt:local:rt:global", msg("global", 2))
	h.dispatch("lt:local:rt:cache", `{"keys":["lt:local:cache:hubs:all"]}`)            // L1 invalidation channel
	h.dispatch("lt:local:rt:tenant:"+tid+":payroll", msg("tenant:"+tid+":payroll", 3)) // outside the catalogue
	h.dispatch("lt:dev:rt:global", msg("global", 4))                                   // another APP_ENV
	h.dispatch("lt:local:rt:global", msg("user:"+tid, 5))                              // topic does not match its channel
	h.dispatch("lt:local:rt:global", `not json`)
	if got := ids(a); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("a got %v", got)
	}
	if got := ids(b); len(got) != 1 || got[0] != 2 {
		t.Fatalf("b got %v", got)
	}
	a.Close()
	a.Close() // idempotent
	h.dispatch("lt:local:rt:global", msg("global", 6))
	if got := ids(b); len(got) != 1 || got[0] != 6 {
		t.Fatalf("b got %v after a closed", got)
	}
}

func TestHubEndsLaggingAndInterruptedSubscriptions(t *testing.T) {
	h := testHub(t, 1)
	h.confirmed()
	slow, _ := h.Subscribe([]string{"global"})
	other, _ := h.Subscribe([]string{"user:" + tid})
	h.dispatch("lt:local:rt:global", msg("global", 1))
	h.dispatch("lt:local:rt:global", msg("global", 2)) // buffer of one is full
	<-slow.Done()
	if slow.Reason() != EndLagging {
		t.Fatalf("slow reason %q", slow.Reason())
	}
	// A reconfirmed PSUBSCRIBE (the connection dropped and came back) ends everything made before it.
	h.confirmed()
	<-other.Done()
	if other.Reason() != EndInterrupted {
		t.Fatalf("other reason %q", other.Reason())
	}
	if _, err := h.Subscribe([]string{"global"}); err != nil {
		t.Fatalf("new subscriptions after the reconfirmation: %v", err)
	}
}

func ids(s *Subscription) []int64 {
	var out []int64
	for {
		select {
		case m := <-s.C():
			out = append(out, m.ID)
		default:
			return out
		}
	}
}
