"use client";

/**
 * Security audit logs over Go (T54S; developer-spec.md §10.10, §10.13; Appendix E §E.2.4 row
 * `/app/security-center/audit`, §E.10). The keyset `GET /v1/security-events` replaces the 500-row
 * `getDocs`: the date range, type and severity go to Go and are in the key; "load more" pages back
 * without a cap; the text search runs over the loaded rows, with a "loaded N" indicator so it never
 * pretends to cover rows that were not fetched. Polled every 30 s while visible and only while one page
 * is loaded (every 5 min for a platform principal); after "load more", Refresh updates. The viewer's own
 * cross-tenant reads (`isOwnCrossTenantRead`, one audit row per request of a platform principal) are
 * hidden from the list, the KPIs and the export by default, behind a toggle with their count.
 */
import { useMemo, useState } from "react";
import { format } from "date-fns";
import { ChevronDown, FileDown, Loader2, RefreshCw, Search, Shield, TrendingUp, Users } from "lucide-react";

import { useLanguage } from "@/context/language";
import { useMe } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import {
    EVENT_RANGES,
    eventActor,
    eventRows,
    eventsCsv,
    eventTarget,
    eventTypesOf,
    rangeStart,
    useSecurityEvents,
    withoutOwnReads,
    type EventRange,
    type SecurityEventDTO,
} from "../api/events";
import { useSecurityReach } from "../api/reach";
import { eventTypeLabel, OwnReadsToggle, SeverityBadge } from "./eventDisplay";

const ALL = "all";
const SEVERITIES = ["info", "warning", "critical"] as const;
const ROLE_EVENTS = new Set([
    "role_matrix_saved",
    "user_role_changed",
    "membership_added",
    "membership_role_changed",
    "membership_removed",
    "user_scope_changed",
    "platform_role_granted",
    "platform_role_revoked",
]);

/** Whether a row matches the text search (summary, actor, type, target, details). */
export function eventMatches(r: SecurityEventDTO, q: string): boolean {
    if (!q) return true;
    return [r.summary, eventActor(r), r.type, eventTarget(r), JSON.stringify(r.details ?? {})].join(" ").toLowerCase().includes(q);
}

