"use client";

/**
 * An admin-entered fuel or other expense over Go (T42; `POST /v1/expenses`, then the photos uploaded
 * under the new id and linked by `PATCH`, developer-spec.md §9.4). Go stamps the tenant from the driver
 * (else the truck), defaults the truck to the driver's active truck and snapshots its plate; the row is
 * approved unless a status is sent, and an approved fuel entry with an odometer runs the PM check.
 */
import { useState } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

import { createExpense, EXPENSE_CATEGORIES, type ExpenseInput, type ExpenseType } from "../../api/expenses.go";
import { categoryLabel } from "./ExpenseDetailDialog";
import { SearchPicker, todayKey, type PickValue } from "./shared";

export function AddExpenseDialog({ type, open, onOpenChange }: { type: ExpenseType; open: boolean; onOpenChange: (o: boolean) => void }) {
    const { t } = useLanguage();
    const isFuel = type === "fuel";
    const [driver, setDriver] = useState<PickValue | null>(null);
    const [truck, setTruck] = useState<PickValue | null>(null);
    const [date, setDate] = useState(todayKey());
    const [time, setTime] = useState("08:00");
    const [amount, setAmount] = useState("");
    const [liters, setLiters] = useState("");
    const [price, setPrice] = useState("");
    const [odometer, setOdometer] = useState("");
    const [category, setCategory] = useState<string>("toll");
    const [description, setDescription] = useState("");
    const [note, setNote] = useState("");
    const [receipt, setReceipt] = useState<File | null>(null);
    const [odoPhoto, setOdoPhoto] = useState<File | null>(null);
    const [saving, setSaving] = useState(false);

    const reset = () => {
        setDriver(null);
        setTruck(null);
        setDate(todayKey());
        setTime("08:00");
        setAmount("");
        setLiters("");
        setPrice("");
        setOdometer("");
        setDescription("");
        setNote("");
        setReceipt(null);
        setOdoPhoto(null);
    };
    const valid = (driver !== null || truck !== null) && date !== "" && amount.trim() !== "" && Number(amount) > 0;

    const save = async () => {
        if (!valid) return;
        setSaving(true);
        const n = (v: string) => (v.trim() === "" ? undefined : Number(v));
        const input: ExpenseInput = {
            type,
            driverId: driver?.id,
            truckId: truck?.id,
            // A Bangkok wall time: Go reads `yyyy-MM-ddTHH:mm:ss` without an offset in Asia/Bangkok.
            date: `${date}T${time || "00:00"}:00`,
            amountThb: Number(amount),
            note: note.trim() || undefined,
            ...(isFuel
                ? { volumeLiters: n(liters), pricePerLiter: n(price), odometerKm: n(odometer) }
                : { category, description: description.trim() || undefined }),
        };
        try {
            await createExpense(input, { receipt, odometer: isFuel ? odoPhoto : null });
            toast.success(isFuel ? t("accounting.fuel.addDialog.saved") : t("accounting.other.addDialog.saved"));
            reset();
            onOpenChange(false);
        } catch (e) {
            toast.error(`${isFuel ? t("accounting.fuel.addDialog.saveError") : t("accounting.other.addDialog.saveError")} ${apiErrorText(e, t)}`);
        } finally {
            setSaving(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={(o) => !saving && onOpenChange(o)}>
            <DialogContent className="sm:max-w-lg">
                <DialogHeader>
                    <DialogTitle>{isFuel ? t("accounting.fuel.addDialog.title") : t("accounting.other.addDialog.title")}</DialogTitle>
                    <DialogDescription>{isFuel ? t("accounting.fuel.addDialog.description") : t("accounting.other.addDialog.description")}</DialogDescription>
                </DialogHeader>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-1">
                        <Label className="text-xs">{t("accounting.fuel.addDialog.driver")}</Label>
                        <SearchPicker kind="driver" value={driver} onChange={setDriver} placeholder={t("accounting.fuel.addDialog.selectDriver")} className="w-full" />
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{t("accounting.fuel.addDialog.truck")}</Label>
                        <SearchPicker kind="truck" value={truck} onChange={setTruck} placeholder={t("accounting.fuel.addDialog.selectTruck")} className="w-full" />
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{t("accounting.fuel.addDialog.date")}</Label>
                        <Input type="date" value={date} onChange={(e) => setDate(e.target.value)} />
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{t("accounting.go.expense.time")}</Label>
                        <Input type="time" value={time} onChange={(e) => setTime(e.target.value)} />
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{t("accounting.table.amount")}</Label>
                        <Input type="number" step="any" min="0" value={amount} onChange={(e) => setAmount(e.target.value)} />
                    </div>
                    {isFuel ? (
                        <>
                            <div className="space-y-1">
                                <Label className="text-xs">{t("accounting.table.volume")}</Label>
                                <Input type="number" step="any" min="0" value={liters} onChange={(e) => setLiters(e.target.value)} />
                            </div>
                            <div className="space-y-1">
                                <Label className="text-xs">{t("accounting.table.pricePerLiter")}</Label>
                                <Input type="number" step="any" min="0" value={price} onChange={(e) => setPrice(e.target.value)} />
                            </div>
                            <div className="space-y-1">
                                <Label className="text-xs">{t("accounting.table.odometer")}</Label>
                                <Input type="number" step="1" min="0" value={odometer} onChange={(e) => setOdometer(e.target.value)} />
                            </div>
                        </>
                    ) : (
                        <>
                            <div className="space-y-1">
                                <Label className="text-xs">{t("accounting.table.category")}</Label>
                                <Select value={category} onValueChange={setCategory}>
                                    <SelectTrigger>
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent className="z-[1005]" position="popper">
                                        {EXPENSE_CATEGORIES.map((c) => (
                                            <SelectItem key={c} value={c}>
                                                {categoryLabel(c, t)}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            <div className="col-span-2 space-y-1">
                                <Label className="text-xs">{t("accounting.table.description")}</Label>
                                <Input value={description} onChange={(e) => setDescription(e.target.value)} />
                            </div>
                        </>
                    )}
                    <div className="col-span-2 space-y-1">
                        <Label className="text-xs">{t("accounting.fuel.addDialog.note")}</Label>
                        <Textarea value={note} onChange={(e) => setNote(e.target.value)} rows={2} />
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{isFuel ? t("accounting.fuel.addDialog.receipt") : t("accounting.other.addDialog.receipt")}</Label>
                        <Input type="file" accept="image/*" onChange={(e) => setReceipt(e.target.files?.[0] ?? null)} />
                    </div>
                    {isFuel ? (
                        <div className="space-y-1">
                            <Label className="text-xs">{t("accounting.fuel.addDialog.odometerPhoto")}</Label>
                            <Input type="file" accept="image/*" onChange={(e) => setOdoPhoto(e.target.files?.[0] ?? null)} />
                        </div>
                    ) : null}
                </div>
                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                        {t("common.cancel")}
                    </Button>
                    <Button onClick={() => void save()} disabled={!valid || saving}>
                        {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("common.save")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
