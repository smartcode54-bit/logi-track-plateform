package parties

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// fields returns the field -> reason pairs of a 422 invalid_argument ("" when err is something else).
func fields(t *testing.T, err error) map[string]string {
	t.Helper()
	e := httpx.AsError(err)
	if err == nil || e == nil || e.Status != http.StatusUnprocessableEntity || e.Code != httpx.CodeInvalidArgument {
		t.Fatalf("want 422 invalid_argument, got %v", err)
	}
	out := map[string]string{}
	for _, f := range e.Details["fields"].([]httpx.FieldViolation) {
		out[f.Field] = f.Reason
	}
	return out
}

func decode[T any](t *testing.T, raw string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// The Thai checksum rule of T20's first acceptance criterion: 0105536000313 is a valid company tax id (the 13th
// digit is (11 - Σ d_i × (13 - i) mod 11) mod 10), one changed digit breaks it.
func TestCustomerTaxIDChecksum(t *testing.T) {
	in := decode[CustomerInput](t, `{"code":"spx","name":"Shopee","taxId":"0105536000313"}`)
	if err := in.validate(true); err != nil {
		t.Fatalf("valid tax id refused: %v", err)
	}
	if in.Code.V != "SPX" {
		t.Errorf("code = %q, want upper case", in.Code.V)
	}
	in = decode[CustomerInput](t, `{"code":"SPX","name":"Shopee","taxId":"0105536000314"}`)
	if got := fields(t, in.validate(true)); got["taxId"] != "invalid" || len(got) != 1 {
		t.Errorf("fields = %v", got)
	}
	in = decode[CustomerInput](t, `{"code":"SPX","name":"Shopee","taxId":"12345"}`)
	if got := fields(t, in.validate(true)); got["taxId"] != "invalid" {
		t.Errorf("short tax id: %v", got)
	}
}

func TestCustomerValidation(t *testing.T) {
	in := decode[CustomerInput](t, `{"code":"bad code","name":"","branchType":"สาขา","branchNumber":"12",
		"billingEmail":"Billing <b@x.test>","paymentTermsDays":-1,"lineGroupId":"G1","billingDateBasis":"shipped",
		"driverIdTypes":[{"key":"appId","label":"App"},{"key":"appId","label":"Again"},{"key":"9x","label":""}],
		"logoKey":""}`)
	got := fields(t, in.validate(true))
	want := map[string]string{"code": "invalid", "name": "required", "branchType": "invalid", "branchNumber": "invalid",
		"billingEmail": "invalid", "paymentTermsDays": "out_of_range", "lineGroupId": "invalid",
		"billingDateBasis": "invalid", "driverIdTypes[1].key": "duplicate", "driverIdTypes[2].key": "invalid",
		"driverIdTypes[2].label": "required", "logoKey": "invalid"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	// PATCH: only the fields present are checked; null on a required field is refused.
	in = decode[CustomerInput](t, `{"description":"x"}`)
	if err := in.validate(false); err != nil {
		t.Errorf("patch of one optional field: %v", err)
	}
	in = decode[CustomerInput](t, `{"name":null,"billingDateBasis":null}`)
	if got := fields(t, in.validate(false)); got["name"] != "required" || got["billingDateBasis"] != "invalid" {
		t.Errorf("nulls: %v", got)
	}
	if err := branchRule(true, ptr("branch"), nil); err == nil {
		t.Error("a branch without a number passed")
	}
	if err := branchRule(false, ptr("branch"), nil); err != nil {
		t.Error("an untouched legacy branch without a number was refused")
	}
}

func TestCompanyValidation(t *testing.T) {
	ok := `{"nameTh":"บริษัท ทดสอบ","taxId":"0105536000313","branchType":"headquarters","address":"กรุงเทพ",
		"withholdingTaxRate":3,"companyType":"subcontractor"}`
	in := decode[CompanyInput](t, ok)
	if err := in.validate(true); err != nil {
		t.Fatalf("valid company: %v", err)
	}
	in = decode[CompanyInput](t, `{}`)
	got := fields(t, in.validate(true))
	for _, f := range []string{"nameTh", "taxId", "branchType", "address", "withholdingTaxRate", "companyType"} {
		if got[f] != "required" {
			t.Errorf("%s = %q, want required (no default for withholdingTaxRate, main spec §19.2 Q6)", f, got[f])
		}
	}
	in = decode[CompanyInput](t, `{"nameTh":"x","taxId":"0105536000314","branchType":"hq","address":"a",
		"withholdingTaxRate":3.333,"companyType":"partner","maxTrucks":0,"email":"nope","stampKey":""}`)
	got = fields(t, in.validate(true))
	want := map[string]string{"taxId": "invalid", "branchType": "invalid", "withholdingTaxRate": "invalid",
		"companyType": "invalid", "maxTrucks": "out_of_range", "email": "invalid", "stampKey": "invalid"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	for _, r := range []float64{0, 1, 3, 3.25, 99.99, 100} {
		if !validRate(r) {
			t.Errorf("rate %v refused", r)
		}
	}
	for _, r := range []float64{-0.01, 100.01, 3.001, 1.005} {
		if validRate(r) {
			t.Errorf("rate %v accepted", r)
		}
	}
	if err := ownerOnlyInOwnFleet(CompanyOwner, "carrier"); err == nil {
		t.Error("a carrier may not hold the owner company")
	}
	if err := ownerOnlyInOwnFleet(CompanyOwner, "own_fleet"); err != nil {
		t.Error(err)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	code, id := "CJSF", uuid.New()
	c, ok := decodeCursor(cursor{S: &code, ID: id}.encode())
	if !ok || *c.S != code || c.ID != id || c.T != nil {
		t.Fatalf("text cursor: %+v %v", c, ok)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	c, ok = decodeCursor(cursor{T: &now, ID: id}.encode())
	if !ok || !c.T.Equal(now) || c.S != nil {
		t.Fatalf("time cursor: %+v %v", c, ok)
	}
	for _, bad := range []string{"", "!!", cursor{ID: id}.encode(), cursor{S: &code}.encode(), cursor{S: &code, T: &now, ID: id}.encode()} {
		if _, ok := decodeCursor(bad); ok {
			t.Errorf("cursor %q accepted", bad)
		}
	}
}

func TestMapDBError(t *testing.T) {
	cases := []struct {
		err          error
		status       int
		code, reason string
	}{
		{&pgconn.PgError{Code: "23505", ConstraintName: "customers_code"}, http.StatusConflict, CodeAlreadyExists, "taken"},
		{&pgconn.PgError{Code: "23505", ConstraintName: "companies_owner_one"}, http.StatusConflict, CodeAlreadyExists, "owner_exists"},
		{&pgconn.PgError{Code: "42501"}, http.StatusForbidden, "permission_denied", ""},
		{&pgconn.PgError{Code: "23514", ConstraintName: "x_check"}, http.StatusUnprocessableEntity, httpx.CodeInvalidArgument, ""},
	}
	for _, c := range cases {
		e := httpx.AsError(mapDBError(c.err))
		if e == nil || e.Status != c.status || e.Code != c.code {
			t.Errorf("%v -> %+v", c.err, e)
			continue
		}
		if c.reason != "" && e.Details["reason"] != c.reason {
			t.Errorf("%v reason = %v", c.err, e.Details["reason"])
		}
	}
	plain := errors.New("x")
	if !errors.Is(mapDBError(plain), plain) {
		t.Error("a non-database error must pass through")
	}
}

func TestEveryRouteIsGuarded(t *testing.T) {
	// The write routes carry their capability guard in the registry; GET /v1/customers checks inside (minimal).
	gs := Groups(nil, func(c fiber.Ctx) error { return c.Next() })
	prefixes := []string{}
	for _, g := range gs {
		prefixes = append(prefixes, g.Prefix)
		if g.Public {
			t.Errorf("%s is public; Appendix B §B.2.10 routes are internal only", g.Prefix)
		}
	}
	if !slices.Equal(prefixes, []string{CustomersPrefix, CompaniesPrefix}) {
		t.Errorf("prefixes = %v", prefixes)
	}
}

func ptr(s string) *string { return &s }
