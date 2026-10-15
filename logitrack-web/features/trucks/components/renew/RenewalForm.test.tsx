import React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { RenewalForm } from "./RenewalForm";
import type { TruckDetailDTO } from "../../api/trucks";
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }) }));
afterEach(() => vi.unstubAllGlobals());
const truck = { id: "truck-1", files: {}, taxRenewalStatus: "pending", taxExpiryDate: "2027-10-15", taxExpenseThb: 0, paymentMethod: null,
    insuranceRenewalStatus: "pending", insuranceExpiryDate: "2027-10-15", insurancePremiumThb: 0 } as TruckDetailDTO;

function serve() {
    return fakeWeb().on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }))
        .on("POST", "/api/go/v1/trucks/truck-1/renewals", () => json(200, { data: {} }));
}
describe("renewal completion", () => {
    it.each(["tax", "insurance"] as const)("initializes %s zero and submits amount zero", async (type) => {
        const web = serve();
        const record = { ...truck, files: { taxReceipt: [{ key: "receipt.pdf", url: "/receipt.pdf" }] } };
        renderWithProviders(<RenewalForm type={type} truck={record} onSuccess={vi.fn()} />);
        const amount = await screen.findByRole("spinbutton");
        expect(amount).toHaveValue(0);
        const complete = screen.getByRole("button", { name: /Complete/i });
        expect(complete).toBeEnabled();
        await userEvent.setup().click(complete);
        await waitFor(() => expect(web.count("POST", "/api/go/v1/trucks/truck-1/renewals")).toBe(1));
        expect(web.calls.find((call) => call.method === "POST")?.body).toMatchObject({ amount: 0, status: "completed" });
    });
    it("shows a field error and refuses completion without an amount", async () => {
        const web = serve();
        renderWithProviders(<RenewalForm type="insurance" truck={{ ...truck, insurancePremiumThb: null }} onSuccess={vi.fn()} />);
        const amount = await screen.findByRole("spinbutton");
        expect(amount).toHaveAttribute("aria-invalid", "true");
        expect(screen.getByText(/Enter an amount before completing/)).toBeInTheDocument();
        const complete = screen.getByRole("button", { name: /Complete/i });
        expect(complete).toBeDisabled();
        await userEvent.setup().click(complete);
        expect(web.count("POST", "/api/go/v1/trucks/truck-1/renewals")).toBe(0);
    });
});
