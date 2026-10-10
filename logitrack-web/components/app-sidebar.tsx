"use client"

import { useMemo } from "react"
import {
    LayoutDashboard,
    Truck,
    Users,
    Package,
    BarChart3,
    ChevronDown,
    Building2,
    Briefcase,
    LogOut,
    User,
    HelpCircle,
    Shield,
    MapPin,
    Calculator,
    MessageCircle,
    Mail,
    Calendar,
    Wrench,
} from "lucide-react"

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
import { useLanguage } from "@/context/language"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { useAuth } from "@/context/auth"
import { getRole } from "@/lib/permissions"
import { routeAllowed } from "@/lib/routeCapabilities"
import type { MeDTO } from "@/features/auth/api/me"
import { useMe } from "@/features/auth/api/useMe"
import type { BadgesDTO } from "@/features/dashboard/api/badges"
import { useBadges } from "@/features/dashboard/api/useBadges"
import { WEB_APP_VERSION } from "@/lib/app-version"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"

const NO_CAPABILITIES: readonly string[] = []
const selectCapabilities = (me: MeDTO | null) => me?.capabilities ?? NO_CAPABILITIES
const selectWaitlistCount = (badges: BadgesDTO) => badges.waitlist ?? 0

/**
 * The main sidebar. Its entries are the routes the `proxy.ts` gate would let this principal open:
 * the same table (lib/routeCapabilities.ts) over the same capabilities (`['me']`, TW4), so a link is
 * never shown for a page that would answer `/app/unauthorized`. The waitlist count is `['badges']`
 * (polled, one count query in P0), not a listener on the whole collection.
 */
