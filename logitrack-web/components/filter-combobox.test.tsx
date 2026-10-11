// TW8: FilterCombobox over a long option list. The virtualised list is a separate chunk; when that chunk
// cannot load, the combobox falls back to the plain list with every row, so no option is ever hidden
// (Appendix E §E.10: no silent caps). The search also matches an option's `searchText`.
import React from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

vi.mock("./filter-combobox-virtual-options", () => {
    throw new Error("chunk failed to load");
});

import { FilterCombobox, FILTER_COMBOBOX_VIRTUALIZE_ABOVE, type FilterComboboxOption } from "./filter-combobox";

const options = (n: number): FilterComboboxOption[] =>
    Array.from({ length: n }, (_, i) => ({ value: `V${i}`, label: `Option ${i}`, searchText: `code-${i}` }));

function renderCombobox(list: FilterComboboxOption[], onChange = vi.fn()) {
    render(
        <FilterCombobox
            options={list}
            value="all"
            onChange={onChange}
            allValue="all"
            allLabel="Everything"
            searchPlaceholder="Search"
            noResultsLabel="Nothing found"
        />
    );
    return onChange;
}

describe("FilterCombobox without its virtualised chunk", () => {
    it("lists every option of a long list in the plain list", async () => {
        vi.spyOn(console, "error").mockImplementation(() => undefined);
        const count = FILTER_COMBOBOX_VIRTUALIZE_ABOVE + 50;
        renderCombobox(options(count));
        await userEvent.setup().click(screen.getByRole("combobox"));
        await waitFor(() => expect(screen.getByText(`Option ${count - 1}`)).toBeInTheDocument());
        for (let i = 0; i < count; i++) expect(screen.getByText(`Option ${i}`)).toBeInTheDocument();
        expect(document.querySelector('[data-slot="filter-combobox-virtual-list"]')).toBeNull();
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
});
