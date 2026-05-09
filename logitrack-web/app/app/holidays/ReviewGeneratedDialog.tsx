"use client";

import { useState, useEffect } from "react";
import { Holiday, HOLIDAY_TYPE_ENUM } from "@/validate/holidaySchema";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Trash2, Loader2, Check } from "lucide-react";
import { format } from "date-fns";
import { ScrollArea } from "@/components/ui/scroll-area";
import { useLanguage } from "@/context/language";

interface ReviewGeneratedDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    holidays: Partial<Holiday>[];
    onSave: (finalHolidays: Partial<Holiday>[], initialHolidays: Partial<Holiday>[]) => Promise<void>;
}

export function ReviewGeneratedDialog({ open, onOpenChange, holidays: initialHolidays, onSave }: ReviewGeneratedDialogProps) {
    const { t } = useLanguage();
    const [tempHolidays, setTempHolidays] = useState<Partial<Holiday>[]>(initialHolidays);
    const [isSaving, setIsSaving] = useState(false);

    // Update internal state when props change (when dialog opens)
    useEffect(() => {
        if (open) {
            setTempHolidays(initialHolidays);
        }
    }, [open, initialHolidays]);

    const handleUpdate = (index: number, updates: Partial<Holiday>) => {
        const updated = [...tempHolidays];
        const current = updated[index];
        const next = { ...current, ...updates };
        
        // Auto-update name if holidayNameEN or holidayNameTH changed
        if (updates.holidayNameEN !== undefined || updates.holidayNameTH !== undefined) {
            const holidayNameEN = updates.holidayNameEN !== undefined ? updates.holidayNameEN : current.holidayNameEN;
            const holidayNameTH = updates.holidayNameTH !== undefined ? updates.holidayNameTH : current.holidayNameTH;
            next.name = holidayNameEN && holidayNameTH ? `${holidayNameEN} (${holidayNameTH})` : (holidayNameEN || holidayNameTH || current.name);
        }
        
        updated[index] = next;
        setTempHolidays(updated);
    };

    const handleRemove = (index: number) => {
        setTempHolidays(tempHolidays.filter((_, i) => i !== index));
    };

    const handleConfirmSave = async () => {
        setIsSaving(true);
        try {
            await onSave(tempHolidays, initialHolidays);
            onOpenChange(false);
        } catch (error) {
            console.error("Error saving generated holidays:", error);
        } finally {
            setIsSaving(false);
        }
    };

    const selectedYear = initialHolidays.length > 0 && initialHolidays[0].date 
        ? format(initialHolidays[0].date as Date, "yyyy")
        : "";

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-5xl h-[90vh] flex flex-col overflow-hidden">
                <DialogHeader className="shrink-0">
                    <DialogTitle>{t("holidays.dialog.reviewTitle")}</DialogTitle>
                    <DialogDescription>
                        {t("holidays.dialog.reviewDesc", { year: selectedYear })}
                    </DialogDescription>
                </DialogHeader>

                <div className="flex-1 min-h-0 mt-4 flex flex-col overflow-hidden">
                    <ScrollArea type="always" className="h-full border rounded-md [&>[data-radix-scroll-area-scrollbar]>div]:bg-muted-foreground/50 [&>[data-radix-scroll-area-scrollbar]>div]:min-h-8">
                    <Table>
                        <TableHeader className="sticky top-0 z-10 bg-background border-b border-border shadow-sm">
                            <TableRow>
                                <TableHead className="w-[300px]">{t("holidays.table.name")}</TableHead>
                                <TableHead className="w-[180px]">{t("holidays.table.date")}</TableHead>
                                <TableHead className="w-[150px]">{t("holidays.table.type")}</TableHead>
                                <TableHead className="w-[100px] text-right">{t("holidays.table.actions")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {tempHolidays.map((holiday, index) => (
                                <TableRow key={index}>
                                    <TableCell>
                                        <div className="flex flex-col gap-1.5 py-1">
                                            <div className="flex items-center gap-2">
                                                <span className="text-[10px] font-bold text-muted-foreground w-6">EN</span>
                                                <Input 
                                                    value={holiday.holidayNameEN} 
                                                    onChange={(e) => handleUpdate(index, { holidayNameEN: e.target.value })}
                                                    className="h-8 text-xs"
                                                    placeholder="English name"
                                                />
                                            </div>
                                            <div className="flex items-center gap-2">
                                                <span className="text-[10px] font-bold text-muted-foreground w-6">TH</span>
                                                <Input 
                                                    value={holiday.holidayNameTH} 
                                                    onChange={(e) => handleUpdate(index, { holidayNameTH: e.target.value })}
                                                    className="h-8 text-xs"
                                                    placeholder="ชื่อภาษาไทย"
                                                />
                                            </div>
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <Input 
                                            type="date"
                                            value={holiday.date ? format(holiday.date as Date, "yyyy-MM-dd") : ""}
                                            onChange={(e) => handleUpdate(index, { date: new Date(e.target.value) })}
                                            className="h-8"
                                        />
                                    </TableCell>
                                    <TableCell>
                                        <Select
                                            value={holiday.type}
                                            onValueChange={(value: any) => handleUpdate(index, { type: value })}
                                        >
                                            <SelectTrigger className="h-8">
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent>
                                                {HOLIDAY_TYPE_ENUM.map(type => (
                                                    <SelectItem key={type} value={type}>
                                                        {t(`holidays.type.${type.toLowerCase()}`)}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                    </TableCell>
                                    <TableCell className="text-right">
                                        <Button 
                                            variant="ghost" 
                                            size="icon" 
                                            onClick={() => handleRemove(index)}
                                            className="h-8 w-8 text-red-500 hover:text-red-600 hover:bg-red-500/10"
                                        >
                                            <Trash2 className="h-4 w-4" />
                                        </Button>
                                    </TableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </ScrollArea>
                </div>

                <DialogFooter className="mt-6 shrink-0">
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={isSaving}>
                        {t("holidays.dialog.cancel")}
                    </Button>
                    <Button onClick={handleConfirmSave} disabled={isSaving || tempHolidays.length === 0} className="bg-green-600 hover:bg-green-700">
                        {isSaving ? (
                            <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                        ) : (
                            <Check className="mr-2 h-4 w-4" />
                        )}
                        {t("holidays.dialog.saveAll")} ({tempHolidays.length})
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
