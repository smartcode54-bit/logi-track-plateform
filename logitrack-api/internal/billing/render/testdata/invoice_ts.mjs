// Writes invoice_ts.json: every text the browser's buildInvoicePdf (logitrack-web/lib/billingDocumentRender.ts, jsPDF +
// jspdf-autotable) draws for three scenarios, captured from jsPDF's preProcessText event: page, x and baseline y in mm,
// size in pt, bold, alignment. TestInvoicePDFMatchesTheBrowser compares the Go renderer's texts with them (developer-spec.md
// §9.9 "Golden").
//
//   TZ=Asia/Bangkok node internal/billing/render/testdata/invoice_ts.mjs
//
// Run from logitrack-api/ after `pnpm install` (repository root). The fonts are served from logitrack-web/public/fonts
// (the browser fetched /fonts/*); the scenarios are built from detail_fixture.json.
import { createRequire, registerHooks } from "node:module";
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import path from "node:path";

if (process.env.TZ !== "Asia/Bangkok") throw new Error("run with TZ=Asia/Bangkok");
const here = path.dirname(fileURLToPath(import.meta.url));
const web = path.resolve(here, "../../../../../logitrack-web");
const entry = path.join(web, "lib/billingDocumentRender.ts");
const shim = "shim:xlsx-js-style";
registerHooks({
  resolve(specifier, context, next) {
    if (specifier === "xlsx-js-style" && context.parentURL === pathToFileURL(entry).href) {
      return { url: shim, shortCircuit: true };
    }
    try {
      return next(specifier, context);
    } catch (err) {
      if (specifier.startsWith(".")) return next(specifier + ".ts", context);
      throw err;
    }
  },
  load(url, context, next) {
    if (url !== shim) return next(url, context);
    const source = `import { createRequire } from "node:module";
const m = createRequire(${JSON.stringify(path.join(web, "package.json"))})("xlsx-js-style");
export const utils = m.utils, write = m.write, read = m.read;
export default m;`;
    return { format: "module", source, shortCircuit: true };
  },
});
globalThis.require = createRequire(entry);
const realFetch = globalThis.fetch;
globalThis.fetch = async (u, ...rest) => {
  if (typeof u === "string" && u.startsWith("/fonts/")) return new Response(readFileSync(path.join(web, "public", u)));
  return realFetch(u, ...rest);
};
const { jsPDF } = await import(pathToFileURL(createRequire(path.join(web, "package.json")).resolve("jspdf")).href);
const { generateInvoiceBlob, generateReceiptBlob } = await import(pathToFileURL(entry).href);

let ops = [];
const k = 72 / 25.4;
jsPDF.API.events.push(["preProcessText", function (payload) {
  const font = payload.mutex.fonts[payload.mutex.activeFontKey];
  const size = payload.mutex.activeFontSize;
  const lines = Array.isArray(payload.text) ? payload.text : [payload.text];
  const lh = (size * this.getLineHeightFactor()) / k;
  lines.forEach((t, i) => ops.push({
    page: this.internal.getCurrentPageInfo().pageNumber, x: round(payload.x), y: round(payload.y + i * lh), text: String(t),
    size, bold: font.fontStyle === "bold", align: payload.options.align ?? "left",
  }));
}]);
const round = (v) => Math.round(v * 1e4) / 1e4;

const fixture = JSON.parse(readFileSync(path.join(here, "detail_fixture.json"), "utf8"));
const rows = fixture.rows.filter((r) => r.id !== "nodate").map((r) => {
  const o = { ...r };
  for (const key of ["deliveredTimestamp", "billingDate", "actualPickupAt"]) if (o[key]) o[key] = new Date(o[key]);
  return o;
});
const customer = { id: "c", name: "CJ Super Fresh", address: "123 ถนนสุขุมวิท แขวงคลองเตย เขตคลองเตย กรุงเทพมหานคร 10110 ประเทศไทย อาคารทดสอบ ชั้น 12 ห้อง 1201",
  taxId: "0105556000001", branchType: "สาขา", branchNumber: "00002", paymentTermsDays: 30, contactName: "คุณเอ",
  contactPhone: "021234567", invoiceNote: "กรุณาชำระภายในกำหนด" };
const provider = { name: "บริษัท ทดสอบ จำกัด", address: "Bangkok", taxId: "0105556000002", bankName: "กสิกรไทย",
  accountNumber: "123-4-56789-0", accountName: "บริษัท ทดสอบ", withholdingTaxRate: 3, signatoryName: "นายทดสอบ" };
// A single round over many lines: no legend, no รอบ column, the footer moved to a new page.
const many = Array.from({ length: 45 }, (_, i) => ({
  id: `p${i}`, tripRecordId: `p${i}`, spxTripId: `SPX-${100 + i}`,
  deliveredTimestamp: new Date(Date.UTC(2026, 8, 1 + (i % 28), 3)), billingEstimateThb: 1000 + i,
  billingLookupHubId: "SPK-GW", billingLookupDestination: "SPK890103", vehicleClass: "6WH", originHubCode: "SPK-GW",
  destinationDisplayName: "ลาดกระบัง26", rowType: "trip", jobCategory: "PRIMARY", billingRoundEffectiveFromDateStr: "2026-09-01",
}));
const scenarios = {};
const run = async (name, fn) => { ops = []; await fn(); scenarios[name] = ops; };
await run("invoice", () => generateInvoiceBlob(rows, customer, fixture.period, "CJSF-202609-001", provider));
await run("receipt", () => generateReceiptBlob(rows, { ...customer, invoiceNote: undefined }, fixture.period, "CJSF-202609-001", provider));
await run("many", () => generateInvoiceBlob(many, { id: "c", name: "TTP Co." }, fixture.period, "TTP-202609-001", provider));
writeFileSync(path.join(here, "invoice_ts.json"), JSON.stringify({ source: "invoice_ts.mjs", node: process.version,
  customer, provider, scenarios }, null, 1) + "\n");
