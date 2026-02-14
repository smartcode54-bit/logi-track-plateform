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
            title: "Dashboard",
            url: "/admin/dashboard",
            icon: LayoutDashboard,
        },
        {
            title: "Fleet",
            icon: Truck,
            items: [
                {
                    title: "Truck Management",
                    url: "/admin/trucks",
                },
                {
                    title: "Truck Assignment",
                    url: "/admin/truck-assignment",
                },
                {
                    title: "Truck Renewals",
                    url: "/admin/renewals",
                },
                {
                    title: "Maintenance Costs",
                    url: "/admin/maintenance",
                },
                {
                    title: "Manage Subcontractors",
                    url: "/admin/subcontractors",
                },
            ],
        },
        {
            title: "Driver Management",
            url: "/admin/drivers",
            icon: User,
        },
        {
            title: "Active Shipments",
            url: "/admin/packages",
            icon: GitBranch, // Using GitBranch to represent flow/shipments
        },
        {
            title: "User Roles",
            url: "/admin/users",
            icon: Shield,
        },
        {
            title: "Reporting",
            url: "/admin/analytics",
            icon: BarChart3,
        },
        {
            title: "Operations",
            icon: MapPin, // Using MapPin or generic icon
            items: [
                {
                    title: "First Mile Tasks",
                    url: "/admin/first-mile",
                },
                {
                    title: "Source Management",
                    url: "/admin/sources",
                },
                // Future: Line Haul, etc.
            ],
        },
    ]

    const { setOpen } = useSidebar()

    return (
        <Sidebar
            collapsible="icon"
            className="border-r-0"
            onMouseEnter={() => setOpen(true)}
            onMouseLeave={() => setOpen(false)}
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
                    <SidebarGroupLabel>MAIN MENU</SidebarGroupLabel>
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
                    <SidebarGroupLabel>SYSTEM</SidebarGroupLabel>
                    <SidebarGroupContent>
                        <SidebarMenu>
                            <SidebarMenuItem>
                                <SidebarMenuButton asChild tooltip="Settings" isActive={pathname === "/admin/settings"}>
                                    <Link href="/admin/settings">
                                        <Settings />
                                        <span>Settings</span>
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
                                <span className="text-muted-foreground">Support Center</span>
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
                            <span>Logout</span>
                        </SidebarMenuButton>
                    </SidebarMenuItem>
                </SidebarMenu>
            </SidebarFooter>
        </Sidebar>
    )
}
