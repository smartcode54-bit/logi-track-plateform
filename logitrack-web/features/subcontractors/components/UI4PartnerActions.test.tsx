import React from "react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useQuery } from "@tanstack/react-query";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { useLanguage } from "@/context/language";
import { queryKeys } from "@/lib/queryKeys";
import { goFetch } from "@/lib/goFetch";
import { useSubcontractorList } from "../api/useSubcontractors";
import type { SubcontractorDTO, TenantDocumentDTO } from "../api/subcontractors";
import SubcontractorDetail from "./SubcontractorDetail";
import SubcontractorEdit from "./SubcontractorEdit";
import { SubcontractorTable } from "./SubcontractorTable";
import NewSubcontractorWizard from "./form/NewSubcontractorWizard";

const { push } = vi.hoisted(() => ({ push: vi.fn() }));
vi.mock("next/navigation", () => ({
    useParams: () => ({ id: "carrier-1" }),
    usePathname: () => "/app/subcontractors/carrier-1",
    useRouter: () => ({ push, replace: vi.fn(), back: vi.fn() }),
}));
vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

function RenderInThai({ children }: { children: React.ReactNode }) {
    const { setLanguage } = useLanguage();
    React.useEffect(() => setLanguage("th"), [setLanguage]);
    return <>{children}</>;
}

const carrier = (over: Partial<SubcontractorDTO> = {}): SubcontractorDTO => ({
    id: "carrier-1", code: "PARTNER", nameTh: "Partner record", nameEn: null, legalType: null,
    status: "pending", contactPerson: null, phone: null, email: null, fleetSize: 4,
    lineGroupId: null, contractorTenantId: null, billingPartyId: null, billingDateBasis: null,
    createdAt: null, taxId: null, website: null, address: null, designation: null,
    dispatchCenter: null, serviceRegions: [], vehicleTypes: [], updatedAt: null, documents: [], ...over,
});
const document = (url: string | null): TenantDocumentDTO => ({ fileId: "file-1", key: "private/document", url, kind: "company_doc", position: 0 });

function setup(record = carrier(), capabilities = ["fleet:manage_subcontractors"]) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities }) }));
    web.on("GET", "/api/go/v1/subcontractors/carrier-1", () => json(200, { data: record }));
    web.on("GET", "/api/go/v1/trucks", () => json(200, { data: [] }));
    return web;
}

beforeAll(() => { Element.prototype.scrollIntoView = vi.fn(); window.scrollTo = vi.fn(); });
beforeEach(() => { localStorage.setItem("language", "en"); push.mockReset(); });
afterEach(() => vi.unstubAllGlobals());

