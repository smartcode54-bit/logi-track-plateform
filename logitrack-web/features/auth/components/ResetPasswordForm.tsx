"use client";

/**
 * `/reset-password#token=...` (T18; Appendix C §C.4.9, R4, R29): the link of the forgot-password and
 * invite emails. The token is in the fragment, so it never reaches a server log or a `Referer` (the page
 * also sends `Referrer-Policy: no-referrer`, next.config.ts); it is read once and removed from the
 * address bar. `POST /api/go/v1/auth/password/reset {token, newPassword}` -> 204 revokes every session
 * of the user, who then signs in with the new password.
 */
import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { ArrowLeftIcon, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useLanguage } from "@/context/language";
import { resetPassword } from "@/lib/authClient";
import { passwordErrorText, violationFor } from "../utils/fieldErrors";
import { AuthCardTitle } from "./AuthCardTitle";

/** The `token` of a `#token=...` fragment, or "". */
export function tokenFromHash(hash: string): string {
    const params = new URLSearchParams(hash.startsWith("#") ? hash.slice(1) : hash);
    return params.get("token")?.trim() ?? "";
}

type State = "reading" | "ready" | "missing" | "done";

export default function ResetPasswordForm() {
    const { t } = useLanguage();
    const [token, setToken] = useState("");
    const [state, setState] = useState<State>("reading");
    const [password, setPassword] = useState("");
    const [confirm, setConfirm] = useState("");
    const [submitting, setSubmitting] = useState(false);
    const [error, setError] = useState("");

    // The token already read: React StrictMode (`next dev`) runs this effect twice on mount, and the
    // second run finds the fragment already removed. A lazy `useState` initialiser is no option: the
    // page is prerendered, so reading `window` during render would break hydration.
    const tokenRef = useRef("");
    useEffect(() => {
        const found = tokenFromHash(window.location.hash);
        if (!found && tokenRef.current) return;
        tokenRef.current = found;
        if (found) {
            // Keep the token out of the history entry and of anything that reads the URL later.
            window.history.replaceState(null, "", window.location.pathname + window.location.search);
        }
        setToken(found);
        setState(found ? "ready" : "missing");
    }, []);

    const submit = async (e: React.FormEvent) => {
        e.preventDefault();
        setError("");
        if (password !== confirm) {
            setError(t("auth.changePassword.mismatch"));
            return;
        }
        setSubmitting(true);
        try {
            await resetPassword(token, password);
            setState("done");
        } catch (err) {
            if (violationFor(err, "token")) setState("missing");
            else setError(passwordErrorText(err, "newPassword", t));
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <Card className="w-full max-w-md">
            <CardHeader className="gap-1">
                <AuthCardTitle>{t("auth.reset.title")}</AuthCardTitle>
                <CardDescription>{t("auth.reset.subtitle")}</CardDescription>
            </CardHeader>
            <CardContent>
                {state === "reading" ? (
                    <div className="flex justify-center py-6">
                        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
                    </div>
                ) : state === "missing" ? (
                    <p role="alert" className="text-sm text-destructive">
                        {t("auth.reset.invalidLink")}{" "}
                        <Link href="/forgot-password" className="underline max-md:inline-flex max-md:min-h-11 max-md:items-center">
                            {t("auth.reset.requestNew")}
                        </Link>
                    </p>
                ) : state === "done" ? (
                    <p role="status" className="text-sm text-success">
                        {t("auth.reset.done")}
                    </p>
                ) : (
                    <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
                        <div className="grid gap-2">
                            <Label htmlFor="reset-new">{t("auth.changePassword.newPassword")}</Label>
                            <Input id="reset-new" type="password" autoComplete="new-password" required value={password} onChange={(e) => setPassword(e.target.value)} disabled={submitting} className="max-md:h-11" />
                        </div>
                        <div className="grid gap-2">
                            <Label htmlFor="reset-confirm">{t("auth.changePassword.confirm")}</Label>
                            <Input id="reset-confirm" type="password" autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} disabled={submitting} className="max-md:h-11" />
                        </div>
                        {error ? (
                            <p role="alert" className="text-sm text-destructive">
                                {error}
                            </p>
                        ) : null}
                        <Button type="submit" className="w-full max-md:h-11" disabled={submitting || !password || !confirm}>
                            {submitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden /> : null}
                            {t("auth.reset.submit")}
                        </Button>
                    </form>
                )}
            </CardContent>
            <CardFooter className="justify-center">
                <Button variant="link" asChild className="max-md:h-11">
                    <Link href="/login">
                        <ArrowLeftIcon aria-hidden /> {t("auth.backToLogin")}
                    </Link>
                </Button>
            </CardFooter>
        </Card>
    );
}
