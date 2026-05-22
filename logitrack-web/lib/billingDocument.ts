"use client";
/**
 * Billing Document Generation
 * Produces Invoice PDF, Receipt PDF, and Excel detail sheet — bundled as ZIP.
 *
 * All functions run client-side in the browser.
 */

import { jsPDF } from "jspdf";
import autoTable from "jspdf-autotable";
// eslint-disable-next-line @typescript-eslint/no-require-imports
const bahttext = require("bahttext") as (amount: number) => string;
import * as XLSX from "xlsx";
import { format } from "date-fns";
import { BILLING_PROVIDER, WITHHOLDING_TAX_RATE } from "./billingConfig";

// ─── Data types ─────────────────────────────────────────────────────────────

export interface BillingTripRow {
  id: string;
  taskId?: string;
  spxTripId?: string;
  deliveredTimestamp?: Date;
  billingEstimateThb: number;
  billingBaseRateThb?: number;
  billingLookupHubId?: string;
  billingLookupDestination?: string;
  billingRateMultiplier?: number;
  billingAddThbPerTrip?: number;
  billingCustomerId?: string;
  vehicleClass?: string;
  driverName?: string;
  driverPhone?: string;
  truckLicensePlate?: string;
  hubDisplayName?: string;
  destinationDisplayName?: string;
}

export interface BillingCustomer {
  id: string;
  name: string;
  address?: string;
  taxId?: string;
  branchType?: string;
  branchNumber?: string;
}

export interface BillingPeriod {
  /** 1-based month (1–12) */
  month: number;
  year: number;
}

// ─── Helpers ────────────────────────────────────────────────────────────────

