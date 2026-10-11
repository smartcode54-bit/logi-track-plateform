// T54S: the Go Security Center pages (overview, roles, audit, api-keys, status, mobile-clients,
// mobile-release) import no Firebase module, statically or lazily, so their chunks carry no Firebase SDK.
// This walks the import graph of the module each route's `GoPage` loads (the component tests mock
// Firebase to throw, which catches a static import but not a lazy one that a try/catch swallows).
//
// It covers the page code only. The auth layer that the providers mount on every /app route still
// loads the Firebase client lazily until TW7: the bridge (`lib/firebaseBridge.ts` -> `./firebaseBridgeSdk`,
// the custom-token sign-in that `useAuth().loading` waits for) and the legacy last-login write
// (`context/auth.tsx` -> `@/lib/updateUserLastLogin`). So at runtime a Go page still loads the SDK
// through the bridge (developer-spec.md §10.4, §10.13 as built); the last test pins that, so the
// difference stays visible.
import { existsSync, readFileSync, statSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "../..");
const ROUTES = ["", "roles", "audit", "api-keys", "status", "mobile-clients", "mobile-release"].map((r) =>
    path.join(root, "app/app/security-center", r, "page.tsx")
);
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
function firebaseImports(entry: string): string[] {
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
            if (next) walk(next, [...chain, label(path.relative(root, next))]);
        }
    };
    walk(entry, [path.relative(root, entry)]);
    return found;
}

describe("the Go Security Center pages' import graph", () => {
    it("has a Go page per route", () => {
        expect(ROUTES.map((r) => path.relative(root, goPageModule(r))).sort()).toEqual(
            [
                "features/mobile-release/components/MobileClientsPage.tsx",
                "features/mobile-release/components/MobileReleasePage.tsx",
                "features/security-center/components/ApiKeysPage.tsx",
                "features/security-center/components/RoleMatrixPage.tsx",
                "features/security-center/components/SecurityAuditPage.tsx",
                "features/security-center/components/SecurityOverviewPage.tsx",
                "features/security-center/components/SystemStatusPage.tsx",
            ].sort()
        );
    });

    it.each(ROUTES.map((r) => [path.relative(root, r), r]))("%s: the Go page imports no Firebase module", (_name, route) => {
        expect(firebaseImports(goPageModule(route))).toEqual([]);
    });

    it("sees Firebase where it is: the legacy pages, and the auth layer's lazy imports (until TW7)", () => {
        expect(firebaseImports(path.join(root, "features/security-center/legacy/LegacyAuditPage.tsx")).length).toBeGreaterThan(0);
        const authLayer = firebaseImports(path.join(root, "context/auth.tsx"));
        expect(authLayer).toContain("context/auth.tsx -> lib/firebaseBridge.ts -> import(lib/firebaseBridgeSdk.ts) -> @/firebase/client");
        expect(authLayer).toContain("context/auth.tsx -> import(lib/updateUserLastLogin.ts) -> @/firebase/client");
    });
});
