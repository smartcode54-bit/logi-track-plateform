package storage

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
)

func TestValidateKeyRefusesTraversalAndOddShapes(t *testing.T) {
	for _, k := range []string{
		"", "/etc/passwd", "../x", "a/../b", "a/./b", "./a", "a/..", "a//b", "a/", "trips/x/", `a\b`, "a\x00b",
		"a\nb", "a\x7fb", string([]byte{0xff, 0xfe}), strings.Repeat("a", MaxKeyBytes+1),
	} {
		if ValidateKey(k) == nil {
			t.Errorf("key %q accepted", k)
		}
	}
	for _, k := range []string{
		"trips/0192b3c4-0000-7000-8000-000000000001/seal-1760000000000.jpg", "app_releases/prod/logitrack-prod-v3.5.0.apk",
		// Legacy Firebase keys keep their spelling (copied 1:1 by the ETL, main spec §9.2).
		"trucks/documents/maintenance/T1/1700000000_ใบเสร็จ (1).pdf", "trip_records/TR-001/stop_1_arrived.jpg", "a..b/c.d",
		// A legacy key whose Thai file name makes one segment 266 bytes (S3 limits only the whole key).
		"drivers/documents/1690000000000_id_card_" + strings.Repeat("ก", 74) + ".jpg",
	} {
		if err := ValidateKey(k); err != nil {
			t.Errorf("key %q refused: %v", k, err)
		}
	}
}

