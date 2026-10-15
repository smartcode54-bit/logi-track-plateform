import React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { createQueryClient } from "@/lib/queryClient";
import TrucksListDashboard from "./TrucksListDashboard";
import { TruckAssignmentHistory } from "./TruckAssignmentHistory";
import { RenewalsPage } from "./RenewalsPage";
import { useLanguage } from "@/context/language";
import { FileViewer } from "@/components/ui/file-viewer";
import { IdentificationSection } from "./form/IdentificationSection";
import { FormProvider, useForm } from "react-hook-form";
import { truckDefaultValues } from "@/validate/truckSchema";
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }), usePathname: () => "/app/trucks", useSearchParams: () => new URLSearchParams() }));
afterEach(() => vi.unstubAllGlobals());
const record = { id: "truck-1", tenantId: "own", ownershipType: "own", licensePlate: "1กก-1234", province: "กรุงเทพมหานคร", brand: "Isuzu", model: "FRR", year: 2023, color: "ขาว", type: "6 Wheels", status: "active", nextServiceDate: null, currentMileage: 0, nextServiceMileage: null };
function serve() {
    return fakeWeb().on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }))
        .on("GET", "/api/go/v1/truck-assignments", () => json(200, { data: [] }));
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
function PlateForm() {
    const form = useForm({ defaultValues: truckDefaultValues });
    return <FormProvider {...form}><IdentificationSection /></FormProvider>;
}

