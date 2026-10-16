import React from "react";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({ usePathname: () => "/app/settings/support-access", useRouter: () => ({ push: vi.fn() }) }));

import { AppSidebar } from "@/components/app-sidebar";
import { QuickJump } from "@/components/quick-jump";
import { SidebarProvider } from "@/components/ui/sidebar";
import { createQueryClient } from "@/lib/queryClient";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

afterEach(() => vi.unstubAllGlobals());

describe("Support access in live navigation", () => {
    it.each(["own_fleet", "carrier"])("sidebar and quick-jump show an active %s admin and hide a manager after refresh", async (kind) => {
        const base = makeMe({ capabilities: ["users:assign_role", "company:manage"] });
        const tenant = { ...base.tenant!, kind };
        const me = { ...base, tenant, tenants: [tenant] };
        const client = createQueryClient();
        client.setQueryData(["me"], me);
        fakeWeb().on("GET", "/api/go/v1/badges", () => json(200, { data: {} }));
        renderWithProviders(<SidebarProvider defaultOpen><AppSidebar /><QuickJump /></SidebarProvider>, client);
        expect(await screen.findByRole("link", { name: "Support access" })).toHaveAttribute("href", "/app/settings/support-access");
        const user = userEvent.setup();
        await user.click(screen.getByTestId("quick-jump-trigger"));
        expect(await screen.findByRole("option", { name: /Support access/ })).toBeVisible();
        await user.keyboard("{Escape}");

        act(() => client.setQueryData(["me"], { ...me, tenant: { ...tenant, role: "manager" }, tenants: [{ ...tenant, role: "manager" }] }));
        await waitFor(() => expect(screen.queryByRole("link", { name: "Support access" })).toBeNull());
        await user.click(screen.getByTestId("quick-jump-trigger"));
        await screen.findByRole("combobox");
        expect(screen.queryByRole("option", { name: /Support access/ })).toBeNull();
    });
});
