"use client";

/**
 * One expense over Go (T42): its fields, its photos (object keys through `GET /v1/files?key=`, saved as
 * one ZIP with jszip loaded on the click only, R69), an edit form (`PATCH /v1/expenses/{id}`, the type's
 * edit capability) and, for the audit page, approve / reject (`POST /v1/expenses/{id}/status`; approving
 * a fuel row with an odometer runs the PM check in the same transaction, so the page no longer calls
 * `checkMaintenanceAlert`).
 */
import { useState } from "react";
import { Archive, Check, Loader2, X } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

import { EXPENSE_CATEGORIES, fileHref, patchExpense, setExpenseStatus, type ExpenseDTO, type ExpensePatch } from "../../api/expenses.go";
import { formatDateTime, formatThb, SearchPicker, type PickValue } from "./shared";

const CATEGORY_KEY: Record<string, string> = {
    tire_repair: "accounting.category.tireRepair",
    maintenance: "accounting.category.maintenance",
    toll: "accounting.category.toll",
    parking: "accounting.category.parking",
    other: "accounting.category.other",
};

export function categoryLabel(category: string | null | undefined, t: (k: string, f?: string) => string): string {
    if (!category) return "—";
    return t(CATEGORY_KEY[category] ?? "", category);
}

export function ExpenseStatusBadge({ status }: { status: string }) {
    const { t } = useLanguage();
    const cls =
        status === "approved"
            ? "border-green-300 bg-green-50 text-green-700"
            : status === "rejected"
              ? "border-red-300 bg-red-50 text-red-700"
              : "border-amber-300 bg-amber-50 text-amber-700";
    const key = status === "approved" ? "statusApproved" : status === "rejected" ? "statusRejected" : "statusPending";
    return (
        <Badge variant="outline" className={`text-xs ${cls}`}>
            {t(`accounting.audit.${key}`)}
        </Badge>
    );
}

interface FormState {
    amountThb: string;
    volumeLiters: string;
    pricePerLiter: string;
    odometerKm: string;
    distanceKm: string;
    stationTaxId: string;
    taxInvId: string;
    category: string;
    description: string;
    note: string;
    adminNote: string;
}

function formOf(e: ExpenseDTO): FormState {
    const s = (v: number | string | null | undefined) => (v == null ? "" : String(v));
    return {
        amountThb: s(e.amountThb),
        volumeLiters: s(e.volumeLiters),
        pricePerLiter: e.pricePerLiter == null ? "" : String(Math.round(e.pricePerLiter * 100) / 100),
        odometerKm: s(e.odometerKm),
        distanceKm: s(e.distanceKm),
        stationTaxId: s(e.stationTaxId),
        taxInvId: s(e.taxInvId),
        category: s(e.category),
        description: s(e.description),
        note: s(e.note),
        adminNote: s(e.adminNote),
    };
}

/** The changed fields only (a blank number or text clears it: `null`). */
export function expensePatchOf(e: ExpenseDTO, f: FormState, truck: PickValue | null): ExpensePatch {
    const before = formOf(e);
    const patch: ExpensePatch = {};
    const num = (v: string) => (v.trim() === "" ? null : Number(v));
    const txt = (v: string) => (v.trim() === "" ? null : v.trim());
    const numbers = ["amountThb", "volumeLiters", "pricePerLiter", "odometerKm", "distanceKm"] as const;
    const texts = ["stationTaxId", "taxInvId", "category", "description", "note", "adminNote"] as const;
    const fuelOnly = new Set(["volumeLiters", "pricePerLiter", "odometerKm", "stationTaxId", "taxInvId"]);
    const otherOnly = new Set(["category"]);
    for (const k of numbers) {
        if (f[k] === before[k]) continue;
        if (e.type !== "fuel" && fuelOnly.has(k)) continue;
        const v = num(f[k]);
        (patch as Record<string, unknown>)[k] = k === "pricePerLiter" && v != null ? Math.round(v * 100) / 100 : v;
    }
    for (const k of texts) {
        if (f[k] === before[k]) continue;
        if (e.type !== "fuel" && fuelOnly.has(k)) continue;
        if (e.type === "fuel" && otherOnly.has(k)) continue;
        (patch as Record<string, unknown>)[k] = txt(f[k]);
    }
    if (truck && truck.id !== e.truckId) (patch as Record<string, unknown>).truckId = truck.id;
    return patch;
}

