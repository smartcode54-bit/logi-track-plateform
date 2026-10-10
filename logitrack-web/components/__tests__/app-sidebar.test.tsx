// TW4 (#84; developer-spec.md §10.5, §10.7; Appendix E §E.5 row 19): the sidebar shows exactly the
// routes the proxy.ts gate allows over ['me'], and its waitlist badge is ['badges'] (one count query,
// polled), not a listener on the whole waitlist collection.
import React from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const firestore = vi.hoisted(() => ({
    onSnapshot: vi.fn(() => () => undefined),
    getCountFromServer: vi.fn(async () => ({ data: () => ({ count: 7 }) })),
    collection: vi.fn((_db: unknown, name: string) => ({ name })),
}));

vi.mock("@/firebase/client", () => ({ db: {}, auth: {}, functions: {}, storage: {} }));
vi.mock("firebase/firestore", () => firestore);
vi.mock("next/navigation", () => ({ usePathname: () => "/app/dashboard", useRouter: () => ({ push: vi.fn(), replace: vi.fn() }) }));
vi.mock("@/context/auth", () => ({
    useAuth: () => ({ logout: vi.fn(), customClaims: { role: "manager", admin: false } }),
}));

const { AppSidebar } = await import("../app-sidebar");
const { SidebarProvider } = await import("@/components/ui/sidebar");
const { LanguageProvider } = await import("@/context/language");
const { createQueryClient } = await import("@/lib/queryClient");
const { BADGES_POLL_MS, badgesQueryOptions, fetchBadgesFromFirestore } = await import("@/features/dashboard/api/badges");

function principal(capabilities: string[]) {
    return {
        id: "u1",
        email: "ann@example.test",
        displayName: "Ann",
        photoUrl: null,
        tenant: { id: "t1", nameTh: "ท", nameEn: null, kind: "own_fleet", role: "manager" },
        tenants: [],
        platformRoles: [],
        dispatcher: false,
        steward: true,
        driver: null,
        customerScopes: [],
        capabilities,
        mustChangePassword: false,
    };
}

beforeEach(() => {
    firestore.onSnapshot.mockClear();
    firestore.getCountFromServer.mockClear();
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL) => {
            if (String(input) === "/api/go/v1/config/web-flags") return new Response(JSON.stringify({ data: { domains: {} } }), { status: 200 });
            throw new Error(`unexpected ${String(input)}`);
        })
    );
});

afterEach(() => {
    vi.unstubAllGlobals();
});

async function renderSidebar(capabilities: string[]) {
    const client = createQueryClient();
    client.setQueryData(["me"], principal(capabilities));
    render(
        <QueryClientProvider client={client}>
            <LanguageProvider>
                <SidebarProvider>
                    <AppSidebar />
                </SidebarProvider>
            </LanguageProvider>
        </QueryClientProvider>
    );
    await screen.findByText("LogiTrack Pro");
    return client;
}

const hrefs = () =>
    within(document.body)
        .queryAllByRole("link")
        .map((a) => a.getAttribute("href"));

describe("AppSidebar", () => {
    it("lists the routes the gate allows, from ['me'] capabilities", async () => {
        await renderSidebar(["fleet:view_trucks", "accounting:view_income", "waitlist:view"]);
        const links = hrefs();
        expect(links).toEqual(expect.arrayContaining(["/app/dashboard", "/app/trucks", "/app/accounting/income", "/app/waitlist"]));
        for (const hidden of ["/app/drivers", "/app/accounting/fuel", "/app/security-center", "/app/utilities/backfill", "/app/companies"]) {
            expect(links).not.toContain(hidden);
        }
    });

    it("counts the waitlist with one count query and opens no listener", async () => {
        await renderSidebar(["waitlist:view"]);
        await waitFor(() => expect(screen.getByText("Waitlist (7)")).toBeInTheDocument());
        expect(firestore.getCountFromServer).toHaveBeenCalledTimes(1);
        expect(firestore.onSnapshot).not.toHaveBeenCalled();
    });

    it("reads no waitlist count without waitlist:view", async () => {
        await renderSidebar(["fleet:view_trucks"]);
        await waitFor(() => expect(hrefs()).toContain("/app/trucks"));
        await new Promise((r) => setTimeout(r, 20));
        expect(firestore.getCountFromServer).not.toHaveBeenCalled();
        expect(firestore.onSnapshot).not.toHaveBeenCalled();
    });
});

describe("['badges']", () => {
    it("is polled every 60 s while visible, 30 s stale", () => {
        expect(badgesQueryOptions.queryKey).toEqual(["badges"]);
        expect(badgesQueryOptions.staleTime).toBe(30_000);
        expect(badgesQueryOptions.refetchInterval).toBe(BADGES_POLL_MS);
        expect(BADGES_POLL_MS).toBe(60_000);
        expect(badgesQueryOptions.refetchIntervalInBackground).toBe(false);
    });

    it("the P0 source gates each counter by its capability", async () => {
        const client = createQueryClient();
        client.setQueryData(["me"], principal(["chat:view"]));
        await expect(fetchBadgesFromFirestore({ client })).resolves.toEqual({});
        client.setQueryData(["me"], principal(["waitlist:view"]));
        await expect(fetchBadgesFromFirestore({ client })).resolves.toEqual({ waitlist: 7 });
        expect(firestore.getCountFromServer).toHaveBeenCalledTimes(1);
    });
});
