import React from "react";
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { createQueryClient } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { useLanguage } from "@/context/language";
import type { SubcontractorDTO } from "../api/subcontractors";
import FileUploader from "./FileUploader";
import SubcontractorEdit from "./SubcontractorEdit";
import SubcontractorDetail from "./SubcontractorDetail";
import SubcontractorsListDashboard from "./SubcontractorsListDashboard";

const { push, back } = vi.hoisted(() => ({ push: vi.fn(), back: vi.fn() }));
vi.mock("next/navigation", () => ({
    useParams: () => ({ id: "carrier-1" }),
    usePathname: () => "/app/subcontractors/carrier-1",
    useRouter: () => ({ push, back, replace: vi.fn() }),
}));
vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

const carrier = (over: Partial<SubcontractorDTO> = {}): SubcontractorDTO => ({
    id: "carrier-1", code: "PARTNER", nameTh: "Partner 1", nameEn: null, legalType: "company",
    status: "active", contactPerson: "Contact", phone: "0812345678", email: null, fleetSize: 20,
    lineGroupId: null, contractorTenantId: null, billingPartyId: null, billingDateBasis: "delivered",
    createdAt: null, taxId: "0105536000313", website: null, address: null, designation: null,
    dispatchCenter: null, serviceRegions: ["Chiang Mai", "Lamphun"], vehicleTypes: [], updatedAt: null, documents: [], ...over,
});

function setup() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities: ["fleet:manage_subcontractors"] }) }));
    const client = createQueryClient();
    client.setDefaultOptions({ queries: { retry: false } });
    return { web, client };
}

function Language({ language, children }: { language: "en" | "th"; children: React.ReactNode }) {
    const { setLanguage } = useLanguage();
    React.useEffect(() => setLanguage(language), [language, setLanguage]);
    return <>{children}</>;
}

