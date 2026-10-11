package fleet

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/fleetdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/xlsx"
)

func set[T any](v T) Field[T] { return Field[T]{Set: true, V: v} }

func null[T any]() Field[T] { return Field[T]{Set: true, Null: true} }

// fields are the field -> reason pairs a checker collected.
func fields(ck *checker) map[string]string {
	out := map[string]string{}
	for _, v := range ck.bad {
		out[v.Field] = v.Reason
	}
	return out
}

// TestPlateRule is the plate half of the T22 acceptance criterion "plate regex + UNIQUE enforced" (the UNIQUE half
// runs against PostgreSQL in the integration tests).
func TestPlateRule(t *testing.T) {
	for _, p := range []string{"กก-1234", "กก1234", "1กก-1234", "1กก1234", "ฆฆ-999", "1ขค-1234", "กข-1111", "1กข-12"} {
		if !ValidPlate(p) {
			t.Errorf("ValidPlate(%q) = false", p)
		}
	}
	// The form's six-character minimum: "กข-1" and "กก-12" match the pattern but are too short.
	for _, p := range []string{"", "กข-1", "กก-12", "กก123", "70-1234", "AB-1234", "กกก-1234", "กก-12345", "กก 1234", "1กก-", "12กก-1234"} {
		if ValidPlate(p) {
			t.Errorf("ValidPlate(%q) = true", p)
		}
	}
}

func validCreate() TruckInput {
	return TruckInput{LicensePlate: set("กก-1234"), Province: set("กรุงเทพมหานคร"), Status: set("active"),
		Brand: set("Isuzu"), Model: set("D-Max"), Year: set[int32](2022), Color: set("ขาว"), Type: set("4 Wheels Jumbo")}
}

func TestMergeCreate(t *testing.T) {
	var ck checker
	in := validCreate()
	m := newTruck()
	changed := merge(&ck, &in, &m, true)
	if len(ck.bad) > 0 {
		t.Fatalf("valid create: %v", ck.bad)
	}
	if m.VehicleClass == nil || *m.VehicleClass != "4WJ" || m.TaxResponsible != DefaultTaxResponsible ||
		m.MaintenanceResponsible != DefaultMaintenanceResponsible || m.Year != 2022 {
		t.Fatalf("merged row %+v", m)
	}
	if !slices.Contains(changed, "licensePlate") || !slices.Contains(changed, "type") {
		t.Fatalf("changed %v", changed)
	}

	// Every required field of the table is required on create.
	ck = checker{}
	empty, m := TruckInput{}, newTruck()
	merge(&ck, &empty, &m, true)
	want := []string{"brand", "color", "licensePlate", "model", "province", "status", "type", "year"}
	got := slices.Sorted(maps.Keys(fields(&ck)))
	if !slices.Equal(got, want) {
		t.Fatalf("required fields %v, want %v", got, want)
	}
	for _, f := range got {
		if fields(&ck)[f] != "required" {
			t.Errorf("%s: %s", f, fields(&ck)[f])
		}
	}
}

// TestUnknownTypeIsNoClass is the T22 acceptance criterion "unknown truck type maps to nil, never 4WJ" on the write
// path: the class follows the type through the one mapper.
func TestUnknownTypeIsNoClass(t *testing.T) {
	for _, typ := range []string{"Trailer", "12W", "4-wheel-jumbo", "รถพ่วง"} {
		var ck checker
		in := validCreate()
		in.Type = set(typ)
		m := newTruck()
		merge(&ck, &in, &m, true)
		if len(ck.bad) > 0 || m.VehicleClass != nil || m.TypeRaw != typ {
			t.Errorf("type %q: class %v type %q errors %v", typ, m.VehicleClass, m.TypeRaw, ck.bad)
		}
	}
	// A PATCH of the type re-derives the class; a PATCH that leaves the type keeps it.
	cur := newTruck()
	cur.TypeRaw, cur.VehicleClass = "6 Wheels", ptr("6WH")
	var ck checker
	in := TruckInput{Type: set("Trailer")}
	m := cur
	if changed := merge(&ck, &in, &m, false); !slices.Equal(changed, []string{"type"}) || m.VehicleClass != nil {
		t.Fatalf("type change: %v %v", changed, m.VehicleClass)
	}
	m = cur
	in = TruckInput{Notes: set("x")}
	merge(&ck, &in, &m, false)
	if m.VehicleClass == nil || *m.VehicleClass != "6WH" {
		t.Fatalf("class lost on an unrelated patch: %v", m.VehicleClass)
	}
}