export function ExpenseDetailDialog({
    expense,
    onClose,
    audit = false,
}: {
    expense: ExpenseDTO;
    onClose: () => void;
    /** The audit page: approve / reject with an admin note. */
    audit?: boolean;
}) {
    const { t } = useLanguage();
    const canEdit = useCan(expense.type === "fuel" ? "accounting:edit_fuel" : "accounting:edit_other").allowed;
    const canAudit = useCan("accounting:audit_expense").allowed;
    const [form, setForm] = useState<FormState>(() => formOf(expense));
    const [truck, setTruck] = useState<PickValue | null>(expense.truckId ? { id: expense.truckId, label: expense.licensePlate ?? expense.truckId } : null);
    const [busy, setBusy] = useState<string | null>(null);
    const [zipping, setZipping] = useState(false);
    const set = (k: keyof FormState) => (e: { target: { value: string } }) => setForm((f) => ({ ...f, [k]: e.target.value }));
    const isFuel = expense.type === "fuel";
    const photos = [
        expense.receiptKey ? { key: expense.receiptKey, label: t("accounting.detail.receiptPhoto"), stem: `${expense.id}_receipt` } : null,
        expense.odometerKey ? { key: expense.odometerKey, label: t("accounting.detail.odometerPhoto"), stem: `${expense.id}_odometer` } : null,
    ].filter((p): p is { key: string; label: string; stem: string } => p !== null);

    const save = async () => {
        const patch = expensePatchOf(expense, form, truck);
        if (Object.keys(patch).length === 0) return onClose();
        setBusy("save");
        try {
            await patchExpense(expense.id, patch);
            toast.success(t("accounting.go.expense.saved"));
            onClose();
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setBusy(null);
        }
    };
    const decide = async (status: "approved" | "rejected") => {
        setBusy(status);
        try {
            const out = await setExpenseStatus(expense.id, status, form.adminNote.trim() || undefined);
            if (out.pmCheck?.created) toast.info(t("accounting.go.expense.pmBooked"));
            onClose();
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setBusy(null);
        }
    };
    const zip = async () => {
        setZipping(true);
        try {
            const { downloadImagesAsZip } = await import("@/lib/download-image-urls-zip");
            const entries = photos.map((p) => ({ url: fileHref(p.key) ?? "", filenameStem: p.stem }));
            const out = await downloadImagesAsZip(`${expense.id}_${expense.expenseDate.replace(/-/g, "")}.zip`, entries);
            if (out.failed.length > 0) toast.error(t("accounting.go.expense.zipPartial", { failed: out.failed.length }));
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setZipping(false);
        }
    };

    const num = (k: keyof FormState, label: string) => (
        <div className="space-y-1">
            <Label className="text-xs">{label}</Label>
            <Input type="number" step="any" value={form[k]} onChange={set(k)} disabled={!canEdit} />
        </div>
    );
    const text = (k: keyof FormState, label: string) => (
        <div className="space-y-1">
            <Label className="text-xs">{label}</Label>
            <Input value={form[k]} onChange={set(k)} disabled={!canEdit} />
        </div>
    );

    return (
        <Dialog open onOpenChange={(o) => !o && busy === null && onClose()}>
            <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
                <DialogHeader>
                    <DialogTitle>{isFuel ? t("accounting.detail.title") : t("accounting.detail.otherTitle")}</DialogTitle>
                    <DialogDescription className="flex flex-wrap items-center gap-2">
                        <span>{formatDateTime(expense.date)}</span>
                        <span>·</span>
                        <span>{expense.driverName ?? "—"}</span>
                        <span>·</span>
                        <span className="font-mono">{expense.licensePlate ?? "—"}</span>
                        <ExpenseStatusBadge status={expense.status} />
                    </DialogDescription>
                </DialogHeader>

                <div className="grid grid-cols-2 gap-3">
                    {num("amountThb", t("accounting.detail.amount"))}
                    {isFuel ? num("volumeLiters", t("accounting.detail.volume")) : null}
                    {isFuel ? num("pricePerLiter", t("accounting.detail.pricePerLiter")) : null}
                    {isFuel ? num("odometerKm", t("accounting.detail.odometer")) : null}
                    {num("distanceKm", t("accounting.detail.distanceKm"))}
                    {isFuel ? text("stationTaxId", t("accounting.detail.stationTaxId")) : null}
                    {isFuel ? text("taxInvId", t("accounting.detail.taxInvId")) : null}
                    {!isFuel ? (
                        <div className="space-y-1">
                            <Label className="text-xs">{t("accounting.detail.category")}</Label>
                            <Select value={form.category || "__none__"} onValueChange={(v) => setForm((f) => ({ ...f, category: v === "__none__" ? "" : v }))} disabled={!canEdit}>
                                <SelectTrigger>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="__none__">—</SelectItem>
                                    {EXPENSE_CATEGORIES.map((c) => (
                                        <SelectItem key={c} value={c}>
                                            {categoryLabel(c, t)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    ) : null}
                    {!isFuel ? text("description", t("accounting.detail.description")) : null}
                    <div className="space-y-1">
                        <Label className="text-xs">{t("accounting.detail.vehicle")}</Label>
                        <SearchPicker kind="truck" value={truck} onChange={setTruck} placeholder={t("accounting.tollImport.selectTruckPlaceholder")} disabled={!canEdit} className="w-full" />
                    </div>
                    <div className="col-span-2 space-y-1">
                        <Label className="text-xs">{t("accounting.detail.note")}</Label>
                        <Textarea value={form.note} onChange={set("note")} disabled={!canEdit} rows={2} />
                    </div>
                    {expense.tollLocation || expense.tollLane ? (
                        <p className="col-span-2 text-xs text-muted-foreground">
                            {t("accounting.tollImport.preview.location")}: {[expense.tollLocation, expense.tollLane].filter(Boolean).join(" / ")}
                            {expense.tollImportSequence != null ? ` · #${expense.tollImportSequence}` : ""}
                        </p>
                    ) : null}
                    <p className="col-span-2 text-xs text-muted-foreground">
                        {t("accounting.audit.created")}: {formatDateTime(expense.createdAt)} · {t("accounting.audit.updated")}: {formatDateTime(expense.updatedAt)} ·{" "}
                        {formatThb(expense.amountThb)}
                    </p>
                </div>

                <div className="space-y-2">
                    {photos.length === 0 ? (
                        <p className="text-sm text-muted-foreground">{t("accounting.detail.noImages")}</p>
                    ) : (
                        <div className="flex flex-wrap gap-3">
                            {photos.map((p) => (
                                <a key={p.key} href={fileHref(p.key) ?? undefined} target="_blank" rel="noopener noreferrer" className="block">
                                    {/* eslint-disable-next-line @next/next/no-img-element -- a signed object URL, not a static asset */}
                                    <img src={fileHref(p.key) ?? undefined} alt={p.label} className="h-32 w-32 rounded border object-cover" loading="lazy" />
                                    <span className="mt-1 block text-center text-xs text-muted-foreground">{p.label}</span>
                                </a>
                            ))}
                        </div>
                    )}
                    {photos.length > 0 ? (
                        <Button size="sm" variant="outline" onClick={() => void zip()} disabled={zipping}>
                            {zipping ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Archive className="mr-2 h-4 w-4" />}
                            {t("accounting.go.expense.zip")}
                        </Button>
                    ) : null}
                </div>

                {audit && canAudit ? (
                    <div className="space-y-1">
                        <Label className="text-xs">{t("accounting.audit.adminNote")}</Label>
                        <Textarea value={form.adminNote} onChange={set("adminNote")} placeholder={t("accounting.audit.adminNotePlaceholder")} rows={2} />
                    </div>
                ) : null}

                <DialogFooter className="flex-wrap gap-2">
                    {audit && canAudit && expense.status !== "rejected" ? (
                        <Button variant="destructive" onClick={() => void decide("rejected")} disabled={busy !== null}>
                            {busy === "rejected" ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <X className="mr-2 h-4 w-4" />}
                            {t("accounting.audit.reject")}
                        </Button>
                    ) : null}
                    {audit && canAudit && expense.status !== "approved" ? (
                        <Button variant="default" className="bg-green-600 hover:bg-green-700" onClick={() => void decide("approved")} disabled={busy !== null}>
                            {busy === "approved" ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Check className="mr-2 h-4 w-4" />}
                            {t("accounting.audit.approve")}
                        </Button>
                    ) : null}
                    {canEdit ? (
                        <Button variant="outline" onClick={() => void save()} disabled={busy !== null}>
                            {busy === "save" ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                            {t("accounting.detail.save")}
                        </Button>
                    ) : null}
                    <Button variant="ghost" onClick={onClose} disabled={busy !== null}>
                        {t("accounting.detail.close")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
