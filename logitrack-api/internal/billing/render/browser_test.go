package render

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/documents"
)

// tsRow is a BillingTripRow of testdata/detail_fixture.json.
type tsRow struct {
	ID                   string   `json:"id"`
	TripRecordID         string   `json:"tripRecordId"`
	SpxTripID            string   `json:"spxTripId"`
	DeliveredTimestamp   string   `json:"deliveredTimestamp"`
	BillingDate          string   `json:"billingDate"`
	ActualPickupAt       string   `json:"actualPickupAt"`
	BillingDateBasis     string   `json:"billingDateBasis"`
	BillingEstimateThb   float64  `json:"billingEstimateThb"`
	BillingLookupHubID   string   `json:"billingLookupHubId"`
	BillingLookupDest    *string  `json:"billingLookupDestination"`
	VehicleClass         *string  `json:"vehicleClass"`
	DriverName           string   `json:"driverName"`
	DriverPhone          string   `json:"driverPhone"`
	SubcontractorName    string   `json:"subcontractorName"`
	TruckLicensePlate    string   `json:"truckLicensePlate"`
	HubDisplayName       string   `json:"hubDisplayName"`
	OriginHubCode        string   `json:"originHubCode"`
	DestinationDisplay   *string  `json:"destinationDisplayName"`
	RowType              string   `json:"rowType"`
	StopIndex            int      `json:"stopIndex"`
	JobCategory          string   `json:"jobCategory"`
	RoundEffectiveFrom   string   `json:"billingRoundEffectiveFromDateStr"`
	FuelBandLower        *float64 `json:"billingFuelBandLowerThb"`
	FuelBandUpper        *float64 `json:"billingFuelBandUpperThb"`
	AddThbPerTrip        *float64 `json:"billingAddThbPerTrip"`
	ReferenceFuelPriceTh *float64 `json:"billingReferenceFuelPriceThb"`
}

