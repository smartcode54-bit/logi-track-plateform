// T46 (issue #66): the five HR routes switch as whole pages on the `hr` flag, and their Go pages import no
// Firebase module, statically or lazily (test-utils/importGraph.ts), so with the flag on they call none of
// generateDriverPayoutRun, approveDriverPayout or the holiday callables (saveHoliday, deleteHoliday,
// saveGeneratedHolidays): AC 1 of the issue. The Firestore pages (`features/*/legacy/*`) keep theirs while the
// flag can still be turned off.
import { existsSync, readFileSync, statSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";

import { firebaseImports, goPageModule, resolveImport, WEB_ROOT as root } from "@/test-utils/importGraph";

const ROUTES = ["payroll", "payroll/config", "payroll/penalties", "leave-requests", "holidays"].map((r) => path.join(root, "app/app", r, "page.tsx"));
// A callable is reached through `httpsCallable(functions, "<name>")`: the factory, or the name as a string.
const CALLABLES = /\bhttpsCallable\b|["'`](generateDriverPayoutRun|approveDriverPayout|saveHoliday|deleteHoliday|saveGeneratedHolidays)["'`]/;
const STATIC_RX = /(?:^|[\n;])\s*(import|export)\s+(type\s+)?(?:[^'";]*?\s+from\s+)?["']([^"']+)["']/g;
const DYNAMIC_RX = /import\(\s*["']([^"']+)["']\s*\)/g;

const code = (file: string) =>
    readFileSync(file, "utf8")
        .replace(/\/\*[\s\S]*?\*\//g, "")
        .replace(/^\s*\/\/.*$/gm, "");

/** Every module of the app reachable from `entry` (static and lazy edges; type-only imports erased). */
function graph(entry: string): string[] {
    const seen = new Set<string>();
    const walk = (file: string) => {
        if (seen.has(file)) return;
        seen.add(file);
        const src = code(file);
        const specs = [...[...src.matchAll(STATIC_RX)].filter((m) => !m[2]).map((m) => m[3]), ...[...src.matchAll(DYNAMIC_RX)].map((m) => m[1])];
        for (const spec of specs) {
            const next = resolveImport(file, spec);
            if (next && existsSync(next) && statSync(next).isFile()) walk(next);
        }
    };
    walk(entry);
    return [...seen];
}

describe("the Go HR pages' import graph", () => {
    it("has a Go page and a Firestore page per route, switched on the hr flag", () => {
        for (const r of ROUTES) {
            const src = readFileSync(r, "utf8");
            expect(src, path.relative(root, r)).toContain('<DomainPage domain="hr"');
            expect(src, path.relative(root, r)).toMatch(/const FirestorePage = dynamic\(\(\) => import\("@\/features\/(driver-compensation|leave|holidays)\/legacy\//);
        }
    });

    it.each(ROUTES.map((r) => [path.relative(root, r), r]))("%s: the Go page imports no Firebase module", (_name, route) => {
        expect(firebaseImports(goPageModule(route))).toEqual([]);
    });

    it.each(ROUTES.map((r) => [path.relative(root, r), r]))("%s: the Go page names no Cloud Function callable (AC 1)", (_name, route) => {
        const offenders = graph(goPageModule(route))
            .filter((f) => CALLABLES.test(code(f)))
            .map((f) => path.relative(root, f));
        expect(offenders).toEqual([]);
    });

    it("sees Firebase and the callables where they are: the Firestore pages", () => {
        for (const legacy of [
            "features/driver-compensation/legacy/LegacyPayrollPage.tsx",
            "features/leave/legacy/LegacyLeaveRequestsPage.tsx",
            "features/holidays/legacy/LegacyHolidaysPage.tsx",
        ]) {
            expect(firebaseImports(path.join(root, legacy)).length, legacy).toBeGreaterThan(0);
        }
        expect(code(path.join(root, "features/driver-compensation/legacy/LegacyPayrollPage.tsx"))).toMatch(/generateDriverPayoutRun/);
    });
});
