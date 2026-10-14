// UI3D (#143): not found is not forbidden, errors stay in the shell, one page header. An address with no page reads
// "Page not found" inside the shell (sidebar, header, crumb); a page that crashes keeps the shell and offers a retry
// and a reference; a missing record says "Not found" (never whether it exists outside the reader's scope) or, when it
// could not be loaded, offers a retry; the 403 page is quiet and says who can help, by kind of principal; and the page
// header names a detail page by its record in the H1, the crumb and the tab title.
import React, { useEffect } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

let mockPathname = "/app/nope";
let mockSearch = new URLSearchParams();
vi.mock("next/navigation", () => ({
    usePathname: () => mockPathname,
    useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn(), back: vi.fn() }),
    useSearchParams: () => mockSearch,
    useParams: () => ({}),
}));
vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

import AdminLayout from "@/app/app/layout";
import AppNotFound from "@/app/app/not-found";
import AppError from "@/app/app/error";
import GlobalError from "@/app/global-error";
import UnauthorizedPage from "@/app/app/unauthorized/page";
import { PageHeader } from "@/components/page-header";
import { RecordState, recordUnavailable } from "@/components/record-state";
import { useLanguage } from "@/context/language";
import type { MeDTO } from "@/features/auth/api/me";
import CustomersList from "@/features/customers/components/CustomersList";
import TruckPreview from "@/features/trucks/components/TruckPreview";
import { ApiError } from "@/lib/apiError";
import { createQueryClient } from "@/lib/queryClient";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

const tenantAdmin = makeMe({
    tenant: { id: "t-wrt", nameTh: "วันเพ็ญ รัชดา", nameEn: "WRT", kind: "own_fleet", role: "tenant_admin" },
    tenants: [{ id: "t-wrt", nameTh: "วันเพ็ญ รัชดา", nameEn: "WRT", kind: "own_fleet", role: "tenant_admin", status: "active" }],
    capabilities: ["accounting:view_income", "fleet:view_trucks", "fleet:manage_customers"],
});
const manager = makeMe({
    displayName: "Mana",
    tenant: { id: "t-wrt", nameTh: "วันเพ็ญ รัชดา", nameEn: "WRT", kind: "own_fleet", role: "manager" },
    tenants: [],
    capabilities: ["fleet:view_trucks"],
});
const customer = makeMe({
    displayName: "Chai",
    tenant: null,
    tenants: [],
    steward: false,
    customerScopes: [{ billingPartyId: "bp-cjsf", name: "CJSF" }],
    capabilities: ["operations:view_driver_monitor"],
});
const partnerAdmin = makeMe({
    displayName: "Pat",
    tenant: { id: "t-car", nameTh: "รถร่วมหนึ่ง", nameEn: "Carrier One", kind: "carrier", role: "tenant_admin" },
    tenants: [],
    steward: false,
    capabilities: ["fleet:view_trucks", "operations:view_driver_monitor"],
});
const partnerStaff = makeMe({
    displayName: "Sam",
    tenant: { id: "t-car", nameTh: "รถร่วมหนึ่ง", nameEn: "Carrier One", kind: "carrier", role: "operation_staff" },
    tenants: [],
    steward: false,
    capabilities: ["fleet:view_trucks"],
});
const platformAdmin = makeMe({ displayName: "Gora", tenant: null, tenants: [], platformRoles: ["platform_admin"], capabilities: ["security:view_overview"] });
const driverOnly = makeMe({
    displayName: "Dan",
    tenant: { id: "t-wrt", nameTh: "วันเพ็ญ รัชดา", nameEn: "WRT", kind: "own_fleet", role: "driver" },
    tenants: [],
    steward: false,
    driver: { id: "d-1" },
    capabilities: [],
});

function serve(me: MeDTO) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
    web.on("GET", "/api/go/v1/me/tenants", () => json(200, { data: me.tenants }));
    web.on("GET", "/api/go/v1/badges", () => json(200, { data: {} }));
    return web;
}

/** Renders `children` once the language is Thai. */
function Thai({ children }: { children: React.ReactNode }) {
    const { language, setLanguage } = useLanguage();
    useEffect(() => setLanguage("th"), [setLanguage]);
    return language === "th" ? <>{children}</> : null;
}

async function renderShell(me: MeDTO, page: React.ReactNode, thai = false) {
    const web = serve(me);
    const shell = <AdminLayout>{page}</AdminLayout>;
    const view = renderWithProviders(thai ? <Thai>{shell}</Thai> : shell);
    await screen.findByTestId("user-menu-trigger");
    return { web, ...view };
}

