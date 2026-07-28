"use client";

import { useEffect, useState } from "react";
import {
    collectionGroup,
    limit,
    onSnapshot,
    orderBy,
    query,
    where,
    type Query,
    Timestamp,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { usePermission } from "@/hooks/usePermission";
import { CAPABILITIES } from "@/lib/capabilities";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Loader2, Smartphone } from "lucide-react";
import { countVersionStatuses, getVersionStatus, type VersionStatus } from "@/lib/mobileVersion";
import { subscribeMobileAppSettings } from "@/features/mobile-release/api/mobileAppSettings";
import type { MobileAppSettings } from "@/validate/mobileAppSettingsSchema";

const MAX_ROWS = 200;

function formatLastSeen(v: unknown): string {
    if (v == null) return "—";
    if (v instanceof Timestamp) return v.toDate().toLocaleString();
    return String(v);
}

/** `current` and `unknown` get no badge — a wall of green hides the rows that need attention. */
const STATUS_BADGE: Partial<Record<VersionStatus, { variant: "destructive" | "secondary" | "outline"; key: string }>> = {
    blocked: { variant: "destructive", key: "securityCenter.mobileClients.badge.blocked" },
    outdated: { variant: "secondary", key: "securityCenter.mobileClients.badge.outdated" },
    ahead: { variant: "outline", key: "securityCenter.mobileClients.badge.ahead" },
};

