"use client";

/**
 * Small pieces the Go accounting and maintenance pages share (T42): money and date formatting, a
 * server-searched driver / truck picker, the error banner over `apiErrorText`. The customer pickers are the
 * shared `CustomerSearchSelect` / `CustomerPartySelect` (`components/entity-pickers.tsx`) and row labels come
 * from `useCustomerLookup` (UI2C). Nothing here imports Firebase, and nothing renders a key of the split
 * `accounting` namespace, so the maintenance routes (outside that route group) can use it.
 */
import { useEffect, useState, type ReactNode } from "react";
import { format, isValid, parseISO } from "date-fns";
import { Loader2 } from "lucide-react";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { cn } from "@/lib/utils";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { DriverSelect, TruckSelect } from "@/components/entity-pickers";
import { nodeText } from "@/components/ui/search-select";

/** `฿1,234.50` (two decimals only when the amount has them). */
export function formatThb(n: number | null | undefined): string {
    if (n == null || !Number.isFinite(n)) return "—";
    return `฿${n.toLocaleString("en-US", { minimumFractionDigits: 0, maximumFractionDigits: 2 })}`;
}

/** An RFC 3339 instant or a `yyyy-MM-dd` day, formatted; `—` when absent or invalid. */
export function formatDay(v: string | null | undefined, pattern = "dd/MM/yyyy"): string {
    if (!v) return "—";
    const d = parseISO(v);
    return isValid(d) ? format(d, pattern) : "—";
}

export function formatDateTime(v: string | null | undefined): string {
    return formatDay(v, "dd/MM/yyyy HH:mm");
}

/** Today in the browser as `yyyy-MM-dd`. */
export function todayKey(): string {
    return format(new Date(), "yyyy-MM-dd");
}

export function ApiErrorAlert({ error, title }: { error: unknown; title?: string }) {
    const { t } = useLanguage();
    if (!error) return null;
    return (
        <Alert variant="destructive">
            <AlertDescription>
                {title ? <span className="font-medium">{title}: </span> : null}
                {apiErrorText(error, t)}
            </AlertDescription>
        </Alert>
    );
}

export function CenteredSpinner() {
    return (
        <div className="flex justify-center py-12" role="status" aria-busy="true">
            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
        </div>
    );
}

/** A trailing debounce of a text value (server searches). */
export function useDebounced<T>(value: T, ms = 300): T {
    const [v, setV] = useState(value);
    useEffect(() => {
        const id = setTimeout(() => setV(value), ms);
        return () => clearTimeout(id);
    }, [value, ms]);
    return v;
}

export interface PickValue {
    id: string;
    label: string;
}

interface SearchPickerProps {
    kind: "driver" | "truck";
    value: PickValue | null;
    onChange: (v: PickValue | null) => void;
    placeholder: string;
    /** The label of the row that clears the selection (a filter's "all"); none = no such row. */
    clearLabel?: string;
    className?: string;
    disabled?: boolean;
}

/**
 * A driver or truck picker searched on the server (`['drivers',{fields:'minimal',q}]`,
 * `['trucks',{view:'picker',q}]`): no list of every driver or truck is read in the browser. The shared
 * `DriverSelect` / `TruckSelect` (UI2); `clearLabel` makes it clearable (a filter's "all").
 */
export function SearchPicker({ kind, value, onChange, placeholder, clearLabel, className, disabled }: SearchPickerProps) {
    const Picker = kind === "driver" ? DriverSelect : TruckSelect;
    return (
        <Picker
            value={value?.id ?? ""}
            selectedLabel={value?.label}
            onValueChange={(id, option) => onChange(id && option ? { id, label: option.text ?? nodeText(option.label) } : null)}
            placeholder={placeholder}
            clearable={!!clearLabel}
            disabled={disabled}
            className={cn("w-[220px]", className)}
        />
    );
}

/** A labelled control of a filter bar. */
export function Field({ label, children, className, htmlFor }: { label: string; children: ReactNode; className?: string; htmlFor?: string }) {
    return (
        <div className={cn("flex flex-col gap-1.5", className)}>
            <label className="text-sm font-medium" htmlFor={htmlFor}>
                {label}
            </label>
            {children}
        </div>
    );
}

export const MONTHS = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12] as const;

/** The selectable years: five back, one ahead. */
export function yearOptions(now = new Date()): number[] {
    const y = now.getFullYear();
    return [y + 1, y, y - 1, y - 2, y - 3, y - 4, y - 5];
}

/** `MMMM` of a month in the page's language. */
export function monthName(month: number, language: string): string {
    return new Date(2000, month - 1, 1).toLocaleString(language === "th" ? "th-TH" : "en-US", { month: "long" });
}
