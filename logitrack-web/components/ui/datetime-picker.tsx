"use client";

import * as React from "react";
import { format } from "date-fns";
import { Calendar as CalendarIcon, Clock, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import {
    Popover,
    PopoverContent,
    PopoverTrigger,
} from "@/components/ui/popover";

interface DateTimePickerProps {
    value?: Date;
    onChange: (date?: Date) => void;
    placeholder?: string;
    disabled?: boolean;
    fromDate?: Date;
    toDate?: Date;
    fromYear?: number;
    toYear?: number;
    className?: string;
}

export function DateTimePicker({
    value,
    onChange,
    placeholder = "Pick a date & time",
    disabled,
    fromDate,
    toDate,
    fromYear = 1900,
    toYear = 2100,
    className,
}: DateTimePickerProps) {
    const [open, setOpen] = React.useState(false);
    const [selectedDate, setSelectedDate] = React.useState<Date | undefined>(value);
    
    const [hour, setHour] = React.useState<string>(value ? format(value, "HH") : "09");
    const [minute, setMinute] = React.useState<string>(value ? format(value, "mm") : "00");

    React.useEffect(() => {
        if (value) {
            setSelectedDate(value);
            setHour(format(value, "HH"));
            setMinute(format(value, "mm"));
        }
    }, [value]);

    const handleSelect = (date: Date | undefined) => {
        setSelectedDate(date);
        if (date) {
            const newDate = new Date(date);
            newDate.setHours(parseInt(hour), parseInt(minute), 0, 0);
            onChange(newDate);
        } else {
            onChange(undefined);
        }
    };

    const handleTimeChange = (type: "hour" | "minute", val: string) => {
        const nextHour = type === "hour" ? val : hour;
        const nextMinute = type === "minute" ? val : minute;
        if (type === "hour") setHour(val);
        else setMinute(val);

        const base =
            selectedDate ??
            value ??
            (() => {
                const d = new Date();
                d.setHours(0, 0, 0, 0);
                return d;
            })();
        const newDate = new Date(base);
        newDate.setHours(parseInt(nextHour, 10), parseInt(nextMinute, 10), 0, 0);
        if (!selectedDate) setSelectedDate(newDate);
        onChange(newDate);
    };

    const hours = Array.from({ length: 24 }, (_, i) => i.toString().padStart(2, '0'));
    const minutes = Array.from({ length: 60 }, (_, i) => i.toString().padStart(2, "0"));

    return (
        <Popover modal={false} open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
                <Button
                    variant={"outline"}
                    className={cn(
                        "w-full text-left font-normal justify-between px-3",
                        !value && "text-muted-foreground",
                        className
                    )}
                    disabled={disabled}
                    type="button"
                >
                    <span className="truncate">
                        {value ? format(value, "dd/MM/yyyy HH:mm") : <span>{placeholder}</span>}
                    </span>
                    <div className="flex items-center gap-1 opacity-50">
                        {value && (
                            <div
                                role="button"
                                className="hover:text-destructive z-10"
                                onClick={(e) => {
                                    e.stopPropagation();
                                    onChange(undefined);
                                }}
                            >
                                <X className="h-4 w-4" />
                            </div>
                        )}
                        <CalendarIcon className="h-4 w-4" />
                    </div>
                </Button>
            </PopoverTrigger>
            <PopoverContent className="w-auto p-0" align="start">
                <Calendar
                    mode="single"
                    selected={selectedDate}
                    onSelect={handleSelect}
                    disabled={disabled as any}
                    initialFocus
                    fromYear={fromYear}
                    toYear={toYear}
                    fromDate={fromDate}
                    toDate={toDate}
                    captionLayout="dropdown"
                />
                <div className="p-3 border-t border-border flex items-center justify-between gap-2 bg-muted/20">
                    <div className="flex items-center gap-1 text-xs text-muted-foreground">
                        <Clock className="w-4 h-4" /> เวลา (24ชม.)
                    </div>
                    <div className="flex items-center gap-1">
                        <select 
                            value={hour} 
                            onChange={(e) => handleTimeChange('hour', e.target.value)}
                            className="h-8 w-[60px] text-xs bg-background border border-input rounded-md px-1 cursor-pointer focus:outline-none focus:ring-1 focus:ring-ring"
                        >
                            {hours.map((h) => (
                                <option key={h} value={h} className="bg-background text-foreground text-xs">{h}</option>
                            ))}
                        </select>
                        <span className="text-muted-foreground">:</span>
                        <select 
                            value={minute} 
                            onChange={(e) => handleTimeChange('minute', e.target.value)}
                            className="h-8 w-[60px] text-xs bg-background border border-input rounded-md px-1 cursor-pointer focus:outline-none focus:ring-1 focus:ring-ring"
                        >
                            {minutes.map((m) => (
                                <option key={m} value={m} className="bg-background text-foreground text-xs">{m}</option>
                            ))}
                        </select>
                    </div>
                </div>
            </PopoverContent>
        </Popover>
    );
}
