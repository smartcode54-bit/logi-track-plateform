// T54R (issue #74; Appendix E §E.5 row 18): the waitlist over Go: keyset pages of GET /v1/waitlist with
// meta.total, "load more" by cursor, and DELETE /v1/waitlist/{id} behind a confirmation, which refreshes
// the list and the sidebar's ['badges']. The web has no Firebase SDK since TW7 (lib/noFirebase.test.ts).
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { queryKeys } from "@/lib/queryKeys";
import { WAITLIST_PAGE_SIZE, type WaitlistEntryDTO } from "../api/waitlist";
import { WaitlistPage } from "./WaitlistPage";

beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
});
beforeEach(() => {
    window.history.replaceState(null, "", "/app/waitlist");
});
afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

function entry(n: number): WaitlistEntryDTO {
    return {
        id: `w-${n}`,
        email: `visitor${n}@example.test`,
        name: `Visitor ${n}`,
        countryCode: "+66",
        phone: n % 2 === 0 ? "812345678" : null,
        createdAt: new Date(Date.UTC(2026, 9, 12, 0, 0, 0) - n * 60_000).toISOString(),
    };
}

function setup() {
    const web = fakeWeb();
    const me = makeMe({ id: "u-self", capabilities: ["waitlist:view"] });
    let rows = [entry(1), entry(2), entry(3)];
    web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
    web.on("GET", "/api/go/v1/waitlist", (call) => {
        const q = new URL(call.url, "http://web.test").searchParams;
        if (q.get("cursor") === "c2") return json(200, { data: [entry(4)], meta: { total: rows.length + 1 } });
        return json(200, { data: rows, nextCursor: "c2", meta: { total: rows.length + 1 } });
    });
    web.on("DELETE", /^\/api\/go\/v1\/waitlist\/[^/]+$/, (call) => {
        const id = decodeURIComponent(call.url.split("/").pop() ?? "");
        if (!rows.some((r) => r.id === id)) return goErr(404, "not_found");
        rows = rows.filter((r) => r.id !== id);
        return new Response(null, { status: 204 });
    });
    web.on("GET", "/api/go/v1/badges", () => json(200, { data: { waitlist: rows.length + 1 } }));
    return { web, removeQuietly: (id: string) => (rows = rows.filter((r) => r.id !== id)) };
}

describe("the waitlist over Go", () => {
    it("lists keyset pages with the server's total and loads the next page by cursor", async () => {
        const { web } = setup();
        const user = userEvent.setup();
        renderWithProviders(<WaitlistPage />);
        expect(await screen.findByText("visitor1@example.test")).toBeInTheDocument();
        expect(screen.getByTestId("waitlist-total")).toHaveTextContent("4");
        expect(screen.getByText("Showing 3 of 4")).toBeInTheDocument();
        expect(screen.getByText("+66 812345678")).toBeInTheDocument();
        const first = web.calls.find((c) => c.url.startsWith("/api/go/v1/waitlist"))!;
        expect(new URL(first.url, "http://web.test").searchParams.get("limit")).toBe(String(WAITLIST_PAGE_SIZE));

        await user.click(screen.getByRole("button", { name: "Load more" }));
        expect(await screen.findByText("visitor4@example.test")).toBeInTheDocument();
        expect(screen.getByText("Showing 4 of 4")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
        expect(web.calls.some((c) => c.url.includes("cursor=c2"))).toBe(true);
    });

    it("removes an entry after a confirmation and refreshes the list and the badges", async () => {
        const { web } = setup();
        const user = userEvent.setup();
        const { client } = renderWithProviders(<WaitlistPage />);
        client.setQueryData(queryKeys.badges(), { waitlist: 4 });
        await screen.findByText("visitor2@example.test");

        await user.click(screen.getByRole("button", { name: "Remove visitor2@example.test" }));
        const dialog = await screen.findByRole("dialog");
        expect(dialog).toHaveTextContent("visitor2@example.test will be removed.");
        expect(web.count("DELETE", "/api/go/v1/waitlist/w-2")).toBe(0);
        await user.click(within(dialog).getByRole("button", { name: "Remove" }));

        await waitFor(() => expect(screen.queryByText("visitor2@example.test")).toBeNull());
        expect(web.count("DELETE", "/api/go/v1/waitlist/w-2")).toBe(1);
        expect(screen.getByTestId("waitlist-total")).toHaveTextContent("3");
        expect(client.getQueryState(queryKeys.badges())?.isInvalidated).toBe(true);
    });

    it("treats an entry someone else removed first (404) as removed", async () => {
        const { web, removeQuietly } = setup();
        const user = userEvent.setup();
        renderWithProviders(<WaitlistPage />);
        await screen.findByText("visitor3@example.test");
        removeQuietly("w-3");
        await user.click(screen.getByRole("button", { name: "Remove visitor3@example.test" }));
        await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove" }));
        await waitFor(() => expect(screen.queryByText("visitor3@example.test")).toBeNull());
        expect(web.count("DELETE", "/api/go/v1/waitlist/w-3")).toBe(1);
    });

    it("cancelling the confirmation deletes nothing", async () => {
        const { web } = setup();
        const user = userEvent.setup();
        renderWithProviders(<WaitlistPage />);
        await screen.findByText("visitor1@example.test");
        await user.click(screen.getByRole("button", { name: "Remove visitor1@example.test" }));
        await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel" }));
        await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
        expect(web.calls.filter((c) => c.method === "DELETE")).toHaveLength(0);
        expect(screen.getByText("visitor1@example.test")).toBeInTheDocument();
    });
});
