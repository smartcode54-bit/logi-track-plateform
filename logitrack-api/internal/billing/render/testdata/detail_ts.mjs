// Writes detail_ts.json: the cells, merges and column widths the browser's generateDetailExcelBuffer
// (logitrack-web/lib/billingDocumentRender.ts) builds for detail_fixture.json, so the Go renderer is compared with
// the TypeScript it replaces (developer-spec.md §9.9 "Golden"; TestDetailXLSXMatchesTheBrowser).
//
//   TZ=Asia/Bangkok node internal/billing/render/testdata/detail_ts.mjs
//
// Run from logitrack-api/ after `pnpm install` (repository root). The browser formatted dates in its own zone; TZ pins
// it to Bangkok, the zone the server prints in. The module is loaded as it is (Node strips the TypeScript types); a
// resolve hook adds the ".ts" of its relative imports and `require` is provided for its bahttext import.
import { createRequire, registerHooks } from "node:module";
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import path from "node:path";

if (process.env.TZ !== "Asia/Bangkok") throw new Error("run with TZ=Asia/Bangkok");
const here = path.dirname(fileURLToPath(import.meta.url));
const web = path.resolve(here, "../../../../../logitrack-web");
const entry = path.join(web, "lib/billingDocumentRender.ts");
// xlsx-js-style is CommonJS without detectable named exports: the bundler gave `import * as XLSX` its properties, Node
// gives only `default`, so the import is served by a shim module that re-exports them.
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
const { generateDetailExcelBuffer } = await import(pathToFileURL(entry).href);
const XLSX = createRequire(path.join(web, "package.json"))("xlsx-js-style");

const fixture = JSON.parse(readFileSync(path.join(here, "detail_fixture.json"), "utf8"));
const dates = ["deliveredTimestamp", "billingDate", "actualPickupAt", "planDate"];
const rows = fixture.rows.map((r) => {
  const o = { ...r };
  for (const k of dates) if (o[k]) o[k] = new Date(o[k]);
  return o;
});
const buf = generateDetailExcelBuffer(rows, fixture.period, fixture.customer, fixture.provider, fixture.showActualPickup);
const wb = XLSX.read(buf, { type: "array", cellStyles: true });
const name = wb.SheetNames[0];
const ws = wb.Sheets[name];
const range = XLSX.utils.decode_range(ws["!ref"]);
const cells = [];
for (let r = range.s.r; r <= range.e.r; r++) {
  const row = [];
  for (let c = range.s.c; c <= range.e.c; c++) {
    const cell = ws[XLSX.utils.encode_cell({ r, c })];
    row.push(cell ? { v: cell.v, t: cell.t, z: cell.z ?? null } : null);
  }
  cells.push(row);
}
const out = {
  source: "detail_ts.mjs", node: process.version, sheet: name, cells,
  merges: (ws["!merges"] ?? []).map((m) => XLSX.utils.encode_range(m)),
  cols: (ws["!cols"] ?? []).map((c) => c.wch ?? null),
};
writeFileSync(path.join(here, "detail_ts.json"), JSON.stringify(out, null, 1) + "\n");
