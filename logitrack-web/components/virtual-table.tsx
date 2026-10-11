"use client";

/**
 * The virtualised table of the paged lists (developer-spec.md §10.10, W9; TW8): TanStack Table v8 for the
 * rows and columns, TanStack Virtual for the window. Only the rows in view (plus `overscan`) are in the
 * DOM, whatever the row count: a 5,000-row list keeps a few dozen `<tr>`. Above and below the window, one
 * spacer row each keeps the scroll height, so the table stays a real `<table>` (columns size from their
 * content, the header row sticks to the top of the viewport, `aria-rowcount` / `aria-rowindex` give
 * assistive technology the full extent).
 *
 * With `infinite` (a `useInfiniteQuery` over a keyset list), the next page is fetched when the window
 * comes within `fetchAheadRows` of the last loaded row, so scrolling reaches the end of the list without
 * a click; the page keeps its `LoadMore` footer for the count, the keyboard and the retry. The automatic
 * request waits while the query fetches anything (a background refetch is never cancelled: the query runs
 * one fetch at a time, and a next page started over it would drop the refetched rows) and stops after a
 * failed next page (no request loop; `LoadMore` retries it, and paging resumes once a page succeeds).
 * Filters belong in the query key and the request (W9), never over the loaded rows: a row the server has
 * not sent yet cannot be filtered in the browser.
 *
 *   <VirtualTable data={rows} columns={columns} getRowId={(r) => r.id}
 *                 infinite={{ hasNextPage, isFetchingNextPage, isFetching, isFetchNextPageError, fetchNextPage }}
 *                 error={isError} errorState={t("…")} aria-label={t("…")} />
 *
 * (A `useInfiniteQuery` result carries every field of `infinite`, so `infinite={query}` works too.)
 */
import { useEffect, useRef, type ReactNode } from "react";
import {
    flexRender,
    getCoreRowModel,
    useReactTable,
    type ColumnDef,
    type RowData,
} from "@tanstack/react-table";
import { useVirtualizer } from "@tanstack/react-virtual";

import { cn } from "@/lib/utils";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

declare module "@tanstack/react-table" {
    // The type parameters must match the library's declaration to merge with it.
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    interface ColumnMeta<TData extends RowData, TValue> {
        /** Classes of the column's `<th>`. */
        headerClassName?: string;
        /** Classes of every `<td>` of the column. */
        cellClassName?: string;
    }
}

/** The part of a `useInfiniteQuery` result the table drives. */
export interface VirtualTableInfinite {
    hasNextPage: boolean;
    isFetchingNextPage: boolean;
    /** Any fetch of the query, a background refetch included (an infinite query runs one fetch at a time). */
    isFetching?: boolean;
    /** The last next-page request failed: the table asks for no more pages; the page's `LoadMore` retries. */
    isFetchNextPageError?: boolean;
    fetchNextPage: (options?: { cancelRefetch?: boolean }) => unknown;
}

export const VIRTUAL_TABLE_DEFAULTS = {
    rowHeight: 48,
    maxHeight: 600,
    overscan: 8,
    fetchAheadRows: 20,
} as const;

export interface VirtualTableProps<TData> {
    data: TData[];
    /** Column definitions (`createColumnHelper<TData>()`); `meta.headerClassName` / `meta.cellClassName` style them. */
    // The value type differs per column; TanStack's own examples type the array this way.
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    columns: ColumnDef<TData, any>[];
    getRowId: (row: TData) => string;
    /** Accessible name of the scroll region. */
    "aria-label": string;
    /** Row height before measurement, px (rows are measured once rendered). */
    estimateRowHeight?: number;
    /** Height limit of the scroll viewport (px or a CSS length). */
    maxHeight?: number | string;
    /** Rows rendered beyond each edge of the viewport. */
    overscan?: number;
    /** Keyset paging: the next page loads as the window nears the last loaded row. */
    infinite?: VirtualTableInfinite;
    /** How close (in rows) to the last loaded row the next page is requested. */
    fetchAheadRows?: number;
    /** Shown in place of the rows while the first page loads. */
    loading?: boolean;
    loadingState?: ReactNode;
    /** Shown when there is no row. */
    emptyState?: ReactNode;
    /** The query failed: with no row to show, `errorState` replaces `emptyState` (a failure is not an empty list). */
    error?: boolean;
    errorState?: ReactNode;
    /** The viewport scrolls back to the top when this value changes (a new filter or search). */
    resetScrollKey?: unknown;
    className?: string;
    headerClassName?: string;
    headerRowClassName?: string;
    rowClassName?: string;
    onRowClick?: (row: TData) => void;
}

