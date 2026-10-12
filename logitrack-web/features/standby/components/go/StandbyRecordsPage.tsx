"use client";

/**
 * Standby records over Go (T34, P2; Appendix E §E.2.1 row standby-records, §E.5 rows 15-17; TW8 M2).
 *
 * `['standby',{from,to}]` (`GET /v1/standby`, keyset 100 per page, "load more", no cap of 300): the range is the
 * server's filter, each row carries its driver's name and its photos signed; the detail reads the task's check-in
 * photo with `['tasks','detail',id]` while it is open. The backfill dialog (`operations:create_standby`) is mounted
 * on open only. SSE `standby.completed` / `standby.priced` refresh the list in every tab.
 */
import { useMemo, useState } from "react";
import dynamic from "next/dynamic";
import { keepPreviousData, useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { format, parseISO } from "date-fns";
import { enUS, th as thLocale } from "date-fns/locale";
import { Clock, MapPin, PauseCircle, Plus, RefreshCw, Search } from "lucide-react";

import { useLanguage } from "@/context/language";
import { ImagePreviewGallery } from "@/components/accounting/ImagePreviewGallery";
import { useCan } from "@/features/auth/api/useMe";
import { ApiErrorAlert, formatDateTime } from "@/features/accounting/components/go/shared";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { DateRangePicker } from "@/components/ui/date-range-picker";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { pageItems, taskDetailQuery } from "@/features/tasks/api/tasks.go";
import { CapabilityGate, LoadMoreFooter } from "@/features/tasks/components/go/opsShared";
import { standbyQuery, type StandbyDTO } from "../../api/standby.go";

const StandbyBackfillDialog = dynamic(() => import("./StandbyBackfillDialog").then((m) => m.StandbyBackfillDialog), { ssr: false });

export function StandbyRecordsPage() {
    return (
        <CapabilityGate capability="operations:view_driver_monitor" anyOf={["dispatch:view_operations"]}>
            <Records />
        </CapabilityGate>
    );
}

function matches(r: StandbyDTO, q: string): boolean {
    const term = q.trim().toLowerCase();
    if (!term) return true;
    return [r.driverName, r.startLocation, r.endLocation, r.note, r.truckLicensePlate].some((v) => (v ?? "").toLowerCase().includes(term));
}

function Records() {
    const { t, language } = useLanguage();
    const dateLocale = language === "th" ? thLocale : enUS;
    const canCreate = useCan("operations:create_standby").allowed;
    const [from, setFrom] = useState("");
    const [to, setTo] = useState("");
    const [search, setSearch] = useState("");
    const [detail, setDetail] = useState<StandbyDTO | null>(null);
    const [backfill, setBackfill] = useState(false);
    const q = useInfiniteQuery({ ...standbyQuery({ from, to }), placeholderData: keepPreviousData });
    const rows = useMemo(() => pageItems(q.data?.pages), [q.data]);
    const shown = useMemo(() => rows.filter((r) => matches(r, search)), [rows, search]);

    return (
        <div className="container mx-auto max-w-[1600px] space-y-6 p-6">
            <div className="flex flex-col items-start justify-between gap-4 md:flex-row md:items-center">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("standbyRecords.title", "Standby Records")}</h1>
                    <p className="mt-1 text-muted-foreground">{t("standbyRecords.subtitle", "Driver check-in records with no delivery jobs")}</p>
                </div>
                <div className="flex items-center gap-2">
                    {canCreate ? (
                        <Button onClick={() => setBackfill(true)} className="gap-2">
                            <Plus className="h-4 w-4" />
                            {t("standbyRecords.create.button", "Backfill Standby")}
                        </Button>
                    ) : null}
                    <Button variant="outline" size="icon" onClick={() => void q.refetch()} aria-label={t("taskBoard.refresh")}>
                        <RefreshCw className={cn("h-4 w-4", q.isFetching && "animate-spin")} />
                    </Button>
                </div>
            </div>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <Card className="bg-card">
                    <CardContent className="flex items-center justify-between p-6">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">{t("standbyRecords.stats.total", "Total Records")}</p>
                            <h2 className="mt-1 text-3xl font-bold">
                                {rows.length}
                                {q.hasNextPage ? "+" : ""}
                            </h2>
                        </div>
                        <div className="flex h-12 w-12 items-center justify-center rounded-full bg-orange-100 text-orange-500 dark:bg-orange-900/30">
                            <PauseCircle className="h-6 w-6" />
                        </div>
                    </CardContent>
                </Card>
            </div>

            <div className="flex flex-col flex-wrap gap-4 rounded-lg border border-border/50 bg-card/50 p-4 sm:flex-row">
                <DateRangePicker
                    from={from ? parseISO(from) : null}
                    to={to ? parseISO(to) : null}
                    onChange={(f, tt) => {
                        setFrom(f ? format(f, "yyyy-MM-dd") : "");
                        setTo(tt ? format(tt, "yyyy-MM-dd") : "");
                    }}
                    locale={dateLocale}
                    placeholder={t("standbyRecords.filter.dateRange", "Date range")}
                    className="w-[230px]"
                />
                <div className="relative max-w-sm flex-1">
                    <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                    <Input placeholder={t("standbyRecords.filter.search", "Search driver, location...")} className="h-9 border-border/50 bg-background/50 pl-10"
                        value={search} onChange={(e) => setSearch(e.target.value)} />
                </div>
                {from || to ? (
                    <Button variant="ghost" size="sm" className="text-muted-foreground" onClick={() => { setFrom(""); setTo(""); }}>
                        ✕
                    </Button>
                ) : null}
            </div>
            {search.trim() && q.hasNextPage ? <p className="text-xs text-muted-foreground">{t("taskBoard.searchLoadedOnly")}</p> : null}

            <ApiErrorAlert error={q.error} />

            <div className="overflow-hidden rounded-lg border bg-card">
                <div className="overflow-x-auto">
                    <Table className="min-w-[900px]">
                        <TableHeader className="bg-muted/50">
                            <TableRow>
                                {["date", "driver", "route", "startedAt", "endedAt", "duration", "status"].map((k) => (
                                    <TableHead key={k} className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{t(`standbyRecords.table.${k}`)}</TableHead>
                                ))}
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {q.isPending ? (
                                <TableRow>
                                    <TableCell colSpan={7} className="h-32 text-center text-sm text-muted-foreground">{t("standbyRecords.table.loading", "Loading...")}</TableCell>
                                </TableRow>
                            ) : shown.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={7} className="h-32 text-center text-sm text-muted-foreground">{t("standbyRecords.table.noRecords", "No standby records found")}</TableCell>
                                </TableRow>
                            ) : (
                                shown.map((r) => (
                                    <TableRow key={r.id} className="cursor-pointer border-b border-border/50 transition-colors hover:bg-orange-50/50 dark:hover:bg-orange-900/10" onClick={() => setDetail(r)}>
                                        <TableCell className="text-sm">{formatDateTime(r.createdAt)}</TableCell>
                                        <TableCell><span className="text-sm font-medium">{r.driverName ?? "—"}</span></TableCell>
                                        <TableCell>
                                            <span className="text-sm text-muted-foreground">
                                                {r.startLocation ?? "—"}
                                                {r.startLocation && r.endLocation ? "  →  " : ""}
                                                {r.endLocation ?? ""}
                                            </span>
                                        </TableCell>
                                        <TableCell className="text-sm">{formatDateTime(r.startedAt)}</TableCell>
                                        <TableCell className="text-sm">{formatDateTime(r.endedAt)}</TableCell>
                                        <TableCell className="font-mono text-sm">{r.durationMinutes ?? "—"}</TableCell>
                                        <TableCell>
                                            <Badge variant="outline" className="border-orange-200 bg-orange-50 font-medium text-orange-700 dark:border-orange-900 dark:bg-orange-950/30 dark:text-orange-400">
                                                {r.status.toUpperCase()}
                                            </Badge>
                                        </TableCell>
                                    </TableRow>
                                ))
                            )}
                        </TableBody>
                    </Table>
                </div>
                <LoadMoreFooter loaded={rows.length} hasNextPage={Boolean(q.hasNextPage)} isFetchingNextPage={q.isFetchingNextPage} onLoadMore={() => void q.fetchNextPage()} />
            </div>

            {backfill ? <StandbyBackfillDialog onClose={() => setBackfill(false)} /> : null}
            {detail ? <StandbyDetail record={detail} onClose={() => setDetail(null)} /> : null}
        </div>
    );
}

