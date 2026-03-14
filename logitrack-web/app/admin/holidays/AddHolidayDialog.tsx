"use client";

import { useState, useEffect } from "react";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Holiday, holidaySchema, HOLIDAY_TYPE_ENUM } from "@/validate/holidaySchema";
import { collection, addDoc, updateDoc, doc, Timestamp } from "firebase/firestore";
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
import { Label } from "@/components/ui/label";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Checkbox } from "@/components/ui/checkbox";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";
import { format } from "date-fns";

interface AddHolidayDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    initialDate?: string;
    editData?: Holiday | null;
}

export function AddHolidayDialog({ open, onOpenChange, initialDate, editData }: AddHolidayDialogProps) {
    const { t } = useLanguage();
    const auth = useAuth();
    const user = auth?.currentUser ?? null;
    const [isSubmitting, setIsSubmitting] = useState(false);

    const [formData, setFormData] = useState({
        name: editData?.name ?? "",
        date: editData?.date ? format(editData.date, "yyyy-MM-dd") : initialDate ?? "",
        type: editData?.type ?? "PUBLIC" as typeof HOLIDAY_TYPE_ENUM[number],
        description: editData?.description ?? "",
        isRecurring: editData?.isRecurring ?? false,
    });

    useEffect(() => {
        if (open) {
            setFormData({
                name: editData?.name ?? "",
                date: editData?.date ? format(editData.date, "yyyy-MM-dd") : initialDate ?? "",
                type: editData?.type ?? "PUBLIC" as typeof HOLIDAY_TYPE_ENUM[number],
                description: editData?.description ?? "",
                isRecurring: editData?.isRecurring ?? false,
            });
        }
    }, [open, editData, initialDate]);

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        if (!formData.name || !formData.date) {
            toast.error("Please fill in all required fields");
            return;
        }

        setIsSubmitting(true);
        try {
            const holidaysRef = collection(db, COLLECTIONS.HOLIDAYS);
            const holidayData = {
                ...formData,
                date: Timestamp.fromDate(new Date(formData.date)),
                updatedAt: Timestamp.now(),
            };

            if (editData?.id) {
                await updateDoc(doc(db, COLLECTIONS.HOLIDAYS, editData.id), holidayData);
                toast.success("Holiday updated successfully");
            } else {
                const newHoliday = {
                    ...holidayData,
                    createdAt: Timestamp.now(),
                    createdBy: user?.uid || "system",
                };
                // Validate with Zod
                holidaySchema.parse(newHoliday);
                await addDoc(holidaysRef, newHoliday);
                toast.success("Holiday added successfully");
            }
            onOpenChange(false);
            setFormData({
                name: "",
                date: "",
                type: "PUBLIC",
                description: "",
                isRecurring: false,
            });
        } catch (error: any) {
            console.error("Error saving holiday:", error);
            toast.error(error.message || "Failed to save holiday");
        } finally {
            setIsSubmitting(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-[425px]">
                <DialogHeader>
                    <DialogTitle>Add New Holiday</DialogTitle>
                    <DialogDescription>
                        Create a new holiday entry for the company calendar.
                    </DialogDescription>
                </DialogHeader>
                <form onSubmit={handleSubmit} className="space-y-4 py-4">
                    <div className="space-y-2">
                        <Label htmlFor="name">Holiday Name *</Label>
                        <Input
                            id="name"
                            placeholder="e.g. Songkran Festival"
                            value={formData.name}
                            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                            required
                        />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="date">Date *</Label>
                        <Input
                            id="date"
                            type="date"
                            value={formData.date}
                            onChange={(e) => setFormData({ ...formData, date: e.target.value })}
                            required
                        />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="type">Holiday Type</Label>
                        <Select
                            value={formData.type}
                            onValueChange={(value: any) => setFormData({ ...formData, type: value })}
                        >
                            <SelectTrigger>
                                <SelectValue placeholder="Select type" />
                            </SelectTrigger>
                            <SelectContent>
                                {HOLIDAY_TYPE_ENUM.map((type) => (
                                    <SelectItem key={type} value={type}>
                                        {type}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex items-center space-x-2 pt-2">
                        <Checkbox
                            id="recurring"
                            checked={formData.isRecurring}
                            onCheckedChange={(checked: boolean) => 
                                setFormData({ ...formData, isRecurring: checked })
                            }
                        />
                        <Label htmlFor="recurring" className="text-sm font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70">
                            Is Recurring (Annual)
                        </Label>
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="description">Description (Optional)</Label>
                        <Textarea
                            id="description"
                            placeholder="Additional details..."
                            value={formData.description}
                            onChange={(e) => setFormData({ ...formData, description: e.target.value })}
                        />
                    </div>
                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                            Cancel
                        </Button>
                        <Button type="submit" disabled={isSubmitting}>
                            {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                            Save Holiday
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}
