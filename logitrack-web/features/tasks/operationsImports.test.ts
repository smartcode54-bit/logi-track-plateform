// T34 (issue #54): the Go operations pages read and write through /api/go only. With the `operations` flag on, the
// six routes render their Go page, whose import graph (static and lazy) holds no Firebase module: no direct
// Firestore read or write of tasks, trip_records, standby_records or incidentReport (AC 1). The monitor's graph
// holds no rate-card reader and no pricing callable (AC 2: `useDriverMonitor.ts:799`, `:810-840`). xlsx and jszip
// load only behind an `import()` (AC 4: the export loads xlsx lazily). The Firestore pages keep theirs (moved to
// `features/*/legacy`, deleted at P2 exit).
import { existsSync, readFileSync, statSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "../..");
const ROUTES = ["first-mile", "line-haul", "job-assign", "driver-monitor", "standby-records", "incident-reports"].map((r) =>
    path.join(root, "app/app", r, "page.tsx")
);
const FIREBASE_RX = /^(firebase|@firebase)(\/|$)|^@\/firebase(\/|$)/;
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

function goPageModule(route: string): string {
    const m = /const GoPage = dynamic\(\(\) => import\(["']([^"']+)["']\)/.exec(readFileSync(route, "utf8"));
    if (!m) throw new Error(`${path.relative(root, route)}: no GoPage`);
    const file = resolveImport(route, m[1]);
    if (!file) throw new Error(`${path.relative(root, route)}: cannot resolve ${m[1]}`);
    return file;
}

/** Every module reachable from `entry` (static and lazy edges), with the chain that reaches it. */
function graph(entry: string): Map<string, string> {
    const seen = new Map<string, string>();
    const walk = (file: string, chain: string) => {
        if (seen.has(file)) return;
        seen.set(file, chain);
        const src = code(file);
        const specs: { spec: string; lazy: boolean }[] = [];
        for (const m of src.matchAll(STATIC_RX)) if (!m[2]) specs.push({ spec: m[3], lazy: false });
        for (const m of src.matchAll(DYNAMIC_RX)) specs.push({ spec: m[1], lazy: true });
        for (const { spec, lazy } of specs) {
            const next = resolveImport(file, spec);
            if (next) walk(next, `${chain} -> ${lazy ? `import(${path.relative(root, next)})` : path.relative(root, next)}`);
        }
    };
    walk(entry, path.relative(root, entry));
    return seen;
}

/** The bare specifiers matching `rx` the graph imports, each with its chain and whether it is lazy. */
function bareImports(entry: string, rx: RegExp): { chain: string; lazy: boolean }[] {
    const out: { chain: string; lazy: boolean }[] = [];
    for (const [file, chain] of graph(entry)) {
        const src = code(file);
        for (const m of src.matchAll(STATIC_RX)) if (!m[2] && rx.test(m[3])) out.push({ chain: `${chain} -> ${m[3]}`, lazy: false });
        for (const m of src.matchAll(DYNAMIC_RX)) if (rx.test(m[1])) out.push({ chain: `${chain} -> import(${m[1]})`, lazy: true });
    }
    return out;
}

describe("the Go operations pages' import graph (T34)", () => {
    it("has a Go page and a Firestore page per route, switched on the operations flag", () => {
        for (const r of ROUTES) {
            const src = readFileSync(r, "utf8");
            expect(src, path.relative(root, r)).toContain('<DomainPage domain="operations"');
            expect(src, path.relative(root, r)).toMatch(/const FirestorePage = dynamic\(\(\) => import\("@\/features\/(tasks|drivers|standby|incident-reports)\/legacy\//);
        }
    });

    it.each(ROUTES.map((r) => [path.relative(root, r), r]))("%s: the Go page imports no Firebase module, statically or lazily (AC 1)", (_n, route) => {
        expect(bareImports(goPageModule(route), FIREBASE_RX).map((x) => x.chain)).toEqual([]);
    });

    it.each(ROUTES.map((r) => [path.relative(root, r), r]))("%s: xlsx and jszip only behind import() (AC 4)", (_n, route) => {
        for (const x of bareImports(goPageModule(route), /^(xlsx|xlsx-js-style|jszip)$/)) expect(x.lazy, x.chain).toBe(true);
        expect(bareImports(goPageModule(route), /^(jspdf|jspdf-autotable|xlsx-js-style)$/)).toEqual([]);
    });

    it("the monitor reads no rate card and calls no pricing callable (AC 2)", () => {
        const files = [...graph(goPageModule(path.join(root, "app/app/driver-monitor/page.tsx"))).keys()];
        const rel = files.map((f) => path.relative(root, f));
        expect(rel.filter((f) => /billingRates|useDriverMonitor|billing\.ts$|rateCard/.test(f))).toEqual([]);
        for (const f of files) {
            const src = code(f);
            expect(src, path.relative(root, f)).not.toMatch(/computeTripBillingSnapshot|setTripJobCategory"|httpsCallable|rate-entries|fuel-adjustments/);
        }
    });

    it("sees Firebase where it is: the Firestore pages", () => {
        for (const legacy of ["features/drivers/legacy/LegacyDriverMonitorPage.tsx", "features/tasks/legacy/LegacyFirstMilePage.tsx",
            "features/standby/legacy/LegacyStandbyRecordsPage.tsx", "features/incident-reports/legacy/LegacyIncidentReportsPage.tsx"]) {
            expect(bareImports(path.join(root, legacy), FIREBASE_RX).length, legacy).toBeGreaterThan(0);
        }
    });
});
