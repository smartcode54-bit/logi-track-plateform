"use client";

import { useState } from "react";
import { format } from "date-fns";
import { th } from "date-fns/locale";
import { CalendarIcon, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { FormControl } from "@/components/ui/form";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { useLanguage } from "@/context/language";
import { cn } from "@/lib/utils";

/** Form-bound birth-date control: the shared DatePicker does not forward its trigger's ARIA props. */
export function DriverBirthDatePicker({ value, onChange, minDate, maxDate }: {
    value?: Date;
    onChange: (date?: Date) => void;
    minDate: Date;
    maxDate: Date;
}) {
    const [open, setOpen] = useState(false);
    const { t, language } = useLanguage();
    return (
        <div className="flex items-center gap-2">
            <Popover open={open} onOpenChange={setOpen}>
                <PopoverTrigger asChild>
                    <FormControl>
                        <Button type="button" variant="outline" role="combobox" aria-required="true" aria-haspopup="dialog"
                            className={cn("min-w-0 flex-1 justify-between bg-foreground px-3 text-left font-normal text-background border-foreground hover:bg-foreground/90 hover:text-background", !value && "text-background/60")}>
                            <span className="truncate">{value ? format(value, "dd/MM/yyyy") : t("drivers.form.birthDate")}</span>
                            <CalendarIcon className="h-4 w-4 opacity-50" aria-hidden />
                        </Button>
                    </FormControl>
                </PopoverTrigger>
                <PopoverContent className="w-auto p-0" align="start">
                    <Calendar mode="single" selected={value} defaultMonth={value ?? maxDate}
                        onSelect={(date) => { onChange(date); setOpen(false); }}
                        disabled={(date) => date > maxDate || date < minDate}
                        fromYear={minDate.getFullYear()} toYear={maxDate.getFullYear()}
                        locale={language === "th" ? th : undefined} captionLayout="dropdown" autoFocus />
                </PopoverContent>
            </Popover>
            {value && (
                <Button type="button" variant="ghost" size="icon" aria-label={t("common.select.clear")} onClick={() => onChange(undefined)}>
                    <X className="h-4 w-4" aria-hidden />
                </Button>
            )}
        </div>
    );
}
