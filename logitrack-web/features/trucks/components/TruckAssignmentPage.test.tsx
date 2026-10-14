// UI2C (follow-up of issue #142): the truck-assignment page picks with the shared server pickers instead of
// filtering its own full driver and truck lists:
// - drivers: `DriverSelect` over `GET /v1/drivers?q` (every status), full rows for the licence type; a driver that
//   still holds an active assignment or is inactive is shown but cannot be picked; an on_duty driver without a
//   truck (set on the driver form, or loaded from the legacy app) can be (Go refuses only the other two);
// - trucks: `TruckSelect` over `GET /v1/trucks?ownership=own&q`; a truck in maintenance is shown but cannot be picked;
// - no list of every driver is read: "available drivers" is active + on_duty of a one-row page's
//   `meta.statusCounts`, less the drivers holding a truck;
// - no list of every truck is read either (UI8): "total trucks" is the own trucks not in maintenance, from
//   `meta.statusCounts` of a one-row `GET /v1/trucks?ownership=own` page;
// - the assignment posts the picked ids only, and the form resets.
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import TruckAssignmentPage from "@/app/app/truck-assignment/page";
import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { fakeWeb, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { stubVirtualLayout } from "@/test-utils/virtualLayout";

const pad = (i: number) => String(i).padStart(4, "0");
// d-0001 still holds a truck on a legacy row; d-0002 is on_duty without a truck; d-0003 is inactive.
const DRIVERS = Array.from({ length: 60 }, (_, i) => ({
    id: `d-${pad(i)}`,
    displayName: `สมชาย ${pad(i)}`,
    status: i === 2 ? "on_duty" : i === 3 ? "inactive" : "active",
    licenseType: i % 2 ? "ท.2" : "ท.3",
    currentAssignment: null,
}));
const TRUCKS = Array.from({ length: 80 }, (_, i) => ({
    id: `t-${pad(i)}`,
    licensePlate: `กข-${pad(i)}`,
    type: i % 2 ? "6WH" : "4WJ",
    model: "FRR",
    status: i === 7 ? "maintenance" : "active",
    ownershipType: "own",
}));
const ACTIVE = [{ id: "a-1", tenantId: "t-own", truckId: "t-0050", truckPlate: "กข-0050", truckModel: "FRR", driverId: "d-0001", driverName: "สมชาย 0001",
    status: "active", adminUserId: null, adminLabel: "Admin", createdAt: "2026-10-01T00:00:00Z", revokedAt: null }];

const params = (call: FakeCall) => new URL(call.url, "http://web.test").searchParams;

let restoreLayout: () => void;
beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
    restoreLayout = stubVirtualLayout({ viewport: 600, row: 40 });
});
afterAll(() => restoreLayout());
afterEach(() => {
    resetQueryClientForTests();
    vi.unstubAllGlobals();
});

function setup() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () =>
        json(200, { data: makeMe({ capabilities: ["fleet:view_assignments", "fleet:manage_assignments", "drivers:view", "fleet:view_trucks"] }) }));
    web.on("GET", "/api/go/v1/truck-assignments", () => json(200, { data: ACTIVE }));
    web.on("POST", "/api/go/v1/truck-assignments", () => json(201, { data: { id: "a-2" } }));
    web.on("GET", "/api/go/v1/drivers", (call) => {
        const p = params(call);
        const q = (p.get("q") ?? "").toLowerCase();
        const limit = Number(p.get("limit") ?? "100");
        const hits = DRIVERS.filter((d) => !q || d.displayName.toLowerCase().includes(q));
        return json(200, { data: hits.slice(0, limit), meta: { statusCounts: { active: 57, on_duty: 9, inactive: 4 } } });
    });
    web.on("GET", "/api/go/v1/trucks", (call) => {
        const p = params(call);
        const q = (p.get("q") ?? "").toLowerCase();
        const limit = Number(p.get("limit") ?? "100");
        const from = Number(p.get("cursor") ?? "0");
        const hits = TRUCKS.filter((tr) => (!p.get("ownership") || tr.ownershipType === p.get("ownership")) && (!q || tr.licensePlate.toLowerCase().includes(q)));
        const next = from + limit < hits.length ? String(from + limit) : undefined;
        // Go counts every matching truck per status, whatever the page (UI8).
        const statusCounts = { active: 0, inactive: 0, maintenance: 0, insurance_claim: 0, sold: 0 } as Record<string, number>;
        for (const tr of hits) statusCounts[tr.status] += 1;
        return json(200, { data: hits.slice(from, from + limit), ...(next ? { nextCursor: next } : {}), meta: { statusCounts } });
    });
    const driverCalls = () => web.calls.filter((c) => c.url.startsWith("/api/go/v1/drivers")).map(params);
    const truckPickerCalls = () => web.calls.filter((c) => c.url.startsWith("/api/go/v1/trucks") && params(c).get("limit") === "50").map(params);
    const truckCalls = () => web.calls.filter((c) => c.url.startsWith("/api/go/v1/trucks")).map((c) => Object.fromEntries(params(c)));
    return { web, driverCalls, truckPickerCalls, truckCalls };
}

