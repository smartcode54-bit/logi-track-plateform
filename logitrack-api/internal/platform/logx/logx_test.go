package logx_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

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
