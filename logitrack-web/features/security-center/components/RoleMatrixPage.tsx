"use client";

/**
 * The role & permission matrix over Go (T54S; developer-spec.md §10.13; Appendix C §C.2.5; Appendix E
 * §E.2.4 row `/app/security-center/roles`). The matrix of a layer loads in one call
 * (`GET /v1/roles/matrix`) with, per role and key, the catalog default, the effective value, whether
 * the layer overrides it and whether it may; a save sends the layer's complete override set
 * (`PUT /v1/roles/matrix`), which Go writes with `role_matrix_saved` and `roles.changed` in one
 * transaction. Rows are the catalog keys a tenant role can hold (shared-docs/schemas/capabilities.ts,
 * generated from Go), columns the six tenant roles; `tenant_admin` and the cells the layer cannot
 * override are read-only. A platform_admin picks the layer: platform-wide or any tenant. A save reads
 * the layer again first and stops, keeping the edits over the refreshed matrix, when someone else
 * changed it meanwhile (`saveRoleMatrixFresh`): the PUT replaces the whole set.
 */
import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Info, Loader2, Search } from "lucide-react";
import { toast } from "sonner";

import { CAPABILITY_CATALOG, type CapabilityInfo } from "shared-docs/schemas/capabilities";
import { useLanguage } from "@/context/language";
import { isPlatformAdmin } from "@/features/auth/api/me";
import { useCan, useMe } from "@/features/auth/api/useMe";
import { useAllTenants } from "@/features/tenants/api/tenants";
import { apiErrorText } from "@/lib/apiError";
import { queryKeys } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

import { useSecurityReach } from "../api/reach";
import { useUsersByRole } from "../api/overview";
import {
    cellValue,
    changedCells,
    MATRIX_ROLES,
    MatrixChangedError,
    saveRoleMatrixFresh,
    useRoleMatrix,
    type MatrixEdits,
    type MatrixTarget,
    type RoleMatrixDTO,
} from "../api/roleMatrix";

/** Tabs of the matrix: catalog modules grouped as the Firestore page grouped its capabilities. */
export const MATRIX_GROUPS: readonly { id: string; modules: readonly string[] }[] = [
    { id: "operations", modules: ["operations", "drivers", "chat", "broadcasts"] },
    { id: "fleet", modules: ["fleet"] },
    { id: "financials", modules: ["accounting", "reporting"] },
    { id: "hr", modules: ["hr"] },
    { id: "access", modules: ["users", "security", "company", "waitlist", "packages"] },
    { id: "driverApp", modules: ["mobile"] },
];

/** The keys of the loaded matrix in catalog order, grouped by tab. */
function groupedKeys(matrix: RoleMatrixDTO): Map<string, CapabilityInfo[]> {
    const present = new Set(Object.keys(matrix.roles[0]?.capabilities ?? {}));
    const out = new Map<string, CapabilityInfo[]>();
    for (const g of MATRIX_GROUPS) out.set(g.id, CAPABILITY_CATALOG.filter((c) => present.has(c.key) && g.modules.includes(c.module)));
    return out;
}

const PLATFORM_VALUE = "platform";

