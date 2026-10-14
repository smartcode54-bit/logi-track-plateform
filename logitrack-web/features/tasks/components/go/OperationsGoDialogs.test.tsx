// T34 (issue #54), the review panel of PR #167: the Go operations pages and dialogs, rendered under the app's
// providers with every request through the BFF (/api/go/v1/...). Pinned here:
// - the boards' SOC and source-hub filters are server parameters (W9): a SOCN task past row 200 of a busy day is on
//   the first page, and the hub picker offers every hub of the Go master (TW8);
// - the monitor is a VirtualTable (developer-spec.md §10.10 W9, TW8: 5,000 rows reached by scrolling with fewer than
//   100 rows in the DOM), has no revenue column (the owner's July layout), and its export shows a count only when it
//   is the count of the filtered set;
// - resolving a stuck trip as delivered sends the editor's delivery time (the billing period it falls in);
// - the trip editor's route and stops need operations:manage_tasks when the trip has a task (Go refuses the mirror);
// - the task form edits the stored stops and sends them only when they were touched;
// - the job-assign import keeps a type's created rows when a later type fails, and never posts them twice.
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const sheet = vi.hoisted(() => ({ rows: [] as unknown[][] }));
vi.mock("xlsx", () => ({
    read: () => ({ SheetNames: ["S"], Sheets: { S: {} } }),
    SSF: { parse_date_code: () => null },
    utils: {
        sheet_to_json: () => sheet.rows,
        aoa_to_sheet: () => ({}),
        book_new: () => ({}),
        book_append_sheet: () => undefined,
    },
    writeFile: vi.fn(),
}));

import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { foldSearchText, matchesTerms, searchTerms } from "@/lib/searchText";
import { fakeWeb, goErr, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { scrollViewport, stubVirtualLayout } from "@/test-utils/virtualLayout";

import { FirstMileBoardPage } from "./TaskBoardPage";
import { TaskFormDialog, editStopsPatch } from "./TaskFormDialog";
import { TaskImportDialog } from "./TaskImportDialog";
import { DriverMonitorPage } from "@/features/drivers/components/go/DriverMonitorPage";
import { TripEditDialog } from "@/features/drivers/components/go/TripEditDialog";
import type { TaskDTO } from "../../api/tasks.go";

const ROW = 48;
let restoreLayout: () => void;
beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
    globalThis.ResizeObserver ??= class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
    restoreLayout = stubVirtualLayout({ viewport: 480, row: ROW });
});
afterAll(() => restoreLayout());
beforeEach(() => {
});
afterEach(() => {
    resetQueryClientForTests();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    sheet.rows = [];
});

const ALL_CAPS = [
    "operations:view_first_mile",
    "operations:view_line_haul",
    "operations:manage_tasks",
    "operations:view_driver_monitor",
    "operations:edit_trip_details",
];

const HUBS = [
    { id: "h1", sourceId: "ALANG-A", sourceNameTh: "วังทองหลาง", sourceNameEn: null, stationType: "HUB", linkedPartyId: "p1", linkedCustomerKind: "customer" },
    { id: "h2", sourceId: "SOCE", sourceNameTh: "บัวโรย", sourceNameEn: null, stationType: "SOC" },
    { id: "h3", sourceId: "SPK890174", sourceNameTh: "ห้วยขวาง10", sourceNameEn: null, stationType: "HUB", linkedPartyId: "p1", linkedCustomerKind: "customer" },
    { id: "h4", sourceId: "RC-01", sourceNameTh: "ศูนย์คืนสินค้า", sourceNameEn: null, stationType: "SOC" },
    ...Array.from({ length: 60 }, (_, i) => ({ id: `hx${i}`, sourceId: `HUB-${String(i).padStart(3, "0")}`, sourceNameTh: `ฮับ ${i}`,
        sourceNameEn: null, stationType: "HUB" })),
];

/**
 * `GET /v1/hubs` as Go answers it (UI2B): `q` folded as lib/searchText.ts folds (Go's searchtext mirrors it), every
 * word in the code or a name; `stationType`; ordered by code; one page with `limit`, else the whole master.
 */
