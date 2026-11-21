import { describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";

import { useListPager } from "./list-pager";

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
