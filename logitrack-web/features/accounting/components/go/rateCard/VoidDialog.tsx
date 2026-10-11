"use client";

/**
 * Voiding a rate-card row (T42; R16, R20, ADR 0009 §1): rate entries, fuel adjustments and standby rates
 * are never edited or deleted, only voided with a reason; a correction is a new row with its own
 * effective date.
 */
import { useState } from "react";
import { Loader2 } from "lucide-react";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export function VoidDialog({ label, onVoid, onClose }: { label: string; onVoid: (reason: string) => Promise<unknown>; onClose: () => void }) {
    const { t } = useLanguage();
    const [reason, setReason] = useState("");
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState<unknown>(null);
    const submit = async () => {
        if (!reason.trim()) return;
        setSaving(true);
        setError(null);
        try {
            await onVoid(reason.trim());
            onClose();
        } catch (e) {
            setError(e);
        } finally {
            setSaving(false);
        }
    };
    return (
        <Dialog open onOpenChange={(o) => !o && !saving && onClose()}>
            <DialogContent className="sm:max-w-md">
                <DialogHeader>
                    <DialogTitle>{t("accounting.rateCard.void.title")}</DialogTitle>
                    <DialogDescription>{t("accounting.rateCard.void.description")}</DialogDescription>
                </DialogHeader>
                <p className="text-sm font-medium">{label}</p>
                <div className="space-y-1">
                    <Label>{t("accounting.rateCard.void.reasonLabel")}</Label>
                    <Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder={t("accounting.rateCard.void.reasonPlaceholder")} />
                </div>
                {error ? <p className="text-sm text-destructive">{`${t("accounting.rateCard.void.error")} ${apiErrorText(error, t)}`}</p> : null}
                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={saving}>
                        {t("common.cancel")}
                    </Button>
                    <Button variant="destructive" onClick={() => void submit()} disabled={saving || !reason.trim()}>
                        {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("accounting.rateCard.void.confirm")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
