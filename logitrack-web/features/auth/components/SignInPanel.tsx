"use client";

/**
 * The sign-in form of the login page and of the landing page's login dialog (T18; developer-spec.md
 * §10.4): email and password through `POST /api/auth/login`, Google through the GIS button and
 * `POST /api/auth/google`, and the must-change-password step (R79) when either answers
 * `403 password_change_required`. Errors are rendered from the en/th catalogues by code (R76).
 *
 * UI3E (DESIGN.md form style): both fields are required, with the Signal Red star on their labels; a missing or
 * malformed value shows its error under its field (`aria-invalid`, `aria-describedby` through `FormControl`), and the
 * shared `onInvalid` handler (ADR 0004) focuses the first invalid field with a summary toast. Nothing is posted until
 * both pass. On a phone the fields, the button and the links are 44 px targets. The fields' ids come from
 * `FormItem` (`useId`), so the panel needs no id prefix of its own; the password-change step keeps "login-chg".
 */
import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Loader2 } from "lucide-react";
import { useForm } from "react-hook-form";

import { Button } from "@/components/ui/button";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { RequiredMark } from "@/components/ui/required-mark";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { passwordChangeTicket } from "@/lib/authClient";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { ChangePasswordForm } from "./ChangePasswordForm";
import { GoogleSignInButton, googleClientId } from "./GoogleSignInButton";

interface SignInValues {
    email: string;
    password: string;
}

/** Something@something: the shape only (Go decides whether the account exists), so no address is turned away here. */
const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+$/;

export interface SignInPanelProps {
    /** The session cookies are set and `['me']` holds the principal. */
    onSignedIn?: () => void;
    /** An informational line above the form (e.g. why the user was signed out). */
    notice?: string;
}

interface ChangeStep {
    ticket: string;
    /** Set when the sign-in was by password: the form signs in again with it after the change. */
    email?: string;
}

export function SignInPanel({ onSignedIn, notice }: SignInPanelProps) {
    const auth = useAuth();
    const { t, language } = useLanguage();
    const form = useForm<SignInValues>({ defaultValues: { email: "", password: "" }, mode: "onSubmit", reValidateMode: "onChange" });
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

    // A shown field error follows a language switch: its text is translated when the rule runs, so the rules run again.
    const hasErrors = Object.keys(form.formState.errors).length > 0;
    const shownLanguage = useRef(language);
    useEffect(() => {
        if (shownLanguage.current === language) return;
        shownLanguage.current = language;
        if (hasErrors) void form.trigger();
    }, [language, hasErrors, form]);

    const submit = async ({ email, password }: SignInValues) => {
        if (!auth) return;
        setError("");
        setSubmitting(true);
        try {
            await auth.login(email.trim(), password);
            form.resetField("password");
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
            form.resetField("password");
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
                idPrefix="login-chg"
                onChanged={changed}
                onCancel={(messageKey) => {
                    setChange(null);
                    form.resetField("password");
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
            <Form {...form}>
                <form
                    onSubmit={form.handleSubmit(submit, createInvalidHandler(form, t))}
                    noValidate
                    className="space-y-4"
                >
                    <FormField
                        control={form.control}
                        name="email"
                        rules={{
                            validate: (value) => {
                                const email = value.trim();
                                if (!email) return t("auth.emailRequired");
                                return EMAIL_SHAPE.test(email) || t("auth.emailInvalid");
                            },
                        }}
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>
                                    <span>
                                        {t("auth.email")}
                                        <RequiredMark />
                                    </span>
                                </FormLabel>
                                <FormControl>
                                    <Input
                                        {...field}
                                        type="email"
                                        autoComplete="username"
                                        placeholder={t("auth.emailPlaceholder")}
                                        aria-required
                                        disabled={submitting}
                                        className="max-md:h-11"
                                    />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={form.control}
                        name="password"
                        rules={{ validate: (value) => value !== "" || t("auth.login.passwordRequired") }}
                        render={({ field }) => (
                            <FormItem>
                                <div className="flex items-center justify-between gap-2">
                                    <FormLabel>
                                        <span>
                                            {t("auth.password")}
                                            <RequiredMark />
                                        </span>
                                    </FormLabel>
                                    <Link
                                        href="/forgot-password"
                                        className="text-sm font-medium text-muted-foreground transition-colors hover:text-primary max-md:inline-flex max-md:min-h-11 max-md:items-center"
                                    >
                                        {t("auth.forgotPassword")}
                                    </Link>
                                </div>
                                <FormControl>
                                    <Input
                                        {...field}
                                        type="password"
                                        autoComplete="current-password"
                                        placeholder={t("auth.passwordPlaceholder")}
                                        aria-required
                                        disabled={submitting}
                                        className="max-md:h-11"
                                    />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    {error ? (
                        <p role="alert" className="text-sm text-destructive">
                            {error}
                        </p>
                    ) : null}
                    <Button type="submit" className="w-full max-md:h-11" disabled={submitting || !auth} data-testid="sign-in-submit">
                        {submitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden /> : null}
                        {submitting ? t("auth.signingIn") : t("auth.login.title")}
                    </Button>
                </form>
            </Form>
            {/* Google only where it is configured: GoogleSignInButton renders nothing without a client id, and an
                "or" with nothing after it would offer a second way in that is not there. */}
            {googleClientId() ? (
                <>
                    {/* Two hairlines around the word, so the divider needs no backing colour on a card or in a dialog. */}
                    <div className="flex items-center gap-3 text-xs text-muted-foreground" data-testid="sign-in-or">
                        <span className="h-px flex-1 bg-border" aria-hidden />
                        <span>{t("auth.or")}</span>
                        <span className="h-px flex-1 bg-border" aria-hidden />
                    </div>
                    <GoogleSignInButton onCredential={google} disabled={submitting} />
                </>
            ) : null}
        </div>
    );
}
