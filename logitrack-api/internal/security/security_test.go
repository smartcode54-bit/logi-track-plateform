package security_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

func TestValidate(t *testing.T) {
	ok := security.Event{EventType: "password_changed", Severity: security.SeverityInfo, Summary: "s", OccurredAt: time.Now()}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*security.Event){
		"type not snake_case": func(e *security.Event) { e.EventType = "PasswordChanged" },
		"empty type":          func(e *security.Event) { e.EventType = "" },
		"unknown severity":    func(e *security.Event) { e.Severity = "error" },
		"no summary":          func(e *security.Event) { e.Summary = "" },
		"no time":             func(e *security.Event) { e.OccurredAt = time.Time{} },
	} {
		e := ok
		mutate(&e)
		if err := e.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// The JSON form is the outbox security.event payload of Appendix B §B.5.7: camelCase, optional ids omitted.
func TestEventJSON(t *testing.T) {
	uid := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	b, err := json.Marshal(security.Event{EventType: "login_failed", Severity: "info", Summary: "s",
		Details: map[string]any{"ip": "203.0.113.9"}, TargetUserID: &uid, OccurredAt: time.Unix(0, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, k := range []string{`"eventType":"login_failed"`, `"targetUserId":"` + uid.String() + `"`, `"occurredAt":"1970-01-01T00:00:00Z"`} {
		if !strings.Contains(got, k) {
			t.Errorf("%s lacks %s", got, k)
		}
	}
	for _, k := range []string{"actorUserId", "actorEmail", "tenantId", "requestId"} {
		if strings.Contains(got, k) {
			t.Errorf("%s carries the empty %s", got, k)
		}
	}
}
