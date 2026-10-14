// UI2 (issue #142): the one searchable select. Thai search, keyboard selection, multi select, remote
// (server) search, virtualisation of 5,000 options, the compound Select API, and a Dialog around it.
import * as React from "react";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";

import { stubVirtualLayout } from "@/test-utils/virtualLayout";

import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./dialog";
import { MultiSearchSelect, SearchSelect, VIRTUALIZE_ABOVE, type SearchSelectOption } from "./search-select";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./select";

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
    restoreLayout = stubVirtualLayout({ viewport: 300, row: 32 });
});
let restoreLayout: () => void;
afterAll(() => restoreLayout());

const HUBS: SearchSelectOption[] = [
    { value: "SPK-GW", label: "J&T EXPRESS บางปู", description: "SPK-GW" },
    { value: "SPK890174", label: "ห้วยขวาง10", description: "SPK890174" },
    { value: "SOCE", label: "Café Hub Est", description: "SOCE" },
    { value: "SOCN", label: "SOC North", description: "SOCN", disabled: true },
    { value: "H5", label: "บางนา", description: "H5" },
    { value: "H6", label: "ลาดกระบัง", description: "H6" },
    { value: "H7", label: "Bang Phli", description: "H7" },
    { value: "H8", label: "Samut Prakan", description: "H8" },
];

function Single({ onChange = vi.fn(), options = HUBS, initial = "" }: { onChange?: (v: string) => void; options?: SearchSelectOption[]; initial?: string }) {
    const [value, setValue] = React.useState(initial);
    return (
        <SearchSelect
            aria-label="Hub"
            options={options}
            value={value}
            clearable
            placeholder="Pick a hub"
            onValueChange={(v) => {
                setValue(v);
                onChange(v);
            }}
        />
    );
}

const optionRows = () => document.querySelectorAll('[data-slot="search-select-option"]');

