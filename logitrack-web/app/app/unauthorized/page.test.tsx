import React from "react";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const router = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn(), back: vi.fn() }));
vi.mock("next/navigation", () => ({
    usePathname: () => "/app/unauthorized",
    useSearchParams: () => new URLSearchParams({ from: "/app/dashboard" }),
    useRouter: () => router,
}));

import AdminLayout from "@/app/app/layout";
import type { MeDTO } from "@/features/auth/api/me";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import UnauthorizedPage from "./page";

const driverTenant = { id: "t-wrt", nameTh: "วันเพ็ญ รัชดา", nameEn: "WRT", kind: "own_fleet", role: "driver", status: "active" };
const driverOnly = makeMe({
    displayName: "Dan",
    tenant: driverTenant,
    tenants: [driverTenant, { ...driverTenant, id: "t-other", nameEn: "Other fleet", role: "operator" }],
    steward: false,
    driver: { id: "d-1" },
    capabilities: [],
});
const manager = makeMe({ tenant: { ...driverTenant, role: "manager" }, capabilities: ["fleet:view_trucks"] });

async function renderPage(me: MeDTO) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
    web.on("GET", "/api/go/v1/me/tenants", () => json(200, { data: me.tenants }));
    web.on("GET", "/api/go/v1/badges", () => json(200, { data: {} }));
    renderWithProviders(<AdminLayout><UnauthorizedPage /></AdminLayout>);
    return screen.findByTestId("unauthorized");
}

beforeEach(() => vi.clearAllMocks());
afterEach(() => vi.unstubAllGlobals());

describe("driver-only 403 content", () => {
    it("offers the driver-app landing link without naming Dashboard or looping into the console", async () => {
        const page = await renderPage(driverOnly);
        expect(within(page).getByRole("heading", { level: 1, name: "Use the LogiTrack driver app" })).toBeInTheDocument();
        expect(page).not.toHaveTextContent(/Dashboard/);
        expect(within(page).getByTestId("unauthorized-guide")).toHaveTextContent("Drivers work in the LogiTrack driver app.");
        const home = within(page).getByRole("link", { name: "Go to my home page" });
        expect(home).toHaveAttribute("href", "/");
        expect(home).toHaveAttribute("data-variant", "default");
        expect(home).toHaveClass("max-md:h-11");
        expect(within(page).queryAllByRole("button")).toEqual([]);
        expect(within(page).getAllByRole("link")).toEqual([home]);
        expect(router.back).not.toHaveBeenCalled();
        expect(router.replace).not.toHaveBeenCalled();
        // The console shell still lets this driver switch to their other membership.
        expect(screen.getByRole("banner")).toBeInTheDocument();
        expect(await screen.findByRole("combobox", { name: /Switch organisation/ })).toBeInTheDocument();
    });

    it("keeps the attempted page and both navigation actions for a manager", async () => {
        const page = await renderPage(manager);
        expect(within(page).getByRole("heading", { level: 1, name: "No access to this page" })).toBeInTheDocument();
        expect(page).toHaveTextContent("Your current role does not include “Dashboard”.");
        expect(within(page).getByTestId("unauthorized-guide")).toHaveTextContent("Ask your organisation's administrator");
        const user = userEvent.setup();
        await user.click(within(page).getByRole("button", { name: "Go back" }));
        expect(router.back).toHaveBeenCalledOnce();
        await user.click(within(page).getByRole("button", { name: "Go to my home page" }));
        expect(router.replace).toHaveBeenCalledOnce();
        expect(router.replace).toHaveBeenCalledWith("/app/dashboard");
    });
});
