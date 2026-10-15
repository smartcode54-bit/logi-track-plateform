import React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { FormProvider, useForm } from "react-hook-form";
import { useLanguage } from "@/context/language";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { resetQueryClientForTests } from "@/lib/queryClient";
import TruckAssignmentPage from "@/app/app/truck-assignment/page";
import type { TruckDetailDTO } from "../api/trucks";
import TrucksListDashboard from "./TrucksListDashboard";
import TruckPreview from "./TruckPreview";
import { TruckComplianceCards } from "./TruckComplianceCards";
import { Step4Documentation } from "./form/TruckSteps/Step4Documentation";

function LanguageToggle() {
    const { setLanguage } = useLanguage();
    return <button onClick={() => setLanguage("th")}>Use Thai</button>;
}

function DocumentationSummary() {
    const form = useForm();
    return <FormProvider {...form}><Step4Documentation /></FormProvider>;
}

vi.mock("next/navigation", () => ({
    useRouter: () => ({ push: vi.fn(), replace: vi.fn(), prefetch: vi.fn(), back: vi.fn() }),
    usePathname: () => "/app/trucks/view",
    useSearchParams: () => new URLSearchParams("id=truck-1"),
}));

const truck = {
    id: "truck-1", tenantId: "partner-1", ownershipType: "subcontractor", licensePlate: "70-1234",
    province: "Bangkok", status: "maintenance", type: "6 Wheels", brand: "Isuzu", model: "FRR", year: 2026,
    files: {}, statusHistory: [], currentAssignments: [], nextServiceDate: null, nextServiceMileage: null,
};

function serve(over: Partial<TruckDetailDTO> = {}, partnerAccess = true) {
    const record = { ...truck, ...over };
    return fakeWeb()
        .on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities: [
            "fleet:view_trucks", "fleet:view_assignments", "drivers:view", ...(partnerAccess ? ["fleet:manage_subcontractors"] : []),
        ] }) }))
        .on("GET", "/api/go/v1/trucks", () => json(200, { data: [record], meta: { statusCounts: { maintenance: 1 } } }))
        .on("GET", "/api/go/v1/trucks/truck-1", () => json(200, { data: record }))
        .on("GET", "/api/go/v1/truck-assignments", () => json(200, { data: [] }))
        .on("GET", "/api/go/v1/subcontractors", () => json(200, { data: [{ id: "partner-1", nameTh: "รถร่วม", nameEn: "Partner" }] }))
        .on("GET", "/api/go/v1/drivers", () => json(200, { data: [], meta: { statusCounts: {} } }));
}

afterEach(() => {
    resetQueryClientForTests();
    vi.unstubAllGlobals();
});

