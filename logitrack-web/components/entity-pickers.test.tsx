// UI2 (issue #142): the server-searched pickers (drivers, trucks, hubs, customers) handle 5,000 records with
// fewer than 100 rows in the DOM. A fake Go origin holds 5,000 of each and answers `q` as Go does (a
// case-insensitive substring; a page of `limit` rows; hubs: every match, Go filters its cache). The typed
// term is debounced into one request and the picked value keeps its label. DriverSelect, TruckSelect and the
// customer pickers are the product's pickers (the pages' tests: UI2C); HubSelect has no page caller yet
// (follow-up of #142).
import * as React from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { fakeWeb, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { stubVirtualLayout } from "@/test-utils/virtualLayout";
import { CustomerSearchSelect, DriverSelect, HubSelect, TruckSelect } from "./entity-pickers";

const N = 5000;
const pad = (i: number) => String(i).padStart(4, "0");
const DRIVERS = Array.from({ length: N }, (_, i) => ({ id: `d-${i}`, displayName: `คนขับ ${pad(i)}` }));
const TRUCKS = Array.from({ length: N }, (_, i) => ({ id: `t-${i}`, licensePlate: `70-${pad(i)}`, type: i % 2 ? "6WH" : "4W" }));
const HUBS = Array.from({ length: N }, (_, i) => ({
    id: `h-${i}`,
    sourceId: `HUB-${pad(i)}`,
    sourceNameTh: `ฮับ ${pad(i)}`,
    sourceNameEn: `Hub ${pad(i)}`,
    stationType: "HUB",
}));
const CUSTOMERS = Array.from({ length: N }, (_, i) => ({ id: `c-${i}`, billingPartyId: `p-${i}`, code: `C${pad(i)}`, name: `ลูกค้า ${pad(i)}` }));

const params = (call: FakeCall) => new URL(call.url, "http://web.test").searchParams;

function serve<T>(rows: T[], text: (r: T) => string, paged: boolean) {
    return (call: FakeCall) => {
        const q = (params(call).get("q") ?? "").toLowerCase();
        const limit = Number(params(call).get("limit") ?? "50");
        const hits = rows.filter((r) => !q || text(r).toLowerCase().includes(q));
        return json(200, { data: paged ? hits.slice(0, limit) : hits });
    };
}

function setup() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }));
    web.on("GET", "/api/go/v1/drivers", serve(DRIVERS, (d) => d.displayName, true));
    web.on("GET", "/api/go/v1/trucks", serve(TRUCKS, (t) => t.licensePlate, true));
    web.on("GET", "/api/go/v1/hubs", serve(HUBS, (h) => `${h.sourceId} ${h.sourceNameTh} ${h.sourceNameEn}`, false));
    web.on("GET", "/api/go/v1/customers", serve(CUSTOMERS, (c) => `${c.code} ${c.name}`, true));
    return web;
}

let restore: () => void;
beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
    restore = stubVirtualLayout({ viewport: 300, row: 32 });
});
afterAll(() => restore());
afterEach(() => {
    vi.unstubAllGlobals();
});

function Harness({ kind, onChange }: { kind: "driver" | "truck" | "hub" | "customer"; onChange: (v: string) => void }) {
    const [value, setValue] = React.useState("");
    const props = {
        "aria-label": kind,
        value,
        onValueChange: (v: string) => {
            setValue(v);
            onChange(v);
        },
    };
    if (kind === "driver") return <DriverSelect {...props} />;
    if (kind === "truck") return <TruckSelect {...props} />;
    if (kind === "hub") return <HubSelect {...props} />;
    return <CustomerSearchSelect {...props} />;
}

const rows = () => document.querySelectorAll('[data-slot="search-select-option"]').length;

describe.each([
    { kind: "driver" as const, path: "/api/go/v1/drivers", term: "4999", label: "คนขับ 4999", value: "d-4999" },
    { kind: "truck" as const, path: "/api/go/v1/trucks", term: "70-4999", label: "70-4999", value: "t-4999" },
    { kind: "hub" as const, path: "/api/go/v1/hubs", term: "HUB-4999", label: "ฮับ 4999", value: "HUB-4999" },
    { kind: "customer" as const, path: "/api/go/v1/customers", term: "C4999", label: "ลูกค้า 4999", value: "c-4999" },
])("$kind picker over 5,000 records", ({ kind, path, term, label, value }) => {
    it("searches on the server with a debounced q, keeps under 100 rows in the DOM, and picks", async () => {
        const web = setup();
        const onChange = vi.fn();
        renderWithProviders(<Harness kind={kind} onChange={onChange} />);
        // Nothing is read before the list opens.
        expect(web.count("GET", path)).toBe(0);
        const u = userEvent.setup();
        await u.click(await screen.findByRole("combobox", { name: kind }));
        await waitFor(() => expect(rows()).toBeGreaterThan(0));
        expect(rows()).toBeLessThan(100);
        const first = web.calls.filter((c) => c.url.startsWith(path));
        expect(first).toHaveLength(1);
        expect(params(first[0]).get("q")).toBeNull();

        await u.type(screen.getByPlaceholderText("Search..."), term);
        await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(1));
        const searched = web.calls.filter((c) => c.url.startsWith(path) && params(c).get("q"));
        // one request for the whole term (debounced), not one per keystroke
        expect(searched.map((c) => params(c).get("q"))).toEqual([term]);

        await u.click(screen.getByRole("option", { name: new RegExp(label) }));
        expect(onChange).toHaveBeenCalledWith(value);
        expect(screen.getByRole("combobox", { name: kind })).toHaveTextContent(label);
    });
});

describe("selected value outside the current page", () => {
    it("keeps the label the caller knows", async () => {
        setup();
        renderWithProviders(<DriverSelect aria-label="Driver" value="d-4321" selectedLabel="คนขับ 4321" onValueChange={vi.fn()} />);
        expect(await screen.findByRole("combobox", { name: "Driver" })).toHaveTextContent("คนขับ 4321");
    });
});