describe("SearchSelect", () => {
    it("finds Thai labels without tone marks and English labels without accents", async () => {
        const user = userEvent.setup();
        render(<Single />);
        await user.click(screen.getByRole("combobox", { name: "Hub" }));
        const search = await screen.findByPlaceholderText("Search...");

        await user.type(search, "หวยขวาง");
        expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual(["ห้วยขวาง10SPK890174"]);

        await user.clear(search);
        await user.type(search, "cafe");
        expect(screen.getAllByRole("option")).toHaveLength(1);
        expect(screen.getByRole("option")).toHaveTextContent("Café Hub Est");

        // the secondary text (a code) is searched too
        await user.clear(search);
        await user.type(search, "spk-gw");
        expect(screen.getByRole("option")).toHaveTextContent("J&T EXPRESS บางปู");
    });

    it("picks with the arrow keys and Enter, skips disabled options, and clears from the keyboard", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        render(<Single onChange={onChange} />);
        const trigger = screen.getByRole("combobox", { name: "Hub" });
        trigger.focus();
        await user.keyboard("{Enter}");
        await screen.findByPlaceholderText("Search...");
        // first row highlighted; down three times skips the disabled "SOC North"
        await user.keyboard("{ArrowDown}{ArrowDown}{ArrowDown}{Enter}");
        expect(onChange).toHaveBeenLastCalledWith("H5");
        await waitFor(() => expect(screen.queryByRole("listbox")).not.toBeInTheDocument());
        expect(trigger).toHaveTextContent("บางนา");

        trigger.focus();
        await user.keyboard("{Delete}");
        expect(onChange).toHaveBeenLastCalledWith("");
        expect(trigger).toHaveTextContent("Pick a hub");
    });

    it("shows the empty state and a create row", async () => {
        const user = userEvent.setup();
        const onCreate = vi.fn();
        render(<SearchSelect aria-label="Hub" options={HUBS} value="" onValueChange={vi.fn()} onCreate={onCreate} />);
        await user.click(screen.getByRole("combobox", { name: "Hub" }));
        await user.type(await screen.findByPlaceholderText("Search..."), "Nowhere");
        await user.click(screen.getByRole("option", { name: /Nowhere/ }));
        expect(onCreate).toHaveBeenCalledWith("Nowhere");
    });

    it("keeps a selected value labelled when a remote page no longer holds it", () => {
        const { rerender } = render(<SearchSelect aria-label="Driver" options={[{ value: "d1", label: "สมชาย" }]} value="d1" onValueChange={vi.fn()} onSearchChange={vi.fn()} />);
        expect(screen.getByRole("combobox")).toHaveTextContent("สมชาย");
        rerender(<SearchSelect aria-label="Driver" options={[{ value: "d2", label: "Somsak" }]} value="d1" onValueChange={vi.fn()} onSearchChange={vi.fn()} />);
        expect(screen.getByRole("combobox")).toHaveTextContent("สมชาย");
    });

    it("hands the typed term to a remote list and shows its options unfiltered", async () => {
        const user = userEvent.setup();
        const onSearch = vi.fn();
        render(
            <SearchSelect
                aria-label="Driver"
                options={[{ value: "d9", label: "Server row" }]}
                value=""
                onValueChange={vi.fn()}
                onSearchChange={onSearch}
            />
        );
        await user.click(screen.getByRole("combobox"));
        await user.type(await screen.findByPlaceholderText("Search..."), "xyz");
        expect(onSearch).toHaveBeenLastCalledWith("xyz");
        expect(screen.getByRole("option")).toHaveTextContent("Server row");
    });

    it(`renders under 100 rows for 5,000 options (virtualised above ${VIRTUALIZE_ABOVE})`, async () => {
        const user = userEvent.setup();
        const many = Array.from({ length: 5000 }, (_, i) => ({ value: `T${i}`, label: `70-${String(i).padStart(4, "0")}`, description: "6WH" }));
        render(<Single options={many} />);
        await user.click(screen.getByRole("combobox", { name: "Hub" }));
        await screen.findByPlaceholderText("Search...");
        await waitFor(() => expect(document.querySelector('[data-slot="search-select-virtual"]')).not.toBeNull());
        expect(optionRows().length).toBeGreaterThan(0);
        expect(optionRows().length).toBeLessThan(100);

        await user.type(screen.getByPlaceholderText("Search..."), "70-4999");
        await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(1));
    });
});

describe("MultiSearchSelect", () => {
    function Multi({ onChange = vi.fn(), max }: { onChange?: (v: string[]) => void; max?: number }) {
        const [value, setValue] = React.useState<string[]>([]);
        return (
            <MultiSearchSelect
                aria-label="Hubs"
                options={HUBS}
                value={value}
                max={max}
                onValueChange={(v) => {
                    setValue(v);
                    onChange(v);
                }}
            />
        );
    }

    it("toggles several options as chips, keeps the list open, and removes a chip", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        render(<Multi onChange={onChange} />);
        await user.click(screen.getByRole("combobox", { name: "Hubs" }));
        await user.click(await screen.findByRole("option", { name: /บางนา/ }));
        await user.click(screen.getByRole("option", { name: /Bang Phli/ }));
        expect(onChange).toHaveBeenLastCalledWith(["H5", "H7"]);
        expect(screen.getByRole("option", { name: /บางนา/ })).toHaveAttribute("data-checked", "true");

        await user.keyboard("{Escape}");
        const chips = document.querySelector('[data-slot="search-select-chips"]') as HTMLElement;
        expect(within(chips).getByText("บางนา")).toBeInTheDocument();
        await user.click(within(chips).getByRole("button", { name: "Remove บางนา" }));
        expect(onChange).toHaveBeenLastCalledWith(["H7"]);
    });

    it("stops at max", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        render(<Multi onChange={onChange} max={1} />);
        await user.click(screen.getByRole("combobox", { name: "Hubs" }));
        await user.click(await screen.findByRole("option", { name: /บางนา/ }));
        await user.click(screen.getByRole("option", { name: /Bang Phli/ }));
        expect(onChange).toHaveBeenCalledTimes(1);
        expect(screen.getByText("1 of 1 selected")).toBeInTheDocument();
    });

    it("takes a selectedOptions list whose length changes without a hook-deps warning (review panel, UI2)", () => {
        const errors = vi.spyOn(console, "error").mockImplementation(() => {});
        try {
            const props = { "aria-label": "Parties", options: [] as SearchSelectOption[], onValueChange: vi.fn() };
            const { rerender } = render(<MultiSearchSelect {...props} value={[]} selectedOptions={[]} />);
            rerender(
                <MultiSearchSelect
                    {...props}
                    value={["p-1", "p-2"]}
                    selectedOptions={[
                        { value: "p-1", label: "ลูกค้า 1" },
                        { value: "p-2", label: "ลูกค้า 2" },
                    ]}
                />
            );
            rerender(<MultiSearchSelect {...props} value={["p-1"]} selectedOptions={[{ value: "p-1", label: "ลูกค้า 1" }]} />);
            const chips = document.querySelector('[data-slot="search-select-chips"]') as HTMLElement;
            expect(within(chips).getByText("ลูกค้า 1")).toBeInTheDocument();
            expect(errors).not.toHaveBeenCalled();
        } finally {
            errors.mockRestore();
        }
    });
});