func TestMergePatch(t *testing.T) {
	// A legacy plate outside the rule stays editable when it is sent back unchanged.
	cur := newTruck()
	cur.LicensePlate, cur.Status, cur.Brand = "70-1234", "active", "Hino"
	var ck checker
	in := TruckInput{LicensePlate: set("70-1234"), Brand: set(" Hino "), Notes: set("ok")}
	m := cur
	changed := merge(&ck, &in, &m, false)
	if len(ck.bad) > 0 || !slices.Equal(changed, []string{"notes"}) {
		t.Fatalf("unchanged legacy plate: %v %v", changed, ck.bad)
	}
	// Changing it applies the rule.
	ck, m = checker{}, cur
	in = TruckInput{LicensePlate: set("71-5555")}
	merge(&ck, &in, &m, false)
	if fields(&ck)["licensePlate"] != "invalid" {
		t.Fatalf("changed plate: %v", ck.bad)
	}
	// Required fields cannot be cleared; optional ones can.
	ck, m = checker{}, cur
	m.Notes = ptr("old")
	in = TruckInput{Brand: null[string](), Year: null[int32](), Notes: null[string](), TaxResponsible: null[string]()}
	merge(&ck, &in, &m, false)
	if f := fields(&ck); f["brand"] != "required" || f["year"] != "required" || len(f) != 2 {
		t.Fatalf("clearing: %v", ck.bad)
	}
	if m.Notes != nil || m.TaxResponsible != DefaultTaxResponsible {
		t.Fatalf("cleared optional fields: notes %v responsible %q", m.Notes, m.TaxResponsible)
	}
	// Vocabularies, bounds, two decimals, dates, VIN.
	ck, m = checker{}, cur
	in = TruckInput{Status: set("Active"), InsuranceType: set("4"), PaymentMethod: set("Cash"), TaxRenewalStatus: set("done"),
		Year: set[int32](1800), Seats: set[int32](11), PMIntervalKm: set[int32](0), CurrentMileage: set[int32](-1),
		TaxExpenseThb: set(10.555), InsurancePremiumThb: set(-1.0), MaxLoadWeightKg: set(100001.0),
		RegistrationDate: set("2023-02-30"), TaxExpiryDate: set("15/01/2023"), VIN: set("SHORT")}
	merge(&ck, &in, &m, false)
	want := map[string]string{"status": "invalid", "insuranceType": "invalid", "paymentMethod": "invalid",
		"taxRenewalStatus": "invalid", "year": "out_of_range", "seats": "out_of_range", "pmIntervalKm": "out_of_range",
		"currentMileage": "out_of_range", "taxExpenseThb": "too_precise", "insurancePremiumThb": "out_of_range",
		"maxLoadWeightKg": "out_of_range", "registrationDate": "invalid", "taxExpiryDate": "invalid", "vin": "invalid"}
	if got := fields(&ck); !maps.Equal(got, want) {
		t.Fatalf("violations\n got %v\nwant %v", got, want)
	}
	// Good values are normalised: VIN upper case, dates parsed, money with two decimals.
	ck, m = checker{}, cur
	in = TruckInput{VIN: set("mrhfb26a0kp123456"), TaxExpiryDate: set("2027-01-31"), TaxExpenseThb: set(1234.5),
		Seats: set[int32](3), InsuranceType: set("2+"), PaymentMethod: set("company_credit")}
	merge(&ck, &in, &m, false)
	if len(ck.bad) > 0 || *m.Vin != "MRHFB26A0KP123456" || m.TaxExpiryDate.Format(time.DateOnly) != "2027-01-31" ||
		*m.TaxExpenseThb != 1234.5 || *m.Seats != 3 {
		t.Fatalf("normalised %+v %v", m, ck.bad)
	}
}

