"use client";

/**
 * Must-change-password step (R79; Appendix C §C.4.8). A sign-in of a `must_change_password` user gets
 * `403 password_change_required` with a single-use `passwordChangeTicket` (10 min) and no cookie. This
 * form redeems it with `POST /api/go/v1/auth/password/change {passwordChangeTicket, newPassword}`
 * (anonymous: the BFF may attach a stale bearer, which Go ignores when the body carries a ticket),
 * then `onChanged(newPassword)` signs in again through `POST /api/auth/login`, the only route that sets
 * the session cookies. A policy violation keeps the ticket (Go checks the policy before consuming it);
 * an expired or used ticket sends the user back to the sign-in form.
 */
import { useState } from "react";
import { Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useLanguage } from "@/context/language";
import { changePasswordWithTicket } from "@/lib/authClient";
import { passwordErrorText, violationFor } from "../utils/fieldErrors";

export interface ChangePasswordFormProps {
    ticket: string;
    /** The account's email, when the sign-in was by password (shown, and used to sign in again). */
    email?: string;
    /** The password was changed; resolve when the follow-up (sign-in or message) is done. */
    onChanged: (newPassword: string) => Promise<void> | void;
    /** The ticket is unusable or the user gave up: back to the sign-in form, with a message key. */
    onCancel: (messageKey?: string) => void;
    idPrefix?: string;
}

export function ChangePasswordForm({ ticket, email, onChanged, onCancel, idPrefix = "chg" }: ChangePasswordFormProps) {
    const { t } = useLanguage();
    const [password, setPassword] = useState("");
    const [confirm, setConfirm] = useState("");
    const [submitting, setSubmitting] = useState(false);
    const [error, setError] = useState("");

    const submit = async (e: React.FormEvent) => {
        e.preventDefault();
        setError("");
        if (password !== confirm) {
            setError(t("auth.changePassword.mismatch"));
            return;
        }
        setSubmitting(true);
        try {
            await changePasswordWithTicket(ticket, password);
        } catch (err) {
            setSubmitting(false);
            if (violationFor(err, "passwordChangeTicket")) {
                onCancel("auth.changePassword.ticketExpired");
                return;
            }
            setError(passwordErrorText(err, "newPassword", t));
            return;
        }
        try {
            await onChanged(password);
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <form onSubmit={submit} className="space-y-4" noValidate>
            <div className="space-y-1">
                <p className="text-base font-semibold">{t("auth.changePassword.title")}</p>
                <p className="text-sm text-muted-foreground">{t("auth.changePassword.subtitle")}</p>
                {email ? <p className="text-sm font-medium">{email}</p> : null}
            </div>
            <div className="space-y-2">
                <Label htmlFor={`${idPrefix}-new`}>{t("auth.changePassword.newPassword")}</Label>
                <Input
                    id={`${idPrefix}-new`}
                    type="password"
                    autoComplete="new-password"
                    required
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    disabled={submitting}
                />
            </div>
            <div className="space-y-2">
                <Label htmlFor={`${idPrefix}-confirm`}>{t("auth.changePassword.confirm")}</Label>
                <Input
                    id={`${idPrefix}-confirm`}
                    type="password"
                    autoComplete="new-password"
                    required
                    value={confirm}
                    onChange={(e) => setConfirm(e.target.value)}
                    disabled={submitting}
                />
            </div>
            {error ? (
                <p role="alert" className="text-sm text-destructive">
                    {error}
                </p>
            ) : null}
            <Button type="submit" className="w-full" disabled={submitting || !password || !confirm}>
                {submitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                {email ? t("auth.changePassword.submit") : t("auth.changePassword.submitGoogle")}
            </Button>
            <Button type="button" variant="link" className="w-full" onClick={() => onCancel()} disabled={submitting}>
                {t("auth.changePassword.back")}
            </Button>
        </form>
    );
}
