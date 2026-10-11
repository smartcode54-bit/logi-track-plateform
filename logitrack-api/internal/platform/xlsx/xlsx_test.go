package xlsx

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func workbook(t *testing.T, rows [][]any, dateCells ...string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	for i, r := range rows {
		cell, err := excelize.CoordinatesToCellName(1, i+1)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetSheetRow("Sheet1", cell, &r); err != nil {
			t.Fatal(err)
		}
	}
	if len(dateCells) > 0 {
		style, err := f.NewStyle(&excelize.Style{NumFmt: 14}) // m/d/yy: a date cell in the author's locale
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range dateCells {
			if err := f.SetCellStyle("Sheet1", c, c, style); err != nil {
				t.Fatal(err)
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadXLSX(t *testing.T) {
	reg := time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC)
	b := workbook(t, [][]any{
		{"License Plate", "Province", "Year", "Registration Date", "Max Load"},
		{" กก-1234 ", "กรุงเทพมหานคร", 2023, reg, 25000},
		{nil, nil, nil, nil, nil}, // blank rows are skipped
		{"1ขค-5678", "นนทบุรี"},
	}, "D2")
	s, err := Read(bytes.NewReader(b), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.Header, []string{"License Plate", "Province", "Year", "Registration Date", "Max Load"}) {
		t.Fatalf("header %q", s.Header)
	}
	if len(s.Rows) != 2 || !slices.Equal(s.Line, []int{2, 4}) {
		t.Fatalf("rows %q lines %v", s.Rows, s.Line)
	}
	if Value(s.Rows[0], 0) != "กก-1234" || Value(s.Rows[0], 2) != "2023" || Value(s.Rows[1], 4) != "" {
		t.Fatalf("row values %q %q", s.Rows[0], s.Rows[1])
	}
	// The date cell is read raw (its serial), whatever display format the author's locale gave it.
	d, ok := Date(Value(s.Rows[0], 3))
	if !ok || !d.Equal(reg) {
		t.Fatalf("date cell %q -> %v %v", Value(s.Rows[0], 3), d, ok)
	}
	if n, ok := Number(Value(s.Rows[0], 4)); !ok || n != 25000 {
		t.Fatalf("number cell %q -> %v %v", Value(s.Rows[0], 4), n, ok)
	}
}

func TestReadCSV(t *testing.T) {
	in := "\xef\xbb\xbfLicense Plate,Province\n\"กก-1234\",กรุงเทพมหานคร\n,\n1ขค-5678,\"นนทบุรี, เมือง\"\n"
	s, err := Read(strings.NewReader(in), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Header[0] != "License Plate" || len(s.Rows) != 2 || !slices.Equal(s.Line, []int{2, 4}) {
		t.Fatalf("csv %q %q %v", s.Header, s.Rows, s.Line)
	}
	if s.Rows[1][1] != "นนทบุรี, เมือง" {
		t.Fatalf("quoted cell %q", s.Rows[1][1])
	}
}

func TestReadLimits(t *testing.T) {
	rows := [][]any{{"h"}}
	for range 4 {
		rows = append(rows, []any{"x"})
	}
	if _, err := Read(bytes.NewReader(workbook(t, rows)), Options{MaxRows: 3}); !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("4 rows over a limit of 3: %v", err)
	}
	if _, err := Read(strings.NewReader("h\na\nb\nc\nd\n"), Options{MaxRows: 3}); !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("csv over the limit: %v", err)
	}
	if _, err := Read(bytes.NewReader([]byte{0xff, 0xfe, 0x00, 0x01}), Options{}); !errors.Is(err, ErrNotSpreadsheet) {
		t.Fatalf("binary garbage: %v", err)
	}
	// A zip that is not a workbook.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("hello.txt")
	_, _ = w.Write([]byte("hi"))
	_ = zw.Close()
	if _, err := Read(&buf, Options{}); !errors.Is(err, ErrNotSpreadsheet) {
		t.Fatalf("zip without a workbook: %v", err)
	}
	// A workbook whose parts unzip past the bound fails instead of filling memory.
	big := [][]any{{"h"}}
	for range 2000 {
		big = append(big, []any{strings.Repeat("z", 200)})
	}
	if _, err := Read(bytes.NewReader(workbook(t, big)), Options{MaxUnzipBytes: 64 << 10}); !errors.Is(err, ErrNotSpreadsheet) {
		t.Fatalf("unzip bound: %v", err)
	}
}

func TestDate(t *testing.T) {
	ok := map[string]string{
		"44941":               "2023-01-15", // Excel serial
		"44941.75":            "2023-01-15", // with a time of day
		"2023-01-15":          "2023-01-15",
		"2023-1-5":            "2023-01-05",
		"2023-01-15T00:00:00": "2023-01-15",
		"15/01/2023":          "2023-01-15",
		"5/1/2023":            "2023-01-05",
		"15.01.2566":          "2023-01-15", // Buddhist era
		"15-01-2023":          "2023-01-15",
		"2567-02-29":          "2024-02-29",
		"243708":              "2024-03-31", // the serial of a date cell typed 31/03/2567 (Buddhist era)
		"243708.5":            "2024-03-31",
		serial(2566, 1, 15):   "2023-01-15",
	}
	for in, want := range ok {
		d, good := Date(in)
		if !good || d.Format(time.DateOnly) != want || d.Location() != time.UTC {
			t.Errorf("Date(%q) = %v %v, want %s", in, d, good, want)
		}
	}
	// 29 February 2568 exists (Gregorian 2568 is a leap year) but 2025-02-29 does not: not a date, in every form.
	for _, in := range []string{"", "abc", "0", "-3", "31/04/2023", "2023-13-01", "2023-02-30", "1/2/23", "2958466",
		serial(2568, 2, 29), "29/02/2568", "2568-02-29"} {
		if d, good := Date(in); good {
			t.Errorf("Date(%q) = %v, want not a date", in, d)
		}
	}
}

func TestNumber(t *testing.T) {
	for in, want := range map[string]float64{"25000": 25000, "25,000": 25000, " 1,234.50 ": 1234.5, "-3": -3, "1e3": 1000} {
		if n, ok := Number(in); !ok || n != want {
			t.Errorf("Number(%q) = %v %v", in, n, ok)
		}
	}
	for _, in := range []string{"", "abc", "NaN", "Inf", "1.2.3"} {
		if _, ok := Number(in); ok {
			t.Errorf("Number(%q) ok", in)
		}
	}
}

// serial is the Excel serial (1900 date system) of a calendar day, as a raw date cell holds it.
func serial(y, m, d int) string {
	days := (time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).Unix() - time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).Unix()) / 86400
	return strconv.FormatInt(days, 10)
}

