"use client";

/**
 * Small pieces the Go accounting and maintenance pages share (T42): money and date formatting, the
 * customer select over `['customers',{fields:'minimal'}]`, a server-searched driver / truck picker,
 * the error banner over `apiErrorText`. Nothing here imports Firebase, and nothing renders a key of the
 * split `accounting` namespace, so the maintenance routes (outside that route group) can use it.
 */
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { format, isValid, parseISO } from "date-fns";
import { Check, ChevronsUpDown, Loader2 } from "lucide-react";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { cn } from "@/lib/utils";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import {
    customerLabel,
    customerPicksQuery,
    driverPicksQuery,
    truckPicksQuery,
    type CustomerPick,
} from "../../api/pickers.go";

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

/** The customer list of the Go pages (`['customers',{fields:'minimal'}]`, every page, 10 min). */
export function useCustomerPicks() {
    const q = useQuery(customerPicksQuery);
    const list = useMemo(() => [...(q.data ?? [])].sort((a, b) => a.code.localeCompare(b.code)), [q.data]);
    return { ...q, list };
}

export interface CustomerSelectProps {
    value: string;
    onChange: (id: string) => void;
    customers: CustomerPick[];
    /** The value and label of the "every customer" row; none = a customer is required. */
    allValue?: string;
    allLabel?: string;
    placeholder?: string;
    className?: string;
    disabled?: boolean;
    id?: string;
}

export function CustomerSelect({ value, onChange, customers, allValue, allLabel, placeholder, className, disabled, id }: CustomerSelectProps) {
    return (
        <Select value={value} onValueChange={onChange} disabled={disabled}>
            <SelectTrigger id={id} className={cn("min-w-[200px]", className)}>
                <SelectValue placeholder={placeholder} />
            </SelectTrigger>
            <SelectContent className="z-[1005]" position="popper">
                {allValue !== undefined ? <SelectItem value={allValue}>{allLabel}</SelectItem> : null}
                {customers.map((c) => (
                    <SelectItem key={c.id} value={c.id}>
                        {customerLabel(c)}
                    </SelectItem>
                ))}
            </SelectContent>
        </Select>
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
 * `['trucks',{view:'picker',q}]`): no list of every driver or truck is read in the browser.
 */
export function SearchPicker({ kind, value, onChange, placeholder, clearLabel, className, disabled }: SearchPickerProps) {
    const { t } = useLanguage();
    const [open, setOpen] = useState(false);
    const [term, setTerm] = useState("");
    const q = useDebounced(term);
    const drivers = useQuery({ ...driverPicksQuery(q), enabled: open && kind === "driver" });
    const trucks = useQuery({ ...truckPicksQuery(q), enabled: open && kind === "truck" });
    const query = kind === "driver" ? drivers : trucks;
    const options: PickValue[] =
        kind === "driver"
            ? (drivers.data ?? []).map((d) => ({ id: d.id, label: d.displayName || d.id.slice(0, 8) }))
            : (trucks.data ?? []).map((tr) => ({ id: tr.id, label: tr.licensePlate || tr.id.slice(0, 8) }));
    const choose = (v: PickValue | null) => {
        onChange(v);
        setOpen(false);
        setTerm("");
    };
    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
                <Button type="button" variant="outline" role="combobox" aria-expanded={open} disabled={disabled} className={cn("w-[220px] justify-between font-normal", className)}>
                    <span className={cn("truncate", !value && "text-muted-foreground")}>{value?.label ?? placeholder}</span>
                    <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
                </Button>
            </PopoverTrigger>
            <PopoverContent className="z-[1005] w-[260px] p-2" align="start">
                <Input autoFocus value={term} onChange={(e) => setTerm(e.target.value)} placeholder={t("common.searchPlaceholder")} className="mb-2 h-8" />
                <div className="max-h-64 overflow-y-auto" role="listbox">
                    {clearLabel ? (
                        <button type="button" className="flex w-full items-center rounded px-2 py-1.5 text-left text-sm text-muted-foreground hover:bg-accent" onClick={() => choose(null)}>
                            {clearLabel}
                        </button>
                    ) : null}
                    {query.isFetching && options.length === 0 ? (
                        <div className="flex justify-center py-3">
                            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
                        </div>
                    ) : null}
                    {query.error ? <p className="px-2 py-1.5 text-xs text-destructive">{apiErrorText(query.error, t)}</p> : null}
                    {!query.isFetching && !query.error && options.length === 0 ? (
                        <p className="px-2 py-1.5 text-xs text-muted-foreground">{t("common.noResults")}</p>
                    ) : null}
                    {options.map((o) => (
                        <button
                            key={o.id}
                            type="button"
                            role="option"
                            aria-selected={value?.id === o.id}
                            className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-accent"
                            onClick={() => choose(o)}
                        >
                            <Check className={cn("h-4 w-4", value?.id === o.id ? "opacity-100" : "opacity-0")} />
                            <span className="truncate">{o.label}</span>
                        </button>
                    ))}
                </div>
            </PopoverContent>
        </Popover>
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
