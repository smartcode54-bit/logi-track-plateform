package notify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

func TestSupportAdminMailContainsOnlyActionReferences(t *testing.T) {
	req := SupportAdminAction{TenantID: uuid.New(), ActorUserID: uuid.New(), TargetUserID: uuid.New(), Action: "temporary_password", Basis: "consent"}
	for _, locale := range []string{LocaleEN, LocaleTH} {
		msg, err := supportActionMail("admin@example.test", locale, "Recognizable <Company>", req)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(msg.Subject, "Recognizable <Company>") || !strings.Contains(msg.Text, req.TenantID.String()) || !strings.Contains(msg.HTML, "&lt;Company&gt;") {
			t.Fatalf("missing safe references: %+v", msg)
		}
		if strings.Contains(msg.Text, "token=") || strings.Contains(msg.Text, "reset-password#") || strings.Contains(msg.HTML, "<pre>") {
			t.Fatal("mail contains credential link or unformatted copy")
		}
	}
}
func TestSupportAdminNotificationRequiresExactTenant(t *testing.T) {
	tenant := uuid.New()
	req := SupportAdminAction{TenantID: tenant, ActorUserID: uuid.New(), TargetUserID: uuid.New()}
	body, _ := json.Marshal(req)
	e := &Email{Enabled: false}
	wrong := uuid.New()
	if err := e.supportAdminAction(context.Background(), &mq.Delivery{Body: body, TenantID: &wrong}); !mq.IsPermanent(err) {
		t.Fatalf("foreign tenant accepted: %v", err)
	}
	if err := e.supportAdminAction(context.Background(), &mq.Delivery{Body: body, TenantID: &tenant}); err != nil {
		t.Fatalf("valid disabled-mail event: %v", err)
	}
}

func TestSupportAdminMailNeverDisplaysInternalEnums(t *testing.T) {
	for _, locale := range []string{LocaleEN, LocaleTH} {
		for _, action := range []string{"membership_set", "membership_add", "membership_remove", "user_create", "user_delete", "profile", "invite", "temporary_password", "disable", "enable", "driver_link", "driver_unlink", "platform_role_grant", "platform_role_remove", "scopes_set", "scopes_remove"} {
			for basis := range supportActionCopies[locale].Bases {
				req := SupportAdminAction{TenantID: uuid.New(), Action: action, Basis: basis}
				msg, err := supportActionMail("admin@example.test", locale, "Example Co.", req)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(msg.Text, "Action: "+action) || strings.Contains(msg.Text, "Authorization: "+basis) || strings.Contains(msg.Text, "การดำเนินการ: "+action) || strings.Contains(msg.Text, "เงื่อนไขการอนุญาต: "+basis) {
					t.Errorf("internal enum shown: %s/%s", action, basis)
				}
			}
		}
	}
	for _, req := range []SupportAdminAction{{Action: "invented", Basis: "consent"}, {Action: "invite", Basis: "invented"}} {
		if _, err := supportActionMail("a@example.test", "en", "Company", req); err == nil {
			t.Fatal("untranslated enum accepted")
		}
	}
}
