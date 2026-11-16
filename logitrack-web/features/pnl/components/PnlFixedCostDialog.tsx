"use client";

import { useEffect, useState } from "react";
import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import type { z } from "zod";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLanguage } from "@/context/language";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import {
    PNL_FIXED_COST_CATEGORIES,
    PNL_FIXED_COST_FREQUENCIES,
    PNL_FIXED_COST_SCOPES,
    PNL_TRUCK_ONLY_CATEGORIES,
    pnlFixedCostInputSchema,
    type PnlFixedCostParsed,
    type PnlFixedCostVersion,
} from "@/validate/pnlSettingsSchema";

type FormIn = z.input<typeof pnlFixedCostInputSchema>;
export type FixedCostDialogMode = "new" | "change" | "stop";

/**
 * Append a fixed-cost version (R2/R3): a new item, a new amount from a month, or a stop from a month.
 * Months before `effectiveFromMonth` keep the figures they had.
 */
export function PnlFixedCostDialog({
    open,
    onOpenChange,
    mode,
    latest,
    trucks,
    customLabels,
    onSave,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    mode: FixedCostDialogMode;
    latest?: PnlFixedCostVersion;
    trucks: Array<{ id: string; plate: string }>;
    /** Names of owner-made categories already in use — offered as suggestions. */
    customLabels: string[];
    onSave: (values: PnlFixedCostParsed) => Promise<void>;
}) {
    const { t } = useLanguage();
    const [saving, setSaving] = useState(false);
    const defaults = (): FormIn => ({
        name: latest?.name ?? "",
        category: latest?.category ?? "installment",
        categoryLabel: latest?.categoryLabel ?? "",
        scope: latest?.scope ?? "truck",
        truckId: latest?.truckId ?? "",
        truckPlate: latest?.truckPlate ?? "",
        frequency: latest?.frequency ?? "monthly",
        amountThb: mode === "stop" ? 0 : latest?.amountThb ?? "",
        effectiveFromMonth: "",
        effectiveToMonth: mode === "stop" ? "" : latest?.effectiveToMonth ?? "",
        isStop: mode === "stop",
        note: "",
    });
    const form = useForm<FormIn, unknown, PnlFixedCostParsed>({ resolver: zodResolver(pnlFixedCostInputSchema), defaultValues: defaults() });
    const scope = useWatch({ control: form.control, name: "scope" });
    const category = useWatch({ control: form.control, name: "category" });
    const truckOnly = PNL_TRUCK_ONLY_CATEGORIES.includes(category);

    useEffect(() => {
        if (open) form.reset(defaults());
        // eslint-disable-next-line react-hooks/exhaustive-deps -- reset only when the dialog opens for an item
    }, [open, mode, latest?.id]);

    // พรบ. / ภาษีรถ belong to one truck.
    useEffect(() => {
        if (truckOnly && form.getValues("scope") !== "truck") form.setValue("scope", "truck");
    }, [truckOnly, form]);

    const submit = async (values: PnlFixedCostParsed) => {
        setSaving(true);
        try {
            const plate = trucks.find((tr) => tr.id === values.truckId)?.plate;
            await onSave({ ...values, truckPlate: values.scope === "truck" ? plate ?? values.truckPlate : undefined });
            onOpenChange(false);
        } finally {
            setSaving(false);
        }
    };

    const title = t(`pnl.settings.fixed.dialog.${mode}`);
    const stop = mode === "stop";

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-lg">
                <DialogHeader>
                    <DialogTitle>{title}</DialogTitle>
                    <DialogDescription>{stop ? t("pnl.settings.fixed.stopHint", { name: latest?.name ?? "" }) : t("pnl.settings.fixed.versionHint")}</DialogDescription>
                </DialogHeader>
                <Form {...form}>
                    <form onSubmit={form.handleSubmit(submit, createInvalidHandler(form, t))} className="space-y-3">
                        {!stop && (
                            <>
                                <FormField control={form.control} name="name" render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("pnl.settings.fixed.name")}</FormLabel>
                                        <FormControl><Input {...field} /></FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )} />
                                <div className="grid grid-cols-2 gap-3">
                                    <EnumField form={form} name="category" label={t("pnl.settings.fixed.categoryLabel")}
                                        options={PNL_FIXED_COST_CATEGORIES.map((c) => [c, t(`pnl.settings.fixed.category.${c}`)])} />
                                    <EnumField form={form} name="frequency" label={t("pnl.settings.fixed.frequencyLabel")}
                                        options={PNL_FIXED_COST_FREQUENCIES.map((f) => [f, t(`pnl.settings.fixed.frequency.${f}`)])} />
                                    {category === "custom" && (
                                        <FormField control={form.control} name="categoryLabel" render={({ field }) => (
                                            <FormItem className="col-span-2">
                                                <FormLabel>{t("pnl.settings.fixed.customLabel")}</FormLabel>
                                                <FormControl>
                                                    <Input {...field} value={field.value ?? ""} list="pnl-custom-categories" placeholder={t("pnl.settings.fixed.customLabelHint")} />
                                                </FormControl>
                                                <datalist id="pnl-custom-categories">
                                                    {customLabels.map((l) => <option key={l} value={l} />)}
                                                </datalist>
                                                <FormMessage />
                                            </FormItem>
                                        )} />
                                    )}
                                    <EnumField form={form} name="scope" label={t("pnl.settings.fixed.scopeLabel")} disabled={truckOnly}
                                        options={PNL_FIXED_COST_SCOPES.map((s) => [s, t(`pnl.settings.fixed.scope.${s}`)])} />
                                    {scope === "truck" && (
                                        <EnumField form={form} name="truckId" label={t("pnl.col.truck")}
                                            options={trucks.map((tr) => [tr.id, tr.plate])} />
                                    )}
                                </div>
                                <FormField control={form.control} name="amountThb" render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("pnl.settings.fixed.amount")}</FormLabel>
                                        <FormControl>
                                            <Input type="number" step="any" min={0} value={String(field.value ?? "")} onChange={field.onChange} onBlur={field.onBlur} name={field.name} ref={field.ref} />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )} />
                            </>
                        )}
                        <div className="grid grid-cols-2 gap-3">
                            <FormField control={form.control} name="effectiveFromMonth" render={({ field }) => (
                                <FormItem>
                                    <FormLabel>{stop ? t("pnl.settings.fixed.stopFrom") : t("pnl.settings.fixed.fromMonth")}</FormLabel>
                                    <FormControl><Input type="month" {...field} /></FormControl>
                                    <FormMessage />
                                </FormItem>
                            )} />
                            {!stop && (
                                <FormField control={form.control} name="effectiveToMonth" render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("pnl.settings.fixed.toMonth")}</FormLabel>
                                        <FormControl><Input type="month" {...field} value={field.value ?? ""} /></FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )} />
                            )}
                        </div>
                        <FormField control={form.control} name="note" render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("pnl.settings.note")}</FormLabel>
                                <FormControl><Input {...field} value={field.value ?? ""} /></FormControl>
                                <FormMessage />
                            </FormItem>
                        )} />
                        <DialogFooter>
                            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>{t("pnl.settings.cancel")}</Button>
                            <Button type="submit" variant={stop ? "destructive" : "default"} disabled={saving}>
                                {saving && <Loader2 className="w-4 h-4 mr-1 animate-spin" />}
                                {stop ? t("pnl.settings.fixed.stopConfirm") : t("pnl.settings.save")}
                            </Button>
                        </DialogFooter>
                    </form>
                </Form>
            </DialogContent>
        </Dialog>
    );
}

function EnumField({
    form,
    name,
    label,
    options,
    disabled,
}: {
    form: ReturnType<typeof useForm<FormIn, unknown, PnlFixedCostParsed>>;
    name: "category" | "frequency" | "scope" | "truckId";
    label: string;
    options: Array<[string, string]>;
    disabled?: boolean;
}) {
    return (
        <FormField control={form.control} name={name} render={({ field }) => (
            <FormItem>
                <FormLabel>{label}</FormLabel>
                <Select value={(field.value as string | undefined) ?? ""} onValueChange={field.onChange} disabled={disabled}>
                    <FormControl>
                        <SelectTrigger><SelectValue /></SelectTrigger>
                    </FormControl>
                    <SelectContent className="z-[1005]" position="popper">
                        {options.map(([v, l]) => (
                            <SelectItem key={v} value={v}>{l}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <FormMessage />
            </FormItem>
        )} />
    );
}
