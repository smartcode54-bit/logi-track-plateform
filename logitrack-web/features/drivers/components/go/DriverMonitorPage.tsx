"use client";

/**
 * The driver monitor over Go (T34, P2; developer-spec.md §10.9 W8, §10.13; Appendix E §E.2.1 row driver-monitor,
 * §E.5 rows 24-27; TW6 and TW8 M2).
 *
 * One aggregate request per page of 200 trips (`['trips','monitor',{from,to,filters}]` over `GET /v1/trips/monitor`):
 * the server joins the task, the driver, the incidents and the places, filters in SQL and counts the options of the
 * range (`meta.facets`), so the page opens no listener, reads no rate card and prices nothing (the stored price
 * shows in the trip detail and the export for `accounting:view_income`; the table has no revenue column, as the
 * owner's July layout). The rows are a `VirtualTable` (developer-spec.md §10.10 W9: "monitor trips, income | 200 |
 * Table + Virtual"): only the rows in view are in the DOM however many pages are loaded, and scrolling near the end
 * loads the next page. SSE `trip.*`, `standby.*`, `incident.created` and `task.*` refresh it through the 2 s window.
 * The export pages through the same route with its cursor and loads xlsx on the click.
 */
import { useMemo, useState } from "react";
import dynamic from "next/dynamic";
import { keepPreviousData, useInfiniteQuery } from "@tanstack/react-query";
import { createColumnHelper } from "@tanstack/react-table";
import { differenceInCalendarDays, endOfMonth, format, parseISO, startOfMonth, subDays } from "date-fns";
import { enUS, th as thLocale } from "date-fns/locale";
import { AlertTriangle, ChevronDown, Download, Loader2, Package, PackageCheck, RefreshCw, Search, Truck, X } from "lucide-react";

import { useLanguage } from "@/context/language";
import { PlaceFilterCombobox } from "@/components/place-filter-combobox";
import { PlateFilterCombobox } from "@/components/plate-filter-combobox";
import { ApiErrorAlert, formatDateTime, SearchPicker, useDebounced, type PickValue } from "@/features/accounting/components/go/shared";
import { cn } from "@/lib/utils";
import { VirtualTable } from "@/components/virtual-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { DateRangePicker } from "@/components/ui/date-range-picker";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { CapabilityGate, LoadMoreFooter } from "@/features/tasks/components/go/opsShared";
import { pageItems } from "@/features/tasks/api/tasks.go";
import {
    activeMonitorFilters,
    deliveredTimeOf,
    emptyMonitorFilter,
    FILTER_NONE,
    MONITOR_ALL,
    MONITOR_DEFAULT_DAYS,
    MONITOR_MAX_DAYS,
    monitorFacets,
    monitorQuery,
    placeLabel,
    TRIP_STATUSES,
    type MonitorFilter,
    type MonitorRowDTO,
} from "../../api/monitor.go";
import { JOB_TYPE_COLOR, STATUS_COLOR } from "./monitorStyles";

const TripDetailDialog = dynamic(() => import("./TripDetailDialog").then((m) => m.TripDetailDialog), { ssr: false });
const MonitorExportDialog = dynamic(() => import("./MonitorExportDialog").then((m) => m.MonitorExportDialog), { ssr: false });

const dayKey = (d: Date) => format(d, "yyyy-MM-dd");
const col = createColumnHelper<MonitorRowDTO>();
const HEAD = "text-xs font-semibold uppercase tracking-wider text-muted-foreground";

/** The monitor's maximum table height, px (the page scrolls the table, not the window). */
export const MONITOR_TABLE_MAX_HEIGHT = 640;

function defaultRange(): { from: string; to: string } {
    const now = new Date();
    return { from: dayKey(subDays(now, MONITOR_DEFAULT_DAYS - 1)), to: dayKey(now) };
}

export function DriverMonitorPage() {
    return (
        <CapabilityGate capability="operations:view_driver_monitor" anyOf={["dispatch:view_operations"]}>
            <Monitor />
        </CapabilityGate>
    );
}

