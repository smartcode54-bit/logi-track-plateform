package jobs

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
)

func TestCursorRoundTrip(t *testing.T) {
	j := Job{ID: uuid.Must(uuid.NewV7()), CreatedAt: time.Date(2026, 10, 10, 1, 2, 3, 456789000, time.UTC)}
	c, ok := decodeCursor(encodeCursor(j))
	if !ok || c.ID != j.ID || !c.CreatedAt.Equal(j.CreatedAt) {
		t.Fatalf("%+v %v", c, ok)
	}
	for _, bad := range []string{"", "!!", "e30", encodeCursor(Job{})} {
		if _, ok := decodeCursor(bad); ok {
			t.Fatalf("%q decoded", bad)
		}
	}
}

func TestObjectAndTypes(t *testing.T) {
	if b, err := object(nil); err != nil || string(b) != "{}" {
		t.Fatal(string(b), err)
	}
	if _, err := object([]int{1}); err == nil {
		t.Fatal("an array was accepted as params")
	}
	for _, ok := range []string{"payroll.run", "billing.backfill-trips", "queue.replay", "auth.token-cleanup"} {
		if !typeRx.MatchString(ok) {
			t.Fatal(ok)
		}
	}
	for _, bad := range []string{"payroll", "Payroll.run", "a..b", "job.billing.x y"} {
		if typeRx.MatchString(bad) {
			t.Fatal(bad)
		}
	}
	ks, err := cache.NewKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	l := NewRedisLocker(nil, ks)
	if l.JobKey("queue.replay", "") != "lt:local:lock:job:queue.replay:all" ||
		!strings.HasPrefix(l.CronKey("storage.gc", time.Date(2026, 1, 2, 3, 0, 0, 0, time.FixedZone("ICT", 7*3600))), "lt:local:lock:cron:storage.gc:20260101T200000Z") {
		t.Fatal(l.JobKey("queue.replay", ""), l.CronKey("storage.gc", time.Now()))
	}
}
