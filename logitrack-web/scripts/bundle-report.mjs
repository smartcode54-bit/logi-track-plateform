/**
 * Per-route initial-JS report and bundle gate (TW9, developer-spec.md §10.11 and §10.14 item 7,
 * Appendix E §E.7).
 *
 * Next 16 no longer prints "First Load JS" in `next build`, so this script derives it from the
 * webpack-bundle-analyzer data that `pnpm analyze` writes to .next/analyze/client.json. Each chunk
 * there carries `isInitialByEntrypoint`, the module tree (with node_modules paths) and its parsed
 * and gzip sizes. A route's initial JS is the union of the chunks that are initial for
 * `main-app`, every layout above the page and the page itself, which is what the browser loads
 * before the route can hydrate; chunks loaded later through `import()` / `next/dynamic` are not
 * part of it.
 *
 * Usage (after `pnpm analyze`):
 *   node scripts/bundle-report.mjs                         # Markdown table on stdout
 *   node scripts/bundle-report.mjs --json out.json         # also write the machine-readable report
 *   node scripts/bundle-report.mjs --check                 # fail on a forbidden library or a
 *                                                          # locale dictionary in initial JS
 *   node scripts/bundle-report.mjs --check --baseline bundle-budget.json [--max-growth 5]
 *                                                          # also fail when a route's initial gzip
 *                                                          # grows more than 5% over the baseline
 *   --analyze-dir <dir>   analyzer output directory (default .next/analyze)
 *
 * `pnpm bundle:check` builds with the analyzer and runs the gate against bundle-budget.json (CI job
 * `bundle-budget`). When a size change is intended, regenerate the budget in the same PR:
 *   pnpm analyze && node scripts/bundle-report.mjs --json bundle-budget.json
 * bundle-before-tw9.json is the report of the tree before TW9, kept as the reference measurement.
 *
 * Exit codes: 0 pass, 1 gate violation, 2 missing or unreadable input.
 */

import { existsSync, readFileSync, writeFileSync } from "fs";
import path from "path";
import { pathToFileURL } from "url";

/**
 * Libraries that must never be in a route's initial JS (developer-spec.md §10.14 item 7). They
 * are loaded with `import()` in click handlers or through `next/dynamic`. A trailing slash marks
 * a scope.
 */
export const FORBIDDEN_INITIAL_PACKAGES = [
    "xlsx",
    "xlsx-js-style",
    "jspdf",
    "jspdf-autotable",
    "bahttext",
    "jszip",
    "fullcalendar",
    "@fullcalendar/",
    "leaflet",
    "react-leaflet",
    "leaflet-geosearch",
];

/** Default allowed growth of a route's initial gzip size over the committed baseline, in percent. */
export const DEFAULT_MAX_GROWTH_PERCENT = 5;

/** The entrypoint every app route loads first (webpack runtime, React, the app router). */
export const ROOT_ENTRYPOINT = "main-app";

/**
 * Package name of a module path from the analyzer, or undefined for app code. pnpm paths look like
 * `./node_modules/.pnpm/xlsx@0.20.3/node_modules/xlsx/xlsx.mjs`; the last `node_modules/` wins.
 * @param {string} modulePath
 * @returns {string | undefined}
 */
export function packageOfModulePath(modulePath) {
    const marker = "node_modules/";
    const at = modulePath.lastIndexOf(marker);
    if (at < 0) return undefined;
    const rest = modulePath.slice(at + marker.length).split("/");
    if (!rest[0] || rest[0] === ".pnpm") return undefined;
    return rest[0].startsWith("@") && rest[1] ? `${rest[0]}/${rest[1]}` : rest[0];
}

/**
 * Language of a translation-dictionary module (`context/locales/<lang>/...`), or undefined.
 * @param {string} modulePath
 * @returns {string | undefined}
 */
export function localeOfModulePath(modulePath) {
    const m = /(?:^|\/)context\/locales\/([a-z]{2})\//.exec(modulePath);
    return m ? m[1] : undefined;
}

/**
 * True when `pkg` is on the forbidden list (exact name, or inside a forbidden scope).
 * @param {string} pkg
 */
export function isForbiddenPackage(pkg) {
    return FORBIDDEN_INITIAL_PACKAGES.some((f) => (f.endsWith("/") ? pkg.startsWith(f) : pkg === f));
}

/**
 * URL path of an app-router page entrypoint (`app/(auth)/login/page` -> `/login`), or undefined
 * when the entrypoint is not a page.
 * @param {string} entrypoint
 * @returns {string | undefined}
 */
export function routeOfEntrypoint(entrypoint) {
    if (!entrypoint.startsWith("app/") || !entrypoint.endsWith("/page")) {
        return entrypoint === "app/page" ? "/" : undefined;
    }
    const segments = entrypoint
        .slice("app/".length, -"/page".length)
        .split("/")
        .filter((s) => !(s.startsWith("(") && s.endsWith(")")));
    return `/${segments.join("/")}`;
}

