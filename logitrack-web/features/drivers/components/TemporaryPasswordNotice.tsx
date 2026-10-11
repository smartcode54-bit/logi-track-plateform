"use client";

import { useState } from "react";
import { Check, Copy, KeyRound } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";

interface TemporaryPasswordNoticeProps {
    email: string;
    /** Shown once (R29): Go never returns it again; absent when an invite was mailed instead. */
    temporaryPassword?: string;
}

/**
 * The result of a driver login created through `POST /v1/users` (T25): the generated temporary
 * password, shown once with a copy button (the driver must change it at the first sign-in), or the
 * note that an invite was mailed.
 */
export function TemporaryPasswordNotice({ email, temporaryPassword }: TemporaryPasswordNoticeProps) {
    const { t } = useLanguage();
    const [copied, setCopied] = useState(false);
    const copy = async () => {
        if (!temporaryPassword) return;
        try {
            await navigator.clipboard.writeText(temporaryPassword);
            setCopied(true);
        } catch {
            setCopied(false);
        }
    };
    return (
        <div className="space-y-2 p-3 rounded-lg border border-amber-200 bg-amber-50 dark:border-amber-800 dark:bg-amber-900/20">
            <p className="text-sm font-medium flex items-center gap-2">
                <KeyRound className="h-4 w-4 text-amber-600" />
                {t("masterData.drivers.login.created", { email })}
            </p>
            {temporaryPassword ? (
                <>
                    <div className="flex items-center gap-2">
                        <code className="rounded bg-background px-2 py-1 font-mono text-sm select-all">{temporaryPassword}</code>
                        <Button type="button" size="sm" variant="outline" onClick={copy} aria-label={t("masterData.drivers.login.copy")}>
                            {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                        </Button>
                    </div>
                    <p className="text-xs text-muted-foreground">{t("masterData.drivers.login.temporaryHint")}</p>
                </>
            ) : (
                <p className="text-xs text-muted-foreground">{t("masterData.drivers.login.inviteSent")}</p>
            )}
        </div>
    );
}
