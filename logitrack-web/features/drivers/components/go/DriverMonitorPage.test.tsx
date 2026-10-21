import React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { differenceInCalendarDays, format, parseISO } from "date-fns";

import { useLanguage } from "@/context/language";
import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import type { MeDTO } from "@/features/auth/api/me";
import { getQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { fakeWeb, goErr, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { stubVirtualLayout } from "@/test-utils/virtualLayout";
import type { MonitorRowDTO } from "../../api/monitor.go";
import { DriverMonitorPage } from "./DriverMonitorPage";

const place = (label: string) => ({ raw: label, key: `code:${label}`, label, resolved: true });
const row: MonitorRowDTO = {
    id: "trip-1", tenantId: "own", tripNo: "FM-123", status: "delivered", jobType: "first_mile", jobCategory: "PRIMARY",
    createdAt: "2026-10-12T02:00:00Z", updatedAt: "2026-10-12T06:00:00Z", deliveredAt: "2026-10-12T06:00:00Z",
    taskId: null, taskNo: null, taskStatus: null, checkInAt: null, runOrder: null, planAt: null, driverId: "driver-1", driverName: "Driver One",
    origin: place("ALANG-A"), destination: place("SOCE"), stops: [
        { index: 1, place: place("SOCE"), status: "delivered", deliveredAt: "2026-10-12T05:00:00Z", completedSeq: 1 },
        { index: 2, place: place("SOCN"), status: "delivered", deliveredAt: "2026-10-12T06:00:00Z", completedSeq: 2 },
    ],
    truck: { truckId: "truck-1", plate: "AB-1234", key: "truck:1", vehicleClass: "6WH", vehicleClassKey: "6WH" },
    partnerCode: null, sealCode: null, isMultiDelivery: true, incidentCount: 0, photoCount: 3,
};
const facets = { total: 412, statusCounts: { delivered: 400, in_transit: 12 }, incident: 2,
    origins: [], destinations: [], plates: [], vehicleClasses: [], partners: [] };
const caps = ["operations:view_driver_monitor"];
const customer = makeMe({ tenant: null, tenants: [], steward: false, customerScopes: [{ billingPartyId: "c1", name: "Customer" }], capabilities: caps });
const partner = makeMe({ tenant: { id: "carrier", nameTh: "รถร่วม", nameEn: "Partner", kind: "carrier", role: "tenant_admin" }, steward: false, capabilities: caps });
const params = (call: FakeCall) => Object.fromEntries(new URL(call.url, "http://web.test").searchParams);
let restoreLayout: () => void;
let resizeListeners: Set<() => void>;
function resize(width: number) {
    act(() => {
        vi.stubGlobal("innerWidth", width);
        resizeListeners.forEach((listener) => listener());
        fireEvent(window, new Event("resize"));
    });
}
beforeEach(() => {
    vi.stubGlobal("innerWidth", 375);
    resizeListeners = new Set();
    vi.spyOn(window, "matchMedia").mockImplementation((query) => ({
        get matches() {
            const maxWidth = query.match(/max-width:\s*(\d+)px/);
            if (maxWidth) return window.innerWidth <= Number(maxWidth[1]);

            const minWidth = query.match(/min-width:\s*(\d+)px/);
            if (minWidth) return window.innerWidth >= Number(minWidth[1]);

            return false;
        },
        media: query, onchange: null,
        addEventListener: (_type: string, listener: () => void) => { resizeListeners.add(listener); },
        removeEventListener: (_type: string, listener: () => void) => { resizeListeners.delete(listener); },
        addListener: () => {}, removeListener: () => {}, dispatchEvent: () => true,
    } as unknown as MediaQueryList));
    restoreLayout = stubVirtualLayout({ viewport: 480, row: 260, width: 375 });
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
});
afterEach(() => {
    restoreLayout();
    resetQueryClientForTests();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

function LanguageSwitch() {
    const { setLanguage } = useLanguage();
    return <button onClick={() => setLanguage("th")}>Thai</button>;
}
function setup(principal: MeDTO = customer, reply: (call: FakeCall) => Response | Promise<Response> = () => json(200, { data: [row], meta: { facets } })) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: principal }));
    web.on("GET", "/api/go/v1/trips/monitor", reply);
    web.on("GET", "/api/go/v1/trips/trip-1", () => json(200, { data: { ...row, photos: [], stops: [] } }));
    window.history.replaceState(null, "", "/app/driver-monitor");
    const view = renderWithProviders(<RouteNamespaces pathname="/app/driver-monitor"><DriverMonitorPage /><LanguageSwitch /></RouteNamespaces>, getQueryClient());
    return { ...view, web, calls: () => web.calls.filter((c) => c.url.startsWith("/api/go/v1/trips/monitor")) };
}

