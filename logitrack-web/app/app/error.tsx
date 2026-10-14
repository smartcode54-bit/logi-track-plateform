"use client";

/**
 * A page that crashed while rendering (UI3D, issue #143): the error boundary of every `/app` page, inside the shell, so
 * the sidebar, the header and sign-out stay. "Try again" re-renders the page (`reset`); "Go to my home page" leaves it.
 * The quiet reference is what support needs to find the failure: Next's digest of a server error, else the request id
 * of a failed Go call. The message itself is never shown (it is for developers, and may name internals).
 *
 * The shell itself (app/app/layout.tsx) is outside this boundary: a crash there reaches app/global-error.tsx.
 */
import { useEffect } from "react";
import Link from "next/link";
import { Home, RotateCw } from "lucide-react";

import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { homeRouteOf } from "@/features/auth/api/homeRoute";
import { errorReference } from "@/lib/errorReference";
import { DASHBOARD_PATH } from "@/lib/routeCapabilities";

export default function AppError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
    const { t } = useLanguage();
    const me = useAuth()?.me;
    const home = me ? homeRouteOf(me) : DASHBOARD_PATH;
    const reference = errorReference(error);

    useEffect(() => {
        // The browser console is where a developer reproducing the report looks first.
        console.error(error);
    }, [error]);

    return (
        <div data-testid="page-error" className="flex flex-col gap-section">
            <PageHeader title={t("pageState.error.title")} description={<span role="alert">{t("pageState.error.body")}</span>} />
            <div className="flex flex-wrap gap-2">
                <Button type="button" onClick={reset}>
                    <RotateCw aria-hidden />
                    {t("pageState.retry")}
                </Button>
                <Button asChild variant="outline">
                    <Link href={home} prefetch={false}>
                        <Home aria-hidden />
                        {t("pageState.goHome")}
                    </Link>
                </Button>
            </div>
            {reference ? <p className="font-mono text-xs text-muted-foreground">{t("pageState.reference", { id: reference })}</p> : null}
        </div>
    );
}