function StandbyDetail({ record, onClose }: { record: StandbyDTO; onClose: () => void }) {
    const { t } = useLanguage();
    const task = useQuery(taskDetailQuery(record.taskId ?? "", Boolean(record.taskId)));
    const checkIn = task.data?.checkInPhoto?.url ?? null;
    const items = [
        ...(checkIn ? [{ url: checkIn, label: t("standbyRecords.detail.checkInPhoto", "Check-in Photo") }] : []),
        ...record.photos
            .filter((p) => p.url)
            .map((p) => ({
                url: p.url ?? "",
                label: p.type === "customer_worksheet" ? t("standbyRecords.detail.worksheetPhoto", "Customer Worksheet") : t("standbyRecords.detail.sitePhoto", "Site Photo"),
            })),
    ];
    return (
        <Dialog open onOpenChange={(o) => !o && onClose()}>
            <DialogContent className="max-h-[90vh] max-w-xl overflow-y-auto">
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        <PauseCircle className="h-5 w-5 text-orange-500" />
                        {t("standbyRecords.detail.title", "Standby Record Detail")}
                    </DialogTitle>
                    <DialogDescription>ID: {record.id}</DialogDescription>
                </DialogHeader>
                <div className="grid gap-4 py-2">
                    <div className="space-y-2 rounded-lg border border-orange-200 bg-orange-50 p-4 dark:border-orange-900 dark:bg-orange-950/20">
                        <div className="flex items-center gap-2 text-sm font-medium text-orange-900 dark:text-orange-100">
                            <MapPin className="h-4 w-4" />
                            {record.startLocation ?? "—"} → {record.endLocation ?? "—"}
                        </div>
                        <div className="flex items-center gap-2 text-xs text-orange-700 dark:text-orange-400">
                            <Clock className="h-3.5 w-3.5" />
                            {formatDateTime(record.startedAt)} → {formatDateTime(record.endedAt)}
                            {record.durationMinutes != null ? (
                                <span className="ml-2 font-mono">({record.durationMinutes} {t("standbyRecords.detail.minutes", "min")})</span>
                            ) : null}
                        </div>
                    </div>
                    <div className="grid grid-cols-2 gap-4 rounded-lg bg-muted/30 p-4 text-sm">
                        <div>
                            <p className="mb-1 text-xs uppercase tracking-wider text-muted-foreground">{t("standbyRecords.table.driver", "Driver")}</p>
                            <p className="font-medium">{record.driverName ?? "—"}</p>
                        </div>
                        <div>
                            <p className="mb-1 text-xs uppercase tracking-wider text-muted-foreground">{t("standbyRecords.detail.taskId", "Task ID")}</p>
                            <p className="font-mono text-xs">{task.data?.taskNo ?? record.taskId?.slice(0, 12) ?? "—"}</p>
                        </div>
                        {record.truckLicensePlate ? (
                            <div>
                                <p className="mb-1 text-xs uppercase tracking-wider text-muted-foreground">{t("incidentReports.table.plate")}</p>
                                <p className="font-mono">{record.truckLicensePlate}</p>
                            </div>
                        ) : null}
                        {record.note ? (
                            <div className="col-span-2">
                                <p className="mb-1 text-xs uppercase tracking-wider text-muted-foreground">{t("standbyRecords.detail.note", "Note")}</p>
                                <p className="text-sm">{record.note}</p>
                            </div>
                        ) : null}
                        {record.lat != null && record.lng != null ? (
                            <div className="col-span-2">
                                <p className="mb-1 text-xs uppercase tracking-wider text-muted-foreground">{t("standbyRecords.detail.location", "Location")}</p>
                                <a href={`https://www.google.com/maps?q=${record.lat},${record.lng}`} target="_blank" rel="noreferrer" className="font-mono text-xs text-blue-500 hover:underline">
                                    {record.lat.toFixed(5)}, {record.lng.toFixed(5)}
                                </a>
                            </div>
                        ) : null}
                    </div>
                    {items.length > 0 ? <ImagePreviewGallery compact items={items} /> : null}
                </div>
            </DialogContent>
        </Dialog>
    );
}
