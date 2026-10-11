// Writes shopee_ts.json: every text the browser's generateShopeeExpressReportBlob (logitrack-web/lib/shopeeExpressReport.ts,
// jsPDF + jspdf-autotable) draws for a month of trips without signed run-sheets (the photo pages need the browser's
// canvas), captured from jsPDF's preProcessText event. TestShopeeReportMatchesTheBrowser compares the Go renderer with it.
//
//   TZ=Asia/Bangkok node internal/billing/render/testdata/shopee_ts.mjs
//
// Run from logitrack-api/ after `pnpm install` (repository root).
import { createRequire, registerHooks } from "node:module";
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import path from "node:path";

if (process.env.TZ !== "Asia/Bangkok") throw new Error("run with TZ=Asia/Bangkok");
const here = path.dirname(fileURLToPath(import.meta.url));
const web = path.resolve(here, "../../../../../logitrack-web");
const entry = path.join(web, "lib/shopeeExpressReport.ts");
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
const { generateShopeeExpressReportBlob } = await import(pathToFileURL(entry).href);

const ops = [];
const k = 72 / 25.4;
const round = (v) => Math.round(v * 1e4) / 1e4;
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
const drivers = ["สมชาย ใจดี", "สมหญิง รักงาน", "Somsak"];
const rows = Array.from({ length: 70 }, (_, i) => ({
  id: `trip${String(i).padStart(4, "0")}xyz`, spxTripId: i % 9 === 0 ? undefined : `SPX-${2000 + i}`,
  deliveredTimestamp: new Date(Date.UTC(2026, 8, 1 + (i % 15), 2 + (i % 5))), driverId: `d${i % 3}`, driverName: drivers[i % 3],
  originDisplay: "J&T EXPRESS บางปู", destinationDisplay: i % 4 === 0 ? "-" : "ลาดกระบัง26",
  vehicleClass: i % 5 === 0 ? undefined : "6WH", truckLicensePlate: i % 6 === 0 ? undefined : `70-${1000 + i}`,
  parcelCount: i % 7 === 0 ? undefined : 10 + i, signedRunsheetPhotos: [],
}));
await generateShopeeExpressReportBlob(rows, { id: "c", name: "TTP Co." }, { year: 2026, month: 9 },
  { name: "บริษัท ทดสอบ จำกัด", address: "", taxId: "" }, "first");
writeFileSync(path.join(here, "shopee_ts.json"), JSON.stringify({ source: "shopee_ts.mjs", node: process.version, ops }, null, 1) + "\n");
