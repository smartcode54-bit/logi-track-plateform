"use client";

/**
 * The Security Center overview over Go: counters, role distribution, projected recent events and
 * active users. Historical cross-tenant reads remain behind the counted, visible toggle.
 */
import { MaskedValue } from "@/components/masked-value";
import Link from "next/link";
import { useState } from "react";
import { Activity, AlertTriangle, Check, Loader2, LogIn, Smartphone, Users } from "lucide-react";

import { DisclosureSummary } from "./DisclosureSummary";
import { useLanguage } from "@/context/language";
import { useCan, useMe } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { eventActorLabel, eventRows, NO_EVENT_FILTER, RECENT_EVENTS_FETCH_PLATFORM, RECENT_EVENTS_LIMIT, useSecurityEvents, withoutOwnReads } from "../api/events";
import { OUTBOX_STALE_SECONDS, roleDistribution, systemHealthy, useSecurityOverview, useUsersByRole } from "../api/overview";
import { useSecurityReach } from "../api/reach";
import { ScopeCaption } from "./ScopeCaption";
import { ActiveUsersPanel } from "./ActiveUsersPanel";
import { permissionHint, eventTypeLabel, formatDateTime, formatRelative, formatCompactInt, OwnReadsToggle, SeverityBadge, severityIcon } from "./eventDisplay";

function Spinner({ className = "h-8 w-8" }: { className?: string }) {
    return <Loader2 className={`${className} animate-spin text-muted-foreground`} />;
}

function StatCard({
    label,
    value,
    valueSize,
    icon: Icon,
    tone,
    children,
    testId,
}: {
    label: string;
    value: React.ReactNode;
    valueSize?: "message";
    icon: React.ComponentType<{ className?: string }>;
    tone?: string;
    children?: React.ReactNode;
    testId?: string;
}) {
    return (
        <Card data-testid={testId}>
            <CardContent className="pt-6">
                <p className="text-xs font-medium text-muted-foreground mb-2">{label}</p>
                <div className="flex items-center gap-2">
                    <span className={`${valueSize === "message" ? "text-lg" : "text-3xl"} font-semibold tabular-nums ${tone ?? ""}`}>{value}</span>
                    <Icon className="h-6 w-6 shrink-0 text-muted-foreground" />
                </div>
                <div className="mt-2 text-sm text-muted-foreground">{children}</div>
            </CardContent>
        </Card>
    );
}

