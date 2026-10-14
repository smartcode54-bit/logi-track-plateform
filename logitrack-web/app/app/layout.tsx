"use client";

import { useAuth } from "@/context/auth";
import { usePathname } from "next/navigation";
import { BootLoading } from "@/components/boot-loading";
import { TenantSwitcher } from "@/components/tenant-switcher";
import { homeRouteOf } from "@/features/auth/api/homeRoute";
import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { SidebarProvider, SidebarTrigger, SidebarInset } from "@/components/ui/sidebar";
import { AppSidebar } from "@/components/app-sidebar";
import { SecurityCenterSidebar } from "@/components/security-center-sidebar";
import { AppBreadcrumb } from "@/components/app-breadcrumb";
import { ScopeLine } from "@/components/scope-line";
import { initialSidebarOpen, sidebarShortcutKeys } from "@/components/sidebar-shell";
import { UserMenu } from "@/components/user-menu";
import { QuickJump } from "@/components/quick-jump";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { useLanguage } from "@/context/language";
import { BreadcrumbProvider } from "@/context/breadcrumb";
import { usePrincipalScope } from "@/features/auth/hooks/useScopeLabel";
import { DASHBOARD_PATH } from "@/lib/routeCapabilities";

/** The id the skip link jumps to: the page area below the header (not exported: a layout exports only its component). */
const MAIN_CONTENT_ID = "main-content";

/**
 * The `/app` shell (UI3, issue #143): sidebar, a 64 px header (sidebar trigger, breadcrumb from the menu's labels,
 * tenant switcher, user menu with the scope line, language, theme and sign-out) and the page. The header holds
 * only working controls: the dead searches, the unwired report button and bell, and the "real-time" label over a
 * 60 s poll were removed.
 *
 * UI3B: the sidebar keeps the user's choice (open or icons) and starts open from `lg` when there is none; the
 * header is the page's banner landmark beside `<main>` (not inside it); on a phone the header names the slice of
 * the data (the scope line) where the breadcrumb would repeat the page's heading.
 */
export default function AdminLayout({
    children,
}: {
    children: React.ReactNode;
}) {
    const authContext = useAuth();
    const pathname = usePathname() ?? "";
    const { t } = useLanguage();
    const scope = usePrincipalScope();
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
        // The same screen and words as the language load before it (components/boot-loading.tsx).
        return <BootLoading />;
    }

    if (!me) {
        return null; // The session ended: goFetch is already on its way to /login.
    }

    const homeHref = homeRouteOf(me) || DASHBOARD_PATH;
    // On a phone the slice replaces the crumb, which there only repeats the page's heading; without a slice or a
    // role to show, the crumb stays.
    const phoneScope = Boolean(scope && (scope.slice || scope.role));

    return (
        <BreadcrumbProvider>
            {/* The first stop of the keyboard: past the sidebar and the header, straight to the page. */}
            <a
                href={`#${MAIN_CONTENT_ID}`}
                className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-modal focus:rounded-md focus:border focus:border-ring focus:bg-background focus:px-4 focus:py-2 focus:text-sm focus:font-medium focus:text-foreground focus:shadow-md focus:outline-none focus:ring-[3px] focus:ring-ring/50"
            >
                {t("shell.skipToContent")}
            </a>
            {/* The user's choice is kept (the `sidebar_state` cookie); without one the sidebar is open from lg. */}
            <SidebarProvider defaultOpen={initialSidebarOpen} layout="grid">
                {/* The page's banner (outside <main>). One height whatever the sidebar does (UI3: it jumped
                    64/48 px on toggle). It spans both grid rows but sits at the top (self-start): a sticky grid item
                    only sticks within its grid area, and a 64 px row alone would let it scroll away. */}
                <header className="sticky top-0 z-sticky col-start-1 row-span-2 row-start-1 flex h-16 self-start min-w-0 items-center gap-2 border-b border-border bg-background px-gutter md:col-start-2">
                    <SidebarTrigger
                        className="-ml-2 size-11 md:size-9"
                        label={t("shell.toggleSidebar")}
                        tooltip={t("shell.toggleSidebarHint", { keys: sidebarShortcutKeys() })}
                    />
                    <QuickJump />
                    <Separator orientation="vertical" className="mr-2 hidden data-[orientation=vertical]:h-4 md:block" />
                    <div className="flex min-w-0 flex-1 items-center">
                        <AppBreadcrumb pathname={pathname} homeHref={homeHref} className={phoneScope ? "hidden md:block" : undefined} />
                        {phoneScope ? <ScopeLine compact className="max-w-full text-sm text-muted-foreground md:hidden" testId="header-scope" /> : null}
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                        <TenantSwitcher />
                        <UserMenu />
                    </div>
                </header>
                {isSecurityCenter ? <SecurityCenterSidebar /> : <AppSidebar />}
                {/* min-w-0 keeps a wide page inside its grid slot without pushing the header controls out. */}
                <SidebarInset>
                    {/* scroll-mt-16: the skip link lands below the 64 px sticky header, not under it. The shell owns the
                        page's 16 px gutter (UI3D, DESIGN.md `gutter`): pages never pad themselves, so every page starts at
                        the same left edge and keeps the full width at 360 px. */}
                    <main id={MAIN_CONTENT_ID} tabIndex={-1} className="flex min-w-0 flex-1 scroll-mt-16 flex-col gap-section p-gutter outline-none">
                        {/* The route group's lazily loaded translation namespaces (accounting, driverMonitor). */}
                        <RouteNamespaces pathname={pathname}>{children}</RouteNamespaces>
                    </main>
                </SidebarInset>
            </SidebarProvider>
        </BreadcrumbProvider>
    );
}
