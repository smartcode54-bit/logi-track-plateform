// UI2C (follow-up of issue #142): the task form's customer is the shared server-searched picker
// (`CustomerPartySelect`, `GET /v1/customers?fields=minimal&q`), not a filter over every customer in the browser.
// Its value is the billing party the task holds: an edit shows the task's customer although it is not on the
// picker's first page, a Thai term is one debounced server search, and the patch sends the picked party.
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { fakeWeb, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { stubVirtualLayout } from "@/test-utils/virtualLayout";

import { TaskFormDialog } from "./TaskFormDialog";
import type { TaskDTO } from "../../api/tasks.go";

const pad = (i: number) => String(i).padStart(4, "0");
// 120 customers ordered by code, as Go orders them; customer 3 has no billing party (it cannot bill a task).
const CUSTOMERS = Array.from({ length: 120 }, (_, i) => ({
    id: `c-${pad(i)}`,
    billingPartyId: i === 3 ? null : `p-${pad(i)}`,
    code: `C${pad(i)}`,
    name: `ลูกค้า ${pad(i)}`,
}));

const params = (call: FakeCall) => new URL(call.url, "http://web.test").searchParams;

let restoreLayout: () => void;
beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
    restoreLayout = stubVirtualLayout({ viewport: 480, row: 32 });
});
afterAll(() => restoreLayout());
afterEach(() => {
    resetQueryClientForTests();
    vi.unstubAllGlobals();
});

function setup() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities: ["operations:view_first_mile", "operations:manage_tasks"] }) }));
    web.on("GET", "/api/go/v1/hubs", () =>
        json(200, { data: [
            { id: "h1", sourceId: "ALANG-A", sourceNameTh: "วังทองหลาง", sourceNameEn: null, stationType: "HUB" },
            { id: "h2", sourceId: "SOCE", sourceNameTh: "บัวโรย", sourceNameEn: null, stationType: "SOC" },
        ] }));
    // Go's minimal list: `q` a case-insensitive substring of the code or the name, keyset pages of `limit`.
    web.on("GET", "/api/go/v1/customers", (call) => {
        const q = (params(call).get("q") ?? "").toLowerCase();
        const limit = Number(params(call).get("limit") ?? "50");
        const from = Number(params(call).get("cursor") ?? "0");
        const hits = CUSTOMERS.filter((c) => !q || `${c.code} ${c.name}`.toLowerCase().includes(q));
        const next = from + limit < hits.length ? String(from + limit) : undefined;
        return json(200, { data: hits.slice(from, from + limit), ...(next ? { nextCursor: next } : {}) });
    });
    // The picker's pages (`limit=50`), as opposed to the label lookups.
    const searches = () => web.calls.filter((c) => c.url.startsWith("/api/go/v1/customers") && params(c).get("limit") === "50").map((c) => params(c).get("q"));
    return { web, searches };
}

function task(over: Partial<TaskDTO> = {}): TaskDTO {
    const place = (label: string) => ({ raw: label, key: `code:${label}`, label, resolved: true });
    return {
        id: "t1", tenantId: "o", taskNo: "FM-12102026-001", taskType: "first_mile", jobCategory: "PRIMARY", status: "pending", planAt: "2026-10-11T17:00:00Z",
        planDate: "2026-10-12", planTime: "09:30", actualPickupAt: null, sourceHub: "ALANG-A", sourceHubId: "h1", source: place("ALANG-A"),
        destination: "SOCE", destinationHubId: null, destinationSocKey: "SOCE", destinationPlace: place("SOCE"), truckId: null, truckType: "6WH",
        licensePlate: null, driverId: null, driverName: null, helperDriverId: null, helperName: null, runOrder: null, checkInAt: null, checkInLat: null,
        checkInLng: null, isMultiDelivery: false, stops: [], trip: null, createdAt: "2026-10-11T00:00:00Z", updatedAt: "2026-10-11T00:00:00Z",
        billingPartyId: "p-0099", ...over,
    };
}

const renderForm = (t: TaskDTO, onClose = vi.fn()) => {
    window.history.replaceState(null, "", "/app/first-mile");
    renderWithProviders(
        <RouteNamespaces pathname="/app/first-mile">
            <TaskFormDialog mode="edit" taskType="first_mile" task={t} allowTypeSwitch={false} onClose={onClose} />
        </RouteNamespaces>,
        getQueryClient()
    );
    return onClose;
};

describe("task form: the customer picker searches on the server (UI2C)", () => {
    it("labels the task's customer off the first page, searches a Thai term once, and patches the picked party", async () => {
        const { web, searches } = setup();
        web.on("PATCH", "/api/go/v1/tasks/t1", () => json(200, { data: task({ billingPartyId: "p-0100" }) }));
        const onClose = renderForm(task());
        const picker = await screen.findByRole("combobox", { name: "Customer" });
        // The task holds party p-0099: the picker names it without opening (and without a picker page).
        await waitFor(() => expect(picker).toHaveTextContent("ลูกค้า 0099"));
        expect(searches()).toEqual([]);

        const u = userEvent.setup();
        await u.click(picker);
        // The first page is the first 50 by code: customer 0099 is not on it, and the trigger still names it.
        await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(50));
        expect(screen.queryByRole("option", { name: /ลูกค้า 0099/ })).toBeNull();
        expect(picker).toHaveTextContent("ลูกค้า 0099");

        await u.type(screen.getByPlaceholderText("Search..."), "ลูกค้า 0100");
        await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(1));
        // One request for the whole Thai term (debounced), never a filter over a full list.
        expect(searches()).toEqual([null, "ลูกค้า 0100"]);
        await u.click(screen.getByRole("option", { name: /ลูกค้า 0100/ }));
        expect(picker).toHaveTextContent("ลูกค้า 0100");

        await u.click(screen.getByRole("button", { name: "Save Changes" }));
        await waitFor(() => expect(onClose).toHaveBeenCalled());
        expect(web.calls.filter((c) => c.method === "PATCH").map((c) => c.body)).toEqual([{ billingPartyId: "p-0100" }]);
    });

    it("lists a customer without a billing party but does not let it be picked", async () => {
        setup();
        renderForm(task({ billingPartyId: null }));
        const u = userEvent.setup();
        await u.click(await screen.findByRole("combobox", { name: "Customer" }));
        await u.type(screen.getByPlaceholderText("Search..."), "C0003");
        const option = await screen.findByRole("option", { name: /ลูกค้า 0003/ });
        expect(option).toHaveAttribute("aria-disabled", "true");
        await u.click(option);
        expect(within(screen.getByRole("combobox", { name: "Customer" })).queryByText("ลูกค้า 0003")).toBeNull();
    });
});
