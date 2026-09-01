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
    disabled?: boolean;
}

/**
 * One control for a from–to date range, replacing two single-date pickers.
 *
 * Picking a range that spans months used to mean opening two calendars and paging each one; here
 * both ends are picked in a single two-month view. The in-progress selection is held locally and
 * only committed once both ends exist — a half-range would otherwise reach the consumer as
 * from === to and trigger a query over one day.
 *
 * `draft` is also what makes the first click start a fresh range rather than edit the committed
 * one; see the note in `onSelect` for why leaving that to react-day-picker does not work here.
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
    disabled = false,
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
                    disabled={disabled}
                    className={cn(
                        "h-9 justify-start font-normal",
                        !(from && to) && "text-muted-foreground",
                        className
                    )}
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
                    onSelect={(range, triggerDate) => {
                        // The first click after opening always starts a NEW range, never edits one.
                        //
                        // react-day-picker decides the next range itself, via addToRange(), and it
                        // returns a COMPLETE {from, to} on a single click in both states this
                        // picker opens in: given the committed range it treats the click as moving
                        // one end of it, and given no selection at all it returns a same-day range.
                        // Either way the commit below fired on click one — handing the consumer a
                        // range the user never picked and closing the popover before they could
                        // pick the second end. Reopening and clicking again just repeated it, so a
                        // range only landed correctly after several tries.
                        //
                        // Holding the start ourselves makes the two clicks mean what they look
                        // like: first sets `from`, second closes the range.
                        if (!draft) {
                            setDraft({ from: triggerDate, to: undefined });
                            return;
                        }
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

/** The wire format for every date-only range in this app: `<input type="date">`, callable payloads. */
const DATE_ONLY_FORMAT = "yyyy-MM-dd";

/**
 * `yyyy-MM-dd` → Date, anchored at noon.
 *
 * Midnight is the wrong anchor: parsed as UTC it lands on the previous day for anyone west of
 * Greenwich, so the calendar would highlight a day the string does not name.
 */
export function parseDateOnly(value: string): Date | null {
    const trimmed = value.trim();
    if (!trimmed) return null;
    const d = new Date(`${trimmed}T12:00:00`);
    return Number.isNaN(d.getTime()) ? null : d;
}

export interface DateOnlyRangePickerProps
    extends Omit<DateRangePickerProps, "from" | "to" | "onChange"> {
    /** `yyyy-MM-dd`, or "" for unset. */
    from: string;
    to: string;
    onChange: (from: string, to: string) => void;
}

/**
 * `DateRangePicker` for the pages that keep their range as `yyyy-MM-dd` strings — the shape they
 * send straight to a callable or a Firestore date field, so converting the state to Date objects
 * would only move the parsing somewhere less obvious.
 */
export function DateOnlyRangePicker({ from, to, onChange, ...rest }: DateOnlyRangePickerProps) {
    return (
        <DateRangePicker
            {...rest}
            from={parseDateOnly(from)}
            to={parseDateOnly(to)}
            onChange={(nextFrom, nextTo) =>
                onChange(format(nextFrom, DATE_ONLY_FORMAT), format(nextTo, DATE_ONLY_FORMAT))
            }
        />
    );
}
