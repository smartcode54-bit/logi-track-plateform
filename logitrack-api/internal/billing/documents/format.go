package documents

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/jsmath"
)

// ThaiMonths are the Thai month names the documents print (THAI_MONTHS, web:lib/billingDocumentRender.ts:362).
var ThaiMonths = [12]string{
	"มกราคม", "กุมภาพันธ์", "มีนาคม", "เมษายน", "พฤษภาคม", "มิถุนายน",
	"กรกฎาคม", "สิงหาคม", "กันยายน", "ตุลาคม", "พฤศจิกายน", "ธันวาคม",
}

// ThaiMonth is the Thai name of month 1-12 ("" outside that range).
func ThaiMonth(month int) string {
	if month < 1 || month > 12 {
		return ""
	}
	return ThaiMonths[month-1]
}

// PeriodLabel is the invoice's "ประจำเดือน" text: the Thai month and the Gregorian year (periodLabel,
// web:lib/billingDocumentRender.ts:46-52; the PDF keeps the Gregorian year while the lines and the Excel sheet print the
// Buddhist one, §6.18 #13, preserved).
func PeriodLabel(year, month int) string {
	return ThaiMonth(month) + " " + strconv.Itoa(year)
}

// BuddhistYear is the Thai Buddhist year of a Gregorian one (+543).
func BuddhistYear(year int) int { return year + 543 }

// FormatTHB is new Intl.NumberFormat("th-TH", {minimumFractionDigits: 2, maximumFractionDigits: 2}).format(x)
// (formatThb, web:lib/billingDocumentRender.ts:38-43): Latin digits, "," every three integer digits, "." and two
// decimals. ICU rounds the shortest decimal that round-trips the double half away from zero ("half expand"), so
// 1.005 prints 1.01 where toFixed(2) prints 1.00 (main spec §6.2 "Display"; pinned by vectors V8 produced). A
// negative value, -0 included, keeps its sign ("-0.00"); NaN and the infinities print as ICU does.
func FormatTHB(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "∞"
	case math.IsInf(x, -1):
		return "-∞"
	}
	sign := ""
	if math.Signbit(x) {
		sign = "-"
	}
	digits := strconv.FormatFloat(math.Abs(x), 'f', -1, 64) // the shortest round-trip decimal, no exponent
	intPart, frac, _ := strings.Cut(digits, ".")
	for len(frac) < 3 {
		frac += "0"
	}
	kept := []byte(intPart + frac[:2])
	if frac[2] >= '5' { // half away from zero on the decimal digits
		i := len(kept) - 1
		for ; i >= 0; i-- {
			if kept[i] == '9' {
				kept[i] = '0'
				continue
			}
			kept[i]++
			break
		}
		if i < 0 {
			kept = append([]byte{'1'}, kept...)
		}
	}
	n := len(kept) - 2
	return sign + group(string(kept[:n])) + "." + string(kept[n:])
}

// group inserts "," every three digits from the right.
func group(intDigits string) string {
	if len(intDigits) <= 3 {
		return intDigits
	}
	var b strings.Builder
	head := len(intDigits) % 3
	if head > 0 {
		b.WriteString(intDigits[:head])
	}
	for i := head; i < len(intDigits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(intDigits[i : i+3])
	}
	return b.String()
}

// DateDMY is date-fns format(d, "dd/MM/yyyy") on the Bangkok calendar ("" for the zero time).
func DateDMY(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(clock.Bangkok).Format("02/01/2006")
}

// DateTimeDMYHM is date-fns format(d, "dd/MM/yyyy HH:mm") on the Bangkok calendar ("" for the zero time).
func DateTimeDMYHM(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(clock.Bangkok).Format("02/01/2006 15:04")
}

// FormatLineItemDates is the date label of an invoice line in the Buddhist year (formatLineItemDates,
// web:lib/billingDocumentModel.ts:202-224), on the Bangkok calendar: a range for trips and ค่าโยก ("5/5/2569",
// "2-31/5/2569"), each day for standby ("14,17,20/5/2569"), both ends spelled out across a month boundary; "-" with no
// date. The month and year come from the latest date.
func FormatLineItemDates(dates []time.Time, enumerateDays bool) string {
	var valid []time.Time
	for _, d := range dates {
		if !d.IsZero() {
			valid = append(valid, d.In(clock.Bangkok))
		}
	}
	if len(valid) == 0 {
		return "-"
	}
	slices.SortStableFunc(valid, func(a, b time.Time) int { return a.Compare(b) })
	ref := valid[len(valid)-1]
	monthYear := strconv.Itoa(int(ref.Month())) + "/" + strconv.Itoa(BuddhistYear(ref.Year()))
	if enumerateDays {
		var days []int
		for _, d := range valid {
			if !slices.Contains(days, d.Day()) {
				days = append(days, d.Day())
			}
		}
		slices.Sort(days)
		parts := make([]string, len(days))
		for i, d := range days {
			parts[i] = strconv.Itoa(d)
		}
		return strings.Join(parts, ",") + "/" + monthYear
	}
	lo, hi := valid[0], valid[len(valid)-1]
	if lo.Year() == hi.Year() && lo.Month() == hi.Month() {
		if lo.Day() == hi.Day() {
			return strconv.Itoa(lo.Day()) + "/" + monthYear
		}
		return strconv.Itoa(lo.Day()) + "-" + strconv.Itoa(hi.Day()) + "/" + monthYear
	}
	full := func(d time.Time) string {
		return strconv.Itoa(d.Day()) + "/" + strconv.Itoa(int(d.Month())) + "/" + strconv.Itoa(BuddhistYear(d.Year()))
	}
	return full(lo) + "-" + full(hi)
}

// WithholdingPercent is the percent a stored withholding fraction prints as (0.0300 -> "3", 0.0150 -> "1.5"): the
// statement's own rate (R18), where the legacy PDF printed a fixed "1%" and the Excel sheet rounded to a whole percent
// (§6.18 #6, fixed).
func WithholdingPercent(rate float64) string {
	return jsmath.NumberToString(jsmath.Round2(float64(rate * 100)))
}

// WithholdingLabel is the withholding line of the invoice, the receipt and the detail sheet.
func WithholdingLabel(rate float64) string {
	return "ภาษีหัก ณ ที่จ่าย " + WithholdingPercent(rate) + "%"
}

// BranchLabel is the Thai label of a customer branch type (customers.branch_type: hq -> สำนักงานใหญ่, branch -> สาขา;
// the legacy documents printed the stored Thai value).
func BranchLabel(branchType string) string {
	switch branchType {
	case "hq", "headquarters":
		return "สำนักงานใหญ่"
	case "branch":
		return "สาขา"
	}
	return branchType
}