describe("scoped monitor presentation", () => {
    it.each([[customer, "Your trips"], [partner, "Fleet trips"], [makeMe({ capabilities: caps }), "Driver monitor"]] as const)("phone profile has collapsed panels and one named trip button (%s)", async (principal, title) => {
        const { calls, client } = setup(principal);
        expect(await screen.findByRole("heading", { level: 1, name: title })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Trip summary" })).toHaveAttribute("aria-expanded", "false");
        expect(screen.getByRole("button", { name: "More filters" })).toHaveAttribute("aria-expanded", "false");
        expect(screen.getByText("You can select at most 366 days at a time; wider selections are clamped automatically.")).toBeVisible();
        expect(screen.queryByRole("textbox", { name: "Search trips" })).toBeNull();
        expect(screen.queryByText("Total Trips")).toBeNull();
        const dateRange = screen.getByRole("button", { name: /^Date range \d{2}\/\d{2}\/\d{4} – \d{2}\/\d{2}\/\d{4}$/ });
        expect(dateRange).toHaveClass("h-11", "w-full");
        const region = screen.getByRole("region", { name: title });
        expect(within(region).getAllByRole("columnheader")).toHaveLength(1);
        expect(await within(region).findByRole("button", { name: "View trip FM-123 and evidence" })).toBeInTheDocument();
        expect(region).toHaveTextContent("Delivered");
        expect(region).toHaveTextContent("ALANG-A");
        expect(region).toHaveTextContent("SOCN");
        expect(region).toHaveTextContent("2 stops");
        expect(region).toHaveTextContent("AB-1234");
        expect(region).toHaveTextContent("Driver One");
        expect(region).toHaveTextContent("3 photos");
        expect(calls()).toHaveLength(1);
        const request = params(calls()[0]);
        expect(Object.keys(request).sort()).toEqual(["from", "limit", "to"]);
        expect(request.limit).toBe("200");
        expect(differenceInCalendarDays(parseISO(request.to), parseISO(request.from)) + 1).toBe(30);
        expect(client.getQueryCache().findAll({ queryKey: ["trips", "monitor"] })).toHaveLength(1);
    });

    it("opens the existing evidence dialog on its named button without requesting detail before the click", async () => {
        const { web } = setup();
        const button = await screen.findByRole("button", { name: "View trip FM-123 and evidence" });
        expect(web.count("GET", "/api/go/v1/trips/trip-1")).toBe(0);
        await userEvent.click(button);
        expect(await screen.findByRole("dialog", { name: /Trip Details/ })).toBeInTheDocument();
        expect(await screen.findByText("No photos available")).toBeInTheDocument();
        expect(web.count("GET", "/api/go/v1/trips/trip-1")).toBe(1);
        expect(screen.queryByRole("button", { name: "Edit partner / trip" })).toBeNull();
    });

    it("shows the incident indicator beside the phone status badge", async () => {
        setup(customer, () => json(200, { data: [{ ...row, incidentCount: 1 }], meta: { facets } }));
        const region = await screen.findByRole("region", { name: "Your trips" });
        expect(await within(region).findByLabelText("Incident")).toBeInTheDocument();
        expect(within(region).getByText("Delivered")).toBeInTheDocument();
    });

    it("retains filters, driver labels and the query across collapse and the 767/768 boundary", async () => {
        const { calls, web } = setup();
        web.on("GET", "/api/go/v1/drivers", () => json(200, { data: [{ id: "driver-1", displayName: "Driver One" }] }));
        await screen.findByRole("heading", { level: 1 });
        const u = userEvent.setup();
        await u.click(screen.getByRole("button", { name: "More filters" }));
        await u.click(screen.getByText("All drivers").closest("button")!);
        await u.click(await screen.findByRole("option", { name: "Driver One" }));
        await u.click(screen.getByRole("button", { name: "First Mile" }));
        await u.type(screen.getByRole("textbox", { name: "Search trips" }), "FM");
        await waitFor(() => expect(params(calls().at(-1)!)).toMatchObject({ jobType: "first_mile", q: "FM", driverId: "driver-1" }));
        const before = calls().length;
        await u.click(screen.getByRole("button", { name: /More filters/ }));
        expect(screen.queryByRole("textbox", { name: "Search trips" })).toBeNull();
        expect(screen.getByRole("button", { name: "Filters (3)" })).toBeInTheDocument();
        resize(768);
        expect(screen.getByRole("button", { name: /^Date range \d{2}\/\d{2}\/\d{4} – \d{2}\/\d{2}\/\d{4}$/ })).toBeVisible();
        expect(screen.getByText("Trips and delivery evidence in your scope.")).toBeInTheDocument();
        expect(screen.getByRole("textbox", { name: "Search trips" })).toHaveValue("FM");
        expect(screen.getAllByRole("combobox").some((control) => control.textContent?.includes("Driver One"))).toBe(true);
        expect(screen.getByText("Total Trips")).toBeVisible();
        expect(within(screen.getByRole("region", { name: "Your trips" })).getAllByRole("columnheader")).toHaveLength(12);
        resize(767);
        expect(screen.getByRole("button", { name: /More filters/ })).toHaveAttribute("aria-expanded", "false");
        await u.click(screen.getByRole("button", { name: /More filters/ }));
        expect(screen.getByRole("textbox", { name: "Search trips" })).toHaveValue("FM");
        expect(screen.getByRole("button", { name: "First Mile" })).toHaveAttribute("data-variant", "secondary");
        expect(calls()).toHaveLength(before);
        await u.click(screen.getByRole("button", { name: "Clear all filters (3)" }));
        await waitFor(() => expect(screen.getByRole("button", { name: "Filters (0)" })).toBeInTheDocument());
        expect(screen.getByRole("textbox", { name: "Search trips" })).toHaveValue("");
    });

    it("status remains a server parameter and summary counts continue describing the date range", async () => {
        const { calls } = setup();
        const u = userEvent.setup();
        await u.click(await screen.findByRole("combobox", { name: "Status" }));
        await u.click(await screen.findByRole("option", { name: /In Transit/ }));
        await waitFor(() => expect(params(calls().at(-1)!)).toMatchObject({ status: "in_transit", limit: "200" }));
        await u.click(screen.getByRole("button", { name: "Trip summary" }));
        expect(screen.getByText("412")).toBeVisible();
        expect(screen.getByText("400")).toBeVisible();
        expect(screen.getByText("12")).toBeVisible();
        expect(screen.getByText("2")).toBeVisible();
    });

    it("Thai names the page, disclosures, search and trip action without raw keys", async () => {
        setup();
        const u = userEvent.setup();
        await screen.findByRole("heading", { name: "Your trips" });
        await u.click(screen.getByRole("button", { name: "Thai" }));
        expect(await screen.findByRole("heading", { name: "เที่ยวขนส่งของคุณ" })).toBeInTheDocument();
        expect(await screen.findByRole("button", { name: "ดูเที่ยว FM-123 และหลักฐาน" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "สรุปเที่ยวขนส่ง" })).toHaveAttribute("aria-expanded", "false");
        await u.click(screen.getByRole("button", { name: "ตัวกรองเพิ่มเติม" }));
        expect(screen.getByRole("textbox", { name: "ค้นหาเที่ยวขนส่ง" })).toBeInTheDocument();
        expect(document.body).not.toHaveTextContent("driverMonitor.");
    });

    it("staff desktops retain the full presentation", async () => {
        resize(1280);
        setup(makeMe({ capabilities: caps }));
        expect(await screen.findByRole("heading", { level: 1, name: "Driver monitor" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Trip summary" })).toBeNull();
        expect(screen.getByText("Total Trips")).toBeVisible();
        expect(screen.getByRole("textbox", { name: "Search trips" })).toBeVisible();
        expect(within(screen.getByRole("region", { name: "Driver monitor" })).getAllByRole("columnheader")).toHaveLength(12);
    });

    it("clamps a completed range to 366 days and keeps the first click uncommitted", async () => {
        const { calls } = setup();
        const u = userEvent.setup();
        await u.click(await screen.findByRole("button", { name: /^Date range \d{2}\/\d{2}\/\d{4} – \d{2}\/\d{2}\/\d{4}$/ }));
        const initial = parseISO(params(calls()[0]).from);
        const first = new Date(initial.getFullYear(), initial.getMonth(), 5);
        const day = (date: Date) => document.querySelector<HTMLElement>(`[data-day="${date.toLocaleDateString()}"]`)!;
        await u.click(day(first));
        expect(calls()).toHaveLength(1);
        for (let i = 0; i < 13; i++) await u.click(screen.getByRole("button", { name: "Go to the Next Month" }));
        const last = new Date(first.getFullYear(), first.getMonth() + 13, 10);
        await u.click(day(last));
        await waitFor(() => expect(calls()).toHaveLength(2));
        const request = params(calls()[1]);
        expect(request.to).toBe(format(last, "yyyy-MM-dd"));
        expect(differenceInCalendarDays(parseISO(request.to), parseISO(request.from)) + 1).toBe(366);
        expect(screen.getByText("You can select at most 366 days at a time; wider selections are clamped automatically.")).toHaveClass("text-warning");
    });

    it("loading, empty and failed first page remain distinct, and refresh retries", async () => {
        let answer!: (value: Response) => void;
        let response = new Promise<Response>((resolve) => { answer = resolve; });
        setup(customer, () => response);
        expect(await screen.findByText("Loading trip data...")).toBeVisible();
        await act(async () => { answer(json(200, { data: [] })); });
        expect(await screen.findByText("No trips found.")).toBeVisible();
        response = Promise.resolve(goErr(422, "invalid_argument"));
        await userEvent.click(screen.getByRole("button", { name: "Refresh" }));
        expect(await screen.findByRole("button", { name: "Try again" })).toBeInTheDocument();
        expect(screen.queryByText("No trips found.")).toBeNull();
        response = Promise.resolve(json(200, { data: [row], meta: { facets } }));
        await userEvent.click(screen.getByRole("button", { name: "Try again" }));
        expect(await screen.findByRole("button", { name: "View trip FM-123 and evidence" })).toBeInTheDocument();
    });

    it("a failed next page keeps the loaded trip and load-more retries its cursor", async () => {
        let fail = true;
        const { calls } = setup(customer, (call) => params(call).cursor
            ? fail ? goErr(422, "invalid_argument") : json(200, { data: [{ ...row, id: "trip-2", tripNo: "FM-456" }] })
            : json(200, { data: [row], nextCursor: "cursor-2", meta: { facets } }));
        expect(await screen.findByRole("button", { name: "View trip FM-123 and evidence" })).toBeInTheDocument();
        await waitFor(() => expect(calls()).toHaveLength(2));
        expect(screen.queryByText("No trips found.")).toBeNull();
        fail = false;
        await userEvent.click(screen.getByRole("button", { name: "Load more" }));
        expect(await screen.findByRole("button", { name: "View trip FM-456 and evidence" })).toBeInTheDocument();
        expect(params(calls()[1]).cursor).toBe("cursor-2");
        expect(params(calls()[2]).cursor).toBe("cursor-2");
    });

    it("a principal without either view capability never queries trips", async () => {
        const { calls } = setup(makeMe({ capabilities: [] }));
        expect(await screen.findByText("No access to this page")).toBeInTheDocument();
        expect(calls()).toHaveLength(0);
    });
});
