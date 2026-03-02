"use client"

import {
    LayoutDashboard,
    Truck,
    Users,
    Package,
    BarChart3,
    ChevronDown,
    Building2,
    LogOut,
    User,
    HelpCircle,
    GitBranch,
    Shield,
    MapPin,
    Calculator,
    MessageCircle,
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
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"

export function AppSidebar() {
    const { t } = useLanguage()
    const pathname = usePathname()
    const auth = useAuth()
    const logout = auth?.logout
    const claims = auth?.customClaims ?? null

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
                { title: t("nav.manageSubcontractors"), url: "/admin/subcontractors", capability: CAPABILITIES.fleet_manage_subcontractors },
                { title: t("nav.customers"), url: "/admin/customers", capability: CAPABILITIES.fleet_manage_customers },
            ],
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
            title: t("nav.activeShipments"),
            url: "/admin/packages",
            icon: GitBranch,
            capability: CAPABILITIES.packages_view,
        },
        {
            title: t("nav.security") || "Security",
            url: "/admin/security-center",
            icon: Shield,
            capability: CAPABILITIES.security_view_overview,
        },
        {
            title: t("nav.reporting"),
            url: "/admin/analytics",
            icon: BarChart3,
            capability: CAPABILITIES.reporting_view_analytics,
        },
        {
            title: t("nav.accounting"),
            icon: Calculator,
            items: [
                { title: t("nav.fuel"), url: "/admin/accounting/fuel", capability: CAPABILITIES.accounting_view_fuel },
                { title: t("nav.other"), url: "/admin/accounting/other", capability: CAPABILITIES.accounting_view_other },
            ],
        },
        {
            title: t("nav.operations"),
            icon: MapPin,
            items: [
                { title: t("nav.firstMileTasks"), url: "/admin/first-mile", capability: CAPABILITIES.operations_view_first_mile },
                { title: "Line Haul Tasks", url: "/admin/line-haul", capability: CAPABILITIES.operations_view_line_haul },
                { title: t("nav.sourceManagement"), url: "/admin/sources", capability: CAPABILITIES.operations_manage_sources },
                { title: t("nav.driverMonitor"), url: "/admin/driver-monitor", capability: CAPABILITIES.operations_view_driver_monitor },
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
            className="border-r-0"
        >
            <SidebarHeader>
                <SidebarMenu>
                    <SidebarMenuItem>
                        <SidebarMenuButton size="lg" asChild>
                            <Link href="/admin/dashboard">
                                <div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-blue-600 text-primary-foreground">
                                    <Truck className="size-4 text-white" />
                                </div>
                                <div className="grid flex-1 text-left text-sm leading-tight">
                                    <span className="truncate font-semibold text-base">Logistics Pro</span>
                                    <span className="truncate text-xs text-muted-foreground">Enterprise Admin</span>
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
                                                                <Link href={subItem.url}>
                                                                    <span>{subItem.title}</span>
                                                                </Link>
                                                            </SidebarMenuSubButton>
                                                        </SidebarMenuSubItem>
                                                    ))}
                                                </SidebarMenuSub>
                                            </CollapsibleContent>
                                        </Collapsible>
                                    ) : (
                                        <SidebarMenuButton asChild tooltip={item.title} isActive={pathname === item.url}>
                                            <Link href={item.url}>
                                                {item.icon && <item.icon />}
                                                <span>{item.title}</span>
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
                                    <Link href="/admin/security-center">
                                        <Shield />
                                        <span>{t("nav.securityCenter")}</span>
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
                            <Link href="/support">
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
