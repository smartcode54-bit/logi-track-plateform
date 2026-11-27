import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, renderHook, screen } from "@testing-library/react";

import { LIST_PAGE_SIZE, ListPager, type ListPagerProps, setListPageSize, useListPager } from "./list-pager";

vi.mock("@/context/language", () => ({ useLanguage: () => ({ t: (key: string) => key }) }));

const rows = (n: number) => Array.from({ length: n }, (_, i) => i);

describe("useListPager", () => {
    it("shows one page of the loaded rows and steps through them without a request", () => {
        const fetchNextPage = vi.fn(() => Promise.resolve());
        const { result } = renderHook(() => useListPager(rows(120), { hasNextPage: false, fetchNextPage }, "k", 50));
        expect(result.current.pageRows).toHaveLength(50);
        expect(result.current.pager).toMatchObject({ page: 1, from: 1, to: 50, total: 120, hasPrevious: false, hasNext: true });
        act(() => result.current.pager.onNext());
        act(() => result.current.pager.onNext());
        expect(result.current.pager).toMatchObject({ page: 3, from: 101, to: 120, hasNext: false });
        expect(fetchNextPage).not.toHaveBeenCalled();
        act(() => result.current.pager.onFirst());
        expect(result.current.pager.page).toBe(1);
    });

    it("asks the server for the next cursor only past the loaded rows, and moves when the rows arrive", () => {
        let loaded = rows(50);
        let source = { hasNextPage: true, isFetching: true, isFetchNextPageError: false, fetchNextPage: vi.fn(() => Promise.resolve()) };
        const { result, rerender } = renderHook(() => useListPager(loaded, source, "k", 50));
        expect(result.current.pager.total).toBeUndefined();
        // Pressed during a background refetch: kept, and requested once the refetch settles.
        act(() => result.current.pager.onNext());
        expect(source.fetchNextPage).not.toHaveBeenCalled();
        expect(result.current.pager).toMatchObject({ page: 1, loading: true });
        source = { ...source, isFetching: false };
        rerender();
        expect(source.fetchNextPage).toHaveBeenCalledTimes(1);
        loaded = rows(80);
        source = { ...source, hasNextPage: false };
        rerender();
        expect(result.current.pager).toMatchObject({ page: 2, from: 51, to: 80, total: 80, loading: false });
    });

    it("stops after a failed next page and retries it on Next", () => {
        const source = { hasNextPage: true, isFetching: false, isFetchNextPageError: true, fetchNextPage: vi.fn(() => Promise.resolve()) };
        const { result, rerender } = renderHook(() => useListPager(rows(50), source, "k", 50));
        rerender();
        expect(source.fetchNextPage).not.toHaveBeenCalled();
        act(() => result.current.pager.onNext());
        expect(source.fetchNextPage).toHaveBeenCalledTimes(1);
        expect(result.current.pager).toMatchObject({ page: 1, error: true, loading: false });
    });

    it("starts a new filter at page 1 and falls back when the list shrinks", () => {
        let key = "a";
        let loaded = rows(120);
        const { result, rerender } = renderHook(() => useListPager(loaded, {}, key, 50));
        act(() => result.current.pager.onNext());
        act(() => result.current.pager.onNext());
        loaded = rows(60);
        rerender();
        expect(result.current.pager.page).toBe(2);
        key = "b";
        rerender();
        expect(result.current.pager.page).toBe(1);
    });
});

describe("useListPager page size", () => {
    beforeEach(() => {
        localStorage.clear();
        act(() => setListPageSize(LIST_PAGE_SIZE));
    });

    it("pages by the reader's choice, keeps the first row on screen and stores the choice", () => {
        const { result } = renderHook(() => useListPager(rows(120), {}, "k"));
        expect(result.current.pager).toMatchObject({ pageSize: 50, page: 1, to: 50 });
        act(() => result.current.pager.onNext());
        act(() => result.current.pager.onPageSizeChange?.(10));
        expect(result.current.pager).toMatchObject({ pageSize: 10, page: 6, from: 51, to: 60 });
        expect(localStorage.getItem("listPager.pageSize")).toBe("10");
        act(() => result.current.pager.onPageSizeChange?.(100));
        expect(result.current.pager).toMatchObject({ page: 1, from: 1, to: 100 });
    });

    it("applies the choice to every list on the page", () => {
        const a = renderHook(() => useListPager(rows(120), {}, "a"));
        const b = renderHook(() => useListPager(rows(120), {}, "b"));
        act(() => a.result.current.pager.onPageSizeChange?.(25));
        expect(b.result.current.pager).toMatchObject({ pageSize: 25, to: 25 });
    });

    it("fills a page larger than the server's page from the next cursor", () => {
        act(() => setListPageSize(100));
        const source = { hasNextPage: true, isFetching: false, fetchNextPage: vi.fn(() => Promise.resolve()) };
        const { result } = renderHook(() => useListPager(rows(50), source, "k"));
        expect(source.fetchNextPage).toHaveBeenCalledTimes(1);
        expect(result.current.pager).toMatchObject({ page: 1, to: 50, loading: true });
    });

    it("has no picker for a list with a fixed size", () => {
        const { result } = renderHook(() => useListPager(rows(120), {}, "k", 50));
        expect(result.current.pager.onPageSizeChange).toBeUndefined();
    });
});

describe("ListPager", () => {
    const props = (page: number): ListPagerProps => ({
        page, from: (page - 1) * 50 + 1, to: page * 50, hasPrevious: page > 1, hasNext: true, loading: false, error: false,
        onFirst: vi.fn(), onPrevious: vi.fn(), onNext: vi.fn(),
    });
    const mount = (tableTop: number) => {
        const ui = (page: number) => (
            <div data-slot="card">
                <div data-slot="table-container" />
                <ListPager {...props(page)} />
            </div>
        );
        const view = render(ui(1));
        const table = view.container.querySelector<HTMLElement>('[data-slot="table-container"]')!;
        table.scrollTop = 120;
        table.getBoundingClientRect = () => ({ top: tableTop }) as DOMRect;
        table.scrollIntoView = vi.fn();
        return { table, next: () => view.rerender(ui(2)) };
    };

    it("brings the table's top back below the sticky header when a new page opens past it", () => {
        const { table, next } = mount(-900);
        next();
        expect(table.scrollTop).toBe(0);
        expect(table.style.scrollMarginTop).toBe("72px");
        expect(table.scrollIntoView).toHaveBeenCalledWith({ block: "start" });
    });

    it("shows the page size picker only for a list longer than the smallest size", () => {
        const one = (to: number): ListPagerProps => ({ ...props(1), from: 1, to, hasNext: false, pageSize: 50, onPageSizeChange: vi.fn() });
        const view = render(<ListPager {...one(30)} />);
        expect(screen.getByRole("combobox", { name: "common.pager.pageSize" })).toBeTruthy();
        view.rerender(<ListPager {...one(8)} />);
        expect(screen.queryByRole("combobox", { name: "common.pager.pageSize" })).toBeNull();
        view.rerender(<ListPager {...props(2)} />);
        expect(screen.queryByRole("combobox", { name: "common.pager.pageSize" })).toBeNull();
    });

    it("leaves the page where it is when the table's top is already on screen", () => {
        const { table, next } = mount(300);
        next();
        expect(table.scrollTop).toBe(0);
        expect(table.scrollIntoView).not.toHaveBeenCalled();
    });
});