export function SecurityOverviewPage() {
    const { t, language } = useLanguage();
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
            ? `conic-gradient(var(--chart-1) 0% ${pct(dist.drivers)}%, var(--chart-2) ${pct(dist.drivers)}% ${pct(dist.drivers) + pct(dist.operations)}%, var(--muted) ${pct(dist.drivers) + pct(dist.operations)}% 100%)`
            : "conic-gradient(var(--muted) 0% 100%)";
    const recentLoaded = eventRows(recent.data?.pages);
    const recentVisible = withoutOwnReads(recentLoaded, viewerId, showOwnReads);
    const recentRows = recentVisible.rows.slice(0, RECENT_EVENTS_LIMIT);

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.securityCenter")}
                description={
                    <>
                        <p>{t("securityCenter.overview.subtitleGo")}</p>
                        {o ? (
                            <div className="mt-1 text-xs" data-testid="overview-scope">
                                <ScopeCaption reach={o.scope.all ? "all" : "tenant"} resource="overview" />
                                {t("securityCenter.overview.generatedAt", { time: formatDateTime(o.generatedAt, language) })}
                            </div>
                        ) : null}
                    </>
                }
            />

            {overview.error ? (
                <Alert variant="destructive">
                    <AlertDescription>{apiErrorText(overview.error, t)}</AlertDescription>
                </Alert>
            ) : null}
            {o?.quarantine && o.quarantine.rows > 0 ? (
                <Alert>
                    <AlertTriangle className="h-4 w-4 text-muted-foreground" />
                    <AlertDescription data-testid="quarantine-alert">
                        {t("securityCenter.overview.quarantineRows", { rows: o.quarantine.rows.toLocaleString(language === "th" ? "th-TH" : "en-GB") })}
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
                            value={systemHealthy(o.system) ? t("securityCenter.ui4.systemResponding") : t("securityCenter.status.degraded")}
                            valueSize="message"
                            icon={systemHealthy(o.system) ? Check : AlertTriangle}
                            tone={systemHealthy(o.system) ? "text-success" : "text-warning"}
                        >
                            {o.system.outbox.oldestSeconds > OUTBOX_STALE_SECONDS ? (
                                <p>{t("securityCenter.ui4.pendingDelivery", { count: o.system.outbox.pending })}</p>
                            ) : null}
                            {canViewStatus ? (
                                <Link href="/app/security-center/status" prefetch={false} className="block text-primary hover:underline">
                                    {t("securityCenter.overview.viewStatus")}
                                </Link>
                            ) : null}
                        </StatCard>
                    ) : (
                        <StatCard testId="card-system" label={t("securityCenter.overview.systemStatus")} value="—" icon={Activity}>
                            {permissionHint("security:view_status", language, t)}
                        </StatCard>
                    )}
                    <StatCard testId="card-sessions" label={t("securityCenter.overview.activeSessions")} value={o.sessions.active.toLocaleString(language === "th" ? "th-TH" : "en-GB")} icon={Users}>
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
                        value={o.logins.failed24h.toLocaleString(language === "th" ? "th-TH" : "en-GB")}
                        icon={LogIn}
                        tone={o.logins.lockouts24h > 0 ? "text-warning" : undefined}
                    >
                        {t("securityCenter.overview.lockouts24h", { count: o.logins.lockouts24h })}
                    </StatCard>
                    <StatCard
                        testId="card-mobile"
                        label={t("securityCenter.overview.mobileInstalls")}
                        value={o.installations ? o.installations.total.toLocaleString(language === "th" ? "th-TH" : "en-GB") : "—"}
                        icon={Smartphone}
                        tone={o.installations && o.installations.belowFloor > 0 ? "text-destructive" : undefined}
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
                            permissionHint("security:view_mobile_clients", language, t)
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
                                <AlertDescription>{permissionHint("security:view_audit", language, t)}</AlertDescription>
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
                                        <li key={ev.id} className="flex flex-wrap items-start gap-3 rounded-lg bg-muted/30 p-3">
                                            <Icon className="h-5 w-5 shrink-0 mt-0.5 text-muted-foreground" />
                                            <div className="min-w-0 flex-1">
                                                <div className="flex flex-wrap items-center gap-2">
                                                    <p className="text-sm font-medium break-words">{label}</p>
                                                    <SeverityBadge severity={ev.severity} t={t} />
                                                </div>
                                                <p className="text-xs text-muted-foreground">
                                                    <MaskedValue value={eventActorLabel(ev, t)} source={ev} path="actorEmail" />
                                                </p>
                                                <details className="mt-1 text-xs text-muted-foreground">
                                                    <DisclosureSummary>{t("securityCenter.ui4.technicalDetails")}</DisclosureSummary>
                                                    <p className="mt-1 break-words"><MaskedValue value={ev.summary} source={ev} path="summary" /></p>
                                                    <p className="mt-1 break-all font-mono">{ev.type}</p>
                                                    {ev.details && Object.keys(ev.details).length > 0 ? (
                                                        <pre className="mt-1 whitespace-pre-wrap break-all">{JSON.stringify(ev.details, null, 2)}</pre>
                                                    ) : null}
                                                </details>
                                            </div>
                                            <span className="shrink-0 text-xs text-muted-foreground">
                                                {formatRelative(ev.createdAt, language)}
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
                            <CardTitle className="text-base">{t("securityCenter.ui4.usersByRole")}</CardTitle>
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
                                <div className="flex flex-wrap items-center gap-6" data-testid="rbac-distribution">
                                    <div className="relative h-32 w-32 shrink-0" aria-hidden="true">
                                        <div className="absolute inset-0 rounded-full" style={{ background: conic }} />
                                        <div className="absolute inset-0 m-auto flex h-20 w-20 flex-col items-center justify-center rounded-full bg-card">
                                            <p className="text-lg font-semibold leading-none tabular-nums">{formatCompactInt(dist.total)}</p>
                                            <p className="mt-1 text-xs text-muted-foreground">{t("securityCenter.overview.users")}</p>
                                        </div>
                                    </div>
                                    <ul className="space-y-2 text-sm">
                                        {(
                                            [
                                                ["var(--chart-1)", "securityCenter.overview.drivers", dist.drivers],
                                                ["var(--chart-2)", "securityCenter.overview.operationsRoles", dist.operations],
                                                ["var(--muted)", "securityCenter.overview.others", dist.others],
                                            ] as const
                                        ).map(([color, key, n]) => (
                                            <li key={key} className="flex items-center gap-2">
                                                <svg width="12" height="12" aria-hidden="true"><circle cx="6" cy="6" r="6" fill={color} /></svg>
                                                <span>
                                                    {t(key)} ({n.toLocaleString(language === "th" ? "th-TH" : "en-GB")}
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
                <h2 className="text-xl font-semibold">{t("securityCenter.ui4.recentlyActiveUsers")}</h2>
                <Card>
                    <CardContent className="space-y-3 pt-6">
                        <ActiveUsersPanel />
                    </CardContent>
                </Card>
            </div>
        </div>
    );
}