describe("fleet demo controls and query states", () => {
    it.each([
        ["trucks", "/api/go/v1/trucks", () => <TrucksListDashboard />],
        ["renewals", "/api/go/v1/trucks/renewals", () => <RenewalsPage />],
        ["assignments", "/api/go/v1/truck-assignments", () => <TruckAssignmentHistory truckId="truck-1" />],
    ] as const)("%s pending query keeps an accessible loader localized across a language change", async (_name, path, page) => {
        let finish!: (response: Response) => void;
        const pending = new Promise<Response>((resolve) => { finish = resolve; });
        const web = fakeWeb().on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }));
        web.on("GET", path, () => pending);
        web.on("GET", "/api/go/v1/truck-assignments", () => json(200, { data: [] }));
        render(<><UseThai />{page()}</>);
        const loading = await screen.findByText("Loading...");
        expect(loading.closest('[role="status"]')).toHaveAttribute("aria-busy", "true");
        await userEvent.setup().click(screen.getByRole("button", { name: "Use Thai" }));
        expect((await screen.findByText("กำลังโหลด...")).closest('[role="status"]')).toHaveAttribute("aria-busy", "true");
        finish(json(200, { data: [] }));
        await waitFor(() => expect(screen.queryByText("กำลังโหลด...")).toBeNull());
    });
    it("plate input accepts all eight characters from the localized example", async () => {
        serve(); render(<PlateForm />);
        const plate = await screen.findByLabelText(/License Plate/) as HTMLInputElement;
        expect(plate).toHaveAttribute("maxlength", "8");
        await userEvent.setup().type(plate, "1กก-1234");
        expect(plate).toHaveValue("1กก-1234");
        expect(plate.checkValidity()).toBe(true);
    });
    it("PM cards filter the complete fleet by the same status shown on rows", async () => {
        const rows = [
            { ...record, id: "due-zero", licensePlate: "1กก-1001", nextServiceMileage: 2000 },
            { ...record, id: "due", licensePlate: "1กก-1002", currentMileage: 100, nextServiceMileage: 2000 },
            { ...record, id: "overdue", licensePlate: "1กก-1003", currentMileage: 100, nextServiceMileage: 0 },
            { ...record, id: "normal", licensePlate: "1กก-1004", nextServiceMileage: 2001 },
            { ...record, id: "unknown", licensePlate: "1กก-1005" },
        ];
        serve().on("GET", "/api/go/v1/trucks", () => json(200, { data: rows }));
        render(<TrucksListDashboard />);
        await screen.findByText("1กก-1005");
        expect(screen.getByText("PM not configured")).toBeInTheDocument();
        const user = userEvent.setup();
        await user.click(screen.getByRole("button", { name: /PM Soon.*2/ }));
        await waitFor(() => expect(screen.queryByText("1กก-1005")).toBeNull());
        expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(3);
        await user.click(screen.getByRole("button", { name: /PM overdue.*1/ }));
        await screen.findByText("1กก-1003");
        expect(screen.queryByText("1กก-1001")).toBeNull();
    });
    it("truck query failure shows retry and never empty rows or zero cards", async () => {
        let failed = true;
        serve().on("GET", "/api/go/v1/trucks", () => failed ? goErr(503, "unavailable") : json(200, { data: [record] }));
        render(<TrucksListDashboard />);
        const retries = await screen.findAllByRole("button", { name: "Try again" });
        expect(screen.queryByText("No trucks found matching your filters.")).toBeNull();
        expect(screen.queryByText("Own Fleet")).toBeNull();
        expect(screen.queryByRole("button", { name: /PM Soon: 0|PM overdue: 0|Active: 0/ })).toBeNull();
        expect(screen.queryByText(/Showing 0.*0/)).toBeNull();
        expect(screen.queryByRole("button", { name: "Previous page" })).toBeNull();
        expect(screen.queryByRole("button", { name: "Next page" })).toBeNull();
        expect(screen.queryByText("0 loaded")).toBeNull();
        failed = false;
        await userEvent.setup().click(retries[retries.length - 1]);
        expect(await screen.findByText("1กก-1234")).toBeInTheDocument();
    });
    it("renewal query failure is an error with retry", async () => {
        serve().on("GET", "/api/go/v1/trucks/renewals", () => goErr(503, "unavailable"));
        render(<RenewalsPage />);
        expect(await screen.findByRole("button", { name: "Try again" })).toBeInTheDocument();
        expect(screen.queryByText("0.00%" )).toBeNull();
    });
    it("assignment history error retries to a successful empty state", async () => {
        let failed = true;
        fakeWeb().on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }))
            .on("GET", "/api/go/v1/truck-assignments", () => failed ? goErr(503, "unavailable") : json(200, { data: [] }));
        render(<TruckAssignmentHistory truckId="truck-1" />);
        const retry = await screen.findByRole("button", { name: "Try again" });
        expect(screen.queryByText("No recent assignments found.")).toBeNull();
        failed = false;
        await userEvent.setup().click(retry);
        expect(await screen.findByText("No recent assignments found.")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
    });
    it("image documents get working zoom controls; PDF gets an iframe and file link", async () => {
        serve();
        render(<FileViewer isOpen onClose={vi.fn()} files={[
            { url: "/insurance.png", type: "pdf", label: "Insurance image" },
            { url: "/registration.pdf", type: "pdf", label: "Registration PDF" },
        ]} />);
        const zoom = await screen.findByRole("button", { name: "Zoom in" });
        for (const control of ["Zoom in", "Zoom out", "Reset zoom", "Close", "Next"]) {
            expect(screen.getByRole("button", { name: control })).toHaveClass("max-md:min-h-11", "max-md:min-w-11");
        }
        await userEvent.setup().click(zoom);
        expect(screen.getByRole("img")).toHaveAttribute("src", "/insurance.png");
        expect(screen.getByRole("img").parentElement).toHaveStyle({ transform: "translate(0px, 0px) scale(1.5)" });
        await userEvent.setup().click(screen.getByRole("button", { name: "Next" }));
        expect(screen.queryByRole("button", { name: "Zoom in" })).toBeNull();
        expect(screen.getByRole("button", { name: "Previous" })).toHaveClass("max-md:min-h-11", "max-md:min-w-11");
        expect(screen.getByTitle("Registration PDF")).toHaveAttribute("src", "/registration.pdf");
        expect(screen.getByRole("link", { name: "Open or download file" })).toHaveAttribute("href", "/registration.pdf");
    });
});
