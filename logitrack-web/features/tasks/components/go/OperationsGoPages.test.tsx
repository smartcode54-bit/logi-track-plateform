// T34 (issue #54): the Go operations pages rendered under the app's providers, every request through the BFF
// (/api/go/v1/...). The Firebase modules throw if a page imports one (operationsImports.test.ts walks the lazy
// edges). Pinned here: each page loads from its one Go list (the monitor from one aggregate request, no rate
// card, no pricing call: AC 2), a task event makes the board refetch and show the mobile's change without a
// reload (AC 3, through the realtime map's keys), and the export pages through the monitor's cursor and writes
// the file with the lazily loaded xlsx (AC 4).
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const xlsx = vi.hoisted(() => ({ writeFile: vi.fn(), aoa: vi.fn() }));
vi.mock("xlsx", () => ({
    utils: { aoa_to_sheet: (rows: unknown[][]) => (xlsx.aoa(rows), {}), book_new: () => ({}), book_append_sheet: () => undefined },
    writeFile: xlsx.writeFile,
}));
vi.mock("@/firebase/client", () => {
    throw new Error("the Go operations pages must not load the Firebase client");
});
vi.mock("firebase/firestore", () => {
    throw new Error("the Go operations pages must not import Firestore");
});
vi.mock("firebase/functions", () => {
    throw new Error("the Go operations pages must not call Cloud Functions");
});

import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { configureFirebaseBridge } from "@/lib/firebaseBridge";
import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { effectsOf } from "@/lib/realtime/eventMap";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

import { FirstMileBoardPage } from "./TaskBoardPage";
import { DriverMonitorPage } from "@/features/drivers/components/go/DriverMonitorPage";
import { StandbyRecordsPage } from "@/features/standby/components/go/StandbyRecordsPage";
import { IncidentReportsPage } from "@/features/incident-reports/components/go/IncidentReportsPage";

beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
    globalThis.ResizeObserver ??= class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
});
beforeEach(() => {
    configureFirebaseBridge(async () => {
        throw new Error("no Firebase SDK on the Go operations pages");
    });
});
afterEach(() => {
    resetQueryClientForTests();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    configureFirebaseBridge(undefined);
    xlsx.writeFile.mockReset();
    xlsx.aoa.mockReset();
});

const CAPS = [
    "operations:view_first_mile",
    "operations:view_line_haul",
    "operations:manage_tasks",
    "operations:view_driver_monitor",
    "operations:edit_trip_details",
    "operations:view_incidents",
    "operations:create_standby",
];

function setup() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ legacyAuthUid: undefined, capabilities: CAPS }) }));
    web.on("POST", "/api/auth/firebase-token", () => goErr(403, "permission_denied"));
    return web;
}

const renderPage = (pathname: string, ui: React.ReactElement) => {
    window.history.replaceState(null, "", pathname);
    return renderWithProviders(<RouteNamespaces pathname={pathname}>{ui}</RouteNamespaces>, getQueryClient());
};

const params = (url: string) => Object.fromEntries(new URL(url, "http://web.test").searchParams);
const place = (label: string) => ({ raw: label, key: `code:${label}`, label, resolved: true });

const task = (status: string) => ({
    id: "t1", tenantId: "o", taskNo: "FM-12102026-001", taskType: "first_mile", jobCategory: "PRIMARY", status, planAt: "2026-10-11T17:00:00Z",
    planDate: "2026-10-12", planTime: "09:30", actualPickupAt: null, sourceHub: "ALANG-A", sourceHubId: "h1", source: place("วังทองหลาง"),
    destination: "SOCE", destinationHubId: null, destinationSocKey: "SOCE", destinationPlace: place("SOCE"), truckId: null, truckType: "6WH",
    licensePlate: "1กข-1234", driverId: "d1", driverName: "สมชาย ใจดี", helperDriverId: null, helperName: null, runOrder: 1,
    checkInAt: status === "checked_in" ? "2026-10-12T02:00:00Z" : null, checkInLat: null, checkInLng: null, isMultiDelivery: false,
    stops: [], trip: null, createdAt: "2026-10-11T00:00:00Z", updatedAt: "2026-10-11T00:00:00Z",
});

