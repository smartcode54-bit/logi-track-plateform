"use client";

import { useMemo, useState, type ReactNode } from "react";
import { Check, ChevronsUpDown, Search } from "lucide-react";
import { cn } from "@/lib/utils";

/** One selectable option. `badge` is rendered next to the label (e.g. "not in fleet"). */
export interface FilterComboboxOption {
    value: string;
    label: string;
    count?: number;
    badge?: string;
}

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
}

/**
 * Searchable filter combobox shared by the plate, origin and destination filters.
 *
 * Options are built from the loaded rows (not from a master collection), so each carries its row
 * count and values with no master record are badged rather than hidden.
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
}: FilterComboboxProps) {
    const [open, setOpen] = useState(false);
    const [search, setSearch] = useState("");

    const selected = useMemo(() => options.find((o) => o.value === value), [options, value]);

    const matching = useMemo(() => {
        const term = search.trim().toLowerCase();
        if (!term) return options;
        return options.filter((o) => o.label.toLowerCase().includes(term));
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
                <ChevronsUpDown className="h-4 w-4 shrink-0 opacity-50" />
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
                        <div className="max-h-[240px] overflow-y-auto p-1">
                            <Row
                                selected={value === allValue}
                                onClick={() => select(allValue)}
                                label={allLabel}
                            />
                            {matching.length === 0 ? (
                                <div className="py-4 text-center text-sm text-muted-foreground">
                                    {noResultsLabel}
                                </div>
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
                    </div>
                </>
            )}
        </div>
    );
}

function Row({
    selected,
    onClick,
    label,
    count,
    badge,
}: {
    selected: boolean;
    onClick: () => void;
    label: string;
    count?: number;
    badge?: string;
}) {
    return (
        <div
            className="relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm hover:bg-accent hover:text-accent-foreground"
            onClick={onClick}
        >
            <Check className={cn("h-4 w-4 shrink-0", selected ? "opacity-100" : "opacity-0")} />
            <span className="min-w-0 flex-1 truncate">
                {label}
                {badge && <span className="ml-2 text-xs text-amber-600">{badge}</span>}
            </span>
            {typeof count === "number" && (
                <span className="shrink-0 text-xs text-muted-foreground tabular-nums">{count}</span>
            )}
        </div>
    );
}
