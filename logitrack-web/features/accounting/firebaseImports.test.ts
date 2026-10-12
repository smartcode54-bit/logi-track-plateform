// T42: the Go accounting, maintenance and billing-utility pages import no Firebase module, statically or
// lazily, so their chunks carry no Firebase SDK (developer-spec.md §12.6 T42: "with the domain flag on,
// pages use only /api/go"). This walks the import graph of the module each route's `GoPage` loads. TW7
// removed the Firestore pages (`features/*/legacy/*`) and the `billing` flag switch: each route renders
// its Go page only.
//
// It also pins R69 on the Go pages: no jspdf, jspdf-autotable or xlsx-js-style in their graph, and jszip
// only behind a click (the lazily imported photo-ZIP helper).
import { existsSync, readFileSync, statSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "../..");
const ROUTES = [
    "accounting/audit",
    "accounting/billing-document",
    "accounting/billing-result",
    "accounting/fuel",
    "accounting/fuel-price-history",
    "accounting/income",
    "accounting/other",
    "accounting/rate-card",
    "accounting/shopee-express-report",
    "utilities/backfill",
    "utilities/billing-impact",
    "maintenance",
    "trucks/maintenance",
].map((r) => path.join(root, "app/app", r, "page.tsx"));
const FIREBASE_RX = /^(firebase|@firebase)(\/|$)|^@\/firebase(\/|$)/;
// Static `import`/`export ... from` (type-only ones are erased by the compiler) and `import("...")`.
const STATIC_RX = /(?:^|[\n;])\s*(import|export)\s+(type\s+)?(?:[^'";]*?\s+from\s+)?["']([^"']+)["']/g;
const DYNAMIC_RX = /import\(\s*["']([^"']+)["']\s*\)/g;

function resolveImport(from: string, spec: string): string | undefined {
    let base: string;
    if (spec.startsWith("@/")) base = path.join(root, spec.slice(2));
    else if (spec.startsWith("shared-docs/")) base = path.join(root, "..", spec);
    else if (spec.startsWith(".")) base = path.resolve(path.dirname(from), spec);
    else return undefined;
    for (const candidate of [base, `${base}.ts`, `${base}.tsx`, path.join(base, "index.ts"), path.join(base, "index.tsx")]) {
        if (existsSync(candidate) && statSync(candidate).isFile()) return candidate;
    }
    return undefined;
}

function code(file: string): string {
    return readFileSync(file, "utf8")
        .replace(/\/\*[\s\S]*?\*\//g, "")
        .replace(/^\s*\/\/.*$/gm, "");
}

/** The module a route's `GoPage = dynamic(() => import("..."))` loads. */
function goPageModule(route: string): string {
    const m = /const GoPage = dynamic\(\(\) => import\(["']([^"']+)["']\)/.exec(readFileSync(route, "utf8"));
    if (!m) throw new Error(`${path.relative(root, route)}: no GoPage`);
    const file = resolveImport(route, m[1]);
    if (!file) throw new Error(`${path.relative(root, route)}: cannot resolve ${m[1]}`);
    return file;
}

/**
 * Every Firebase specifier reachable from `entry`, as the import chain that reaches it; a lazy edge is
 * written `import(x)`.
 */
function importsMatching(entry: string, rx: RegExp): string[] {
    const found: string[] = [];
    const seen = new Set<string>();
    const walk = (file: string, chain: string[]) => {
        if (seen.has(file)) return;
        seen.add(file);
        const src = code(file);
        const edges: { spec: string; lazy: boolean }[] = [];
        for (const m of src.matchAll(STATIC_RX)) if (!m[2]) edges.push({ spec: m[3], lazy: false });
        for (const m of src.matchAll(DYNAMIC_RX)) edges.push({ spec: m[1], lazy: true });
        for (const { spec, lazy } of edges) {
            const label = (name: string) => (lazy ? `import(${name})` : name);
            if (rx.test(spec)) found.push([...chain, label(spec)].join(" -> "));
            const next = resolveImport(file, spec);
            if (next) walk(next, [...chain, label(path.relative(root, next))]);
        }
    };
    walk(entry, [path.relative(root, entry)]);
    return found;
}

const firebaseImports = (entry: string) => importsMatching(entry, FIREBASE_RX);
const DOCUMENT_STACK_RX = /^(jspdf|jspdf-autotable|xlsx-js-style|bahttext)$|billingDocumentRender|shopeeExpressReport|pdfThai/;
const JSZIP_RX = /^jszip$/;

describe("the Go accounting and maintenance pages' import graph", () => {
    it("renders only its Go page per route (TW7: no Firestore page, no billing flag switch)", () => {
        for (const r of ROUTES) {
            const src = readFileSync(r, "utf8");
            expect(src, path.relative(root, r)).toMatch(/const GoPage = dynamic\(/);
            expect(src, path.relative(root, r)).not.toMatch(/DomainPage|FirestorePage|useDomainPageSource|\/legacy\//);
        }
    });

    it.each(ROUTES.map((r) => [path.relative(root, r), r]))("%s: the Go page imports no Firebase module", (_name, route) => {
        expect(firebaseImports(goPageModule(route))).toEqual([]);
    });

    it.each(ROUTES.map((r) => [path.relative(root, r), r]))("%s: no client-side document stack, jszip only lazily (R69)", (_name, route) => {
        const entry = goPageModule(route);
        expect(importsMatching(entry, DOCUMENT_STACK_RX)).toEqual([]);
        for (const chain of importsMatching(entry, JSZIP_RX)) expect(chain).toMatch(/import\(/);
    });
});
