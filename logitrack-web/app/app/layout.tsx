"use client";

import { useAuth } from "@/context/auth";
import { usePathname } from "next/navigation";
import { Loader2 } from "lucide-react";
import { TenantSwitcher } from "@/components/tenant-switcher";
import { homeRouteOf } from "@/features/auth/api/homeRoute";
import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { SidebarProvider, SidebarTrigger, SidebarInset } from "@/components/ui/sidebar";
import { AppSidebar } from "@/components/app-sidebar";
import { SecurityCenterSidebar } from "@/components/security-center-sidebar";
import { AppBreadcrumb } from "@/components/app-breadcrumb";
import { UserMenu } from "@/components/user-menu";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { useLanguage } from "@/context/language";
import { BreadcrumbProvider } from "@/context/breadcrumb";
import { DASHBOARD_PATH } from "@/lib/routeCapabilities";

/** The id the skip link jumps to: the page area below the header (not exported: a layout exports only its component). */
const MAIN_CONTENT_ID = "main-content";

/**
 * The `/app` shell (UI3, issue #143): sidebar, a 64 px header (sidebar trigger, breadcrumb from the menu's labels,
 * tenant switcher, user menu with the scope line, language, theme and sign-out) and the page. The header holds
 * only working controls: the dead searches, the unwired report button and bell, and the "real-time" label over a
 * 60 s poll were removed.
 */
export default function AdminLayout({
    children,
}: {
    children: React.ReactNode;
}) {
    const authContext = useAuth();
    const pathname = usePathname() ?? "";
    const { t } = useLanguage();
    // `['me']` decides who is signed in: proxy.ts let this route through on a valid `lt_at`, and a
    // session that ends later is handled by goFetch (lib/sessionEnd.ts), which leaves for /login.
    const me = authContext?.me ?? null;

    const isSecurityCenter = pathname.startsWith("/app/security-center");

    // Route access is decided before any page code runs, by the proxy.ts edge gate over the Go
    // capabilities (TW3, developer-spec.md §10.5). Who is signed in comes from `['me']` (TW4), so the
    // 500 ms wait for Firebase persistence before redirecting to /login is gone.

    // A session that ends meanwhile is taken to /login by lib/sessionEnd.ts; an api that cannot be
    // reached shows a retry instead of a sign-out (T18).
    if (authContext?.error && !me) {
        return (
            <div className="min-h-screen bg-background flex flex-col items-center justify-center gap-4 p-6 text-center" role="alert">
                <p className="text-muted-foreground">{t("shell.sessionLoadFailed")}</p>
                <Button type="button" variant="outline" onClick={authContext.retry}>
                    {t("shell.retry")}
                </Button>
            </div>
        );
    }

    if (!authContext || authContext.loading) {
        return (
            <div className="min-h-screen bg-background flex flex-col items-center justify-center gap-4" role="status">
                <Loader2 className="h-8 w-8 animate-spin text-primary" aria-hidden />
                <p className="text-muted-foreground">{t("common.loading")}</p>
            </div>
        );
    }

    if (!me) {
        return null; // The session ended: goFetch is already on its way to /login.
    }

    const homeHref = homeRouteOf(me) || DASHBOARD_PATH;

    return (
        <BreadcrumbProvider>
            {/* The first stop of the keyboard: past the sidebar and the header, straight to the page. */}
            <a
                href={`#${MAIN_CONTENT_ID}`}
                className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-modal focus:rounded-md focus:border focus:border-ring focus:bg-background focus:px-4 focus:py-2 focus:text-sm focus:font-medium focus:text-foreground focus:shadow-md focus:outline-none focus:ring-[3px] focus:ring-ring/50"
            >
                {t("shell.skipToContent")}
            </a>
            <SidebarProvider defaultOpen={false}>
                {isSecurityCenter ? <SecurityCenterSidebar /> : <AppSidebar />}
                <SidebarInset>
                    {/* One height whatever the sidebar does (UI3: it jumped 64/48 px on toggle). */}
                    <header className="sticky top-0 z-sticky flex h-16 shrink-0 items-center gap-2 border-b border-border bg-background px-4">
                        <SidebarTrigger className="-ml-2 size-11 md:size-9" aria-label={t("shell.toggleSidebar")} />
                        <Separator orientation="vertical" className="mr-2 hidden data-[orientation=vertical]:h-4 md:block" />
                        <div className="flex min-w-0 flex-1 items-center">
                            <AppBreadcrumb pathname={pathname} homeHref={homeHref} />
                        </div>
                        <div className="flex shrink-0 items-center gap-2">
                            <TenantSwitcher />
                            <UserMenu />
                        </div>
                    </header>
                    <div id={MAIN_CONTENT_ID} tabIndex={-1} className="flex flex-1 flex-col gap-4 p-4 outline-none">
                        {/* The route group's lazily loaded translation namespaces (accounting, driverMonitor). */}
                        <RouteNamespaces pathname={pathname}>{children}</RouteNamespaces>
                    </div>
                </SidebarInset>
            </SidebarProvider>
        </BreadcrumbProvider>
    );
}
