"use client";

/**
 * The Security Center overview over Go (T54S; developer-spec.md §10.13; Appendix E §E.2.4 row
 * `/app/security-center`). One `GET /v1/security/overview` holds the cards and the event histogram
 * (counted in PostgreSQL, no 150-document listener), `GET /v1/stats/users-by-role` the RBAC
 * distribution (no seven count queries), `GET /v1/security-events?limit=8` the recent events (30 s
 * poll), and the active-users panel `GET /v1/users` (60 s poll). Permission checks are selectors over
 * `['me']` (no `permissions_config` read).
 *
 * A platform principal reads every tenant with `X-Act-On-Tenant: *`, and each such request is a
 * `platform_cross_tenant_access` warning row that the next feed read returns. For it the page polls
 * every 5 min, reads the 50 newest events and shows the 8 newest that are not its own cross-tenant
 * reads (a toggle with their count shows them), and says that the server counters include them.
 */
import Link from "next/link";
import { useState } from "react";
import { formatDistanceToNow } from "date-fns";
import { Activity, AlertTriangle, Check, Loader2, LogIn, Smartphone, Users } from "lucide-react";

import { useLanguage } from "@/context/language";
import { useCan, useMe } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { eventRows, NO_EVENT_FILTER, RECENT_EVENTS_FETCH_PLATFORM, RECENT_EVENTS_LIMIT, useSecurityEvents, withoutOwnReads } from "../api/events";
import { roleDistribution, systemHealthy, useSecurityOverview, useUsersByRole } from "../api/overview";
import { useSecurityReach } from "../api/reach";
import { ActiveUsersPanel } from "./ActiveUsersPanel";
import { eventTypeLabel, formatCompactInt, OwnReadsToggle, severityColor, severityIcon } from "./eventDisplay";

function Spinner({ className = "h-8 w-8" }: { className?: string }) {
    return <Loader2 className={`${className} animate-spin text-muted-foreground`} />;
}

function StatCard({
    label,
    value,
    icon: Icon,
    tone,
    children,
    testId,
}: {
    label: string;
    value: React.ReactNode;
    icon: React.ComponentType<{ className?: string }>;
    tone?: string;
    children?: React.ReactNode;
    testId?: string;
}) {
    return (
        <Card data-testid={testId}>
            <CardContent className="pt-6">
                <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-2">{label}</p>
                <div className="flex items-center gap-2">
                    <span className={`text-3xl font-bold tabular-nums ${tone ?? ""}`}>{value}</span>
                    <Icon className={`h-6 w-6 ${tone ?? "text-muted-foreground"}`} />
                </div>
                <div className="mt-2 text-sm text-muted-foreground">{children}</div>
            </CardContent>
        </Card>
    );
}

