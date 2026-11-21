// TW8 review fixes (issue #88, M1 slice; developer-spec.md §10.10 W9): the truck-assignment history's paging
// against a Go origin that answers with real latency (a response that resolves in microtasks hides these races):
// - a failed next page is not requested again by itself (no request loop, one 403 toast); Try again retries it;
// - a page fetched by Next never cancels a background refetch (an invalidation's fresh rows are kept);
// - a failed first page shows the load error, not "no history"; the search box stops at Go's 100 characters.
import React from "react";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({
    useRouter: () => ({ push: vi.fn(), replace: vi.fn(), prefetch: vi.fn(), back: vi.fn() }),
    usePathname: () => "/app",
    useSearchParams: () => new URLSearchParams(),
}));

import { LanguageProvider } from "@/context/language";
import { createQueryClient, setForbiddenNotifier } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { fakeWeb, goErr, json, type FakeCall } from "@/test-utils/fakeWeb";
import { stubVirtualLayout } from "@/test-utils/virtualLayout";
import { AssignmentHistory, ASSIGNMENT_SEARCH_MAX, HISTORY_ROW_HEIGHT } from "./AssignmentHistory";
import { ASSIGNMENTS_PAGE_SIZE, type TruckAssignmentDTO } from "../api/truckAssignments";

const PATH = "/api/go/v1/truck-assignments";
const LOAD_ERROR = "Could not load assignment history. Try again.";
const NEXT_PAGE_ERROR = "The next page could not be loaded.";
const NO_HISTORY = "No recent assignments found.";

let restoreLayout: () => void;
beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
    restoreLayout = stubVirtualLayout({ viewport: 600, row: HISTORY_ROW_HEIGHT });
});
afterAll(() => restoreLayout());
beforeEach(() => {
    try {
        window.localStorage.setItem("language", "en");
    } catch {
        // Storage may be unavailable in the test environment.
    }
});
afterEach(() => vi.unstubAllGlobals());

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
/** Lets real time pass with React's updates flushed. */
const wait = (ms: number) => act(() => sleep(ms));

function assignment(i: number, driverName = `Driver ${i}`): TruckAssignmentDTO {
    return {
        id: `a-${i}`, tenantId: "t-own", truckId: `t-${i}`, truckPlate: `PLATE-${String(i).padStart(4, "0")}`,
        truckModel: "FRR", driverId: `d-${i}`, driverName, status: "revoked", adminUserId: null, adminLabel: "Admin",
        createdAt: new Date(Date.UTC(2026, 9, 1) - i * 60_000).toISOString(), revokedAt: null,
    };
}

/**
 * Go's history list (keyset, `limit` rows from the cursor = the next index) answering after `latency` ms;
 * `fail(params)` returns the error response of a request that should fail.
 */
function historyServer(rows: TruckAssignmentDTO[], latency: number, fail: (p: URLSearchParams) => Response | undefined = () => undefined) {
    const web = fakeWeb();
    web.on("GET", PATH, async (call: FakeCall) => {
        await sleep(latency);
        const p = new URL(call.url, "http://web.test").searchParams;
        const failed = fail(p);
        if (failed) return failed;
        const q = (p.get("q") ?? "").toLowerCase();
        const all = rows.filter((r) => !q || (r.truckPlate ?? "").toLowerCase().includes(q));
        const limit = Number(p.get("limit"));
        const from = Number(p.get("cursor") ?? "0");
        const next = from + limit < all.length ? String(from + limit) : undefined;
        return json(200, { data: all.slice(from, from + limit), ...(next ? { nextCursor: next } : {}) });
    });
    const calls = () => web.calls.filter((c) => c.url.startsWith(PATH)).map((c) => new URL(c.url, "http://web.test").searchParams);
    return { web, calls };
}

function renderHistory(): QueryClient {
    const client = createQueryClient();
    render(
        <QueryClientProvider client={client}>
            <LanguageProvider>
                <AssignmentHistory onRevoke={vi.fn()} />
            </LanguageProvider>
        </QueryClientProvider>
    );
    return client;
}

const PAGE = 50;
/** The footer of page `n` (1-based) of `PAGE` rows; `total` once the last server page is loaded. */
const range = (n: number, total?: number) => `Rows ${(n - 1) * PAGE + 1}–${n * PAGE}${total === undefined ? "" : ` of ${total}`}`;
const next = () => screen.getByRole("button", { name: "Next" });

describe("AssignmentHistory: a failed next page", () => {
    it.each([
        [422, "invalid_argument"],
        [403, "permission_denied"],
    ])("a %i is requested once by Next, shown in the footer, and retried by Try again", async (status, code) => {
        const rows = Array.from({ length: 250 }, (_, i) => assignment(i));
        let down = true;
        const server = historyServer(rows, 20, (p) => (down && p.get("cursor") ? goErr(status, code) : undefined));
        const toast = vi.fn();
        const unregister = setForbiddenNotifier(toast);
        try {
            renderHistory();
            expect(await screen.findByText(range(1))).toBeInTheDocument();
            const u = userEvent.setup();
            const cursorCalls = () => server.calls().filter((p) => p.get("cursor") !== null);

            // The second 50 are already loaded (server pages are 100): no request.
            await u.click(next());
            expect(screen.getByText(range(2))).toBeInTheDocument();
            expect(cursorCalls()).toHaveLength(0);

            await u.click(next());
            // The failure settles after 20 ms; a loop would have asked dozens of times by now.
            await wait(600);
            expect(cursorCalls()).toHaveLength(1);
            expect(toast).toHaveBeenCalledTimes(status === 403 ? 1 : 0);
            // The loaded rows stay; the footer says the next page failed and Try again is the retry.
            expect(screen.getByText(range(2))).toBeInTheDocument();
            expect(screen.getByRole("alert")).toHaveTextContent(NEXT_PAGE_ERROR);
            expect(screen.getByText(`Driver ${PAGE}`)).toBeInTheDocument();

            down = false;
            await u.click(screen.getByRole("button", { name: "Try again" }));
            expect(await screen.findByText(range(3))).toBeInTheDocument();
            expect(screen.queryByRole("alert")).toBeNull();
            expect(cursorCalls()).toHaveLength(2);

            // Once a page succeeds, Next pages again: one request per server page reached.
            await u.click(next());
            expect(screen.getByText(range(4))).toBeInTheDocument();
            expect(cursorCalls()).toHaveLength(2);
            await u.click(next());
            expect(await screen.findByText(range(5, 250))).toBeInTheDocument();
            expect(cursorCalls()).toHaveLength(3);
            expect(next()).toBeDisabled();
        } finally {
            unregister();
        }
    });
});