// A date cell typed in the Buddhist era on a Gregorian dd/MM/yyyy locale holds a 25xx serial: it is converted like
// the same date typed as text.
func TestReadBuddhistDateCell(t *testing.T) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	for i, r := range [][]any{{"licensePlate", "taxExpiryDate"}, {"กก-1", time.Date(2567, 3, 31, 0, 0, 0, 0, time.UTC)},
		{"กก-2", "31/03/2567"}} {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", cell, &r); err != nil {
			t.Fatal(err)
		}
	}
	format := "dd/mm/yyyy"
	style, err := f.NewStyle(&excelize.Style{CustomNumFmt: &format})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellStyle("Sheet1", "B2", "B2", style); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	s, err := Read(&buf, Options{MaxRows: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if raw := Value(s.Rows[0], 1); raw != "243708" {
		t.Fatalf("the date cell is not read raw: %q", raw)
	}
	for i, r := range s.Rows {
		if d, ok := Date(Value(r, 1)); !ok || d.Format(time.DateOnly) != "2024-03-31" {
			t.Errorf("row %d (%q): %v %v", i, Value(r, 1), d, ok)
		}
	}
}

// wide is a workbook of a header and rows rows, each with one cell at column col: a value, or a formula without a
// cached value. The sheet XML is written by hand (excelize would itself pad each row to the column).
func wide(t *testing.T, rows int, col string, formula bool) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	b.WriteString(`<row r="1"><c r="A1" t="inlineStr"><is><t>licensePlate</t></is></c></row>`)
	for i := 2; i <= rows+1; i++ {
		cell := col + strconv.Itoa(i)
		if formula {
			fmt.Fprintf(&b, `<row r="%d"><c r="%s"><f>1+1</f></c></row>`, i, cell)
		} else {
			fmt.Fprintf(&b, `<row r="%d"><c r="%s" t="inlineStr"><is><t>x</t></is></c></row>`, i, cell)
		}
	}
	b.WriteString(`</sheetData></worksheet>`)
	return rezip(t, workbook(t, [][]any{{"h"}}), map[string][]byte{"xl/worksheets/sheet1.xml": []byte(b.String())})
}

