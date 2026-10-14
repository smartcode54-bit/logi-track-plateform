// UI2C (follow-up of issue #142): the rate card's customer filter is the shared server-searched picker
// (`CustomerSearchSelect`, `GET /v1/customers?fields=minimal&q`) and its tables name their rows' customers by id
// (`useCustomerLookup`), so the page never reads every customer to show a filter:
// - nothing about customers is read while the "all" view has no rows; a Thai term is one debounced search; the
//   picked customer filters the entries on the server and keeps its name (from the search page, no lookup);
// - a fee's customer off the picker's first page is named in its row and in the edit dialog's picker.
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { fakeWeb, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { stubVirtualLayout } from "@/test-utils/virtualLayout";

import { RateCardPage } from "./RateCardPage";

const pad = (i: number) => String(i).padStart(4, "0");
const CUSTOMERS = Array.from({ length: 120 }, (_, i) => ({ id: `c-${pad(i)}`, billingPartyId: `p-${pad(i)}`, code: `C${pad(i)}`, name: `ลูกค้า ${pad(i)}` }));

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

const entry = (customerId: string) => ({
    id: `e-${customerId}`, customerId, billingPartyId: customerId.replace("c-", "p-"), hubId: "SPX-A", rawHubName: "SPX-A - Bang Na",
    destinationCode: "SOCE", vehicleClass: "4WJ", rateThb: 1500, jobCategory: "PRIMARY", effectiveFromDateStr: "2026-09-01", importId: "imp-1", voided: false,
});

function setup() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities: ["accounting:view_rate_card", "accounting:edit_rate_card"] }) }));
    web.on("GET", "/api/go/v1/hubs", () => json(200, { data: [{ id: "h1", sourceId: "SPX-A", sourceNameTh: "บางนา", sourceNameEn: "Bang Na", stationType: "HUB" }] }));
    web.on("GET", "/api/go/v1/customers", (call) => {
        const q = (params(call).get("q") ?? "").toLowerCase();
        const limit = Number(params(call).get("limit") ?? "50");
        const from = Number(params(call).get("cursor") ?? "0");
        const hits = CUSTOMERS.filter((c) => !q || `${c.code} ${c.name}`.toLowerCase().includes(q));
        const next = from + limit < hits.length ? String(from + limit) : undefined;
        return json(200, { data: hits.slice(from, from + limit), ...(next ? { nextCursor: next } : {}) });
    });
    // The "all" view has no rates yet; one customer has one.
    web.on("GET", "/api/go/v1/billing/rate-entries", (call) => {
        const cust = params(call).get("customerId");
        return json(200, { data: cust ? [entry(cust)] : [] });
    });
    const customerCalls = () => web.calls.filter((c) => c.url.startsWith("/api/go/v1/customers"));
    return { web, customerCalls };
}

const renderPage = () => {
    window.history.replaceState(null, "", "/app/accounting/rate-card");
    renderWithProviders(
        <RouteNamespaces pathname="/app/accounting/rate-card">
            <RateCardPage />
        </RouteNamespaces>,
        getQueryClient()
    );
};

describe("rate card: the customer picker searches on the server (UI2C)", () => {
    it("reads no customer for the empty all view, searches a Thai term once and filters the entries by the pick", async () => {
        const { web, customerCalls } = setup();
        renderPage();
        const picker = await screen.findByRole("combobox", { name: "Customer" });
        expect(picker).toHaveTextContent("All customers");
        await screen.findByText("No rate card entries found.");
        expect(customerCalls()).toEqual([]);

        const u = userEvent.setup();
        await u.click(picker);
        await waitFor(() => expect(screen.getAllByRole("option").length).toBeGreaterThan(1));
        // "All customers" first, then the server's first page.
        expect(screen.getAllByRole("option")[0]).toHaveTextContent("All customers");
        await u.type(screen.getByPlaceholderText("Search..."), "ลูกค้า 0110");
        await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(2));
        expect(customerCalls().map((c) => params(c).get("q"))).toEqual([null, "ลูกค้า 0110"]);
        await u.click(screen.getByRole("option", { name: /ลูกค้า 0110/ }));

        expect(picker).toHaveTextContent("ลูกค้า 0110");
        await waitFor(() => expect(web.calls.some((c) => c.url.startsWith("/api/go/v1/billing/rate-entries") && params(c).get("customerId") === "c-0110")).toBe(true));
        expect(await screen.findByText("SPX-A - Bang Na")).toBeInTheDocument();
        // The picked customer's name came with the search page: no lookup, no read of every customer.
        expect(customerCalls()).toHaveLength(2);
    });

    it("names a fee's customer that is not on the picker's first page, in the row and in the edit dialog", async () => {
        const { web, customerCalls } = setup();
        web.on("GET", "/api/go/v1/billing/service-fees", () =>
            json(200, { data: [
                { id: "f1", customerId: "c-0119", billingPartyId: "p-0119", feeType: "extra_stop", customTypeName: null, amountThb: 300, unit: "per_stop", note: null },
                { id: "f2", customerId: "c-0001", billingPartyId: "p-0001", feeType: "extra_stop", customTypeName: null, amountThb: 200, unit: "per_stop", note: null },
                { id: "f3", customerId: "c-0119", billingPartyId: "p-0119", feeType: "waiting_time", customTypeName: null, amountThb: 100, unit: "per_trip", note: null },
            ] })
        );
        renderPage();
        const u = userEvent.setup();
        await u.click(await screen.findByRole("tab", { name: "Service Fees" }));
        await waitFor(() => expect(screen.getAllByText("C0119 — ลูกค้า 0119")).toHaveLength(2));
        expect(screen.getByText("C0001 — ลูกค้า 0001")).toBeInTheDocument();
        // Two customers on three rows: one lookup each; no picker page was asked for.
        expect(getQueryClient().getQueryCache().findAll({ queryKey: ["customers", "ref"] })).toHaveLength(2);
        expect(customerCalls().filter((c) => params(c).get("limit") === "50")).toEqual([]);

        const row = screen.getAllByText("C0119 — ลูกค้า 0119")[0].closest("tr")!;
        await u.click(within(row).getByRole("button", { name: "Edit" }));
        const dialog = await screen.findByRole("dialog");
        const picker = within(dialog).getByRole("combobox", { name: "Customer" });
        expect(picker).toBeDisabled();
        expect(picker).toHaveTextContent("ลูกค้า 0119");
        expect(web.count("GET", "/api/go/v1/billing/service-fees")).toBe(1);
    });
});
