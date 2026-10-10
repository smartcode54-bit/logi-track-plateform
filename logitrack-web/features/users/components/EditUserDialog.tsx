"use client";

/**
 * Role and scope changes of one user (T18; Appendix B §B.2.4, §B.2.5; R50, R86). Each section saves on
 * its own Go route; the user's open tabs get `session.revoked` `claims_changed`, refresh and stay
 * signed in:
 * - tenant role: `PUT /v1/tenants/{tenantId}/members/{userId} {role}`, removal `DELETE` (`users:assign_role`);
 * - customer scope: `PUT|DELETE /v1/users/{id}/scopes/customer {billingPartyIds}` (`users:assign_role`);
 * - driver link: `PUT|DELETE /v1/users/{id}/driver-link {driverId}` (`drivers:edit` + `users:manage`);
 * - platform roles: `POST /v1/users/{id}/platform-roles`, `DELETE .../{role}` (`platform:manage_platform_roles`, not self).
 */
import { useEffect, useMemo, useState } from "react";
import { Link2, Loader2, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { useLanguage } from "@/context/language";
import { PLATFORM_ROLES, type MeDTO, type PlatformRole } from "@/features/auth/api/me";
import { apiErrorText } from "@/lib/apiError";
import { removeMember, setDriverLink, setMemberRole, setPlatformRole, setUserScope, type UserDTO } from "../api/users";
import { grantableRoles, isSelf, type UserActions } from "../utils/roles";
import { CustomerScopePicker, DriverPicker, TenantPicker } from "./pickers";

function sameSet(a: string[], b: string[]): boolean {
    if (a.length !== b.length) return false;
    const s = new Set(a);
    return b.every((x) => s.has(x));
}

export function EditUserDialog({
    user,
    me,
    actions,
    onOpenChange,
    onSaved,
}: {
    user: UserDTO | null;
    me: MeDTO | null;
    actions: UserActions;
    onOpenChange: (open: boolean) => void;
    onSaved: () => void;
}) {
    const { t, language } = useLanguage();
    const [busy, setBusy] = useState<string | null>(null);
    const [roles, setRoles] = useState<Record<string, string>>({});
    const [newTenantId, setNewTenantId] = useState("");
    const [newRole, setNewRole] = useState("user");
    const [parties, setParties] = useState<string[]>([]);
    const [driverId, setDriverId] = useState("");
    const [driverLabel, setDriverLabel] = useState("");
    const [platformRoles, setPlatformRolesState] = useState<PlatformRole[]>([]);

    const userId = user?.id;
    useEffect(() => {
        if (!user) return;
        setRoles(Object.fromEntries(user.memberships.map((m) => [m.tenantId, m.role])));
        setNewTenantId(actions.platform ? "" : (me?.tenant?.id ?? ""));
        setNewRole("user");
        setParties(user.scopes.filter((s) => s.kind === "customer").map((s) => s.billingPartyId));
        setDriverId(user.driver?.id ?? "");
        setDriverLabel(user.driver?.displayName ?? "");
        setPlatformRolesState([...user.platformRoles]);
        // Reset when another user is opened, not on every poll of the list.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [userId]);

    const customerScope = useMemo(() => (user ? user.scopes.filter((s) => s.kind === "customer").map((s) => s.billingPartyId) : []), [user]);

    if (!user) return null;
    const self = isSelf(me, user);
    const memberTenants = new Set(user.memberships.map((m) => m.tenantId));
    // Tenant staff manage memberships of their active tenant; platform admins of any tenant.
    const editable = user.memberships.filter((m) => actions.platform || m.tenantId === me?.tenant?.id);
    const canAdd = actions.assignRole && (actions.platform || (me?.tenant && !memberTenants.has(me.tenant.id)));

    const run = async (key: string, fn: () => Promise<unknown>, okKey: string) => {
        setBusy(key);
        try {
            await fn();
            toast.success(t(okKey));
            onSaved();
        } catch (err) {
            toast.error(apiErrorText(err, t));
        } finally {
            setBusy(null);
        }
    };

    // The roles the caller may grant in that tenant, plus the current one (shown even when not grantable).
    const roleOptions = (tenantId: string, current: string): string[] => {
        const options: string[] = grantableRoles(me, tenantId);
        return options.includes(current) ? options : [...options, current];
    };

    const tenantLabel = (m: UserDTO["memberships"][number]) => (language === "en" && m.tenantNameEn ? m.tenantNameEn : m.tenantNameTh || m.tenantId);

    return (
        <Dialog open onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-[560px] max-h-[90vh] overflow-y-auto">
                <DialogHeader>
                    <DialogTitle>{t("users.editRole")}</DialogTitle>
                    <DialogDescription>
                        <span className="font-medium text-foreground">{user.displayName || user.email}</span>
                        {user.displayName && user.email ? <span className="block">{user.email}</span> : null}
                    </DialogDescription>
                </DialogHeader>

                <section className="space-y-3">
                    <h3 className="text-sm font-semibold">{t("users.edit.membershipsTitle")}</h3>
                    {editable.length === 0 ? <p className="text-xs text-muted-foreground">{t("users.edit.noMemberships")}</p> : null}
                    {editable.map((m) => (
                        <div key={m.tenantId} className="flex flex-wrap items-center gap-2">
                            <span className="min-w-[120px] flex-1 text-sm">{tenantLabel(m)}</span>
                            <Select
                                value={roles[m.tenantId] ?? m.role}
                                onValueChange={(v) => setRoles((r) => ({ ...r, [m.tenantId]: v }))}
                                disabled={!actions.assignRole || self || busy !== null}
                            >
                                <SelectTrigger className="h-8 w-[180px]" aria-label={t("users.form.role")}>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent position="popper" className="z-[1005]">
                                    {roleOptions(m.tenantId, m.role).map((r) => (
                                        <SelectItem key={r} value={r}>
                                            {t(`users.tenantRole.${r}`, r)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                            <Button
                                type="button"
                                size="sm"
                                disabled={!actions.assignRole || self || busy !== null || (roles[m.tenantId] ?? m.role) === m.role}
                                onClick={() => void run(`role-${m.tenantId}`, () => setMemberRole(m.tenantId, user.id, roles[m.tenantId]), "users.toast.roleUpdated")}
                            >
                                {busy === `role-${m.tenantId}` ? <Loader2 className="h-3 w-3 animate-spin" /> : t("users.form.save")}
                            </Button>
                            <Button
                                type="button"
                                size="icon"
                                variant="ghost"
                                aria-label={t("users.edit.removeMembership")}
                                disabled={!actions.assignRole || self || busy !== null}
                                onClick={() => void run(`rm-${m.tenantId}`, () => removeMember(m.tenantId, user.id), "users.toast.membershipRemoved")}
                            >
                                <Trash2 className="h-4 w-4 text-destructive" />
                            </Button>
                        </div>
                    ))}
                    {canAdd && !self ? (
                        <div className="flex flex-wrap items-end gap-2 rounded-md border p-2">
                            {actions.platform ? (
                                <div className="min-w-[180px] flex-1 space-y-1">
                                    <Label className="text-xs">{t("users.form.tenant")}</Label>
                                    <TenantPicker value={newTenantId} onChange={setNewTenantId} disabled={busy !== null} />
                                </div>
                            ) : null}
                            <div className="space-y-1">
                                <Label className="text-xs">{t("users.form.role")}</Label>
                                <Select value={newRole} onValueChange={setNewRole} disabled={busy !== null}>
                                    <SelectTrigger className="h-9 w-[170px]">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent position="popper" className="z-[1005]">
                                        {grantableRoles(me, newTenantId).map((r) => (
                                            <SelectItem key={r} value={r}>
                                                {t(`users.tenantRole.${r}`)}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                            <Button
                                type="button"
                                size="sm"
                                disabled={!newTenantId || memberTenants.has(newTenantId) || busy !== null}
                                onClick={() => void run("add", () => setMemberRole(newTenantId, user.id, newRole), "users.toast.roleUpdated")}
                            >
                                {busy === "add" ? <Loader2 className="h-3 w-3 animate-spin" /> : t("users.edit.addMembership")}
                            </Button>
                        </div>
                    ) : null}
                </section>

                {actions.assignRole ? (
                    <>
                        <Separator />
                        <section className="space-y-2">
                            <h3 className="text-sm font-semibold">{t("users.scope.customerTitle")}</h3>
                            <p className="text-xs text-muted-foreground">{t("users.scope.customerHint")}</p>
                            <CustomerScopePicker value={parties} onChange={setParties} disabled={busy !== null || self} />
                            <Button
                                type="button"
                                size="sm"
                                disabled={busy !== null || self || sameSet(parties, customerScope)}
                                onClick={() => void run("scope", () => setUserScope(user.id, "customer", parties), "users.toast.scopeUpdated")}
                            >
                                {busy === "scope" ? <Loader2 className="h-3 w-3 animate-spin" /> : t("users.form.save")}
                            </Button>
                        </section>
                    </>
                ) : null}

                {actions.linkDriver ? (
                    <>
                        <Separator />
                        <section className="space-y-2">
                            <h3 className="flex items-center gap-1.5 text-sm font-semibold">
                                <Link2 className="h-3.5 w-3.5 text-muted-foreground" />
                                {t("users.driverLink.title")}
                            </h3>
                            <p className="text-xs text-muted-foreground">{t("users.driverLinkHint")}</p>
                            <p className="text-xs">{driverLabel ? t("users.driverLinked", { name: driverLabel }) : t("users.driverLinkNone")}</p>
                            <DriverPicker
                                value={driverId}
                                onChange={(id, label) => {
                                    setDriverId(id);
                                    setDriverLabel(label);
                                }}
                                disabled={busy !== null || self}
                            />
                            <div className="flex gap-2">
                                <Button
                                    type="button"
                                    size="sm"
                                    disabled={busy !== null || self || !driverId || driverId === (user.driver?.id ?? "")}
                                    onClick={() => void run("link", () => setDriverLink(user.id, driverId), "users.driverLinkSaved")}
                                >
                                    {busy === "link" ? <Loader2 className="h-3 w-3 animate-spin" /> : t("users.driverLinkSave")}
                                </Button>
                                {user.driver ? (
                                    <Button
                                        type="button"
                                        size="sm"
                                        variant="outline"
                                        disabled={busy !== null || self}
                                        onClick={() => void run("unlink", () => setDriverLink(user.id, ""), "users.driverLinkCleared")}
                                    >
                                        {t("users.driverLink.unlink")}
                                    </Button>
                                ) : null}
                            </div>
                        </section>
                    </>
                ) : null}

                {actions.platformRoles ? (
                    <>
                        <Separator />
                        <section className="space-y-2">
                            <h3 className="text-sm font-semibold">{t("users.platformRoles.title")}</h3>
                            {self ? <p className="text-xs text-muted-foreground">{t("users.selfNotAllowed")}</p> : null}
                            {PLATFORM_ROLES.map((r) => {
                                const held = user.platformRoles.includes(r);
                                return (
                                    <label key={r} className="flex items-center gap-2 text-sm">
                                        <Checkbox
                                            checked={platformRoles.includes(r)}
                                            disabled={busy !== null || self}
                                            onCheckedChange={(v) => {
                                                const on = v === true;
                                                setPlatformRolesState((cur) => (on ? [...cur, r] : cur.filter((x) => x !== r)));
                                                if (on !== held) void run(`plt-${r}`, () => setPlatformRole(user.id, r, on), "users.toast.platformRoleUpdated");
                                            }}
                                        />
                                        {t(`users.platformRole.${r}`)}
                                        {busy === `plt-${r}` ? <Loader2 className="h-3 w-3 animate-spin" /> : null}
                                    </label>
                                );
                            })}
                        </section>
                    </>
                ) : null}

                <DialogFooter>
                    <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={busy !== null}>
                        {t("users.close")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
