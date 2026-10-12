package render

import (
	"strconv"
	"strings"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/documents"
)

// Customer is the bill-to block of a statement (BillingCustomer, web:lib/billingDocumentModel.ts:226-237): the
// customer, or the carrier tenant, the billing party bills.
type Customer struct {
	Code             string
	Name             string
	Address          string
	TaxID            string
	BranchType       string // hq | branch (customers.branch_type)
	BranchNumber     string
	ContactName      string
	ContactPhone     string
	PaymentTermsDays *int
	InvoiceNote      string
}

// Provider is the issuing company of a statement (BillingProviderInfo, web:lib/billingDocumentModel.ts:13-27;
// companies): name and address, bank account, signatory, stamp and signature images (nil when the company has none
// or its file cannot be read).
type Provider struct {
	Name          string
	ShortName     string
	Address       string
	TaxID         string
	BankName      string
	AccountNumber string
	AccountName   string
	SignatoryName string
	Stamp         *Image
	Signature     *Image
}

// Fallback provider texts when the statement names no issuing company (BILLING_PROVIDER,
// web:lib/billingConfig.ts:2-9): a statement always has one since T37, the texts only keep the layout of a legacy row.
const (
	fallbackProviderName    = "บริษัท โลจิแทรค จำกัด"
	fallbackProviderAddress = "กรุณาอัปเดตที่อยู่บริษัท"
	fallbackProviderTaxID   = "กรุณาอัปเดต Tax ID"
)

// Statement is what the documents print from the statement row: the invoice number, the period, the date the document
// is issued on and the exact totals (R18: the withholding from the stored rate, never recomputed from the company).
type Statement struct {
	InvoiceNumber string
	Year, Month   int
	IssuedAt      time.Time // the invoice: generated_at; the receipt: paid_at (Bangkok calendar)
	Totals        documents.DetailTotals
}

// InvoiceInput is everything one invoice or receipt PDF needs.
type InvoiceInput struct {
	Rows      []documents.Row
	Customer  Customer
	Provider  Provider
	Statement Statement
	Receipt   bool // ใบเสร็จรับเงิน instead of ใบวางบิล (ใบแจ้งหนี้)
}