export function SecurityAuditPage() {
    const { t } = useLanguage();
    const reach = useSecurityReach();
    const viewerId = useMe().data?.id;
    const [showOwnReads, setShowOwnReads] = useState(false);
    const [range, setRange] = useState<EventRange>("last7");
    const [type, setType] = useState(ALL);
    const [severity, setSeverity] = useState(ALL);
    const [search, setSearch] = useState("");
    const from = useMemo(() => rangeStart(range), [range]);
    const events = useSecurityEvents({ type: type === ALL ? "" : type, severity: severity === ALL ? "" : severity, from, to: "" }, reach);

    const loaded = eventRows(events.data?.pages);
    const visible = withoutOwnReads(loaded, viewerId, showOwnReads);
    const q = search.trim().toLowerCase();
    const rows = visible.rows.filter((r) => eventMatches(r, q));
    const pollPaused = (events.data?.pages.length ?? 0) > 1;
    const types = eventTypesOf(events.data?.pages);
    const kpis = {
        total: rows.length,
        elevated: rows.filter((r) => r.severity === "warning" || r.severity === "critical").length,
        roles: rows.filter((r) => ROLE_EVENTS.has(r.type)).length,
    };

    const exportCsv = () => {
        const blob = new Blob([eventsCsv(rows)], { type: "text/csv;charset=utf-8" });
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = `security_events_${format(new Date(), "yyyyMMdd_HHmm")}.csv`;
        a.click();
        URL.revokeObjectURL(url);
    };

    const clearFilters = () => {
        setSearch("");
        setType(ALL);
        setSeverity(ALL);
        setRange("last7");
    };

    return (
        <div className="p-6 space-y-6">
            <div className="flex flex-col items-start justify-between gap-4 md:flex-row md:items-center">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("securityCenter.audit.title")}</h1>
                    <p className="mt-1 text-muted-foreground">{t("securityCenter.audit.subtitle")}</p>
                </div>
                <div className="flex gap-2">
                    <Button variant="outline" size="sm" onClick={exportCsv} disabled={rows.length === 0}>
                        <FileDown className="mr-2 h-4 w-4" />
                        {t("securityCenter.audit.exportCsv")}
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => void events.refetch()} disabled={events.isFetching}>
                        <RefreshCw className={`mr-2 h-4 w-4 ${events.isFetching ? "animate-spin" : ""}`} />
                        {t("securityCenter.audit.refreshLogs")}
                    </Button>
                </div>
            </div>

            {events.error ? (
                <Alert variant="destructive">
                    <AlertDescription>{apiErrorText(events.error, t)}</AlertDescription>
                </Alert>
            ) : null}

            <div className="grid gap-4 md:grid-cols-3">
                {(
                    [
                        ["securityCenter.audit.totalEvents", kpis.total, "securityCenter.audit.kpiLoaded", TrendingUp],
                        ["securityCenter.audit.elevatedSeverity", kpis.elevated, "securityCenter.audit.elevatedSeverityHint", Shield],
                        ["securityCenter.audit.roleChangesInRange", kpis.roles, "securityCenter.audit.roleChangesHint", Users],
                    ] as const
                ).map(([label, value, hint, Icon]) => (
                    <Card key={label}>
                        <CardContent className="pt-6">
                            <div className="flex items-center justify-between">
                                <div>
                                    <p className="text-sm font-medium text-muted-foreground">{t(label)}</p>
                                    <p className="text-2xl font-bold tabular-nums">{value}</p>
                                    <p className="text-xs text-muted-foreground">{t(hint)}</p>
                                </div>
                                <Icon className="h-8 w-8 text-muted-foreground" />
                            </div>
                        </CardContent>
                    </Card>
                ))}
            </div>

            <div className="space-y-4 rounded-lg border border-border/50 bg-card/50 p-4">
                <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
                    <ChevronDown className="h-4 w-4" />
                    {t("securityCenter.audit.filters")}
                </div>
                <div className="flex flex-wrap gap-4">
                    <Select value={range} onValueChange={(v) => setRange(v as EventRange)}>
                        <SelectTrigger className="w-[180px]" aria-label={t("securityCenter.audit.dateRange")}>
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            {EVENT_RANGES.map((r) => (
                                <SelectItem key={r} value={r}>
                                    {t(`securityCenter.audit.${r === "last7" ? "last7Days" : r === "last30" ? "last30Days" : "last90Days"}`)}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <Select value={type} onValueChange={setType}>
                        <SelectTrigger className="w-[240px]" aria-label={t("securityCenter.audit.actionType")}>
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value={ALL}>{t("securityCenter.audit.allActions")}</SelectItem>
                            {(types.length > 0 ? types : type !== ALL ? [type] : []).map((ty) => (
                                <SelectItem key={ty} value={ty}>
                                    {eventTypeLabel(ty, t)}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <Select value={severity} onValueChange={setSeverity}>
                        <SelectTrigger className="w-[180px]" aria-label={t("securityCenter.audit.severity")}>
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value={ALL}>{t("securityCenter.audit.allSeverities")}</SelectItem>
                            {SEVERITIES.map((s) => (
                                <SelectItem key={s} value={s}>
                                    {t(`securityCenter.severity.${s}`)}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <div className="relative min-w-[200px] flex-1">
                        <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                        <Input
                            placeholder={t("securityCenter.audit.searchLogs")}
                            aria-label={t("securityCenter.audit.searchLogs")}
                            className="pl-10"
                            value={search}
                            onChange={(e) => setSearch(e.target.value)}
                        />
                    </div>
                    <Button variant="ghost" size="sm" onClick={clearFilters}>
                        {t("securityCenter.audit.clearFilters")}
                    </Button>
                </div>
                {q ? (
                    <p className="text-xs text-muted-foreground" data-testid="search-scope">
                        {t("securityCenter.audit.searchLoadedOnly", { count: loaded.length })}
                    </p>
                ) : null}
                <OwnReadsToggle own={visible.own} show={showOwnReads} onChange={setShowOwnReads} t={t} />
            </div>

            <div className="overflow-hidden rounded-lg border border-border bg-card">
                {events.isPending ? (
                    <div className="flex justify-center py-16">
                        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                    </div>
                ) : (
                    <div className="overflow-x-auto">
                        <Table>
                            <TableHeader>
                                <TableRow className="bg-muted/50">
                                    <TableHead className="text-xs font-semibold uppercase">{t("securityCenter.audit.timestamp")}</TableHead>
                                    <TableHead className="text-xs font-semibold uppercase">{t("securityCenter.audit.performedBy")}</TableHead>
                                    <TableHead className="text-xs font-semibold uppercase">{t("securityCenter.audit.action")}</TableHead>
                                    <TableHead className="text-xs font-semibold uppercase">{t("securityCenter.audit.severity")}</TableHead>
                                    <TableHead className="text-xs font-semibold uppercase">{t("securityCenter.audit.target")}</TableHead>
                                    <TableHead className="text-xs font-semibold uppercase">{t("securityCenter.audit.changeDetails")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {rows.length === 0 ? (
                                    <TableRow>
                                        <TableCell colSpan={6} className="py-12 text-center text-muted-foreground">
                                            {t("securityCenter.audit.noRows")}
                                        </TableCell>
                                    </TableRow>
                                ) : (
                                    rows.map((r) => (
                                        <TableRow key={r.id} data-testid="event-row">
                                            <TableCell className="whitespace-nowrap text-sm text-muted-foreground">
                                                {format(new Date(r.createdAt), "MMM d, yyyy, HH:mm:ss")}
                                            </TableCell>
                                            <TableCell className="font-medium">{eventActor(r) || t("securityCenter.audit.system")}</TableCell>
                                            <TableCell>{eventTypeLabel(r.type, t)}</TableCell>
                                            <TableCell>
                                                <SeverityBadge severity={r.severity} t={t} />
                                            </TableCell>
                                            <TableCell className="text-sm">{eventTarget(r) || "—"}</TableCell>
                                            <TableCell className="max-w-md truncate text-sm" title={r.summary}>
                                                {r.summary}
                                            </TableCell>
                                        </TableRow>
                                    ))
                                )}
                            </TableBody>
                        </Table>
                    </div>
                )}
                <div className="flex flex-col items-start justify-between gap-2 border-t bg-muted/20 px-4 py-4 text-sm text-muted-foreground sm:flex-row sm:items-center">
                    <span data-testid="loaded-count">
                        {t("securityCenter.audit.loadedRows", { shown: rows.length, loaded: loaded.length })}
                        {pollPaused ? <span className="block text-xs" data-testid="poll-paused">{t("securityCenter.pollPaused")}</span> : null}
                    </span>
                    {events.hasNextPage ? (
                        <Button variant="outline" size="sm" onClick={() => void events.fetchNextPage()} disabled={events.isFetchingNextPage}>
                            {events.isFetchingNextPage ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                            {t("securityCenter.audit.loadMore")}
                        </Button>
                    ) : loaded.length > 0 ? (
                        <span>{t("securityCenter.audit.allLoaded")}</span>
                    ) : null}
                </div>
            </div>
        </div>
    );
}
