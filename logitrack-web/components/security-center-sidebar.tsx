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
import { routeAllowed } from "@/lib/routeCapabilities"
import type { MeDTO } from "@/features/auth/api/me"
import { useMe } from "@/features/auth/api/useMe"
import { useScopeLabel } from "@/features/auth/hooks/useScopeLabel"
import { activeNavUrl, SECURITY_CENTER_PATH, SECURITY_NAV } from "./app-nav"

const NO_CAPABILITIES: readonly string[] = []
const selectCapabilities = (me: MeDTO | null) => me?.capabilities ?? NO_CAPABILITIES

/**
 * The Security Center sidebar. Each item (components/app-nav.ts) is shown when the edge gate would let the
 * principal open it (lib/routeCapabilities.ts over the Go capabilities of `['me']`, TW4, T18), so the menu and
 * proxy.ts can never disagree. Its header carries the same scope line as the main sidebar (UI3, issue #143);
 * sign-out lives in the user menu only.
 */
export function SecurityCenterSidebar() {
    const { t } = useLanguage()
    const pathname = usePathname()
    const scope = useScopeLabel()
    const title = t("securityCenter.title")
    const active = activeNavUrl(pathname)
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
        >
            <SidebarHeader>
                <SidebarMenu>
                    <SidebarMenuItem>
                        <SidebarMenuButton size="lg" asChild tooltip={scope ? `${title} · ${scope}` : title}>
                            <Link href={SECURITY_CENTER_PATH} prefetch={false}>
                                <div className="flex aspect-square size-8 items-center justify-center rounded-md border border-sidebar-border bg-sidebar-accent text-sidebar-accent-foreground">
                                    <ShieldCheck className="size-4" aria-hidden />
                                </div>
                                <div className="grid min-w-0 flex-1 text-left leading-tight">
                                    <span className="truncate text-sm font-semibold">{title}</span>
                                    {scope ? (
                                        <span className="truncate text-xs text-muted-foreground" data-testid="sidebar-scope">
                                            {scope}
                                        </span>
                                    ) : null}
                                </div>
                            </Link>
                        </SidebarMenuButton>
                    </SidebarMenuItem>
                </SidebarMenu>
            </SidebarHeader>
            <SidebarContent>
                <SidebarGroup>
                    <SidebarGroupLabel>{title}</SidebarGroupLabel>
                    <SidebarGroupContent>
                        <SidebarMenu>
                            {filteredItems.map((item) => (
                                <SidebarMenuItem key={item.url}>
                                    <SidebarMenuButton asChild tooltip={t(item.labelKey)} isActive={active === item.url}>
                                        <Link href={item.url} prefetch={false}>
                                            {item.icon ? <item.icon /> : null}
                                            <span>{t(item.labelKey)}</span>
                                        </Link>
                                    </SidebarMenuButton>
                                </SidebarMenuItem>
                            ))}
                        </SidebarMenu>
                    </SidebarGroupContent>
                </SidebarGroup>
            </SidebarContent>
            <SidebarFooter>
                <SidebarMenu>
                    <SidebarMenuItem>
                        <SidebarMenuButton asChild tooltip={t("securityCenter.backToAdmin")}>
                            <Link href="/app/dashboard" prefetch={false}>
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