describe("UI4 fleet actions", () => {
    it("removes Export while preserving working Import and Add actions", async () => {
        localStorage.setItem("language", "en");
        serve();
        renderWithProviders(<TrucksListDashboard />);
        await screen.findByText("70-1234");
        expect(screen.queryByRole("button", { name: /export/i })).toBeNull();
        expect(screen.getByRole("link", { name: /add truck/i })).toHaveAttribute("href", "/app/trucks/new");
        expect(screen.getAllByText("Maintenance", { selector: "[data-slot=badge]" }).every((badge) => badge.classList.contains("bg-warning"))).toBe(true);
        await userEvent.setup().click(screen.getByRole("button", { name: /import trucks/i }));
        expect(await screen.findByRole("dialog", { name: /import trucks/i })).toBeInTheDocument();
    });

    it.each([
        { ownershipType: "subcontractor", tenantId: "partner-1", capability: true, visible: true },
        { ownershipType: "own", tenantId: "partner-1", capability: true, visible: false },
        { ownershipType: "subcontractor", tenantId: "", capability: true, visible: false },
        { ownershipType: "subcontractor", tenantId: "partner-1", capability: false, visible: false },
    ] as const)("partner profile requires subcontracted ownership, a tenant and route access ($ownershipType/$tenantId/$capability)", async ({ ownershipType, tenantId, capability, visible }) => {
        localStorage.setItem("language", "en");
        serve({ ownershipType, tenantId }, capability);
        renderWithProviders(<TruckPreview />);
        await screen.findByRole("heading", { level: 1, name: "70-1234" });
        const link = screen.queryByRole("link", { name: /view partner profile/i });
        expect(Boolean(link)).toBe(visible);
        if (visible) {
            expect(link).toHaveAttribute("href", "/app/subcontractors/partner-1");
            expect(link).toHaveClass("max-md:min-h-11", "max-md:min-w-11");
        }
        expect(screen.getByText("Maintenance", { selector: "[data-slot=badge]" })).toHaveClass("bg-warning");
    });

    it.each([
        { type: "1", en: "Class 1", th: "ประกันชั้น 1" },
        { type: "2", en: "Class 2", th: "ประกันชั้น 2" },
        { type: "2+", en: "Class 2+", th: "ประกันชั้น 2+" },
        { type: "3", en: "Class 3", th: "ประกันชั้น 3" },
        { type: "3+", en: "Class 3+", th: "ประกันชั้น 3+" },
    ])("localizes insurance class $type in English and Thai", async ({ type, en, th }) => {
        localStorage.setItem("language", "en");
        serve({ insuranceType: type });
        renderWithProviders(<><LanguageToggle /><TruckPreview /></>);
        expect(await screen.findByText(en)).toBeInTheDocument();
        await userEvent.setup().click(screen.getByRole("button", { name: "Use Thai" }));
        expect(await screen.findByText(th)).toBeInTheDocument();
        expect(screen.queryByText(/trucks\.insurance\.type\./)).toBeNull();
    });

    it.each([12345, 12345.5])("keeps premium %s free of forced decimals with a localized unit", async (premium) => {
        localStorage.setItem("language", "en");
        serve({ insurancePremiumThb: premium });
        renderWithProviders(<><LanguageToggle /><TruckPreview /></>);
        const amount = premium.toLocaleString("en-US");
        expect(await screen.findByText(`${amount} THB`)).toBeInTheDocument();
        await userEvent.setup().click(screen.getByRole("button", { name: "Use Thai" }));
        expect(await screen.findByText(`${amount} บาท`)).toBeInTheDocument();
    });

    it.each(["en", "th"])("localizes all missing import fields in %s while keeping spreadsheet columns unchanged", async (language) => {
        localStorage.setItem("language", "en");
        const web = serve().on("POST", "/api/go/v1/trucks/import", () => json(200, { created: 1, updated: 0, errors: [] }));
        renderWithProviders(<><LanguageToggle /><TrucksListDashboard /></>);
        const user = userEvent.setup();
        if (language === "th") await user.click(await screen.findByRole("button", { name: "Use Thai" }));
        await user.click(await screen.findByRole("button", { name: language === "th" ? "นำเข้าข้อมูลรถ" : "Import Trucks" }));
        const dialog = await screen.findByRole("dialog");
        const columns = ["License Plate", "Province", "Type", "Brand", "Model"];
        await user.upload(within(dialog).getByLabelText(language === "th" ? "เลือกไฟล์" : "Select File"), new File([
            `${columns.join(",")},Color\n,,,,,Red\n70-5678,Bangkok,6W,Isuzu,FRR,White`,
        ], "trucks.csv", { type: "text/csv" }));
        await within(dialog).findByText("70-5678");
        const invalid = dialog.querySelector<SVGElement>("[data-slot=tooltip-trigger]")!;
        await user.hover(invalid);
        const tooltip = await screen.findByRole("tooltip");
        for (const column of columns) expect(tooltip).toHaveTextContent(`${language === "th" ? "ไม่มีข้อมูล" : "Missing"} ${column}`);
        if (language === "th") expect(tooltip).not.toHaveTextContent("Missing");
        await user.click(within(dialog).getByRole("button", { name: language === "th" ? "ยืนยันการนำเข้า" : "Confirm Import" }));
        await waitFor(() => expect(web.count("POST", "/api/go/v1/trucks/import")).toBe(1));
        expect(web.calls.find((call) => call.method === "POST")?.body).toEqual({ rows: [
            { Color: "Red" },
            { "License Plate": "70-5678", Province: "Bangkok", Type: "6W", Brand: "Isuzu", Model: "FRR", Color: "White" },
        ] });
    });

    it.each([
        { nextServiceMileage: 9000, currentMileage: 10000, nextServiceDate: null, label: "PM: เกินกำหนด 1000 กม." },
        { nextServiceMileage: 11000, currentMileage: 10000, nextServiceDate: null, label: "PM: เหลือ 1000 กม." },
        { nextServiceMileage: null, currentMileage: 10000, nextServiceDate: new Date(Date.now() + 10 * 86400000).toISOString(), label: "PM: เหลือ 10 วัน" },
    ])("keeps Thai maintenance labels consistent: $label", async ({ label, ...over }) => {
        localStorage.setItem("language", "en");
        serve({ ...over, status: "active" });
        renderWithProviders(<><LanguageToggle /><TrucksListDashboard /></>);
        await userEvent.setup().click(await screen.findByRole("button", { name: "Use Thai" }));
        expect(await screen.findByText(label)).toBeInTheDocument();
    });

    it("localizes the four absent documentation values", async () => {
        localStorage.setItem("language", "en");
        serve();
        renderWithProviders(<><LanguageToggle /><DocumentationSummary /></>);
        expect(await screen.findAllByText("Not specified")).toHaveLength(4);
        await userEvent.setup().click(screen.getByRole("button", { name: "Use Thai" }));
        expect(await screen.findAllByText("ไม่ระบุ")).toHaveLength(4);
        expect(screen.queryByText("N/A")).toBeNull();
    });

    it("keeps computed PM counts and removes the fabricated zero PM tiles", async () => {
        localStorage.setItem("language", "en");
        serve({ ownershipType: "own", nextServiceMileage: 1000 });
        renderWithProviders(<TruckComplianceCards onFilterChange={vi.fn()} />);
        const due = (await screen.findByText("PM Soon")).closest("button")!;
        expect(within(due).getByText("1")).toHaveClass("bg-warning");
        expect(screen.queryByText("PM", { exact: true })).toBeNull();
        expect(screen.getAllByRole("button", { name: /Corrective/ }).every((button) => button.querySelector("[data-slot=badge]")?.classList.contains("bg-warning"))).toBe(true);
    });

    it("shows real assignment counters without trend claims or Download CSV", async () => {
        localStorage.setItem("language", "en");
        serve();
        renderWithProviders(<TruckAssignmentPage />);
        await screen.findByRole("heading", { name: "Assignment History" });
        expect(screen.queryByRole("button", { name: /download csv/i })).toBeNull();
        expect(screen.queryByText(/~\+5%|↘-2%|~\+12%/)).toBeNull();
        expect(screen.getByRole("button", { name: "Confirm and Assign" })).toBeInTheDocument();
    });
});
