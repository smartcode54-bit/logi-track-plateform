package render

import (
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/documents"
)

// DetailInput is everything the detail sheet needs.
type DetailInput struct {
	Rows             []documents.Row
	Year, Month      int
	CustomerName     string
	ProviderName     string
	OwnerShortName   string // Sub of the own fleet's rows (companies.short_name)
	Totals           documents.DetailTotals
	ShowActualPickup bool // the optional วันรับงานจริง column (ADR 0028)
}

// DetailColumn is one column of the detail sheet: its header and width in characters.
type DetailColumn struct {
	Header string
	Width  float64
}

// DetailColumns are the columns of the sheet (COLS, web:lib/billingDocumentRender.ts:494-515).
func DetailColumns(planBasis, showActualPickup bool) []DetailColumn {
	date := DetailColumn{"วันที่", 12}
	if planBasis {
		date = DetailColumn{"วันที่ตามแผน", 14}
	}
	cols := []DetailColumn{{"No.", 5}, date}
	if showActualPickup {
		cols = append(cols, DetailColumn{"วันรับงานจริง", 16})
	}
	return append(cols,
		DetailColumn{"เลขใบงาน", 18}, DetailColumn{"เส้นทาง", 30}, DetailColumn{"ประเภท", 9}, DetailColumn{"ทะเบียน", 11},
		DetailColumn{"ชื่อคนขับรถ", 18}, DetailColumn{"Sub", 10}, DetailColumn{"เบอร์รถ", 13}, DetailColumn{"จำนวนรถ", 9},
		DetailColumn{"ค่าโยก", 11}, DetailColumn{"ค่าขนส่ง/เที่ยว", 14}, DetailColumn{"ยอดรวม", 14}, DetailColumn{"รอบ", 7},
		DetailColumn{"ช่วงราคาน้ำมัน", 16}, DetailColumn{"หมายเหตุ", 14})
}

// DetailSheetName is the sheet's name: "รายละเอียดขนส่ง {month} {BE year}", cut to 31 UTF-16 code units (Excel's
// limit; String.prototype.slice(0, 31)). Thai is in the BMP, so a code unit is a rune.
func DetailSheetName(year, month int) string {
	name := "รายละเอียดขนส่ง " + documents.ThaiMonth(month) + " " + strconv.Itoa(documents.BuddhistYear(year))
	if utf8.RuneCountInString(name) > 31 {
		name = string([]rune(name)[:31])
	}
	return name
}

