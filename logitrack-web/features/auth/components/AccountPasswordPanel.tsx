"use client";

import { useEffect, useId, useState } from "react";
import Link from "next/link";
import { useForm } from "react-hook-form";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RequiredMark } from "@/components/ui/required-mark";
import { useLanguage } from "@/context/language";
import { apiErrorText, isApiError } from "@/lib/apiError";
import { changePassword } from "@/lib/authClient";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { passwordErrorText, violationFor } from "../utils/fieldErrors";

interface PasswordValues {
    currentPassword: string;
    newPassword: string;
    confirm: string;
}

const FIELDS = [
    { name: "currentPassword", label: "auth.account.password.current", autoComplete: "current-password" },
    { name: "newPassword", label: "auth.changePassword.newPassword", autoComplete: "new-password" },
    { name: "confirm", label: "auth.changePassword.confirm", autoComplete: "new-password" },
] as const;

/** Session-bound password change; only presence and confirmation are checked locally. Go owns password policy. */
export function AccountPasswordPanel() {
    const { t } = useLanguage();
    const id = useId();
    const form = useForm<PasswordValues>({ defaultValues: { currentPassword: "", newPassword: "", confirm: "" } });
    const { setFocus } = form;
    const { errors, isSubmitting } = form.formState;
    const [failure, setFailure] = useState<unknown>(null);
    const [success, setSuccess] = useState(false);
    const hasFieldFailure = isApiError(failure) && ["currentPassword", "newPassword"].some((name) => violationFor(failure, name));

    useEffect(() => {
        if (isSubmitting || !failure) return;
        const first = (["currentPassword", "newPassword"] as const).find((name) => violationFor(failure, name));
        if (first) setFocus(first);
    }, [failure, isSubmitting, setFocus]);

    const onValid = async ({ currentPassword, newPassword }: PasswordValues) => {
        setFailure(null);
        setSuccess(false);
        try {
            await changePassword(currentPassword, newPassword);
            form.reset();
            setSuccess(true);
        } catch (error) {
            setFailure(error);
            for (const name of ["currentPassword", "newPassword"] as const) {
                if (violationFor(error, name)) form.setError(name, { type: "server", message: passwordErrorText(error, name, t) });
            }
        }
    };

    let errorText = apiErrorText(failure, t);
    if (isApiError(failure) && failure.status === 429) {
        errorText = t("apiError.resource_exhausted");
        const seconds = failure.details.retryAfterSeconds;
        if (typeof seconds === "number" && Number.isFinite(seconds) && seconds >= 0) {
            errorText += ` ${t("auth.account.password.retryAfter", { seconds })}`;
        }
    }

    return (
        <section aria-labelledby={`${id}-title`} className="space-y-stack border-t border-border pt-section">
            <div className="space-y-2">
                <h2 id={`${id}-title`} className="text-base font-semibold">{t("auth.account.password.title")}</h2>
                <p className="text-sm leading-relaxed text-muted-foreground">{t("auth.account.password.description")}</p>
            </div>
            <form noValidate onSubmit={form.handleSubmit(onValid, createInvalidHandler(form, t))} className="space-y-stack" onChange={() => setSuccess(false)}>
                {FIELDS.map(({ name, label, autoComplete }) => {
                    const error = errors[name];
                    const message = error?.type === "server" ? passwordErrorText(failure, name, t) : error ? t(error.type === "required" ? "auth.passwordPolicy.required" : "auth.changePassword.mismatch") : undefined;
                    return (
                        <div key={name} className="grid gap-2">
                            <Label htmlFor={`${id}-${name}`}><span>{t(label)}<RequiredMark /></span></Label>
                            <Input
                                {...form.register(name, {
                                    required: t("auth.passwordPolicy.required"),
                                    validate: name === "confirm" ? (value) => value === form.getValues("newPassword") || t("auth.changePassword.mismatch") : undefined,
                                    deps: name === "newPassword" ? ["confirm"] : undefined,
                                })}
                                id={`${id}-${name}`}
                                type="password"
                                autoComplete={autoComplete}
                                aria-required
                                aria-invalid={Boolean(error)}
                                aria-describedby={error ? `${id}-${name}-error` : undefined}
                                disabled={isSubmitting}
                                className="max-md:h-11"
                            />
                            {error ? <p id={`${id}-${name}-error`} role={error.type === "server" ? "alert" : undefined} className="text-sm text-destructive">{message}</p> : null}
                        </div>
                    );
                })}
                {failure && !hasFieldFailure ? <p role="alert" className="text-sm text-destructive">{errorText}</p> : null}
                {success ? <p role="status" className="text-sm leading-relaxed">{t("auth.account.password.success")}</p> : null}
                <div className="flex justify-end">
                    <Button type="submit" disabled={isSubmitting} className="max-md:h-11 max-md:w-full">
                        {isSubmitting ? t("auth.account.password.saving") : t("auth.account.password.submit")}
                    </Button>
                </div>
            </form>
            <p className="text-sm leading-relaxed text-muted-foreground">
                {t("auth.account.password.googleHint")}{" "}
                <Link href="/forgot-password" className="font-medium text-primary underline underline-offset-4 max-md:inline-flex max-md:min-h-11 max-md:items-center">
                    {t("auth.account.password.forgotLink")}
                </Link>
            </p>
        </section>
    );
}
