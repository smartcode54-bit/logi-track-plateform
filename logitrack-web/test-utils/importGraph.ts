/**
 * The import-graph walker of the Firebase-free page tests (T54S, T54R): from a module, every Firebase
 * specifier reachable through static `import`/`export ... from` (type-only ones are erased) and lazy
 * `import("...")` edges, as the chain that reaches it. Component tests mock Firebase to throw, which
 * catches a static import but not a lazy one that a try/catch swallows; this walks both.
 */
import { existsSync, readFileSync, statSync } from "fs";
import path from "path";

export const WEB_ROOT = path.resolve(__dirname, "..");

const FIREBASE_RX = /^(firebase|@firebase)(\/|$)|^@\/firebase(\/|$)/;
const STATIC_RX = /(?:^|[\n;])\s*(import|export)\s+(type\s+)?(?:[^'";]*?\s+from\s+)?["']([^"']+)["']/g;
const DYNAMIC_RX = /import\(\s*["']([^"']+)["']\s*\)/g;

export function resolveImport(from: string, spec: string): string | undefined {
    let base: string;
    if (spec.startsWith("@/")) base = path.join(WEB_ROOT, spec.slice(2));
    else if (spec.startsWith("shared-docs/")) base = path.join(WEB_ROOT, "..", spec);
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
export function goPageModule(route: string): string {
    const m = /const GoPage = dynamic\(\(\) => import\(["']([^"']+)["']\)/.exec(readFileSync(route, "utf8"));
    if (!m) throw new Error(`${path.relative(WEB_ROOT, route)}: no GoPage`);
    const file = resolveImport(route, m[1]);
    if (!file) throw new Error(`${path.relative(WEB_ROOT, route)}: cannot resolve ${m[1]}`);
    return file;
}

/** Every Firebase specifier reachable from `entry`, as the import chain that reaches it; a lazy edge is written `import(x)`. */
export function firebaseImports(entry: string): string[] {
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
            if (FIREBASE_RX.test(spec)) found.push([...chain, label(spec)].join(" -> "));
            const next = resolveImport(file, spec);
            if (next) walk(next, [...chain, label(path.relative(WEB_ROOT, next))]);
        }
    };
    walk(entry, [path.relative(WEB_ROOT, entry)]);
    return found;
}
