package iam

import (
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// A key name is a one-line label: control characters, NUL among them, are 422 name invalid. A NUL used to
// pass validation and fail the insert (SQLSTATE 22021) as a 500.
func TestValidateCreateName(t *testing.T) {
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	for name, want := range map[string]string{
		"a\x00b": "invalid", "a\x01b": "invalid", "a\nb": "invalid", "a\tb": "invalid", "a\u0085b": "invalid",
		"  ": "required", "": "required", "ok name": "", "  ชื่อกุญแจ  ": "",
	} {
		r, bad := validateCreate(CreateAPIKeyInput{Name: name, Scope: "script", Capabilities: []string{"waitlist:view"}}, now)
		var got []string
		for _, v := range bad {
			if v.Field == "name" {
				got = append(got, v.Reason)
			}
		}
		switch {
		case want == "" && len(bad) != 0:
			t.Errorf("name %q refused: %+v", name, bad)
		case want != "" && !slices.Equal(got, []string{want}):
			t.Errorf("name %q: want name %s, got %+v", name, want, bad)
		case want == "" && r.name == "":
			t.Errorf("name %q lost", name)
		}
	}
}

// A NUL byte that still reaches a text column is a 422, not a 500 (defence in depth for every iam writer).
func TestMapDBErrorNUL(t *testing.T) {
	err := mapDBError(&pgconn.PgError{Code: "22021", Message: "invalid byte sequence for encoding \"UTF8\": 0x00"})
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != http.StatusUnprocessableEntity || he.Code != httpx.CodeInvalidArgument {
		t.Fatalf("22021 mapped to %v", err)
	}
}
