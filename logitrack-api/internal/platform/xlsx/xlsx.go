// Package xlsx reads spreadsheet uploads for the import endpoints (POST /v1/trucks/import first, T22; the hub, task
// and toll imports follow): the first worksheet of an XLSX workbook (xuri/excelize/v2, main spec §2.4) or a CSV file,
// as rows of trimmed cell text, plus the date and number forms such sheets carry. Cells are read raw: a date cell
// is its Excel serial number, never the text of the author's locale, and Date turns it into a calendar date.
//
// An upload is untrusted, and a workbook is a zip whose XML expands far beyond the bytes sent. Before excelize opens
// a workbook the reader bounds the declared uncompressed size of the archive (16 MiB by default), its number of
// entries, the XML elements of the parts excelize unmarshals whole (styles, relationships, content types, the shared
// strings, which it reads in full before the first row) and the number of shared strings; while it reads it bounds the
// data rows, the spreadsheet lines (blank rows included) and the columns of a row (a cell in column XFD would
// otherwise pad every row to 16384 cells). At most MaxConcurrentReads reads run in the process at once; another is
// ErrBusy at once (the caller answers 429). A file that is neither a workbook nor CSV is ErrNotSpreadsheet.
package xlsx

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
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

// Errors of Read; the caller answers 422 with the field that carried the file (429 for ErrBusy).
var (
	// ErrNotSpreadsheet: the bytes are neither an XLSX workbook nor UTF-8 CSV, the workbook is unreadable, or it
	// expands past the bounds (uncompressed size, entries, XML elements, shared strings).
	ErrNotSpreadsheet = errors.New("xlsx: not a readable XLSX workbook or CSV file")
	// ErrTooManyRows: the sheet has more data rows than Options.MaxRows, or more lines than MaxLines.
	ErrTooManyRows = errors.New("xlsx: too many rows")
	// ErrTooManyColumns: a row of the sheet reaches past Options.MaxCols (counted before trimming, blank cells too).
	ErrTooManyColumns = errors.New("xlsx: too many columns")
	// ErrBusy: MaxConcurrentReads reads are already running in the process.
	ErrBusy = errors.New("xlsx: too many spreadsheet reads in progress")
)

// MaxConcurrentReads bounds the reads running in one process at once (every import shares it).
const MaxConcurrentReads = 2

// readSlots is the process-wide semaphore of Read.
var readSlots = make(chan struct{}, MaxConcurrentReads)

// maxZipEntries bounds the entries of a workbook archive (a workbook has a few dozen parts).
const maxZipEntries = 1000

// sharedStringsPath is the shared-strings part excelize reads (case-insensitively, a backslash read as '/').
const sharedStringsPath = "xl/sharedstrings.xml"

// Options bound a read.
type Options struct {
	// MaxRows is the largest number of data rows (after the header row) accepted; 0 means 10000.
	MaxRows int
	// MaxCols is the widest row accepted, in columns up to the last non-blank cell (or formula); 0 means 256.
	MaxCols int
	// MaxUnzipBytes bounds the declared uncompressed size of the whole workbook archive; 0 means 16 MiB.
	MaxUnzipBytes int64
	// MaxSharedStrings bounds the workbook's shared-strings table; 0 means (MaxRows+1) x 64.
	MaxSharedStrings int
}

func (o Options) maxRows() int {
	if o.MaxRows > 0 {
		return o.MaxRows
	}
	return 10000
}

func (o Options) maxCols() int {
	if o.MaxCols > 0 {
		return o.MaxCols
	}
	return 256
}

func (o Options) maxUnzip() int64 {
	if o.MaxUnzipBytes > 0 {
		return o.MaxUnzipBytes
	}
	return 16 << 20
}

func (o Options) maxSharedStrings() int {
	if o.MaxSharedStrings > 0 {
		return o.MaxSharedStrings
	}
	return (o.maxRows() + 1) * 64
}