function formatThb(amount: number): string {
  return new Intl.NumberFormat("th-TH", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(amount);
}

function periodLabel(period: BillingPeriod): string {
  const monthNames = [
    "มกราคม", "กุมภาพันธ์", "มีนาคม", "เมษายน", "พฤษภาคม", "มิถุนายน",
    "กรกฎาคม", "สิงหาคม", "กันยายน", "ตุลาคม", "พฤศจิกายน", "ธันวาคม",
  ];
  return `${monthNames[period.month - 1]} ${period.year}`;
}

function invoiceNumber(period: BillingPeriod): string {
  const mm = String(period.month).padStart(2, "0");
  const seq = String(Date.now()).slice(-4);
  return `INV-${period.year}${mm}-${seq}`;
}

/** Group trips by vehicleClass + route for the invoice body table */
function groupToLineItems(trips: BillingTripRow[]): {
  vehicleClass: string;
  route: string;
  count: number;
  unitPrice: number;
  total: number;
}[] {
  const map = new Map<string, { vehicleClass: string; route: string; count: number; unitPrice: number; total: number }>();
  for (const t of trips) {
    const vc = t.vehicleClass ?? "-";
    const route = [t.hubDisplayName ?? t.billingLookupHubId ?? "-", t.destinationDisplayName ?? t.billingLookupDestination ?? "-"].join(" → ");
    const unitPrice = t.billingBaseRateThb ?? t.billingEstimateThb;
    const key = `${vc}::${route}::${unitPrice}`;
    const existing = map.get(key);
    if (existing) {
      existing.count += 1;
      existing.total += t.billingEstimateThb;
    } else {
      map.set(key, { vehicleClass: vc, route, count: 1, unitPrice, total: t.billingEstimateThb });
    }
  }
  return Array.from(map.values());
}

// ─── PDF generation ─────────────────────────────────────────────────────────

function buildInvoicePdf(
  trips: BillingTripRow[],
  customer: BillingCustomer,
  period: BillingPeriod,
  isReceipt = false,
): jsPDF {
  const doc = new jsPDF({ orientation: "portrait", unit: "mm", format: "a4" });

  const grandTotal = trips.reduce((s, t) => s + t.billingEstimateThb, 0);
  const withholdingTax = isReceipt ? 0 : Math.round(grandTotal * WITHHOLDING_TAX_RATE * 100) / 100;
  const totalNet = grandTotal - withholdingTax;
  const invNumber = invoiceNumber(period);
  const issuedDate = format(new Date(), "dd/MM/yyyy");

  const docTitle = isReceipt ? "ใบเสร็จรับเงิน" : "ใบวางบิล / ใบแจ้งหนี้";

  // ── Title ──
  doc.setFont("helvetica", "bold");
  doc.setFontSize(18);
  doc.text(docTitle, 105, 20, { align: "center" });

  // ── Provider block (left) ──
  doc.setFontSize(9);
  doc.setFont("helvetica", "normal");
  let y = 32;
  doc.setFont("helvetica", "bold");
  doc.text(BILLING_PROVIDER.name, 14, y);
  doc.setFont("helvetica", "normal");
  y += 5;
  doc.text(BILLING_PROVIDER.address, 14, y);
  y += 5;
  doc.text(`Tax ID: ${BILLING_PROVIDER.taxId}`, 14, y);

  // ── Invoice meta (right) ──
  doc.setFont("helvetica", "bold");
  doc.text(`เลขที่: ${invNumber}`, 196, 32, { align: "right" });
  doc.setFont("helvetica", "normal");
  doc.text(`วันที่: ${issuedDate}`, 196, 37, { align: "right" });
  doc.text(`ประจำเดือน: ${periodLabel(period)}`, 196, 42, { align: "right" });

  // ── Bill To ──
  y = 55;
  doc.setFont("helvetica", "bold");
  doc.text("เรียน / Bill To:", 14, y);
  doc.setFont("helvetica", "normal");
  y += 5;
  doc.text(customer.name, 14, y);
  if (customer.address) {
    y += 5;
    const lines = doc.splitTextToSize(customer.address, 120);
    doc.text(lines, 14, y);
    y += lines.length * 5;
  }
  if (customer.taxId) {
    doc.text(`Tax ID: ${customer.taxId}${customer.branchType ? ` (${customer.branchType})` : ""}`, 14, y);
    y += 5;
  }

  // ── Line items table ──
  const lineItems = groupToLineItems(trips);
  autoTable(doc, {
    startY: Math.max(y + 5, 80),
    head: [["ลำดับ", "ประเภทรถ", "รายการ", "จำนวน", "ราคา/หน่วย", "รวม"]],
    body: lineItems.map((item, i) => [
      String(i + 1),
      item.vehicleClass,
      item.route,
      String(item.count),
      formatThb(item.unitPrice),
      formatThb(item.total),
    ]),
    theme: "striped",
    headStyles: { fillColor: [30, 80, 160], textColor: 255, fontSize: 9 },
    bodyStyles: { fontSize: 9 },
    columnStyles: {
      0: { cellWidth: 12, halign: "center" },
      1: { cellWidth: 25 },
      3: { cellWidth: 18, halign: "center" },
      4: { cellWidth: 28, halign: "right" },
      5: { cellWidth: 28, halign: "right" },
    },
  });

  // ── Totals footer ──
  const finalY: number = (doc as jsPDF & { lastAutoTable: { finalY: number } }).lastAutoTable.finalY + 8;
  const rx = 196;

  doc.setFont("helvetica", "bold");
  doc.text("ยอดรวมทั้งสิ้น:", rx - 40, finalY);
  doc.text(formatThb(grandTotal), rx, finalY, { align: "right" });

  doc.setFont("helvetica", "normal");
  if (isReceipt) {
    doc.text("ภาษีหัก ณ ที่จ่าย 1%:", rx - 40, finalY + 7);
    doc.text("-", rx, finalY + 7, { align: "right" });
    doc.setFont("helvetica", "bold");
    doc.text("ยอดรวมสุทธิ:", rx - 40, finalY + 14);
    doc.text(formatThb(grandTotal), rx, finalY + 14, { align: "right" });
  } else {
    doc.text(`ภาษีหัก ณ ที่จ่าย 1% (${formatThb(grandTotal)})`, rx - 60, finalY + 7);
    doc.text(`- ${formatThb(withholdingTax)}`, rx, finalY + 7, { align: "right" });
    doc.setFont("helvetica", "bold");
    doc.text("ยอดรวมสุทธิ:", rx - 40, finalY + 14);
    doc.text(formatThb(totalNet), rx, finalY + 14, { align: "right" });
  }

  // ── Thai baht text ──
  const textAmount = isReceipt ? grandTotal : totalNet;
  doc.setFont("helvetica", "italic");
  doc.setFontSize(9);
  try {
    const bahtStr: string = bahttext(textAmount);
    doc.text(`(${bahtStr})`, 14, finalY + 21);
  } catch {
    doc.text(`(${formatThb(textAmount)} บาท)`, 14, finalY + 21);
  }

  // ── Bank info (invoice only) ──
  if (!isReceipt && BILLING_PROVIDER.bankName) {
    doc.setFont("helvetica", "normal");
    doc.setFontSize(9);
    doc.text(`ชำระโดย: ${BILLING_PROVIDER.bankName} เลขที่ ${BILLING_PROVIDER.accountNumber} ชื่อบัญชี: ${BILLING_PROVIDER.accountName}`, 14, finalY + 28);
  }

  // ── Signature block ──
  const sigY = finalY + 45;
  doc.setFont("helvetica", "normal");
  doc.setFontSize(9);
  const cols = [40, 105, 170];
  const labels = ["ผู้จัดทำวางบิล", "ตราประทับ", "ผู้รับวางบิล"];
  for (let i = 0; i < 3; i++) {
    doc.line(cols[i] - 25, sigY, cols[i] + 25, sigY);
    doc.text(labels[i], cols[i], sigY + 5, { align: "center" });
  }

  return doc;
}

// ─── Public API ─────────────────────────────────────────────────────────────

export function generateInvoiceBlob(
  trips: BillingTripRow[],
  customer: BillingCustomer,
  period: BillingPeriod,
): Blob {
  return buildInvoicePdf(trips, customer, period, false).output("blob") as Blob;
}

export function generateReceiptBlob(
  trips: BillingTripRow[],
  customer: BillingCustomer,
  period: BillingPeriod,
): Blob {
  return buildInvoicePdf(trips, customer, period, true).output("blob") as Blob;
}

export function generateDetailExcelBuffer(
  trips: BillingTripRow[],
  period: BillingPeriod,
): Uint8Array {
  const mm = String(period.month).padStart(2, "0");
  const rows = trips.map((t, i) => ({
    "No.": i + 1,
    "วันที่": t.deliveredTimestamp ? format(t.deliveredTimestamp, "dd/MM/yyyy") : "",
    "เลขใบงาน": t.spxTripId ?? t.id,
    "เส้นทาง": [t.hubDisplayName ?? t.billingLookupHubId ?? "-", t.destinationDisplayName ?? t.billingLookupDestination ?? "-"].join(" → "),
    "ประเภทรถ": t.vehicleClass ?? "-",
    "ทะเบียน": t.truckLicensePlate ?? "-",
    "ชื่อคนขับ": t.driverName ?? "-",
    "เบอร์โทร": t.driverPhone ?? "-",
    "จำนวนรถ": 1,
    "ราคาขนส่ง/เที่ยว": t.billingEstimateThb,
    "หมายเหตุ": "",
  }));

  const grandTotal = trips.reduce((s, t) => s + t.billingEstimateThb, 0);
  const withholdingTax = Math.round(grandTotal * WITHHOLDING_TAX_RATE * 100) / 100;

  // Footer rows
  rows.push({
    "No.": NaN,
    "วันที่": "",
    "เลขใบงาน": "",
    "เส้นทาง": "",
    "ประเภทรถ": "",
    "ทะเบียน": "",
    "ชื่อคนขับ": "",
    "เบอร์โทร": "",
    "จำนวนรถ": NaN,
    "ราคาขนส่ง/เที่ยว": NaN,
    "หมายเหตุ": "",
  });
  rows.push({
    "No.": NaN,
    "วันที่": "",
    "เลขใบงาน": "",
    "เส้นทาง": "",
    "ประเภทรถ": "",
    "ทะเบียน": "",
    "ชื่อคนขับ": "ยอดรวมทั้งหมด",
    "เบอร์โทร": "",
    "จำนวนรถ": NaN,
    "ราคาขนส่ง/เที่ยว": grandTotal,
    "หมายเหตุ": "",
  });
  rows.push({
    "No.": NaN,
    "วันที่": "",
    "เลขใบงาน": "",
    "เส้นทาง": "",
    "ประเภทรถ": "",
    "ทะเบียน": "",
    "ชื่อคนขับ": "ภาษีหัก ณ ที่จ่าย 1%",
    "เบอร์โทร": "",
    "จำนวนรถ": NaN,
    "ราคาขนส่ง/เที่ยว": -withholdingTax,
    "หมายเหตุ": "",
  });
  rows.push({
    "No.": NaN,
    "วันที่": "",
    "เลขใบงาน": "",
    "เส้นทาง": "",
    "ประเภทรถ": "",
    "ทะเบียน": "",
    "ชื่อคนขับ": "ยอดสุทธิ",
    "เบอร์โทร": "",
    "จำนวนรถ": NaN,
    "ราคาขนส่ง/เที่ยว": grandTotal - withholdingTax,
    "หมายเหตุ": "",
  });

  const wb = XLSX.utils.book_new();
  const ws = XLSX.utils.json_to_sheet(rows);

  // Column widths
  ws["!cols"] = [
    { wch: 5 }, { wch: 12 }, { wch: 18 }, { wch: 30 }, { wch: 12 },
    { wch: 12 }, { wch: 20 }, { wch: 14 }, { wch: 10 }, { wch: 18 }, { wch: 16 },
  ];

  XLSX.utils.book_append_sheet(wb, ws, `รายละเอียด ${mm}-${period.year}`);
  return XLSX.write(wb, { type: "array", bookType: "xlsx" }) as Uint8Array;
}

export async function downloadBillingZip(
  trips: BillingTripRow[],
  customer: BillingCustomer,
  period: BillingPeriod,
): Promise<void> {
  const mm = String(period.month).padStart(2, "0");
  const zipName = `invoice_CJSF_${period.year}${mm}.zip`;

  const [invoiceBlob, receiptBlob, excelBuffer] = [
    generateInvoiceBlob(trips, customer, period),
    generateReceiptBlob(trips, customer, period),
    generateDetailExcelBuffer(trips, period),
  ];

  const { default: JSZip } = await import("jszip");
  const zip = new JSZip();
  zip.file("invoice_summary.pdf", invoiceBlob);
  zip.file("receipt.pdf", receiptBlob);
  zip.file("invoice_detail.xlsx", excelBuffer);

  const zipBlob = await zip.generateAsync({ type: "blob" });
  const url = URL.createObjectURL(zipBlob);
  const a = document.createElement("a");
  a.href = url;
  a.download = zipName;
  a.click();
  URL.revokeObjectURL(url);
}