// allocated is the bytes allocated while fn ran.
func allocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A row is bounded in width before it is trimmed: a cell in column XFD (excelize pads the row to 16384 cells) fails
// at the first such row, a value or a formula alike, so 2000 of them do not pin hundreds of megabytes.
func TestReadColumnBound(t *testing.T) {
	for _, formula := range []bool{false, true} {
		b := wide(t, 2000, "XFD", formula)
		var err error
		n := allocated(func() { _, err = Read(bytes.NewReader(b), Options{MaxRows: 2000}) })
		if !errors.Is(err, ErrTooManyColumns) {
			t.Fatalf("formula %v: %v", formula, err)
		}
		if n > 32<<20 {
			t.Fatalf("formula %v: a refused wide sheet allocated %d MiB", formula, n>>20)
		}
		t.Logf("formula %v: %d KiB allocated", formula, n>>10)
	}
	if _, err := Read(bytes.NewReader(wide(t, 3, "E", false)), Options{MaxCols: 5}); err != nil {
		t.Fatalf("a sheet as wide as the bound: %v", err)
	}
	if _, err := Read(bytes.NewReader(wide(t, 3, "F", false)), Options{MaxCols: 5}); !errors.Is(err, ErrTooManyColumns) {
		t.Fatalf("one column past the bound: %v", err)
	}
	if _, err := Read(strings.NewReader("a,b\n1,2,3,4,5,6\n"), Options{MaxCols: 5}); !errors.Is(err, ErrTooManyColumns) {
		t.Fatalf("csv: %v", err)
	}
	if _, err := Read(strings.NewReader("a,b\n1,2,3,4,5\n"), Options{MaxCols: 5}); err != nil {
		t.Fatalf("csv as wide as the bound: %v", err)
	}
}

// Lines count blank rows too: a stray cell far down the sheet, or a CSV of empty records, cannot make a read walk
// hundreds of thousands of lines.
func TestReadLineBound(t *testing.T) {
	o := Options{MaxRows: 10}
	if o.MaxLines() != 50000 || (Options{MaxRows: 20000}).MaxLines() != 80000 {
		t.Fatalf("MaxLines %d", o.MaxLines())
	}
	f := excelize.NewFile()
	_ = f.SetCellValue("Sheet1", "A1", "licensePlate")
	_ = f.SetCellValue("Sheet1", "A50001", "กก-1")
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err := Read(bytes.NewReader(buf.Bytes()), o); !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("a cell on line 50001: %v", err)
	}
	if _, err := Read(strings.NewReader("h\n"+strings.Repeat(",\n", 50001)), o); !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("csv of blank records: %v", err)
	}
	if s, err := Read(strings.NewReader("h\n"+strings.Repeat(",\n", 49990)+"x\n"), o); err != nil || len(s.Rows) != 1 {
		t.Fatalf("blank records within the bound: %v", err)
	}
}