export function VirtualTable<TData>({
    data,
    columns,
    getRowId,
    "aria-label": ariaLabel,
    estimateRowHeight = VIRTUAL_TABLE_DEFAULTS.rowHeight,
    maxHeight = VIRTUAL_TABLE_DEFAULTS.maxHeight,
    overscan = VIRTUAL_TABLE_DEFAULTS.overscan,
    infinite,
    fetchAheadRows = VIRTUAL_TABLE_DEFAULTS.fetchAheadRows,
    loading = false,
    loadingState,
    emptyState,
    error = false,
    errorState,
    resetScrollKey,
    className,
    headerClassName,
    headerRowClassName,
    rowClassName,
    onRowClick,
}: VirtualTableProps<TData>) {
    const scrollRef = useRef<HTMLDivElement>(null);

    // TanStack Table and Virtual hand back mutable instances whose methods read the latest state; the
    // component reads them during render only, so React Compiler memoisation does not apply here.
    // eslint-disable-next-line react-hooks/incompatible-library
    const table = useReactTable({
        data,
        columns,
        getRowId: (row) => getRowId(row),
        getCoreRowModel: getCoreRowModel(),
    });
    const rows = table.getRowModel().rows;

    const virtualizer = useVirtualizer({
        count: rows.length,
        getScrollElement: () => scrollRef.current,
        estimateSize: () => estimateRowHeight,
        overscan,
        getItemKey: (index) => rows[index]?.id ?? index,
        // A row that is not laid out (a hidden tab, a test DOM) measures 0: keep the estimate, so a
        // zero-height window never renders every row.
        measureElement: (element) => (element as HTMLElement).offsetHeight || estimateRowHeight,
    });
    const items = virtualizer.getVirtualItems();
    const lastIndex = items.length > 0 ? items[items.length - 1].index : -1;

    // Infinite paging: the window reached the last `fetchAheadRows` loaded rows. Not while the query fetches
    // (`isFetching` is in the deps, so the request follows once a refetch settles), and not after a failed next
    // page: `hasNextPage` stays true then, and asking again on every settle would loop for as long as the
    // window stays at the end. `cancelRefetch: false` covers the render before the observer reports a refetch.
    const hasNextPage = infinite?.hasNextPage ?? false;
    const isFetchingNextPage = infinite?.isFetchingNextPage ?? false;
    const isFetching = infinite?.isFetching ?? false;
    const nextPageFailed = infinite?.isFetchNextPageError ?? false;
    const fetchNextPage = infinite?.fetchNextPage;
    useEffect(() => {
        if (!fetchNextPage || !hasNextPage || isFetchingNextPage || isFetching || nextPageFailed || lastIndex < 0) return;
        if (lastIndex >= rows.length - 1 - fetchAheadRows) void fetchNextPage({ cancelRefetch: false });
    }, [fetchNextPage, hasNextPage, isFetchingNextPage, isFetching, nextPageFailed, lastIndex, rows.length, fetchAheadRows]);

    // A new filter starts at the top of its list.
    const firstReset = useRef(true);
    useEffect(() => {
        if (firstReset.current) {
            firstReset.current = false;
            return;
        }
        if (scrollRef.current) scrollRef.current.scrollTop = 0;
    }, [resetScrollKey]);

    const columnCount = table.getVisibleLeafColumns().length;
    const paddingTop = items.length > 0 ? items[0].start : 0;
    const paddingBottom = items.length > 0 ? virtualizer.getTotalSize() - items[items.length - 1].end : 0;
    const placeholder = loading ? loadingState : rows.length === 0 ? (error ? errorState : emptyState) : null;

    return (
        <div
            ref={scrollRef}
            data-slot="virtual-table-viewport"
            role="region"
            aria-label={ariaLabel}
            // The viewport scrolls; keyboard users reach it with Tab.
            tabIndex={0}
            className={cn("relative w-full overflow-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring", className)}
            style={{ maxHeight }}
        >
            {/* The header row counts; -1 while more pages exist (the total is not known yet, WAI-ARIA). */}
            <table data-slot="table" className="w-full caption-bottom text-sm" aria-rowcount={hasNextPage ? -1 : rows.length + 1}>
                <TableHeader className={cn("sticky top-0 z-10 bg-card", headerClassName)}>
                    {table.getHeaderGroups().map((group) => (
                        <TableRow key={group.id} aria-rowindex={1} className={headerRowClassName}>
                            {group.headers.map((header) => (
                                <TableHead key={header.id} colSpan={header.colSpan} className={header.column.columnDef.meta?.headerClassName}>
                                    {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}
                                </TableHead>
                            ))}
                        </TableRow>
                    ))}
                </TableHeader>
                <TableBody>
                    {placeholder != null ? (
                        <TableRow className="hover:bg-transparent">
                            <TableCell colSpan={columnCount} className="h-24 text-center text-muted-foreground">
                                {placeholder}
                            </TableCell>
                        </TableRow>
                    ) : (
                        <>
                            {paddingTop > 0 && <tr aria-hidden="true" data-slot="virtual-spacer" style={{ height: paddingTop }} />}
                            {items.map((item) => {
                                const row = rows[item.index];
                                return (
                                    <TableRow
                                        key={row.id}
                                        ref={virtualizer.measureElement}
                                        data-index={item.index}
                                        aria-rowindex={item.index + 2}
                                        className={cn(onRowClick && "cursor-pointer", rowClassName)}
                                        onClick={onRowClick ? () => onRowClick(row.original) : undefined}
                                    >
                                        {row.getVisibleCells().map((cell) => (
                                            <TableCell key={cell.id} className={cell.column.columnDef.meta?.cellClassName}>
                                                {flexRender(cell.column.columnDef.cell, cell.getContext())}
                                            </TableCell>
                                        ))}
                                    </TableRow>
                                );
                            })}
                            {paddingBottom > 0 && <tr aria-hidden="true" data-slot="virtual-spacer" style={{ height: paddingBottom }} />}
                        </>
                    )}
                </TableBody>
            </table>
        </div>
    );
}
