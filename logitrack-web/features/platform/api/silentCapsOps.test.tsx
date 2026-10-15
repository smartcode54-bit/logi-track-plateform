// TW8 (issue #88, acceptance "caps removed and verified with over-cap fixtures"; Appendix E §E.10 rows 4-7;
// developer-spec.md §10.10, §10.14 item 9): the M2/M3 lists on Go with data above every old cap, rendered as the pages
// render them under the app's providers (every request through the BFF, /api/go/v1/...):
// - standby records: 350 records (old cap 300), "load more" to the last one, the count without "+";
// - incident reports: 250 reports (old cap 200), the same;
// - income: 650 delivered trips (old cap 500) and 350 standby records, "load the whole range": the totals and the
//   export cover all 1,000 rows, the table keeps < 100 DOM rows and scrolls to the last one;
// - the task board's hub filter over the Go hub master: 404 hubs (old cap 50), a hub past the old cap is found and
//   filters the board on the server.
// The Go side of each list (no server cap, the keyset walk reaches every row) is pinned by
// internal/operations/overcap_integration_test.go and internal/hubs/overcap_integration_test.go.
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const xlsx = vi.hoisted(() => ({ writeFile: vi.fn(), rows: [] as unknown[][] }));
vi.mock("xlsx", () => ({
    utils: {
        json_to_sheet: (rows: unknown[]) => (xlsx.rows.push(rows), {}),
        aoa_to_sheet: () => ({}),
        book_new: () => ({}),
        book_append_sheet: () => undefined,
    },
    writeFile: xlsx.writeFile,
}));

import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { fakeWeb, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { scrollViewport, stubVirtualLayout } from "@/test-utils/virtualLayout";

import { StandbyRecordsPage } from "@/features/standby/components/go/StandbyRecordsPage";
import { STANDBY_PAGE_SIZE } from "@/features/standby/api/standby.go";
import { IncidentReportsPage } from "@/features/incident-reports/components/go/IncidentReportsPage";
import { INCIDENTS_PAGE_SIZE } from "@/features/incident-reports/api/incidents.go";
import { IncomePage } from "@/features/accounting/components/go/IncomePage";
import { INCOME_PAGE_SIZE } from "@/features/accounting/api/income.go";
import { FirstMileBoardPage } from "@/features/tasks/components/go/TaskBoardPage";

const OLD_STANDBY_CAP = 300;
const OLD_INCIDENTS_CAP = 200;
const OLD_INCOME_CAP = 500;
const OLD_HUB_CAP = 50;
const VIEWPORT = 600;
const ROW = 40;

let restoreLayout: () => void;
beforeAll(() => {
    restoreLayout = stubVirtualLayout({ viewport: VIEWPORT, row: ROW });
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
    globalThis.ResizeObserver ??= class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
});
afterAll(() => restoreLayout());
afterEach(() => {
    resetQueryClientForTests();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    xlsx.writeFile.mockReset();
    xlsx.rows.length = 0;
});

const CAPS = [
    "operations:view_first_mile",
    "operations:view_driver_monitor",
    "operations:view_incidents",
    "accounting:view_income",
];

function setup() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities: CAPS }) }));
    web.on("GET", "/api/go/v1/customers", () => json(200, { data: [{ id: "c1", billingPartyId: "p1", code: "CJSF", name: "CJ Super Fresh" }] }));
    return web;
}

const renderPage = (pathname: string, ui: React.ReactElement) => {
    window.history.replaceState(null, "", pathname);
    return renderWithProviders(<RouteNamespaces pathname={pathname}>{ui}</RouteNamespaces>, getQueryClient());
};

const params = (url: string) => new URL(url, "http://web.test").searchParams;

/**
 * A keyset list as Go serves it (Appendix B §B.1.5): `limit` rows from the opaque cursor (here the next index),
 * `nextCursor` absent on the last page. No cap: the fake hands out every row it holds.
 */
function keyset<T>(all: T[]) {
    return (call: FakeCall) => {
        const q = params(call.url);
        const limit = Number(q.get("limit") ?? "50");
        const from = Number(q.get("cursor") ?? "0");
        const next = from + limit < all.length ? String(from + limit) : undefined;
        return json(200, { data: all.slice(from, from + limit), ...(next ? { nextCursor: next } : {}) });
    };
}

const cursors = (web: ReturnType<typeof setup>, path: string) =>
    web.calls.filter((c) => c.url.split("?")[0] === path).map((c) => params(c.url).get("cursor") ?? "");