// rezip copies a workbook, replacing (or adding) the named parts.
func rezip(t *testing.T, b []byte, parts map[string][]byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	write := func(name string, body []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range zr.File {
		if body, ok := parts[f.Name]; ok {
			write(f.Name, body)
			delete(parts, f.Name)
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rc)
		_ = rc.Close()
		write(f.Name, body)
	}
	for name, body := range parts {
		write(name, body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// The shared-strings table is unmarshalled whole before the first row, so its size is bounded before excelize
// opens the file: a zip of ~100 KB holding most of a million tiny strings is refused quickly, in bounded memory.
func TestReadSharedStringsBound(t *testing.T) {
	base := workbook(t, [][]any{{"licensePlate"}, {"กก-1"}})
	sst := func(n int) []byte {
		var b bytes.Buffer
		fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">`, n, n)
		b.WriteString(strings.Repeat("<si><t>a</t></si>", n))
		b.WriteString("</sst>")
		return b.Bytes()
	}
	bomb := rezip(t, base, map[string][]byte{"xl/sharedStrings.xml": sst(900000)})
	if len(bomb) > 200<<10 {
		t.Fatalf("the bomb is %d bytes", len(bomb))
	}
	var err error
	start := time.Now()
	n := allocated(func() { _, err = Read(bytes.NewReader(bomb), Options{MaxRows: 2000}) })
	if !errors.Is(err, ErrNotSpreadsheet) {
		t.Fatalf("shared strings bomb: %v", err)
	}
	if n > 96<<20 || time.Since(start) > 5*time.Second {
		t.Fatalf("refusing the bomb allocated %d MiB in %v", n>>20, time.Since(start))
	}
	t.Logf("shared strings bomb of %d bytes: %d KiB allocated in %v", len(bomb), n>>10, time.Since(start))
	// The bound counts the table's strings (the base workbook holds two).
	if _, err := Read(bytes.NewReader(base), Options{MaxSharedStrings: 2}); err != nil {
		t.Fatalf("a workbook within the bound: %v", err)
	}
	if _, err := Read(bytes.NewReader(base), Options{MaxSharedStrings: 1}); !errors.Is(err, ErrNotSpreadsheet) {
		t.Fatalf("one string past the bound: %v", err)
	}
	// The other parts excelize unmarshals whole when it opens a workbook (styles here) have an element budget too.
	styles := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><cellXfs>` +
		strings.Repeat(`<xf numFmtId="0"/>`, 800000) + `</cellXfs></styleSheet>`
	bomb = rezip(t, base, map[string][]byte{"xl/styles.xml": []byte(styles)})
	n = allocated(func() { _, err = Read(bytes.NewReader(bomb), Options{MaxRows: 2000}) })
	if !errors.Is(err, ErrNotSpreadsheet) || n > 96<<20 {
		t.Fatalf("styles bomb of %d bytes: %v, %d MiB allocated", len(bomb), err, n>>20)
	}
	// Too many archive entries.
	parts := map[string][]byte{}
	for i := range maxZipEntries {
		parts[fmt.Sprintf("x/%d.bin", i)] = nil
	}
	if _, err := Read(bytes.NewReader(rezip(t, base, parts)), Options{}); !errors.Is(err, ErrNotSpreadsheet) {
		t.Fatalf("archive entries: %v", err)
	}
}

// At most MaxConcurrentReads reads run at once; another is ErrBusy without reading.
func TestReadBusy(t *testing.T) {
	for range MaxConcurrentReads {
		readSlots <- struct{}{}
	}
	_, err := Read(strings.NewReader("h\nx\n"), Options{})
	for range MaxConcurrentReads {
		<-readSlots
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("a read past the bound: %v", err)
	}
	if _, err := Read(strings.NewReader("h\nx\n"), Options{}); err != nil {
		t.Fatalf("after the slots were freed: %v", err)
	}
}
