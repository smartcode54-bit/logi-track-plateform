"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { format } from "date-fns";
import {
    collection,
    getDocs,
    limit,
    orderBy,
    query,
    Timestamp,
    where,
    type QueryConstraint,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useLanguage } from "@/context/language";
import { usePermission } from "@/hooks/usePermission";
import { CAPABILITIES } from "@/lib/capabilities";
import type { SecurityEventRow } from "@/hooks/useSecurityEventsFeed";
import { SECURITY_EVENT_TYPES } from "@/lib/securityEventsConstants";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { Shield, TrendingUp, Users, ChevronDown, Search, RefreshCw, FileDown, Loader2 } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Alert, AlertDescription } from "@/components/ui/alert";

const PAGE_SIZE = 15;
const FETCH_LIMIT = 500;

function startDateForRange(range: string): Date {
    const d = new Date();
    if (range === "last30") d.setDate(d.getDate() - 30);
    else if (range === "last90") d.setDate(d.getDate() - 90);
    else d.setDate(d.getDate() - 7);
    d.setHours(0, 0, 0, 0);
    return d;
}

function mapDoc(id: string, data: Record<string, unknown>): SecurityEventRow {
    const createdAt = (data.createdAt as Timestamp | undefined) ?? null;
    return {
        id,
        createdAt,
        type: typeof data.type === "string" ? data.type : "unknown",
        severity: typeof data.severity === "string" ? data.severity : "info",
        summary: typeof data.summary === "string" ? data.summary : "",
        actorUid: typeof data.actorUid === "string" ? data.actorUid : "",
        actorEmail: typeof data.actorEmail === "string" ? data.actorEmail : null,
        details: data.details && typeof data.details === "object" && !Array.isArray(data.details)
            ? (data.details as Record<string, unknown>)
            : {},
    };
}

function eventTypeLabelKey(type: string): string {
    return `securityCenter.securityEvents.types.${type}`;
}

function targetSummary(details: Record<string, unknown>): string {
    const email = details.email;
    const targetUid = details.targetUid;
    if (typeof email === "string" && email) return email;
    if (typeof targetUid === "string" && targetUid) return targetUid;
    return "—";
}

