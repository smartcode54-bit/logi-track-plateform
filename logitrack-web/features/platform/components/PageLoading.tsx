"use client";

import { Loader2 } from "lucide-react";

import { useLanguage } from "@/context/language";

/**
 * The page-area spinner while a route's page chunk loads (the shell stays). Each `app/app/**` page
 * loads its Go page through `next/dynamic` with this as the placeholder (developer-spec.md §10.11,
 * §10.13). Until TW7 it also covered the web flags request of the per-page Firestore/Go switch,
 * removed with the Firestore pages. The status carries words for screen readers (UI3), not only a spinner.
 */
export function PageLoading() {
    const { t } = useLanguage();
    return (
        <div className="flex min-h-[50vh] items-center justify-center" role="status" aria-busy="true">
            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" aria-hidden />
            <span className="sr-only">{t("common.loading")}</span>
        </div>
    );
}
