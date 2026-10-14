// TW8 / UI2: FilterCombobox over a long option list. Since UI2 it is the shared searchable select: a long
// list is virtualised (fewer rows in the DOM than options) and every option stays reachable by search
// (Appendix E §E.10: no silent caps). The search also matches an option's `searchText`, counts show as
// secondary text, and a selection with no option keeps its fallback label.
import React from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";

import { stubVirtualLayout } from "@/test-utils/virtualLayout";
import { FilterCombobox, FILTER_COMBOBOX_VIRTUALIZE_ABOVE, type FilterComboboxOption } from "./filter-combobox";

const options = (n: number): FilterComboboxOption[] =>
    Array.from({ length: n }, (_, i) => ({ value: `V${i}`, label: `Option ${i}`, searchText: `code-${i}`, count: i }));

function renderCombobox(list: FilterComboboxOption[], onChange = vi.fn(), value = "all") {
    render(
        <FilterCombobox
            options={list}
            value={value}
            onChange={onChange}
            allValue="all"
            allLabel="Everything"
            searchPlaceholder="Search"
            noResultsLabel="Nothing found"
            fallbackLabel={(v) => `Gone ${v}`}
            clearLabel="Show everything"
        />
    );
    return onChange;
}

let restore: () => void;
beforeAll(() => {
    globalThis.ResizeObserver ??= class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
    Element.prototype.scrollIntoView ??= () => {};
    restore = stubVirtualLayout({ viewport: 240, row: 32 });
});
afterAll(() => restore());

describe("FilterCombobox", () => {
    it("virtualises a long list and still finds its last option", async () => {
        const count = FILTER_COMBOBOX_VIRTUALIZE_ABOVE + 300;
        const onChange = renderCombobox(options(count));
        const u = userEvent.setup();
        await u.click(screen.getByRole("combobox"));
        await waitFor(() => expect(document.querySelector('[data-slot="search-select-virtual"]')).not.toBeNull());
        expect(document.querySelectorAll('[data-slot="search-select-option"]').length).toBeLessThan(60);
        await u.type(screen.getByPlaceholderText("Search"), `Option ${count - 1}`);
        await u.click(await screen.findByText(`Option ${count - 1}`));
        expect(onChange).toHaveBeenCalledWith(`V${count - 1}`);
    });

    it("matches the search on searchText as well as the label", async () => {
        const onChange = renderCombobox(options(20));
        const u = userEvent.setup();
        await u.click(screen.getByRole("combobox"));
        await u.type(screen.getByPlaceholderText("Search"), "CODE-17");
        expect(screen.getByText("Option 17")).toBeInTheDocument();
        expect(screen.queryByText("Option 1")).toBeNull();
        await u.click(screen.getByText("Option 17"));
        expect(onChange).toHaveBeenCalledWith("V17");
    });

    it("keeps a selection that lost its option readable, and clears back to every value", async () => {
        const onChange = renderCombobox(options(5), vi.fn(), "V99");
        expect(screen.getByRole("combobox")).toHaveTextContent("Gone V99");
        await userEvent.setup().click(screen.getByRole("button", { name: "Show everything" }));
        expect(onChange).toHaveBeenCalledWith("all");
    });
});
