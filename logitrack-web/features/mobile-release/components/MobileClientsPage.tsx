"use client";

/**
 * Mobile clients over Go (T54S; `GET /v1/app-installations[/stats]`, T52S; Appendix E §E.2.4 row
 * `/app/security-center/mobile-clients`, §E.5 row 28). The keyset list replaces the collection-group
 * listener capped at 200 (100 per page, "load more", polled every 60 s while visible and while one page
 * is loaded, every 5 min for a platform principal; Refresh updates after "load more"); each row's
 * version status comes from Go (the gate's floor and latest release), and the summary is one grouped
 * read over the installations seen in the last 7 days. RLS scopes the rows (a carrier's tenant_admin
 * its own tenant): no `partnerScopeId` claim is involved any more.
 */
import { useState } from "react";
import { Loader2, RefreshCw } from "lucide-react";

import { useLanguage } from "@/context/language";
import { useSecurityReach } from "@/features/security-center/api/reach";
import { formatDateTime } from "@/features/security-center/components/eventDisplay";
import { apiErrorText } from "@/lib/apiError";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { ACTIVE_WINDOW, installationRows, useInstallations, useInstallationStats, type VersionStatus } from "../api/installations";

/** `current` and `unknown` get no badge: a wall of green hides the rows that need attention. */
const STATUS_BADGE: Partial<Record<VersionStatus, { variant: "destructive" | "secondary" | "outline"; key: string }>> = {
    blocked: { variant: "destructive", key: "securityCenter.mobileClients.badge.blocked" },
    outdated: { variant: "secondary", key: "securityCenter.mobileClients.badge.outdated" },
    ahead: { variant: "outline", key: "securityCenter.mobileClients.badge.ahead" },
};

const ALL = "all";

export function MobileClientsPage() {
    const { t } = useLanguage();
    const reach = useSecurityReach();
    const [flavor, setFlavor] = useState(ALL);
    const list = useInstallations({ flavor: flavor === ALL ? "" : flavor }, reach);
    const stats = useInstallationStats({ since: ACTIVE_WINDOW, belowVersion: "" }, reach);
    const rows = installationRows(list.data?.pages);
    const pollPaused = (list.data?.pages.length ?? 0) > 1;
    const s = stats.data;

    return (
        <div className="flex flex-col gap-section">
            <PageHeader title={t("securityCenter.mobileClients")} description={t("securityCenter.mobileClients.subtitle")} />

            {list.error ? (
                <Alert variant="destructive">
                    <AlertDescription>{apiErrorText(list.error, t)}</AlertDescription>
                </Alert>
            ) : null}

            <Card>
                <CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                    <div>
                        <CardTitle>{t("securityCenter.mobileClients.title")}</CardTitle>
                        <CardDescription data-testid="mobile-summary">
                            {!s
                                ? stats.error
                                    ? apiErrorText(stats.error, t)
                                    : "…"
                                : s.latestVersion
                                  ? t("securityCenter.mobileClients.summary7d", {
                                        total: s.total,
                                        blocked: s.byStatus.blocked ?? 0,
                                        outdated: s.byStatus.outdated ?? 0,
                                        current: s.byStatus.current ?? 0,
                                    })
                                  : t("securityCenter.mobileClients.summaryNoRelease")}
                        </CardDescription>
                        {s ? (
                            <p className="mt-1 text-xs text-muted-foreground">
                                {t("securityCenter.mobileClients.gate", {
                                    floor: s.minAllowedVersion ?? "—",
                                    latest: s.latestVersion ?? "—",
                                    flavor: s.flavor ?? "—",
                                })}
                            </p>
                        ) : null}
                    </div>
                    <Select value={flavor} onValueChange={setFlavor}>
                        <SelectTrigger className="w-[160px]" aria-label={t("securityCenter.mobileClients.colFlavor")}>
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value={ALL}>{t("securityCenter.mobileClients.allFlavors")}</SelectItem>
                            <SelectItem value="prod">prod</SelectItem>
                            <SelectItem value="dev">dev</SelectItem>
                        </SelectContent>
                    </Select>
                </CardHeader>
                <CardContent>
                    {list.isPending ? (
                        <div className="flex justify-center py-12">
                            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                        </div>
                    ) : rows.length === 0 ? (
                        <p className="text-sm text-muted-foreground">{t("securityCenter.mobileClients.empty")}</p>
                    ) : (
                        <div className="overflow-x-auto rounded-md border">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t("securityCenter.mobileClients.colDriver")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colDriverId")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colTenant")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colPlatform")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colVersion")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colBuild")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colFlavor")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colLastSeen")}</TableHead>
                                        <TableHead>{t("securityCenter.mobileClients.colInstallId")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {rows.map((r) => {
                                        const badge = STATUS_BADGE[r.versionStatus as VersionStatus];
                                        return (
                                            <TableRow key={`${r.driverId}/${r.installId}`} data-testid="installation-row">
                                                <TableCell className="font-medium">{r.driverName ?? "—"}</TableCell>
                                                <TableCell className="font-mono text-xs">{r.driverId}</TableCell>
                                                <TableCell className="text-sm">{r.tenantName ?? "—"}</TableCell>
                                                <TableCell>{r.platform}</TableCell>
                                                <TableCell>
                                                    <span className="flex items-center gap-2 whitespace-nowrap">
                                                        {r.appVersion ?? "—"}
                                                        {badge ? <Badge variant={badge.variant}>{t(badge.key)}</Badge> : null}
                                                    </span>
                                                </TableCell>
                                                <TableCell>{r.buildNumber ?? "—"}</TableCell>
                                                <TableCell>
                                                    {r.flavor === "dev" ? (
                                                        <Badge variant="destructive">{t("securityCenter.mobileClients.badge.devFlavor")}</Badge>
                                                    ) : (
                                                        r.flavor
                                                    )}
                                                </TableCell>
                                                <TableCell className="whitespace-nowrap text-sm">{formatDateTime(r.lastSeenAt)}</TableCell>
                                                <TableCell className="max-w-[140px] truncate font-mono text-xs" title={r.installId}>
                                                    {r.installId}
                                                </TableCell>
                                            </TableRow>
                                        );
                                    })}
                                </TableBody>
                            </Table>
                        </div>
                    )}
                    <div className="mt-4 flex flex-wrap items-center justify-between gap-2 text-sm text-muted-foreground">
                        <span>
                            {t("securityCenter.mobileClients.loaded", { count: rows.length })}
                            {pollPaused ? <span className="block text-xs" data-testid="poll-paused">{t("securityCenter.pollPaused")}</span> : null}
                        </span>
                        <div className="flex gap-2">
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => void Promise.all([list.refetch(), stats.refetch()])}
                                disabled={list.isFetching}
                            >
                                <RefreshCw className={`mr-2 h-4 w-4 ${list.isFetching ? "animate-spin" : ""}`} />
                                {t("securityCenter.mobileClients.refresh")}
                            </Button>
                            {list.hasNextPage ? (
                                <Button variant="outline" size="sm" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
                                    {list.isFetchingNextPage ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                                    {t("securityCenter.audit.loadMore")}
                                </Button>
                            ) : null}
                        </div>
                    </div>
                </CardContent>
            </Card>
        </div>
    );
}