describe("AssignmentHistory: paging and a background refetch", () => {
    // The prefix every assignment write and `assignment.*` event invalidates.
    const prefix = queryKeys.truckAssignments();

    it("Next past the loaded rows during a refetch keeps the refetched rows and loads the next page after it", async () => {
        const rows = Array.from({ length: 250 }, (_, i) => assignment(i));
        const server = historyServer(rows, 100);
        const client = renderHistory();
        expect(await screen.findByText(range(1))).toBeInTheDocument();
        const u = userEvent.setup();
        await u.click(next());

        // Row 0 changes on the server (a revoke, another admin's write); the page's key is invalidated.
        rows[0] = assignment(0, "CHANGED 0");
        act(() => {
            void client.invalidateQueries({ queryKey: prefix });
        });
        await wait(30);
        // The refetch is in flight when the reader steps past the loaded rows.
        await u.click(next());
        expect(await screen.findByText(range(3), undefined, { timeout: 2000 })).toBeInTheDocument();
        expect(server.calls().map((p) => p.toString())).toEqual(["limit=100", "limit=100", "limit=100&cursor=100"]);

        await u.click(screen.getByRole("button", { name: "First page" }));
        expect(screen.getByText("CHANGED 0")).toBeInTheDocument();
        expect(screen.queryByText("Driver 0")).toBeNull();
    });

    it("an invalidation while Next's page is in flight keeps the refetched rows and still loads the page", async () => {
        const rows = Array.from({ length: 250 }, (_, i) => assignment(i));
        const server = historyServer(rows, 100);
        const client = renderHistory();
        expect(await screen.findByText(range(1))).toBeInTheDocument();
        const u = userEvent.setup();
        await u.click(next());

        await u.click(next());
        await wait(30);
        // The next page is in flight; an `assignment.*` event invalidates the key (it cancels that page).
        rows[0] = assignment(0, "CHANGED 0");
        act(() => {
            void client.invalidateQueries({ queryKey: prefix });
        });
        expect(await screen.findByText(range(3), undefined, { timeout: 2000 })).toBeInTheDocument();
        await wait(250);
        expect(screen.getByText(range(3))).toBeInTheDocument();

        await u.click(screen.getByRole("button", { name: "First page" }));
        expect(screen.getByText("CHANGED 0")).toBeInTheDocument();
        expect(screen.queryByText("Driver 0")).toBeNull();
        // The cancelled page, the refetch of the first page, then the page again.
        expect(server.calls().map((p) => p.toString())).toEqual(["limit=100", "limit=100&cursor=100", "limit=100", "limit=100&cursor=100"]);
    });
});

describe("AssignmentHistory: search errors", () => {
    it("a failed first page shows the load error, not the empty history", async () => {
        const rows = Array.from({ length: 10 }, (_, i) => assignment(i));
        historyServer(rows, 5, (p) => (p.get("q") === "boom" ? goErr(422, "invalid_argument", { fields: { q: "invalid" } }) : undefined));
        renderHistory();
        expect(await screen.findByText("10 rows")).toBeInTheDocument();

        const u = userEvent.setup();
        await u.type(screen.getByRole("textbox", { name: "Search detail..." }), "boom");
        expect(await screen.findByText(LOAD_ERROR)).toBeInTheDocument();
        expect(screen.queryByText(NO_HISTORY)).toBeNull();

        // A search that matches nothing is still the empty history.
        await u.clear(screen.getByRole("textbox", { name: "Search detail..." }));
        await u.type(screen.getByRole("textbox", { name: "Search detail..." }), "nothing");
        expect(await screen.findByText(NO_HISTORY)).toBeInTheDocument();
        expect(screen.queryByText(LOAD_ERROR)).toBeNull();
    });

    it("the search box stops at Go's 100 characters (GET /v1/truck-assignments q too_long)", async () => {
        const server = historyServer([assignment(1)], 5);
        renderHistory();
        expect(await screen.findByText("1 rows")).toBeInTheDocument();

        const input = screen.getByRole("textbox", { name: "Search detail..." });
        expect(ASSIGNMENT_SEARCH_MAX).toBe(100);
        expect(input).toHaveAttribute("maxlength", "100");
        const u = userEvent.setup();
        await u.click(input);
        await u.paste("x".repeat(ASSIGNMENT_SEARCH_MAX + 1));
        expect(input).toHaveValue("x".repeat(ASSIGNMENT_SEARCH_MAX));
        expect(await screen.findByText(NO_HISTORY)).toBeInTheDocument();
        expect(server.calls().at(-1)!.get("q")).toHaveLength(ASSIGNMENT_SEARCH_MAX);
    });
});
