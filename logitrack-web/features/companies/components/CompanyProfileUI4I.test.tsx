import React from "react";
import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import CompanyProfilePage from "@/app/app/settings/company-profile/page";
import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

it("associates profile validation messages with their fields and keeps the shared invalid handler", async () => {
    window.localStorage.setItem("language", "en");
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities: ["company:manage"] }) }));
    web.on("GET", "/api/go/v1/companies/owner", () => goErr(404, "not_found"));
    const notice = vi.spyOn(toast, "error");
    renderWithProviders(<RouteNamespaces pathname="/app/settings/company-profile"><CompanyProfilePage /></RouteNamespaces>);
    const user = userEvent.setup();
    const name = await screen.findByLabelText("Company Name (Thai) *");
    await user.click(screen.getByRole("button", { name: /^Save$/ }));
    await waitFor(() => expect(name).toHaveAttribute("aria-invalid", "true"));
    expect(name).toHaveFocus();
    expect(name).toHaveAccessibleDescription("ต้องระบุชื่อบริษัท");
    expect(screen.getByLabelText("Tax ID (13 digits) *")).toHaveAccessibleDescription("Tax ID ต้องมี 13 หลัก");
    expect(screen.getByLabelText("Registered Address *")).toHaveAccessibleDescription("ต้องระบุที่อยู่");
    expect(notice).toHaveBeenCalledTimes(1);
    expect(web.calls.filter((call) => call.method !== "GET")).toHaveLength(0);
});

it.each(["3.1", "3.3"])("preserves native withholding-tax step validation for %s before saving", async (invalidRate) => {
    window.localStorage.setItem("language", "en");
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities: ["company:manage"] }) }));
    web.on("GET", "/api/go/v1/companies/owner", () => goErr(404, "not_found"));
    web.on("POST", "/api/go/v1/companies", () => json(201, { data: { id: "company-1" } }));
    renderWithProviders(<RouteNamespaces pathname="/app/settings/company-profile"><CompanyProfilePage /></RouteNamespaces>);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Company Name (Thai) *"), "บริษัททดสอบ");
    await user.type(screen.getByLabelText("Tax ID (13 digits) *"), "1234567890123");
    await user.type(screen.getByLabelText("Registered Address *"), "Bangkok");
    const tax = screen.getByRole("spinbutton", { name: "Withholding Tax Rate (%)" }) as HTMLInputElement;
    const save = screen.getByRole("button", { name: /^Save$/ });
    await user.clear(tax);
    await user.type(tax, invalidRate);
    expect(tax.validity.stepMismatch).toBe(true);
    await user.click(save);
    expect(web.calls.filter((call) => call.method !== "GET")).toHaveLength(0);

    await user.clear(tax);
    await user.type(tax, "3.5");
    expect(tax.validity.valid).toBe(true);
    await user.click(save);
    await waitFor(() => expect(web.calls.filter((call) => call.method === "POST")).toHaveLength(1));
    expect(web.calls.find((call) => call.method === "POST")?.body).toMatchObject({ withholdingTaxRate: 3.5 });
});
