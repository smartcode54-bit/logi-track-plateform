import React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { fakeWeb, json } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { stubVirtualLayout } from "@/test-utils/virtualLayout";
import type { MaintenanceRecordDTO } from "../../api/maintenance.go";
import { MaintenanceOverviewPage } from "./MaintenanceOverviewPage";

const viewport = vi.hoisted(() => ({ mobile: true }));
vi.mock("@/hooks/use-mobile", () => ({ useIsMobile: () => viewport.mobile }));

const record: MaintenanceRecordDTO = {
    id: "m1", tenantId: "tenant-1", truckId: "truck-1", licensePlate: "70-1234", truckBrand: "Isuzu", type: "PM",
    truckModel: null, truckOwnership: null, truckStatus: null,
    serviceType: "periodic_check", startDate: "2026-10-01", endDate: null, status: "pm_booking",
    appointmentTime: null, pickupAppointmentAt: null, costLaborThb: null, costPartsThb: null,
    totalCostThb: null, invoiceAmountThb: 1500, displayCostThb: 1500, provider: null,
    providerLat: null, providerLng: null, paymentMethod: null, currentMileage: 120000,
    nextServiceMileage: null, driverSubmitted: true, notes: null,
    checkInAt: null, checkOutAt: null, createdBy: null, updatedBy: null,
    files: { images: [], receipts: [], invoices: [] }, createdAt: "2026-10-01T00:00:00Z", updatedAt: "2026-10-01T00:00:00Z",
};
let restoreLayout: (() => void) | undefined;
afterEach(() => { restoreLayout?.(); vi.unstubAllGlobals(); });

function mount(mobile: boolean) {
    viewport.mobile = mobile;
    restoreLayout = stubVirtualLayout({ viewport: 480, row: 180, width: mobile ? 360 : 1280 });
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/maintenance", (call) => json(200, {
        data: new URL(call.url, "http://web.test").searchParams.get("status") === "scheduled" ? [] : [record],
        meta: { statusCounts: { pm_booking: 1 } },
    }));
    renderWithProviders(<MaintenanceOverviewPage />);
    return web;
}

describe("maintenance responsive presentation", () => {
    it("keeps booking fields and the existing record action reachable on a phone without writing", async () => {
        const web = mount(true);
        const history = await screen.findByRole("region", { name: "Service History" });
        expect(within(history).getAllByRole("columnheader")).toHaveLength(1);
        expect(await within(history).findByRole("link", { name: "Manage / Update" })).toHaveAttribute("href", "/app/trucks/maintenance?id=truck-1");
        expect(history).toHaveTextContent("70-1234");
        expect(await screen.findByLabelText("Appt. date · 70-1234")).toBeInTheDocument();
        expect(screen.getAllByRole("button", { name: "Save appointment" }).length).toBeGreaterThan(0);
        expect(web.calls.every((call) => call.method === "GET")).toBe(true);
    });

    it("keeps the full history columns on desktop", async () => {
        mount(false);
        const history = await screen.findByRole("region", { name: "Service History" });
        expect(within(history).getAllByRole("columnheader")).toHaveLength(9);
        expect(await within(history).findByRole("link", { name: "Manage / Update" })).toHaveAttribute("href", "/app/trucks/maintenance?id=truck-1");
    });
});
