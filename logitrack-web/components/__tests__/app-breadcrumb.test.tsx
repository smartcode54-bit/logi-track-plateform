import React from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AppBreadcrumb } from "../app-breadcrumb";
import { PageHeader } from "../page-header";
import { BreadcrumbProvider } from "@/context/breadcrumb";
import { LanguageProvider } from "@/context/language";
import { createQueryClient } from "@/lib/queryClient";
import { makeMe } from "@/test-utils/fakeWeb";

function renderTrail(pathname: string, capabilities: string[], recordName?: string) {
    try {
        window.localStorage.setItem("language", "en");
    } catch {
        // Storage may be unavailable in the test environment.
    }
    const client = createQueryClient();
    client.setQueryData(["me"], makeMe({
        tenant: { id: "carrier", nameTh: "รถร่วม", nameEn: "Carrier", kind: "carrier", role: "tenant_admin" },
        capabilities,
    }));
    render(
        <QueryClientProvider client={client}>
            <LanguageProvider>
                <BreadcrumbProvider>
                    <AppBreadcrumb pathname={pathname} homeHref="/app/dashboard" />
                    {recordName ? <PageHeader title="Trucks" recordName={recordName} /> : null}
                </BreadcrumbProvider>
            </LanguageProvider>
        </QueryClientProvider>
    );
}

afterEach(() => vi.unstubAllGlobals());

describe("complete-catalog breadcrumbs (UI3F)", () => {
    it("links a hidden destination when routeAllowed admits it", async () => {
        renderTrail("/app/payroll/config", ["hr:view_payroll", "hr:manage_payroll"]);
        const nav = await screen.findByRole("navigation", { name: "Breadcrumb" });
        expect(within(nav).getByRole("link", { name: "Payroll" })).toHaveAttribute("href", "/app/payroll");
        expect(within(nav).getByText("Payroll and HR")).toBeInTheDocument();
    });

    it("keeps a forbidden parent as text while naming the hidden direct page", async () => {
        renderTrail("/app/payroll/config", ["hr:manage_payroll"]);
        const nav = await screen.findByRole("navigation", { name: "Breadcrumb" });
        expect(within(nav).getByText("Payroll")).toBeInTheDocument();
        expect(within(nav).queryByRole("link", { name: "Payroll" })).toBeNull();
        expect(document.title).not.toMatch(/nav\.|driverComp\./);
    });

    it("retains a record's name, profile group and allowed list link", async () => {
        renderTrail("/app/trucks/x1/edit", ["fleet:view_trucks"], "70-1234");
        const nav = await screen.findByRole("navigation", { name: "Breadcrumb" });
        expect(within(nav).getByText("Fleet")).toBeInTheDocument();
        expect(within(nav).getByRole("link", { name: "Trucks" })).toHaveAttribute("href", "/app/trucks");
        expect(await within(nav).findByText("70-1234")).toHaveAttribute("aria-current", "page");
        await waitFor(() => expect(document.title).toBe("70-1234 · Trucks · LogiTrack Pro"));
    });
});
