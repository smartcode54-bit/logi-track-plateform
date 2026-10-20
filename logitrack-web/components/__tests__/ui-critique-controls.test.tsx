import React, { useState } from "react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LanguageProvider } from "@/context/language";
import { Field } from "@/features/accounting/components/go/shared";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { SearchSelect } from "@/components/ui/search-select";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { DateTimePicker } from "@/components/ui/datetime-picker";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { ImagePreviewGallery } from "@/components/accounting/ImagePreviewGallery";

beforeAll(() => {
    globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
    Element.prototype.scrollIntoView ??= () => {};
    Element.prototype.setPointerCapture ??= () => {};
});
beforeEach(() => localStorage.setItem("language", "en"));
const mount = (ui: React.ReactNode) => render(<LanguageProvider>{ui}</LanguageProvider>);

describe("critique control regressions", () => {
    it("Escape closes the evidence viewer while preserving its parent detail dialog", async () => {
        const user = userEvent.setup();
        mount(<Dialog open><DialogContent><DialogTitle>Incident details</DialogTitle><ImagePreviewGallery items={[{url:"/proof.png",label:"Proof"}]} /></DialogContent></Dialog>);
        await user.click(await screen.findByRole("button",{name:"Proof"}));
        expect(await screen.findByRole("dialog",{name:"Proof (1 / 1)"})).toBeInTheDocument();
        await user.keyboard("{Escape}");
        expect(screen.queryByRole("dialog",{name:"Proof (1 / 1)"})).toBeNull();
        expect(screen.getByRole("dialog",{name:"Incident details"})).toBeInTheDocument();
    });

    it("keeps visible field names on inputs, compound selects and entity-style pickers after selection", async () => {
        mount(<>
            <Field label="Reference"><Input defaultValue="REF-1" /></Field>
            <Field label="Notes"><Textarea defaultValue="Review" /></Field>
            <Field label="Driver"><SearchSelect value="driver-1" onValueChange={vi.fn()} options={[{ value: "driver-1", label: "Somchai" }]} /></Field>
            <Field label="Job category"><Select value="main"><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="main">Main</SelectItem></SelectContent></Select></Field>
            <Field label="Context"><Input aria-label="Explicit name" /></Field>
        </>);
        expect(await screen.findByRole("textbox", { name: "Reference" })).toHaveValue("REF-1");
        expect(screen.getByRole("textbox", { name: "Notes" })).toHaveValue("Review");
        expect(screen.getByRole("combobox", { name: "Driver" })).toHaveTextContent("Somchai");
        expect(screen.getByRole("combobox", { name: "Job category" })).toHaveTextContent("Main");
        expect(screen.getByRole("textbox", { name: "Explicit name" })).toBeInTheDocument();
    });

    it("clears the date with a real keyboard button and names the two searchable time pickers", async () => {
        function Appointment() {
            const [value, setValue] = useState<Date | undefined>(new Date(2026, 9, 19, 13, 45));
            return <DateTimePicker value={value} onChange={setValue} />;
        }
        const user = userEvent.setup();
        mount(<Appointment />);
        const clear = await screen.findByRole("button", { name: "Clear date and time" });
        expect(clear.tagName).toBe("BUTTON");
        clear.focus();
        await user.keyboard("{Enter}");
        expect(screen.queryByRole("button", { name: "Clear date and time" })).toBeNull();
        await user.click(screen.getByRole("button", { name: "Pick a date and time" }));
        const hour = await screen.findByRole("combobox", { name: "Hour" });
        const minute = screen.getByRole("combobox", { name: "Minute" });
        expect(hour).toHaveTextContent("09");
        expect(minute).toHaveTextContent("00");
        expect(hour.tagName).toBe("BUTTON");
        expect(minute.tagName).toBe("BUTTON");
    });

    it("opens evidence as a named modal and provides keyboard pan without changing the photo", async () => {
        const user = userEvent.setup();
        mount(<ImagePreviewGallery items={[{ url: "/proof.png", label: "Delivery proof" }, { url: "/second.png", label: "Other proof" }]} />);
        await user.click(await screen.findByRole("button", { name: "Delivery proof" }));
        const modal = await screen.findByRole("dialog", { name: "Delivery proof (1 / 2)" });
        expect(modal).toHaveAttribute("aria-modal", "true");
        await user.click(within(modal).getByRole("button", { name: "Zoom in" }));
        await user.keyboard("{Shift>}{ArrowRight}{/Shift}");
        const image = within(modal).getByRole("img", { name: "Delivery proof" });
        expect(image.parentElement).toHaveStyle({ transform: "translate(40px, 0px) scale(1.5)" });
        expect(image.parentElement?.parentElement).toHaveClass("touch-none");
        fireEvent(modal, new Event("cancel", { bubbles: false, cancelable: true }));
        await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    });
});