const crumbs = () => screen.getByRole("navigation", { name: /Breadcrumb|ตำแหน่งของหน้า/ });
const currentCrumb = () => within(crumbs()).getByText((_, el) => el?.getAttribute("aria-current") === "page");

beforeEach(() => {
    mockPathname = "/app/nope";
    mockSearch = new URLSearchParams();
    document.cookie = "sidebar_state=; path=/; max-age=0";
});
afterEach(() => {
    vi.unstubAllGlobals();
});

describe("Page not found, inside the shell", () => {
    it("(en) keeps the sidebar, header and crumb, names itself in the crumb and the tab, and leads home", async () => {
        await renderShell(tenantAdmin, <AppNotFound />);
        expect(document.querySelector("header")).not.toBeNull();
        expect(screen.getByRole("navigation", { name: "Main menu" })).toBeInTheDocument();
        expect(screen.getByRole("main")).toContainElement(screen.getByRole("heading", { level: 1, name: "Page not found" }));
        await waitFor(() => expect(currentCrumb()).toHaveTextContent("Page not found"));
        await waitFor(() => expect(document.title).toBe("Page not found · LogiTrack Pro"));
        expect(screen.getByRole("link", { name: "Go to my home page" })).toHaveAttribute("href", "/app/dashboard");
        // Not the 403 page's words.
        expect(screen.queryByText(/permission|No access/i)).toBeNull();
    });

    it("(th) reads ไม่พบหน้านี้ and leads a customer to the customer's own home", async () => {
        await renderShell(customer, <AppNotFound />, true);
        expect(await screen.findByRole("heading", { level: 1, name: "ไม่พบหน้านี้" })).toBeInTheDocument();
        await waitFor(() => expect(document.title).toBe("ไม่พบหน้านี้ · LogiTrack Pro"));
        expect(screen.getByRole("link", { name: "ไปหน้าหลักของฉัน" })).toHaveAttribute("href", "/app/driver-monitor");
    });

    it("keeps the entry of a mapped prefix in the crumb (/app/trucks/x/y)", async () => {
        mockPathname = "/app/trucks/x/y";
        await renderShell(tenantAdmin, <AppNotFound />);
        expect(within(crumbs()).getByRole("link", { name: "Trucks" })).toHaveAttribute("href", "/app/trucks");
        await waitFor(() => expect(currentCrumb()).toHaveTextContent("Page not found"));
    });
});

describe("a page that crashed, inside the shell", () => {
    const crash = Object.assign(new Error("TypeError: cannot read 'plate' of undefined"), { digest: "d1g3st" });

    it("(en) offers Try again (reset) and home, and a reference for support, never the message", async () => {
        mockPathname = "/app/accounting/income";
        const reset = vi.fn();
        const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
        await renderShell(tenantAdmin, <AppError error={crash} reset={reset} />);
        // The route group's namespace (accounting) loads first; the boundary renders inside the page area.
        expect(screen.getByRole("main")).toContainElement(await screen.findByRole("heading", { level: 1, name: "This page stopped working" }));
        expect(document.querySelector("header")).not.toBeNull();
        // The crumb still names the page that crashed.
        await waitFor(() => expect(currentCrumb()).toHaveTextContent("Trips to bill"));
        await userEvent.setup().click(screen.getByRole("button", { name: "Try again" }));
        expect(reset).toHaveBeenCalledTimes(1);
        expect(screen.getByRole("link", { name: "Go to my home page" })).toHaveAttribute("href", "/app/dashboard");
        expect(screen.getByText("Reference for support: d1g3st")).toBeInTheDocument();
        expect(document.body.textContent).not.toMatch(/cannot read/);
        consoleError.mockRestore();
    });

    it("(th) in Thai, with a failed Go call's request id as the reference", async () => {
        const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
        const goFailure = new ApiError({ status: 500, code: "internal", message: "boom", requestId: "req-42" });
        await renderShell(tenantAdmin, <AppError error={goFailure} reset={() => {}} />, true);
        expect(await screen.findByRole("heading", { level: 1, name: "หน้านี้ทำงานผิดพลาด" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "ลองอีกครั้ง" })).toBeInTheDocument();
        expect(screen.getByText("รหัสอ้างอิงสำหรับฝ่ายช่วยเหลือ: req-42")).toBeInTheDocument();
        consoleError.mockRestore();
    });

    it("the root crash page stands alone: its own html, both languages, a retry, the start page, the reference", () => {
        const html = renderToStaticMarkup(<GlobalError error={Object.assign(new Error("secret internals"), { digest: "g-77" })} reset={() => {}} />);
        expect(html).toMatch(/^<html lang="th">(<head><\/head>)?<body/);
        expect(html).toContain("LogiTrack Pro stopped working");
        expect(html).toContain("LogiTrack Pro หยุดทำงาน");
        expect(html).toContain("ลองอีกครั้ง");
        expect(html).toContain("Try again");
        expect(html).toContain('href="/"');
        expect(html).toContain("g-77");
        expect(html).not.toContain("secret internals");
        expect(html.match(/<h1/g)).toHaveLength(1);
    });
});

