"use client";

/**
 * Create a carrier tenant (`POST /v1/tenants`) or edit a tenant's names and status
 * (`PATCH /v1/tenants/{id}`) (T18 owner addition; Appendix B §B.2.4). Go creates the tenant's
 * `billing_parties` row in the same transaction and records `tenant_created` / `tenant_updated`.
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
import { apiErrorText } from "@/lib/apiError";
import { createTenant, TENANT_STATUSES, updateTenant, type LegalType, type TenantDTO, type TenantStatus } from "../api/tenants";

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
    const [code, setCode] = useState(tenant?.code ?? "");
    const [nameTh, setNameTh] = useState(tenant?.nameTh ?? "");
    const [nameEn, setNameEn] = useState(tenant?.nameEn ?? "");
    const [legalType, setLegalType] = useState<LegalType>(tenant?.legalType ?? "company");
    const [status, setStatus] = useState<TenantStatus>(tenant?.status ?? "active");
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState("");

    const submit = async (e: React.FormEvent) => {
        e.preventDefault();
        setError("");
        if (!nameTh.trim() || (!editing && !code.trim())) {
            setError(t("users.toast.fillRequired"));
            return;
        }
        setSaving(true);
        try {
            const saved = editing
                ? await updateTenant(tenant.id, { nameTh, nameEn, status })
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
        </Dialog>
    );
}
