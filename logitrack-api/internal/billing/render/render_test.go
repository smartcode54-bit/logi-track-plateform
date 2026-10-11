package render

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/documents"
)

var bkk = time.FixedZone("ICT", 7*3600)

func fonts(t *testing.T) Fonts {
	t.Helper()
	f, err := LoadFonts("")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func ptr[T any](v T) *T { return &v }

// fixture: two trips of one route in two price rounds, a multi-drop trip with three billed stops and a standby record,
// for a plan-basis party (the dates print on the plan axis).
func fixtureRows() []documents.Row {
	vc := ptr("6WH")
	plan := documents.BasisPlan
	trip := func(id, no string, day int, amount float64, round string, add float64) documents.Row {
		return documents.Row{ID: id, TripRecordID: id, TripNo: no, DeliveredAt: time.Date(2026, 9, day+1, 9, 0, 0, 0, bkk),
			BillingDate: time.Date(2026, 9, day, 8, 0, 0, 0, bkk), BillingDateBasis: plan, EstimateTHB: amount,
			VehicleClass: vc, OriginHubCode: "SPK-GW", LookupHubCode: "SPK-GW", DestinationDisplayName: ptr("ลาดกระบัง"),
			LookupDestinationCode: ptr("SPK890103"), RoundEffectiveFromDate: round, AddTHBPerTrip: ptr(add),
			FuelBandLowerTHB: ptr(37.01), FuelBandUpperTHB: ptr(38.0), DriverName: "สมชาย ใจดี", DriverPhone: "0812345678",
			TruckLicensePlate: "70-1234", JobCategory: "PRIMARY"}
	}
	rows := []documents.Row{
		trip("t1", "SPX-001", 2, 1493.43, "2026-09-01", -40),
		trip("t2", "SPX-002", 20, 1453.43, "2026-09-15", -80),
		trip("t3", "SPX-003", 3, 1493.43, "2026-09-01", -40),
	}
	for i, amount := range []float64{1200, 150, 150} {
		r := trip("m1", "SPX-010-s"+string(rune('1'+i)), 5, amount, "2026-09-01", -40)
		r.ID = "m1_s" + string(rune('1'+i))
		r.Type = documents.RowMultidropStop
		r.StopIndex = i + 1
		rows = append(rows, r)
	}
	rows = append(rows, documents.Row{ID: "sb1", TripNo: "", DeliveredAt: time.Date(2026, 9, 14, 18, 0, 0, 0, bkk),
		EstimateTHB: 500, VehicleClass: vc, OriginHubCode: "SPK-GW", DestinationDisplayName: ptr("ลาดกระบัง"),
		Type: documents.RowStandby, DriverName: "สมหญิง", JobCategory: "SUPPLEMENTARY"})
	return rows
}

func totals() documents.DetailTotals {
	// 1493.43 + 1453.43 + 1493.43 + 1200 + 150 + 150 + 500 = 6440.29; WHT 3 % = 193.21 (Round2(193.2087))
	return documents.DetailTotals{GrandTotal: 6440.29, WithholdingRate: 0.03, WithholdingTax: 193.21, Net: 6247.08}
}

func invoiceInput() InvoiceInput {
	return InvoiceInput{Rows: fixtureRows(),
		Customer: Customer{Code: "CJSF", Name: "CJ Super Fresh", Address: "123 ถนนสุขุมวิท กรุงเทพมหานคร", TaxID: "0105556000001",
			BranchType: "branch", BranchNumber: "00002", PaymentTermsDays: ptr(30), ContactName: "คุณเอ", ContactPhone: "021234567"},
		Provider: Provider{Name: "บริษัท ทดสอบ จำกัด", ShortName: "WRT", Address: "Bangkok", TaxID: "0105556000002",
			BankName: "กสิกรไทย", AccountNumber: "123-4-56789-0", AccountName: "บริษัท ทดสอบ"},
		Statement: Statement{InvoiceNumber: "CJSF-202609-001", Year: 2026, Month: 9,
			IssuedAt: time.Date(2026, 9, 30, 17, 30, 0, 0, time.UTC), Totals: totals()}}
}

// find returns the ops whose text is s.
func find(ops []Op, s string) []Op {
	var out []Op
	for _, o := range ops {
		if o.Text == s {
			out = append(out, o)
		}
	}
	return out
}

func one(t *testing.T, ops []Op, s string) Op {
	t.Helper()
	got := find(ops, s)
	if len(got) != 1 {
		var texts []string
		for _, o := range ops {
			texts = append(texts, o.Text)
		}
		t.Fatalf("%q drawn %d times; texts: %q", s, len(got), texts)
	}
	return got[0]
}

func near(a, b float64) bool { return a-b < 0.01 && b-a < 0.01 }

// TestInvoiceLayout pins buildInvoicePdf's layout (web:lib/billingDocumentRender.ts:62-341): the coordinates, fonts and
// texts the browser drew, with the withholding from the statement (R18) and the Bangkok issue date.
func TestInvoiceLayout(t *testing.T) {
	pdf, ops, err := InvoicePDF(fonts(t), invoiceInput())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || !bytes.Contains(pdf, []byte("/FontFile2")) {
		t.Fatalf("not a PDF with an embedded TrueType font")
	}
	for _, c := range []struct {
		text  string
		x, y  float64
		bold  bool
		size  float64
		align string
	}{
		{"ใบวางบิล", 105, 18, true, 16, "center"},
		{"(ใบแจ้งหนี้)", 105, 26, false, 11, "center"},
		{"บริษัท ทดสอบ จำกัด", 14, 34, true, 9, "left"},
		{"Tax ID: 0105556000002", 14, 44, false, 9, "left"},
		{"เลขที่: CJSF-202609-001", 196, 32, true, 9, "right"},
		{"วันที่: 01/10/2026", 196, 37, false, 9, "right"}, // generated 30 Sep 17:30Z = 1 Oct in Bangkok
		{"ประจำเดือน: กันยายน 2026", 196, 42, false, 9, "right"},
		{"กำหนดชำระ: 30 วัน", 196, 47, false, 9, "right"},
		{"เรียน / Bill To:", 14, 55, true, 9, "left"},
		{"CJ Super Fresh", 14, 60, false, 9, "left"},
		{"Tax ID: 0105556000001 (สาขา 00002)", 14, 70, false, 9, "left"},
		{"ผู้ติดต่อ: คุณเอ  |  021234567", 14, 75, false, 9, "left"},
		{"เรตตามช่วงราคาน้ำมัน (ดีเซล)", 14, 85, true, 9, "left"},
	} {
		o := one(t, ops, c.text)
		if !near(o.X, c.x) || !near(o.Y, c.y) || o.Bold != c.bold || o.Size != c.size || o.Align != c.align {
			t.Errorf("%q drawn at (%.2f, %.2f) bold=%v size=%v %s, want (%v, %v) bold=%v size=%v %s",
				c.text, o.X, o.Y, o.Bold, o.Size, o.Align, c.x, c.y, c.bold, c.size, c.align)
		}
	}
	// The legend: two rounds, labelled by date order, spans on the plan axis, band and add of the round's first row.
	one(t, ops, "R1   2-5/9/2569   37.01–38.00 ฿/L   ปรับ -40.00/เที่ยว")
	one(t, ops, "R2   20/9/2569   37.01–38.00 ฿/L   ปรับ -80.00/เที่ยว")
	// Two rounds add the รอบ column; the date head names the plan axis. ลำดับ wraps in its 10 mm column ("ลำดั" / "บ"),
	// as jspdf-autotable broke a word wider than its cell.
	for _, h := range []string{"ลำดั", "รอบ", "ประเภทรถ", "รายการ", "วันที่ตามแผน", "จำนวน", "ราคา/หน่วย", "รวม"} {
		if len(find(ops, h)) == 0 {
			t.Errorf("head %q missing", h)
		}
	}
	// Lines: R1 trips grouped (2 x 1,493.43), the R2 trip alone, every multi-drop stop under ค่าโยก by price, standby
	// listed per day.
	one(t, ops, "2,986.86")
	one(t, ops, "2-3/9/2569")
	if len(find(ops, "ค่าโยก")) != 2 {
		t.Errorf("ค่าโยก lines: %d, want 2 (1,200.00 and 2 x 150.00)", len(find(ops, "ค่าโยก")))
	}
	one(t, ops, "SPK-GW → ลาดกระบัง (Stand by)")
	one(t, ops, "14/9/2569")
	// Totals from the statement, the withholding label from its stored rate (not the legacy fixed 1 %).
	one(t, ops, "6,440.29")
	one(t, ops, "ภาษีหัก ณ ที่จ่าย 3%")
	one(t, ops, "-193.21")
	net := one(t, ops, "6,247.08")
	if !net.Bold || net.Align != "right" || !near(net.X, 196) {
		t.Errorf("net drawn %+v", net)
	}
	baht := one(t, ops, "ยอดรวม : "+documents.BahtText(6247.08))
	if !near(baht.Y, net.Y+7) || !near(baht.X, 14) {
		t.Errorf("baht text at (%.2f, %.2f), net at y %.2f", baht.X, baht.Y, net.Y)
	}
	one(t, ops, "ธนาคาร กสิกรไทย   เลขที่บัญชี 123-4-56789-0   ชื่อบัญชี บริษัท ทดสอบ")
	sig := one(t, ops, "ผู้จัดทำวางบิล")
	if !near(sig.Y, net.Y-14+52+5) || !near(sig.X, 40) || sig.Align != "center" {
		t.Errorf("signatory at (%.2f, %.2f)", sig.X, sig.Y)
	}
	for _, s := range []string{"ผู้จัดทำ/ผู้ตรวจสอบ", "ตราประทับ", "ผู้รับวางบิล"} {
		one(t, ops, s)
	}
}

func TestReceiptTitleAndPaidDate(t *testing.T) {
	in := invoiceInput()
	in.Receipt = true
	in.Statement.IssuedAt = time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC)
	_, ops, err := InvoicePDF(fonts(t), in)
	if err != nil {
		t.Fatal(err)
	}
	title := one(t, ops, "ใบเสร็จรับเงิน")
	if !near(title.Y, 20) || title.Size != 18 || !title.Bold {
		t.Errorf("receipt title %+v", title)
	}
	if len(find(ops, "ใบวางบิล")) != 0 || len(find(ops, "(ใบแจ้งหนี้)")) != 0 {
		t.Error("a receipt carries the invoice title")
	}
	one(t, ops, "วันที่: 20/10/2026")
	one(t, ops, "ภาษีหัก ณ ที่จ่าย 3%") // the receipt shows the deduction (commit e3bb23b)
}

