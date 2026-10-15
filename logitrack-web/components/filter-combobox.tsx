"use client";

import { useMemo, type ReactNode } from "react";

import { SearchSelect, VIRTUALIZE_ABOVE, type SearchSelectOption } from "@/components/ui/search-select";
import { cn } from "@/lib/utils";

/** One selectable option. `badge` is rendered next to the label (e.g. "not in fleet"). */
export interface FilterComboboxOption {
    value: string;
    label: string;
    count?: number;
    badge?: string;
    /** More text the search matches besides the label (a hub's code and other-language name). */
    searchText?: string;
}

/**
 * Above this many matching options the open list is virtualised (TanStack Virtual, TW8): only the rows in
 * view are in the DOM, so a list of every hub (hundreds) opens as fast as a short one. It is the shared
 * searchable select's threshold (UI2).
 */
export const FILTER_COMBOBOX_VIRTUALIZE_ABOVE = VIRTUALIZE_ABOVE;

export interface FilterComboboxProps {
    options: FilterComboboxOption[];
    value: string;
    onChange: (value: string) => void;
    /** Sentinel meaning "no filter" — rendered as the first row and styled as placeholder text. */
    allValue: string;
    /** Label for the "no filter" row. */
    allLabel: string;
    searchPlaceholder: string;
    noResultsLabel: string;
    /** Leading icon inside the trigger. */
    icon?: ReactNode;
    className?: string;
    /** Rendered above the control (Billing Document uses a label; the filter bar does not). */
    label?: string;
    /** Fallback trigger text when the selection has no matching option — see `triggerText`. */
    fallbackLabel?: (value: string) => string;
    /** Tooltip/aria-label for the inline clear (✕). Omit to leave the control without one. */
    clearLabel?: string;
}

/**
 * Searchable filter combobox shared by the plate, origin and destination filters, and the hub filter of the
 * task boards (every hub of `['hubs']`, no cap: Appendix E §E.10 row 7). Since UI2 it is the shared
 * searchable select (`components/ui/search-select.tsx`) with a "no filter" row first, the row counts as
 * secondary text and the badges after the label.
 *
 * The plate and place filters build their options from the loaded rows (not from a master collection), so
 * each carries its row count and values with no master record are badged rather than hidden.
 */
export function FilterCombobox({
    options,
    value,
    onChange,
    allValue,
    allLabel,
    searchPlaceholder,
    noResultsLabel,
    icon,
    className,
    label,
    fallbackLabel,
    clearLabel,
}: FilterComboboxProps) {
    const items = useMemo<SearchSelectOption[]>(
        () => [
            { value: allValue, label: allLabel },
            ...options.map((o) => ({
                value: o.value,
                label: o.badge ? (
                    <>
                        {o.label}
                        <span className="ml-2 text-xs text-warning">{o.badge}</span>
                    </>
                ) : (
                    o.label
                ),
                text: o.label,
                keywords: o.searchText ? [o.searchText] : undefined,
                description: typeof o.count === "number" ? <span className="tabular-nums">{o.count}</span> : undefined,
            })),
        ],
        [options, allValue, allLabel]
    );

    // The selection can outlive its option (e.g. the date range narrowed) — keep it readable rather than
    // silently resetting to "all", which would misrepresent what the table is showing.
    const missing = value !== allValue && !options.some((o) => o.value === value);
    const selectedOption = missing ? { value, label: fallbackLabel?.(value) ?? value } : null;

    return (
        <div className={cn("relative", className)}>
            {label && <p className="text-xs text-muted-foreground mb-1">{label}</p>}
            <SearchSelect
                value={value}
                onValueChange={(v) => onChange(v)}
                options={items}
                selectedOption={selectedOption}
                renderValue={(o) => (o.value === allValue ? <span className="text-muted-foreground">{allLabel}</span> : (o.text ?? o.label))}
                searchable
                searchPlaceholder={searchPlaceholder}
                emptyText={noResultsLabel}
                icon={icon}
                clearable={!!clearLabel}
                clearValue={allValue}
                clearLabel={clearLabel}
            />
        </div>
    );
}
