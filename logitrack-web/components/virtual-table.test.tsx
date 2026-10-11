// TW8 (issue #88; developer-spec.md §10.10 W9, §10.14 item 9): the virtualised table keeps a bounded
// number of rows in the DOM whatever the list size (AC: a 5,000-row list keeps < 100 DOM rows), still
// reaches the last row by scrolling, and pages a keyset list as the window nears its end (never over a
// running fetch, never again after a failed page: features/trucks/components/AssignmentHistory.test.tsx
// runs the same against a Go origin with latency).
import React from "react";
import { render, screen } from "@testing-library/react";
import { createColumnHelper } from "@tanstack/react-table";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";

import { VirtualTable } from "./virtual-table";
import { scrollViewport, stubVirtualLayout } from "@/test-utils/virtualLayout";

interface Item {
    id: string;
    name: string;
    n: number;
}

const ROW = 40;
const VIEWPORT = 600;
const col = createColumnHelper<Item>();
const columns = [
    col.accessor("name", { header: "Name", meta: { headerClassName: "head-name", cellClassName: "cell-name" } }),
    col.accessor("n", { header: () => "Number", cell: (info) => `#${info.getValue()}` }),
];
const items = (count: number, from = 0): Item[] =>
    Array.from({ length: count }, (_, i) => ({ id: `r-${from + i}`, name: `Row ${from + i}`, n: from + i }));

let restore: () => void;
beforeAll(() => {
    restore = stubVirtualLayout({ viewport: VIEWPORT, row: ROW });
});
afterAll(() => restore());

const bodyRows = (container: HTMLElement) => container.querySelectorAll("tbody tr[data-index]");
const viewportOf = () => screen.getByRole("region", { name: "Rows" });

