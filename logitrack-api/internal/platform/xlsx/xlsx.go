// Package xlsx reads spreadsheet uploads for the import endpoints (POST /v1/trucks/import first, T22; the hub, task
// and toll imports follow): the first worksheet of an XLSX workbook (xuri/excelize/v2, main spec §2.4) or a CSV file,
// as rows of trimmed cell text, plus the date and number forms such sheets carry. Cells are read raw: a date cell
// is its Excel serial number, never the text of the author's locale, and Date turns it into a calendar date.
//
// An upload is untrusted: the reader bounds the uncompressed size of the workbook (a zip bomb fails instead of
// filling memory) and the number of rows, and a file that is neither a workbook nor CSV is ErrNotSpreadsheet.
package xlsx

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// Errors of Read; the caller answers 422 with the field that carried the file.
var (
	// ErrNotSpreadsheet: the bytes are neither an XLSX workbook nor UTF-8 CSV, or the workbook is unreadable.
	ErrNotSpreadsheet = errors.New("xlsx: not a readable XLSX workbook or CSV file")
	// ErrTooManyRows: the sheet has more data rows than Options.MaxRows.
	ErrTooManyRows = errors.New("xlsx: too many rows")
)

// Options bound a read.
type Options struct {
	// MaxRows is the largest number of data rows (after the header row) accepted; 0 means 10000.
	MaxRows int
	// MaxUnzipBytes bounds the uncompressed workbook and each of its XML parts; 0 means 64 MiB.
	MaxUnzipBytes int64
}

func (o Options) maxRows() int {
	if o.MaxRows > 0 {
		return o.MaxRows
	}
	return 10000
}

func (o Options) maxUnzip() int64 {
	if o.MaxUnzipBytes > 0 {
		return o.MaxUnzipBytes
	}
	return 64 << 20
}

// Sheet is a read worksheet: Header is the first non-empty row, Rows the rows after it. Line is the 1-based
// spreadsheet row number of each row in Rows (what a user sees in Excel), so an import can name the rows it
// rejects. Rows with no non-blank cell are skipped.
type Sheet struct {
	Header []string
	Rows   [][]string
	Line   []int
}

// Value is the trimmed cell of row at column i ("" past the end of the row).
func Value(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

var xlsxMagic = []byte("PK\x03\x04")

// Read reads the first worksheet of an XLSX workbook, or a CSV file when the bytes are not a zip archive.
func Read(r io.Reader, o Options) (*Sheet, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(b, xlsxMagic) {
		return readXLSX(b, o)
	}
	return readCSV(b, o)
}

func readXLSX(b []byte, o Options) (*Sheet, error) {
	f, err := excelize.OpenReader(bytes.NewReader(b), excelize.Options{RawCellValue: true,
		UnzipSizeLimit: o.maxUnzip(), UnzipXMLSizeLimit: o.maxUnzip()})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotSpreadsheet, err)
	}
	defer func() { _ = f.Close() }()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, ErrNotSpreadsheet
	}
	rows, err := f.Rows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotSpreadsheet, err)
	}
	defer func() { _ = rows.Close() }()
	s := &Sheet{}
	line := 0
	for rows.Next() {
		line++
		cols, err := rows.Columns(excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotSpreadsheet, err)
		}
		if err := s.add(cols, line, o); err != nil {
			return nil, err
		}
	}
	if err := rows.Error(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotSpreadsheet, err)
	}
	return s, nil
}

func readCSV(b []byte, o Options) (*Sheet, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")) // Excel's UTF-8 BOM
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return nil, ErrNotSpreadsheet
	}
	cr := csv.NewReader(bytes.NewReader(b))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	s := &Sheet{}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotSpreadsheet, err)
		}
		line, _ := cr.FieldPos(0)
		if err := s.add(rec, line, o); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Sheet) add(cols []string, line int, o Options) error {
	blank := true
	for i, c := range cols {
		cols[i] = strings.TrimSpace(c)
		if cols[i] != "" {
			blank = false
		}
	}
	if blank {
		return nil
	}
	if s.Header == nil {
		s.Header = cols
		return nil
	}
	if len(s.Rows) >= o.maxRows() {
		return ErrTooManyRows
	}
	s.Rows = append(s.Rows, cols)
	s.Line = append(s.Line, line)
	return nil
}

// --- cell values --------------------------------------------------------------------------------------------

var (
	isoDate   = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})(?:[T ].*)?$`)
	slashDate = regexp.MustCompile(`^(\d{1,2})[/.-](\d{1,2})[/.-](\d{4})(?: .*)?$`)
)

// Date reads a calendar date cell: an Excel serial number (a date cell read raw), yyyy-MM-dd (with an optional time),
// or dd/MM/yyyy as Thai sheets write it (also with '.' or '-'). A Buddhist-era year (2400 and later) is converted
// (2567 -> 2024). The result is midnight UTC of that calendar day; ok is false for anything else, including a day
// that does not exist.
func Date(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		// Serial 1 is 1900-01-01, 2958465 is 9999-12-31 (the 1900 date system; the time of day is dropped).
		if n < 1 || n > 2958465 || math.IsNaN(n) {
			return time.Time{}, false
		}
		t, err := excelize.ExcelDateToTime(math.Floor(n), false)
		if err != nil {
			return time.Time{}, false
		}
		return day(t.Year(), int(t.Month()), t.Day())
	}
	if m := isoDate.FindStringSubmatch(v); m != nil {
		return ymd(m[1], m[2], m[3])
	}
	if m := slashDate.FindStringSubmatch(v); m != nil {
		return ymd(m[3], m[2], m[1])
	}
	return time.Time{}, false
}

func ymd(ys, ms, ds string) (time.Time, bool) {
	y, _ := strconv.Atoi(ys)
	m, _ := strconv.Atoi(ms)
	d, _ := strconv.Atoi(ds)
	if y >= 2400 {
		y -= 543
	}
	return day(y, m, d)
}

func day(y, m, d int) (time.Time, bool) {
	if y < 1 || y > 9999 || m < 1 || m > 12 || d < 1 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Day() != d { // 31 April rolls over: not a date
		return time.Time{}, false
	}
	return t, true
}

// Number reads a numeric cell: a raw number, or text with thousands separators ("25,000", "1,234.50"). ok is false
// for anything else.
func Number(v string) (float64, bool) {
	v = strings.ReplaceAll(strings.TrimSpace(v), ",", "")
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	return n, true
}
