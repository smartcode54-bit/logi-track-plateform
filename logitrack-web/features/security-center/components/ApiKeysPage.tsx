"use client";

/**
 * API key metadata, one-time creation secrets and revocation through Go. Platform reads use
 * projected DTOs; every mutation remains subject to the dedicated server control policy.
 */
import { MaskedValue } from "@/components/masked-value";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Key, Loader2, Plus, ShieldOff } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useMe } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { queryKeys } from "@/lib/queryKeys";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { apiKeyRows, revokeApiKey, useApiKeys, type ApiKeyDTO } from "../api/apiKeys";
import { useSecurityReach } from "../api/reach";
import { ScopeCaption } from "./ScopeCaption";
import { CreateApiKeyDialog } from "./CreateApiKeyDialog";
import { formatDateTime, formatRelative } from "./eventDisplay";

function StatusBadge({ status, t }: { status: string; t: (k: string, f?: string) => string }) {
    const variant = status === "active" ? "success" : "secondary";
    return <Badge variant={variant}>{t(`securityCenter.apiKeys.status.${status}`, status)}</Badge>;
}

export function ApiKeysPage() {
    const { t, language } = useLanguage();
    const client = useQueryClient();
    const me = useMe().data;
    const reach = useSecurityReach();
    const keys = useApiKeys(reach);
    const [creating, setCreating] = useState(false);
    const [revoking, setRevoking] = useState<ApiKeyDTO | null>(null);
    const [busy, setBusy] = useState(false);

    const rows = apiKeyRows(keys.data?.pages);
    const invalidate = () => client.invalidateQueries({ queryKey: queryKeys.apiKeys() });

    const confirmRevoke = async () => {
        if (!revoking) return;
        setBusy(true);
        try {
            await revokeApiKey(revoking.id);
            toast.success(t("securityCenter.apiKeys.revoked", { name: revoking.name }));
            setRevoking(null);
            await invalidate();
        } catch (e) {
            toast.error(`${t("securityCenter.apiKeys.revokeFailed")}: ${apiErrorText(e, t)}`);
        } finally {
            setBusy(false);
        }
    };

    const tenantLabel = (k: ApiKeyDTO) =>
        k.tenantId === null ? t("securityCenter.apiKeys.platform") : (language === "en" && k.tenantNameEn) || k.tenantNameTh || k.tenantId;

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("securityCenter.apiKeys")}
                description={<>{t("securityCenter.apiKeys.subtitle")}<ScopeCaption reach={reach} /></>}
                actions={
                    <Button onClick={() => setCreating(true)} disabled={!me}>
                        <Plus className="mr-2 h-4 w-4 text-muted-foreground" aria-hidden />
                        {t("securityCenter.apiKeys.create")}
                    </Button>
                }
            />

            <Card>
                <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                        <Key className="h-5 w-5 text-muted-foreground" />
                        {t("securityCenter.apiKeys.management")}
                    </CardTitle>
                    <CardDescription>{t("securityCenter.apiKeys.listHint")}</CardDescription>
                </CardHeader>
                <CardContent>
                    {keys.error ? (
                        <Alert variant="destructive">
                            <AlertDescription>{apiErrorText(keys.error, t)}</AlertDescription>
                        </Alert>
                    ) : keys.isPending ? (
                        <div className="flex justify-center py-12">
                            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                        </div>
                    ) : rows.length === 0 ? (
                        <p className="text-sm text-muted-foreground">{t("securityCenter.apiKeys.empty")}</p>
                    ) : (
                        <div className="overflow-x-auto rounded-md border">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t("securityCenter.apiKeys.name")}</TableHead>
                                        <TableHead>{t("securityCenter.apiKeys.scope")}</TableHead>
                                        <TableHead>{t("securityCenter.apiKeys.tenant")}</TableHead>
                                        <TableHead>{t("securityCenter.apiKeys.capabilities")}</TableHead>
                                        <TableHead>{t("securityCenter.apiKeys.statusLabel")}</TableHead>
                                        <TableHead>{t("securityCenter.apiKeys.created")}</TableHead>
                                        <TableHead>{t("securityCenter.apiKeys.lastUsed")}</TableHead>
                                        <TableHead className="text-right">{t("users.table.actions")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {rows.map((k) => (
                                        <TableRow key={k.id} data-testid="api-key-row">
                                            <TableCell>
                                                <span className="font-medium"><MaskedValue value={k.name} source={k} path="name" /></span>
                                                <span className="block font-mono text-xs text-muted-foreground"><MaskedValue value={k.keyPrefix} source={k} path="keyPrefix" format={(v) => `${v}…`} /></span>
                                            </TableCell>
                                            <TableCell>
                                                <Badge variant="secondary">{t(`securityCenter.apiKeys.scopes.${k.scope}`, k.scope)}</Badge>
                                            </TableCell>
                                            <TableCell className="text-sm"><MaskedValue value={tenantLabel(k)} /></TableCell>
                                            <TableCell className="text-xs">
                                                <span title={k.capabilities.join("\n")}>
                                                    {t("securityCenter.apiKeys.capabilityCount", { count: k.capabilities.length })}
                                                </span>
                                            </TableCell>
                                            <TableCell>
                                                <StatusBadge status={k.status} t={t} />
                                                {k.expiresAt ? (
                                                    <span className="block text-xs text-muted-foreground">
                                                        {t("securityCenter.apiKeys.expires", { time: formatDateTime(k.expiresAt, language) })}
                                                    </span>
                                                ) : null}
                                            </TableCell>
                                            <TableCell className="whitespace-nowrap text-xs">
                                                {formatDateTime(k.createdAt, language)}
                                                <span className="block text-muted-foreground"><MaskedValue value={k.createdByEmail} source={k} path="createdByEmail" /></span>
                                            </TableCell>
                                            <TableCell className="whitespace-nowrap text-xs">
                                                {k.lastUsedAt ? formatRelative(k.lastUsedAt, language) : t("securityCenter.apiKeys.never")}
                                            </TableCell>
                                            <TableCell className="text-right">
                                                {k.status !== "revoked" ? (
                                                    <Button variant="ghost" size="sm" onClick={() => setRevoking(k)}>
                                                        <ShieldOff className="mr-1 h-4 w-4 text-muted-foreground" />
                                                        {t("securityCenter.apiKeys.revoke")}
                                                    </Button>
                                                ) : null}
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </div>
                    )}
                    {keys.hasNextPage ? (
                        <div className="mt-4 flex justify-center">
                            <Button variant="outline" size="sm" onClick={() => void keys.fetchNextPage()} disabled={keys.isFetchingNextPage}>
                                {keys.isFetchingNextPage ? <Loader2 className="mr-2 h-4 w-4 animate-spin text-muted-foreground" /> : null}
                                {t("securityCenter.audit.loadMore")}
                            </Button>
                        </div>
                    ) : null}
                </CardContent>
            </Card>

            <CreateApiKeyDialog open={creating} onOpenChange={setCreating} me={me} onCreated={() => void invalidate()} />

            <Dialog open={!!revoking} onOpenChange={(open) => !open && !busy && setRevoking(null)}>
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t("securityCenter.apiKeys.revokeTitle")}</DialogTitle>
                        <DialogDescription>{t("securityCenter.apiKeys.revokeHint")}</DialogDescription>
                    </DialogHeader>
                    {revoking ? (
                        <p className="text-sm">
                            <span className="font-medium"><MaskedValue value={revoking.name} source={revoking} path="name" /></span>{" "}
                            <span className="font-mono text-xs text-muted-foreground"><MaskedValue value={revoking.keyPrefix} source={revoking} path="keyPrefix" format={(v) => `${v}…`} /></span>
                        </p>
                    ) : null}
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setRevoking(null)} disabled={busy}>
                            {t("securityCenter.apiKeys.cancel")}
                        </Button>
                        <Button variant="destructive" onClick={() => void confirmRevoke()} disabled={busy}>
                            {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin text-muted-foreground" /> : null}
                            {t("securityCenter.apiKeys.revokeConfirm")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
