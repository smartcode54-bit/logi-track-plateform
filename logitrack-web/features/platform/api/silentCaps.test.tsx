// TW8 (issue #88, M1 slice; Appendix E §E.10 rows 2, 3; developer-spec.md §10.10, §10.14 item 9): with
// the lists on Go (the only source since TW7) and data above every old cap, the M1 lists reach their last row through
// the keyset cursor. Over-cap fixtures, rendered as the pages render them:
// - drivers: 150 drivers (old cap 100), stat cards from the server's counts, "load more" to the end;
// - trucks: 150 trucks (old cap 100), "load more" to the end, the last truck on the last table page;
// - truck-assignment history: 5,000 rows, virtualised (< 100 DOM rows), paged by scrolling to the last
//   row; the search and the status go to Go (`?q&status`), so a row that is not loaded yet is found.
// The hub dropdown (old cap 50) is covered in features/hubs/components/HubFilterCombobox.test.tsx.
import React from "react";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const push = vi.fn();
vi.mock("next/navigation", () => ({
    useRouter: () => ({ push, replace: vi.fn(), prefetch: vi.fn(), back: vi.fn() }),
    usePathname: () => "/app",
    useSearchParams: () => new URLSearchParams(),
}));

import { LanguageProvider } from "@/context/language";
import { createQueryClient } from "@/lib/queryClient";
import { fakeWeb, json, type FakeCall } from "@/test-utils/fakeWeb";
import { scrollViewport, stubVirtualLayout } from "@/test-utils/virtualLayout";
import DriversList from "@/features/drivers/components/DriversList";
import TrucksListDashboard from "@/features/trucks/components/TrucksListDashboard";
import { AssignmentHistory, HISTORY_ROW_HEIGHT } from "@/features/trucks/components/AssignmentHistory";
import { ASSIGNMENTS_PAGE_SIZE, type TruckAssignmentDTO } from "@/features/trucks/api/truckAssignments";
import { DRIVERS_PAGE_SIZE } from "@/features/drivers/api/drivers";
import { TRUCKS_PAGE_SIZE } from "@/features/trucks/api/trucks";

const OLD_DRIVERS_CAP = 100;
const OLD_TRUCKS_CAP = 100;
const VIEWPORT = 600;

let restoreLayout: () => void;
beforeAll(() => {
    // Radix pointer handling in jsdom.
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
    restoreLayout = stubVirtualLayout({ viewport: VIEWPORT, row: HISTORY_ROW_HEIGHT });
});
afterAll(() => restoreLayout());
beforeEach(() => {
    try {
        window.localStorage.removeItem("language");
    } catch {
        // English is the default anyway
    }
});
afterEach(() => vi.unstubAllGlobals());

/** The fake origin: Go answers every master-data list. */
function goWeb() {
    const web = fakeWeb();
    return web;
}

/**
 * A keyset list as Go serves it (Appendix B §B.1.5): `limit` rows from the opaque cursor (here the next
 * index), `nextCursor` absent on the last page; `filter` applies the query the way the server does.
 */
function keyset<T>(rows: () => T[], filter: (row: T, params: URLSearchParams) => boolean = () => true, meta?: (all: T[]) => unknown) {
    return (call: FakeCall) => {
        const params = new URL(call.url, "http://web.test").searchParams;
        const all = rows().filter((r) => filter(r, params));
        const limit = Number(params.get("limit") ?? "50");
        const from = Number(params.get("cursor") ?? "0");
        const page = all.slice(from, from + limit);
        const next = from + limit < all.length ? String(from + limit) : undefined;
        return json(200, { data: page, ...(next ? { nextCursor: next } : {}), ...(meta ? { meta: meta(all) } : {}) });
    };
}

function renderPage(ui: React.ReactElement) {
    const client = createQueryClient();
    return render(
        <QueryClientProvider client={client}>
            <LanguageProvider>{ui}</LanguageProvider>
        </QueryClientProvider>
    );
}

const loadedText = (n: number) => `${n} loaded`;

function driver(i: number) {
    return {
        id: `d-${i}`, tenantId: "t-own", tenantNameTh: null, tenantNameEn: null, tenantKind: "own_fleet", userId: null,
        legacyDocId: null, displayName: `คนขับ ${i}`, firstName: `F${i}`, lastName: `L${i}`, fullNameTh: `คนขับ ${i}`,
        mobile: `08${String(i).padStart(8, "0")}`, email: null, status: i % 10 === 0 ? "inactive" : "active",
        employmentType: "full_time", contractYears: null, employmentStart: null, employmentEnd: null, hireDate: null,
        probationPassed: false, assignToProject: null, truckLicenseId: null, licenseType: null, licenseExpiredDate: null,
        activeTruck: null, currentAssignment: null, createdAt: "2026-10-01T00:00:00Z", updatedAt: "2026-10-01T00:00:00Z",
    };
}