describe("partner profile records (English)", () => {
    it("shows an honest empty state and no invented profile or portal content", async () => {
        setup();
        const { container } = renderWithProviders(<SubcontractorDetail />);
        expect(await screen.findByText("No documents have been added.")).toBeVisible();
        expect(screen.getByText("Pending")).toHaveAttribute("data-slot", "badge");
        expect(screen.getByRole("heading", { name: "Fleet preview (0 shown)" })).toBeVisible();
        expect(screen.getByRole("link", { name: "Manage documents" })).toHaveAttribute("href", "/app/subcontractors/carrier-1/edit");
        expect(container.querySelector('a[href^="mailto:"]')).toBeNull();
        expect(container.querySelector('a[href="#"]')).toBeNull();
        expect(container.textContent).not.toMatch(/XX-XXXX882|Chicago|email@example.com|08:00|CST|Bangkok, TH|Master_Service|COI_Insurance|W9_Tax|DISPATCH MANAGER|Partner portal|Contracts|Performance|Activity log|View all fleet|↑|↓/i);
    });

    it.each([null, "javascript:alert(1)"])("shows unavailable documents without an open link (%s)", async (url) => {
        setup(carrier({ documents: [document(url)] }));
        renderWithProviders(<SubcontractorDetail />);
        expect(await screen.findByText("Document unavailable")).toBeVisible();
        expect(screen.queryByRole("link", { name: /Open document/ })).toBeNull();
    });

    it("uses the translated generic label for other and unknown document kinds in Thai", async () => {
        const documents = [
            { ...document("https://files.test/other"), fileId: "other", kind: "other", position: 0 },
            { ...document("https://files.test/future"), fileId: "future", kind: "future_kind", position: 1 },
        ];
        setup(carrier({ documents }));
        const { container } = renderWithProviders(<RenderInThai><SubcontractorDetail /></RenderInThai>);
        expect(await screen.findAllByText("เอกสาร", { exact: true })).toHaveLength(3);
        expect(screen.getByRole("link", { name: "เปิดเอกสารลำดับที่ 1" })).toBeVisible();
        expect(screen.getByRole("link", { name: "เปิดเอกสารลำดับที่ 2" })).toBeVisible();
        expect(container.textContent).not.toMatch(/other|future_kind/);
    });

    it("opens actual documents safely and renders stored contact and service data", async () => {
        setup(carrier({ status: "suspended", email: "contact@partner.test", phone: "0812345678", designation: "Fleet coordinator",
            dispatchCenter: "Chiang Mai depot", serviceRegions: ["Chiang Mai", "Lamphun"],
            documents: [document("https://files.test/document?signature=short-lived")] }));
        const { container } = renderWithProviders(<SubcontractorDetail />);
        const open = await screen.findByRole("link", { name: "Open document 1" });
        expect(open).toHaveAttribute("href", "https://files.test/document?signature=short-lived");
        expect(open).toHaveAttribute("target", "_blank");
        expect(open).toHaveAttribute("rel", "noopener noreferrer");
        expect(open).not.toHaveAttribute("download");
        expect(screen.getByText("Suspended")).toBeVisible();
        expect(screen.getByText("Fleet coordinator")).toBeVisible();
        expect(screen.getByText("Chiang Mai depot")).toBeVisible();
        expect(screen.getByText("Chiang Mai, Lamphun")).toBeVisible();
        expect(container.querySelector('a[href="mailto:contact@partner.test"]')).toBeInTheDocument();
        expect(container.querySelector('a[href="tel:0812345678"]')).toBeInTheDocument();
        expect(container.textContent).not.toMatch(/Download|Verified|Renew|Expires|Audit/);
    });

    it("omits edit links when the current route map denies editing", async () => {
        setup(carrier(), []);
        renderWithProviders(<SubcontractorDetail />);
        await screen.findByText("No documents have been added.");
        expect(screen.queryByRole("link", { name: /Manage documents|Edit profile/ })).toBeNull();
    });

    it.each([
        { legalType: "individual" as const, label: "ID card number" },
        { legalType: "company" as const, label: "Tax ID / commercial registration" },
    ])("marks the required $legalType identifier in the edit form", async ({ legalType, label }) => {
        setup(carrier({ legalType }));
        renderWithProviders(<SubcontractorEdit />);
        expect(await screen.findByRole("textbox", { name: label })).toHaveAttribute("aria-required", "true");
    });

    it("labels only the three trucks actually shown as a preview", async () => {
        const web = setup();
        web.handlers.unshift((call) => call.url.startsWith("/api/go/v1/trucks") ? json(200, { data: Array.from({ length: 4 }, (_, i) => ({ id: `truck-${i}`, type: "6W", licensePlate: `TEST-${i}`, status: "active" })), nextCursor: "more" }) : undefined);
        renderWithProviders(<SubcontractorDetail />);
        expect(await screen.findByRole("heading", { name: "Fleet preview (3 shown)" })).toBeVisible();
        expect(screen.getAllByRole("row")).toHaveLength(4);
    });
});

function PartnerList() {
    const { rows } = useSubcontractorList();
    useQuery({ queryKey: queryKeys.tenants.list({ kind: "carrier" }), queryFn: () => goFetch("/v1/tenants") });
    return <SubcontractorTable subcontractors={rows} loading={false} currentPage={1} setCurrentPage={vi.fn()} itemsPerPage={10} filteredCount={rows.length} sortConfig={null} handleSort={vi.fn()} />;
}

function setupList(capabilities = ["platform:manage_tenants"], status = "active") {
    const web = setup(carrier(), capabilities);
    let current = carrier({ status });
    web.on("GET", "/api/go/v1/subcontractors", () => json(200, { data: [current] }));
    web.on("GET", "/api/go/v1/tenants", () => json(200, { data: [current] }));
    return { web, suspended: () => { current = { ...current, status: "suspended" }; } };
}

async function openActions(u: ReturnType<typeof userEvent.setup>) {
    await u.click(await screen.findByRole("button", { name: "Actions for Partner record" }));
}