func TestCheckFiles(t *testing.T) {
	var ck checker
	checkFiles(&ck, map[string][]string{
		"imageFrontRight":   {"a", "b"},
		"insuranceDocument": {"c", "c"},
		"photos":            {"x"},
		"other":             {" "},
	})
	want := map[string]string{"files.imageFrontRight": "too_many", "files.insuranceDocument": "duplicate",
		"files.photos": "invalid", "files.other": "invalid"}
	if got := fields(&ck); !maps.Equal(got, want) {
		t.Fatalf("violations\n got %v\nwant %v", got, want)
	}
	// A key in two kinds is reported once, on whichever kind is checked second.
	ck = checker{}
	checkFiles(&ck, map[string][]string{"imageFrontLeft": {"k"}, "legacyImage": {"k"}})
	if got := fields(&ck); len(got) != 1 || (got["files.imageFrontLeft"] != "duplicate" && got["files.legacyImage"] != "duplicate") {
		t.Fatalf("cross-kind duplicate: %v", ck.bad)
	}
	ck = checker{}
	many := make([]string, MaxFilesPerKind+1)
	for i := range many {
		many[i] = "k" + strings.Repeat("x", i+1)
	}
	checkFiles(&ck, map[string][]string{"legacyImage": many, "documentTax": {"t"}})
	if f := fields(&ck); f["files.legacyImage"] != "too_many" || len(f) != 1 {
		t.Fatalf("too many: %v", ck.bad)
	}
	for _, k := range fileKinds {
		if kindByJSON[k.json].db != k.db || kindByDB[k.db].json != k.json || len(k.purposes) == 0 {
			t.Errorf("kind %+v", k)
		}
	}
	// The kinds are the truck_files CHECK (Appendix A §A.2.2) and nothing else.
	db := []string{"image_front_right", "image_front_left", "image_back_right", "image_back_left", "document_tax",
		"document_register", "tax_receipt", "insurance_receipt", "insurance_document", "legacy_image", "other"}
	if got := slices.Sorted(maps.Keys(kindByDB)); !slices.Equal(got, slices.Sorted(slices.Values(db))) {
		t.Fatalf("kinds %v", got)
	}
}

func TestCursor(t *testing.T) {
	at := time.Date(2026, 10, 1, 1, 2, 3, 4000, time.UTC)
	id := uuid.New()
	c, ok := decodeCursor(cursor{T: &at, ID: id}.encode())
	if !ok || !c.T.Equal(at) || c.ID != id || c.S != nil || c.D != nil {
		t.Fatalf("round trip %+v %v", c, ok)
	}
	d := "2026-10-01"
	if c, ok := decodeCursor(cursor{D: &d, ID: id}.encode()); !ok || *c.D != d {
		t.Fatalf("date cursor %+v %v", c, ok)
	}
	s := "กก-1"
	two := cursor{T: &at, S: &s, ID: id}.encode()
	for _, bad := range []string{"", "!!", "e30", cursor{T: &at}.encode(), two} {
		if _, ok := decodeCursor(bad); ok {
			t.Errorf("decodeCursor(%q) ok", bad)
		}
	}
}

