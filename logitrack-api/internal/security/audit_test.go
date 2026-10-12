package security_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// The security.audit consumer acknowledges what it can never append (wrong key, malformed body, an invalid event, an
// event that belongs in the transaction of its change) as permanent failures, before it touches the database.
func TestAuditRefusesPermanently(t *testing.T) {
	a := &security.Audit{}
	ok := security.Event{EventType: "login_failed", Severity: security.SeverityWarning, Summary: "x", OccurredAt: time.Now()}
	body := func(e security.Event) []byte {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	inTx := ok
	inTx.EventType = "user_role_changed"
	badSeverity := ok
	badSeverity.Severity = "loud"
	noSummary := ok
	noSummary.Summary = ""
	for name, d := range map[string]*mq.Delivery{
		"another routing key": {RoutingKey: "user.logged_in", Body: body(ok)},
		"not JSON":            {RoutingKey: security.RouteEvent, Body: []byte("not json")},
		"an in-tx event":      {RoutingKey: security.RouteEvent, Body: body(inTx)},
		"a bad severity":      {RoutingKey: security.RouteEvent, Body: body(badSeverity)},
		"no summary":          {RoutingKey: security.RouteEvent, Body: body(noSummary)},
		"no time":             {RoutingKey: security.RouteEvent, Body: []byte(`{"eventType":"login_failed","severity":"info","summary":"x"}`)},
	} {
		if err := a.Handle(context.Background(), d); !mq.IsPermanent(err) {
			t.Errorf("%s: %v, want a permanent failure", name, err)
		}
	}
	// A valid event reaches the database step (no pool here: a transient error the ladder retries).
	d := &mq.Delivery{Queue: security.QueueAudit, MessageID: "1", RoutingKey: security.RouteEvent, Body: body(ok)}
	if err := a.Handle(context.Background(), d); err == nil || mq.IsPermanent(err) {
		t.Fatalf("a valid event without a pool: %v, want a transient error", err)
	}
	if r := a.Registration(); r.Queue != security.QueueAudit || r.Handler == nil {
		t.Fatalf("registration %+v", r)
	}
}

// The consumer-written types are exactly those of Appendix C §C.4.13 and all belong to the catalogue; Emit refuses
// any other (they are written with Append in the transaction of their change).
func TestConsumerTypes(t *testing.T) {
	for _, typ := range security.ConsumerTypes {
		if !security.KnownType(typ) {
			t.Errorf("%s is not in EventTypes", typ)
		}
	}
	for _, typ := range []string{"login_failed", "login_lockout", "password_reset_requested", "tenant_orphans_detected"} {
		if !slices.Contains(security.ConsumerTypes, typ) {
			t.Errorf("%s is consumer-written (Appendix C §C.4.13)", typ)
		}
	}
	err := security.Emit(context.Background(), nil, security.Event{EventType: "task_tenant_reassigned",
		Severity: security.SeverityWarning, Summary: "x", OccurredAt: time.Now()}, "task", "1")
	if err == nil {
		t.Fatal("Emit queued an in-transaction event type")
	}
}
