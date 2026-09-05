"use client";
/**
 * Shopee Express (TTP) billing support report — a single combined PDF handed to the
 * customer alongside the invoice. It is deliberately PRICE-FREE.
 *
 * Two parts, one PDF:
 *   1. สรุปเที่ยววิ่ง (ไม่มีราคา) — a manifest of every delivered trip in the month.
 *   2. รูปรันชีพที่เซ็นแล้ว แยกตามคนขับ — the signed run-sheets (runsheet_received),
 *      grouped by driver.
 *
 * Runs client-side. Thai text is rendered with Sarabun (helvetica cannot render Thai).
 */

import { jsPDF } from "jspdf";
import autoTable from "jspdf-autotable";
import { format } from "date-fns";
import { loadThaiFont, registerThaiFont, fetchImageAsBase64, imageFormat } from "./pdfThai";
import type { BillingProviderInfo, BillingCustomer, BillingPeriod } from "./billingDocument";
import type { ShopeeReportTripRow, BillingHalf } from "@/features/accounting";

// ─── Layout constants (A4 portrait, mm) ───────────────────────────────────────
const PAGE_W = 210;
const PAGE_H = 297;
const MARGIN = 12;
const CONTENT_W = PAGE_W - MARGIN * 2;

const THAI_MONTHS = [
  "มกราคม", "กุมภาพันธ์", "มีนาคม", "เมษายน", "พฤษภาคม", "มิถุนายน",
  "กรกฎาคม", "สิงหาคม", "กันยายน", "ตุลาคม", "พฤศจิกายน", "ธันวาคม",
];

function periodLabelBe(period: BillingPeriod): string {
  return `${THAI_MONTHS[period.month - 1]} ${period.year + 543}`;
}

/** Round suffix for the header, in Thai. "full" prints nothing. */
function roundLabel(half: BillingHalf): string {
  if (half === "first") return "(รอบ 1–15)";
  if (half === "second") return "(รอบ 16–สิ้นเดือน)";
  return "";
}

// ─── Image downscale (offscreen canvas) ───────────────────────────────────────
// Signed run-sheets are camera photos (multi-MB). Embedding them raw would produce a
// huge PDF, so each is redrawn to a bounded JPEG before addImage.

interface ScaledImage {
  dataUrl: string;
  w: number;
  h: number;
}

async function loadAndDownscale(
  base64: string,
  fmt: "PNG" | "JPEG",
  // Run-sheets now print at full page width, so keep more resolution for legible text/signatures.
  maxDim = 1600,
  quality = 0.75
): Promise<ScaledImage | null> {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => {
      const scale = Math.min(1, maxDim / Math.max(img.width, img.height));
      const w = Math.max(1, Math.round(img.width * scale));
      const h = Math.max(1, Math.round(img.height * scale));
      const canvas = document.createElement("canvas");
      canvas.width = w;
      canvas.height = h;
      const ctx = canvas.getContext("2d");
      if (!ctx) {
        resolve(null);
        return;
      }
      ctx.fillStyle = "#ffffff";
      ctx.fillRect(0, 0, w, h);
      ctx.drawImage(img, 0, 0, w, h);
      try {
        resolve({ dataUrl: canvas.toDataURL("image/jpeg", quality), w, h });
      } catch {
        resolve(null);
      }
    };
    img.onerror = () => resolve(null);
    img.src = `data:image/${fmt === "JPEG" ? "jpeg" : "png"};base64,${base64}`;
  });
}

/** Fetch + downscale every unique photo URL, in bounded-concurrency batches. */
async function prefetchImages(urls: string[]): Promise<Map<string, ScaledImage>> {
  const unique = Array.from(new Set(urls));
  const byUrl = new Map<string, ScaledImage>();
  const CONCURRENCY = 6;
  for (let i = 0; i < unique.length; i += CONCURRENCY) {
    const slice = unique.slice(i, i + CONCURRENCY);
    const results = await Promise.all(
      slice.map(async (url) => {
        const b64 = await fetchImageAsBase64(url);
        if (!b64) return { url, img: null as ScaledImage | null };
        const img = await loadAndDownscale(b64, imageFormat(url));
        return { url, img };
      })
    );
    for (const r of results) if (r.img) byUrl.set(r.url, r.img);
  }
  return byUrl;
}

// ─── Grouping ──────────────────────────────────────────────────────────────────
interface DriverGroup {
  key: string;
  name: string;
  trips: ShopeeReportTripRow[];
}

function groupByDriver(rows: ShopeeReportTripRow[]): DriverGroup[] {
  const map = new Map<string, DriverGroup>();
  const order: string[] = [];
  for (const r of rows) {
    const key = (r.driverId?.trim() || r.driverName || "-").toLowerCase();
    let g = map.get(key);
    if (!g) {
      g = { key, name: r.driverName || "-", trips: [] };
      map.set(key, g);
      order.push(key);
    }
    g.trips.push(r);
  }
  return order.map((k) => map.get(k)!);
}

