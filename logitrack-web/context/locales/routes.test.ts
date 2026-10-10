// TW4 locale step 2 (developer-spec.md §10.11, Appendix E §E.7 row 9): `accounting` and
// `driverMonitor` load per route group. This walks the import graph of every app page, collects the
// split namespaces whose keys it can render, and checks them against ./routes.ts, so a page that
// starts using one of them without its group listing it fails here (it would show raw keys).
import { existsSync, readdirSync, readFileSync, statSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";
import { SPLIT_NAMESPACES, type SplitNamespace } from "./load";
import { namespacesForPath, ROUTE_NAMESPACES } from "./routes";
import enBase from "./en";
import thBase from "./th";
import enAccounting from "./en/accounting";
import thAccounting from "./th/accounting";
import enDriverMonitor from "./en/driverMonitor";
import thDriverMonitor from "./th/driverMonitor";

const root = path.resolve(__dirname, "../..");
const IMPORT_RX = /(?:import|export)\s+(?:type\s+)?(?:[^'"]*?\s+from\s+)?["']([^"']+)["']|import\(\s*["']([^"']+)["']\s*\)/g;
// A key (or a template prefix such as `accounting.billingDocument.filters.${x}`) of a split namespace.
const NS_RX = new RegExp(`["'\`]((${SPLIT_NAMESPACES.join("|")})\\.[A-Za-z][A-Za-z0-9_.]*)`, "g");
const SPLIT_KEYS: Record<SplitNamespace, string[]> = {
    accounting: Object.keys(enAccounting),
    driverMonitor: Object.keys(enDriverMonitor),
};

/**
 * Whether a referenced key needs its split namespace: it is one of its keys, or a prefix of one that
 * the base dictionary does not hold (keys shared with other routes live in the base, imagePreview.ts).
 */
function needsNamespace(ns: SplitNamespace, key: string): boolean {
    if (key in enBase) return false;
    const prefix = key.endsWith(".") ? key : `${key}.`;
    return SPLIT_KEYS[ns].some((k) => k === key || k.startsWith(prefix));
}

function resolveImport(from: string, spec: string): string | undefined {
    let base: string;
    if (spec.startsWith("@/")) base = path.join(root, spec.slice(2));
    else if (spec.startsWith(".")) base = path.resolve(path.dirname(from), spec);
    else return undefined;
    for (const candidate of [base, `${base}.ts`, `${base}.tsx`, path.join(base, "index.ts"), path.join(base, "index.tsx")]) {
        if (existsSync(candidate) && statSync(candidate).isFile()) return candidate;
    }
    return undefined;
}

/** Source without comments (doc comments name key prefixes they do not render). */
function code(file: string): string {
    return readFileSync(file, "utf8")
        .replace(/\/\*[\s\S]*?\*\//g, "")
        .replace(/^\s*\/\/.*$/gm, "");
}

const fileInfo = new Map<string, { deps: string[]; namespaces: Set<SplitNamespace> }>();
function info(file: string) {
    let v = fileInfo.get(file);
    if (v) return v;
    const src = code(file);
    const deps: string[] = [];
    for (const m of src.matchAll(IMPORT_RX)) {
        const resolved = resolveImport(file, m[1] ?? m[2]);
        // The dictionaries themselves hold every key; the loader decides when they load.
        if (resolved && !resolved.includes(`${path.sep}context${path.sep}locales${path.sep}`)) deps.push(resolved);
    }
    const namespaces = new Set<SplitNamespace>();
    for (const m of src.matchAll(NS_RX)) {
        const ns = m[2] as SplitNamespace;
        if (needsNamespace(ns, m[1])) namespaces.add(ns);
    }
    v = { deps, namespaces };
    fileInfo.set(file, v);
    return v;
}

function namespacesUsedBy(entry: string): Map<SplitNamespace, string[]> {
    const seen = new Set([entry]);
    const stack = [entry];
    const used = new Map<SplitNamespace, string[]>();
    while (stack.length > 0) {
        const file = stack.pop()!;
        const i = info(file);
        for (const ns of i.namespaces) used.set(ns, [...(used.get(ns) ?? []), path.relative(root, file)]);
        for (const dep of i.deps) {
            if (!seen.has(dep)) {
                seen.add(dep);
                stack.push(dep);
            }
        }
    }
    return used;
}

function pages(dir: string, out: string[] = []): string[] {
    for (const name of readdirSync(dir)) {
        const p = path.join(dir, name);
        if (statSync(p).isDirectory()) pages(p, out);
        else if (name === "page.tsx") out.push(p);
    }
    return out;
}

function routeOf(page: string): string {
    const segments = path
        .relative(path.join(root, "app"), path.dirname(page))
        .split(path.sep)
        .filter((s) => s !== "" && !(s.startsWith("(") && s.endsWith(")")));
    return `/${segments.join("/")}`;
}

describe("route namespaces (./routes.ts)", () => {
    const all = pages(path.join(root, "app")).map((page) => ({ page, route: routeOf(page), used: namespacesUsedBy(page) }));

    it("finds the app pages", () => {
        expect(all.length).toBeGreaterThan(60);
        expect(all.map((p) => p.route)).toContain("/app/accounting/income");
    });

    it("every page's group lists each split namespace the page renders", () => {
        const missing = all.flatMap(({ route, used }) =>
            [...used.keys()].filter((ns) => !namespacesForPath(route).includes(ns)).map((ns) => `${route} needs ${ns} (${used.get(ns)!.join(", ")})`)
        );
        expect(missing).toEqual([]);
    });

    it("no route outside the accounting group (/app/accounting, /app/utilities) loads the accounting namespace", () => {
        const loading = all.filter(({ route }) => namespacesForPath(route).includes("accounting")).map(({ route }) => route);
        expect(loading.length).toBeGreaterThan(0);
        expect(loading.filter((r) => !r.startsWith("/app/accounting/") && !r.startsWith("/app/utilities/"))).toEqual([]);
    });

    it("the driverMonitor namespace loads only on the monitor and the boards that open its trip editor", () => {
        const loading = all.filter(({ route }) => namespacesForPath(route).includes("driverMonitor")).map(({ route }) => route);
        expect(loading.sort()).toEqual(["/app/driver-monitor", "/app/first-mile", "/app/job-assign", "/app/line-haul"]);
    });

    it("a group never lists a namespace none of its pages use", () => {
        for (const { prefix, namespaces } of ROUTE_NAMESPACES) {
            const inGroup = all.filter(({ route }) => route === prefix || route.startsWith(`${prefix}/`));
            for (const ns of namespaces) {
                expect(inGroup.some(({ used }) => used.has(ns)), `${prefix} lists ${ns}`).toBe(true);
            }
        }
    });

    it("matches whole segments only", () => {
        expect(namespacesForPath("/app/accounting")).toEqual(["accounting"]);
        expect(namespacesForPath("/app/accounting/fuel")).toEqual(["accounting"]);
        expect(namespacesForPath("/app/accountingx")).toEqual([]);
        expect(namespacesForPath("/app/dashboard")).toEqual([]);
        expect(namespacesForPath(null)).toEqual([]);
    });
});

describe("split dictionaries", () => {
    it("the base dictionaries hold none of the split namespaces' keys", () => {
        for (const [base, parts] of [
            [enBase, [enAccounting, enDriverMonitor]],
            [thBase, [thAccounting, thDriverMonitor]],
        ] as const) {
            const overlap = parts.flatMap((part) => Object.keys(part).filter((k) => k in base));
            expect(overlap).toEqual([]);
        }
    });

    it("each split namespace has the same keys in English and Thai", () => {
        for (const [en, th] of [
            [enAccounting, thAccounting],
            [enDriverMonitor, thDriverMonitor],
        ] as const) {
            expect(Object.keys(th).filter((k) => !(k in en))).toEqual([]);
            expect(Object.keys(en).filter((k) => !(k in th))).toEqual([]);
        }
    });
});
