// UI2: when the virtualised list's chunk cannot load, a long list shows its first rows and says how many
// there are (never thousands of rows in the DOM, never a silent cut: Appendix E §E.10), and the search
// still reaches every option.
import * as React from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

vi.mock("./search-select-virtual", () => {
    throw new Error("chunk failed to load");
});

import { FALLBACK_ROWS, SearchSelect } from "./search-select";

describe("SearchSelect without its virtualised chunk", () => {
    it("shows the first rows with a note, and finds the last option by search", async () => {
        vi.spyOn(console, "error").mockImplementation(() => undefined);
        const onChange = vi.fn();
        const options = Array.from({ length: 5000 }, (_, i) => ({ value: `V${i}`, label: `Row ${i}` }));
        render(<SearchSelect aria-label="Rows" options={options} value="" onValueChange={onChange} />);
        const u = userEvent.setup();
        await u.click(screen.getByRole("combobox", { name: "Rows" }));
        expect(await screen.findByText(`Showing ${FALLBACK_ROWS} of 5000. Type to narrow the list.`)).toBeInTheDocument();
        expect(screen.getAllByRole("option")).toHaveLength(FALLBACK_ROWS);

        await u.type(screen.getByPlaceholderText("Search..."), "Row 4999");
        await u.click(await screen.findByRole("option", { name: "Row 4999" }));
        expect(onChange).toHaveBeenCalledWith("V4999", expect.objectContaining({ value: "V4999" }));
    });
});
