package iam

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// POST /v1/subcontractors keeps the rule of the legacy subcontractorSchema (T20): an individual needs a valid ID
// card number, a company a valid tax id; POST /v1/tenants only checks the checksum of what it is given.
func TestValidateCarrierIdentity(t *testing.T) {
	base := CreateTenantInput{Kind: "carrier", NameTh: "รถร่วม", LegalType: "individual"}
	reasons := func(in CreateTenantInput, strict bool) map[string]string {
		_, bad := validateCarrier(in, strict)
		out := map[string]string{}
		for _, f := range bad {
			out[f.Field] = f.Reason
		}
		return out
	}
	if got := reasons(base, true); got["idCardNumber"] != "required" || len(got) != 1 {
		t.Errorf("individual without an ID card: %v", got)
	}
	if got := reasons(base, false); len(got) != 0 {
		t.Errorf("POST /v1/tenants keeps the ID optional: %v", got)
	}
	in := base
	in.IDCardNumber = "3100100123452" // one digit off
	if got := reasons(in, true); got["idCardNumber"] != "invalid" {
		t.Errorf("bad ID checksum: %v", got)
	}
	in.IDCardNumber = "3100100123451"
	if got := reasons(in, true); len(got) != 0 {
		t.Errorf("valid individual: %v", got)
	}
	co := base
	co.LegalType = "company"
	if got := reasons(co, true); got["taxId"] != "required" {
		t.Errorf("company without a tax id: %v", got)
	}
	co.TaxID = "0105536000314"
	if got := reasons(co, true); got["taxId"] != "invalid" {
		t.Errorf("bad tax id: %v", got)
	}
	co.TaxID, co.Code = "0105536000313", "bad code!"
	if got := reasons(co, true); got["code"] != "invalid" || len(got) != 1 {
		t.Errorf("code: %v", got)
	}
}

func TestDocumentKind(t *testing.T) {
	id := uuid.NewString()
	for key, want := range map[string]string{
		"subcontractors/" + id + "/id_cards/1_card.pdf":         "id_card",
		"subcontractors/" + id + "/company_docs/1_id_cards.pdf": "company_doc",
		"subcontractors/" + id + "/other/1_x.pdf":               "other",
		"subcontractors/legacy.pdf":                             "other",
		"customers/" + id + "/logo-1.png":                       "other",
	} {
		if got := documentKind(key); got != want {
			t.Errorf("%s: %s, want %s", key, got, want)
		}
	}
}

func TestCheckDocumentKeys(t *testing.T) {
	if bad := checkDocumentKeys([]string{"a", "b"}); len(bad) != 0 {
		t.Errorf("two keys: %v", bad)
	}
	bad := checkDocumentKeys([]string{"a", "", "a", strings.Repeat("k", 1025)})
	want := map[string]string{"documentKeys[1]": "invalid", "documentKeys[2]": "duplicate", "documentKeys[3]": "invalid"}
	if len(bad) != len(want) {
		t.Fatalf("violations = %v", bad)
	}
	for _, f := range bad {
		if want[f.Field] != f.Reason {
			t.Errorf("%s: %s", f.Field, f.Reason)
		}
	}
	many := make([]string, MaxTenantDocuments+1)
	for i := range many {
		many[i] = uuid.NewString()
	}
	if bad := checkDocumentKeys(many); len(bad) != 1 || bad[0].Reason != "too_many" {
		t.Errorf("too many: %v", bad)
	}
}

func TestNameCursor(t *testing.T) {
	id := uuid.New()
	c, ok := decodeNameCursor(encodeNameCursor("บริษัท ก", id))
	if !ok || c.S != "บริษัท ก" || c.ID != id {
		t.Fatalf("round trip: %+v %v", c, ok)
	}
	for _, bad := range []string{"", "%%", encodeNameCursor("x", uuid.Nil)} {
		if _, ok := decodeNameCursor(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