const renderPage = () => renderWithProviders(<TruckAssignmentPage />, getQueryClient());

describe("truck assignment: server pickers (UI2C)", () => {
    it("counts drivers from the status counts and lists drivers on the server, an assigned or inactive one not pickable", async () => {
        const { driverCalls, truckCalls } = setup();
        renderPage();
        // Available drivers = active 57 + on_duty 9 - 1 holding a truck; trucks = own trucks not in maintenance.
        await waitFor(() => expect(screen.getByText("65")).toBeInTheDocument());
        expect(screen.getByText("79")).toBeInTheDocument();
        expect(driverCalls().map((p) => Object.fromEntries(p))).toEqual([{ fields: "minimal", limit: "1" }]);
        // Both counters from one-row pages: no truck list (80 trucks would be a page of 100) is read.
        expect(truckCalls()).toEqual([{ ownership: "own", limit: "1" }]);

        const u = userEvent.setup();
        await u.click(screen.getByRole("combobox", { name: "1. Select Available Driver" }));
        await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(20));
        // Every status, full rows (no fields=minimal) for the licence type; one page of 20.
        expect(Object.fromEntries(driverCalls()[1])).toEqual({ limit: "20" });
        expect(screen.getByRole("option", { name: /สมชาย 0000/ })).toHaveTextContent("ท.3");
        const held = screen.getByRole("option", { name: /สมชาย 0001/ });
        expect(held).toHaveAttribute("aria-disabled", "true");
        expect(held).toHaveTextContent("Already assigned · กข-0050");
        expect(screen.getByRole("option", { name: /สมชาย 0002/ })).not.toHaveAttribute("aria-disabled", "true");
        const inactive = screen.getByRole("option", { name: /สมชาย 0003/ });
        expect(inactive).toHaveAttribute("aria-disabled", "true");
        expect(inactive).toHaveTextContent("Inactive");

        await u.type(screen.getByPlaceholderText("Search by name..."), "สมชาย 0042");
        await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(1));
        expect(driverCalls().slice(1).map((p) => p.get("q"))).toEqual([null, "สมชาย 0042"]);
    });

    it("lists the own fleet's trucks on the server (a Thai plate search), one in maintenance not pickable, and assigns the picked ids", async () => {
        const { web, truckPickerCalls } = setup();
        renderPage();
        const u = userEvent.setup();

        await u.click(await screen.findByRole("combobox", { name: "2. Select Available Truck" }));
        await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(50));
        expect(Object.fromEntries(truckPickerCalls()[0])).toEqual({ ownership: "own", limit: "50" });
        const repair = screen.getByRole("option", { name: /กข-0007/ });
        expect(repair).toHaveAttribute("aria-disabled", "true");
        expect(repair).toHaveTextContent("In maintenance");
        await u.type(screen.getByPlaceholderText("Search by plate or model..."), "กข-0063");
        await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(1));
        expect(truckPickerCalls().map((p) => p.get("q"))).toEqual([null, "กข-0063"]);
        await u.click(screen.getByRole("option", { name: /กข-0063/ }));
        expect(screen.getByText("6WH - กข-0063")).toBeInTheDocument();

        await u.click(screen.getByRole("combobox", { name: "1. Select Available Driver" }));
        await u.type(await screen.findByPlaceholderText("Search by name..."), "0042");
        await u.click(await screen.findByRole("option", { name: /สมชาย 0042/ }));
        expect(screen.getByText("สมชาย 0042 - ท.3")).toBeInTheDocument();

        await u.click(screen.getByRole("button", { name: "Confirm and Assign" }));
        await waitFor(() => expect(web.calls.some((c) => c.method === "POST")).toBe(true));
        expect(web.calls.find((c) => c.method === "POST")!.body).toEqual({ truckId: "t-0063", driverId: "d-0042" });
        await waitFor(() => expect(screen.getByText("No driver selected")).toBeInTheDocument());
        expect(within(screen.getByRole("combobox", { name: "2. Select Available Truck" })).queryByText("กข-0063")).toBeNull();
    });
});
