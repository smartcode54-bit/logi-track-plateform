"use client";

import { useState, useEffect } from "react";
import { useLanguage } from "@/context/language";
import { functions } from "@/firebase/client";
import { Holiday, holidaySchema, HOLIDAY_TYPE_ENUM, HOLIDAY_STATUS_ENUM } from "@/validate/holidaySchema";
import { httpsCallable } from "firebase/functions";
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
    const [isSubmitting, setIsSubmitting] = useState(false);

    const [formData, setFormData] = useState({
        name: "",
        holidayNameEN: "",
        holidayNameTH: "",
        date: "",
        type: "PUBLIC" as typeof HOLIDAY_TYPE_ENUM[number],
        status: "DRAFT" as typeof HOLIDAY_STATUS_ENUM[number],
        description: "",
        descriptionEn: "",
        descriptionTh: "",
        isRecurring: false,
    });

    useEffect(() => {
        if (open) {
            setFormData({
                name: editData?.name ?? "",
                holidayNameEN: editData?.holidayNameEN ?? "",
                holidayNameTH: editData?.holidayNameTH ?? "",
                date: editData?.date ? format(editData.date, "yyyy-MM-dd") : initialDate ?? "",
                type: editData?.type ?? "PUBLIC",
                status: editData?.status ?? "DRAFT",
                description: editData?.description ?? "",
                descriptionEn: editData?.descriptionEn ?? "",
                descriptionTh: editData?.descriptionTh ?? "",
                isRecurring: editData?.isRecurring ?? false,
            });
        }
    }, [open, editData, initialDate]);

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        if (!formData.holidayNameEN && !formData.holidayNameTH) {
            toast.error(t("holidays.toast.fillRequired"));
            return;
        }

        setIsSubmitting(true);
        try {
            const name = formData.holidayNameEN && formData.holidayNameTH
                ? `${formData.holidayNameEN} (${formData.holidayNameTH})`
                : (formData.holidayNameEN || formData.holidayNameTH);
            const payload = {
                ...formData,
                name,
                date: formData.date,
                ...(editData?.id && { id: editData.id }),
            };
            holidaySchema.parse({ ...payload, date: new Date(payload.date) });
            const saveHolidayFn = httpsCallable<typeof payload, { id: string }>(functions, "saveHoliday");
            await saveHolidayFn(payload);
            toast.success(editData?.id ? t("holidays.toast.updated") : t("holidays.toast.added"));
            onOpenChange(false);
        } catch (error: any) {
            console.error("Error saving holiday:", error);
            toast.error(error.message || t("holidays.toast.failedSave"));
        } finally {
            setIsSubmitting(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-[425px]">
                <DialogHeader>
                    <DialogTitle>{editData ? t("holidays.dialog.editTitle") : t("holidays.dialog.addTitle")}</DialogTitle>
                    <DialogDescription>
                        {editData ? t("holidays.dialog.editDesc") : t("holidays.dialog.addDesc")}
                    </DialogDescription>
                </DialogHeader>
                <form onSubmit={handleSubmit} className="space-y-4 py-4">
                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-2">
                            <Label htmlFor="holidayNameEN">{t("holidays.dialog.nameEnLabel")} *</Label>
                            <Input
                                id="holidayNameEN"
                                placeholder={t("holidays.dialog.nameEnPlaceholder")}
                                value={formData.holidayNameEN}
                                onChange={(e) => setFormData({ ...formData, holidayNameEN: e.target.value })}
                                required
                            />
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="holidayNameTH">{t("holidays.dialog.nameThLabel")} *</Label>
                            <Input
                                id="holidayNameTH"
                                placeholder={t("holidays.dialog.nameThPlaceholder")}
                                value={formData.holidayNameTH}
                                onChange={(e) => setFormData({ ...formData, holidayNameTH: e.target.value })}
                                required
                            />
                        </div>
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="name">{t("holidays.dialog.nameLabel")}</Label>
                        <Input
                            id="name"
                            placeholder={t("holidays.dialog.namePlaceholder")}
                            value={formData.holidayNameEN && formData.holidayNameTH 
                                ? `${formData.holidayNameEN} (${formData.holidayNameTH})` 
                                : (formData.holidayNameEN || formData.holidayNameTH || formData.name)}
                            disabled
                            className="bg-muted"
                        />
                        <p className="text-[10px] text-muted-foreground italic">
                            * Auto-generated from English and Thai names
                        </p>
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="date">{t("holidays.dialog.dateLabel")} *</Label>
                        <Input
                            id="date"
                            type="date"
                            value={formData.date}
                            onChange={(e) => setFormData({ ...formData, date: e.target.value })}
                            required
                        />
                    </div>
                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-2">
                            <Label htmlFor="type">{t("holidays.dialog.typeLabel")}</Label>
                            <Select
                                value={formData.type}
                                onValueChange={(value: any) => setFormData({ ...formData, type: value })}
                            >
                                <SelectTrigger>
                                    <SelectValue placeholder={t("holidays.dialog.typeLabel")} />
                                </SelectTrigger>
                                <SelectContent>
                                    {HOLIDAY_TYPE_ENUM.map((type) => (
                                        <SelectItem key={type} value={type}>
                                            {t(`holidays.type.${type.toLowerCase()}`)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="status">{t("holidays.dialog.statusLabel")}</Label>
                            <Select
                                value={formData.status}
                                onValueChange={(value: any) => setFormData({ ...formData, status: value })}
                            >
                                <SelectTrigger>
                                    <SelectValue placeholder={t("holidays.dialog.statusLabel")} />
                                </SelectTrigger>
                                <SelectContent>
                                    {HOLIDAY_STATUS_ENUM.map((status) => (
                                        <SelectItem key={status} value={status}>
                                            {t(`holidays.status.${status.toLowerCase()}`)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
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
                            {t("holidays.dialog.recurringLabel")}
                        </Label>
                    </div>
                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-2">
                            <Label htmlFor="descriptionEn">Description (English)</Label>
                            <Textarea
                                id="descriptionEn"
                                placeholder="Details in English"
                                value={formData.descriptionEn}
                                onChange={(e) => setFormData({ ...formData, descriptionEn: e.target.value })}
                                className="h-20 text-xs"
                            />
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="descriptionTh">รายละเอียด (ภาษาไทย)</Label>
                            <Textarea
                                id="descriptionTh"
                                placeholder="รายละเอียดภาษาไทย"
                                value={formData.descriptionTh}
                                onChange={(e) => setFormData({ ...formData, descriptionTh: e.target.value })}
                                className="h-20 text-xs"
                            />
                        </div>
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="description">{t("holidays.dialog.descriptionLabel")}</Label>
                        <Textarea
                            id="description"
                            placeholder={t("holidays.dialog.descriptionPlaceholder")}
                            value={formData.descriptionEn && formData.descriptionTh 
                                ? `${formData.descriptionEn} (${formData.descriptionTh})` 
                                : (formData.descriptionEn || formData.descriptionTh || formData.description)}
                            disabled
                            className="bg-muted h-20 text-xs"
                        />
                    </div>
                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                            {t("holidays.dialog.cancel")}
                        </Button>
                        <Button type="submit" disabled={isSubmitting} className="bg-blue-600 hover:bg-blue-700">
                            {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                            {editData ? t("holidays.dialog.updateButton") : t("holidays.dialog.saveButton")}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}
