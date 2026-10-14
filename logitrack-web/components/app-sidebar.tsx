"use client"

import { useMemo, useState } from "react"
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
    useSidebar,
} from "@/components/ui/sidebar"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useLanguage } from "@/context/language"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { DASHBOARD_PATH } from "@/lib/routeCapabilities"
import { cn } from "@/lib/utils"
import type { MeDTO } from "@/features/auth/api/me"
import { homeRouteOf } from "@/features/auth/api/homeRoute"
import { useMe } from "@/features/auth/api/useMe"
import { useScopeLabel } from "@/features/auth/hooks/useScopeLabel"
import type { BadgesDTO } from "@/features/dashboard/api/badges"
import { useBadges } from "@/features/dashboard/api/useBadges"
import { WEB_APP_VERSION } from "@/lib/app-version"
import { activeNavUrl, groupOf, isNavGroup, menuFor, type NavGroup, type NavLink } from "./app-nav"
import { ScopeLine } from "./scope-line"
import { sidebarHeaderTooltip, useCloseSidebarOnPhone, useRevealCurrentEntry } from "./sidebar-shell"

const NO_CAPABILITIES: readonly string[] = []
const selectCapabilities = (me: MeDTO | null) => me?.capabilities ?? NO_CAPABILITIES
/** The principal's home, as the edge gate and the breadcrumb's Home pick it (R89). */
const selectHome = (me: MeDTO | null) => (me ? homeRouteOf(me) : DASHBOARD_PATH)
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
 *
 * UI3B: the menu follows the work loop and starts with the principal's home; only the group holding the current
 * page starts open; the current page's entry is Route Blue with `aria-current="page"` and is scrolled into view when
 * the sidebar opens; in the icon rail each group opens its pages as a menu to the right (the collapsible lists are
 * hidden there, so a group button used to open nothing); the menu is a labelled `<nav>`.
 */
