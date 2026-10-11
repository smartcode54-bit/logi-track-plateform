package documents

import (
	"slices"
	"strings"
	"time"
)

// DetailRow is one row of the detail sheet (invoice_detail.xlsx; DetailRow of generateDetailExcelBuffer,
// web:lib/billingDocumentRender.ts:420-437): one per trip or standby, one per multi-drop trip (its stops folded).
type DetailRow struct {
	Date            time.Time // the billing axis date (AxisDate): the plan date of a plan-basis party; zero = none
	ActualPickupAt  time.Time // วันรับงานจริง (ADR 0028), the optional column
	JobNo           string    // เลขใบงาน; "Stand by" for standby
	Route           string    // origin CODE → destination NAME
	VehicleClass    string    // PICKUP prints 4WH
	Plate           string
	DriverName      string
	Sub             string // the carrier that ran the row, else the owner company's short name, else "-"
	Phone           string
	TruckCount      int     // 0 for standby
	DropFeeTHB      float64 // ค่าโยก: the multi-drop stops 2+; 0 prints blank
	TransportFeeTHB float64 // ค่าขนส่ง/เที่ยว: the row, or a multi-drop trip's stop 1
	TotalTHB        float64
	Round           string // R1.. of the period's rounds; "-" without one
	FuelBand        string // "37.01–38.00"; "-" without one
	Remark          string // "เสริม" for SUPPLEMENTARY
}

// DetailRows builds the detail sheet's rows (generateDetailExcelBuffer, web:lib/billingDocumentRender.ts:399-488): a
// multi-drop trip's stops fold into one row whose transport fee is stop 1 and whose ค่าโยก is the unrounded float sum
// of stops 2+ (§6.18 #5: the invoice groups every stop under ค่าโยก, the sheet splits them); rows sort by their axis
// date, stably, folded trips after the other rows of the same instant (they are appended after them, as in the
// browser). ownerShortName fills Sub for the own fleet's rows.
func DetailRows(rows []Row, ownerShortName string) []DetailRow {
	rounds := CollectRounds(rows)
	labels := make(map[string]string, len(rounds))
	for _, r := range rounds {
		labels[r.EffectiveFromDate] = r.Label
	}
	roundOf := func(r Row) string {
		if l, ok := labels[r.RoundEffectiveFromDate]; ok && r.RoundEffectiveFromDate != "" {
			return l
		}
		return "-"
	}
	owner := strings.TrimSpace(ownerShortName)
	sub := func(name string) string {
		if s := strings.TrimSpace(name); s != "" {
			return s
		}
		if owner != "" {
			return owner
		}
		return "-"
	}
	vehicle := func(vc *string) string {
		v := "-"
		if vc != nil {
			v = *vc
		}
		if v == "PICKUP" {
			return "4WH"
		}
		return v
	}
	orDash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	route := func(r Row) string {
		dest := "-"
		switch {
		case r.DestinationDisplayName != nil:
			dest = *r.DestinationDisplayName
		case r.LookupDestinationCode != nil:
			dest = *r.LookupDestinationCode
		}
		return firstNonEmpty(r.OriginHubCode, r.LookupHubCode, r.HubDisplayName, "-") + " → " + dest
	}
	remark := func(r Row) string {
		if r.JobCategory == "SUPPLEMENTARY" {
			return "เสริม"
		}
		return ""
	}
	jobNo := func(r Row) string {
		if r.TripNo != "" {
			return r.TripNo
		}
		return truncate(r.ID, 12)
	}
	date := func(r Row) time.Time {
		d, _ := AxisDate(r)
		return d
	}

	var out []DetailRow
	var groupOrder []string
	groups := map[string][]Row{}
	for _, r := range rows {
		if r.Type == RowMultidropStop {
			key := r.TripRecordID
			if key == "" {
				key = stripStop(r.ID)
			}
			if _, ok := groups[key]; !ok {
				groupOrder = append(groupOrder, key)
			}
			groups[key] = append(groups[key], r)
			continue
		}
		standby := r.Type == RowStandby
		d := DetailRow{
			Date: date(r), ActualPickupAt: r.ActualPickupAt, JobNo: jobNo(r), Route: route(r),
			VehicleClass: vehicle(r.VehicleClass), Plate: orDash(r.TruckLicensePlate), DriverName: orDash(r.DriverName),
			Sub: sub(r.SubcontractorName), Phone: orDash(r.DriverPhone), TruckCount: 1, TransportFeeTHB: r.EstimateTHB,
			TotalTHB: r.EstimateTHB, Round: roundOf(r),
			FuelBand: FormatFuelBand(r.FuelBandLowerTHB, r.FuelBandUpperTHB), Remark: remark(r),
		}
		if standby {
			d.JobNo, d.TruckCount = "Stand by", 0
		}
		out = append(out, d)
	}
	for _, key := range groupOrder {
		stops := slices.Clone(groups[key])
		slices.SortStableFunc(stops, func(a, b Row) int { return a.StopIndex - b.StopIndex })
		base := stops[0]
		drop := 0.0
		for _, s := range stops[1:] {
			drop += s.EstimateTHB
		}
		no := stripStop(base.TripNo)
		if no == "" {
			no = truncate(stripStop(base.ID), 12)
		}
		out = append(out, DetailRow{
			Date: date(base), ActualPickupAt: base.ActualPickupAt, JobNo: no, Route: route(base),
			VehicleClass: vehicle(base.VehicleClass), Plate: orDash(base.TruckLicensePlate),
			DriverName: orDash(base.DriverName), Sub: sub(base.SubcontractorName), Phone: orDash(base.DriverPhone),
			TruckCount: 1, DropFeeTHB: drop, TransportFeeTHB: base.EstimateTHB, TotalTHB: base.EstimateTHB + drop,
			Round: roundOf(base), FuelBand: FormatFuelBand(base.FuelBandLowerTHB, base.FuelBandUpperTHB),
			Remark: remark(base),
		})
	}
	slices.SortStableFunc(out, func(a, b DetailRow) int { return compareMillis(a.Date, b.Date) })
	return out
}

// compareMillis orders by the JavaScript getTime() of a date, an absent one counting as 0.
func compareMillis(a, b time.Time) int {
	ms := func(t time.Time) int64 {
		if t.IsZero() {
			return 0
		}
		return t.UnixMilli()
	}
	x, y := ms(a), ms(b)
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// stripStop drops a "-s{n}" / "_s{n}" stop suffix (s.replace(/[-_]s\d+$/, "")).
func stripStop(s string) string {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == len(s) || i < 2 || s[i-1] != 's' || (s[i-2] != '-' && s[i-2] != '_') {
		return s
	}
	return s[:i-2]
}

// truncate is String.prototype.slice(0, n) on the UTF-16 code units of an ASCII id (ids here are ASCII).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// DetailTotals are the footer of the detail sheet and the totals of the PDFs, taken from the statement (exact NUMERIC
// sums, R18) rather than re-added from the rows.
type DetailTotals struct {
	GrandTotal      float64
	WithholdingRate float64 // fraction
	WithholdingTax  float64
	Net             float64
}
