"use client";

import { useEffect, useMemo, useState } from "react";
import { useLanguage } from "@/context/language";
import { toast } from "sonner";
import {
    createFuelExpense,
    getDriversWithTruckAssignments,
    getTrucksForFilter,
    type DriverWithTruckAssignment,
    type TruckOption,
} from "../api/billing";
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

export interface AddFuelExpenseDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onSaved: () => void;
}

const EMPTY_FORM = {
    driverId: "",
    truckId: "",
    date: new Date(),
    amount: "",
    volumeLiters: "",
    odometer: "",
    stationTaxId: "",
    taxInvId: "",
    note: "",
};

export function AddFuelExpenseDialog({ open, onOpenChange, onSaved }: AddFuelExpenseDialogProps) {
    const { t } = useLanguage();

    const [drivers, setDrivers] = useState<DriverWithTruckAssignment[]>([]);
    const [trucks, setTrucks] = useState<TruckOption[]>([]);
    const [form, setForm] = useState(EMPTY_FORM);
    const [receiptFile, setReceiptFile] = useState<File | null>(null);
    const [odometerFile, setOdometerFile] = useState<File | null>(null);
    const [submitting, setSubmitting] = useState(false);

    useEffect(() => {
        if (!open) return;
        setForm(EMPTY_FORM);
        setReceiptFile(null);
        setOdometerFile(null);
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

    const pricePerLiter = useMemo(() => {
        const amount = Number(form.amount);
        const liters = Number(form.volumeLiters);
        if (!amount || !liters) return undefined;
        return Math.round((amount / liters) * 100) / 100;
    }, [form.amount, form.volumeLiters]);

    const canSave = form.driverId.trim() !== "" && Number(form.amount) > 0 && !submitting;

    const handleSave = async () => {
        if (!canSave) return;
        setSubmitting(true);
        try {
            const truck = trucks.find((tr) => tr.id === form.truckId);
            await createFuelExpense({
                driverId: form.driverId,
                truckId: form.truckId || undefined,
                truckLicensePlate: truck?.licensePlate,
                date: form.date,
                amount: Number(form.amount),
                volumeLiters: form.volumeLiters ? Number(form.volumeLiters) : undefined,
                pricePerLiter,
                odometer: form.odometer ? Number(form.odometer) : undefined,
                stationTaxId: form.stationTaxId.trim() || undefined,
                taxInvId: form.taxInvId.trim() || undefined,
                note: form.note.trim() || undefined,
                receiptPhotoFile: receiptFile ?? undefined,
                odometerPhotoFile: odometerFile ?? undefined,
            });
            toast.success(t("accounting.fuel.addDialog.saved"));
            onOpenChange(false);
            onSaved();
        } catch (err) {
            console.error("Failed to create fuel expense:", err);
            toast.error(t("accounting.fuel.addDialog.saveError"));
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-lg">
                <DialogHeader>
                    <DialogTitle>{t("accounting.fuel.addDialog.title")}</DialogTitle>
                    <DialogDescription>{t("accounting.fuel.addDialog.description")}</DialogDescription>
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
                            <Label>{t("accounting.table.volume")}</Label>
                            <Input
                                type="number"
                                step="any"
                                min={0}
                                value={form.volumeLiters}
                                onChange={(e) => setForm((f) => ({ ...f, volumeLiters: e.target.value }))}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.table.pricePerLiter")}</Label>
                            <Input type="text" readOnly disabled value={pricePerLiter != null ? pricePerLiter.toFixed(2) : "-"} />
                        </div>
                    </div>

                    <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1.5">
                            <Label>{t("accounting.table.odometer")}</Label>
                            <Input
                                type="number"
                                step="any"
                                min={0}
                                value={form.odometer}
                                onChange={(e) => setForm((f) => ({ ...f, odometer: e.target.value }))}
                            />
                        </div>
                    </div>

                    <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1.5">
                            <Label>{t("accounting.fuel.addDialog.receipt")}</Label>
                            <Input
                                type="file"
                                accept="image/*"
                                onChange={(e) => setReceiptFile(e.target.files?.[0] ?? null)}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.fuel.addDialog.odometerPhoto")}</Label>
                            <Input
                                type="file"
                                accept="image/*"
                                onChange={(e) => setOdometerFile(e.target.files?.[0] ?? null)}
                            />
                        </div>
                    </div>

                    <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-1.5">
                            <Label>{t("accounting.detail.stationTaxId")}</Label>
                            <Input
                                value={form.stationTaxId}
                                onChange={(e) => setForm((f) => ({ ...f, stationTaxId: e.target.value }))}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label>{t("accounting.detail.taxInvId")}</Label>
                            <Input
                                value={form.taxInvId}
                                onChange={(e) => setForm((f) => ({ ...f, taxInvId: e.target.value }))}
                            />
                        </div>
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
