"use client";

/** The confirmation before a broadcast is withdrawn from the history and the driver app (both sources). */
import { Loader2 } from "lucide-react";

import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";

interface BroadcastVoidConfirmDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onConfirm: () => void | Promise<void>;
    loading: boolean;
}

export function BroadcastVoidConfirmDialog({ open, onOpenChange, onConfirm, loading }: BroadcastVoidConfirmDialogProps) {
    const { t } = useLanguage();
    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent
                className="z-[1003] sm:max-w-md"
                onPointerDownOutside={(e) => loading && e.preventDefault()}
                onEscapeKeyDown={(e) => loading && e.preventDefault()}
            >
                <DialogHeader>
                    <DialogTitle>{t("chat.broadcast.voidTitle")}</DialogTitle>
                    <DialogDescription className="space-y-2 pt-1 text-left">
                        <span className="block">{t("chat.broadcast.voidBody")}</span>
                        <span className="block text-sm text-amber-600 dark:text-amber-500">{t("chat.broadcast.voidPushNote")}</span>
                    </DialogDescription>
                </DialogHeader>
                <DialogFooter className="gap-2 sm:gap-0">
                    <Button type="button" variant="outline" disabled={loading} onClick={() => onOpenChange(false)}>
                        {t("chat.broadcast.back")}
                    </Button>
                    <Button type="button" variant="destructive" disabled={loading} onClick={() => void onConfirm()}>
                        {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : t("chat.broadcast.void")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
