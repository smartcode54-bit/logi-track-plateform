package xlsx

import (
	"archive/zip"
	"bytes"
	"errors"
	"slices"
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
	}
	for in, want := range ok {
		d, good := Date(in)
		if !good || d.Format(time.DateOnly) != want || d.Location() != time.UTC {
			t.Errorf("Date(%q) = %v %v, want %s", in, d, good, want)
		}
	}
	for _, in := range []string{"", "abc", "0", "-3", "31/04/2023", "2023-13-01", "2023-02-30", "1/2/23", "2958466"} {
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