export function AppSidebar() {
    const { t } = useLanguage()
    const pathname = usePathname()
    const auth = useAuth()
    const logout = auth?.logout
    const claims = auth?.customClaims ?? null
    const { data: capabilities = NO_CAPABILITIES } = useMe(selectCapabilities)
    const held = useMemo(() => new Set(capabilities), [capabilities])
    const canOpen = (url: string) => routeAllowed(held, url)
    const { data: waitlistCount = 0 } = useBadges(selectWaitlistCount)

    // Menu items structure based on "LogiTrack Pro" design; filtered by URL with the gate's table
    const allItems = [
        {
            title: t("nav.dashboard"),
            url: "/app/dashboard",
            icon: LayoutDashboard,
        },
        {
            title: t("nav.fleets"),
            icon: Truck,
            items: [
                { title: t("nav.truckManagement"), url: "/app/trucks" },
                { title: t("nav.truckAssignment"), url: "/app/truck-assignment" },
                { title: t("nav.truckRenewals"), url: "/app/renewals" },
                { title: t("nav.maintenanceCosts"), url: "/app/maintenance" },
            ],
        },
        {
            title: t("nav.customers"),
            url: "/app/customers",
            icon: Building2,
        },
        {
            title: t("nav.manageSubcontractors"),
            url: "/app/subcontractors",
            icon: Briefcase,
        },
        {
            title: t("nav.driverManagement"),
            url: "/app/drivers",
            icon: User,
        },
        {
            title: t("nav.chat") || "Chat",
            url: "/app/chat",
            icon: MessageCircle,
        },
        {
            title: t("nav.waitlist"),
            url: "/app/waitlist",
            icon: Mail,
        },
        {
            title: t("nav.accounting"),
            icon: Calculator,
            items: [
                { title: t("nav.fuel"), url: "/app/accounting/fuel" },
                { title: t("nav.fuelPriceHistory"), url: "/app/accounting/fuel-price-history" },
                { title: t("nav.other"), url: "/app/accounting/other" },
                { title: t("nav.auditExpense"), url: "/app/accounting/audit" },
                { title: t("nav.rateCard"), url: "/app/accounting/rate-card" },
                { title: t("nav.income"), url: "/app/accounting/income" },
                { title: t("nav.billingDocument"), url: "/app/accounting/billing-document" },
                { title: t("nav.billingResult"), url: "/app/accounting/billing-result" },
                { title: t("nav.shopeeExpressReport"), url: "/app/accounting/shopee-express-report" },
            ],
        },
        {
            title: t("nav.operations"),
            icon: MapPin,
            items: [
                { title: t("nav.jobAssign", "มอบหมายงาน"), url: "/app/job-assign" },
                { title: t("nav.sourceManagement"), url: "/app/sources" },
                { title: t("nav.driverMonitor"), url: "/app/driver-monitor" },
                { title: t("nav.incidentReports"), url: "/app/incident-reports" },
                { title: t("nav.standbyRecords"), url: "/app/standby-records" },
            ],
        },
        {
            title: t("nav.hr"),
            icon: Users,
            items: [
                { title: t("nav.payroll"), url: "/app/payroll" },
                { title: t("nav.leaveRequests"), url: "/app/leave-requests" },
                { title: t("nav.holidays"), url: "/app/holidays" },
            ],
        },
    ]

    const items = allItems
        .map((item) => {
            if (item.items) {
                const filteredSub = item.items.filter((sub) => canOpen(sub.url))
                if (filteredSub.length === 0) return null
                return { ...item, items: filteredSub }
            }
            // `/app/dashboard` maps to no capability: every signed-in principal may open it.
            return item.url && canOpen(item.url) ? item : null
        })
        .filter(Boolean) as typeof allItems

    const showSecurityCenter = canOpen("/app/security-center")
    const showUtilities = canOpen("/app/utilities/backfill")
    const showCompanies = canOpen("/app/companies")
    const showCompanyProfile = canOpen("/app/settings/company-profile")

    const { setOpen } = useSidebar()

    return (
        <Sidebar
            collapsible="icon"
            className="border-r-0 !z-40"
        >
            <SidebarHeader>
                <SidebarMenu>
                    <SidebarMenuItem>
                        <SidebarMenuButton size="lg" asChild>
                            <Link href="/app/dashboard" prefetch={false}>
                                <div className="flex aspect-square size-8 items-center justify-center rounded-lg overflow-hidden">
                                    <img src="/Logitrack-logo.jpg" alt="LogiTrack" className="w-full h-full object-cover" />
                                </div>
                                <div className="grid flex-1 text-left text-sm leading-tight">
                                    <span className="truncate font-semibold text-base">LogiTrack Pro</span>
                                    <span className="truncate text-xs text-muted-foreground capitalize">
                                        {(getRole(claims) || "user").replace(/_/g, " ")}
                                    </span>
                                    <span className="truncate text-[11px] text-muted-foreground/90">
                                        {t("nav.appVersion", { version: WEB_APP_VERSION })}
                                    </span>
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
                            {items.map((item) => (
                                <SidebarMenuItem key={item.title}>
                                    {item.items ? (
                                        <Collapsible defaultOpen className="group/collapsible">
                                            <CollapsibleTrigger asChild>
                                                <SidebarMenuButton tooltip={item.title}>
                                                    {item.icon && <item.icon />}
                                                    <span>{item.title}</span>
                                                    <ChevronDown className="ml-auto transition-transform group-data-[state=open]/collapsible:rotate-180" />
                                                </SidebarMenuButton>
                                            </CollapsibleTrigger>
                                            <CollapsibleContent>
                                                <SidebarMenuSub>
                                                    {item.items.map((subItem: { title: string; url: string }) => (
                                                        <SidebarMenuSubItem key={subItem.title}>
                                                            <SidebarMenuSubButton asChild isActive={pathname === subItem.url}>
                                                                <Link href={subItem.url} prefetch={false}>
                                                                    <span>{subItem.title}</span>
                                                                </Link>
                                                            </SidebarMenuSubButton>
                                                        </SidebarMenuSubItem>
                                                    ))}
                                                </SidebarMenuSub>
                                            </CollapsibleContent>
                                        </Collapsible>
                                    ) : (
                                        <SidebarMenuButton asChild tooltip={item.url === "/app/waitlist" && waitlistCount > 0 ? `${item.title} (${waitlistCount})` : item.title} isActive={pathname === item.url}>
                                            <Link href={item.url} prefetch={false}>
                                                {item.icon && <item.icon />}
                                                <span>{item.title}{item.url === "/app/waitlist" && waitlistCount > 0 ? ` (${waitlistCount})` : ""}</span>
                                            </Link>
                                        </SidebarMenuButton>
                                    )}
                                </SidebarMenuItem>
                            ))}
                        </SidebarMenu>
                    </SidebarGroupContent>
                </SidebarGroup>

                {(showSecurityCenter || showUtilities || showCompanies || showCompanyProfile) && (
                    <SidebarGroup className="mt-auto">
                        <SidebarGroupLabel>{t("nav.system")}</SidebarGroupLabel>
                        <SidebarGroupContent>
                            <SidebarMenu>
                                {showSecurityCenter && (
                                <SidebarMenuItem>
                                    <SidebarMenuButton asChild tooltip={t("nav.securityCenter")} isActive={pathname?.startsWith("/app/security-center")}>
                                        <Link href="/app/security-center" prefetch={false}>
                                            <Shield />
                                            <span>{t("nav.securityCenter")}</span>
                                        </Link>
                                    </SidebarMenuButton>
                                </SidebarMenuItem>
                                )}
                                {showUtilities && (
                                <SidebarMenuItem>
                                    <SidebarMenuButton asChild tooltip="Utilities" isActive={pathname?.startsWith("/app/utilities")}>
                                        <Link href="/app/utilities/backfill" prefetch={false}>
                                            <Wrench className="h-4 w-4" />
                                            <span>Utilities</span>
                                        </Link>
                                    </SidebarMenuButton>
                                </SidebarMenuItem>
                                )}
                                {showCompanies && (
                                    <SidebarMenuItem>
                                        <SidebarMenuButton asChild tooltip={t("nav.companies")} isActive={pathname?.startsWith("/app/companies")}>
                                            <Link href="/app/companies" prefetch={false}>
                                                <Building2 className="h-4 w-4" />
                                                <span>{t("nav.companies")}</span>
                                            </Link>
                                        </SidebarMenuButton>
                                    </SidebarMenuItem>
                                )}
                                {showCompanyProfile && (
                                    <SidebarMenuItem>
                                        <SidebarMenuButton asChild tooltip={t("nav.companyProfile")} isActive={pathname?.startsWith("/app/settings/company-profile")}>
                                            <Link href="/app/settings/company-profile" prefetch={false}>
                                                <Building2 className="h-4 w-4" />
                                                <span>{t("nav.companyProfile")}</span>
                                            </Link>
                                        </SidebarMenuButton>
                                    </SidebarMenuItem>
                                )}
                            </SidebarMenu>
                        </SidebarGroupContent>
                    </SidebarGroup>
                )}
            </SidebarContent>
            <SidebarFooter>
                <SidebarMenu>
                    <SidebarMenuItem>
                        <SidebarMenuButton asChild tooltip="Support Center">
                            <Link href="/support" prefetch={false}>
                                <HelpCircle />
                                <span className="text-muted-foreground">{t("nav.supportCenter")}</span>
                            </Link>
                        </SidebarMenuButton>
                    </SidebarMenuItem>
                    <SidebarMenuItem>
                        <SidebarMenuButton
                            className="text-red-500 hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-950/50"
                            onClick={async () => {
                                await logout?.()
                                window.location.href = "/"
                            }}
                        >
                            <LogOut />
                            <span>{t("nav.logout")}</span>
                        </SidebarMenuButton>
                    </SidebarMenuItem>
                </SidebarMenu>
            </SidebarFooter>
        </Sidebar>
    )
}
