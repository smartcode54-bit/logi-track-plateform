"use client";

/**
 * Paging of a list: one page of rows on screen at a time, Previous / Next under the table. `useListPager`
 * slices the rows the page already holds (the flattened pages of its `useInfiniteQuery`, after the page's own
 * client filter if it has one) and asks the server for the next keyset cursor (Appendix B §B.1.5) only when
 * the reader steps past the loaded rows: a list of thousands costs `pageSize` rows in the DOM and one request
 * per server page reached. A keyset cursor cannot jump to page N or count the list, so the footer says the
 * page number and the row range, and the total once the last server page has arrived.
 *
 *   const list = useInfiniteQuery(expensesQuery(filter));
 *   const rows = expenseRows(list.data?.pages);
 *   const { pageRows, pager } = useListPager(rows, list, filter);
 *   <VirtualTable data={pageRows} … />
 *   <ListPager {...pager} />
 *
 * `resetKey` is whatever the query key and the client filter are built from: a new filter starts at page 1.
 * Totals and exports keep reading every row (`rows`, or their own walk of the cursor), never `pageRows`.
 * A small in-memory list pages the same way with no source: `useListPager(rows)`.
 */
import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronLeft, ChevronRight, ChevronsLeft, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";
import { cn } from "@/lib/utils";

/** The part of a `useInfiniteQuery` result the pager drives (the query itself fits). */
export interface PagerSource {
    hasNextPage?: boolean;
    isFetchingNextPage?: boolean;
    /** Any fetch of the query: the next page is never requested over a running refetch. */
    isFetching?: boolean;
    isFetchNextPageError?: boolean;
    fetchNextPage?: (options?: { cancelRefetch?: boolean }) => Promise<unknown>;
}

export const LIST_PAGE_SIZE = 50;

/** Room above a list brought back into view: the app shell's sticky header (h-16) plus a small gap. */
const STICKY_HEADER_OFFSET_PX = 72;

export interface ListPagerProps {
    /** 1-based page number. */
    page: number;
    /** 1-based positions of the first and last row on screen (0 and 0 for an empty list). */
    from: number;
    to: number;
    /** Row count of the whole list, known once its last page is loaded. */
    total?: number;
    hasPrevious: boolean;
    hasNext: boolean;
    /** The next page is on its way. */
    loading: boolean;
    /** The last next-page request failed; Next retries it. */
    error: boolean;
    onFirst: () => void;
    onPrevious: () => void;
    onNext: () => void;
    className?: string;
}

export function useListPager<T>(rows: T[], source: PagerSource = {}, resetKey?: unknown, pageSize = LIST_PAGE_SIZE): { pageRows: T[]; pager: ListPagerProps } {
    const key = JSON.stringify(resetKey ?? null);
    const [at, setAt] = useState({ key, index: 0 });
    const loadedPages = Math.ceil(rows.length / pageSize);
    // A new filter starts at page 1; a list that came back shorter falls back to its last page.
    const index = at.key === key ? Math.min(at.index, Math.max(loadedPages - 1, 0)) : 0;
    const start = index * pageSize;
    const pageRows = useMemo(() => rows.slice(start, start + pageSize), [rows, start, pageSize]);
    const hasNextPage = Boolean(source.hasNextPage);
    const { fetchNextPage } = source;
    const busy = Boolean(source.isFetching || source.isFetchingNextPage);
    const failed = Boolean(source.isFetchNextPageError);

    // Next past the loaded rows records the page wanted; this asks the server for it once no fetch is running
    // (a press during a background refetch is kept, not dropped) and again while the page is still short (a
    // client filter may match nothing in a server page). A failed request stops it; Next retries.
    const wanting = at.key === key && at.index > index && hasNextPage;
    useEffect(() => {
        if (wanting && !busy && !failed) void fetchNextPage?.({ cancelRefetch: false });
    }, [wanting, busy, failed, fetchNextPage, rows.length]);

    return {
        pageRows,
        pager: {
            page: index + 1,
            from: pageRows.length > 0 ? start + 1 : 0,
            to: start + pageRows.length,
            total: hasNextPage ? undefined : rows.length,
            hasPrevious: index > 0,
            hasNext: index + 1 < loadedPages || hasNextPage,
            loading: Boolean(source.isFetchingNextPage) || (wanting && !failed),
            error: failed,
            onFirst: () => setAt({ key, index: 0 }),
            onPrevious: () => setAt({ key, index: Math.max(index - 1, 0) }),
            onNext: () => {
                setAt({ key, index: index + 1 });
                if (failed && index + 1 >= loadedPages) void fetchNextPage?.({ cancelRefetch: false });
            },
        },
    };
}

