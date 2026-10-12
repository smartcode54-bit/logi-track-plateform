package coexist

import (
	"context"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

func TestParsePlan(t *testing.T) {
	p, errs := ParsePlan("", "", "")
	if len(errs) != 0 || len(p.Projection) != 0 || p.Mirror.Mode("drivers") != "full" || p.Mirror.Mode("tasks") != "full" {
		t.Fatalf("P0 plan %+v %v", p, errs)
	}
	p, errs = ParsePlan("auth,masterdata", "", "hubs, drivers")
	if len(errs) != 0 || strings.Join(p.Projection, ",") != "hubs,drivers" || p.Mirror.Mode("drivers") != "fields" || !p.Owned["masterdata"] {
		t.Fatalf("P1 plan %+v %v", p, errs)
	}
	p, errs = ParsePlan("all", "", "")
	// Every projection, the holidays (T45) and then the billing fields of trips and standby records (T38) last.
	if len(errs) != 0 || len(p.Projection) != 14 || p.Projection[10] != "settings" || p.Projection[11] != "holidays" ||
		p.Projection[12] != "trip_billing" || p.Projection[13] != "standby_billing" {
		t.Fatalf("all: %+v %v", p, errs)
	}
	_, errs = ParsePlan("masterdata,bogus_zz", "tasks,users_zz", "projected_zz")
	joined := strings.Join(errs, "; ")
	for _, want := range []string{"PG_OWNED_DOMAINS", "ETL_MIRROR_COLLECTIONS", "COMPAT_PROJECTION_COLLECTIONS"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors %q lack %s", joined, want)
		}
	}
	if strings.Contains(joined, "_zz") {
		t.Errorf("an error echoes a value: %s", joined)
	}
}

// A disabled projection answers every event without a projection; an undecodable or unknown event is permanent.
func TestShadowDisabledAndBadEvents(t *testing.T) {
	s, err := NewShadow(ShadowConfig{Metrics: NewMetrics(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Handle(context.Background(), &mq.Delivery{RoutingKey: "hubs.changed", Body: []byte(`{"ids":["0199c000-0000-7000-8000-000000000001"]}`)}); err != nil {
		t.Fatalf("disabled: %v", err)
	}
	for _, d := range []*mq.Delivery{{RoutingKey: "hubs.changed", Body: []byte(`{"ids":["nope"]}`)}, {RoutingKey: "user.created", Body: []byte(`{}`)}} {
		if err := s.Handle(context.Background(), d); !mq.IsPermanent(err) {
			t.Errorf("%s %s: %v", d.RoutingKey, d.Body, err)
		}
	}
	if _, err := NewShadow(ShadowConfig{Enabled: true}); err == nil {
		t.Error("an enabled projection without a pool and a client was built")
	}
	if _, err := NewSync(SyncConfig{Enabled: true}); err == nil {
		t.Error("an enabled mirror without its pools was built")
	}
}