function Monitor() {
    const { t, language } = useLanguage();
    const dateLocale = language === "th" ? thLocale : enUS;
    const [filter, setFilter] = useState<MonitorFilter>(() => {
        const r = defaultRange();
        return emptyMonitorFilter(r.from, r.to);
    });
    const [driver, setDriver] = useState<PickValue | null>(null);
    const [search, setSearch] = useState("");
    const q = useDebounced(search);
    const [rangeNote, setRangeNote] = useState(false);
    const [detail, setDetail] = useState<MonitorRowDTO | null>(null);
    const [exportOpen, setExportOpen] = useState(false);

    const effective: MonitorFilter = { ...filter, q, driverId: driver?.id ?? "" };
    const list = useInfiniteQuery({ ...monitorQuery(effective), placeholderData: keepPreviousData });
    const rows = useMemo(() => pageItems(list.data?.pages), [list.data]);
    const facets = monitorFacets(list.data?.pages);
    const set = <K extends keyof MonitorFilter>(k: K, v: MonitorFilter[K]) => setFilter((prev) => ({ ...prev, [k]: v }));
    const active = activeMonitorFilters(effective);

    const setRange = (from: Date | undefined, to: Date | undefined) => {
        if (!from || !to) return;
        const span = differenceInCalendarDays(to, from) + 1;
        const clamped = span > MONITOR_MAX_DAYS ? subDays(to, MONITOR_MAX_DAYS - 1) : from;
        setRangeNote(span > MONITOR_MAX_DAYS);
        setFilter((prev) => ({ ...prev, from: dayKey(clamped), to: dayKey(to) }));
    };
    const clearAll = () => {
        setFilter((prev) => emptyMonitorFilter(prev.from, prev.to));
        setDriver(null);
        setSearch("");
    };
    const jobTypeLabel = (j: string) => (j === "line_haul" ? t("driverMonitor.jobType.lineHaul") : t("driverMonitor.jobType.firstMile"));
    const count = (s: string) => facets?.statusCounts?.[s] ?? 0;
    // The export's up-front count: the facets count the range only (not the other filters), so it is the file's
    // size only with no filter on; with filters it is the loaded rows once the last page is in, else unknown (the
    // export then reports the streamed count).
    const exportTotal = active === 0 ? (facets?.total ?? null) : !list.hasNextPage && !list.isPending ? rows.length : null;

    const columns = useMemo(
        () => [
            col.accessor("tripNo", { header: () => t("driverMonitor.table.tripId"), cell: (c) => <span className="font-mono text-xs">{c.getValue()}</span>, meta: { headerClassName: HEAD } }),
            col.accessor("checkInAt", { header: () => t("driverMonitor.table.checkInAt"), cell: (c) => formatDateTime(c.getValue()), meta: { headerClassName: HEAD, cellClassName: "text-sm text-muted-foreground" } }),
            col.accessor("createdAt", { header: () => t("driverMonitor.table.depart"), cell: (c) => formatDateTime(c.getValue()), meta: { headerClassName: HEAD, cellClassName: "text-sm text-muted-foreground" } }),
            col.accessor("driverName", {
                header: () => t("driverMonitor.table.driver"),
                cell: (c) => <span className="text-sm font-medium">{c.getValue() ?? t("driverMonitor.table.unknown")}</span>,
                meta: { headerClassName: HEAD },
            }),
            col.display({
                id: "plate",
                header: () => t("driverMonitor.table.licensePlate"),
                cell: ({ row: { original: r } }) => <span className="font-mono text-sm text-muted-foreground">{r.truck?.plate ?? "—"}</span>,
                meta: { headerClassName: HEAD },
            }),
            col.accessor("jobType", {
                header: () => t("driverMonitor.table.jobType"),
                cell: (c) => (
                    <Badge variant="secondary" className={cn("border font-medium", JOB_TYPE_COLOR[c.getValue()] ?? "bg-gray-500/10 text-gray-500")}>
                        {jobTypeLabel(c.getValue())}
                    </Badge>
                ),
                meta: { headerClassName: HEAD },
            }),
            col.display({
                id: "origin",
                header: () => t("driverMonitor.table.origin"),
                cell: ({ row: { original: r } }) => placeLabel(r.origin),
                meta: { headerClassName: HEAD, cellClassName: "text-sm font-medium" },
            }),
            col.display({
                id: "destination",
                header: () => t("driverMonitor.table.destination"),
                cell: ({ row: { original: r } }) =>
                    r.stops?.length > 0 ? (
                        <Collapsible>
                            <CollapsibleTrigger onClick={(e) => e.stopPropagation()}
                                className="inline-flex items-center gap-1 rounded-full border border-blue-200 bg-blue-100 px-1.5 py-0.5 text-xs font-medium text-blue-700 hover:bg-blue-200">
                                {t("taskBoard.stops", { count: r.stops.length })}
                                <ChevronDown className="h-3 w-3" />
                            </CollapsibleTrigger>
                            <CollapsibleContent className="mt-2 w-max space-y-1">
                                {r.stops.map((s) => (
                                    <Badge key={s.index} variant="outline"
                                        className={cn("block text-xs font-medium", s.status === "delivered" ? "border-green-200 bg-green-50 text-green-700" : "border-gray-200 bg-gray-50 text-gray-600")}>
                                        {s.status === "delivered" ? "✓" : "◯"} {placeLabel(s.place)}
                                    </Badge>
                                ))}
                            </CollapsibleContent>
                        </Collapsible>
                    ) : (
                        <span className="font-medium">{placeLabel(r.destination)}</span>
                    ),
                meta: { headerClassName: HEAD, cellClassName: "text-sm" },
            }),
            col.accessor("sealCode", { header: () => t("driverMonitor.table.sealCode"), cell: (c) => <span className="font-mono text-xs">{c.getValue() || "—"}</span>, meta: { headerClassName: HEAD } }),
            col.accessor("partnerCode", { header: () => t("driverMonitor.table.partnerCode"), cell: (c) => <span className="font-mono text-xs">{c.getValue() || "—"}</span>, meta: { headerClassName: HEAD } }),
            col.display({
                id: "status",
                header: () => t("driverMonitor.table.status"),
                cell: ({ row: { original: r } }) => (
                    <div className="flex items-center gap-1">
                        <Badge variant="outline" className={cn("font-medium", STATUS_COLOR[r.status])}>{t(`driverMonitor.status.${r.status}`)}</Badge>
                        {r.incidentCount > 0 && r.status !== "incident" ? <AlertTriangle className="h-4 w-4 text-red-500" aria-label={t("driverMonitor.status.incident")} /> : null}
                    </div>
                ),
                meta: { headerClassName: HEAD },
            }),
            col.display({
                id: "deliveredTime",
                header: () => t("driverMonitor.table.deliveredTime"),
                cell: ({ row: { original: r } }) => formatDateTime(deliveredTimeOf(r)),
                meta: { headerClassName: HEAD, cellClassName: "text-sm text-muted-foreground" },
            }),
        ],
        // eslint-disable-next-line react-hooks/exhaustive-deps -- the labels follow the language only
        [t]
    );

    return (
        <div className="container mx-auto max-w-[1600px] space-y-6 p-6">
            <div className="flex flex-col items-start justify-between gap-4 md:flex-row md:items-center">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("nav.driverMonitor")}</h1>
                    <p className="mt-1 text-muted-foreground">{t("driverMonitor.subtitle")}</p>
                </div>
                <div className="flex items-center gap-2">
                    <Button variant="outline" size="sm" onClick={() => setExportOpen(true)}>
                        <Download className="mr-2 h-4 w-4" />
                        {t("driverMonitor.export.open")}
                    </Button>
                    <Button variant="outline" size="icon" onClick={() => void list.refetch()} aria-label={t("driverMonitor.refresh")}>
                        <RefreshCw className={cn("h-4 w-4", list.isFetching && "animate-spin")} />
                    </Button>
                </div>
            </div>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
                <Stat label={t("driverMonitor.stats.totalTrips")} value={facets?.total} icon={<Package className="h-6 w-6" />} tone="bg-blue-100 text-blue-500 dark:bg-blue-900/30" />
                <Stat label={t("driverMonitor.stats.inTransit")} value={facets ? count("in_transit") : undefined} icon={<Truck className="h-5 w-5" />} tone="bg-amber-100 text-amber-500 dark:bg-amber-900/30" />
                <Stat label={t("driverMonitor.stats.delivered")} value={facets ? count("delivered") : undefined} icon={<PackageCheck className="h-5 w-5" />} tone="bg-emerald-100 text-emerald-500 dark:bg-emerald-900/30" />
                <Stat label={t("driverMonitor.status.incident")} value={facets?.incident} icon={<AlertTriangle className="h-5 w-5" />} tone="bg-red-100 text-red-500 dark:bg-red-900/30" />
            </div>

            <div className="flex flex-col flex-wrap gap-4 rounded-lg border border-border/50 bg-card/50 p-4 sm:flex-row">
                <div className="flex gap-2 border-b border-border/50 pb-4 sm:border-b-0 sm:border-r sm:pb-0 sm:pr-4">
                    {[MONITOR_ALL, "first_mile", "line_haul"].map((j) => (
                        <Button key={j} variant={filter.jobType === j ? "secondary" : "ghost"} size="sm" className="whitespace-nowrap" onClick={() => set("jobType", j)}>
                            {j === MONITOR_ALL ? t("driverMonitor.jobType.all") : jobTypeLabel(j)}
                        </Button>
                    ))}
                </div>
                <div className="flex flex-col gap-2">
                    <div className="flex flex-wrap items-center gap-2">
                        <span className="whitespace-nowrap text-xs text-muted-foreground">{t("driverMonitor.filter.dateRange")}</span>
                        <DateRangePicker from={parseISO(filter.from)} to={parseISO(filter.to)} onChange={setRange} locale={dateLocale} className="w-[230px]" />
                        <Button type="button" variant="secondary" size="sm" onClick={() => setFilter((prev) => ({ ...prev, ...defaultRange() }))}>
                            {t("driverMonitor.filter.presetLastDays", { days: MONITOR_DEFAULT_DAYS })}
                        </Button>
                        <Button type="button" variant="outline" size="sm"
                            onClick={() => setFilter((prev) => ({ ...prev, from: dayKey(startOfMonth(new Date())), to: dayKey(endOfMonth(new Date())) }))}>
                            {t("driverMonitor.filter.presetThisMonth")}
                        </Button>
                    </div>
                    <p className={cn("max-w-xl text-[11px]", rangeNote ? "text-amber-700" : "text-muted-foreground")}>
                        {t("driverMonitor.filter.maxRangeHint", { days: MONITOR_MAX_DAYS })}
                    </p>
                </div>
                <SearchPicker kind="driver" value={driver} onChange={setDriver} placeholder={t("driverMonitor.filter.allDrivers")}
                    clearLabel={t("driverMonitor.filter.allDrivers")} className="h-9 w-[200px]" />
                <Select value={filter.status} onValueChange={(v) => set("status", v)}>
                    <SelectTrigger className="h-9 w-[180px]">
                        <SelectValue placeholder={t("driverMonitor.filter.allStatuses")} />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value={MONITOR_ALL}>{t("driverMonitor.filter.allStatuses")}</SelectItem>
                        {TRIP_STATUSES.map((s) => (
                            <SelectItem key={s} value={s}>
                                {t(`driverMonitor.status.${s}`)}{facets ? ` (${s === "incident" ? facets.incident : count(s)})` : ""}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <Select value={filter.partner} onValueChange={(v) => set("partner", v)}>
                    <SelectTrigger className="h-9 w-[200px]">
                        <SelectValue placeholder={t("driverMonitor.filter.partnerChannel")} />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value={MONITOR_ALL}>{t("driverMonitor.filter.allPartnerChannels")}</SelectItem>
                        {(facets?.partners ?? []).map((o) => (
                            <SelectItem key={o.value} value={o.value}>
                                {o.value === FILTER_NONE ? t("driverMonitor.filter.partnerNotSpecified") : o.label} ({o.count})
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <PlateFilterCombobox options={facets?.plates ?? []} value={filter.plate} onChange={(v) => set("plate", v)} keyPrefix="driverMonitor.filter" className="w-[200px]" />
                <Select value={filter.vehicleClass} onValueChange={(v) => set("vehicleClass", v)}>
                    <SelectTrigger className="h-9 w-[200px]">
                        <SelectValue placeholder={t("driverMonitor.filter.vehicleClass")} />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value={MONITOR_ALL}>{t("driverMonitor.filter.allVehicleClasses")}</SelectItem>
                        {(facets?.vehicleClasses ?? []).map((o) => (
                            <SelectItem key={o.value} value={o.value}>
                                {o.value === FILTER_NONE ? t("driverMonitor.filter.vehicleClassNotSpecified") : o.label} ({o.count})
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <PlaceFilterCombobox kind="origin" options={facets?.origins ?? []} value={filter.origin} onChange={(v) => set("origin", v)} keyPrefix="driverMonitor.filter" className="w-[200px]" />
                <PlaceFilterCombobox kind="destination" options={facets?.destinations ?? []} value={filter.destination} onChange={(v) => set("destination", v)}
                    keyPrefix="driverMonitor.filter" className="w-[200px]" />
                <div className="relative min-w-[200px] flex-1">
                    <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                    <Input placeholder={t("driverMonitor.filter.searchDriver")} className="h-9 border-border/50 bg-background/50 pl-10" value={search}
                        onChange={(e) => setSearch(e.target.value)} />
                </div>
                {active > 0 ? (
                    <Button variant="ghost" size="sm" onClick={clearAll} className="h-9 whitespace-nowrap text-muted-foreground">
                        <X className="mr-1.5 h-4 w-4" />
                        {t("driverMonitor.filter.clearAll", { count: active })}
                    </Button>
                ) : null}
            </div>

            <ApiErrorAlert error={list.error} />

            <div className="overflow-hidden rounded-lg border bg-card">
                <VirtualTable
                    data={rows}
                    columns={columns}
                    getRowId={(r) => r.id}
                    aria-label={t("driverMonitor.title")}
                    infinite={list}
                    loading={list.isPending}
                    loadingState={
                        <span className="flex flex-col items-center justify-center gap-2 py-8">
                            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                            <span className="text-sm text-muted-foreground">{t("driverMonitor.table.loadingData")}</span>
                        </span>
                    }
                    error={Boolean(list.error)}
                    emptyState={<span className="block py-8 text-sm">{t("driverMonitor.table.noTrips")}</span>}
                    resetScrollKey={JSON.stringify(effective)}
                    onRowClick={setDetail}
                    maxHeight={MONITOR_TABLE_MAX_HEIGHT}
                    headerRowClassName="border-b border-border/50 bg-muted/50"
                    rowClassName="border-b border-border/50 transition-colors hover:bg-muted/50"
                />
                <LoadMoreFooter loaded={rows.length} hasNextPage={Boolean(list.hasNextPage)} isFetchingNextPage={list.isFetchingNextPage}
                    onLoadMore={() => void list.fetchNextPage()} />
            </div>

            {detail ? <TripDetailDialog row={detail} onClose={() => setDetail(null)} /> : null}
            {exportOpen ? <MonitorExportDialog filter={effective} total={exportTotal} onClose={() => setExportOpen(false)} /> : null}
        </div>
    );
}

function Stat({ label, value, icon, tone }: { label: string; value: number | undefined; icon: React.ReactNode; tone: string }) {
    return (
        <Card className="bg-card">
            <CardContent className="flex items-center justify-between p-6">
                <div>
                    <p className="text-sm font-medium text-muted-foreground">{label}</p>
                    <h2 className="mt-1 text-3xl font-bold">{value ?? "—"}</h2>
                </div>
                <div className={cn("flex h-12 w-12 items-center justify-center rounded-full", tone)}>{icon}</div>
            </CardContent>
        </Card>
    );
}
