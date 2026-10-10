"use client";

/**
 * The sign-in form of the login page and of the landing page's login dialog (T18; developer-spec.md
 * §10.4): email and password through `POST /api/auth/login`, Google through the GIS button and
 * `POST /api/auth/google`, and the must-change-password step (R79) when either answers
 * `403 password_change_required`. Errors are rendered from the en/th catalogues by code (R76).
 */
import { useCallback, useState } from "react";
import Link from "next/link";
import { Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { passwordChangeTicket } from "@/lib/authClient";
import { ChangePasswordForm } from "./ChangePasswordForm";
import { GoogleSignInButton } from "./GoogleSignInButton";

export interface SignInPanelProps {
    /** The session cookies are set and `['me']` holds the principal. */
    onSignedIn?: () => void;
    /** An informational line above the form (e.g. why the user was signed out). */
    notice?: string;
    idPrefix?: string;
}

interface ChangeStep {
    ticket: string;
    /** Set when the sign-in was by password: the form signs in again with it after the change. */
    email?: string;
}

export function SignInPanel({ onSignedIn, notice, idPrefix = "login" }: SignInPanelProps) {
    const auth = useAuth();
    const { t } = useLanguage();
    const [email, setEmail] = useState("");
    const [password, setPassword] = useState("");
    const [submitting, setSubmitting] = useState(false);
    const [error, setError] = useState("");
    const [info, setInfo] = useState(notice ?? "");
    const [change, setChange] = useState<ChangeStep | null>(null);

    const failed = useCallback(
        (err: unknown, passwordEmail?: string) => {
            const ticket = passwordChangeTicket(err);
            if (ticket) {
                setError("");
                setInfo("");
                setChange({ ticket, email: passwordEmail });
                return;
            }
            setError(apiErrorText(err, t));
        },
        [t]
    );

    const submit = async (e: React.FormEvent) => {
        e.preventDefault();
        if (!auth) return;
        setError("");
        setSubmitting(true);
        try {
            await auth.login(email.trim(), password);
            setPassword("");
            onSignedIn?.();
        } catch (err) {
            failed(err, email.trim());
        } finally {
            setSubmitting(false);
        }
    };

    const google = useCallback(
        async (idToken: string, nonce: string) => {
            if (!auth) return;
            setError("");
            setSubmitting(true);
            try {
                await auth.loginWithGoogle(idToken, nonce);
                onSignedIn?.();
            } catch (err) {
                failed(err);
                throw err;
            } finally {
                setSubmitting(false);
            }
        },
        [auth, failed, onSignedIn]
    );

    const changed = async (newPassword: string) => {
        const step = change;
        if (!auth || !step) return;
        if (!step.email) {
            // Google sign-in: the user signs in again (with Google, or with the new password).
            setChange(null);
            setInfo(t("auth.changePassword.done"));
            return;
        }
        try {
            await auth.login(step.email, newPassword);
            setChange(null);
            setPassword("");
            onSignedIn?.();
        } catch (err) {
            setChange(null);
            setInfo(t("auth.changePassword.done"));
            failed(err, step.email);
        }
    };

    if (change) {
        return (
            <ChangePasswordForm
                ticket={change.ticket}
                email={change.email}
                idPrefix={`${idPrefix}-chg`}
                onChanged={changed}
                onCancel={(messageKey) => {
                    setChange(null);
                    setPassword("");
                    if (messageKey) setInfo(t(messageKey));
                }}
            />
        );
    }

    return (
        <div className="space-y-4">
            {info ? (
                <p role="status" className="rounded-md border border-border bg-muted/40 px-3 py-2 text-sm">
                    {info}
                </p>
            ) : null}
            <form onSubmit={submit} className="space-y-4">
                <div className="space-y-2">
                    <Label htmlFor={`${idPrefix}-email`}>{t("auth.email")}</Label>
                    <Input
                        id={`${idPrefix}-email`}
                        type="email"
                        autoComplete="username"
                        placeholder={t("auth.emailPlaceholder")}
                        required
                        value={email}
                        onChange={(e) => setEmail(e.target.value)}
                        disabled={submitting}
                        className="bg-muted/5 border-muted-foreground/20 focus-visible:ring-primary"
                    />
                </div>
                <div className="space-y-2">
                    <div className="flex items-center justify-between">
                        <Label htmlFor={`${idPrefix}-password`}>{t("auth.password")}</Label>
                        <Link href="/forgot-password" className="text-sm font-medium text-muted-foreground hover:text-primary transition-colors">
                            {t("auth.forgotPassword")}
                        </Link>
                    </div>
                    <Input
                        id={`${idPrefix}-password`}
                        type="password"
                        autoComplete="current-password"
                        placeholder={t("auth.passwordPlaceholder")}
                        required
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        disabled={submitting}
                        className="bg-muted/5 border-muted-foreground/20 focus-visible:ring-primary"
                    />
                </div>
                {error ? (
                    <p role="alert" className="text-sm text-destructive">
                        {error}
                    </p>
                ) : null}
                <Button
                    type="submit"
                    className="w-full bg-foreground text-background hover:bg-foreground/90 font-semibold"
                    disabled={submitting || !auth}
                >
                    {submitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                    {submitting ? t("auth.signingIn") : t("auth.login.title")}
                </Button>
            </form>
            <div className="relative">
                <div className="absolute inset-0 flex items-center">
                    <span className="w-full border-t" />
                </div>
                <div className="relative flex justify-center text-xs uppercase">
                    <span className="bg-background/90 px-2 text-muted-foreground">{t("auth.or")}</span>
                </div>
            </div>
            <GoogleSignInButton onCredential={google} disabled={submitting} />
        </div>
    );
}
