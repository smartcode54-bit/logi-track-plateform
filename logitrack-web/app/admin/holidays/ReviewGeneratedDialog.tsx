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

interface ReviewGeneratedDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    holidays: Partial<Holiday>[];
    onSave: (finalHolidays: Partial<Holiday>[]) => Promise<void>;
}

export function ReviewGeneratedDialog({ open, onOpenChange, holidays: initialHolidays, onSave }: ReviewGeneratedDialogProps) {
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
        updated[index] = { ...updated[index], ...updates };
        setTempHolidays(updated);
    };

    const handleRemove = (index: number) => {
        setTempHolidays(tempHolidays.filter((_, i) => i !== index));
    };

    const handleConfirmSave = async () => {
        setIsSaving(true);
        try {
            await onSave(tempHolidays);
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
            <DialogContent className="max-w-5xl max-h-[90vh] flex flex-col">
                <DialogHeader>
                    <DialogTitle>Review Generated Holidays</DialogTitle>
                    <DialogDescription>
                        Review and edit the generated Thai Public Holidays for {selectedYear} before saving to the database.
                    </DialogDescription>
                </DialogHeader>

                <ScrollArea className="flex-1 mt-4 border rounded-md">
                    <Table>
                        <TableHeader className="sticky top-0 bg-background z-10">
                            <TableRow>
                                <TableHead className="w-[300px]">Name</TableHead>
                                <TableHead className="w-[180px]">Date</TableHead>
                                <TableHead className="w-[150px]">Type</TableHead>
                                <TableHead className="w-[100px] text-right">Action</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {tempHolidays.map((holiday, index) => (
                                <TableRow key={index}>
                                    <TableCell>
                                        <Input 
                                            value={holiday.name} 
                                            onChange={(e) => handleUpdate(index, { name: e.target.value })}
                                            className="h-8"
                                        />
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
                                                {HOLIDAY_TYPE_ENUM.map(t => (
                                                    <SelectItem key={t} value={t}>{t}</SelectItem>
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

                <DialogFooter className="mt-6">
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={isSaving}>
                        Cancel
                    </Button>
                    <Button onClick={handleConfirmSave} disabled={isSaving || tempHolidays.length === 0} className="bg-green-600 hover:bg-green-700">
                        {isSaving ? (
                            <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                        ) : (
                            <Check className="mr-2 h-4 w-4" />
                        )}
                        Save All ({tempHolidays.length})
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