func TestRenewalState(t *testing.T) {
	today := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	day := func(n int) *time.Time { d := today.AddDate(0, 0, n); return &d }
	cases := []struct {
		expiry *time.Time
		status *string
		state  string
		days   *int
	}{
		{nil, nil, StateOverdue, nil},
		{day(-1), nil, StateOverdue, ptr(-1)},
		{day(0), nil, StateExpiringSoon, ptr(0)},
		{day(30), nil, StateExpiringSoon, ptr(30)},
		{day(31), ptr("completed"), StateOK, ptr(31)},
		{day(-5), ptr("in_progress"), StateInProgress, ptr(-5)},
		{nil, ptr("in_progress"), StateInProgress, nil},
	}
	for i, c := range cases {
		state, days := renewalState(c.expiry, c.status, today)
		if state != c.state || !eq(days, c.days) {
			t.Errorf("case %d: %s %v, want %s %v", i, state, days, c.state, c.days)
		}
	}
}

func TestValidateRenewal(t *testing.T) {
	s := &Service{now: func() time.Time { return time.Date(2026, 10, 10, 18, 0, 0, 0, time.UTC) }} // 01:00 on 11 Oct in Bangkok
	r, err := s.validateRenewal(&RenewalInput{Type: "tax", Amount: set(1500.0), ExpiryDate: set("2027-10-10"),
		PaymentMethod: set("transfer"), Responsible: set("Ops")})
	if err != nil {
		t.Fatal(err)
	}
	if !r.completed || r.date.Format(time.DateOnly) != "2026-10-11" || r.legacyNote() != "Expiry: 2027-10-10" ||
		r.subType() != "Tax Renewal" {
		t.Fatalf("tax renewal %+v %q %q", r, r.legacyNote(), r.subType())
	}
	m := fleetdb.UpdateTruckParams{TaxResponsible: "x"}
	r.apply(&m)
	if *m.TaxRenewalStatus != "completed" || *m.TaxExpenseThb != 1500 || m.TaxExpiryDate.Format(time.DateOnly) != "2027-10-10" ||
		*m.PaymentMethod != "transfer" || m.TaxResponsible != "Ops" || m.InsuranceRenewalStatus != nil {
		t.Fatalf("applied %+v", m)
	}

	r, err = s.validateRenewal(&RenewalInput{Type: "insurance", Amount: set(9000.0), ExpiryDate: set("2027-01-31"),
		Coverage: set("2+"), PolicyNumber: set("P-1"), Provider: set("Viriyah"), StartDate: set("2026-02-01"),
		DocumentKeys: set([]string{"d1", "d2"}), Date: set("2026-10-01")})
	if err != nil {
		t.Fatal(err)
	}
	if r.legacyNote() != "Policy: P-1 | Expiry: 2027-01-31" || r.subType() != "Insurance Renewal (2+)" ||
		r.date.Format(time.DateOnly) != "2026-10-01" || !slices.Equal(r.docs, []string{"d1", "d2"}) {
		t.Fatalf("insurance renewal %+v", r)
	}

	// Completed needs the new expiry and the amount; type-specific fields are refused on the other type.
	_, err = s.validateRenewal(&RenewalInput{Type: "tax", Coverage: set("1"), StartDate: set("2026-01-01"),
		DocumentKeys: set([]string{"d"}), Amount: set(1.001)})
	got := map[string]string{}
	for _, f := range errFields(t, err) {
		got[f[0]] = f[1]
	}
	want := map[string]string{"expiryDate": "required", "amount": "too_precise", "coverage": "not_available",
		"startDate": "not_available", "documentKeys": "not_available"}
	if !maps.Equal(got, want) {
		t.Fatalf("violations\n got %v\nwant %v", got, want)
	}
	// A draft (in_progress) needs neither.
	if _, err := s.validateRenewal(&RenewalInput{Type: "insurance", Status: set("in_progress")}); err != nil {
		t.Fatalf("draft: %v", err)
	}
	if _, err := s.validateRenewal(&RenewalInput{Type: "registration"}); err == nil {
		t.Fatal("unknown type accepted")
	}
}

