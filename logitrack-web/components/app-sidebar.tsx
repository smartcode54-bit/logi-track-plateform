"use client"

import { useEffect, useState } from "react"
import { collection, onSnapshot } from "firebase/firestore"
import { db } from "@/firebase/client"
import { COLLECTIONS } from "@/lib/collections"
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
import { can } from "@/lib/permissions"
import { CAPABILITIES } from "@/lib/capabilities"
import { WEB_APP_VERSION } from "@/lib/app-version"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"

export function AppSidebar() {
    const { t } = useLanguage()
    const pathname = usePathname()
    const auth = useAuth()
    const logout = auth?.logout
    const claims = auth?.customClaims ?? null
    const [waitlistCount, setWaitlistCount] = useState(0)

    useEffect(() => {
        if (!can(claims, CAPABILITIES.waitlist_view)) return
        const unsub = onSnapshot(collection(db, COLLECTIONS.WAITLIST), (snap) => {
            setWaitlistCount(snap.size)
        })
        return () => unsub()
    }, [claims])

    // Menu items structure based on "Logistics Pro" design (with capability for filtering)
    const allItems = [
        {
            title: t("nav.dashboard"),
            url: "/admin/dashboard",
            icon: LayoutDashboard,
            capability: CAPABILITIES.fleet_view_trucks,
        },
        {
            title: t("nav.fleets"),
            icon: Truck,
            items: [
                { title: t("nav.truckManagement"), url: "/admin/trucks", capability: CAPABILITIES.fleet_view_trucks },
                { title: t("nav.truckAssignment"), url: "/admin/truck-assignment", capability: CAPABILITIES.fleet_view_assignments },
                { title: t("nav.truckRenewals"), url: "/admin/renewals", capability: CAPABILITIES.fleet_view_renewals },
                { title: t("nav.maintenanceCosts"), url: "/admin/maintenance", capability: CAPABILITIES.fleet_manage_maintenance },
            ],
        },
        {
            title: t("nav.customers"),
            url: "/admin/customers",
            icon: Building2,
            capability: CAPABILITIES.fleet_manage_customers,
        },
        {
            title: t("nav.manageSubcontractors"),
            url: "/admin/subcontractors",
            icon: Briefcase,
            capability: CAPABILITIES.fleet_manage_subcontractors,
        },
        {
            title: t("nav.driverManagement"),
            url: "/admin/drivers",
            icon: User,
            capability: CAPABILITIES.drivers_view,
        },
        {
            title: t("nav.chat") || "Chat",
            url: "/admin/chat",
            icon: MessageCircle,
            capability: CAPABILITIES.chat_view,
        },
        {
            title: t("nav.waitlist"),
            url: "/admin/waitlist",
            icon: Mail,
            capability: CAPABILITIES.waitlist_view,
        },
        {
            title: t("nav.accounting"),
            icon: Calculator,
            items: [
                { title: t("nav.fuel"), url: "/admin/accounting/fuel", capability: CAPABILITIES.accounting_view_fuel },
                { title: t("nav.other"), url: "/admin/accounting/other", capability: CAPABILITIES.accounting_view_other },
                { title: t("nav.auditExpense"), url: "/admin/accounting/audit", capability: CAPABILITIES.accounting_audit_expense },
                { title: t("nav.rateCard"), url: "/admin/accounting/rate-card", capability: CAPABILITIES.accounting_view_rate_card },
                { title: t("nav.income"), url: "/admin/accounting/income", capability: CAPABILITIES.accounting_view_income },
            ],
        },
        {
            title: t("nav.operations"),
            icon: MapPin,
            items: [
                { title: t("nav.firstMileTasks"), url: "/admin/first-mile", capability: CAPABILITIES.operations_view_first_mile },
                { title: t("nav.lineHaulTasks"), url: "/admin/line-haul", capability: CAPABILITIES.operations_view_line_haul },
                { title: t("nav.sourceManagement"), url: "/admin/sources", capability: CAPABILITIES.operations_manage_sources },
                { title: t("nav.driverMonitor"), url: "/admin/driver-monitor", capability: CAPABILITIES.operations_view_driver_monitor },
                { title: t("nav.incidentReports"), url: "/admin/incident-reports", capability: CAPABILITIES.operations_view_incidents },
            ],
        },
        {
            title: t("nav.hr"),
            icon: Users,
            items: [
                { title: t("nav.payroll"), url: "/admin/payroll", capability: CAPABILITIES.hr_view_payroll },
                { title: t("nav.leaveRequests"), url: "/admin/leave-requests", capability: CAPABILITIES.hr_view_leave },
                { title: t("nav.holidays"), url: "/admin/holidays", capability: CAPABILITIES.hr_manage_holidays },
            ],
        },
    ]

    const items = allItems
        .map((item) => {
            if (item.items) {
                const filteredSub = item.items.filter((sub) => can(claims, sub.capability))
                if (filteredSub.length === 0) return null
                return { ...item, items: filteredSub }
            }
            return can(claims, item.capability) ? item : null
        })
        .filter(Boolean) as typeof allItems

    const showSecurityCenter = can(claims, CAPABILITIES.security_view_overview)

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
                            <Link href="/admin/dashboard" prefetch={false}>
                                <div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-blue-600 text-primary-foreground">
                                    <Truck className="size-4 text-white" />
                                </div>
                                <div className="grid flex-1 text-left text-sm leading-tight">
                                    <span className="truncate font-semibold text-base">Logistics Pro</span>
                                    <span className="truncate text-xs text-muted-foreground">Enterprise Admin</span>
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
                                        <SidebarMenuButton asChild tooltip={item.url === "/admin/waitlist" && waitlistCount > 0 ? `${item.title} (${waitlistCount})` : item.title} isActive={pathname === item.url}>
                                            <Link href={item.url} prefetch={false}>
                                                {item.icon && <item.icon />}
                                                <span>{item.title}{item.url === "/admin/waitlist" && waitlistCount > 0 ? ` (${waitlistCount})` : ""}</span>
                                            </Link>
                                        </SidebarMenuButton>
                                    )}
                                </SidebarMenuItem>
                            ))}
                        </SidebarMenu>
                    </SidebarGroupContent>
                </SidebarGroup>

                {showSecurityCenter && (
                    <SidebarGroup className="mt-auto">
                        <SidebarGroupLabel>{t("nav.system")}</SidebarGroupLabel>
                        <SidebarGroupContent>
                            <SidebarMenu>
                                <SidebarMenuItem>
                                    <SidebarMenuButton asChild tooltip={t("nav.securityCenter")} isActive={pathname?.startsWith("/admin/security-center")}>
                                        <Link href="/admin/security-center" prefetch={false}>
                                            <Shield />
                                            <span>{t("nav.securityCenter")}</span>
                                        </Link>
                                    </SidebarMenuButton>
                                </SidebarMenuItem>
                                <SidebarMenuItem>
                                    <SidebarMenuButton asChild tooltip="Utilities" isActive={pathname?.startsWith("/admin/utilities")}>
                                        <Link href="/admin/utilities/backfill" prefetch={false}>
                                            <Wrench className="h-4 w-4" />
                                            <span>Utilities</span>
                                        </Link>
                                    </SidebarMenuButton>
                                </SidebarMenuItem>
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
