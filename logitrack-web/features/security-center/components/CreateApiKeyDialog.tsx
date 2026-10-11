"use client";

/**
 * Create an API key (T54S; `POST /v1/api-keys`, T32K, Appendix C §C.4.11, R49, R82). The secret is in
 * the answer of this one request: it lives in this dialog's state only (no query or mutation cache,
 * no storage) and is dropped when the dialog closes. The capability list offers only what Go would
 * accept for the scope and the key's kind (tenant or platform) and, for a caller that is not a
 * platform_admin, only keys the caller holds.
 */
import { useMemo, useState } from "react";
import { Check, Copy, Loader2, TriangleAlert } from "lucide-react";

import { useLanguage } from "@/context/language";
import { isPlatformAdmin, type MeDTO } from "@/features/auth/api/me";
import { useAllTenants } from "@/features/tenants/api/tenants";
import { apiErrorText } from "@/lib/apiError";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import {
    API_KEY_SCOPES,
    createApiKey,
    expiresAtFromInput,
    grantableCapabilities,
    PLATFORM_ONLY_SCOPES,
    RELEASE_PUBLISHER_CAPABILITY,
    type ApiKeyScope,
    type CreatedApiKeyDTO,
} from "../api/apiKeys";

const MAX_NAME = 100;

export function CreateApiKeyDialog({
    open,
    onOpenChange,
    me,
    onCreated,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    me: MeDTO | null | undefined;
    onCreated: () => void;
}) {
    const { t, language } = useLanguage();
    const platformAdmin = isPlatformAdmin(me);
    const tenants = useAllTenants(open && platformAdmin);
    const [name, setName] = useState("");
    const [scope, setScope] = useState<ApiKeyScope>("integration");
    const [platformKeyChoice, setPlatformKeyChoice] = useState(false);
    const [tenantChoice, setTenantChoice] = useState("");
    const [caps, setCaps] = useState<string[]>([]);
    const [expires, setExpires] = useState("");
    const [error, setError] = useState<string | null>(null);
    const [submitting, setSubmitting] = useState(false);
    const [created, setCreated] = useState<CreatedApiKeyDTO | null>(null);
    const [copied, setCopied] = useState(false);

    const platformKey = platformAdmin && (platformKeyChoice || PLATFORM_ONLY_SCOPES.includes(scope));
    const tenantId = platformKey ? null : platformAdmin ? tenantChoice || me?.tenant?.id || "" : (me?.tenant?.id ?? "");
    const grantable = useMemo(
        () => grantableCapabilities({ scope, platformKey, platformAdmin, held: me?.capabilities ?? [] }),
        [scope, platformKey, platformAdmin, me?.capabilities]
    );
    const selected = caps.filter((c) => grantable.some((g) => g.key === c));
    const nameError = !name.trim() ? "required" : name.trim().length > MAX_NAME ? "tooLong" : null;
    const canSubmit = !nameError && selected.length > 0 && (platformKey || !!tenantId) && !submitting;

    const reset = () => {
        setName("");
        setScope("integration");
        setPlatformKeyChoice(false);
        setTenantChoice("");
        setCaps([]);
        setExpires("");
        setError(null);
        setCreated(null);
        setCopied(false);
    };

    const close = (next: boolean) => {
        if (next || submitting) return;
        // The secret leaves memory with the dialog.
        reset();
        onOpenChange(false);
    };

    const changeScope = (value: string) => {
        const s = value as ApiKeyScope;
        setScope(s);
        if (s === "release_publisher") setCaps([RELEASE_PUBLISHER_CAPABILITY]);
    };

    const toggleCap = (key: string, on: boolean) => setCaps((prev) => (on ? [...prev.filter((c) => c !== key), key] : prev.filter((c) => c !== key)));

    const submit = async () => {
        if (!canSubmit) return;
        setSubmitting(true);
        setError(null);
        try {
            const key = await createApiKey({ name, scope, capabilities: selected, expiresAt: expiresAtFromInput(expires), tenantId });
            setCreated(key);
            onCreated();
        } catch (e) {
            setError(apiErrorText(e, t));
        } finally {
            setSubmitting(false);
        }
    };

    const copy = async () => {
        if (!created) return;
        try {
            await navigator.clipboard.writeText(created.secret);
            setCopied(true);
        } catch {
            setCopied(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={close}>
            <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                    <DialogTitle>{created ? t("securityCenter.apiKeys.createdTitle") : t("securityCenter.apiKeys.createTitle")}</DialogTitle>
                    <DialogDescription>{created ? t("securityCenter.apiKeys.createdHint") : t("securityCenter.apiKeys.createHint")}</DialogDescription>
                </DialogHeader>

                {created ? (
                    <div className="space-y-3">
                        <Alert>
                            <TriangleAlert className="h-4 w-4" />
                            <AlertDescription>{t("securityCenter.apiKeys.secretOnce")}</AlertDescription>
                        </Alert>
                        <div className="flex items-center gap-2">
                            <code className="flex-1 break-all rounded-md border bg-muted px-3 py-2 font-mono text-sm" data-testid="api-key-secret">
                                {created.secret}
                            </code>
                            <Button type="button" variant="outline" size="icon" onClick={() => void copy()} aria-label={t("securityCenter.apiKeys.copy")}>
                                {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                            </Button>
                        </div>
                        <p className="text-xs text-muted-foreground">
                            {t("securityCenter.apiKeys.createdSummary", { name: created.name, prefix: created.keyPrefix, scope: created.scope })}
                        </p>
                    </div>
                ) : (
                    <div className="max-h-[65vh] space-y-4 overflow-y-auto pr-1">
                        <div className="space-y-2">
                            <Label htmlFor="api-key-name">{t("securityCenter.apiKeys.name")}</Label>
                            <Input id="api-key-name" value={name} maxLength={MAX_NAME + 20} onChange={(e) => setName(e.target.value)} autoComplete="off" />
                            {name && nameError === "tooLong" ? <p className="text-xs text-red-500">{t("securityCenter.apiKeys.nameTooLong", { max: MAX_NAME })}</p> : null}
                        </div>
                        <div className="grid gap-4 sm:grid-cols-2">
                            <div className="space-y-2">
                                <Label htmlFor="api-key-scope">{t("securityCenter.apiKeys.scope")}</Label>
                                <Select value={scope} onValueChange={changeScope}>
                                    <SelectTrigger id="api-key-scope" aria-label={t("securityCenter.apiKeys.scope")}>
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent className="z-[1005]" position="popper">
                                        {API_KEY_SCOPES.filter((s) => platformAdmin || !PLATFORM_ONLY_SCOPES.includes(s)).map((s) => (
                                            <SelectItem key={s} value={s}>
                                                {t(`securityCenter.apiKeys.scopes.${s}`)}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                                <p className="text-xs text-muted-foreground">{t(`securityCenter.apiKeys.scopeHint.${scope}`)}</p>
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor="api-key-expires">{t("securityCenter.apiKeys.expiresAt")}</Label>
                                <Input id="api-key-expires" type="datetime-local" value={expires} onChange={(e) => setExpires(e.target.value)} />
                                <p className="text-xs text-muted-foreground">{t("securityCenter.apiKeys.expiresHint")}</p>
                            </div>
                        </div>
                        {platformAdmin ? (
                            <div className="space-y-2">
                                <div className="flex items-center gap-2">
                                    <Checkbox
                                        id="api-key-platform"
                                        checked={platformKey}
                                        disabled={PLATFORM_ONLY_SCOPES.includes(scope)}
                                        onCheckedChange={(v) => setPlatformKeyChoice(v === true)}
                                    />
                                    <Label htmlFor="api-key-platform">{t("securityCenter.apiKeys.platformKey")}</Label>
                                </div>
                                {!platformKey ? (
                                    <Select value={tenantChoice || me?.tenant?.id || ""} onValueChange={setTenantChoice}>
                                        <SelectTrigger aria-label={t("securityCenter.apiKeys.tenant")}>
                                            <SelectValue placeholder={t("securityCenter.apiKeys.pickTenant")} />
                                        </SelectTrigger>
                                        <SelectContent className="z-[1005]" position="popper">
                                            {(tenants.data ?? [])
                                                .filter((tn) => tn.kind !== "quarantine" && tn.status === "active")
                                                .map((tn) => (
                                                    <SelectItem key={tn.id} value={tn.id}>
                                                        {(language === "en" && tn.nameEn) || tn.nameTh}
                                                    </SelectItem>
                                                ))}
                                        </SelectContent>
                                    </Select>
                                ) : (
                                    <p className="text-xs text-muted-foreground">{t("securityCenter.apiKeys.platformKeyHint")}</p>
                                )}
                            </div>
                        ) : null}
                        <div className="space-y-2">
                            <Label>{t("securityCenter.apiKeys.capabilities")}</Label>
                            {grantable.length === 0 ? (
                                <p className="text-sm text-muted-foreground">{t("securityCenter.apiKeys.noGrantable")}</p>
                            ) : (
                                <div className="grid max-h-56 gap-2 overflow-y-auto rounded-md border p-3 sm:grid-cols-2" data-testid="api-key-capabilities">
                                    {grantable.map((c) => (
                                        <label key={c.key} className="flex items-start gap-2 text-sm">
                                            <Checkbox
                                                checked={selected.includes(c.key)}
                                                disabled={scope === "release_publisher"}
                                                onCheckedChange={(v) => toggleCap(c.key, v === true)}
                                                aria-label={c.key}
                                            />
                                            <span>
                                                {language === "th" ? c.titleTh : c.titleEn}
                                                <span className="block font-mono text-xs text-muted-foreground">{c.key}</span>
                                            </span>
                                        </label>
                                    ))}
                                </div>
                            )}
                        </div>
                        {error ? (
                            <Alert variant="destructive">
                                <AlertDescription>{error}</AlertDescription>
                            </Alert>
                        ) : null}
                    </div>
                )}

                <DialogFooter>
                    {created ? (
                        <Button type="button" onClick={() => close(false)}>
                            {t("securityCenter.apiKeys.done")}
                        </Button>
                    ) : (
                        <>
                            <Button type="button" variant="outline" onClick={() => close(false)} disabled={submitting}>
                                {t("securityCenter.apiKeys.cancel")}
                            </Button>
                            <Button type="button" onClick={() => void submit()} disabled={!canSubmit}>
                                {submitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                                {t("securityCenter.apiKeys.create")}
                            </Button>
                        </>
                    )}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
