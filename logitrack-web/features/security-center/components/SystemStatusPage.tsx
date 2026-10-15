"use client";

/**
 * System status over Go (T54S; `GET /v1/security/status`, T51, `security:view_status`; Appendix E
 * §E.2.4 row `/app/security-center/status`): what the api sees of the platform in PostgreSQL and
 * Redis (database, Redis, the scheduler leader lock, the outbox backlog, the jobs). Broker queue and
 * DLQ depths are on `/metrics` and Grafana, which the browser never reaches. Polled every 60 s while
 * the tab is visible.
 */
import { AlertTriangle, CheckCircle2, Clock, Database, Loader2, RefreshCw, Server, Workflow, Zap } from "lucide-react";

import { DisclosureSummary } from "./DisclosureSummary";
import { useLanguage } from "@/context/language";
import { toneBadgeVariant, toneTextClass, type StatusTone } from "@/lib/statusPresentation";
import { apiErrorText } from "@/lib/apiError";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

import { formatDateTime, formatRelative } from "./eventDisplay";

import { OUTBOX_STALE_SECONDS, systemHealthy, useSecurityStatus } from "../api/overview";

function StateBadge({ ok, label, failureTone = "destructive" }: { ok: boolean; label: string; failureTone?: StatusTone }) {
    return (
        <Badge variant={toneBadgeVariant(ok ? "success" : failureTone)}>
            {label}
        </Badge>
    );
}

function Row({ icon: Icon, title, hint, children }: { icon: React.ComponentType<{ className?: string }>; title: string; hint: string; children: React.ReactNode }) {
    return (
        <div className="flex flex-wrap items-start justify-between gap-4 py-3" data-testid="status-row">
            <div className="flex items-start gap-3">
                <Icon className="mt-0.5 h-5 w-5 text-muted-foreground" />
                <div>
                    <p className="font-medium">{title}</p>
                    <p className="text-xs text-muted-foreground">{hint}</p>
                </div>
            </div>
            <div className="shrink-0 text-right">{children}</div>
        </div>
    );
}

export function SystemStatusPage() {
    const { t, language } = useLanguage();
    const status = useSecurityStatus();
    const s = status.data;
    const summaryTone: StatusTone = !s || systemHealthy(s) ? "success"
        : s.database !== "ok" || s.redis !== "ok" || !s.scheduler.leaderPresent ? "destructive" : "warning";

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("securityCenter.systemStatus")}
                description={t("securityCenter.status.subtitle")}
                actions={
                    <Button variant="outline" size="sm" onClick={() => void status.refetch()} disabled={status.isFetching}>
                        <RefreshCw className={`mr-2 h-4 w-4 text-muted-foreground ${status.isFetching ? "animate-spin" : ""}`} aria-hidden />
                        {t("securityCenter.status.refresh")}
                    </Button>
                }
            />

            {status.error ? (
                <Alert variant="destructive">
                    <AlertDescription>{apiErrorText(status.error, t)}</AlertDescription>
                </Alert>
            ) : null}

            {!s ? (
                status.isPending ? (
                    <div className="flex justify-center py-16">
                        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                    </div>
                ) : null
            ) : (
                <>
                    <Card>
                        <CardContent className="flex items-center gap-3 pt-6" data-testid="status-summary">
                            {systemHealthy(s) ? (
                                <CheckCircle2 className={`h-8 w-8 ${toneTextClass(summaryTone)}`} aria-hidden />
                            ) : (
                                <AlertTriangle className={`h-8 w-8 ${toneTextClass(summaryTone)}`} aria-hidden />
                            )}
                            <div>
                                <p className="text-2xl font-semibold">{systemHealthy(s) ? t("securityCenter.status.healthy") : t("securityCenter.status.degraded")}</p>
                                <p className="text-xs text-muted-foreground">
                                    {t("securityCenter.status.checkedAt", {
                                        time: formatRelative(status.dataUpdatedAt, language),
                                    })}
                                </p>
                            </div>
                        </CardContent>
                    </Card>

                    <Card>
                        <CardHeader>
                            <CardTitle className="flex items-center gap-2 text-base">
                                <Server className="h-5 w-5 text-muted-foreground" />
                                {t("securityCenter.status.health")}
                            </CardTitle>
                            <CardDescription>{t("securityCenter.status.healthHint")}</CardDescription>
                            <details className="text-xs text-muted-foreground">
                                <DisclosureSummary>{t("securityCenter.ui4.technicalDetails")}</DisclosureSummary>
                                <p className="mt-2">{t("securityCenter.status.technicalHint")}</p>
                                <p>{formatDateTime(status.dataUpdatedAt, language)}</p>
                            </details>
                        </CardHeader>
                        <CardContent className="divide-y">
                            <Row icon={Database} title={t("securityCenter.status.database")} hint={t("securityCenter.status.databaseHint")}>
                                <StateBadge ok={s.database === "ok"} label={t(`securityCenter.status.state.${s.database}`, s.database)} />
                            </Row>
                            <Row icon={Zap} title={t("securityCenter.status.redis")} hint={t("securityCenter.status.redisHint")}>
                                <StateBadge ok={s.redis === "ok"} label={t(`securityCenter.status.state.${s.redis}`, s.redis)} />
                            </Row>
                            <Row icon={Clock} title={t("securityCenter.status.scheduler")} hint={t("securityCenter.status.schedulerHint")}>
                                <StateBadge
                                    ok={s.scheduler.leaderPresent}
                                    label={s.scheduler.leaderPresent ? t("securityCenter.status.leaderPresent") : t("securityCenter.status.leaderMissing")}
                                />
                            </Row>
                            <Row icon={Workflow} title={t("securityCenter.status.outbox")} hint={t("securityCenter.status.outboxHint")}>
                                <StateBadge
                                    ok={s.outbox.oldestSeconds < OUTBOX_STALE_SECONDS}
                                    failureTone="warning"
                                    label={t("securityCenter.status.outboxPending", { pending: s.outbox.pending, seconds: s.outbox.oldestSeconds })}
                                />
                            </Row>
                        </CardContent>
                    </Card>

                    <Card>
                        <CardHeader>
                            <CardTitle className="text-base">{t("securityCenter.status.jobs")}</CardTitle>
                            <CardDescription>{t("securityCenter.status.jobsHint")}</CardDescription>
                        </CardHeader>
                        <CardContent>
                            <div className="grid grid-cols-2 gap-4 sm:grid-cols-4" data-testid="status-jobs">
                                {(
                                    [
                                        ["securityCenter.status.jobsQueued", s.jobs.queued, ""],
                                        ["securityCenter.status.jobsRunning", s.jobs.running, ""],
                                        ["securityCenter.status.jobsFailed24h", s.jobs.failed24h, s.jobs.failed24h > 0 ? "text-destructive" : ""],
                                        ["securityCenter.status.jobsSucceeded24h", s.jobs.succeeded24h, ""],
                                    ] as const
                                ).map(([key, n, tone]) => (
                                    <div key={key}>
                                        <p className="text-xs text-muted-foreground">{t(key)}</p>
                                        <p className={`text-2xl font-semibold tabular-nums ${tone}`}>{n.toLocaleString(language === "th" ? "th-TH" : "en-GB")}</p>
                                    </div>
                                ))}
                            </div>
                        </CardContent>
                    </Card>
                </>
            )}
        </div>
    );
}
