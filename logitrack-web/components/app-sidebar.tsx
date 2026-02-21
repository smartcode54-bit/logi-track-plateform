"use client"

import {
    LayoutDashboard,
    Truck,
    Users,
    Package,
    BarChart3,
    Settings,
    ChevronDown,
    Building2,
    LogOut,
    User,
    HelpCircle,
    GitBranch,
    Shield,
    MapPin
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
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"

export function AppSidebar() {
    const { t } = useLanguage()
    const pathname = usePathname()
    const auth = useAuth()
    const logout = auth?.logout

    // Menu items structure based on "Logistics Pro" design
    const items = [
        {
            title: t("nav.dashboard"),
            url: "/admin/dashboard",
            icon: LayoutDashboard,
        },
        {
            title: t("nav.fleets"),
            icon: Truck,
            items: [
                {
                    title: t("nav.truckManagement"),
                    url: "/admin/trucks",
                },
                {
                    title: t("nav.truckAssignment"),
                    url: "/admin/truck-assignment",
                },
                {
                    title: t("nav.truckRenewals"),
                    url: "/admin/renewals",
                },
                {
                    title: t("nav.maintenanceCosts"),
                    url: "/admin/maintenance",
                },
                {
                    title: t("nav.manageSubcontractors"),
                    url: "/admin/subcontractors",
                },
            ],
        },
        {
            title: t("nav.driverManagement"),
            url: "/admin/drivers",
            icon: User,
        },
        {
            title: t("nav.activeShipments"),
            url: "/admin/packages",
            icon: GitBranch, // Using GitBranch to represent flow/shipments
        },
        {
            title: t("nav.userRoles"),
            url: "/admin/users",
            icon: Shield,
        },
        {
            title: t("nav.reporting"),
            url: "/admin/analytics",
            icon: BarChart3,
        },
        {
            title: t("nav.operations"),
            icon: MapPin, // Using MapPin or generic icon
            items: [
                {
                    title: t("nav.firstMileTasks"),
                    url: "/admin/first-mile",
                },
                {
                    title: t("nav.sourceManagement"),
                    url: "/admin/sources",
                },
                {
                    title: t("nav.driverMonitor"),
                    url: "/admin/driver-monitor",
                },
            ],
        },
    ]

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
                                                    {item.items.map((subItem) => (
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

                <SidebarGroup className="mt-auto">
                    <SidebarGroupLabel>{t("nav.system")}</SidebarGroupLabel>
                    <SidebarGroupContent>
                        <SidebarMenu>
                            <SidebarMenuItem>
                                <SidebarMenuButton asChild tooltip="Settings" isActive={pathname === "/admin/settings"}>
                                    <Link href="/admin/settings">
                                        <Settings />
                                        <span>{t("nav.settings")}</span>
                                    </Link>
                                </SidebarMenuButton>
                            </SidebarMenuItem>
                        </SidebarMenu>
                    </SidebarGroupContent>
                </SidebarGroup>
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
