// T25 acceptance (issue #45): "With web flag `masterdata` on, these pages import nothing from
// `firebase/*`". Since TW7 there is no flag and no Firestore branch: the `*.firestore.ts` adapters and
// `domainQueryFn` / `domainMutationFn` are gone, and the API modules call Go only.
//
// This walks each page's module graph the way the bundler loads it at render time (static imports
// and `next/dynamic` component imports; an `await import()` inside a function loads only when that
// function runs) and fails when it reaches the Firebase SDK, `@/firebase/client` or an adapter. A
// second check keeps the adapters from coming back.
import { existsSync, readdirSync, readFileSync, statSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "../../..");

/** The pages of issue #45 (P1, flag `masterdata`, Appendix E §E.2.0). */
const MASTER_DATA_ROUTES = [
    "customers",
    "subcontractors",
    "companies",
    "settings/company-profile",
    "sources",
    "trucks",
    "renewals",
    "truck-assignment",
    "drivers",
];
/** `/app/trucks/maintenance` moves with maintenance (P3, flag `billing`). */
const EXCLUDED = new Set(["app/app/trucks/maintenance/page.tsx"]);

const STATIC_IMPORT_RX = /(?:^|\n)\s*(?:import|export)\s+(?:type\s+)?(?:[^'"]*?\s+from\s+)?["']([^"']+)["']/g;
const DYNAMIC_COMPONENT_RX = /dynamic\(\s*\(\)\s*=>\s*import\(\s*["']([^"']+)["']\s*\)/g;
const ANY_IMPORT_CALL_RX = /import\(\s*["']([^"']+)["']\s*\)/g;

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

/** Source without comments (a comment may name a module it does not import). */
function code(file: string): string {
    return readFileSync(file, "utf8")
        .replace(/\/\*[\s\S]*?\*\//g, "")
        .replace(/^\s*\/\/.*$/gm, "");
}

const isFirebaseSpec = (spec: string) => spec === "firebase" || spec.startsWith("firebase/") || spec === "@/firebase/client";
const isAdapter = (file: string) => /\.firestore\.tsx?$/.test(file);

/** Modules a page loads when it renders, and what they import from Firebase or the adapters. */
function renderGraph(entry: string): { files: Set<string>; offenders: string[] } {
    const files = new Set([entry]);
    const stack = [entry];
    const offenders: string[] = [];
    while (stack.length > 0) {
        const file = stack.pop()!;
        const src = code(file);
        const specs = [...src.matchAll(STATIC_IMPORT_RX)]
            .filter((m) => !/^\s*(?:import|export)\s+type\s/.test(m[0].replace(/^\n/, "")))
            .map((m) => m[1])
            .concat([...src.matchAll(DYNAMIC_COMPONENT_RX)].map((m) => m[1]));
        for (const spec of specs) {
            const rel = path.relative(root, file);
            if (isFirebaseSpec(spec)) {
                offenders.push(`${rel} -> ${spec}`);
                continue;
            }
            const resolved = resolveImport(file, spec);
            if (!resolved) continue;
            if (isAdapter(resolved)) {
                offenders.push(`${rel} -> ${path.relative(root, resolved)}`);
                continue;
            }
            if (!files.has(resolved)) {
                files.add(resolved);
                stack.push(resolved);
            }
        }
    }
    return { files, offenders };
}

function walk(dir: string, out: string[] = []): string[] {
    for (const name of readdirSync(dir)) {
        const p = path.join(dir, name);
        if (statSync(p).isDirectory()) {
            if (name !== "node_modules" && name !== "__tests__") walk(p, out);
        } else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p);
    }
    return out;
}

const pages = MASTER_DATA_ROUTES.flatMap((route) => walk(path.join(root, "app/app", route)).filter((f) => f.endsWith("page.tsx")))
    .map((f) => path.relative(root, f))
    .filter((f) => !EXCLUDED.has(f));

describe("master-data pages without Firebase (T25)", () => {
    it("finds exactly the 22 pages of issue #45 (Appendix E §E.2.0, P1 masterdata)", () => {
        // An exact list: a page that drops out of the walk loses its no-Firebase check, so it must fail here.
        expect([...pages].sort()).toEqual(
            [
                "app/app/companies/page.tsx",
                "app/app/customers/[id]/edit/page.tsx",
                "app/app/customers/[id]/page.tsx",
                "app/app/customers/new/page.tsx",
                "app/app/customers/page.tsx",
                "app/app/drivers/edit/page.tsx",
                "app/app/drivers/new/page.tsx",
                "app/app/drivers/page.tsx",
                "app/app/drivers/view/page.tsx",
                "app/app/renewals/page.tsx",
                "app/app/settings/company-profile/page.tsx",
                "app/app/sources/page.tsx",
                "app/app/subcontractors/[id]/edit/page.tsx",
                "app/app/subcontractors/[id]/page.tsx",
                "app/app/subcontractors/new/page.tsx",
                "app/app/subcontractors/page.tsx",
                "app/app/truck-assignment/page.tsx",
                "app/app/trucks/edit/page.tsx",
                "app/app/trucks/new/page.tsx",
                "app/app/trucks/page.tsx",
                "app/app/trucks/renew/page.tsx",
                "app/app/trucks/view/page.tsx",
            ].sort()
        );
    });

    it.each(pages)("%s loads no Firebase module and no Firestore adapter when it renders", (page) => {
        const { files, offenders } = renderGraph(path.join(root, page));
        expect(files.size).toBeGreaterThan(1);
        expect(offenders).toEqual([]);
    });

    it("no Firestore adapter is left, and no API module loads one (TW7)", () => {
        const adapters = ["app", "components", "context", "features", "hooks", "lib"]
            .flatMap((d) => walk(path.join(root, d)))
            .filter(isAdapter)
            .map((f) => path.relative(root, f));
        expect(adapters).toEqual([]);
        const apis = [
            "features/customers/api/customers.ts",
            "features/subcontractors/api/subcontractors.ts",
            "features/companies/api/companies.ts",
            "features/hubs/api/hubs.ts",
            "features/trucks/api/trucks.ts",
            "features/trucks/api/truckAssignments.ts",
            "features/drivers/api/drivers.ts",
        ];
        for (const api of apis) {
            const lazy = [...code(path.join(root, api)).matchAll(ANY_IMPORT_CALL_RX)].map((m) => m[1]);
            expect(lazy.filter((spec) => spec.endsWith(".firestore")), api).toEqual([]);
            expect(code(path.join(root, api)), api).not.toMatch(/domainQueryFn|domainMutationFn/);
        }
    });
});
