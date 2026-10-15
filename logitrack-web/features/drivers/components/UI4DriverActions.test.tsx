import React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { resetQueryClientForTests } from "@/lib/queryClient";
import type { DriverDetailDTO } from "../api/drivers";
import DriversList from "./DriversList";
import DriverPreview from "./DriverPreview";
import NewDriverForm from "./NewDriverForm";
import EditDriverForm from "./EditDriverForm";

vi.mock("next/navigation", () => ({
    useRouter: () => ({ push: vi.fn(), replace: vi.fn(), prefetch: vi.fn(), back: vi.fn() }),
    usePathname: () => "/app/drivers/view",
    useSearchParams: () => new URLSearchParams("id=driver-1"),
}));

const driver = {
    id: "driver-1", tenantId: "own-1", displayName: "Somchai Test", firstName: "Somchai", lastName: "Test",
    mobile: "0812345678", email: "driver@example.test", status: "on_duty", employmentType: "full_time",
    customerDriverIds: [], statusHistory: [], currentAssignment: null, files: {},
};

function serve(over: Partial<DriverDetailDTO> = {}) {
    const record = { ...driver, ...over };
    return fakeWeb()
        .on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities: ["drivers:view"] }) }))
        .on("GET", "/api/go/v1/drivers", () => json(200, { data: [record], meta: { statusCounts: { on_duty: 1 } } }))
        .on("GET", "/api/go/v1/drivers/driver-1", () => json(200, { data: record }))
        .on("GET", "/api/go/v1/subcontractors", () => json(200, { data: [] }))
        .on("GET", "/api/go/v1/truck-assignments", () => json(200, { data: [] }));
}

afterEach(() => {
    resetQueryClientForTests();
    vi.unstubAllGlobals();
});

describe("UI4 driver actions", () => {
    it("removes Export and preserves the Add destination and shared on-duty status", async () => {
        localStorage.setItem("language", "en");
        serve();
        renderWithProviders(<DriversList />);
        expect(await screen.findByText("Somchai Test")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /export/i })).toBeNull();
        expect(screen.getByRole("link", { name: /add new driver/i })).toHaveAttribute("href", "/app/drivers/new");
        expect(screen.getByText(/On duty/i, { selector: "[data-slot=badge]" })).toHaveClass("bg-success");
    });

    it("links the driver's actual phone and email", async () => {
        localStorage.setItem("language", "en");
        serve();
        renderWithProviders(<DriverPreview />);
        expect(await screen.findByRole("link", { name: "Call" })).toHaveAttribute("href", "tel:0812345678");
        expect(screen.getByRole("link", { name: "Email" })).toHaveAttribute("href", "mailto:driver@example.test");
        for (const name of ["Call", "Email"]) expect(screen.getByRole("link", { name })).toHaveClass("max-md:min-h-11");
        expect(screen.getByText(/On duty/i, { selector: "[data-slot=badge]" })).toHaveClass("bg-success");
    });

    it.each([["new", NewDriverForm], ["edit", EditDriverForm]] as const)("announces required controls in the %s driver form and keeps date selection working", async (mode, Component) => {
        localStorage.setItem("language", "en");
        serve();
        renderWithProviders(<Component />);
        const user = userEvent.setup();
        for (const name of ["First Name", "Last Name", "Full Name (Thai)", "Mobile"]) {
            expect(await screen.findByRole("textbox", { name })).toBeRequired();
        }
        expect(screen.getByRole("textbox", { name: "Email" })).not.toBeRequired();
        const birth = screen.getByRole("combobox", { name: "Birth Date (Age 20-55)" });
        expect(birth).toHaveAttribute("aria-required", "true");
        await user.click(birth);
        const calendar = await screen.findByRole("grid");
        const day = within(calendar).getAllByRole("button").find((button) => !button.hasAttribute("disabled"))!;
        await user.click(day);
        expect(birth).toHaveTextContent(/\d{2}\/\d{2}\/\d{4}/);
        expect(screen.queryByRole("grid")).toBeNull();
        if (mode === "new") {
            for (const [name, value] of [["First Name", "Somchai"], ["Last Name", "Test"], ["Full Name (Thai)", "สมชาย"], ["Mobile", "0812345678"]]) {
                await user.type(screen.getByRole("textbox", { name }), value);
            }
            await user.click(screen.getByRole("button", { name: "Next Step" }));
        }
        for (const name of ["ID Number", "License Number"]) {
            expect(await screen.findByRole("textbox", { name })).toBeRequired();
        }
    });

    it.each([
        { mobile: "", email: null, call: false, mail: false },
        { mobile: "   ", email: "driver@example.test", call: false, mail: true },
        { mobile: "0812345678", email: null, call: true, mail: false },
    ])("shows only contact actions with values ($mobile, $email)", async ({ mobile, email, call, mail }) => {
        localStorage.setItem("language", "en");
        serve({ mobile, email });
        renderWithProviders(<DriverPreview />);
        await screen.findByRole("heading", { level: 1, name: "Somchai Test" });
        expect(Boolean(screen.queryByRole("link", { name: "Call" }))).toBe(call);
        expect(Boolean(screen.queryByRole("link", { name: "Email" }))).toBe(mail);
        expect(screen.queryByRole("button", { name: /^(Call|Email)$/ })).toBeNull();
    });
});
