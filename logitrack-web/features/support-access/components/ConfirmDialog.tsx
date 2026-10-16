"use client";

import { useRef, type RefObject } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useLanguage } from "@/context/language";

// The shared Dialog provides the modal/focus primitives; this confirmation has alertdialog semantics.
export function ConfirmDialog({ open, busy, confirmDisabled, title, description, confirmLabel, returnFocus, fallbackFocus, onCancel, onConfirm }: {
    open: boolean;
    busy: boolean;
    confirmDisabled: boolean;
    title: string;
    description: string;
    confirmLabel: string;
    returnFocus: RefObject<HTMLElement | null>;
    fallbackFocus: RefObject<HTMLElement | null>;
    onCancel: () => void;
    onConfirm: () => void;
}) {
    const { t } = useLanguage();
    const cancel = useRef<HTMLButtonElement>(null);
    return (
        <Dialog open={open} onOpenChange={(value) => { if (!value && !busy) onCancel(); }}>
            <DialogContent role="alertdialog" showCloseButton={false} aria-busy={busy}
                onOpenAutoFocus={(event) => { event.preventDefault(); cancel.current?.focus(); }}
                onCloseAutoFocus={(event) => {
                    event.preventDefault();
                    const trigger = returnFocus.current;
                    if (trigger?.isConnected && !trigger.matches(':disabled, [aria-disabled="true"]')) trigger.focus();
                    else fallbackFocus.current?.focus();
                }}
                onEscapeKeyDown={(event) => { if (busy) event.preventDefault(); }}
                onInteractOutside={(event) => event.preventDefault()}>
                <DialogHeader>
                    <DialogTitle className="leading-normal">{title}</DialogTitle>
                    <DialogDescription className="break-words leading-relaxed">{description}</DialogDescription>
                </DialogHeader>
                <DialogFooter>
                    <Button ref={cancel} variant="outline" className="max-md:min-h-11" disabled={busy} onClick={onCancel}>{t("supportAccess.cancel")}</Button>
                    <Button className="max-md:min-h-11" disabled={busy || confirmDisabled} onClick={onConfirm}>{busy ? t("supportAccess.saving") : confirmLabel}</Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
