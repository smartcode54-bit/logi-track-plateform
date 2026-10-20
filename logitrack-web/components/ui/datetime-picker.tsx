"use client";

import * as React from "react";
import { format } from "date-fns";
import { Calendar as CalendarIcon, Clock, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useFieldLabel } from "./field-label";

interface DateTimePickerProps {
    id?: string;
    value?: Date;
    onChange: (date?: Date) => void;
    placeholder?: string;
    disabled?: boolean;
    fromDate?: Date;
    toDate?: Date;
    fromYear?: number;
    toYear?: number;
    className?: string;
    "aria-label"?: string;
    "aria-labelledby"?: string;
}

export function DateTimePicker({ id, value, onChange, placeholder, disabled, fromDate, toDate, fromYear = 1900, toYear = 2100, className, ...aria }: DateTimePickerProps) {
    const { t } = useLanguage();
    const fieldLabel = useFieldLabel();
    const [open, setOpen] = React.useState(false);
    const [selectedDate, setSelectedDate] = React.useState<Date | undefined>(value);
    const [hour, setHour] = React.useState(value ? format(value, "HH") : "09");
    const [minute, setMinute] = React.useState(value ? format(value, "mm") : "00");

    React.useEffect(() => {
        setSelectedDate(value);
        setHour(value ? format(value, "HH") : "09");
        setMinute(value ? format(value, "mm") : "00");
    }, [value]);

    const handleSelect = (date?: Date) => {
        setSelectedDate(date);
        if (!date) return onChange(undefined);
        const next = new Date(date);
        next.setHours(Number(hour), Number(minute), 0, 0);
        onChange(next);
    };
    const handleTimeChange = (type: "hour" | "minute", nextValue: string) => {
        const nextHour = type === "hour" ? nextValue : hour;
        const nextMinute = type === "minute" ? nextValue : minute;
        if (type === "hour") setHour(nextValue); else setMinute(nextValue);
        const next = new Date(selectedDate ?? value ?? new Date());
        next.setHours(Number(nextHour), Number(nextMinute), 0, 0);
        setSelectedDate(next);
        onChange(next);
    };
    const hours = Array.from({ length: 24 }, (_, i) => String(i).padStart(2, "0"));
    const minutes = Array.from({ length: 60 }, (_, i) => String(i).padStart(2, "0"));

    return <Popover modal={false} open={open} onOpenChange={setOpen}>
        <div className="flex min-w-0 items-center gap-1">
            <PopoverTrigger asChild>
                <Button id={id} type="button" variant="outline" disabled={disabled}
                    aria-labelledby={aria["aria-label"] ? undefined : fieldLabel}
                    {...aria}
                    className={cn("min-w-0 flex-1 justify-between px-3 text-left font-normal", !value && "text-muted-foreground", className)}>
                    <span className="truncate">{value ? format(value, "dd/MM/yyyy HH:mm") : placeholder ?? t("dateTime.pick")}</span>
                    <CalendarIcon className="h-4 w-4 shrink-0 opacity-50" aria-hidden />
                </Button>
            </PopoverTrigger>
            {value && <Button type="button" variant="ghost" size="icon" className="shrink-0 max-md:min-h-11 max-md:min-w-11" disabled={disabled}
                aria-label={t("dateTime.clear")} onClick={() => handleSelect(undefined)}><X className="h-4 w-4" aria-hidden /></Button>}
        </div>
        <PopoverContent className="w-auto p-0" align="start">
            <Calendar mode="single" selected={selectedDate} onSelect={handleSelect} disabled={disabled} initialFocus
                fromYear={fromYear} toYear={toYear} fromDate={fromDate} toDate={toDate} captionLayout="dropdown" />
            <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border bg-muted/20 p-3">
                <span className="flex items-center gap-1 text-xs text-muted-foreground"><Clock className="h-4 w-4" aria-hidden />{t("dateTime.time")}</span>
                <div className="flex items-center gap-1">
                    <Select value={hour} onValueChange={(next) => handleTimeChange("hour", next)} disabled={disabled}>
                        <SelectTrigger className="w-20" aria-label={t("dateTime.hour")}><SelectValue /></SelectTrigger>
                        <SelectContent>{hours.map((h) => <SelectItem key={h} value={h}>{h}</SelectItem>)}</SelectContent>
                    </Select>
                    <span className="text-muted-foreground" aria-hidden>:</span>
                    <Select value={minute} onValueChange={(next) => handleTimeChange("minute", next)} disabled={disabled}>
                        <SelectTrigger className="w-20" aria-label={t("dateTime.minute")}><SelectValue /></SelectTrigger>
                        <SelectContent>{minutes.map((m) => <SelectItem key={m} value={m}>{m}</SelectItem>)}</SelectContent>
                    </Select>
                </div>
            </div>
        </PopoverContent>
    </Popover>;
}
