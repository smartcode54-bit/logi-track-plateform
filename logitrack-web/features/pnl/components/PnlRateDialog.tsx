"use client";

import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
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
import { PNL_RATE_KINDS, pnlRateInputSchema, type PnlRateInput, type PnlRateKind } from "@/validate/pnlSettingsSchema";

type FormIn = z.input<typeof pnlRateInputSchema>;

/** Add a rate row effective from a date (R1/R3 — never edits an existing row). */
export function PnlRateDialog({
    open,
    onOpenChange,
    kind,
    defaultAmount,
    onSave,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    kind: PnlRateKind;
    defaultAmount?: number;
    onSave: (input: PnlRateInput) => Promise<void>;
}) {
    const { t } = useLanguage();
    const [saving, setSaving] = useState(false);
    const form = useForm<FormIn, unknown, PnlRateInput>({
        resolver: zodResolver(pnlRateInputSchema),
        defaultValues: { kind, amountThb: defaultAmount ?? "", effectiveFrom: "", note: "" },
    });

    useEffect(() => {
        if (open) form.reset({ kind, amountThb: defaultAmount ?? "", effectiveFrom: "", note: "" });
    }, [open, kind, defaultAmount, form]);

    const submit = async (values: PnlRateInput) => {
        setSaving(true);
        try {
            await onSave(values);
            onOpenChange(false);
        } finally {
            setSaving(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent>
                <DialogHeader>
                    <DialogTitle>{t("pnl.settings.rate.addTitle")}</DialogTitle>
                    <DialogDescription>{t("pnl.settings.rate.addHint")}</DialogDescription>
                </DialogHeader>
                <Form {...form}>
                    <form onSubmit={form.handleSubmit(submit, createInvalidHandler(form, t))} className="space-y-4">
                        <FormField
                            control={form.control}
                            name="kind"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>{t("pnl.settings.rate.kind")}</FormLabel>
                                    <Select value={field.value} onValueChange={field.onChange}>
                                        <FormControl>
                                            <SelectTrigger><SelectValue /></SelectTrigger>
                                        </FormControl>
                                        <SelectContent className="z-[1005]" position="popper">
                                            {PNL_RATE_KINDS.map((k) => (
                                                <SelectItem key={k} value={k}>{t(`pnl.settings.rate.${k}`)}</SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />
                        <FormField
                            control={form.control}
                            name="amountThb"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>{t("pnl.settings.rate.amount")}</FormLabel>
                                    <FormControl>
                                        <Input type="number" step="any" min={0} value={String(field.value ?? "")} onChange={field.onChange} onBlur={field.onBlur} name={field.name} ref={field.ref} />
                                    </FormControl>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />
                        <FormField
                            control={form.control}
                            name="effectiveFrom"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>{t("pnl.settings.effectiveFrom")}</FormLabel>
                                    <FormControl>
                                        <Input type="date" {...field} />
                                    </FormControl>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />
                        <FormField
                            control={form.control}
                            name="note"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>{t("pnl.settings.note")}</FormLabel>
                                    <FormControl>
                                        <Input {...field} value={field.value ?? ""} />
                                    </FormControl>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />
                        <DialogFooter>
                            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>{t("pnl.settings.cancel")}</Button>
                            <Button type="submit" disabled={saving}>
                                {saving && <Loader2 className="w-4 h-4 mr-1 animate-spin" />}
                                {t("pnl.settings.save")}
                            </Button>
                        </DialogFooter>
                    </form>
                </Form>
            </DialogContent>
        </Dialog>
    );
}
