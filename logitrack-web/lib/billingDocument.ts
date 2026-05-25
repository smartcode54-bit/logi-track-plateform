"use client";
/**
 * Billing Document Generation
 * Produces Invoice PDF, Receipt PDF, and Excel detail sheet — bundled as ZIP.
 *
 * All functions run client-side in the browser.
 *
 * Thai font (Sarabun) is embedded at runtime by fetching /fonts/Sarabun-*.ttf
 * from the public directory. helvetica cannot render Thai Unicode — never use it here.
 */

import { jsPDF } from "jspdf";
import autoTable from "jspdf-autotable";
// eslint-disable-next-line @typescript-eslint/no-require-imports
const bahttext = require("bahttext") as (amount: number) => string;
import * as XLSX from "xlsx";
import { format } from "date-fns";
import { BILLING_PROVIDER, WITHHOLDING_TAX_RATE } from "./billingConfig";

// ─── Provider type (extends BILLING_PROVIDER with optional stamp/signature) ──

export interface BillingProviderInfo {
  name: string;
  address: string;
  taxId: string;
  bankName?: string;
  accountNumber?: string;
  accountName?: string;
  withholdingTaxRate?: number; // 0–100 (%). Falls back to WITHHOLDING_TAX_RATE (1%)
  stampUrl?: string;
  signatureUrl?: string;
  signatoryName?: string;
}

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
  /** Row type for grouping/display: "trip" = normal, "multidrop_stop" = expanded stop, "standby" = จอดรอ */
  rowType?: "trip" | "multidrop_stop" | "standby";
  stopIndex?: number;
}

export interface BillingCustomer {
  id: string;
  name: string;
  address?: string;
  taxId?: string;
  branchType?: string;
  branchNumber?: string;
  contactName?: string;
  contactPhone?: string;
  paymentTermsDays?: number;
  invoiceNote?: string;
}

export interface BillingPeriod {
  /** 1-based month (1–12) */
  month: number;
  year: number;
}

// ─── Thai font loader ────────────────────────────────────────────────────────
// Fonts live in public/fonts/ — fetched once per browser session then cached.

let _fontCache: { regular: string; bold: string } | null = null;

async function loadThaiFont(): Promise<{ regular: string; bold: string }> {
  if (_fontCache) return _fontCache;

  const toBase64 = async (url: string): Promise<string> => {
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`[billingDocument] Font load failed (${resp.status}): ${url}`);
    const buf = await resp.arrayBuffer();
    const bytes = new Uint8Array(buf);
    // Process in 8 KB chunks to stay well within the JS call-stack limit
    let binary = "";
    const CHUNK = 8192;
    for (let i = 0; i < bytes.length; i += CHUNK) {
      const slice = bytes.subarray(i, i + CHUNK);
      for (let j = 0; j < slice.length; j++) binary += String.fromCharCode(slice[j]);
    }
    return btoa(binary);
  };

  const [regular, bold] = await Promise.all([
    toBase64("/fonts/Sarabun-Regular.ttf"),
    toBase64("/fonts/Sarabun-Bold.ttf"),
  ]);

  _fontCache = { regular, bold };
  return _fontCache;
}

/**
 * Fetch a remote image URL and return it as a base64 data string (no data URI prefix).
 * Uses the same 8 KB chunk approach as loadThaiFont.
 * Returns null on any fetch error so callers can skip gracefully.
 */
async function fetchImageAsBase64(url: string): Promise<string | null> {
  try {
    const resp = await fetch(url);
    if (!resp.ok) return null;
    const buf = await resp.arrayBuffer();
    const bytes = new Uint8Array(buf);
    let binary = "";
    const CHUNK = 8192;
    for (let i = 0; i < bytes.length; i += CHUNK) {
      const slice = bytes.subarray(i, i + CHUNK);
      for (let j = 0; j < slice.length; j++) binary += String.fromCharCode(slice[j]);
    }
    return btoa(binary);
  } catch {
    return null;
  }
}