beforeEach(() => { push.mockReset(); back.mockReset(); });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe("partner list filters and pagination", () => {
    async function select(label: string, option: string) {
        const u = userEvent.setup();
        await u.click(screen.getByRole("combobox", { name: label }));
        await u.click(await screen.findByRole("option", { name: option }));
    }

    it.each([{ regions: [] }, { regions: null }, { regions: undefined }])("offers each stored region and matches unspecified rows ($regions)", async ({ regions }) => {
        const { web, client } = setup();
        web.on("GET", "/api/go/v1/subcontractors", () => json(200, { data: [carrier(), { ...carrier({ id: "no-region", nameTh: "No region" }), serviceRegions: regions }] }));
        renderWithProviders(<SubcontractorsListDashboard />, client);
        expect(await screen.findByText("Chiang Mai, Lamphun")).toBeVisible();
        await select("Service region", "Lamphun");
        expect(screen.getByText("Partner 1")).toBeVisible();
        expect(screen.queryByText("No region")).toBeNull();
        await select("Service region", "Not specified");
        expect(screen.getByText("No region")).toBeVisible();
        expect(screen.queryByText("Partner 1")).toBeNull();
        expect(screen.queryByText("Unknown")).toBeNull();
    });

    it("keeps filter trigger names tied to their fields and uses phone-sized controls", async () => {
        const { web, client } = setup();
        web.on("GET", "/api/go/v1/subcontractors", () => json(200, { data: [carrier()] }));
        renderWithProviders(<SubcontractorsListDashboard />, client);
        await screen.findByText("Partner 1");

        const search = screen.getByRole("textbox", { name: "Search companies or contacts..." });
        const status = screen.getByRole("combobox", { name: "Partner status" });
        const region = screen.getByRole("combobox", { name: "Service region" });
        const fleetSize = screen.getByRole("combobox", { name: "Fleet size" });
        expect(search).toHaveClass("max-md:h-11");
        expect(status).toHaveClass("max-md:h-11");
        expect(region).toHaveClass("max-md:h-11");
        expect(fleetSize).toHaveClass("max-md:h-11");

        await select("Partner status", "Active");
        expect(screen.getByRole("combobox", { name: "Partner status" })).toBeVisible();
        await select("Service region", "Lamphun");
        expect(screen.getByRole("combobox", { name: "Service region" })).toBeVisible();
    });

    it.each(["search", "status", "region", "fleet size"])("resets page 2 when %s changes, even when many results remain", async (filter) => {
        const { web, client } = setup();
        const rows = Array.from({ length: 25 }, (_, i) => carrier({ id: `carrier-${i + 1}`, nameTh: `Partner ${i + 1}` }));
        web.on("GET", "/api/go/v1/subcontractors", () => json(200, { data: rows }));
        renderWithProviders(<SubcontractorsListDashboard />, client);
        await screen.findByText("Partner 1");
        await userEvent.click(screen.getByRole("button", { name: "Next" }));
        expect(screen.getByText("Partner 11")).toBeVisible();
        if (filter === "search") await userEvent.type(screen.getByRole("textbox", { name: "Search companies or contacts..." }), "Partner");
        if (filter === "status") await select("Partner status", "Active");
        if (filter === "region") await select("Service region", "Lamphun");
        if (filter === "fleet size") await select("Fleet size", "Medium (20-50)");
        expect(screen.getByText("Partner 1")).toBeVisible();
        expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled();
    });

    it("clamps the page after a refetch and keeps it clamped when data grows again", async () => {
        const { web, client } = setup();
        const rows = Array.from({ length: 25 }, (_, i) => carrier({ id: `carrier-${i + 1}`, nameTh: `Partner ${i + 1}` }));
        web.on("GET", "/api/go/v1/subcontractors", () => json(200, { data: rows }));
        renderWithProviders(<SubcontractorsListDashboard />, client);
        await screen.findByText("Partner 1");
        await userEvent.click(screen.getByRole("button", { name: "Next" }));
        await userEvent.click(screen.getByRole("button", { name: "Next" }));
        act(() => client.setQueryData(queryKeys.subcontractors.list({ view: "full" }), rows.slice(0, 12)));
        await waitFor(() => expect(screen.getByText("Partner 11")).toBeVisible());
        expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();
        act(() => client.setQueryData(queryKeys.subcontractors.list({ view: "full" }), rows));
        await waitFor(() => expect(screen.getByText("Partner 11")).toBeVisible());
        act(() => client.setQueryData(queryKeys.subcontractors.list({ view: "full" }), []));
        await waitFor(() => expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled());
    });

    it("puts 19, 20, 50 and 51 trucks in the groups named by the labels", async () => {
        const { web, client } = setup();
        web.on("GET", "/api/go/v1/subcontractors", () => json(200, { data: [19, 20, 50, 51].map((n) => carrier({ id: `${n}`, nameTh: `Fleet ${n}`, fleetSize: n })) }));
        renderWithProviders(<SubcontractorsListDashboard />, client);
        await screen.findByText("Fleet 19");
        await select("Fleet size", "Small (<20)");
        expect(screen.getByText("Fleet 19")).toBeVisible();
        expect(screen.queryByText("Fleet 20")).toBeNull();
        await select("Fleet size", "Medium (20-50)");
        expect(screen.getByText("Fleet 20")).toBeVisible();
        expect(screen.getByText("Fleet 50")).toBeVisible();
        expect(screen.queryByText("Fleet 51")).toBeNull();
        await select("Fleet size", "Large (>50)");
        expect(screen.getByText("Fleet 51")).toBeVisible();
        expect(screen.queryByText("Fleet 50")).toBeNull();
    });
});

