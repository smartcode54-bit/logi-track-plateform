// TW4 (#84; developer-spec.md §10.5, §10.7; Appendix E §E.5 row 19): the sidebar shows exactly the
// routes the proxy.ts gate allows over ['me'], and its waitlist badge is ['badges'] (`GET /v1/badges`,
// polled), not a listener on the whole waitlist collection. TW7: the badge reads Go only, with no web
// flag to wait for (T54R had a Firestore count query while the `dashboard` flag was off).
import React from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({ usePathname: () => "/app/dashboard", useRouter: () => ({ push: vi.fn(), replace: vi.fn() }) }));

const { AppSidebar } = await import("../app-sidebar");
const { SidebarProvider } = await import("@/components/ui/sidebar");
const { LanguageProvider } = await import("@/context/language");
const { createQueryClient } = await import("@/lib/queryClient");
const { BADGES_POLL_MS, badgesQueryOptions } = await import("@/features/dashboard/api/badges");

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

let fetched: string[] = [];

beforeEach(() => {
    fetched = [];
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL) => {
            fetched.push(String(input));
            if (String(input) === "/api/go/v1/badges") return new Response(JSON.stringify({ data: { waitlist: 12 } }), { status: 200 });
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
        expect(await screen.findByText("Waitlist (12)")).toBeInTheDocument();
    });

    it("names the slice in its header from ['me'] and leaves sign-out to the user menu (UI3)", async () => {
        await renderSidebar(["fleet:view_trucks"]);
        // The tenant has no English name, so the Thai one stands in; the role is the label of principalRoleLabel.
        expect(screen.getByTestId("sidebar-scope")).toHaveTextContent("ท · Manager");
        expect(screen.queryByText("Logout")).toBeNull();
        expect(screen.getByText("Main menu")).toBeInTheDocument();
        expect(screen.queryByText("MAIN MENU")).toBeNull();
    });

    it("counts the waitlist from one GET /v1/badges, with no web flags request (TW7)", async () => {
        await renderSidebar(["waitlist:view"]);
        await waitFor(() => expect(screen.getByText("Waitlist (12)")).toBeInTheDocument());
        expect(fetched).toEqual(["/api/go/v1/badges"]);
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
});
