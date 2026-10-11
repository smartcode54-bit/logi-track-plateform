//go:build integration

// The class C write-back of T31 end to end against the coexistence it was built for: the operations routes write the
// Firestore document first (the gcptest fake, through the same writeback.Firestore adapter the api uses), the
// mirror's listener applies it to PostgreSQL and raises cache:mirror_ack, and the handler, which waited for that ack,
// answers with the row as GET reads it. The minted uuidv7 doc id is the row's id (R25), and PostgreSQL is never
// written by the handler itself.
package coexist

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/cache/cachetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

func TestWriteBackThroughTheMirror(t *testing.T) {
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

	svc, err := operations.New(operations.Deps{Pool: f.app, Log: zerolog.Nop(),
		WriteBack: &operations.WriteBack{Writer: writeback.Firestore{FS: f.fs.Firestore()}, Acks: acks, Wait: 10 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	own := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	staff := &authz.Principal{UserID: uuid.New(), TenantID: &own, TenantRole: authz.TenantAdmin, TenantKind: "own_fleet",
		Steward: true, Caps: authz.TenantDefaults(authz.TenantAdmin)}
	var driver, party uuid.UUID
	if err := f.etl.QueryRow(f.ctx, `SELECT id FROM drivers WHERE legacy_doc_id = 'drv_own'`).Scan(&driver); err != nil {
		t.Fatal(err)
	}
	if err := f.etl.QueryRow(f.ctx, `SELECT bp.id FROM billing_parties bp JOIN customers c ON c.id = bp.customer_id
		WHERE c.legacy_doc_id = 'cust_cjsf'`).Scan(&party); err != nil {
		t.Fatal(err)
	}
	var before int
	_ = f.etl.QueryRow(f.ctx, `SELECT count(*) FROM outbox_events WHERE headers->>'origin' IS NULL`).Scan(&before)
	body, _ := json.Marshal(map[string]any{"taskType": "first_mile", "date": time.Now().In(time.UTC).Format(time.DateOnly),
		"time": "08:15", "sourceHub": "HUBA", "destination": "SOCE", "billingPartyId": party, "driverId": driver,
		"jobCategory": "SUPPLEMENTARY"})
	res, err := svc.CreateTaskAPI(f.ctx, staff, "rid-1", body)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusCreated {
		t.Fatalf("not acknowledged within the wait: %d %+v", res.Status, res.Data)
	}
	view, _ := json.Marshal(res.Data)
	var got map[string]any
	_ = json.Unmarshal(view, &got)
	id := got["id"].(string)
	if _, ok := f.fs.Doc("tasks/" + id); !ok {
		t.Fatalf("no document under the row id %s", id)
	}
	if got["driverId"] != driver.String() || got["jobCategory"] != "SUPPLEMENTARY" || got["planTime"] != "08:15" ||
		got["status"] != "assigned" || got["billingPartyId"] != party.String() || got["taskNo"] == nil {
		t.Fatalf("the mirrored row %v", got)
	}
	var legacy string
	if err := f.etl.QueryRow(f.ctx, `SELECT legacy_doc_id FROM tasks WHERE id = $1`, id).Scan(&legacy); err != nil || legacy != id {
		t.Fatalf("legacy_doc_id %q %v", legacy, err)
	}
	// The write-back's own event is the push (origin=writeback, no topic); the mirror's event of the same change
	// (origin=etl.sync) carries the realtime topics; no event without an origin was appended by the handler.
	var wb, mirrored, plain int
	_ = f.etl.QueryRow(f.ctx, `SELECT count(*) FILTER (WHERE headers->>'origin' = 'writeback' AND routing_key = 'task.assigned'
			AND cardinality(realtime_topics) = 0),
		count(*) FILTER (WHERE headers->>'origin' = 'etl.sync' AND routing_key = 'task.created'),
		count(*) FILTER (WHERE headers->>'origin' IS NULL)
		FROM outbox_events WHERE aggregate_id = $1`, id).Scan(&wb, &mirrored, &plain)
	if wb != 1 || mirrored != 1 || plain != 0 {
		t.Fatalf("events: write-back %d, mirror %d, plain %d", wb, mirrored, plain)
	}
	// A PATCH writes only its fields and is answered from the mirrored row.
	res, err = svc.PatchTaskAPI(f.ctx, staff, "rid-2", uuid.MustParse(id), []byte(`{"jobCategory":"PRIMARY","time":"10:45"}`))
	if err != nil || res.Status != http.StatusOK {
		t.Fatalf("patch %d %v", res.Status, err)
	}
	view, _ = json.Marshal(res.Data)
	_ = json.Unmarshal(view, &got)
	if got["jobCategory"] != "PRIMARY" || got["planTime"] != "10:45" || got["driverId"] != driver.String() {
		t.Fatalf("patched row %v", got)
	}
}