function goHubs(call: FakeCall) {
    const q = params(call.url);
    const terms = searchTerms(q.q);
    const rows = HUBS.filter((h) => (!q.stationType || h.stationType === q.stationType) &&
        matchesTerms(foldSearchText(`${h.sourceId} ${h.sourceNameTh} ${h.sourceNameEn ?? ""}`), terms))
        .sort((a, b) => (a.sourceId < b.sourceId ? -1 : a.sourceId > b.sourceId ? 1 : 0));
    if (!q.limit) return json(200, { data: rows });
    const limit = Number(q.limit);
    return json(200, { data: rows.slice(0, limit), ...(rows.length > limit ? { nextCursor: "next" } : {}) });
}

/** `GET /v1/hubs/{code}`: one hub by its code (case-insensitive), else 404. */
function goHub(call: FakeCall) {
    const code = decodeURIComponent(call.url.split("?")[0].slice("/api/go/v1/hubs/".length)).toUpperCase();
    const hub = HUBS.find((h) => h.sourceId.toUpperCase() === code);
    return hub ? json(200, { data: hub }) : goErr(404, "not_found");
}

function setup(caps = ALL_CAPS, principal = makeMe({ capabilities: caps })) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: principal }));
    web.on("GET", "/api/go/v1/hubs", goHubs);
    web.on("GET", /^\/api\/go\/v1\/hubs\/[^/]+$/, goHub);
    web.on("GET", "/api/go/v1/customers", () => json(200, { data: [{ id: "c1", billingPartyId: "p1", code: "CJSF", name: "CJ" }] }));
    return web;
}

const renderPage = (pathname: string, ui: React.ReactElement) => {
    window.history.replaceState(null, "", pathname);
    return renderWithProviders(<RouteNamespaces pathname={pathname}>{ui}</RouteNamespaces>, getQueryClient());
};

const params = (url: string) => Object.fromEntries(new URL(url, "http://web.test").searchParams);
const place = (label: string) => ({ raw: label, key: `code:${label}`, label, resolved: true });

function task(over: Partial<TaskDTO> = {}): TaskDTO {
    return {
        id: "t1", tenantId: "o", taskNo: "FM-12102026-001", taskType: "first_mile", jobCategory: "PRIMARY", status: "pending", planAt: "2026-10-11T17:00:00Z",
        planDate: "2026-10-12", planTime: "09:30", actualPickupAt: null, sourceHub: "ALANG-A", sourceHubId: "h1", source: place("ALANG-A"),
        destination: "SOCE", destinationHubId: null, destinationSocKey: "SOCE", destinationPlace: place("SOCE"), truckId: null, truckType: "6WH",
        licensePlate: null, driverId: null, driverName: null, helperDriverId: null, helperName: null, runOrder: null, checkInAt: null, checkInLat: null,
        checkInLng: null, isMultiDelivery: false, stops: [], trip: null, createdAt: "2026-10-11T00:00:00Z", updatedAt: "2026-10-11T00:00:00Z",
        billingPartyId: "p1", ...over,
    };
}

const stop = (index: number, destination: string) => ({ index, destination, destinationHubId: null, place: place(destination), status: "pending",
    deliveredAt: null, sequence: index - 1, sourceId: null, isCustom: false });