// DetailXLSX draws invoice_detail.xlsx: port of generateDetailExcelBuffer (web:lib/billingDocumentRender.ts:374-627)
// with excelize: title rows merged across the sheet, the styled header, one row per trip or standby (a multi-drop trip
// folded, documents.DetailRows), zebra rows, "#,##0.00" amounts, and the footer under ยอดรวม: the statement's total,
// its withholding (label from the stored rate, R18) and the net. A4 landscape, fit to one page wide. Dates are on the
// Bangkok calendar.
func DetailXLSX(in DetailInput) ([]byte, error) {
	planBasis := documents.BasisOf(in.Rows) == documents.BasisPlan
	cols := DetailColumns(planBasis, in.ShowActualPickup)
	nc := len(cols)
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := DetailSheetName(in.Year, in.Month)
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}
	providerName := in.ProviderName
	if providerName == "" {
		providerName = fallbackProviderName
	}
	customerName := in.CustomerName
	if customerName == "" {
		customerName = "-"
	}

	const fontName = "TH SarabunPSK"
	border := []excelize.Border{{Type: "top", Color: "BDBDBD", Style: 1}, {Type: "bottom", Color: "BDBDBD", Style: 1},
		{Type: "left", Color: "BDBDBD", Style: 1}, {Type: "right", Color: "BDBDBD", Style: 1}}
	fill := func(rgb string) excelize.Fill {
		return excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{rgb}}
	}
	numFmt := "#,##0.00"
	style := func(s *excelize.Style) int {
		id, err := f.NewStyle(s)
		if err != nil {
			panic(err) // a static style table: an error is a programming error, caught by the tests
		}
		return id
	}
	title := style(&excelize.Style{Font: &excelize.Font{Family: fontName, Size: 13, Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"}})
	sub := style(&excelize.Style{Font: &excelize.Font{Family: fontName, Size: 11}, Alignment: &excelize.Alignment{Horizontal: "center"}})
	header := style(&excelize.Style{Font: &excelize.Font{Family: fontName, Size: 11, Bold: true, Color: "FFFFFF"},
		Fill: fill("1565C0"), Border: border,
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true}})
	type rowStyles struct{ plain, center, right, num, wrap, mono int }
	body := func(bg string) rowStyles {
		base := func(a *excelize.Alignment, num bool, font *excelize.Font) int {
			if font == nil {
				font = &excelize.Font{Family: fontName, Size: 11}
			}
			s := &excelize.Style{Font: font, Border: border, Fill: fill(bg), Alignment: a}
			if num {
				s.CustomNumFmt = &numFmt
			}
			return style(s)
		}
		return rowStyles{
			plain:  base(nil, false, nil),
			center: base(&excelize.Alignment{Horizontal: "center"}, false, nil),
			right:  base(&excelize.Alignment{Horizontal: "right"}, false, nil),
			num:    base(&excelize.Alignment{Horizontal: "right"}, true, nil),
			wrap:   base(&excelize.Alignment{WrapText: true}, false, nil),
			mono:   base(nil, false, &excelize.Font{Family: "Consolas", Size: 9}),
		}
	}
	even, odd := body("FFFFFF"), body("E3F2FD")
	footLabel := style(&excelize.Style{Font: &excelize.Font{Family: fontName, Size: 11, Bold: true}, Fill: fill("F5F5F5"),
		Border: border, Alignment: &excelize.Alignment{Horizontal: "right"}})
	footNum := style(&excelize.Style{Font: &excelize.Font{Family: fontName, Size: 11, Bold: true}, Fill: fill("F5F5F5"),
		Border: border, Alignment: &excelize.Alignment{Horizontal: "right"}, CustomNumFmt: &numFmt})

	cell := func(col, row int) string {
		name, _ := excelize.CoordinatesToCellName(col+1, row+1)
		return name
	}
	var err error
	set := func(col, row int, v any, styleID int) {
		if err != nil {
			return
		}
		c := cell(col, row)
		if err = f.SetCellValue(sheet, c, v); err == nil && styleID != 0 {
			err = f.SetCellStyle(sheet, c, c, styleID)
		}
	}

	// Title rows, merged across the sheet.
	set(0, 0, providerName, title)
	set(0, 1, "รายละเอียดการขนส่ง เดือน"+documents.ThaiMonth(in.Month)+" "+strconv.Itoa(documents.BuddhistYear(in.Year)), title)
	set(0, 2, "ลูกค้า: "+customerName, sub)
	for r := range 3 {
		if err == nil {
			err = f.MergeCell(sheet, cell(0, r), cell(nc-1, r))
		}
	}
	for c, col := range cols {
		set(c, 4, col.Header, header)
	}

	rows := documents.DetailRows(in.Rows, in.OwnerShortName)
	for i, r := range rows {
		st := even
		if i%2 == 1 {
			st = odd
		}
		y := 5 + i
		c := 0
		next := func(v any, s int) {
			set(c, y, v, s)
			c++
		}
		next(i+1, st.center)
		next(documents.DateDMY(r.Date), st.plain)
		if in.ShowActualPickup {
			next(documents.DateTimeDMYHM(r.ActualPickupAt), st.center)
		}
		next(r.JobNo, st.mono)
		next(r.Route, st.wrap)
		next(r.VehicleClass, st.center)
		next(r.Plate, st.center)
		next(r.DriverName, st.plain)
		next(r.Sub, st.center)
		next(r.Phone, st.center)
		next(r.TruckCount, st.center)
		if r.DropFeeTHB != 0 {
			next(r.DropFeeTHB, st.num)
		} else {
			next("", st.num) // ค่าโยก stays blank without stops
		}
		next(r.TransportFeeTHB, st.num)
		next(r.TotalTHB, st.num)
		next(r.Round, st.center)
		next(r.FuelBand, st.center)
		next(r.Remark, st.center)
	}

	// Footer under ยอดรวม, its label under ค่าขนส่ง/เที่ยว (anchored by header, as the browser did).
	labelCol, totalCol := -1, -1
	for i, col := range cols {
		switch col.Header {
		case "ค่าขนส่ง/เที่ยว":
			labelCol = i
		case "ยอดรวม":
			totalCol = i
		}
	}
	y := 5 + len(rows) + 1
	t := in.Totals
	for _, line := range []struct {
		label  string
		amount float64
	}{
		{"ยอดรวมทั้งหมด", t.GrandTotal},
		{documents.WithholdingLabel(t.WithholdingRate), -t.WithholdingTax},
		{"ยอดสุทธิ", t.Net},
	} {
		set(labelCol, y, line.label, footLabel)
		set(totalCol, y, line.amount, footNum)
		y++
	}
	if err != nil {
		return nil, err
	}

	for i, col := range cols {
		name, _ := excelize.ColumnNumberToName(i + 1)
		if err := f.SetColWidth(sheet, name, name, col.Width); err != nil {
			return nil, err
		}
	}
	for i, h := range []float64{22, 20, 18, 8, 22} {
		if err := f.SetRowHeight(sheet, i+1, h); err != nil {
			return nil, err
		}
	}
	a4, landscape, one, unlimited := 9, "landscape", 1, 0
	if err := f.SetPageLayout(sheet, &excelize.PageLayoutOptions{Size: &a4, Orientation: &landscape,
		FitToWidth: &one, FitToHeight: &unlimited}); err != nil {
		return nil, err
	}
	fit := true
	if err := f.SetSheetProps(sheet, &excelize.SheetPropsOptions{FitToPage: &fit}); err != nil {
		return nil, err
	}
	lr, tb, hf := 0.5, 0.75, 0.3
	if err := f.SetPageMargins(sheet, &excelize.PageLayoutMarginsOptions{Left: &lr, Right: &lr, Top: &tb, Bottom: &tb,
		Header: &hf, Footer: &hf}); err != nil {
		return nil, err
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, fmt.Errorf("render: detail sheet: %w", err)
	}
	return buf.Bytes(), nil
}
