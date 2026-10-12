"use client";

/**
 * Add or edit a holiday over Go (T46): a new holiday is `POST /v1/holidays` (upsert on its day and type), an
 * existing one `PATCH /v1/holidays/{id}` with the fields that changed, so moving its day or type edits the
 * same row. Public holidays (`tenantId` null, the calendar every tenant prices against) are a steward's only
 * (R60): others get company and other types, and a row's type moves only between company and other (Go
 * refuses a change of scope). The display name and description keep the legacy `EN (TH)` form the
 * Firestore projection hands the 3.x app.
 */
import { useState, type FormEvent } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { HrErrorAlert } from "@/features/driver-compensation/components/go/hrShared";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

import {
    HOLIDAY_STATUSES,
    combinedName,
    usePatchHoliday,
    useSaveHoliday,
    type HolidayDTO,
    type HolidayFields,
    type HolidayStatus,
    type HolidayType,
} from "../api/holidays.go";

interface FormState {
    nameEn: string;
    nameTh: string;
    date: string;
    type: HolidayType;
    status: HolidayStatus;
    descriptionEn: string;
    descriptionTh: string;
    isRecurring: boolean;
}

function initialForm(edit: HolidayDTO | null, day: string | undefined, steward: boolean): FormState {
    return {
        nameEn: edit?.nameEn ?? "",
        nameTh: edit?.nameTh ?? "",
        date: edit?.date ?? day ?? "",
        type: edit?.type ?? (steward ? "public" : "company"),
        status: edit?.status ?? "draft",
        descriptionEn: edit?.descriptionEn ?? "",
        descriptionTh: edit?.descriptionTh ?? "",
        isRecurring: edit?.isRecurring ?? false,
    };
}

const orNull = (s: string) => (s.trim() === "" ? null : s.trim());

/** The full field set of a form (POST), and the fields that differ from the row (PATCH). */
export function holidayFields(f: FormState): HolidayFields {
    const description = combinedName(f.descriptionEn, f.descriptionTh);
    return {
        date: f.date,
        type: f.type,
        status: f.status,
        name: combinedName(f.nameEn, f.nameTh),
        nameEn: orNull(f.nameEn),
        nameTh: orNull(f.nameTh),
        descriptionEn: orNull(f.descriptionEn),
        descriptionTh: orNull(f.descriptionTh),
        description: description === "" ? null : description,
        isRecurring: f.isRecurring,
    };
}

export function changedFields(row: HolidayDTO, next: HolidayFields): Partial<HolidayFields> {
    const out: Partial<HolidayFields> = {};
    const cur: Record<string, unknown> = { ...row };
    for (const [k, v] of Object.entries(next) as [keyof HolidayFields, unknown][]) {
        if ((cur[k] ?? null) !== (v ?? null)) (out as Record<string, unknown>)[k] = v;
    }
    return out;
}

export function HolidayDialog({
    open,
    onOpenChange,
    edit,
    initialDate,
    steward,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    edit: HolidayDTO | null;
    initialDate?: string;
    steward: boolean;
}) {
    if (!open) return null;
    // Remounted per opening, so the form starts from the row (or the clicked day) each time.
    return <HolidayForm key={edit?.id ?? `new:${initialDate ?? ""}`} onOpenChange={onOpenChange} edit={edit} initialDate={initialDate} steward={steward} />;
}

