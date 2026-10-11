package drivers

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/drivers/driversdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

var today = time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)

func decode(t *testing.T, body string) DriverInput {
	t.Helper()
	var in DriverInput
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func reasons(ck checker) map[string]string {
	out := map[string]string{}
	for _, v := range ck.bad {
		out[v.Field] = v.Reason
	}
	return out
}

func TestMergeCreateRequiresTheFormFields(t *testing.T) {
	var ck checker
	in := decode(t, `{}`)
	m := driversdb.UpdateDriverParams{Status: StatusActive}
	merge(&ck, &in, &m, true, today)
	got := reasons(ck)
	for _, f := range []string{"firstName", "lastName", "fullNameTh", "mobile"} {
		if got[f] != "required" {
			t.Errorf("%s: %q", f, got[f])
		}
	}
	if len(got) != 4 {
		t.Errorf("only the four required fields: %v", got)
	}
}

func TestMergeFormRules(t *testing.T) {
	cases := []struct {
		body, field, reason string
	}{
		{`{"idCard":"1101700230709"}`, "idCard", "invalid"},
		{`{"idCard":"110170023070"}`, "idCard", "invalid"},
		{`{"truckLicenseId":"1234567"}`, "truckLicenseId", "invalid_length"},
		{`{"birthDate":"2007-10-12"}`, "birthDate", "out_of_range"}, // 18
		{`{"birthDate":"1970-10-11"}`, "birthDate", "out_of_range"}, // 56 today
		{`{"birthDate":"11/10/1990"}`, "birthDate", "invalid"},
		{`{"email":"no-at-sign"}`, "email", "invalid"},
		{`{"licenseType":"B2"}`, "licenseType", "invalid"},
		{`{"employmentType":"contract"}`, "employmentType", "invalid"},
		{`{"employmentType":null}`, "employmentType", "required"},
		{`{"status":"on-duty"}`, "status", "invalid"},
		{`{"status":null}`, "status", "required"},
		{`{"contractYears":101}`, "contractYears", "out_of_range"},
		{`{"contractYears":1.005}`, "contractYears", "too_precise"},
		{`{"employmentStart":"2025-01-02","employmentEnd":"2025-01-01"}`, "employmentEnd", "before_start"},
		{`{"firstName":"a\u0000b"}`, "firstName", "invalid"},
		{`{"mobile":"` + strings.Repeat("1", 31) + `"}`, "mobile", "too_long"},
		{`{"fullNameTh":"  "}`, "fullNameTh", "required"},
		{`{"fullNameTh":null}`, "fullNameTh", "required"},
		{`{"statusNote":"` + strings.Repeat("x", 501) + `"}`, "statusNote", "too_long"},
		{`{"profileKey":"` + strings.Repeat("k", 1025) + `"}`, "profileKey", "invalid"},
	}
	for _, c := range cases {
		var ck checker
		in := decode(t, c.body)
		m := driversdb.UpdateDriverParams{FirstName: "A", LastName: "B", FullNameTh: ptr("ก ข"), Mobile: "0", Status: StatusActive,
			EmploymentType: "full_time"}
		merge(&ck, &in, &m, false, today)
		if got := reasons(ck)[c.field]; got != c.reason {
			t.Errorf("%s: %s = %q, want %q (%v)", c.body, c.field, got, c.reason, ck.bad)
		}
	}
}

func TestMergeAgeBounds(t *testing.T) {
	for birth, ok := range map[string]bool{"2006-10-11": true, "2006-10-12": false, "1970-10-12": true, "1970-10-11": false} {
		var ck checker
		in := decode(t, `{"birthDate":"`+birth+`"}`)
		m := driversdb.UpdateDriverParams{}
		merge(&ck, &in, &m, false, today)
		if (len(ck.bad) == 0) != ok {
			t.Errorf("%s: %v", birth, ck.bad)
		}
	}
}

// A legacy value that breaks a form rule stays editable while it comes back unchanged; a change must pass the rule.
func TestMergeLegacyValuesSentBack(t *testing.T) {
	birth := time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC)
	m := driversdb.UpdateDriverParams{FirstName: "A", LastName: "B", Mobile: "0", Status: StatusActive, EmploymentType: "full_time",
		IDCard: ptr("1234567890123"), TruckLicenseID: ptr("123"), BirthDate: &birth}
	var ck checker
	in := decode(t, `{"idCard":"1234567890123","truckLicenseId":"123","birthDate":"1950-01-01","mobile":"1"}`)
	changed := merge(&ck, &in, &m, false, today)
	if len(ck.bad) != 0 || !slices.Equal(changed, []string{"mobile"}) {
		t.Fatalf("legacy values sent back: %v, changed %v", ck.bad, changed)
	}
}

