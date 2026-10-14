// UI1 (issue #141): every floating surface opens, filters (where it can) and picks inside a Dialog and
// inside a Sheet with no per-dialog z-index or `position` override. jsdom has no layout, so "above" is
// checked as what decides it in the browser: the floating content is portalled to <body> (outside the
// dialog's stacking context) on a layer that globals.css orders above `modal` (lib/layering.test.ts).
import * as React from "react";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./dialog";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from "./dropdown-menu";
import { Popover, PopoverContent, PopoverTrigger } from "./popover";
import { SearchSelect, type SearchSelectOption } from "./search-select";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./select";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "./sheet";
import { Tooltip, TooltipContent, TooltipTrigger } from "./tooltip";

beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
});

const HUBS: SearchSelectOption[] = [
    { value: "SPK-GW", label: "J&T EXPRESS บางปู", description: "SPK-GW" },
    { value: "SPK890174", label: "ห้วยขวาง10", description: "SPK890174" },
    { value: "SOCE", label: "SOC East", description: "SOCE" },
    { value: "H5", label: "บางนา", description: "H5" },
    { value: "H6", label: "ลาดกระบัง", description: "H6" },
    { value: "H7", label: "Bang Phli", description: "H7" },
    { value: "H8", label: "Samut Prakan", description: "H8" },
    { value: "H9", label: "Lat Krabang", description: "H9" },
];

function HubPicker({ onChange }: { onChange: (v: string) => void }) {
    const [value, setValue] = React.useState("");
    return (
        <SearchSelect
            aria-label="Hub"
            options={HUBS}
            value={value}
            placeholder="Pick a hub"
            onValueChange={(v) => {
                setValue(v);
                onChange(v);
            }}
        />
    );
}

function StatusSelect({ onChange }: { onChange: (v: string) => void }) {
    const [value, setValue] = React.useState("open");
    return (
        <Select
            value={value}
            onValueChange={(v) => {
                setValue(v);
                onChange(v);
            }}
        >
            <SelectTrigger aria-label="Status">
                <SelectValue />
            </SelectTrigger>
            <SelectContent>
                <SelectItem value="open">Open</SelectItem>
                <SelectItem value="done">Done</SelectItem>
            </SelectContent>
        </Select>
    );
}

function RowMenu({ onPick }: { onPick: (v: string) => void }) {
    return (
        <DropdownMenu>
            <DropdownMenuTrigger>Row actions</DropdownMenuTrigger>
            <DropdownMenuContent>
                <DropdownMenuItem onSelect={() => onPick("edit")}>Edit</DropdownMenuItem>
                <DropdownMenuItem variant="destructive" onSelect={() => onPick("delete")}>
                    Delete
                </DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
    );
}

function DatePopover({ onPick }: { onPick: (v: string) => void }) {
    const [open, setOpen] = React.useState(false);
    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger>Pick a date</PopoverTrigger>
            <PopoverContent aria-label="Dates">
                <button
                    type="button"
                    onClick={() => {
                        onPick("2026-10-14");
                        setOpen(false);
                    }}
                >
                    14 Oct 2026
                </button>
            </PopoverContent>
        </Popover>
    );
}

function Pickers(props: { onHub: (v: string) => void; onStatus: (v: string) => void; onMenu: (v: string) => void; onDate: (v: string) => void }) {
    return (
        <>
            <HubPicker onChange={props.onHub} />
            <StatusSelect onChange={props.onStatus} />
            <RowMenu onPick={props.onMenu} />
            <DatePopover onPick={props.onDate} />
            <Tooltip>
                <TooltipTrigger>Help</TooltipTrigger>
                <TooltipContent>Plate, hub or driver</TooltipContent>
            </Tooltip>
        </>
    );
}

const containers = {
    Dialog: (children: React.ReactNode) => (
        <Dialog open>
            <DialogContent>
                <DialogTitle>Edit trip</DialogTitle>
                <DialogDescription>Pick the hub, status and date.</DialogDescription>
                {children}
            </DialogContent>
        </Dialog>
    ),
    Sheet: (children: React.ReactNode) => (
        <Sheet open>
            <SheetContent>
                <SheetTitle>Filters</SheetTitle>
                <SheetDescription>Pick the hub, status and date.</SheetDescription>
                {children}
            </SheetContent>
        </Sheet>
    ),
} as const;