describe("platform-only partner suspension (English)", () => {
    it.each([{ capabilities: ["fleet:manage_subcontractors"] }, { capabilities: [] }])("hides suspension without the platform capability ($capabilities)", async ({ capabilities }) => {
        setupList(capabilities);
        renderWithProviders(<PartnerList />);
        await openActions(userEvent.setup());
        expect(screen.queryByRole("menuitem", { name: /Suspend/ })).toBeNull();
        expect(screen.queryByRole("menuitem", { name: /Contracts/ })).toBeNull();
    });

    it("hides suspension for already suspended partners", async () => {
        setupList(["platform:manage_tenants"], "suspended");
        renderWithProviders(<PartnerList />);
        await openActions(userEvent.setup());
        expect(screen.queryByRole("menuitem", { name: /Suspend/ })).toBeNull();
    });

    it("cancel sends zero requests; confirm sends one status PATCH and refetches both lists", async () => {
        const { web, suspended } = setupList();
        web.on("PATCH", "/api/go/v1/tenants/carrier-1", () => { suspended(); return json(200, { data: carrier({ status: "suspended" }) }); });
        const { client } = renderWithProviders(<PartnerList />);
        client.setQueryData(queryKeys.subcontractors.detail("carrier-1"), carrier());
        const u = userEvent.setup();
        await openActions(u);
        await u.click(screen.getByRole("menuitem", { name: "Suspend partner" }));
        let dialog = await screen.findByRole("alertdialog", { name: "Suspend this partner?" });
        expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus();
        expect(dialog).toHaveTextContent("immediately ends access for all of its active users");
        expect(screen.getByText("Code: PARTNER")).toBeVisible();
        expect(screen.getByText("P", { exact: true })).toBeVisible();
        expect(screen.queryByText(/ID:/)).toBeNull();
        await u.click(within(dialog).getByRole("button", { name: "Cancel" }));
        expect(screen.getByRole("button", { name: "Actions for Partner record" })).toHaveFocus();
        expect(web.calls.filter((c) => c.method === "PATCH")).toHaveLength(0);
        await openActions(u);
        await u.click(screen.getByRole("menuitem", { name: "Suspend partner" }));
        dialog = await screen.findByRole("alertdialog");
        await u.click(within(dialog).getByRole("button", { name: "Suspend partner" }));
        expect(await screen.findByText("Partner suspended")).toBeVisible();
        expect(web.calls.filter((c) => c.method === "PATCH")).toEqual([expect.objectContaining({ url: "/api/go/v1/tenants/carrier-1", body: { status: "suspended" } })]);
        expect(web.count("GET", "/api/go/v1/subcontractors")).toBe(2);
        expect(web.count("GET", "/api/go/v1/tenants")).toBe(2);
        expect(client.getQueryState(queryKeys.subcontractors.detail("carrier-1"))?.isInvalidated).toBe(true);
        await openActions(u);
        expect(screen.queryByRole("menuitem", { name: /Suspend/ })).toBeNull();
    });

    it("disables confirmation while pending and keeps API denials in the shared error alert", async () => {
        const { web } = setupList();
        let reply!: (value: Response) => void;
        web.on("PATCH", "/api/go/v1/tenants/carrier-1", () => new Promise((resolve) => { reply = resolve; }));
        renderWithProviders(<PartnerList />);
        const u = userEvent.setup();
        await openActions(u);
        await u.click(screen.getByRole("menuitem", { name: "Suspend partner" }));
        const dialog = await screen.findByRole("alertdialog");
        await u.click(within(dialog).getByRole("button", { name: "Suspend partner" }));
        expect(within(dialog).getByRole("button", { name: "Saving..." })).toBeDisabled();
        expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled();
        reply(goErr(403, "permission_denied"));
        expect(await within(dialog).findByRole("alert")).toHaveTextContent("Could not suspend partner");
        expect(within(dialog).getByRole("alert").textContent).not.toMatch(/apiError\.|permission_denied/);
        expect(web.calls.filter((c) => c.method === "PATCH")).toHaveLength(1);
        expect(web.count("GET", "/api/go/v1/subcontractors")).toBe(1);
    });

    it("moves focus to the table when filtering removes the invoking row", async () => {
        setupList();
        const view = renderWithProviders(<PartnerList />);
        const u = userEvent.setup();
        await openActions(u);
        await u.click(screen.getByRole("menuitem", { name: "Suspend partner" }));
        await screen.findByRole("alertdialog");
        view.client.setQueryData(queryKeys.subcontractors.list({ view: "full" }), []);
        await waitFor(() => expect(screen.queryByRole("button", { name: "Actions for Partner record" })).toBeNull());
        await u.click(screen.getByRole("button", { name: "Cancel" }));
        await waitFor(() => expect(view.container.querySelector('[tabindex="-1"]')).toHaveFocus());
    });
});

async function wizardToStep3() {
    const u = userEvent.setup();
    await u.type(await screen.findByLabelText(/Legal business name/i), "Created partner");
    await u.type(screen.getByLabelText(/National ID number/i), "1234567890121");
    await u.click(screen.getByRole("button", { name: "Next step" }));
    await u.type(await screen.findByLabelText(/Full name/i), "Real contact");
    await u.type(screen.getByLabelText(/Mobile phone number/i), "0812345678");
    await u.click(screen.getByRole("button", { name: "Next step" }));
    return u;
}

