"use client";

/**
 * System status over Go (T54S; `GET /v1/security/status`, T51, `security:view_status`; Appendix E
 * §E.2.4 row `/app/security-center/status`): what the api sees of the platform in PostgreSQL and
 * Redis (database, Redis, the scheduler leader lock, the outbox backlog, the jobs). Broker queue and
 * DLQ depths are on `/metrics` and Grafana, which the browser never reaches. Polled every 60 s while
 * the tab is visible.
 */
import { formatDistanceToNow } from "date-fns";
import { AlertTriangle, CheckCircle2, Clock, Database, Loader2, RefreshCw, Server, Workflow, Zap } from "lucide-react";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

import { OUTBOX_STALE_SECONDS, systemHealthy, useSecurityStatus } from "../api/overview";

function StateBadge({ ok, label }: { ok: boolean; label: string }) {
    return (
        <Badge variant={ok ? "outline" : "destructive"} className={ok ? "border-emerald-500 text-emerald-600" : undefined}>
            {label}
        </Badge>
    );
}

function Row({ icon: Icon, title, hint, children }: { icon: React.ComponentType<{ className?: string }>; title: string; hint: string; children: React.ReactNode }) {
    return (
        <div className="flex items-start justify-between gap-4 py-3" data-testid="status-row">
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
    const { t } = useLanguage();
    const status = useSecurityStatus();
    const s = status.data;

    return (
        <div className="p-6 space-y-6">
            <div className="flex flex-col items-start justify-between gap-4 sm:flex-row sm:items-center">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("securityCenter.status.title")}</h1>
                    <p className="mt-1 text-muted-foreground">{t("securityCenter.status.subtitle")}</p>
                </div>
                <Button variant="outline" size="sm" onClick={() => void status.refetch()} disabled={status.isFetching}>
                    <RefreshCw className={`mr-2 h-4 w-4 ${status.isFetching ? "animate-spin" : ""}`} />
                    {t("securityCenter.status.refresh")}
                </Button>
            </div>

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
                                <CheckCircle2 className="h-8 w-8 text-emerald-500" />
                            ) : (
                                <AlertTriangle className="h-8 w-8 text-amber-500" />
                            )}
                            <div>
                                <p className="text-2xl font-bold">{systemHealthy(s) ? t("securityCenter.status.healthy") : t("securityCenter.status.degraded")}</p>
                                <p className="text-xs text-muted-foreground">
                                    {t("securityCenter.status.checkedAt", {
                                        time: formatDistanceToNow(status.dataUpdatedAt, { addSuffix: true }),
                                    })}
                                </p>
                            </div>
                        </CardContent>
                    </Card>

                    <Card>
                        <CardHeader>
                            <CardTitle className="flex items-center gap-2 text-base">
                                <Server className="h-5 w-5" />
                                {t("securityCenter.status.health")}
                            </CardTitle>
                            <CardDescription>{t("securityCenter.status.healthHint")}</CardDescription>
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
                                        ["securityCenter.status.jobsFailed24h", s.jobs.failed24h, s.jobs.failed24h > 0 ? "text-red-500" : ""],
                                        ["securityCenter.status.jobsSucceeded24h", s.jobs.succeeded24h, ""],
                                    ] as const
                                ).map(([key, n, tone]) => (
                                    <div key={key}>
                                        <p className="text-xs text-muted-foreground">{t(key)}</p>
                                        <p className={`text-2xl font-bold tabular-nums ${tone}`}>{n.toLocaleString()}</p>
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