export default function SecurityAuditPage() {
    const { t } = useLanguage();
    const { hasPermission, loading: permLoading } = usePermission(CAPABILITIES.security_view_audit);
    const [dateRange, setDateRange] = useState("last7");
    const [actionFilter, setActionFilter] = useState("all");
    const [searchQuery, setSearchQuery] = useState("");
    const [currentPage, setCurrentPage] = useState(1);
    const [rows, setRows] = useState<SecurityEventRow[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const load = useCallback(async () => {
        if (!hasPermission) {
            setRows([]);
            setLoading(false);
            return;
        }
        setLoading(true);
        setError(null);
        try {
            const start = Timestamp.fromDate(startDateForRange(dateRange));
            const constraints: QueryConstraint[] =
                actionFilter === "all"
                    ? [where("createdAt", ">=", start), orderBy("createdAt", "asc"), limit(FETCH_LIMIT)]
                    : [
                          where("type", "==", actionFilter),
                          where("createdAt", ">=", start),
                          orderBy("createdAt", "asc"),
                          limit(FETCH_LIMIT),
                      ];
            const snap = await getDocs(query(collection(db, COLLECTIONS.SECURITY_EVENTS), ...constraints));
            const list = snap.docs
                .map((d) => mapDoc(d.id, d.data() as Record<string, unknown>))
                .reverse();
            setRows(list);
        } catch (e) {
            console.error("[SecurityAudit]", e);
            setError((e as Error).message || "load failed");
            setRows([]);
        } finally {
            setLoading(false);
        }
    }, [hasPermission, dateRange, actionFilter]);

    useEffect(() => {
        if (permLoading) return;
        void load();
    }, [permLoading, load]);

    useEffect(() => {
        setCurrentPage(1);
    }, [dateRange, actionFilter, searchQuery]);

    const filtered = useMemo(() => {
        const q = searchQuery.trim().toLowerCase();
        if (!q) return rows;
        return rows.filter((r) => {
            const hay = [
                r.summary,
                r.actorEmail ?? "",
                r.actorUid,
                r.type,
                JSON.stringify(r.details),
            ]
                .join(" ")
                .toLowerCase();
            return hay.includes(q);
        });
    }, [rows, searchQuery]);

    const kpis = useMemo(() => {
        const total = filtered.length;
        const elevated = filtered.filter((r) => r.severity === "warning" || r.severity === "critical").length;
        const roleRelated = filtered.filter((r) => r.type === "role_matrix_saved" || r.type === "user_role_changed").length;
        return { total, elevated, roleRelated };
    }, [filtered]);

    const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
    const page = Math.min(currentPage, totalPages);
    const pageRows = filtered.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);

    const exportCsv = () => {
        const header = ["timestamp", "type", "severity", "actorEmail", "actorUid", "summary", "details"];
        const lines = [header.join(",")];
        for (const r of filtered) {
            const ts = r.createdAt ? format(r.createdAt.toDate(), "yyyy-MM-dd'T'HH:mm:ss") : "";
            const det = JSON.stringify(r.details).replaceAll('"', '""');
            lines.push(
                [ts, r.type, r.severity, r.actorEmail ?? "", r.actorUid, r.summary.replaceAll('"', '""'), `"${det}"`].join(
                    ",",
                ),
            );
        }
        const blob = new Blob([lines.join("\n")], { type: "text/csv;charset=utf-8" });
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = `security_events_${format(new Date(), "yyyyMMdd_HHmm")}.csv`;
        a.click();
        URL.revokeObjectURL(url);
    };

    if (permLoading) {
        return (
            <div className="p-6 flex items-center justify-center min-h-[50vh]">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
        );
    }

    if (!hasPermission) {
        return (
            <div className="p-6">
                <Alert>
                    <AlertDescription>{t("securityCenter.audit.accessDenied")}</AlertDescription>
                </Alert>
            </div>
        );
    }

    return (
        <div className="p-6 space-y-6">
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("securityCenter.audit.title")}</h1>
                    <p className="text-muted-foreground mt-1">{t("securityCenter.audit.subtitle")}</p>
                </div>
                <div className="flex gap-2">
                    <Button variant="outline" size="sm" onClick={exportCsv} disabled={filtered.length === 0}>
                        <FileDown className="mr-2 h-4 w-4" />
                        {t("securityCenter.audit.exportCsv")}
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
                        <RefreshCw className={`mr-2 h-4 w-4 ${loading ? "animate-spin" : ""}`} />
                        {t("securityCenter.audit.refreshLogs")}
                    </Button>
                </div>
            </div>

            {error ? (
                <Alert variant="destructive">
                    <AlertDescription>{error}</AlertDescription>
                </Alert>
            ) : null}

            <div className="grid gap-4 md:grid-cols-3">
                <Card>
                    <CardContent className="pt-6">
                        <div className="flex items-center justify-between">
                            <div>
                                <p className="text-sm font-medium text-muted-foreground">{t("securityCenter.audit.totalEvents")}</p>
                                <p className="text-2xl font-bold">{kpis.total}</p>
                                <p className="text-xs text-muted-foreground">{t("securityCenter.audit.kpiInDateRange")}</p>
                            </div>
                            <TrendingUp className="h-8 w-8 text-muted-foreground" />
                        </div>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-6">
                        <div className="flex items-center justify-between">
                            <div>
                                <p className="text-sm font-medium text-muted-foreground">{t("securityCenter.audit.elevatedSeverity")}</p>
                                <p className="text-2xl font-bold">{kpis.elevated}</p>
                                <p className="text-xs text-muted-foreground">{t("securityCenter.audit.elevatedSeverityHint")}</p>
                            </div>
                            <Shield className="h-8 w-8 text-muted-foreground" />
                        </div>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-6">
                        <div className="flex items-center justify-between">
                            <div>
                                <p className="text-sm font-medium text-muted-foreground">{t("securityCenter.audit.roleChangesInRange")}</p>
                                <p className="text-2xl font-bold">{kpis.roleRelated}</p>
                                <p className="text-xs text-muted-foreground">{t("securityCenter.audit.roleChangesHint")}</p>
                            </div>
                            <Users className="h-8 w-8 text-muted-foreground" />
                        </div>
                    </CardContent>
                </Card>
            </div>

            <div className="space-y-4 rounded-lg border border-border/50 bg-card/50 p-4">
                <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
                    <ChevronDown className="h-4 w-4" />
                    {t("securityCenter.audit.filters")}
                </div>
                <div className="flex flex-wrap gap-4">
                    <Select value={dateRange} onValueChange={setDateRange}>
                        <SelectTrigger className="w-[180px]">
                            <SelectValue placeholder={t("securityCenter.audit.dateRange")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="last7">{t("securityCenter.audit.last7Days")}</SelectItem>
                            <SelectItem value="last30">{t("securityCenter.audit.last30Days")}</SelectItem>
                            <SelectItem value="last90">{t("securityCenter.audit.last90Days")}</SelectItem>
                        </SelectContent>
                    </Select>
                    <Select value={actionFilter} onValueChange={setActionFilter}>
                        <SelectTrigger className="w-[220px]">
                            <SelectValue placeholder={t("securityCenter.audit.actionType")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t("securityCenter.audit.allActions")}</SelectItem>
                            {SECURITY_EVENT_TYPES.map((ty) => (
                                <SelectItem key={ty} value={ty}>
                                    {t(eventTypeLabelKey(ty))}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <div className="relative flex-1 min-w-[200px]">
                        <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                        <Input
                            placeholder={t("securityCenter.audit.searchLogs")}
                            className="pl-10"
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                        />
                    </div>
                    <Button variant="ghost" size="sm" onClick={() => setSearchQuery("")}>
                        {t("securityCenter.audit.clearFilters")}
                    </Button>
                </div>
            </div>

            <div className="rounded-lg border border-border bg-card overflow-hidden">
                {loading ? (
                    <div className="flex justify-center py-16">
                        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                    </div>
                ) : (
                    <Table>
                        <TableHeader>
                            <TableRow className="bg-muted/50">
                                <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.audit.timestamp")}</TableHead>
                                <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.audit.performedBy")}</TableHead>
                                <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.audit.action")}</TableHead>
                                <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.audit.targetRole")}</TableHead>
                                <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.audit.changeDetails")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {pageRows.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={5} className="text-center text-muted-foreground py-12">
                                        {t("securityCenter.audit.noRows")}
                                    </TableCell>
                                </TableRow>
                            ) : (
                                pageRows.map((log) => (
                                    <TableRow key={log.id}>
                                        <TableCell className="text-sm text-muted-foreground whitespace-nowrap">
                                            {log.createdAt ? format(log.createdAt.toDate(), "MMM d, yyyy, HH:mm:ss") : "—"}
                                        </TableCell>
                                        <TableCell>
                                            <span className="font-medium">{log.actorEmail || log.actorUid || "—"}</span>
                                        </TableCell>
                                        <TableCell>{t(eventTypeLabelKey(log.type))}</TableCell>
                                        <TableCell className="text-sm">{targetSummary(log.details)}</TableCell>
                                        <TableCell className="text-sm max-w-md truncate" title={log.summary}>
                                            {log.summary}
                                        </TableCell>
                                    </TableRow>
                                ))
                            )}
                        </TableBody>
                    </Table>
                )}
                <div className="flex items-center justify-between px-4 py-4 border-t bg-muted/20 text-sm text-muted-foreground">
                    <span>
                        {t("securityCenter.audit.showingResults")
                            .replace("{count}", String(pageRows.length))
                            .replace("{total}", String(filtered.length))}
                    </span>
                    <div className="flex gap-2">
                        <Button
                            variant="outline"
                            size="sm"
                            disabled={page <= 1}
                            onClick={() => setCurrentPage((p) => Math.max(1, p - 1))}
                        >
                            {t("securityCenter.audit.previous")}
                        </Button>
                        <span className="flex items-center px-2 text-xs tabular-nums">
                            {page} / {totalPages}
                        </span>
                        <Button
                            variant="outline"
                            size="sm"
                            disabled={page >= totalPages}
                            onClick={() => setCurrentPage((p) => Math.min(totalPages, p + 1))}
                        >
                            {t("securityCenter.audit.next")}
                        </Button>
                    </div>
                </div>
            </div>
        </div>
    );
}
