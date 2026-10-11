package etl

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
)

// The T24 load order puts every collection after the ones it links to (Appendix A §A.3.0).
func TestLoadOrderOfT24(t *testing.T) {
	order := MappedCollections()
	before := func(a, b string) {
		t.Helper()
		ia, ib := slices.Index(order, a), slices.Index(order, b)
		if ia < 0 || ib < 0 || ia >= ib {
			t.Errorf("%s must load before %s: %v", a, b, order)
		}
	}
	before("customer_rate_entries", "trip_records")
	before("customer_fuel_rate_adjustments", "trip_records")
	before("standby_rate_entries", "standby_records")
	before("hubs", "tasks")
	before("hubs", "hub_soc_distances")
	before("trucks", "truckAssignment")
	before("drivers", "truckAssignment")
	before("drivers", "mobile_installations")
	before("billing_statements", "vehicle_expenses")
	before("driver_penalties", "payroll")
	before("payroll", "transactions")
	before("chats", "messages")
	for _, n := range []string{"users", "permissions_config"} {
		if slices.Contains(order, n) {
			t.Errorf("%s is loaded by its own step, not by load", n)
		}
	}
}

func TestPlainJSON(t *testing.T) {
	at := time.Date(2026, 9, 1, 2, 3, 4, 5000, time.UTC)
	v := plain(map[string]any{"a": dump.Timestamp{Time: at}, "g": dump.GeoPoint{Lat: 1, Lng: 2}, "r": dump.Ref{Path: "x/y"},
		"b": dump.Bytes("hi"), "l": []any{dump.Timestamp{Time: at}, "s"}})
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":"2026-09-01T02:03:04.000005Z","b":"aGk=","g":{"lat":1,"lng":2},"l":["2026-09-01T02:03:04.000005Z","s"],"r":"x/y"}`
	if string(b) != want {
		t.Errorf("plain: %s", b)
	}
}

func TestDocIDPatterns(t *testing.T) {
	for id, want := range map[string][]string{
		"uid_own_2026-09_R1":      {"uid_own", "2026-09", "R1"},
		"a_b_c_2026-12_R2":        {"a_b_c", "2026-12", "R2"},
		"uid_2026-13_R1":          nil,
		"uid_2026-09_R3":          nil,
		"FXUID000000000000DRV1_x": nil,
	} {
		m := payrollDocID.FindStringSubmatch(id)
		if want == nil {
			if m != nil {
				t.Errorf("%s parsed as %v", id, m)
			}
			continue
		}
		if m == nil || !slices.Equal(m[1:], want) {
			t.Errorf("%s: %v, want %v", id, m, want)
		}
	}
	for no, ok := range map[string]bool{"FM-01092026-001": true, "LH-05092026-12": true, "FM-DUP-001": false, "FM-01092026": false} {
		if taskNoPattern.MatchString(no) != ok {
			t.Errorf("task number %s: %v", no, !ok)
		}
	}
}

func TestHubWinnerInputs(t *testing.T) {
	if s := hubSourceID(map[string]any{"hubId": " huba ", "hubCode": "X"}); s != "HUBA" {
		t.Errorf("hubSourceID: %q", s)
	}
	if s := hubSourceID(map[string]any{"source_id": "", "hubCode": "spk-gw"}); s != "SPK-GW" {
		t.Errorf("hubSourceID fallback: %q", s)
	}
	if n := nonEmpty(map[string]any{"a": "x", "b": "  ", "c": nil, "d": 0.0}); n != 2 {
		t.Errorf("nonEmpty: %d", n)
	}
}