/** The Dialog or Sheet panel (a Popover content is a `dialog` role too). */
function modalPanel(): HTMLElement {
    const panel = document.querySelector<HTMLElement>("[data-slot=dialog-content], [data-slot=sheet-content]");
    expect(panel).not.toBeNull();
    return panel!;
}

/** The floating element is portalled to <body> (not inside the modal panel) and carries its layer class. */
function expectAboveModal(floating: HTMLElement, layer: "z-popover" | "z-tooltip") {
    const panel = modalPanel();
    const layered = floating.closest(`.${layer}`) as HTMLElement | null;
    expect(layered, `${layer} on the floating content`).not.toBeNull();
    expect(panel.contains(layered)).toBe(false);
    expect(panel.className).toContain("z-modal");
}

describe.each(Object.keys(containers) as (keyof typeof containers)[])("floating surfaces inside a %s", (kind) => {
    const wrap = containers[kind];

    function setup() {
        const calls = { onHub: vi.fn(), onStatus: vi.fn(), onMenu: vi.fn(), onDate: vi.fn() };
        render(wrap(<Pickers {...calls} />));
        return calls;
    }

    it("a SearchSelect opens above it, filters and picks", async () => {
        const user = userEvent.setup();
        const { onHub } = setup();
        await user.click(screen.getByRole("combobox", { name: "Hub" }));
        const search = await screen.findByPlaceholderText("Search...");
        expectAboveModal(search, "z-popover");
        await user.type(search, "บางปู");
        expect(screen.getAllByRole("option")).toHaveLength(1);
        await user.click(screen.getByRole("option", { name: /บางปู/ }));
        expect(onHub).toHaveBeenCalledWith("SPK-GW");
        expect(modalPanel()).toBeInTheDocument();
        await waitFor(() => expect(screen.queryByPlaceholderText("Search...")).not.toBeInTheDocument());
    });

    it("a compound Select opens above it and picks", async () => {
        const user = userEvent.setup();
        const { onStatus } = setup();
        await user.click(screen.getByRole("combobox", { name: "Status" }));
        const done = await screen.findByRole("option", { name: "Done" });
        expectAboveModal(done, "z-popover");
        await user.click(done);
        expect(onStatus).toHaveBeenCalledWith("done");
        expect(screen.getByRole("combobox", { name: "Status" })).toHaveTextContent("Done");
    });

    it("a DropdownMenu opens above it and picks an item", async () => {
        const user = userEvent.setup();
        const { onMenu } = setup();
        await user.click(screen.getByRole("button", { name: "Row actions" }));
        const menu = await screen.findByRole("menu");
        expectAboveModal(menu, "z-popover");
        await user.click(within(menu).getByRole("menuitem", { name: "Delete" }));
        expect(onMenu).toHaveBeenCalledWith("delete");
        await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
        expect(modalPanel()).toBeInTheDocument();
    });

    it("a Popover opens above it and its content takes clicks", async () => {
        const user = userEvent.setup();
        const { onDate } = setup();
        await user.click(screen.getByRole("button", { name: "Pick a date" }));
        const day = await screen.findByRole("button", { name: "14 Oct 2026" });
        expectAboveModal(day, "z-popover");
        await user.click(day);
        expect(onDate).toHaveBeenCalledWith("2026-10-14");
        await waitFor(() => expect(screen.queryByRole("button", { name: "14 Oct 2026" })).not.toBeInTheDocument());
        expect(modalPanel()).toBeInTheDocument();
    });

    it("a Tooltip shows on the top layer", async () => {
        const user = userEvent.setup();
        setup();
        await user.hover(screen.getByRole("button", { name: "Help" }));
        const tip = await screen.findByRole("tooltip");
        // Radix renders the accessible copy inside the visible content.
        expectAboveModal(tip.parentElement as HTMLElement, "z-tooltip");
        await act(async () => undefined);
    });
});
