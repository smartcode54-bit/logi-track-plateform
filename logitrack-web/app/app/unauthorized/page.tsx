"use client";

/**
 * The 403 page of the console (UI3D, issue #143): a page that exists (it is mapped in the gate's table) but that the
 * principal's role does not open. An address with no page is "Page not found" instead (app/app/not-found.tsx, the edge
 * gate tells the two apart). It names the page as the menu does (`breadcrumbTrail(from)`), never the raw path, and
 * says who can change that, by kind of principal (features/auth/utils/unauthorizedGuide.ts).
 *
 * Quiet by design (DESIGN.md: no icon tile above the heading, red only for state): ink heading, graphite text.
 */
import { Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { ArrowLeft, Home } from "lucide-react";

import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { homeRouteOf } from "@/features/auth/api/homeRoute";
import { useScopeLabel } from "@/features/auth/hooks/useScopeLabel";
import { attemptedPageName, unauthorizedAudience } from "@/features/auth/utils/unauthorizedGuide";
import { DASHBOARD_PATH } from "@/lib/routeCapabilities";

function UnauthorizedContent() {
    const searchParams = useSearchParams();
    const router = useRouter();
    const { t } = useLanguage();
    const me = useAuth()?.me ?? null;
    // The scope line of the shell (UI3): the same label as the sidebar and the user menu.
    const scope = useScopeLabel();
    // The same home as the proxy.ts gate (R89), over `['me']`.
    const homeRoute = me ? homeRouteOf(me) : DASHBOARD_PATH;
    const page = attemptedPageName(searchParams.get("from") ?? "", t, homeRoute);
    const audience = unauthorizedAudience(me);

    return (
        <div data-testid="unauthorized" className="flex max-w-2xl flex-col gap-section">
            <PageHeader
                title={t("unauthorized.title")}
                description={
                    <div className="flex flex-col gap-2">
                        <p>{page ? t("unauthorized.page", { page }) : t("unauthorized.description")}</p>
                        <p data-testid="unauthorized-guide">{t(`unauthorized.guide.${audience}`)}</p>
                    </div>
                }
            />
            <div className="flex flex-wrap gap-2">
                <Button type="button" variant="outline" onClick={() => router.back()}>
                    <ArrowLeft aria-hidden />
                    {t("unauthorized.goBack")}
                </Button>
                <Button type="button" onClick={() => router.replace(homeRoute)}>
                    <Home aria-hidden />
                    {t("pageState.goHome")}
                </Button>
            </div>
            {scope ? <p className="text-xs text-muted-foreground">{t("unauthorized.roleHint", { role: scope })}</p> : null}
        </div>
    );
}

export default function UnauthorizedPage() {
    return (
        <Suspense fallback={null}>
            <UnauthorizedContent />
        </Suspense>
    );
}
