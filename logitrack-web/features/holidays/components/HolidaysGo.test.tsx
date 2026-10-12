// T46 (issue #66; Appendix E §E.2.2, §E.3.5, §E.5 row 6): holidays over Go. One Bangkok year is a plain fetch
// (`['holidays', year]`, no listener, no poll) and a language toggle re-renders the names without a request
// (AC: no holiday refetch on a toggle, `holidays/page.tsx:144`). Writes are Go routes, never the saveHoliday,
// deleteHoliday or saveGeneratedHolidays callables; public holidays are a steward's. No Firebase module is
// imported (the mocks throw). FullCalendar is replaced by a probe that lists the events it is handed.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React, { useEffect } from "react";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("@/firebase/client", () => {
    throw new Error("the Go holidays page must not load the Firebase client");
});
vi.mock("firebase/firestore", () => {
    throw new Error("the Go holidays page must not import Firestore");
});
vi.mock("firebase/functions", () => {
    throw new Error("the Go holidays page must not call a Cloud Function");
});
vi.mock("./HolidayCalendar", () => ({
    default: function CalendarProbe({ events, datesSet }: { events: { id: string; title: string }[]; datesSet?: (arg: unknown) => void }) {
        useEffect(() => {
            const now = new Date();
            datesSet?.({ view: { currentStart: new Date(now.getFullYear(), now.getMonth(), 1) } });
        }, [datesSet]);
        return (
            <ul data-testid="calendar">
                {events.map((e) => (
                    <li key={e.id}>{e.title}</li>
                ))}
            </ul>
        );
    },
}));

import { useLanguage } from "@/context/language";
import { configureFirebaseBridge } from "@/lib/firebaseBridge";
import { fakeWeb, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { queryKeys } from "@/lib/queryKeys";
import type { HolidayDTO } from "../api/holidays.go";
import { HolidaysPage } from "./HolidaysPage";

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
    window.history.replaceState(null, "", "/app/holidays");
    configureFirebaseBridge(async () => {
        throw new Error("no Firebase SDK on the holidays page");
    });
});
afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    configureFirebaseBridge(undefined);
});

const Y = new Date().getFullYear();
const q = (call: FakeCall) => new URL(call.url, "http://web.test").searchParams;

function holiday(id: string, over: Partial<HolidayDTO> = {}): HolidayDTO {
    return {
        id,
        tenantId: null,
        date: `${Y}-12-05`,
        type: "public",
        status: "published",
        name: "Father's Day (วันพ่อแห่งชาติ)",
        nameEn: "Father's Day",
        nameTh: "วันพ่อแห่งชาติ",
        description: null,
        descriptionEn: null,
        descriptionTh: null,
        isRecurring: true,
        createdBy: null,
        createdAt: `${Y}-01-05T02:00:00Z`,
        updatedAt: `${Y}-01-05T02:00:00Z`,
        ...over,
    };
}

function setup({ steward = true } = {}) {
    const web = fakeWeb();
    let rows: HolidayDTO[] = [
        holiday("h1"),
        holiday("h2", { tenantId: "t-own", type: "company", status: "draft", date: `${Y}-12-30`, name: "Company day", nameEn: "Company day", nameTh: "วันหยุดบริษัท", isRecurring: false }),
    ];
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ steward, capabilities: ["hr:manage_holidays"] }) }));
    web.on("GET", "/api/go/v1/config/web-flags", () => json(200, { data: { domains: { auth: "go", hr: "go" } } }));
    web.on("GET", "/api/go/v1/holidays", (call) => json(200, { data: rows.filter((r) => r.date.startsWith(q(call).get("year") ?? "")) }));
    web.on("POST", "/api/go/v1/holidays", (call) => {
        const b = call.body as Partial<HolidayDTO>;
        const row = holiday("h9", { ...b, tenantId: b.type === "public" ? null : "t-own" } as Partial<HolidayDTO>);
        rows = [...rows, row];
        return json(201, { data: row });
    });
    web.on("PATCH", /^\/api\/go\/v1\/holidays\/[^/]+$/, (call) => {
        const id = call.url.split("/").pop() ?? "";
        rows = rows.map((r) => (r.id === id ? { ...r, ...(call.body as object) } : r));
        return json(200, { data: rows.find((r) => r.id === id) });
    });
    web.on("DELETE", /^\/api\/go\/v1\/holidays\/[^/]+$/, (call) => {
        const id = call.url.split("/").pop() ?? "";
        rows = rows.filter((r) => r.id !== id);
        return new Response(null, { status: 204 });
    });
    web.on("PUT", "/api/go/v1/holidays/generate", (call) => {
        const b = call.body as { finalHolidays: unknown[] };
        return json(200, { data: { saved: b.finalHolidays.length, deleted: 0, duplicates: 0 } });
    });
    return web;
}