describe("partner uploads", () => {
    it("gives the remove action a phone-sized target", async () => {
        const { client } = setup();
        renderWithProviders(<Language language="en"><FileUploader label="Documents" currentUrls={["https://files.example.test/document.pdf"]} upload={vi.fn()} onUploadComplete={vi.fn()} onRemove={vi.fn()} /></Language>, client);
        expect(await screen.findByRole("button", { name: "Remove file" })).toHaveClass("max-md:size-11");
    });

    it.each(["en", "th"] as const)("shows an unavailable document without linking its raw key (%s)", async (language) => {
        const { client } = setup();
        const { container } = renderWithProviders(<Language language={language}><FileUploader label="Documents" currentUrls={["subcontractors/carrier-1/company_docs/missing.pdf"]} upload={vi.fn()} onUploadComplete={vi.fn()} /></Language>, client);
        expect(await screen.findByText(language === "th" ? "ไม่สามารถเปิดเอกสารได้" : "Document unavailable")).toBeVisible();
        expect(container.querySelector("a")).toBeNull();
        expect(container.querySelector("img")).toBeNull();
    });

    it("keeps an unavailable stored document in the edit form without using its key as a link", async () => {
        const { web, client } = setup();
        web.on("GET", "/api/go/v1/subcontractors/carrier-1", () => json(200, { data: carrier({ documents: [{ fileId: "missing", key: "private/missing.pdf", url: null, kind: "company_doc", position: 0 }] }) }));
        web.on("PATCH", "/api/go/v1/subcontractors/carrier-1", () => json(200, { data: carrier() }));
        const { container } = renderWithProviders(<SubcontractorEdit />, client);
        expect(await screen.findByText("Document unavailable")).toBeVisible();
        expect(container.querySelector('a[href="private/missing.pdf"]')).toBeNull();
        await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
        await waitFor(() => expect(push).toHaveBeenCalled());
        expect(web.calls.find((call) => call.method === "PATCH")?.body).toMatchObject({ documentKeys: ["private/missing.pdf"] });
    });

    it("unlocks Save after an upload failure without adding an unattached document", async () => {
        const { web, client } = setup();
        web.on("GET", "/api/go/v1/subcontractors/carrier-1", () => json(200, { data: carrier() }));
        web.on("POST", "/api/go/v1/uploads/presign", () => goErr(503, "unavailable"));
        vi.spyOn(console, "error").mockImplementation(() => {});
        renderWithProviders(<SubcontractorEdit />, client);
        const input = await screen.findByLabelText("Company documents (PP20, registration)");
        await userEvent.upload(input, new File(["pdf"], "report.pdf", { type: "application/pdf" }));
        await waitFor(() => expect(web.count("POST", "/api/go/v1/uploads/presign")).toBe(1));
        await waitFor(() => expect(screen.getByRole("button", { name: "Save changes" })).toBeEnabled());
        expect(screen.queryByRole("link", { name: "Open document 1" })).toBeNull();
        expect(push).not.toHaveBeenCalled();
    });

    it("keeps Save and Cancel disabled until PUT finishes, then saves the key before navigating; a PDF stays a tile", async () => {
        const { web, client } = setup();
        const key = "subcontractors/carrier-1/company_docs/report.pdf";
        web.on("GET", "/api/go/v1/subcontractors/carrier-1", () => json(200, { data: carrier() }));
        web.on("POST", "/api/go/v1/uploads/presign", () => json(200, { data: { key, url: "/v1/uploads/local/report", method: "PUT" } }));
        let finishUpload!: (response: Response) => void;
        web.on("PUT", "/api/go/v1/uploads/local/report", () => new Promise((resolve) => { finishUpload = resolve; }));
        let finishSave!: (response: Response) => void;
        web.on("PATCH", "/api/go/v1/subcontractors/carrier-1", () => new Promise((resolve) => { finishSave = resolve; }));
        vi.stubGlobal("URL", class extends URL {
            static createObjectURL = vi.fn(() => "blob:report");
            static revokeObjectURL = vi.fn();
        });
        const { container } = renderWithProviders(<SubcontractorEdit />, client);
        const input = await screen.findByLabelText("Company documents (PP20, registration)");
        await userEvent.upload(input, new File(["pdf"], "report.pdf", { type: "application/pdf" }));
        await waitFor(() => expect(web.count("PUT", "/api/go/v1/uploads/local/report")).toBe(1));
        expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
        expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
        expect(screen.getByRole("button", { name: "Save changes" })).toHaveClass("max-md:min-h-11");
        expect(screen.getByRole("button", { name: "Cancel" })).toHaveClass("max-md:min-h-11");
        fireEvent.submit(input.closest("form")!);
        await act(async () => {});
        expect(web.count("PATCH", "/api/go/v1/subcontractors/carrier-1")).toBe(0);
        expect(push).not.toHaveBeenCalled();
        await act(async () => finishUpload(new Response(null, { status: 200 })));
        await waitFor(() => expect(screen.getByRole("button", { name: "Save changes" })).toBeEnabled());
        expect(screen.getByRole("link", { name: "Open document 1" })).toHaveAttribute("href", "blob:report");
        expect(container.querySelector("img")).toBeNull();
        await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
        await waitFor(() => expect(web.count("PATCH", "/api/go/v1/subcontractors/carrier-1")).toBe(1));
        expect(web.calls.find((c) => c.method === "PATCH")?.body).toMatchObject({ documentKeys: [key] });
        expect(push).not.toHaveBeenCalled();
        await act(async () => finishSave(json(200, { data: carrier() })));
        await waitFor(() => expect(push).toHaveBeenCalledWith("/app/subcontractors/carrier-1"));
    });
});

