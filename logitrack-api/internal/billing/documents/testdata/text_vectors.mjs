// Writes text_vectors.json: what V8 prints for the two texts the billing documents compute in Go (T39, main spec §6.2
// "Display"): new Intl.NumberFormat("th-TH", {minimumFractionDigits: 2, maximumFractionDigits: 2}).format(n) (formatThb,
// of the browser's billing documents, deleted in F87) and bahttext(n) of npm bahttext@2.4.0.
//
//   node internal/billing/documents/testdata/text_vectors.mjs path/to/bahttext/src/index.js
//
// The web no longer depends on bahttext (F87, issue #87), so install bahttext@2.4.0 in a scratch directory
// (`npm install --prefix <dir> bahttext@2.4.0`) and pass <dir>/node_modules/bahttext/src/index.js. The output is
// deterministic for one ICU version; the file records the Node and ICU versions it was produced with.
import { createRequire } from "node:module";
import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const require = createRequire(import.meta.url);
const bahtPath = process.argv[2];
if (!bahtPath) throw new Error("usage: node text_vectors.mjs path/to/bahttext/src/index.js (bahttext@2.4.0)");
const { bahttext } = require(bahtPath);
const pkg = require(path.join(path.dirname(bahtPath), "..", "package.json"));
if (pkg.version !== "2.4.0") throw new Error(`bahttext ${pkg.version}, want 2.4.0`);

const fmt = new Intl.NumberFormat("th-TH", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const nums = [0, -0, 1, 1.005, 2.675, 1.255, 0.125, 0.135, 0.005, 0.015, 0.045, 10.235, 1234.5, 1234567.891, 999.995,
  -1.005, -0.001, -50, 43458.81, 1493.43, 44.8, 1448.63, 21, 101, 201, 1001, 1000001, 11, 12, 20, 21.21, 100.01,
  0.01, 0.1, 0.5, 0.99, 1.01, 11000000, 21000021, 1e15, 123456789.12, 0.07, 0.29, 0.57, 1.15, 4.35, 8.2, 16.4,
  9007199254740991, 9007199254740992, 1e21, 3000.3, 200.2, 15.555, 33.335, 1000000.005, 70.07, 99.99, 5.05, 1.1,
  2.3, 4.6, 9.7, 19.99, 0.3, 0.6, 1.9, NaN, Infinity, -Infinity];
const tag = (n) => (Number.isNaN(n) ? "NaN" : Object.is(n, -0) ? "-0" : !Number.isFinite(n) ? String(n) : n);
const vectors = nums.map((n) => ({ n: tag(n), intl: fmt.format(n), baht: bahttext(n) }));
const out = { source: "text_vectors.mjs", node: process.version, icu: process.versions.icu, bahttext: pkg.version, vectors };
writeFileSync(path.join(here, "text_vectors.json"), JSON.stringify(out, null, 1) + "\n");