function HolidayForm({
    onOpenChange,
    edit,
    initialDate,
    steward,
}: {
    onOpenChange: (open: boolean) => void;
    edit: HolidayDTO | null;
    initialDate?: string;
    steward: boolean;
}) {
    const { t } = useLanguage();
    const save = useSaveHoliday();
    const patch = usePatchHoliday();
    const [form, setForm] = useState<FormState>(() => initialForm(edit, initialDate, steward));
    const [error, setError] = useState<unknown>(null);
    const busy = save.isPending || patch.isPending;
    const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setForm((f) => ({ ...f, [k]: v }));

    // A public row stays public; a tenant's row moves between company and other only.
    const types: HolidayType[] = edit ? (edit.type === "public" ? ["public"] : ["company", "other"]) : steward ? ["public", "company", "other"] : ["company", "other"];

    const submit = async (e: FormEvent) => {
        e.preventDefault();
        setError(null);
        if (!form.nameEn.trim() && !form.nameTh.trim()) {
            toast.error(t("holidays.toast.fillRequired"));
            return;
        }
        if (!form.date) {
            toast.error(t("holidays.toast.fillRequired"));
            return;
        }
        const fields = holidayFields(form);
        try {
            if (edit) {
                const diff = changedFields(edit, fields);
                if (Object.keys(diff).length > 0) await patch.mutateAsync({ id: edit.id, fields: diff });
                toast.success(t("holidays.toast.updated"));
            } else {
                await save.mutateAsync(fields);
                toast.success(t("holidays.toast.added"));
            }
            onOpenChange(false);
        } catch (err) {
            setError(err);
        }
    };

    return (
        <Dialog open onOpenChange={(o) => !busy && onOpenChange(o)}>
            <DialogContent className="sm:max-w-[460px]">
                <DialogHeader>
                    <DialogTitle>{edit ? t("holidays.dialog.editTitle") : t("holidays.dialog.addTitle")}</DialogTitle>
                    <DialogDescription>{edit ? t("holidays.dialog.editDesc") : t("holidays.dialog.addDesc")}</DialogDescription>
                </DialogHeader>
                <form onSubmit={(e) => void submit(e)} className="space-y-4 py-2">
                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-2">
                            <Label htmlFor="holiday-name-en">{t("holidays.dialog.nameEnLabel")}</Label>
                            <Input
                                id="holiday-name-en"
                                placeholder={t("holidays.dialog.nameEnPlaceholder")}
                                value={form.nameEn}
                                maxLength={300}
                                onChange={(e) => set("nameEn", e.target.value)}
                            />
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="holiday-name-th">{t("holidays.dialog.nameThLabel")}</Label>
                            <Input
                                id="holiday-name-th"
                                placeholder={t("holidays.dialog.nameThPlaceholder")}
                                value={form.nameTh}
                                maxLength={300}
                                onChange={(e) => set("nameTh", e.target.value)}
                            />
                        </div>
                    </div>
                    <p className="text-[11px] italic text-muted-foreground">{t("hr.holidays.nameHint")}</p>
                    <div className="space-y-2">
                        <Label htmlFor="holiday-date">{t("holidays.dialog.dateLabel")} *</Label>
                        <Input id="holiday-date" type="date" value={form.date} onChange={(e) => set("date", e.target.value)} required />
                    </div>
                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-2">
                            <Label>{t("holidays.dialog.typeLabel")}</Label>
                            <Select value={form.type} onValueChange={(v) => set("type", v as HolidayType)} disabled={types.length === 1}>
                                <SelectTrigger aria-label={t("holidays.dialog.typeLabel")}>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="z-[1005]" position="popper">
                                    {types.map((ty) => (
                                        <SelectItem key={ty} value={ty}>
                                            {t(`holidays.type.${ty}`)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-2">
                            <Label>{t("holidays.dialog.statusLabel")}</Label>
                            <Select value={form.status} onValueChange={(v) => set("status", v as HolidayStatus)}>
                                <SelectTrigger aria-label={t("holidays.dialog.statusLabel")}>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="z-[1005]" position="popper">
                                    {HOLIDAY_STATUSES.map((st) => (
                                        <SelectItem key={st} value={st}>
                                            {t(`holidays.status.${st}`)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    </div>
                    <div className="flex items-center space-x-2">
                        <Checkbox id="holiday-recurring" checked={form.isRecurring} onCheckedChange={(c) => set("isRecurring", c === true)} />
                        <Label htmlFor="holiday-recurring" className="text-sm font-medium leading-none">
                            {t("holidays.dialog.recurringLabel")}
                        </Label>
                    </div>
                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-2">
                            <Label htmlFor="holiday-desc-en">{t("hr.holidays.descriptionEn")}</Label>
                            <Textarea
                                id="holiday-desc-en"
                                value={form.descriptionEn}
                                maxLength={2000}
                                onChange={(e) => set("descriptionEn", e.target.value)}
                                className="h-20 text-xs"
                            />
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="holiday-desc-th">{t("hr.holidays.descriptionTh")}</Label>
                            <Textarea
                                id="holiday-desc-th"
                                value={form.descriptionTh}
                                maxLength={2000}
                                onChange={(e) => set("descriptionTh", e.target.value)}
                                className="h-20 text-xs"
                            />
                        </div>
                    </div>
                    <HrErrorAlert error={error} />
                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
                            {t("holidays.dialog.cancel")}
                        </Button>
                        <Button type="submit" disabled={busy} className="bg-blue-600 hover:bg-blue-700">
                            {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                            {edit ? t("holidays.dialog.updateButton") : t("holidays.dialog.saveButton")}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}
