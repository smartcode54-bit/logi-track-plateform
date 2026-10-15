import React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { createQueryClient } from "@/lib/queryClient";
import { stubVirtualLayout } from "@/test-utils/virtualLayout";
import { RenewalsPage } from "./RenewalsPage";
import TrucksListDashboard from "./TrucksListDashboard";
import TruckPreview from "./TruckPreview";
import { AssignmentHistory, HISTORY_ROW_HEIGHT } from "./AssignmentHistory";
import { useLanguage } from "@/context/language";

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }), usePathname: () => "/app/trucks/view", useSearchParams: () => new URLSearchParams("id=truck-1") }));
afterEach(() => vi.unstubAllGlobals());
const truck = { id: "truck-1", tenantId: "own", ownershipType: "own", licensePlate: "1กก-1234", province: "กรุงเทพมหานคร", brand: "Isuzu", model: "FRR", year: 2023, color: "ขาว", type: "6 Wheels", status: "active", files: {}, currentAssignments: [], statusHistory: [], nextServiceDate: null, currentMileage: 0, nextServiceMileage: null };
const assignment = { id: "a-1", truckId: "truck-1", driverId: "d-1", driverName: null, status: "active" };
function serve(assignmentsReply = () => json(200, { data: [] })) {
    return fakeWeb().on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }))
        .on("GET", "/api/go/v1/truck-assignments", assignmentsReply);
}
function render(ui: React.ReactElement) {
    const client = createQueryClient();
    client.setDefaultOptions({ queries: { retry: false } });
    return renderWithProviders(ui, client);
}
function UseThai() {
    const { setLanguage } = useLanguage();
    return <button onClick={() => setLanguage("th")}>Use Thai</button>;
}

describe("fleet review regressions", () => {
    it("keeps image documents out of the vehicle-photo gallery", async () => {
        serve().on("GET", "/api/go/v1/trucks/truck-1", () => json(200, { data: { ...truck,
            files: { documentTax: [{ key: "tax.png", url: "/tax.png" }], insuranceDocument: [{ key: "policy.jpg", url: "/policy.jpg" }] },
        } }));
        render(<TruckPreview />);
        expect(await screen.findByText("0 Photos")).toBeInTheDocument();
    });
    it("matches renewal search against the vehicle class displayed in the table", async () => {
        serve().on("GET", "/api/go/v1/trucks/renewals", () => json(200, { data: [{ ...truck,
            tax: { state: "ok", amountThb: null }, insurance: { state: "ok", amountThb: null },
        }] }));
        render(<RenewalsPage />);
        await screen.findByText(/^6WH ·/);
        await userEvent.setup().type(screen.getByRole("textbox"), "6WH");
        expect(screen.getByText("1กก-1234")).toBeInTheDocument();
    });
    it("previews imports with a dry run and keeps rejected rows and Go reasons after saving", async () => {
        const web = serve().on("GET", "/api/go/v1/trucks", () => json(200, { data: [] }))
            .on("POST", "/api/go/v1/trucks/import", () => json(200, { data: {
                created: 1, updated: 0, unchanged: 0,
                errors: [{ row: 1, licensePlate: "1กก-1234", fields: [{ field: "vin", reason: "invalid" }] }],
            } }));
        render(<TrucksListDashboard />);
        const user = userEvent.setup();
        await user.click(await screen.findByRole("button", { name: "Import Trucks" }));
        const dialog = await screen.findByRole("dialog", { name: "Import Trucks" });
        await user.upload(await within(dialog).findByLabelText("Select File"), new File([
            "License Plate,Province,Type,Brand,Model,VIN\n1กก-1234,Bangkok,6W,Isuzu,FRR,invalid\n1กก-5678,Bangkok,6W,Isuzu,FRR,JH4KA8260MC000001",
        ], "trucks.csv", { type: "text/csv" }));
        await within(dialog).findByText("1กก-5678");
        await user.click(within(dialog).getByRole("button", { name: "Confirm Import" }));
        await waitFor(() => expect(within(dialog).queryByText("1กก-5678")).toBeNull());
        expect(dialog).toBeInTheDocument();
        expect(within(dialog).getByText("1กก-1234")).toBeInTheDocument();
        expect(within(dialog).getByText(/^VIN:/)).toHaveTextContent("This value is not valid.");
        expect(web.calls.filter((call) => call.method === "POST").map((call) => (call.body as { dryRun: boolean }).dryRun)).toEqual([true, false]);
    });
    it("shows one assignment error and a short placeholder in each driver cell", async () => {
        serve(() => goErr(503, "unavailable")).on("GET", "/api/go/v1/trucks", () => json(200, { data: [truck, { ...truck, id: "truck-2", licensePlate: "1กก-5678" }] }));
        render(<TrucksListDashboard />);
        await screen.findByText("1กก-5678");
        const message = "The list could not be loaded. Please try again.";
        await screen.findByRole("button", { name: "Try again" });
        expect(screen.getAllByText(message)).toHaveLength(1);
        expect(within(screen.getByRole("table")).queryByText(message)).toBeNull();
        expect(within(screen.getAllByRole("row")[1]).getAllByRole("cell")[3]).toHaveTextContent("-");
        const pm = screen.getAllByRole("link", { name: "PM not configured" });
        for (const link of pm) expect(link).toHaveClass("max-md:min-h-11", "max-md:min-w-11");
    });
    it.each([1, 2])("uses neutral avatars for %i unnamed assigned drivers in the list and profile", async (count) => {
        const assignments = Array.from({ length: count }, (_, i) => ({ ...assignment, id: `a-${i}`, driverId: `d-${i}` }));
        serve(() => json(200, { data: assignments })).on("GET", "/api/go/v1/trucks", () => json(200, { data: [truck] }))
            .on("GET", "/api/go/v1/trucks/truck-1", () => json(200, { data: { ...truck, currentAssignments: assignments } }));
        const view = render(<><UseThai /><TrucksListDashboard /><TruckPreview /></>);
        await screen.findByRole("heading", { level: 1, name: "1กก-1234" });
        const user = userEvent.setup();
        for (const language of ["en", "th"]) {
            if (language === "th") await user.click(screen.getByRole("button", { name: "Use Thai" }));
            expect(screen.queryAllByText(language === "en" ? "NO" : "ไม")).toHaveLength(0);
            const avatars = view.container.querySelectorAll('[data-slot="avatar-fallback"]');
            await waitFor(() => expect(avatars).toHaveLength(count));
            for (const avatar of avatars) expect(avatar).toHaveTextContent("-");
            if (count === 2) {
                await user.click(screen.getByText(language === "en" ? "2 drivers" : "คนขับ 2 คน"));
                expect(screen.queryAllByText(language === "en" ? "NO" : "ไม")).toHaveLength(0);
                await user.keyboard("{Escape}");
            }
        }
    });
    it("uses a neutral avatar for an unnamed driver in assignment history", async () => {
        const restore = stubVirtualLayout({ viewport: 600, row: HISTORY_ROW_HEIGHT });
        try {
            serve(() => json(200, { data: [{ ...assignment, createdAt: "2026-10-01T00:00:00Z" }] }));
            render(<AssignmentHistory onRevoke={vi.fn()} />);
            await screen.findByText("1 loaded");
            expect(screen.queryByText("NO")).toBeNull();
            expect(screen.getAllByText("-").length).toBeGreaterThan(0);
        } finally { restore(); }
    });
});