export function RoleMatrixPage() {
    const { t, language } = useLanguage();
    const client = useQueryClient();
    const me = useMe().data;
    const platformAdmin = isPlatformAdmin(me);
    const reach = useSecurityReach();
    const canViewOverview = useCan("security:view_overview").allowed;
    const tenants = useAllTenants(platformAdmin);
    const ownTenant = me?.tenant?.id ?? "";
    // A platform admin picks the layer; everyone else edits its effective tenant.
    const [picked, setPicked] = useState<string | null>(null);
    const layerValue = picked ?? (ownTenant || PLATFORM_VALUE);
    const target: MatrixTarget = !platformAdmin
        ? { layer: "default" }
        : layerValue === PLATFORM_VALUE
          ? { layer: "platform" }
          : { layer: "tenant", tenantId: layerValue };

    const matrix = useRoleMatrix(target, me !== undefined);
    const counts = useUsersByRole(reach, canViewOverview);
    const [edits, setEdits] = useState<MatrixEdits>({});
    const [search, setSearch] = useState("");
    const save = useMutation({
        mutationFn: (m: RoleMatrixDTO) => saveRoleMatrixFresh(client, target, m, edits),
        meta: { invalidates: [queryKeys.roles.all(), queryKeys.me()] },
        onSuccess: (res) => {
            setEdits({});
            toast.success(t("securityCenter.roles.savedChanges", { count: res.changes }));
        },
        onError: (e) =>
            e instanceof MatrixChangedError
                ? toast.warning(t("securityCenter.roles.changedElsewhere"))
                : toast.error(`${t("securityCenter.roles.failedSave")}: ${apiErrorText(e, t)}`),
    });

    const m = matrix.data;
    const groups = useMemo(() => (m ? groupedKeys(m) : new Map<string, CapabilityInfo[]>()), [m]);
    const pending = m ? changedCells(m, edits).length : 0;
    const title = (c: CapabilityInfo) => (language === "th" ? c.titleTh : c.titleEn);
    const q = search.trim().toLowerCase();
    const matches = (c: CapabilityInfo) => !q || title(c).toLowerCase().includes(q) || c.key.includes(q);

    const toggle = (role: string, key: string, value: boolean) =>
        setEdits((prev) => ({ ...prev, [role]: { ...prev[role], [key]: value } }));

    const switchLayer = (value: string) => {
        if (pending > 0 && !window.confirm(t("securityCenter.roles.discardOnSwitch"))) return;
        setEdits({});
        setPicked(value);
    };

    return (
        <div className="p-6 space-y-6">
            <div className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("securityCenter.roles.title")}</h1>
                    <p className="mt-1 text-muted-foreground">{t("securityCenter.roles.subtitleGo")}</p>
                </div>
                {platformAdmin ? (
                    <div className="w-full max-w-xs space-y-1">
                        <p className="text-xs font-medium text-muted-foreground">{t("securityCenter.roles.layer")}</p>
                        <Select value={layerValue} onValueChange={switchLayer}>
                            <SelectTrigger aria-label={t("securityCenter.roles.layer")}>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent className="z-[1005]" position="popper">
                                <SelectItem value={PLATFORM_VALUE}>{t("securityCenter.roles.layerPlatform")}</SelectItem>
                                {(tenants.data ?? [])
                                    .filter((tn) => tn.kind !== "quarantine")
                                    .map((tn) => (
                                        <SelectItem key={tn.id} value={tn.id}>
                                            {(language === "en" && tn.nameEn) || tn.nameTh}
                                        </SelectItem>
                                    ))}
                                {ownTenant && !(tenants.data ?? []).some((tn) => tn.id === ownTenant) ? (
                                    <SelectItem value={ownTenant}>{me?.tenant?.nameTh ?? ownTenant}</SelectItem>
                                ) : null}
                            </SelectContent>
                        </Select>
                    </div>
                ) : null}
            </div>

            {canViewOverview && counts.data ? (
                <div className="grid gap-4 sm:grid-cols-3 lg:grid-cols-6" data-testid="role-counts">
                    {MATRIX_ROLES.map((role) => {
                        const n = counts.data.tenantRoles[role] ?? 0;
                        const max = Math.max(1, ...MATRIX_ROLES.map((r) => counts.data.tenantRoles[r] ?? 0));
                        return (
                            <Card key={role}>
                                <CardContent className="pb-2 pt-6">
                                    <span className="text-2xl font-bold tabular-nums">{n.toLocaleString()}</span>
                                    <p className="mt-1 text-sm font-medium text-muted-foreground">{t(`users.tenantRole.${role}`)}</p>
                                    <div className="mt-3 h-1 overflow-hidden rounded-full bg-primary/20">
                                        <div className="h-full rounded-full bg-primary" style={{ width: `${Math.min(100, (n / max) * 100)}%` }} />
                                    </div>
                                </CardContent>
                            </Card>
                        );
                    })}
                </div>
            ) : null}

            {matrix.error ? (
                <Alert variant="destructive">
                    <AlertDescription>
                        {t("securityCenter.roles.failedLoad")}: {apiErrorText(matrix.error, t)}
                    </AlertDescription>
                </Alert>
            ) : null}

            {!m ? (
                matrix.isPending ? (
                    <div className="flex min-h-[300px] items-center justify-center">
                        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                    </div>
                ) : null
            ) : (
                <>
                    <p className="text-sm text-muted-foreground" data-testid="matrix-layer">
                        {m.layer === "platform"
                            ? t("securityCenter.roles.layerPlatformHint")
                            : m.tenantKind === "own_fleet"
                              ? t("securityCenter.roles.layerOwnFleetHint")
                              : t("securityCenter.roles.layerCarrierHint")}
                    </p>
                    <div className="relative max-w-sm">
                        <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                        <Input
                            className="pl-9"
                            placeholder={t("securityCenter.roles.searchPermissions")}
                            aria-label={t("securityCenter.roles.searchPermissions")}
                            value={search}
                            onChange={(e) => setSearch(e.target.value)}
                        />
                    </div>
                    <Tabs defaultValue={MATRIX_GROUPS[0].id} className="space-y-4">
                        <TabsList className="h-auto flex-wrap justify-start gap-0 rounded-none border-b border-border/50 bg-transparent p-0">
                            {MATRIX_GROUPS.map((g) => (
                                <TabsTrigger
                                    key={g.id}
                                    value={g.id}
                                    className={cn(
                                        "rounded-none border-b-2 border-transparent bg-transparent px-4 pb-3 pt-2 font-semibold text-muted-foreground",
                                        "data-[state=active]:border-primary data-[state=active]:text-primary data-[state=active]:shadow-none"
                                    )}
                                >
                                    {t(`securityCenter.roles.group.${g.id}`)}
                                </TabsTrigger>
                            ))}
                        </TabsList>
                        {MATRIX_GROUPS.map((g) => (
                            <TabsContent key={g.id} value={g.id} className="mt-0">
                                <Card>
                                    <div className="overflow-x-auto">
                                        <Table>
                                            <TableHeader>
                                                <TableRow className="bg-muted/50 hover:bg-muted/50">
                                                    <TableHead className="w-[320px] font-semibold">{t("securityCenter.roles.capabilities")}</TableHead>
                                                    {MATRIX_ROLES.map((r) => (
                                                        <TableHead key={r} className="w-24 text-center font-semibold">
                                                            {t(`users.tenantRole.${r}`)}
                                                        </TableHead>
                                                    ))}
                                                </TableRow>
                                            </TableHeader>
                                            <TableBody>
                                                {(groups.get(g.id) ?? []).filter(matches).map((c) => (
                                                    <TableRow key={c.key} data-testid="matrix-row" data-key={c.key}>
                                                        <TableCell className="align-top">
                                                            <p className="font-medium">{title(c)}</p>
                                                            <p className="font-mono text-xs text-muted-foreground">{c.key}</p>
                                                        </TableCell>
                                                        {MATRIX_ROLES.map((role) => {
                                                            const cell = m.roles.find((r) => r.role === role)?.capabilities[c.key];
                                                            if (!cell) return <TableCell key={role} />;
                                                            const value = cellValue(m, edits, role, c.key);
                                                            const edited = value !== cell.effective;
                                                            const label = `${t(`users.tenantRole.${role}`)}: ${title(c)}`;
                                                            return (
                                                                <TableCell key={role} className="py-4 text-center align-top">
                                                                    <div className="flex flex-col items-center gap-1">
                                                                        <Checkbox
                                                                            aria-label={label}
                                                                            checked={value}
                                                                            disabled={!cell.overridable || save.isPending}
                                                                            onCheckedChange={(checked) => toggle(role, c.key, checked === true)}
                                                                            className={cn(
                                                                                "h-5 w-5 data-[state=checked]:border-primary data-[state=checked]:bg-primary",
                                                                                edited && "ring-2 ring-amber-400 ring-offset-1"
                                                                            )}
                                                                        />
                                                                        {cell.overridden ? (
                                                                            <span className="text-[10px] text-primary" title={t("securityCenter.roles.overriddenHint")}>
                                                                                {t("securityCenter.roles.overridden")}
                                                                            </span>
                                                                        ) : cell.platformOverride !== undefined ? (
                                                                            <span className="text-[10px] text-muted-foreground" title={t("securityCenter.roles.platformOverrideHint")}>
                                                                                {t("securityCenter.roles.platformOverride")}
                                                                            </span>
                                                                        ) : null}
                                                                    </div>
                                                                </TableCell>
                                                            );
                                                        })}
                                                    </TableRow>
                                                ))}
                                            </TableBody>
                                        </Table>
                                    </div>
                                </Card>
                            </TabsContent>
                        ))}
                    </Tabs>

                    <div className="flex flex-col items-start justify-between gap-4 pt-4 sm:flex-row sm:items-center">
                        <div className="flex items-center gap-2 rounded-lg bg-primary/10 px-4 py-3 text-sm text-primary">
                            <Info className="h-4 w-4 shrink-0" />
                            <span>{t("securityCenter.roles.changesAppliedGo")}</span>
                        </div>
                        <div className="flex shrink-0 items-center gap-2">
                            {pending > 0 ? (
                                <span className="text-sm text-muted-foreground" data-testid="pending-changes">
                                    {t("securityCenter.roles.pendingChanges", { count: pending })}
                                </span>
                            ) : null}
                            <Button variant="ghost" onClick={() => setEdits({})} disabled={pending === 0 || save.isPending} className="text-muted-foreground">
                                {t("securityCenter.roles.discardChanges")}
                            </Button>
                            <Button onClick={() => save.mutate(m)} disabled={pending === 0 || save.isPending}>
                                {save.isPending ? (
                                    <>
                                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                        {t("securityCenter.roles.saving")}
                                    </>
                                ) : (
                                    t("securityCenter.roles.saveConfiguration")
                                )}
                            </Button>
                        </div>
                    </div>
                </>
            )}
        </div>
    );
}