// TestInvoiceSingleRoundAndDeliveredBasis: one round draws no legend and no รอบ column; a delivered-basis set heads
// its dates วันที่จัดส่ง.
func TestInvoiceSingleRoundAndDeliveredBasis(t *testing.T) {
	in := invoiceInput()
	rows := fixtureRows()[:1]
	rows[0].BillingDateBasis = documents.BasisDelivered
	in.Rows = rows
	_, ops, err := InvoicePDF(fonts(t), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(find(ops, "เรตตามช่วงราคาน้ำมัน (ดีเซล)")) != 0 || len(find(ops, "รอบ")) != 0 {
		t.Error("one round draws a legend or a รอบ column")
	}
	one(t, ops, "วันที่จัดส่ง")
}

// TestInvoiceFooterMovesToANewPage: a table that leaves no room for the footer block moves the whole block (totals,
// payment, signatures) to the next page, as the browser did; the table's head repeats on each page.
func TestInvoiceFooterMovesToANewPage(t *testing.T) {
	in := invoiceInput()
	var rows []documents.Row
	for i := range 60 {
		r := fixtureRows()[0]
		r.EstimateTHB = float64(1000 + i) // one line per price
		rows = append(rows, r)
	}
	in.Rows = rows
	_, ops, err := InvoicePDF(fonts(t), in)
	if err != nil {
		t.Fatal(err)
	}
	heads := find(ops, "ราคา/หน่วย")
	if len(heads) < 2 {
		t.Fatalf("the head is drawn %d times over %d pages", len(heads), ops[len(ops)-1].Page)
	}
	total := one(t, ops, "ยอดรวมทั้งสิ้น:")
	last := one(t, ops, "ผู้รับวางบิล")
	if total.Page != last.Page || last.Y > pageH-10 {
		t.Errorf("footer split: totals on page %d, signatures on page %d at y %.1f", total.Page, last.Page, last.Y)
	}
}

func TestDetailXLSX(t *testing.T) {
	in := DetailInput{Rows: fixtureRows(), Year: 2026, Month: 9, CustomerName: "CJ Super Fresh",
		ProviderName: "บริษัท ทดสอบ จำกัด", OwnerShortName: "WRT", Totals: totals(), ShowActualPickup: true}
	b, err := DetailXLSX(in)
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sheet := "รายละเอียดขนส่ง กันยายน 2569"
	if got := f.GetSheetList(); !slices.Equal(got, []string{sheet}) {
		t.Fatalf("sheets %q", got)
	}
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != "บริษัท ทดสอบ จำกัด" || rows[1][0] != "รายละเอียดการขนส่ง เดือนกันยายน 2569" || rows[2][0] != "ลูกค้า: CJ Super Fresh" {
		t.Errorf("titles %q %q %q", rows[0], rows[1], rows[2])
	}
	wantHead := []string{"No.", "วันที่ตามแผน", "วันรับงานจริง", "เลขใบงาน", "เส้นทาง", "ประเภท", "ทะเบียน", "ชื่อคนขับรถ", "Sub",
		"เบอร์รถ", "จำนวนรถ", "ค่าโยก", "ค่าขนส่ง/เที่ยว", "ยอดรวม", "รอบ", "ช่วงราคาน้ำมัน", "หมายเหตุ"}
	if !slices.Equal(rows[4], wantHead) {
		t.Errorf("head %q", rows[4])
	}
	// One row per trip, the multi-drop trip folded (transport = stop 1, ค่าโยก = stops 2+), sorted on the axis date.
	data := rows[5 : 5+5]
	wantNos := []string{"SPX-001", "SPX-003", "SPX-010", "Stand by", "SPX-002"}
	for i, r := range data {
		if r[3] != wantNos[i] {
			t.Errorf("row %d job %q, want %q (%q)", i, r[3], wantNos[i], r)
		}
	}
	md := data[2]
	if md[11] != "300.00" || md[12] != "1,200.00" || md[13] != "1,500.00" || md[0] != "3" || md[1] != "05/09/2026" {
		t.Errorf("multi-drop row %q", md)
	}
	sb := data[3]
	if sb[10] != "0" || sb[11] != "" || sb[8] != "WRT" || sb[16] != "เสริม" || sb[14] != "-" || sb[15] != "-" {
		t.Errorf("standby row %q", sb)
	}
	if data[0][14] != "R1" || data[4][14] != "R2" || data[0][15] != "37.01–38.00" || data[0][5] != "6WH" {
		t.Errorf("round columns %q / %q", data[0], data[4])
	}
	// Footer under ยอดรวม: the statement's total, its withholding at the stored rate, the net.
	foot := rows[len(rows)-3:]
	for i, want := range [][2]string{{"ยอดรวมทั้งหมด", "6,440.29"}, {"ภาษีหัก ณ ที่จ่าย 3%", "-193.21"}, {"ยอดสุทธิ", "6,247.08"}} {
		if foot[i][12] != want[0] || foot[i][13] != want[1] {
			t.Errorf("footer %d %q, want %q", i, foot[i], want)
		}
	}
	// Styles and page setup: amounts as #,##0.00 numbers, the header fill, A4 landscape fit to width, merged titles.
	v, err := f.GetCellValue(sheet, "N8", excelize.Options{RawCellValue: true})
	if err != nil || v != "1500" {
		t.Errorf("N8 raw %q %v", v, err)
	}
	merged, _ := f.GetMergeCells(sheet)
	if len(merged) != 3 {
		t.Errorf("merges %d", len(merged))
	}
	layout, err := f.GetPageLayout(sheet)
	if err != nil || layout.Orientation == nil || *layout.Orientation != "landscape" || layout.Size == nil || *layout.Size != 9 ||
		layout.FitToWidth == nil || *layout.FitToWidth != 1 {
		t.Errorf("page layout %+v %v", layout, err)
	}
	if w, _ := f.GetColWidth(sheet, "E"); w != 30 {
		t.Errorf("route column width %v", w)
	}
	styleID, _ := f.GetCellStyle(sheet, "A5")
	st, err := f.GetStyle(styleID)
	if err != nil || len(st.Fill.Color) != 1 || st.Fill.Color[0] != "1565C0" || st.Font == nil || !st.Font.Bold {
		t.Errorf("header style %+v %v", st, err)
	}
}

func TestDetailSheetNameIs31Characters(t *testing.T) {
	name := DetailSheetName(2026, 9)
	if name != "รายละเอียดขนส่ง กันยายน 2569" {
		t.Errorf("name %q", name)
	}
	if n := len([]rune(DetailSheetName(2026, 11))); n > 31 {
		t.Errorf("%d characters", n)
	}
}

func TestBundleZIP(t *testing.T) {
	at := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	b, err := BundleZIP([]byte("%PDF-x"), []byte("PK-xlsx"), at)
	if err != nil {
		t.Fatal(err)
	}
	r, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		_ = rc.Close()
		if (f.Name == BundleInvoiceName && string(body) != "%PDF-x") || (f.Name == BundleDetailName && string(body) != "PK-xlsx") {
			t.Errorf("%s holds %q", f.Name, body)
		}
		if !f.Modified.Equal(at) {
			t.Errorf("%s dated %v", f.Name, f.Modified)
		}
	}
	if !slices.Equal(names, []string{"invoice_summary.pdf", "invoice_detail.xlsx"}) {
		t.Errorf("entries %q (the receipt is never bundled)", names)
	}
}

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 80, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDownscaleAndEmbeddable(t *testing.T) {
	img, err := Downscale(jpegOf(t, 3200, 1600), PhotoMaxDim, PhotoJPEGQuality)
	if err != nil || img.W != 1600 || img.H != 800 {
		t.Fatalf("downscale %dx%d %v", img.W, img.H, err)
	}
	small, err := Downscale(jpegOf(t, 400, 300), PhotoMaxDim, PhotoJPEGQuality)
	if err != nil || small.W != 400 || small.H != 300 {
		t.Fatalf("a small photo is never scaled up: %dx%d %v", small.W, small.H, err)
	}
	if _, err := Downscale([]byte("not an image"), PhotoMaxDim, 75); err == nil {
		t.Error("garbage accepted")
	}
	// A header that declares more than MaxPixels is refused before decoding.
	var huge bytes.Buffer
	if err := png.Encode(&huge, image.NewGray(image.Rect(0, 0, 10, 10))); err != nil {
		t.Fatal(err)
	}
	b := huge.Bytes()
	copy(b[16:24], []byte{0, 0, 0x27, 0x10, 0, 0, 0x27, 0x10}) // IHDR 10000 x 10000
	if _, err := Downscale(b, PhotoMaxDim, 75); err == nil {
		t.Error("a 100-megapixel header was decoded")
	}
	var stamp bytes.Buffer
	if err := png.Encode(&stamp, image.NewNRGBA(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatal(err)
	}
	e, err := Embeddable(stamp.Bytes())
	if err != nil || e.W != 40 || e.H != 30 || !bytes.Equal(e.Data, stamp.Bytes()) {
		t.Errorf("a PNG stamp is kept as is: %dx%d %v", e.W, e.H, err)
	}
	// A transparent PNG stamp and a JPEG signature embed in the invoice.
	in := invoiceInput()
	in.Provider.Stamp = &e
	sig := Image{Data: jpegOf(t, 120, 40), W: 120, H: 40}
	in.Provider.Signature = &sig
	pdf, _, err := InvoicePDF(fonts(t), in)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(pdf, []byte("/Subtype /Image")); n < 2 {
		t.Errorf("%d images embedded, want the stamp and the signature", n)
	}
}

func TestShopeeReportLayout(t *testing.T) {
	photo, err := Downscale(jpegOf(t, 800, 1200), PhotoMaxDim, PhotoJPEGQuality)
	if err != nil {
		t.Fatal(err)
	}
	day := func(d int) time.Time { return time.Date(2026, 9, d, 10, 0, 0, 0, bkk) }
	in := ShopeeInput{CustomerName: "TTP Co.", ProviderName: "บริษัท ทดสอบ จำกัด", Year: 2026, Month: 9, Half: HalfFirst,
		IssuedAt: time.Date(2026, 9, 16, 1, 0, 0, 0, time.UTC),
		Rows: []ShopeeRow{
			{ID: "a", TripNo: "SPX-1", DeliveredAt: day(2), DriverKey: "d1", DriverName: "สมชาย", Origin: "SPK-GW", Destination: "ลาดกระบัง",
				VehicleClass: "6WH", Plate: "70-1", ParcelCount: ptr(int32(12)), PhotoCount: 3, Photos: []Image{photo, photo, photo}},
			{ID: "b", TripNo: "SPX-2", DeliveredAt: day(3), DriverKey: "d2", DriverName: "สมหญิง", Origin: "SPK-GW", Destination: "-"},
			{ID: "c", TripNo: "SPX-3", DeliveredAt: day(4), DriverKey: "d1", DriverName: "สมชาย", Origin: "SPK-GW", Destination: "บางนา",
				PhotoCount: 1, Photos: []Image{photo}},
		}}
	pdf, ops, err := ShopeeReportPDF(fonts(t), in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatal("not a PDF")
	}
	one(t, ops, "รายงานประกอบการวางบิล — Shopee Express")
	one(t, ops, "ประจำเดือน: กันยายน 2569 (รอบ 1–15)")
	one(t, ops, "จำนวนเที่ยว: 3 เที่ยว")
	one(t, ops, "คนขับ: 2 คน")
	one(t, ops, "รูปรันชีพที่เซ็น: 4 รูป")
	one(t, ops, "วันที่ออกรายงาน: 16/09/2026")
	one(t, ops, "(ไม่แสดงราคา)")
	missing := one(t, ops, "เที่ยวที่ยังไม่มีรูปรันชีพที่เซ็น (1)")
	if missing.Color != (RGB{180, 30, 30}) {
		t.Errorf("fail-loud heading colour %v", missing.Color)
	}
	if len(find(ops, "SPK-GW")) != 2 {
		t.Errorf("a route without a destination prints the origin alone: %d", len(find(ops, "SPK-GW")))
	}
	// สมชาย: 4 photos, two per page -> 2 pages; สมหญิง has none (covered by the fail-loud table).
	headers := find(ops, "คนขับ: สมชาย")
	if len(headers) != 2 || len(find(ops, "คนขับ: สมหญิง")) != 0 {
		t.Errorf("driver pages: %d for สมชาย", len(headers))
	}
	if n := len(find(ops, "SPX-1 · 02/09/2026 · SPK-GW → ลาดกระบัง")); n != 3 {
		t.Errorf("SPX-1 captions: %d, want one per photo (3)", n)
	}
	last := ops[len(ops)-1]
	if last.Text != "หน้า 3/3" || last.Page != 3 {
		t.Errorf("last op %+v, want the page number of page 3", last)
	}
	if n := bytes.Count(pdf, []byte("/Subtype /Image")); n < 1 {
		t.Errorf("no image embedded")
	}
}

func TestLoadFonts(t *testing.T) {
	if _, err := LoadFonts(""); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := LoadFonts(dir); err == nil || !strings.Contains(err.Error(), "PDF_FONT_DIR") {
		t.Errorf("an empty directory: %v", err)
	}
	for _, n := range FontFiles {
		b, _ := embedded.ReadFile("fonts/" + n)
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadFonts(dir); err != nil {
		t.Errorf("the web's fonts in PDF_FONT_DIR: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FontFiles[1]), []byte("not a font"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFonts(dir); err == nil {
		t.Error("a broken font accepted")
	}
}

// TestFontsMatchTheWeb: the built-in Sarabun is the web's (logitrack-web/public/fonts), byte for byte.
func TestFontsMatchTheWeb(t *testing.T) {
	for _, n := range FontFiles {
		web, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "logitrack-web", "public", "fonts", n))
		if err != nil {
			t.Skipf("web fonts not beside the module: %v", err)
		}
		ours, _ := embedded.ReadFile("fonts/" + n)
		if !bytes.Equal(web, ours) {
			t.Errorf("%s differs from logitrack-web/public/fonts", n)
		}
	}
}
