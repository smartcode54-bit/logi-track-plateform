"use client";

import { Field as AccessibleField } from "@/features/accounting/components/go/shared";

/**
 * An admin-entered fuel or other expense over Go (T42; `POST /v1/expenses`, then the photos uploaded
 * under the new id and linked by `PATCH`, developer-spec.md §9.4). Go stamps the tenant from the driver
 * (else the truck), defaults the truck to the driver's active truck and snapshots its plate; the row is
 * approved unless a status is sent, and an approved fuel entry with an odometer runs the PM check.
 *
 * A photo Go would refuse (JPEG, PNG or WebP up to 10 MiB) is refused here before anything is written. If
 * the row is written but a photo fails afterwards, the dialog keeps the saved row: Save then only uploads
 * and links the photos to it, so a retry never creates a second (approved) expense.
 */
import { useState } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { PartialCreateError, uploadAccept, UploadRefusedError } from "@/lib/goUpload";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

import { attachExpenseFiles, createExpense, EXPENSE_CATEGORIES, type ExpenseDTO, type ExpenseInput, type ExpenseType } from "../../api/expenses.go";
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
    // The row a save wrote before its photos failed: the next save attaches the photos to it.
    const [created, setCreated] = useState<ExpenseDTO | null>(null);

    const reset = () => {
        setCreated(null);
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

    const close = (o: boolean) => {
        if (saving) return;
        // A saved row whose photos were not attached stays saved; the form starts empty next time.
        if (!o && created) reset();
        onOpenChange(o);
    };

    const fileRefused = (e: UploadRefusedError) =>
        t(e.reason === "too_large" ? "accounting.go.expense.fileTooLarge" : "accounting.go.expense.fileType", { name: e.fileName });

    const save = async () => {
        if (!valid) return;
        setSaving(true);
        const files = { receipt, odometer: isFuel ? odoPhoto : null };
        if (created) {
            try {
                await attachExpenseFiles(created, files);
                toast.success(isFuel ? t("accounting.fuel.addDialog.saved") : t("accounting.other.addDialog.saved"));
                reset();
                onOpenChange(false);
            } catch (e) {
                toast.error(e instanceof UploadRefusedError ? fileRefused(e) : `${t("accounting.go.expense.photosNotAttached")} ${apiErrorText(e, t)}`);
            } finally {
                setSaving(false);
            }
            return;
        }
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
            await createExpense(input, files);
            toast.success(isFuel ? t("accounting.fuel.addDialog.saved") : t("accounting.other.addDialog.saved"));
            reset();
            onOpenChange(false);
        } catch (e) {
            if (e instanceof UploadRefusedError) toast.error(fileRefused(e));
            else if (e instanceof PartialCreateError) {
                setCreated(e.entity as ExpenseDTO);
                toast.error(`${t("accounting.go.expense.photosNotAttached")} ${apiErrorText(e.failure, t)}`);
            } else toast.error(`${isFuel ? t("accounting.fuel.addDialog.saveError") : t("accounting.other.addDialog.saveError")} ${apiErrorText(e, t)}`);
        } finally {
            setSaving(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={close}>
            <DialogContent className="sm:max-w-lg">
                <DialogHeader>
                    <DialogTitle>{isFuel ? t("accounting.fuel.addDialog.title") : t("accounting.other.addDialog.title")}</DialogTitle>
                    <DialogDescription>{isFuel ? t("accounting.fuel.addDialog.description") : t("accounting.other.addDialog.description")}</DialogDescription>
                </DialogHeader>
                {created ? (
                    <Alert variant="warning" data-testid="expense-saved-without-photos"><AlertDescription>
                        {t("accounting.go.expense.savedWithoutPhotos")}
                    </AlertDescription></Alert>
                ) : null}
                {/* Once the row is saved only the photos can change: the fields are its stored values. */}
                <fieldset disabled={created !== null} className="min-w-0">
                    <div className="grid grid-cols-2 gap-3">
                        <AccessibleField className="space-y-1" label={t("accounting.fuel.addDialog.driver")} labelClassName="text-xs">

                            <SearchPicker kind="driver" value={driver} onChange={setDriver} placeholder={t("accounting.fuel.addDialog.selectDriver")} className="w-full" />
                        </AccessibleField>
                        <AccessibleField className="space-y-1" label={t("accounting.fuel.addDialog.truck")} labelClassName="text-xs">

                            <SearchPicker kind="truck" value={truck} onChange={setTruck} placeholder={t("accounting.fuel.addDialog.selectTruck")} className="w-full" />
                        </AccessibleField>
                        <AccessibleField className="space-y-1" label={t("accounting.fuel.addDialog.date")} labelClassName="text-xs">

                            <Input type="date" value={date} onChange={(e) => setDate(e.target.value)} />
                        </AccessibleField>
                        <AccessibleField className="space-y-1" label={t("accounting.go.expense.time")} labelClassName="text-xs">

                            <Input type="time" value={time} onChange={(e) => setTime(e.target.value)} />
                        </AccessibleField>
                        <AccessibleField className="space-y-1" label={t("accounting.table.amount")} labelClassName="text-xs">

                            <Input type="number" step="any" min="0" value={amount} onChange={(e) => setAmount(e.target.value)} />
                        </AccessibleField>
                        {isFuel ? (
                            <>
                                <AccessibleField className="space-y-1" label={t("accounting.table.volume")} labelClassName="text-xs">

                                    <Input type="number" step="any" min="0" value={liters} onChange={(e) => setLiters(e.target.value)} />
                                </AccessibleField>
                                <AccessibleField className="space-y-1" label={t("accounting.table.pricePerLiter")} labelClassName="text-xs">

                                    <Input type="number" step="any" min="0" value={price} onChange={(e) => setPrice(e.target.value)} />
                                </AccessibleField>
                                <AccessibleField className="space-y-1" label={t("accounting.table.odometer")} labelClassName="text-xs">

                                    <Input type="number" step="1" min="0" value={odometer} onChange={(e) => setOdometer(e.target.value)} />
                                </AccessibleField>
                            </>
                        ) : (
                            <>
                                <AccessibleField className="space-y-1" label={t("accounting.table.category")} labelClassName="text-xs">

                                    <Select value={category} onValueChange={setCategory}>
                                        <SelectTrigger>
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            {EXPENSE_CATEGORIES.map((c) => (
                                                <SelectItem key={c} value={c}>
                                                    {categoryLabel(c, t)}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                </AccessibleField>
                                <AccessibleField className="col-span-2 space-y-1" label={t("accounting.table.description")} labelClassName="text-xs">

                                    <Input value={description} onChange={(e) => setDescription(e.target.value)} />
                                </AccessibleField>
                            </>
                        )}
                        <AccessibleField className="col-span-2 space-y-1" label={t("accounting.fuel.addDialog.note")} labelClassName="text-xs">

                            <Textarea value={note} onChange={(e) => setNote(e.target.value)} rows={2} />
                        </AccessibleField>
                    </div>
                </fieldset>
                <div className="grid grid-cols-2 gap-3">
                    <AccessibleField className="space-y-1" label={isFuel ? t("accounting.fuel.addDialog.receipt") : t("accounting.other.addDialog.receipt")} labelClassName="text-xs">

                        <Input type="file" accept={uploadAccept("expense_receipt")} onChange={(e) => setReceipt(e.target.files?.[0] ?? null)} />
                    </AccessibleField>
                    {isFuel ? (
                        <AccessibleField className="space-y-1" label={t("accounting.fuel.addDialog.odometerPhoto")} labelClassName="text-xs">

                            <Input type="file" accept={uploadAccept("expense_odometer")} onChange={(e) => setOdoPhoto(e.target.files?.[0] ?? null)} />
                        </AccessibleField>
                    ) : null}
                </div>
                <DialogFooter>
                    <Button variant="outline" onClick={() => close(false)} disabled={saving}>
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