/** Detect image type from URL extension. Defaults to PNG. */
function imageFormat(url: string): "PNG" | "JPEG" {
  return url.toLowerCase().includes(".jpg") || url.toLowerCase().includes(".jpeg") ? "JPEG" : "PNG";
}

/** Register Sarabun (normal + bold + italic-as-normal) on a fresh jsPDF instance. */
function registerThaiFont(doc: jsPDF, font: { regular: string; bold: string }): void {
  doc.addFileToVFS("Sarabun-Regular.ttf", font.regular);
  doc.addFont("Sarabun-Regular.ttf", "Sarabun", "normal");
  doc.addFileToVFS("Sarabun-Bold.ttf", font.bold);
  doc.addFont("Sarabun-Bold.ttf", "Sarabun", "bold");
  // Thai script has no italic convention; reuse regular so setFont("Sarabun","italic") doesn't crash
  doc.addFileToVFS("Sarabun-Italic.ttf", font.regular);
  doc.addFont("Sarabun-Italic.ttf", "Sarabun", "italic");
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

/** Group trips by vehicleClass + route for the invoice body table. Standby rows group separately. */
function groupToLineItems(trips: BillingTripRow[]): {
  vehicleClass: string;
  route: string;
  count: number;
  unitPrice: number;
  total: number;
}[] {
  const map = new Map<string, { vehicleClass: string; route: string; count: number; unitPrice: number; total: number }>();
  for (const t of trips) {
    const isStandby = t.rowType === "standby";
    const vc = isStandby ? "-" : (t.vehicleClass ?? "-");
    const route = isStandby
      ? `จอดรอ (Standby): ${t.hubDisplayName ?? "-"}`
      : [t.hubDisplayName ?? t.billingLookupHubId ?? "-", t.destinationDisplayName ?? t.billingLookupDestination ?? "-"].join(" → ");
    const unitPrice = t.billingBaseRateThb ?? t.billingEstimateThb;
    const key = isStandby ? `standby::${t.billingEstimateThb}` : `${vc}::${route}::${unitPrice}`;
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

async function buildInvoicePdf(
  trips: BillingTripRow[],
  customer: BillingCustomer,
  period: BillingPeriod,
  isReceipt = false,
  invoiceNumberOverride?: string,
  provider?: BillingProviderInfo,
): Promise<jsPDF> {
  // Resolve provider — fall back to hardcoded BILLING_PROVIDER for backward compatibility
  const prov: BillingProviderInfo = provider ?? BILLING_PROVIDER;
  const withholdingRate = (prov.withholdingTaxRate != null ? prov.withholdingTaxRate / 100 : null) ?? WITHHOLDING_TAX_RATE;

  // Load & embed Thai font + optionally prefetch stamp/signature in parallel
  const [font, stampBase64, signatureBase64] = await Promise.all([
    loadThaiFont(),
    prov.stampUrl ? fetchImageAsBase64(prov.stampUrl) : Promise.resolve(null),
    prov.signatureUrl ? fetchImageAsBase64(prov.signatureUrl) : Promise.resolve(null),
  ]);

  const doc = new jsPDF({ orientation: "portrait", unit: "mm", format: "a4" });
  registerThaiFont(doc, font);

  const grandTotal = trips.reduce((s, t) => s + t.billingEstimateThb, 0);
  const withholdingTax = isReceipt ? 0 : Math.round(grandTotal * withholdingRate * 100) / 100;
  const totalNet = grandTotal - withholdingTax;
  const invNumber = invoiceNumberOverride ?? invoiceNumber(period);
  const issuedDate = format(new Date(), "dd/MM/yyyy");

  // ── Title ──
  doc.setFont("Sarabun", "bold");
  if (isReceipt) {
    doc.setFontSize(18);
    doc.text("ใบเสร็จรับเงิน", 105, 20, { align: "center" });
  } else {
    // Two-line title: main doc type + sub label, so they never overlap
    doc.setFontSize(16);
    doc.text("ใบวางบิล", 105, 18, { align: "center" });
    doc.setFontSize(11);
    doc.setFont("Sarabun", "normal");
    doc.text("(ใบแจ้งหนี้)", 105, 26, { align: "center" });
  }

  // ── Provider block (left) ──
  doc.setFontSize(9);
  doc.setFont("Sarabun", "bold");
  let y = 34;
  doc.text(prov.name, 14, y);
  doc.setFont("Sarabun", "normal");
  y += 5;
  doc.text(prov.address, 14, y);
  y += 5;
  doc.text(`Tax ID: ${prov.taxId}`, 14, y);

  // ── Invoice meta (right) ──
  doc.setFont("Sarabun", "bold");
  doc.text(`เลขที่: ${invNumber}`, 196, 32, { align: "right" });
  doc.setFont("Sarabun", "normal");
  doc.text(`วันที่: ${issuedDate}`, 196, 37, { align: "right" });
  doc.text(`ประจำเดือน: ${periodLabel(period)}`, 196, 42, { align: "right" });
  if (customer.paymentTermsDays != null) {
    doc.text(`กำหนดชำระ: ${customer.paymentTermsDays} วัน`, 196, 47, { align: "right" });
  }

  // ── Bill To ──
  y = 55;
  doc.setFont("Sarabun", "bold");
  doc.text("เรียน / Bill To:", 14, y);
  doc.setFont("Sarabun", "normal");
  y += 5;
  doc.text(customer.name, 14, y);
  if (customer.address) {
    y += 5;
    const lines = doc.splitTextToSize(customer.address, 120);
    doc.text(lines, 14, y);
    y += lines.length * 5;
  }
  if (customer.taxId) {
    const branchInfo = customer.branchType
      ? ` (${customer.branchType}${customer.branchNumber ? ` ${customer.branchNumber}` : ""})`
      : "";
    doc.text(`Tax ID: ${customer.taxId}${branchInfo}`, 14, y);
    y += 5;
  }
  if (customer.contactName || customer.contactPhone) {
    const contactLine = [customer.contactName, customer.contactPhone].filter(Boolean).join("  |  ");
    doc.text(`ผู้ติดต่อ: ${contactLine}`, 14, y);
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
    styles: { font: "Sarabun", fontStyle: "normal", fontSize: 9 },
    headStyles: { fillColor: [30, 80, 160], textColor: 255, fontSize: 9, font: "Sarabun", fontStyle: "bold" },
    bodyStyles: { fontSize: 9, font: "Sarabun", fontStyle: "normal" },
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

  doc.setFont("Sarabun", "bold");
  doc.text("ยอดรวมทั้งสิ้น:", rx - 40, finalY);
  doc.text(formatThb(grandTotal), rx, finalY, { align: "right" });

  doc.setFont("Sarabun", "normal");
  if (isReceipt) {
    doc.text("ภาษีหัก ณ ที่จ่าย 1%:", rx - 40, finalY + 7);
    doc.text("-", rx, finalY + 7, { align: "right" });
    doc.setFont("Sarabun", "bold");
    doc.text("ยอดรวมสุทธิ:", rx - 40, finalY + 14);
    doc.text(formatThb(grandTotal), rx, finalY + 14, { align: "right" });
  } else {
    doc.text(`ภาษีหัก ณ ที่จ่าย 1% (${formatThb(grandTotal)})`, rx - 60, finalY + 7);
    doc.text(`- ${formatThb(withholdingTax)}`, rx, finalY + 7, { align: "right" });
    doc.setFont("Sarabun", "bold");
    doc.text("ยอดรวมสุทธิ:", rx - 40, finalY + 14);
    doc.text(formatThb(totalNet), rx, finalY + 14, { align: "right" });
  }

  // ── Thai baht text ──
  const textAmount = isReceipt ? grandTotal : totalNet;
  doc.setFont("Sarabun", "normal");
  doc.setFontSize(9);
  try {
    const bahtStr: string = bahttext(textAmount);
    doc.text(`(${bahtStr})`, 14, finalY + 21);
  } catch {
    doc.text(`(${formatThb(textAmount)} บาท)`, 14, finalY + 21);
  }

  // ── Bank info (invoice only) ──
  const bankName = prov.bankName || BILLING_PROVIDER.bankName;
  const accountNumber = prov.accountNumber || BILLING_PROVIDER.accountNumber;
  const accountName = prov.accountName || BILLING_PROVIDER.accountName;
  if (!isReceipt && bankName) {
    doc.setFont("Sarabun", "normal");
    doc.setFontSize(9);
    doc.text(
      `ชำระโดย: ${bankName} เลขที่ ${accountNumber} ชื่อบัญชี: ${accountName}`,
      14,
      finalY + 28,
    );
  }

  // ── Invoice note ──
  if (customer.invoiceNote) {
    doc.setFont("Sarabun", "normal");
    doc.setFontSize(8);
    const noteLines = doc.splitTextToSize(`หมายเหตุ: ${customer.invoiceNote}`, 180);
    doc.text(noteLines, 14, finalY + 35);
  }

  // ── Signature block ──
  const sigY = finalY + (customer.invoiceNote ? 55 : 45);
  doc.setFont("Sarabun", "normal");
  doc.setFontSize(9);

  // Left column: issuer (stamp + signature)
  const stampX = 40;
  const sigX = 105;  // center
  const receiverX = 170;

  // Stamp image — centered in middle column
  if (stampBase64) {
    try {
      const fmt = imageFormat(prov.stampUrl ?? "");
      // Position stamp centered around the middle column (x=105), above signature line
      doc.addImage(`data:image/${fmt === "JPEG" ? "jpeg" : "png"};base64,${stampBase64}`, fmt, sigX - 18, sigY - 32, 36, 36);
    } catch {
      // silently skip if image corrupt
    }
  }

  // Signature image — above issuer line (left column)
  if (signatureBase64) {
    try {
      const fmt = imageFormat(prov.signatureUrl ?? "");
      doc.addImage(`data:image/${fmt === "JPEG" ? "jpeg" : "png"};base64,${signatureBase64}`, fmt, stampX - 22, sigY - 20, 44, 16);
    } catch {
      // silently skip
    }
  }

  // Signature lines
  doc.line(stampX - 25, sigY, stampX + 25, sigY);
  doc.text(prov.signatoryName ?? "ผู้จัดทำวางบิล", stampX, sigY + 5, { align: "center" });

  doc.line(sigX - 25, sigY, sigX + 25, sigY);
  doc.text("ตราประทับ", sigX, sigY + 5, { align: "center" });

  doc.line(receiverX - 25, sigY, receiverX + 25, sigY);
  doc.text("ผู้รับวางบิล", receiverX, sigY + 5, { align: "center" });

  return doc;
}

// ─── Public API ─────────────────────────────────────────────────────────────

export async function generateInvoiceBlob(
  trips: BillingTripRow[],
  customer: BillingCustomer,
  period: BillingPeriod,
  invoiceNumberOverride?: string,
  provider?: BillingProviderInfo,
): Promise<Blob> {
  return (await buildInvoicePdf(trips, customer, period, false, invoiceNumberOverride, provider)).output("blob") as Blob;
}

export async function generateReceiptBlob(
  trips: BillingTripRow[],
  customer: BillingCustomer,
  period: BillingPeriod,
  invoiceNumberOverride?: string,
  provider?: BillingProviderInfo,
): Promise<Blob> {
  return (await buildInvoicePdf(trips, customer, period, true, invoiceNumberOverride, provider)).output("blob") as Blob;
}

export function generateDetailExcelBuffer(
  trips: BillingTripRow[],
  period: BillingPeriod,
): Uint8Array {
  const mm = String(period.month).padStart(2, "0");
  type ExcelRow = {
    "No.": number | string;
    "วันที่": string;
    "เลขใบงาน": string;
    "เส้นทาง": string;
    "ประเภทรถ": string;
    "ทะเบียน": string;
    "ชื่อคนขับ": string;
    "เบอร์โทร": string;
    "จำนวนรถ": number | string;
    "ราคาขนส่ง/เที่ยว": number | string;
    "หมายเหตุ": string;
  };
  const rows: ExcelRow[] = trips.map((t, i) => {
    const isStandby = t.rowType === "standby";
    const isStop = t.rowType === "multidrop_stop";
    const route = isStandby
      ? `จอดรอ (Standby): ${t.hubDisplayName ?? "-"}`
      : [t.hubDisplayName ?? t.billingLookupHubId ?? "-", t.destinationDisplayName ?? t.billingLookupDestination ?? "-"].join(" → ");
    const note = isStandby ? "จอดรอ" : isStop ? `Multidrop stop ${t.stopIndex ?? ""}` : "";
    return {
      "No.": i + 1,
      "วันที่": t.deliveredTimestamp ? format(t.deliveredTimestamp, "dd/MM/yyyy") : "",
      "เลขใบงาน": t.spxTripId ?? t.id,
      "เส้นทาง": route,
      "ประเภทรถ": isStandby ? "-" : (t.vehicleClass ?? "-"),
      "ทะเบียน": t.truckLicensePlate ?? "-",
      "ชื่อคนขับ": t.driverName ?? "-",
      "เบอร์โทร": t.driverPhone ?? "-",
      "จำนวนรถ": isStandby ? 0 : 1,
      "ราคาขนส่ง/เที่ยว": t.billingEstimateThb,
      "หมายเหตุ": note,
    };
  });

  const grandTotal = trips.reduce((s, t) => s + t.billingEstimateThb, 0);
  const withholdingTax = Math.round(grandTotal * WITHHOLDING_TAX_RATE * 100) / 100;

  // Footer rows — use "" for cells that should appear blank
  rows.push({
    "No.": "", "วันที่": "", "เลขใบงาน": "", "เส้นทาง": "",
    "ประเภทรถ": "", "ทะเบียน": "", "ชื่อคนขับ": "", "เบอร์โทร": "",
    "จำนวนรถ": "", "ราคาขนส่ง/เที่ยว": "", "หมายเหตุ": "",
  });
  rows.push({
    "No.": "", "วันที่": "", "เลขใบงาน": "", "เส้นทาง": "",
    "ประเภทรถ": "", "ทะเบียน": "", "ชื่อคนขับ": "ยอดรวมทั้งหมด", "เบอร์โทร": "",
    "จำนวนรถ": "", "ราคาขนส่ง/เที่ยว": grandTotal, "หมายเหตุ": "",
  });
  rows.push({
    "No.": "", "วันที่": "", "เลขใบงาน": "", "เส้นทาง": "",
    "ประเภทรถ": "", "ทะเบียน": "", "ชื่อคนขับ": "ภาษีหัก ณ ที่จ่าย 1%", "เบอร์โทร": "",
    "จำนวนรถ": "", "ราคาขนส่ง/เที่ยว": -withholdingTax, "หมายเหตุ": "",
  });
  rows.push({
    "No.": "", "วันที่": "", "เลขใบงาน": "", "เส้นทาง": "",
    "ประเภทรถ": "", "ทะเบียน": "", "ชื่อคนขับ": "ยอดสุทธิ", "เบอร์โทร": "",
    "จำนวนรถ": "", "ราคาขนส่ง/เที่ยว": grandTotal - withholdingTax, "หมายเหตุ": "",
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
  invoiceNumberOverride?: string,
  provider?: BillingProviderInfo,
): Promise<void> {
  const mm = String(period.month).padStart(2, "0");
  const zipName = `invoice_CJSF_${period.year}${mm}.zip`;

  // Generate invoice + receipt in parallel (font is cached after first load)
  const [invoiceBlob, receiptBlob, excelBuffer] = await Promise.all([
    generateInvoiceBlob(trips, customer, period, invoiceNumberOverride, provider),
    generateReceiptBlob(trips, customer, period, invoiceNumberOverride, provider),
    Promise.resolve(generateDetailExcelBuffer(trips, period)),
  ]);

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