describe("boards: the SOC and source-hub filters are server parameters (W9)", () => {
    it("a SOCN task past row 200 of a busy day shows on the first page; the hub picker offers every hub of the Go master", async () => {
        const web = setup();
        const day = Array.from({ length: 200 }, (_, i) => task({ id: `e${i}`, taskNo: `FM-E-${i}` }));
        const socn = task({ id: "n1", taskNo: "FM-SOCN-201", destination: "SOCN", destinationSocKey: "SOCN", destinationPlace: place("SOCN") });
        web.on("GET", "/api/go/v1/tasks", (c) => {
            const q = params(c.url);
            if (q.destination === "code:SOCN") return json(200, { data: [socn] });
            if (q.sourceHub) return json(200, { data: [] });
            return json(200, { data: day, nextCursor: "page2" });
        });
        renderPage("/app/first-mile", <FirstMileBoardPage />);
        expect(await screen.findByText("FM-E-0")).toBeInTheDocument();
        expect(screen.queryByText("FM-SOCN-201")).toBeNull();

        const u = userEvent.setup();
        // A combobox takes no name from its content: the trigger is found by its text.
        await u.click(screen.getByText("All SOCs").closest("button")!);
        await u.click(await screen.findByRole("option", { name: "SOCN" }));
        expect(await screen.findByText("FM-SOCN-201")).toBeInTheDocument();
        expect(screen.queryByText("No tasks found")).toBeNull();
        const socCall = web.calls.filter((c) => c.url.startsWith("/api/go/v1/tasks")).at(-1)!;
        expect(params(socCall.url)).toMatchObject({ taskType: "first_mile", destination: "code:SOCN", limit: "200" });

        // The source-hub picker lists the Go master (62 hubs), not the loaded rows' one source.
        await u.click(screen.getByText("All Pickup Points").closest("button")!);
        expect(await screen.findByText("ฮับ 59")).toBeInTheDocument();
        await u.click(screen.getByText("ฮับ 59"));
        await waitFor(() => expect(params(web.calls.filter((c) => c.url.startsWith("/api/go/v1/tasks")).at(-1)!.url).sourceHub).toBe("code:HUB-059"));
    });
});

function monitorRow(i: number) {
    return {
        id: `r${i}`, tenantId: "o", tripNo: `SPXTH-${i}`, status: "delivered", jobType: "first_mile", jobCategory: "PRIMARY",
        createdAt: "2026-10-11T02:00:00Z", updatedAt: "2026-10-11T02:00:00Z", deliveredAt: "2026-10-11T06:00:00Z", taskId: null, taskNo: null,
        taskStatus: null, checkInAt: null, runOrder: null, planAt: null, driverId: "d1", driverName: `Driver ${i}`, origin: place("ALANG-A"),
        destination: place("SOCE"), stops: [], truck: null, partnerCode: null, sealCode: null, isMultiDelivery: false, incidentCount: 0,
        photoCount: 0, price: { estimateThb: 1200, unpricedReason: null, manualOverride: false, jobCategoryAtPricing: "PRIMARY",
            isMultiDelivery: false, computedAt: "2026-10-11T06:00:00Z" },
    };
}

function monitorWeb(total: number, customer = false) {
    const web = setup(ALL_CAPS, customer ? makeMe({ tenant: null, platformRoles: [], steward: false,
        customerScopes: [{ billingPartyId: "p1", name: "Customer" }], capabilities: ALL_CAPS }) : undefined);
    const all = Array.from({ length: total }, (_, i) => monitorRow(i));
    web.on("GET", "/api/go/v1/trips/monitor", (c) => {
        const q = params(c.url);
        const from = q.cursor ? Number(q.cursor) : 0;
        const limit = Number(q.limit ?? 200);
        const page = all.slice(from, from + limit);
        const next = from + limit < all.length ? String(from + limit) : undefined;
        const meta = q.cursor || q.facets === "false" ? undefined : { facets: { total, statusCounts: { delivered: total }, incident: 0, origins: [],
            destinations: [], plates: [], vehicleClasses: [], partners: [] } };
        return json(200, { data: page, ...(next ? { nextCursor: next } : {}), ...(meta ? { meta } : {}) });
    });
    return web;
}

