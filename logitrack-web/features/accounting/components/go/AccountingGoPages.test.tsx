// T42 (issue #62): the Go accounting pages rendered under the app's providers, every request through the
// BFF (/api/go/v1/...). The web has no Firebase SDK since TW7 (lib/noFirebase.test.ts; firebaseImports.test.ts
// walks the pages' import graph). Pinned here: the audit's status switch is a new server
// request for that status (AC 3); income asks Go for the chosen range (AC 2); a rate-card void refetches
// only the entries (AC 1); a statement's documents are the stored ones, with the WHT rate of the
// statement (AC 5); Billing Document creates the statement from the server's invoice set.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

// The income export loads xlsx on the click; a stub records whether a file was written.
const xlsx = vi.hoisted(() => ({ writeFile: vi.fn() }));
vi.mock("xlsx", () => ({
    utils: { json_to_sheet: () => ({}), book_new: () => ({}), book_append_sheet: () => undefined },
    writeFile: xlsx.writeFile,
}));

import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { stubVirtualLayout } from "@/test-utils/virtualLayout";

import { BillingDocumentPage } from "./BillingDocumentPage";
import { BillingResultPage } from "./BillingResultPage";
import { ExpenseAuditPage } from "./ExpenseAuditPage";
import { IncomePage } from "./IncomePage";
import { RateCardPage } from "./RateCardPage";

let restoreLayout: () => void = () => undefined;
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
    window.history.replaceState(null, "", "/app/accounting/income");
    restoreLayout = stubVirtualLayout({ viewport: 600, row: 40 });
});
afterEach(() => {
    restoreLayout();
    resetQueryClientForTests();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

const CAPS = [
    "accounting:view_income",
    "accounting:view_rate_card",
    "accounting:edit_rate_card",
    "accounting:billing_document",
    "accounting:billing_result",
    "accounting:manage_statements",
    "accounting:audit_expense",
    "accounting:view_fuel",
    "accounting:view_other",
    "accounting:recompute_force",
];

const CUSTOMERS = [
    { id: "c1", billingPartyId: "p1", code: "CJSF", name: "CJ Super Fresh" },
    { id: "c2", billingPartyId: "p2", code: "TTP", name: "TTP Co" },
];

function setup(capabilities: string[] = CAPS) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities }) }));
    web.on("GET", "/api/go/v1/customers", () => json(200, { data: CUSTOMERS }));
    web.on("GET", "/api/go/v1/hubs", () => json(200, { data: [{ id: "h1", sourceId: "SPX-A", sourceNameTh: "บางนา", sourceNameEn: "Bang Na", stationType: "HUB" }] }));
    return web;
}

function at(pathname: string, ui: React.ReactElement) {
    return <RouteNamespaces pathname={pathname}>{ui}</RouteNamespaces>;
}

/** The app's providers over the tab's client, the one the pages' plain write functions invalidate. */
const renderPage = (pathname: string, ui: React.ReactElement) => renderWithProviders(at(pathname, ui), getQueryClient());

const query = (url: string) => Object.fromEntries(new URL(url, "http://web.test").searchParams);

const trip = (id: string, tripNo: string, billing: Record<string, unknown>) => ({
    id,
    tripNo,
    taskId: `k-${id}`,
    jobCategory: "PRIMARY",
    isMultiDelivery: false,
    originPlace: { raw: "SPX-A", key: "SPX-A", label: "Bang Na", resolved: true },
    destinationPlace: { raw: "SOCE", key: "SOCE", label: "SOC East", resolved: true },
    deliveredAt: "2026-10-05T03:00:00Z",
    billing: { customerId: "c1", billingPartyId: "p1", isMultiDelivery: false, manualOverride: false, ...billing },
});

