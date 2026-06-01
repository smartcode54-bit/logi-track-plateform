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
import { can, getRole } from "@/lib/permissions"
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
        }, (err: any) => {
            // Silently handle permission-denied (claims may not be ready yet)
            if (err?.code !== "permission-denied" && err?.code !== "PERMISSION_DENIED") {
                console.error("[Sidebar] waitlist listener error:", err)
            }
        })
        return () => unsub()
    }, [claims])

    // Menu items structure based on "LogiTrack Pro" design (with capability for filtering)
    const allItems = [
        {
            title: t("nav.dashboard"),
            url: "/app/dashboard",
            icon: LayoutDashboard,
            capability: null as any, // Dashboard is open to all authenticated users
        },
        {
            title: t("nav.fleets"),
            icon: Truck,
            items: [
                { title: t("nav.truckManagement"), url: "/app/trucks", capability: CAPABILITIES.fleet_view_trucks },
                { title: t("nav.truckAssignment"), url: "/app/truck-assignment", capability: CAPABILITIES.fleet_view_assignments },
                { title: t("nav.truckRenewals"), url: "/app/renewals", capability: CAPABILITIES.fleet_view_renewals },
                { title: t("nav.maintenanceCosts"), url: "/app/maintenance", capability: CAPABILITIES.fleet_manage_maintenance },
            ],
        },
        {
            title: t("nav.customers"),
            url: "/app/customers",
            icon: Building2,
            capability: CAPABILITIES.fleet_manage_customers,
        },
        {
            title: t("nav.manageSubcontractors"),
            url: "/app/subcontractors",
            icon: Briefcase,
            capability: CAPABILITIES.fleet_manage_subcontractors,
        },
        {
            title: t("nav.driverManagement"),
            url: "/app/drivers",
            icon: User,
            capability: CAPABILITIES.drivers_view,
        },
        {
            title: t("nav.chat") || "Chat",
            url: "/app/chat",
            icon: MessageCircle,
            capability: CAPABILITIES.chat_view,
        },
        {
            title: t("nav.waitlist"),
            url: "/app/waitlist",
            icon: Mail,
            capability: CAPABILITIES.waitlist_view,
        },
        {
            title: t("nav.accounting"),
            icon: Calculator,
            items: [
                { title: t("nav.fuel"), url: "/app/accounting/fuel", capability: CAPABILITIES.accounting_view_fuel },
                { title: t("nav.fuelPriceHistory"), url: "/app/accounting/fuel-price-history", capability: CAPABILITIES.accounting_view_fuel },
                { title: t("nav.other"), url: "/app/accounting/other", capability: CAPABILITIES.accounting_view_other },
                { title: t("nav.auditExpense"), url: "/app/accounting/audit", capability: CAPABILITIES.accounting_audit_expense },
                { title: t("nav.rateCard"), url: "/app/accounting/rate-card", capability: CAPABILITIES.accounting_view_rate_card },
                { title: t("nav.income"), url: "/app/accounting/income", capability: CAPABILITIES.accounting_view_income },
                { title: t("nav.billingDocument"), url: "/app/accounting/billing-document", capability: CAPABILITIES.accounting_billing_document },
                { title: t("nav.billingResult"), url: "/app/accounting/billing-result", capability: CAPABILITIES.accounting_billing_result },
            ],
        },
        {
            title: t("nav.operations"),
            icon: MapPin,
            items: [
                { title: t("nav.firstMileTasks"), url: "/app/first-mile", capability: CAPABILITIES.operations_view_first_mile },
                { title: t("nav.lineHaulTasks"), url: "/app/line-haul", capability: CAPABILITIES.operations_view_line_haul },
                { title: t("nav.sourceManagement"), url: "/app/sources", capability: CAPABILITIES.operations_manage_sources },
                { title: t("nav.driverMonitor"), url: "/app/driver-monitor", capability: CAPABILITIES.operations_view_driver_monitor },
                { title: t("nav.incidentReports"), url: "/app/incident-reports", capability: CAPABILITIES.operations_view_incidents },
                { title: t("nav.standbyRecords"), url: "/app/standby-records", capability: CAPABILITIES.operations_view_driver_monitor },
            ],
        },
        {
            title: t("nav.hr"),
            icon: Users,
            items: [
                { title: t("nav.payroll"), url: "/app/payroll", capability: CAPABILITIES.hr_view_payroll },
                { title: t("nav.leaveRequests"), url: "/app/leave-requests", capability: CAPABILITIES.hr_view_leave },
                { title: t("nav.holidays"), url: "/app/holidays", capability: CAPABILITIES.hr_manage_holidays },
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
            // Items with no capability requirement (e.g. Dashboard) are always visible
            return !item.capability || can(claims, item.capability) ? item : null
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

                {showSecurityCenter && (
                    <SidebarGroup className="mt-auto">
                        <SidebarGroupLabel>{t("nav.system")}</SidebarGroupLabel>
                        <SidebarGroupContent>
                            <SidebarMenu>
                                <SidebarMenuItem>
                                    <SidebarMenuButton asChild tooltip={t("nav.securityCenter")} isActive={pathname?.startsWith("/app/security-center")}>
                                        <Link href="/app/security-center" prefetch={false}>
                                            <Shield />
                                            <span>{t("nav.securityCenter")}</span>
                                        </Link>
                                    </SidebarMenuButton>
                                </SidebarMenuItem>
                                <SidebarMenuItem>
                                    <SidebarMenuButton asChild tooltip="Utilities" isActive={pathname?.startsWith("/app/utilities")}>
                                        <Link href="/app/utilities/backfill" prefetch={false}>
                                            <Wrench className="h-4 w-4" />
                                            <span>Utilities</span>
                                        </Link>
                                    </SidebarMenuButton>
                                </SidebarMenuItem>
                                {can(claims, CAPABILITIES.company_view) && (
                                    <SidebarMenuItem>
                                        <SidebarMenuButton asChild tooltip={t("nav.companies")} isActive={pathname?.startsWith("/app/companies")}>
                                            <Link href="/app/companies" prefetch={false}>
                                                <Building2 className="h-4 w-4" />
                                                <span>{t("nav.companies")}</span>
                                            </Link>
                                        </SidebarMenuButton>
                                    </SidebarMenuItem>
                                )}
                                {can(claims, CAPABILITIES.company_manage) && (
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