func instant(t *testing.T, s string) time.Time {
	t.Helper()
	if s == "" {
		return time.Time{}
	}
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestDetailXLSXMatchesTheBrowser compares the detail sheet with what the browser's generateDetailExcelBuffer built for
// the same rows (testdata/detail_ts.json, written by detail_ts.mjs from logitrack-web/lib/billingDocumentRender.ts; frozen since F87
// removed both, the last tree holding them is 4fc3d70e):
// every title, header and data cell (value, type, number format), the merges and the column widths. The one intended
// difference is the footer: the server prints the statement's exact NUMERIC totals where the browser printed its float
// sum (5047.150000000001), within the 0.005 THB of R20.
func TestDetailXLSXMatchesTheBrowser(t *testing.T) {
	var fixture struct {
		Period   struct{ Year, Month int } `json:"period"`
		Customer struct{ Name string }     `json:"customer"`
		Provider struct {
			Name      string `json:"name"`
			ShortName string `json:"shortName"`
		} `json:"provider"`
		ShowActualPickup bool    `json:"showActualPickup"`
		Rows             []tsRow `json:"rows"`
	}
	var want struct {
		Sheet string `json:"sheet"`
		Cells [][]*struct {
			V any     `json:"v"`
			T string  `json:"t"`
			Z *string `json:"z"`
		} `json:"cells"`
		Merges []string   `json:"merges"`
		Cols   []*float64 `json:"cols"`
	}
	for name, v := range map[string]any{"testdata/detail_fixture.json": &fixture, "testdata/detail_ts.json": &want} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatal(err)
		}
	}
	var rows []documents.Row
	for _, r := range fixture.Rows {
		rows = append(rows, documents.Row{ID: r.ID, TripRecordID: r.TripRecordID, TripNo: r.SpxTripID,
			DeliveredAt: instant(t, r.DeliveredTimestamp), BillingDate: instant(t, r.BillingDate),
			ActualPickupAt: instant(t, r.ActualPickupAt), BillingDateBasis: documents.DateBasis(r.BillingDateBasis),
			EstimateTHB: r.BillingEstimateThb, LookupHubCode: r.BillingLookupHubID, LookupDestinationCode: r.BillingLookupDest,
			VehicleClass: r.VehicleClass, DriverName: r.DriverName, DriverPhone: r.DriverPhone,
			SubcontractorName: r.SubcontractorName, TruckLicensePlate: r.TruckLicensePlate, HubDisplayName: r.HubDisplayName,
			OriginHubCode: r.OriginHubCode, DestinationDisplayName: r.DestinationDisplay, Type: documents.RowType(r.RowType),
			StopIndex: r.StopIndex, JobCategory: r.JobCategory, RoundEffectiveFromDate: r.RoundEffectiveFrom,
			FuelBandLowerTHB: r.FuelBandLower, FuelBandUpperTHB: r.FuelBandUpper, AddTHBPerTrip: r.AddThbPerTrip,
			ReferenceFuelPriceTHB: r.ReferenceFuelPriceTh})
	}
	b, err := DetailXLSX(DetailInput{Rows: rows, Year: fixture.Period.Year, Month: fixture.Period.Month,
		CustomerName: fixture.Customer.Name, ProviderName: fixture.Provider.Name, OwnerShortName: fixture.Provider.ShortName,
		ShowActualPickup: fixture.ShowActualPickup,
		Totals:           documents.DetailTotals{GrandTotal: 5047.15, WithholdingRate: 0.03, WithholdingTax: 151.41, Net: 4895.74}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if f.GetSheetName(0) != want.Sheet {
		t.Fatalf("sheet %q, browser %q", f.GetSheetName(0), want.Sheet)
	}
	footer := len(want.Cells) - 3
	// excelize reads every cell of a merged range as its top-left value; the browser's hidden cells there are "".
	hidden := map[string]bool{}
	merges, _ := f.GetMergeCells(want.Sheet)
	for _, m := range merges {
		c1, r1, _ := excelize.CellNameToCoordinates(m.GetStartAxis())
		c2, r2, _ := excelize.CellNameToCoordinates(m.GetEndAxis())
		for r := r1; r <= r2; r++ {
			for c := c1; c <= c2; c++ {
				if r != r1 || c != c1 {
					name, _ := excelize.CoordinatesToCellName(c, r)
					hidden[name] = true
				}
			}
		}
	}
	for r, row := range want.Cells {
		for c, cell := range row {
			name, _ := excelize.CoordinatesToCellName(c+1, r+1)
			if hidden[name] {
				continue
			}
			raw, err := f.GetCellValue(want.Sheet, name, excelize.Options{RawCellValue: true})
			if err != nil {
				t.Fatal(err)
			}
			typ, _ := f.GetCellType(want.Sheet, name)
			if cell == nil {
				if raw != "" {
					t.Errorf("%s = %q, the browser left it empty", name, raw)
				}
				continue
			}
			switch v := cell.V.(type) {
			case string:
				if raw != v {
					t.Errorf("%s = %q, browser %q", name, raw, v)
				}
			case float64:
				got, err := strconv.ParseFloat(raw, 64)
				if err != nil || typ != excelize.CellTypeNumber && typ != excelize.CellTypeUnset {
					t.Errorf("%s = %q (type %v), browser the number %v", name, raw, typ, v)
					continue
				}
				if r >= footer {
					if math.Abs(got-v) > 0.005 {
						t.Errorf("footer %s = %v, browser %v (beyond 0.005 THB)", name, got, v)
					}
				} else if got != v {
					t.Errorf("%s = %v, browser %v", name, got, v)
				}
			}
			if cell.Z != nil && *cell.Z != "General" {
				id, _ := f.GetCellStyle(want.Sheet, name)
				st, err := f.GetStyle(id)
				if err != nil || st.CustomNumFmt == nil || *st.CustomNumFmt != *cell.Z {
					t.Errorf("%s number format %v, browser %q", name, st.CustomNumFmt, *cell.Z)
				}
			}
		}
	}
	var got []string
	for _, m := range merges {
		got = append(got, m.GetStartAxis()+":"+m.GetEndAxis())
	}
	if len(got) != len(want.Merges) {
		t.Errorf("merges %v, browser %v", got, want.Merges)
	}
	for i, w := range want.Cols {
		name, _ := excelize.ColumnNumberToName(i + 1)
		width, _ := f.GetColWidth(want.Sheet, name)
		if w != nil && width != *w {
			t.Errorf("column %s width %v, browser %v", name, width, *w)
		}
	}
}

// tsOp is one text the browser's jsPDF drew (testdata/invoice_ts.json): autoTable cells come already aligned (align
// left at their final x); the other texts keep their anchor and alignment.
type tsOp struct {
	Page  int     `json:"page"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Text  string  `json:"text"`
	Size  float64 `json:"size"`
	Bold  bool    `json:"bold"`
	Align string  `json:"align"`
}

func browserRows(t *testing.T) []documents.Row {
	t.Helper()
	var fixture struct {
		Rows []tsRow `json:"rows"`
	}
	b, err := os.ReadFile("testdata/detail_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	var rows []documents.Row
	for _, r := range fixture.Rows {
		if r.ID == "nodate" {
			continue
		}
		rows = append(rows, documents.Row{ID: r.ID, TripRecordID: r.TripRecordID, TripNo: r.SpxTripID,
			DeliveredAt: instant(t, r.DeliveredTimestamp), BillingDate: instant(t, r.BillingDate),
			BillingDateBasis: documents.DateBasis(r.BillingDateBasis), EstimateTHB: r.BillingEstimateThb,
			LookupHubCode: r.BillingLookupHubID, LookupDestinationCode: r.BillingLookupDest, VehicleClass: r.VehicleClass,
			OriginHubCode: r.OriginHubCode, DestinationDisplayName: r.DestinationDisplay, Type: documents.RowType(r.RowType),
			StopIndex: r.StopIndex, JobCategory: r.JobCategory, RoundEffectiveFromDate: r.RoundEffectiveFrom,
			FuelBandLowerTHB: r.FuelBandLower, FuelBandUpperTHB: r.FuelBandUpper, AddTHBPerTrip: r.AddThbPerTrip})
	}
	return rows
}

// TestInvoicePDFMatchesTheBrowser compares every text of the invoice and receipt PDFs with what the browser's
// buildInvoicePdf drew for the same input (testdata/invoice_ts.json, written by invoice_ts.mjs from
// logitrack-web/lib/billingDocumentRender.ts with jsPDF 4 and jspdf-autotable; frozen since F87 removed them, the last
// tree holding them is 4fc3d70e): text, page, start x and baseline y
// within 0.05 mm, size and weight; three scenarios (two price rounds with the legend, the receipt, a single round over
// 45 lines that moves the footer to a second page). Intended differences (main spec §6.18 #6, §9.9): the issue date
// is the statement's (the browser printed the day it ran) and the withholding label prints the stored rate (the
// browser printed "1%" beside a 3 % deduction).
func TestInvoicePDFMatchesTheBrowser(t *testing.T) {
	var golden struct {
		Customer struct {
			Name             string `json:"name"`
			Address          string `json:"address"`
			TaxID            string `json:"taxId"`
			BranchNumber     string `json:"branchNumber"`
			PaymentTermsDays int    `json:"paymentTermsDays"`
			ContactName      string `json:"contactName"`
			ContactPhone     string `json:"contactPhone"`
			InvoiceNote      string `json:"invoiceNote"`
		} `json:"customer"`
		Provider struct {
			Name          string `json:"name"`
			Address       string `json:"address"`
			TaxID         string `json:"taxId"`
			BankName      string `json:"bankName"`
			AccountNumber string `json:"accountNumber"`
			AccountName   string `json:"accountName"`
			SignatoryName string `json:"signatoryName"`
		} `json:"provider"`
		Scenarios map[string][]tsOp `json:"scenarios"`
	}
	b, err := os.ReadFile("testdata/invoice_ts.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &golden); err != nil {
		t.Fatal(err)
	}
	gc, gp := golden.Customer, golden.Provider
	customer := Customer{Name: gc.Name, Address: gc.Address, TaxID: gc.TaxID, BranchType: "branch", BranchNumber: gc.BranchNumber,
		PaymentTermsDays: &gc.PaymentTermsDays, ContactName: gc.ContactName, ContactPhone: gc.ContactPhone, InvoiceNote: gc.InvoiceNote}
	provider := Provider{Name: gp.Name, Address: gp.Address, TaxID: gp.TaxID, BankName: gp.BankName, AccountNumber: gp.AccountNumber,
		AccountName: gp.AccountName, SignatoryName: gp.SignatoryName}
	// The browser's totals of these rows (its float sum, WHT Math.round(sum * 0.03 * 100) / 100).
	totals := documents.DetailTotals{GrandTotal: 4947.16, WithholdingRate: 0.03, WithholdingTax: 148.41, Net: 4798.75}
	issued := time.Date(2026, 9, 30, 17, 0, 0, 0, time.UTC)
	stmt := Statement{InvoiceNumber: "CJSF-202609-001", Year: 2026, Month: 9, IssuedAt: issued, Totals: totals}

	var many []documents.Row
	manyTotal := 0.0
	for i := range 45 {
		est := float64(1000 + i)
		manyTotal += est
		many = append(many, documents.Row{ID: "p" + strconv.Itoa(i), TripRecordID: "p" + strconv.Itoa(i),
			TripNo: "SPX-" + strconv.Itoa(100+i), DeliveredAt: time.Date(2026, 9, 1+i%28, 3, 0, 0, 0, time.UTC), EstimateTHB: est,
			LookupHubCode: "SPK-GW", LookupDestinationCode: ptr("SPK890103"), VehicleClass: ptr("6WH"), OriginHubCode: "SPK-GW",
			DestinationDisplayName: ptr("ลาดกระบัง26"), Type: documents.RowTrip, JobCategory: "PRIMARY",
			RoundEffectiveFromDate: "2026-09-01"})
	}
	// 1000 + ... + 1044 = 45990; WHT 3 % = 1379.70.
	if manyTotal != 45990 {
		t.Fatal(manyTotal)
	}
	wht := documents.DetailTotals{GrandTotal: 45990, WithholdingRate: 0.03, WithholdingTax: 1379.7, Net: 44610.3}
	receiptCustomer := customer
	receiptCustomer.InvoiceNote = ""
	for name, in := range map[string]InvoiceInput{
		"invoice": {Rows: browserRows(t), Customer: customer, Provider: provider, Statement: stmt},
		"receipt": {Rows: browserRows(t), Customer: receiptCustomer, Provider: provider, Statement: stmt, Receipt: true},
		"many": {Rows: many, Customer: Customer{Name: "TTP Co."}, Provider: provider, Statement: Statement{
			InvoiceNumber: "TTP-202609-001", Year: 2026, Month: 9, IssuedAt: issued, Totals: wht}},
	} {
		t.Run(name, func(t *testing.T) {
			_, ops, err := InvoicePDF(fonts(t), in)
			if err != nil {
				t.Fatal(err)
			}
			want := golden.Scenarios[name]
			if len(want) == 0 {
				t.Fatal("no golden")
			}
			matchOps(t, ops, want, func(text string) string {
				switch {
				case strings.HasPrefix(text, "วันที่: "):
					return "วันที่: 01/10/2026" // the statement's date, not the day the browser ran
				case text == "ภาษีหัก ณ ที่จ่าย 1%":
					return "ภาษีหัก ณ ที่จ่าย 3%" // the stored rate (§6.18 #6)
				}
				return text
			})
		})
	}
}

// matchOps compares a PDF's texts with the browser's (each within 0.05 mm, same size and weight), after rewrite maps a
// browser text to the one the server prints on purpose.
func matchOps(t *testing.T, ops []Op, want []tsOp, rewrite func(string) string) {
	t.Helper()
	used := make([]bool, len(ops))
	for _, w := range want {
		text := rewrite(w.Text)
		best := -1
		for i, o := range ops {
			if !used[i] && o.Text == text && o.Page == w.Page && (best < 0 || math.Abs(o.Y-w.Y) < math.Abs(ops[best].Y-w.Y)) {
				best = i
			}
		}
		if best < 0 {
			t.Errorf("page %d: %q (%.2f, %.2f) not drawn", w.Page, w.Text, w.X, w.Y)
			continue
		}
		used[best] = true
		o := ops[best]
		x := o.Left
		if w.Align != "left" {
			x = o.X
			if o.Align != w.Align {
				t.Errorf("%q aligned %s, browser %s", w.Text, o.Align, w.Align)
			}
		}
		if math.Abs(x-w.X) > 0.05 || math.Abs(o.Y-w.Y) > 0.05 || o.Size != w.Size || o.Bold != w.Bold {
			t.Errorf("page %d %q: at (%.3f, %.3f) %vpt bold=%v, browser (%.3f, %.3f) %vpt bold=%v",
				w.Page, w.Text, x, o.Y, o.Size, o.Bold, w.X, w.Y, w.Size, w.Bold)
		}
	}
	for i, o := range ops {
		if !used[i] && o.Text != "" {
			t.Errorf("page %d: %q drawn, the browser did not", o.Page, o.Text)
		}
	}
}

// TestShopeeReportMatchesTheBrowser compares the Shopee report's texts with what the browser's
// generateShopeeExpressReportBlob drew for 70 trips without signed run-sheets (testdata/shopee_ts.json, written by
// shopee_ts.mjs from logitrack-web/lib/shopeeExpressReport.ts, frozen since F87 removed both at 4fc3d70e; the photo pages need the browser's canvas and are pinned by TestShopeeReportLayout): the header, the
// summary table over pages, the fail-loud table and the page numbers. The issue date is the render's.
func TestShopeeReportMatchesTheBrowser(t *testing.T) {
	var golden struct {
		Ops []tsOp `json:"ops"`
	}
	b, err := os.ReadFile("testdata/shopee_ts.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &golden); err != nil {
		t.Fatal(err)
	}
	drivers := []string{"สมชาย ใจดี", "สมหญิง รักงาน", "Somsak"}
	var rows []ShopeeRow
	for i := range 70 {
		r := ShopeeRow{ID: "trip" + leftPad(i) + "xyz", TripNo: "SPX-" + strconv.Itoa(2000+i),
			DeliveredAt: time.Date(2026, 9, 1+i%15, 2+i%5, 0, 0, 0, time.UTC), DriverKey: "d" + strconv.Itoa(i%3),
			DriverName: drivers[i%3], Origin: "J&T EXPRESS บางปู", Destination: "ลาดกระบัง26", VehicleClass: "6WH",
			Plate: "70-" + strconv.Itoa(1000+i)}
		if i%9 == 0 {
			r.TripNo = ""
		}
		if i%4 == 0 {
			r.Destination = "-"
		}
		if i%5 == 0 {
			r.VehicleClass = ""
		}
		if i%6 == 0 {
			r.Plate = ""
		}
		if i%7 != 0 {
			n := int32(10 + i)
			r.ParcelCount = &n
		}
		rows = append(rows, r)
	}
	_, ops, err := ShopeeReportPDF(fonts(t), ShopeeInput{Rows: rows, CustomerName: "TTP Co.", ProviderName: "บริษัท ทดสอบ จำกัด",
		Year: 2026, Month: 9, Half: HalfFirst, IssuedAt: time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	matchOps(t, ops, golden.Ops, func(s string) string {
		if strings.HasPrefix(s, "วันที่ออกรายงาน: ") {
			return "วันที่ออกรายงาน: 01/10/2026"
		}
		return s
	})
}

func leftPad(i int) string {
	s := strconv.Itoa(i)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}
