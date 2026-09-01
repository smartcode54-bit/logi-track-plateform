"use client";
/**
 * Shared Thai-PDF helpers for client-side jsPDF documents.
 *
 * The Sarabun font (regular + bold) is embedded at runtime by fetching
 * /fonts/Sarabun-*.ttf from the public directory — helvetica cannot render Thai
 * Unicode, so every Thai document must register Sarabun before drawing text.
 *
 * Remote images (Firebase Storage URLs, etc.) are fetched and returned as base64
 * for `doc.addImage`. Used by lib/billingDocument.ts and lib/shopeeExpressReport.ts.
 */

import type { jsPDF } from "jspdf";

export interface ThaiFont {
  regular: string;
  bold: string;
}

let _fontCache: ThaiFont | null = null;

/** Fetch one URL and return raw base64 (no data-URI prefix), in 8 KB chunks. */
async function urlToBase64(url: string): Promise<string> {
  const resp = await fetch(url);
  if (!resp.ok) throw new Error(`[pdfThai] Fetch failed (${resp.status}): ${url}`);
  const buf = await resp.arrayBuffer();
  const bytes = new Uint8Array(buf);
  let binary = "";
  const CHUNK = 8192;
  for (let i = 0; i < bytes.length; i += CHUNK) {
    const slice = bytes.subarray(i, i + CHUNK);
    for (let j = 0; j < slice.length; j++) binary += String.fromCharCode(slice[j]);
  }
  return btoa(binary);
}

/**
 * Load & cache the Sarabun font (regular + bold) from /public/fonts.
 * Cached for the browser session so repeated PDF generations reuse it.
 */
export async function loadThaiFont(): Promise<ThaiFont> {
  if (_fontCache) return _fontCache;
  const [regular, bold] = await Promise.all([
    urlToBase64("/fonts/Sarabun-Regular.ttf"),
    urlToBase64("/fonts/Sarabun-Bold.ttf"),
  ]);
  _fontCache = { regular, bold };
  return _fontCache;
}

/**
 * Fetch a remote image URL and return it as raw base64 (no data-URI prefix).
 * Returns null on any fetch error so callers can skip gracefully.
 */
export async function fetchImageAsBase64(url: string): Promise<string | null> {
  try {
    return await urlToBase64(url);
  } catch {
    return null;
  }
}

/** Detect image type from URL extension. Defaults to PNG. */
export function imageFormat(url: string): "PNG" | "JPEG" {
  const u = url.toLowerCase();
  return u.includes(".jpg") || u.includes(".jpeg") ? "JPEG" : "PNG";
}

/** Register Sarabun (normal + bold + italic-as-normal) on a fresh jsPDF instance. */
export function registerThaiFont(doc: jsPDF, font: ThaiFont): void {
  doc.addFileToVFS("Sarabun-Regular.ttf", font.regular);
  doc.addFont("Sarabun-Regular.ttf", "Sarabun", "normal");
  doc.addFileToVFS("Sarabun-Bold.ttf", font.bold);
  doc.addFont("Sarabun-Bold.ttf", "Sarabun", "bold");
  // Thai script has no italic convention; reuse regular so setFont("Sarabun","italic") doesn't crash.
  doc.addFileToVFS("Sarabun-Italic.ttf", font.regular);
  doc.addFont("Sarabun-Italic.ttf", "Sarabun", "italic");
}
