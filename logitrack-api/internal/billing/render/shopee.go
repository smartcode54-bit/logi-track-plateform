package render

import (
	"strconv"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/documents"
)

// Half is the billing round of a Shopee report month (BillingHalf, logitrack-web/features/accounting/api/billing.ts).
type Half string

const (
	HalfFull   Half = "full"   // the whole month
	HalfFirst  Half = "first"  // days 1-15
	HalfSecond Half = "second" // day 16 to the month's end
)

// ShopeeRow is one delivered trip of the report (ShopeeReportTripRow): price-free, with its signed run-sheet photos
// already downscaled (a photo that could not be read is left out, as the browser skipped it).
type ShopeeRow struct {
	ID           string
	TripNo       string
	DeliveredAt  time.Time
	DriverKey    string // the driver's id, else ""
	DriverName   string
	Origin       string // display names
	Destination  string
	VehicleClass string
	Plate        string
	ParcelCount  *int32
	PhotoCount   int // signed run-sheet photos the trip carries, readable or not
	Photos       []Image
}

// ShopeeInput is everything the report needs.
type ShopeeInput struct {
	Rows         []ShopeeRow
	CustomerName string
	ProviderName string
	Year, Month  int
	Half         Half
	IssuedAt     time.Time
}

const shopeeMargin = 12.0

func shopeeRoundLabel(h Half) string {
	switch h {
	case HalfFirst:
		return "(รอบ 1–15)"
	case HalfSecond:
		return "(รอบ 16–สิ้นเดือน)"
	}
	return ""
}

func shopeeRoute(r ShopeeRow) string {
	var parts []string
	for _, p := range []string{r.Origin, r.Destination} {
		if p != "" && p != "-" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " → ")
}

func shopeeDate(r ShopeeRow) string {
	if r.DeliveredAt.IsZero() {
		return "-"
	}
	return documents.DateDMY(r.DeliveredAt)
}

func shopeeJobNo(r ShopeeRow) string {
	if r.TripNo != "" {
		return r.TripNo
	}
	return truncateID(r.ID, 10)
}

