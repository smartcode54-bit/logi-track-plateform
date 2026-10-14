// T54R (issue #74): the Go dashboard and waitlist pages, and the anonymous landing forms, import no
// Firebase module, statically or lazily (test-utils/importGraph.ts; the component tests mock Firebase to
// throw, which catches only a static import). TW7 removed the last lazy edge, the Firestore source of
// `['badges']` behind the `dashboard` flag (`badges.firestore.ts`), and the legacy pages.
import path from "path";
import { describe, expect, it } from "vitest";

import { firebaseImports, goPageModule, WEB_ROOT as root } from "@/test-utils/importGraph";

const ROUTES = ["app/app/dashboard/page.tsx", "app/app/waitlist/page.tsx"].map((r) => path.join(root, r));

describe("the Go dashboard and waitlist pages' import graph", () => {
    it("has a Go page per route", () => {
        expect(ROUTES.map((r) => path.relative(root, goPageModule(r)))).toEqual([
            "features/dashboard/components/DashboardPage.tsx",
            "features/waitlist/components/WaitlistPage.tsx",
        ]);
    });

    it("the dashboard imports no Firebase module, ['badges'] included (TW7)", () => {
        expect(firebaseImports(goPageModule(ROUTES[0]))).toEqual([]);
    });

    it("the waitlist page imports no Firebase module", () => {
        expect(firebaseImports(goPageModule(ROUTES[1]))).toEqual([]);
    });

    it.each(["features/auth/components/WaitlistForm.tsx", "app/join-network/page.tsx"])(
        "the anonymous form %s imports no Firebase module (AC: it posts only to /api/forms/*)",
        (file) => {
            expect(firebaseImports(path.join(root, file))).toEqual([]);
        }
    );
});
