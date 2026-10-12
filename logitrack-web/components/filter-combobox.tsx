"use client";

import { Component, Suspense, lazy, useEffect, useMemo, useState, type ReactNode } from "react";
import { ChevronsUpDown, Search, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { FilterComboboxRow as Row } from "./filter-combobox-row";

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
 * Above this many matching options the open list is virtualised (TanStack Virtual, TW8): only the
 * rows in view are in the DOM, so a list of every hub (hundreds) opens as fast as a short one. Shorter
 * lists render every row, as before.
 */
export const FILTER_COMBOBOX_VIRTUALIZE_ABOVE = 100;

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

const loadVirtualOptions = () => import("./filter-combobox-virtual-options");

/**
 * The virtualised list, in its own chunk, so `@tanstack/react-virtual` stays out of the initial JS of
 * the pages that use the combobox (developer-spec.md §10.11). A combobox with more than
 * `FILTER_COMBOBOX_VIRTUALIZE_ABOVE` options preloads it when it mounts, before the first open; until
 * it is in, or if its chunk fails, the plain list renders every row (nothing is hidden either way).
 */
const VirtualOptions = lazy(() => loadVirtualOptions().then((m) => ({ default: m.FilterComboboxVirtualOptions })));

/** Renders `fallback` (the plain list) when the virtualised list's chunk fails to load. */
class VirtualListBoundary extends Component<{ fallback: ReactNode; children: ReactNode }, { failed: boolean }> {
    state = { failed: false };
    static getDerivedStateFromError() {
        return { failed: true };
    }
    render() {
        return this.state.failed ? this.props.fallback : this.props.children;
    }
}

/**
 * Searchable filter combobox shared by the plate, origin and destination filters, and the hub filter
 * of the task boards (every hub of `['hubs']`, no cap: Appendix E §E.10 row 7).
 *
 * The plate and place filters build their options from the loaded rows (not from a master collection),
 * so each carries its row count and values with no master record are badged rather than hidden.
 *
 * Hand-rolled popover rather than Radix Select, mirroring features/tasks/components/TruckPlateField:
 * it renders inside the current stacking context, so it also works inside a Dialog without the
 * z-index dance a portalled SelectContent needs.
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
    const [open, setOpen] = useState(false);
    const [search, setSearch] = useState("");
    const long = options.length > FILTER_COMBOBOX_VIRTUALIZE_ABOVE;
    useEffect(() => {
        if (long) loadVirtualOptions().catch(() => undefined);
    }, [long]);

    const selected = useMemo(() => options.find((o) => o.value === value), [options, value]);

    const matching = useMemo(() => {
        const term = search.trim().toLowerCase();
        if (!term) return options;
        return options.filter(
            (o) => o.label.toLowerCase().includes(term) || (o.searchText?.toLowerCase().includes(term) ?? false)
        );
    }, [options, search]);

    // The selection can outlive its option (e.g. the date range narrowed) — keep it readable rather
    // than silently resetting to "all", which would misrepresent what the table is showing.
    const triggerText =
        value === allValue ? allLabel : (selected?.label ?? fallbackLabel?.(value) ?? value);

    const select = (next: string) => {
        onChange(next);
        setOpen(false);
        setSearch("");
    };

    // Every row: lists up to the threshold, and the fallback of the virtualised one.
    const plainList = open ? (
        <div className="max-h-[240px] overflow-y-auto p-1">
            <Row selected={value === allValue} onClick={() => select(allValue)} label={allLabel} />
            {matching.length === 0 ? (
                <div className="py-4 text-center text-sm text-muted-foreground">{noResultsLabel}</div>
            ) : (
                matching.map((option) => (
                    <Row
                        key={option.value}
                        selected={option.value === value}
                        onClick={() => select(option.value)}
                        label={option.label}
                        count={option.count}
                        badge={option.badge}
                    />
                ))
            )}
        </div>
    ) : null;

    return (
        <div className={cn("relative", className)}>
            {label && <p className="text-xs text-muted-foreground mb-1">{label}</p>}
            <button
                type="button"
                role="combobox"
                aria-expanded={open}
                className={cn(
                    "flex h-9 w-full items-center justify-between gap-2 rounded-md border border-input bg-background px-3 py-2 text-sm",
                    "ring-offset-background focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2",
                    value === allValue && "text-muted-foreground"
                )}
                onClick={() => {
                    setOpen(!open);
                    setSearch("");
                }}
            >
                <span className="flex min-w-0 items-center gap-2">
                    {icon}
                    <span className="truncate">{triggerText}</span>
                </span>
                <span className="flex shrink-0 items-center gap-1">
                    {/* Clearing the filter is one click from the trigger, no need to reopen and hunt
                        for the "all" row. A bare icon rather than a <button>: this sits inside the
                        trigger button, and nesting buttons is invalid HTML (same reason
                        ui/date-picker and tasks/HelperDriverField do it this way). */}
                    {value !== allValue && clearLabel && (
                        <span
                            role="button"
                            aria-label={clearLabel}
                            title={clearLabel}
                            className="opacity-60 hover:opacity-100 hover:text-destructive"
                            onClick={(e) => {
                                e.preventDefault();
                                e.stopPropagation();
                                select(allValue);
                            }}
                        >
                            <X className="h-4 w-4" />
                        </span>
                    )}
                    <ChevronsUpDown className="h-4 w-4 opacity-50" />
                </span>
            </button>

            {open && (
                <>
                    <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
                    <div className="absolute top-[calc(100%+4px)] left-0 w-full min-w-[240px] z-50 rounded-md border bg-popover text-popover-foreground shadow-md">
                        <div className="flex items-center border-b px-3">
                            <Search className="mr-2 h-4 w-4 shrink-0 opacity-50" />
                            <input
                                className="flex h-10 w-full rounded-md bg-transparent py-3 text-sm outline-none placeholder:text-muted-foreground"
                                placeholder={searchPlaceholder}
                                value={search}
                                onChange={(e) => setSearch(e.target.value)}
                                autoFocus
                            />
                        </div>
                        {matching.length > FILTER_COMBOBOX_VIRTUALIZE_ABOVE ? (
                            <VirtualListBoundary fallback={plainList}>
                                <Suspense fallback={plainList}>
                                    <VirtualOptions
                                        options={matching}
                                        value={value}
                                        allValue={allValue}
                                        allLabel={allLabel}
                                        search={search}
                                        onSelect={select}
                                    />
                                </Suspense>
                            </VirtualListBoundary>
                        ) : (
                            plainList
                        )}
                    </div>
                </>
            )}
        </div>
    );
}