const routeOf = (r: ShopeeReportTripRow) =>
  [r.originDisplay, r.destinationDisplay].filter((v) => v && v !== "-").join(" → ") || "-";
const dateOf = (r: ShopeeReportTripRow) =>
  r.deliveredTimestamp ? format(r.deliveredTimestamp, "dd/MM/yyyy") : "-";
const jobNoOf = (r: ShopeeReportTripRow) => r.spxTripId ?? r.id.slice(0, 10);

// ─── PDF build ───────────────────────────────────────────────────────────────
export async function generateShopeeExpressReportBlob(
  rows: ShopeeReportTripRow[],
  customer: BillingCustomer,
  period: BillingPeriod,
  provider?: BillingProviderInfo,
  half: BillingHalf = "full"
): Promise<Blob> {
  const font = await loadThaiFont();

  // Prefetch images before drawing so the page loop is synchronous.
  const imageByUrl = await prefetchImages(rows.flatMap((r) => r.signedRunsheetPhotos));

  const doc = new jsPDF({ orientation: "portrait", unit: "mm", format: "a4" });
  registerThaiFont(doc, font);

  const groups = groupByDriver(rows);
  const missing = rows.filter((r) => r.signedRunsheetPhotos.length === 0);
  const totalPhotos = rows.reduce((s, r) => s + r.signedRunsheetPhotos.length, 0);
  const issuedDate = format(new Date(), "dd/MM/yyyy");

  // ── Cover / header ──
  doc.setFont("Sarabun", "bold");
  doc.setFontSize(16);
  doc.text("รายงานประกอบการวางบิล — Shopee Express", PAGE_W / 2, 20, { align: "center" });
  doc.setFontSize(11);
  doc.setFont("Sarabun", "normal");
  if (provider?.name) doc.text(provider.name, PAGE_W / 2, 27, { align: "center" });

  let y = 38;
  doc.setFontSize(10);
  doc.setFont("Sarabun", "bold");
  doc.text(`ลูกค้า: ${customer.name}`, MARGIN, y);
  doc.setFont("Sarabun", "normal");
  doc.text(`ประจำเดือน: ${periodLabelBe(period)} ${roundLabel(half)}`.trimEnd(), PAGE_W - MARGIN, y, {
    align: "right",
  });
  y += 6;
  doc.text(`จำนวนเที่ยว: ${rows.length} เที่ยว`, MARGIN, y);
  doc.text(`คนขับ: ${groups.length} คน`, MARGIN + 55, y);
  doc.text(`รูปรันชีพที่เซ็น: ${totalPhotos} รูป`, MARGIN + 95, y);
  doc.text(`วันที่ออกรายงาน: ${issuedDate}`, PAGE_W - MARGIN, y, { align: "right" });

  // ── Section 1: trip summary (NO price) ──
  y += 9;
  doc.setFont("Sarabun", "bold");
  doc.setFontSize(12);
  doc.text("สรุปเที่ยววิ่ง", MARGIN, y);
  doc.setFont("Sarabun", "normal");
  doc.setFontSize(9);
  doc.text("(ไม่แสดงราคา)", MARGIN + 26, y);

  autoTable(doc, {
    startY: y + 3,
    head: [["No.", "วันที่ส่ง", "เลขงาน", "เส้นทาง", "ประเภทรถ", "ทะเบียน", "คนขับ", "พัสดุ"]],
    body: rows.map((r, i) => [
      String(i + 1),
      dateOf(r),
      jobNoOf(r),
      routeOf(r),
      r.vehicleClass ?? "-",
      r.truckLicensePlate ?? "-",
      r.driverName,
      r.parcelCount != null ? String(r.parcelCount) : "-",
    ]),
    theme: "striped",
    styles: { font: "Sarabun", fontStyle: "normal", fontSize: 8, cellPadding: 1.5 },
    headStyles: { fillColor: [30, 80, 160], textColor: 255, font: "Sarabun", fontStyle: "bold", fontSize: 8 },
    bodyStyles: { font: "Sarabun", fontStyle: "normal", fontSize: 8 },
    margin: { left: MARGIN, right: MARGIN },
    columnStyles: {
      0: { cellWidth: 9, halign: "center" },
      1: { cellWidth: 20, halign: "center" },
      2: { cellWidth: 26 },
      4: { cellWidth: 18, halign: "center" },
      5: { cellWidth: 20, halign: "center" },
      7: { cellWidth: 12, halign: "center" },
    },
  });

  let afterTableY = (doc as jsPDF & { lastAutoTable: { finalY: number } }).lastAutoTable.finalY + 6;

  // ── Fail-loud: trips with no signed run-sheet ──
  if (missing.length > 0) {
    if (afterTableY > PAGE_H - 40) {
      doc.addPage();
      afterTableY = 20;
    }
    doc.setFont("Sarabun", "bold");
    doc.setFontSize(10);
    doc.setTextColor(180, 30, 30);
    doc.text(`เที่ยวที่ยังไม่มีรูปรันชีพที่เซ็น (${missing.length})`, MARGIN, afterTableY);
    doc.setTextColor(0, 0, 0);
    autoTable(doc, {
      startY: afterTableY + 2,
      head: [["No.", "วันที่ส่ง", "เลขงาน", "เส้นทาง", "คนขับ"]],
      body: missing.map((r, i) => [String(i + 1), dateOf(r), jobNoOf(r), routeOf(r), r.driverName]),
      theme: "grid",
      styles: { font: "Sarabun", fontStyle: "normal", fontSize: 8, cellPadding: 1.5 },
      headStyles: { fillColor: [180, 30, 30], textColor: 255, font: "Sarabun", fontStyle: "bold", fontSize: 8 },
      bodyStyles: { font: "Sarabun", fontStyle: "normal", fontSize: 8 },
      margin: { left: MARGIN, right: MARGIN },
      columnStyles: { 0: { cellWidth: 9, halign: "center" }, 1: { cellWidth: 22, halign: "center" } },
    });
  }

  // ── Section 2: signed run-sheets, grouped by driver ──
  // 2 run-sheets per page, stacked top + bottom (1 column × 2 rows). Full page width
  // per image so each is as large as possible. Each cell: caption line + image fit-to-box.
  const COLS = 1;
  const ROWS = 2;
  const GAP = 6;
  const CAPTION_H = 5;
  const cellW = (CONTENT_W - GAP * (COLS - 1)) / COLS;

  for (const g of groups) {
    const items = g.trips.flatMap((trip) =>
      trip.signedRunsheetPhotos
        .map((url) => ({ trip, img: imageByUrl.get(url) }))
        .filter((x): x is { trip: ShopeeReportTripRow; img: ScaledImage } => !!x.img)
    );
    if (items.length === 0) continue; // drivers with no signed photos are covered by the fail-loud block

    doc.addPage();
    // Driver header (top of the driver's first page)
    const drawDriverHeader = () => {
      doc.setFont("Sarabun", "bold");
      doc.setFontSize(12);
      doc.text(`คนขับ: ${g.name}`, MARGIN, 16);
      doc.setFont("Sarabun", "normal");
      doc.setFontSize(9);
      doc.text(`${g.trips.length} เที่ยว · ${items.length} รูป`, PAGE_W - MARGIN, 16, { align: "right" });
    };
    drawDriverHeader();

    const gridTop = 22;
    const gridH = PAGE_H - gridTop - MARGIN;
    const rowH = (gridH - GAP * (ROWS - 1)) / ROWS;
    const imgBoxH = rowH - CAPTION_H;

    let idx = 0;
    for (const item of items) {
      const posOnPage = idx % (COLS * ROWS);
      if (idx > 0 && posOnPage === 0) {
        doc.addPage();
        drawDriverHeader();
      }
      const col = posOnPage % COLS;
      const row = Math.floor(posOnPage / COLS);
      const cellX = MARGIN + col * (cellW + GAP);
      const cellY = gridTop + row * (rowH + GAP);

      // Caption
      doc.setFont("Sarabun", "normal");
      doc.setFontSize(7.5);
      const caption = `${jobNoOf(item.trip)} · ${dateOf(item.trip)} · ${routeOf(item.trip)}`;
      doc.text(doc.splitTextToSize(caption, cellW)[0], cellX, cellY + 3.5);

      // Image fit-to-box (top-aligned, horizontally centered)
      const boxY = cellY + CAPTION_H;
      const ratio = Math.min(cellW / item.img.w, imgBoxH / item.img.h);
      const drawW = item.img.w * ratio;
      const drawH = item.img.h * ratio;
      const drawX = cellX + (cellW - drawW) / 2;
      try {
        doc.addImage(item.img.dataUrl, "JPEG", drawX, boxY, drawW, drawH);
      } catch {
        // skip a corrupt image without aborting the whole report
      }
      idx++;
    }
  }

  // ── Page numbers ──
  const pageCount = doc.getNumberOfPages();
  for (let p = 1; p <= pageCount; p++) {
    doc.setPage(p);
    doc.setFont("Sarabun", "normal");
    doc.setFontSize(8);
    doc.setTextColor(120, 120, 120);
    doc.text(`หน้า ${p}/${pageCount}`, PAGE_W / 2, PAGE_H - 6, { align: "center" });
    doc.setTextColor(0, 0, 0);
  }

  return doc.output("blob") as Blob;
}

export async function downloadShopeeExpressReportPdf(
  rows: ShopeeReportTripRow[],
  customer: BillingCustomer,
  period: BillingPeriod,
  provider?: BillingProviderInfo,
  customerCode?: string,
  half: BillingHalf = "full"
): Promise<void> {
  const blob = await generateShopeeExpressReportBlob(rows, customer, period, provider, half);
  const mm = String(period.month).padStart(2, "0");
  const code = (customerCode || customer.name || "customer").replace(/[^A-Za-z0-9_-]/g, "");
  const roundSuffix = half === "first" ? "_r1" : half === "second" ? "_r2" : "";
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `shopee_express_report_${code}_${period.year}${mm}${roundSuffix}.pdf`;
  a.click();
  URL.revokeObjectURL(url);
}