export default function MobileClientsPage() {
    const { t } = useLanguage();
    const auth = useAuth();
    const claims = auth?.customClaims as Record<string, unknown> | null | undefined;
    const { hasPermission, loading: permLoading } = usePermission(CAPABILITIES.security_view_mobile_clients);

    const isAdmin = claims?.admin === true;
    const role = typeof claims?.role === "string" ? claims.role : "";
    const partnerScopeId =
        typeof claims?.partnerScopeId === "string" ? claims.partnerScopeId.trim() : "";

    const [rows, setRows] = useState<Array<{ id: string; path: string; data: Record<string, unknown> }>>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [release, setRelease] = useState<MobileAppSettings | null>(null);

    // Live-follow the published release so badges update the moment a build is published or the
    // floor is raised, without the operator reloading the page.
    useEffect(() => {
        if (permLoading || !hasPermission) return;
        return subscribeMobileAppSettings(setRelease, (e) =>
            // Non-fatal: the table still lists installations, just without status badges.
            console.error("[MobileClients] release settings listen failed", e),
        );
    }, [hasPermission, permLoading]);

    useEffect(() => {
        if (permLoading) return;
        if (!hasPermission) {
            setLoading(false);
            return;
        }

        let q: Query;
        if (isAdmin) {
            q = query(collectionGroup(db, "mobile_installations"), orderBy("lastSeenAt", "desc"), limit(MAX_ROWS));
        } else if (role === "partner") {
            if (!partnerScopeId) {
                setError("no_scope");
                setLoading(false);
                return;
            }
            q = query(
                collectionGroup(db, "mobile_installations"),
                where("partnerId", "==", partnerScopeId),
                orderBy("lastSeenAt", "desc"),
                limit(MAX_ROWS),
            );
        } else {
            setError("no_access");
            setLoading(false);
            return;
        }

        setLoading(true);
        setError(null);
        const unsub = onSnapshot(
            q,
            (snap) => {
                setRows(
                    snap.docs.map((d) => ({
                        id: d.id,
                        path: d.ref.path,
                        data: d.data() as Record<string, unknown>,
                    })),
                );
                setLoading(false);
            },
            (e) => {
                console.error("[MobileClients]", e);
                setError((e as Error).message || "listen failed");
                setLoading(false);
            },
        );
        return () => unsub();
    }, [hasPermission, permLoading, isAdmin, role, partnerScopeId]);

    const latestVersion = release?.latestVersion ?? "";
    const minAllowedVersion = release?.minAllowedVersion ?? "";
    const statusCounts = countVersionStatuses(
        rows.map((row) => row.data.appVersion),
        latestVersion,
        minAllowedVersion,
    );

    if (permLoading) {
        return (
            <div className="flex h-[50vh] items-center justify-center">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
        );
    }

    if (!hasPermission) {
        return (
            <div className="p-6">
                <Alert>
                    <AlertDescription>{t("securityCenter.mobileClients.accessDenied")}</AlertDescription>
                </Alert>
            </div>
        );
    }

    if (error === "no_scope") {
        return (
            <div className="p-6 space-y-4">
                <h1 className="text-3xl font-bold tracking-tight">{t("securityCenter.mobileClients.title")}</h1>
                <Alert>
                    <AlertDescription>{t("securityCenter.mobileClients.partnerScopeMissing")}</AlertDescription>
                </Alert>
            </div>
        );
    }

    if (error === "no_access") {
        return (
            <div className="p-6">
                <Alert>
                    <AlertDescription>{t("securityCenter.mobileClients.accessDenied")}</AlertDescription>
                </Alert>
            </div>
        );
    }

    return (
        <div className="p-6 space-y-6">
            <div>
                <h1 className="text-3xl font-bold tracking-tight flex items-center gap-2">
                    <Smartphone className="h-8 w-8" />
                    {t("securityCenter.mobileClients.title")}
                </h1>
                <p className="text-muted-foreground mt-2">{t("securityCenter.mobileClients.subtitle")}</p>
            </div>

            {error && error !== "no_scope" && error !== "no_access" ? (
                <Alert variant="destructive">
                    <AlertDescription>{error}</AlertDescription>
                </Alert>
            ) : null}

            <Card>
                <CardHeader>
                    <CardTitle>{t("securityCenter.mobileClients.title")}</CardTitle>
                    <CardDescription>
                        {latestVersion
                            ? t("securityCenter.mobileClients.summary", {
                                  blocked: statusCounts.blocked,
                                  outdated: statusCounts.outdated,
                                  current: statusCounts.current,
                              })
                            : t("securityCenter.mobileClients.summaryNoRelease")}
                    </CardDescription>
                </CardHeader>
                <CardContent>
                    {loading ? (
                        <div className="flex justify-center py-12">
                            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                        </div>
                    ) : rows.length === 0 ? (
                        <p className="text-sm text-muted-foreground">{t("securityCenter.mobileClients.empty")}</p>
                    ) : (
                        <div className="rounded-md border overflow-x-auto">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t("securityCenter.mobileClients.colDriver")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colDriverId")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colPartner")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colPlatform")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colVersion")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colBuild")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colFlavor")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colLastSeen")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colInstallId")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {rows.map((row) => {
                                        const status = getVersionStatus(
                                            row.data.appVersion,
                                            latestVersion,
                                            minAllowedVersion,
                                        );
                                        const badge = STATUS_BADGE[status];
                                        const isDevBuild = String(row.data.flavor ?? "") === "dev";
                                        return (
                                        <TableRow key={`${row.path}`}>
                                            <TableCell className="font-medium">
                                                {String(row.data.driverName ?? "—")}
                                            </TableCell>
                                            <TableCell className="font-mono text-xs">
                                                {String(row.data.driverId ?? "—")}
                                            </TableCell>
                                            <TableCell className="font-mono text-xs">
                                                {row.data.partnerId ? String(row.data.partnerId) : "—"}
                                            </TableCell>
                                            <TableCell>{String(row.data.platform ?? "—")}</TableCell>
                                            <TableCell>
                                                <span className="flex items-center gap-2 whitespace-nowrap">
                                                    {String(row.data.appVersion ?? "—")}
                                                    {badge && (
                                                        <Badge variant={badge.variant}>{t(badge.key)}</Badge>
                                                    )}
                                                </span>
                                            </TableCell>
                                            <TableCell>{String(row.data.buildNumber ?? "—")}</TableCell>
                                            <TableCell>
                                                {isDevBuild ? (
                                                    <Badge variant="destructive">
                                                        {t("securityCenter.mobileClients.badge.devFlavor")}
                                                    </Badge>
                                                ) : (
                                                    String(row.data.flavor ?? "—")
                                                )}
                                            </TableCell>
                                            <TableCell className="whitespace-nowrap text-sm">
                                                {formatLastSeen(row.data.lastSeenAt)}
                                            </TableCell>
                                            <TableCell className="font-mono text-xs max-w-[140px] truncate" title={row.id}>
                                                {row.id}
                                            </TableCell>
                                        </TableRow>
                                        );
                                    })}
                                </TableBody>
                            </Table>
                        </div>
                    )}
                </CardContent>
            </Card>
        </div>
    );
}
