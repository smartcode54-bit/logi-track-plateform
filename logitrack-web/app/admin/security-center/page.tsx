"use client";

import Link from "next/link";
import { formatDistanceToNow } from "date-fns";
import { useLanguage } from "@/context/language";
import { useSecurityOverviewStats } from "@/hooks/useSecurityOverviewStats";
import { usePermission } from "@/hooks/usePermission";
import { useSecurityEventsFeed } from "@/hooks/useSecurityEventsFeed";
import { CAPABILITIES } from "@/lib/capabilities";
import { buildSecurityEventHistogram } from "@/lib/securityEventsHistogram";
import { Check, Users, Smartphone, Loader2, AlertTriangle, Info, Shield, LogOut } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { SessionManagementActiveUsers } from "@/components/security-center/SessionManagementActiveUsers";
import { useState, useMemo } from "react";

function formatCompactInt(n: number): string {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
    if (n >= 10_000) return `${Math.round(n / 1000)}k`;
    if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
    return String(n);
}

function eventTypeLabelKey(type: string): string {
    return `securityCenter.securityEvents.types.${type}`;
}

function severityIcon(sev: string) {
    if (sev === "critical" || sev === "warning") return AlertTriangle;
    if (sev === "info") return Info;
    return Shield;
}

export default function SecurityCenterOverviewPage() {
    const { t } = useLanguage();
    const [failedRange, setFailedRange] = useState("24h");
    const { loadingUserStats, loadingMobileCount, error, isAdmin, distribution, mobileInstallCount, canViewMobileClients } =
        useSecurityOverviewStats();
    const { hasPermission: canViewAudit, loading: auditPermLoading } = usePermission(CAPABILITIES.security_view_audit);
    const { hasPermission: canManageUsers } = usePermission(CAPABILITIES.security_manage_users);
    const { rows: securityRows, loading: securityLoading, error: securityFeedError } = useSecurityEventsFeed(
        150,
        canViewAudit && !auditPermLoading,
    );

    const histogram = useMemo(
        () => buildSecurityEventHistogram(securityRows, failedRange === "7d" ? "7d" : "24h"),
        [securityRows, failedRange],
    );
    const histMax = Math.max(1, ...histogram);

    const rbacSegments = useMemo(() => {
        if (!distribution || distribution.totalUsers <= 0) {
            return {
                total: 0,
                drivers: 0,
                operations: 0,
                others: 0,
                pDrivers: 0,
                pOperations: 0,
                pOthers: 0,
            };
        }
        const { drivers, operationsRoles, others, totalUsers } = distribution;
        const pDrivers = (drivers / totalUsers) * 100;
        const pOperations = (operationsRoles / totalUsers) * 100;
        const pOthers = (others / totalUsers) * 100;
        return {
            total: totalUsers,
            drivers,
            operations: operationsRoles,
            others,
            pDrivers,
            pOperations,
            pOthers,
        };
    }, [distribution]);

    const conicBackground =
        rbacSegments.total > 0
            ? `conic-gradient(
                rgb(59 130 246) 0% ${rbacSegments.pDrivers}%,
                rgb(249 115 22) ${rbacSegments.pDrivers}% ${rbacSegments.pDrivers + rbacSegments.pOperations}%,
                hsl(var(--muted)) ${rbacSegments.pDrivers + rbacSegments.pOperations}% 100%
            )`
            : "conic-gradient(hsl(var(--muted)) 0% 100%)";

    const mobileCardSubtitle = () => {
        if (!canViewMobileClients) return t("securityCenter.overview.mobileInstallsNoPermission");
        if (mobileInstallCount === null) return t("securityCenter.overview.partnerMobileNoScope");
        return (
            <Link
                href="/admin/security-center/mobile-clients"
                className="text-primary hover:underline text-sm"
                prefetch={false}
            >
                {t("securityCenter.overview.viewMobileClients")}
            </Link>
        );
    };

    return (
        <div className="p-6 space-y-6">
            <div>
                <h1 className="text-3xl font-bold tracking-tight">{t("securityCenter.overview.title")}</h1>
                <p className="text-muted-foreground mt-1">{t("securityCenter.overview.subtitle")}</p>
            </div>

            {error ? (
                <Alert variant="destructive">
                    <AlertDescription>{error}</AlertDescription>
                </Alert>
            ) : null}
            {securityFeedError && canViewAudit ? (
                <Alert variant="destructive">
                    <AlertDescription>{securityFeedError}</AlertDescription>
                </Alert>
            ) : null}

            <div className="grid gap-4 md:grid-cols-3">
                <Card>
                    <CardContent className="pt-6">
                        <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-2">
                            {t("securityCenter.overview.systemStatus")}
                        </p>
                        <div className="flex items-center gap-2">
                            <Check className="h-8 w-8 text-emerald-500" />
                            <span className="text-3xl font-bold text-emerald-500">{t("securityCenter.overview.secure")}</span>
                        </div>
                        <p className="text-sm text-muted-foreground mt-2">{t("securityCenter.overview.uptime")}</p>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-6">
                        <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-2">
                            {t("securityCenter.overview.sessionsRevocationTitle")}
                        </p>
                        <div className="flex items-center gap-2">
                            <span className="text-3xl font-bold text-muted-foreground">—</span>
                            <Users className="h-6 w-6 text-muted-foreground" />
                        </div>
                        <p className="text-sm text-muted-foreground mt-2">{t("securityCenter.overview.sessionsRevocationBody")}</p>
                        {canManageUsers ? (
                            <Button variant="link" className="h-auto p-0 mt-1 gap-1.5 inline-flex" asChild>
                                <Link href="/admin/security-center/users" prefetch={false}>
                                    <LogOut className="h-3.5 w-3.5 shrink-0" />
                                    {t("securityCenter.overview.sessionsGoToUsers")}
                                </Link>
                            </Button>
                        ) : (
                            <p className="text-xs text-muted-foreground mt-2">{t("securityCenter.overview.sessionsManageUsersRequired")}</p>
                        )}
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-6">
                        <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-2">
                            {t("securityCenter.overview.mobileInstalls")}
                        </p>
                        <div className="flex items-center gap-2">
                            {loadingMobileCount ? (
                                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                            ) : (
                                <>
                                    <span className="text-3xl font-bold tabular-nums">
                                        {mobileInstallCount !== null ? mobileInstallCount.toLocaleString() : "—"}
                                    </span>
                                    <Smartphone className="h-6 w-6 text-muted-foreground" />
                                </>
                            )}
                        </div>
                        <div className="mt-2">{mobileCardSubtitle()}</div>
                    </CardContent>
                </Card>
            </div>

            <div className="grid gap-6 lg:grid-cols-2">
                <div className="space-y-6">
                    <Card>
                        <CardHeader className="flex flex-row items-center justify-between pb-2">
                            <CardTitle className="text-base">{t("securityCenter.overview.recentEvents")}</CardTitle>
                            <Button variant="ghost" size="sm" asChild>
                                <Link href="/admin/security-center/audit" prefetch={false}>
                                    {t("securityCenter.overview.viewAll")}
                                </Link>
                            </Button>
                        </CardHeader>
                        <CardContent>
                            {auditPermLoading ? (
                                <div className="flex justify-center py-8">
                                    <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                                </div>
                            ) : !canViewAudit ? (
                                <Alert>
                                    <AlertDescription>{t("securityCenter.overview.auditFeedNoPermission")}</AlertDescription>
                                </Alert>
                            ) : securityLoading ? (
                                <div className="flex justify-center py-8">
                                    <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                                </div>
                            ) : securityRows.length === 0 ? (
                                <Alert>
                                    <AlertDescription>{t("securityCenter.overview.auditFeedEmpty")}</AlertDescription>
                                </Alert>
                            ) : (
                                <div className="space-y-4">
                                    {securityRows.slice(0, 8).map((ev) => {
                                        const Icon = severityIcon(ev.severity);
                                        const color =
                                            ev.severity === "critical"
                                                ? "text-red-500"
                                                : ev.severity === "warning"
                                                  ? "text-amber-500"
                                                  : "text-blue-500";
                                        const typeKey = eventTypeLabelKey(ev.type);
                                        const typeLabel = t(typeKey) === typeKey ? ev.type : t(typeKey);
                                        return (
                                            <div
                                                key={ev.id}
                                                className="flex items-start gap-3 p-3 rounded-lg bg-muted/30 hover:bg-muted/50 transition-colors"
                                            >
                                                <Icon className={`h-5 w-5 shrink-0 mt-0.5 ${color}`} />
                                                <div className="flex-1 min-w-0">
                                                    <p className="font-medium text-sm">{ev.summary || typeLabel}</p>
                                                    <p className="text-xs text-muted-foreground">
                                                        {typeLabel}
                                                        {ev.actorEmail ? ` · ${ev.actorEmail}` : ""}
                                                    </p>
                                                </div>
                                                <span className="text-xs text-muted-foreground shrink-0">
                                                    {ev.createdAt
                                                        ? formatDistanceToNow(ev.createdAt.toDate(), { addSuffix: true })
                                                        : "—"}
                                                </span>
                                            </div>
                                        );
                                    })}
                                </div>
                            )}
                        </CardContent>
                    </Card>
                </div>

                <div className="space-y-6">
                    <Card>
                        <CardHeader className="flex flex-row items-center justify-between pb-2">
                            <CardTitle className="text-base">{t("securityCenter.overview.securityEventsHistogram")}</CardTitle>
                            <Select value={failedRange} onValueChange={setFailedRange}>
                                <SelectTrigger className="w-[140px] h-8">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="24h">{t("securityCenter.overview.last24Hours")}</SelectItem>
                                    <SelectItem value="7d">{t("securityCenter.overview.last7Days")}</SelectItem>
                                </SelectContent>
                            </Select>
                        </CardHeader>
                        <CardContent>
                            {!canViewAudit || auditPermLoading ? (
                                <p className="text-sm text-muted-foreground">{t("securityCenter.overview.auditFeedNoPermission")}</p>
                            ) : securityLoading ? (
                                <div className="flex h-24 items-center justify-center">
                                    <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                </div>
                            ) : (
                                <>
                                    <div className="h-24 flex items-end gap-1">
                                        {histogram.map((h, i) => (
                                            <div
                                                key={i}
                                                className={`flex-1 rounded-t min-h-[8px] transition-all ${
                                                    h > 0 ? "bg-primary/80" : "bg-muted"
                                                }`}
                                                style={{ height: `${Math.max(8, (h / histMax) * 100)}%` }}
                                                title={String(h)}
                                            />
                                        ))}
                                    </div>
                                    <p className="text-xs text-muted-foreground mt-2">{t("securityCenter.overview.securityEventsHistogramHint")}</p>
                                </>
                            )}
                        </CardContent>
                    </Card>

                    <Card>
                        <CardHeader>
                            <CardTitle className="text-base">{t("securityCenter.overview.rbacDistribution")}</CardTitle>
                        </CardHeader>
                        <CardContent>
                            {!isAdmin ? (
                                <Alert>
                                    <AlertDescription>{t("securityCenter.overview.userStatsRequireAdmin")}</AlertDescription>
                                </Alert>
                            ) : loadingUserStats ? (
                                <div className="flex h-40 items-center justify-center">
                                    <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                                </div>
                            ) : (
                                <div className="flex items-center gap-6">
                                    <div className="relative h-32 w-32 shrink-0">
                                        <div className="absolute inset-0 rounded-full" style={{ background: conicBackground }} />
                                        <div className="absolute inset-0 m-auto h-20 w-20 rounded-full bg-card flex flex-col items-center justify-center">
                                            <p className="text-lg font-bold leading-none tabular-nums">
                                                {formatCompactInt(rbacSegments.total)}
                                            </p>
                                            <p className="text-[10px] text-muted-foreground uppercase mt-1">
                                                {t("securityCenter.overview.users")}
                                            </p>
                                        </div>
                                    </div>
                                    <div className="space-y-2 text-sm">
                                        <div className="flex items-center gap-2">
                                            <div className="h-3 w-3 rounded-full bg-blue-500" />
                                            <span>
                                                {t("securityCenter.overview.drivers")} ({rbacSegments.drivers.toLocaleString()}
                                                {rbacSegments.total > 0
                                                    ? ` · ${Math.round((rbacSegments.drivers / rbacSegments.total) * 100)}%`
                                                    : ""}
                                                )
                                            </span>
                                        </div>
                                        <div className="flex items-center gap-2">
                                            <div className="h-3 w-3 rounded-full bg-orange-500" />
                                            <span>
                                                {t("securityCenter.overview.operationsRoles")} (
                                                {rbacSegments.operations.toLocaleString()}
                                                {rbacSegments.total > 0
                                                    ? ` · ${Math.round((rbacSegments.operations / rbacSegments.total) * 100)}%`
                                                    : ""}
                                                )
                                            </span>
                                        </div>
                                        <div className="flex items-center gap-2">
                                            <div className="h-3 w-3 rounded-full bg-muted" />
                                            <span>
                                                {t("securityCenter.overview.others")} ({rbacSegments.others.toLocaleString()}
                                                {rbacSegments.total > 0
                                                    ? ` · ${Math.round((rbacSegments.others / rbacSegments.total) * 100)}%`
                                                    : ""}
                                                )
                                            </span>
                                        </div>
                                    </div>
                                </div>
                            )}
                        </CardContent>
                    </Card>
                </div>
            </div>

            <div className="space-y-4">
                <h2 className="text-xl font-bold">{t("securityCenter.overview.sessionsHelpTitle")}</h2>
                <Card>
                    <CardContent className="pt-6 space-y-3">
                        <SessionManagementActiveUsers />
                    </CardContent>
                </Card>
            </div>

            <div className="flex flex-col sm:flex-row justify-between items-center gap-4 pt-8 border-t text-sm text-muted-foreground">
                <p>{t("securityCenter.overview.footer")}</p>
                <div className="flex gap-6">
                    <Link href="#" className="hover:text-foreground transition-colors">
                        {t("securityCenter.overview.complianceReport")}
                    </Link>
                    <Link href="#" className="hover:text-foreground transition-colors">
                        {t("securityCenter.overview.privacyPolicy")}
                    </Link>
                    <Link href="#" className="hover:text-foreground transition-colors">
                        {t("securityCenter.overview.support")}
                    </Link>
                </div>
            </div>
        </div>
    );
}