describe("driver monitor: Table + Virtual (§10.10 W9, TW8)", () => {
    const viewport = () => screen.getByRole("region", { name: "Driver monitor" });
    const domRows = () => viewport().querySelectorAll("tbody tr[data-index]");

    it("5,000 trips: scrolling pages to the last row while fewer than 100 rows are in the DOM; no revenue column", async () => {
        const total = 5000;
        const web = monitorWeb(total);
        renderPage("/app/driver-monitor", <DriverMonitorPage />);
        expect(await screen.findByText("200 rows loaded")).toBeInTheDocument();
        // The owner's July layout: the price is in the detail and the export, not a table column.
        expect(within(viewport()).queryByText("Estimated revenue")).toBeNull();
        expect(within(viewport()).getAllByRole("columnheader")).toHaveLength(12);

        let loaded = 200;
        let maxDomRows = domRows().length;
        while (loaded < total) {
            scrollViewport(viewport(), "end", ROW, loaded);
            loaded += 200;
            await screen.findByText(`${loaded} rows loaded`);
            maxDomRows = Math.max(maxDomRows, domRows().length);
        }
        scrollViewport(viewport(), "end", ROW, total);
        expect(screen.getByText("SPXTH-4999")).toBeInTheDocument();
        expect(screen.queryByText("SPXTH-0")).toBeNull();
        expect(screen.getByText("All rows loaded")).toBeInTheDocument();
        maxDomRows = Math.max(maxDomRows, domRows().length);
        expect(maxDomRows).toBeGreaterThan(0);
        expect(maxDomRows).toBeLessThan(100);
        expect(web.calls.filter((c) => c.url.startsWith("/api/go/v1/trips/monitor"))).toHaveLength(total / 200);
    }, 60_000);

    it("customer phone: the same 5,000 trips page through one summary column with fewer than 100 rows", async () => {
        vi.stubGlobal("innerWidth", 375);
        const restore = stubVirtualLayout({ viewport: 480, row: 260, width: 375 });
        try {
            const total = 5000;
            const web = monitorWeb(total, true);
            renderPage("/app/driver-monitor", <DriverMonitorPage />);
            await screen.findByText("200 rows loaded");
            const region = screen.getByRole("region", { name: "Your trips" });
            expect(within(region).getAllByRole("columnheader")).toHaveLength(1);
            expect(within(region).queryByText("Estimated revenue")).toBeNull();
            let maxRows = 0;
            for (let loaded = 200; loaded <= total; loaded += 200) {
                await screen.findByText(`${loaded} rows loaded`);
                maxRows = Math.max(maxRows, region.querySelectorAll("tbody tr[data-index]").length);
                scrollViewport(region, "end", 260, loaded);
            }
            expect(screen.getByRole("button", { name: "View trip SPXTH-4999 and evidence" })).toBeInTheDocument();
            expect(maxRows).toBeGreaterThan(0);
            expect(maxRows).toBeLessThan(100);
            expect(web.calls.filter((c) => c.url.startsWith("/api/go/v1/trips/monitor"))).toHaveLength(total / 200);
        } finally {
            restore();
        }
    }, 60_000);

    it.each([false, true])("the export shows the facets total only with no filter on (customer phone: %s)", async (customer) => {
        if (customer) vi.stubGlobal("innerWidth", 375);
        monitorWeb(412, customer);
        renderPage("/app/driver-monitor", <DriverMonitorPage />);
        expect(await screen.findByText("200 rows loaded")).toBeInTheDocument();
        const u = userEvent.setup();
        await u.click(screen.getByRole("button", { name: /^Export$/ }));
        expect(await screen.findByText("412 trips match these filters.")).toBeInTheDocument();
        await u.keyboard("{Escape}");
        await waitFor(() => expect(screen.queryByText("412 trips match these filters.")).toBeNull());
        // A filter on (first mile), more pages to load: no up-front number (it would be the unfiltered range).
        if (customer) await u.click(screen.getByRole("button", { name: "More filters" }));
        await u.click(screen.getByRole("button", { name: "First Mile" }));
        await waitFor(() => expect(screen.getByRole("button", { name: /Clear/ })).toBeInTheDocument());
        await u.click(screen.getByRole("button", { name: /^Export$/ }));
        expect(await screen.findByRole("dialog", { name: "Export trips" })).toBeInTheDocument();
        expect(screen.queryByText(/trips match these filters/)).toBeNull();
    });
});

function tripDetail(over: Record<string, unknown> = {}) {
    return {
        id: "r1", tenantId: "o", tripNo: "SPXTH-1", status: "in_transit", jobType: "first_mile", jobCategory: "PRIMARY", taskId: null, driverId: null,
        driverName: null, origin: "ALANG-A", originHubId: "h1", originPlace: place("ALANG-A"), destination: "SOCE", destinationHubId: "h2",
        destinationPlace: place("SOCE"), sealCode: null, partnerCode: null, truckId: null, truckLicensePlate: null, vehicleClass: null,
        deliveredAt: null, isMultiDelivery: false, createdAt: "2026-09-28T02:00:00Z", updatedAt: "2026-09-28T02:00:00Z", stops: [], photos: [],
        ...over,
    };
}

