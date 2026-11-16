"use client";

import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { Textarea } from "@/components/ui/textarea";
import { useLanguage } from "@/context/language";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { pnlVoidInputSchema, type PnlVoidInput } from "@/validate/pnlSettingsSchema";

/** Void a settings row with a required reason (append-only: a void is the only allowed update). */
export function PnlVoidDialog({
    open,
    onOpenChange,
    description,
    onConfirm,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    description: string;
    onConfirm: (reason: string) => Promise<void>;
}) {
    const { t } = useLanguage();
    const [saving, setSaving] = useState(false);
    const form = useForm<PnlVoidInput>({ resolver: zodResolver(pnlVoidInputSchema), defaultValues: { reason: "" } });

    useEffect(() => {
        if (open) form.reset({ reason: "" });
    }, [open, form]);

    const submit = async (values: PnlVoidInput) => {
        setSaving(true);
        try {
            await onConfirm(values.reason);
            onOpenChange(false);
        } finally {
            setSaving(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent>
                <DialogHeader>
                    <DialogTitle>{t("pnl.settings.void.title")}</DialogTitle>
                    <DialogDescription>{description}</DialogDescription>
                </DialogHeader>
                <Form {...form}>
                    <form onSubmit={form.handleSubmit(submit, createInvalidHandler(form, t))} className="space-y-4">
                        <FormField
                            control={form.control}
                            name="reason"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>{t("pnl.settings.void.reason")}</FormLabel>
                                    <FormControl>
                                        <Textarea {...field} />
                                    </FormControl>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />
                        <p className="text-xs text-muted-foreground">{t("pnl.settings.void.warning")}</p>
                        <DialogFooter>
                            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>{t("pnl.settings.cancel")}</Button>
                            <Button type="submit" variant="destructive" disabled={saving}>
                                {saving && <Loader2 className="w-4 h-4 mr-1 animate-spin" />}
                                {t("pnl.settings.void.confirm")}
                            </Button>
                        </DialogFooter>
                    </form>
                </Form>
            </DialogContent>
        </Dialog>
    );
}