func TestMergeChangesAndNormalises(t *testing.T) {
	m := driversdb.UpdateDriverParams{FirstName: "A", LastName: "B", Mobile: "0", Status: StatusActive, EmploymentType: "full_time"}
	var ck checker
	in := decode(t, `{"firstName":" A ","email":"Some@One.TEST","idCard":"1101700230708","contractYears":2.25,
		"probationPassed":true,"status":"on_duty","assignToProject":"","hireDate":"2024-01-31"}`)
	changed := merge(&ck, &in, &m, false, today)
	if len(ck.bad) != 0 {
		t.Fatal(ck.bad)
	}
	want := []string{"email", "idCard", "contractYears", "hireDate", "probationPassed", "status"}
	if !slices.Equal(changed, want) {
		t.Errorf("changed %v, want %v", changed, want)
	}
	if *m.Email != "some@one.test" || m.AssignToProject != nil || *m.ContractYears != 2.25 || m.Status != StatusOnDuty {
		t.Errorf("merged %+v", m)
	}
}

func TestRefusedFields(t *testing.T) {
	in := decode(t, `{"userId":"x","authId":"y","login":{"email":"a@b.c"},"password":"p","loginPassword":"p",
		"activeTruck":null,"currentAssignment":{},"currentAssignmentId":"z"}`)
	var ck checker
	in.refused(&ck)
	got := reasons(ck)
	want := map[string]string{"userId": "not_editable", "authId": "not_editable", "login": "not_allowed", "password": "not_allowed",
		"loginPassword": "not_allowed", "activeTruck": "not_editable", "currentAssignment": "not_editable",
		"currentAssignmentId": "not_editable"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q", k, got[k])
		}
	}
	in = decode(t, `{"tenantId":"x","statusNote":"y"}`)
	if in.others() || in.piiSet() {
		t.Error("tenantId and statusNote alone")
	}
	for _, f := range []string{"idCard", "idCardExpiredDate", "birthDate", "idCardKey", "licenseKey"} {
		if in = decode(t, `{"`+f+`":null}`); !in.piiSet() || !in.others() {
			t.Errorf("%s is PII", f)
		}
	}
	if in = decode(t, `{"truckLicenseId":"12345678","profileKey":"k"}`); in.piiSet() {
		t.Error("the licence number and the profile photo are not PII (Appendix C §C.2.3 row 15)")
	}
}