// Read rules of GET /v1/files (main spec §9.2, §9.5; Appendix C §C.2 row 15, §C.9 row 11): driver ID cards and
// licences need drivers:view_pii and live 5 minutes; HR, penalty and statement files need their capability and
// live 15 minutes; reports and a tenant's ID cards have no staff rule; everything else keeps p_read.
func TestPurposeReadRules(t *testing.T) {
	for name, want := range map[string]ReadRule{
		"driver_id_card":     {Capability: "drivers:view_pii", MaxTTL: 5 * time.Minute},
		"driver_license":     {Capability: "drivers:view_pii", MaxTTL: 5 * time.Minute},
		"leave_evidence":     {Capability: "hr:view_leave", MaxTTL: 15 * time.Minute},
		"penalty_evidence":   {Capability: "hr:view_payroll", MaxTTL: 15 * time.Minute},
		"statement_document": {Capability: "accounting:billing_result", MaxTTL: 15 * time.Minute},
		"report":             {NoStaffRead: true, MaxTTL: 15 * time.Minute},
		"driver_profile":     {},
		"trip_photo":         {},
		"tenant_document":    {},
	} {
		if got := Purposes[name].ReadRuleFor("x/y.jpg"); got != want {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
	id := uuid.New().String()
	if got := Purposes["tenant_document"].ReadRuleFor("subcontractors/" + id + "/id_cards/1_card.pdf"); !got.NoStaffRead || got.MaxTTL != 5*time.Minute {
		t.Errorf("a tenant ID card: %+v", got)
	}
	if got := Purposes["tenant_document"].ReadRuleFor("subcontractors/" + id + "/company_docs/1_id_cards.pdf"); got.NoStaffRead {
		t.Errorf("a company document: %+v", got)
	}
	for name, p := range Purposes {
		if p.ReadCapability != "" && !authz.Known(p.ReadCapability) {
			t.Errorf("%s: %s is not a catalog capability", name, p.ReadCapability)
		}
	}
}

func TestPurposeKeysFollowTheLayout(t *testing.T) {
	id := uuid.MustParse("0192b3c4-0000-7000-8000-000000000001")
	now := time.UnixMilli(1760000000123)
	cases := map[string]struct {
		in   KeyInput
		want string
	}{
		"trip_photo":             {KeyInput{Variant: "stop_2_arrived", ContentType: "image/jpeg"}, "trips/{id}/stop_2_arrived-1760000000123.jpg"},
		"checkin_photo":          {KeyInput{ContentType: "image/jpeg"}, "checkin/{id}/1760000000123.jpg"},
		"checkin_app_screenshot": {KeyInput{ContentType: "image/png"}, "checkin/{id}/app_screenshot_1760000000123.png"},
		"standby_photo":          {KeyInput{Variant: "site_photo", ContentType: "image/jpeg"}, "standby/{id}/site_photo-1760000000123.jpg"},
		"incident_photo":         {KeyInput{Variant: "situation2", ContentType: "image/webp"}, "incidents/{id}/situation2-1760000000123.webp"},
		"chat_image":             {KeyInput{ContentType: "image/jpeg"}, "chats/{id}/1760000000123.jpg"},
		"leave_evidence":         {KeyInput{ContentType: "application/pdf"}, "leave/{id}/1760000000123_0.pdf"},
		"maintenance_file":       {KeyInput{Variant: "invoice", ContentType: "application/pdf"}, "maintenance/{id}/invoice_1760000000123.pdf"},
		"expense_receipt":        {KeyInput{ContentType: "image/jpeg"}, "expenses/{id}/receipt-1760000000123.jpg"},
		"expense_odometer":       {KeyInput{ContentType: "image/jpeg"}, "expenses/{id}/odometer-1760000000123.jpg"},
		"driver_profile":         {KeyInput{ContentType: "image/jpeg"}, "drivers/{id}/profile-1760000000123.jpg"},
		"driver_id_card":         {KeyInput{ContentType: "image/jpeg"}, "drivers/{id}/id_card-1760000000123.jpg"},
		"driver_license":         {KeyInput{ContentType: "application/pdf"}, "drivers/{id}/license-1760000000123.pdf"},
		"truck_photo":            {KeyInput{FileName: "หน้ารถ front.JPG", ContentType: "image/jpeg"}, "trucks/{id}/photos/1760000000123_front.jpg"},
		"truck_document":         {KeyInput{FileName: "../../etc/passwd", ContentType: "application/pdf"}, "trucks/{id}/documents/1760000000123_passwd.pdf"},
		"truck_receipt":          {KeyInput{ContentType: "image/png"}, "trucks/{id}/receipts/1760000000123_file.png"},
		"insurance_document":     {KeyInput{FileName: "policy 2026.pdf", ContentType: "application/pdf"}, "trucks/{id}/insurance/1760000000123_policy_2026.pdf"},
		"tenant_document":        {KeyInput{Variant: "company_doc", FileName: "หนังสือรับรอง.pdf", ContentType: "application/pdf"}, "subcontractors/{id}/company_docs/1760000000123_file.pdf"},
		"customer_logo":          {KeyInput{ContentType: "image/png"}, "customers/{id}/logo-1760000000123.png"},
		"company_logo":           {KeyInput{ContentType: "image/png"}, "companies/{id}/logo-1760000000123.png"},
		"company_stamp":          {KeyInput{ContentType: "image/png"}, "companies/{id}/stamp-1760000000123.png"},
		"company_signature":      {KeyInput{ContentType: "image/png"}, "companies/{id}/signature-1760000000123.png"},
		"user_photo":             {KeyInput{ContentType: "image/jpeg"}, "users/{id}/photo-1760000000123.jpg"},
		"penalty_evidence":       {KeyInput{ContentType: "image/jpeg"}, "penalties/{id}/evidence-1760000000123.jpg"},
	}
	for name, tc := range cases {
		p, ok := Purposes[name]
		if !ok || !p.Presign {
			t.Fatalf("%s: not a client purpose", name)
		}
		tc.in.EntityID, tc.in.Now = id, now
		got, err := p.Key(tc.in)
		if want := strings.ReplaceAll(tc.want, "{id}", id.String()); err != nil || got != want {
			t.Errorf("%s: key %q (%v), want %q", name, got, err, want)
		}
		if p.OwnerKind == "" {
			t.Errorf("%s: no owner kind", name)
		}
	}
	// Every presignable purpose has a test case; the server-only ones are never presigned.
	for name, p := range Purposes {
		if _, tested := cases[name]; p.Presign && !tested {
			t.Errorf("%s: no key test", name)
		}
	}
	for _, name := range []string{"statement_document", "report", "apk"} {
		if Purposes[name].Presign {
			t.Errorf("%s is presignable", name)
		}
	}
}

// The vocabulary is the one of Appendix A §A.2.1 (format CHECK ^[a-z][a-z0-9_]*$).
func TestPurposeVocabularyMatchesAppendixA(t *testing.T) {
	want := []string{"trip_photo", "checkin_photo", "checkin_app_screenshot", "standby_photo", "incident_photo", "chat_image",
		"leave_evidence", "maintenance_file", "expense_receipt", "expense_odometer", "company_logo", "company_stamp",
		"company_signature", "customer_logo", "driver_profile", "driver_id_card", "driver_license", "truck_photo",
		"truck_document", "truck_receipt", "insurance_document", "tenant_document", "user_photo", "penalty_evidence",
		"statement_document", "report", "apk"}
	if len(Purposes) != len(want) {
		t.Fatalf("%d purposes, Appendix A lists %d", len(Purposes), len(want))
	}
	rx := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	for _, n := range want {
		if _, ok := Purposes[n]; !ok || !rx.MatchString(n) {
			t.Errorf("purpose %s missing", n)
		}
	}
}

func TestVariantsAndContentTypes(t *testing.T) {
	trip, standby, leave, chat := Purposes["trip_photo"], Purposes["standby_photo"], Purposes["leave_evidence"], Purposes["chat_image"]
	for v, ok := range map[string]bool{"seal": true, "stop_12_runsheet_received": true, "": false, "Seal": false, "a/b": false, "../x": false} {
		if trip.ValidVariant(v) != ok {
			t.Errorf("trip variant %q: %v", v, !ok)
		}
	}
	if !standby.ValidVariant("customer_worksheet") || standby.ValidVariant("map") || standby.ValidVariant("") {
		t.Error("standby variants")
	}
	if !leave.ValidVariant("") || !leave.ValidVariant("3") || leave.ValidVariant("x") {
		t.Error("leave index")
	}
	if !chat.ValidVariant("") || chat.ValidVariant("x") {
		t.Error("chat takes no variant")
	}
	if !chat.AcceptsType("image/jpeg") || chat.AcceptsType("application/pdf") || chat.AcceptsType("text/html") ||
		!leave.AcceptsType("application/pdf") || leave.AcceptsType("image/svg+xml") {
		t.Error("content types")
	}
	if normalizeType("IMAGE/JPEG; charset=binary") != "image/jpeg" || normalizeType("not a type") != "" {
		t.Error("normalizeType")
	}
}
