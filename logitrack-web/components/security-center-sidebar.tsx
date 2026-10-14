"use client"

import { LayoutDashboard, ShieldCheck } from "lucide-react"

import {
    Sidebar,
    SidebarContent,
    SidebarFooter,
    SidebarGroup,
    SidebarGroupContent,
    SidebarGroupLabel,
    SidebarHeader,
    SidebarMenu,
    SidebarMenuButton,
    SidebarMenuItem,
} from "@/components/ui/sidebar"
import { useLanguage } from "@/context/language"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { useMemo } from "react"
import { DASHBOARD_PATH, routeAllowed } from "@/lib/routeCapabilities"
import type { MeDTO } from "@/features/auth/api/me"
import { homeRouteOf } from "@/features/auth/api/homeRoute"
import { useMe } from "@/features/auth/api/useMe"
import { useScopeLabel } from "@/features/auth/hooks/useScopeLabel"
import { activeNavUrl, SECURITY_CENTER_PATH, SECURITY_NAV } from "./app-nav"
import { ScopeLine } from "./scope-line"
import { sidebarHeaderTooltip, useCloseSidebarOnPhone, useRevealCurrentEntry } from "./sidebar-shell"

const NO_CAPABILITIES: readonly string[] = []
const selectCapabilities = (me: MeDTO | null) => me?.capabilities ?? NO_CAPABILITIES
/** "Back to the main menu" goes to the principal's home, as the edge gate and the breadcrumb's Home pick it (R89). */
const selectHome = (me: MeDTO | null) => (me ? homeRouteOf(me) : DASHBOARD_PATH)

/**
 * The Security Center sidebar. Each item (components/app-nav.ts) is shown when the edge gate would let the
 * principal open it (lib/routeCapabilities.ts over the Go capabilities of `['me']`, TW4, T18), so the menu and
 * proxy.ts can never disagree. Its header carries the same scope line as the main sidebar (UI3, issue #143);
 * sign-out lives in the user menu only. As in the main sidebar (UI3B), the menu is a labelled `<nav>`, and the
 * current page's entry is Route Blue, `aria-current="page"` and scrolled into view when the sidebar opens.
 */
export function SecurityCenterSidebar() {
    const { t } = useLanguage()
    const pathname = usePathname()
    const scope = useScopeLabel()
    const { data: homeHref = DASHBOARD_PATH } = useMe(selectHome)
    const closeOnPhone = useCloseSidebarOnPhone()
    const title = t("securityCenter.title")
    const active = activeNavUrl(pathname)
    const navRef = useRevealCurrentEntry<HTMLElement>(active)
    const { data: capabilities = NO_CAPABILITIES } = useMe(selectCapabilities)
    const filteredItems = useMemo(() => {
        const held = new Set(capabilities)
        return SECURITY_NAV.filter((item) => routeAllowed(held, item.url))
    }, [capabilities])

    return (
        <Sidebar
            collapsible="icon"
            className="border-r-0"
            sheetTitle={t("shell.mobileNav.title")}
            sheetDescription={t("shell.mobileNav.description")}
            sheetCloseLabel={t("shell.closeMenu")}
        >
            <SidebarHeader>
                <SidebarMenu>
                    <SidebarMenuItem>
                        <SidebarMenuButton size="lg" asChild tooltip={sidebarHeaderTooltip(title, scope)} className="h-auto min-h-12">
                            <Link href={SECURITY_CENTER_PATH} prefetch={false} onClick={closeOnPhone}>
                                <div className="flex aspect-square size-8 shrink-0 items-center justify-center rounded-md border border-sidebar-border bg-sidebar-accent text-sidebar-accent-foreground">
                                    <ShieldCheck className="size-4" aria-hidden />
                                </div>
                                <div className="grid min-w-0 flex-1 text-left">
                                    <span className="truncate text-sm leading-normal font-semibold">{title}</span>
                                    <ScopeLine className="text-xs text-muted-foreground" testId="sidebar-scope" />
                                </div>
                            </Link>
                        </SidebarMenuButton>
                    </SidebarMenuItem>
                </SidebarMenu>
            </SidebarHeader>
            <SidebarContent asChild>
                <nav aria-label={t("shell.nav.security")} ref={navRef}>
                    <SidebarGroup>
                        <SidebarGroupLabel>{title}</SidebarGroupLabel>
                        <SidebarGroupContent>
                            <SidebarMenu>
                                {filteredItems.map((item) => (
                                    <SidebarMenuItem key={item.url}>
                                        <SidebarMenuButton asChild tooltip={t(item.labelKey)} isActive={active === item.url}>
                                            <Link href={item.url} prefetch={false} onClick={closeOnPhone}>
                                                {item.icon ? <item.icon /> : null}
                                                <span>{t(item.labelKey)}</span>
                                            </Link>
                                        </SidebarMenuButton>
                                    </SidebarMenuItem>
                                ))}
                            </SidebarMenu>
                        </SidebarGroupContent>
                    </SidebarGroup>
                </nav>
            </SidebarContent>
            <SidebarFooter>
                <SidebarMenu>
                    <SidebarMenuItem>
                        <SidebarMenuButton asChild tooltip={t("securityCenter.backToAdmin")}>
                            <Link href={homeHref} prefetch={false} onClick={closeOnPhone}>
                                <LayoutDashboard />
                                <span>{t("securityCenter.backToAdmin")}</span>
                            </Link>
                        </SidebarMenuButton>
                    </SidebarMenuItem>
                </SidebarMenu>
            </SidebarFooter>
        </Sidebar>
    )
}
