/**
 * Code-based UI audit counts per page group (UI1, issue #141): the numbers behind
 * shared-docs/specs/mv-go/ui-audit-baseline.md, so each UI pass (UI3-UI7) can re-measure its group and put the
 * before and after in its pull request. Heuristics, not proof: every count is a candidate to verify in context
 * (e.g. an Input without id may sit inside a FormControl further up).
 *
 * Usage (from logitrack-web): node scripts/ui-audit-counts.mjs . [--json out.json]
 */
import fs from "node:fs";
import path from "node:path";

const webDir = process.argv[2] ?? ".";
const jsonOut = process.argv[3] === "--json" ? process.argv[4] : null;

const GROUPS = [
  ["UI1 shared", [/^components\/ui\//, /^components\/(virtual-table|filter-combobox|place-filter-combobox|plate-filter-combobox|entity-pickers|lazy-dialog|load-more)\.tsx$/, /^components\/image-preview\//, /^app\/globals\.css$/]],
  ["UI3 shell, sign-in, navigation", [/^app\/(layout|providers|page-not-used)\.tsx$/, /^app\/app\/(layout|page)\.tsx$/, /^app\/app\/unauthorized\//, /^app\/\(auth\)\//, /^app\/my-account\//, /^components\/(app-sidebar|security-center-sidebar|tenant-switcher|tenant-switcher-menu|language-switcher|navigation|page-permission-guard|continue-with-google-button|footer)\.tsx$/, /^features\/auth\//, /^features\/platform\//]],
  ["UI4 master data", [/^app\/app\/(customers|subcontractors|companies|settings\/company-profile|sources|trucks|renewals|truck-assignment|drivers)\//, /^features\/(customers|subcontractors|companies|hubs|trucks)\//, /^features\/drivers\/(?!components\/go\/(DriverMonitorPage|MonitorExportDialog|TripDetailDialog|TripEditDialog|monitorStyles))/, /^components\/map\//, /^app\/app\/first-mile\/hub-dialog\.tsx$/]],
  ["UI5 operations", [/^app\/app\/(driver-monitor|first-mile|line-haul|job-assign|standby-records|incident-reports)\//, /^features\/(tasks|standby|incident-reports|jobs|line)\//, /^features\/drivers\/components\/go\/(DriverMonitorPage|MonitorExportDialog|TripDetailDialog|TripEditDialog|monitorStyles)/]],
  ["UI6 accounting, billing, maintenance, utilities", [/^app\/app\/(accounting|maintenance|utilities)\//, /^features\/(accounting|maintenance)\//, /^components\/accounting\//]],
  ["UI7 HR, comms, security, settings, dashboard, public", [/^app\/app\/(dashboard|payroll|leave-requests|holidays|chat|security-center|operations|users|waitlist)\//, /^app\/(page\.tsx|join-network\/|about\/|support\/)/, /^components\/landing\//, /^features\/(dashboard|driver-compensation|leave|holidays|chat|security-center|mobile-release|users|waitlist|tenants)\//]],
];

function walk(dir, out = []) {
  for (const name of fs.readdirSync(dir)) {
    const p = path.join(dir, name);
    const st = fs.statSync(p);
    if (st.isDirectory()) {
      if (!["node_modules", ".next", "__tests__"].includes(name)) walk(p, out);
    } else if (/\.(tsx|css)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p);
  }
  return out;
}
const files = ["app", "components", "features"].flatMap((d) => walk(path.join(webDir, d))).map((f) => ({
  rel: path.relative(webDir, f).split(path.sep).join("/"),
  src: fs.readFileSync(f, "utf8"),
}));

/** The JSX opening tags of `name` (`<Button ...>`), braces and strings respected. */
function tags(src, name) {
  const out = [];
  const re = new RegExp(`<${name}(?=[\\s>/])`, "g");
  let m;
  while ((m = re.exec(src))) {
    let i = m.index + m[0].length, depth = 0, q = null;
    for (; i < src.length; i++) {
      const c = src[i];
      if (q) { if (c === q && src[i - 1] !== "\\") q = null; continue; }
      if (c === '"' || c === "'" || c === "`") { q = c; continue; }
      if (c === "{") depth++;
      else if (c === "}") depth--;
      else if (c === ">" && depth === 0) break;
    }
    out.push({ start: m.index, end: i, text: src.slice(m.index, i + 1) });
  }
  return out;
}
const count = (src, re) => (src.match(re) || []).length;

const METRICS = {
  gradients: (s) => count(s, /\bbg-gradient-|\bbg-linear-|\bbg-radial|\b(?:linear|radial|conic)-gradient\(/g),
  backdropBlur: (s) => count(s, /\bbackdrop-blur/g),
  glow: (s) => count(s, /(?<![\w-])blur-(?:xl|2xl|3xl)\b|shadow-(?:[a-z]+-\d{2,3}|primary|blue-\d+)\/\d+/g),
  inlineStyles: (s) => count(s, /\bstyle=\{\{/g),
  oversizedShadows: (s) => count(s, /(?<![\w-])shadow-(?:xl|2xl)\b/g),
  oversizedRadius: (s) => count(s, /(?<![\w-])rounded-(?:2xl|3xl|\[\d{2,}px\])/g),
  zArbitrary: (s) => count(s, /(?<![\w-])!?z-\[/g),
  paletteColors: (s) => count(s, /(?<![\w-])(?:bg|text|border|ring|from|to|fill|stroke|divide)-(?:red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|neutral|stone)-\d{2,3}\b/g),
  hexColors: (s) => count(s, /\[#[0-9a-fA-F]{3,8}\]|#[0-9a-fA-F]{6}\b/g),
  rawButton: (s) => count(s, /<button[\s>]/g),
  rawInput: (s) => tags(s, "input").filter((t) => !/type=["'](hidden|file|checkbox|radio|color)["']/.test(t.text)).length,
  rawTextarea: (s) => count(s, /<textarea[\s>]/g),
  rawSelect: (s) => count(s, /<select[\s>]/g),
  rawTable: (s) => count(s, /<table[\s>]/g),
  iconButtonsNoName: (s) =>
    tags(s, "Button").filter((t) => /size=["']icon/.test(t.text) && !/aria-label|title=/.test(t.text) && !/sr-only/.test(s.slice(t.end, t.end + 400).split("</Button>")[0])).length,
  imgNoAlt: (s) => tags(s, "img").filter((t) => !/\balt=/.test(t.text)).length,
  unlabeledInputs: (s) =>
    tags(s, "Input").filter((t) => !/\bid=|aria-label|aria-labelledby/.test(t.text) && !/<FormControl[^>]*>\s*$/.test(s.slice(Math.max(0, t.start - 120), t.start))).length,
  lowContrastText: (s) => count(s, /(?<![\w:-])text-(?:gray|slate|zinc|neutral|stone)-(?:300|400)\b|text-muted-foreground\/\d+|text-white\/[1-6]\d\b/g),
  emoji: (s) => count(s.replace(/\/\/.*$|\/\*[\s\S]*?\*\//gm, ""), /\p{Extended_Pictographic}|\p{Regional_Indicator}{2}/gu),
  wideFixedWidths: (s) => [...s.matchAll(/(?<![\w-])(?:min-)?w-\[(\d+)px\]/g)].filter((m) => Number(m[1]) > 360).length,
  darkVariants: (s) => count(s, /\bdark:/g),
  motionReduce: (s) => count(s, /motion-reduce:|motion-safe:|prefers-reduced-motion/g),
  hoverScale: (s) => count(s, /hover:scale-|group-hover:scale-/g),
};

const result = {};
for (const [group, patterns] of GROUPS) {
  const gfiles = files.filter((f) => patterns.some((p) => p.test(f.rel)));
  const row = { files: gfiles.length, pages: gfiles.filter((f) => /page\.tsx$/.test(f.rel)).length };
  const top = {};
  for (const [k, fn] of Object.entries(METRICS)) {
    row[k] = 0;
    for (const f of gfiles) {
      const n = fn(f.src);
      row[k] += n;
      if (n) (top[k] ??= []).push([f.rel, n]);
    }
  }
  for (const k of Object.keys(top)) top[k] = top[k].sort((a, b) => b[1] - a[1]).slice(0, 6);
  result[group] = { row, top };
}
const assigned = new Set(GROUPS.flatMap(([, ps]) => files.filter((f) => ps.some((p) => p.test(f.rel))).map((f) => f.rel)));
result.unassigned = files.filter((f) => !assigned.has(f.rel)).map((f) => f.rel);

const keys = ["files", "pages", ...Object.keys(METRICS)];
console.log(["group", ...keys].join("\t"));
for (const [g, { row }] of Object.entries(result)) if (g !== "unassigned") console.log([g, ...keys.map((k) => row[k])].join("\t"));
const totals = keys.map((k) => Object.entries(result).filter(([g]) => g !== "unassigned").reduce((a, [, v]) => a + v.row[k], 0));
console.log(["total", ...totals].join("\t"));
console.log("unassigned:", result.unassigned.join(", "));
if (jsonOut) fs.writeFileSync(jsonOut, JSON.stringify(result, null, 2));
