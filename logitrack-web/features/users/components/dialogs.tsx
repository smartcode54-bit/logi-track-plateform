"use client";

/**
 * Small dialogs of the users and tenants pages (T18): the temporary password shown once (R29: never
 * emailed, never shown again; the user must change it at the next sign-in, R79) and a confirmation
 * with an optional reason.
 */
import { useState } from "react";
import { Copy, Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useLanguage } from "@/context/language";

export function TemporaryPasswordDialog({
    password,
    account,
    onClose,
}: {
    /** The password returned by Go, or null when nothing is shown. */
    password: string | null;
    account?: string;
    onClose: () => void;
}) {
    const { t } = useLanguage();
    const copy = async () => {
        if (!password) return;
        try {
            await navigator.clipboard.writeText(password);
            toast.success(t("users.tempPassword.copied"));
        } catch {
            toast.error(t("users.tempPassword.copyFailed"));
        }
    };
    return (
        <Dialog open={password !== null} onOpenChange={(open) => !open && onClose()}>
            <DialogContent className="sm:max-w-[460px]">
                <DialogHeader>
                    <DialogTitle>{t("users.tempPassword.title")}</DialogTitle>
                    <DialogDescription>{t("users.tempPassword.description")}</DialogDescription>
                </DialogHeader>
                {account ? <p className="text-sm font-medium">{account}</p> : null}
                <div className="flex items-center gap-2">
                    <code data-testid="temporary-password" className="flex-1 select-all rounded-md border bg-muted px-3 py-2 font-mono text-base tracking-wider">
                        {password}
                    </code>
                    <Button type="button" variant="outline" size="icon" onClick={() => void copy()} aria-label={t("users.tempPassword.copy")}>
                        <Copy className="h-4 w-4" />
                    </Button>
                </div>
                <p className="text-xs text-amber-600">{t("users.tempPassword.once")}</p>
                <DialogFooter>
                    <Button type="button" onClick={onClose}>
                        {t("users.tempPassword.done")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

export function ConfirmDialog({
    open,
    title,
    description,
    confirmLabel,
    destructive,
    withReason,
    onConfirm,
    onCancel,
}: {
    open: boolean;
    title: string;
    description?: React.ReactNode;
    confirmLabel: string;
    destructive?: boolean;
    /** Ask for an optional reason (`POST /v1/users/{id}/disable {reason}`). */
    withReason?: boolean;
    onConfirm: (reason: string) => Promise<void>;
    onCancel: () => void;
}) {
    const { t } = useLanguage();
    const [reason, setReason] = useState("");
    const [busy, setBusy] = useState(false);
    const confirm = async () => {
        setBusy(true);
        try {
            await onConfirm(reason);
            setReason("");
        } finally {
            setBusy(false);
        }
    };
    return (
        <Dialog
            open={open}
            onOpenChange={(next) => {
                if (!next && !busy) {
                    setReason("");
                    onCancel();
                }
            }}
        >
            <DialogContent>
                <DialogHeader>
                    <DialogTitle>{title}</DialogTitle>
                    {description ? <DialogDescription asChild><div className="space-y-2 text-sm text-muted-foreground">{description}</div></DialogDescription> : null}
                </DialogHeader>
                {withReason ? (
                    <div className="space-y-2">
                        <Label htmlFor="confirm-reason">{t("users.disable.reason")}</Label>
                        <Input id="confirm-reason" value={reason} onChange={(e) => setReason(e.target.value)} maxLength={500} disabled={busy} />
                    </div>
                ) : null}
                <DialogFooter>
                    <Button type="button" variant="outline" onClick={onCancel} disabled={busy}>
                        {t("users.form.cancel")}
                    </Button>
                    <Button type="button" variant={destructive ? "destructive" : "default"} onClick={() => void confirm()} disabled={busy}>
                        {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {confirmLabel}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
