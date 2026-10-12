// T54R (issue #74): the dashboard over Go. The page imports no Firebase module (the mocks below throw on a
// static import; firebaseImports.test.ts walks the import graph). One summary request per period carries
// every figure (TW6), the counters come from ['badges'] (no expense or chat listener), the map and the
// position list share ['vehicleLocations'], and a principal without a block's capability neither asks for
// it nor sees its card.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("@/firebase/client", () => {
    throw new Error("the Go dashboard must not load the Firebase client");
});
vi.mock("firebase/firestore", () => {
    throw new Error("the Go dashboard must not import Firestore");
});
// Leaflet needs a real layout; the map client is replaced by a probe of what the card hands it.
vi.mock("./FleetMapClient", () => ({
    FleetMapClient: ({ vehicles, incidentTruckIds, incidentPlates }: { vehicles: { truckId: string }[]; incidentTruckIds: Set<string>; incidentPlates: Set<string> }) => (
        <div data-testid="map-probe">{`${vehicles.map((v) => v.truckId).join(",")}|${[...incidentTruckIds].join(",")}|${[...incidentPlates].join(",")}`}</div>
    ),
}));

import { configureFirebaseBridge } from "@/lib/firebaseBridge";
import { fakeWeb, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import type { MeDTO } from "@/features/auth/api/me";
import { periodRange, type DashboardSummaryDTO } from "../api/summary";
import { DashboardPage } from "./DashboardPage";

beforeAll(() => {
    globalThis.ResizeObserver ??= class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
});
beforeEach(() => {
    window.history.replaceState(null, "", "/app/dashboard");
    configureFirebaseBridge(async () => {
        throw new Error("no Firebase SDK on the dashboard");
    });
});
afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    configureFirebaseBridge(undefined);
});

const ALL = [
    "security:view_overview",
    "drivers:view",
    "operations:view_driver_monitor",
    "operations:view_incidents",
    "reporting:view_analytics",
    "fleet:view_trucks",
    "fleet:view_live_map",
    "fleet:manage_maintenance",
    "accounting:audit_expense",
    "chat:view",
];

const SUMMARY: DashboardSummaryDTO = {
    period: { from: "2026-10-06", to: "2026-10-12", days: 7 },
    trips: { delivered: 120, parcels: 3450, withIncident: 6, onTimePct: 95, delayPct: 5 },
    activity: {
        days: ["06", "07", "08", "09", "10", "11", "12"].map((d, i) => ({ date: `2026-10-${d}`, delivered: i * 3 })),
        total: 63,
        previousTotal: 50,
        trendPct: 26,
    },
    incidents: { inPeriod: 6 },
    drivers: { active: 30, onDuty: 12, inactive: 3, total: 45 },
    chats: { unassigned: 4, urgent: 1 },
    compliance: {
        taxDue: 1,
        insuranceDue: 0,
        serviceDateDue: 0,
        serviceKmDue: 1,
        items: [
            { truckId: "tr-1", licensePlate: "70-1111", kind: "tax", dueDate: "2026-10-15", daysRemaining: 3 },
            { truckId: "tr-2", licensePlate: "70-2222", kind: "service_km", kmRemaining: -120 },
        ],
        itemsTotal: 3,
    },
    recent: [
        { type: "delivered", id: "r1", at: new Date(Date.now() - 5 * 60_000).toISOString(), plate: "70-1111", place: "SOC North" },
        { type: "incident", id: "r2", at: new Date(Date.now() - 2 * 3600_000).toISOString(), text: "Flat tyre" },
    ],
};

const BY_ROLE = {
    scope: { all: false, tenantId: "t-own" },
    total: 20,
    none: 0,
    tenantRoles: { tenant_admin: 1, manager: 2, operation_staff: 3, operator: 1, user: 1, driver: 12 },
    scopes: { customer: 0, dispatcher: 0 },
    platformRoles: { platform_admin: 0, support: 0 },
};

const LOCATIONS = [
    { truckId: "tr-1", tenantId: "t-own", licensePlate: "70-1111", lat: 13.7, lng: 100.5, speedKmh: 40, heading: 90, lastGpsAt: "2026-10-12T01:00:00Z", updatedAt: "2026-10-12T01:00:05Z", engineOn: true, positionDescription: "Bang Na" },
    { truckId: "tr-3", tenantId: "t-own", licensePlate: "70-3333", lat: 13.9, lng: 100.6, speedKmh: 0, heading: null, lastGpsAt: "2026-10-12T00:50:00Z", updatedAt: "2026-10-12T00:50:05Z", engineOn: false, positionDescription: null },
];

