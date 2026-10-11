package expenses

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/expenses/expensesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/xlsx"
)

func bkk(y int, m time.Month, d, hh, mi, ss int) time.Time {
	return time.Date(y, m, d, hh, mi, ss, 0, clock.Bangkok)
}

func TestNormalizeStatus(t *testing.T) {
	for in, want := range map[string]string{"PENDING": "pending", "Approved": "approved", " rejected ": "rejected"} {
		if got, ok := NormalizeStatus(in); !ok || got != want {
			t.Errorf("NormalizeStatus(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := NormalizeStatus("cancelled"); ok {
		t.Error("cancelled is not an expense status")
	}
	if LegacyStatus("approved") != "APPROVED" {
		t.Error("the legacy literal is upper case")
	}
}

func TestParseInstant(t *testing.T) {
	for in, want := range map[string]time.Time{
		"2026-10-11T03:00:00Z":      time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC),
		"2026-10-11T10:00:00+07:00": time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC),
		"2026-10-11T10:00":          bkk(2026, 10, 11, 10, 0, 0),
		"2026-10-11 10:00:05":       bkk(2026, 10, 11, 10, 0, 5),
		"2026-10-11":                bkk(2026, 10, 11, 0, 0, 0),
	} {
		got, ok := parseInstant(in)
		if !ok || !got.Equal(want) {
			t.Errorf("parseInstant(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "11/10/2026", "2026-02-30", "2026-13-01T00:00"} {
		if _, ok := parseInstant(in); ok {
			t.Errorf("parseInstant(%q) accepted", in)
		}
	}
}

// The toll dialog's date rules on the Bangkok wall clock (parseTollDate, TollExpenseImportDialog.tsx:93-120).
func TestParseTollTime(t *testing.T) {
	for in, want := range map[string]time.Time{
		"15/03/2026 08:15:30":  bkk(2026, 3, 15, 8, 15, 30), // day > 12: d/m
		"03/15/2026 08:15":     bkk(2026, 3, 15, 8, 15, 0),  // second part > 12: m/d
		"05/03/2026":           bkk(2026, 3, 5, 0, 0, 0),    // ambiguous: d/m
		"5-3-26 7":             bkk(2026, 3, 5, 7, 0, 0),    // two-digit year
		"15.03.2569 08:15":     bkk(2026, 3, 15, 8, 15, 0),  // Buddhist-era year
		"45366.5":              bkk(2024, 3, 15, 12, 0, 0),  // Excel serial with its time of day
		"2026-03-15T08:15:00":  bkk(2026, 3, 15, 8, 15, 0),
		"2026-03-15 08:15":     bkk(2026, 3, 15, 8, 15, 0),
		"2026-03-15T01:15:00Z": time.Date(2026, 3, 15, 1, 15, 0, 0, time.UTC),
		"1773536100000":        time.UnixMilli(1773536100000),
	} {
		got, ok := ParseTollTime(in)
		if !ok || !got.Equal(want) {
			t.Errorf("ParseTollTime(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "ผ่านทาง", "31/02/2026", "15/03/2026 25:00", "123", "13/13/2026"} {
		if got, ok := ParseTollTime(in); ok {
			t.Errorf("ParseTollTime(%q) = %v, accepted", in, got)
		}
	}
}

func TestIsTollType(t *testing.T) {
	nbsp := string(rune(0xA0))
	for _, in := range []string{"ผ่านทาง", "ค่าผ่านทาง" + nbsp + "ด่วน", "ทางด่วนพิเศษ", "Easy Pass", "M-Flow MFLOW", "toll", "Expressway toll",
		"toll plaza", "easy-pass top"} {
		if !IsTollType(in) {
			t.Errorf("IsTollType(%q) = false", in)
		}
	}
	for _, in := range []string{"", "เติมเงิน", "Top up", "tollway", "parking"} {
		if IsTollType(in) {
			t.Errorf("IsTollType(%q) = true", in)
		}
	}
}

func sheet(header []string, rows ...[]string) *xlsx.Sheet {
	s := &xlsx.Sheet{Header: header, Rows: rows}
	for i := range rows {
		s.Line = append(s.Line, i+2)
	}
	return s
}

// The card-history layout and its header detection; a title row before the header; rows that are not tolls or do not
// parse keep their reason; a missing sequence is the row's position.
func TestTollRowsFromSheet(t *testing.T) {
	s := sheet([]string{"ลำดับ", "สถานที่", "เครื่อง/เลน", "วันที่เกิดรายการ", "ประเภท", "จำนวนเงิน"},
		[]string{"1", "ด่านบางนา", "03", "15/03/2026 08:15:30", "ผ่านทาง", "45"},
		[]string{"2", "สาขาสีลม", "", "15/03/2026 09:00", "เติมเงิน", "500"},
		[]string{"", "ด่านดินแดง", "1", "16/03/2026 10:00", "ผ่านทาง", "1,050.50"},
		[]string{"4", "ด่านลาดพร้าว", "2", "ไม่ระบุ", "ผ่านทาง", "45"},
		[]string{"5", "ด่านลาดพร้าว", "2", "17/03/2026", "ผ่านทาง", "-"})
	rows, err := TollRowsFromSheet(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("%d rows", len(rows))
	}
	r := rows[0]
	if r.Skip != "" || *r.Sequence != 1 || r.Location != "ด่านบางนา" || r.Lane != "03" || r.SourceType != "ผ่านทาง" ||
		r.AmountThb != 45 || !r.At.Equal(bkk(2026, 3, 15, 8, 15, 30)) || r.Row != 2 {
		t.Errorf("row 1: %+v", r)
	}
	if rows[1].Skip != SkipNotToll || rows[3].Skip != SkipInvalidDate || rows[4].Skip != SkipInvalidAmount {
		t.Errorf("reasons %q %q %q", rows[1].Skip, rows[3].Skip, rows[4].Skip)
	}
	if rows[2].Skip != "" || *rows[2].Sequence != 3 || rows[2].AmountThb != 1050.5 {
		t.Errorf("row 3 (no sequence): %+v", rows[2])
	}
	if got := tollDescription(rows[0]); got != "ผ่านทาง · ด่านบางนา · 03" {
		t.Errorf("description %q", got)
	}
	if got := tollDescription(TollRow{}); got != "Toll" {
		t.Errorf("empty description %q", got)
	}

	// An English export with a title row: the header is found among the first rows.
	s = sheet([]string{"Easy Pass statement"},
		[]string{"No.", "Location", "Lane", "Transaction Date", "Type", "Amount"},
		[]string{"1", "Bang Na", "3", "2026-03-15 08:15", "Expressway toll", "60"})
	rows, err = TollRowsFromSheet(s)
	if err != nil || len(rows) != 1 || rows[0].Skip != "" || rows[0].AmountThb != 60 || rows[0].Location != "Bang Na" {
		t.Fatalf("title row: %+v, %v", rows, err)
	}

	// Columns inferred from the cells when no header names them.
	s = sheet([]string{"a", "b", "c"},
		[]string{"15/03/2026 08:00", "ผ่านทาง", "45"}, []string{"16/03/2026 08:00", "ผ่านทาง", "50"})
	if rows, err = TollRowsFromSheet(s); err != nil || len(rows) != 2 || rows[1].AmountThb != 50 {
		t.Fatalf("inferred: %+v, %v", rows, err)
	}
	if _, err := TollRowsFromSheet(sheet([]string{"x"}, []string{"y"})); err != ErrNoTollColumns {
		t.Fatalf("no columns: %v", err)
	}
}

func TestTollRowsFromJSON(t *testing.T) {
	amt, neg := 45.0, -1.0
	seq := int64(7)
	rows := TollRowsFromJSON([]TollRowInput{
		{Sequence: &seq, Location: " ด่าน ", Date: "2026-03-15T08:15:00+07:00", AmountThb: &amt},
		{Date: "15/03/2026 09:00", AmountThb: &amt},
		{Date: "x", AmountThb: &amt},
		{Date: "15/03/2026", AmountThb: &neg},
		{Date: "15/03/2026"},
	})
	if rows[0].Skip != "" || *rows[0].Sequence != 7 || rows[0].Location != "ด่าน" || rows[0].Row != 1 {
		t.Errorf("row 1 %+v", rows[0])
	}
	if *rows[1].Sequence != 2 || rows[1].Skip != "" {
		t.Errorf("row 2 %+v", rows[1])
	}
	if rows[2].Skip != SkipInvalidDate || rows[3].Skip != SkipInvalidAmount || rows[4].Skip != SkipInvalidAmount {
		t.Errorf("reasons %q %q %q", rows[2].Skip, rows[3].Skip, rows[4].Skip)
	}
}

func decode(t *testing.T, body string) Input {
	t.Helper()
	var in Input
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

// Type-specific fields: fuel fields on an other row and other fields on a fuel row are not_available, a null or blank
// one counts as absent; amounts have two decimals, litres three.
func TestMerge(t *testing.T) {
	var ck checker
	f := fields{}
	merge(&ck, ptr(decode(t, `{"date":"2026-10-11T08:00","amountThb":1500.25,"volumeLiters":40.123,"pricePerLiter":37.39,
		"odometerKm":98500,"stationTaxId":"0105","taxInvId":"INV-1","refillLat":13.7,"refillLng":100.5,"category":null,
		"tollLane":""}`)), &f, TypeFuel, true)
	if err := ck.err(); err != nil {
		t.Fatal(reasons(ck))
	}
	if f.Amount != 1500.25 || *f.Volume != 40.123 || *f.Odometer != 98500 || *f.TaxInv != "INV-1" || !f.At.Equal(bkk(2026, 10, 11, 8, 0, 0)) {
		t.Fatalf("merged %+v", f)
	}
	ck, f = checker{}, fields{}
	merge(&ck, ptr(decode(t, `{"amountThb":1.234,"volumeLiters":-1,"category":"toll","tollLocation":"x","refillLat":13}`)),
		&f, TypeFuel, true)
	got := reasons(ck)
	for field, reason := range map[string]string{"date": "required", "amountThb": "too_precise", "volumeLiters": "out_of_range",
		"category": "not_available", "tollLocation": "not_available", "refillLng": "incomplete"} {
		if got[field] != reason {
			t.Errorf("%s: %q, want %q (all %v)", field, got[field], reason, got)
		}
	}
	ck, f = checker{}, fields{}
	merge(&ck, ptr(decode(t, `{"date":"2026-10-11","amountThb":30,"category":"Parking","volumeLiters":1,"taxInvId":"A"}`)),
		&f, TypeOther, true)
	got = reasons(ck)
	if got["volumeLiters"] != "not_available" || got["taxInvId"] != "not_available" || *f.Category != "parking" {
		t.Errorf("other row: %v category %v", got, f.Category)
	}
	// A PATCH reports only what changed.
	cur := fields{At: bkk(2026, 10, 11, 0, 0, 0), Amount: 30}
	ck = checker{}
	changed := merge(&ck, ptr(decode(t, `{"date":"2026-10-11","amountThb":30,"note":"x"}`)), &cur, TypeOther, false)
	if len(changed) != 1 || changed[0] != "note" {
		t.Errorf("changed %v", changed)
	}
}

func ptr[T any](v T) *T { return &v }

// The write-back shape of a row (vehicle_expense.dart toFirestore, main spec §13.8).
func TestLegacyFields(t *testing.T) {
	plate, inv := "กข-1234", "INV-1"
	odo := int32(98500)
	lat, lng := 13.75, 100.5
	e := expensesdb.VehicleExpense{ID: uuid.New(), ExpenseType: TypeFuel, Status: StatusApproved, AmountThb: 1500.25,
		ExpenseAt: time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC), TruckLicensePlateSnapshot: &plate, TaxInvID: &inv,
		OdometerKm: &odo, RefillLat: &lat, RefillLng: &lng}
	f := LegacyFields(e, "authUid1", "trk1")
	for k, want := range map[string]any{"driverId": "authUid1", "truckId": "trk1", "truckLicensePlate": plate, "type": "fuel",
		"status": "APPROVED", "amount": 1500.25, "odometer": int64(98500), "taxInvId": inv, "refillLocation": "13.75,100.5",
		"category": nil, "note": nil} {
		if f[k] != want {
			t.Errorf("%s = %#v, want %#v", k, f[k], want)
		}
	}
	if f["date"] != e.ExpenseAt {
		t.Errorf("date %v", f["date"])
	}
	for _, k := range []string{"receiptPhotoUrl", "odometerPhotoUrl", "clientOpId"} {
		if _, ok := f[k]; ok {
			t.Errorf("%s is the APK's", k)
		}
	}
}

// A staff write of a row touches only what it changed (main spec §12.1, §13.8): a created row writes every staff
// field with createdAt / createdByUid; an audit decision only status and adminNote (the driver's own fields of a
// pending row stay the APK's); a PATCH the Firestore fields of its changed JSON fields; a photo key alone nothing.
func TestLegacyWrite(t *testing.T) {
	now := time.Date(2026, 10, 11, 3, 0, 0, 0, time.UTC)
	note, plate := "ตรวจแล้ว", "กข-1234"
	odo := int32(98500)
	e := expensesdb.VehicleExpense{ID: uuid.New(), ExpenseType: TypeFuel, Status: StatusApproved, AmountThb: 1500,
		ExpenseAt: now, AdminNote: &note, OdometerKm: &odo, TruckLicensePlateSnapshot: &plate}
	refs := LegacyRefs{Driver: "authUid1", Truck: "trk1", CreatedBy: "uidStaff"}

	created := LegacyWrite(e, refs, nil, true, now)
	if created["driverId"] != "authUid1" || created["status"] != "APPROVED" || created["amount"] != 1500.0 ||
		created["createdAt"] != now || created["createdByUid"] != "uidStaff" || created["updatedAt"] != now {
		t.Fatalf("created %v", created)
	}
	if _, ok := LegacyWrite(e, LegacyRefs{}, nil, true, now)["createdByUid"]; ok {
		t.Error("an unknown creator is written")
	}
	decision := LegacyWrite(e, LegacyRefs{}, []string{"status", "adminNote"}, false, now)
	if want := map[string]any{"status": "APPROVED", "adminNote": note, "updatedAt": now}; !maps.Equal(decision, want) {
		t.Errorf("an audit decision writes %v, want %v", decision, want)
	}
	moved := LegacyWrite(e, refs, []string{"truckId", "odometerKm"}, false, now)
	if want := map[string]any{"truckId": "trk1", "truckLicensePlate": plate, "odometer": int64(98500), "updatedAt": now}; !maps.Equal(moved, want) {
		t.Errorf("a truck change writes %v, want %v", moved, want)
	}
	if w := LegacyWrite(e, refs, []string{"receiptKey"}, false, now); w != nil {
		t.Errorf("a photo key alone writes %v", w)
	}
	all := LegacyFields(e, "authUid1", "trk1")
	for jsonName, names := range legacyNames {
		for _, name := range names {
			if _, ok := all[name]; !ok {
				t.Errorf("%s -> %s is not a field of the legacy shape", jsonName, name)
			}
		}
	}
}

// A deadlock or serialization failure rolled the transaction back: 503 unavailable (retryable), never 500.
func TestMapDBErrorContention(t *testing.T) {
	for _, code := range []string{"40P01", "40001"} {
		var he *httpx.Error
		if err := mapDBError(fmt.Errorf("tx: %w", &pgconn.PgError{Code: code})); !errors.As(err, &he) ||
			he.Status != http.StatusServiceUnavailable || he.Code != httpx.CodeUnavailable || he.Details["reason"] != "contention" {
			t.Errorf("%s: %v", code, err)
		}
	}
}
