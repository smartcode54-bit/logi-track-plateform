"use client";

import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";

export { PageLoading } from "@/features/platform/components/PageLoading";

/** A query that is still loading: a spinner and a label, never an empty table or a zero. */
export function QueryLoading({ label }: { label?: string }) {
    const { t } = useLanguage();
    return (
        <div role="status" aria-busy="true" className="flex items-center justify-center gap-2 py-8 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
            <span>{label ?? t("common.loading")}</span>
        </div>
    );
}

/**
 * A failed query with a retry, so a failed read never looks like missing data. The label names what failed; the
 * error, when given, adds the API's reason under it (or stands alone when there is no label).
 */
export function QueryError({ label, error, onRetry, retrying = false }: { label?: string; error?: unknown; onRetry: () => void; retrying?: boolean }) {
    const { t } = useLanguage();
    const reason = error === undefined ? null : apiErrorText(error, t);
    return (
        <div role="alert" className="flex flex-wrap items-center justify-between gap-3 py-4 text-sm">
            <div>
                <p className="font-medium text-destructive">{label ?? reason ?? apiErrorText(undefined, t)}</p>
                {label && reason ? <p className="text-muted-foreground">{reason}</p> : null}
            </div>
            <Button type="button" variant="outline" size="sm" className="max-md:min-h-11 max-md:min-w-11" onClick={onRetry} disabled={retrying}>
                {t("pageState.retry")}
            </Button>
        </div>
    );
}
