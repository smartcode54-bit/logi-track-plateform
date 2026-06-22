"use client";

import * as React from "react";
import { format } from "date-fns";
import { Calendar as CalendarIcon, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import {
    Popover,
    PopoverContent,
    PopoverTrigger,
} from "@/components/ui/popover";
import { useLanguage } from "@/context/language";

interface DatePickerProps {
    value?: Date;
    onChange: (date?: Date) => void;
    placeholder?: string;
    disabled?: boolean | ((date: Date) => boolean);
    fromDate?: Date;
    toDate?: Date;
    fromYear?: number;
    toYear?: number;
    className?: string;
}

export function DatePicker({
    value,
    onChange,
    placeholder = "Pick a date",
    disabled,
    fromDate,
    toDate,
    fromYear = 1900,
    toYear = 2100,
    className,
}: DatePickerProps) {
    const [open, setOpen] = React.useState(false);

    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
                <Button
                    variant={"outline"}
                    className={cn(
                        // Inverted vs theme: contrasts in both dark & light (token-driven, auto-swaps)
                        "w-full text-left font-normal justify-between px-3",
                        "bg-foreground text-background border-foreground",
                        "hover:bg-foreground/90 hover:text-background",
                        !value && "text-background/60",
                        className
                    )}
                    disabled={disabled === true}
                    type="button" // Prevent form submission
                >
                    <span className="truncate">
                        {value ? format(value, "dd/MM/yyyy") : <span>{placeholder}</span>}
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
                    selected={value}
                    onSelect={(d) => {
                        onChange(d);
                        setOpen(false);
                    }}
                    disabled={disabled}
                    initialFocus
                    fromYear={fromYear}
                    toYear={toYear}
                    fromDate={fromDate}
                    toDate={toDate}
                    captionLayout="dropdown"
                />
            </PopoverContent>
        </Popover>
    );
}
