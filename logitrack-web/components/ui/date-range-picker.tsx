"use client";

import { useState } from "react";
import { format } from "date-fns";
import type { Locale } from "date-fns";
import { Calendar as CalendarIcon } from "lucide-react";
import type { DateRange } from "react-day-picker";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";

export interface DateRangePickerProps {
    from: Date | null;
    to: Date | null;
    /** Fired once BOTH ends are picked. The consumer still clamps (see `clampDateRange`). */
    onChange: (from: Date, to: Date) => void;
    locale?: Locale;
    /** Months side by side. 2 (the default) is what lets a range cross a month boundary in one view. */
    numberOfMonths?: number;
    /** Shown when either end is missing. */
    placeholder?: string;
    className?: string;
    align?: "start" | "center" | "end";
}

/**
 * One control for a from–to date range, replacing two single-date pickers.
 *
 * Picking a range that spans months used to mean opening two calendars and paging each one; here
 * both ends are picked in a single two-month view. The in-progress selection is held locally and
 * only committed once both ends exist — a half-range would otherwise reach the consumer as
 * from === to and trigger a query over one day.
 */
export function DateRangePicker({
    from,
    to,
    onChange,
    locale,
    numberOfMonths = 2,
    placeholder = "—",
    className,
    align = "start",
}: DateRangePickerProps) {
    const [open, setOpen] = useState(false);
    const [draft, setDraft] = useState<DateRange | undefined>(undefined);

    const committed: DateRange | undefined = from ? { from, to: to ?? undefined } : undefined;
    const shown = draft ?? committed;

    const label =
        from && to
            ? `${format(from, "dd/MM/yyyy")} – ${format(to, "dd/MM/yyyy")}`
            : placeholder;

    return (
        <Popover
            open={open}
            onOpenChange={(next) => {
                setOpen(next);
                // Drop an unfinished selection on close so the trigger never disagrees with the data.
                if (!next) setDraft(undefined);
            }}
        >
            <PopoverTrigger asChild>
                <Button
                    type="button"
                    variant="outline"
                    className={cn("h-9 justify-start font-normal", className)}
                >
                    <CalendarIcon className="mr-2 h-4 w-4 shrink-0" />
                    {label}
                </Button>
            </PopoverTrigger>
            <PopoverContent className="w-auto p-0" align={align}>
                <Calendar
                    mode="range"
                    numberOfMonths={numberOfMonths}
                    defaultMonth={from ?? undefined}
                    selected={shown}
                    onSelect={(range) => {
                        setDraft(range);
                        if (range?.from && range?.to) {
                            onChange(range.from, range.to);
                            setDraft(undefined);
                            setOpen(false);
                        }
                    }}
                    locale={locale}
                    autoFocus
                />
            </PopoverContent>
        </Popover>
    );
}