export function AppSidebar() {
    const { t } = useLanguage()
    const pathname = usePathname()
    const { data: capabilities = NO_CAPABILITIES } = useMe(selectCapabilities)
    const { data: homeHref = DASHBOARD_PATH } = useMe(selectHome)
    const held = useMemo(() => new Set(capabilities), [capabilities])
    const { main, system } = useMemo(() => menuFor(held, homeHref), [held, homeHref])
    const { data: waitlistCount = 0 } = useBadges(selectWaitlistCount)
    const scope = useScopeLabel()
    const closeOnPhone = useCloseSidebarOnPhone()
    const { state, isMobile } = useSidebar()
    const rail = state === "collapsed" && !isMobile
    const brand = t("brand.name")
    const active = activeNavUrl(pathname)
    const navRef = useRevealCurrentEntry<HTMLElement>(active)

    // Only the group of the current page starts open; the others wait to be opened. Moving to a page of another
    // group opens that group too (and leaves the ones the user opened as they are).
    const activeGroup = groupOf(main, active)
    const [openGroups, setOpenGroups] = useState<ReadonlySet<string>>(() => new Set(activeGroup ? [activeGroup] : []))
    const [shownGroup, setShownGroup] = useState(activeGroup)
    if (activeGroup !== shownGroup) {
        setShownGroup(activeGroup)
        if (activeGroup && !openGroups.has(activeGroup)) setOpenGroups(new Set([...openGroups, activeGroup]))
    }
    const setGroupOpen = (key: string, open: boolean) =>
        setOpenGroups((previous) => {
            const next = new Set(previous)
            if (open) next.add(key)
            else next.delete(key)
            return next
        })

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
            sheetCloseLabel={t("shell.closeMenu")}
        >
            <SidebarHeader>
                <SidebarMenu>
                    <SidebarMenuItem>
                        <SidebarMenuButton size="lg" asChild tooltip={sidebarHeaderTooltip(brand, scope)} className="h-auto min-h-12">
                            <Link href={homeHref} prefetch={false} onClick={closeOnPhone}>
                                <div className="flex aspect-square size-8 shrink-0 items-center justify-center overflow-hidden rounded-md">
                                    {/* eslint-disable-next-line @next/next/no-img-element */}
                                    <img src="/Logitrack-logo.jpg" alt="" className="h-full w-full object-cover" />
                                </div>
                                <div className="grid min-w-0 flex-1 text-left">
                                    <span className="truncate text-sm leading-normal font-semibold">{brand}</span>
                                    <ScopeLine className="text-xs text-muted-foreground" testId="sidebar-scope" />
                                </div>
                            </Link>
                        </SidebarMenuButton>
                    </SidebarMenuItem>
                </SidebarMenu>
            </SidebarHeader>
            <SidebarContent asChild>
                <nav aria-label={t("shell.nav.main")} ref={navRef}>
                    <SidebarGroup>
                        <SidebarGroupContent>
                            <SidebarMenu>
                                {main.map((item) => {
                                    const title = t(item.labelKey)
                                    if (!isNavGroup(item)) {
                                        return (
                                            <SidebarMenuItem key={item.url}>
                                                <SidebarMenuButton asChild tooltip={title} isActive={active === item.url}>
                                                    <Link href={item.url} prefetch={false} onClick={closeOnPhone}>
                                                        {item.icon ? <item.icon /> : null}
                                                        <span>{title}</span>
                                                    </Link>
                                                </SidebarMenuButton>
                                            </SidebarMenuItem>
                                        )
                                    }
                                    const holdsActive = activeGroup === item.labelKey
                                    return (
                                        <SidebarMenuItem key={item.labelKey}>
                                            {rail ? (
                                                <RailGroup group={item} title={title} active={active} label={linkLabel} />
                                            ) : (
                                                <Collapsible
                                                    open={openGroups.has(item.labelKey)}
                                                    onOpenChange={(open) => setGroupOpen(item.labelKey, open)}
                                                    className="group/collapsible"
                                                >
                                                    <CollapsibleTrigger asChild>
                                                        {/* A closed group that holds the current page says so: Route Blue, and
                                                            aria-current="true" for those who do not see the colour. */}
                                                        <SidebarMenuButton
                                                            isActive={holdsActive && !openGroups.has(item.labelKey)}
                                                            aria-current={holdsActive && !openGroups.has(item.labelKey) ? "true" : undefined}
                                                        >
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
                                                                        <Link href={sub.url} prefetch={false} onClick={closeOnPhone}>
                                                                            <span>{linkLabel(sub)}</span>
                                                                        </Link>
                                                                    </SidebarMenuSubButton>
                                                                </SidebarMenuSubItem>
                                                            ))}
                                                        </SidebarMenuSub>
                                                    </CollapsibleContent>
                                                </Collapsible>
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
                                            <SidebarMenuButton asChild tooltip={linkLabel(link)} isActive={active === link.url}>
                                                <Link href={link.url} prefetch={false} onClick={closeOnPhone}>
                                                    {link.icon ? <link.icon /> : null}
                                                    <span>{linkLabel(link)}</span>
                                                </Link>
                                            </SidebarMenuButton>
                                        </SidebarMenuItem>
                                    ))}
                                </SidebarMenu>
                            </SidebarGroupContent>
                        </SidebarGroup>
                    )}
                </nav>
            </SidebarContent>
            <SidebarFooter>
                <SidebarMenu>
                    <SidebarMenuItem>
                        <SidebarMenuButton asChild tooltip={t("nav.supportCenter")}>
                            <Link href="/support" prefetch={false} onClick={closeOnPhone}>
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

/**
 * A menu group in the icon rail (UI3B): the group's icon opens its pages as a menu to the right, on the popover
 * layer, keyboard operable (Enter, Space or Arrow Down opens it, arrows move, Enter follows a link). The group's
 * name is the button's tooltip and the menu's label; the current page is marked as in the open sidebar.
 */
function RailGroup({
    group,
    title,
    active,
    label,
}: {
    group: NavGroup
    title: string
    active: string | undefined
    label: (link: NavLink) => string
}) {
    const holdsActive = group.items.some((link) => link.url === active)
    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                {/* The group of the current page: Route Blue, and aria-current="true" (the current section). */}
                <SidebarMenuButton tooltip={title} isActive={holdsActive} aria-current={holdsActive ? "true" : undefined}>
                    <group.icon />
                    <span>{title}</span>
                </SidebarMenuButton>
            </DropdownMenuTrigger>
            <DropdownMenuContent side="right" align="start" className="min-w-56" data-testid="rail-group-menu">
                <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">{title}</DropdownMenuLabel>
                <DropdownMenuSeparator />
                {group.items.map((link) => {
                    const current = active === link.url
                    return (
                        <DropdownMenuItem key={link.url} asChild>
                            <Link
                                href={link.url}
                                prefetch={false}
                                aria-current={current ? "page" : undefined}
                                className={cn(current && "font-medium text-primary")}
                            >
                                {label(link)}
                            </Link>
                        </DropdownMenuItem>
                    )
                })}
            </DropdownMenuContent>
        </DropdownMenu>
    )
}