describe("Select (compound API on the same panel)", () => {
    function Status({ onChange = vi.fn() }: { onChange?: (v: string) => void }) {
        const [v, setV] = React.useState("open");
        return (
            <Select
                value={v}
                onValueChange={(x) => {
                    setV(x);
                    onChange(x);
                }}
            >
                <SelectTrigger aria-label="Status" className="w-[160px]">
                    <SelectValue placeholder="Status" />
                </SelectTrigger>
                <SelectContent position="popper">
                    <SelectItem value="open">เปิด</SelectItem>
                    <>
                        {["done", "void"].map((s) => (
                            <SelectItem key={s} value={s}>
                                {s === "done" ? "เสร็จ" : <span>ยกเลิก</span>}
                            </SelectItem>
                        ))}
                    </>
                </SelectContent>
            </Select>
        );
    }

    it("shows the selected item's label and picks another", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        render(<Status onChange={onChange} />);
        const trigger = screen.getByRole("combobox", { name: "Status" });
        expect(trigger).toHaveTextContent("เปิด");
        await user.click(trigger);
        // three options: no search box, the arrow keys still work
        expect(screen.queryByPlaceholderText("Search...")).not.toBeInTheDocument();
        await user.click(await screen.findByRole("option", { name: /ยกเลิก/ }));
        expect(onChange).toHaveBeenCalledWith("void");
        expect(trigger).toHaveTextContent("ยกเลิก");
    });

    it("opens, filters and selects inside a Dialog", async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        render(
            <Dialog open>
                <DialogContent>
                    <DialogTitle>Edit</DialogTitle>
                    <DialogDescription>Pick a hub</DialogDescription>
                    <Single onChange={onChange} />
                </DialogContent>
            </Dialog>
        );
        await user.click(screen.getByRole("combobox", { name: "Hub" }));
        const search = await screen.findByPlaceholderText("Search...");
        expect(search).toHaveFocus();
        await user.type(search, "บางปู");
        expect(screen.getAllByRole("option")).toHaveLength(1);
        await user.keyboard("{Enter}");
        expect(onChange).toHaveBeenCalledWith("SPK-GW");
        // the dialog is still open, the picker closed
        expect(screen.getByRole("dialog")).toBeInTheDocument();
        await waitFor(() => expect(screen.queryByPlaceholderText("Search...")).not.toBeInTheDocument());
        await act(async () => undefined);
    });
});
