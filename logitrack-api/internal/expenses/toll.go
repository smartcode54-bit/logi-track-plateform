package expenses

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/expenses/expensesdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/xlsx"
)

// The toll import (TollExpenseImportDialog.tsx + batchCreateTollExpenseImports, expenses.ts:222-260) reads a toll
// card's transaction history (Easy Pass / M-Flow exports: ลำดับ | สถานที่ | เครื่อง/เลน | วันที่เกิดรายการ | ประเภท |
// จำนวนเงิน), keeps the toll rows and records each one as a pending other expense (category toll) of one driver and
// one truck. The column detection, the toll-type test and the date rules are the dialog's, run on the server.

// MaxTollRows bounds the rows of one toll import.
const MaxTollRows = 5000

// TollRow is one toll transaction: Row is the spreadsheet row (header = its own row) or the 1-based index of rows[].
type TollRow struct {
	Row        int
	Sequence   *int64
	Location   string
	Lane       string
	SourceType string
	At         time.Time
	AmountThb  float64
	// Skip is the reason the row is not imported ("" = imported): not_toll, invalid_date, invalid_amount.
	Skip string
}

// Reasons a toll row is skipped.
const (
	SkipNotToll         = "not_toll"
	SkipInvalidDate     = "invalid_date"
	SkipInvalidAmount   = "invalid_amount"
	SkipDuplicate       = "duplicate"
	SkipDuplicateInFile = "duplicate_in_file"
)

// ErrNoTollColumns is a sheet whose date, type and amount columns could not be found (422 file missing_column).
var ErrNoTollColumns = errors.New("expenses: the sheet has no date, type and amount columns")

var (
	tollSpaces  = regexp.MustCompile(`[\x{2000}-\x{200B}\x{202F}\x{205F}\x{3000}]`)
	tollRuns    = regexp.MustCompile(`\s+`)
	tollDateRx  = regexp.MustCompile(`^(\d{1,2})[/.-](\d{1,2})[/.-](\d{2,4})(?:\s+(\d{1,2})(?::(\d{1,2}))?(?::(\d{1,2}))?)?`)
	tollISODate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
)

// normalizeHeader is normalizeHeaderCell: BOM and odd spaces removed, full-width parentheses folded, lower case,
// runs of white space collapsed.
func normalizeHeader(h string) string {
	h = strings.TrimPrefix(h, "\uFEFF")
	h = strings.ReplaceAll(h, "\u00A0", " ")
	h = tollSpaces.ReplaceAllString(h, " ")
	h = strings.NewReplacer("\uFF08", "(", "\uFF09", ")").Replace(h)
	return strings.TrimSpace(tollRuns.ReplaceAllString(strings.ToLower(h), " "))
}

// headerMatches is the dialog's headerMatches: the keyword is contained in the header, or (two characters and more)
// contained once both lose their spaces.
func headerMatches(header, keyword string) bool {
	k := normalizeHeader(keyword)
	if k == "" {
		return false
	}
	if strings.Contains(header, k) {
		return true
	}
	if len([]rune(k)) < 2 {
		return false
	}
	hc, kc := strings.ReplaceAll(header, " ", ""), strings.ReplaceAll(k, " ", "")
	return len([]rune(kc)) >= 2 && strings.Contains(hc, kc)
}

var (
	seqKeywords    = []string{"ลำดับ", "seq", "no.", "no", "#", "order", "index"}
	locKeywords    = []string{"สถานที่", "location", "place", "station", "จุด", "ทางเข้า", "ด่าน"}
	laneKeywords   = []string{"เครื่อง", "เลน", "lane", "machine", "channel", "ช่อง"}
	dateKeywords   = []string{"วันที่", "date", "วัน", "วันเวลา", "datetime", "date time", "transaction date", "วันที่ทำรายการ", "เวลาทำรายการ"}
	typeKeywords   = []string{"ประเภท", "type", "category", "ชนิด", "transaction type", "ประเภทรายการ"}
	amountKeywords = []string{"จำนวนเงิน", "amount", "บาท", "baht", "total", "ค่าธรรมเนียม", "ค่าผ่านทาง", "ยอดเงิน", "ค่าบริการ"}
)

