"use client";

/**
 * Incident reports over Go (T34, P2; Appendix E §E.2.1 row incident-reports, §E.5 rows 7-8; TW8 M2).
 *
 * `['incidents',{from,to}]` (`GET /v1/incidents`, keyset 100 per page, "load more", no cap of 200): the day picked
 * is the server's range; each row carries its trip number, driver name and photos signed; a customer reads the
 * incidents of its own trips only (RLS), never everything. The map loads its leaflet chunk when a report with a
 * position is opened (the component file, not the feature barrel). SSE `incident.created` refreshes the list.
 */
import { useMemo, useState } from "react";
import { keepPreviousData, useInfiniteQuery } from "@tanstack/react-query";
import { format } from "date-fns";
import { AlertTriangle, CalendarIcon, Clock, Navigation, RefreshCw, Search } from "lucide-react";

import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { ImagePreviewGallery } from "@/components/accounting/ImagePreviewGallery";
import { IncidentLocationMap } from "@/features/incident-reports/components/IncidentLocationMap";
import { ApiErrorAlert, formatDateTime } from "@/features/accounting/components/go/shared";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { pageItems } from "@/features/tasks/api/tasks.go";
import { CapabilityGate, LoadMoreFooter } from "@/features/tasks/components/go/opsShared";
import { incidentCause, incidentsQuery, matchesIncident, type IncidentDTO } from "../../api/incidents.go";

export function IncidentReportsPage() {
    return (
        <CapabilityGate capability="operations:view_incidents" anyOf={["dispatch:view_operations"]}>
            <Reports />
        </CapabilityGate>
    );
}

