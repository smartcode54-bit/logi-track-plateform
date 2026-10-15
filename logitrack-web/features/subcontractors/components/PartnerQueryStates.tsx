"use client";

import { Loader2, RotateCw } from "lucide-react";
import { useLanguage } from "@/context/language";
import { apiErrorText, isApiError } from "@/lib/apiError";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

export function LoadingState({ label }: { label?: string }) {
    const { t } = useLanguage();
    return (
        <div role="status" aria-busy="true" className="flex items-center justify-center gap-2 py-8 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
            <span>{label ?? t("common.loading")}</span>
        </div>
    );
}

export function QueryErrorState({ title, error, onRetry, retrying = false }: { title: string; error: unknown; onRetry: () => void; retrying?: boolean }) {
    const { t } = useLanguage();
    return (
        <Alert variant="destructive">
            <AlertTitle>{title}</AlertTitle>
            <AlertDescription className="gap-3">
                <p>{apiErrorText(error, t)}</p>
                {isApiError(error) && error.requestId && <p className="text-xs">{t("pageState.reference", { id: error.requestId })}</p>}
                <Button type="button" variant="outline" onClick={onRetry} disabled={retrying} className="min-h-11 md:min-h-9">
                    <RotateCw className={retrying ? "animate-spin" : undefined} aria-hidden />
                    {t("pageState.retry")}
                </Button>
            </AlertDescription>
        </Alert>
    );
}
