// UI8 (issue #184): the sources page (`/app/sources`) no longer reads every customer. The hubs' linked customers are
// named by id (`useCustomerLookup`: the distinct customer ids of the master in one `GET /v1/customers?ids=`), so the
// table search still finds a hub by its customer's code; the customer filter is the server-searched picker with
// its "All customers" and "Not linked" rows.
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("@/components/map/SourcesMap", () => ({ default: () => null }));

import SourcesPage from "@/app/app/sources/page";
import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { customerDirectoryReads, customerLookups, customersReply, fakeWeb, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { stubVirtualLayout } from "@/test-utils/virtualLayout";

const pad = (i: number) => String(i).padStart(4, "0");
const CUSTOMERS = Array.from({ length: 120 }, (_, i) => ({ id: `c-${pad(i)}`, billingPartyId: `p-${pad(i)}`, code: `C${pad(i)}`, name: `ลูกค้า ${pad(i)}` }));

const hub = (id: string, sourceId: string, name: string, link: { id: string; kind: "customer" | "partner" } | null) => ({
    id, sourceId, sourceNameTh: name, sourceNameEn: name, latitude: 13.7, longitude: 100.5, stationType: "HUB", network: "SPX",
    linkedPartyId: null, linkedCustomerId: link?.id ?? null, linkedCustomerName: null, linkedCustomerKind: link?.kind ?? null,
    createdByDriver: false, updatedAt: "2026-10-01T00:00:00Z",
});
// c-0119 is off the picker's first page; one hub links a carrier, one nothing.
const HUBS = [
    hub("h1", "SPX-A", "บางนา", { id: "c-0001", kind: "customer" }),
    hub("h2", "SPX-B", "ลาดกระบัง", { id: "c-0119", kind: "customer" }),
    hub("h3", "SPK-1", "บางปู", { id: "t-car", kind: "partner" }),
    hub("h4", "SPX-C", "ไม่ผูก", null),
    hub("h5", "SPX-D", "บางพลี", { id: "c-0001", kind: "customer" }),
];

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

function setup() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities: ["operations:manage_sources"] }) }));
    web.on("GET", "/api/go/v1/hubs", () => json(200, { data: HUBS }));
    web.on("GET", "/api/go/v1/distances/meta", () => json(200, { data: { lastCalculatedAt: null } }));
    web.on("GET", "/api/go/v1/subcontractors", () => json(200, { data: [{ id: "t-car", code: "CAR", nameTh: "ขนส่งเอ", nameEn: null, status: "active" }] }));
    web.on("GET", "/api/go/v1/customers", customersReply(CUSTOMERS));
    return web;
}

const rowIds = () => ["SPX-A", "SPX-B", "SPK-1", "SPX-C", "SPX-D"].filter((id) => screen.queryByText(id));

describe("sources page: customers by id and by search, never every customer (UI8)", () => {
    it("names the linked customers in one ids lookup, searches the table by a customer's code, and filters with the server picker", async () => {
        const web = setup();
        renderWithProviders(
            <RouteNamespaces pathname="/app/sources">
                <SourcesPage />
            </RouteNamespaces>,
            getQueryClient()
        );
        await waitFor(() => expect(rowIds()).toEqual(["SPX-A", "SPX-B", "SPK-1", "SPX-C", "SPX-D"]));
        // The distinct customer links of the master, once; the carrier link is not a customer.
        await waitFor(() => expect(customerLookups(web.calls)).toEqual([["c-0001", "c-0119"]]));
        expect(customerDirectoryReads(web.calls)).toEqual([]);

        // The table search matches the linked customer's code and the carrier's code.
        const u = userEvent.setup();
        const search = screen.getByPlaceholderText("Search pickup-delivery points...");
        await u.type(search, "c0119");
        await waitFor(() => expect(rowIds()).toEqual(["SPX-B"]));
        await u.clear(search);
        await u.type(search, "car");
        await waitFor(() => expect(rowIds()).toEqual(["SPK-1"]));
        await u.clear(search);

        // The filter: "All customers", "Not linked", then the server's first page; a Thai term is one search.
        const picker = screen.getByRole("combobox", { name: "Customer" });
        expect(picker).toHaveTextContent("All customers");
        await u.click(picker);
        await waitFor(() => expect(screen.getAllByRole("option").length).toBeGreaterThan(2));
        expect(screen.getAllByRole("option").slice(0, 2).map((o) => o.textContent)).toEqual(["All customers", "Not linked"]);
        await u.click(screen.getByRole("option", { name: "Not linked" }));
        await waitFor(() => expect(rowIds()).toEqual(["SPX-C"]));
        expect(picker).toHaveTextContent("Not linked");

        await u.click(picker);
        await u.type(await screen.findByPlaceholderText("Search..."), "ลูกค้า 0119");
        await waitFor(() => expect(screen.getAllByRole("option").map((o) => o.textContent)).toContain("ลูกค้า 0119C0119"));
        await u.click(screen.getByRole("option", { name: /ลูกค้า 0119/ }));
        await waitFor(() => expect(rowIds()).toEqual(["SPX-B"]));

        const reads = web.calls.filter((c) => c.url.split("?")[0] === "/api/go/v1/customers").map((c) => Object.fromEntries(params(c)));
        expect(reads.filter((p) => !("ids" in p))).toEqual([
            { fields: "minimal", limit: "50" },
            { fields: "minimal", q: "ลูกค้า 0119", limit: "50" },
        ]);
        expect(customerDirectoryReads(web.calls)).toEqual([]);
    });
});