describe("a record that is missing (components/record-state.tsx)", () => {
    function renderState(error: unknown, onRetry = vi.fn(), thai = false) {
        mockPathname = "/app/trucks/view";
        serve(tenantAdmin);
        const node = <RecordState error={error} onRetry={onRetry} />;
        renderWithProviders(thai ? <Thai>{node}</Thai> : node);
        return onRetry;
    }

    it("a 404 is 'Not found', in or out of scope alike, with the way back to the entry's list and no retry", async () => {
        renderState(new ApiError({ status: 404, code: "not_found", message: "not found", requestId: "r1" }));
        expect(await screen.findByRole("heading", { level: 1, name: "Not found" })).toBeInTheDocument();
        expect(screen.getByText("This record does not exist or is not in your scope.")).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "Back to Trucks" })).toHaveAttribute("href", "/app/trucks");
        expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
    });

    it("no error at all (an address without an id) is 'Not found' too; Thai", async () => {
        renderState(undefined, vi.fn(), true);
        expect(await screen.findByRole("heading", { level: 1, name: "ไม่พบข้อมูล" })).toBeInTheDocument();
        expect(screen.getByText("ไม่มีรายการนี้ หรือรายการนี้อยู่นอกขอบเขตข้อมูลของคุณ")).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "กลับไปที่รถบรรทุก" })).toHaveAttribute("href", "/app/trucks");
    });

    it("a 5xx or a network failure is a load error with a retry, the reason in words and the request id", async () => {
        const onRetry = renderState(new ApiError({ status: 503, code: "unavailable", message: "", requestId: "req-9" }));
        expect(await screen.findByRole("heading", { level: 1, name: "Could not load this record" })).toBeInTheDocument();
        expect(screen.getByRole("alert")).toHaveTextContent("The service is temporarily unavailable. Please try again.");
        await userEvent.setup().click(screen.getByRole("button", { name: "Try again" }));
        expect(onRetry).toHaveBeenCalledTimes(1);
        expect(screen.getByRole("link", { name: "Back to Trucks" })).toBeInTheDocument();
        expect(screen.getByText("Reference for support: req-9")).toBeInTheDocument();
        expect(screen.queryByRole("heading", { name: "Not found" })).toBeNull();
    });

    it("the truck page with an id that does not exist: an h1 'Not found' in the shell, crumb and tab included (was a red h2)", async () => {
        mockPathname = "/app/trucks/view";
        mockSearch = new URLSearchParams("id=does-not-exist");
        // GET /v1/trucks/does-not-exist answers the fake origin's default: 404 not_found.
        await renderShell(tenantAdmin, <TruckPreview />);
        expect(await screen.findByRole("heading", { level: 1, name: "Not found" })).toBeInTheDocument();
        expect(screen.queryByRole("heading", { level: 2 })).toBeNull();
        await waitFor(() => expect(document.title).toBe("Not found · Trucks · LogiTrack Pro"));
        expect(screen.getAllByRole("link", { name: "Back to Trucks" }).length).toBeGreaterThan(0);
    });

    it("the truck page when Go is down: a retry, not 'Not found'", async () => {
        mockPathname = "/app/trucks/view";
        mockSearch = new URLSearchParams("id=0199c000-0000-7000-8000-000000000001");
        const web = serve(tenantAdmin);
        web.on("GET", /^\/api\/go\/v1\/trucks\/0199c000/, () => json(503, { error: { code: "unavailable", message: "", requestId: "rq" } }));
        const client = createQueryClient();
        client.setDefaultOptions({ queries: { retry: false } });
        renderWithProviders(<TruckPreview />, client);
        expect(await screen.findByRole("heading", { level: 1, name: "Could not load this record" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
    });
});

describe("the 403 page (UI3D: quiet, names the page, says who can help)", () => {
    async function render403(me: MeDTO, thai = false, from = "/app/accounting/income") {
        mockPathname = "/app/unauthorized";
        mockSearch = new URLSearchParams({ from });
        await renderShell(me, <UnauthorizedPage />, thai);
        return screen.getByTestId("unauthorized");
    }

    it("has an ink heading and no lock tile, no 403 badge, no red, no raw path", async () => {
        const page = await render403(manager);
        expect(within(page).getByRole("heading", { level: 1, name: "No access to this page" })).toBeInTheDocument();
        // Red only names a state: nothing on this page is painted with the destructive colour.
        const red = [...page.querySelectorAll("*")].filter((el) =>
            (el.getAttribute("class") ?? "").split(/\s+/).some((c) => /^(bg|text|ring|border)-destructive/.test(c))
        );
        expect(red).toEqual([]);
        expect(page.textContent).not.toMatch(/403/);
        expect(page.textContent).not.toContain("/app/accounting/income");
        expect(within(page).getByText("Your current role does not include “Trips to bill”.")).toBeInTheDocument();
        expect(within(page).getByRole("button", { name: "Go back" })).toBeInTheDocument();
        expect(within(page).getByRole("button", { name: "Go to my home page" })).toBeInTheDocument();
    });

    it.each([
        ["an own-fleet tenant admin: the platform team at GORATECH", tenantAdmin, /GORATECH that runs LogiTrack Pro/],
        ["other staff: the organisation's administrator", manager, /Ask your organisation's administrator/],
        ["a customer: the transport company", customer, /transport company that invited you/],
        ["a partner admin (a carrier's tenant admin): the transport company", partnerAdmin, /transport company that invited you/],
        ["a partner admin on an own-fleet page (customers): nobody can grant it", partnerAdmin, /No role of a partner carrier's account can open it/, "/app/customers"],
        ["partner staff on an own-fleet page (pickup points): nobody can grant it", partnerStaff, /No role of a partner carrier's account can open it/, "/app/sources"],
        ["a platform admin outside a tenant: platform pages only", platformAdmin, /Platform roles open the platform's own pages/],
        ["a driver-only account: the driver app", driverOnly, /LogiTrack driver app/],
    ])("%s", async (_who, me, guide, from = "/app/accounting/income") => {
        const page = await render403(me, false, from);
        expect(within(page).getByTestId("unauthorized-guide")).toHaveTextContent(guide);
    });

    it("(th) names the page and the guidance in Thai", async () => {
        const page = await render403(customer, true);
        await waitFor(() => expect(within(page).getByRole("heading", { level: 1, name: "ไม่มีสิทธิ์เปิดหน้านี้" })).toBeInTheDocument());
        expect(within(page).getByText("บทบาทปัจจุบันของคุณไม่รวมหน้า “เที่ยวที่ต้องวางบิล”")).toBeInTheDocument();
        expect(within(page).getByTestId("unauthorized-guide")).toHaveTextContent("บริษัทขนส่งที่เชิญคุณ");
    });
});

describe("the page header (components/page-header.tsx)", () => {
    it("names a detail page by its record: H1, crumb and tab title read '<record> · <entry> · LogiTrack Pro'", async () => {
        mockPathname = "/app/customers/c1/edit";
        function Page() {
            const [shown, setShown] = React.useState(true);
            return shown ? (
                <PageHeader
                    title="Customers"
                    recordName="CJSF Co., Ltd."
                    back={{ href: "/app/customers/c1" }}
                    actions={
                        <button type="button" onClick={() => setShown(false)}>
                            Leave
                        </button>
                    }
                />
            ) : (
                <p>other</p>
            );
        }
        await renderShell(tenantAdmin, <Page />);
        const h1 = screen.getByRole("heading", { level: 1, name: "CJSF Co., Ltd." });
        expect(h1.className).toContain("text-2xl");
        expect(h1.className).toContain("font-semibold");
        expect(h1.className).toContain("tracking-tight");
        expect(h1.className).not.toMatch(/text-3xl|font-bold/);
        await waitFor(() => expect(currentCrumb()).toHaveTextContent("CJSF Co., Ltd."));
        expect(within(crumbs()).getByRole("link", { name: "Customers" })).toHaveAttribute("href", "/app/customers");
        await waitFor(() => expect(document.title).toBe("CJSF Co., Ltd. · Customers · LogiTrack Pro"));
        expect(screen.getByRole("link", { name: "Back to Customers" })).toHaveAttribute("href", "/app/customers/c1");
        // Unmounted, the crumb goes back to the generic name until the next page names itself.
        await userEvent.setup().click(screen.getByRole("button", { name: "Leave" }));
        await waitFor(() => expect(currentCrumb()).toHaveTextContent("Edit"));
    });

    it("on a menu page the title is the H1 and the crumb keeps the menu's label", async () => {
        mockPathname = "/app/accounting/income";
        await renderShell(tenantAdmin, <PageHeader title="Trips to bill" description="Trips delivered in the period" />);
        expect(screen.getByRole("heading", { level: 1, name: "Trips to bill" })).toBeInTheDocument();
        expect(screen.getByText("Trips delivered in the period")).toBeInTheDocument();
        await waitFor(() => expect(document.title).toBe("Trips to bill · LogiTrack Pro"));
    });
});

describe("a loaded record survives a failed background refetch (UI3D review)", () => {
    it("recordUnavailable: only a 404, or a settled query without a record, replaces the page", () => {
        const truck = { id: "t1" };
        const down = new ApiError({ status: 503, code: "unavailable", message: "" });
        const gone = new ApiError({ status: 404, code: "not_found", message: "" });
        expect(recordUnavailable({ data: truck, error: down, status: "error" })).toBe(false);
        expect(recordUnavailable({ data: truck, error: gone, status: "error" })).toBe(true);
        expect(recordUnavailable({ data: undefined, error: down, status: "error" })).toBe(true);
        expect(recordUnavailable({ data: undefined, error: null, status: "pending" })).toBe(false);
        expect(recordUnavailable({ data: null, error: null, status: "success" })).toBe(true);
    });

    it("the truck page keeps its truck when a refetch fails (window focus refetches; TanStack keeps the data)", async () => {
        mockPathname = "/app/trucks/view";
        const id = "0199c000-0000-7000-8000-00000000aa01";
        mockSearch = new URLSearchParams(`id=${id}`);
        let down = false;
        const web = serve(tenantAdmin);
        web.on("GET", `/api/go/v1/trucks/${id}`, () =>
            down
                ? json(503, { error: { code: "unavailable", message: "", requestId: "rq" } })
                : json(200, {
                      data: { id, tenantId: "t-wrt", ownershipType: "own", licensePlate: "70-1234", status: "active", files: {}, statusHistory: [], currentAssignments: [] },
                  })
        );
        const client = createQueryClient();
        client.setDefaultOptions({ queries: { retry: false } });
        renderWithProviders(<TruckPreview />, client);
        expect(await screen.findByRole("heading", { level: 1, name: "70-1234" })).toBeInTheDocument();
        down = true;
        await client.refetchQueries();
        await waitFor(() => expect(client.getQueryCache().findAll().some((q) => q.state.status === "error")).toBe(true));
        expect(screen.getByRole("heading", { level: 1, name: "70-1234" })).toBeInTheDocument();
        expect(screen.queryByText("Could not load this record")).toBeNull();
    });
});

describe("a record outside the reader's access (403)", () => {
    it("is said as such, with no retry that could never succeed", async () => {
        mockPathname = "/app/trucks/view";
        serve(tenantAdmin);
        renderWithProviders(<RecordState error={new ApiError({ status: 403, code: "permission_denied", message: "", requestId: "r3" })} onRetry={vi.fn()} />);
        expect(await screen.findByRole("heading", { level: 1, name: "No access to this record" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
        expect(screen.getByRole("link", { name: "Back to Trucks" })).toHaveAttribute("href", "/app/trucks");
    });
});

describe("icon-only buttons have names (UI3D)", () => {
    it("every row's actions button on /app/customers names the customer, in both languages", async () => {
        mockPathname = "/app/customers";
        const web = serve(tenantAdmin);
        web.on("GET", "/api/go/v1/customers", () =>
            json(200, {
                data: [
                    { id: "c1", code: "CJSF", name: "CJSF Co., Ltd.", driverIdTypes: [], logoUrl: null },
                    { id: "c2", code: "TTP", name: "TTP", driverIdTypes: [], logoUrl: null },
                ],
            })
        );
        renderWithProviders(<CustomersList />);
        expect(await screen.findByRole("button", { name: "Actions for CJSF Co., Ltd." })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Actions for TTP" })).toBeInTheDocument();
        for (const button of screen.getAllByRole("button")) expect(button).toHaveAccessibleName();
        expect(screen.getByRole("heading", { level: 1, name: "Customers" })).toBeInTheDocument();
    });
});
