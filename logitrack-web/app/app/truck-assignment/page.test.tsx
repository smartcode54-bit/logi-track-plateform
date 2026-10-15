import React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createQueryClient, resetQueryClientForTests } from "@/lib/queryClient";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import TruckAssignmentPage from "./page";

vi.mock("next/navigation", () => ({
    useRouter: () => ({ push: vi.fn(), replace: vi.fn(), prefetch: vi.fn() }),
    usePathname: () => "/app/truck-assignment",
    useSearchParams: () => new URLSearchParams(),
}));

afterEach(() => { resetQueryClientForTests(); vi.unstubAllGlobals(); });

function serve(failed: (path: string, search: URLSearchParams) => boolean) {
    return fakeWeb().on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }))
        .on("GET", /^\/api\/go\/v1\/(drivers|trucks|truck-assignments)$/, (call) => {
            const url = new URL(call.url, "http://web.test");
            if (failed(url.pathname, url.searchParams)) return json(503, { error: { code: "unavailable" } });
            return json(200, { data: [], meta: { statusCounts: { active: 2 } } });
        });
}

function renderPage() {
    const client = createQueryClient();
    client.setDefaultOptions({ queries: { retry: false } });
    return renderWithProviders(<TruckAssignmentPage />, client);
}

describe("truck assignment query states", () => {
    it.each(["drivers", "trucks", "truck-assignments"])("does not turn failed %s reads into zero counters and retries them", async (path) => {
        let failing = true;
        const web = serve((pathname, query) => failing && pathname.endsWith(`/${path}`) && (path !== "truck-assignments" || query.get("status") === "active"));
        renderPage();
        expect(await screen.findByRole("alert")).toHaveTextContent("Failed to load dashboard data");
        const deploy = screen.getByRole("button", { name: "Confirm and Assign" });
        if (path === "truck-assignments") expect(deploy).toBeDisabled();
        else expect(deploy).toBeEnabled();
        expect(screen.getByRole("button", { name: "Reset form" })).toBeEnabled();
        failing = false;
        await userEvent.setup().click(screen.getByRole("button", { name: "Try again" }));
        await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
        expect(web.calls.filter((call) => call.url.includes(`/${path}`)).length).toBeGreaterThanOrEqual(2);
    });

    it("keeps Deploy, Reset and counters available when background refetches fail with retained data", async () => {
        let failing = false;
        serve(() => failing);
        const { client } = renderPage();
        await waitFor(() => expect(screen.getByRole("button", { name: "Confirm and Assign" })).toBeEnabled());
        expect(screen.getAllByText("2")).toHaveLength(2);
        failing = true;
        await act(async () => { await client.invalidateQueries({ predicate: (query) => query.queryKey[0] !== "me" }); });
        expect(screen.getByRole("button", { name: "Confirm and Assign" })).toBeEnabled();
        expect(screen.getByRole("button", { name: "Reset form" })).toBeEnabled();
        expect(screen.getAllByText("2")).toHaveLength(2);
    });

    it("retries a failed history filter while retaining the table", async () => {
        let failing = true;
        const web = serve((pathname, query) => failing && pathname.endsWith("/truck-assignments") && query.get("status") !== "active");
        renderPage();
        expect(await screen.findByRole("alert")).toHaveTextContent("Could not load assignment history");
        failing = false;
        await userEvent.setup().click(screen.getByRole("button", { name: "Try again" }));
        expect(await screen.findByText("No recent assignments found.")).toBeInTheDocument();
        expect(web.calls.filter((call) => call.url.includes("truck-assignments") && !call.url.includes("status=active"))).toHaveLength(2);
    });
});