describe("trip editor", () => {
    it("resolve -> delivered sends the delivery time typed in the editor (the billing period it falls in)", async () => {
        const web = setup();
        web.on("GET", "/api/go/v1/trips/r1", () => json(200, { data: tripDetail() }));
        web.on("POST", "/api/go/v1/trips/r1/deliver", () => json(200, { data: tripDetail({ status: "delivered" }) }));
        const onClose = vi.fn();
        renderPage("/app/driver-monitor", <TripEditDialog tripId="r1" onClose={onClose} />);
        const input = await screen.findByLabelText(/Delivered at|Delivery/i, { selector: "input" });
        fireEvent.change(input, { target: { value: "2026-09-28T18:00" } });
        const u = userEvent.setup();
        await u.click(screen.getByRole("button", { name: "Resolve stuck job" }));
        await u.click(await screen.findByText("Mark delivered"));
        expect(screen.getByTestId("resolve-delivered-at")).toHaveTextContent("28/09/2026 18:00");
        await u.click(screen.getByRole("button", { name: "Confirm" }));
        await waitFor(() => expect(onClose).toHaveBeenCalled());
        const post = web.calls.find((c) => c.url === "/api/go/v1/trips/r1/deliver")!;
        expect(post.body).toEqual({ deliveredAt: new Date("2026-09-28T18:00").toISOString(), photos: [] });
    });

    it("an empty delivery time leaves Go's order (the trip's own, else now): no deliveredAt sent", async () => {
        const web = setup();
        web.on("GET", "/api/go/v1/trips/r1", () => json(200, { data: tripDetail() }));
        web.on("POST", "/api/go/v1/trips/r1/deliver", () => json(200, { data: tripDetail({ status: "delivered" }) }));
        renderPage("/app/driver-monitor", <TripEditDialog tripId="r1" onClose={vi.fn()} />);
        const u = userEvent.setup();
        await u.click(await screen.findByRole("button", { name: "Resolve stuck job" }));
        await u.click(await screen.findByText("Mark delivered"));
        await u.click(screen.getByRole("button", { name: "Confirm" }));
        await waitFor(() => expect(web.calls.some((c) => c.url === "/api/go/v1/trips/r1/deliver")).toBe(true));
        expect(web.calls.find((c) => c.url === "/api/go/v1/trips/r1/deliver")!.body).toEqual({ photos: [] });
    });

    it("without operations:manage_tasks the route and the stops of a trip with a task are locked (Go refuses the mirror)", async () => {
        const web = setup(ALL_CAPS.filter((c) => c !== "operations:manage_tasks"));
        web.on("GET", "/api/go/v1/trips/r1", () => json(200, { data: tripDetail({ taskId: "t1" }) }));
        web.on("GET", "/api/go/v1/tasks/t1", () => json(200, { data: task() }));
        renderPage("/app/driver-monitor", <TripEditDialog tripId="r1" onClose={vi.fn()} />);
        expect(await screen.findByText(/editing them needs the "Manage tasks" permission/)).toBeInTheDocument();
        for (const label of ["ALANG-A — วังทองหลาง", "SOCE — บัวโรย"]) expect((await screen.findByText(label)).closest("button")).toBeDisabled();
        expect(screen.getByRole("button", { name: "Add Delivery Stop" })).toBeDisabled();
    });
});

