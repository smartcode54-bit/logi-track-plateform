"use client";

import { useState } from "react";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Calendar } from "lucide-react";

interface YearSelectDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onSelect: (year: number) => void;
}

export function YearSelectDialog({ open, onOpenChange, onSelect }: YearSelectDialogProps) {
    const currentYear = new Date().getFullYear();
    const [selectedYear, setSelectedYear] = useState<string>(currentYear.toString());

    // Generate a range of years (e.g., current year +/- 5 years)
    const years = Array.from({ length: 11 }, (_, i) => (currentYear - 2 + i).toString());

    const handleConfirm = () => {
        onSelect(parseInt(selectedYear));
        onOpenChange(false);
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-[425px]">
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        <Calendar className="h-5 w-5 text-blue-500" />
                        Select Year
                    </DialogTitle>
                    <DialogDescription>
                        Choose the year you want to generate public holidays for.
                    </DialogDescription>
                </DialogHeader>
                <div className="py-6">
                    <Select
                        value={selectedYear}
                        onValueChange={setSelectedYear}
                    >
                        <SelectTrigger className="w-full h-12 text-lg">
                            <SelectValue placeholder="Select Year" />
                        </SelectTrigger>
                        <SelectContent>
                            {years.map((year) => (
                                <SelectItem key={year} value={year} className="text-lg">
                                    {year}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </div>
                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)}>
                        Cancel
                    </Button>
                    <Button onClick={handleConfirm} className="bg-blue-600 hover:bg-blue-700 text-white px-8">
                        OK
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