/** The Billing Document answer of CJSF, September 2026, one priced trip. */
const billingRows = (q: Record<string, string>) => ({
    customerId: "c1",
    billingPartyId: "p1",
    year: 2026,
    month: 9,
    basis: "delivered",
    rows: [
        { id: "t1", rowType: "trip", tripNo: "SPXTH-9", estimateThb: 2000, inInvoiceSet: true, inPreview: true, flags: [], jobCategory: "PRIMARY", originHubCode: "SPX-A", destinationDisplayName: "SOC East", hubDisplayName: "Bang Na", lookupHubCode: "SPX-A", lookupDestinationCode: "SOCE" },
    ].filter(() => (q.types ?? "trip").split(",").includes("trip")),
    unpriced: [{ id: "t9", rowType: "trip", tripNo: "SPXTH-X", reason: "no_rate", reasonSource: "stored", hubDisplayName: "", destinationDisplayName: "", createdAt: "2026-09-02T00:00:00Z", dryRunEstimateThb: null }],
    invoice: { grandTotal: 2000, withholdingTaxRate: 0.01, withholdingTax: 20, net: 1980, rowCount: 1, tripOnlyCount: 1, tripSubtotal: 2000, standbyCount: 0, standbySubtotal: 0, multiDropCount: 0, multiDropSubtotal: 0 },
    preview: { grandTotal: 2000, withholdingTaxRate: 0.01, withholdingTax: 20, net: 1980, rowCount: 1, tripOnlyCount: 1, tripSubtotal: 2000, standbyCount: 0, standbySubtotal: 0, multiDropCount: 0, multiDropSubtotal: 0 },
    typeCounts: { trip: 1, multidrop: 0, standby: 0 },
    categoryCounts: { PRIMARY: 1, SUPPLEMENTARY: 0, unverified: 0 },
    issues: { planDateDrifted: 0, planDateOutOfPeriod: 0, noPlanDate: 0, standbyPlanOtherMonth: 0, suppPricedWithFuel: 0, suppManualPriceWithFuel: 0, repairTripIds: [] },
    filterOptions: { plates: [], vehicleClasses: [], reviewMonths: [] },
    outsideReach: 0,
    documentable: true,
});

/** The Billing Document page's handlers; `/v1/billing/rows` answers 422 to an empty toggle list, as Go does. */
function billingDocumentSetup() {
    const web = setup();
    web.on("GET", "/api/go/v1/billing/rows", (c) => {
        const q = query(c.url);
        if (q.types === "" || q.categories === "") return goErr(422, "invalid_argument", { fields: [{ field: q.types === "" ? "types" : "categories", reason: "required" }] });
        return json(200, { data: billingRows(q) });
    });
    web.on("GET", "/api/go/v1/billing/standby-diagnostics", () => json(200, { data: { unpriced: [], missingEndedAt: [], missingEndedAtTruncated: false } }));
    web.on("GET", "/api/go/v1/billing/rows/missing-billing-date", () => json(200, { data: [] }));
    return web;
}

/**
 * The rows' customers are named by id once the rows are there (`useCustomerLookup`, UI2C); the table's cells
 * re-render then, so a test takes its row elements after the names.
 */
const named = (label = "CJSF — CJ Super Fresh") => waitFor(() => expect(screen.getAllByText(label).length).toBeGreaterThan(0));

async function loadCJSF(u: ReturnType<typeof userEvent.setup>) {
    await u.click((await screen.findAllByRole("combobox"))[2]);
    await u.click(await screen.findByRole("option", { name: /CJSF/ }));
    await u.click(screen.getByRole("button", { name: /Load data/ }));
}