func errFields(t *testing.T, err error) [][2]string {
	t.Helper()
	fs, ok := rowViolations(err)
	if !ok {
		t.Fatalf("not a field error: %v", err)
	}
	out := make([][2]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, [2]string{f.Field, f.Reason})
	}
	return out
}

func TestImportRows(t *testing.T) {
	// The legacy template of TruckImportDialog.tsx, plus a Thai header and an unknown column.
	sheet := &xlsx.Sheet{
		Header: []string{"License Plate", "Province", "Type", "Brand", "Model", "Color", "Year", "VIN", "Engine Number",
			"Fuel Type", "Ownership", "Status", "Registration Date", "Max Load", "หมายเหตุ", "Driver"},
		Rows: [][]string{
			{"กก-1234", "กรุงเทพมหานคร", "10W", "Isuzu", "FXZ360", "White", "2566", "", "", "Diesel", "Company", "Active",
				"44941", "25,000.5", "note", "someone"},
			{"1กก-5", "", "", "", "", "", "20x3", "", "", "", "Elsewhere", "", "31/02/2023", "a lot"},
		},
		Line: []int{2, 5},
	}
	rows, err := RowsFromSheet(sheet)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Line != 2 || rows[1].Line != 5 {
		t.Fatalf("rows %+v", rows)
	}
	var ck checker
	in, own := rows[0].input(&ck)
	if len(ck.bad) > 0 || own != OwnershipOwn {
		t.Fatalf("row 2: %v %q", ck.bad, own)
	}
	if in.LicensePlate.V != "กก-1234" || in.Year.V != 2023 || in.Status.V != "active" ||
		in.RegistrationDate.V != "2023-01-15" || in.MaxLoadWeightKg.V != 25000.5 || in.Notes.V != "note" ||
		in.Type.V != "10W" || in.VIN.Set {
		t.Fatalf("row 2 input %+v", in)
	}
	ck = checker{}
	_, _ = rows[1].input(&ck)
	want := map[string]string{"year": "invalid", "ownership": "invalid", "registrationDate": "invalid", "maxLoadWeightKg": "invalid"}
	if got := fields(&ck); !maps.Equal(got, want) {
		t.Fatalf("row 5\n got %v\nwant %v", got, want)
	}

	if _, err := RowsFromSheet(&xlsx.Sheet{Header: []string{"Province", "Brand"}}); err != ErrNoPlateColumn {
		t.Fatalf("no plate column: %v", err)
	}

	// JSON rows use the field names; a value that is neither a string nor a number is reported.
	js := RowsFromJSON([]map[string]any{
		{"licensePlate": "กก-1", "year": float64(2020), "maxLoadWeightKg": 1.5, "status": "insurance-claim", "unknown": true},
		{"licensePlate": "กก-2", "province": []any{"x"}, "color": nil},
	})
	ck = checker{}
	in, _ = js[0].input(&ck)
	if len(ck.bad) > 0 || js[0].Line != 1 || in.Year.V != 2020 || in.MaxLoadWeightKg.V != 1.5 || in.Status.V != "insurance_claim" {
		t.Fatalf("json row 1 %+v %v", in, ck.bad)
	}
	ck = checker{}
	in, _ = js[1].input(&ck)
	if f := fields(&ck); f["province"] != "invalid" || len(f) != 1 || in.Color.Set {
		t.Fatalf("json row 2: %v", ck.bad)
	}
}

func TestNormHeader(t *testing.T) {
	for in, want := range map[string]string{"License Plate": "licenseplate", "license_plate": "licenseplate",
		"licensePlate": "licenseplate", " ทะเบียนรถ ": "ทะเบียนรถ", "Max Load (kg)": "maxloadkg", "GPS-ID": "gpsid"} {
		if got := normHeader(in); got != want {
			t.Errorf("normHeader(%q) = %q, want %q", in, got, want)
		}
	}
	if importColumns["ทะเบียนรถ"] != "licensePlate" || importColumns["maxload"] != "maxLoadWeightKg" {
		t.Fatal("aliases")
	}
}