describe("partner query states", () => {
    it.each(["en", "th"] as const)("shows localized list loading without empty counts (%s)", async (language) => {
        const { web, client } = setup();
        let finish!: (response: Response) => void;
        web.on("GET", "/api/go/v1/subcontractors", () => new Promise(resolve => { finish = resolve; }));
        renderWithProviders(<Language language={language}><SubcontractorsListDashboard /></Language>, client);
        expect(await screen.findByText(language === "th" ? "กำลังโหลดข้อมูลรถร่วม..." : "Loading partners...")).toBeVisible();
        expect(screen.queryByRole("table")).toBeNull();
        await act(async () => finish(json(200, { data: [carrier()] })));
        expect(await screen.findByText("Partner 1")).toBeVisible();
    });

    it.each(["en", "th"] as const)("shows a list error instead of zeros/empty results and retries (%s)", async (language) => {
        const { web, client } = setup();
        let fail = true;
        web.on("GET", "/api/go/v1/subcontractors", () => fail ? goErr(503, "unavailable") : json(200, { data: [carrier()] }));
        renderWithProviders(<Language language={language}><SubcontractorsListDashboard /></Language>, client);
        const retry = await screen.findByRole("button", { name: language === "th" ? "ลองอีกครั้ง" : "Try again" });
        expect(screen.getByText(language === "th" ? "ไม่สามารถโหลดรายชื่อรถร่วมได้" : "Could not load the partner list")).toBeVisible();
        expect(retry).toHaveClass("min-h-11", "md:min-h-9");
        expect(screen.queryByText(language === "th" ? "ไม่พบรถร่วมที่ตรงกับตัวกรอง" : "No partners match your filters.")).toBeNull();
        expect(screen.queryByText(language === "th" ? "รถร่วมทั้งหมด" : "Total partners")).toBeNull();
        fail = false;
        await userEvent.click(retry);
        expect(await screen.findByText("Partner 1")).toBeVisible();
    });

    it("shows a record error with retry when the partner cannot load", async () => {
        const { web, client } = setup();
        let fail = true;
        web.on("GET", "/api/go/v1/subcontractors/carrier-1", () => fail ? goErr(503, "unavailable") : json(200, { data: carrier() }));
        web.on("GET", "/api/go/v1/trucks", () => json(200, { data: [] }));
        renderWithProviders(<SubcontractorDetail />, client);
        const retry = await screen.findByRole("button", { name: "Try again" });
        expect(screen.getByText("Could not load this record")).toBeVisible();
        expect(screen.queryByText("No trucks assigned yet.")).toBeNull();
        fail = false;
        await userEvent.click(retry);
        expect(await screen.findByRole("heading", { name: "Partner 1" })).toBeVisible();
    });

    it.each(["en", "th"] as const)("shows the localized fleet error and retries without pretending there are no trucks (%s)", async (language) => {
        const { web, client } = setup();
        let fail = true;
        web.on("GET", "/api/go/v1/subcontractors/carrier-1", () => json(200, { data: carrier() }));
        web.on("GET", "/api/go/v1/trucks", () => fail ? goErr(503, "unavailable") : json(200, { data: [] }));
        renderWithProviders(<Language language={language}><SubcontractorDetail /></Language>, client);
        const retry = await screen.findByRole("button", { name: language === "th" ? "ลองอีกครั้ง" : "Try again" });
        expect(screen.getByText(language === "th" ? "ไม่สามารถโหลดข้อมูลรถในสังกัดได้" : "Could not load the partner fleet")).toBeVisible();
        expect(screen.queryByText(language === "th" ? "ยังไม่มีรถในสังกัด" : "No trucks assigned yet.")).toBeNull();
        fail = false;
        await userEvent.click(retry);
        expect(await screen.findByText(language === "th" ? "ยังไม่มีรถในสังกัด" : "No trucks assigned yet.")).toBeVisible();
    });
});

