package cache

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func mustKeyspace(t *testing.T, env string) Keyspace {
	t.Helper()
	ks, err := NewKeyspace(env)
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

// allKeys builds one key with every builder of the keyspace (Appendix B §B.6.2, Appendix C §C.4.14).
func allKeys(ks Keyspace) map[string]string {
	return map[string]string{
		"HubsAll":              ks.HubsAll(),
		"HubsNameToCode":       ks.HubsNameToCode(),
		"HubsCodeToName":       ks.HubsCodeToName(),
		"RateCard":             ks.RateCard("p1"),
		"Customer":             ks.Customer("c1"),
		"Settings":             ks.Settings("mobile_app"),
		"PeriodLocks":          ks.PeriodLocks("p1"),
		"FuelRetail":           ks.FuelRetail("th"),
		"GeoReverse":           ks.GeoReverse(13.75, 100.5),
		"VehicleLocations":     ks.VehicleLocations("t1"),
		"TenantOwnFleet":       ks.TenantOwnFleet(),
		"TenantSubtenants":     ks.TenantSubtenants("t1"),
		"WebFlags":             ks.WebFlags(),
		"MirrorAck":            ks.MirrorAck("trips", "d1"),
		"AuthRefresh":          ks.AuthRefresh("ab"),
		"AuthSessionRevoked":   ks.AuthSessionRevoked("s1"),
		"AuthUserVersion":      ks.AuthUserVersion("u1"),
		"AuthSSETicket":        ks.AuthSSETicket("x"),
		"AuthGoogleNonce":      ks.AuthGoogleNonce("n"),
		"AuthPasswordChange":   ks.AuthPasswordChange("x"),
		"AuthFirebaseUID":      ks.AuthFirebaseUID("f"),
		"RBACVersion":          ks.RBACVersion(),
		"RBACCapabilities":     ks.RBACCapabilities("platform", "tenant_admin", 3),
		"IdemHTTP":             ks.IdemHTTP("u1", "k"),
		"IdemLock":             ks.IdemLock("u1", "k"),
		"IdemFCM":              ks.IdemFCM("m", "t"),
		"IdemTasksChangedPush": ks.IdemTasksChangedPush("d1"),
		"IdemLine":             ks.IdemLine("r", "delivered"),
		"IdemWebhook":          ks.IdemWebhook("p", "d"),
		"RateLimit":            ks.RateLimit("login_ip", "s"),
		"SSEConnections":       ks.SSEConnections("u1"),
		"Channel":              ks.Channel("tenant:t1:tasks"),
		"CacheChannel":         ks.CacheChannel(),
		"RealtimeSeq":          ks.RealtimeSeq(),
		"RealtimeLog":          ks.RealtimeLog("user:u1"),
		"JobLock":              ks.JobLock("payroll.run", "2026-10"),
		"CronLock":             ks.CronLock("rtlog.trim", "2026-10-10T04:00"),
	}
}

func TestEveryKeyIsUnderThePrefixAndAListedNamespace(t *testing.T) {
	for _, env := range AppEnvs {
		ks := mustKeyspace(t, env)
		for name, key := range allKeys(ks) {
			if !strings.HasPrefix(key, "lt:"+env+":") {
				t.Errorf("%s = %q: not under lt:%s:", name, key, env)
			}
			if ns, ok := ks.Namespace(key); !ok {
				t.Errorf("%s = %q: namespace %q is not one of %v", name, key, ns, Namespaces)
			}
		}
	}
}

func TestSpecKeyShapes(t *testing.T) {
	ks := mustKeyspace(t, "prod")
	want := map[string]string{
		"HubsAll":            "lt:prod:cache:hubs:all",
		"HubsNameToCode":     "lt:prod:cache:hubs:n2c",
		"HubsCodeToName":     "lt:prod:cache:hubs:c2n",
		"PeriodLocks":        "lt:prod:cache:period_locks:p1",
		"TenantSubtenants":   "lt:prod:cache:tenant:subtenants:t1",
		"MirrorAck":          "lt:prod:cache:mirror_ack:trips:d1",
		"AuthPasswordChange": "lt:prod:auth:pwchg:x",
		"AuthFirebaseUID":    "lt:prod:auth:fbuid:f",
		"IdemHTTP":           "lt:prod:idem:http:u1:k",
		"IdemLock":           "lt:prod:idem:lock:u1:k",
		"RateLimit":          "lt:prod:rl:login_ip:s",
		"CacheChannel":       "lt:prod:rt:cache",
		"RealtimeSeq":        "lt:prod:rtlog:seq",
		"GeoReverse":         "lt:prod:cache:geo:rev:13.75000:100.50000",
		"RBACCapabilities":   "lt:prod:rbac:caps:platform:tenant_admin:3",
	}
	got := allKeys(ks)
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %q, want %q", name, got[name], w)
		}
	}
}