const INCIDENTS = [
    { id: "i1", truckId: "tr-1", truckLicensePlate: "70-1111", createdAt: "2026-10-11T03:00:00Z" },
    { id: "i2", truckId: null, truckLicensePlate: "70-3333", createdAt: "2026-10-10T03:00:00Z" },
];

function setup(capabilities: string[] = ALL, meOver: Partial<MeDTO> = {}) {
    const web = fakeWeb();
    const me = makeMe({ id: "u-self", legacyAuthUid: undefined, capabilities, ...meOver });
    web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
    web.on("GET", "/api/go/v1/config/web-flags", () => json(200, { data: { domains: { auth: "go", dashboard: "go" } } }));
    web.on("GET", "/api/go/v1/dashboard/summary", (call) => {
        const q = new URL(call.url, "http://web.test").searchParams;
        return json(200, { data: { ...SUMMARY, period: { from: q.get("from"), to: q.get("to"), days: 7 } } });
    });
    web.on("GET", "/api/go/v1/stats/users-by-role", () => json(200, { data: BY_ROLE }));
    web.on("GET", "/api/go/v1/vehicle-locations", () => json(200, { data: LOCATIONS }));
    web.on("GET", "/api/go/v1/incidents", () => json(200, { data: INCIDENTS, nextCursor: "c2" }));
    web.on("GET", "/api/go/v1/badges", () => json(200, { data: { pendingExpenses: 7, unassignedChats: 4, urgentChats: 1 } }));
    return { web, me };
}

const queryOf = (call: FakeCall) => Object.fromEntries(new URL(call.url, "http://web.test").searchParams);
const callsTo = (web: ReturnType<typeof fakeWeb>, path: string) => web.calls.filter((c) => c.url.split("?")[0] === `/api/go${path}`);

