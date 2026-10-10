"use client";

/**
 * `/reset-password#token=...` (T18; Appendix C §C.4.9, R4, R29): the link of the forgot-password and
 * invite emails. The token is in the fragment, so it never reaches a server log or a `Referer` (the page
 * also sends `Referrer-Policy: no-referrer`, next.config.ts); it is read once and removed from the
 * address bar. `POST /api/go/v1/auth/password/reset {token, newPassword}` -> 204 revokes every session
 * of the user, who then signs in with the new password.
 */
import { useEffect, useState } from "react";
import Link from "next/link";
import { ArrowLeftIcon, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useLanguage } from "@/context/language";
import { resetPassword } from "@/lib/authClient";
import { passwordErrorText, violationFor } from "../utils/fieldErrors";

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

    useEffect(() => {
        const found = tokenFromHash(window.location.hash);
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
            <CardHeader>
                <CardTitle>{t("auth.reset.title")}</CardTitle>
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
                        <Link href="/forgot-password" className="underline">
                            {t("auth.reset.requestNew")}
                        </Link>
                    </p>
                ) : state === "done" ? (
                    <p role="status" className="text-sm text-green-600">
                        {t("auth.reset.done")}
                    </p>
                ) : (
                    <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
                        <div className="grid gap-2">
                            <Label htmlFor="reset-new">{t("auth.changePassword.newPassword")}</Label>
                            <Input id="reset-new" type="password" autoComplete="new-password" required value={password} onChange={(e) => setPassword(e.target.value)} disabled={submitting} />
                        </div>
                        <div className="grid gap-2">
                            <Label htmlFor="reset-confirm">{t("auth.changePassword.confirm")}</Label>
                            <Input id="reset-confirm" type="password" autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} disabled={submitting} />
                        </div>
                        {error ? (
                            <p role="alert" className="text-sm text-destructive">
                                {error}
                            </p>
                        ) : null}
                        <Button type="submit" className="w-full" disabled={submitting || !password || !confirm}>
                            {submitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                            {t("auth.reset.submit")}
                        </Button>
                    </form>
                )}
            </CardContent>
            <CardAction>
                <div className="flex justify-center">
                    <Button variant="link" asChild>
                        <Link className="text-sm hover:underline flex items-center gap-1" href="/login">
                            <ArrowLeftIcon className="w-4 h-4" /> {t("auth.backToLogin")}
                        </Link>
                    </Button>
                </div>
            </CardAction>
        </Card>
    );
}
