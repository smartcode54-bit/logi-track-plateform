"use client";

import { useEffect, useState } from "react";
import { useLanguage } from "@/context/language";
import { toast } from "sonner";
import {
    createOtherExpense,
    getDriversWithTruckAssignments,
    getTrucksForFilter,
    type DriverWithTruckAssignment,
    type TruckOption,
} from "../api/expenses";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { DatePicker } from "@/components/ui/date-picker";
import { Loader2 } from "lucide-react";

export interface AddOtherExpenseDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onSaved: () => void;
}

const CATEGORY_KEYS: Record<string, string> = {
    tire_repair: "accounting.category.tireRepair",
    maintenance: "accounting.category.maintenance",
    toll: "accounting.category.toll",
    parking: "accounting.category.parking",
    other: "accounting.category.other",
};

const EMPTY_FORM = {
    driverId: "",
    truckId: "",
    date: new Date(),
    amount: "",
    category: "",
    description: "",
    note: "",
};

export function AddOtherExpenseDialog({ open, onOpenChange, onSaved }: AddOtherExpenseDialogProps) {
    const { t } = useLanguage();

    const [drivers, setDrivers] = useState<DriverWithTruckAssignment[]>([]);
    const [trucks, setTrucks] = useState<TruckOption[]>([]);
    const [form, setForm] = useState(EMPTY_FORM);
    const [receiptFile, setReceiptFile] = useState<File | null>(null);
    const [submitting, setSubmitting] = useState(false);

    useEffect(() => {
        if (!open) return;
        setForm(EMPTY_FORM);
        setReceiptFile(null);
        Promise.all([getDriversWithTruckAssignments(), getTrucksForFilter()]).then(([d, tr]) => {
            setDrivers(d);
            setTrucks(tr);
        });
    }, [open]);

    // Auto-fill truck from the selected driver's current assignment — admin can still change it.
    const handleDriverChange = (driverId: string) => {
        const driver = drivers.find((d) => d.filterId === driverId);
        setForm((f) => ({ ...f, driverId, truckId: driver?.truckId ?? f.truckId }));
    };

    const canSave = form.driverId.trim() !== "" && Number(form.amount) > 0 && !submitting;

    const handleSave = async () => {
        if (!canSave) return;
        setSubmitting(true);
        try {
            const truck = trucks.find((tr) => tr.id === form.truckId);
            await createOtherExpense({
                driverId: form.driverId,
                truckId: form.truckId || undefined,
                truckLicensePlate: truck?.licensePlate,
                date: form.date,
                amount: Number(form.amount),
                category: form.category || undefined,
                description: form.description.trim() || undefined,
                note: form.note.trim() || undefined,
                receiptPhotoFile: receiptFile ?? undefined,
            });
            toast.success(t("accounting.other.addDialog.saved"));
            onOpenChange(false);
            onSaved();
        } catch (err) {
            console.error("Failed to create other expense:", err);
            toast.error(t("accounting.other.addDialog.saveError"));
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-lg">
                <DialogHeader>
                    <DialogTitle>{t("accounting.other.addDialog.title")}</DialogTitle>
                    <DialogDescription>{t("accounting.other.addDialog.description")}</DialogDescription>
                </DialogHeader>

                <div className="space-y-4 py-2">
                    <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1.5">
                            <Label>{t("accounting.fuel.addDialog.driver")} *</Label>
                            <Select value={form.driverId} onValueChange={handleDriverChange}>
                                <SelectTrigger>
                                    <SelectValue placeholder={t("accounting.fuel.addDialog.selectDriver")} />
                                </SelectTrigger>
                                <SelectContent className="z-[1005]" position="popper">
                                    {drivers.map((d) => (
                                        <SelectItem key={d.filterId} value={d.filterId}>{d.name}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.fuel.addDialog.truck")}</Label>
                            <Select value={form.truckId} onValueChange={(v) => setForm((f) => ({ ...f, truckId: v }))}>
                                <SelectTrigger>
                                    <SelectValue placeholder={t("accounting.fuel.addDialog.selectTruck")} />
                                </SelectTrigger>
                                <SelectContent className="z-[1005]" position="popper">
                                    {trucks.map((tr) => (
                                        <SelectItem key={tr.id} value={tr.id}>{tr.licensePlate}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    </div>

                    <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1.5">
                            <Label>{t("accounting.fuel.addDialog.date")}</Label>
                            <DatePicker value={form.date} onChange={(d) => d && setForm((f) => ({ ...f, date: d }))} />
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.table.amount")} *</Label>
                            <Input
                                type="number"
                                step="any"
                                min={0}
                                value={form.amount}
                                onChange={(e) => setForm((f) => ({ ...f, amount: e.target.value }))}
                            />
                        </div>
                    </div>

                    <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1.5">
                            <Label>{t("accounting.detail.category")}</Label>
                            <Select value={form.category} onValueChange={(v) => setForm((f) => ({ ...f, category: v }))}>
                                <SelectTrigger>
                                    <SelectValue placeholder={t("accounting.audit.typeOther")} />
                                </SelectTrigger>
                                <SelectContent className="z-[1005]" position="popper">
                                    {Object.entries(CATEGORY_KEYS).map(([key, labelKey]) => (
                                        <SelectItem key={key} value={key}>{t(labelKey)}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.other.addDialog.receipt")}</Label>
                            <Input
                                type="file"
                                accept="image/*"
                                onChange={(e) => setReceiptFile(e.target.files?.[0] ?? null)}
                            />
                        </div>
                    </div>

                    <div className="space-y-1.5">
                        <Label>{t("accounting.detail.description")}</Label>
                        <Input
                            value={form.description}
                            onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                        />
                    </div>

                    <div className="space-y-1.5">
                        <Label>{t("accounting.fuel.addDialog.note")}</Label>
                        <Textarea
                            rows={2}
                            value={form.note}
                            onChange={(e) => setForm((f) => ({ ...f, note: e.target.value }))}
                        />
                    </div>
                </div>

                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
                        {t("common.cancel")}
                    </Button>
                    <Button onClick={handleSave} disabled={!canSave}>
                        {submitting ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : null}
                        {t("common.save")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