describe("Go accounting pages (T42)", () => {
    it("audit: the status tab is a server filter (a switch asks Go for that status only)", async () => {
        const web = setup();
        web.on("GET", "/api/go/v1/expenses", (c) => {
            const status = query(c.url).status;
            const row = (id: string, s: string) => ({ id, type: "fuel", status: s, date: "2026-10-01T03:00:00Z", expenseDate: "2026-10-01", amountThb: 500, driverName: `Driver ${id}`, licensePlate: "70-1234" });
            return json(200, { data: status === "approved" ? [row("a1", "approved")] : [row("p1", "pending"), row("p2", "pending")], meta: { statusCounts: { pending: 2, approved: 1, rejected: 0 } } });
        });
        renderPage("/app/accounting/audit", <ExpenseAuditPage />);
        expect(await screen.findByText("Driver p1")).toBeInTheDocument();
        const u = userEvent.setup();
        await u.click(screen.getByRole("tab", { name: /Approved/ }));
        expect(await screen.findByText("Driver a1")).toBeInTheDocument();
        const statuses = web.calls.filter((c) => c.url.startsWith("/api/go/v1/expenses")).map((c) => query(c.url).status);
        expect(statuses).toEqual(["pending", "approved"]);
    });

    it("income: asks Go for the range on the delivered axis and shows the stored prices", async () => {
        const web = setup();
        web.on("GET", "/api/go/v1/trips", (c) => {
            // Go answers 422 `status: not_on_axis` when `status` rides on the delivered axis.
            if (query(c.url).status !== undefined) return goErr(422, "invalid_argument", { fields: [{ field: "status", reason: "not_on_axis" }] });
            return json(200, {
                data: [
                    {
                        id: "t1",
                        tripNo: "SPXTH-001",
                        taskId: "k1",
                        jobCategory: "PRIMARY",
                        isMultiDelivery: false,
                        originPlace: { raw: "SPX-A", key: "SPX-A", label: "Bang Na", resolved: true },
                        destinationPlace: { raw: "SOCE", key: "SOCE", label: "SOC East", resolved: true },
                        deliveredAt: "2026-10-05T03:00:00Z",
                        billing: { customerId: "c1", billingPartyId: "p1", priced: true, estimateThb: 1234, baseRateThb: 1200, rateMultiplier: 1.0283, isMultiDelivery: false, manualOverride: false },
                    },
                ],
            });
        });
        web.on("GET", "/api/go/v1/standby", () => json(200, { data: [] }));
        renderPage("/app/accounting/income", <IncomePage />);
        await named();
        expect(await screen.findByText("SPXTH-001")).toBeInTheDocument();
        expect(screen.getAllByText("฿1,234").length).toBeGreaterThan(0);
        const trips = web.calls.find((c) => c.url.startsWith("/api/go/v1/trips"))!;
        const q = query(trips.url);
        expect(q).toMatchObject({ axis: "delivered", limit: "200" });
        expect(q).not.toHaveProperty("status");
        expect(q.from).toMatch(/^\d{4}-\d{2}-01$/);
        expect(q.to).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    });

    it("income: a user without billing_document gets the loaded unpriced rows on the missing-billing tab, never a 403", async () => {
        const web = setup(["accounting:view_income"]);
        web.on("GET", "/api/go/v1/trips", () =>
            json(200, { data: [trip("t1", "SPXTH-001", { priced: true, estimateThb: 1234 }), trip("t2", "SPXTH-002", { priced: false, estimateThb: null, unpricedReason: "no_rate" })] })
        );
        web.on("GET", "/api/go/v1/standby", () => json(200, { data: [] }));
        web.on("GET", "/api/go/v1/billing/rows", () => goErr(403, "permission_denied"));
        renderPage("/app/accounting/income", <IncomePage />);
        await named();
        expect(await screen.findByText("SPXTH-001")).toBeInTheDocument();
        const u = userEvent.setup();
        await u.click(screen.getByTestId("missing-billing-tab"));
        const tab = await screen.findByTestId("missing-billing-loaded");
        expect(within(tab).getByText("SPXTH-002")).toBeInTheDocument();
        expect(within(tab).queryByText("SPXTH-001")).toBeNull();
        expect(web.count("GET", "/api/go/v1/billing/rows")).toBe(0);
    });

    it("income: a billing_document holder's missing-billing tab reads the month's rows from Go", async () => {
        const web = setup();
        web.on("GET", "/api/go/v1/trips", () => json(200, { data: [trip("t1", "SPXTH-001", { priced: true, estimateThb: 1234 })] }));
        web.on("GET", "/api/go/v1/standby", () => json(200, { data: [] }));
        web.on("GET", "/api/go/v1/billing/rows", () => json(200, { data: billingRows({}) }));
        renderPage("/app/accounting/income", <IncomePage />);
        await named();
        expect(await screen.findByText("SPXTH-001")).toBeInTheDocument();
        const u = userEvent.setup();
        await u.click(screen.getByTestId("missing-billing-tab"));
        expect(await screen.findByText("SPXTH-X")).toBeInTheDocument();
        expect(query(web.calls.find((c) => c.url.startsWith("/api/go/v1/billing/rows"))!.url)).toMatchObject({ customerId: "all" });
    });

    it("income: the export stops, with an error, when a page of the range fails (no partial file)", async () => {
        const web = setup();
        web.on("GET", "/api/go/v1/trips", (c) =>
            query(c.url).cursor === "c2" ? goErr(403, "permission_denied") : json(200, { data: [trip("t1", "SPXTH-001", { priced: true, estimateThb: 1234 })], nextCursor: "c2" })
        );
        web.on("GET", "/api/go/v1/standby", () => json(200, { data: [] }));
        const error = vi.spyOn(toast, "error");
        renderPage("/app/accounting/income", <IncomePage />);
        await named();
        expect(await screen.findByText("SPXTH-001")).toBeInTheDocument();
        const u = userEvent.setup();
        await u.click(screen.getByRole("button", { name: /Export Excel/ }));
        await waitFor(() => expect(error).toHaveBeenCalledWith("The export was stopped: not every page of the range could be loaded. Try again."));
        expect(xlsx.writeFile).not.toHaveBeenCalled();
        expect(web.calls.some((c) => c.url.startsWith("/api/go/v1/trips") && query(c.url).cursor === "c2")).toBe(true);
    });

    it("rate card: a void refetches the entries and reads no other table", async () => {
        const web = setup();
        let voided = false;
        web.on("GET", "/api/go/v1/billing/rate-entries", () =>
            json(200, {
                data: [
                    {
                        id: "e1",
                        customerId: "c1",
                        hubId: "SPX-A",
                        rawHubName: "SPX-A - Bang Na",
                        destinationCode: "SOCE",
                        vehicleClass: "4WJ",
                        rateThb: 1500,
                        jobCategory: "PRIMARY",
                        effectiveFromDateStr: "2026-09-01",
                        importId: "imp-1",
                        voided,
                    },
                ],
            })
        );
        web.on("POST", "/api/go/v1/billing/rate-entries/e1/void", () => {
            voided = true;
            return json(200, { data: { id: "e1", customerId: "c1", voided: true } });
        });
        renderPage("/app/accounting/rate-card", <RateCardPage />);
        const u = userEvent.setup();
        await named();
        await u.click(await screen.findByRole("button", { name: /Void this round/ }));
        const dialog = await screen.findByRole("dialog");
        await u.type(within(dialog).getByRole("textbox"), "wrong price");
        await u.click(within(dialog).getByRole("button", { name: "Void" }));
        await waitFor(() => expect(web.count("GET", "/api/go/v1/billing/rate-entries")).toBe(2));
        expect(web.calls.find((c) => c.method === "POST")!.body).toEqual({ reason: "wrong price" });
        for (const other of ["/api/go/v1/billing/fuel-adjustments", "/api/go/v1/billing/service-fees", "/api/go/v1/billing/standby-rates", "/api/go/v1/fuel/monthly"]) {
            expect(web.count("GET", other), other).toBe(0);
        }
    });

    it("billing results: the period filter is server-side and the documents are the stored ones with the statement's WHT", async () => {
        const web = setup();
        const statement = {
            id: "s1",
            customerId: "c1",
            invoiceNumber: "INV-CJSF-202609-001",
            customerName: "CJ Super Fresh",
            customerCode: "CJSF",
            period: { year: 2026, month: 9 },
            totalAmount: 10000,
            withholdingTaxRate: 0.03,
            withholdingTax: 300,
            netAmount: 9700,
            tripCount: 8,
            status: "draft",
            generatedAt: "2026-10-01T02:00:00Z",
            dueDate: null,
            generatedByName: "Accountant",
        };
        web.on("GET", "/api/go/v1/billing/statements", () => json(200, { data: [statement] }));
        web.on("GET", "/api/go/v1/billing/statements/s1", () =>
            json(200, { data: { ...statement, documents: [{ kind: "bundle_zip", status: "ready", url: "/v1/billing/statements/s1/documents/bundle_zip", renderedAt: "2026-10-01T02:01:00Z", updatedAt: "2026-10-01T02:01:00Z" }] } })
        );
        web.on("GET", "/api/go/v1/billing/statements/s1/documents/bundle_zip", () => new Response(null, { status: 302, headers: { Location: "https://media.test/f" } }));
        const assign = vi.fn();
        vi.stubGlobal("location", { ...window.location, assign, pathname: "/app/accounting/billing-result", href: "http://localhost/app/accounting/billing-result" });
        renderPage("/app/accounting/billing-result", <BillingResultPage />);
        const row = await screen.findByTestId("statement-row");
        expect(within(row).getByText("INV-CJSF-202609-001")).toBeInTheDocument();
        expect(within(row).getByText("3%")).toBeInTheDocument();
        const u = userEvent.setup();
        await u.click(within(row).getByRole("button", { name: "Actions" }));
        // A draft is sent before it is paid (Go answers 409 invalid_transition to draft → paid).
        expect(await screen.findByRole("menuitem", { name: "Mark as sent" })).toBeInTheDocument();
        expect(screen.queryByRole("menuitem", { name: "Mark as paid" })).toBeNull();
        await u.click(await screen.findByRole("menuitem", { name: "Documents" }));
        const docs = await screen.findByTestId("statement-documents");
        await u.click(within(docs).getByRole("button", { name: /Download/ }));
        await waitFor(() => expect(assign).toHaveBeenCalledWith("/api/go/v1/billing/statements/s1/documents/bundle_zip"));
        expect(query(web.calls.find((c) => c.url.startsWith("/api/go/v1/billing/statements?") || c.url === "/api/go/v1/billing/statements")!.url)).toEqual({ limit: "50" });
    });

    it("billing document: Load reads the server's invoice set; Create posts the statement of that set", async () => {
        const web = billingDocumentSetup();
        web.on("POST", "/api/go/v1/billing/statements", () => json(201, { data: { id: "s9", invoiceNumber: "INV-9", customerId: "c1", period: { year: 2026, month: 9 } } }));
        web.on("GET", "/api/go/v1/billing/statements/s9", () => json(200, { data: { id: "s9", invoiceNumber: "INV-9", status: "draft", documents: [] } }));
        renderPage("/app/accounting/billing-document", <BillingDocumentPage />);
        const u = userEvent.setup();
        // Choose CJSF, then load (the all-customers view is never documented).
        await loadCJSF(u);
        expect(await screen.findByText("SPXTH-9")).toBeInTheDocument();
        expect(screen.getByText("Withholding tax 1%")).toBeInTheDocument();
        expect(screen.getByText(/1 row\(s\) of this period have no price/)).toBeInTheDocument();
        await u.click(screen.getByRole("button", { name: /Create statement and documents/ }));
        await waitFor(() => expect(web.count("POST", "/api/go/v1/billing/statements")).toBe(1));
        const post = web.calls.find((c) => c.method === "POST" && c.url === "/api/go/v1/billing/statements")!;
        expect(post.body).toMatchObject({ customerId: "c1", types: ["trip", "multidrop", "standby"], categories: ["PRIMARY", "SUPPLEMENTARY", "unverified"], showActualPickup: false });
        const rows = web.calls.find((c) => c.url.startsWith("/api/go/v1/billing/rows?"))!;
        expect(query(rows.url)).toMatchObject({ customerId: "c1", types: "trip,multidrop,standby" });
        // The documents are queued: Go has no row for them yet, so the dialog waits instead of saying "none".
        expect(await screen.findByTestId("documents-waiting")).toBeInTheDocument();
        expect(screen.queryByText("No document has been rendered for this statement yet.")).toBeNull();
    });

    it("billing document: a new statement's documents dialog polls while the stream is closed until the documents arrive", async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            const web = billingDocumentSetup();
            web.on("POST", "/api/go/v1/billing/statements", () => json(201, { data: { id: "s9", invoiceNumber: "INV-9", customerId: "c1", period: { year: 2026, month: 9 } } }));
            let reads = 0;
            web.on("GET", "/api/go/v1/billing/statements/s9", () => {
                reads += 1;
                const documents =
                    reads === 1
                        ? []
                        : ["bundle_zip", "invoice_summary_pdf", "invoice_detail_xlsx"].map((kind) => ({ kind, status: "ready", url: `/v1/billing/statements/s9/documents/${kind}`, renderedAt: "2026-10-01T02:01:00Z", updatedAt: "2026-10-01T02:01:00Z" }));
                return json(200, { data: { id: "s9", invoiceNumber: "INV-9", status: "draft", documents } });
            });
            renderPage("/app/accounting/billing-document", <BillingDocumentPage />);
            const u = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
            await loadCJSF(u);
            expect(await screen.findByText("SPXTH-9")).toBeInTheDocument();
            await u.click(screen.getByRole("button", { name: /Create statement and documents/ }));
            expect(await screen.findByTestId("documents-waiting")).toBeInTheDocument();
            await vi.advanceTimersByTimeAsync(5_000);
            const docs = await screen.findByTestId("statement-documents");
            expect(within(docs).getAllByRole("button", { name: /Download/ })).toHaveLength(3);
            expect(reads).toBeGreaterThanOrEqual(2);
            expect(screen.queryByTestId("documents-waiting")).toBeNull();
        } finally {
            vi.useRealTimers();
        }
    });

    it("billing document: unticking every type never sends an empty list; the toggles stay and a tick loads again", async () => {
        const web = billingDocumentSetup();
        renderPage("/app/accounting/billing-document", <BillingDocumentPage />);
        const u = userEvent.setup();
        await loadCJSF(u);
        expect(await screen.findByText("SPXTH-9")).toBeInTheDocument();
        for (const name of [/Regular trips/, /Multi-drop stops/, /^Standby/]) await u.click(screen.getByRole("checkbox", { name }));
        expect(await screen.findByTestId("toggles-incomplete")).toBeInTheDocument();
        expect(screen.getByTestId("row-toggles")).toBeInTheDocument();
        expect(screen.queryByText("SPXTH-9")).toBeNull();
        // Load with nothing ticked asks for nothing either.
        await u.click(screen.getByRole("button", { name: /Load data/ }));
        await u.click(screen.getByRole("checkbox", { name: /Regular trips/ }));
        expect(await screen.findByText("SPXTH-9")).toBeInTheDocument();
        const types = web.calls.filter((c) => c.url.startsWith("/api/go/v1/billing/rows?")).map((c) => query(c.url).types);
        expect(types).not.toContain("");
        expect(types.at(-1)).toBe("trip");
    });
});
