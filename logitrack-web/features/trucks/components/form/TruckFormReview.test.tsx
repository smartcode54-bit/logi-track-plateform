import React from "react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { FormProvider, useForm } from "react-hook-form";
import { toast } from "sonner";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { ApiError } from "@/lib/apiError";
import { uploadToGo } from "@/lib/goUpload";
import { truckDefaultValues } from "@/validate/truckSchema";
import NewTruckWizard from "./NewTruckWizard";
import { VehicleDetailsSection } from "./VehicleDetailsSection";
import en from "@/context/locales/en/trucks";

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }), usePathname: () => "/app/trucks/new", useSearchParams: () => new URLSearchParams() }));
vi.mock("@/lib/goUpload", () => ({ uploadToGo: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));
// Prefill the form so these tests can focus on real step transitions and save recovery.
vi.mock("@/validate/truckSchema", async (importOriginal) => {
    const actual = await importOriginal<typeof import("@/validate/truckSchema")>();
    return { ...actual, truckDefaultValues: { ...actual.truckDefaultValues,
        licensePlate: "1กก-1234", province: "กรุงเทพมหานคร", brand: "Isuzu", model: "FRR", year: "2023",
        color: "White", type: "6 Wheels", truckStatus: "active",
    } };
});

beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => {};
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.resetAllMocks(); });

function serve() {
    return fakeWeb().on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }))
        .on("POST", "/api/go/v1/trucks", () => json(200, { data: { id: "saved-truck" } }));
}

function YearForm({ year }: { year: string }) {
    const form = useForm({ defaultValues: { ...truckDefaultValues, year } });
    return <FormProvider {...form}><VehicleDetailsSection /><button onClick={() => form.reset({ ...truckDefaultValues, year: "1975" })}>Load older truck</button></FormProvider>;
}

describe("truck form review regressions", () => {
    it("keeps picked photo and document MIME types after leaving and returning to specs", async () => {
        serve();
        vi.spyOn(window, "scrollTo").mockImplementation(() => {});
        let blobIndex = 0;
        vi.stubGlobal("URL", class extends URL { static createObjectURL() { return `blob:review-${blobIndex++}`; } });
        renderWithProviders(<NewTruckWizard />);
        const user = userEvent.setup();
        for (const [key, name, type] of [
            ["trucks.photos.frontRight", "photo.png", "image/png"],
            ["trucks.documents.tax", "tax.png", "image/png"],
            ["trucks.documents.register", "register.pdf", "application/pdf"],
        ]) {
            const input = await screen.findByLabelText(en[key as keyof typeof en], { selector: "input" });
            await user.upload(input, new File(["file"], name, { type }));
        }
        expect(screen.getAllByRole("img")).toHaveLength(2);
        await user.click(screen.getByRole("button", { name: en["trucks.wizard.next"] }));
        await screen.findByText(en["trucks.step2.title"]);
        await user.click(screen.getByRole("button", { name: "Back" }));
        await screen.findByText(en["trucks.step1.title"]);
        expect(screen.getAllByRole("img")).toHaveLength(2);
        const pdfLink = screen.getAllByRole("link").find((link) => link.getAttribute("href") === "blob:review-2");
        expect(pdfLink).toBeInTheDocument();
        expect(within(pdfLink!).queryByRole("img")).toBeNull();
    });

    it("always explains that the truck saved when the upload returns an API error, and focuses the warning on each retry", async () => {
        const web = serve();
        vi.spyOn(window, "scrollTo").mockImplementation(() => {});
        const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
        vi.stubGlobal("URL", class extends URL { static createObjectURL() { return "blob:photo"; } });
        vi.mocked(uploadToGo).mockRejectedValue(new ApiError({ status: 503, code: "unavailable", message: "storage unavailable" }));
        renderWithProviders(<NewTruckWizard />);
        const user = userEvent.setup();
        await user.upload(await screen.findByLabelText(en["trucks.photos.frontRight"], { selector: "input" }), new File(["image"], "photo.png", { type: "image/png" }));
        for (let step = 2; step <= 4; step++) {
            await user.click(screen.getByRole("button", { name: en["trucks.wizard.next"] }));
            await screen.findByText(en[`trucks.step${step}.title` as keyof typeof en]);
        }
        for (let attempt = 0; attempt < 2; attempt++) {
            await user.click(screen.getByRole("button", { name: en["trucks.wizard.save"] }));
            const warning = await screen.findByRole("alert");
            expect(warning).toHaveTextContent(en["trucks.upload.savedWithFileErrors"]);
            await waitFor(() => expect(warning).toHaveFocus());
            expect(scroll).toHaveBeenCalled();
            expect(toast.error).toHaveBeenLastCalledWith(en["trucks.upload.savedWithFileErrors"]);
        }
        expect(web.count("POST", "/api/go/v1/trucks")).toBe(1);
    });

    it("offers recent years first and preserves a loaded year outside the usual range", async () => {
        serve();
        renderWithProviders(<YearForm year="2023" />);
        const user = userEvent.setup();
        const trigger = await screen.findByRole("combobox", { name: "Year" });
        await user.click(trigger);
        let options = within(screen.getByRole("listbox")).getAllByRole("option");
        expect(options[0]).toHaveTextContent(String(new Date().getFullYear() + 1));
        expect(options.at(-1)).toHaveTextContent("1980");
        expect(options.length).toBeLessThan(100);
        await user.keyboard("{Escape}");
        await user.click(screen.getByRole("button", { name: "Load older truck" }));
        expect(trigger).toHaveTextContent("1975");
        await user.click(trigger);
        options = within(screen.getByRole("listbox")).getAllByRole("option");
        expect(options.filter((option) => option.textContent === "1975")).toHaveLength(1);
    });
});
