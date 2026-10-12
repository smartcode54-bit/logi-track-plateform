package logx_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/logx"
)

func TestRedactsSensitiveKeysAtAnyDepth(t *testing.T) {
	var buf bytes.Buffer
	l, err := logx.New(logx.Options{Level: "info", Format: "json", Service: "test", Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	l.Info().
		Str("Authorization", "Bearer abc.def.ghi").
		Str("password", "hunter2").
		Str("refreshToken", "rt-123").
		Str("token", "tok-1").
		Str("idCard", "1234567890123").
		Str("x-api-key", "k-1").
		Interface("body", map[string]any{"user": map[string]any{"accessToken": "at-9", "email": "a@b.c"}}).
		Str("user", "kept").
		Msg("login")

	line := buf.String()
	for _, secret := range []string{"abc.def.ghi", "hunter2", "rt-123", "tok-1", "1234567890123", "k-1", "at-9"} {
		if strings.Contains(line, secret) {
			t.Fatalf("secret %q leaked: %s", secret, line)
		}
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		t.Fatalf("line is not JSON: %v: %s", err, line)
	}
	if obj["user"] != "kept" || obj["message"] != "login" {
		t.Fatalf("non-sensitive fields lost: %v", obj)
	}
	if obj["password"] != logx.Redacted {
		t.Fatalf("password not replaced: %v", obj["password"])
	}
	inner := obj["body"].(map[string]any)["user"].(map[string]any)
	if inner["accessToken"] != logx.Redacted || inner["email"] != "a@b.c" {
		t.Fatalf("nested redaction wrong: %v", inner)
	}
}

func TestPassThroughKeepsLineUnchanged(t *testing.T) {
	var buf bytes.Buffer
	l, err := logx.New(logx.Options{Level: "info", Format: "json", Service: "test", Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	l.Info().Str("route", "/v1/hubs").Int("status", 200).Msg("request")
	line := buf.String()
	// Fast path: zerolog field order is preserved when nothing is redacted.
	if !strings.HasPrefix(line, `{"level":"info","service":"test"`) {
		t.Fatalf("unexpected line: %s", line)
	}
}

func TestConsoleFormatIsRedactedToo(t *testing.T) {
	var buf bytes.Buffer
	l, err := logx.New(logx.Options{Level: "debug", Format: "console", Service: "test", Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	l.Debug().Str("password", "hunter2").Msg("x")
	if strings.Contains(buf.String(), "hunter2") {
		t.Fatalf("console output leaked: %s", buf.String())
	}
}

func TestRejectsUnknownLevelAndFormat(t *testing.T) {
	if _, err := logx.New(logx.Options{Level: "loud", Format: "json"}); err == nil {
		t.Fatal("want error for unknown level")
	}
	if _, err := logx.New(logx.Options{Level: "info", Format: "xml"}); err == nil {
		t.Fatal("want error for unknown format")
	}
}

func TestRedactsSeparatorVariantsOutsideTheAPIProcess(t *testing.T) {
	var buf bytes.Buffer
	l, err := logx.New(logx.Options{Level: "info", Format: "json", Service: "worker", Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	l.Info().Str("truck_license_id", "TL-111").Str("Truck-License-Id", "TL-222").
		Str("pass_word", "pw-333").Str("ID-CARD", "ic-444").Str("Api_Key", "ak-555").Msg("job")
	for _, v := range []string{"TL-111", "TL-222", "pw-333", "ic-444", "ak-555"} {
		if strings.Contains(buf.String(), v) {
			t.Fatalf("%s leaked: %s", v, buf.String())
		}
	}
}

// A process (and every test harness) builds more than one logger: the api
// and its background loops, two servers in one test. New must not write the
// zerolog package state a running logger reads on every timestamped line, or
// the race detector fails whichever test overlaps a New with a log line
// (CIFIX1: TestXForwardedForOnlyFromTrustedProxies starts two servers).
func TestNewDoesNotRaceWithARunningLogger(t *testing.T) {
	first, err := logx.New(logx.Options{Level: "info", Format: "json", Service: "api", Out: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	started, stop, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		first.Info().Msg("serving")
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
				first.Info().Msg("request")
			}
		}
	}()
	<-started
	for range 50 {
		if _, err := logx.New(logx.Options{Level: "info", Format: "json", Service: "worker", Out: io.Discard}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	<-done
}

// The RFC 3339 nanosecond layout is still the process-wide timestamp format,
// set once when the package loads rather than by each New.
func TestTimestampsUseRFC3339Nano(t *testing.T) {
	if zerolog.TimeFieldFormat != time.RFC3339Nano {
		t.Fatalf("zerolog.TimeFieldFormat = %q before any New, want RFC3339Nano", zerolog.TimeFieldFormat)
	}
	var buf bytes.Buffer
	l, err := logx.New(logx.Options{Level: "info", Format: "json", Service: "test", Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	l.Info().Msg("tick")
	var obj map[string]any
	if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
		t.Fatalf("line is not JSON: %v: %s", err, buf.String())
	}
	ts, _ := obj[zerolog.TimestampFieldName].(string)
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Fatalf("time %q is not RFC 3339: %v", ts, err)
	}
}
