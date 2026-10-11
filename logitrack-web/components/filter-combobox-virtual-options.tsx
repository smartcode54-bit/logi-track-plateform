"use client";

/**
 * The virtualised open list of `FilterCombobox` (TW8). Loaded with `import()` by a combobox over more
 * than `FILTER_COMBOBOX_VIRTUALIZE_ABOVE` options only (eslint.config.mjs LAZY_MODULES), so the pages
 * that use the combobox keep `@tanstack/react-virtual` out of their initial JS.
 */
import { useEffect, useRef } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";

import type { FilterComboboxOption } from "./filter-combobox";
import { FilterComboboxRow as Row } from "./filter-combobox-row";

/** Height of one option row (`py-1.5` + `text-sm`), px. */
const OPTION_ROW_HEIGHT = 32;

export interface VirtualOptionsProps {
    options: FilterComboboxOption[];
    value: string;
    allValue: string;
    allLabel: string;
    search: string;
    onSelect: (value: string) => void;
}

/**
 * The "no filter" row first, then the options, only those in view rendered (the rest are spacing in
 * the scroll height). A new search starts at the top.
 */
export function FilterComboboxVirtualOptions({ options, value, allValue, allLabel, search, onSelect }: VirtualOptionsProps) {
    const scrollRef = useRef<HTMLDivElement>(null);
    // The virtualizer is read during render only (see components/virtual-table.tsx).
    // eslint-disable-next-line react-hooks/incompatible-library
    const virtualizer = useVirtualizer({
        count: options.length + 1,
        getScrollElement: () => scrollRef.current,
        estimateSize: () => OPTION_ROW_HEIGHT,
        overscan: 6,
    });
    useEffect(() => {
        if (scrollRef.current) scrollRef.current.scrollTop = 0;
    }, [search]);

    return (
        <div ref={scrollRef} data-slot="filter-combobox-virtual-list" className="max-h-[240px] overflow-y-auto p-1">
            <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
                {virtualizer.getVirtualItems().map((item) => {
                    const option = item.index === 0 ? null : options[item.index - 1];
                    return (
                        <div
                            key={option ? option.value : allValue}
                            data-index={item.index}
                            className="absolute left-0 top-0 w-full"
                            style={{ height: item.size, transform: `translateY(${item.start}px)` }}
                        >
                            {option ? (
                                <Row
                                    selected={option.value === value}
                                    onClick={() => onSelect(option.value)}
                                    label={option.label}
                                    count={option.count}
                                    badge={option.badge}
                                />
                            ) : (
                                <Row selected={value === allValue} onClick={() => onSelect(allValue)} label={allLabel} />
                            )}
                        </div>
                    );
                })}
            </div>
        </div>
    );
}
