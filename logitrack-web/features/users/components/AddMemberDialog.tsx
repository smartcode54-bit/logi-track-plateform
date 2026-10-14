"use client";

/**
 * Add a person to an organisation by email: `POST /v1/tenants/{id}/members {email, role, displayName?}` (T62,
 * Appendix B §B.2.4). Go gives the membership to the account that holds the address, or creates the account and
 * mails it an invite (a reset link, R29); the answer is the same in both cases and never says where else the person
 * belongs, so the dialog shows one message for both. Tenant staff add to their active tenant; a platform admin
 * chooses the tenant. `driver` is not offered (a driver works through its drivers profile).
 */
import { useState } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLanguage } from "@/context/language";
import type { MeDTO, TenantRole } from "@/features/auth/api/me";
import { apiErrorText, isApiError } from "@/lib/apiError";
import { addMemberByEmail } from "../api/users";
import { grantableRoles, type UserActions } from "../utils/roles";
import { TenantPicker } from "./pickers";

export function AddMemberDialog({
    open,
    onOpenChange,
    me,
    actions,
    onAdded,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    me: MeDTO | null;
    actions: UserActions;
    onAdded: () => void;
}) {
    const { t } = useLanguage();
    const [email, setEmail] = useState("");
    const [displayName, setDisplayName] = useState("");
    const [role, setRole] = useState<TenantRole>("user");
    // Only a platform admin picks the tenant; tenant staff always add to the tenant active now (read at use time, so a
    // tenant switch while the page stays mounted never sends the add to the tenant left).
    const [pickedTenant, setTenantId] = useState(me?.tenant?.id ?? "");
    const tenantId = actions.platform ? pickedTenant : (me?.tenant?.id ?? "");
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState("");

    const reset = () => {
        setEmail("");
        setDisplayName("");
        setRole("user");
        setTenantId(me?.tenant?.id ?? "");
        setError("");
    };
    const roles = grantableRoles(me, tenantId).filter((r) => r !== "driver");

    const submit = async (e: React.FormEvent) => {
        e.preventDefault();
        setError("");
        if (!email.trim() || !tenantId) {
            setError(t("users.toast.fillRequired"));
            return;
        }
        setSaving(true);
        try {
            await addMemberByEmail(tenantId, { email, role, displayName });
            toast.success(t("users.addMember.done"));
            onAdded();
            reset();
            onOpenChange(false);
        } catch (err) {
            const already = isApiError(err) && err.code === "already_exists" && err.details.reason === "already_member";
            const platformTarget = isApiError(err) && err.code === "permission_denied" && err.details.reason === "platform_target";
            setError(
                already ? t("users.addMember.alreadyMember") : platformTarget ? t("users.account.locked.platform_target") : apiErrorText(err, t),
            );
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
            <DialogContent className="sm:max-w-[460px]">
                <form onSubmit={submit} className="space-y-4">
                    <DialogHeader>
                        <DialogTitle>{t("users.addMember.title")}</DialogTitle>
                        <DialogDescription>{t("users.addMember.description")}</DialogDescription>
                    </DialogHeader>
                    {actions.platform ? (
                        <div className="space-y-1">
                            <Label>{t("users.form.tenant")}</Label>
                            <TenantPicker value={tenantId} onChange={setTenantId} disabled={saving} />
                        </div>
                    ) : null}
                    <div className="space-y-1">
                        <Label htmlFor="add-member-email">{t("users.form.email")}</Label>
                        <Input
                            id="add-member-email"
                            type="email"
                            autoComplete="off"
                            value={email}
                            onChange={(e) => setEmail(e.target.value)}
                            disabled={saving}
                        />
                    </div>
                    <div className="space-y-1">
                        <Label htmlFor="add-member-name">{t("users.addMember.displayName")}</Label>
                        <Input id="add-member-name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} disabled={saving} />
                        <p className="text-xs text-muted-foreground">{t("users.addMember.displayNameHint")}</p>
                    </div>
                    <div className="space-y-1">
                        <Label>{t("users.form.role")}</Label>
                        <Select value={role} onValueChange={(v) => setRole(v as TenantRole)} disabled={saving}>
                            <SelectTrigger aria-label={t("users.form.role")}>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {roles.map((r) => (
                                    <SelectItem key={r} value={r}>
                                        {t(`users.tenantRole.${r}`)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <p className="text-xs text-muted-foreground">{t("users.addMember.privacyHint")}</p>
                    {error ? (
                        <p className="text-sm text-destructive" role="alert">
                            {error}
                        </p>
                    ) : null}
                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                            {t("users.close")}
                        </Button>
                        <Button type="submit" disabled={saving || !email.trim() || !tenantId}>
                            {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                            {t("users.addMember.submit")}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}
