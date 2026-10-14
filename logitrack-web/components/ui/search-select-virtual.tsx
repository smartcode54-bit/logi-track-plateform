"use client";

/**
 * The virtualised list of the searchable select (UI2). Loaded with `import()` by a select over more than
 * `VIRTUALIZE_ABOVE` options only (eslint.config.mjs LAZY_MODULES), so the pages that use short selects keep
 * `@tanstack/react-virtual` out of their initial JS.
 */
import * as React from "react";
import { useVirtualizer } from "@tanstack/react-virtual";

import type { SearchSelectOption } from "./search-select";

/**
 * The virtualised rows of a long list: only the rows in view (plus a few) are in the DOM, the rest are
 * spacing in the scroll height. The scroll element is cmdk's list, so cmdk's arrow keys scroll the
 * highlighted row into view and the virtualiser mounts the next ones.
 */
export function SearchSelectVirtualRows({
    options,
    scrollElement,
    resetKey,
    initialValue,
    renderRow,
    rowHeight,
}: {
    options: readonly SearchSelectOption[];
    /** cmdk's list element (state, not a ref: it is attached after this child's first layout effect). */
    scrollElement: HTMLDivElement | null;
    resetKey: string;
    initialValue?: string;
    renderRow: (option: SearchSelectOption) => React.ReactNode;
    /** Height of one row, px (`OPTION_ROW_HEIGHT`). */
    rowHeight: number;
}) {
    // The virtualizer is read during render only (see components/virtual-table.tsx).
    // eslint-disable-next-line react-hooks/incompatible-library
    const virtualizer = useVirtualizer({
        count: options.length,
        getScrollElement: () => scrollElement,
        estimateSize: () => rowHeight,
        overscan: 8,
    });
    const opened = React.useRef(false);
    React.useEffect(() => {
        if (opened.current) return;
        opened.current = true;
        const index = initialValue ? options.findIndex((o) => o.value === initialValue) : -1;
        if (index > 0) virtualizer.scrollToIndex(index, { align: "center" });
    }, [initialValue, options, virtualizer]);
    const lastKey = React.useRef(resetKey);
    React.useEffect(() => {
        if (lastKey.current === resetKey) return;
        lastKey.current = resetKey;
        virtualizer.scrollToOffset(0);
    }, [resetKey, virtualizer]);

    return (
        <div data-slot="search-select-virtual" className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
            {virtualizer.getVirtualItems().map((item) => {
                const option = options[item.index];
                return (
                    <div
                        key={option.value}
                        data-index={item.index}
                        className="absolute left-0 top-0 w-full"
                        style={{ height: item.size, transform: `translateY(${item.start}px)` }}
                    >
                        {renderRow(option)}
                    </div>
                );
            })}
        </div>
    );
}