function Reports() {
    const { t } = useLanguage();
    const [date, setDate] = useState<Date | undefined>(undefined);
    const [search, setSearch] = useState("");
    const [detail, setDetail] = useState<IncidentDTO | null>(null);
    const day = date ? format(date, "yyyy-MM-dd") : "";
    const q = useInfiniteQuery({ ...incidentsQuery({ from: day, to: day }), placeholderData: keepPreviousData });
    const rows = useMemo(() => pageItems(q.data?.pages), [q.data]);
    const shown = useMemo(() => rows.filter((r) => matchesIncident(r, search)), [rows, search]);

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.incidentReports")}
                description={t("incidentReports.subtitle")}
                actions={
                    <Button variant="outline" size="icon" onClick={() => void q.refetch()} aria-label={t("taskBoard.refresh")}>
                        <RefreshCw className={cn("h-4 w-4", q.isFetching && "animate-spin")} aria-hidden />
                    </Button>
                }
            />

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <Card className="bg-card">
                    <CardContent className="flex items-center justify-between p-6">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">{t("incidentReports.stats.total")}</p>
                            <h2 className="mt-1 text-3xl font-bold">
                                {rows.length}
                                {q.hasNextPage ? "+" : ""}
                            </h2>
                        </div>
                        <div className="flex h-12 w-12 items-center justify-center rounded-full bg-red-100 text-red-500 dark:bg-red-900/30">
                            <AlertTriangle className="h-6 w-6" />
                        </div>
                    </CardContent>
                </Card>
            </div>

            <div className="flex flex-col flex-wrap gap-4 rounded-lg border border-border/50 bg-card/50 p-4 sm:flex-row">
                <Popover>
                    <PopoverTrigger asChild>
                        <Button variant="outline" className={cn("w-[220px] justify-start text-left font-normal", !date && "text-muted-foreground")}>
                            <CalendarIcon className="mr-2 h-4 w-4" />
                            {date ? format(date, "dd/MM/yyyy") : t("incidentReports.filter.pickDate")}
                        </Button>
                    </PopoverTrigger>
                    <PopoverContent className="w-auto p-0" align="start">
                        <Calendar mode="single" selected={date} onSelect={setDate} initialFocus />
                    </PopoverContent>
                </Popover>
                {date ? (
                    <Button variant="ghost" size="sm" className="text-muted-foreground" onClick={() => setDate(undefined)}>
                        ✕
                    </Button>
                ) : null}
                <div className="relative max-w-sm flex-1">
                    <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                    <Input placeholder={t("incidentReports.filter.search")} className="h-9 border-border/50 bg-background/50 pl-10" value={search}
                        onChange={(e) => setSearch(e.target.value)} />
                </div>
            </div>
            {search.trim() && q.hasNextPage ? <p className="text-xs text-muted-foreground">{t("taskBoard.searchLoadedOnly")}</p> : null}

            <ApiErrorAlert error={q.error} />

            <div className="overflow-hidden rounded-lg border bg-card">
                <div className="overflow-x-auto">
                    <Table className="min-w-[900px]">
                        <TableHeader className="bg-muted/50">
                            <TableRow>
                                {["tripId", "createdAt", "driver", "plate", "description", "cause"].map((k) => (
                                    <TableHead key={k} className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{t(`incidentReports.table.${k}`)}</TableHead>
                                ))}
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {q.isPending ? (
                                <TableRow>
                                    <TableCell colSpan={6} className="h-32 text-center text-sm text-muted-foreground">{t("incidentReports.table.loading")}</TableCell>
                                </TableRow>
                            ) : shown.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={6} className="h-32 text-center text-sm text-muted-foreground">{t("incidentReports.table.noReports")}</TableCell>
                                </TableRow>
                            ) : (
                                shown.map((r) => (
                                    <TableRow key={r.id} className="cursor-pointer border-b border-border/50 hover:bg-red-50/50 dark:hover:bg-red-900/10" onClick={() => setDetail(r)}>
                                        <TableCell className="font-mono text-xs">{r.tripNo ?? "—"}</TableCell>
                                        <TableCell className="text-sm">{formatDateTime(r.createdAt)}</TableCell>
                                        <TableCell className="text-sm font-medium">{r.driverName ?? "—"}</TableCell>
                                        <TableCell className="font-mono text-sm">{r.truckLicensePlate ?? "—"}</TableCell>
                                        <TableCell className="max-w-[300px] truncate text-sm">{r.description ?? "—"}</TableCell>
                                        <TableCell>
                                            {r.delayCause ? <Badge variant="outline" className="border-red-200 bg-red-50 text-red-700">{incidentCause(r.delayCause)}</Badge> : "—"}
                                        </TableCell>
                                    </TableRow>
                                ))
                            )}
                        </TableBody>
                    </Table>
                </div>
                <LoadMoreFooter loaded={rows.length} hasNextPage={Boolean(q.hasNextPage)} isFetchingNextPage={q.isFetchingNextPage} onLoadMore={() => void q.fetchNextPage()} />
            </div>

            {detail ? (
                <Dialog open onOpenChange={(o) => !o && setDetail(null)}>
                    <DialogContent className="max-h-[90vh] max-w-xl overflow-y-auto">
                        <DialogHeader>
                            <DialogTitle className="flex items-center gap-2">
                                <AlertTriangle className="h-5 w-5 text-red-500" />
                                {t("incidentReports.detail.title")}
                            </DialogTitle>
                            <DialogDescription>ID: {detail.id}</DialogDescription>
                        </DialogHeader>
                        <div className="grid gap-4 py-2">
                            <div className="space-y-2 rounded-lg border border-red-200 bg-red-50 p-4 dark:border-red-900 dark:bg-red-950/20">
                                <p className="text-sm text-red-900 dark:text-red-100">{detail.description ?? "—"}</p>
                                {detail.delayCause ? (
                                    <p className="text-xs font-medium text-red-700">{t("incidentReports.detail.cause")}: {incidentCause(detail.delayCause)}</p>
                                ) : null}
                                <p className="flex items-center gap-1 text-xs text-red-700">
                                    <Clock className="h-3.5 w-3.5" />
                                    {t("incidentReports.detail.reported")}: {formatDateTime(detail.createdAt)}
                                </p>
                            </div>
                            <div className="grid grid-cols-2 gap-4 rounded-lg bg-muted/30 p-4 text-sm">
                                <div>
                                    <p className="mb-1 text-xs uppercase tracking-wider text-muted-foreground">{t("incidentReports.table.driver")}</p>
                                    <p className="font-medium">{detail.driverName ?? "—"}</p>
                                </div>
                                <div>
                                    <p className="mb-1 text-xs uppercase tracking-wider text-muted-foreground">{t("incidentReports.detail.licensePlate")}</p>
                                    <p className="font-mono">{detail.truckLicensePlate ?? "—"}</p>
                                </div>
                                <div className="col-span-2">
                                    <p className="mb-1 text-xs uppercase tracking-wider text-muted-foreground">{t("incidentReports.table.tripId")}</p>
                                    <p className="font-mono text-xs">{detail.tripNo ?? "—"}</p>
                                </div>
                            </div>
                            {detail.lat != null && detail.lng != null ? (
                                <div>
                                    <p className="mb-2 text-xs uppercase tracking-wider text-muted-foreground">{t("incidentReports.detail.locationMap")}</p>
                                    <div className="h-[220px] overflow-hidden rounded-lg border">
                                        <IncidentLocationMap lat={detail.lat} lng={detail.lng} />
                                    </div>
                                    <a href={`https://www.google.com/maps?q=${detail.lat},${detail.lng}`} target="_blank" rel="noreferrer"
                                        className="mt-1 flex items-center gap-1 text-xs text-blue-500 hover:underline">
                                        <Navigation className="h-3 w-3" />
                                        {detail.lat.toFixed(5)}, {detail.lng.toFixed(5)} ({t("incidentReports.detail.openInMaps")})
                                    </a>
                                </div>
                            ) : null}
                            <ImagePreviewGallery compact items={[
                                detail.mapPhoto?.url ? { url: detail.mapPhoto.url, label: t("incidentReports.detail.locationSnapshot") } : null,
                                detail.situation1Photo?.url ? { url: detail.situation1Photo.url, label: t("incidentReports.detail.situation1") } : null,
                                detail.situation2Photo?.url ? { url: detail.situation2Photo.url, label: t("incidentReports.detail.situation2") } : null,
                            ].filter((x): x is { url: string; label: string } => x !== null)} />
                        </div>
                    </DialogContent>
                </Dialog>
            ) : null}
        </div>
    );
}