func findColumn(headers []string, keywords []string) int {
	for i, h := range headers {
		for _, k := range keywords {
			if headerMatches(h, k) {
				return i
			}
		}
	}
	return -1
}

// IsTollType is isTollPassType: the type cell names a toll / pass-through payment (ผ่านทาง, ทางด่วน, expressway, easy
// pass, mflow, toll).
func IsTollType(cell string) bool {
	raw := strings.TrimSpace(tollSpaces.ReplaceAllString(strings.ReplaceAll(cell, "\u00A0", " "), " "))
	if raw == "" {
		return false
	}
	if strings.Contains(raw, "ผ่านทาง") || strings.Contains(raw, "ทางด่วน") {
		return true
	}
	lower := strings.ToLower(raw)
	for _, c := range []string{"expressway", "easy pass", "mflow", "easy-pass"} {
		if strings.Contains(lower, c) {
			return true
		}
	}
	return lower == "toll" || strings.HasSuffix(lower, " toll") || strings.HasPrefix(lower, "toll ")
}

// ParseTollTime is parseTollDate on the Bangkok wall clock (the dialog ran in the office's browser): an Excel serial
// with its time fraction (a date cell read raw), milliseconds since the epoch, d/m/y with an optional H:m:s (a first
// part above 12 is the day, else a second part above 12 is the day, else d/m; a two-digit year is 20yy; a Buddhist-era
// year, 2400 and later, is converted), or an ISO date-time (Bangkok when it carries no zone).
func ParseTollTime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
		switch {
		case n > 20000 && n < 100000: // an Excel serial: days since 1899-12-30, the fraction is the time of day
			day := math.Floor(n)
			secs := math.Round((n - day) * 86400)
			base := time.Date(1899, 12, 30, 0, 0, 0, 0, clock.Bangkok)
			return base.AddDate(0, 0, int(day)).Add(time.Duration(secs) * time.Second), true
		case n > 1e12:
			return time.UnixMilli(int64(n)), true
		}
		return time.Time{}, false
	}
	if m := tollDateRx.FindStringSubmatch(v); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if len(m[3]) == 2 {
			y += 2000
		}
		if y >= 2400 {
			y -= 543
		}
		num := func(s string) int { n, _ := strconv.Atoi(s); return n }
		hh, mi, ss := num(m[4]), num(m[5]), num(m[6])
		day, month := a, b
		if a <= 12 && b > 12 {
			day, month = b, a
		}
		if month < 1 || month > 12 || day < 1 || day > 31 || hh > 23 || mi > 59 || ss > 59 {
			return time.Time{}, false
		}
		t := time.Date(y, time.Month(month), day, hh, mi, ss, 0, clock.Bangkok)
		if t.Day() != day {
			return time.Time{}, false
		}
		return t, true
	}
	if tollISODate.MatchString(v) {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04",
			"2006-01-02 15:04", time.DateOnly} {
			if layout == time.RFC3339Nano {
				if t, err := time.Parse(layout, v); err == nil {
					return t, true
				}
				continue
			}
			if t, err := time.ParseInLocation(layout, v, clock.Bangkok); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// TollRowsFromSheet finds the columns of a toll card history (the dialog's three passes: a header row among the first
// 20 that names date, type and amount in three different columns; the card-history layout ลำดับ | สถานที่ | … |
// จำนวนเงิน; columns inferred from the cells, with the six-column layout as the last resort) and reads its rows.
// Rows whose type is not a toll, or whose date or amount does not parse, are kept with their Skip reason; a missing
// sequence is the row's position after the header (the dialog's ri + 1).
func TollRowsFromSheet(s *xlsx.Sheet) ([]TollRow, error) {
	all := append([][]string{s.Header}, s.Rows...)
	line := func(i int) int { // the spreadsheet row of all[i]
		if i == 0 {
			if len(s.Line) > 0 {
				return s.Line[0] - 1
			}
			return 1
		}
		return s.Line[i-1]
	}
	norm := func(i int) []string {
		out := make([]string, len(all[i]))
		for j, c := range all[i] {
			out[j] = normalizeHeader(c)
		}
		return out
	}
	header, colDate, colType, colAmt, colSeq, colLoc, colLane := 0, -1, -1, -1, -1, -1, -1
	probe := min(20, len(all))
	for i := range probe {
		h := norm(i)
		d, ty, am := findColumn(h, dateKeywords), findColumn(h, typeKeywords), findColumn(h, amountKeywords)
		if d == -1 || ty == -1 || am == -1 || d == ty || ty == am || d == am {
			continue
		}
		header, colDate, colType, colAmt = i, d, ty, am
		colSeq, colLoc, colLane = findColumn(h, seqKeywords), findColumn(h, locKeywords), findColumn(h, laneKeywords)
		break
	}
	if colDate == -1 || colType == -1 || colAmt == -1 {
		for i := range probe {
			h := norm(i)
			at := func(j int) string {
				if j < len(h) {
					return h[j]
				}
				return ""
			}
			if len(h) >= 6 && headerMatches(at(0), "ลำดับ") &&
				(headerMatches(at(1), "สถานที่") || headerMatches(at(1), "location")) &&
				(headerMatches(at(3), "วันที่เกิดรายการ") || headerMatches(at(3), "date")) &&
				(headerMatches(at(4), "ประเภท") || headerMatches(at(4), "type")) &&
				(headerMatches(at(5), "จำนวนเงิน") || headerMatches(at(5), "amount")) {
				header, colSeq, colLoc, colLane, colDate, colType, colAmt = i, 0, 1, 2, 3, 4, 5
				break
			}
		}
	}
	if colDate == -1 || colType == -1 || colAmt == -1 {
		sample := all[header:min(len(all), 120)]
		width := 0
		for _, r := range sample {
			width = max(width, len(r))
		}
		bestDate, bestAmt, bestType, scoreDate, scoreAmt, scoreType := -1, -1, -1, 0, 0, 0
		for c := range width {
			d, a, t := 0, 0, 0
			for _, r := range sample {
				cell := xlsx.Value(r, c)
				if _, ok := ParseTollTime(cell); ok {
					d++
				}
				if n, ok := xlsx.Number(cell); ok && n >= 0 {
					a++
				}
				if IsTollType(cell) {
					t++
				}
			}
			if d > scoreDate {
				bestDate, scoreDate = c, d
			}
			if a > scoreAmt {
				bestAmt, scoreAmt = c, a
			}
			if t > scoreType {
				bestType, scoreType = c, t
			}
		}
		if colDate == -1 && scoreDate >= 2 {
			colDate = bestDate
		}
		if colAmt == -1 && scoreAmt >= 2 {
			colAmt = bestAmt
		}
		if colType == -1 && scoreType >= 1 {
			colType = bestType
		}
		if (colDate == -1 || colType == -1 || colAmt == -1) && width >= 6 {
			if colSeq == -1 {
				colSeq = 0
			}
			if colLoc == -1 {
				colLoc = 1
			}
			if colLane == -1 {
				colLane = 2
			}
			if colDate == -1 {
				colDate = 3
			}
			if colType == -1 {
				colType = 4
			}
			if colAmt == -1 {
				colAmt = 5
			}
		}
	}
	if colDate == -1 || colType == -1 || colAmt == -1 {
		return nil, ErrNoTollColumns
	}
	var out []TollRow
	for i := header + 1; i < len(all); i++ {
		r := all[i]
		get := func(c int) string { return xlsx.Value(r, c) }
		if get(colType) == "" && get(colAmt) == "" && get(colDate) == "" {
			continue
		}
		row := TollRow{Row: line(i), Location: get(colLoc), Lane: get(colLane), SourceType: get(colType)}
		if v := get(colSeq); v != "" {
			if n, ok := xlsx.Number(v); ok && n >= 0 && n <= maxSequence {
				seq := int64(math.Floor(n))
				row.Sequence = &seq
			}
		}
		if row.Sequence == nil {
			seq := int64(i - header)
			row.Sequence = &seq
		}
		switch {
		case !IsTollType(row.SourceType):
			row.Skip = SkipNotToll
		default:
			t, ok := ParseTollTime(get(colDate))
			amount, okAmt := xlsx.Number(get(colAmt))
			switch {
			case !ok:
				row.Skip = SkipInvalidDate
			case !okAmt || amount < 0 || amount > maxAmountThb:
				row.Skip = SkipInvalidAmount
			default:
				row.At, row.AmountThb = t, math.Round(amount*100)/100
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// TollRowInput is one row of the JSON form of POST /v1/expenses/toll-import: a toll transaction the client parsed.
type TollRowInput struct {
	Sequence   *int64   `json:"sequence"`
	Location   string   `json:"location"`
	Lane       string   `json:"lane"`
	SourceType string   `json:"sourceType"`
	Date       string   `json:"date"`
	AmountThb  *float64 `json:"amountThb"`
}

// TollRowsFromJSON reads the JSON rows (Row = the 1-based index; a missing sequence is the index).
func TollRowsFromJSON(rows []TollRowInput) []TollRow {
	out := make([]TollRow, 0, len(rows))
	for i, r := range rows {
		row := TollRow{Row: i + 1, Sequence: r.Sequence, Location: strings.TrimSpace(r.Location),
			Lane: strings.TrimSpace(r.Lane), SourceType: strings.TrimSpace(r.SourceType)}
		if row.Sequence == nil || *row.Sequence < 0 || *row.Sequence > maxSequence {
			seq := int64(i + 1)
			row.Sequence = &seq
		}
		t, ok := ParseTollTime(r.Date)
		switch {
		case !ok:
			row.Skip = SkipInvalidDate
		case r.AmountThb == nil || math.IsNaN(*r.AmountThb) || *r.AmountThb < 0 || *r.AmountThb > maxAmountThb:
			row.Skip = SkipInvalidAmount
		default:
			row.At, row.AmountThb = t, math.Round(*r.AmountThb*100)/100
		}
		out = append(out, row)
	}
	return out
}

// tollDescription is tollImportDescription: the parts type · place · lane, else "Toll".
func tollDescription(r TollRow) string {
	var parts []string
	for _, p := range []string{r.SourceType, r.Location, r.Lane} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "Toll"
	}
	return strings.Join(parts, " · ")
}

// TollImportInput is a toll import: the driver and truck every row belongs to, and the rows.
type TollImportInput struct {
	DriverID, TruckID string
	DryRun            bool
	Rows              []TollRow
}

// SkippedRow is a row the import did not record.
type SkippedRow struct {
	Row    int    `json:"row"`
	Reason string `json:"reason"`
}

// TollImportResult is the answer of a toll import.
type TollImportResult struct {
	Created     int          `json:"created"`
	Skipped     int          `json:"skipped"`
	SkippedRows []SkippedRow `json:"skippedRows"`
	DryRun      bool         `json:"dryRun"`
}

// ImportTolls is POST /v1/expenses/toll-import (accounting:edit_other): every toll row becomes a pending other
// expense (category toll) of the driver and the truck, both of the caller's reach (stamped with the driver's tenant,
// the truck's plate snapshotted), with the row's sequence, place, lane and source type, in one transaction. Skipped,
// with their reason: rows that are not tolls or whose date or amount does not parse, a row the file repeats
// (duplicate_in_file) and a transaction the truck already holds (duplicate: same instant, amount, place and lane, so a
// re-imported history adds nothing; the imports of one truck hold a transaction-scoped advisory lock, so two
// overlapping imports of one history add it once). dryRun answers the counts and writes nothing. One expense.created
// event (payload count) tells the pages to refetch.
func (s *Service) ImportTolls(ctx context.Context, p *authz.Principal, rid string, in TollImportInput) (*TollImportResult, error) {
	if err := writeTenant(p); err != nil {
		return nil, err
	}
	var ck checker
	parse := func(field, v string) uuid.UUID {
		id, err := uuid.Parse(strings.TrimSpace(v))
		switch {
		case strings.TrimSpace(v) == "":
			ck.add(field, "required")
		case err != nil:
			ck.add(field, "invalid")
		}
		return id
	}
	driverID, truckID := parse("driverId", in.DriverID), parse("truckId", in.TruckID)
	if len(in.Rows) > MaxTollRows {
		ck.addParams("rows", "too_many", map[string]any{"max": MaxTollRows})
	}
	if err := ck.err(); err != nil {
		return nil, err
	}
	res := &TollImportResult{SkippedRows: []SkippedRow{}, DryRun: in.DryRun}
	errDryRun := errors.New("dry run")
	err := s.tx(ctx, p, func(tx pgx.Tx, q *expensesdb.Queries) error {
		all, tenants := p.TenantReach()
		d, err := q.GetDriver(ctx, expensesdb.GetDriverParams{ID: driverID, AllTenants: all, TenantIds: tenants})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrInvalidArgument(violation("driverId", "not_found"))
		}
		if err != nil {
			return err
		}
		t, err := truckRef(ctx, q, p, truckID)
		if err != nil {
			return err
		}
		// Imports of one truck run one at a time, so a history two overlapping requests send is added once.
		if err := q.LockTollImport(ctx, truckID); err != nil {
			return err
		}
		seen := map[string]bool{}
		var first *expensesdb.VehicleExpense
		category, status := "toll", StatusPending
		for _, r := range in.Rows {
			skip := r.Skip
			if skip == "" {
				key := r.At.UTC().Format(time.RFC3339Nano) + "\x00" + strconv.FormatFloat(r.AmountThb, 'f', 2, 64) + "\x00" +
					r.Location + "\x00" + r.Lane
				switch {
				case seen[key]:
					skip = SkipDuplicateInFile
				default:
					seen[key] = true
					taken, err := q.TollRowExists(ctx, expensesdb.TollRowExistsParams{TruckID: &truckID, ExpenseAt: r.At,
						AmountThb: r.AmountThb, TollLocation: r.Location, TollLane: r.Lane})
					if err != nil {
						return err
					}
					if taken {
						skip = SkipDuplicate
					}
				}
			}
			if skip != "" {
				res.Skipped++
				res.SkippedRows = append(res.SkippedRows, SkippedRow{Row: r.Row, Reason: skip})
				continue
			}
			res.Created++
			if in.DryRun {
				continue
			}
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			seq := int32(*r.Sequence)
			desc := tollDescription(r)
			opt := func(v string) *string {
				if v = strings.TrimSpace(v); v == "" {
					return nil
				}
				return &v
			}
			plate := t.LicensePlate
			row, err := q.InsertExpense(ctx, expensesdb.InsertExpenseParams{ID: id, TenantID: d.TenantID,
				TenantSource: "driver", DriverID: &driverID, TruckID: &truckID, PlateSnapshot: &plate, ExpenseType: TypeOther,
				Category: &category, ExpenseAt: r.At, AmountThb: r.AmountThb, Status: status, Description: &desc,
				TollImportSequence: &seq, TollLocation: opt(r.Location), TollLane: opt(r.Lane),
				TollSourceType: opt(r.SourceType), Actor: actor(p)})
			if err != nil {
				return err
			}
			if first == nil {
				first = &row
			}
		}
		if in.DryRun {
			return errDryRun
		}
		if first != nil {
			return emit(ctx, tx, RouteCreated, *first, map[string]any{"count": res.Created, "source": "toll_import"}, rid)
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDryRun) {
		return nil, err
	}
	return res, nil
}