/**
 * Entrypoints a page loads before it can hydrate: `main-app`, each `layout` from the root down
 * that exists in the build, then the page.
 * @param {string} pageEntrypoint e.g. `app/app/accounting/income/page`
 * @param {Set<string>} known every entrypoint in the build
 * @returns {string[]}
 */
export function entrypointChain(pageEntrypoint, known) {
    const dirs = pageEntrypoint.slice(0, -"/page".length).split("/");
    const chain = [ROOT_ENTRYPOINT];
    for (let i = 1; i <= dirs.length; i++) {
        const layout = `${dirs.slice(0, i).join("/")}/layout`;
        if (known.has(layout)) chain.push(layout);
    }
    chain.push(pageEntrypoint);
    return chain;
}

/**
 * Leaf modules of an analyzer chunk.
 * @param {{ groups?: unknown[] }} node
 * @param {Array<{ path?: string, gzipSize?: number, parsedSize?: number }>} [out]
 */
function leafModules(node, out = []) {
    const groups = /** @type {any[] | undefined} */ (node.groups);
    if (!groups || groups.length === 0) {
        out.push(/** @type {any} */ (node));
        return out;
    }
    for (const g of groups) leafModules(g, out);
    return out;
}

/**
 * Summarise one analyzer chunk: its sizes and, per package / locale of interest, the bytes it holds.
 * @param {any} chunk
 */
function summariseChunk(chunk) {
    /** @type {Map<string, number>} */ const forbidden = new Map();
    /** @type {Map<string, number>} */ const locales = new Map();
    for (const mod of leafModules(chunk)) {
        const modulePath = typeof mod.path === "string" ? mod.path : "";
        const gzip = typeof mod.gzipSize === "number" ? mod.gzipSize : 0;
        const pkg = packageOfModulePath(modulePath);
        if (pkg && isForbiddenPackage(pkg)) forbidden.set(pkg, (forbidden.get(pkg) ?? 0) + gzip);
        const lang = pkg ? undefined : localeOfModulePath(modulePath);
        if (lang) locales.set(lang, (locales.get(lang) ?? 0) + gzip);
    }
    return {
        label: String(chunk.label),
        parsedSize: Number(chunk.parsedSize ?? 0),
        gzipSize: Number(chunk.gzipSize ?? 0),
        initial: /** @type {Record<string, boolean>} */ (chunk.isInitialByEntrypoint ?? {}),
        forbidden,
        locales,
    };
}

/**
 * Build the per-route report from webpack-bundle-analyzer client data.
 * @param {any[]} clientChunks parsed `.next/analyze/client.json`
 * @returns {{ routes: Record<string, { chunks: number, parsedBytes: number, gzipBytes: number, forbidden: Record<string, number>, locales: Record<string, number> }> }}
 */
export function buildReport(clientChunks) {
    const chunks = clientChunks.filter((c) => String(c.label).endsWith(".js")).map(summariseChunk);
    /** @type {Set<string>} */ const known = new Set();
    for (const c of chunks) for (const ep of Object.keys(c.initial)) known.add(ep);

    /** @type {Record<string, any>} */ const routes = {};
    const pages = [...known].filter((ep) => routeOfEntrypoint(ep) !== undefined).sort();
    for (const page of pages) {
        const chain = entrypointChain(page, known);
        const initial = chunks.filter((c) => chain.some((ep) => c.initial[ep]));
        /** @type {Record<string, number>} */ const forbidden = {};
        /** @type {Record<string, number>} */ const locales = {};
        for (const c of initial) {
            for (const [k, v] of c.forbidden) forbidden[k] = (forbidden[k] ?? 0) + v;
            for (const [k, v] of c.locales) locales[k] = (locales[k] ?? 0) + v;
        }
        routes[/** @type {string} */ (routeOfEntrypoint(page))] = {
            chunks: initial.length,
            parsedBytes: initial.reduce((s, c) => s + c.parsedSize, 0),
            gzipBytes: initial.reduce((s, c) => s + c.gzipSize, 0),
            forbidden: sortKeys(forbidden),
            locales: sortKeys(locales),
        };
    }
    return { routes: sortKeys(routes) };
}

/**
 * Gate violations: a forbidden library or any locale dictionary in a route's initial JS (the
 * provider loads exactly the active language through `context/locales/load.ts`), and, with a
 * baseline, initial gzip growth above `maxGrowthPercent`.
 * @param {ReturnType<typeof buildReport>} report
 * @param {ReturnType<typeof buildReport> | undefined} baseline
 * @param {number} maxGrowthPercent
 * @returns {string[]}
 */
export function gateViolations(report, baseline, maxGrowthPercent = DEFAULT_MAX_GROWTH_PERCENT) {
    const problems = [];
    for (const [route, r] of Object.entries(report.routes)) {
        const libs = Object.keys(r.forbidden);
        if (libs.length > 0) problems.push(`${route}: ${libs.join(", ")} in initial JS`);
        const langs = Object.keys(r.locales);
        if (langs.length > 0) problems.push(`${route}: locale dictionary ${langs.join(", ")} in initial JS`);
        const before = baseline?.routes?.[route];
        if (before && before.gzipBytes > 0) {
            const growth = ((r.gzipBytes - before.gzipBytes) / before.gzipBytes) * 100;
            if (growth > maxGrowthPercent) {
                problems.push(
                    `${route}: initial JS ${kb(r.gzipBytes)} gzip is ${growth.toFixed(1)}% over the baseline ` +
                        `${kb(before.gzipBytes)} (limit ${maxGrowthPercent}%)`,
                );
            }
        }
    }
    return problems;
}