// MaxLines is the number of spreadsheet lines (blank ones included) a read walks through before it gives up with
// ErrTooManyRows: four lines per accepted data row, at least 50000.
func (o Options) MaxLines() int {
	return max(4*o.maxRows(), 50000)
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

// Read reads the first worksheet of an XLSX workbook, or a CSV file when the bytes are not a zip archive. It is
// ErrBusy without reading when MaxConcurrentReads reads are already running.
func Read(r io.Reader, o Options) (*Sheet, error) {
	select {
	case readSlots <- struct{}{}:
		defer func() { <-readSlots }()
	default:
		return nil, ErrBusy
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(b, xlsxMagic) {
		if err := checkArchive(b, o); err != nil {
			return nil, err
		}
		return readXLSX(b, o)
	}
	return readCSV(b, o)
}

// checkArchive bounds a workbook before excelize opens it. excelize reads every part into memory, unmarshals the
// workbook, its relationships, content types and styles when it opens the file, and the whole shared-strings table
// on the first row: a zip of a few dozen kilobytes (millions of tiny shared strings or styles) would expand to
// gigabytes of heap. The declared sizes are enforced by archive/zip while reading (a part that inflates past its
// declared size is an error), so their sum bounds what excelize reads; the XML parts it unmarshals whole (every part
// but the worksheets, which it streams row by row) are then tokenised in constant memory and their elements counted
// against a budget, the shared strings against their own bound.
func checkArchive(b []byte, o Options) error {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotSpreadsheet, err)
	}
	if len(zr.File) > maxZipEntries {
		return fmt.Errorf("%w: %d archive entries (at most %d)", ErrNotSpreadsheet, len(zr.File), maxZipEntries)
	}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if total > uint64(o.maxUnzip()) {
			return fmt.Errorf("%w: expands past %d bytes", ErrNotSpreadsheet, o.maxUnzip())
		}
	}
	c := counter{parts: maxPartElements, sharedStrings: 3*o.maxSharedStrings() + 64, strings: o.maxSharedStrings()}
	for _, f := range zr.File {
		name := strings.ToLower(strings.ReplaceAll(f.Name, "\\", "/"))
		xmlPart := strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels")
		if !xmlPart || strings.HasPrefix(name, "xl/worksheets/") { // excelize streams the worksheet it reads
			continue
		}
		if err := c.count(f, name == sharedStringsPath); err != nil {
			return err
		}
	}
	return nil
}

// maxPartElements is the element budget of the XML parts other than the worksheets and the shared strings (a
// workbook's styles, theme, relationships and content types hold a few thousand). The shared strings may hold three
// elements per string of their bound (si, t, and room for rich-text runs) plus the root. At the default bounds of a
// 2000-row import a workbook that spends every budget peaks below 100 MiB of heap.
const maxPartElements = 50000

// counter is what is left of the element budgets and of the shared strings.
type counter struct{ parts, sharedStrings, strings int }

// count tokenises one zip part and spends its start elements (and, for the shared strings, its si elements); past
// a budget it stops with ErrNotSpreadsheet.
func (c *counter) count(f *zip.File, sharedStrings bool) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotSpreadsheet, err)
	}
	defer func() { _ = rc.Close() }()
	budget := &c.parts
	if sharedStrings {
		budget = &c.sharedStrings
	}
	d := xml.NewDecoder(rc)
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNotSpreadsheet, err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if *budget--; *budget < 0 {
			return fmt.Errorf("%w: too many XML elements in %s", ErrNotSpreadsheet, f.Name)
		}
		if sharedStrings && se.Name.Local == "si" {
			if c.strings--; c.strings < 0 {
				return fmt.Errorf("%w: too many shared strings", ErrNotSpreadsheet)
			}
		}
	}
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
		line++ // excelize also yields the rows a sheet skips, so line is the spreadsheet row number
		if line > o.MaxLines() {
			return nil, ErrTooManyRows
		}
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
	cr.ReuseRecord = true // add copies the cells it keeps
	s := &Sheet{}
	for records := 1; ; records++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotSpreadsheet, err)
		}
		if records > o.MaxLines() {
			return nil, ErrTooManyRows
		}
		line, _ := cr.FieldPos(0)
		if err := s.add(rec, line, o); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// add keeps a row: its width is checked on the raw cells first (excelize pads a row with blank cells up to its last
// value or formula), so a wide row fails before it is trimmed, skipped as blank or counted.
func (s *Sheet) add(cols []string, line int, o Options) error {
	if len(cols) > o.maxCols() {
		return ErrTooManyColumns
	}
	blank := true
	for _, c := range cols {
		if strings.TrimSpace(c) != "" {
			blank = false
			break
		}
	}
	if blank {
		return nil
	}
	row := make([]string, len(cols))
	for i, c := range cols {
		row[i] = strings.TrimSpace(c)
	}
	if s.Header == nil {
		s.Header = row
		return nil
	}
	if len(s.Rows) >= o.maxRows() {
		return ErrTooManyRows
	}
	s.Rows = append(s.Rows, row)
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
// (2567 -> 2024) in all three forms, the serial of a date cell included. The result is midnight UTC of that
// calendar day; ok is false for anything else, including a day that does not exist.
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
		// A Buddhist-era date typed into a date cell (31/03/2567 on a Gregorian locale) is stored as that year.
		return day(gregorian(t.Year()), int(t.Month()), t.Day())
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
	return day(gregorian(y), m, d)
}

// gregorian converts a Buddhist-era year (2400 and later; Gregorian dates of trucks never reach it) to the Gregorian
// year: 2567 -> 2024. The day is checked after the conversion, so 29 February of a Buddhist year whose Gregorian
// year is not a leap year is not a date.
func gregorian(y int) int {
	if y >= 2400 {
		return y - 543
	}
	return y
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
