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
const { bahttext } = require("bahttext") as { bahttext: (amount: number) => string };
import * as XLSX from "xlsx-js-style";
import { format } from "date-fns";
import { BILLING_PROVIDER, WITHHOLDING_TAX_RATE } from "./billingConfig";

// ─── Provider type (extends BILLING_PROVIDER with optional stamp/signature) ──

export interface BillingProviderInfo {
  name: string;
  /** ชื่อย่อบริษัทเจ้าของ (e.g. "WRT") — shown in the "Sub" column for own-fleet trips (ADR-0005). */
  shortName?: string;
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
  /** ผู้รับเหมา (Sup) ของคนขับ — แสดงในคอลัมน์ Sup ของ Excel detail; "-" ถ้าเป็นรถตัวเอง */
  subcontractorName?: string;
  truckLicensePlate?: string;
  /** trucks/{id} for the vehicle that ran this row — the identity the plate filter matches on
   *  (a plate string is not an identity). See ADR 0005 §4-5. */
  truckId?: string;
  hubDisplayName?: string;
  /** Source-hub CODE (source_id) resolved by the page — used for the J&T origin-code rule (ADR-0005). */
  originHubCode?: string;
  destinationDisplayName?: string;
  /** Row type for grouping/display: "trip" = normal, "multidrop_stop" = expanded stop, "standby" = จอดรอ */
  rowType?: "trip" | "multidrop_stop" | "standby";
  stopIndex?: number;
  /** หลัก/เสริม — SUPPLEMENTARY rows show "เสริม" in หมายเหตุ (ADR-0005). */
  jobCategory?: "PRIMARY" | "SUPPLEMENTARY";
  // Rate round + fuel band, denormalized onto the trip when it was priced (ADR 0009 §4).
  // Read straight off the record: resolving them at render time through
  // `billingFuelAdjustmentId` could print a band that contradicts the frozen amount beside it.
  /** `yyyy-MM-dd` the price of this row last changed — groups rows into rounds. */
  billingRoundEffectiveFromDateStr?: string;
  billingFuelBandLowerThb?: number;
  billingFuelBandUpperThb?: number;
  billingReferenceFuelPriceThb?: number;
}

/** One price round present in a billing period — the invoice legend (ADR 0009 §6). */
export interface BillingRound {
  /** Display label, assigned by date order at render time and never stored. */
  label: string;
  effectiveFromDateStr: string;
  fuelBandLowerThb?: number;
  fuelBandUpperThb?: number;
  addThbPerTrip?: number;
  firstDeliveredAt?: Date;
  lastDeliveredAt?: Date;
}

/**
 * Collect the distinct rounds a period's rows were priced under, oldest first.
 *
 * Labels are derived here rather than stored: a stored `R2` would renumber the moment a round is
 * voided or a back-dated row appears, and would then disagree with an invoice already sent.
 */
export function collectBillingRounds(trips: BillingTripRow[]): BillingRound[] {
  const byDate = new Map<string, BillingRound>();
  for (const t of trips) {
    const key = t.billingRoundEffectiveFromDateStr;
    if (!key) continue;
    const existing = byDate.get(key);
    const d = t.deliveredTimestamp;
    if (existing) {
      if (d) {
        if (!existing.firstDeliveredAt || d < existing.firstDeliveredAt) existing.firstDeliveredAt = d;
        if (!existing.lastDeliveredAt || d > existing.lastDeliveredAt) existing.lastDeliveredAt = d;
      }
      continue;
    }
    byDate.set(key, {
      label: "",
      effectiveFromDateStr: key,
      fuelBandLowerThb: t.billingFuelBandLowerThb,
      fuelBandUpperThb: t.billingFuelBandUpperThb,
      addThbPerTrip: t.billingAddThbPerTrip,
      firstDeliveredAt: d,
      lastDeliveredAt: d,
    });
  }
  return Array.from(byDate.values())
    .sort((a, b) => a.effectiveFromDateStr.localeCompare(b.effectiveFromDateStr))
    .map((r, i) => ({ ...r, label: `R${i + 1}` }));
}