/**
 * Markdown table of the report, with the baseline delta when one is given.
 * @param {ReturnType<typeof buildReport>} report
 * @param {ReturnType<typeof buildReport> | undefined} baseline
 */
export function toMarkdown(report, baseline) {
    const head = baseline
        ? "| Route | Initial JS gzip | vs baseline | Parsed | Chunks | Forbidden libs (gzip) | Locales (gzip) |\n|---|---:|---:|---:|---:|---|---|"
        : "| Route | Initial JS gzip | Parsed | Chunks | Forbidden libs (gzip) | Locales (gzip) |\n|---|---:|---:|---:|---|---|";
    const rows = Object.entries(report.routes).map(([route, r]) => {
        const libs = Object.entries(r.forbidden).map(([k, v]) => `${k} ${kb(v)}`).join(", ") || "none";
        const langs = Object.entries(r.locales).map(([k, v]) => `${k} ${kb(v)}`).join(", ") || "none";
        const before = baseline?.routes?.[route];
        const delta = before
            ? `${r.gzipBytes >= before.gzipBytes ? "+" : ""}${(((r.gzipBytes - before.gzipBytes) / before.gzipBytes) * 100).toFixed(1)}%`
            : "new";
        const cells = baseline
            ? [route, kb(r.gzipBytes), delta, kb(r.parsedBytes), r.chunks, libs, langs]
            : [route, kb(r.gzipBytes), kb(r.parsedBytes), r.chunks, libs, langs];
        return `| ${cells.join(" | ")} |`;
    });
    return `${head}\n${rows.join("\n")}\n`;
}

/** @param {number} bytes */
function kb(bytes) {
    return `${(bytes / 1024).toFixed(1)} KB`;
}

/**
 * @template T
 * @param {Record<string, T>} obj
 * @returns {Record<string, T>}
 */
function sortKeys(obj) {
    return Object.fromEntries(Object.entries(obj).sort(([a], [b]) => a.localeCompare(b)));
}

/** @param {string[]} argv */
function parseArgs(argv) {
    /** @type {{ check: boolean, json?: string, baseline?: string, maxGrowth: number, analyzeDir: string }} */
    const opts = { check: false, maxGrowth: DEFAULT_MAX_GROWTH_PERCENT, analyzeDir: path.join(".next", "analyze") };
    for (let i = 0; i < argv.length; i++) {
        const a = argv[i];
        const value = () => {
            const v = argv[++i];
            if (v === undefined) throw new Error(`${a} needs a value`);
            return v;
        };
        if (a === "--check") opts.check = true;
        else if (a === "--json") opts.json = value();
        else if (a === "--baseline") opts.baseline = value();
        else if (a === "--max-growth") opts.maxGrowth = Number(value());
        else if (a === "--analyze-dir") opts.analyzeDir = value();
        else throw new Error(`unknown argument ${a}`);
    }
    if (!Number.isFinite(opts.maxGrowth) || opts.maxGrowth < 0) throw new Error("--max-growth must be a non-negative number");
    return opts;
}

/** @param {string} file */
function readJson(file) {
    return JSON.parse(readFileSync(file, "utf8"));
}

function main() {
    let opts;
    try {
        opts = parseArgs(process.argv.slice(2));
    } catch (e) {
        console.error(`bundle-report: ${e instanceof Error ? e.message : String(e)}`);
        process.exit(2);
    }
    const clientFile = path.join(opts.analyzeDir, "client.json");
    if (!existsSync(clientFile)) {
        console.error(`bundle-report: ${clientFile} not found; run \`pnpm analyze\` first`);
        process.exit(2);
    }
    let report;
    let baseline;
    try {
        report = buildReport(readJson(clientFile));
        baseline = opts.baseline ? readJson(opts.baseline) : undefined;
    } catch (e) {
        console.error(`bundle-report: ${e instanceof Error ? e.message : String(e)}`);
        process.exit(2);
    }
    if (Object.keys(report.routes).length === 0) {
        console.error(`bundle-report: no app routes found in ${clientFile}`);
        process.exit(2);
    }
    process.stdout.write(toMarkdown(report, baseline));
    if (opts.json) writeFileSync(opts.json, `${JSON.stringify(report, null, 2)}\n`);
    if (!opts.check) return;
    const problems = gateViolations(report, baseline, opts.maxGrowth);
    if (problems.length > 0) {
        console.error(`\nbundle gate: ${problems.length} problem(s)`);
        for (const p of problems) console.error(`  - ${p}`);
        process.exit(1);
    }
    console.error(`\nbundle gate: ok (${Object.keys(report.routes).length} routes)`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main();
