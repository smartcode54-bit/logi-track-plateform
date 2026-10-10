"use client";

/**
 * Create a carrier tenant (`POST /v1/tenants`) or edit a tenant's names and status
 * (`PATCH /v1/tenants/{id}`) (T18 owner addition; Appendix B §B.2.4). Go creates the tenant's
 * `billing_parties` row in the same transaction and records `tenant_created` / `tenant_updated`.
 *
 * Status is edited for carrier tenants only and sent only when it changed: suspending the own-fleet
 * row would take the `tid` of every staff session at its next refresh (Appendix C §C.4.4), so its
 * status is shown read-only (Go refuses it too, Appendix B §B.2.4 web contract). Suspending a carrier
 * asks for confirmation first.
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
import { ConfirmDialog } from "@/features/users/components/dialogs";
import { apiErrorText } from "@/lib/apiError";
import { createTenant, TENANT_STATUSES, updateTenant, type LegalType, type TenantDTO, type TenantStatus, type UpdateTenantInput } from "../api/tenants";

export function TenantFormDialog({
    tenant,
    open,
    onOpenChange,
    onSaved,
}: {
    /** The tenant to edit; `null` creates a carrier tenant. */
    tenant: TenantDTO | null;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onSaved: (tenant: TenantDTO | null) => void;
}) {
    const { t } = useLanguage();
    const editing = tenant !== null;
    // Only a carrier's status is edited here (see the module comment).
    const statusEditable = tenant?.kind === "carrier";
    const [code, setCode] = useState(tenant?.code ?? "");
    const [nameTh, setNameTh] = useState(tenant?.nameTh ?? "");
    const [nameEn, setNameEn] = useState(tenant?.nameEn ?? "");
    const [legalType, setLegalType] = useState<LegalType>(tenant?.legalType ?? "company");
    const [status, setStatus] = useState<TenantStatus>(tenant?.status ?? "active");
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState("");
    const [confirmSuspend, setConfirmSuspend] = useState(false);

    /** The PATCH body: the names, and `status` only for a carrier whose status changed. */
    const patch = (): UpdateTenantInput => ({
        nameTh,
        nameEn,
        ...(statusEditable && tenant && status !== tenant.status ? { status } : {}),
    });

    const save = async () => {
        setSaving(true);
        try {
            const saved = editing
                ? await updateTenant(tenant.id, patch())
                : await createTenant({ code, nameTh, nameEn, legalType });
            toast.success(editing ? t("tenants.toast.updated") : t("tenants.toast.created"));
            onOpenChange(false);
            onSaved(saved ?? null);
        } catch (err) {
            setError(apiErrorText(err, t));
        } finally {
            setSaving(false);
        }
    };

    const submit = async (e: React.FormEvent) => {
        e.preventDefault();
        setError("");
        if (!nameTh.trim() || (!editing && !code.trim())) {
            setError(t("users.toast.fillRequired"));
            return;
        }
        if (patch().status === "suspended") {
            setConfirmSuspend(true);
            return;
        }
        await save();
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-[480px]">
                <DialogHeader>
                    <DialogTitle>{editing ? t("tenants.editTitle") : t("tenants.createTitle")}</DialogTitle>
                    <DialogDescription>{editing ? t("tenants.editDesc") : t("tenants.createDesc")}</DialogDescription>
                </DialogHeader>
                <form onSubmit={submit} className="space-y-4" noValidate>
                    <div className="space-y-2">
                        <Label htmlFor="tenant-code">{t("tenants.form.code")}</Label>
                        <Input id="tenant-code" value={code} onChange={(e) => setCode(e.target.value)} disabled={saving || editing} required={!editing} maxLength={32} />
                        {editing ? <p className="text-xs text-muted-foreground">{t("tenants.form.codeFixed")}</p> : null}
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="tenant-name-th">{t("tenants.form.nameTh")}</Label>
                        <Input id="tenant-name-th" value={nameTh} onChange={(e) => setNameTh(e.target.value)} disabled={saving} required />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="tenant-name-en">{t("tenants.form.nameEn")}</Label>
                        <Input id="tenant-name-en" value={nameEn} onChange={(e) => setNameEn(e.target.value)} disabled={saving} />
                    </div>
                    {!editing ? (
                        <div className="space-y-2">
                            <Label htmlFor="tenant-legal">{t("tenants.form.legalType")}</Label>
                            <Select value={legalType} onValueChange={(v) => setLegalType(v as LegalType)} disabled={saving}>
                                <SelectTrigger id="tenant-legal">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent position="popper" className="z-[1005]">
                                    <SelectItem value="company">{t("tenants.legalType.company")}</SelectItem>
                                    <SelectItem value="individual">{t("tenants.legalType.individual")}</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>
                    ) : !statusEditable ? (
                        <div className="space-y-2">
                            <Label htmlFor="tenant-status-fixed">{t("tenants.form.status")}</Label>
                            <Input id="tenant-status-fixed" value={t(`tenants.status.${tenant.status}`, tenant.status)} disabled readOnly />
                            <p className="text-xs text-muted-foreground">{t("tenants.form.statusFixed")}</p>
                        </div>
                    ) : (
                        <div className="space-y-2">
                            <Label htmlFor="tenant-status">{t("tenants.form.status")}</Label>
                            <Select value={status} onValueChange={(v) => setStatus(v as TenantStatus)} disabled={saving}>
                                <SelectTrigger id="tenant-status">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent position="popper" className="z-[1005]">
                                    {TENANT_STATUSES.map((s) => (
                                        <SelectItem key={s} value={s}>
                                            {t(`tenants.status.${s}`)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    )}
                    {error ? (
                        <p role="alert" className="text-sm text-destructive">
                            {error}
                        </p>
                    ) : null}
                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                            {t("users.form.cancel")}
                        </Button>
                        <Button type="submit" disabled={saving}>
                            {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                            {editing ? t("users.form.save") : t("tenants.create")}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
            <ConfirmDialog
                open={confirmSuspend}
                title={t("tenants.suspend.title")}
                description={t("tenants.suspend.desc")}
                confirmLabel={t("tenants.suspend.confirm")}
                destructive
                onConfirm={async () => {
                    setConfirmSuspend(false);
                    await save();
                }}
                onCancel={() => setConfirmSuspend(false)}
            />
        </Dialog>
    );
}
