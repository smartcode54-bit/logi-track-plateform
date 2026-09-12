"use client";

import { format } from "date-fns";
import { CalendarIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

/**
 * Shadcn date + time picker producing a single `Date`. Time granularity is 30 minutes (HH:00 / HH:30).
 * Used for the actual pickup date-time (ADR 0028) instead of the native `datetime-local`, so it matches
 * the rest of the form and constrains minutes to 00/30.
 */
export function DateTimePicker({
    value,
    onChange,
    datePlaceholder,
    timePlaceholder,
}: {
    value?: Date;
    onChange: (d?: Date) => void;
    datePlaceholder?: string;
    timePlaceholder?: string;
}) {
    const timeStr = value ? format(value, "HH:mm") : "";

    const setDatePart = (d?: Date) => {
        if (!d) {
            onChange(undefined);
            return;
        }
        const base = value ?? new Date();
        const merged = new Date(d);
        merged.setHours(base.getHours(), base.getMinutes(), 0, 0);
        onChange(merged);
    };

    const setTimePart = (hhmm: string) => {
        if (!hhmm) return;
        const [h, m] = hhmm.split(":").map(Number);
        const merged = new Date(value ?? new Date());
        merged.setHours(h, m, 0, 0);
        onChange(merged);
    };

    return (
        <div className="grid grid-cols-2 gap-2">
            <Popover modal={true}>
                <PopoverTrigger asChild>
                    <Button
                        type="button"
                        variant="outline"
                        className={cn("w-full justify-start text-left font-normal", !value && "text-muted-foreground")}
                    >
                        <CalendarIcon className="mr-2 h-4 w-4 opacity-50" />
                        {value ? format(value, "dd/MM/yyyy") : (datePlaceholder ?? "เลือกวันที่")}
                    </Button>
                </PopoverTrigger>
                <PopoverContent className="w-auto p-0 z-[1005]" align="start">
                    <Calendar mode="single" selected={value} onSelect={setDatePart} initialFocus />
                </PopoverContent>
            </Popover>

            <Select value={timeStr} onValueChange={setTimePart}>
                <SelectTrigger>
                    <SelectValue placeholder={timePlaceholder ?? "--:--"} />
                </SelectTrigger>
                <SelectContent className="max-h-[200px] z-[1005]" position="popper">
                    {Array.from({ length: 48 }).map((_, i) => {
                        const hour = Math.floor(i / 2).toString().padStart(2, "0");
                        const minute = i % 2 === 0 ? "00" : "30";
                        const t = `${hour}:${minute}`;
                        return (
                            <SelectItem key={t} value={t}>
                                {t}
                            </SelectItem>
                        );
                    })}
                </SelectContent>
            </Select>
        </div>
    );
}