func TestParseCodes(t *testing.T) {
	var ck checker
	if parseCodes(&ck, Field[[]CustomerDriverIDInput]{}) != nil {
		t.Error("absent: nil")
	}
	if c := parseCodes(&ck, Field[[]CustomerDriverIDInput]{Set: true, Null: true}); c == nil || len(c) != 0 {
		t.Error("null: empty")
	}
	id := uuid.MustParse("019a0000-0000-7000-8000-000000000001").String()
	in := decode(t, `{"customerDriverIds":[{"customerId":"`+id+`","idTypeKey":"appId","value":" 1 "},
		{"customerId":"`+id+`","idTypeKey":"appId","value":"2"},{"customerId":"x","idTypeKey":"k","value":"v"},
		{"customerId":"`+id+`","idTypeKey":"","value":"v"},{"customerId":"`+id+`","idTypeKey":"workId","value":""}]}`)
	codes := parseCodes(&ck, in.CustomerDriverIDs)
	if len(codes) != 1 || codes[0].value != "1" {
		t.Errorf("codes %+v", codes)
	}
	want := map[string]string{"customerDriverIds[1].idTypeKey": "duplicate", "customerDriverIds[2].customerId": "invalid",
		"customerDriverIds[3].idTypeKey": "required", "customerDriverIds[4].value": "required"}
	got := reasons(ck)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q (%v)", k, got[k], got)
		}
	}
	ck = checker{}
	parseCodes(&ck, Field[[]CustomerDriverIDInput]{Set: true, V: make([]CustomerDriverIDInput, MaxCustomerIDs+1)})
	if reasons(ck)["customerDriverIds"] != "too_many" {
		t.Errorf("%v", ck.bad)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 6000, time.UTC)
	id := uuid.MustParse("019a0000-0000-7000-8000-000000000009")
	c, ok := decodeCursor(cursor{T: &ts, ID: id}.encode())
	if !ok || !c.T.Equal(ts) || c.ID != id {
		t.Errorf("time cursor %+v", c)
	}
	s := "สมชาย"
	if c, ok = decodeCursor(cursor{S: &s, ID: id}.encode()); !ok || *c.S != s {
		t.Errorf("name cursor %+v", c)
	}
	for _, bad := range []string{"", "!!", cursor{ID: id}.encode(), cursor{T: &ts, S: &s, ID: id}.encode(), cursor{T: &ts}.encode()} {
		if _, ok := decodeCursor(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestDisplayName(t *testing.T) {
	if got := displayName(ptr(" สมชาย ใจดี "), "Somchai", "Jaidee"); got != "สมชาย ใจดี" {
		t.Errorf("%q", got)
	}
	if got := displayName(ptr("   "), "Somchai", "Jaidee"); got != "Somchai Jaidee" {
		t.Errorf("%q", got)
	}
	if got := displayName(nil, "", "Jaidee"); got != "Jaidee" {
		t.Errorf("%q", got)
	}
}

// The detail never carries PII as null-masked keys: without drivers:view_pii the keys are absent.
func TestPIIKeysAbsentWithoutCapability(t *testing.T) {
	d := DriverDetail{Driver: Driver{ID: uuid.New()}, CustomerDriverIDs: []CustomerDriverID{}, StatusHistory: []StatusEntry{}}
	b, _ := json.Marshal(d)
	for _, k := range []string{"idCard", "birthDate", "idCardExpiredDate", "idCardKey", "idCardUrl", "licenseKey", "licenseUrl"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("%s in %s", k, b)
		}
	}
	d.PII = &PII{}
	b, _ = json.Marshal(d)
	for _, k := range []string{"idCard", "birthDate", "idCardKey", "licenseUrl"} {
		if !strings.Contains(string(b), `"`+k+`":null`) {
			t.Errorf("%s missing with the capability: %s", k, b)
		}
	}
}

func TestTargetTenant(t *testing.T) {
	own, sub, other := uuid.New(), uuid.New(), uuid.New()
	p := &authz.Principal{SubtenantIDs: []uuid.UUID{sub}}
	for _, c := range []struct {
		in     Field[string]
		want   uuid.UUID
		reason string
	}{
		{Field[string]{}, own, ""}, {Field[string]{Set: true, Null: true}, own, ""}, {Field[string]{Set: true, V: own.String()}, own, ""},
		{Field[string]{Set: true, V: sub.String()}, sub, ""}, {Field[string]{Set: true, V: other.String()}, own, "not_in_reach"},
		{Field[string]{Set: true, V: "x"}, own, "invalid"},
	} {
		var ck checker
		if got := targetTenant(&ck, p, own, c.in); got != c.want || reasons(ck)["tenantId"] != c.reason {
			t.Errorf("%+v: %s %v", c.in, got, ck.bad)
		}
	}
}

func TestMapDBError(t *testing.T) {
	plain := errors.New("x")
	if !errors.Is(mapDBError(plain), plain) {
		t.Error("non-database errors pass through")
	}
	for code, want := range map[string]int{"23505": http.StatusConflict, "23503": http.StatusUnprocessableEntity,
		"23514": http.StatusUnprocessableEntity, "22021": http.StatusUnprocessableEntity, "42501": http.StatusForbidden} {
		if e := httpx.AsError(mapDBError(&pgconn.PgError{Code: code, ConstraintName: "c"})); e.Status != want {
			t.Errorf("%s: %d", code, e.Status)
		}
	}
	if e := httpx.AsError(errPIIWrite()); e.Status != http.StatusForbidden || e.Code != "permission_denied" {
		t.Errorf("%+v", e)
	}
}