export function SecurityOverviewPage() {
    const { t } = useLanguage();
    const reach = useSecurityReach();
    const viewerId = useMe().data?.id;
    const canViewAudit = useCan("security:view_audit").allowed;
    const canViewUsers = useCan("users:view").allowed;
    const canViewStatus = useCan("security:view_status").allowed;
    const canViewMobile = useCan("security:view_mobile_clients").allowed;
    const overview = useSecurityOverview(reach);
    const byRole = useUsersByRole(reach);
    const recentFetch = reach === "all" ? RECENT_EVENTS_FETCH_PLATFORM : RECENT_EVENTS_LIMIT;
    const recent = useSecurityEvents(NO_EVENT_FILTER, reach, { pageSize: recentFetch, enabled: canViewAudit });
    const [range, setRange] = useState<"24h" | "7d">("24h");
    const [showOwnReads, setShowOwnReads] = useState(false);

    const o = overview.data;
    const histogram = o ? (range === "7d" ? o.events.daily7d : o.events.hourly24h) : [];
    const histMax = Math.max(1, ...histogram);
    const dist = byRole.data ? roleDistribution(byRole.data) : null;
    const pct = (n: number) => (dist && dist.total > 0 ? (n / dist.total) * 100 : 0);
    const conic =
        dist && dist.total > 0
            ? `conic-gradient(rgb(59 130 246) 0% ${pct(dist.drivers)}%, rgb(249 115 22) ${pct(dist.drivers)}% ${pct(dist.drivers) + pct(dist.operations)}%, hsl(var(--muted)) ${pct(dist.drivers) + pct(dist.operations)}% 100%)`
            : "conic-gradient(hsl(var(--muted)) 0% 100%)";
    const recentLoaded = eventRows(recent.data?.pages);
    const recentVisible = withoutOwnReads(recentLoaded, viewerId, showOwnReads);
    const recentRows = recentVisible.rows.slice(0, RECENT_EVENTS_LIMIT);

    return (
        <div className="p-6 space-y-6">
            <div className="flex flex-col gap-1 sm:flex-row sm:items-end sm:justify-between">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("securityCenter.overview.title")}</h1>
                    <p className="text-muted-foreground mt-1">{t("securityCenter.overview.subtitleGo")}</p>
                </div>
                {o ? (
                    <p className="text-xs text-muted-foreground" data-testid="overview-scope">
                        {o.scope.all ? t("securityCenter.overview.scopeAll") : t("securityCenter.overview.scopeTenant")}
                        {" · "}
                        {t("securityCenter.overview.generatedAt", { time: new Date(o.generatedAt).toLocaleTimeString() })}
                    </p>
                ) : null}
            </div>

            {overview.error ? (
                <Alert variant="destructive">
                    <AlertDescription>{apiErrorText(overview.error, t)}</AlertDescription>
                </Alert>
            ) : null}
            {o?.quarantine && o.quarantine.rows > 0 ? (
                <Alert>
                    <AlertTriangle className="h-4 w-4" />
                    <AlertDescription data-testid="quarantine-alert">
                        {t("securityCenter.overview.quarantineRows", { rows: o.quarantine.rows.toLocaleString() })}
                    </AlertDescription>
                </Alert>
            ) : null}

            {!o ? (
                overview.isPending ? (
                    <div className="flex justify-center py-12">
                        <Spinner />
                    </div>
                ) : null
            ) : (
                <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
                    {o.system ? (
                        <StatCard
                            testId="card-system"
                            label={t("securityCenter.overview.systemStatus")}
                            value={systemHealthy(o.system) ? t("securityCenter.status.healthy") : t("securityCenter.status.degraded")}
                            icon={systemHealthy(o.system) ? Check : AlertTriangle}
                            tone={systemHealthy(o.system) ? "text-emerald-500" : "text-amber-500"}
                        >
                            {t("securityCenter.overview.systemSummary", {
                                redis: t(`securityCenter.status.state.${o.system.redis}`, o.system.redis),
                                pending: o.system.outbox.pending,
                            })}
                            {canViewStatus ? (
                                <Link href="/app/security-center/status" prefetch={false} className="block text-primary hover:underline">
                                    {t("securityCenter.overview.viewStatus")}
                                </Link>
                            ) : null}
                        </StatCard>
                    ) : (
                        <StatCard testId="card-system" label={t("securityCenter.overview.systemStatus")} value="—" icon={Activity}>
                            {t("securityCenter.overview.systemNoPermission")}
                        </StatCard>
                    )}
                    <StatCard testId="card-sessions" label={t("securityCenter.overview.activeSessions")} value={o.sessions.active.toLocaleString()} icon={Users}>
                        {t("securityCenter.overview.sessionsRevoked24h", { count: o.sessions.revoked24h })}
                        {" · "}
                        {t("securityCenter.overview.usersActive", { count: o.users.byStatus.active ?? 0, total: o.users.total })}
                        {canViewUsers ? (
                            <Link href="/app/security-center/users" prefetch={false} className="block text-primary hover:underline">
                                {t("securityCenter.overview.sessionsGoToUsers")}
                            </Link>
                        ) : null}
                    </StatCard>
                    <StatCard
                        testId="card-logins"
                        label={t("securityCenter.overview.failedLogins24h")}
                        value={o.logins.failed24h.toLocaleString()}
                        icon={LogIn}
                        tone={o.logins.lockouts24h > 0 ? "text-amber-500" : undefined}
                    >
                        {t("securityCenter.overview.lockouts24h", { count: o.logins.lockouts24h })}
                    </StatCard>
                    <StatCard
                        testId="card-mobile"
                        label={t("securityCenter.overview.mobileInstalls")}
                        value={o.installations ? o.installations.total.toLocaleString() : "—"}
                        icon={Smartphone}
                        tone={o.installations && o.installations.belowFloor > 0 ? "text-red-500" : undefined}
                    >
                        {o.installations ? (
                            <>
                                {t("securityCenter.overview.mobileSummary", {
                                    seen: o.installations.seen7d,
                                    blocked: o.installations.belowFloor,
                                    outdated: o.installations.belowLatest,
                                })}
                                {canViewMobile ? (
                                    <Link href="/app/security-center/mobile-clients" prefetch={false} className="block text-primary hover:underline">
                                        {t("securityCenter.overview.viewMobileClients")}
                                    </Link>
                                ) : null}
                            </>
                        ) : (
                            t("securityCenter.overview.mobileInstallsNoPermission")
                        )}
                    </StatCard>
                </div>
            )}

            <div className="grid gap-6 lg:grid-cols-2">
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between pb-2">
                        <CardTitle className="text-base">{t("securityCenter.overview.recentEvents")}</CardTitle>
                        {canViewAudit ? (
                            <Button variant="ghost" size="sm" asChild>
                                <Link href="/app/security-center/audit" prefetch={false}>
                                    {t("securityCenter.overview.viewAll")}
                                </Link>
                            </Button>
                        ) : null}
                    </CardHeader>
                    <CardContent>
                        {!canViewAudit ? (
                            <Alert>
                                <AlertDescription>{t("securityCenter.overview.auditFeedNoPermission")}</AlertDescription>
                            </Alert>
                        ) : recent.isPending ? (
                            <div className="flex justify-center py-8">
                                <Spinner />
                            </div>
                        ) : recent.error ? (
                            <Alert variant="destructive">
                                <AlertDescription>{apiErrorText(recent.error, t)}</AlertDescription>
                            </Alert>
                        ) : recentRows.length === 0 ? (
                            <div className="space-y-3">
                                <Alert>
                                    <AlertDescription>
                                        {recentVisible.own > 0
                                            ? t("securityCenter.overview.recentOnlyOwn", { count: recentLoaded.length })
                                            : t("securityCenter.overview.auditFeedEmptyGo")}
                                    </AlertDescription>
                                </Alert>
                                <OwnReadsToggle own={recentVisible.own} show={showOwnReads} onChange={setShowOwnReads} t={t} />
                            </div>
                        ) : (
                            <ul className="space-y-3" data-testid="recent-events">
                                {recentRows.map((ev) => {
                                    const Icon = severityIcon(ev.severity);
                                    const label = eventTypeLabel(ev.type, t);
                                    return (
                                        <li key={ev.id} className="flex items-start gap-3 rounded-lg bg-muted/30 p-3">
                                            <Icon className={`h-5 w-5 shrink-0 mt-0.5 ${severityColor(ev.severity)}`} />
                                            <div className="min-w-0 flex-1">
                                                <p className="text-sm font-medium break-words">{ev.summary || label}</p>
                                                <p className="text-xs text-muted-foreground">
                                                    {label}
                                                    {ev.actorEmail ? ` · ${ev.actorEmail}` : ""}
                                                </p>
                                            </div>
                                            <span className="shrink-0 text-xs text-muted-foreground">
                                                {formatDistanceToNow(new Date(ev.createdAt), { addSuffix: true })}
                                            </span>
                                        </li>
                                    );
                                })}
                            </ul>
                        )}
                        {recentRows.length > 0 ? (
                            <div className="mt-3">
                                <OwnReadsToggle own={recentVisible.own} show={showOwnReads} onChange={setShowOwnReads} t={t} />
                            </div>
                        ) : null}
                    </CardContent>
                </Card>

                <div className="space-y-6">
                    <Card>
                        <CardHeader className="flex flex-row items-center justify-between pb-2">
                            <CardTitle className="text-base">{t("securityCenter.overview.securityEventsHistogram")}</CardTitle>
                            <Select value={range} onValueChange={(v) => setRange(v === "7d" ? "7d" : "24h")}>
                                <SelectTrigger className="h-8 w-[140px]" aria-label={t("securityCenter.overview.histogramRange")}>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="24h">{t("securityCenter.overview.last24Hours")}</SelectItem>
                                    <SelectItem value="7d">{t("securityCenter.overview.last7Days")}</SelectItem>
                                </SelectContent>
                            </Select>
                        </CardHeader>
                        <CardContent>
                            {!o ? (
                                <div className="flex h-24 items-center justify-center">
                                    {overview.isPending ? <Spinner className="h-6 w-6" /> : <span className="text-sm text-muted-foreground">—</span>}
                                </div>
                            ) : (
                                <>
                                    <div className="flex h-24 items-end gap-1" data-testid="event-histogram">
                                        {histogram.map((h, i) => (
                                            <div
                                                key={i}
                                                className={`min-h-[8px] flex-1 rounded-t ${h > 0 ? "bg-primary/80" : "bg-muted"}`}
                                                style={{ height: `${Math.max(8, (h / histMax) * 100)}%` }}
                                                title={String(h)}
                                            />
                                        ))}
                                    </div>
                                    <p className="mt-2 text-xs text-muted-foreground">
                                        {t("securityCenter.overview.histogramSummary", {
                                            day: o.events.last24h,
                                            week: o.events.last7d,
                                            critical: o.events.bySeverity24h.critical ?? 0,
                                            warning: o.events.bySeverity24h.warning ?? 0,
                                        })}
                                    </p>
                                    {reach === "all" ? (
                                        <p className="mt-1 text-xs text-muted-foreground" data-testid="histogram-own-reads">
                                            {t("securityCenter.overview.histogramIncludesOwnReads")}
                                        </p>
                                    ) : null}
                                </>
                            )}
                        </CardContent>
                    </Card>

                    <Card>
                        <CardHeader>
                            <CardTitle className="text-base">{t("securityCenter.overview.rbacDistribution")}</CardTitle>
                        </CardHeader>
                        <CardContent>
                            {byRole.error ? (
                                <Alert variant="destructive">
                                    <AlertDescription>{apiErrorText(byRole.error, t)}</AlertDescription>
                                </Alert>
                            ) : !dist ? (
                                <div className="flex h-32 items-center justify-center">
                                    <Spinner />
                                </div>
                            ) : (
                                <div className="flex items-center gap-6" data-testid="rbac-distribution">
                                    <div className="relative h-32 w-32 shrink-0">
                                        <div className="absolute inset-0 rounded-full" style={{ background: conic }} />
                                        <div className="absolute inset-0 m-auto flex h-20 w-20 flex-col items-center justify-center rounded-full bg-card">
                                            <p className="text-lg font-bold leading-none tabular-nums">{formatCompactInt(dist.total)}</p>
                                            <p className="mt-1 text-[10px] uppercase text-muted-foreground">{t("securityCenter.overview.users")}</p>
                                        </div>
                                    </div>
                                    <ul className="space-y-2 text-sm">
                                        {(
                                            [
                                                ["bg-blue-500", "securityCenter.overview.drivers", dist.drivers],
                                                ["bg-orange-500", "securityCenter.overview.operationsRoles", dist.operations],
                                                ["bg-muted", "securityCenter.overview.others", dist.others],
                                            ] as const
                                        ).map(([color, key, n]) => (
                                            <li key={key} className="flex items-center gap-2">
                                                <span className={`h-3 w-3 rounded-full ${color}`} />
                                                <span>
                                                    {t(key)} ({n.toLocaleString()}
                                                    {dist.total > 0 ? ` · ${Math.round((n / dist.total) * 100)}%` : ""})
                                                </span>
                                            </li>
                                        ))}
                                    </ul>
                                </div>
                            )}
                        </CardContent>
                    </Card>
                </div>
            </div>

            <div className="space-y-4">
                <h2 className="text-xl font-bold">{t("securityCenter.overview.sessionsHelpTitle")}</h2>
                <Card>
                    <CardContent className="space-y-3 pt-6">
                        <ActiveUsersPanel />
                    </CardContent>
                </Card>
            </div>
        </div>
    );
}
