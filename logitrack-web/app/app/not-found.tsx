"use client";

/**
 * "Page not found" inside the shell (UI3D, issue #143): the sidebar, the header and the breadcrumb stay, so the way on
 * is in reach. Rendered for `notFound()` under `/app`: the catch-all `app/app/[...missing]/page.tsx` answers every
 * path no page matches; the edge gate lets an unmapped path through to it with HTTP 404 instead of calling it
 * forbidden (lib/bff/edgeGate.ts). Crumb and tab title: "Page not found" / "ไม่พบหน้านี้".
 */
import Link from "next/link";
import { Home } from "lucide-react";

import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { homeRouteOf } from "@/features/auth/api/homeRoute";
import { DASHBOARD_PATH } from "@/lib/routeCapabilities";

export default function AppNotFound() {
    const { t } = useLanguage();
    const me = useAuth()?.me;
    const home = me ? homeRouteOf(me) : DASHBOARD_PATH;
    const title = t("pageState.notFound.title");

    return (
        <div data-testid="page-not-found" className="flex flex-col gap-section">
            <PageHeader title={title} recordName={title} description={t("pageState.notFound.body")} />
            <div className="flex flex-wrap gap-2">
                <Button asChild>
                    <Link href={home} prefetch={false}>
                        <Home aria-hidden />
                        {t("pageState.goHome")}
                    </Link>
                </Button>
            </div>
        </div>
    );
}
