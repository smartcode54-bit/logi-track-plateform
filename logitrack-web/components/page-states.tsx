"use client";

import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";

export { PageLoading } from "@/features/platform/components/PageLoading";

export function QueryLoading({ label }: { label?: string }) {
    const { t } = useLanguage();
    return (
        <div role="status" aria-busy="true" className="flex items-center justify-center gap-2 p-6 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
            <span>{label ?? t("common.loading")}</span>
        </div>
    );
}

export function QueryError({ label, error, onRetry, retrying = false }: { label?: string; error?: unknown; onRetry: () => void; retrying?: boolean }) {
    const { t } = useLanguage();
    return (
        <div className="flex flex-wrap items-center justify-center gap-3 p-6">
            <p role="alert" className="text-sm text-destructive">{label ?? apiErrorText(error, t)}</p>
            <Button type="button" variant="outline" className="max-md:min-h-11 max-md:min-w-11" onClick={onRetry} disabled={retrying}>{t("pageState.retry")}</Button>
        </div>
    );
}
