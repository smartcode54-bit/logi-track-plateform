import React from "react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { customersReply, fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { stubVirtualLayout } from "@/test-utils/virtualLayout";
import { BackfillPanel } from "./BackfillPanel";
import { IncomePage } from "./IncomePage";
import { RateCardPage } from "./RateCardPage";
import { BackfillUtilitiesPage } from "./BackfillUtilitiesPage";
import { bangkokLast30Days, bangkokMonthRange, formatBillingDay } from "../../lib/billingUiDates";

const xlsx = vi.hoisted(() => ({ writeFile: vi.fn(), rows: vi.fn(), sheet: vi.fn() }));
vi.mock("xlsx", () => ({ utils: { json_to_sheet: xlsx.rows, book_new: () => ({}), book_append_sheet: xlsx.sheet }, writeFile: xlsx.writeFile }));

const allCaps = ["accounting:view_income", "accounting:billing_document", "accounting:recompute_force", "accounting:edit_rate_card", "accounting:override_price", "operations:edit_trip_details"];
const customer = { id: "c1", billingPartyId: "p1", code: "CJ", name: "Customer One" };
const row = (id: string, amount: number | null) => ({
    id, tripNo: `TR-${id}`, taskId: "task1", deliveredAt: "2026-10-04T19:00:00Z", jobCategory: "PRIMARY", vehicleClass: "4W",
    originPlace: { label: "Hub" }, destinationPlace: { label: "SOC" },
    billing: { estimateThb: amount, priced: amount !== null, billingAxisDate: "2026-10-05", billingDate: "2026-10-01", customerId: "c1", billingPartyId: "p1", rateEntryId: "rate-returned", lookupHubCode: "HUB-RETURNED", lookupDestinationCode: "SOC-RETURNED", baseRateThb: 100, rateMultiplier: 1.05, fuelEffectiveFromDate: "2026-10-01", referenceFuelPriceThb: 32.50, fuelBandLowerThb: 32, fuelBandUpperThb: 32.99, stops: [{ stopIndex: 1, destinationCode: "STOP-RETURNED", baseRateThb: 20, finalRateThb: 21 }], unpricedReason: amount === null ? "no_rate" : null },
});
let restoreLayout: () => void;
beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
    globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
});
beforeEach(() => {
    localStorage.setItem("language", "en");
    sessionStorage.clear();
    window.history.replaceState(null, "", "/app/accounting/income");
    restoreLayout = stubVirtualLayout({ viewport: 600, row: 40 });
});
afterEach(() => { restoreLayout(); resetQueryClientForTests(); vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.clearAllMocks(); });
function setup(capabilities = allCaps, viewer = "viewer-a", tenantId = "tenant-a") {
    const web = fakeWeb();
    const me = makeMe({ capabilities });
    web.on("GET", "/api/go/v1/me", () => json(200, { data: { ...me, id: viewer, tenant: { ...me.tenant, id: tenantId } } }));
    web.on("GET", "/api/go/v1/customers", customersReply([customer]));
    web.on("GET", "/api/go/v1/trips", () => json(200, { data: [row("1", 1234.50), row("2", null)] }));
    web.on("GET", "/api/go/v1/standby", () => json(200, { data: [] }));
    web.on("GET", "/api/go/v1/billing/rows", () => json(200, { data: { rows: [], unpriced: [{ id: "missing1", tripId: "mt1", tripNo: "MISSING-1", reason: "no_rate", reasonSource: "stored", customerId: "c1", dryRunEstimateThb: 99.75 }] } }));
    return web;
}
const renderPage = (ui: React.ReactElement) => renderWithProviders(<RouteNamespaces pathname="/app/accounting/income">{ui}</RouteNamespaces>, getQueryClient());
const card = (title: string) => screen.getAllByText(title).find((el) => el.getAttribute("data-slot") === "card-title")!.closest<HTMLElement>('[data-slot="card"]')!;
const jobs = (web: ReturnType<typeof fakeWeb>) => web.calls.filter((c) => c.method === "POST" && c.url.includes("/jobs/"));
const panel = () => <BackfillPanel defaultFrom="2026-10-01" defaultTo="2026-10-31" defaultCustomer="c1" />;