/** Clicks "Load more" until the footer says every row is loaded. */
async function loadToTheEnd(u: ReturnType<typeof userEvent.setup>, total: number) {
    for (let i = 0; i < 20 && !screen.queryByText(`${total} rows loaded`); i++) {
        const loaded = screen.getByText(/^\d+ rows loaded$/).textContent;
        await u.click(screen.getByRole("button", { name: "Load more" }));
        await waitFor(() => expect(screen.getByText(/^\d+ rows loaded$/).textContent).not.toBe(loaded));
    }
    expect(await screen.findByText(`${total} rows loaded`)).toBeInTheDocument();
    expect(screen.getByText("All rows loaded")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
}

const at = (i: number) => new Date(Date.UTC(2026, 9, 10) - i * 60_000).toISOString();

function standbyRecord(i: number) {
    return {
        id: `s-${i}`, tenantId: "o", driverId: `d-${i}`, driverName: `คนขับรอ ${i}`, taskId: null, tripId: null, jobCategory: null,
        startLocation: `ต้นทาง ${i}`, endLocation: `ปลายทาง ${i}`, startedAt: at(i), endedAt: at(i), durationMinutes: 30, note: null,
        status: "completed", lat: null, lng: null, truckId: null, truckLicensePlate: null, photos: [], createdAt: at(i),
    };
}

function incident(i: number) {
    return {
        id: `i-${i}`, tenantId: "o", tripId: `r-${i}`, tripNo: `INC-${String(i).padStart(4, "0")}`, driverId: `d-${i}`, driverName: `คนขับ ${i}`,
        delayCause: "incident_cause_tire", description: `เหตุ ${i}`, lat: null, lng: null, truckId: null, truckLicensePlate: null,
        mapPhoto: null, situation1Photo: null, situation2Photo: null, reportedByKind: "driver", createdAt: at(i),
    };
}

describe("standby records over the old 300-row cap", () => {
    it("pages past 300 records to the last one", async () => {
        const web = setup();
        const records = Array.from({ length: OLD_STANDBY_CAP + 50 }, (_, i) => standbyRecord(i));
        web.on("GET", "/api/go/v1/standby", keyset(records));
        renderPage("/app/standby-records", <StandbyRecordsPage />);

        expect(await screen.findByText(`${STANDBY_PAGE_SIZE} rows loaded`)).toBeInTheDocument();
        expect(screen.getByText(`${STANDBY_PAGE_SIZE}+`)).toBeInTheDocument();
        await loadToTheEnd(userEvent.setup(), records.length);

        // The 301st to the 350th were never on the Firestore page.
        expect(screen.getByText("คนขับรอ 349")).toBeInTheDocument();
        expect(screen.getByText(String(records.length))).toBeInTheDocument();
        expect(cursors(web, "/api/go/v1/standby")).toEqual(["", "100", "200", "300"]);
        expect(web.calls.filter((c) => c.url.startsWith("/api/go/v1/standby")).every((c) => params(c.url).get("limit") === "100")).toBe(true);
    });
});

describe("incident reports over the old 200-row cap", () => {
    it("pages past 200 reports to the last one", async () => {
        const web = setup();
        const reports = Array.from({ length: OLD_INCIDENTS_CAP + 50 }, (_, i) => incident(i));
        web.on("GET", "/api/go/v1/incidents", keyset(reports));
        renderPage("/app/incident-reports", <IncidentReportsPage />);

        expect(await screen.findByText(`${INCIDENTS_PAGE_SIZE} rows loaded`)).toBeInTheDocument();
        await loadToTheEnd(userEvent.setup(), reports.length);

        expect(screen.getByText("INC-0249")).toBeInTheDocument();
        expect(screen.getByText(String(reports.length))).toBeInTheDocument();
        expect(cursors(web, "/api/go/v1/incidents")).toEqual(["", "100", "200"]);
    });
});

describe("income over the old 500-row cap", () => {
    const tripRow = (i: number) => ({
        id: `t-${i}`, tenantId: "o", tripNo: `SPXTH-${String(i).padStart(4, "0")}`, status: "delivered", jobType: "first_mile", jobCategory: "PRIMARY",
        taskId: null, driverId: null, driverName: null, origin: "SPX-A", destination: "SOCE",
        originPlace: { raw: "SPX-A", key: "SPX-A", label: "Bang Na", resolved: true },
        destinationPlace: { raw: "SOCE", key: "SOCE", label: "SOC East", resolved: true },
        truckId: null, truckLicensePlate: null, vehicleClass: "6WH", isMultiDelivery: false, createdAt: at(i), deliveredAt: at(i),
        billing: { customerId: "c1", billingPartyId: "p1", priced: true, estimateThb: 1000, isMultiDelivery: false, manualOverride: false, stops: [] },
    });
    const standbyRow = (i: number) => ({
        ...standbyRecord(i), id: `sb-${i}`, endedAt: at(i + 0.5), customerPartyId: "p1",
        billing: { estimateThb: 500, partyId: "p1", rateSource: "standby_rate", rateEntryId: null, effectiveFromDate: null, unpricedReason: null, computedAt: null, billingTenantId: null },
    });

    it("loads the whole range: totals and export cover every row, the table stays virtual and reaches the last one", async () => {
        const web = setup();
        const trips = Array.from({ length: OLD_INCOME_CAP + 150 }, (_, i) => tripRow(i));
        const standby = Array.from({ length: OLD_STANDBY_CAP + 50 }, (_, i) => standbyRow(i));
        web.on("GET", "/api/go/v1/trips", keyset(trips));
        web.on("GET", "/api/go/v1/standby", keyset(standby));
        renderPage("/app/accounting/income", <IncomePage />);

        // First pages: 200 trips + 200 standby; the figures say they cover the loaded rows only.
        expect(await screen.findByText(`${2 * INCOME_PAGE_SIZE} loaded`)).toBeInTheDocument();
        expect(screen.getByText("The figures above cover the rows loaded so far; load more to include the rest.")).toBeInTheDocument();

        const u = userEvent.setup();
        await u.click(screen.getByRole("button", { name: "Load the whole range" }));
        const total = trips.length + standby.length;
        expect(await screen.findByText(`${total} loaded`, undefined, { timeout: 5000 })).toBeInTheDocument();
        expect(screen.queryByText("The figures above cover the rows loaded so far; load more to include the rest.")).toBeNull();

        // Totals over every row: 650 × ฿1,000 + 350 × ฿500.
        // A card is the CardHeader (its title) and the CardContent (its figure) of one Card.
        const card = (title: string) => screen.getAllByText(title).find((el) => el.getAttribute("data-slot") === "card-title")!.parentElement!.parentElement!;
        expect(within(card("Billing amount")).getByText(`฿${(650 * 1000 + 350 * 500).toLocaleString("en-US", { minimumFractionDigits: 2 })}`)).toBeInTheDocument();
        expect(within(card("Priced rows")).getByText(total.toLocaleString())).toBeInTheDocument();

        // 1,000 rows, fewer than 100 in the DOM; the oldest trip is reached by scrolling.
        const viewport = document.querySelector<HTMLElement>('[data-slot="virtual-table-viewport"]')!;
        const domRows = () => viewport.querySelectorAll("tbody tr[data-index]").length;
        expect(domRows()).toBeGreaterThan(0);
        expect(domRows()).toBeLessThan(100);
        // Unmeasured rows count at the table's estimate (48 px) until they render: scroll past both heights.
        scrollViewport(viewport, "end", 48, total);
        scrollViewport(viewport, "end", 48, total);
        expect(within(viewport).getByText("SPXTH-0649")).toBeInTheDocument();
        expect(domRows()).toBeLessThan(100);

        // The export writes every row of the range (no partial file, no 500 cap).
        await u.click(screen.getByRole("button", { name: /Export Excel/ }));
        await waitFor(() => expect(xlsx.writeFile).toHaveBeenCalledTimes(1));
        expect(xlsx.rows[0]).toHaveLength(total);

        expect(cursors(web, "/api/go/v1/trips")).toEqual(["", "200", "400", "600"]);
        expect(cursors(web, "/api/go/v1/standby")).toEqual(["", "200"]);
    });
});

describe("task board hub filter over the old 50-hub cap, from the Go hub master", () => {
    it("offers all 404 hubs and filters the board on the server by a hub past the old cap", async () => {
        const web = setup();
        const hubs = Array.from({ length: 404 }, (_, i) => ({
            id: `h-${i}`, sourceId: `HUB-${String(i).padStart(4, "0")}`, sourceNameTh: `ฮับ ${i}`, sourceNameEn: `Hub ${i}`, stationType: "HUB",
        }));
        web.on("GET", "/api/go/v1/hubs", () => json(200, { data: hubs }));
        web.on("GET", "/api/go/v1/tasks", () => json(200, { data: [] }));
        renderPage("/app/first-mile", <FirstMileBoardPage />);

        const u = userEvent.setup();
        const trigger = await waitFor(() => {
            const el = screen.getAllByRole("combobox").find((c) => c.textContent?.includes("All Pickup Points"));
            expect(el).toBeDefined();
            return el!;
        });
        await u.click(trigger);
        const list = await waitFor(() => {
            const el = document.querySelector<HTMLElement>('[data-slot="search-select-virtual"]')?.closest<HTMLElement>('[data-slot="search-select-list"]') ?? null;
            expect(el).not.toBeNull();
            return el!;
        });
        // Virtualised, and the last hub (404th, far past the old 50) is on the list.
        expect(list.querySelectorAll("[data-index]").length).toBeLessThan(40);
        scrollViewport(list, "end", ROW, hubs.length + 1);
        expect(within(list).getByText("ฮับ 403")).toBeInTheDocument();

        await u.type(screen.getByPlaceholderText("Search by name or code..."), "HUB-0399");
        await u.click(await screen.findByText("ฮับ 399"));
        await waitFor(() =>
            expect(web.calls.some((c) => c.url.startsWith("/api/go/v1/tasks") && params(c.url).get("sourceHub") === "code:HUB-0399")).toBe(true)
        );
        expect(web.count("GET", "/api/go/v1/hubs")).toBe(1);
        expect(hubs.length).toBeGreaterThan(OLD_HUB_CAP);
    });
});
