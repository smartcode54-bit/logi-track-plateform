"use client"

import { useMemo } from "react"
import { ChevronDown, HelpCircle } from "lucide-react"

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
    SidebarMenuSub,
    SidebarMenuSubButton,
    SidebarMenuSubItem,
} from "@/components/ui/sidebar"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { useLanguage } from "@/context/language"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { routeAllowed } from "@/lib/routeCapabilities"
import type { MeDTO } from "@/features/auth/api/me"
import { useMe } from "@/features/auth/api/useMe"
import { useScopeLabel } from "@/features/auth/hooks/useScopeLabel"
import type { BadgesDTO } from "@/features/dashboard/api/badges"
import { useBadges } from "@/features/dashboard/api/useBadges"
import { WEB_APP_VERSION } from "@/lib/app-version"
import { activeNavUrl, isNavGroup, MAIN_NAV, SYSTEM_NAV, type NavEntry, type NavLink } from "./app-nav"

const NO_CAPABILITIES: readonly string[] = []
const selectCapabilities = (me: MeDTO | null) => me?.capabilities ?? NO_CAPABILITIES
const selectWaitlistCount = (badges: BadgesDTO) => badges.waitlist ?? 0

const WAITLIST_PATH = "/app/waitlist"

/**
 * The main sidebar. Its entries (components/app-nav.ts) are the routes the `proxy.ts` gate would let this
 * principal open: the same table (lib/routeCapabilities.ts) over the same capabilities (`['me']`, TW4), so a link
 * is never shown for a page that would answer `/app/unauthorized`. The waitlist count is `['badges']` (polled, one
 * count query in P0), not a listener on the whole collection.
 *
 * The header names the slice of the data (UI3, issue #143): the scope line ("WRT · Tenant admin"), and in the
 * collapsed rail the same line as the logo's tooltip. Sign-out lives in the user menu only.
 */
export function AppSidebar() {
    const { t } = useLanguage()
    const pathname = usePathname()
    const { data: capabilities = NO_CAPABILITIES } = useMe(selectCapabilities)
    const held = useMemo(() => new Set(capabilities), [capabilities])
    const canOpen = (url: string) => routeAllowed(held, url)
    const { data: waitlistCount = 0 } = useBadges(selectWaitlistCount)
    const scope = useScopeLabel()
    const brand = t("brand.name")
    const active = activeNavUrl(pathname)

    const items = MAIN_NAV.flatMap((entry): NavEntry[] => {
        if (isNavGroup(entry)) {
            const visible = entry.items.filter((sub) => canOpen(sub.url))
            return visible.length > 0 ? [{ ...entry, items: visible }] : []
        }
        // `/app/dashboard` maps to no capability: every signed-in principal may open it.
        return canOpen(entry.url) ? [entry] : []
    })
    const system = SYSTEM_NAV.filter((link) => canOpen(link.url))

    const linkLabel = (link: NavLink) => {
        const label = t(link.labelKey)
        return link.url === WAITLIST_PATH && waitlistCount > 0 ? `${label} (${waitlistCount})` : label
    }

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
                        <SidebarMenuButton size="lg" asChild tooltip={scope ? `${brand} · ${scope}` : brand}>
                            <Link href="/app/dashboard" prefetch={false}>
                                <div className="flex aspect-square size-8 items-center justify-center overflow-hidden rounded-md">
                                    {/* eslint-disable-next-line @next/next/no-img-element */}
                                    <img src="/Logitrack-logo.jpg" alt="" className="h-full w-full object-cover" />
                                </div>
                                <div className="grid min-w-0 flex-1 text-left leading-tight">
                                    <span className="truncate text-sm font-semibold">{brand}</span>
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
                    <SidebarGroupLabel>{t("nav.mainMenu")}</SidebarGroupLabel>
                    <SidebarGroupContent>
                        <SidebarMenu>
                            {items.map((item) => {
                                const title = t(item.labelKey)
                                return (
                                    <SidebarMenuItem key={item.labelKey}>
                                        {isNavGroup(item) ? (
                                            <Collapsible defaultOpen className="group/collapsible">
                                                <CollapsibleTrigger asChild>
                                                    <SidebarMenuButton tooltip={title}>
                                                        <item.icon />
                                                        <span>{title}</span>
                                                        <ChevronDown className="ml-auto transition-transform group-data-[state=open]/collapsible:rotate-180" />
                                                    </SidebarMenuButton>
                                                </CollapsibleTrigger>
                                                <CollapsibleContent>
                                                    <SidebarMenuSub>
                                                        {item.items.map((sub) => (
                                                            <SidebarMenuSubItem key={sub.url}>
                                                                <SidebarMenuSubButton asChild isActive={active === sub.url}>
                                                                    <Link href={sub.url} prefetch={false}>
                                                                        <span>{t(sub.labelKey)}</span>
                                                                    </Link>
                                                                </SidebarMenuSubButton>
                                                            </SidebarMenuSubItem>
                                                        ))}
                                                    </SidebarMenuSub>
                                                </CollapsibleContent>
                                            </Collapsible>
                                        ) : (
                                            <SidebarMenuButton asChild tooltip={linkLabel(item)} isActive={active === item.url}>
                                                <Link href={item.url} prefetch={false}>
                                                    {item.icon ? <item.icon /> : null}
                                                    <span>{linkLabel(item)}</span>
                                                </Link>
                                            </SidebarMenuButton>
                                        )}
                                    </SidebarMenuItem>
                                )
                            })}
                        </SidebarMenu>
                    </SidebarGroupContent>
                </SidebarGroup>

                {system.length > 0 && (
                    <SidebarGroup className="mt-auto">
                        <SidebarGroupLabel>{t("nav.system")}</SidebarGroupLabel>
                        <SidebarGroupContent>
                            <SidebarMenu>
                                {system.map((link) => (
                                    <SidebarMenuItem key={link.url}>
                                        <SidebarMenuButton asChild tooltip={t(link.labelKey)} isActive={active === link.url}>
                                            <Link href={link.url} prefetch={false}>
                                                {link.icon ? <link.icon /> : null}
                                                <span>{t(link.labelKey)}</span>
                                            </Link>
                                        </SidebarMenuButton>
                                    </SidebarMenuItem>
                                ))}
                            </SidebarMenu>
                        </SidebarGroupContent>
                    </SidebarGroup>
                )}
            </SidebarContent>
            <SidebarFooter>
                <SidebarMenu>
                    <SidebarMenuItem>
                        <SidebarMenuButton asChild tooltip={t("nav.supportCenter")}>
                            <Link href="/support" prefetch={false}>
                                <HelpCircle />
                                <span>{t("nav.supportCenter")}</span>
                            </Link>
                        </SidebarMenuButton>
                    </SidebarMenuItem>
                </SidebarMenu>
                <p className="truncate px-2 text-xs text-muted-foreground group-data-[collapsible=icon]:hidden">
                    {t("nav.appVersion", { version: WEB_APP_VERSION })}
                </p>
            </SidebarFooter>
        </Sidebar>
    )
}
