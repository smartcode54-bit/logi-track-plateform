"use client";

import { Loader2 } from "lucide-react";
import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { Button } from "@/components/ui/button";

export function QueryLoading({ label }: { label: string }) {
    return (
        <div role="status" aria-busy="true" className="flex items-center justify-center gap-2 py-8 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
            {label}
        </div>
    );
}

export function QueryError({ error, label, onRetry, retrying = false }: { error: unknown; label: string; onRetry: () => void; retrying?: boolean }) {
    const { t } = useLanguage();
    return (
        <div role="alert" className="flex flex-wrap items-center justify-between gap-3 py-4 text-sm">
            <div>
                <p className="font-medium text-destructive">{label}</p>
                <p className="text-muted-foreground">{apiErrorText(error, t)}</p>
            </div>
            <Button type="button" variant="outline" size="sm" className="max-md:min-h-11 max-md:min-w-11" onClick={onRetry} disabled={retrying}>
                {t("pageState.retry")}
            </Button>
        </div>
    );
}
