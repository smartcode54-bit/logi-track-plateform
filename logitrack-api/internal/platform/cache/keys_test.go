package cache

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
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
		"CacheGen":             ks.CacheGen("ratecard"),
		"AuthRefresh":          ks.AuthRefresh("ab"),
		"AuthSessionRevoked":   ks.AuthSessionRevoked("s1"),
		"AuthUserVersion":      ks.AuthUserVersion("u1"),
		"AuthSSETicket":        ks.AuthSSETicket("x"),
		"AuthGoogleNonce":      ks.AuthGoogleNonce("n"),
		"AuthPasswordChange":   ks.AuthPasswordChange("x"),
		"AuthFirebaseUID":      ks.AuthFirebaseUID("f"),
		"RBACVersion":          ks.RBACVersion(),
		"RBACCapabilities":     ks.RBACCapabilities("platform", "tenant_admin", 3, "1a2b3c4d"),
		"IdemHTTP":             ks.IdemHTTP("u1", "k"),
		"IdemLock":             ks.IdemLock("u1", "k"),
		"IdemFCM":              ks.IdemFCM("m", "t"),
		"IdemFCMLog":           ks.IdemFCMLog("m"),
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
	for _, env := range config.AppEnvs {
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
		"CacheGen":           "lt:prod:cache:gen:ratecard",
		"AuthPasswordChange": "lt:prod:auth:pwchg:x",
		"AuthFirebaseUID":    "lt:prod:auth:fbuid:f",
		"IdemHTTP":           "lt:prod:idem:http:u1:k",
		"IdemLock":           "lt:prod:idem:lock:u1:k",
		"IdemFCMLog":         "lt:prod:idem:fcm:m:log",
		"RateLimit":          "lt:prod:rl:login_ip:s",
		"CacheChannel":       "lt:prod:rt:cache",
		"RealtimeSeq":        "lt:prod:rtlog:seq",
		"GeoReverse":         "lt:prod:cache:geo:rev:13.75000:100.50000",
		"RBACCapabilities":   "lt:prod:rbac:caps:platform:tenant_admin:3:1a2b3c4d",
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

func TestStaleForEvents(t *testing.T) {
	c := New(nil, mustKeyspace(t, "local"))
	ks := c.Keys()
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	sweep := func(parts ...string) []string { return []string{ks.Pattern(NSCache, parts...)} }
	cases := []struct {
		name  string
		ev    Event
		keys  []string
		sweep []string
	}{
		{"hubs", Event{RoutingKey: "hubs.changed", Payload: raw(map[string]any{"ids": []string{"h1"}})},
			[]string{ks.HubsAll(), ks.HubsCodeToName(), ks.HubsNameToCode()}, nil},
		// Appendix B §B.4.3 shapes: the payload names the billing party.
		{"ratecard with party", Event{RoutingKey: "ratecard.changed", AggregateID: "rc_1760000000000",
			Payload: raw(map[string]string{"billingPartyId": "p2", "customerId": "cust-uuid", "table": "entries"})},
			[]string{ks.RateCard("p2")}, nil},
		// No party in the payload: aggregate_id is not the party id, so every rate card goes.
		{"ratecard without party", Event{RoutingKey: "ratecard.changed", AggregateID: "rc_1760000000000",
			Payload: raw(map[string]string{"customerId": "cust-uuid", "table": "entries"})}, nil, sweep("ratecard")},
		{"customers ids", Event{RoutingKey: "customers.changed", Payload: raw(map[string]any{"ids": []string{"c1", "c2"}})},
			[]string{ks.Customer("c1"), ks.Customer("c2")}, nil},
		{"customers aggregate", Event{RoutingKey: "customers.changed", AggregateID: "c1"}, []string{ks.Customer("c1")}, nil},
		{"customers nothing", Event{RoutingKey: "customers.changed"}, nil, sweep("customer")},
		{"settings key", Event{RoutingKey: "settings.changed", AggregateID: "x", Payload: raw(map[string]string{"key": "mobile_app"})},
			[]string{ks.Settings("mobile_app")}, nil},
		{"settings aggregate", Event{RoutingKey: "settings.changed", AggregateID: "mobile_app", Payload: raw(map[string]string{"flavor": "prod"})},
			[]string{ks.Settings("mobile_app")}, nil},
		{"settings nothing", Event{RoutingKey: "settings.changed", Payload: raw(map[string]string{"flavor": "prod"})}, nil, sweep("settings")},
		{"statement with party", Event{RoutingKey: "statement.status_changed", AggregateID: "s1",
			Payload: raw(map[string]string{"billingPartyId": "p1", "statementId": "s1", "status": "sent"})}, []string{ks.PeriodLocks("p1")}, nil},
		// Without billingPartyId (an older producer): every period-lock hint goes, never none.
		{"statement status without party", Event{RoutingKey: "statement.status_changed", AggregateID: "s1",
			Payload: raw(map[string]string{"statementId": "s1", "status": "sent"})}, nil, sweep("period_locks")},
		// Appendix D EV3 as it was: {statementId, invoiceNumber}.
		{"statement created EV3", Event{RoutingKey: "statement.created", AggregateID: "@ST3",
			Payload: raw(map[string]string{"statementId": "@ST3", "invoiceNumber": "CJSF-202608-001"})}, nil, sweep("period_locks")},
		{"tenant contractors", Event{RoutingKey: "tenant.updated", AggregateID: "t9", Payload: raw(map[string]string{"contractorTenantId": "t1", "previousContractorTenantId": "t2"})},
			[]string{ks.TenantOwnFleet(), ks.TenantSubtenants("t1"), ks.TenantSubtenants("t2")}, nil},
		{"tenant without contractors", Event{RoutingKey: "tenant.created", AggregateID: "t9"}, []string{ks.TenantOwnFleet()}, sweep("tenant", "subtenants")},
		{"undecodable payload", Event{RoutingKey: "statement.created", Payload: json.RawMessage("{")}, nil, sweep("period_locks")},
		{"unrelated", Event{RoutingKey: "trip.delivered", AggregateID: "x"}, nil, nil},
	}
	for _, tc := range cases {
		got := c.StaleFor(tc.ev)
		want := slices.Clone(tc.keys)
		slices.Sort(want)
		if !slices.Equal(got.Keys, want) || !slices.Equal(got.Sweep, tc.sweep) {
			t.Errorf("%s: StaleFor = %+v, want keys %v sweep %v", tc.name, got, want, tc.sweep)
		}
	}
	// Every invalidating event drops something whatever its payload.
	for _, rk := range InvalidatingEvents {
		for _, ev := range []Event{{RoutingKey: rk}, {RoutingKey: rk, AggregateID: "id"}} {
			if st := c.StaleFor(ev); len(st.Keys)+len(st.Sweep) == 0 {
				t.Errorf("%+v drops nothing", ev)
			}
		}
	}
}

func TestCacheFamilyAndGenKeys(t *testing.T) {
	ks := mustKeyspace(t, "local")
	for key, want := range map[string]string{
		ks.RateCard("p1"):                   "ratecard",
		ks.TenantSubtenants("t1"):           "tenant",
		ks.Pattern(NSCache, "period_locks"): "period_locks",
		ks.HubsNameToCode():                 "hubs",
		ks.WebFlags():                       "web_flags",
		ks.AuthPasswordChange("x"):          "",
		"lt:dev:cache:ratecard:p1":          "",
		ks.Key(NSCache):                     "",
	} {
		if got := ks.cacheFamily(key); got != want {
			t.Errorf("cacheFamily(%q) = %q, want %q", key, got, want)
		}
	}
	if got := ks.CacheGen("ratecard"); got != "lt:local:cache:gen:ratecard" {
		t.Errorf("CacheGen = %q", got)
	}
}