func TestHubMapsAreTwoDistinctKeys(t *testing.T) {
	ks := mustKeyspace(t, "local")
	if ks.HubsNameToCode() == ks.HubsCodeToName() {
		t.Fatal("cache:hubs:n2c and cache:hubs:c2n must be distinct keys (R53)")
	}
}

func TestParseKeyspace(t *testing.T) {
	if ks, err := ParseKeyspace("", "dev"); err != nil || ks.Prefix() != "lt:dev:" {
		t.Fatalf("empty prefix: %v %q", err, ks.Prefix())
	}
	if ks, err := ParseKeyspace("lt:prod:", "prod"); err != nil || ks.Prefix() != "lt:prod:" {
		t.Fatalf("matching prefix: %v %q", err, ks.Prefix())
	}
	for _, c := range [][2]string{{"lt:dev:", "prod"}, {"logitrack:", "local"}, {"lt:local", "local"}, {"", "test"}, {"", ""}} {
		if _, err := ParseKeyspace(c[0], c[1]); err == nil {
			t.Errorf("ParseKeyspace(%q, %q) accepted", c[0], c[1])
		} else if c[0] != "" && strings.Contains(err.Error(), c[0]) {
			t.Errorf("error echoes the value: %v", err)
		}
	}
}

func TestNamespaceRejectsForeignKeys(t *testing.T) {
	ks := mustKeyspace(t, "local")
	for _, k := range []string{"lt:dev:cache:x", "lt:local:sess:x", "lt:local:cache", "cache:hubs:all", "lt:local:", "lt:local:cachex:y"} {
		if ns, ok := ks.Namespace(k); ok {
			t.Errorf("Namespace(%q) = %q, want rejected", k, ns)
		}
	}
	if ns, ok := ks.Namespace("lt:local:rtlog:tenant:t1"); !ok || ns != NSRealtimeLog {
		t.Errorf("rtlog key: %q %v", ns, ok)
	}
}

func TestPatternEscapesGlobCharacters(t *testing.T) {
	ks := mustKeyspace(t, "local")
	if got := ks.Pattern(NSCache, "tenant", "subtenants"); got != "lt:local:cache:tenant:subtenants:*" {
		t.Errorf("Pattern = %q", got)
	}
	if got := ks.Pattern(NSCache, "a*b?[c]"); got != `lt:local:cache:a\*b\?\[c\]:*` {
		t.Errorf("Pattern escaping = %q", got)
	}
}

func TestZeroKeyspacePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("zero Keyspace built a key")
		}
	}()
	_ = Keyspace{}.HubsAll()
}

func TestKeysForEvents(t *testing.T) {
	c := New(nil, mustKeyspace(t, "local"))
	ks := c.Keys()
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	cases := []struct {
		ev   Event
		want []string
	}{
		{Event{RoutingKey: "hubs.changed"}, []string{ks.HubsAll(), ks.HubsCodeToName(), ks.HubsNameToCode()}},
		{Event{RoutingKey: "ratecard.changed", AggregateID: "p1"}, []string{ks.RateCard("p1")}},
		{Event{RoutingKey: "ratecard.changed", AggregateID: "x", Payload: raw(map[string]string{"billingPartyId": "p2"})}, []string{ks.RateCard("p2")}},
		{Event{RoutingKey: "customers.changed", AggregateID: "c1"}, []string{ks.Customer("c1")}},
		{Event{RoutingKey: "settings.changed", AggregateID: "mobile_app"}, []string{ks.Settings("mobile_app")}},
		{Event{RoutingKey: "statement.status_changed", AggregateID: "s1", Payload: raw(map[string]string{"billingPartyId": "p1"})}, []string{ks.PeriodLocks("p1")}},
		{Event{RoutingKey: "statement.created", AggregateID: "s1"}, nil},
		{Event{RoutingKey: "tenant.updated", AggregateID: "t9", Payload: raw(map[string]string{"contractorTenantId": "t1", "previousContractorTenantId": "t2"})},
			[]string{ks.TenantOwnFleet(), ks.TenantSubtenants("t1"), ks.TenantSubtenants("t2")}},
		{Event{RoutingKey: "tenant.created", AggregateID: "t9"}, []string{ks.TenantOwnFleet()}},
		{Event{RoutingKey: "trip.delivered", AggregateID: "x"}, nil},
	}
	for _, tc := range cases {
		got := c.KeysFor(tc.ev)
		want := slices.Clone(tc.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: KeysFor = %v, want %v", tc.ev.RoutingKey, got, want)
		}
	}
	for _, rk := range InvalidatingEvents {
		if strings.HasPrefix(rk, "statement.") || rk == "tenant.created" {
			continue // keyed by payload fields, covered above
		}
		if len(c.KeysFor(Event{RoutingKey: rk, AggregateID: "id"})) == 0 {
			t.Errorf("%s drops no key", rk)
		}
	}
}