describe("Go operations pages (T34)", () => {
    it("first-mile board: one Go list for the day; a task event refetches it and shows the mobile's check-in (AC 3)", async () => {
        const web = setup();
        let status = "assigned";
        web.on("GET", "/api/go/v1/tasks", () => json(200, { data: [task(status)] }));
        renderPage("/app/first-mile", <FirstMileBoardPage />);
        expect(await screen.findByText("FM-12102026-001")).toBeInTheDocument();
        expect(await screen.findByText("Assigned")).toBeInTheDocument();
        const first = web.calls.find((c) => c.url.startsWith("/api/go/v1/tasks"))!;
        expect(params(first.url)).toMatchObject({ taskType: "first_mile", limit: "200" });
        expect(params(first.url).date).toMatch(/^\d{4}-\d{2}-\d{2}$/);
        // The driver checks in on the phone: Go publishes task.checked_in; the realtime map makes the board stale.
        status = "checked_in";
        await act(async () => {
            for (const i of effectsOf({ event: "task.checked_in", data: { entity: "task", id: "t1", changed: ["status"] } }).invalidate) {
                await getQueryClient().invalidateQueries({ queryKey: i.queryKey });
            }
        });
        expect(await screen.findByText("Checked in")).toBeInTheDocument();
        expect(web.calls.filter((c) => !c.url.startsWith("/api/go/v1/tasks") && !c.url.startsWith("/api/go/v1/me") && !c.url.includes("firebase-token"))).toEqual([]);
    });

    it("driver monitor: one aggregate request, the facets on screen, no rate card or pricing call (AC 2); the export pages through the cursor (AC 4)", async () => {
        const web = setup();
        const row = (id: string, tripNo: string) => ({
            id, tenantId: "o", tripNo, status: "delivered", jobType: "first_mile", jobCategory: "PRIMARY", createdAt: "2026-10-11T02:00:00Z",
            updatedAt: "2026-10-11T02:00:00Z", deliveredAt: "2026-10-11T06:00:00Z", taskId: "t1", taskNo: "FM-1", taskStatus: "completed",
            checkInAt: "2026-10-11T01:00:00Z", runOrder: 1, planAt: null, driverId: "d1", driverName: "สมชาย", origin: place("ALANG-A"),
            destination: place("SOCE"), stops: [], truck: { truckId: "k1", plate: "1กข-1234", key: "id:k1", vehicleClass: "6WH", vehicleClassKey: "6WH" },
            partnerCode: null, sealCode: "S1", isMultiDelivery: false, incidentCount: 0, photoCount: 2,
        });
        web.on("GET", "/api/go/v1/trips/monitor", (c) => {
            const q = params(c.url);
            if (q.facets === "false") return q.cursor ? json(200, { data: [row("r2", "SPXTH-2")] }) : json(200, { data: [row("r1", "SPXTH-1")], nextCursor: "p2" });
            return json(200, { data: [row("r1", "SPXTH-1")], nextCursor: "p2", meta: { facets: { total: 412, statusCounts: { delivered: 400, in_transit: 12 }, incident: 3,
                origins: [], destinations: [], plates: [], vehicleClasses: [], partners: [] } } });
        });
        renderPage("/app/driver-monitor", <DriverMonitorPage />);
        expect(await screen.findByText("SPXTH-1")).toBeInTheDocument();
        expect(await screen.findByText("412")).toBeInTheDocument();
        const monitorCalls = web.calls.filter((c) => c.url.startsWith("/api/go/v1/trips/monitor"));
        expect(monitorCalls).toHaveLength(1);
        expect(params(monitorCalls[0].url)).toMatchObject({ limit: "200" });
        expect(web.calls.some((c) => /rate-entries|fuel-adjustments|billing\/compute|computeTripBillingSnapshot/.test(c.url))).toBe(false);

        const u = userEvent.setup();
        await u.click(screen.getByRole("button", { name: /Export/ }));
        await u.click(await screen.findByRole("button", { name: /Excel/ }));
        await waitFor(() => expect(xlsx.writeFile).toHaveBeenCalledTimes(1));
        const exportCalls = web.calls.filter((c) => c.url.startsWith("/api/go/v1/trips/monitor") && params(c.url).facets === "false");
        expect(exportCalls.map((c) => params(c.url).cursor ?? "")).toEqual(["", "p2"]);
        const sheet = xlsx.aoa.mock.calls[0][0] as unknown[][];
        expect(sheet.slice(1).map((r) => r[0])).toEqual(["SPXTH-1", "SPXTH-2"]);
    });

    it("standby and incidents: one keyset list each, the driver's name from the row (no driver listener)", async () => {
        const web = setup();
        web.on("GET", "/api/go/v1/standby", () => json(200, { data: [{ id: "s1", tenantId: "o", driverId: "d1", driverName: "สมหญิง", taskId: null, tripId: null,
            jobCategory: null, startLocation: "A", endLocation: "B", startedAt: null, endedAt: null, durationMinutes: 45, note: null, status: "completed",
            lat: null, lng: null, truckId: null, truckLicensePlate: null, photos: [], createdAt: "2026-10-11T00:00:00Z" }], nextCursor: "n" }));
        web.on("GET", "/api/go/v1/incidents", () => json(200, { data: [{ id: "i1", tenantId: "o", tripId: "r1", tripNo: "SPXTH-7", driverId: "d1",
            driverName: "สมปอง", delayCause: "incident_cause_tire", description: "ยางแตก", lat: null, lng: null, truckId: null, truckLicensePlate: "1กข",
            mapPhoto: null, situation1Photo: null, situation2Photo: null, reportedByKind: "driver", createdAt: "2026-10-11T00:00:00Z" }] }));
        const standby = renderPage("/app/standby-records", <StandbyRecordsPage />);
        expect(await screen.findByText("สมหญิง")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Load more/ })).toBeInTheDocument();
        standby.unmount();
        renderPage("/app/incident-reports", <IncidentReportsPage />);
        expect(await screen.findByText("SPXTH-7")).toBeInTheDocument();
        expect(screen.getByText("TIRE")).toBeInTheDocument();
        expect(web.calls.some((c) => c.url.startsWith("/api/go/v1/drivers"))).toBe(false);
    });
});