func truncateID(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ShopeeReportPDF draws the Shopee Express billing support report (generateShopeeExpressReportBlob,
// web:lib/shopeeExpressReport.ts:136-321): a price-free manifest of the period's delivered trips, a fail-loud list of
// trips without a signed run-sheet, then the signed run-sheets grouped by driver, two to a page, and page numbers.
// The issue date is the render's, on the Bangkok calendar.
func ShopeeReportPDF(f Fonts, in ShopeeInput) ([]byte, []Op, error) {
	if len(f.Regular) == 0 || len(f.Bold) == 0 {
		return nil, nil, errNoFonts
	}
	d, err := newPDF(f, in.IssuedAt)
	if err != nil {
		return nil, nil, err
	}
	type group struct {
		key, name string
		rows      []ShopeeRow
	}
	var groups []*group
	byKey := map[string]*group{}
	var missing []ShopeeRow
	totalPhotos := 0
	for _, r := range in.Rows {
		key := strings.TrimSpace(r.DriverKey)
		if key == "" {
			key = r.DriverName
		}
		if key == "" {
			key = "-"
		}
		key = strings.ToLower(key)
		g, ok := byKey[key]
		if !ok {
			name := r.DriverName
			if name == "" {
				name = "-"
			}
			g = &group{key: key, name: name}
			byKey[key] = g
			groups = append(groups, g)
		}
		g.rows = append(g.rows, r)
		if r.PhotoCount == 0 {
			missing = append(missing, r)
		}
		totalPhotos += r.PhotoCount
	}

	d.setFont(true, 16)
	d.text("รายงานประกอบการวางบิล — Shopee Express", pageW/2, 20, "center")
	d.setFont(false, 11)
	if in.ProviderName != "" {
		d.text(in.ProviderName, pageW/2, 27, "center")
	}
	y := 38.0
	d.setFont(true, 10)
	d.text("ลูกค้า: "+in.CustomerName, shopeeMargin, y, "")
	d.setFont(false, 10)
	period := documents.ThaiMonth(in.Month) + " " + strconv.Itoa(documents.BuddhistYear(in.Year))
	d.text(strings.TrimRight("ประจำเดือน: "+period+" "+shopeeRoundLabel(in.Half), " "), pageW-shopeeMargin, y, "right")
	y += 6
	d.text("จำนวนเที่ยว: "+strconv.Itoa(len(in.Rows))+" เที่ยว", shopeeMargin, y, "")
	d.text("คนขับ: "+strconv.Itoa(len(groups))+" คน", shopeeMargin+55, y, "")
	d.text("รูปรันชีพที่เซ็น: "+strconv.Itoa(totalPhotos)+" รูป", shopeeMargin+95, y, "")
	d.text("วันที่ออกรายงาน: "+documents.DateDMY(in.IssuedAt), pageW-shopeeMargin, y, "right")

	y += 9
	d.setFont(true, 12)
	d.text("สรุปเที่ยววิ่ง", shopeeMargin, y, "")
	d.setFont(false, 9)
	d.text("(ไม่แสดงราคา)", shopeeMargin+26, y, "")
	body := make([][]string, len(in.Rows))
	for i, r := range in.Rows {
		parcels := "-"
		if r.ParcelCount != nil {
			parcels = strconv.Itoa(int(*r.ParcelCount))
		}
		vc, plate := r.VehicleClass, r.Plate
		if vc == "" {
			vc = "-"
		}
		if plate == "" {
			plate = "-"
		}
		body[i] = []string{strconv.Itoa(i + 1), shopeeDate(r), shopeeJobNo(r), shopeeRoute(r), vc, plate, r.DriverName, parcels}
	}
	style := tableStyle{fontSize: 8, padding: 1.5, headFill: RGB{30, 80, 160}, headText: white, bodyText: RGB{20, 20, 20},
		marginL: shopeeMargin, marginR: shopeeMargin}
	after := d.table(y+3, []string{"No.", "วันที่ส่ง", "เลขงาน", "เส้นทาง", "ประเภทรถ", "ทะเบียน", "คนขับ", "พัสดุ"}, body,
		map[int]column{0: {9, "center"}, 1: {20, "center"}, 2: {26, ""}, 4: {18, "center"}, 5: {20, "center"}, 7: {12, "center"}},
		style) + 6

	if len(missing) > 0 {
		if after > pageH-40 {
			d.addPage()
			after = 20
		}
		d.setFont(true, 10)
		d.setTextColor(RGB{180, 30, 30})
		d.text("เที่ยวที่ยังไม่มีรูปรันชีพที่เซ็น ("+strconv.Itoa(len(missing))+")", shopeeMargin, after, "")
		d.setTextColor(black)
		rows := make([][]string, len(missing))
		for i, r := range missing {
			rows[i] = []string{strconv.Itoa(i + 1), shopeeDate(r), shopeeJobNo(r), shopeeRoute(r), r.DriverName}
		}
		grid := style
		grid.grid, grid.headFill = true, RGB{180, 30, 30}
		d.table(after+2, []string{"No.", "วันที่ส่ง", "เลขงาน", "เส้นทาง", "คนขับ"}, rows,
			map[int]column{0: {9, "center"}, 1: {22, "center"}}, grid)
	}

	// Signed run-sheets, two per page stacked, full content width.
	const gap, captionH, gridTop = 6.0, 5.0, 22.0
	cellW := pageW - 2*shopeeMargin
	rowH := (pageH - gridTop - shopeeMargin - gap) / 2
	boxH := rowH - captionH
	for _, g := range groups {
		type item struct {
			row ShopeeRow
			img Image
		}
		var items []item
		for _, r := range g.rows {
			for _, img := range r.Photos {
				items = append(items, item{r, img})
			}
		}
		if len(items) == 0 {
			continue
		}
		header := func() {
			d.setFont(true, 12)
			d.text("คนขับ: "+g.name, shopeeMargin, 16, "")
			d.setFont(false, 9)
			d.text(strconv.Itoa(len(g.rows))+" เที่ยว · "+strconv.Itoa(len(items))+" รูป", pageW-shopeeMargin, 16, "right")
		}
		d.addPage()
		header()
		for i, it := range items {
			pos := i % 2
			if i > 0 && pos == 0 {
				d.addPage()
				header()
			}
			cellY := gridTop + float64(pos)*(rowH+gap)
			d.setFont(false, 7.5)
			caption := shopeeJobNo(it.row) + " · " + shopeeDate(it.row) + " · " + shopeeRoute(it.row)
			if ls := d.split(caption, cellW); len(ls) > 0 {
				d.text(ls[0], shopeeMargin, cellY+3.5, "")
			}
			ratio := min(cellW/float64(it.img.W), boxH/float64(it.img.H))
			dw, dh := float64(it.img.W)*ratio, float64(it.img.H)*ratio
			d.image(it.img, shopeeMargin+(cellW-dw)/2, cellY+captionH, dw, dh)
		}
	}

	total := d.pages()
	for p := 1; p <= total; p++ {
		d.setPage(p)
		d.setFont(false, 8)
		d.setTextColor(RGB{120, 120, 120})
		d.text("หน้า "+strconv.Itoa(p)+"/"+strconv.Itoa(total), pageW/2, pageH-6, "center")
		d.setTextColor(black)
	}
	b, err := d.bytes()
	return b, d.ops, err
}