describe("the dashboard over Go", () => {
    it("loads every figure of the week from one summary request, the counters from ['badges']", async () => {
        const { web } = setup();
        renderWithProviders(<DashboardPage />);

        expect(await screen.findByTestId("stat-trips")).toHaveTextContent("120 / 3,450");
        expect(screen.getByTestId("stat-efficiency")).toHaveTextContent("95% / 5%");
        expect(screen.getByTestId("stat-drivers")).toHaveTextContent("42");
        expect(screen.getByTestId("stat-drivers")).toHaveTextContent("45 drivers in total");
        // Users other than drivers: 20 in reach, 12 drivers.
        await waitFor(() => expect(screen.getByTestId("stat-users")).toHaveTextContent("8"));

        const summaryCalls = callsTo(web, "/v1/dashboard/summary");
        expect(summaryCalls).toHaveLength(1);
        expect(queryOf(summaryCalls[0])).toEqual(periodRange("week"));

        expect(screen.getByTestId("activity-chart")).toHaveTextContent("63");
        expect(screen.getByTestId("activity-trend")).toHaveTextContent("↑ 26%");
        expect(await screen.findByTestId("expense-audit")).toHaveTextContent("7");
        expect(within(screen.getByTestId("chat-status")).getByTestId("chats-unassigned")).toHaveTextContent("4");
        expect(within(screen.getByTestId("chat-status")).getByTestId("chats-urgent")).toHaveTextContent("1");

        const recent = screen.getByTestId("recent-updates");
        expect(recent).toHaveTextContent("Delivery completed · 70-1111");
        expect(recent).toHaveTextContent("5 minutes ago · SOC North");
        expect(recent).toHaveTextContent("Flat tyre");

        const compliance = screen.getByTestId("compliance");
        expect(compliance).toHaveTextContent("3 alerts");
        expect(compliance).toHaveTextContent("due on 15/10/2026");
        expect(compliance).toHaveTextContent("3 days left");
        expect(compliance).toHaveTextContent("120 km overdue");
        expect(compliance).toHaveTextContent("1 more not listed");
        expect(within(compliance).getAllByRole("link")[0]).toHaveAttribute("href", "/app/trucks/edit?id=tr-1");

        // No request reached Firestore or a listener; every read went through the BFF.
        expect(web.calls.every((c) => c.url.startsWith("/api/go/v1/") || c.url.startsWith("/api/auth/"))).toBe(true);
    });

    it("the map and the position list share ['vehicleLocations']; the first incident page flags trucks", async () => {
        const { web } = setup();
        renderWithProviders(<DashboardPage />);
        expect(await screen.findByTestId("map-probe")).toHaveTextContent("tr-1,tr-3|tr-1|70-1111,70-3333");
        expect(screen.getByTestId("fleet-map")).toHaveTextContent("latest 2 incident reports");
        const positions = screen.getByTestId("vehicle-positions");
        expect(positions).toHaveTextContent("70-1111");
        expect(positions).toHaveTextContent("Bang Na");
        expect(callsTo(web, "/v1/vehicle-locations")).toHaveLength(1);
        const incidents = callsTo(web, "/v1/incidents");
        expect(incidents).toHaveLength(1); // the first page only, never the next cursor
        expect(queryOf(incidents[0])).toEqual(periodRange("week"));
    });

    it("the period switch asks for the last 30 days in one more request", async () => {
        const { web } = setup();
        const user = userEvent.setup();
        renderWithProviders(<DashboardPage />);
        await screen.findByTestId("stat-trips");
        await user.click(screen.getByRole("button", { name: "Month" }));
        await waitFor(() => expect(callsTo(web, "/v1/dashboard/summary")).toHaveLength(2));
        const month = periodRange("month");
        expect(queryOf(callsTo(web, "/v1/dashboard/summary")[1])).toEqual(month);
        expect(screen.getByTestId("dashboard-period")).toHaveTextContent(`Last 30 days · ${month.from} to ${month.to}`);
        expect(screen.getByRole("button", { name: "Month" })).toHaveAttribute("aria-pressed", "true");
    });

    it("asks for nothing a principal may not read, and shows only its own blocks", async () => {
        const { web } = setup(["drivers:view"]);
        web.handlers.unshift((call) =>
            call.url.startsWith("/api/go/v1/dashboard/summary")
                ? json(200, { data: { period: SUMMARY.period, drivers: SUMMARY.drivers, recent: [] } })
                : call.url.startsWith("/api/go/v1/badges")
                  ? json(200, { data: {} })
                  : undefined
        );
        renderWithProviders(<DashboardPage />);
        expect(await screen.findByTestId("stat-drivers")).toBeInTheDocument();
        await waitFor(() => expect(callsTo(web, "/v1/badges")).toHaveLength(1));
        for (const id of ["stat-users", "stat-trips", "activity-chart", "fleet-map", "vehicle-positions", "expense-audit", "chat-status", "compliance"]) {
            expect(screen.queryByTestId(id)).toBeNull();
        }
        expect(screen.getByTestId("recent-updates")).toHaveTextContent("No recent activity yet.");
        for (const path of ["/v1/stats/users-by-role", "/v1/vehicle-locations", "/v1/incidents"]) expect(callsTo(web, path)).toHaveLength(0);
    });

    it("says so when the role has no figure at all", async () => {
        const { web } = setup([]);
        web.handlers.unshift((call) =>
            call.url.startsWith("/api/go/v1/dashboard/summary")
                ? json(200, { data: { period: SUMMARY.period, recent: [] } })
                : call.url.startsWith("/api/go/v1/badges")
                  ? json(200, { data: {} })
                  : undefined
        );
        renderWithProviders(<DashboardPage />);
        expect(await screen.findByText("Your role has no dashboard figures to show.")).toBeInTheDocument();
    });

    it("offers a retry when the summary fails", async () => {
        const { web } = setup();
        let fail = true;
        web.handlers.unshift((call) =>
            call.url.startsWith("/api/go/v1/dashboard/summary") && fail ? json(400, { error: { code: "bad_request", message: "x", details: {}, requestId: "r" } }) : undefined
        );
        const user = userEvent.setup();
        renderWithProviders(<DashboardPage />);
        expect(await screen.findByRole("alert")).toHaveTextContent("The dashboard could not be loaded.");
        fail = false;
        await user.click(screen.getByRole("button", { name: "Retry" }));
        expect(await screen.findByTestId("stat-trips")).toHaveTextContent("120 / 3,450");
    });
});
