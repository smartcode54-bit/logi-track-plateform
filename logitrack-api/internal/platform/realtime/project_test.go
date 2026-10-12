package realtime

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/scope"
)

const party = "0199c000-0000-7000-8000-0000000000a1"

var (
	tenantTasks   = TenantTopic(tid, FamilyTasks)
	tenantTrips   = TenantTopic(tid, FamilyTrips)
	dispatchTasks = DispatchTopic(party, FamilyTasks)
	dispatchTrips = DispatchTopic(party, FamilyTrips)
	driverQueue   = DriverTopic(party)
)

func decode(t *testing.T, b json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return m
}

// costKey reports a key that names a price, a cost, a billing value, a party or PII (the AC of #53 for dispatch
// topics: no cost field).
func costKey(k string) bool {
	k = strings.ToLower(k)
	for _, w := range []string{"billing", "price", "cost", "amount", "thb", "rate", "fee", "fuel", "party", "customer",
		"phone", "mobile", "email", "idcard", "licensetype", "licenseexpired", "licensefile", "evidence", "ocr", "review",
		"legacy", "name"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// A payload with everything a careless producer could add: the realtime copies keep the thin keys only, and a
// dispatch copy no billing, party, PII or cost field (AC of #53).
func TestProjectStripsTheOperationsEvents(t *testing.T) {
	full := json.RawMessage(`{"entity":"trip","id":"t1","tenantId":"` + tid + `","at":"2026-10-11T03:00:00Z","source":"mirror",
		"created":false,"changed":["status","deliveredAt","billingPartyId","evidenceToken","reviewStatus"],"status":"delivered",
		"tripNo":"TRIP1","taskId":"k1","legacyDocId":"TRIP1","customerPartyId":"` + party + `","billingEstimateThb":1234.5,
		"billingBaseRateThb":1000,"billingRateMultiplier":1.1,"amountThb":5,"costThb":3,"driverName":"สมชาย ใจดี",
		"driverPhone":"0800000001","nested":{"billingEstimateThb":1},"billingDateMoved":true}`)

	tenant, ok := Project(tenantTrips, "trip.delivered", full)
	if !ok {
		t.Fatal("tenant trips dropped trip.delivered")
	}
	got := decode(t, tenant)
	for k := range got {
		if !slices.Contains(ThinKeys(), k) {
			t.Errorf("tenant copy carries %s", k)
		}
	}
	if got["status"] != "delivered" || got["tripNo"] != "TRIP1" || got["billingDateMoved"] != true || len(got["changed"].([]any)) != 5 {
		t.Errorf("tenant copy lost a thin key: %v", got)
	}

	disp, ok := Project(dispatchTrips, "trip.delivered", full)
	if !ok {
		t.Fatal("dispatch trips dropped trip.delivered")
	}
	got = decode(t, disp)
	for k := range got {
		if costKey(k) || !slices.Contains(ThinKeys(), k) {
			t.Errorf("dispatch copy carries %s", k)
		}
	}
	changed := got["changed"].([]any)
	if !slices.Equal(changed, []any{"status", "deliveredAt"}) {
		t.Errorf("dispatch changed %v, want the scope_trips columns only", changed)
	}
	if got["id"] != "t1" || got["tenantId"] != tid || got["at"] != "2026-10-11T03:00:00Z" {
		t.Errorf("dispatch copy %v", got)
	}

	q, ok := Project(driverQueue, "trip.delivered", full)
	if !ok {
		t.Fatal("driver topic dropped the event")
	}
	if got := decode(t, q); got["driverId"] != nil || got["status"] != "delivered" {
		t.Errorf("driver copy %v", got)
	}
}

func TestProjectDispatchRules(t *testing.T) {
	cases := []struct {
		name, topic, typ, data string
		send                   bool
		changed                []any // nil: no changed key expected
	}{
		{"created", dispatchTasks, "task.created", `{"created":true,"changed":[]}`, true, []any{}},
		{"no changed key", dispatchTasks, "task.updated", `{}`, true, nil},
		{"empty payload", dispatchTasks, "task.updated", ``, true, nil},
		{"visible change", dispatchTasks, "task.updated", `{"changed":["runOrder","createdBy"]}`, true, []any{"runOrder"}},
		{"stops", dispatchTasks, "task.updated", `{"changed":["deliveryStops"]}`, true, []any{"deliveryStops"}},
		{"hidden change only", dispatchTasks, "task.updated", `{"changed":["lineCheckinNotifiedAt","legacyDocId"]}`, false, nil},
		{"party moved", dispatchTasks, "task.updated", `{"changed":["billingPartyId"]}`, true, []any{}},
		{"stop party moved", dispatchTasks, "task.updated", `{"changed":["sourceLinkedPartyId","runOrder"]}`, true, []any{"runOrder"}},
		{"tenant move", dispatchTasks, "task.updated", `{"changed":["tenantId"]}`, true, []any{"tenantId"}},
		{"reassigned", dispatchTasks, "task.reassigned", `{"changed":["driverId","status"]}`, true, []any{"driverId", "status"}},
		{"trip review only", dispatchTrips, "trip.resubmitted", `{"changed":["resubmittedAt","reviewStatus"]}`, false, nil},
		{"trip photos", dispatchTrips, "trip.updated", `{"changed":["photosOrStops"]}`, true, []any{"photosOrStops"}},
		{"standby", dispatchTrips, "standby.completed", `{"changed":["endedAt","billingEstimateThb"]}`, true, []any{"endedAt"}},
		{"standby customer", dispatchTrips, "standby.customer_assigned", `{"changed":["customerPartyId"]}`, false, nil},
		{"incident", dispatchTrips, "incident.created", `{"created":true,"tripId":"x"}`, true, nil},
		{"trip.priced", dispatchTrips, "trip.priced", `{}`, false, nil},
		{"trip.repriced", dispatchTrips, "trip.repriced", `{}`, false, nil},
		{"standby.priced", dispatchTrips, "standby.priced", `{}`, false, nil},
		{"delivered_at_changed", dispatchTrips, "trip.delivered_at_changed", `{}`, false, nil},
		{"task on trips", dispatchTrips, "task.updated", `{}`, false, nil},
		{"trip on tasks", dispatchTasks, "trip.updated", `{}`, false, nil},
		{"not operational", dispatchTrips, "hubs.changed", `{}`, false, nil},
		{"unreadable changed", dispatchTasks, "task.updated", `{"changed":"status"}`, false, nil},
		{"changed objects", dispatchTasks, "task.updated", `{"changed":[{"billingEstimateThb":1}]}`, false, nil},
		{"not an object", dispatchTasks, "task.updated", `[]`, false, nil},
	}
	for _, c := range cases {
		out, ok := Project(c.topic, c.typ, json.RawMessage(c.data))
		if ok != c.send {
			t.Errorf("%s: sent %v, want %v (%s)", c.name, ok, c.send, out)
			continue
		}
		if !ok {
			continue
		}
		got := decode(t, out)
		ch, has := got["changed"]
		switch {
		case c.changed == nil && has:
			t.Errorf("%s: changed %v", c.name, ch)
		case c.changed != nil && (!has || !slices.Equal(ch.([]any), c.changed)):
			t.Errorf("%s: changed %v, want %v", c.name, ch, c.changed)
		}
	}
}

// The party a row has just left (leftPartyIds) gets the envelope only on its dispatch topic: in one 3.x save a task
// billed to the party moves to another one with a new driver, a trip leaves the party as it is delivered; the parties
// that hold the row keep their copy, and an unreadable list gives every dispatch topic the envelope (fail closed).
func TestProjectLeftParty(t *testing.T) {
	const other = "0199c000-0000-7000-8000-0000000000b2"
	envelope := map[string]bool{"entity": true, "id": true, "tenantId": true, "at": true, "changed": true}
	cases := []struct {
		name, typ, family, data string
		values                  []string // the thin values the party that holds the row still gets
	}{
		{"task reassigned to another party", "task.reassigned", FamilyTasks,
			`{"entity":"task","id":"k1","tenantId":"` + tid + `","at":"2026-10-11T03:00:00Z","source":"mirror","created":false,` +
				`"changed":["billingPartyId","driverId","planDate"],"driverId":"0199c000-0000-7000-8000-0000000000c1",` +
				`"previousDriverId":"0199c000-0000-7000-8000-0000000000c2","planDate":"2026-10-05","status":"assigned",` +
				`"taskType":"first_mile","leftPartyIds":["` + strings.ToUpper(party) + `"]}`,
			[]string{"driverId", "previousDriverId", "planDate", "status", "taskType"}},
		{"trip delivered as it leaves", "trip.delivered", FamilyTrips,
			`{"entity":"trip","id":"t1","tenantId":"` + tid + `","at":"2026-10-11T03:00:00Z","source":"mirror","created":false,` +
				`"changed":["billingPartyId","status"],"status":"delivered","tripNo":"TRIP300","taskId":"k1",` +
				`"leftPartyIds":["` + party + `"]}`,
			[]string{"status", "tripNo", "taskId"}},
		{"only a hidden column changed besides the party", "trip.updated", FamilyTrips,
			`{"entity":"trip","id":"t1","tenantId":"` + tid + `","changed":["billingPartyId","reviewStatus"],"status":"in_transit",` +
				`"leftPartyIds":["` + party + `"]}`,
			[]string{"status"}},
	}
	for _, c := range cases {
		out, ok := Project(DispatchTopic(party, c.family), c.typ, json.RawMessage(c.data))
		if !ok {
			t.Errorf("%s: the party the row left got nothing", c.name)
			continue
		}
		got := decode(t, out)
		for k := range got {
			if !envelope[k] {
				t.Errorf("%s: the party the row left reads %s: %s", c.name, k, out)
			}
		}
		if ch, _ := got["changed"].([]any); ch == nil || len(ch) != 0 || got["id"] == nil || got["entity"] == nil || got["tenantId"] != tid {
			t.Errorf("%s: the envelope %s", c.name, out)
		}
		held, ok := Project(DispatchTopic(other, c.family), c.typ, json.RawMessage(c.data))
		if !ok {
			t.Fatalf("%s: the party that holds the row got nothing", c.name)
		}
		g := decode(t, held)
		for _, k := range c.values {
			if g[k] == nil {
				t.Errorf("%s: the party that holds the row lost %s: %s", c.name, k, held)
			}
		}
		if g[LeftPartiesKey] != nil {
			t.Errorf("%s: %s reached a dispatch topic", c.name, LeftPartiesKey)
		}
		tenant, _ := Project(TenantTopic(tid, c.family), c.typ, json.RawMessage(c.data))
		if g := decode(t, tenant); g[LeftPartiesKey] != nil || g["status"] == nil {
			t.Errorf("%s: the tenant copy %s", c.name, tenant)
		}
	}
	// A priced event still never reaches the party, and a list the relay cannot read fails closed.
	if out, ok := Project(dispatchTrips, "trip.priced", json.RawMessage(`{"leftPartyIds":["`+party+`"]}`)); ok {
		t.Errorf("trip.priced reached the party the row left: %s", out)
	}
	for _, bad := range []string{`"` + party + `"`, `[{"id":"` + party + `"}]`, `{}`} {
		data := `{"entity":"task","id":"k1","changed":["runOrder"],"driverId":"d","status":"assigned","leftPartyIds":` + bad + `}`
		for _, p := range []string{party, other} {
			out, ok := Project(DispatchTopic(p, FamilyTasks), "task.updated", json.RawMessage(data))
			if !ok {
				t.Errorf("leftPartyIds %s: %s got nothing", bad, p)
				continue
			}
			if g := decode(t, out); g["driverId"] != nil || g["status"] != nil || len(g["changed"].([]any)) != 0 {
				t.Errorf("leftPartyIds %s: %s reads %s", bad, p, out)
			}
		}
	}
	// A null list names nobody.
	out, ok := Project(dispatchTasks, "task.updated", json.RawMessage(`{"changed":["runOrder"],"status":"assigned","leftPartyIds":null}`))
	if g := decode(t, out); !ok || g["status"] != "assigned" {
		t.Errorf("a null leftPartyIds: %s %v", out, ok)
	}
}

// Events of other domains keep their payload byte for byte on their topics (job.updated carries the whole job), and
// a value of a thin key that is not a scalar is dropped, never sent.
func TestProjectLeavesOtherEventsAndRejectsNonScalars(t *testing.T) {
	job := json.RawMessage(`{"id":"j","type":"billing.backfill-trips","status":"succeeded","result":{"written":3}}`)
	for _, topic := range []string{UserTopic(tid), TopicGlobal, TenantTopic(tid, FamilyBilling)} {
		out, ok := Project(topic, "job.updated", job)
		if !ok || string(out) != string(job) {
			t.Errorf("%s: %s %v", topic, out, ok)
		}
	}
	out, ok := Project(tenantTasks, "task.updated", json.RawMessage(`{"status":{"a":1},"taskType":["x"],"planDate":"2026-10-11"}`))
	if got := decode(t, out); !ok || len(got) != 1 || got["planDate"] != "2026-10-11" {
		t.Errorf("non-scalar thin values: %v %v", got, ok)
	}
	if out, ok := Project(tenantTasks, "task.updated", json.RawMessage(`null`)); ok {
		t.Errorf("a null payload of an operations event was sent: %s", out)
	}
	if _, ok := Project(tenantTasks, "task.updated", json.RawMessage(`{"changed":"status"}`)); !ok {
		t.Error("an unreadable changed[] drops the key on a tenant topic, not the event")
	}
}

// A leave event keeps the leave keys only on every topic (T45): the rejection's free-text reason, which notify.fcm reads
// from the outbox row, never reaches tenant:{tid}:hr (also open to hr:view_payroll, which does not read leave requests)
// nor the driver's stream, and the driver's copy names no driver.
func TestProjectThinsTheLeaveEvents(t *testing.T) {
	const did = "0199c000-0000-7000-8000-0000000000d1"
	full := json.RawMessage(`{"entity":"leave_request","id":"l1","tenantId":"` + tid + `","status":"rejected","from":"pending",
		"driverId":"` + did + `","reason":"sick note: diagnosis XYZ","driverName":"สมชาย","note":{"reason":"x"},"at":"2026-10-11T03:00:00Z"}`)
	hrTopic := TenantTopic(tid, FamilyHR)
	out, ok := Project(hrTopic, "leave.decided", full)
	if !ok {
		t.Fatal("tenant hr dropped leave.decided")
	}
	got := decode(t, out)
	want := map[string]any{"entity": "leave_request", "id": "l1", "tenantId": tid, "status": "rejected", "from": "pending",
		"driverId": did, "at": "2026-10-11T03:00:00Z"}
	if !maps.Equal(got, want) {
		t.Errorf("tenant hr copy %v", got)
	}
	out, ok = Project(DriverTopic(did), "leave.decided", full)
	if !ok {
		t.Fatal("the driver's topic dropped leave.decided")
	}
	got = decode(t, out)
	delete(want, "driverId")
	if !maps.Equal(got, want) {
		t.Errorf("driver copy %v", got)
	}
	for k := range got {
		if !slices.Contains(LeaveKeys(), k) {
			t.Errorf("driver copy carries %s", k)
		}
	}
	if _, ok := Project(DispatchTopic(party, FamilyTrips), "leave.decided", full); ok {
		t.Error("a dispatch topic carries leave.decided")
	}
	if _, ok := Project(hrTopic, "leave.decided", json.RawMessage(`"x"`)); ok {
		t.Error("a leave payload that is not an object was sent")
	}
	// The payroll events of the same topic keep their payload.
	pay := json.RawMessage(`{"period":"2026-10","round":1}`)
	if out, ok := Project(hrTopic, "payroll.approved", pay); !ok || string(out) != string(pay) {
		t.Errorf("payroll.approved %s %v", out, ok)
	}
}

// The dispatch tables: every type travels on its family, no price ever does, and every changed[] name is a column a
// scope projection may show (scope.IsHidden; the integration test of internal/sse checks them against the views).
func TestDispatchTables(t *testing.T) {
	for _, typ := range []string{"trip.priced", "trip.repriced", "standby.priced", "trip.delivered_at_changed", "standby.customer_assigned"} {
		if _, ok := DispatchFamily(typ); ok {
			t.Errorf("%s travels on a dispatch topic", typ)
		}
	}
	for typ, family := range dispatchTypes {
		entity := OperationsEntity(typ)
		if entity == "" || (entity == "task") != (family == FamilyTasks) {
			t.Errorf("%s on %s", typ, family)
		}
	}
	for _, entity := range []string{"task", "trip", "standby", "incident"} {
		names := DispatchColumns(entity)
		if len(names) == 0 {
			t.Fatalf("no dispatch columns for %s", entity)
		}
		for _, n := range names {
			if scope.IsHidden(snake(n)) || strings.HasSuffix(n, partySuffix) || costKey(n) {
				t.Errorf("%s: %s is not a column a dispatcher may see", entity, n)
			}
		}
	}
	if DispatchColumns("job") != nil {
		t.Error("an unknown entity has dispatch columns")
	}
	keys := slices.Sorted(maps.Keys(thinKeys))
	if !slices.Equal(keys, slices.Sorted(slices.Values(ThinKeys()))) {
		t.Errorf("thin keys %v", keys)
	}
}

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