describe("VirtualTable", () => {
    it("keeps fewer than 100 rows in the DOM for 5,000 rows and reaches the last one by scrolling", () => {
        const data = items(5000);
        const { container } = render(
            <VirtualTable data={data} columns={columns} getRowId={(r) => r.id} aria-label="Rows" estimateRowHeight={ROW} maxHeight={VIEWPORT} />
        );
        const first = bodyRows(container);
        expect(first.length).toBeGreaterThan(0);
        expect(first.length).toBeLessThan(100);
        // Every row of the list counts for assistive technology, rendered or not.
        expect(container.querySelector("table")).toHaveAttribute("aria-rowcount", "5001");
        expect(screen.getByText("Row 0")).toBeInTheDocument();
        expect(screen.queryByText("Row 4999")).toBeNull();
        // Column meta styles the header and the cells.
        expect(screen.getByText("Name").closest("th")).toHaveClass("head-name");
        expect(screen.getByText("Row 0").closest("td")).toHaveClass("cell-name");

        // The spacers keep the full scroll height: rendered rows + spacers = 5,000 rows.
        const spacer = (el: Element | null) => (el ? parseFloat((el as HTMLElement).style.height || "0") : 0);
        const height = () =>
            [...container.querySelectorAll('tr[data-slot="virtual-spacer"]')].reduce((sum, el) => sum + spacer(el), 0) +
            bodyRows(container).length * ROW;
        expect(height()).toBe(5000 * ROW);

        scrollViewport(viewportOf(), "end", ROW, 5000);
        const last = bodyRows(container);
        expect(last.length).toBeLessThan(100);
        expect(screen.getByText("Row 4999")).toBeInTheDocument();
        expect(screen.queryByText("Row 0")).toBeNull();
        expect(last[last.length - 1]).toHaveAttribute("aria-rowindex", "5001");
        expect(height()).toBe(5000 * ROW);
    });

    it("loads the next keyset page as the window nears the last loaded row, once per page", () => {
        const fetchNextPage = vi.fn();
        const view = (data: Item[], infinite: { hasNextPage: boolean; isFetchingNextPage: boolean }) => (
            <VirtualTable
                data={data}
                columns={columns}
                getRowId={(r) => r.id}
                aria-label="Rows"
                estimateRowHeight={ROW}
                maxHeight={VIEWPORT}
                infinite={{ ...infinite, fetchNextPage }}
            />
        );
        const { rerender } = render(view(items(100), { hasNextPage: true, isFetchingNextPage: false }));
        // The top of the first page: nothing to fetch yet.
        expect(fetchNextPage).not.toHaveBeenCalled();
        scrollViewport(viewportOf(), "end", ROW, 100);
        expect(fetchNextPage).toHaveBeenCalledTimes(1);

        // While the page is in flight, more scrolling asks for nothing.
        rerender(view(items(100), { hasNextPage: true, isFetchingNextPage: true }));
        scrollViewport(viewportOf(), 100 * ROW - VIEWPORT - 10, ROW, 100);
        expect(fetchNextPage).toHaveBeenCalledTimes(1);

        // The page arrived (200 rows, the window is mid-list): no request until the new end is near.
        rerender(view(items(200), { hasNextPage: true, isFetchingNextPage: false }));
        expect(fetchNextPage).toHaveBeenCalledTimes(1);
        scrollViewport(viewportOf(), "end", ROW, 200);
        expect(fetchNextPage).toHaveBeenCalledTimes(2);

        // While pages remain, the row count is unknown to assistive technology.
        expect(document.querySelector("table")).toHaveAttribute("aria-rowcount", "-1");

        // The last page: the end of the list fetches nothing, and the count is known.
        rerender(view(items(250), { hasNextPage: false, isFetchingNextPage: false }));
        expect(document.querySelector("table")).toHaveAttribute("aria-rowcount", "251");
        scrollViewport(viewportOf(), "end", ROW, 250);
        expect(fetchNextPage).toHaveBeenCalledTimes(2);
        expect(screen.getByText("Row 249")).toBeInTheDocument();
    });

    it("asks for no page while the query fetches or after a failed next page, and never cancels a refetch", () => {
        const fetchNextPage = vi.fn();
        type State = { isFetching?: boolean; isFetchNextPageError?: boolean };
        const view = (state: State) => (
            <VirtualTable
                data={items(100)}
                columns={columns}
                getRowId={(r) => r.id}
                aria-label="Rows"
                estimateRowHeight={ROW}
                maxHeight={VIEWPORT}
                infinite={{ hasNextPage: true, isFetchingNextPage: false, ...state, fetchNextPage }}
            />
        );
        // A background refetch (an invalidation, window focus) is in flight when the window reaches the end.
        const { rerender } = render(view({ isFetching: true }));
        scrollViewport(viewportOf(), "end", ROW, 100);
        expect(fetchNextPage).not.toHaveBeenCalled();
        // It settled: the page follows without another scroll, and would not cancel a fetch it raced.
        rerender(view({ isFetching: false }));
        expect(fetchNextPage).toHaveBeenCalledTimes(1);
        expect(fetchNextPage).toHaveBeenCalledWith({ cancelRefetch: false });

        // That page failed: settling again (and further scrolling) asks for nothing; LoadMore is the retry.
        rerender(view({ isFetching: false, isFetchNextPageError: true }));
        scrollViewport(viewportOf(), 100 * ROW - VIEWPORT - 5, ROW, 100);
        scrollViewport(viewportOf(), "end", ROW, 100);
        expect(fetchNextPage).toHaveBeenCalledTimes(1);
        // A page succeeded (the retry or a refetch): paging resumes.
        rerender(view({ isFetching: false, isFetchNextPageError: false }));
        expect(fetchNextPage).toHaveBeenCalledTimes(2);
    });

    it("shows the error state instead of the empty state when the query failed with no row", () => {
        const view = (data: Item[], error: boolean) => (
            <VirtualTable data={data} columns={columns} getRowId={(r) => r.id} aria-label="Rows" emptyState="Nothing" error={error} errorState="Failed" />
        );
        const { rerender, container } = render(view([], true));
        expect(screen.getByText("Failed").closest("td")).toHaveAttribute("colspan", "2");
        expect(screen.queryByText("Nothing")).toBeNull();
        rerender(view([], false));
        expect(screen.getByText("Nothing")).toBeInTheDocument();
        // Rows on screen stay on screen when a later fetch fails (a refetch or a next page).
        rerender(view(items(3), true));
        expect(screen.queryByText("Failed")).toBeNull();
        expect(bodyRows(container)).toHaveLength(3);
    });

    it("shows the loading and empty states in one full-width row", () => {
        const { rerender, container } = render(
            <VirtualTable data={[]} columns={columns} getRowId={(r) => r.id} aria-label="Rows" loading loadingState="Loading…" emptyState="Nothing" />
        );
        expect(screen.getByText("Loading…").closest("td")).toHaveAttribute("colspan", "2");
        expect(screen.queryByText("Nothing")).toBeNull();
        rerender(<VirtualTable data={[]} columns={columns} getRowId={(r) => r.id} aria-label="Rows" loadingState="Loading…" emptyState="Nothing" />);
        expect(screen.getByText("Nothing")).toBeInTheDocument();
        expect(bodyRows(container)).toHaveLength(0);
    });

    it("returns to the top when the filter changes", () => {
        const data = items(1000);
        const view = (key: string) => (
            <VirtualTable data={data} columns={columns} getRowId={(r) => r.id} aria-label="Rows" estimateRowHeight={ROW} resetScrollKey={key} />
        );
        const { rerender } = render(view("a"));
        scrollViewport(viewportOf(), 20_000, ROW, 1000);
        expect(viewportOf().scrollTop).toBe(20_000);
        rerender(view("a"));
        expect(viewportOf().scrollTop).toBe(20_000);
        rerender(view("b"));
        expect(viewportOf().scrollTop).toBe(0);
    });

    it("calls onRowClick with the row's data", () => {
        const onRowClick = vi.fn();
        render(<VirtualTable data={items(3)} columns={columns} getRowId={(r) => r.id} aria-label="Rows" onRowClick={onRowClick} />);
        screen.getByText("Row 2").click();
        expect(onRowClick).toHaveBeenCalledWith({ id: "r-2", name: "Row 2", n: 2 });
    });
});
