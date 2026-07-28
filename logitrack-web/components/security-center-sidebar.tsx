"use client"

import {
    LayoutDashboard,
    Users,
    Shield,
    Key,
    Server,
    LogOut,
    ShieldCheck,
    Layers,
    Smartphone,
    Rocket,
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
    useSidebar,
} from "@/components/ui/sidebar"
import { useLanguage } from "@/context/language"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { useAuth } from "@/context/auth"
import { can } from "@/lib/permissions"
import { CAPABILITIES } from "@/lib/capabilities"

const securityItems = [
    { titleKey: "securityCenter.overviewSoon", url: "/app/security-center", icon: LayoutDashboard, capability: CAPABILITIES.security_view_overview },
    { titleKey: "securityCenter.userManagement", url: "/app/security-center/users", icon: Users, capability: CAPABILITIES.security_manage_users },
    { titleKey: "securityCenter.rolePermissionMatrix", url: "/app/security-center/roles", icon: Layers, capability: CAPABILITIES.security_manage_roles },
    { titleKey: "securityCenter.securityAudit", url: "/app/security-center/audit", icon: Shield, capability: CAPABILITIES.security_view_audit },
    { titleKey: "securityCenter.apiKeys", url: "/app/security-center/api-keys", icon: Key, capability: CAPABILITIES.security_manage_api_keys },
    { titleKey: "securityCenter.systemStatus", url: "/app/security-center/status", icon: Server, capability: CAPABILITIES.security_view_status },
    { titleKey: "securityCenter.mobileClients", url: "/app/security-center/mobile-clients", icon: Smartphone, capability: CAPABILITIES.security_view_mobile_clients },
    { titleKey: "securityCenter.mobileRelease", url: "/app/security-center/mobile-release", icon: Rocket, capability: CAPABILITIES.security_manage_mobile_release },
]

export function SecurityCenterSidebar() {
    const { t } = useLanguage()
    const pathname = usePathname()
    const auth = useAuth()
    const logout = auth?.logout
    const claims = auth?.customClaims ?? null
    const filteredItems = securityItems.filter((item) => can(claims, item.capability))

    return (
        <Sidebar
            collapsible="icon"
            className="border-r-0"
        >
            <SidebarHeader>
                <SidebarMenu>
                    <SidebarMenuItem>
                        <SidebarMenuButton size="lg" asChild>
                            <Link href="/app/security-center" prefetch={false}>
                                <div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-indigo-600 text-primary-foreground">
                                    <ShieldCheck className="size-4 text-white" />
                                </div>
                                <div className="grid flex-1 text-left text-sm leading-tight">
                                    <span className="truncate font-semibold text-base">{t("securityCenter.title")}</span>
                                    <span className="truncate text-xs text-muted-foreground">{t("securityCenter.subtitle")}</span>
                                </div>
                            </Link>
                        </SidebarMenuButton>
                    </SidebarMenuItem>
                </SidebarMenu>
            </SidebarHeader>
            <SidebarContent>
                <SidebarGroup>
                    <SidebarGroupLabel>{t("securityCenter.title")}</SidebarGroupLabel>
                    <SidebarGroupContent>
                        <SidebarMenu>
                            {filteredItems.map((item) => (
                                <SidebarMenuItem key={item.titleKey}>
                                    <SidebarMenuButton
                                        asChild
                                        tooltip={t(item.titleKey)}
                                        isActive={pathname === item.url}
                                    >
                                        <Link href={item.url} prefetch={false}>
                                            <item.icon />
                                            <span>{t(item.titleKey)}</span>
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