describe("task form: the stops as stored, sent only when touched", () => {
    const multi = (over: Partial<TaskDTO> = {}) => task({ isMultiDelivery: true, stops: [stop(1, "SPK1"), stop(2, "SPK2")], ...over });
    const patches = (web: ReturnType<typeof setup>) => web.calls.filter((c: FakeCall) => c.method === "PATCH");

    async function open(t: TaskDTO) {
        const web = setup();
        web.on("PATCH", `/api/go/v1/tasks/${t.id}`, () => json(200, { data: t }));
        const onClose = vi.fn();
        renderPage("/app/first-mile", <TaskFormDialog mode="edit" taskType="first_mile" task={t} allowTypeSwitch={false} onClose={onClose} />);
        await waitFor(() => expect(web.calls.some((c) => c.url.startsWith("/api/go/v1/customers"))).toBe(true));
        await act(async () => undefined);
        return { web, onClose, u: userEvent.setup() };
    }

    it("a pending task whose stop 1 is not its destination: stop 1 shows, a date edit sends no stops", async () => {
        const { web, onClose, u } = await open(multi());
        expect(screen.getByText("SPK1")).toBeInTheDocument();
        expect(screen.getByText("SPK2")).toBeInTheDocument();
        fireEvent.change(document.getElementById("task-date")!, { target: { value: "2026-10-14" } });
        await u.click(screen.getByRole("button", { name: "Save Changes" }));
        await waitFor(() => expect(onClose).toHaveBeenCalled());
        expect(patches(web).map((c) => c.body)).toEqual([{ date: "2026-10-14" }]);
    });

    it("a checked-in task: a pickup-time edit sends only that (no 409 task_in_progress for unchanged stops)", async () => {
        const { web, onClose, u } = await open(multi({ status: "checked_in" }));
        fireEvent.change(document.getElementById("task-pickup")!, { target: { value: "2026-10-12T10:00" } });
        await u.click(screen.getByRole("button", { name: "Save Changes" }));
        await waitFor(() => expect(onClose).toHaveBeenCalled());
        expect(patches(web).map((c) => Object.keys(c.body as object))).toEqual([["actualPickupAt"]]);
    });

    it("stops touched on a task past assigned: said here, no request", async () => {
        const { web, onClose, u } = await open(multi({ status: "in_transit", stops: [stop(1, "SPK1"), stop(2, "SPK2"), stop(3, "SPK3")] }));
        const removes = screen.getAllByRole("button", { name: "Remove Stop" });
        await u.click(removes[2]);
        await u.click(screen.getByRole("button", { name: "Save Changes" }));
        expect(await screen.findByText("The planned stops change only while the task is pending or assigned.")).toBeInTheDocument();
        expect(patches(web)).toEqual([]);
        expect(onClose).not.toHaveBeenCalled();
    });

    it("editStopsPatch: untouched, case-only and unchanged lists send nothing; a real change sends the list as edited", () => {
        const t = multi({ stops: [stop(1, "SOCE"), stop(2, "SPK2")] });
        expect(editStopsPatch(t, { multi: true, stops: ["X", "Y"] }, false)).toBeUndefined();
        expect(editStopsPatch(t, { multi: true, stops: ["soce ", "spk2"] }, true)).toBeUndefined();
        expect(editStopsPatch(t, { multi: true, stops: ["SPK2", "SOCE"] }, true)).toEqual({ isMultiDelivery: true,
            deliveryStops: [{ destination: "SPK2" }, { destination: "SOCE" }] });
        expect(editStopsPatch(t, { multi: false, stops: ["SOCE", "SPK2"] }, true)).toEqual({ isMultiDelivery: false, deliveryStops: [] });
        expect(editStopsPatch(task(), { multi: false, stops: [] }, true)).toBeUndefined();
    });
});