/** The footer of a paged list: row range, page number, First / Previous / Next. Nothing for an empty list. */
export function ListPager({ page, from, to, total, hasPrevious, hasNext, loading, error, onFirst, onPrevious, onNext, className }: ListPagerProps) {
    const { t } = useLanguage();
    const root = useRef<HTMLElement>(null);

    // A new page starts at its first row: the table's own scroll goes back to the top, and when the reader has
    // scrolled past the table's top (the usual case after pressing Next under a long page) the page scrolls back
    // up to it, below the sticky header. `block: "nearest"` never moved a table taller than the screen.
    const shown = useRef(page);
    useEffect(() => {
        if (shown.current === page) return;
        shown.current = page;
        const scope = root.current?.closest<HTMLElement>('[data-slot="card"]') ?? root.current?.parentElement;
        const table = scope?.querySelector<HTMLElement>('[data-slot="virtual-table-viewport"], [data-slot="table-container"]') ?? scope;
        if (!table) return;
        table.scrollTop = 0;
        if (table.getBoundingClientRect().top >= STICKY_HEADER_OFFSET_PX) return;
        table.style.scrollMarginTop = `${STICKY_HEADER_OFFSET_PX}px`;
        table.scrollIntoView?.({ block: "start" });
    }, [page]);

    if (to === 0 && !hasPrevious) return null;
    const n = (value: number) => value.toLocaleString();
    const range =
        total !== undefined
            ? t("common.pager.rowsOf", { from: n(from), to: n(to), total: n(total) })
            : t("common.pager.rows", { from: n(from), to: n(to) });
    const single = !hasPrevious && !hasNext;

    return (
        <nav
            ref={root}
            aria-label={t("common.pager.label")}
            className={cn("flex flex-wrap items-center justify-between gap-x-4 gap-y-2 text-sm text-muted-foreground", className)}
        >
            <p className="tabular-nums" aria-live="polite">
                {single ? t("common.pager.count", { count: n(to) }) : range}
            </p>
            {error && (
                <p role="alert" className="text-destructive">
                    {t("common.pager.error")}
                </p>
            )}
            {!single && (
                <div className="ml-auto flex items-center gap-1">
                    <Button type="button" variant="ghost" size="icon-sm" onClick={onFirst} disabled={!hasPrevious} aria-label={t("common.pager.first")} title={t("common.pager.first")}>
                        <ChevronsLeft />
                    </Button>
                    <Button type="button" variant="outline" size="sm" onClick={onPrevious} disabled={!hasPrevious}>
                        <ChevronLeft />
                        <span className="max-sm:sr-only">{t("common.pager.previous")}</span>
                    </Button>
                    <span className="min-w-16 px-2 text-center font-medium tabular-nums text-foreground" aria-current="page">
                        {t("common.pager.page", { page: n(page) })}
                    </span>
                    <Button type="button" variant="outline" size="sm" onClick={onNext} disabled={!hasNext || loading}>
                        <span className="max-sm:sr-only">{error ? t("common.pager.retry") : t("common.pager.next")}</span>
                        {loading ? <Loader2 className="animate-spin" /> : <ChevronRight />}
                    </Button>
                </div>
            )}
        </nav>
    );
}