describe("partner form validation", () => {
    function SwitchLanguage() {
        const { setLanguage } = useLanguage();
        return <button onClick={() => setLanguage("th")}>Switch to Thai</button>;
    }

    it("translates existing errors immediately after switching language, including the shared invalid toast", async () => {
        const { web, client } = setup();
        web.on("GET", "/api/go/v1/subcontractors/carrier-1", () => json(200, { data: carrier() }));
        const errorToast = vi.spyOn(toast, "error");
        renderWithProviders(<><SubcontractorEdit /><SwitchLanguage /></>, client);
        await userEvent.clear(await screen.findByRole("textbox", { name: "Name / company name" }));
        await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
        expect(await screen.findByText("Enter the partner’s name.")).toBeVisible();
        await userEvent.click(screen.getByRole("button", { name: "Switch to Thai" }));
        expect(await screen.findByText("กรุณากรอกชื่อรถร่วมหรือชื่อบริษัท")).toBeVisible();
        expect(screen.queryByText("Enter the partner’s name.")).toBeNull();
        await userEvent.click(screen.getByRole("button", { name: "บันทึกการเปลี่ยนแปลง" }));
        await waitFor(() => expect(errorToast).toHaveBeenLastCalledWith(expect.stringContaining("กรุณากรอกชื่อรถร่วมหรือชื่อบริษัท")));
        expect(errorToast.mock.calls.flat().join(" ")).not.toContain("subcontractors.validation.");
    });

    it.each(["en", "th"] as const)("renders schema errors in the selected language (%s)", async (language) => {
        const { web, client } = setup();
        web.on("GET", "/api/go/v1/subcontractors/carrier-1", () => json(200, { data: carrier() }));
        renderWithProviders(<Language language={language}><SubcontractorEdit /></Language>, client);
        const input = await screen.findByRole("textbox", { name: language === "th" ? "ชื่อ / ชื่อบริษัท" : "Name / company name" });
        await userEvent.clear(input);
        await userEvent.click(screen.getByRole("button", { name: language === "th" ? "บันทึกการเปลี่ยนแปลง" : "Save changes" }));
        expect(await screen.findByText(language === "th" ? "กรุณากรอกชื่อรถร่วมหรือชื่อบริษัท" : "Enter the partner’s name.")).toBeVisible();
        expect(web.calls.filter(call => call.method === "PATCH")).toHaveLength(0);
        expect(push).not.toHaveBeenCalled();
    });
});
