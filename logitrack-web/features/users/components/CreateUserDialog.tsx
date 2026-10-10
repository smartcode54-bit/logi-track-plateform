"use client";

/**
 * Create a user through `POST /v1/users` (T18; Appendix B §B.2.5; R29). No password is typed: Go
 * returns a generated temporary password, shown once, which the user must change at the first sign-in
 * (R79); or, with "send invite", it mails a reset link and returns none. A platform admin chooses the
 * tenant; tenant staff create in their active tenant. `customer` creates a scope-only account on the
 * chosen billing parties; `driver` may link a driver row at once.
 */
import { useState } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLanguage } from "@/context/language";
import type { MeDTO, TenantRole } from "@/features/auth/api/me";
import { apiErrorText } from "@/lib/apiError";
import { createUser, type CreateUserResult } from "../api/users";
import { grantableRoles, type CreateRole, type UserActions } from "../utils/roles";
import { CustomerScopePicker, DriverPicker, TenantPicker } from "./pickers";

export function CreateUserDialog({
    open,
    onOpenChange,
    me,
    actions,
    onCreated,
    fixedTenantId,
    fixedRole,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    me: MeDTO | null;
    actions: UserActions;
    onCreated: (result: CreateUserResult) => void;
    /** Create in this tenant (the tenants page creating a tenant's admin). */
    fixedTenantId?: string;
    /** Only this role (the tenants page: `tenant_admin`). */
    fixedRole?: TenantRole;
}) {
    const { t } = useLanguage();
    const [displayName, setDisplayName] = useState("");
    const [email, setEmail] = useState("");
    const [role, setRole] = useState<CreateRole>(fixedRole ?? "user");
    const [tenantId, setTenantId] = useState(fixedTenantId ?? me?.tenant?.id ?? "");
    const [billingPartyIds, setBillingPartyIds] = useState<string[]>([]);
    const [driverId, setDriverId] = useState("");
    const [driverLabel, setDriverLabel] = useState("");
    const [sendInvite, setSendInvite] = useState(false);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState("");

    const reset = () => {
        setDisplayName("");
        setEmail("");
        setRole(fixedRole ?? "user");
        setTenantId(fixedTenantId ?? me?.tenant?.id ?? "");
        setBillingPartyIds([]);
        setDriverId("");
        setDriverLabel("");
        setSendInvite(false);
        setError("");
    };

    const chooseTenant = !fixedTenantId && actions.platform && role !== "customer";
    const roles: CreateRole[] = fixedRole ? [fixedRole] : [...grantableRoles(me, tenantId), "customer"];
    const needsTenant = role !== "customer" && !tenantId;
    const needsParties = role === "customer" && billingPartyIds.length === 0;

    const submit = async (e: React.FormEvent) => {
        e.preventDefault();
        setError("");
        if (!displayName.trim() || !email.trim()) {
            setError(t("users.toast.fillRequired"));
            return;
        }
        setSaving(true);
        try {
            const result = await createUser({
                email,
                displayName,
                role,
                tenantId: role !== "customer" && (fixedTenantId || actions.platform) ? tenantId || undefined : undefined,
                billingPartyIds: role === "customer" ? billingPartyIds : undefined,
                driverId: role === "driver" && driverId ? driverId : undefined,
                sendInvite,
            });
            toast.success(sendInvite ? t("users.toast.invited") : t("users.toast.created"));
            reset();
            onOpenChange(false);
            onCreated(result);
        } catch (err) {
            setError(apiErrorText(err, t));
        } finally {
            setSaving(false);
        }
    };

    return (
        <Dialog
            open={open}
            onOpenChange={(next) => {
                if (!next) reset();
                onOpenChange(next);
            }}
        >
            <DialogContent className="sm:max-w-[520px] max-h-[90vh] overflow-y-auto">
                <DialogHeader>
                    <DialogTitle>{fixedRole === "tenant_admin" ? t("tenants.admins.createTitle") : t("users.createTitle")}</DialogTitle>
                    <DialogDescription>{t("users.createDesc")}</DialogDescription>
                </DialogHeader>
                <form onSubmit={submit} className="space-y-4" noValidate>
                    <div className="space-y-2">
                        <Label htmlFor="create-name">{t("users.form.displayName")}</Label>
                        <Input id="create-name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} required disabled={saving} />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="create-email">{t("users.form.email")}</Label>
                        <Input id="create-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required disabled={saving} />
                    </div>
                    {!fixedRole ? (
                        <div className="space-y-2">
                            <Label htmlFor="create-role">{t("users.form.role")}</Label>
                            <Select value={role} onValueChange={(v) => setRole(v as CreateRole)} disabled={saving}>
                                <SelectTrigger id="create-role">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent position="popper" className="z-[1005]">
                                    {roles.map((r) => (
                                        <SelectItem key={r} value={r}>
                                            {r === "customer" ? t("users.scopeKind.customer") : t(`users.tenantRole.${r}`)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    ) : null}
                    {chooseTenant ? (
                        <div className="space-y-2">
                            <Label htmlFor="create-tenant">{t("users.form.tenant")}</Label>
                            <TenantPicker id="create-tenant" value={tenantId} onChange={setTenantId} disabled={saving} />
                        </div>
                    ) : null}
                    {role === "customer" ? (
                        <div className="space-y-2">
                            <Label>{t("users.scope.customerTitle")}</Label>
                            <CustomerScopePicker value={billingPartyIds} onChange={setBillingPartyIds} disabled={saving} />
                        </div>
                    ) : null}
                    {role === "driver" && actions.linkDriver ? (
                        <div className="space-y-2 rounded-md border bg-muted/30 p-3">
                            <Label>{t("users.driverLink.title")}</Label>
                            <p className="text-xs text-muted-foreground">{t("users.driverLinkHint")}</p>
                            <DriverPicker
                                value={driverId}
                                onChange={(id, label) => {
                                    setDriverId(id);
                                    setDriverLabel(label);
                                }}
                                disabled={saving}
                            />
                            {driverLabel ? <p className="text-xs">{t("users.driverLinked", { name: driverLabel })}</p> : null}
                        </div>
                    ) : null}
                    <label className="flex items-start gap-2 text-sm">
                        <Checkbox checked={sendInvite} onCheckedChange={(v) => setSendInvite(v === true)} disabled={saving} />
                        <span>
                            {t("users.form.sendInvite")}
                            <span className="block text-xs text-muted-foreground">{t("users.form.sendInviteHint")}</span>
                        </span>
                    </label>
                    {error ? (
                        <p role="alert" className="text-sm text-destructive">
                            {error}
                        </p>
                    ) : null}
                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                            {t("users.form.cancel")}
                        </Button>
                        <Button type="submit" disabled={saving || needsTenant || needsParties}>
                            {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                            {t("users.form.create")}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}