function Toggle() {
    const { language, setLanguage } = useLanguage();
    return (
        <button type="button" onClick={() => setLanguage(language === "en" ? "th" : "en")}>
            toggle-language
        </button>
    );
}

const yearReads = (web: ReturnType<typeof fakeWeb>) => web.calls.filter((c) => c.method === "GET" && c.url.startsWith("/api/go/v1/holidays?")).length;

describe("holidays over Go", () => {
    it("reads one year from Go as a plain fetch; a language toggle re-renders the names and reads nothing", async () => {
        const web = setup();
        const user = userEvent.setup();
        const { client } = renderWithProviders(
            <>
                <Toggle />
                <HolidaysPage />
            </>
        );
        const calendar = await screen.findByTestId("calendar");
        expect(await within(calendar).findByText("Father's Day")).toBeInTheDocument();
        expect(yearReads(web)).toBe(1);
        expect(q(web.calls.find((c) => c.url.startsWith("/api/go/v1/holidays?"))!).get("year")).toBe(String(Y));
        const query = client.getQueryCache().find({ queryKey: queryKeys.holidays.year(Y) });
        expect(query?.observers[0]?.options.refetchInterval ?? false).toBe(false); // a plain fetch, no poll

        await user.click(screen.getByText("toggle-language"));
        expect(await within(screen.getByTestId("calendar")).findByText("วันพ่อแห่งชาติ")).toBeInTheDocument();
        await user.click(screen.getByText("toggle-language"));
        expect(await within(screen.getByTestId("calendar")).findByText("Father's Day")).toBeInTheDocument();
        expect(yearReads(web)).toBe(1);
        expect(screen.getByTestId("holidays-total")).toHaveTextContent("2");
    });

    it("a steward adds a public holiday with POST: both names, the combined display name, lower-case type", async () => {
        const web = setup();
        const user = userEvent.setup();
        renderWithProviders(<HolidaysPage />);
        await screen.findByTestId("calendar");
        await user.click(screen.getByRole("button", { name: "Add Holiday" }));
        const dialog = await screen.findByRole("dialog");
        await user.type(within(dialog).getByLabelText("Holiday Name (English)"), "New Year");
        await user.type(within(dialog).getByLabelText("Holiday Name (Thai)"), "วันขึ้นปีใหม่");
        await user.type(within(dialog).getByLabelText(/^Date/), `${Y}-12-31`);
        await user.click(within(dialog).getByRole("button", { name: "Save Holiday" }));
        await waitFor(() => expect(web.count("POST", "/api/go/v1/holidays")).toBe(1));
        expect(web.calls.find((c) => c.method === "POST")!.body).toEqual({
            date: `${Y}-12-31`,
            type: "public",
            status: "draft",
            name: "New Year (วันขึ้นปีใหม่)",
            nameEn: "New Year",
            nameTh: "วันขึ้นปีใหม่",
            descriptionEn: null,
            descriptionTh: null,
            description: null,
            isRecurring: false,
        });
        await waitFor(() => expect(yearReads(web)).toBe(2)); // the write makes ['holidays'] stale
    });

    it("edits a holiday with PATCH of the fields that changed, and deletes one after a confirmation", async () => {
        const web = setup();
        const user = userEvent.setup();
        renderWithProviders(<HolidaysPage />);
        await screen.findByTestId("calendar");
        await user.click(screen.getByRole("button", { name: "List" }));
        await user.click(await screen.findByRole("button", { name: "Actions Company day" }));
        await user.click(await screen.findByRole("menuitem", { name: "Edit Holiday" }));
        const dialog = await screen.findByRole("dialog");
        const th = within(dialog).getByLabelText("Holiday Name (Thai)");
        await user.clear(th);
        await user.type(th, "หยุดสิ้นปี");
        await user.click(within(dialog).getByRole("button", { name: "Update Holiday" }));
        await waitFor(() => expect(web.count("PATCH", "/api/go/v1/holidays/h2")).toBe(1));
        expect(web.calls.find((c) => c.method === "PATCH")!.body).toEqual({ name: "Company day (หยุดสิ้นปี)", nameTh: "หยุดสิ้นปี" });

        await user.click(await screen.findByRole("button", { name: "Actions Company day" }));
        await user.click(await screen.findByRole("menuitem", { name: "Delete Holiday" }));
        const confirm = await screen.findByRole("dialog");
        expect(web.count("DELETE", "/api/go/v1/holidays/h2")).toBe(0);
        await user.click(within(confirm).getByRole("button", { name: "Delete Holiday" }));
        await waitFor(() => expect(web.count("DELETE", "/api/go/v1/holidays/h2")).toBe(1));
        await waitFor(() => expect(screen.queryByText("Company day")).toBeNull());
    });

    it("generates a year's public holidays with PUT /v1/holidays/generate (Bangkok days, lower-case types)", async () => {
        const web = setup();
        const user = userEvent.setup();
        renderWithProviders(<HolidaysPage />);
        await screen.findByTestId("calendar");
        await user.click(await screen.findByRole("button", { name: "Generate Holidays" }));
        await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "OK" }));
        const review = await screen.findByRole("dialog", { name: "Review Generated Holidays" });
        await user.click(within(review).getByRole("button", { name: /Save All/ }));
        await waitFor(() => expect(web.count("PUT", "/api/go/v1/holidays/generate")).toBe(1));
        const body = web.calls.find((c) => c.method === "PUT")!.body as { year: number; finalHolidays: Record<string, unknown>[]; initialHolidays: unknown[] };
        expect(body.year).toBe(Y);
        expect(body.finalHolidays.length).toBeGreaterThan(10);
        expect(body.initialHolidays).toHaveLength(body.finalHolidays.length);
        for (const h of body.finalHolidays) {
            expect(h.date).toMatch(new RegExp(`^${Y}-\\d{2}-\\d{2}$`));
            expect(h.type).toBe("public");
            expect(typeof h.name).toBe("string");
        }
    });

    it("anyone but a steward sees public holidays read-only, cannot generate, and adds company holidays", async () => {
        const web = setup({ steward: false });
        const user = userEvent.setup();
        renderWithProviders(<HolidaysPage />);
        await screen.findByTestId("calendar");
        await waitFor(() => expect(web.count("GET", "/api/go/v1/me")).toBeGreaterThan(0));
        await act(async () => undefined);
        expect(screen.queryByRole("button", { name: "Generate Holidays" })).toBeNull();
        await user.click(screen.getByRole("button", { name: "List" }));
        expect(await screen.findByText("Father's Day")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Actions Father's Day" })).toBeNull();
        expect(screen.getByRole("button", { name: "Actions Company day" })).toBeInTheDocument();
        await user.click(screen.getByRole("button", { name: "Add Holiday" }));
        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByRole("combobox", { name: "Type" })).toHaveTextContent("COMPANY");
    });
});