describe("hub pickers: a server search per term, each value labelled by a single-record lookup (UI2B)", () => {
    const hubCalls = (web: ReturnType<typeof setup>) => web.calls.filter((c) => c.url.split("?")[0] === "/api/go/v1/hubs");
    const lookups = (web: ReturnType<typeof setup>) => web.calls.filter((c) => c.url.startsWith("/api/go/v1/hubs/")).map((c) => c.url);

    it("task form: no whole-master read; a Thai term without its tone mark finds the hub; the pick prefills the customer and posts its code", async () => {
        const web = setup();
        web.on("POST", "/api/go/v1/tasks", () => json(201, { data: task({ taskNo: "FM-NEW-1" }) }));
        const onClose = vi.fn();
        renderPage("/app/first-mile", <TaskFormDialog mode="create" taskType="first_mile" allowTypeSwitch={false} onClose={onClose} />);
        const source = await screen.findByRole("combobox", { name: "Pickup Point" });
        expect(hubCalls(web)).toEqual([]); // nothing is read before a list opens: no whole master

        const u = userEvent.setup();
        await u.click(source);
        // Both kinds stay selectable, the preferred one (HUB for a first-mile source) first: one page of each.
        await waitFor(() => expect(hubCalls(web).map((c) => params(c.url))).toEqual([{ stationType: "HUB", limit: "50" }, { stationType: "SOC", limit: "50" }]));
        await u.type(screen.getByPlaceholderText("Search..."), "หวยขวาง");
        await u.click(await screen.findByRole("option", { name: /SPK890174 — ห้วยขวาง10/ }));
        expect(hubCalls(web).filter((c) => params(c.url).q).map((c) => params(c.url))).toEqual([
            { q: "หวยขวาง", stationType: "HUB", limit: "50" },
            { q: "หวยขวาง", stationType: "SOC", limit: "50" },
        ]);
        await waitFor(() => expect(source).toHaveTextContent("SPK890174 — ห้วยขวาง10"), { timeout: 3000 });

        await u.click(screen.getByRole("combobox", { name: "Destination (SOC)" }));
        await u.click(await screen.findByRole("option", { name: /SOCE — บัวโรย/ }));
        await u.click(screen.getByRole("button", { name: "Create Assignment" }));
        await waitFor(() => expect(onClose).toHaveBeenCalled());
        // No customer was picked: the hub's billing party (p1) prefilled it.
        expect(web.calls.find((c) => c.method === "POST")!.body).toMatchObject({ sourceHub: "SPK890174", destination: "SOCE", billingPartyId: "p1" });
        // A picked record labels its own code: no lookup request.
        expect(lookups(web)).toEqual([]);
    });

    it("task form: a value outside the current page keeps its label (a lookup by code); a code not in the master shows as is", async () => {
        const web = setup();
        const t = task({ sourceHub: "HUB-059", isMultiDelivery: true, stops: [stop(1, "SOCE"), stop(2, "LEGACY-9")] });
        renderPage("/app/first-mile", <TaskFormDialog mode="edit" taskType="first_mile" task={t} allowTypeSwitch={false} onClose={vi.fn()} />);
        const source = await screen.findByRole("combobox", { name: "Pickup Point" });
        await waitFor(() => expect(source).toHaveTextContent("HUB-059 — ฮับ 59"));
        expect(screen.getByRole("combobox", { name: "Destination (SOC)" })).toHaveTextContent("SOCE — บัวโรย");
        expect(screen.getByRole("combobox", { name: "Stop 2" })).toHaveTextContent("LEGACY-9");
        // One lookup per code (the destination and stop 1 share SOCE's).
        expect(lookups(web).sort()).toEqual(["/api/go/v1/hubs/HUB-059", "/api/go/v1/hubs/LEGACY-9", "/api/go/v1/hubs/SOCE"]);
        expect(hubCalls(web)).toEqual([]);

        // The first page by code (ALANG-A, HUB-000 ... HUB-048) does not hold HUB-059; the trigger still names it.
        const u = userEvent.setup();
        await u.click(source);
        expect(await screen.findByRole("option", { name: /HUB-048 — ฮับ 48/ })).toBeInTheDocument();
        expect(screen.queryByRole("option", { name: /HUB-059/ })).toBeNull();
        expect(source).toHaveTextContent("HUB-059 — ฮับ 59");
    });

    it("trip editor: the origin's lookup labels it and gives the job type; the destination searches on the server, SOCs first", async () => {
        const web = setup();
        web.on("GET", "/api/go/v1/trips/r1", () =>
            json(200, { data: tripDetail({ origin: "RC-01", originHubId: "h4", originPlace: place("RC-01"), destination: "ALANG-A",
                destinationHubId: "h1", destinationPlace: place("ALANG-A") }) }));
        web.on("PATCH", "/api/go/v1/trips/r1", () => json(200, { data: tripDetail() }));
        const onClose = vi.fn();
        renderPage("/app/driver-monitor", <TripEditDialog tripId="r1" onClose={onClose} />);
        const origin = await screen.findByRole("combobox", { name: "Origin" });
        await waitFor(() => expect(origin).toHaveTextContent("RC-01 — ศูนย์คืนสินค้า"));
        // RC-01 is a SOC by its master record only (no SOCE/SOCN/SOCW code): line haul, from the lookup.
        expect(await screen.findByText("Line Haul")).toBeInTheDocument();
        expect(lookups(web).filter((u) => u.endsWith("/RC-01"))).toHaveLength(1);

        const u = userEvent.setup();
        await u.click(screen.getByRole("combobox", { name: "Destination" }));
        await waitFor(() => expect(hubCalls(web).map((c) => params(c.url).stationType)).toEqual(["SOC", "HUB"]));
        await u.type(screen.getByPlaceholderText("Search..."), "หวยขวาง๑๐");
        await u.click(await screen.findByRole("option", { name: /SPK890174 — ห้วยขวาง10/ }));
        await u.click(screen.getByRole("button", { name: "Save Changes" }));
        await waitFor(() => expect(onClose).toHaveBeenCalled());
        expect(web.calls.find((c) => c.method === "PATCH")!.body).toEqual({ destination: "SPK890174" });
    });
});