describe("Income billing clarity", () => {
    it("shows dashes while pending and errors instead of zero aggregates", async () => {
        const web = setup();
        let release!: () => void;
        const gate = new Promise<void>((resolve) => { release = resolve; });
        web.on("GET", "/api/go/v1/trips", async () => { await gate; return goErr(403, "permission_denied"); });
        web.handlers.reverse();
        renderPage(<IncomePage />);
        await screen.findAllByText("Billing amount");
        expect(within(card("Billing amount")).getByText("—")).toBeInTheDocument();
        expect(within(card("Priced rows")).getByText("—")).toBeInTheDocument();
        await userEvent.setup().click(screen.getByTestId("missing-billing-tab"));
        release();
        await screen.findByRole("alert");
        expect(within(card("Billing amount")).queryByText("฿0.00")).toBeNull();
        expect(within(card("Priced rows")).queryByText("0")).toBeNull();
    });
    it("keeps satang, loaded-row labels, matching priced badge and returned provenance", async () => {
        const web = setup();
        web.on("GET", "/api/go/v1/trips", () => json(200, { data: [row("1", 1234.50), ...Array.from({ length: 198 }, (_, i) => row(`zero-${i}`, 0)), row("missing", null)], nextCursor: "more" }));
        web.handlers.reverse();
        renderPage(<IncomePage />);
        await screen.findByText("TR-1");
        await screen.findAllByText("CJ — Customer One");
        expect(within(card("Billing amount")).getByText("฿1,234.50")).toBeInTheDocument();
        expect(within(card("Priced rows")).getByText("199")).toBeInTheDocument();
        expect(screen.getByRole("tab", { name: /With billing/ })).toHaveTextContent("199");
        expect(screen.getAllByText("Based on loaded rows").length).toBeGreaterThanOrEqual(4);
        expect(screen.getByRole("button", { name: "1 loaded rows without a price" })).toBeInTheDocument();
        expect(screen.getByText("The figures above cover the rows loaded so far; load more to include the rest.")).toBeInTheDocument();
        const disclosure = screen.getAllByText("rate-returned")[0].closest("details")!;
        fireEvent.click(within(disclosure).getByText("Price details"));
        for (const fact of ["HUB-RETURNED", "SOC-RETURNED", "5%", "฿32.50"]) expect(within(disclosure).getByText(fact)).toBeInTheDocument();
        expect(within(disclosure).getByText(/STOP-RETURNED/)).toBeInTheDocument();
        expect(screen.queryByText("Rate import")).toBeNull();
    });
    it("uses the selected customer and month for missing rows and counts them on the tab", async () => {
        const web = setup();
        renderPage(<IncomePage />);
        await screen.findByText("TR-1");
        await screen.findAllByText("CJ — Customer One");
        const u = userEvent.setup();
        await u.click(screen.getByRole("combobox", { name: "Customer" }));
        await u.click(await screen.findByRole("option", { name: /CJ/ }));
        await u.click(screen.getByTestId("missing-billing-tab"));
        expect(await screen.findByText("MISSING-1")).toBeInTheDocument();
        expect(screen.getByRole("tab", { name: /Missing billing/ })).toHaveTextContent("1");
        expect(screen.getByText("Price if repriced now (server dry run)")).toBeInTheDocument();
        expect(screen.getByText("฿99.75")).toBeInTheDocument();
        await u.click(screen.getByRole("combobox", { name: "Delivery month" }));
        await u.click(screen.getByRole("option", { name: "February" }));
        await waitFor(() => expect(web.calls.some((c) => c.url.includes("/billing/rows?") && new URL(c.url, "http://test").searchParams.get("customerId") === "p1" && new URL(c.url, "http://test").searchParams.get("month") === "2")).toBe(true));
    });
    it("refetches the selected customer's missing rows after pricing", async () => {
        const web = setup();
        let priced = false;
        web.on("GET", "/api/go/v1/billing/rows", () => json(200, { data: { rows: [], unpriced: priced ? [] : [{ id: "missing1", tripId: "mt1", tripNo: "MISSING-1", customerId: "c1", reason: "no_rate", reasonSource: "stored" }] } }));
        web.handlers.reverse();
        web.on("POST", "/api/go/v1/trips/mt1/billing/compute", () => { priced = true; return json(200, { data: { ok: true } }); });
        renderPage(<IncomePage />); const u = userEvent.setup();
        await screen.findByText("TR-1");
        await u.click(screen.getByRole("combobox", { name: "Customer" }));
        await u.click(await screen.findByRole("option", { name: /CJ/ }));
        await u.click(screen.getByTestId("missing-billing-tab"));
        await screen.findByText("MISSING-1");
        const calls = () => web.calls.filter((c) => c.url.includes("/billing/rows?") && new URL(c.url, "http://test").searchParams.get("customerId") === "p1").length;
        const before = calls();
        await u.click(screen.getByRole("button", { name: "Recompute the price" }));
        await waitFor(() => expect(calls()).toBeGreaterThan(before));
        await waitFor(() => expect(screen.queryByText("MISSING-1")).toBeNull());
        expect(screen.getByRole("tab", { name: /Missing billing/ })).toHaveTextContent("0");
    });
    it.each([true, false])("keeps manual-price and delivered-time edits for unpriced trips (billing rows: %s)", async (billingRows) => {
        const web = setup(allCaps.filter((c) => billingRows || c !== "accounting:billing_document"));
        web.on("GET", "/api/go/v1/billing/rows", () => json(200, { data: { rows: [], unpriced: [{ id: "missing1", rowType: "trip", tripId: "mt1", tripNo: "MISSING-1", deliveredAt: "2026-10-04T19:00:00Z", customerId: "c1", reason: "no_rate", reasonSource: "stored" }] } }));
        web.handlers.reverse();
        const id = billingRows ? "mt1" : "2";
        web.on("PATCH", `/api/go/v1/trips/${id}`, () => json(200, { data: {} }));
        web.on("PUT", `/api/go/v1/trips/${id}/billing/manual`, () => json(200, { data: {} }));
        renderPage(<IncomePage />); const u = userEvent.setup();
        await screen.findByText("TR-1");
        await u.click(screen.getByTestId("missing-billing-tab"));
        await screen.findByText(billingRows ? "MISSING-1" : "TR-2");
        await u.click(screen.getByRole("button", { name: "Edit delivered time (Asia/Bangkok)" }));
        const input = screen.getByLabelText("Edit delivered time (Asia/Bangkok)");
        expect(input).toHaveValue("2026-10-05T02:00");
        expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
        fireEvent.change(input, { target: { value: "2026-10-05T03:00" } });
        await u.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(web.count("PATCH", `/api/go/v1/trips/${id}`)).toBe(1));
        await u.click(screen.getByRole("button", { name: "Row actions" }));
        await u.click(await screen.findByRole("menuitem", { name: "Edit billing" }));
        fireEvent.change(screen.getByRole("spinbutton"), { target: { value: "432.25" } });
        await u.click(screen.getByRole("button", { name: "Save Billing" }));
        await waitFor(() => expect(web.count("PUT", `/api/go/v1/trips/${id}/billing/manual`)).toBe(1));
        expect(web.calls.find((c) => c.method === "PUT")?.body).toEqual({ billingEstimateThb: 432.25, jobCategory: "PRIMARY" });
    });
    it.each([true, false])("keeps missing-trip edits hidden without capabilities (billing rows: %s)", async (billingRows) => {
        setup(["accounting:view_income", ...(billingRows ? ["accounting:billing_document"] : [])]);
        renderPage(<IncomePage />); const u = userEvent.setup();
        await screen.findByText("TR-1");
        await u.click(screen.getByTestId("missing-billing-tab"));
        await screen.findByText(billingRows ? "MISSING-1" : "TR-2");
        expect(screen.queryByRole("button", { name: "Row actions" })).toBeNull();
        expect(screen.queryByRole("button", { name: "Edit delivered time (Asia/Bangkok)" })).toBeNull();
    });
    it("paginates and retries the fallback month independently of the delivered range", async () => {
        const web = setup(["accounting:view_income"]);
        let fail = true;
        for (const kind of ["trips", "standby"]) web.on("GET", `/api/go/v1/${kind}`, (call) => {
            const q = new URL(call.url, "http://test").searchParams;
            if (!q.get("from")?.endsWith("-02-01")) return json(200, { data: [] });
            if (q.has("cursor") && fail) return goErr(403, "permission_denied");
            const id = `${kind}-${q.has("cursor") ? "next" : "first"}`;
            return json(200, { data: kind === "trips" ? [row(id, null)] : [{ id, driverName: id, endedAt: "2026-02-01T19:00:00Z" }], nextCursor: q.has("cursor") ? undefined : "more" });
        });
        web.handlers.reverse();
        renderPage(<IncomePage />); const u = userEvent.setup();
        await screen.findByRole("combobox", { name: "Customer" });
        await u.click(screen.getByTestId("missing-billing-tab"));
        await u.click(screen.getByRole("combobox", { name: "Month" }));
        await u.click(screen.getByRole("option", { name: "February" }));
        await screen.findByText("TR-trips-first");
        await u.click(screen.getByRole("button", { name: "Load more" }));
        await screen.findAllByRole("alert");
        fail = false;
        await u.click(screen.getByRole("button", { name: "Load more" }));
        await screen.findByText("TR-trips-next");
        expect(screen.getByText(/standby-next/)).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
    });
    it.each(["trip", "standby"])("loads only selected %s pages in the fallback month", async (kind) => {
        const web = setup(["accounting:view_income"]);
        for (const list of ["trips", "standby"]) web.on("GET", `/api/go/v1/${list}`, (call) => {
            const q = new URL(call.url, "http://test").searchParams;
            const next = q.has("cursor");
            return json(200, { data: list === "trips" ? [row(next ? "next" : "first", null)] : [{ id: next ? "next" : "first", driverName: next ? "Standby next" : "Standby first" }], nextCursor: next ? undefined : "more" });
        });
        web.handlers.reverse();
        renderPage(<IncomePage />); const u = userEvent.setup();
        await u.click(await screen.findByRole("combobox", { name: "Trip type" }));
        await u.click(screen.getByRole("option", { name: kind === "trip" ? "Normal trip" : "Standby" }));
        await u.click(screen.getByTestId("missing-billing-tab"));
        await u.click(await screen.findByRole("button", { name: "Load more" }));
        await screen.findByText(kind === "trip" ? "TR-next" : /Standby next/);
        const cursorCalls = web.calls.filter((c) => new URL(c.url, "http://test").searchParams.has("cursor"));
        expect(cursorCalls.length).toBeGreaterThan(0);
        expect(cursorCalls.every((c) => c.url.includes(kind === "trip" ? "/trips?" : "/standby?"))).toBe(true);
    });
    it.each([true, false])("labels missing standby rows with returned identity and Bangkok day (billing rows: %s)", async (billingRows) => {
        const web = setup(allCaps.filter((c) => billingRows || c !== "accounting:billing_document"));
        web.on("GET", "/api/go/v1/billing/rows", () => json(200, { data: { rows: [], unpriced: [{ id: "opaque-standby", rowType: "standby", driverName: "Driver One", deliveredAt: "2026-10-04T19:00:00Z", reason: "no_rate" }, { id: "empty-identity", rowType: "standby", deliveredAt: "2026-10-04T19:00:00Z", reason: "no_rate" }] } }));
        web.on("GET", "/api/go/v1/standby", () => json(200, { data: [{ id: "opaque-standby", driverName: "Driver One", truckLicensePlate: "AB-123", endedAt: "2026-10-04T19:00:00Z" }, { id: "empty-identity", endedAt: "2026-10-04T19:00:00Z" }] }));
        web.handlers.reverse();
        renderPage(<IncomePage />);
        await screen.findByText("TR-1");
        await userEvent.setup().click(screen.getByTestId("missing-billing-tab"));
        expect(await screen.findByText(billingRows ? /Driver One · 05\/10\/2026/ : /Driver One · AB-123 · 05\/10\/2026/)).toBeInTheDocument();
        expect(screen.getByText(/Standby · 05\/10\/2026/)).toBeInTheDocument();
        expect(screen.queryByText("opaque-s")).toBeNull();
    });
    it("keeps the view-income-only fallback monthly and customer-filtered when the page range changes", async () => {
        const web = setup(["accounting:view_income"]);
        const month = bangkokMonthRange();
        web.on("GET", "/api/go/v1/trips", (call) => {
            const query = new URL(call.url, "http://test").searchParams;
            return json(200, { data: [row(query.get("from") === month.from ? "monthly" : "range", null)] });
        });
        web.handlers.reverse();
        renderPage(<IncomePage />); const u = userEvent.setup();
        await screen.findByRole("combobox", { name: "Customer" });
        await u.click(screen.getByRole("button", { name: "Last 30 days" }));
        await u.click(screen.getByRole("combobox", { name: "Customer" }));
        await u.click(await screen.findByRole("option", { name: /CJ/ }));
        await u.click(screen.getByTestId("missing-billing-tab"));
        const fallback = await screen.findByTestId("missing-billing-loaded");
        expect(within(fallback).getByText("TR-monthly")).toBeInTheDocument();
        expect(within(fallback).queryByText("TR-range")).toBeNull();
        expect(web.count("GET", "/api/go/v1/billing/rows")).toBe(0);
        expect(web.calls.some((call) => {
            const query = new URL(call.url, "http://test").searchParams;
            return call.url.includes("/trips?") && query.get("from") === month.from && query.get("to") === month.to && query.get("customerId") === "p1";
        })).toBe(true);
    });
    it("mounts fresh backfill defaults from the current range and customer", async () => {
        setup(); renderPage(<IncomePage />);
        const u = userEvent.setup();
        await u.click(await screen.findByRole("button", { name: "Price or reprice records" }));
        const first = screen.getByRole("dialog");
        await u.click(within(first).getByRole("button", { name: "Close" }));
        await waitFor(() => expect(screen.getByRole("button", { name: "Price or reprice records" })).toHaveFocus());
        await u.click(screen.getByRole("button", { name: "Last 30 days" }));
        await u.click(screen.getByRole("combobox", { name: "Customer" }));
        await u.click(await screen.findByRole("option", { name: /CJ/ }));
        await u.click(screen.getByRole("button", { name: "Price or reprice records" }));
        const dialog = screen.getByRole("dialog");
        const range = bangkokLast30Days();
        expect(within(dialog).getByRole("button", { name: "Billing range (Asia/Bangkok)" })).toHaveTextContent(`${formatBillingDay(range.from)} – ${formatBillingDay(range.to)}`);
        expect(within(dialog).getByRole("combobox", { name: "Customer" })).toHaveTextContent("CJ");
    });
    it("edits and exports Bangkok time, disables unchanged save and never edits task.date", async () => {
        const web = setup();
        web.on("PATCH", "/api/go/v1/trips/1", () => json(200, { data: {} }));
        renderPage(<IncomePage />);
        const u = userEvent.setup();
        await screen.findAllByText("CJ — Customer One");
        await u.click(await screen.findByRole("button", { name: "Edit delivered time (Asia/Bangkok)" }));
        const input = await waitFor(() => { const el = screen.getByLabelText("Edit delivered time (Asia/Bangkok)"); expect(el.tagName).toBe("INPUT"); return el; });
        expect(input).toHaveValue("2026-10-05T02:00");
        expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
        expect(web.calls.filter((c) => c.method === "PATCH")).toHaveLength(0);
        fireEvent.change(input, { target: { value: "2026-10-05T03:00" } });
        await u.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(web.count("PATCH", "/api/go/v1/trips/1")).toBe(1));
        expect(web.calls.find((c) => c.method === "PATCH")?.body).toEqual({ deliveredAt: "2026-10-04T20:00:00.000Z" });
        expect(web.calls.some((c) => c.url.includes("/tasks/"))).toBe(false);
        await u.click(screen.getByRole("button", { name: "Export Excel" }));
        await waitFor(() => expect(xlsx.writeFile).toHaveBeenCalledOnce());
        expect(xlsx.rows.mock.calls[0][0][0]).toMatchObject({ "Delivered at (Asia/Bangkok)": "2026-10-05 02:00", "Billing date": "2026-10-05", "Trip type": "Normal trip", "Job type": "Primary" });
        expect(xlsx.sheet.mock.calls[0][2]).toBe("Billing amounts");
    });
    it("confirms each forced row recompute and cancel sends no write", async () => {
        const web = setup();
        web.on("POST", "/api/go/v1/trips/1/billing/compute", () => json(200, { data: { ok: true, billingDateMoved: true } }));
        const success = vi.spyOn(toast, "success");
        renderPage(<IncomePage />); const u = userEvent.setup();
        await screen.findAllByText("CJ — Customer One");
        await u.click(await screen.findByRole("button", { name: "Row actions" }));
        await u.click(await screen.findByRole("menuitem", { name: "Recompute the price" }));
        expect(screen.getByRole("dialog")).toHaveTextContent("TR-1");
        await u.click(screen.getByRole("button", { name: "Cancel" }));
        expect(web.count("POST", "/api/go/v1/trips/1/billing/compute")).toBe(0);
        await u.click(screen.getByRole("button", { name: "Row actions" }));
        await u.click(await screen.findByRole("menuitem", { name: "Recompute the price" }));
        await u.click(screen.getByRole("button", { name: "Reprice existing amounts" }));
        await waitFor(() => expect(success).toHaveBeenCalledWith("Billing amount updated and billing date moved."));
        expect(web.calls.find((c) => c.method === "POST")?.body).toEqual({ forceRecompute: true });
    });
});

