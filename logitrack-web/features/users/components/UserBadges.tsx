"use client";

import { Link2, Shield, Truck, User, Users } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { useLanguage } from "@/context/language";
import { toneBadgeVariant } from "@/lib/statusPresentation";
import type { UserDTO, UserMembership } from "../api/users";

function membershipName(m: UserMembership, language: string): string {
    if (language === "en" && m.tenantNameEn) return m.tenantNameEn;
    return m.tenantNameTh || m.tenantNameEn || m.tenantId;
}

/** Body wording; the shared role label also names the principal in navigation. */
export function organisationRoleLabel(role: string, t: (key: string, fallback?: string) => string): string {
    return role === "tenant_admin" ? t("users.organisationAdmin") : t(`users.tenantRole.${role}`, role);
}

/** Tenant roles (with the tenant's name when it is not the viewer's active tenant) and platform roles. */
export function UserRoleBadges({ user, activeTenantId, showOrganisation = false }: { user: UserDTO; activeTenantId?: string; showOrganisation?: boolean }) {
    const { t, language } = useLanguage();
    return (
        <div className="flex flex-wrap gap-1">
            {user.platformRoles.map((r) => (
                <Badge key={r} variant="secondary">
                    <Shield className="mr-1 h-3 w-3 text-muted-foreground" />
                    {t(`users.platformRole.${r}`)}
                </Badge>
            ))}
            {user.memberships.map((m) => {
                const Icon = m.role === "tenant_admin" ? Shield : m.role === "driver" ? Truck : User;
                return (
                    <Badge key={m.tenantId} variant="outline">
                        <Icon className="mr-1 h-3 w-3 text-muted-foreground" />
                        {organisationRoleLabel(m.role, t)}
                        {showOrganisation || m.tenantId !== activeTenantId ? <span className="ml-1 opacity-80">· {membershipName(m, language)}</span> : null}
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
                <Users className="h-3 w-3 text-muted-foreground" />
                {t("users.scopeKind.customer")}: {customers.map((s) => s.name).join(", ")}
            </span>
        );
    }
    if (dispatcher.length > 0) {
        parts.push(
            <span key="dispatcher" className="flex items-center gap-1 text-xs">
                <Users className="h-3 w-3 text-muted-foreground" />
                {t("users.scopeKind.dispatcher")}: {dispatcher.map((s) => s.name).join(", ")}
            </span>
        );
    }
    if (user.driver) {
        parts.push(
            <span key="driver" className="flex items-center gap-1 text-xs">
                <Link2 className="h-3 w-3 text-muted-foreground" />
                {user.driver.displayName || user.driver.id}
            </span>
        );
    } else if (driverRole) {
        parts.push(
            <span key="driver" className="flex items-center gap-1 text-xs text-warning">
                <Link2 className="h-3 w-3 text-muted-foreground" />
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
    const tone = user.status === "active" ? "success" : user.status === "reset_required" ? "warning" : "neutral";
    return (
        <div className="flex flex-col gap-1">
            <Badge variant={toneBadgeVariant(tone)} className="w-fit">
                {t(`users.accountStatus.${user.status}`, user.status)}
            </Badge>
            {user.mustChangePassword ? <span className="text-xs text-warning">{t("users.mustChangePassword")}</span> : null}
        </div>
    );
}
