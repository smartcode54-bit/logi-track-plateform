package mobileapp

import (
	"encoding/base64"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestPlatformInstallCursorCannotCarryDeviceIdentifier(t *testing.T) {
	device := "SECRET-install-device-id"
	legacy := installCursor{T: time.Now().UTC(), D: uuid.New(), I: device}.encode()
	if _, ok := decodePlatformInstallCursor(legacy); ok {
		t.Fatal("platform accepted legacy device cursor")
	}
	in := platformInstallCursor{T: time.Now().UTC(), D: uuid.New(), ID: uuid.New()}
	encoded := in.encode()
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), device) || strings.Contains(string(raw), `"i"`) {
		t.Fatalf("device identifier in cursor: %s", raw)
	}
	out, ok := decodePlatformInstallCursor(encoded)
	if !ok || out != in {
		t.Fatalf("cursor roundtrip: %+v %v", out, ok)
	}
}