/** `37.01–38.00`, or "-" when the row carries no band (legacy rows, or a percent-only round). */
export function formatFuelBand(lowerThb?: number, upperThb?: number): string {
  if (typeof lowerThb !== "number" || typeof upperThb !== "number") return "-";
  return `${lowerThb.toFixed(2)}–${upperThb.toFixed(2)}`;
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

interface LineItem {
  vehicleClass: string;
  route: string;
  count: number;
  unitPrice: number;
  total: number;
  dates: Date[];
  /** Standby lists each working day (14,17,20); others use a min–max range (2-31). */
  enumerateDays: boolean;
  /** Round label (R1/R2/…) resolved from the legend; "" when the rows carry no round. */
  roundLabel: string;
}

const beYear = (d: Date) => d.getFullYear() + 543;

/**
 * Delivery date label for a line item, in Thai Buddhist year.
 * A statement covers a single billing month, so all dates share month/year.
 *  - range (trips / ค่าโยก): same day → "5/5/2569"; span → "2-31/5/2569"
 *  - enumerated (standby): distinct days → "14,17,20/5/2569"
 */
function formatLineItemDates(dates: Date[], enumerateDays: boolean): string {
  const valid = dates.filter(Boolean).sort((a, b) => a.getTime() - b.getTime());
  if (valid.length === 0) return "-";
  const ref = valid[valid.length - 1];
  const monthYear = `${ref.getMonth() + 1}/${beYear(ref)}`;

  if (enumerateDays) {
    const days = [...new Set(valid.map((d) => d.getDate()))].sort((a, b) => a - b);
    return `${days.join(",")}/${monthYear}`;
  }

  const min = valid[0];
  const max = valid[valid.length - 1];
  const sameMonth = min.getFullYear() === max.getFullYear() && min.getMonth() === max.getMonth();
  if (sameMonth) {
    const dMin = min.getDate();
    const dMax = max.getDate();
    const dayPart = dMin === dMax ? `${dMin}` : `${dMin}-${dMax}`;
    return `${dayPart}/${monthYear}`;
  }
  // Cross-month fallback (rare): spell out both ends fully
  return `${min.getDate()}/${min.getMonth() + 1}/${beYear(min)}-${max.getDate()}/${max.getMonth() + 1}/${beYear(max)}`;
}

/**
 * Group trips by vehicleClass + route for the invoice body table.
 *
 * The grouping key is unchanged (ADR 0009 §6): the round is a *label* on an existing group, not a
 * new way to total, so `count × unitPrice = total` still holds on every line. A route priced in two
 * rounds already produced two lines, because the unit price is part of the key.
 */
export function groupToLineItems(trips: BillingTripRow[], rounds: BillingRound[] = []): LineItem[] {
  const roundLabelByDate = new Map(rounds.map((r) => [r.effectiveFromDateStr, r.label]));
  const map = new Map<string, LineItem>();
  for (const t of trips) {
    const isStandby = t.rowType === "standby";
    const isStop    = t.rowType === "multidrop_stop";
    const vc = t.vehicleClass ?? "-";
    const baseRoute = [
      t.hubDisplayName         ?? t.billingLookupHubId        ?? "-",
      t.destinationDisplayName ?? t.billingLookupDestination  ?? "-",
    ].join(" → ");
    // ค่าโยก (multidrop) จัดกลุ่มรวมเป็นรายการเดียว ไม่ระบุเส้นทาง — แจกแจงด้วยช่วงวันที่เหมือนเที่ยวปกติ
    const route = isStandby ? `${baseRoute} (Stand by)`
                : isStop    ? "ค่าโยก"
                : baseRoute;
    // Use final (adjusted) rate as unit price so quantity × unitPrice = total
    const unitPrice = t.billingEstimateThb;
    // The round joins the key so a line can never span two rounds. Price alone is not enough:
    // two rounds can land on the same unit price for a route (a round that only moved other
    // routes), and those rows would then merge into one line carrying an arbitrary round label.
    const roundKey = t.billingRoundEffectiveFromDateStr ?? "";
    const key = `${vc}::${route}::${unitPrice}::${roundKey}`;
    const d = t.deliveredTimestamp;
    const existing = map.get(key);
    if (existing) {
      existing.count += 1;
      existing.total += t.billingEstimateThb;
      if (d) existing.dates.push(d);
    } else {
      map.set(key, {
        vehicleClass: vc,
        route,
        count: 1,
        unitPrice,
        total: t.billingEstimateThb,
        dates: d ? [d] : [],
        enumerateDays: isStandby,
        roundLabel: roundLabelByDate.get(t.billingRoundEffectiveFromDateStr ?? "") ?? "",
      });
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
  const withholdingTax = Math.round(grandTotal * withholdingRate * 100) / 100;
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

  // ── Rate rounds legend (ADR 0009 §6) ──
  // A month can contain several price rounds. Without this the customer sees the same route on two
  // lines at two prices and no stated reason; the legend is that reason.
  const rounds = collectBillingRounds(trips);
  const showRounds = rounds.length > 1;
  if (showRounds) {
    doc.setFont("Sarabun", "bold");
    doc.setFontSize(9);
    y += 5;
    doc.text("เรตตามช่วงราคาน้ำมัน (ดีเซล)", 14, y);
    doc.setFont("Sarabun", "normal");
    doc.setFontSize(8);
    for (const r of rounds) {
      y += 4.5;
      const span =
        r.firstDeliveredAt && r.lastDeliveredAt
          ? formatLineItemDates([r.firstDeliveredAt, r.lastDeliveredAt], false)
          : r.effectiveFromDateStr;
      const band = formatFuelBand(r.fuelBandLowerThb, r.fuelBandUpperThb);
      const adj =
        typeof r.addThbPerTrip === "number"
          ? `ปรับ ${r.addThbPerTrip >= 0 ? "+" : ""}${formatThb(r.addThbPerTrip)}/เที่ยว`
          : "";
      doc.text(`${r.label}   ${span}   ${band} ฿/L   ${adj}`.trimEnd(), 18, y);
    }
    y += 2;
  }

  // ── Line items table ──
  const lineItems = groupToLineItems(trips, rounds);
  // The `รอบ` column costs ~12mm, which comes out of the auto-width `รายการ` column. It is only
  // drawn when the period actually has more than one round, so a normal month is unchanged.
  const head = showRounds
    ? ["ลำดับ", "รอบ", "ประเภทรถ", "รายการ", "วันที่จัดส่ง", "จำนวน", "ราคา/หน่วย", "รวม"]
    : ["ลำดับ", "ประเภทรถ", "รายการ", "วันที่จัดส่ง", "จำนวน", "ราคา/หน่วย", "รวม"];
  autoTable(doc, {
    startY: Math.max(y + 5, 80),
    head: [head],
    body: lineItems.map((item, i) => {
      const base = [
        item.vehicleClass,
        item.route,
        formatLineItemDates(item.dates, item.enumerateDays),
        String(item.count),
        formatThb(item.unitPrice),
        formatThb(item.total),
      ];
      return showRounds
        ? [String(i + 1), item.roundLabel || "-", ...base]
        : [String(i + 1), ...base];
    }),
    theme: "striped",
    styles: { font: "Sarabun", fontStyle: "normal", fontSize: 9 },
    headStyles: { fillColor: [30, 80, 160], textColor: 255, fontSize: 9, font: "Sarabun", fontStyle: "bold" },
    bodyStyles: { fontSize: 9, font: "Sarabun", fontStyle: "normal" },
    columnStyles: showRounds
      ? {
          0: { cellWidth: 10, halign: "center" },
          1: { cellWidth: 12, halign: "center" },
          2: { cellWidth: 20 },
          4: { cellWidth: 28, halign: "center" },
          5: { cellWidth: 14, halign: "center" },
          6: { cellWidth: 24, halign: "right" },
          7: { cellWidth: 24, halign: "right" },
        }
      : {
          0: { cellWidth: 10, halign: "center" },
          1: { cellWidth: 20 },
          3: { cellWidth: 28, halign: "center" },
          4: { cellWidth: 14, halign: "center" },
          5: { cellWidth: 24, halign: "right" },
          6: { cellWidth: 24, halign: "right" },
        },
  });

  // ── Totals footer ──
  // The table auto-paginates, but everything below (totals, baht text, payment, signatures)
  // is drawn at a fixed offset from the table end. If that block would overflow the page,
  // move the WHOLE block to a fresh page so the signature/stamp section never gets cut off.
  const tableEndY = (doc as jsPDF & { lastAutoTable: { finalY: number } }).lastAutoTable.finalY;
  const pageHeight = doc.internal.pageSize.getHeight();
  // mm needed below `finalY` for totals + baht text + payment + (note) + signature block.
  const footerBlockHeight = customer.invoiceNote ? 86 : 76;
  let finalY: number = tableEndY + 8;
  if (finalY + footerBlockHeight > pageHeight - 10) {
    doc.addPage();
    finalY = 20; // restart the footer block near the top of the new page
  }
  const rx = 196;

  doc.setFont("Sarabun", "bold");
  doc.text("ยอดรวมทั้งสิ้น:", rx - 40, finalY);
  doc.text(formatThb(grandTotal), rx, finalY, { align: "right" });

  doc.setFont("Sarabun", "normal");
  doc.text("ภาษีหัก ณ ที่จ่าย 1%", rx - 60, finalY + 7);
  doc.text(`-${formatThb(withholdingTax)}`, rx, finalY + 7, { align: "right" });
  doc.setFont("Sarabun", "bold");
  doc.text("ยอดรวมสุทธิ:", rx - 40, finalY + 14);
  doc.text(formatThb(totalNet), rx, finalY + 14, { align: "right" });

  // ── Thai baht text ──
  const textAmount = totalNet;
  doc.setFont("Sarabun", "normal");
  doc.setFontSize(9);
  try {
    const bahtStr: string = bahttext(textAmount);
    doc.text(`ยอดรวม : ${bahtStr}`, 14, finalY + 21);
  } catch {
    doc.text(`ยอดรวม : ${formatThb(textAmount)} บาท`, 14, finalY + 21);
  }

  // ── Payment method + bank info ──
  const bankName = prov.bankName || BILLING_PROVIDER.bankName;
  const accountNumber = prov.accountNumber || BILLING_PROVIDER.accountNumber;
  const accountName = prov.accountName || BILLING_PROVIDER.accountName;
  {
    doc.setFont("Sarabun", "normal");
    doc.setFontSize(9);
    const py = finalY + 26;
    doc.text("ชำระโดย", 14, py);

    // Checkbox: เงินสด
    const box = 3.2;
    doc.rect(30, py - box, box, box);
    doc.text("เงินสด", 34.5, py);

    // Checkbox: เงินโอน
    doc.rect(52, py - box, box, box);
    doc.text("เงินโอน", 56.5, py);

    // Bank account info on the next line
    if (bankName) {
      doc.text(
        `ธนาคาร ${bankName}  เลขที่บัญชี ${accountNumber}  ชื่อบัญชี ${accountName}`,
        14,
        py + 6,
      );
    }
  }

  // ── Invoice note ──
  if (customer.invoiceNote) {
    doc.setFont("Sarabun", "normal");
    doc.setFontSize(8);
    const noteLines = doc.splitTextToSize(`หมายเหตุ: ${customer.invoiceNote}`, 180);
    doc.text(noteLines, 14, finalY + 42);
  }

  // ── Signature block ──
  const sigY = finalY + (customer.invoiceNote ? 62 : 52);
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
  doc.text("ผู้จัดทำ/ผู้ตรวจสอบ", stampX, sigY + 10, { align: "center" });
  doc.text("วันที่ ___/___/______", stampX, sigY + 16, { align: "center" });

  doc.line(sigX - 25, sigY, sigX + 25, sigY);
  doc.text("ตราประทับ", sigX, sigY + 5, { align: "center" });
  doc.text("วันที่ ___/___/______", sigX, sigY + 11, { align: "center" });

  doc.line(receiverX - 25, sigY, receiverX + 25, sigY);
  doc.text("ผู้รับวางบิล", receiverX, sigY + 5, { align: "center" });
  doc.text("วันที่ ___/___/______", receiverX, sigY + 11, { align: "center" });

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

const THAI_MONTHS = [
  "มกราคม","กุมภาพันธ์","มีนาคม","เมษายน","พฤษภาคม","มิถุนายน",
  "กรกฎาคม","สิงหาคม","กันยายน","ตุลาคม","พฤศจิกายน","ธันวาคม",
];

export function generateDetailExcelBuffer(
  trips: BillingTripRow[],
  period: BillingPeriod,
  customer?: BillingCustomer,
  provider?: BillingProviderInfo,
): Uint8Array {
  const mm = String(period.month).padStart(2, "0");
  const providerName = provider?.name ?? BILLING_PROVIDER.name;
  const customerName = customer?.name ?? "-";
  const thaiMonth = THAI_MONTHS[period.month - 1];
  const thaiYear  = period.year + 543;

  const grandTotal = trips.reduce((s, t) => s + t.billingEstimateThb, 0);
  const whtRate = provider?.withholdingTaxRate != null
    ? provider.withholdingTaxRate / 100
    : WITHHOLDING_TAX_RATE;
  const withholdingTax = Math.round(grandTotal * whtRate * 100) / 100;

  // ── Display rules ────────────────────────────────────────────────────────────
  // (1) Origin shows the hub CODE (e.g. SPK-GW) for every customer, not the resolved billing name
  //     (J&T EXPRESS บางปู). Was J&T-only; generalised on request 2026-08-04. Destination is
  //     deliberately left as the display NAME — the two ends of the route are not symmetric here.
  // (2) Global: vehicle class PICKUP renders as 4WH.
  const displayVehicleClass = (vc?: string) => ((vc ?? "-") === "PICKUP" ? "4WH" : (vc ?? "-"));
  // "Sub" column: subcontractor name when the trip was run by a subcontractor; otherwise the
  // owner company's short name (e.g. WRT) for own-fleet trips (ADR-0005).
  const ownerShortName = (provider?.shortName ?? "").trim();
  const subText = (subcontractorName?: string) =>
    subcontractorName && subcontractorName.trim() ? subcontractorName.trim() : (ownerShortName || "-");
  // `originHubCode` is the hub's `source_id`, resolved at load time by `fetchBillingTripRows`.
  // Falls back to the raw lookup id, then the display name, so a hub missing from the master data
  // still prints something identifiable rather than "-".
  const originLabel = (t: BillingTripRow) =>
    t.originHubCode || t.billingLookupHubId || t.hubDisplayName || "-";

  // ── Aggregate multidrop_stop rows into one row per trip ──────────────────────
  // The billing page expands a multi-delivery trip into one row per billed stop.
  // For this report we want ONE row per trip: stop 1 = ค่าขนส่ง/เที่ยว (base route),
  // stops 2+ = ค่าโยก (drop fee). Total = base + drop. No detail rows split out.
  const routeOf = (t: BillingTripRow) =>
    [
      originLabel(t),
      t.destinationDisplayName ?? t.billingLookupDestination  ?? "-",
    ].join(" → ");
  const stripStop = (s: string) => s.replace(/[-_]s\d+$/, "");

  interface DetailRow {
    date?: Date;
    jobNo: string;          // เลขใบงาน ("Stand by" สำหรับเที่ยว standby)
    route: string;
    vehicleClass: string;
    plate: string;
    driverName: string;
    sup: string;            // ผู้รับเหมา
    phone: string;
    truckCount: number;     // จำนวนรถ
    dropFeeThb: number;     // ค่าโยก
    transportFeeThb: number;// ค่าขนส่ง/เที่ยว
    totalThb: number;       // ยอดรวม
    round: string;          // รอบ (R1/R2/…) — ADR 0009 §7
    fuelBand: string;       // ช่วงราคาน้ำมัน ("37.01–38.00"); "-" when the row carries no band
    remark: string;         // หมายเหตุ ("เสริม" สำหรับ SUPPLEMENTARY)
  }

  // Rounds are labelled from the same source the invoice legend uses, so R2 on the detail sheet
  // and R2 on the PDF are always the same round.
  const detailRounds = collectBillingRounds(trips);
  const roundLabelByDate = new Map(detailRounds.map((r) => [r.effectiveFromDateStr, r.label]));
  const roundLabelOf = (t: BillingTripRow) =>
    roundLabelByDate.get(t.billingRoundEffectiveFromDateStr ?? "") ?? "-";

  const multidropGroups = new Map<string, BillingTripRow[]>();
  const detailRows: DetailRow[] = [];

  for (const t of trips) {
    if (t.rowType === "multidrop_stop") {
      const pid = stripStop(t.id);
      const arr = multidropGroups.get(pid) ?? [];
      arr.push(t);
      multidropGroups.set(pid, arr);
      continue;
    }
    const isStandby = t.rowType === "standby";
    detailRows.push({
      date: t.deliveredTimestamp,
      jobNo: isStandby ? "Stand by" : (t.spxTripId ?? t.id.slice(0, 12)),
      route: routeOf(t),
      vehicleClass: displayVehicleClass(t.vehicleClass),
      plate: t.truckLicensePlate ?? "-",
      driverName: t.driverName ?? "-",
      sup: subText(t.subcontractorName),
      phone: t.driverPhone ?? "-",
      truckCount: isStandby ? 0 : 1,
      dropFeeThb: 0,
      transportFeeThb: t.billingEstimateThb,
      totalThb: t.billingEstimateThb,
      round: roundLabelOf(t),
      fuelBand: formatFuelBand(t.billingFuelBandLowerThb, t.billingFuelBandUpperThb),
      remark: t.jobCategory === "SUPPLEMENTARY" ? "เสริม" : "",
    });
  }

  for (const stops of multidropGroups.values()) {
    const sorted = [...stops].sort((a, b) => (a.stopIndex ?? 0) - (b.stopIndex ?? 0));
    const base = sorted[0];
    const transportFeeThb = base.billingEstimateThb;                                  // stop 1 = base route
    const dropFeeThb = sorted.slice(1).reduce((s, r) => s + r.billingEstimateThb, 0); // stops 2+ = ค่าโยก
    detailRows.push({
      date: base.deliveredTimestamp,
      jobNo: base.spxTripId ? stripStop(base.spxTripId) : stripStop(base.id).slice(0, 12),
      route: routeOf(base),
      vehicleClass: displayVehicleClass(base.vehicleClass),
      plate: base.truckLicensePlate ?? "-",
      driverName: base.driverName ?? "-",
      sup: subText(base.subcontractorName),
      phone: base.driverPhone ?? "-",
      truckCount: 1,
      dropFeeThb,
      transportFeeThb,
      totalThb: transportFeeThb + dropFeeThb,
      round: roundLabelOf(base),
      fuelBand: formatFuelBand(base.billingFuelBandLowerThb, base.billingFuelBandUpperThb),
      remark: base.jobCategory === "SUPPLEMENTARY" ? "เสริม" : "",
    });
  }

  detailRows.sort((a, b) => (a.date?.getTime() ?? 0) - (b.date?.getTime() ?? 0));

  // ── Column definitions (ตามรูป) ──────────────────────────────────────────────
  const COLS: { header: string; wch: number }[] = [
    { header: "No.",              wch: 5  },
    { header: "วันที่",           wch: 12 },
    { header: "เลขใบงาน",        wch: 18 },
    { header: "เส้นทาง",         wch: 30 },
    { header: "ประเภท",          wch: 9  },
    { header: "ทะเบียน",         wch: 11 },
    { header: "ชื่อคนขับรถ",     wch: 18 },
    { header: "Sub",             wch: 10 },
    { header: "เบอร์รถ",         wch: 13 },
    { header: "จำนวนรถ",         wch: 9  },
    { header: "ค่าโยก",          wch: 11 },
    { header: "ค่าขนส่ง/เที่ยว",  wch: 14 },
    { header: "ยอดรวม",          wch: 14 },
    { header: "รอบ",             wch: 7  },
    { header: "ช่วงราคาน้ำมัน",   wch: 16 },
    { header: "หมายเหตุ",        wch: 14 },
  ];
  const NC = COLS.length;

  // ── Style helpers ────────────────────────────────────────────────────────────
  const BORDER_THIN = { style: "thin", color: { rgb: "BDBDBD" } };
  const ALL_BORDERS = { top: BORDER_THIN, bottom: BORDER_THIN, left: BORDER_THIN, right: BORDER_THIN };
  const HEADER_FILL = { fgColor: { rgb: "1565C0" }, patternType: "solid" };
  const ALT_FILL    = { fgColor: { rgb: "E3F2FD" }, patternType: "solid" };
  const FOOTER_FILL = { fgColor: { rgb: "F5F5F5" }, patternType: "solid" };
  const FONT_BASE   = { name: "TH SarabunPSK", sz: 11 };
  const FONT_HEADER = { ...FONT_BASE, bold: true, color: { rgb: "FFFFFF" } };
  const FONT_TITLE  = { ...FONT_BASE, sz: 13, bold: true };
  const FONT_BOLD   = { ...FONT_BASE, bold: true };

  const cell = (v: string | number, s: object): XLSX.CellObject => ({ v, t: typeof v === "number" ? "n" : "s", s });

  // ── Build rows ───────────────────────────────────────────────────────────────
  const TITLE_STYLE = { font: FONT_TITLE, alignment: { horizontal: "center", vertical: "center" } };
  const SUB_STYLE   = { font: { ...FONT_BASE, sz: 11 }, alignment: { horizontal: "center" } };

  const rows: XLSX.CellObject[][] = [
    [cell(providerName, TITLE_STYLE),  ...Array(NC - 1).fill(cell("", {}))],
    [cell(`รายละเอียดการขนส่ง เดือน${thaiMonth} ${thaiYear}`, TITLE_STYLE), ...Array(NC - 1).fill(cell("", {}))],
    [cell(`ลูกค้า: ${customerName}`, SUB_STYLE), ...Array(NC - 1).fill(cell("", {}))],
    Array(NC).fill(cell("", {})), // blank row
    // Column header row
    COLS.map((c) => cell(c.header, { font: FONT_HEADER, fill: HEADER_FILL, border: ALL_BORDERS, alignment: { horizontal: "center", vertical: "center", wrapText: true } })),
  ];

  // Data rows
  detailRows.forEach((r, i) => {
    const isAlt     = i % 2 === 1;
    const dateFill  = isAlt ? ALT_FILL : { fgColor: { rgb: "FFFFFF" }, patternType: "solid" };
    const ds = { font: FONT_BASE, border: ALL_BORDERS, fill: dateFill };
    const dsNum = { ...ds, alignment: { horizontal: "right" } };
    const dsCenter = { ...ds, alignment: { horizontal: "center" } };

    rows.push([
      cell(i + 1,                                                       dsCenter),
      cell(r.date ? format(r.date, "dd/MM/yyyy") : "",                  ds),
      cell(r.jobNo,                                                     { ...ds, font: { ...FONT_BASE, name: "Consolas", sz: 9 } }),
      cell(r.route,                                                     { ...ds, alignment: { wrapText: true } }),
      cell(r.vehicleClass,                                             dsCenter),
      cell(r.plate,                                                    dsCenter),
      cell(r.driverName,                                              ds),
      cell(r.sup,                                                     dsCenter),
      cell(r.phone,                                                   dsCenter),
      cell(r.truckCount,                                              dsCenter),
      cell(r.dropFeeThb ? r.dropFeeThb : "",                          { ...dsNum, numFmt: "#,##0.00" }), // ค่าโยก: เว้นว่างถ้าไม่มี
      cell(r.transportFeeThb,                                         { ...dsNum, numFmt: "#,##0.00" }),
      cell(r.totalThb,                                                { ...dsNum, numFmt: "#,##0.00" }),
      cell(r.round,                                                    dsCenter), // รอบ (ADR 0009 §7)
      cell(r.fuelBand,                                                 dsCenter), // ช่วงราคาน้ำมัน
      cell(r.remark,                                                   dsCenter), // หมายเหตุ ("เสริม")
    ]);
  });

  // Footer rows
  rows.push(Array(NC).fill(cell("", {})));
  const footerLabelStyle = { font: FONT_BOLD, fill: FOOTER_FILL, border: ALL_BORDERS, alignment: { horizontal: "right" } };
  const footerNumStyle   = { font: FONT_BOLD, fill: FOOTER_FILL, border: ALL_BORDERS, alignment: { horizontal: "right" }, numFmt: "#,##0.00" };
  // Anchored by header name, not by an offset from the last column: the totals must stay under
  // ยอดรวม when columns are appended (ADR 0009 §7 added รอบ + ช่วงราคาน้ำมัน after it).
  const TOTAL_COL = COLS.findIndex((c) => c.header === "ยอดรวม");
  const LABEL_COL = COLS.findIndex((c) => c.header === "ค่าขนส่ง/เที่ยว");
  const makeFooter = (label: string, amount: number): XLSX.CellObject[] => {
    const row: XLSX.CellObject[] = Array(NC).fill(cell("", {}));
    row[LABEL_COL] = { ...cell(label, footerLabelStyle), s: footerLabelStyle } as XLSX.CellObject;
    row[TOTAL_COL] = cell(amount, footerNumStyle);
    return row;
  };
  rows.push(makeFooter("ยอดรวมทั้งหมด", grandTotal));
  rows.push(makeFooter(`ภาษีหัก ณ ที่จ่าย ${Math.round(whtRate * 100)}%`, -withholdingTax));
  rows.push(makeFooter("ยอดสุทธิ", grandTotal - withholdingTax));

  // ── Build worksheet ──────────────────────────────────────────────────────────
  const wb = XLSX.utils.book_new();
  const ws: XLSX.WorkSheet = XLSX.utils.aoa_to_sheet(rows.map((r) => r.map((c) => c.v)));

  // Apply cell styles
  rows.forEach((row, r) => {
    row.forEach((cellObj, c) => {
      const addr = XLSX.utils.encode_cell({ r, c });
      if (!ws[addr]) ws[addr] = { v: cellObj.v, t: cellObj.t ?? "s" };
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      (ws[addr] as any).s = (cellObj as any).s ?? {};
      if ((cellObj as any).numFmt) (ws[addr] as any).z = (cellObj as any).numFmt;
    });
  });

  // Merges for title rows
  ws["!merges"] = [
    { s: { r: 0, c: 0 }, e: { r: 0, c: NC - 1 } },
    { s: { r: 1, c: 0 }, e: { r: 1, c: NC - 1 } },
    { s: { r: 2, c: 0 }, e: { r: 2, c: NC - 1 } },
  ];

  // Column widths
  ws["!cols"] = COLS.map((c) => ({ wch: c.wch }));

  // Row heights
  ws["!rows"] = [
    { hpt: 22 }, // row 0: company name
    { hpt: 20 }, // row 1: title
    { hpt: 18 }, // row 2: customer
    { hpt: 8  }, // row 3: blank
    { hpt: 22 }, // row 4: column headers
  ];

  // ── Page setup: A4 landscape, fit to 1 page wide ─────────────────────────────
  ws["!pageSetup"] = {
    paperSize: 9,          // A4
    orientation: "landscape",
    fitToPage: true,
    fitToWidth: 1,
    fitToHeight: 0,        // unlimited height
    scale: 100,
  };
  ws["!printOptions"] = { gridLines: false };
  ws["!margins"] = { left: 0.5, right: 0.5, top: 0.75, bottom: 0.75, header: 0.3, footer: 0.3 };

  const sheetName = `รายละเอียดขนส่ง ${thaiMonth} ${thaiYear}`.slice(0, 31);
  XLSX.utils.book_append_sheet(wb, ws, sheetName);
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

  // Receipt is generated separately on "Mark as paid" — not bundled here
  const [invoiceBlob, excelBuffer] = await Promise.all([
    generateInvoiceBlob(trips, customer, period, invoiceNumberOverride, provider),
    Promise.resolve(generateDetailExcelBuffer(trips, period, customer, provider)),
  ]);

  const { default: JSZip } = await import("jszip");
  const zip = new JSZip();
  zip.file("invoice_summary.pdf", invoiceBlob);
  zip.file("invoice_detail.xlsx", excelBuffer);

  const zipBlob = await zip.generateAsync({ type: "blob" });
  const url = URL.createObjectURL(zipBlob);
  const a = document.createElement("a");
  a.href = url;
  a.download = zipName;
  a.click();
  URL.revokeObjectURL(url);
}

/** Called by manager on "Mark as paid" — downloads receipt PDF only. */
export async function downloadReceiptPdf(
  trips: BillingTripRow[],
  customer: BillingCustomer,
  period: BillingPeriod,
  invoiceNumberOverride?: string,
  provider?: BillingProviderInfo,
): Promise<void> {
  const mm = String(period.month).padStart(2, "0");
  const blob = await generateReceiptBlob(trips, customer, period, invoiceNumberOverride, provider);
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `receipt_CJSF_${period.year}${mm}.pdf`;
  a.click();
  URL.revokeObjectURL(url);
}