// InvoicePDF draws the invoice summary (invoice_summary.pdf) or the receipt (receipt.pdf) of a statement: port of
// buildInvoicePdf (web:lib/billingDocumentRender.ts:62-341) with the browser's coordinates. Differences, all fixes of
// main spec §6.18: the totals and the withholding line come from the statement (#6: the stored rate and amount, label
// "ภาษีหัก ณ ที่จ่าย {rate}%", where the browser printed "1%" and recomputed), the issue date is the statement's (the
// browser printed the day of the download), and dates are on the Bangkok calendar. It returns the PDF and the texts it
// drew.
func InvoicePDF(f Fonts, in InvoiceInput) ([]byte, []Op, error) {
	if len(f.Regular) == 0 || len(f.Bold) == 0 {
		return nil, nil, errNoFonts
	}
	d, err := newPDF(f, in.Statement.IssuedAt)
	if err != nil {
		return nil, nil, err
	}
	prov := in.Provider
	if strings.TrimSpace(prov.Name) == "" {
		prov.Name = fallbackProviderName
	}
	if strings.TrimSpace(prov.Address) == "" {
		prov.Address = fallbackProviderAddress
	}
	if strings.TrimSpace(prov.TaxID) == "" {
		prov.TaxID = fallbackProviderTaxID
	}
	cust := in.Customer
	st := in.Statement

	// ── Title ──
	if in.Receipt {
		d.setFont(true, 18)
		d.text("ใบเสร็จรับเงิน", 105, 20, "center")
	} else {
		d.setFont(true, 16)
		d.text("ใบวางบิล", 105, 18, "center")
		d.setFont(false, 11)
		d.text("(ใบแจ้งหนี้)", 105, 26, "center")
	}

	// ── Provider block (left) ──
	d.setFont(true, 9)
	y := 34.0
	d.text(prov.Name, 14, y, "")
	d.setFont(false, 9)
	y += 5
	d.text(prov.Address, 14, y, "")
	y += 5
	d.text("Tax ID: "+prov.TaxID, 14, y, "")

	// ── Invoice meta (right) ──
	d.setFont(true, 9)
	d.text("เลขที่: "+st.InvoiceNumber, 196, 32, "right")
	d.setFont(false, 9)
	d.text("วันที่: "+documents.DateDMY(st.IssuedAt), 196, 37, "right")
	d.text("ประจำเดือน: "+documents.PeriodLabel(st.Year, st.Month), 196, 42, "right")
	if cust.PaymentTermsDays != nil {
		d.text("กำหนดชำระ: "+strconv.Itoa(*cust.PaymentTermsDays)+" วัน", 196, 47, "right")
	}

	// ── Bill To ──
	y = 55
	d.setFont(true, 9)
	d.text("เรียน / Bill To:", 14, y, "")
	d.setFont(false, 9)
	y += 5
	d.text(cust.Name, 14, y, "")
	if cust.Address != "" {
		y += 5
		ls := d.split(cust.Address, 120)
		d.lines(ls, 14, y)
		y += float64(len(ls)) * 5
	}
	if cust.TaxID != "" {
		branch := ""
		if cust.BranchType != "" {
			branch = " (" + documents.BranchLabel(cust.BranchType)
			if cust.BranchNumber != "" {
				branch += " " + cust.BranchNumber
			}
			branch += ")"
		}
		d.text("Tax ID: "+cust.TaxID+branch, 14, y, "")
		y += 5
	}
	if cust.ContactName != "" || cust.ContactPhone != "" {
		var parts []string
		for _, p := range []string{cust.ContactName, cust.ContactPhone} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		d.text("ผู้ติดต่อ: "+strings.Join(parts, "  |  "), 14, y, "")
		y += 5
	}

	// ── Rate rounds legend (ADR 0009 §6) ──
	rounds := documents.CollectRounds(in.Rows)
	showRounds := len(rounds) > 1
	if showRounds {
		d.setFont(true, 9)
		y += 5
		d.text("เรตตามช่วงราคาน้ำมัน (ดีเซล)", 14, y, "")
		d.setFont(false, 8)
		for _, r := range rounds {
			y += 4.5
			span := r.EffectiveFromDate
			if !r.FirstBillingDate.IsZero() && !r.LastBillingDate.IsZero() {
				span = documents.FormatLineItemDates([]time.Time{r.FirstBillingDate, r.LastBillingDate}, false)
			}
			band := documents.FormatFuelBand(r.FuelBandLowerTHB, r.FuelBandUpperTHB)
			adj := ""
			if r.AddTHBPerTrip != nil {
				sign := ""
				if *r.AddTHBPerTrip >= 0 {
					sign = "+"
				}
				adj = "ปรับ " + sign + documents.FormatTHB(*r.AddTHBPerTrip) + "/เที่ยว"
			}
			d.text(strings.TrimRight(r.Label+"   "+span+"   "+band+" ฿/L   "+adj, " \t"), 18, y, "")
		}
		y += 2
	}

	// ── Line items table ──
	items := documents.GroupToLineItems(in.Rows, rounds)
	dateHead := "วันที่จัดส่ง"
	if documents.BasisOf(in.Rows) == documents.BasisPlan {
		dateHead = "วันที่ตามแผน"
	}
	head := []string{"ลำดับ", "ประเภทรถ", "รายการ", dateHead, "จำนวน", "ราคา/หน่วย", "รวม"}
	cols := map[int]column{0: {10, "center"}, 1: {20, ""}, 3: {28, "center"}, 4: {14, "center"}, 5: {24, "right"}, 6: {24, "right"}}
	if showRounds {
		head = []string{"ลำดับ", "รอบ", "ประเภทรถ", "รายการ", dateHead, "จำนวน", "ราคา/หน่วย", "รวม"}
		cols = map[int]column{0: {10, "center"}, 1: {12, "center"}, 2: {20, ""}, 4: {28, "center"}, 5: {14, "center"},
			6: {24, "right"}, 7: {24, "right"}}
	}
	body := make([][]string, len(items))
	for i, it := range items {
		base := []string{it.VehicleClass, it.Route, documents.FormatLineItemDates(it.Dates, it.EnumerateDays),
			strconv.Itoa(it.Count), documents.FormatTHB(it.UnitPrice), documents.FormatTHB(it.Total)}
		if showRounds {
			label := it.RoundLabel
			if label == "" {
				label = "-"
			}
			body[i] = append([]string{strconv.Itoa(i + 1), label}, base...)
		} else {
			body[i] = append([]string{strconv.Itoa(i + 1)}, base...)
		}
	}
	tableEnd := d.table(max(y+5, 80), head, body, cols, tableStyle{fontSize: 9, headFill: RGB{30, 80, 160},
		headText: white, bodyText: RGB{20, 20, 20}})

	// ── Totals footer: moved whole to a new page when it would not fit ──
	footer := 76.0
	if cust.InvoiceNote != "" {
		footer = 86
	}
	finalY := tableEnd + 8
	if finalY+footer > pageH-10 {
		d.addPage()
		finalY = 20
	}
	const rx = 196.0
	// The totals keep the font size the table found: jspdf-autotable restores the size set before it, which is the
	// legend's 8 pt when the rounds legend was drawn, else the 9 pt of the blocks above (the browser set no size here).
	totalsSize := 9.0
	if showRounds {
		totalsSize = 8
	}
	d.setFont(true, totalsSize)
	d.text("ยอดรวมทั้งสิ้น:", rx-40, finalY, "")
	d.text(documents.FormatTHB(st.Totals.GrandTotal), rx, finalY, "right")
	d.setFont(false, totalsSize)
	d.text(documents.WithholdingLabel(st.Totals.WithholdingRate), rx-60, finalY+7, "")
	d.text("-"+documents.FormatTHB(st.Totals.WithholdingTax), rx, finalY+7, "right")
	d.setFont(true, totalsSize)
	d.text("ยอดรวมสุทธิ:", rx-40, finalY+14, "")
	d.text(documents.FormatTHB(st.Totals.Net), rx, finalY+14, "right")

	// ── Thai baht text ──
	d.setFont(false, 9)
	d.text("ยอดรวม : "+documents.BahtText(st.Totals.Net), 14, finalY+21, "")

	// ── Payment method + bank info ──
	py := finalY + 26
	d.text("ชำระโดย", 14, py, "")
	const box = 3.2
	d.rect(30, py-box, box, box)
	d.text("เงินสด", 34.5, py, "")
	d.rect(52, py-box, box, box)
	d.text("เงินโอน", 56.5, py, "")
	if prov.BankName != "" {
		bank := "ธนาคาร " + prov.BankName + "   เลขที่บัญชี " + prov.AccountNumber + "   ชื่อบัญชี " + prov.AccountName
		d.lines(d.split(bank, 182), 14, py+6)
	}

	// ── Invoice note ──
	if cust.InvoiceNote != "" {
		d.setFont(false, 8)
		d.lines(d.split("หมายเหตุ: "+cust.InvoiceNote, 180), 14, finalY+42)
	}

	// ── Signature block ──
	sigY := finalY + 52
	if cust.InvoiceNote != "" {
		sigY = finalY + 62
	}
	d.setFont(false, 9)
	const stampX, sigX, receiverX = 40.0, 105.0, 170.0
	if prov.Stamp != nil {
		d.image(*prov.Stamp, sigX-18, sigY-32, 36, 36)
	}
	if prov.Signature != nil {
		d.image(*prov.Signature, stampX-22, sigY-20, 44, 16)
	}
	signatory := prov.SignatoryName
	if signatory == "" {
		signatory = "ผู้จัดทำวางบิล"
	}
	d.line(stampX-25, sigY, stampX+25, sigY)
	d.text(signatory, stampX, sigY+5, "center")
	d.text("ผู้จัดทำ/ผู้ตรวจสอบ", stampX, sigY+10, "center")
	d.text("วันที่ ___/___/______", stampX, sigY+16, "center")
	d.line(sigX-25, sigY, sigX+25, sigY)
	d.text("ตราประทับ", sigX, sigY+5, "center")
	d.text("วันที่ ___/___/______", sigX, sigY+11, "center")
	d.line(receiverX-25, sigY, receiverX+25, sigY)
	d.text("ผู้รับวางบิล", receiverX, sigY+5, "center")
	d.text("วันที่ ___/___/______", receiverX, sigY+11, "center")

	b, err := d.bytes()
	return b, d.ops, err
}
