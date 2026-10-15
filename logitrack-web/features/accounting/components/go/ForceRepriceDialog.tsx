"use client";

import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";

export function ForceRepriceDialog({ scope, perJob = false, onCancel, onConfirm }: {
    scope: { from: string; to: string; customer: string; kinds: string; limit: number };
    perJob?: boolean;
    onCancel: () => void;
    onConfirm: () => void;
}) {
    const { t } = useLanguage();
    return (
        <Dialog open onOpenChange={(open) => !open && onCancel()}>
            <DialogContent>
                <DialogHeader>
                    <DialogTitle>{t("accounting.ui4.forceTitle")}</DialogTitle>
                    <DialogDescription>{t(perJob ? "accounting.ui4.forceBody" : "accounting.ui4.forceRowsBody", scope)}</DialogDescription>
                </DialogHeader>
                <DialogFooter>
                    <Button className="max-md:min-h-11" variant="outline" onClick={onCancel}>{t("common.cancel")}</Button>
                    <Button className="max-md:min-h-11" onClick={onConfirm}>{t("accounting.ui4.forceAction")}</Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