async function reviewWizard() {
    const u = await wizardToStep3();
    await u.clear(await screen.findByLabelText(/Total available trucks/i));
    await u.type(screen.getByLabelText(/Total available trucks/i), "7");
    await u.type(screen.getByLabelText(/Primary dispatch center/i), "Lamphun depot");
    await u.type(screen.getByLabelText(/Service provinces & regions/i), "Chiang Mai, Lamphun");
    await u.click(screen.getByRole("button", { name: "Next step" }));
    return u;
}

describe("partner registration review (English)", () => {
    it("sends no document keys, reviews actual regions and redirects to the returned id", async () => {
        const web = setup();
        web.on("POST", "/api/go/v1/subcontractors", () => json(201, { data: carrier({ id: "returned-id" }) }));
        const { container } = renderWithProviders(<NewSubcontractorWizard />);
        const u = await reviewWizard();
        expect(screen.getByRole("button", { name: "Edit company details" })).toBeVisible();
        expect(screen.getByRole("button", { name: "Edit contact details" })).toBeVisible();
        expect(screen.getByRole("button", { name: "Edit operational details" })).toBeVisible();
        expect(screen.getByText("Chiang Mai, Lamphun")).toBeVisible();
        expect(screen.getAllByText("After registering, add documents on the partner edit page.").length).toBeGreaterThan(0);
        expect(container.textContent).not.toMatch(/insurance_cert_2024|Midwest|Northeast|California|Texas|Florida|New York|Add 12 more|Drag and drop/);
        expect(container.querySelector('input[type="file"]')).toBeNull();
        await u.click(screen.getByRole("button", { name: "Complete registration" }));
        await waitFor(() => expect(push).toHaveBeenCalledWith("/app/subcontractors/returned-id/edit"));
        const calls = web.calls.filter((c) => c.method === "POST");
        expect(calls).toHaveLength(1);
        expect(calls[0].body).toMatchObject({ serviceRegions: ["Chiang Mai", "Lamphun"], dispatchCenter: "Lamphun depot", fleetSize: 7 });
        expect(Object.keys(calls[0].body as object).some((key) => /document/i.test(key))).toBe(false);
    });

    it("retains server field errors and does not redirect when creation is denied", async () => {
        const web = setup();
        web.on("POST", "/api/go/v1/subcontractors", () => goErr(422, "invalid_argument", { fields: [{ field: "nameTh", reason: "taken", params: {} }] }));
        renderWithProviders(<NewSubcontractorWizard />);
        const u = await reviewWizard();
        await u.click(screen.getByRole("button", { name: "Complete registration" }));
        await waitFor(() => expect(web.count("POST", "/api/go/v1/subcontractors")).toBe(1));
        await u.click(screen.getByRole("button", { name: "Edit company details" }));
        expect(await screen.findByText(/already in use/i)).toBeVisible();
        expect(push).not.toHaveBeenCalled();
    });

    it("rejects fractional fleet sizes before advancing and before registration", async () => {
        const web = setup();
        renderWithProviders(<NewSubcontractorWizard />);
        const u = await wizardToStep3();
        const fleetSize = await screen.findByLabelText(/Total available trucks/i);
        await u.clear(fleetSize);
        await u.type(fleetSize, "2.5");
        await u.click(screen.getByRole("button", { name: "Next step" }));
        expect(await screen.findByText("Enter a whole number of trucks greater than or equal to zero.")).toBeVisible();
        fireEvent.submit(fleetSize.closest("form")!);
        expect(web.calls.filter((call) => call.method === "POST")).toHaveLength(0);
    });

    it("keeps raw service-region text editable and parses it on the next step", async () => {
        renderWithProviders(<NewSubcontractorWizard />);
        const u = await wizardToStep3();
        const regions = await screen.findByLabelText("Service provinces & regions") as HTMLInputElement;
        expect(screen.getByText("Separate provinces or regions with commas")).toBeVisible();
        await u.type(regions, "Chiang Mai, ");
        expect(regions).toHaveValue("Chiang Mai, ");
        await u.keyboard("{Backspace}");
        expect(regions).toHaveValue("Chiang Mai,");
        await u.keyboard("{Backspace}");
        expect(regions).toHaveValue("Chiang Mai");
        await u.clear(regions);
        await u.type(regions, "A,B");
        regions.setSelectionRange(1, 1);
        await u.keyboard("x");
        expect(regions).toHaveValue("Ax,B");
        expect(regions.selectionStart).toBe(2);
        await u.click(screen.getByRole("button", { name: "Next step" }));
        expect(screen.getByText("Ax, B")).toBeVisible();
    });
});
