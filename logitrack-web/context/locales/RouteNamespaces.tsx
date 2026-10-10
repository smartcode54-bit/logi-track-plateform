"use client";

import type { ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { useLanguage, useLocaleNamespaces } from "@/context/language";
import { namespacesForPath } from "./routes";

/**
 * Renders a route only once the split translation namespaces of its group (`./routes.ts`) are in
 * the active language (developer-spec.md §10.11 step 2, TW4). The `/app` layout wraps its page area
 * with it, so the shell stays on screen and only the first visit of a group in a language waits for
 * one small chunk; routes outside every group render at once.
 */
export function RouteNamespaces({ pathname, children }: { pathname: string | null; children: ReactNode }) {
    const { t } = useLanguage();
    const status = useLocaleNamespaces(namespacesForPath(pathname));
    if (status === "ready") return <>{children}</>;
    if (status === "failed") {
        return (
            <div className="flex h-[50vh] flex-col items-center justify-center gap-4 text-center" role="alert">
                <p className="text-muted-foreground">{t("shell.namespaceLoadFailed")}</p>
                <button
                    type="button"
                    className="rounded-md border px-4 py-2 text-sm hover:bg-accent"
                    onClick={() => window.location.reload()}
                >
                    {t("shell.reload")}
                </button>
            </div>
        );
    }
    return (
        <div className="flex h-[50vh] items-center justify-center" aria-busy="true">
            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
        </div>
    );
}
