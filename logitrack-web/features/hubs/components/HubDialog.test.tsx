// UI8 (issue #184): the hub dialog (sources page) picks a hub's customer link with the server-searched
// `CustomerSearchSelect` and names the stored link by id (`['customers','ref',id]`, one `GET /v1/customers?ids=`),
// instead of listing every customer.
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("@/components/map/SimpleMap", () => ({ default: () => null }));

import { HubDialog } from "@/app/app/first-mile/hub-dialog";
import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { customerDirectoryReads, customerLookups, customersReply, fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { stubVirtualLayout } from "@/test-utils/virtualLayout";

const pad = (i: number) => String(i).padStart(4, "0");
const CUSTOMERS = Array.from({ length: 120 }, (_, i) => ({ id: `c-${pad(i)}`, billingPartyId: `p-${pad(i)}`, code: `C${pad(i)}`, name: `ลูกค้า ${pad(i)}` }));

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

describe("hub dialog: the customer link by search and by id (UI8)", () => {
    it("names the stored customer by id, picks another by search and saves its id", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities: ["operations:manage_sources"] }) }));
        web.on("GET", "/api/go/v1/subcontractors", () => json(200, { data: [] }));
        web.on("GET", "/api/go/v1/customers", customersReply(CUSTOMERS));
        web.on("PATCH", "/api/go/v1/hubs/h1", () => json(200, { data: {} }));
        const onSuccess = vi.fn();
        renderWithProviders(
            <RouteNamespaces pathname="/app/sources">
                <HubDialog
                    open
                    onOpenChange={() => undefined}
                    onSuccess={onSuccess}
                    documentId="h1"
                    defaultValues={{ source_id: "SPX-B", source_name_th: "ลาดกระบัง", source_name_en: "Lat Krabang", latitude: 13.7, longitude: 100.7, station_type: "HUB", linkedCustomerId: "c-0119", customerLinkKind: "customer" }}
                />
            </RouteNamespaces>,
            getQueryClient()
        );
        // c-0119 is off the picker's first page: named by one id lookup, no list.
        const picker = await screen.findByRole("combobox", { name: "Reference" });
        await waitFor(() => expect(picker).toHaveTextContent("ลูกค้า 0119"));
        expect(customerLookups(web.calls)).toEqual([["c-0119"]]);

        const u = userEvent.setup();
        await u.click(picker);
        await u.type(await screen.findByPlaceholderText("Search..."), "C0005");
        await u.click(await screen.findByRole("option", { name: /ลูกค้า 0005/ }));
        expect(picker).toHaveTextContent("ลูกค้า 0005");
        await u.click(screen.getByRole("button", { name: "Save Source" }));
        await waitFor(() => expect(onSuccess).toHaveBeenCalled());
        expect(web.calls.find((c) => c.method === "PATCH")!.body).toMatchObject({ sourceId: "SPX-B", linkedCustomerId: "c-0005", linkedCustomerKind: "customer" });
        expect(customerDirectoryReads(web.calls)).toEqual([]);
    });
});
