// T54S: the Go Security Center pages (overview, roles, audit, api-keys, status, mobile-clients,
// mobile-release) import no Firebase module, statically or lazily, so their chunks carry no Firebase SDK.
// This walks the import graph of the module each route's `GoPage` loads (the component tests mock
// Firebase to throw, which catches a static import but not a lazy one that a try/catch swallows).
//
// TW7 removed what the auth layer still loaded lazily on every /app route (the Firebase bridge and the
// legacy last-login write) and the Firestore pages; the last test pins that the auth layer and the
// providers reach no Firebase module either (developer-spec.md §10.4, §10.13 as built).
import path from "path";
import { describe, expect, it } from "vitest";

import { firebaseImports, goPageModule, WEB_ROOT as root } from "@/test-utils/importGraph";

const ROUTES = ["", "roles", "audit", "api-keys", "status", "mobile-clients", "mobile-release"].map((r) =>
    path.join(root, "app/app/security-center", r, "page.tsx")
);

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

    it("the auth layer, the providers and the /app shell reach no Firebase module (TW7)", () => {
        for (const entry of ["context/auth.tsx", "app/providers.tsx", "app/app/layout.tsx", "app/layout.tsx"]) {
            expect(firebaseImports(path.join(root, entry)), entry).toEqual([]);
        }
    });
});