describe("safe bounded backfill", () => {
    it.each(["rate-card", "utilities"])("seeds %s backfill with Bangkok month dates at a UTC month boundary", async (page) => {
        vi.useFakeTimers({ toFake: ["Date"] });
        vi.setSystemTime(new Date("2026-09-30T19:00:00Z"));
        try {
            const web = setup(["accounting:view_rate_card", "accounting:edit_rate_card"]);
            web.on("GET", "/api/go/v1/rate-card/entries", () => json(200, { data: [] }));
            renderPage(page === "rate-card" ? <RateCardPage /> : <BackfillUtilitiesPage />);
            if (page === "rate-card") await userEvent.setup().click(await screen.findByRole("button", { name: "Recompute billing" }));
            expect(await screen.findByRole("button", { name: "Billing range (Asia/Bangkok)" })).toHaveTextContent("01/10/2026 – 31/10/2026");
            await waitFor(() => expect(screen.getByRole("button", { name: "Backfill by date range" })).toBeEnabled());
            expect(screen.queryByRole("checkbox", { name: "Reprice existing billing amounts" })).toBeNull();
        } finally { vi.useRealTimers(); }
    });
    it("requires edit_rate_card to run even when force capability is present", async () => {
        const web = setup(["accounting:recompute_force"]); renderPage(panel());
        expect(await screen.findByText("Running a backfill requires permission to edit rate cards.")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Backfill by date range" })).toBeDisabled();
        expect(screen.getByRole("checkbox", { name: "Reprice existing billing amounts" })).toBeInTheDocument();
        expect(jobs(web)).toHaveLength(0);
    });
    it("allows ordinary jobs with edit_rate_card and hides force without recompute_force", async () => {
        const web = setup(["accounting:edit_rate_card"]);
        for (const kind of ["trips", "standby"]) {
            web.on("POST", `/api/go/v1/jobs/billing.backfill-${kind}`, () => json(202, { data: { jobId: kind } }));
            web.on("GET", `/api/go/v1/jobs/${kind}`, () => json(200, { data: { id: kind, status: "succeeded", result: { scanned: 500, eligible: 201, written: 200, skipped: 0, failed: 28, capped: true, failures: [{ tripId: "technical-only", error: "no_rate" }, { tripId: "hidden-id", tripNo: "TR-visible", error: "new_reason" }] } } }));
        }
        renderPage(panel()); const u = userEvent.setup();
        const run = await screen.findByRole("button", { name: "Backfill by date range" });
        await waitFor(() => expect(run).toBeEnabled());
        expect(screen.queryByRole("checkbox", { name: "Reprice existing billing amounts" })).toBeNull();
        await u.click(run);
        await screen.findAllByText("This job checked 500 or more rows. The range may be incomplete; narrow the date range.");
        expect(screen.getAllByText("This job reached the 200-write limit. Narrow the range to review remaining records.")).toHaveLength(2);
        expect(screen.getAllByText("And 26 more could not be priced.")).toHaveLength(2);
        expect(screen.getAllByText("Technical details")).toHaveLength(2);
        expect(screen.queryByText("hidden-id")).toBeNull();
        expect(screen.getAllByText(/TR-visible/)).toHaveLength(2);
        expect(jobs(web)).toHaveLength(2);
        for (const job of jobs(web)) expect(job.body).toEqual({ fromDateStr: "2026-10-01", toDateStr: "2026-10-31", customerId: "c1" });
    });
    it("confirms actual forced scope; cancel submits nothing and confirm starts one job per kind", async () => {
        const web = setup();
        for (const kind of ["trips", "standby"]) {
            web.on("POST", `/api/go/v1/jobs/billing.backfill-${kind}`, () => json(202, { data: { jobId: kind } }));
            web.on("GET", `/api/go/v1/jobs/${kind}`, () => json(200, { data: { id: kind, status: "running" } }));
        }
        renderPage(panel()); const u = userEvent.setup();
        await u.click(await screen.findByRole("checkbox", { name: "Reprice existing billing amounts" }));
        await u.click(screen.getByRole("button", { name: "Reprice existing amounts" }));
        const confirm = screen.getByRole("dialog");
        expect(confirm).toHaveTextContent("01/10/2026–31/10/2026");
        expect(confirm).toHaveTextContent("CJ — Customer One");
        expect(confirm).toHaveTextContent("200 rows per selected job");
        await u.click(within(confirm).getByRole("button", { name: "Cancel" }));
        expect(jobs(web)).toHaveLength(0);
        await u.click(screen.getByRole("button", { name: "Reprice existing amounts" }));
        await u.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Reprice existing amounts" }));
        await waitFor(() => expect(jobs(web)).toHaveLength(2));
        expect(jobs(web)[0].body).toEqual({ fromDateStr: "2026-10-01", toDateStr: "2026-10-31", customerId: "c1", forceRecompute: true });
        const keys = Object.keys(sessionStorage);
        expect(keys).toHaveLength(2);
        expect(keys.every((key) => key.includes("viewer-a") && key.includes("tenant-a") && key.includes("2026-10-01") && key.includes("c1"))).toBe(true);
        expect(keys.map((key) => sessionStorage.getItem(key)).sort()).toEqual(["standby", "trips"]);
    });
    it("restores the same viewer, tenant, kind and scope and reports it", async () => {
        const web = setup();
        const notice = vi.spyOn(toast, "info");
        sessionStorage.setItem(`billing-job:${JSON.stringify(["viewer-a", "tenant-a", "billing.backfill-trips", "2026-10-01", "2026-10-31", "c1"])}`, "same-job");
        web.on("GET", "/api/go/v1/jobs/same-job", () => json(200, { data: { id: "same-job", status: "running" } }));
        renderPage(panel());
        await waitFor(() => expect(web.count("GET", "/api/go/v1/jobs/same-job")).toBe(1));
        expect(notice).toHaveBeenCalledWith("Resumed tracking the previous job.");
        expect(jobs(web)).toHaveLength(0);
        expect(screen.getByRole("button", { name: "Backfill by date range" })).toBeDisabled();
    });
    it("attaches to an already running job and reports it instead of starting again", async () => {
        const web = setup(["accounting:edit_rate_card"]);
        const notice = vi.spyOn(toast, "info");
        for (const kind of ["trips", "standby"]) {
            web.on("POST", `/api/go/v1/jobs/billing.backfill-${kind}`, () => goErr(409, "already_exists", { jobId: `existing-${kind}` }));
            web.on("GET", `/api/go/v1/jobs/existing-${kind}`, () => json(200, { data: { id: `existing-${kind}`, status: "running" } }));
        }
        renderPage(panel()); const u = userEvent.setup();
        await waitFor(() => expect(screen.getByRole("button", { name: "Backfill by date range" })).toBeEnabled());
        await u.click(screen.getByRole("button", { name: "Backfill by date range" }));
        await waitFor(() => expect(web.count("GET", "/api/go/v1/jobs/existing-trips")).toBe(1));
        expect(notice).toHaveBeenCalledWith("A job is already running; tracking it now.");
        expect(jobs(web)).toHaveLength(2);
    });
    it.each([["viewer-b", "tenant-a"], ["viewer-a", "tenant-b"]])("does not restore another viewer or tenant (%s, %s)", async (viewer, tenant) => {
        const web = setup();
        const scope = ["viewer-a", "tenant-a", "billing.backfill-trips", "2026-10-01", "2026-10-31", "c1"];
        sessionStorage.setItem(`billing-job:${JSON.stringify(scope)}`, "private-job");
        const me = makeMe({ capabilities: allCaps });
        web.on("GET", "/api/go/v1/me", () => json(200, { data: { ...me, id: viewer, tenant: { ...me.tenant, id: tenant } } }));
        web.handlers.reverse();
        renderPage(panel());
        await waitFor(() => expect(screen.getByRole("button", { name: "Backfill by date range" })).toBeEnabled());
        expect(web.count("GET", "/api/go/v1/jobs/private-job")).toBe(0);
    });
});