function truck(i: number) {
    return {
        id: `t-${String(i).padStart(4, "0")}`, tenantId: "t-own", ownershipType: "own", licensePlate: `กข-${1000 + i}`,
        province: "กรุงเทพมหานคร", status: "active", type: "6 Wheels", brand: "Isuzu", model: `FRR-${i}`,
    };
}

function assignment(i: number): TruckAssignmentDTO {
    return {
        id: `a-${i}`, tenantId: "t-own", truckId: `t-${i % 300}`, truckPlate: `PLATE-${String(i).padStart(4, "0")}`,
        truckModel: "FRR", driverId: `d-${i}`, driverName: `Driver ${i}`, status: i % 7 === 0 ? "active" : "revoked",
        adminUserId: null, adminLabel: "Admin", createdAt: new Date(Date.UTC(2026, 9, 1) - i * 60_000).toISOString(),
        revokedAt: null,
    };
}

describe("drivers list over the old 100-row cap", () => {
    it("pages past 100 drivers to the last one, with stat cards from the server's counts", async () => {
        const web = goWeb();
        const drivers = Array.from({ length: OLD_DRIVERS_CAP + 50 }, (_, i) => driver(i));
        const counts = (all: ReturnType<typeof driver>[]) => ({
            statusCounts: { active: all.filter((d) => d.status === "active").length, inactive: all.filter((d) => d.status === "inactive").length },
        });
        web.on("GET", "/api/go/v1/drivers", keyset(() => drivers, () => true, counts));
        renderPage(<DriversList />);

        expect(await screen.findByText(loadedText(DRIVERS_PAGE_SIZE))).toBeInTheDocument();
        // The cards count all 150 drivers on the server, not the 100 loaded.
        // A card is the <p> label and the <h2> count of one CardContent.
        const card = (label: string) => screen.getAllByText(label).find((el) => el.tagName === "P")!.parentElement!;
        expect(within(card("Total Drivers")).getByRole("heading")).toHaveTextContent("150");
        expect(within(card("Inactive")).getByRole("heading")).toHaveTextContent("15");

        const u = userEvent.setup();
        await u.click(screen.getByRole("button", { name: "Load more" }));
        expect(await screen.findByText(loadedText(150))).toBeInTheDocument();
        expect(screen.getByText("All rows loaded")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
        expect(screen.getByText(/of 150 entries/)).toBeInTheDocument();

        // The table's last page holds the 150th driver, unreachable before.
        for (let page = 1; page < 15; page++) await u.click(screen.getByRole("button", { name: "Next" }));
        expect(screen.getByText("F149 L149")).toBeInTheDocument();
        expect(web.calls.filter((c) => c.url.startsWith("/api/go/v1/drivers")).map((c) => c.url)).toEqual([
            "/api/go/v1/drivers?limit=100",
            "/api/go/v1/drivers?limit=100&cursor=100",
        ]);
    });
});

describe("trucks list over the old 100-row cap", () => {
    it("pages past 100 trucks to the last one", async () => {
        const web = goWeb();
        const trucks = Array.from({ length: OLD_TRUCKS_CAP + 50 }, (_, i) => truck(i));
        web.on("GET", "/api/go/v1/trucks", keyset(() => trucks));
        web.on("GET", "/api/go/v1/truck-assignments", () => json(200, { data: [] }));
        renderPage(<TrucksListDashboard />);

        expect(await screen.findByText(loadedText(TRUCKS_PAGE_SIZE))).toBeInTheDocument();
        const u = userEvent.setup();
        await u.click(screen.getByRole("button", { name: "Load more" }));
        expect(await screen.findByText(loadedText(150))).toBeInTheDocument();
        expect(screen.getByText("All rows loaded")).toBeInTheDocument();
        expect(screen.getByText("Showing 1 to 10 of 150 loaded")).toBeInTheDocument();
        // The list pages on the server (limit=100 + cursor), never one capped read.
        const listCalls = web.calls.filter((c) => c.url.startsWith("/api/go/v1/trucks?")).map((c) => c.url);
        expect(listCalls).toContain("/api/go/v1/trucks?limit=100&cursor=100");
    });
});

describe("truck-assignment history: virtualised keyset list", () => {
    function historyWeb(rows: TruckAssignmentDTO[]) {
        const web = goWeb();
        web.on(
            "GET",
            "/api/go/v1/truck-assignments",
            keyset(
                () => rows,
                // Go's filters (internal/fleet ListAssignments): status, and q over plate, model, driver, admin.
                (r, p) => {
                    const status = p.get("status");
                    const q = (p.get("q") ?? "").toLowerCase();
                    if (status && r.status !== status) return false;
                    return !q || [r.truckPlate, r.truckModel, r.driverName, r.adminLabel].some((v) => (v ?? "").toLowerCase().includes(q));
                }
            )
        );
        return web;
    }
    const historyCalls = (web: ReturnType<typeof fakeWeb>) =>
        web.calls.filter((c) => c.url.startsWith("/api/go/v1/truck-assignments")).map((c) => new URL(c.url, "http://web.test").searchParams);
    const viewport = () => screen.getByRole("region", { name: "Assignment History" });
    const domRows = () => viewport().querySelectorAll("tbody tr[data-index]");

    it("5,000 rows: scrolling pages to the last row while fewer than 100 rows are in the DOM", async () => {
        const total = 5000;
        const rows = Array.from({ length: total }, (_, i) => assignment(i));
        const web = historyWeb(rows);
        renderPage(<AssignmentHistory onRevoke={vi.fn()} />);

        expect(await screen.findByText(loadedText(ASSIGNMENTS_PAGE_SIZE))).toBeInTheDocument();
        let loaded = ASSIGNMENTS_PAGE_SIZE;
        let maxDomRows = domRows().length;
        // Each scroll to the end of the loaded rows requests the next keyset page, with no click.
        while (loaded < total) {
            scrollViewport(viewport(), "end", HISTORY_ROW_HEIGHT, loaded);
            loaded += ASSIGNMENTS_PAGE_SIZE;
            await screen.findByText(loadedText(loaded));
            maxDomRows = Math.max(maxDomRows, domRows().length);
        }
        scrollViewport(viewport(), "end", HISTORY_ROW_HEIGHT, total);
        expect(screen.getByText("Driver 4999")).toBeInTheDocument();
        expect(screen.queryByText("Driver 0")).toBeNull();
        expect(screen.getByText("All rows loaded")).toBeInTheDocument();
        maxDomRows = Math.max(maxDomRows, domRows().length);
        expect(maxDomRows).toBeGreaterThan(0);
        expect(maxDomRows).toBeLessThan(100);
        expect(viewport().querySelector("table")).toHaveAttribute("aria-rowcount", String(total + 1));

        const calls = historyCalls(web);
        expect(calls).toHaveLength(total / ASSIGNMENTS_PAGE_SIZE);
        expect(calls.every((p) => p.get("limit") === String(ASSIGNMENTS_PAGE_SIZE))).toBe(true);
        // At the end of the list nothing more is requested.
        scrollViewport(viewport(), "end", HISTORY_ROW_HEIGHT, total);
        await act(async () => undefined);
        expect(historyCalls(web)).toHaveLength(total / ASSIGNMENTS_PAGE_SIZE);
    }, 60_000);

    it("search and status run on the server: a row past the loaded pages is found", async () => {
        const rows = Array.from({ length: 250 }, (_, i) => assignment(i));
        const web = historyWeb(rows);
        renderPage(<AssignmentHistory onRevoke={vi.fn()} />);
        expect(await screen.findByText(loadedText(100))).toBeInTheDocument();
        expect(screen.queryByText("Driver 240")).toBeNull();

        const u = userEvent.setup();
        await u.type(screen.getByRole("textbox", { name: "Search detail..." }), "plate-0240");
        expect(await screen.findByText("Driver 240")).toBeInTheDocument();
        expect(screen.getByText(loadedText(1))).toBeInTheDocument();
        const searched = historyCalls(web).at(-1)!;
        expect(Object.fromEntries(searched)).toEqual({ q: "plate-0240", limit: "100" });

        await u.clear(screen.getByRole("textbox", { name: "Search detail..." }));
        await screen.findByText(loadedText(100));
        await u.click(screen.getByRole("combobox", { name: "Filter by Status" }));
        await u.click(await screen.findByRole("option", { name: "Active" }));
        // 250 rows, every seventh active: 36 rows, one page.
        expect(await screen.findByText(loadedText(36))).toBeInTheDocument();
        expect(Object.fromEntries(historyCalls(web).at(-1)!)).toEqual({ status: "active", limit: "100" });
        expect(screen.getByText("All rows loaded")).toBeInTheDocument();
    });
});