describe("job-assign import: a later type's failure keeps the earlier type's results", () => {
    it("FM created, LH fails once: the FM rows leave the list; Import again posts LH only, under the same key", async () => {
        const web = setup();
        const header = ["FM/LH (ชนิดงาน)", "Date (วัน)", "Pickup Location (จุดรับงาน)", "Destination (ปลายทาง)", "Time (เวลา)", "Customer (ลูกค้า)"];
        sheet.rows = [header, ["FM", "12/10/2026", "ALANG-A", "SOCE", "09:00", "CJSF"], ["FM", "12/10/2026", "ALANG-A", "SOCN", "09:30", "CJSF"],
            ["LH", "12/10/2026", "SOCE", "HUB-001", "10:00", "CJSF"], ["LH", "12/10/2026", "SOCN", "HUB-002", "11:00", "CJSF"]];
        const posted: { type: string; rows: number; key: string | undefined }[] = [];
        let lhFails = 1;
        web.on("POST", "/api/go/v1/tasks/import", (c) => {
            const b = c.body as { taskType: string; rows: unknown[]; dryRun: boolean };
            posted.push({ type: b.taskType, rows: b.rows.length, key: c.headers["idempotency-key"] });
            if (b.taskType === "line_haul" && lhFails-- > 0) return goErr(503, "unavailable");
            return json(200, { data: { created: b.rows.map((_, i) => ({ row: i + 1, id: `${b.taskType}-${i}`, taskNo: `${b.taskType}-${i}` })), errors: [],
                dryRun: false } });
        });
        renderPage("/app/job-assign", <TaskImportDialog kind="all" onClose={vi.fn()} />);
        const u = userEvent.setup();
        // Nothing about customers is read before the sheet is.
        await screen.findByRole("button", { name: /Click to Upload/ });
        expect(web.count("GET", "/api/go/v1/customers")).toBe(0);
        await u.upload(document.querySelector<HTMLInputElement>('input[type="file"]')!, new File(["x"], "jobs.xlsx"));
        expect(await screen.findByText(/4 records/i)).toBeInTheDocument();
        // The one customer cell is one server search (UI2C), not a read of every customer; the rows' pickers
        // are labelled from that page.
        expect(web.calls.filter((c) => c.url.startsWith("/api/go/v1/customers")).map((c) => params(c.url))).toEqual([{ fields: "minimal", q: "CJSF", limit: "50" }]);
        expect(screen.getAllByRole("combobox", { name: /^Customer \d$/ }).map((b) => b.textContent)).toEqual(Array(4).fill("CJCJSF"));

        await u.click(screen.getByRole("button", { name: "Upload" }));
        expect(await screen.findByText(/2 task\(s\) were imported before the error/)).toBeInTheDocument();
        expect(posted.map((p) => [p.type, p.rows])).toEqual([["first_mile", 2], ["line_haul", 2]]);
        // The FM rows left the list: only the two LH rows remain.
        await waitFor(() => expect(screen.getAllByRole("row")).toHaveLength(3));

        await u.click(screen.getByRole("button", { name: "Upload" }));
        await waitFor(() => expect(posted).toHaveLength(3));
        expect(posted[2]).toMatchObject({ type: "line_haul", rows: 2 });
        // The same LH group keeps its Idempotency-Key, so a lost answer replays instead of writing again.
        expect(posted[2].key).toBe(posted[1].key);
        expect(posted.filter((p) => p.type === "first_mile")).toHaveLength(1);
    });
});
