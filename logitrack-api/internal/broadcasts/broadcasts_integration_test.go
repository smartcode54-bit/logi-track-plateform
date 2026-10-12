//go:build integration

package broadcasts_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/broadcasts"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/realtime"
)

func sorted(ids ...string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// The recipient groups resolve on the server, in the caller's reach (R60): the own fleet's staff reach their drivers
// and those of the carriers working for them, a carrier only its own, nobody an independent carrier's; an inactive
// driver is never addressed. Recipients are persisted with the broadcast, recipientCount is their number, and one
// broadcast.created carries the user topic of each recipient (Appendix B §B.4.3).
func TestSendResolvesTheAudience(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o@example.test")
	cases := []struct {
		group string
		want  []string
	}{
		{broadcasts.GroupAllDriver, sorted(w.uDO, w.uDOs, w.uDA)},
		{broadcasts.GroupOwnFleet, sorted(w.uDO)},
		{broadcasts.GroupSubcontractor, sorted(w.uDOs, w.uDA)},
	}
	for _, tc := range cases {
		r := w.post("/v1/broadcasts", ta, map[string]any{"recipientGroup": tc.group, "title": "  วันหยุด  ", "messageText": "หยุดสงกรานต์"})
		expect(t, r, http.StatusCreated, "")
		d := r.data()
		id := str(d["id"])
		if got := w.recipients(id); !slices.Equal(got, tc.want) {
			t.Fatalf("%s recipients %v, want %v", tc.group, got, tc.want)
		}
		if d["tenantId"] != w.O || d["recipientGroup"] != tc.group || int(num(d["recipientCount"])) != len(tc.want) ||
			d["title"] != "วันหยุด" || d["messageText"] != "หยุดสงกรานต์" || d["createdByName"] != "ฝ่ายปฏิบัติการ" ||
			d["createdByUserId"] != w.taO || num(d["readCount"]) != 0 || d["voidedAt"] != nil {
			t.Fatalf("%s answer %s", tc.group, r.raw)
		}
		es := w.events(id)
		if len(es) != 1 || es[0].Key != broadcasts.RouteCreated {
			t.Fatalf("%s events %+v", tc.group, es)
		}
		var topics []string
		for _, u := range tc.want {
			topics = append(topics, realtime.UserTopic(u))
		}
		if !slices.Equal(sorted(es[0].Topics...), sorted(topics...)) || es[0].Payload["title"] != "วันหยุด" ||
			es[0].Payload["id"] != id || es[0].Payload["entity"] != "broadcast" || es[0].Payload["messageText"] != nil {
			t.Fatalf("%s event %s %v", tc.group, es[0].Raw, es[0].Topics)
		}
	}
	// A carrier reaches its own drivers only (no subtenants): all_driver is its driver, subcontractor nobody (422).
	taA := w.login("ta.a@example.test")
	id := w.send(taA, map[string]any{"recipientGroup": broadcasts.GroupAllDriver})
	if got := w.recipients(id); !slices.Equal(got, []string{w.uDA}) {
		t.Fatalf("carrier all_driver %v", got)
	}
	r := w.post("/v1/broadcasts", taA, map[string]any{"recipientGroup": broadcasts.GroupSubcontractor, "title": "t", "messageText": "m"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if !slices.Equal(r.fields(), []string{"recipientGroup:no_recipients"}) {
		t.Fatalf("empty audience %s", r.raw)
	}
	// An explicit list: the drivers' logins of the caller's reach; anything else is refused by index.
	id = w.send(ta, map[string]any{"recipientUserIds": []string{w.uDA, w.uDO, w.uDA}})
	if got := w.recipients(id); !slices.Equal(got, sorted(w.uDO, w.uDA)) {
		t.Fatalf("selected %v", got)
	}
	r = w.post("/v1/broadcasts", ta, map[string]any{"recipientUserIds": []string{w.uDO, w.uDC, w.taO, w.uDOi}, "title": "t", "messageText": "m"})
	expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
	if got := r.fields(); !slices.Equal(got, []string{"recipientUserIds[1]:not_found", "recipientUserIds[2]:not_found",
		"recipientUserIds[3]:not_found"}) {
		t.Fatalf("selected outside the reach %s", r.raw)
	}
	if n := w.count(`SELECT count(*) FROM broadcasts WHERE recipient_group = 'selected'`); n != 1 {
		t.Fatalf("a refused send wrote a broadcast (%d)", n)
	}
}

// The validation of a send, the capability and the steward rule of a platform-wide broadcast (R60, Appendix C
// §C.8 #15): only a steward sends to every tenant's drivers (tenant_id NULL); a carrier's admin is refused before the
// database, and so is a platform admin without a tenant (it acts through X-Act-On-Tenant).
func TestSendRulesAndPlatformWide(t *testing.T) {
	w := newWorld(t)
	ta, mg := w.login("ta.o@example.test"), w.login("mg.o@example.test")
	bad := []struct {
		body   map[string]any
		fields []string
	}{
		{map[string]any{"recipientGroup": "all_driver"}, []string{"title:required", "messageText:required"}},
		{map[string]any{"recipientGroup": "all_driver", "title": " ", "messageText": "\n"}, []string{"title:required", "messageText:required"}},
		{map[string]any{"recipientGroup": "everyone", "title": "t", "messageText": "m"}, []string{"recipientGroup:invalid"}},
		{map[string]any{"title": "t", "messageText": "m"}, []string{"recipientGroup:required"}},
		{map[string]any{"recipientGroup": "all_driver", "recipientUserIds": []string{w.uDO}, "title": "t", "messageText": "m"},
			[]string{"recipientUserIds:conflict"}},
		{map[string]any{"recipientUserIds": []string{}, "title": "t", "messageText": "m"}, []string{"recipientUserIds:required"}},
		{map[string]any{"recipientUserIds": []any{"x", 3}, "title": "t", "messageText": "m"},
			[]string{"recipientUserIds[0]:invalid", "recipientUserIds[1]:invalid"}},
		{map[string]any{"recipientGroup": "all_driver", "title": strings.Repeat("ท", broadcasts.MaxTitleLength+1), "messageText": "m"},
			[]string{"title:too_long"}},
	}
	for _, b := range bad {
		r := w.post("/v1/broadcasts", ta, b.body)
		expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
		if !slices.Equal(r.fields(), b.fields) {
			t.Errorf("%v: %v, want %v", b.body, r.fields(), b.fields)
		}
	}
	// operation_staff views the history but does not send; a driver neither.
	os := w.login("os.o@example.test")
	expect(t, w.get("/v1/broadcasts", os), http.StatusOK, "")
	expect(t, w.post("/v1/broadcasts", os, map[string]any{"recipientGroup": "all_driver", "title": "t", "messageText": "m"}),
		http.StatusForbidden, authz.CodePermissionDenied)
	expect(t, w.get("/v1/broadcasts", w.login("d.o@example.test")), http.StatusForbidden, authz.CodePermissionDenied)

	// all_tenants: a manager of the own fleet is a steward; the platform-wide audience is every active driver of every
	// tenant (the inactive one left out).
	r := w.post("/v1/broadcasts", mg, map[string]any{"recipientGroup": broadcasts.GroupAllTenants, "title": "ทั้งหมด", "messageText": "m"})
	expect(t, r, http.StatusCreated, "")
	pid := str(r.data()["id"])
	if r.data()["tenantId"] != nil || int(num(r.data()["recipientCount"])) != 4 {
		t.Fatalf("platform-wide %s", r.raw)
	}
	if got := w.recipients(pid); !slices.Equal(got, sorted(w.uDO, w.uDOs, w.uDA, w.uDC)) {
		t.Fatalf("platform-wide recipients %v", got)
	}
	expect(t, w.post("/v1/broadcasts", w.login("ta.a@example.test"), map[string]any{"recipientGroup": broadcasts.GroupAllTenants,
		"title": "t", "messageText": "m"}), http.StatusForbidden, authz.CodePermissionDenied)
	expect(t, w.post("/v1/broadcasts", w.login("pa@example.test"), map[string]any{"recipientGroup": broadcasts.GroupAllTenants,
		"title": "t", "messageText": "m"}), http.StatusForbidden, "")
	// Every tenant's staff reads the platform-wide broadcast; a carrier cannot void it, a steward can.
	for _, email := range []string{"ta.a@example.test", "ta.c@example.test"} {
		if ids := listIDs(t, w.get("/v1/broadcasts", w.login(email))); !slices.Contains(ids, pid) {
			t.Errorf("%s does not see the platform-wide broadcast", email)
		}
	}
	expect(t, w.del("/v1/broadcasts/"+pid, w.login("ta.c@example.test")), http.StatusForbidden, authz.CodePermissionDenied)
	expect(t, w.del("/v1/broadcasts/"+pid, mg), http.StatusNoContent, "")
}

func listIDs(t *testing.T, r resp) []string {
	t.Helper()
	expect(t, r, http.StatusOK, "")
	var out []string
	for _, x := range r.list() {
		out = append(out, str(x.(map[string]any)["id"]))
	}
	return out
}

// The history is a keyset page of the caller's reach, newest first, with read counts; a voided broadcast leaves it.
// Tenants see their own and the platform-wide broadcasts, the own fleet also its carriers' (contractor reach), an
// independent carrier nobody else's.
func TestHistoryPagesAndReach(t *testing.T) {
	w := newWorld(t)
	ta, taA, taC := w.login("ta.o@example.test"), w.login("ta.a@example.test"), w.login("ta.c@example.test")
	var own []string
	for i := range 5 {
		own = append(own, w.send(ta, map[string]any{"recipientGroup": "all_driver", "title": fmt.Sprintf("b%d", i)}))
	}
	carrier := w.send(taA, map[string]any{"recipientGroup": "all_driver"})
	other := w.send(taC, map[string]any{"recipientGroup": "all_driver"})
	w.exec(`INSERT INTO broadcast_reads (broadcast_id, user_id) VALUES ($1, $2), ($1, $3)`, own[4], w.uDO, w.uDA)

	var got []string
	cursor := ""
	for page := 0; ; page++ {
		path := "/v1/broadcasts?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := w.get(path, ta)
		ids := listIDs(t, r)
		if len(ids) > 2 || page > 4 {
			t.Fatalf("page %d: %s", page, r.raw)
		}
		if page == 0 && (r.list()[0].(map[string]any)["id"] != carrier || num(r.list()[1].(map[string]any)["readCount"]) != 2) {
			t.Fatalf("first page %s", r.raw)
		}
		got = append(got, ids...)
		if cursor = r.next(); cursor == "" {
			break
		}
	}
	want := []string{carrier, own[4], own[3], own[2], own[1], own[0]}
	if !slices.Equal(got, want) {
		t.Fatalf("pages %v, want %v", got, want)
	}
	if ids := listIDs(t, w.get("/v1/broadcasts", taA)); !slices.Equal(ids, []string{carrier}) {
		t.Errorf("carrier A sees %v", ids)
	}
	if ids := listIDs(t, w.get("/v1/broadcasts", taC)); !slices.Equal(ids, []string{other}) {
		t.Errorf("carrier C sees %v", ids)
	}
	expect(t, w.get("/v1/broadcasts?cursor=nope", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/broadcasts?limit=201", ta), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/broadcasts?status=x", ta), http.StatusBadRequest, "bad_request")

	// The void (R14): one event, the row stays with who and when; a second void changes nothing; the history and the
	// drivers' lists leave it out; another tenant cannot see it (404).
	expect(t, w.del("/v1/broadcasts/"+carrier, taC), http.StatusNotFound, "not_found")
	expect(t, w.del("/v1/broadcasts/"+carrier, ta), http.StatusNoContent, "") // the contractor voids its carrier's broadcast
	expect(t, w.del("/v1/broadcasts/"+carrier, taA), http.StatusNoContent, "")
	if es := w.events(carrier); len(es) != 2 || es[1].Key != broadcasts.RouteVoided || len(es[1].Topics) != 0 || es[1].Payload["id"] != carrier {
		t.Fatalf("void events %+v", es)
	}
	if n := w.count(`SELECT count(*) FROM broadcasts WHERE id = $1 AND voided_at IS NOT NULL AND voided_by = $2`, carrier, w.taO); n != 1 {
		t.Fatalf("the voided row %d", n)
	}
	if ids := listIDs(t, w.get("/v1/broadcasts", ta)); slices.Contains(ids, carrier) || len(ids) != 5 {
		t.Errorf("history after the void %v", ids)
	}
	expect(t, w.del("/v1/broadcasts/"+uuid.NewString(), ta), http.StatusNotFound, "not_found")
	expect(t, w.del("/v1/broadcasts/not-a-uuid", ta), http.StatusNotFound, "not_found")
}

// AC: a read receipt is idempotent per user. Through the markBroadcastRead shim target (cf_shim key + the driver's
// Firebase ID token): the first read writes the row and appends broadcast.read (firestore.shadow projects the readers),
// every later one (concurrent ones included) answers alreadyRead and writes nothing; the Firestore document id names
// the broadcast too; a voided broadcast, another tenant's that is not addressed to the driver, the own tenant's
// addressed to other drivers (T48 review: the broadcasts GET /v1/mobile/broadcasts lists, so readCount counts
// recipients only), an unknown id and a staff user are refused.
func TestReadReceiptIsIdempotentPerUser(t *testing.T) {
	w := newWorld(t)
	if n := w.shimCalls(); n != 0 {
		t.Fatalf("shim_requests_total starts at %v", n)
	}
	ta := w.login("ta.o@example.test")
	id := w.send(ta, map[string]any{"recipientGroup": "all_driver"})
	r := w.markRead("d.o@example.test", id)
	expect(t, r, http.StatusOK, "")
	if r.data()["alreadyRead"] != false || r.data()["broadcastId"] != id {
		t.Fatalf("first read %s", r.raw)
	}
	r = w.markRead("d.o@example.test", id)
	if r.status != http.StatusOK || r.data()["alreadyRead"] != true {
		t.Fatalf("second read %d %s", r.status, r.raw)
	}
	// Ten concurrent first reads of another driver: one writes, nine answer alreadyRead.
	var wg sync.WaitGroup
	var mu sync.Mutex
	first := 0
	for range 10 {
		wg.Go(func() {
			r := w.markRead("d.a@example.test", id) // a carrier's driver reached by the own fleet (0022)
			mu.Lock()
			defer mu.Unlock()
			if r.status != http.StatusOK {
				t.Errorf("concurrent read %d %s", r.status, r.raw)
			} else if r.data()["alreadyRead"] == false {
				first++
			}
		})
	}
	wg.Wait()
	if first != 1 {
		t.Fatalf("%d first reads, want 1", first)
	}
	if n := w.count(`SELECT count(*) FROM broadcast_reads WHERE broadcast_id = $1`, id); n != 2 {
		t.Fatalf("%d read rows, want 2", n)
	}
	if rc := w.get("/v1/broadcasts", ta).list()[0].(map[string]any); rc["id"] != id || num(rc["readCount"]) != 2 {
		t.Fatalf("readCount %v", rc)
	}
	// One broadcast.read per first receipt (2), none for the repeats; no realtime topic.
	es := w.events(id)
	if len(es) != 3 || es[0].Key != broadcasts.RouteCreated {
		t.Fatalf("events after the reads %+v", es)
	}
	readers := []string{}
	for _, e := range es[1:] {
		if e.Key != broadcasts.RouteRead || len(e.Topics) != 0 || e.Payload["id"] != id || e.Payload["tenantId"] != w.O ||
			e.Payload["entity"] != "broadcast" {
			t.Fatalf("read event %s %v", e.Raw, e.Topics)
		}
		readers = append(readers, str(e.Payload["userId"]))
	}
	if !slices.Equal(sorted(readers...), sorted(w.uDO, w.uDA)) {
		t.Fatalf("read events of %v", readers)
	}
	// The Firestore document id (a loaded legacy broadcast: platform-wide, no recipients) names it too.
	legacy := w.id(`INSERT INTO broadcasts (legacy_doc_id, message_text, recipient_count) VALUES ('fsBroadcast01', 'เก่า', 2) RETURNING id::text`)
	r = w.markRead("d.c@example.test", "fsBroadcast01")
	if r.status != http.StatusOK || r.data()["broadcastId"] != legacy || r.data()["alreadyRead"] != false {
		t.Fatalf("by doc id %d %s", r.status, r.raw)
	}
	if es := w.events(legacy); len(es) != 1 || es[0].Key != broadcasts.RouteRead || es[0].Payload["tenantId"] != nil {
		t.Fatalf("the legacy broadcast's read event %+v", es)
	}
	// Refusals.
	ownOnly := w.send(ta, map[string]any{"recipientGroup": "own_fleet"})    // recipients: d.o
	toDO := w.send(ta, map[string]any{"recipientUserIds": []string{w.uDO}}) // recipients: d.o
	for _, tc := range []struct{ email, ref string }{
		{"d.a@example.test", ownOnly},          // the own fleet's broadcast not addressed to a carrier's driver
		{"d.os@example.test", ownOnly},         // the own tenant's broadcast addressed to other drivers
		{"d.os@example.test", toDO},            // a broadcast to selected users, read by another driver of the tenant
		{"d.o@example.test", uuid.NewString()}, // unknown
		{"d.o@example.test", "noSuchDoc"},      // unknown document id
	} {
		r := w.markRead(tc.email, tc.ref)
		expect(t, r, http.StatusNotFound, "not_found")
		if r.detail("reason") != broadcasts.ReasonBroadcastNotFound {
			t.Fatalf("%s %s: %s", tc.email, tc.ref, r.raw)
		}
	}
	if n := w.count(`SELECT count(*) FROM broadcast_reads WHERE broadcast_id IN ($1, $2)`, ownOnly, toDO); n != 0 {
		t.Fatalf("%d read rows of non-recipients", n)
	}
	for _, b := range []string{ownOnly, toDO} {
		if es := w.events(b); len(es) != 1 {
			t.Fatalf("a refused read appended an event: %+v", es)
		}
	}
	// Their recipient still reads them.
	for _, b := range []string{ownOnly, toDO} {
		if r := w.markRead("d.o@example.test", b); r.status != http.StatusOK || r.data()["alreadyRead"] != false {
			t.Fatalf("the recipient's read %d %s", r.status, r.raw)
		}
	}
	expect(t, w.del("/v1/broadcasts/"+id, ta), http.StatusNoContent, "")
	r = w.markRead("d.os@example.test", id)
	expect(t, r, http.StatusNotFound, "not_found")
	if r.detail("reason") != broadcasts.ReasonBroadcastNotFound {
		t.Fatalf("voided: %s", r.raw)
	}
	// A staff user's Firebase token reaches the target but holds no mobile:broadcasts; a Go token is no shim call.
	w.exec(`UPDATE users SET legacy_auth_uid = $2 WHERE id = $1`, w.taO, firebaseUID("ta.o@example.test"))
	expect(t, w.markRead("ta.o@example.test", ownOnly), http.StatusForbidden, authz.CodePermissionDenied)
	expect(t, w.call(w.public, http.MethodPost, "/v1/mobile/broadcasts/"+ownOnly+"/read", nil, bearer(w.login("d.o@example.test"))...),
		http.StatusUnauthorized, "")
	// Every authenticated call counts (2 + 10 + 1 + 5 refusals + 2 recipient reads + the voided one + the staff user),
	// the Go token none.
	if n := w.shimCalls(); n != 22 {
		t.Fatalf("shim_requests_total{callable=markBroadcastRead} = %v, want 22", n)
	}
}

// The driver's list (APK 4.x, P7a): the broadcasts addressed to it and the legacy ones without recipients it may read,
// newest first, its own read state; never a voided one, never one addressed to others.
func TestDriverList(t *testing.T) {
	w := newWorld(t)
	ta, taC := w.login("ta.o@example.test"), w.login("ta.c@example.test")
	legacy := w.id(`INSERT INTO broadcasts (legacy_doc_id, message_text, recipient_count, sent_at)
		VALUES ('fsLegacy', 'เก่า', 3, now() - interval '1 day') RETURNING id::text`)
	all := w.send(ta, map[string]any{"recipientGroup": "all_driver", "title": "all"})
	own := w.send(ta, map[string]any{"recipientGroup": "own_fleet", "title": "own"})
	cOnly := w.send(taC, map[string]any{"recipientGroup": "all_driver", "title": "c"})
	voided := w.send(ta, map[string]any{"recipientGroup": "all_driver", "title": "voided"})
	expect(t, w.del("/v1/broadcasts/"+voided, ta), http.StatusNoContent, "")
	expect(t, w.markRead("d.a@example.test", all), http.StatusOK, "")

	mine := func(email string) []string {
		t.Helper()
		r := w.call(w.public, http.MethodGet, "/v1/mobile/broadcasts", nil, bearer(w.login(email))...)
		return listIDs(t, r)
	}
	if got := mine("d.o@example.test"); !slices.Equal(got, []string{own, all, legacy}) {
		t.Errorf("own driver sees %v", got)
	}
	if got := mine("d.a@example.test"); !slices.Equal(got, []string{all, legacy}) {
		t.Errorf("carrier driver sees %v", got)
	}
	if got := mine("d.c@example.test"); !slices.Equal(got, []string{cOnly, legacy}) {
		t.Errorf("independent carrier's driver sees %v", got)
	}
	r := w.call(w.public, http.MethodGet, "/v1/mobile/broadcasts?limit=1", nil, bearer(w.login("d.a@example.test"))...)
	item := r.list()[0].(map[string]any)
	if item["id"] != all || item["read"] != true || item["readAt"] == nil || item["title"] != "all" || item["ref"] != all || r.next() == "" {
		t.Fatalf("carrier driver page %s", r.raw)
	}
	r = w.call(w.public, http.MethodGet, "/v1/mobile/broadcasts?limit=1&cursor="+r.next(), nil, bearer(w.login("d.a@example.test"))...)
	if item := r.list()[0].(map[string]any); item["id"] != legacy || item["ref"] != "fsLegacy" || item["read"] != false {
		t.Fatalf("second page %s", r.raw)
	}
	r = w.call(w.public, http.MethodGet, "/v1/mobile/broadcasts/latest", nil, bearer(w.login("d.c@example.test"))...)
	if r.status != http.StatusOK || r.data()["id"] != cOnly {
		t.Fatalf("latest %d %s", r.status, r.raw)
	}
	// Staff hold no mobile:broadcasts.
	expect(t, w.call(w.public, http.MethodGet, "/v1/mobile/broadcasts", nil, bearer(ta)...), http.StatusForbidden, authz.CodePermissionDenied)
}

// AC: one FCM "broadcast" push per recipient, through notify.fcm in-process over the FCM stand-in: every device of every
// recipient once (a recipient with two devices gets it on both), nobody else, the legacy title and body; a redelivery
// sends nothing more; a broadcast voided before its message is read pushes nothing.
func TestOnePushPerRecipient(t *testing.T) {
	w := newWorld(t)
	ta := w.login("ta.o@example.test")
	w.exec(`INSERT INTO device_tokens (user_id, install_id, token, platform) VALUES ($1, 'second', 'tok-second', 'android')`, w.uDA)
	id := w.send(ta, map[string]any{"recipientGroup": "subcontractor", "title": "แจ้งเตือน", "messageText": strings.Repeat("ข", 90)})
	srv, f := w.fcm()
	e := w.events(id)[0]
	if err := w.deliver(f, e); err != nil {
		t.Fatal(err)
	}
	sent := srv.Sent()
	if len(sent) != 3 {
		t.Fatalf("%d pushes, want 3 (dOs, dA twice)", len(sent))
	}
	for _, tok := range []string{"tok-" + w.uDOs, "tok-" + w.uDA, "tok-second"} {
		got := srv.SentTo(tok)
		if len(got) != 1 {
			t.Fatalf("%s got %d pushes", tok, len(got))
		}
		m := got[0].Message
		if m.Notification == nil || m.Notification.Title != "แจ้งเตือน" || m.Notification.Body != strings.Repeat("ข", 80)+"…" ||
			m.Data["type"] != "broadcast" {
			t.Fatalf("%s push %+v", tok, m)
		}
	}
	for _, tok := range []string{"tok-" + w.uDO, "tok-" + w.uDC, "tok-" + w.uDOi} {
		if len(srv.SentTo(tok)) != 0 {
			t.Fatalf("%s was not a recipient", tok)
		}
	}
	if err := w.deliver(f, e); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.Sent()); n != 3 {
		t.Fatalf("a redelivery sent again: %d", n)
	}
	if n := w.count(`SELECT count(*) FROM notification_deliveries WHERE kind = 'broadcast' AND status = 'sent'`); n != 3 {
		t.Fatalf("%d delivery rows, want 3", n)
	}
	// Voided before the push: nothing.
	late := w.send(ta, map[string]any{"recipientGroup": "all_driver"})
	expect(t, w.del("/v1/broadcasts/"+late, ta), http.StatusNoContent, "")
	srv2, f2 := w.fcm()
	if err := w.deliver(f2, w.events(late)[0]); err != nil {
		t.Fatal(err)
	}
	if n := len(srv2.Sent()); n != 0 {
		t.Fatalf("a voided broadcast pushed %d times", n)
	}
}
