import React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { resetQueryClientForTests } from "@/lib/queryClient";
import NewCustomerForm from "./NewCustomerForm";
import EditCustomerForm from "./EditCustomerForm";

vi.mock("next/navigation", () => ({
    useParams: () => ({ id: "customer-1" }),
    useRouter: () => ({ push: vi.fn(), replace: vi.fn(), prefetch: vi.fn(), back: vi.fn() }),
    usePathname: () => "/app/customers/new",
}));

afterEach(() => {
    resetQueryClientForTests();
    vi.unstubAllGlobals();
});

describe.each([
    ["new", NewCustomerForm],
    ["edit", EditCustomerForm],
] as const)("UI4 %s customer form", (_mode, Component) => {
    it("marks and announces required fields and removes an uploaded logo without submitting", async () => {
        localStorage.setItem("language", "en");
        const web = fakeWeb()
            .on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }))
            .on("GET", "/api/go/v1/customers/customer-1", () => json(200, { data: {
                id: "customer-1", code: "TEST", name: "Test customer", logoUrl: null, driverIdTypes: [],
            } }));
        renderWithProviders(<Component />);
        for (const name of ["Customer Code", "Display Name"]) {
            const input = await screen.findByRole("textbox", { name });
            expect(input).toBeRequired();
            expect(document.querySelector(`label[for="${input.id}"] [aria-hidden="true"]`)).toHaveTextContent("*");
        }
        expect(screen.getByRole("textbox", { name: "Description" })).not.toBeRequired();
        const user = userEvent.setup();
        await user.upload(screen.getByLabelText("Upload logo"), new File(["image"], "logo.png", { type: "image/png" }));
        const remove = await screen.findByRole("button", { name: "Remove logo" });
        expect(remove).toHaveAttribute("data-variant", "destructive");
        expect(remove).toHaveClass("size-9", "max-md:size-11");
        await user.click(remove);
        await waitFor(() => expect(screen.queryByRole("button", { name: "Remove logo" })).toBeNull());
        expect(web.calls.filter((call) => call.method !== "GET")).toHaveLength(0);
    });
});
