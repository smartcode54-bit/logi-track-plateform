// T54R (issue #74): the Go dashboard and waitlist pages, and the anonymous landing forms, import no
// Firebase module, statically or lazily (test-utils/importGraph.ts; the component tests mock Firebase to
// throw, which catches only a static import). One lazy edge is expected and pinned: the shell's
// `['badges']` (the sidebar mounts it on every /app route, the dashboard reads its counters) keeps its
// Firestore source for the `dashboard` flag being off, behind `import("./badges.firestore")`, so that
// chunk loads only when the flag sends a fetch there, never on a Go page. The auth layer's own lazy SDK
// loads until TW7 are covered by features/security-center/firebaseImports.test.ts.
import path from "path";
import { describe, expect, it } from "vitest";

import { firebaseImports, goPageModule, WEB_ROOT as root } from "@/test-utils/importGraph";

const ROUTES = ["app/app/dashboard/page.tsx", "app/app/waitlist/page.tsx"].map((r) => path.join(root, r));

// The flag-off source of ['badges'], reached only through its lazy import.
const BADGES_FLAG_OFF = "features/dashboard/api/useBadges.ts -> features/dashboard/api/badges.ts -> import(features/dashboard/api/badges.firestore.ts) -> ";

describe("the Go dashboard and waitlist pages' import graph", () => {
    it("has a Go page per route", () => {
        expect(ROUTES.map((r) => path.relative(root, goPageModule(r)))).toEqual([
            "features/dashboard/components/DashboardPage.tsx",
            "features/waitlist/components/WaitlistPage.tsx",
        ]);
    });

    it("the dashboard reaches Firebase only through the lazy flag-off source of ['badges']", () => {
        const found = firebaseImports(goPageModule(ROUTES[0]));
        expect(found.length).toBeGreaterThan(0);
        for (const chain of found) expect(chain).toContain(BADGES_FLAG_OFF);
        expect(found.some((chain) => chain.endsWith(`${BADGES_FLAG_OFF}firebase/firestore`))).toBe(true);
    });

    it("the waitlist page imports no Firebase module", () => {
        expect(firebaseImports(goPageModule(ROUTES[1]))).toEqual([]);
    });

    it.each(["components/landing/WaitlistModal.tsx", "features/auth/components/WaitlistForm.tsx", "app/join-network/page.tsx"])(
        "the anonymous form %s imports no Firebase module (AC: it posts only to /api/forms/*)",
        (file) => {
            expect(firebaseImports(path.join(root, file))).toEqual([]);
        }
    );

    it("the legacy pages still reach Firestore (they are what the flag falls back to until TW7)", () => {
        expect(firebaseImports(path.join(root, "features/dashboard/legacy/LegacyDashboardPage.tsx")).length).toBeGreaterThan(0);
        expect(firebaseImports(path.join(root, "features/waitlist/legacy/LegacyWaitlistPage.tsx")).length).toBeGreaterThan(0);
    });
});
