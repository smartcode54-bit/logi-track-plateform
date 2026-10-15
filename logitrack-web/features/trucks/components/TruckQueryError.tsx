"use client";

import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";

/** An independent read can fail while the rest of the page remains usable. */
export function TruckQueryError({ onRetry, retrying = false }: { onRetry: () => void; retrying?: boolean }) {
    const { t } = useLanguage();
    return (
        <div role="alert" className="flex flex-wrap items-center gap-3 p-4">
            <p className="text-sm text-destructive">{t("masterData.list.loadError")}</p>
            <Button variant="outline" onClick={onRetry} disabled={retrying}>{t("shell.retry")}</Button>
        </div>
    );
}
