"use client";

import { Link2, Shield, Truck, User, Users } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { useLanguage } from "@/context/language";
import type { UserDTO, UserMembership } from "../api/users";

const ROLE_STYLE: Record<string, string> = {
    tenant_admin: "bg-green-700 hover:bg-green-700",
    manager: "bg-indigo-600 hover:bg-indigo-600",
    operation_staff: "bg-teal-600 hover:bg-teal-600",
    operator: "bg-cyan-600 hover:bg-cyan-600",
    driver: "bg-amber-600 hover:bg-amber-600",
};

function membershipName(m: UserMembership, language: string): string {
    if (language === "en" && m.tenantNameEn) return m.tenantNameEn;
    return m.tenantNameTh || m.tenantNameEn || m.tenantId;
}

/** Tenant roles (with the tenant's name when it is not the viewer's active tenant) and platform roles. */
export function UserRoleBadges({ user, activeTenantId }: { user: UserDTO; activeTenantId?: string }) {
    const { t, language } = useLanguage();
    return (
        <div className="flex flex-wrap gap-1">
            {user.platformRoles.map((r) => (
                <Badge key={r} className="bg-rose-700 hover:bg-rose-700">
                    <Shield className="mr-1 h-3 w-3" />
                    {t(`users.platformRole.${r}`)}
                </Badge>
            ))}
            {user.memberships.map((m) => {
                const Icon = m.role === "tenant_admin" ? Shield : m.role === "driver" ? Truck : User;
                const style = ROLE_STYLE[m.role];
                return (
                    <Badge key={m.tenantId} className={style} variant={style ? "default" : "outline"}>
                        <Icon className="mr-1 h-3 w-3" />
                        {t(`users.tenantRole.${m.role}`, m.role)}
                        {m.tenantId !== activeTenantId ? <span className="ml-1 opacity-80">· {membershipName(m, language)}</span> : null}
                    </Badge>
                );
            })}
            {user.memberships.length === 0 && user.platformRoles.length === 0 && user.scopes.length === 0 ? (
                <Badge variant="outline">{t("users.noRole")}</Badge>
            ) : null}
        </div>
    );
}

/** Customer scopes, dispatcher grant and driver link. */
export function UserScopeCell({ user }: { user: UserDTO }) {
    const { t } = useLanguage();
    const customers = user.scopes.filter((s) => s.kind === "customer");
    const dispatcher = user.scopes.filter((s) => s.kind === "dispatcher");
    const driverRole = user.memberships.some((m) => m.role === "driver");
    const parts: React.ReactNode[] = [];
    if (customers.length > 0) {
        parts.push(
            <span key="customer" className="flex items-center gap-1 text-xs">
                <Users className="h-3 w-3 text-blue-600" />
                {t("users.scopeKind.customer")}: {customers.map((s) => s.name).join(", ")}
            </span>
        );
    }
    if (dispatcher.length > 0) {
        parts.push(
            <span key="dispatcher" className="flex items-center gap-1 text-xs">
                <Users className="h-3 w-3 text-purple-600" />
                {t("users.scopeKind.dispatcher")}: {dispatcher.map((s) => s.name).join(", ")}
            </span>
        );
    }
    if (user.driver) {
        parts.push(
            <span key="driver" className="flex items-center gap-1 text-xs">
                <Link2 className="h-3 w-3 text-green-600" />
                {user.driver.displayName || user.driver.id}
            </span>
        );
    } else if (driverRole) {
        parts.push(
            <span key="driver" className="flex items-center gap-1 text-xs text-amber-600">
                <Link2 className="h-3 w-3" />
                {t("users.driverLinkNone")}
            </span>
        );
    }
    if (parts.length === 0) return <span className="text-xs text-muted-foreground">—</span>;
    return <div className="flex flex-col gap-1">{parts}</div>;
}

/** The account state: active, disabled, needs a reset; plus "must change password". */
export function UserStatusBadge({ user }: { user: UserDTO }) {
    const { t } = useLanguage();
    const active = user.status === "active";
    return (
        <div className="flex flex-col gap-1">
            <span
                className={`inline-flex w-fit items-center rounded-full border px-2.5 py-1 text-xs font-medium ${
                    active ? "border-green-200/50 bg-green-500/10 text-green-600" : "border-gray-200 bg-gray-100 text-gray-500"
                }`}
            >
                <span className={`mr-1.5 h-1.5 w-1.5 rounded-full ${active ? "bg-green-500" : "bg-gray-400"}`} />
                {t(`users.accountStatus.${user.status}`, user.status)}
            </span>
            {user.mustChangePassword ? <span className="text-[11px] text-amber-600">{t("users.mustChangePassword")}</span> : null}
        </div>
    );
}
