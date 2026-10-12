// T46 (issue #66), TW7: the five HR routes render their Go pages only, which import no Firebase module,
// statically or lazily (test-utils/importGraph.ts), so they call none of generateDriverPayoutRun,
// approveDriverPayout or the holiday callables (saveHoliday, deleteHoliday, saveGeneratedHolidays): AC 1 of
// the issue. TW7 removed the Firestore pages (`features/*/legacy/*`) and the `hr` flag switch.
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
    it("renders only its Go page per route (TW7: no Firestore page, no hr flag switch)", () => {
        for (const r of ROUTES) {
            const src = readFileSync(r, "utf8");
            expect(src, path.relative(root, r)).toMatch(/const GoPage = dynamic\(/);
            expect(src, path.relative(root, r)).not.toMatch(/DomainPage|FirestorePage|useDomainPageSource|\/legacy\//);
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
});
