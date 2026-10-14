// UI2: the react-hook-form binding of the searchable select. A required pick fails through the shared
// onInvalid handler (ADR 0004): the toast names the field, the trigger takes the focus and carries
// aria-invalid with the FormMessage; picking a value then submits it.
import * as React from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { beforeAll, describe, expect, it, vi } from "vitest";

const toastError = vi.fn();
vi.mock("sonner", () => ({ toast: { error: (msg: string) => toastError(msg) } }));

import { Form } from "./form";
import { FormSearchSelect } from "./form-search-select";
import { createInvalidHandler } from "@/lib/formInvalidHandler";

beforeAll(() => {
    globalThis.ResizeObserver ??= class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
    Element.prototype.scrollIntoView ??= () => {};
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
});

const schema = z.object({
    hub: z.string().min(1, "Pick a hub"),
    tags: z.array(z.string()).min(1, "Pick a tag"),
});
type Values = z.infer<typeof schema>;

const t = (key: string, params?: Record<string, string | number>) => `${key}:${params?.field ?? ""}`;

function HubForm({ onSubmit }: { onSubmit: (v: Values) => void }) {
    const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { hub: "", tags: [] } });
    return (
        <Form {...form}>
            <form onSubmit={form.handleSubmit(onSubmit, createInvalidHandler(form, t))}>
                <FormSearchSelect
                    control={form.control}
                    name="hub"
                    label="Hub"
                    options={[
                        { value: "SPK-GW", label: "J&T EXPRESS บางปู" },
                        { value: "SOCE", label: "SOC East" },
                    ]}
                    placeholder="Pick..."
                />
                <FormSearchSelect
                    control={form.control}
                    name="tags"
                    label="Tags"
                    multiple
                    options={[
                        { value: "a", label: "Alpha" },
                        { value: "b", label: "Beta" },
                    ]}
                />
                <button type="submit">Save</button>
            </form>
        </Form>
    );
}

describe("FormSearchSelect", () => {
    it("fails loudly on an empty required pick, then submits the picked values", async () => {
        const user = userEvent.setup();
        const onSubmit = vi.fn();
        render(<HubForm onSubmit={onSubmit} />);

        await user.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(toastError).toHaveBeenCalledWith("common.toast.validationError:Pick a hub"));
        const hub = screen.getByRole("combobox", { name: "Hub" });
        expect(hub).toHaveFocus();
        expect(hub).toHaveAttribute("aria-invalid", "true");
        expect(screen.getByText("Pick a hub")).toBeInTheDocument();
        expect(onSubmit).not.toHaveBeenCalled();

        await user.click(hub);
        await user.click(await screen.findByRole("option", { name: /บางปู/ }));
        await user.click(screen.getByRole("combobox", { name: "Tags" }));
        await user.click(await screen.findByRole("option", { name: /Beta/ }));
        await user.keyboard("{Escape}");
        await user.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(onSubmit).toHaveBeenCalled());
        expect(onSubmit.mock.calls[0][0]).toEqual({ hub: "SPK-GW", tags: ["b"] });
        expect(screen.getByRole("combobox", { name: "Hub" })).toHaveAttribute("aria-invalid", "false");
    });
});
