package forms

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestSupportCannotReadLeadRowsOrDeleteWaitlist(t *testing.T) {
	s := &Service{}
	p := &authz.Principal{TenantRole: authz.SupportReadOnly, Caps: authz.NewCapSet(authz.WaitlistView)}
	_, _, _, err := s.ListWaitlist(context.Background(), p, 50, "")
	e, ok := err.(*httpx.Error)
	if !ok || e.Code != httpx.CodeSupportViewUnavailable {
		t.Fatalf("raw lead read: %v", err)
	}
	err = s.DeleteWaitlist(context.Background(), p, uuid.New())
	e, ok = err.(*httpx.Error)
	if !ok || e.Code != httpx.CodeSupportReadOnly {
		t.Fatalf("waitlist delete: %v", err)
	}
}
func TestSupportCountsDTOIsCompletelyClassified(t *testing.T) {
	if _, err := httpx.SchemaContracts(); err != nil {
		t.Fatal(err)
	}
}
